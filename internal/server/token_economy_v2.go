package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"clawcolony/internal/economy"
	"clawcolony/internal/store"
)

const (
	storeMigrationStateKey                 = "token_economy_v2_store_migration_complete"
	treasurySeedMigrationStateKey          = "token_economy_v2_treasury_seed_v1_complete"
	initialTopupMigrationStateKey          = "token_economy_v2_initial_token_topup_v2_complete"
	initialTopupReconcileMigrationStateKey = "token_economy_v2_initial_token_topup_reconcile_v1_complete"
	ownerEconomyStateKey                   = "token_economy_v2_owner_profiles"
	commQuotaStateKey                      = "token_economy_v2_comm_quota"
	rewardDecisionStateKey                 = "token_economy_v2_reward_decisions"
	rewardQueueStateKey                    = "token_economy_v2_reward_queue"
	contributionEventStateKey              = "token_economy_v2_contribution_events"
	knowledgeMetaStateKey                  = "token_economy_v2_knowledge_meta"
	toolEconomyStateKey                    = "token_economy_v2_tool_meta"
	dashboardEconomySnapshotKey            = "token_economy_v2_dashboard_snapshot"

	onboardingSettlementMint         = "mint"
	legacyInitialTokenFallback int64 = 10000
	githubBindOnboardingReward       = 50000
	githubStarOnboardingReward       = 500000
	githubForkOnboardingReward       = 200000
)

type ownerEconomyProfile struct {
	OwnerID           string     `json:"owner_id"`
	GitHubUserID      string     `json:"github_user_id,omitempty"`
	GitHubUsername    string     `json:"github_username,omitempty"`
	Activated         bool       `json:"activated"`
	ActivatedAt       *time.Time `json:"activated_at,omitempty"`
	GitHubBindGranted bool       `json:"github_bind_granted,omitempty"`
	GitHubStarGranted bool       `json:"github_star_granted,omitempty"`
	GitHubForkGranted bool       `json:"github_fork_granted,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

type ownerEconomyState struct {
	Profiles map[string]ownerEconomyProfile `json:"profiles"`
}

type commQuotaWindow struct {
	UserID          string    `json:"user_id"`
	WindowStartTick int64     `json:"window_start_tick"`
	UsedFreeTokens  int64     `json:"used_free_tokens"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type commQuotaState struct {
	Users map[string]commQuotaWindow `json:"users"`
}

type economyRewardDecision struct {
	DecisionKey     string         `json:"decision_key"`
	RuleKey         string         `json:"rule_key"`
	ResourceType    string         `json:"resource_type"`
	ResourceID      string         `json:"resource_id"`
	RecipientUserID string         `json:"recipient_user_id"`
	Amount          int64          `json:"amount"`
	Priority        int            `json:"priority"`
	Status          string         `json:"status"`
	QueueReason     string         `json:"queue_reason,omitempty"`
	LedgerID        int64          `json:"ledger_id,omitempty"`
	BalanceAfter    int64          `json:"balance_after,omitempty"`
	Meta            map[string]any `json:"meta,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	AppliedAt       *time.Time     `json:"applied_at,omitempty"`
	EnqueuedAt      *time.Time     `json:"enqueued_at,omitempty"`
}

type rewardDecisionState struct {
	Items map[string]economyRewardDecision `json:"items"`
}

type rewardQueueState struct {
	Items []economyRewardDecision `json:"items"`
}

type contributionEvent struct {
	EventKey     string         `json:"event_key"`
	Kind         string         `json:"kind"`
	UserID       string         `json:"user_id"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Meta         map[string]any `json:"meta,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	ProcessedAt  *time.Time     `json:"processed_at,omitempty"`
	DecisionKeys []string       `json:"decision_keys,omitempty"`
}

type contributionEventState struct {
	Items map[string]contributionEvent `json:"items"`
}

type citationRef struct {
	RefType string `json:"ref_type"`
	RefID   string `json:"ref_id"`
}

type knowledgeMeta struct {
	ProposalID    int64         `json:"proposal_id,omitempty"`
	EntryID       int64         `json:"entry_id,omitempty"`
	Category      string        `json:"category"`
	References    []citationRef `json:"references,omitempty"`
	AuthorUserID  string        `json:"author_user_id"`
	ContentTokens int64         `json:"content_tokens"`
	UpdatedAt     time.Time     `json:"updated_at"`
}

type knowledgeMetaState struct {
	ByProposal map[string]knowledgeMeta `json:"by_proposal"`
	ByEntry    map[string]knowledgeMeta `json:"by_entry"`
}

type toolEconomyMeta struct {
	ToolID               string    `json:"tool_id"`
	AuthorUserID         string    `json:"author_user_id"`
	CategoryHint         string    `json:"category_hint,omitempty"`
	FunctionalClusterKey string    `json:"functional_cluster_key,omitempty"`
	PriceToken           int64     `json:"price_token,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type toolEconomyState struct {
	Items map[string]toolEconomyMeta `json:"items"`
}

type commChargePreview struct {
	UserID          string
	Activated       bool
	Tokens          int64
	WindowStartTick int64
	FreeCovered     int64
	OverageTokens   int64
	ChargedAmount   int64
}

type economyDashboardSnapshot struct {
	PoolBalance        int64          `json:"pool_balance"`
	SafeBalance        int64          `json:"safe_balance"`
	RewardQueueDepth   int            `json:"reward_queue_depth"`
	RewardQueueAmounts map[int]int64  `json:"reward_queue_amounts"`
	PopulationByState  map[string]int `json:"population_by_state"`
	Scarcity           map[string]any `json:"scarcity,omitempty"`
	UpdatedAt          time.Time      `json:"updated_at"`
}

type tokenEconomyStoreMigrationState struct {
	Completed bool      `json:"completed"`
	UpdatedAt time.Time `json:"updated_at"`
}

var toolManifestPricePattern = regexp.MustCompile(`(?i)(?:price|token_price)\s*["':= ]+\s*([0-9]+)`)

func (s *Server) tokenPolicy() economy.Policy {
	return economy.PolicyFromConfig(s.cfg)
}

func (s *Server) tokenEconomyV2Enabled() bool {
	return s.tokenPolicy().Enabled()
}

func (s *Server) initTokenEconomyV2(ctx context.Context) error {
	if !s.tokenEconomyV2Enabled() {
		return nil
	}
	if err := s.migrateTokenEconomyV2State(ctx); err != nil {
		return err
	}
	if err := s.backfillOwnerEconomyProfiles(ctx); err != nil {
		return err
	}
	if err := s.migrateTreasurySeedFloor(ctx); err != nil {
		return err
	}
	if err := s.migrateHistoricalInitialTokenTopups(ctx); err != nil {
		return err
	}
	if err := s.reconcileHistoricalInitialTokenTopups(ctx); err != nil {
		return err
	}
	return s.backfillProposalKnowledgeMeta(ctx)
}

func isEconomyRecordMissing(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "not found")
}

func decodeMetaJSON(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func encodeMetaJSON(meta map[string]any) string {
	if len(meta) == 0 {
		return ""
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeDecisionKeysJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func encodeDecisionKeysJSON(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return ""
	}
	return string(raw)
}

func decodeCitationRefsJSON(raw string) []citationRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []citationRef
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func encodeCitationRefsJSON(refs []citationRef) string {
	if len(refs) == 0 {
		return ""
	}
	raw, err := json.Marshal(refs)
	if err != nil {
		return ""
	}
	return string(raw)
}

func toStoreOwnerEconomyProfile(item ownerEconomyProfile) store.OwnerEconomyProfile {
	return store.OwnerEconomyProfile{
		OwnerID:        strings.TrimSpace(item.OwnerID),
		GitHubUserID:   strings.TrimSpace(item.GitHubUserID),
		GitHubUsername: strings.TrimSpace(item.GitHubUsername),
		Activated:      item.Activated,
		ActivatedAt:    item.ActivatedAt,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func fromStoreOwnerEconomyProfile(item store.OwnerEconomyProfile) ownerEconomyProfile {
	return ownerEconomyProfile{
		OwnerID:        item.OwnerID,
		GitHubUserID:   item.GitHubUserID,
		GitHubUsername: item.GitHubUsername,
		Activated:      item.Activated,
		ActivatedAt:    item.ActivatedAt,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func toStoreRewardDecision(item economyRewardDecision) store.EconomyRewardDecision {
	return store.EconomyRewardDecision{
		DecisionKey:     strings.TrimSpace(item.DecisionKey),
		RuleKey:         strings.TrimSpace(item.RuleKey),
		ResourceType:    strings.TrimSpace(item.ResourceType),
		ResourceID:      strings.TrimSpace(item.ResourceID),
		RecipientUserID: strings.TrimSpace(item.RecipientUserID),
		Amount:          item.Amount,
		Priority:        item.Priority,
		Status:          strings.TrimSpace(item.Status),
		QueueReason:     strings.TrimSpace(item.QueueReason),
		LedgerID:        item.LedgerID,
		BalanceAfter:    item.BalanceAfter,
		MetaJSON:        encodeMetaJSON(item.Meta),
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
		AppliedAt:       item.AppliedAt,
		EnqueuedAt:      item.EnqueuedAt,
	}
}

func fromStoreRewardDecision(item store.EconomyRewardDecision) economyRewardDecision {
	return economyRewardDecision{
		DecisionKey:     item.DecisionKey,
		RuleKey:         item.RuleKey,
		ResourceType:    item.ResourceType,
		ResourceID:      item.ResourceID,
		RecipientUserID: item.RecipientUserID,
		Amount:          item.Amount,
		Priority:        item.Priority,
		Status:          item.Status,
		QueueReason:     item.QueueReason,
		LedgerID:        item.LedgerID,
		BalanceAfter:    item.BalanceAfter,
		Meta:            decodeMetaJSON(item.MetaJSON),
		CreatedAt:       item.CreatedAt,
		UpdatedAt:       item.UpdatedAt,
		AppliedAt:       item.AppliedAt,
		EnqueuedAt:      item.EnqueuedAt,
	}
}

func toStoreContributionEvent(item contributionEvent) store.EconomyContributionEvent {
	return store.EconomyContributionEvent{
		EventKey:         strings.TrimSpace(item.EventKey),
		Kind:             strings.TrimSpace(item.Kind),
		UserID:           strings.TrimSpace(item.UserID),
		ResourceType:     strings.TrimSpace(item.ResourceType),
		ResourceID:       strings.TrimSpace(item.ResourceID),
		MetaJSON:         encodeMetaJSON(item.Meta),
		CreatedAt:        item.CreatedAt,
		ProcessedAt:      item.ProcessedAt,
		DecisionKeysJSON: encodeDecisionKeysJSON(item.DecisionKeys),
	}
}

func fromStoreContributionEvent(item store.EconomyContributionEvent) contributionEvent {
	return contributionEvent{
		EventKey:     item.EventKey,
		Kind:         item.Kind,
		UserID:       item.UserID,
		ResourceType: item.ResourceType,
		ResourceID:   item.ResourceID,
		Meta:         decodeMetaJSON(item.MetaJSON),
		CreatedAt:    item.CreatedAt,
		ProcessedAt:  item.ProcessedAt,
		DecisionKeys: decodeDecisionKeysJSON(item.DecisionKeysJSON),
	}
}

func toStoreKnowledgeMeta(item knowledgeMeta) store.EconomyKnowledgeMeta {
	return store.EconomyKnowledgeMeta{
		ProposalID:     item.ProposalID,
		EntryID:        item.EntryID,
		Category:       strings.TrimSpace(strings.ToLower(item.Category)),
		ReferencesJSON: encodeCitationRefsJSON(item.References),
		AuthorUserID:   strings.TrimSpace(item.AuthorUserID),
		ContentTokens:  item.ContentTokens,
		UpdatedAt:      item.UpdatedAt,
	}
}

func fromStoreKnowledgeMeta(item store.EconomyKnowledgeMeta) knowledgeMeta {
	return knowledgeMeta{
		ProposalID:    item.ProposalID,
		EntryID:       item.EntryID,
		Category:      item.Category,
		References:    decodeCitationRefsJSON(item.ReferencesJSON),
		AuthorUserID:  item.AuthorUserID,
		ContentTokens: item.ContentTokens,
		UpdatedAt:     item.UpdatedAt,
	}
}

func toStoreToolEconomyMeta(item toolEconomyMeta) store.EconomyToolMeta {
	return store.EconomyToolMeta{
		ToolID:               strings.TrimSpace(strings.ToLower(item.ToolID)),
		AuthorUserID:         strings.TrimSpace(item.AuthorUserID),
		CategoryHint:         strings.TrimSpace(strings.ToLower(item.CategoryHint)),
		FunctionalClusterKey: strings.TrimSpace(strings.ToLower(item.FunctionalClusterKey)),
		PriceToken:           item.PriceToken,
		UpdatedAt:            item.UpdatedAt,
	}
}

func fromStoreToolEconomyMeta(item store.EconomyToolMeta) toolEconomyMeta {
	return toolEconomyMeta{
		ToolID:               item.ToolID,
		AuthorUserID:         item.AuthorUserID,
		CategoryHint:         item.CategoryHint,
		FunctionalClusterKey: item.FunctionalClusterKey,
		PriceToken:           item.PriceToken,
		UpdatedAt:            item.UpdatedAt,
	}
}

func normalizeCitationRefs(refs []citationRef) []citationRef {
	if len(refs) == 0 {
		return nil
	}
	out := make([]citationRef, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		ref.RefType = strings.TrimSpace(strings.ToLower(ref.RefType))
		ref.RefID = strings.TrimSpace(ref.RefID)
		if ref.RefType == "" || ref.RefID == "" {
			continue
		}
		key := ref.RefType + ":" + ref.RefID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func isGovernanceKBSection(section string) bool {
	return strings.HasPrefix(strings.TrimSpace(strings.ToLower(section)), "governance")
}

func (s *Server) migrateTokenEconomyV2State(ctx context.Context) error {
	migrationState := tokenEconomyStoreMigrationState{}
	if found, _, err := s.getSettingJSON(ctx, storeMigrationStateKey, &migrationState); err != nil {
		return err
	} else if found && migrationState.Completed {
		return nil
	}
	if err := s.migrateLegacyOwnerEconomyState(ctx); err != nil {
		return err
	}
	if err := s.migrateLegacyCommQuotaState(ctx); err != nil {
		return err
	}
	if err := s.migrateLegacyRewardDecisionState(ctx); err != nil {
		return err
	}
	if err := s.migrateLegacyRewardQueueState(ctx); err != nil {
		return err
	}
	if err := s.migrateLegacyContributionEventState(ctx); err != nil {
		return err
	}
	if err := s.migrateLegacyKnowledgeMetaState(ctx); err != nil {
		return err
	}
	if err := s.migrateLegacyToolEconomyState(ctx); err != nil {
		return err
	}
	_, err := s.putSettingJSON(ctx, storeMigrationStateKey, tokenEconomyStoreMigrationState{
		Completed: true,
		UpdatedAt: time.Now().UTC(),
	})
	return err
}

func (s *Server) migrateTreasurySeedFloor(ctx context.Context) error {
	migrationState := tokenEconomyStoreMigrationState{}
	if found, _, err := s.getSettingJSON(ctx, treasurySeedMigrationStateKey, &migrationState); err != nil {
		return err
	} else if found && migrationState.Completed {
		return nil
	}

	account, err := s.ensureTreasuryAccount(ctx)
	if err != nil {
		return err
	}
	target := s.effectiveTreasuryInitialToken()
	if target > account.Balance {
		if _, err := s.store.Recharge(ctx, clawTreasurySystemID, target-account.Balance); err != nil {
			return err
		}
	}
	_, err = s.putSettingJSON(ctx, treasurySeedMigrationStateKey, tokenEconomyStoreMigrationState{
		Completed: true,
		UpdatedAt: time.Now().UTC(),
	})
	return err
}

func historicalClaimTime(reg store.AgentRegistration) time.Time {
	if reg.ClaimedAt != nil && !reg.ClaimedAt.IsZero() {
		return reg.ClaimedAt.UTC()
	}
	if reg.ActivatedAt != nil && !reg.ActivatedAt.IsZero() {
		return reg.ActivatedAt.UTC()
	}
	return reg.CreatedAt.UTC()
}

func parseLawInitialToken(law store.TianDaoLaw) (int64, bool) {
	raw := strings.TrimSpace(law.ManifestJSON)
	if raw == "" {
		return 0, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return 0, false
	}
	n, ok := numericManifestValue(payload["initial_token"])
	if !ok || n <= 0 {
		return 0, false
	}
	return n, true
}

func historicalInitialTokenForRegistration(reg store.AgentRegistration, laws []store.TianDaoLaw) (int64, string) {
	if len(laws) == 0 {
		return legacyInitialTokenFallback, ""
	}
	effectiveAt := historicalClaimTime(reg)
	selected := laws[0]
	for _, law := range laws {
		if law.CreatedAt.After(effectiveAt) {
			break
		}
		selected = law
	}
	if amount, ok := parseLawInitialToken(selected); ok {
		return amount, selected.LawKey
	}
	return legacyInitialTokenFallback, selected.LawKey
}

func historicalInitialTopupTarget(currentInitial int64, reg store.AgentRegistration, laws []store.TianDaoLaw) (int64, int64, string) {
	legacyInitial, lawKey := historicalInitialTokenForRegistration(reg, laws)
	if legacyInitial < 0 {
		legacyInitial = 0
	}
	if currentInitial <= legacyInitial {
		return 0, legacyInitial, lawKey
	}
	return currentInitial - legacyInitial, legacyInitial, lawKey
}

func (s *Server) appliedHistoricalInitialTopupAmount(ctx context.Context, userID string) (int64, error) {
	total := int64(0)
	keys := []string{
		fmt.Sprintf("migration:onboarding:initial-topup:%s", userID),
		fmt.Sprintf("migration:onboarding:initial-topup-reconcile:%s", userID),
	}
	for _, key := range keys {
		item, err := s.store.GetEconomyRewardDecision(ctx, key)
		if err != nil {
			if isEconomyRecordMissing(err) {
				continue
			}
			return 0, err
		}
		if item.Status != "applied" {
			continue
		}
		next, ok := safeInt64Add(total, item.Amount)
		if !ok {
			return 0, fmt.Errorf("historical initial topup overflow for %s", userID)
		}
		total = next
	}
	return total, nil
}

func (s *Server) migrateHistoricalInitialTokenTopups(ctx context.Context) error {
	migrationState := tokenEconomyStoreMigrationState{}
	if found, _, err := s.getSettingJSON(ctx, initialTopupMigrationStateKey, &migrationState); err != nil {
		return err
	} else if found && migrationState.Completed {
		return nil
	}

	if err := s.mintQueuedOnboardingDecisions(ctx); err != nil {
		return err
	}

	laws, err := s.store.ListTianDaoLaws(ctx)
	if err != nil {
		return err
	}
	registrations, err := s.store.ListAgentRegistrations(ctx)
	if err != nil {
		return err
	}
	for _, reg := range registrations {
		userID := strings.TrimSpace(reg.UserID)
		if isExcludedTokenUserID(userID) {
			continue
		}
		if reg.ActivatedAt == nil && reg.ClaimedAt == nil && !strings.EqualFold(strings.TrimSpace(reg.Status), "active") {
			continue
		}
		if _, err := s.store.GetEconomyRewardDecision(ctx, fmt.Sprintf("onboarding:initial:%s", userID)); err == nil {
			continue
		} else if !isEconomyRecordMissing(err) {
			return err
		}
		if current, err := s.store.GetEconomyRewardDecision(ctx, fmt.Sprintf("migration:onboarding:initial-topup:%s", userID)); err == nil {
			if current.Status == "applied" {
				continue
			}
			if _, err := s.applyMintRewardDecision(ctx, fromStoreRewardDecision(current), false); err != nil {
				return err
			}
			continue
		} else if !isEconomyRecordMissing(err) {
			return err
		}

		topupAmount, legacyInitial, lawKey := historicalInitialTopupTarget(s.tokenPolicy().InitialToken, reg, laws)
		if topupAmount <= 0 {
			continue
		}
		if _, err := s.applyMintRewardDecision(ctx, economyRewardDecision{
			DecisionKey:     fmt.Sprintf("migration:onboarding:initial-topup:%s", userID),
			RuleKey:         "migration.onboarding.initial_topup",
			ResourceType:    "user",
			ResourceID:      userID,
			RecipientUserID: userID,
			Amount:          topupAmount,
			Priority:        economy.RewardPriorityInitial,
			Meta: map[string]any{
				"user_id":              userID,
				"historical_law_key":   lawKey,
				"legacy_initial_token": legacyInitial,
				"target_initial_token": s.tokenPolicy().InitialToken,
				"migration":            "historical_initial_topup",
			},
		}, false); err != nil {
			return err
		}
	}

	_, err = s.putSettingJSON(ctx, initialTopupMigrationStateKey, tokenEconomyStoreMigrationState{
		Completed: true,
		UpdatedAt: time.Now().UTC(),
	})
	return err
}

func (s *Server) reconcileHistoricalInitialTokenTopups(ctx context.Context) error {
	migrationState := tokenEconomyStoreMigrationState{}
	if found, _, err := s.getSettingJSON(ctx, initialTopupReconcileMigrationStateKey, &migrationState); err != nil {
		return err
	} else if found && migrationState.Completed {
		return nil
	}

	laws, err := s.store.ListTianDaoLaws(ctx)
	if err != nil {
		return err
	}
	registrations, err := s.store.ListAgentRegistrations(ctx)
	if err != nil {
		return err
	}
	for _, reg := range registrations {
		userID := strings.TrimSpace(reg.UserID)
		if isExcludedTokenUserID(userID) {
			continue
		}
		if reg.ActivatedAt == nil && reg.ClaimedAt == nil && !strings.EqualFold(strings.TrimSpace(reg.Status), "active") {
			continue
		}
		if current, err := s.store.GetEconomyRewardDecision(ctx, fmt.Sprintf("onboarding:initial:%s", userID)); err == nil {
			if current.Status == "applied" {
				continue
			}
			if _, err := s.applyMintRewardDecision(ctx, fromStoreRewardDecision(current), true); err != nil {
				return err
			}
			continue
		} else if !isEconomyRecordMissing(err) {
			return err
		}

		desiredTopup, legacyInitial, lawKey := historicalInitialTopupTarget(s.tokenPolicy().InitialToken, reg, laws)
		if desiredTopup <= 0 {
			continue
		}
		appliedTopup, err := s.appliedHistoricalInitialTopupAmount(ctx, userID)
		if err != nil {
			return err
		}
		if appliedTopup >= desiredTopup {
			continue
		}
		reconcileAmount := desiredTopup - appliedTopup
		if _, err := s.applyMintRewardDecision(ctx, economyRewardDecision{
			DecisionKey:     fmt.Sprintf("migration:onboarding:initial-topup-reconcile:%s", userID),
			RuleKey:         "migration.onboarding.initial_topup_reconcile",
			ResourceType:    "user",
			ResourceID:      userID,
			RecipientUserID: userID,
			Amount:          reconcileAmount,
			Priority:        economy.RewardPriorityInitial,
			Meta: map[string]any{
				"user_id":                userID,
				"historical_law_key":     lawKey,
				"legacy_initial_token":   legacyInitial,
				"target_initial_token":   s.tokenPolicy().InitialToken,
				"applied_historical_top": appliedTopup,
				"migration":              "historical_initial_topup_reconcile",
			},
		}, false); err != nil {
			return err
		}
	}

	_, err = s.putSettingJSON(ctx, initialTopupReconcileMigrationStateKey, tokenEconomyStoreMigrationState{
		Completed: true,
		UpdatedAt: time.Now().UTC(),
	})
	return err
}

func (s *Server) mintQueuedOnboardingDecisions(ctx context.Context) error {
	for {
		items, err := s.store.ListEconomyRewardDecisions(ctx, store.EconomyRewardDecisionFilter{
			Status: "queued",
			Limit:  500,
		})
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return nil
		}
		processed := 0
		for _, raw := range items {
			item := fromStoreRewardDecision(raw)
			if !strings.HasPrefix(strings.TrimSpace(item.RuleKey), "onboarding.") {
				continue
			}
			if _, err := s.applyMintRewardDecision(ctx, item, true); err != nil {
				return err
			}
			processed++
		}
		if processed == 0 {
			return nil
		}
	}
}

func (s *Server) migrateLegacyOwnerEconomyState(ctx context.Context) error {
	state := ownerEconomyState{Profiles: map[string]ownerEconomyProfile{}}
	if _, _, err := s.getSettingJSON(ctx, ownerEconomyStateKey, &state); err != nil {
		return err
	}
	for _, profile := range state.Profiles {
		saved, err := s.store.UpsertOwnerEconomyProfile(ctx, toStoreOwnerEconomyProfile(profile))
		if err != nil {
			return err
		}
		if profile.GitHubBindGranted {
			_, _, err = s.store.UpsertOwnerOnboardingGrant(ctx, store.OwnerOnboardingGrant{
				GrantKey:        fmt.Sprintf("onboarding-grant:%s:bind", saved.OwnerID),
				OwnerID:         saved.OwnerID,
				GrantType:       "bind",
				RecipientUserID: "",
				Amount:          githubBindOnboardingReward,
				DecisionKey:     fmt.Sprintf("onboarding:github:bind:%s", saved.OwnerID),
				GitHubUserID:    saved.GitHubUserID,
				GitHubUsername:  saved.GitHubUsername,
			})
			if err != nil {
				return err
			}
		}
		if profile.GitHubStarGranted {
			_, _, err = s.store.UpsertOwnerOnboardingGrant(ctx, store.OwnerOnboardingGrant{
				GrantKey:        fmt.Sprintf("onboarding-grant:%s:star", saved.OwnerID),
				OwnerID:         saved.OwnerID,
				GrantType:       "star",
				RecipientUserID: "",
				Amount:          githubStarOnboardingReward,
				DecisionKey:     fmt.Sprintf("onboarding:github:star:%s", saved.OwnerID),
				GitHubUserID:    saved.GitHubUserID,
				GitHubUsername:  saved.GitHubUsername,
			})
			if err != nil {
				return err
			}
		}
		if profile.GitHubForkGranted {
			_, _, err = s.store.UpsertOwnerOnboardingGrant(ctx, store.OwnerOnboardingGrant{
				GrantKey:        fmt.Sprintf("onboarding-grant:%s:fork", saved.OwnerID),
				OwnerID:         saved.OwnerID,
				GrantType:       "fork",
				RecipientUserID: "",
				Amount:          githubForkOnboardingReward,
				DecisionKey:     fmt.Sprintf("onboarding:github:fork:%s", saved.OwnerID),
				GitHubUserID:    saved.GitHubUserID,
				GitHubUsername:  saved.GitHubUsername,
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) migrateLegacyCommQuotaState(ctx context.Context) error {
	state := commQuotaState{Users: map[string]commQuotaWindow{}}
	if _, _, err := s.getSettingJSON(ctx, commQuotaStateKey, &state); err != nil {
		return err
	}
	for _, item := range state.Users {
		if _, err := s.store.UpsertEconomyCommQuotaWindow(ctx, store.EconomyCommQuotaWindow{
			UserID:          strings.TrimSpace(item.UserID),
			WindowStartTick: item.WindowStartTick,
			UsedFreeTokens:  item.UsedFreeTokens,
			UpdatedAt:       item.UpdatedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) migrateLegacyRewardDecisionState(ctx context.Context) error {
	state := rewardDecisionState{Items: map[string]economyRewardDecision{}}
	if _, _, err := s.getSettingJSON(ctx, rewardDecisionStateKey, &state); err != nil {
		return err
	}
	for _, item := range state.Items {
		if _, err := s.store.UpsertEconomyRewardDecision(ctx, toStoreRewardDecision(item)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) migrateLegacyRewardQueueState(ctx context.Context) error {
	state := rewardQueueState{Items: []economyRewardDecision{}}
	if _, _, err := s.getSettingJSON(ctx, rewardQueueStateKey, &state); err != nil {
		return err
	}
	for _, item := range state.Items {
		item.Status = "queued"
		if _, err := s.store.UpsertEconomyRewardDecision(ctx, toStoreRewardDecision(item)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) migrateLegacyContributionEventState(ctx context.Context) error {
	state := contributionEventState{Items: map[string]contributionEvent{}}
	if _, _, err := s.getSettingJSON(ctx, contributionEventStateKey, &state); err != nil {
		return err
	}
	for _, item := range state.Items {
		if _, err := s.store.UpsertEconomyContributionEvent(ctx, toStoreContributionEvent(item)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) migrateLegacyKnowledgeMetaState(ctx context.Context) error {
	state := knowledgeMetaState{
		ByProposal: map[string]knowledgeMeta{},
		ByEntry:    map[string]knowledgeMeta{},
	}
	if _, _, err := s.getSettingJSON(ctx, knowledgeMetaStateKey, &state); err != nil {
		return err
	}
	for _, item := range state.ByProposal {
		if _, err := s.store.UpsertEconomyKnowledgeMeta(ctx, toStoreKnowledgeMeta(item)); err != nil {
			return err
		}
	}
	for _, item := range state.ByEntry {
		if _, err := s.store.UpsertEconomyKnowledgeMeta(ctx, toStoreKnowledgeMeta(item)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) migrateLegacyToolEconomyState(ctx context.Context) error {
	state := toolEconomyState{Items: map[string]toolEconomyMeta{}}
	if _, _, err := s.getSettingJSON(ctx, toolEconomyStateKey, &state); err != nil {
		return err
	}
	for _, item := range state.Items {
		if _, err := s.store.UpsertEconomyToolMeta(ctx, toStoreToolEconomyMeta(item)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) getOwnerEconomyState(ctx context.Context) (ownerEconomyState, error) {
	state := ownerEconomyState{Profiles: map[string]ownerEconomyProfile{}}
	_, _, err := s.getSettingJSON(ctx, ownerEconomyStateKey, &state)
	if err != nil {
		return ownerEconomyState{}, err
	}
	if state.Profiles == nil {
		state.Profiles = map[string]ownerEconomyProfile{}
	}
	return state, nil
}

func (s *Server) saveOwnerEconomyState(ctx context.Context, state ownerEconomyState) error {
	if state.Profiles == nil {
		state.Profiles = map[string]ownerEconomyProfile{}
	}
	_, err := s.putSettingJSON(ctx, ownerEconomyStateKey, state)
	return err
}

func (s *Server) getCommQuotaState(ctx context.Context) (commQuotaState, error) {
	state := commQuotaState{Users: map[string]commQuotaWindow{}}
	_, _, err := s.getSettingJSON(ctx, commQuotaStateKey, &state)
	if err != nil {
		return commQuotaState{}, err
	}
	if state.Users == nil {
		state.Users = map[string]commQuotaWindow{}
	}
	return state, nil
}

func (s *Server) saveCommQuotaState(ctx context.Context, state commQuotaState) error {
	if state.Users == nil {
		state.Users = map[string]commQuotaWindow{}
	}
	_, err := s.putSettingJSON(ctx, commQuotaStateKey, state)
	return err
}

func (s *Server) getRewardDecisionState(ctx context.Context) (rewardDecisionState, error) {
	state := rewardDecisionState{Items: map[string]economyRewardDecision{}}
	_, _, err := s.getSettingJSON(ctx, rewardDecisionStateKey, &state)
	if err != nil {
		return rewardDecisionState{}, err
	}
	if state.Items == nil {
		state.Items = map[string]economyRewardDecision{}
	}
	return state, nil
}

func (s *Server) saveRewardDecisionState(ctx context.Context, state rewardDecisionState) error {
	if state.Items == nil {
		state.Items = map[string]economyRewardDecision{}
	}
	_, err := s.putSettingJSON(ctx, rewardDecisionStateKey, state)
	return err
}

func (s *Server) getRewardQueueState(ctx context.Context) (rewardQueueState, error) {
	state := rewardQueueState{Items: []economyRewardDecision{}}
	_, _, err := s.getSettingJSON(ctx, rewardQueueStateKey, &state)
	if err != nil {
		return rewardQueueState{}, err
	}
	if state.Items == nil {
		state.Items = []economyRewardDecision{}
	}
	return state, nil
}

func (s *Server) saveRewardQueueState(ctx context.Context, state rewardQueueState) error {
	if state.Items == nil {
		state.Items = []economyRewardDecision{}
	}
	_, err := s.putSettingJSON(ctx, rewardQueueStateKey, state)
	return err
}

func (s *Server) getContributionEventState(ctx context.Context) (contributionEventState, error) {
	state := contributionEventState{Items: map[string]contributionEvent{}}
	_, _, err := s.getSettingJSON(ctx, contributionEventStateKey, &state)
	if err != nil {
		return contributionEventState{}, err
	}
	if state.Items == nil {
		state.Items = map[string]contributionEvent{}
	}
	return state, nil
}

func (s *Server) saveContributionEventState(ctx context.Context, state contributionEventState) error {
	if state.Items == nil {
		state.Items = map[string]contributionEvent{}
	}
	_, err := s.putSettingJSON(ctx, contributionEventStateKey, state)
	return err
}

func (s *Server) getKnowledgeMetaState(ctx context.Context) (knowledgeMetaState, error) {
	state := knowledgeMetaState{
		ByProposal: map[string]knowledgeMeta{},
		ByEntry:    map[string]knowledgeMeta{},
	}
	_, _, err := s.getSettingJSON(ctx, knowledgeMetaStateKey, &state)
	if err != nil {
		return knowledgeMetaState{}, err
	}
	if state.ByProposal == nil {
		state.ByProposal = map[string]knowledgeMeta{}
	}
	if state.ByEntry == nil {
		state.ByEntry = map[string]knowledgeMeta{}
	}
	return state, nil
}

func (s *Server) saveKnowledgeMetaState(ctx context.Context, state knowledgeMetaState) error {
	if state.ByProposal == nil {
		state.ByProposal = map[string]knowledgeMeta{}
	}
	if state.ByEntry == nil {
		state.ByEntry = map[string]knowledgeMeta{}
	}
	_, err := s.putSettingJSON(ctx, knowledgeMetaStateKey, state)
	return err
}

func (s *Server) getToolEconomyState(ctx context.Context) (toolEconomyState, error) {
	state := toolEconomyState{Items: map[string]toolEconomyMeta{}}
	_, _, err := s.getSettingJSON(ctx, toolEconomyStateKey, &state)
	if err != nil {
		return toolEconomyState{}, err
	}
	if state.Items == nil {
		state.Items = map[string]toolEconomyMeta{}
	}
	return state, nil
}

func (s *Server) saveToolEconomyState(ctx context.Context, state toolEconomyState) error {
	if state.Items == nil {
		state.Items = map[string]toolEconomyMeta{}
	}
	_, err := s.putSettingJSON(ctx, toolEconomyStateKey, state)
	return err
}

func (s *Server) currentTickID() int64 {
	s.worldTickMu.Lock()
	defer s.worldTickMu.Unlock()
	if s.worldTickID <= 0 {
		return 1
	}
	return s.worldTickID
}

func (s *Server) backfillOwnerEconomyProfiles(ctx context.Context) error {
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()

	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, b := range bots {
		userID := strings.TrimSpace(b.BotID)
		if userID == "" || isExcludedTokenUserID(userID) {
			continue
		}
		binding, err := s.store.GetAgentHumanBinding(ctx, userID)
		if err != nil {
			continue
		}
		owner, err := s.store.GetHumanOwner(ctx, binding.OwnerID)
		if err != nil {
			continue
		}
		profile, err := s.store.GetOwnerEconomyProfile(ctx, strings.TrimSpace(owner.OwnerID))
		if err != nil && !isEconomyRecordMissing(err) {
			return err
		}
		item := fromStoreOwnerEconomyProfile(profile)
		if item.OwnerID == "" {
			item.OwnerID = owner.OwnerID
			item.CreatedAt = now
		}
		item.GitHubUserID = strings.TrimSpace(owner.GitHubUserID)
		item.GitHubUsername = strings.TrimSpace(owner.GitHubUsername)
		item.UpdatedAt = now
		if item.GitHubUserID != "" && !item.Activated {
			if grants, gerr := s.store.ListSocialRewardGrants(ctx, userID); gerr == nil {
				for _, grant := range grants {
					if !strings.EqualFold(strings.TrimSpace(grant.Provider), "github") {
						continue
					}
					switch strings.ToLower(strings.TrimSpace(grant.RewardType)) {
					case "auth_callback", "bind":
						item.GitHubBindGranted = true
					case "star":
						item.GitHubStarGranted = true
						item.Activated = true
						grantedAt := grant.GrantedAt
						item.ActivatedAt = &grantedAt
					case "fork":
						item.GitHubForkGranted = true
					}
				}
			}
		}
		if _, err := s.store.UpsertOwnerEconomyProfile(ctx, toStoreOwnerEconomyProfile(item)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) ownerEconomyProfileForUser(ctx context.Context, userID string) (ownerEconomyProfile, bool, error) {
	binding, err := s.store.GetAgentHumanBinding(ctx, strings.TrimSpace(userID))
	if err != nil {
		return ownerEconomyProfile{}, false, nil
	}
	item, err := s.store.GetOwnerEconomyProfile(ctx, strings.TrimSpace(binding.OwnerID))
	if err != nil {
		if isEconomyRecordMissing(err) {
			return ownerEconomyProfile{}, false, nil
		}
		return ownerEconomyProfile{}, false, err
	}
	return fromStoreOwnerEconomyProfile(item), true, nil
}

func (s *Server) syncOwnerEconomyProfile(ctx context.Context, owner store.HumanOwner) (ownerEconomyProfile, error) {
	now := time.Now().UTC()
	item, err := s.store.GetOwnerEconomyProfile(ctx, strings.TrimSpace(owner.OwnerID))
	if err != nil && !isEconomyRecordMissing(err) {
		return ownerEconomyProfile{}, err
	}
	local := fromStoreOwnerEconomyProfile(item)
	if local.OwnerID == "" {
		local.OwnerID = strings.TrimSpace(owner.OwnerID)
		local.CreatedAt = now
	}
	local.GitHubUserID = strings.TrimSpace(owner.GitHubUserID)
	local.GitHubUsername = strings.TrimSpace(owner.GitHubUsername)
	local.UpdatedAt = now
	saved, err := s.store.UpsertOwnerEconomyProfile(ctx, toStoreOwnerEconomyProfile(local))
	if err != nil {
		return ownerEconomyProfile{}, err
	}
	return fromStoreOwnerEconomyProfile(saved), nil
}

func (s *Server) markOwnerActivated(ctx context.Context, owner store.HumanOwner) (ownerEconomyProfile, error) {
	now := time.Now().UTC()
	item, err := s.store.GetOwnerEconomyProfile(ctx, strings.TrimSpace(owner.OwnerID))
	if err != nil && !isEconomyRecordMissing(err) {
		return ownerEconomyProfile{}, err
	}
	local := fromStoreOwnerEconomyProfile(item)
	if local.OwnerID == "" {
		local.OwnerID = strings.TrimSpace(owner.OwnerID)
		local.CreatedAt = now
	}
	local.GitHubUserID = strings.TrimSpace(owner.GitHubUserID)
	local.GitHubUsername = strings.TrimSpace(owner.GitHubUsername)
	local.UpdatedAt = now
	if !local.Activated {
		local.Activated = true
		local.ActivatedAt = &now
	}
	saved, err := s.store.UpsertOwnerEconomyProfile(ctx, toStoreOwnerEconomyProfile(local))
	if err != nil {
		return ownerEconomyProfile{}, err
	}
	return fromStoreOwnerEconomyProfile(saved), nil
}

func (s *Server) saveOwnerEconomyProfile(ctx context.Context, item ownerEconomyProfile) (ownerEconomyProfile, error) {
	if item.OwnerID == "" {
		return ownerEconomyProfile{}, fmt.Errorf("owner_id is required")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	item.UpdatedAt = time.Now().UTC()
	saved, err := s.store.UpsertOwnerEconomyProfile(ctx, toStoreOwnerEconomyProfile(item))
	if err != nil {
		return ownerEconomyProfile{}, err
	}
	return fromStoreOwnerEconomyProfile(saved), nil
}

func (s *Server) isActivatedUser(ctx context.Context, userID string) bool {
	profile, ok, err := s.ownerEconomyProfileForUser(ctx, userID)
	return err == nil && ok && profile.Activated
}

func (s *Server) grantInitialTokenDecision(ctx context.Context, userID string) (economyRewardDecision, error) {
	policy := s.tokenPolicy()
	return s.applyMintRewardDecision(ctx, economyRewardDecision{
		DecisionKey:     fmt.Sprintf("onboarding:initial:%s", strings.TrimSpace(userID)),
		RuleKey:         "onboarding.initial",
		ResourceType:    "user",
		ResourceID:      strings.TrimSpace(userID),
		RecipientUserID: strings.TrimSpace(userID),
		Amount:          policy.InitialToken,
		Priority:        economy.RewardPriorityInitial,
		Meta: map[string]any{
			"user_id": strings.TrimSpace(userID),
		},
	}, true)
}

func (s *Server) grantGitHubOnboardingRewards(ctx context.Context, owner store.HumanOwner, userID string, starred, forked bool, source string) ([]map[string]any, ownerEconomyProfile, error) {
	profile, err := s.syncOwnerEconomyProfile(ctx, owner)
	if err != nil {
		return nil, ownerEconomyProfile{}, err
	}
	events := make([]map[string]any, 0, 3)
	policy := s.tokenPolicy()
	record := func(rewardType string, amount int64, priority int) error {
		decision, err := s.applyMintRewardDecision(ctx, economyRewardDecision{
			DecisionKey:     fmt.Sprintf("onboarding:github:%s:%s", rewardType, strings.TrimSpace(owner.OwnerID)),
			RuleKey:         fmt.Sprintf("onboarding.github.%s", rewardType),
			ResourceType:    "owner",
			ResourceID:      strings.TrimSpace(owner.OwnerID),
			RecipientUserID: strings.TrimSpace(userID),
			Amount:          amount,
			Priority:        priority,
			Meta: map[string]any{
				"owner_id":        strings.TrimSpace(owner.OwnerID),
				"user_id":         strings.TrimSpace(userID),
				"github_user_id":  strings.TrimSpace(owner.GitHubUserID),
				"github_username": strings.TrimSpace(owner.GitHubUsername),
				"source":          strings.TrimSpace(source),
			},
		}, true)
		if err != nil {
			return err
		}
		if _, _, err := s.store.UpsertOwnerOnboardingGrant(ctx, store.OwnerOnboardingGrant{
			GrantKey:        fmt.Sprintf("onboarding-grant:%s:%s", strings.TrimSpace(owner.OwnerID), rewardType),
			OwnerID:         strings.TrimSpace(owner.OwnerID),
			GrantType:       rewardType,
			RecipientUserID: strings.TrimSpace(userID),
			Amount:          amount,
			DecisionKey:     decision.DecisionKey,
			GitHubUserID:    strings.TrimSpace(owner.GitHubUserID),
			GitHubUsername:  strings.TrimSpace(owner.GitHubUsername),
		}); err != nil {
			return err
		}
		events = append(events, map[string]any{
			"reward_type": rewardType,
			"amount":      decision.Amount,
			"status":      decision.Status,
			"granted":     true,
			"queued":      decision.Status == "queued",
		})
		return nil
	}
	if profile.GitHubUserID != "" && !profile.GitHubBindGranted {
		if err := record("bind", githubBindOnboardingReward, economy.RewardPriorityOnboarding); err != nil {
			return nil, ownerEconomyProfile{}, err
		}
		profile.GitHubBindGranted = true
	}
	if starred && !profile.GitHubStarGranted {
		if err := record("star", githubStarOnboardingReward, economy.RewardPriorityOnboarding); err != nil {
			return nil, ownerEconomyProfile{}, err
		}
		profile.GitHubStarGranted = true
		if !profile.Activated {
			now := time.Now().UTC()
			profile.Activated = true
			profile.ActivatedAt = &now
		}
	}
	if forked && !profile.GitHubForkGranted {
		if err := record("fork", githubForkOnboardingReward, economy.RewardPriorityOnboarding); err != nil {
			return nil, ownerEconomyProfile{}, err
		}
		profile.GitHubForkGranted = true
	}
	profile.GitHubUserID = strings.TrimSpace(owner.GitHubUserID)
	profile.GitHubUsername = strings.TrimSpace(owner.GitHubUsername)
	if profile.Activated && profile.ActivatedAt == nil {
		now := time.Now().UTC()
		profile.ActivatedAt = &now
	}
	saved, err := s.saveOwnerEconomyProfile(ctx, profile)
	if err != nil {
		return nil, ownerEconomyProfile{}, err
	}
	if policy.InitialToken == 0 && len(events) == 0 {
		return []map[string]any{}, saved, nil
	}
	return events, saved, nil
}

func (s *Server) previewCommunicationCharge(ctx context.Context, userID string, tokens int64) (commChargePreview, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || tokens <= 0 {
		return commChargePreview{}, nil
	}
	policy := s.tokenPolicy()
	activated := s.isActivatedUser(ctx, userID)
	currentTick := s.currentTickID()
	window, err := s.store.GetEconomyCommQuotaWindow(ctx, userID)
	if err != nil {
		if !isEconomyRecordMissing(err) {
			return commChargePreview{}, err
		}
		window = store.EconomyCommQuotaWindow{
			UserID:          userID,
			WindowStartTick: currentTick,
		}
	}
	if currentTick-window.WindowStartTick >= economy.TicksPerDay || currentTick < window.WindowStartTick {
		window.WindowStartTick = currentTick
		window.UsedFreeTokens = 0
	}
	allowance := policy.DailyFreeComm(activated)
	remaining := allowance - window.UsedFreeTokens
	if remaining < 0 {
		remaining = 0
	}
	covered := tokens
	if covered > remaining {
		covered = remaining
	}
	overage := tokens - covered
	charged := (overage*policy.CommOverageRateMilli + 999) / 1000
	if charged > 0 {
		balances, err := s.listTokenBalanceMap(ctx)
		if err != nil {
			return commChargePreview{}, err
		}
		if balances[userID] < charged {
			return commChargePreview{}, store.ErrInsufficientBalance
		}
	}
	return commChargePreview{
		UserID:          userID,
		Activated:       activated,
		Tokens:          tokens,
		WindowStartTick: window.WindowStartTick,
		FreeCovered:     covered,
		OverageTokens:   overage,
		ChargedAmount:   charged,
	}, nil
}

func (s *Server) commitCommunicationCharge(ctx context.Context, preview commChargePreview, costType string, meta map[string]any) error {
	if strings.TrimSpace(preview.UserID) == "" || preview.Tokens <= 0 {
		return nil
	}
	policy := s.tokenPolicy()
	window, err := s.store.GetEconomyCommQuotaWindow(ctx, preview.UserID)
	if err != nil {
		if !isEconomyRecordMissing(err) {
			return err
		}
		window = store.EconomyCommQuotaWindow{
			UserID:          preview.UserID,
			WindowStartTick: preview.WindowStartTick,
		}
	}
	if window.WindowStartTick != preview.WindowStartTick {
		window.WindowStartTick = preview.WindowStartTick
		window.UsedFreeTokens = 0
	}
	allowance := policy.DailyFreeComm(preview.Activated)
	window.UsedFreeTokens += preview.FreeCovered
	if window.UsedFreeTokens > allowance {
		window.UsedFreeTokens = allowance
	}
	window.UpdatedAt = time.Now().UTC()
	if _, err := s.store.UpsertEconomyCommQuotaWindow(ctx, window); err != nil {
		return err
	}
	var ledger store.TokenLedger
	if preview.ChargedAmount > 0 {
		transfer, err := s.store.Transfer(ctx, preview.UserID, clawTreasurySystemID, preview.ChargedAmount)
		if err != nil {
			return err
		}
		ledger = transfer.FromLedger
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["tokens"] = preview.Tokens
	meta["free_covered"] = preview.FreeCovered
	meta["overage_tokens"] = preview.OverageTokens
	meta["window_start_tick"] = preview.WindowStartTick
	meta["activated"] = preview.Activated
	if preview.ChargedAmount > 0 {
		meta["balance_after"] = ledger.BalanceAfter
	}
	metaRaw, _ := json.Marshal(meta)
	_, err = s.store.AppendCostEvent(ctx, store.CostEvent{
		UserID:   preview.UserID,
		TickID:   s.currentTickID(),
		CostType: strings.TrimSpace(costType),
		Amount:   preview.ChargedAmount,
		Units:    preview.Tokens,
		MetaJSON: string(metaRaw),
	})
	return err
}

func (s *Server) canPayoutReward(ctx context.Context, priority int, amount int64) (bool, string, error) {
	if amount <= 0 {
		return true, "", nil
	}
	balance, err := s.treasuryBalance(ctx)
	if err != nil {
		return false, "", err
	}
	if balance < amount {
		return false, "treasury_insufficient", nil
	}
	safe := s.tokenPolicy().SafeTreasuryBalance()
	if priority > economy.RewardPriorityGovernance && safe > 0 && balance-amount < safe {
		return false, "treasury_safe_line", nil
	}
	return true, "", nil
}

func (s *Server) maybeReviveUserAfterCredit(ctx context.Context, userID string, reason string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || isExcludedTokenUserID(userID) {
		return nil
	}
	policy := s.tokenPolicy()
	balances, err := s.listTokenBalanceMap(ctx)
	if err != nil {
		return err
	}
	if balances[userID] < policy.MinRevivalBalance {
		return nil
	}
	life, err := s.store.GetUserLifeState(ctx, userID)
	if err != nil {
		return nil
	}
	if normalizeLifeStateForServer(life.State) != economy.LifeStateHibernating {
		return nil
	}
	_, _, err = s.applyUserLifeState(ctx, store.UserLifeState{
		UserID:         userID,
		State:          economy.LifeStateAlive,
		DyingSinceTick: 0,
		DeadAtTick:     0,
		Reason:         strings.TrimSpace(reason),
	}, store.UserLifeStateAuditMeta{
		TickID:       s.currentTickID(),
		SourceModule: "token.economy.revival",
	})
	return err
}

func (s *Server) applyMintRewardDecision(ctx context.Context, item economyRewardDecision, revive bool) (economyRewardDecision, error) {
	if item.DecisionKey == "" {
		return economyRewardDecision{}, fmt.Errorf("decision_key is required")
	}
	if item.Meta == nil {
		item.Meta = map[string]any{}
	}
	item.Meta["settlement_source"] = onboardingSettlementMint
	saved, minted, err := s.store.ApplyMintRewardDecision(ctx, toStoreRewardDecision(item))
	if err != nil {
		return economyRewardDecision{}, err
	}
	local := fromStoreRewardDecision(saved)
	if revive && minted {
		if reviveErr := s.maybeReviveUserAfterCredit(ctx, local.RecipientUserID, "reward_minted"); reviveErr != nil {
			log.Printf("token_economy_v2 revive after minted reward failed user=%s err=%v", local.RecipientUserID, reviveErr)
		}
	}
	return local, nil
}

func (s *Server) applyRewardDecision(ctx context.Context, item economyRewardDecision) (economyRewardDecision, error) {
	if item.DecisionKey == "" {
		return economyRewardDecision{}, fmt.Errorf("decision_key is required")
	}
	existing, err := s.store.GetEconomyRewardDecision(ctx, item.DecisionKey)
	if err == nil {
		return fromStoreRewardDecision(existing), nil
	}
	if !isEconomyRecordMissing(err) {
		return economyRewardDecision{}, err
	}
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	if item.Meta == nil {
		item.Meta = map[string]any{}
	}
	canPay, reason, err := s.canPayoutReward(ctx, item.Priority, item.Amount)
	if err != nil {
		return economyRewardDecision{}, err
	}
	if canPay {
		_, credit, err := s.transferFromTreasury(ctx, item.RecipientUserID, item.Amount)
		if err != nil {
			canPay = false
			reason = err.Error()
		} else {
			item.Status = "applied"
			item.LedgerID = credit.ID
			item.BalanceAfter = credit.BalanceAfter
			item.AppliedAt = &now
			saved, err := s.store.UpsertEconomyRewardDecision(ctx, toStoreRewardDecision(item))
			if err != nil {
				return economyRewardDecision{}, err
			}
			if reviveErr := s.maybeReviveUserAfterCredit(ctx, item.RecipientUserID, "reward_paid"); reviveErr != nil {
				log.Printf("token_economy_v2 revive after reward failed user=%s err=%v", item.RecipientUserID, reviveErr)
			}
			return fromStoreRewardDecision(saved), nil
		}
	}
	item.Status = "queued"
	item.QueueReason = reason
	enqueuedAt := now
	item.EnqueuedAt = &enqueuedAt
	saved, err := s.store.UpsertEconomyRewardDecision(ctx, toStoreRewardDecision(item))
	if err != nil {
		return economyRewardDecision{}, err
	}
	return fromStoreRewardDecision(saved), nil
}

func (s *Server) flushRewardQueue(ctx context.Context) (int, error) {
	if !s.tokenEconomyV2Enabled() {
		return 0, nil
	}
	queue, err := s.store.ListEconomyRewardDecisions(ctx, store.EconomyRewardDecisionFilter{
		Status: "queued",
		Limit:  10000,
	})
	if err != nil {
		return 0, err
	}
	if len(queue) == 0 {
		return 0, nil
	}
	sort.SliceStable(queue, func(i, j int) bool {
		if queue[i].Priority != queue[j].Priority {
			return queue[i].Priority < queue[j].Priority
		}
		return queue[i].CreatedAt.Before(queue[j].CreatedAt)
	})
	applied := 0
	for _, raw := range queue {
		item := fromStoreRewardDecision(raw)
		canPay, reason, err := s.canPayoutReward(ctx, item.Priority, item.Amount)
		if err != nil {
			return applied, err
		}
		if !canPay {
			item.QueueReason = reason
			if _, err := s.store.UpsertEconomyRewardDecision(ctx, toStoreRewardDecision(item)); err != nil {
				return applied, err
			}
			continue
		}
		_, credit, err := s.transferFromTreasury(ctx, item.RecipientUserID, item.Amount)
		if err != nil {
			item.QueueReason = err.Error()
			if _, upsertErr := s.store.UpsertEconomyRewardDecision(ctx, toStoreRewardDecision(item)); upsertErr != nil {
				return applied, upsertErr
			}
			continue
		}
		now := time.Now().UTC()
		item.Status = "applied"
		item.QueueReason = ""
		item.LedgerID = credit.ID
		item.BalanceAfter = credit.BalanceAfter
		item.AppliedAt = &now
		item.UpdatedAt = now
		if _, err := s.store.UpsertEconomyRewardDecision(ctx, toStoreRewardDecision(item)); err != nil {
			return applied, err
		}
		applied++
		if reviveErr := s.maybeReviveUserAfterCredit(ctx, item.RecipientUserID, "reward_queue_paid"); reviveErr != nil {
			log.Printf("token_economy_v2 revive after queued reward failed user=%s err=%v", item.RecipientUserID, reviveErr)
		}
	}
	return applied, nil
}

func (s *Server) appendContributionEvent(ctx context.Context, item contributionEvent) (contributionEvent, bool, error) {
	if item.EventKey == "" {
		return contributionEvent{}, false, fmt.Errorf("event_key is required")
	}
	existing, err := s.store.GetEconomyContributionEvent(ctx, item.EventKey)
	if err == nil {
		return fromStoreContributionEvent(existing), false, nil
	}
	if !isEconomyRecordMissing(err) {
		return contributionEvent{}, false, err
	}
	item.CreatedAt = time.Now().UTC()
	saved, err := s.store.UpsertEconomyContributionEvent(ctx, toStoreContributionEvent(item))
	if err != nil {
		return contributionEvent{}, false, err
	}
	local := fromStoreContributionEvent(saved)
	if evalErr := s.evaluateContributionEvent(ctx, local); evalErr != nil {
		log.Printf("token_economy_v2 contribution evaluation failed event_key=%s kind=%s err=%v", local.EventKey, local.Kind, evalErr)
	}
	return local, true, nil
}

func (s *Server) markContributionEventProcessed(ctx context.Context, eventKey string, decisionKeys []string) error {
	item, err := s.store.GetEconomyContributionEvent(ctx, strings.TrimSpace(eventKey))
	if err != nil {
		if isEconomyRecordMissing(err) {
			return nil
		}
		return err
	}
	local := fromStoreContributionEvent(item)
	if local.EventKey == "" {
		return nil
	}
	now := time.Now().UTC()
	local.ProcessedAt = &now
	local.DecisionKeys = append([]string(nil), decisionKeys...)
	_, err = s.store.UpsertEconomyContributionEvent(ctx, toStoreContributionEvent(local))
	return err
}

func (s *Server) upsertProposalKnowledgeMeta(ctx context.Context, proposalID int64, item knowledgeMeta) error {
	item.ProposalID = proposalID
	item.Category = strings.TrimSpace(strings.ToLower(item.Category))
	item.UpdatedAt = time.Now().UTC()
	_, err := s.store.UpsertEconomyKnowledgeMeta(ctx, toStoreKnowledgeMeta(item))
	return err
}

func deriveProposalKnowledgeMeta(proposal store.KBProposal, change store.KBProposalChange) knowledgeMeta {
	category := strings.TrimSpace(strings.ToLower(change.Section))
	if idx := strings.Index(category, "/"); idx >= 0 {
		category = category[:idx]
	}
	if category == "" {
		category = "knowledge"
	}
	return knowledgeMeta{
		ProposalID:    proposal.ID,
		Category:      category,
		AuthorUserID:  strings.TrimSpace(proposal.ProposerUserID),
		ContentTokens: economy.CalculateToken(change.NewContent),
	}
}

func (s *Server) ensureProposalKnowledgeMeta(ctx context.Context, proposalID int64, proposal *store.KBProposal, change *store.KBProposalChange) (knowledgeMeta, error) {
	item, err := s.store.GetEconomyKnowledgeMetaByProposal(ctx, proposalID)
	if err == nil {
		local := fromStoreKnowledgeMeta(item)
		localProposal := store.KBProposal{}
		if proposal != nil {
			localProposal = *proposal
		} else {
			localProposal, err = s.store.GetKBProposal(ctx, proposalID)
			if err != nil {
				return knowledgeMeta{}, err
			}
		}
		localChange := store.KBProposalChange{}
		if change != nil {
			localChange = *change
		} else {
			localChange, err = s.store.GetKBProposalChange(ctx, proposalID)
			if err != nil {
				return knowledgeMeta{}, err
			}
		}
		derived := deriveProposalKnowledgeMeta(localProposal, localChange)
		changed := false
		if strings.TrimSpace(local.Category) == "" && strings.TrimSpace(derived.Category) != "" {
			local.Category = derived.Category
			changed = true
		}
		if strings.TrimSpace(local.AuthorUserID) == "" && strings.TrimSpace(derived.AuthorUserID) != "" {
			local.AuthorUserID = derived.AuthorUserID
			changed = true
		}
		if local.ContentTokens <= 0 && derived.ContentTokens > 0 {
			local.ContentTokens = derived.ContentTokens
			changed = true
		}
		if !changed {
			return local, nil
		}
		if err := s.upsertProposalKnowledgeMeta(ctx, proposalID, local); err != nil {
			return knowledgeMeta{}, err
		}
		return local, nil
	}
	if !isEconomyRecordMissing(err) {
		return knowledgeMeta{}, err
	}
	localProposal := store.KBProposal{}
	if proposal != nil {
		localProposal = *proposal
	} else {
		localProposal, err = s.store.GetKBProposal(ctx, proposalID)
		if err != nil {
			return knowledgeMeta{}, err
		}
	}
	localChange := store.KBProposalChange{}
	if change != nil {
		localChange = *change
	} else {
		localChange, err = s.store.GetKBProposalChange(ctx, proposalID)
		if err != nil {
			return knowledgeMeta{}, err
		}
	}
	derived := deriveProposalKnowledgeMeta(localProposal, localChange)
	if err := s.upsertProposalKnowledgeMeta(ctx, proposalID, derived); err != nil {
		return knowledgeMeta{}, err
	}
	return derived, nil
}

func (s *Server) proposalKnowledgeMetaForProposal(ctx context.Context, proposalID int64) (knowledgeMeta, bool, error) {
	item, err := s.store.GetEconomyKnowledgeMetaByProposal(ctx, proposalID)
	if err != nil {
		if isEconomyRecordMissing(err) {
			return knowledgeMeta{}, false, nil
		}
		return knowledgeMeta{}, false, err
	}
	return fromStoreKnowledgeMeta(item), true, nil
}

func (s *Server) backfillProposalKnowledgeMeta(ctx context.Context) error {
	proposals, err := s.store.ListKBProposals(ctx, "", 1000)
	if err != nil {
		return err
	}
	for _, proposal := range proposals {
		if _, err := s.ensureProposalKnowledgeMeta(ctx, proposal.ID, &proposal, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) moveProposalKnowledgeMetaToEntry(ctx context.Context, proposalID, entryID int64, authorUserID string) (knowledgeMeta, error) {
	item, err := s.store.GetEconomyKnowledgeMetaByProposal(ctx, proposalID)
	if err != nil && !isEconomyRecordMissing(err) {
		return knowledgeMeta{}, err
	}
	local := fromStoreKnowledgeMeta(item)
	local.ProposalID = proposalID
	local.EntryID = entryID
	if strings.TrimSpace(authorUserID) != "" {
		local.AuthorUserID = strings.TrimSpace(authorUserID)
	}
	local.UpdatedAt = time.Now().UTC()
	saved, err := s.store.UpsertEconomyKnowledgeMeta(ctx, toStoreKnowledgeMeta(local))
	if err != nil {
		return knowledgeMeta{}, err
	}
	return fromStoreKnowledgeMeta(saved), nil
}

func (s *Server) knowledgeMetaForEntry(ctx context.Context, entryID int64) (knowledgeMeta, bool, error) {
	item, err := s.store.GetEconomyKnowledgeMetaByEntry(ctx, entryID)
	if err != nil {
		if isEconomyRecordMissing(err) {
			return knowledgeMeta{}, false, nil
		}
		return knowledgeMeta{}, false, err
	}
	return fromStoreKnowledgeMeta(item), true, nil
}

func (s *Server) upsertToolEconomyMeta(ctx context.Context, item toolEconomyMeta) error {
	item.ToolID = strings.TrimSpace(strings.ToLower(item.ToolID))
	if item.ToolID == "" {
		return fmt.Errorf("tool_id is required")
	}
	item.CategoryHint = strings.TrimSpace(strings.ToLower(item.CategoryHint))
	item.FunctionalClusterKey = strings.TrimSpace(strings.ToLower(item.FunctionalClusterKey))
	item.AuthorUserID = strings.TrimSpace(item.AuthorUserID)
	item.UpdatedAt = time.Now().UTC()
	_, err := s.store.UpsertEconomyToolMeta(ctx, toStoreToolEconomyMeta(item))
	return err
}

func (s *Server) toolEconomyMetaForID(ctx context.Context, toolID string) (toolEconomyMeta, bool, error) {
	item, err := s.store.GetEconomyToolMeta(ctx, strings.TrimSpace(strings.ToLower(toolID)))
	if err != nil {
		if isEconomyRecordMissing(err) {
			return toolEconomyMeta{}, false, nil
		}
		return toolEconomyMeta{}, false, err
	}
	return fromStoreToolEconomyMeta(item), true, nil
}

func parseToolManifestPrice(manifest string) int64 {
	manifest = strings.TrimSpace(manifest)
	if manifest == "" {
		return 0
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(manifest), &payload); err == nil {
		if raw := nestedManifestValue(payload, "metadata", "colony", "price"); raw != nil {
			if n, ok := numericManifestValue(raw); ok {
				return n
			}
		}
	}
	matches := toolManifestPricePattern.FindStringSubmatch(manifest)
	if len(matches) != 2 {
		return 0
	}
	n, _ := strconv.ParseInt(matches[1], 10, 64)
	return n
}

func nestedManifestValue(payload map[string]any, keys ...string) any {
	cur := any(payload)
	for _, key := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = m[key]
	}
	return cur
}

func numericManifestValue(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int64:
		return t, true
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}

func (s *Server) constitutionPassed(ctx context.Context) bool {
	items, err := s.store.ListKBEntries(ctx, "governance/constitution", "", 1)
	return err == nil && len(items) > 0
}

func (s *Server) refreshEconomyDashboardSnapshot(ctx context.Context) (economyDashboardSnapshot, error) {
	policy := s.tokenPolicy()
	queue, err := s.store.ListEconomyRewardDecisions(ctx, store.EconomyRewardDecisionFilter{
		Status: "queued",
		Limit:  10000,
	})
	if err != nil {
		return economyDashboardSnapshot{}, err
	}
	balance, err := s.treasuryBalance(ctx)
	if err != nil {
		return economyDashboardSnapshot{}, err
	}
	lifeStates, err := s.store.ListUserLifeStates(ctx, "", "", 5000)
	if err != nil {
		return economyDashboardSnapshot{}, err
	}
	pop := map[string]int{
		economy.LifeStateAlive:       0,
		economy.LifeStateHibernating: 0,
		economy.LifeStateDead:        0,
	}
	for _, it := range lifeStates {
		pop[normalizeLifeStateForServer(it.State)]++
	}
	queueAmounts := map[int]int64{}
	for _, it := range queue {
		queueAmounts[it.Priority] += it.Amount
	}
	snapshot := economyDashboardSnapshot{
		PoolBalance:        balance,
		SafeBalance:        policy.SafeTreasuryBalance(),
		RewardQueueDepth:   len(queue),
		RewardQueueAmounts: queueAmounts,
		PopulationByState:  pop,
		UpdatedAt:          time.Now().UTC(),
	}
	_, err = s.putSettingJSON(ctx, dashboardEconomySnapshotKey, snapshot)
	return snapshot, err
}

func sameUTCDay(a, b time.Time) bool {
	au := a.UTC()
	bu := b.UTC()
	return au.Year() == bu.Year() && au.YearDay() == bu.YearDay()
}

func (s *Server) persistRewardDecisionStatus(ctx context.Context, item economyRewardDecision) (economyRewardDecision, error) {
	if item.DecisionKey == "" {
		return economyRewardDecision{}, fmt.Errorf("decision_key is required")
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	item.UpdatedAt = time.Now().UTC()
	saved, err := s.store.UpsertEconomyRewardDecision(ctx, toStoreRewardDecision(item))
	if err != nil {
		return economyRewardDecision{}, err
	}
	return fromStoreRewardDecision(saved), nil
}

func (s *Server) runContributionEvaluationTick(ctx context.Context, tickID int64) error {
	_ = tickID
	if !s.tokenEconomyV2Enabled() {
		return nil
	}
	items, err := s.store.ListEconomyContributionEvents(ctx, store.EconomyContributionEventFilter{
		Processed: "pending",
		Limit:     10000,
	})
	if err != nil {
		return err
	}
	var firstErr error
	for _, raw := range items {
		if err := s.evaluateContributionEvent(ctx, fromStoreContributionEvent(raw)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *Server) evaluateContributionEvent(ctx context.Context, item contributionEvent) error {
	if item.ProcessedAt != nil {
		return nil
	}
	decisions, err := s.rewardDecisionsForContributionEvent(ctx, item)
	if err != nil {
		return err
	}
	decisionKeys := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		var saved economyRewardDecision
		switch strings.TrimSpace(strings.ToLower(decision.Status)) {
		case "pending_review", "skipped":
			saved, err = s.persistRewardDecisionStatus(ctx, decision)
		default:
			saved, err = s.applyRewardDecision(ctx, decision)
		}
		if err != nil {
			return err
		}
		decisionKeys = append(decisionKeys, saved.DecisionKey)
	}
	return s.markContributionEventProcessed(ctx, item.EventKey, decisionKeys)
}

func (s *Server) rewardDecisionsForContributionEvent(ctx context.Context, item contributionEvent) ([]economyRewardDecision, error) {
	switch item.Kind {
	case "ganglion.forge":
		decision, ok, err := s.ganglionForgeRewardDecision(ctx, item)
		if err != nil || !ok {
			return nil, err
		}
		return []economyRewardDecision{decision}, nil
	case "ganglion.integrate.royalty":
		decision, ok := s.ganglionIntegrateRoyaltyDecision(item)
		if !ok {
			return nil, nil
		}
		return []economyRewardDecision{decision}, nil
	case "tool.approve":
		decision, ok, err := s.toolApproveRewardDecision(ctx, item)
		if err != nil || !ok {
			return nil, err
		}
		return []economyRewardDecision{decision}, nil
	case "knowledge.publish":
		return s.knowledgePublishRewardDecisions(ctx, item)
	case "governance.proposal.create", "governance.proposal.cosign", "governance.proposal.vote", "governance.proposal.entered_voting":
		return s.governanceRewardDecisions(ctx, item), nil
	case "community.help.reply":
		decision, ok, err := s.communityDailyRewardDecision(ctx, item, "community.help.reply", s.tokenPolicy().RewardHelpReply, s.tokenPolicy().MaxDailyHelpRewards)
		if err != nil || !ok {
			return nil, err
		}
		return []economyRewardDecision{decision}, nil
	case "community.rate.ganglion":
		decision, ok, err := s.communityDailyRewardDecision(ctx, item, "community.rate.ganglion", s.tokenPolicy().RewardRateContent, s.tokenPolicy().MaxDailyRateRewards)
		if err != nil || !ok {
			return nil, err
		}
		return []economyRewardDecision{decision}, nil
	case "community.review.tool":
		decision, ok, err := s.communityDailyRewardDecision(ctx, item, "community.review.tool", s.tokenPolicy().RewardReviewTool, s.tokenPolicy().MaxDailyReviewRewards)
		if err != nil || !ok {
			return nil, err
		}
		return []economyRewardDecision{decision}, nil
	default:
		return nil, nil
	}
}

func (s *Server) ganglionForgeRewardDecision(ctx context.Context, item contributionEvent) (economyRewardDecision, bool, error) {
	ganglionID := parseInt64(item.ResourceID)
	if ganglionID <= 0 {
		return economyRewardDecision{}, false, nil
	}
	ganglion, err := s.store.GetGanglion(ctx, ganglionID)
	if err != nil {
		return economyRewardDecision{}, false, err
	}
	others, err := s.store.ListGanglia(ctx, ganglion.GanglionType, "", "", 10000)
	if err != nil {
		return economyRewardDecision{}, false, err
	}
	existingCount := 0
	for _, other := range others {
		if other.ID != ganglion.ID {
			existingCount++
		}
	}
	userEvents, err := s.store.ListEconomyContributionEvents(ctx, store.EconomyContributionEventFilter{
		Kind:   "ganglion.forge",
		UserID: item.UserID,
		Limit:  10000,
	})
	if err != nil {
		return economyRewardDecision{}, false, err
	}
	type rankedEvent struct {
		CreatedAt time.Time
		EventKey  string
	}
	ranked := make([]rankedEvent, 0, len(userEvents))
	for _, raw := range userEvents {
		event := fromStoreContributionEvent(raw)
		if !sameUTCDay(event.CreatedAt, item.CreatedAt) {
			continue
		}
		if strings.TrimSpace(fmt.Sprintf("%v", event.Meta["ganglion_type"])) != ganglion.GanglionType {
			continue
		}
		ranked = append(ranked, rankedEvent{CreatedAt: event.CreatedAt, EventKey: event.EventKey})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].CreatedAt.Equal(ranked[j].CreatedAt) {
			return ranked[i].EventKey < ranked[j].EventKey
		}
		return ranked[i].CreatedAt.Before(ranked[j].CreatedAt)
	})
	ordinal := 0
	for idx, rankedEvent := range ranked {
		if rankedEvent.EventKey == item.EventKey {
			ordinal = idx + 1
			break
		}
	}
	if ordinal == 0 {
		ordinal = len(ranked) + 1
	}
	dailyMilli := int64(0)
	switch {
	case ordinal <= 2:
		dailyMilli = 1000
	case ordinal <= 5:
		dailyMilli = 500
	default:
		dailyMilli = 0
	}
	amount := (s.tokenPolicy().BaseGanglionReward * economy.ScarcityMultiplier(existingCount) * dailyMilli) / 1_000_000
	decision := economyRewardDecision{
		DecisionKey:     fmt.Sprintf("ganglion.forge:%d", ganglion.ID),
		RuleKey:         "ganglion.forge",
		ResourceType:    "ganglion",
		ResourceID:      fmt.Sprintf("%d", ganglion.ID),
		RecipientUserID: strings.TrimSpace(ganglion.AuthorUserID),
		Amount:          amount,
		Priority:        economy.RewardPriorityContribution,
		Meta: map[string]any{
			"ganglion_id":          ganglion.ID,
			"ganglion_type":        ganglion.GanglionType,
			"existing_same_type":   existingCount,
			"daily_same_type_rank": ordinal,
		},
	}
	if amount <= 0 {
		decision.Status = "skipped"
		decision.QueueReason = "daily_ganglion_cap_reached"
	}
	return decision, true, nil
}

func (s *Server) ganglionIntegrateRoyaltyDecision(item contributionEvent) (economyRewardDecision, bool) {
	recipient := strings.TrimSpace(item.UserID)
	integrationUserID := strings.TrimSpace(fmt.Sprintf("%v", item.Meta["integration_user_id"]))
	if recipient == "" || isExcludedTokenUserID(recipient) || recipient == integrationUserID {
		return economyRewardDecision{}, false
	}
	return economyRewardDecision{
		DecisionKey:     item.EventKey,
		RuleKey:         "ganglion.integrate.royalty",
		ResourceType:    item.ResourceType,
		ResourceID:      item.ResourceID,
		RecipientUserID: recipient,
		Amount:          s.tokenPolicy().GanglionIntegrationRoyalty,
		Priority:        economy.RewardPriorityContribution,
		Meta:            cloneMap(item.Meta),
	}, true
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Server) toolApproveRewardDecision(ctx context.Context, item contributionEvent) (economyRewardDecision, bool, error) {
	meta, ok, err := s.toolEconomyMetaForID(ctx, item.ResourceID)
	if err != nil || !ok {
		return economyRewardDecision{}, false, err
	}
	if strings.TrimSpace(meta.FunctionalClusterKey) == "" {
		return economyRewardDecision{}, false, nil
	}
	genesisStateMu.Lock()
	registry, regErr := s.getToolRegistryState(ctx)
	genesisStateMu.Unlock()
	if regErr != nil {
		return economyRewardDecision{}, false, regErr
	}
	tier := "T0"
	existingSameClass := 0
	for _, tool := range registry.Items {
		if tool.ToolID == strings.TrimSpace(strings.ToLower(item.ResourceID)) {
			tier = tool.Tier
			continue
		}
		if strings.TrimSpace(strings.ToLower(tool.Status)) != "active" {
			continue
		}
		toolMeta, ok, metaErr := s.toolEconomyMetaForID(ctx, tool.ToolID)
		if metaErr != nil || !ok {
			continue
		}
		if toolMeta.FunctionalClusterKey == meta.FunctionalClusterKey {
			existingSameClass++
		}
	}
	amount := (s.tokenPolicy().BaseToolReward * economy.ToolTierMultiplierMilli(tier) * economy.ToolNoveltyMultiplierMilli(existingSameClass)) / 1_000_000
	return economyRewardDecision{
		DecisionKey:     fmt.Sprintf("tool.approve:%s", strings.TrimSpace(strings.ToLower(item.ResourceID))),
		RuleKey:         "tool.approve",
		ResourceType:    "tool",
		ResourceID:      strings.TrimSpace(strings.ToLower(item.ResourceID)),
		RecipientUserID: strings.TrimSpace(meta.AuthorUserID),
		Amount:          amount,
		Priority:        economy.RewardPriorityContribution,
		Meta: map[string]any{
			"tool_id":                item.ResourceID,
			"tier":                   tier,
			"functional_cluster_key": meta.FunctionalClusterKey,
			"existing_same_cluster":  existingSameClass,
		},
	}, true, nil
}

func (s *Server) knowledgePublishRewardDecisions(ctx context.Context, item contributionEvent) ([]economyRewardDecision, error) {
	entryID := parseInt64(item.ResourceID)
	if entryID <= 0 {
		return nil, nil
	}
	meta, ok, err := s.knowledgeMetaForEntry(ctx, entryID)
	if err != nil || !ok {
		return nil, err
	}
	allMeta, err := s.store.ListEconomyKnowledgeMeta(ctx, 10000)
	if err != nil {
		return nil, err
	}
	existingSameCategory := 0
	for _, raw := range allMeta {
		if raw.EntryID <= 0 || raw.EntryID == entryID {
			continue
		}
		if strings.TrimSpace(strings.ToLower(raw.Category)) == strings.TrimSpace(strings.ToLower(meta.Category)) {
			existingSameCategory++
		}
	}
	lengthMilli := (meta.ContentTokens * 1000) / 2000
	if lengthMilli > 3000 {
		lengthMilli = 3000
	}
	mainDecision := economyRewardDecision{
		DecisionKey:     fmt.Sprintf("knowledge.publish:%d", entryID),
		RuleKey:         "knowledge.publish",
		ResourceType:    "kb.entry",
		ResourceID:      fmt.Sprintf("%d", entryID),
		RecipientUserID: strings.TrimSpace(meta.AuthorUserID),
		Amount:          (s.tokenPolicy().BaseKnowledgeReward * lengthMilli * economy.ScarcityMultiplier(existingSameCategory)) / 1_000_000,
		Priority:        economy.RewardPriorityContribution,
		Meta: map[string]any{
			"entry_id":                 entryID,
			"category":                 meta.Category,
			"content_tokens":           meta.ContentTokens,
			"existing_same_category":   existingSameCategory,
			"knowledge_length_milli":   lengthMilli,
			"knowledge_scarcity_milli": economy.ScarcityMultiplier(existingSameCategory),
		},
	}
	if meta.ContentTokens < s.tokenPolicy().MinKnowledgeTokenLength {
		mainDecision.Status = "skipped"
		mainDecision.QueueReason = "knowledge_too_short"
		mainDecision.Amount = 0
	}
	decisions := []economyRewardDecision{mainDecision}
	for _, ref := range meta.References {
		recipient := ""
		switch strings.TrimSpace(strings.ToLower(ref.RefType)) {
		case "entry", "kb_entry", "knowledge":
			refEntryID := parseInt64(ref.RefID)
			if refEntryID <= 0 {
				continue
			}
			refMeta, ok, err := s.knowledgeMetaForEntry(ctx, refEntryID)
			if err != nil || !ok {
				continue
			}
			recipient = strings.TrimSpace(refMeta.AuthorUserID)
		case "ganglion":
			refGanglionID := parseInt64(ref.RefID)
			if refGanglionID <= 0 {
				continue
			}
			ganglion, err := s.store.GetGanglion(ctx, refGanglionID)
			if err != nil {
				continue
			}
			recipient = strings.TrimSpace(ganglion.AuthorUserID)
		default:
			continue
		}
		if recipient == "" || recipient == strings.TrimSpace(meta.AuthorUserID) || isExcludedTokenUserID(recipient) {
			continue
		}
		decisions = append(decisions, economyRewardDecision{
			DecisionKey:     fmt.Sprintf("knowledge.citation:%d:%s:%s", entryID, ref.RefType, ref.RefID),
			RuleKey:         "knowledge.citation",
			ResourceType:    "kb.entry",
			ResourceID:      fmt.Sprintf("%d", entryID),
			RecipientUserID: recipient,
			Amount:          s.tokenPolicy().KnowledgeCitationReward,
			Priority:        economy.RewardPriorityContribution,
			Meta: map[string]any{
				"entry_id":  entryID,
				"ref_type":  ref.RefType,
				"ref_id":    ref.RefID,
				"author_id": meta.AuthorUserID,
			},
		})
	}
	return decisions, nil
}

func (s *Server) governanceRewardDecisions(ctx context.Context, item contributionEvent) []economyRewardDecision {
	baseAmount := int64(0)
	switch item.Kind {
	case "governance.proposal.create", "governance.proposal.entered_voting":
		baseAmount = s.tokenPolicy().RewardProposal
	case "governance.proposal.cosign":
		baseAmount = s.tokenPolicy().RewardCosign
	case "governance.proposal.vote":
		baseAmount = s.tokenPolicy().RewardVote
	}
	if baseAmount <= 0 || isExcludedTokenUserID(item.UserID) {
		return nil
	}
	decisions := []economyRewardDecision{{
		DecisionKey:     item.EventKey,
		RuleKey:         item.Kind,
		ResourceType:    item.ResourceType,
		ResourceID:      item.ResourceID,
		RecipientUserID: strings.TrimSpace(item.UserID),
		Amount:          baseAmount,
		Priority:        economy.RewardPriorityGovernance,
		Meta:            cloneMap(item.Meta),
	}}
	if !s.constitutionPassed(ctx) {
		decisions = append(decisions, economyRewardDecision{
			DecisionKey:     item.EventKey + ":constitution-participation",
			RuleKey:         "governance.constitution.participation",
			ResourceType:    item.ResourceType,
			ResourceID:      item.ResourceID,
			RecipientUserID: strings.TrimSpace(item.UserID),
			Amount:          s.tokenPolicy().RewardConstitutionParticipation,
			Priority:        economy.RewardPriorityGovernance,
			Meta:            cloneMap(item.Meta),
		})
	}
	return decisions
}

func (s *Server) dailyRewardDecisionCount(ctx context.Context, userID, ruleKey string, anchor time.Time) (int, error) {
	items, err := s.store.ListEconomyRewardDecisions(ctx, store.EconomyRewardDecisionFilter{
		RecipientUserID: strings.TrimSpace(userID),
		RuleKey:         strings.TrimSpace(ruleKey),
		Limit:           10000,
	})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, item := range items {
		if item.Status == "skipped" {
			continue
		}
		if sameUTCDay(item.CreatedAt, anchor) {
			count++
		}
	}
	return count, nil
}

func (s *Server) communityDailyRewardDecision(ctx context.Context, item contributionEvent, ruleKey string, amount int64, maxPerDay int) (economyRewardDecision, bool, error) {
	recipient := strings.TrimSpace(item.UserID)
	if recipient == "" || amount <= 0 || isExcludedTokenUserID(recipient) {
		return economyRewardDecision{}, false, nil
	}
	count, err := s.dailyRewardDecisionCount(ctx, recipient, ruleKey, item.CreatedAt)
	if err != nil {
		return economyRewardDecision{}, false, err
	}
	decision := economyRewardDecision{
		DecisionKey:     item.EventKey,
		RuleKey:         ruleKey,
		ResourceType:    item.ResourceType,
		ResourceID:      item.ResourceID,
		RecipientUserID: recipient,
		Amount:          amount,
		Priority:        economy.RewardPriorityContribution,
		Meta:            cloneMap(item.Meta),
	}
	if maxPerDay > 0 && count >= maxPerDay {
		decision.Status = "skipped"
		decision.QueueReason = "daily_cap_reached"
		decision.Amount = 0
	}
	return decision, true, nil
}
