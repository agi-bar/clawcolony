package server

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"clawcolony/internal/store"
)

const clawTreasurySystemID = "clawcolony-treasury"
const defaultTreasuryInitialToken int64 = 1000000000

func isSystemTokenUserID(userID string) bool {
	// Normalize case to avoid mixed-case ID bypasses.
	switch strings.ToLower(strings.TrimSpace(userID)) {
	case clawWorldSystemID, clawTreasurySystemID:
		return true
	default:
		return false
	}
}

func isExcludedTokenUserID(userID string) bool {
	uid := strings.TrimSpace(userID)
	return uid == "" || isSystemTokenUserID(uid)
}

func (s *Server) effectiveTreasuryInitialToken() int64 {
	if s.cfg.TreasuryInitialToken > 0 {
		return s.cfg.TreasuryInitialToken
	}
	return defaultTreasuryInitialToken
}

func (s *Server) ensureTreasuryAccount(ctx context.Context) (store.TokenAccount, error) {
	s.treasuryInitMu.Lock()
	defer s.treasuryInitMu.Unlock()

	accounts, err := s.store.ListTokenAccounts(ctx)
	if err != nil {
		return store.TokenAccount{}, err
	}
	for _, account := range accounts {
		if strings.TrimSpace(account.BotID) == clawTreasurySystemID {
			return account, nil
		}
	}

	if _, err := s.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       clawTreasurySystemID,
		Name:        "Clawcolony Treasury",
		Provider:    "system",
		Status:      "system",
		Initialized: false,
	}); err != nil {
		return store.TokenAccount{}, err
	}

	initial := s.effectiveTreasuryInitialToken()
	if initial > 0 {
		if _, err := s.store.Recharge(ctx, clawTreasurySystemID, initial); err != nil {
			return store.TokenAccount{}, err
		}
	}

	accounts, err = s.store.ListTokenAccounts(ctx)
	if err != nil {
		return store.TokenAccount{}, err
	}
	for _, account := range accounts {
		if strings.TrimSpace(account.BotID) == clawTreasurySystemID {
			return account, nil
		}
	}
	return store.TokenAccount{}, fmt.Errorf("treasury account not found after initialization")
}

func (s *Server) treasuryBalance(ctx context.Context) (int64, error) {
	account, err := s.ensureTreasuryAccount(ctx)
	if err != nil {
		return 0, err
	}
	return account.Balance, nil
}

func (s *Server) transferFromTreasury(ctx context.Context, userID string, amount int64) (store.TokenLedger, store.TokenLedger, error) {
	if isExcludedTokenUserID(userID) || amount <= 0 {
		return store.TokenLedger{}, store.TokenLedger{}, fmt.Errorf("treasury recipient and positive amount are required")
	}
	if _, err := s.ensureTreasuryAccount(ctx); err != nil {
		return store.TokenLedger{}, store.TokenLedger{}, err
	}
	debit, err := s.store.Consume(ctx, clawTreasurySystemID, amount)
	if err != nil {
		if errors.Is(err, store.ErrInsufficientBalance) {
			return store.TokenLedger{}, store.TokenLedger{}, fmt.Errorf("treasury insufficient balance: %w", err)
		}
		return store.TokenLedger{}, store.TokenLedger{}, err
	}
	credit, err := s.store.Recharge(ctx, userID, amount)
	if err != nil {
		_, _ = s.store.Recharge(ctx, clawTreasurySystemID, amount)
		return store.TokenLedger{}, store.TokenLedger{}, err
	}
	return debit, credit, nil
}

func (s *Server) distributeFromTreasury(ctx context.Context, payouts map[string]int64) (store.TokenLedger, map[string]store.TokenLedger, error) {
	normalized := make(map[string]int64, len(payouts))
	var total int64
	for uid, amount := range payouts {
		uid = strings.TrimSpace(uid)
		if isExcludedTokenUserID(uid) || amount <= 0 {
			continue
		}
		next, ok := safeInt64Add(normalized[uid], amount)
		if !ok {
			return store.TokenLedger{}, nil, fmt.Errorf("treasury payout overflow for %s", uid)
		}
		normalized[uid] = next
	}
	if len(normalized) == 0 {
		return store.TokenLedger{}, map[string]store.TokenLedger{}, nil
	}
	for _, amount := range normalized {
		var ok bool
		total, ok = safeInt64Add(total, amount)
		if !ok {
			return store.TokenLedger{}, nil, fmt.Errorf("treasury payout total overflow")
		}
	}
	if _, err := s.ensureTreasuryAccount(ctx); err != nil {
		return store.TokenLedger{}, nil, err
	}
	debit, err := s.store.Consume(ctx, clawTreasurySystemID, total)
	if err != nil {
		if errors.Is(err, store.ErrInsufficientBalance) {
			return store.TokenLedger{}, nil, fmt.Errorf("treasury insufficient balance: %w", err)
		}
		return store.TokenLedger{}, nil, err
	}
	recipients := make([]string, 0, len(normalized))
	for uid := range normalized {
		recipients = append(recipients, uid)
	}
	sort.Strings(recipients)
	credits := make(map[string]store.TokenLedger, len(recipients))
	for _, uid := range recipients {
		ledger, err := s.store.Recharge(ctx, uid, normalized[uid])
		if err != nil {
			for rollbackUID, rollbackLedger := range credits {
				if _, rollbackErr := s.store.Consume(ctx, rollbackUID, rollbackLedger.Amount); rollbackErr != nil {
					// Best-effort rollback; caller still gets the original error.
				}
			}
			_, _ = s.store.Recharge(ctx, clawTreasurySystemID, total)
			return store.TokenLedger{}, nil, err
		}
		credits[uid] = ledger
	}
	return debit, credits, nil
}
