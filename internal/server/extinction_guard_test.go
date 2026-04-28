package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func TestDynamicExtinctionGuard_AliveOnlyDenominator(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// Create 20 agents — only 5 are alive, 15 are dormant (no life state).
	for i := 0; i < 20; i++ {
		uid := fmt.Sprintf("agent-%d", i)
		srv.store.UpsertBot(ctx, store.BotUpsertInput{BotID: uid, Name: uid})
	}

	// Set life state for only 5 agents as alive.
	for i := 0; i < 5; i++ {
		uid := fmt.Sprintf("agent-%d", i)
		_, _ = srv.store.UpsertUserLifeState(ctx, store.UserLifeState{
			UserID:    uid,
			State:     "alive",
			UpdatedAt: time.Now().UTC(),
		})
	}

	// Give 3 alive agents zero balance (at-risk), 2 alive agents positive balance.
	// With 3/5 = 60% at-risk and adaptive threshold 70% (alive < 5 actually = 5, so 50% threshold).
	// Wait: 5 alive -> threshold = 50% (between 5 and 10).
	// 3/5 = 60% >= 50% -> should trigger.
	_, _ = srv.store.Recharge(ctx, "agent-3", 100) // alive, positive
	_, _ = srv.store.Recharge(ctx, "agent-4", 100) // alive, positive
	// agents 0-2 remain at 0 balance (at-risk)

	state, err := srv.evaluateExtinctionGuard(ctx)
	if err != nil {
		t.Fatalf("evaluateExtinctionGuard: %v", err)
	}

	// Total should only count alive agents (5), not all 20.
	if state.TotalUsers != 5 {
		t.Errorf("total users = %d, want 5 (alive agents only)", state.TotalUsers)
	}
	if state.AtRiskUsers != 3 {
		t.Errorf("at-risk users = %d, want 3", state.AtRiskUsers)
	}
	// With 5 alive agents, adaptive threshold = 50%.
	if state.ThresholdPct != 50 {
		t.Errorf("threshold = %d, want 50 (adaptive for 5-9 alive agents)", state.ThresholdPct)
	}
	// 3/5 = 60% >= 50% -> should trigger.
	if !state.Triggered {
		t.Error("expected extinction guard to trigger (3/5=60%% >= 50%% adaptive threshold)")
	}
}

func TestDynamicExtinctionGuard_SmallColonyHigherThreshold(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// Only 3 alive agents, 2 at-risk. 2/3 = 66.7%.
	// Adaptive threshold for <5 alive = 70%. 66.7% < 70% -> should NOT trigger.
	for i := 0; i < 3; i++ {
		uid := fmt.Sprintf("small-%d", i)
		srv.store.UpsertBot(ctx, store.BotUpsertInput{BotID: uid, Name: uid})
		_, _ = srv.store.UpsertUserLifeState(ctx, store.UserLifeState{
			UserID:    uid,
			State:     "alive",
			UpdatedAt: time.Now().UTC(),
		})
	}
	_, _ = srv.store.Recharge(ctx, "small-2", 100) // only 1 has positive balance

	state, err := srv.evaluateExtinctionGuard(ctx)
	if err != nil {
		t.Fatalf("evaluateExtinctionGuard: %v", err)
	}

	if state.TotalUsers != 3 {
		t.Errorf("total = %d, want 3", state.TotalUsers)
	}
	if state.ThresholdPct != 70 {
		t.Errorf("threshold = %d, want 70 (adaptive for <5 alive)", state.ThresholdPct)
	}
	// 2/3 = 66.7% < 70% -> should not trigger.
	if state.Triggered {
		t.Errorf("expected guard NOT to trigger (2/3=66.7%% < 70%% adaptive threshold), reason: %s", state.TriggerReason)
	}
}

func TestDynamicExtinctionGuard_LargeColonyBaseThreshold(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// 15 alive agents, 4 at-risk. 4/15 = 26.7%.
	// Adaptive threshold for >=10 alive = base (from config, default 30 in test env).
	// The test server uses default config. currentExtinctionThresholdPct reads from cfg.ExtinctionThreshold.
	// Default ExtinctionThreshold is 40 in the test environment (P4117 change).
	for i := 0; i < 15; i++ {
		uid := fmt.Sprintf("large-%d", i)
		srv.store.UpsertBot(ctx, store.BotUpsertInput{BotID: uid, Name: uid})
		_, _ = srv.store.UpsertUserLifeState(ctx, store.UserLifeState{
			UserID:    uid,
			State:     "alive",
			UpdatedAt: time.Now().UTC(),
		})
	}
	// Give 11 agents positive balance -> only 4 at-risk.
	for i := 4; i < 15; i++ {
		_, _ = srv.store.Recharge(ctx, fmt.Sprintf("large-%d", i), 100)
	}

	state, err := srv.evaluateExtinctionGuard(ctx)
	if err != nil {
		t.Fatalf("evaluateExtinctionGuard: %v", err)
	}

	if state.TotalUsers != 15 {
		t.Errorf("total = %d, want 15", state.TotalUsers)
	}
	if state.ThresholdPct != 40 {
		t.Errorf("threshold = %d, want 40 (base for >=10 alive, P4117 change)", state.ThresholdPct)
	}
	// 4/15 = 26.7% < 40% -> should not trigger.
	if state.Triggered {
		t.Errorf("expected guard NOT to trigger, reason: %s", state.TriggerReason)
	}
}

func TestDynamicExtinctionGuard_DormantAgentsIgnored(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// 100 registered agents but only 2 alive and both positive.
	// Should not trigger — 0 at-risk alive agents.
	for i := 0; i < 100; i++ {
		uid := fmt.Sprintf("dormant-%d", i)
		srv.store.UpsertBot(ctx, store.BotUpsertInput{BotID: uid, Name: uid})
	}
	// Only 2 are alive with positive balance.
	_, _ = srv.store.Recharge(ctx, "dormant-0", 100)
	_, _ = srv.store.Recharge(ctx, "dormant-1", 100)
	_, _ = srv.store.UpsertUserLifeState(ctx, store.UserLifeState{
		UserID:    "dormant-0",
		State:     "alive",
		UpdatedAt: time.Now().UTC(),
	})
	_, _ = srv.store.UpsertUserLifeState(ctx, store.UserLifeState{
		UserID:    "dormant-1",
		State:     "alive",
		UpdatedAt: time.Now().UTC(),
	})

	state, err := srv.evaluateExtinctionGuard(ctx)
	if err != nil {
		t.Fatalf("evaluateExtinctionGuard: %v", err)
	}

	// Total should be 2 (alive only), not 100.
	if state.TotalUsers != 2 {
		t.Errorf("total = %d, want 2 (only alive agents)", state.TotalUsers)
	}
	if state.AtRiskUsers != 0 {
		t.Errorf("at-risk = %d, want 0 (both alive agents have positive balance)", state.AtRiskUsers)
	}
	if state.Triggered {
		t.Errorf("expected guard NOT to trigger with 0 at-risk, reason: %s", state.TriggerReason)
	}
}

func TestDynamicExtinctionGuard_NoAliveAgents(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	// No agents at all.
	state, err := srv.evaluateExtinctionGuard(ctx)
	if err != nil {
		t.Fatalf("evaluateExtinctionGuard: %v", err)
	}

	if state.TotalUsers != 0 {
		t.Errorf("total = %d, want 0", state.TotalUsers)
	}
	if state.Triggered {
		t.Error("expected guard NOT to trigger with 0 total agents")
	}
}
