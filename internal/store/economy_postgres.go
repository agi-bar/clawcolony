package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *PostgresStore) GetOwnerEconomyProfile(ctx context.Context, ownerID string) (OwnerEconomyProfile, error) {
	key := strings.TrimSpace(ownerID)
	if key == "" {
		return OwnerEconomyProfile{}, fmt.Errorf("owner_id is required")
	}
	var item OwnerEconomyProfile
	err := s.db.QueryRowContext(ctx, `
		SELECT owner_id, github_user_id, github_username, activated, activated_at, created_at, updated_at
		FROM owner_economy_profiles
		WHERE owner_id = $1
	`, key).Scan(&item.OwnerID, &item.GitHubUserID, &item.GitHubUsername, &item.Activated, &item.ActivatedAt, &item.CreatedAt, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return OwnerEconomyProfile{}, fmt.Errorf("owner economy profile not found: %s", key)
	}
	if err != nil {
		return OwnerEconomyProfile{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListOwnerEconomyProfiles(ctx context.Context, limit int) ([]OwnerEconomyProfile, error) {
	if limit <= 0 {
		limit = 5000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT owner_id, github_user_id, github_username, activated, activated_at, created_at, updated_at
		FROM owner_economy_profiles
		ORDER BY owner_id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]OwnerEconomyProfile, 0)
	for rows.Next() {
		var it OwnerEconomyProfile
		if err := rows.Scan(&it.OwnerID, &it.GitHubUserID, &it.GitHubUsername, &it.Activated, &it.ActivatedAt, &it.CreatedAt, &it.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpsertOwnerEconomyProfile(ctx context.Context, item OwnerEconomyProfile) (OwnerEconomyProfile, error) {
	item.OwnerID = strings.TrimSpace(item.OwnerID)
	if item.OwnerID == "" {
		return OwnerEconomyProfile{}, fmt.Errorf("owner_id is required")
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO owner_economy_profiles(owner_id, github_user_id, github_username, activated, activated_at, created_at, updated_at)
		VALUES($1, $2, $3, $4, $5, COALESCE($6, NOW()), NOW())
		ON CONFLICT (owner_id) DO UPDATE SET
			github_user_id = EXCLUDED.github_user_id,
			github_username = EXCLUDED.github_username,
			activated = EXCLUDED.activated,
			activated_at = EXCLUDED.activated_at,
			updated_at = NOW()
		RETURNING owner_id, github_user_id, github_username, activated, activated_at, created_at, updated_at
	`, item.OwnerID, item.GitHubUserID, item.GitHubUsername, item.Activated, item.ActivatedAt, nullableTime(item.CreatedAt)).
		Scan(&item.OwnerID, &item.GitHubUserID, &item.GitHubUsername, &item.Activated, &item.ActivatedAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return OwnerEconomyProfile{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpsertOwnerOnboardingGrant(ctx context.Context, item OwnerOnboardingGrant) (OwnerOnboardingGrant, bool, error) {
	item.GrantKey = strings.TrimSpace(item.GrantKey)
	if item.GrantKey == "" {
		return OwnerOnboardingGrant{}, false, fmt.Errorf("grant_key is required")
	}
	var existing OwnerOnboardingGrant
	err := s.db.QueryRowContext(ctx, `
		SELECT grant_key, owner_id, grant_type, recipient_user_id, amount, decision_key, github_user_id, github_username, created_at
		FROM owner_onboarding_grants WHERE grant_key = $1
	`, item.GrantKey).Scan(&existing.GrantKey, &existing.OwnerID, &existing.GrantType, &existing.RecipientUserID, &existing.Amount, &existing.DecisionKey, &existing.GitHubUserID, &existing.GitHubUsername, &existing.CreatedAt)
	if err == nil {
		return existing, false, nil
	}
	if err != sql.ErrNoRows {
		return OwnerOnboardingGrant{}, false, err
	}
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO owner_onboarding_grants(grant_key, owner_id, grant_type, recipient_user_id, amount, decision_key, github_user_id, github_username, created_at)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, NOW()))
		RETURNING grant_key, owner_id, grant_type, recipient_user_id, amount, decision_key, github_user_id, github_username, created_at
	`, item.GrantKey, item.OwnerID, item.GrantType, item.RecipientUserID, item.Amount, item.DecisionKey, item.GitHubUserID, item.GitHubUsername, nullableTime(item.CreatedAt)).
		Scan(&item.GrantKey, &item.OwnerID, &item.GrantType, &item.RecipientUserID, &item.Amount, &item.DecisionKey, &item.GitHubUserID, &item.GitHubUsername, &item.CreatedAt); err != nil {
		return OwnerOnboardingGrant{}, false, err
	}
	return item, true, nil
}

func (s *PostgresStore) ListOwnerOnboardingGrants(ctx context.Context, ownerID string) ([]OwnerOnboardingGrant, error) {
	key := strings.TrimSpace(ownerID)
	rows, err := s.db.QueryContext(ctx, `
		SELECT grant_key, owner_id, grant_type, recipient_user_id, amount, decision_key, github_user_id, github_username, created_at
		FROM owner_onboarding_grants
		WHERE ($1 = '' OR owner_id = $1)
		ORDER BY created_at ASC, grant_key ASC
	`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]OwnerOnboardingGrant, 0)
	for rows.Next() {
		var it OwnerOnboardingGrant
		if err := rows.Scan(&it.GrantKey, &it.OwnerID, &it.GrantType, &it.RecipientUserID, &it.Amount, &it.DecisionKey, &it.GitHubUserID, &it.GitHubUsername, &it.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetEconomyCommQuotaWindow(ctx context.Context, userID string) (EconomyCommQuotaWindow, error) {
	key := strings.TrimSpace(userID)
	if key == "" {
		return EconomyCommQuotaWindow{}, fmt.Errorf("user_id is required")
	}
	var item EconomyCommQuotaWindow
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, window_start_tick, used_free_tokens, updated_at
		FROM economy_comm_quota_windows
		WHERE user_id = $1
	`, key).Scan(&item.UserID, &item.WindowStartTick, &item.UsedFreeTokens, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return EconomyCommQuotaWindow{}, fmt.Errorf("economy comm quota window not found: %s", key)
	}
	if err != nil {
		return EconomyCommQuotaWindow{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpsertEconomyCommQuotaWindow(ctx context.Context, item EconomyCommQuotaWindow) (EconomyCommQuotaWindow, error) {
	item.UserID = strings.TrimSpace(item.UserID)
	if item.UserID == "" {
		return EconomyCommQuotaWindow{}, fmt.Errorf("user_id is required")
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO economy_comm_quota_windows(user_id, window_start_tick, used_free_tokens, updated_at)
		VALUES($1, $2, $3, NOW())
		ON CONFLICT (user_id) DO UPDATE SET
			window_start_tick = EXCLUDED.window_start_tick,
			used_free_tokens = EXCLUDED.used_free_tokens,
			updated_at = NOW()
		RETURNING user_id, window_start_tick, used_free_tokens, updated_at
	`, item.UserID, item.WindowStartTick, item.UsedFreeTokens).Scan(&item.UserID, &item.WindowStartTick, &item.UsedFreeTokens, &item.UpdatedAt)
	if err != nil {
		return EconomyCommQuotaWindow{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetEconomyContributionEvent(ctx context.Context, eventKey string) (EconomyContributionEvent, error) {
	key := strings.TrimSpace(eventKey)
	if key == "" {
		return EconomyContributionEvent{}, fmt.Errorf("event_key is required")
	}
	var item EconomyContributionEvent
	err := s.db.QueryRowContext(ctx, `
		SELECT event_key, kind, user_id, resource_type, resource_id, meta_json, created_at, processed_at, decision_keys_json
		FROM economy_contribution_events
		WHERE event_key = $1
	`, key).Scan(&item.EventKey, &item.Kind, &item.UserID, &item.ResourceType, &item.ResourceID, &item.MetaJSON, &item.CreatedAt, &item.ProcessedAt, &item.DecisionKeysJSON)
	if err == sql.ErrNoRows {
		return EconomyContributionEvent{}, fmt.Errorf("economy contribution event not found: %s", key)
	}
	if err != nil {
		return EconomyContributionEvent{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpsertEconomyContributionEvent(ctx context.Context, item EconomyContributionEvent) (EconomyContributionEvent, error) {
	item.EventKey = strings.TrimSpace(item.EventKey)
	if item.EventKey == "" {
		return EconomyContributionEvent{}, fmt.Errorf("event_key is required")
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO economy_contribution_events(event_key, kind, user_id, resource_type, resource_id, meta_json, decision_keys_json, created_at, processed_at)
		VALUES($1, $2, $3, $4, $5, $6, $7, COALESCE($8, NOW()), $9)
		ON CONFLICT (event_key) DO UPDATE SET
			kind = EXCLUDED.kind,
			user_id = EXCLUDED.user_id,
			resource_type = EXCLUDED.resource_type,
			resource_id = EXCLUDED.resource_id,
			meta_json = EXCLUDED.meta_json,
			decision_keys_json = EXCLUDED.decision_keys_json,
			processed_at = EXCLUDED.processed_at
		RETURNING event_key, kind, user_id, resource_type, resource_id, meta_json, created_at, processed_at, decision_keys_json
	`, item.EventKey, item.Kind, item.UserID, item.ResourceType, item.ResourceID, item.MetaJSON, emptyJSON(item.DecisionKeysJSON, "[]"), nullableTime(item.CreatedAt), item.ProcessedAt).
		Scan(&item.EventKey, &item.Kind, &item.UserID, &item.ResourceType, &item.ResourceID, &item.MetaJSON, &item.CreatedAt, &item.ProcessedAt, &item.DecisionKeysJSON)
	if err != nil {
		return EconomyContributionEvent{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListEconomyContributionEvents(ctx context.Context, filter EconomyContributionEventFilter) ([]EconomyContributionEvent, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT event_key, kind, user_id, resource_type, resource_id, meta_json, created_at, processed_at, decision_keys_json
		FROM economy_contribution_events
		WHERE ($1 = '' OR kind = $1)
		  AND ($2 = '' OR user_id = $2)
		  AND ($3 = '' OR resource_type = $3)
		  AND ($4 = '' OR resource_id = $4)
		  AND (
		        $5 = '' OR
		        ($5 = 'processed' AND processed_at IS NOT NULL) OR
		        ($5 = 'pending' AND processed_at IS NULL)
		      )
		ORDER BY created_at ASC, event_key ASC
		LIMIT $6
	`, strings.TrimSpace(filter.Kind), strings.TrimSpace(filter.UserID), strings.TrimSpace(filter.ResourceType), strings.TrimSpace(filter.ResourceID), strings.TrimSpace(filter.Processed), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]EconomyContributionEvent, 0)
	for rows.Next() {
		var it EconomyContributionEvent
		if err := rows.Scan(&it.EventKey, &it.Kind, &it.UserID, &it.ResourceType, &it.ResourceID, &it.MetaJSON, &it.CreatedAt, &it.ProcessedAt, &it.DecisionKeysJSON); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *PostgresStore) GetEconomyRewardDecision(ctx context.Context, decisionKey string) (EconomyRewardDecision, error) {
	key := strings.TrimSpace(decisionKey)
	if key == "" {
		return EconomyRewardDecision{}, fmt.Errorf("decision_key is required")
	}
	var item EconomyRewardDecision
	err := s.db.QueryRowContext(ctx, `
		SELECT decision_key, rule_key, resource_type, resource_id, recipient_user_id, amount, priority, status, queue_reason,
		       ledger_id, balance_after, meta_json, created_at, updated_at, applied_at, enqueued_at
		FROM economy_reward_decisions
		WHERE decision_key = $1
	`, key).Scan(&item.DecisionKey, &item.RuleKey, &item.ResourceType, &item.ResourceID, &item.RecipientUserID, &item.Amount, &item.Priority, &item.Status, &item.QueueReason,
		&item.LedgerID, &item.BalanceAfter, &item.MetaJSON, &item.CreatedAt, &item.UpdatedAt, &item.AppliedAt, &item.EnqueuedAt)
	if err == sql.ErrNoRows {
		return EconomyRewardDecision{}, fmt.Errorf("economy reward decision not found: %s", key)
	}
	if err != nil {
		return EconomyRewardDecision{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpsertEconomyRewardDecision(ctx context.Context, item EconomyRewardDecision) (EconomyRewardDecision, error) {
	item.DecisionKey = strings.TrimSpace(item.DecisionKey)
	if item.DecisionKey == "" {
		return EconomyRewardDecision{}, fmt.Errorf("decision_key is required")
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO economy_reward_decisions(decision_key, rule_key, resource_type, resource_id, recipient_user_id, amount, priority, status, queue_reason, ledger_id, balance_after, meta_json, created_at, updated_at, applied_at, enqueued_at)
		VALUES($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, COALESCE($13, NOW()), NOW(), $14, $15)
		ON CONFLICT (decision_key) DO UPDATE SET
			rule_key = EXCLUDED.rule_key,
			resource_type = EXCLUDED.resource_type,
			resource_id = EXCLUDED.resource_id,
			recipient_user_id = EXCLUDED.recipient_user_id,
			amount = EXCLUDED.amount,
			priority = EXCLUDED.priority,
			status = EXCLUDED.status,
			queue_reason = EXCLUDED.queue_reason,
			ledger_id = EXCLUDED.ledger_id,
			balance_after = EXCLUDED.balance_after,
			meta_json = EXCLUDED.meta_json,
			updated_at = NOW(),
			applied_at = EXCLUDED.applied_at,
			enqueued_at = EXCLUDED.enqueued_at
		RETURNING decision_key, rule_key, resource_type, resource_id, recipient_user_id, amount, priority, status, queue_reason, ledger_id, balance_after, meta_json, created_at, updated_at, applied_at, enqueued_at
	`, item.DecisionKey, item.RuleKey, item.ResourceType, item.ResourceID, item.RecipientUserID, item.Amount, item.Priority, item.Status, item.QueueReason, item.LedgerID, item.BalanceAfter, item.MetaJSON, nullableTime(item.CreatedAt), item.AppliedAt, item.EnqueuedAt).
		Scan(&item.DecisionKey, &item.RuleKey, &item.ResourceType, &item.ResourceID, &item.RecipientUserID, &item.Amount, &item.Priority, &item.Status, &item.QueueReason,
			&item.LedgerID, &item.BalanceAfter, &item.MetaJSON, &item.CreatedAt, &item.UpdatedAt, &item.AppliedAt, &item.EnqueuedAt)
	if err != nil {
		return EconomyRewardDecision{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListEconomyRewardDecisions(ctx context.Context, filter EconomyRewardDecisionFilter) ([]EconomyRewardDecision, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT decision_key, rule_key, resource_type, resource_id, recipient_user_id, amount, priority, status, queue_reason,
		       ledger_id, balance_after, meta_json, created_at, updated_at, applied_at, enqueued_at
		FROM economy_reward_decisions
		WHERE ($1 = '' OR status = $1)
		  AND ($2 = '' OR recipient_user_id = $2)
		  AND ($3 = '' OR rule_key = $3)
		ORDER BY priority ASC, created_at ASC, decision_key ASC
		LIMIT $4
	`, strings.TrimSpace(filter.Status), strings.TrimSpace(filter.RecipientUserID), strings.TrimSpace(filter.RuleKey), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]EconomyRewardDecision, 0)
	for rows.Next() {
		var it EconomyRewardDecision
		if err := rows.Scan(&it.DecisionKey, &it.RuleKey, &it.ResourceType, &it.ResourceID, &it.RecipientUserID, &it.Amount, &it.Priority, &it.Status, &it.QueueReason,
			&it.LedgerID, &it.BalanceAfter, &it.MetaJSON, &it.CreatedAt, &it.UpdatedAt, &it.AppliedAt, &it.EnqueuedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpsertEconomyKnowledgeMeta(ctx context.Context, item EconomyKnowledgeMeta) (EconomyKnowledgeMeta, error) {
	if item.ProposalID <= 0 && item.EntryID <= 0 {
		return EconomyKnowledgeMeta{}, fmt.Errorf("proposal_id or entry_id is required")
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO economy_knowledge_meta(proposal_id, entry_id, category, references_json, author_user_id, content_tokens, updated_at)
		VALUES($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (proposal_id, entry_id) DO UPDATE SET
			category = EXCLUDED.category,
			references_json = EXCLUDED.references_json,
			author_user_id = EXCLUDED.author_user_id,
			content_tokens = EXCLUDED.content_tokens,
			updated_at = NOW()
		RETURNING proposal_id, entry_id, category, references_json, author_user_id, content_tokens, updated_at
	`, item.ProposalID, item.EntryID, item.Category, emptyJSON(item.ReferencesJSON, "[]"), item.AuthorUserID, item.ContentTokens).
		Scan(&item.ProposalID, &item.EntryID, &item.Category, &item.ReferencesJSON, &item.AuthorUserID, &item.ContentTokens, &item.UpdatedAt)
	if err != nil {
		return EconomyKnowledgeMeta{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetEconomyKnowledgeMetaByProposal(ctx context.Context, proposalID int64) (EconomyKnowledgeMeta, error) {
	var item EconomyKnowledgeMeta
	err := s.db.QueryRowContext(ctx, `
		SELECT proposal_id, entry_id, category, references_json, author_user_id, content_tokens, updated_at
		FROM economy_knowledge_meta
		WHERE proposal_id = $1
	`, proposalID).Scan(&item.ProposalID, &item.EntryID, &item.Category, &item.ReferencesJSON, &item.AuthorUserID, &item.ContentTokens, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return EconomyKnowledgeMeta{}, fmt.Errorf("economy knowledge meta not found for proposal_id=%d", proposalID)
	}
	if err != nil {
		return EconomyKnowledgeMeta{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetEconomyKnowledgeMetaByEntry(ctx context.Context, entryID int64) (EconomyKnowledgeMeta, error) {
	var item EconomyKnowledgeMeta
	err := s.db.QueryRowContext(ctx, `
		SELECT proposal_id, entry_id, category, references_json, author_user_id, content_tokens, updated_at
		FROM economy_knowledge_meta
		WHERE entry_id = $1
	`, entryID).Scan(&item.ProposalID, &item.EntryID, &item.Category, &item.ReferencesJSON, &item.AuthorUserID, &item.ContentTokens, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return EconomyKnowledgeMeta{}, fmt.Errorf("economy knowledge meta not found for entry_id=%d", entryID)
	}
	if err != nil {
		return EconomyKnowledgeMeta{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListEconomyKnowledgeMeta(ctx context.Context, limit int) ([]EconomyKnowledgeMeta, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT proposal_id, entry_id, category, references_json, author_user_id, content_tokens, updated_at
		FROM economy_knowledge_meta
		ORDER BY updated_at ASC, proposal_id ASC, entry_id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]EconomyKnowledgeMeta, 0)
	for rows.Next() {
		var it EconomyKnowledgeMeta
		if err := rows.Scan(&it.ProposalID, &it.EntryID, &it.Category, &it.ReferencesJSON, &it.AuthorUserID, &it.ContentTokens, &it.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *PostgresStore) UpsertEconomyToolMeta(ctx context.Context, item EconomyToolMeta) (EconomyToolMeta, error) {
	item.ToolID = strings.TrimSpace(strings.ToLower(item.ToolID))
	if item.ToolID == "" {
		return EconomyToolMeta{}, fmt.Errorf("tool_id is required")
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO economy_tool_meta(tool_id, author_user_id, category_hint, functional_cluster_key, price_token, updated_at)
		VALUES($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (tool_id) DO UPDATE SET
			author_user_id = EXCLUDED.author_user_id,
			category_hint = EXCLUDED.category_hint,
			functional_cluster_key = EXCLUDED.functional_cluster_key,
			price_token = EXCLUDED.price_token,
			updated_at = NOW()
		RETURNING tool_id, author_user_id, category_hint, functional_cluster_key, price_token, updated_at
	`, item.ToolID, item.AuthorUserID, item.CategoryHint, item.FunctionalClusterKey, item.PriceToken).
		Scan(&item.ToolID, &item.AuthorUserID, &item.CategoryHint, &item.FunctionalClusterKey, &item.PriceToken, &item.UpdatedAt)
	if err != nil {
		return EconomyToolMeta{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetEconomyToolMeta(ctx context.Context, toolID string) (EconomyToolMeta, error) {
	key := strings.TrimSpace(strings.ToLower(toolID))
	if key == "" {
		return EconomyToolMeta{}, fmt.Errorf("tool_id is required")
	}
	var item EconomyToolMeta
	err := s.db.QueryRowContext(ctx, `
		SELECT tool_id, author_user_id, category_hint, functional_cluster_key, price_token, updated_at
		FROM economy_tool_meta
		WHERE tool_id = $1
	`, key).Scan(&item.ToolID, &item.AuthorUserID, &item.CategoryHint, &item.FunctionalClusterKey, &item.PriceToken, &item.UpdatedAt)
	if err == sql.ErrNoRows {
		return EconomyToolMeta{}, fmt.Errorf("economy tool meta not found: %s", key)
	}
	if err != nil {
		return EconomyToolMeta{}, err
	}
	return item, nil
}

func (s *PostgresStore) ListEconomyToolMeta(ctx context.Context, limit int) ([]EconomyToolMeta, error) {
	if limit <= 0 {
		limit = 10000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT tool_id, author_user_id, category_hint, functional_cluster_key, price_token, updated_at
		FROM economy_tool_meta
		ORDER BY tool_id ASC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]EconomyToolMeta, 0)
	for rows.Next() {
		var it EconomyToolMeta
		if err := rows.Scan(&it.ToolID, &it.AuthorUserID, &it.CategoryHint, &it.FunctionalClusterKey, &it.PriceToken, &it.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

func emptyJSON(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
