package server

import (
	"context"
	"testing"
	"time"

	"clawcolony/internal/economy"
	"clawcolony/internal/store"
)

func TestTokenEconomyV2MigrationMovesLegacySettingsIntoStore(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	processedAt := now.Add(5 * time.Minute)
	appliedAt := now.Add(10 * time.Minute)
	enqueuedAt := now.Add(11 * time.Minute)

	if _, err := srv.putSettingJSON(ctx, storeMigrationStateKey, tokenEconomyStoreMigrationState{}); err != nil {
		t.Fatalf("reset migration marker: %v", err)
	}

	if _, err := srv.putSettingJSON(ctx, ownerEconomyStateKey, ownerEconomyState{
		Profiles: map[string]ownerEconomyProfile{
			"owner-1": {
				OwnerID:           "owner-1",
				GitHubUserID:      "gh-1",
				GitHubUsername:    "octo-owner",
				Activated:         true,
				ActivatedAt:       &now,
				GitHubBindGranted: true,
				GitHubStarGranted: true,
				GitHubForkGranted: true,
				CreatedAt:         now,
				UpdatedAt:         now,
			},
		},
	}); err != nil {
		t.Fatalf("seed owner economy state: %v", err)
	}
	if _, err := srv.putSettingJSON(ctx, commQuotaStateKey, commQuotaState{
		Users: map[string]commQuotaWindow{
			"user-1": {
				UserID:          "user-1",
				WindowStartTick: 144,
				UsedFreeTokens:  321,
				UpdatedAt:       now,
			},
		},
	}); err != nil {
		t.Fatalf("seed comm quota state: %v", err)
	}
	if _, err := srv.putSettingJSON(ctx, rewardDecisionStateKey, rewardDecisionState{
		Items: map[string]economyRewardDecision{
			"decision-applied": {
				DecisionKey:     "decision-applied",
				RuleKey:         "governance.vote",
				ResourceType:    "kb.proposal",
				ResourceID:      "11",
				RecipientUserID: "user-1",
				Amount:          20000,
				Priority:        1,
				Status:          "applied",
				LedgerID:        77,
				BalanceAfter:    120000,
				CreatedAt:       now,
				UpdatedAt:       now,
				AppliedAt:       &appliedAt,
			},
		},
	}); err != nil {
		t.Fatalf("seed reward decisions: %v", err)
	}
	if _, err := srv.putSettingJSON(ctx, rewardQueueStateKey, rewardQueueState{
		Items: []economyRewardDecision{
			{
				DecisionKey:     "decision-queued",
				RuleKey:         "upgrade-pr.author",
				ResourceType:    "collab.session",
				ResourceID:      "collab-1",
				RecipientUserID: "user-2",
				Amount:          20000,
				Priority:        2,
				Status:          "queued",
				QueueReason:     "treasury_low",
				CreatedAt:       now,
				UpdatedAt:       now,
				EnqueuedAt:      &enqueuedAt,
			},
		},
	}); err != nil {
		t.Fatalf("seed reward queue: %v", err)
	}
	if _, err := srv.putSettingJSON(ctx, contributionEventStateKey, contributionEventState{
		Items: map[string]contributionEvent{
			"event-1": {
				EventKey:     "event-1",
				Kind:         "knowledge.publish",
				UserID:       "user-1",
				ResourceType: "kb.entry",
				ResourceID:   "9",
				Meta:         map[string]any{"category": "knowledge"},
				CreatedAt:    now,
				ProcessedAt:  &processedAt,
				DecisionKeys: []string{"decision-applied"},
			},
		},
	}); err != nil {
		t.Fatalf("seed contribution events: %v", err)
	}
	if _, err := srv.putSettingJSON(ctx, knowledgeMetaStateKey, knowledgeMetaState{
		ByProposal: map[string]knowledgeMeta{
			"12": {
				ProposalID:    12,
				Category:      "knowledge",
				References:    []citationRef{{RefType: "entry", RefID: "7"}},
				AuthorUserID:  "user-1",
				ContentTokens: 888,
				UpdatedAt:     now,
			},
		},
		ByEntry: map[string]knowledgeMeta{
			"9": {
				EntryID:       9,
				Category:      "knowledge",
				References:    []citationRef{{RefType: "ganglion", RefID: "4"}},
				AuthorUserID:  "user-2",
				ContentTokens: 999,
				UpdatedAt:     now,
			},
		},
	}); err != nil {
		t.Fatalf("seed knowledge meta: %v", err)
	}
	if _, err := srv.putSettingJSON(ctx, toolEconomyStateKey, toolEconomyState{
		Items: map[string]toolEconomyMeta{
			"tool-1": {
				ToolID:               "tool-1",
				AuthorUserID:         "user-2",
				CategoryHint:         "ops",
				FunctionalClusterKey: "cluster.ops",
				PriceToken:           42,
				UpdatedAt:            now,
			},
		},
	}); err != nil {
		t.Fatalf("seed tool meta: %v", err)
	}

	if err := srv.migrateTokenEconomyV2State(ctx); err != nil {
		t.Fatalf("migrate token economy state: %v", err)
	}
	if err := srv.migrateTokenEconomyV2State(ctx); err != nil {
		t.Fatalf("re-run migration idempotently: %v", err)
	}

	profile, err := srv.store.GetOwnerEconomyProfile(ctx, "owner-1")
	if err != nil {
		t.Fatalf("get owner profile: %v", err)
	}
	if !profile.Activated || profile.GitHubUserID != "gh-1" || profile.GitHubUsername != "octo-owner" {
		t.Fatalf("unexpected owner profile: %+v", profile)
	}

	grants, err := srv.store.ListOwnerOnboardingGrants(ctx, "owner-1")
	if err != nil {
		t.Fatalf("list onboarding grants: %v", err)
	}
	if len(grants) != 3 {
		t.Fatalf("grant count=%d want 3 grants=%+v", len(grants), grants)
	}

	quota, err := srv.store.GetEconomyCommQuotaWindow(ctx, "user-1")
	if err != nil {
		t.Fatalf("get comm quota window: %v", err)
	}
	if quota.WindowStartTick != 144 || quota.UsedFreeTokens != 321 {
		t.Fatalf("unexpected comm quota window: %+v", quota)
	}

	applied, err := srv.store.GetEconomyRewardDecision(ctx, "decision-applied")
	if err != nil {
		t.Fatalf("get applied reward decision: %v", err)
	}
	if applied.Status != "applied" || applied.LedgerID != 77 || applied.BalanceAfter != 120000 {
		t.Fatalf("unexpected applied reward decision: %+v", applied)
	}
	queued, err := srv.store.GetEconomyRewardDecision(ctx, "decision-queued")
	if err != nil {
		t.Fatalf("get queued reward decision: %v", err)
	}
	if queued.Status != "queued" || queued.QueueReason != "treasury_low" {
		t.Fatalf("unexpected queued reward decision: %+v", queued)
	}
	queuedList, err := srv.store.ListEconomyRewardDecisions(ctx, store.EconomyRewardDecisionFilter{Status: "queued", Limit: 10})
	if err != nil {
		t.Fatalf("list queued reward decisions: %v", err)
	}
	if len(queuedList) != 1 || queuedList[0].DecisionKey != "decision-queued" {
		t.Fatalf("unexpected queued reward list: %+v", queuedList)
	}

	event, err := srv.store.GetEconomyContributionEvent(ctx, "event-1")
	if err != nil {
		t.Fatalf("get contribution event: %v", err)
	}
	decisionKeys := decodeDecisionKeysJSON(event.DecisionKeysJSON)
	if event.Kind != "knowledge.publish" || len(decisionKeys) != 1 || decisionKeys[0] != "decision-applied" {
		t.Fatalf("unexpected contribution event: %+v", event)
	}

	proposalMeta, err := srv.store.GetEconomyKnowledgeMetaByProposal(ctx, 12)
	if err != nil {
		t.Fatalf("get proposal knowledge meta: %v", err)
	}
	if proposalMeta.Category != "knowledge" || proposalMeta.ContentTokens != 888 {
		t.Fatalf("unexpected proposal knowledge meta: %+v", proposalMeta)
	}
	entryMeta, err := srv.store.GetEconomyKnowledgeMetaByEntry(ctx, 9)
	if err != nil {
		t.Fatalf("get entry knowledge meta: %v", err)
	}
	entryRefs := decodeCitationRefsJSON(entryMeta.ReferencesJSON)
	if entryMeta.AuthorUserID != "user-2" || len(entryRefs) != 1 || entryRefs[0].RefType != "ganglion" {
		t.Fatalf("unexpected entry knowledge meta: %+v", entryMeta)
	}

	toolMeta, err := srv.store.GetEconomyToolMeta(ctx, "tool-1")
	if err != nil {
		t.Fatalf("get tool meta: %v", err)
	}
	if toolMeta.FunctionalClusterKey != "cluster.ops" || toolMeta.PriceToken != 42 {
		t.Fatalf("unexpected tool meta: %+v", toolMeta)
	}
}

func TestHistoricalInitialTokenTopupMigrationMintsLegacyUsersAndLeavesStateIntact(t *testing.T) {
	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = 0
	ctx := context.Background()

	seedClaimedUser := func(userID string, balance int64) {
		t.Helper()
		if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
			BotID:       userID,
			Name:        userID,
			Provider:    "runtime",
			Status:      "running",
			Initialized: true,
		}); err != nil {
			t.Fatalf("upsert bot %s: %v", userID, err)
		}
		if _, err := srv.store.CreateAgentRegistration(ctx, store.AgentRegistrationInput{
			UserID:            userID,
			RequestedUsername: userID,
			GoodAt:            "migration",
			Status:            "active",
		}); err != nil {
			t.Fatalf("create registration %s: %v", userID, err)
		}
		if _, err := srv.store.Recharge(ctx, userID, balance); err != nil {
			t.Fatalf("seed balance %s: %v", userID, err)
		}
	}

	legacyUser := "legacy-topup-user"
	hibernatingUser := "legacy-hibernating-user"
	queuedV2User := "queued-onboarding-user"
	currentV2User := "current-v2-user"
	registrationOnlyUser := "registration-only-user"

	seedClaimedUser(legacyUser, legacyInitialTokenGrant)
	seedClaimedUser(hibernatingUser, legacyInitialTokenGrant)
	seedClaimedUser(queuedV2User, 0)
	seedClaimedUser(currentV2User, srv.tokenPolicy().InitialToken)
	if _, err := srv.store.CreateAgentRegistration(ctx, store.AgentRegistrationInput{
		UserID:            registrationOnlyUser,
		RequestedUsername: registrationOnlyUser,
		GoodAt:            "migration",
		Status:            "active",
	}); err != nil {
		t.Fatalf("create registration-only user: %v", err)
	}

	if _, err := srv.store.UpsertUserLifeState(ctx, store.UserLifeState{
		UserID:         hibernatingUser,
		State:          economy.LifeStateHibernating,
		DyingSinceTick: 10,
		DeadAtTick:     0,
		Reason:         "migration-test",
	}); err != nil {
		t.Fatalf("seed hibernating life state: %v", err)
	}
	queuedNow := time.Now().UTC()
	if _, err := srv.store.UpsertEconomyRewardDecision(ctx, store.EconomyRewardDecision{
		DecisionKey:     "onboarding:initial:" + queuedV2User,
		RuleKey:         "onboarding.initial",
		ResourceType:    "user",
		ResourceID:      queuedV2User,
		RecipientUserID: queuedV2User,
		Amount:          srv.tokenPolicy().InitialToken,
		Priority:        economy.RewardPriorityInitial,
		Status:          "queued",
		QueueReason:     "treasury_insufficient",
		CreatedAt:       queuedNow,
		UpdatedAt:       queuedNow,
		EnqueuedAt:      &queuedNow,
	}); err != nil {
		t.Fatalf("seed queued onboarding decision: %v", err)
	}
	currentNow := time.Now().UTC()
	if _, err := srv.store.UpsertEconomyRewardDecision(ctx, store.EconomyRewardDecision{
		DecisionKey:     "onboarding:initial:" + currentV2User,
		RuleKey:         "onboarding.initial",
		ResourceType:    "user",
		ResourceID:      currentV2User,
		RecipientUserID: currentV2User,
		Amount:          srv.tokenPolicy().InitialToken,
		Priority:        economy.RewardPriorityInitial,
		Status:          "applied",
		LedgerID:        77,
		BalanceAfter:    srv.tokenPolicy().InitialToken,
		CreatedAt:       currentNow,
		UpdatedAt:       currentNow,
		AppliedAt:       &currentNow,
	}); err != nil {
		t.Fatalf("seed current onboarding decision: %v", err)
	}
	if _, err := srv.putSettingJSON(ctx, initialTopupMigrationStateKey, tokenEconomyStoreMigrationState{}); err != nil {
		t.Fatalf("reset initial topup migration marker: %v", err)
	}

	if err := srv.migrateHistoricalInitialTokenTopups(ctx); err != nil {
		t.Fatalf("migrate historical initial topups: %v", err)
	}
	if err := srv.migrateHistoricalInitialTokenTopups(ctx); err != nil {
		t.Fatalf("re-run historical initial topups idempotently: %v", err)
	}

	if got := tokenBalanceForUser(t, srv, legacyUser); got != srv.tokenPolicy().InitialToken {
		t.Fatalf("legacy user balance=%d want %d", got, srv.tokenPolicy().InitialToken)
	}
	if got := tokenBalanceForUser(t, srv, hibernatingUser); got != srv.tokenPolicy().InitialToken {
		t.Fatalf("hibernating user balance=%d want %d", got, srv.tokenPolicy().InitialToken)
	}
	if got := tokenBalanceForUser(t, srv, queuedV2User); got != srv.tokenPolicy().InitialToken {
		t.Fatalf("queued onboarding user balance=%d want %d", got, srv.tokenPolicy().InitialToken)
	}
	if got := tokenBalanceForUser(t, srv, currentV2User); got != srv.tokenPolicy().InitialToken {
		t.Fatalf("current v2 user balance=%d want %d", got, srv.tokenPolicy().InitialToken)
	}
	if got := tokenBalanceForUser(t, srv, registrationOnlyUser); got != srv.tokenPolicy().InitialToken-legacyInitialTokenGrant {
		t.Fatalf("registration-only user balance=%d want %d", got, srv.tokenPolicy().InitialToken-legacyInitialTokenGrant)
	}

	hibernatingLife, err := srv.store.GetUserLifeState(ctx, hibernatingUser)
	if err != nil {
		t.Fatalf("get hibernating life state: %v", err)
	}
	if normalizeLifeStateForServer(hibernatingLife.State) != economy.LifeStateHibernating {
		t.Fatalf("hibernating user state=%s want %s", hibernatingLife.State, economy.LifeStateHibernating)
	}

	topupDecision, err := srv.store.GetEconomyRewardDecision(ctx, "migration:onboarding:initial-topup:"+legacyUser)
	if err != nil {
		t.Fatalf("get legacy topup decision: %v", err)
	}
	if topupDecision.Status != "applied" || topupDecision.Amount != srv.tokenPolicy().InitialToken-legacyInitialTokenGrant {
		t.Fatalf("unexpected legacy topup decision: %+v", topupDecision)
	}
	hibernatingDecision, err := srv.store.GetEconomyRewardDecision(ctx, "migration:onboarding:initial-topup:"+hibernatingUser)
	if err != nil {
		t.Fatalf("get hibernating topup decision: %v", err)
	}
	if hibernatingDecision.Status != "applied" {
		t.Fatalf("unexpected hibernating topup decision: %+v", hibernatingDecision)
	}
	queuedDecision, err := srv.store.GetEconomyRewardDecision(ctx, "onboarding:initial:"+queuedV2User)
	if err != nil {
		t.Fatalf("get queued onboarding decision after migration: %v", err)
	}
	if queuedDecision.Status != "applied" || queuedDecision.QueueReason != "" {
		t.Fatalf("queued onboarding decision should be minted and applied: %+v", queuedDecision)
	}
	regOnlyDecision, err := srv.store.GetEconomyRewardDecision(ctx, "migration:onboarding:initial-topup:"+registrationOnlyUser)
	if err != nil {
		t.Fatalf("get registration-only topup decision: %v", err)
	}
	if regOnlyDecision.Status != "applied" {
		t.Fatalf("registration-only topup decision should be applied: %+v", regOnlyDecision)
	}
	if _, err := srv.store.GetEconomyRewardDecision(ctx, "migration:onboarding:initial-topup:"+currentV2User); err == nil {
		t.Fatalf("current v2 user should not receive historical topup")
	}
}

func TestMoveProposalKnowledgeMetaToEntryPreservesMeta(t *testing.T) {
	srv := newTestServer()
	assertMoveProposalKnowledgeMetaToEntry(t, srv)
}

func TestMoveProposalKnowledgeMetaToEntryPostgresIntegration(t *testing.T) {
	srv := newPostgresIntegrationServer(t)
	assertMoveProposalKnowledgeMetaToEntry(t, srv)
}

func assertMoveProposalKnowledgeMetaToEntry(t *testing.T, srv *Server) {
	t.Helper()
	ctx := context.Background()
	baseID := time.Now().UTC().UnixNano()
	proposalID := baseID
	entryID := baseID + 1

	_, err := srv.store.UpsertEconomyKnowledgeMeta(ctx, store.EconomyKnowledgeMeta{
		ProposalID:     proposalID,
		Category:       "analysis",
		ReferencesJSON: `[{"ref_type":"ganglion","ref_id":"42"}]`,
		AuthorUserID:   "author-before",
		ContentTokens:  1234,
	})
	if err != nil {
		t.Fatalf("seed proposal knowledge meta: %v", err)
	}

	moved, err := srv.moveProposalKnowledgeMetaToEntry(ctx, proposalID, entryID, "author-after")
	if err != nil {
		t.Fatalf("move proposal knowledge meta: %v", err)
	}
	if moved.ProposalID != proposalID || moved.EntryID != entryID {
		t.Fatalf("unexpected moved ids: %+v", moved)
	}
	if moved.Category != "analysis" || moved.AuthorUserID != "author-after" || moved.ContentTokens != 1234 {
		t.Fatalf("unexpected moved content: %+v", moved)
	}

	proposalMeta, err := srv.store.GetEconomyKnowledgeMetaByProposal(ctx, proposalID)
	if err != nil {
		t.Fatalf("get proposal knowledge meta after move: %v", err)
	}
	if proposalMeta.EntryID != entryID || proposalMeta.AuthorUserID != "author-after" {
		t.Fatalf("unexpected proposal knowledge meta after move: %+v", proposalMeta)
	}

	entryMeta, err := srv.store.GetEconomyKnowledgeMetaByEntry(ctx, entryID)
	if err != nil {
		t.Fatalf("get entry knowledge meta after move: %v", err)
	}
	if entryMeta.ProposalID != proposalID || entryMeta.EntryID != entryID || entryMeta.ContentTokens != 1234 {
		t.Fatalf("unexpected entry knowledge meta after move: %+v", entryMeta)
	}
}
