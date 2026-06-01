package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func (s *InMemoryStore) GetOwnerEconomyProfile(_ context.Context, ownerID string) (OwnerEconomyProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(ownerID)
	item, ok := s.ownerEconomyProfiles[key]
	if !ok {
		return OwnerEconomyProfile{}, fmt.Errorf("owner economy profile not found: %s", key)
	}
	return item, nil
}

func (s *InMemoryStore) ListOwnerEconomyProfiles(_ context.Context, limit int) ([]OwnerEconomyProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]OwnerEconomyProfile, 0, len(s.ownerEconomyProfiles))
	for _, it := range s.ownerEconomyProfiles {
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].OwnerID < items[j].OwnerID })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *InMemoryStore) UpsertOwnerEconomyProfile(_ context.Context, item OwnerEconomyProfile) (OwnerEconomyProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.OwnerID = strings.TrimSpace(item.OwnerID)
	if item.OwnerID == "" {
		return OwnerEconomyProfile{}, fmt.Errorf("owner_id is required")
	}
	now := time.Now().UTC()
	current := s.ownerEconomyProfiles[item.OwnerID]
	if current.OwnerID == "" {
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
	} else if item.CreatedAt.IsZero() {
		item.CreatedAt = current.CreatedAt
	}
	item.UpdatedAt = now
	s.ownerEconomyProfiles[item.OwnerID] = item
	return item, nil
}

func (s *InMemoryStore) UpsertOwnerOnboardingGrant(_ context.Context, item OwnerOnboardingGrant) (OwnerOnboardingGrant, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.GrantKey = strings.TrimSpace(item.GrantKey)
	if item.GrantKey == "" {
		return OwnerOnboardingGrant{}, false, fmt.Errorf("grant_key is required")
	}
	if existing, ok := s.onboardingGrants[item.GrantKey]; ok {
		return existing, false, nil
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	s.onboardingGrants[item.GrantKey] = item
	return item, true, nil
}

func (s *InMemoryStore) ListOwnerOnboardingGrants(_ context.Context, ownerID string) ([]OwnerOnboardingGrant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(ownerID)
	items := make([]OwnerOnboardingGrant, 0)
	for _, it := range s.onboardingGrants {
		if key != "" && it.OwnerID != key {
			continue
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	return items, nil
}

func (s *InMemoryStore) GetEconomyCommQuotaWindow(_ context.Context, userID string) (EconomyCommQuotaWindow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(userID)
	item, ok := s.commQuotaWindows[key]
	if !ok {
		return EconomyCommQuotaWindow{}, fmt.Errorf("economy comm quota window not found: %s", key)
	}
	return item, nil
}

func (s *InMemoryStore) UpsertEconomyCommQuotaWindow(_ context.Context, item EconomyCommQuotaWindow) (EconomyCommQuotaWindow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.UserID = strings.TrimSpace(item.UserID)
	if item.UserID == "" {
		return EconomyCommQuotaWindow{}, fmt.Errorf("user_id is required")
	}
	item.UpdatedAt = time.Now().UTC()
	s.commQuotaWindows[item.UserID] = item
	return item, nil
}

func (s *InMemoryStore) GetEconomyContributionEvent(_ context.Context, eventKey string) (EconomyContributionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(eventKey)
	item, ok := s.contributionEvents[key]
	if !ok {
		return EconomyContributionEvent{}, fmt.Errorf("economy contribution event not found: %s", key)
	}
	return item, nil
}

func (s *InMemoryStore) UpsertEconomyContributionEvent(_ context.Context, item EconomyContributionEvent) (EconomyContributionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.EventKey = strings.TrimSpace(item.EventKey)
	if item.EventKey == "" {
		return EconomyContributionEvent{}, fmt.Errorf("event_key is required")
	}
	current := s.contributionEvents[item.EventKey]
	if current.EventKey == "" && item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	} else if item.CreatedAt.IsZero() {
		item.CreatedAt = current.CreatedAt
	}
	s.contributionEvents[item.EventKey] = item
	return item, nil
}

func (s *InMemoryStore) ListEconomyContributionEvents(_ context.Context, filter EconomyContributionEventFilter) ([]EconomyContributionEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]EconomyContributionEvent, 0, len(s.contributionEvents))
	for _, it := range s.contributionEvents {
		if filter.Kind != "" && it.Kind != filter.Kind {
			continue
		}
		if filter.UserID != "" && it.UserID != filter.UserID {
			continue
		}
		if filter.ResourceType != "" && it.ResourceType != filter.ResourceType {
			continue
		}
		if filter.ResourceID != "" && it.ResourceID != filter.ResourceID {
			continue
		}
		switch strings.TrimSpace(filter.Processed) {
		case "processed":
			if it.ProcessedAt == nil {
				continue
			}
		case "pending":
			if it.ProcessedAt != nil {
				continue
			}
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].EventKey < items[j].EventKey
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *InMemoryStore) GetEconomyRewardDecision(_ context.Context, decisionKey string) (EconomyRewardDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(decisionKey)
	item, ok := s.rewardDecisions[key]
	if !ok {
		return EconomyRewardDecision{}, fmt.Errorf("economy reward decision not found: %s", key)
	}
	return item, nil
}

func (s *InMemoryStore) UpsertEconomyRewardDecision(_ context.Context, item EconomyRewardDecision) (EconomyRewardDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.DecisionKey = strings.TrimSpace(item.DecisionKey)
	if item.DecisionKey == "" {
		return EconomyRewardDecision{}, fmt.Errorf("decision_key is required")
	}
	now := time.Now().UTC()
	current := s.rewardDecisions[item.DecisionKey]
	if current.DecisionKey == "" {
		if item.CreatedAt.IsZero() {
			item.CreatedAt = now
		}
	} else if item.CreatedAt.IsZero() {
		item.CreatedAt = current.CreatedAt
	}
	item.UpdatedAt = now
	s.rewardDecisions[item.DecisionKey] = item
	return item, nil
}

func (s *InMemoryStore) ApplyMintRewardDecision(_ context.Context, item EconomyRewardDecision) (EconomyRewardDecision, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.DecisionKey = strings.TrimSpace(item.DecisionKey)
	item.RecipientUserID = strings.TrimSpace(item.RecipientUserID)
	if item.DecisionKey == "" {
		return EconomyRewardDecision{}, false, fmt.Errorf("decision_key is required")
	}
	if item.RecipientUserID == "" {
		return EconomyRewardDecision{}, false, fmt.Errorf("recipient_user_id is required")
	}
	if existing, ok := s.rewardDecisions[item.DecisionKey]; ok && existing.Status == "applied" {
		return existing, false, nil
	}
	now := time.Now().UTC()
	current := s.rewardDecisions[item.DecisionKey]
	if current.DecisionKey != "" && item.CreatedAt.IsZero() {
		item.CreatedAt = current.CreatedAt
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	s.ensureBot(item.RecipientUserID)
	account := s.accounts[item.RecipientUserID]
	if item.Amount > 0 && account.Balance > (math.MaxInt64-item.Amount) {
		return EconomyRewardDecision{}, false, ErrBalanceOverflow
	}
	account.Balance += item.Amount
	account.UpdatedAt = now
	s.accounts[item.RecipientUserID] = account
	s.nextLedgerID++
	ledger := TokenLedger{
		ID:           s.nextLedgerID,
		BotID:        item.RecipientUserID,
		OpType:       "recharge",
		Amount:       item.Amount,
		BalanceAfter: account.Balance,
		CreatedAt:    now,
	}
	s.ledger = append(s.ledger, ledger)
	item.Status = "applied"
	item.QueueReason = ""
	item.LedgerID = ledger.ID
	item.BalanceAfter = ledger.BalanceAfter
	item.AppliedAt = &now
	item.EnqueuedAt = nil
	item.UpdatedAt = now
	s.rewardDecisions[item.DecisionKey] = item
	return item, true, nil
}

func (s *InMemoryStore) ListEconomyRewardDecisions(_ context.Context, filter EconomyRewardDecisionFilter) ([]EconomyRewardDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]EconomyRewardDecision, 0, len(s.rewardDecisions))
	for _, it := range s.rewardDecisions {
		if filter.Status != "" && it.Status != filter.Status {
			continue
		}
		if filter.RecipientUserID != "" && it.RecipientUserID != filter.RecipientUserID {
			continue
		}
		if filter.RuleKey != "" && it.RuleKey != filter.RuleKey {
			continue
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Priority != items[j].Priority {
			return items[i].Priority < items[j].Priority
		}
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].DecisionKey < items[j].DecisionKey
	})
	if filter.Limit > 0 && len(items) > filter.Limit {
		items = items[:filter.Limit]
	}
	return items, nil
}

func (s *InMemoryStore) UpsertEconomyKnowledgeMeta(_ context.Context, item EconomyKnowledgeMeta) (EconomyKnowledgeMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if item.ProposalID <= 0 && item.EntryID <= 0 {
		return EconomyKnowledgeMeta{}, fmt.Errorf("proposal_id or entry_id is required")
	}
	if existing, ok := s.knowledgeMetaByProp[item.ProposalID]; ok {
		if existing.ProposalID > 0 {
			delete(s.knowledgeMetaByProp, existing.ProposalID)
		}
		if existing.EntryID > 0 {
			delete(s.knowledgeMetaByEntry, existing.EntryID)
		}
		item = mergeEconomyKnowledgeMeta(item, existing)
	} else if existing, ok := s.knowledgeMetaByEntry[item.EntryID]; ok {
		if existing.ProposalID > 0 {
			delete(s.knowledgeMetaByProp, existing.ProposalID)
		}
		if existing.EntryID > 0 {
			delete(s.knowledgeMetaByEntry, existing.EntryID)
		}
		item = mergeEconomyKnowledgeMeta(item, existing)
	}
	item.UpdatedAt = time.Now().UTC()
	if item.ProposalID > 0 {
		s.knowledgeMetaByProp[item.ProposalID] = item
	}
	if item.EntryID > 0 {
		s.knowledgeMetaByEntry[item.EntryID] = item
	}
	return item, nil
}

func (s *InMemoryStore) GetEconomyKnowledgeMetaByProposal(_ context.Context, proposalID int64) (EconomyKnowledgeMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.knowledgeMetaByProp[proposalID]
	if !ok {
		return EconomyKnowledgeMeta{}, fmt.Errorf("economy knowledge meta not found for proposal_id=%d", proposalID)
	}
	return item, nil
}

func (s *InMemoryStore) GetEconomyKnowledgeMetaByEntry(_ context.Context, entryID int64) (EconomyKnowledgeMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.knowledgeMetaByEntry[entryID]
	if !ok {
		return EconomyKnowledgeMeta{}, fmt.Errorf("economy knowledge meta not found for entry_id=%d", entryID)
	}
	return item, nil
}

func (s *InMemoryStore) ListEconomyKnowledgeMeta(_ context.Context, limit int) ([]EconomyKnowledgeMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]EconomyKnowledgeMeta, 0, len(s.knowledgeMetaByProp)+len(s.knowledgeMetaByEntry))
	seen := make(map[string]struct{})
	for _, it := range s.knowledgeMetaByProp {
		key := fmt.Sprintf("p:%d:e:%d", it.ProposalID, it.EntryID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, it)
	}
	for _, it := range s.knowledgeMetaByEntry {
		key := fmt.Sprintf("p:%d:e:%d", it.ProposalID, it.EntryID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.Before(items[j].UpdatedAt) })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *InMemoryStore) UpsertEconomyToolMeta(_ context.Context, item EconomyToolMeta) (EconomyToolMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.ToolID = strings.TrimSpace(strings.ToLower(item.ToolID))
	if item.ToolID == "" {
		return EconomyToolMeta{}, fmt.Errorf("tool_id is required")
	}
	item.UpdatedAt = time.Now().UTC()
	s.toolEconomyMeta[item.ToolID] = item
	return item, nil
}

func (s *InMemoryStore) GetEconomyToolMeta(_ context.Context, toolID string) (EconomyToolMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(strings.ToLower(toolID))
	item, ok := s.toolEconomyMeta[key]
	if !ok {
		return EconomyToolMeta{}, fmt.Errorf("economy tool meta not found: %s", key)
	}
	return item, nil
}

func (s *InMemoryStore) ListEconomyToolMeta(_ context.Context, limit int) ([]EconomyToolMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]EconomyToolMeta, 0, len(s.toolEconomyMeta))
	for _, it := range s.toolEconomyMeta {
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].ToolID < items[j].ToolID })
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
