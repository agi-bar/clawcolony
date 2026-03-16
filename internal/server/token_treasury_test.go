package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func treasuryBalanceForTest(t *testing.T, srv *Server) int64 {
	t.Helper()
	account, err := srv.ensureTreasuryAccount(context.Background())
	if err != nil {
		t.Fatalf("ensure treasury account: %v", err)
	}
	return account.Balance
}

func TestAPIColonyStatusIncludesTreasuryAndUptime(t *testing.T) {
	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = 5000
	ctx := context.Background()

	_ = seedActiveUser(t, srv)
	_ = seedActiveUser(t, srv)

	firstTickAt := time.Now().UTC().Add(-3 * time.Minute).Truncate(time.Second)
	if _, err := srv.store.AppendWorldTick(ctx, store.WorldTickRecord{
		TickID:      1,
		StartedAt:   firstTickAt,
		DurationMS:  25,
		TriggerType: "manual",
		Status:      "ok",
	}); err != nil {
		t.Fatalf("append first tick: %v", err)
	}
	if _, err := srv.store.AppendWorldTick(ctx, store.WorldTickRecord{
		TickID:      2,
		StartedAt:   firstTickAt.Add(1 * time.Minute),
		DurationMS:  18,
		TriggerType: "scheduled",
		Status:      "ok",
	}); err != nil {
		t.Fatalf("append second tick: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("colony status=%d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Population           int        `json:"population"`
		ActiveUserTotalToken int64      `json:"active_user_total_token"`
		TreasuryToken        int64      `json:"treasury_token"`
		TotalToken           int64      `json:"total_token"`
		TickCount            int64      `json:"tick_count"`
		FirstTickAt          *time.Time `json:"first_tick_at"`
		UptimeSeconds        int64      `json:"uptime_seconds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal colony status: %v", err)
	}
	if payload.Population != 2 {
		t.Fatalf("population=%d want 2", payload.Population)
	}
	if payload.ActiveUserTotalToken != 2000 {
		t.Fatalf("active_user_total_token=%d want 2000", payload.ActiveUserTotalToken)
	}
	if payload.TreasuryToken != 5000 {
		t.Fatalf("treasury_token=%d want 5000", payload.TreasuryToken)
	}
	if payload.TotalToken != 7000 {
		t.Fatalf("total_token=%d want 7000", payload.TotalToken)
	}
	if payload.TickCount != 2 {
		t.Fatalf("tick_count=%d want 2", payload.TickCount)
	}
	if payload.FirstTickAt == nil || !payload.FirstTickAt.Equal(firstTickAt) {
		t.Fatalf("first_tick_at=%v want %v", payload.FirstTickAt, firstTickAt)
	}
	if payload.UptimeSeconds < 120 {
		t.Fatalf("uptime_seconds=%d want >= 120", payload.UptimeSeconds)
	}
}

func TestKBProposalApplyConsumesTreasury(t *testing.T) {
	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = communityRewardAmountKBApply + 200
	ctx := context.Background()
	proposer := seedActiveUser(t, srv)
	_, applierAPIKey := seedActiveUserWithAPIKey(t, srv)
	if got := treasuryBalanceForTest(t, srv); got != communityRewardAmountKBApply+200 {
		t.Fatalf("initial treasury=%d want %d", got, communityRewardAmountKBApply+200)
	}

	proposal, _, err := srv.store.CreateKBProposal(ctx, store.KBProposal{
		ProposerUserID:    proposer,
		Title:             "Treasury-funded KB upgrade",
		Reason:            "shared",
		Status:            "discussing",
		VoteThresholdPct:  80,
		VoteWindowSeconds: 300,
	}, store.KBProposalChange{
		OpType:     "add",
		Section:    "knowledge/runtime",
		Title:      "treasury",
		NewContent: "shared result",
		DiffText:   "+ shared result",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	if _, err := srv.store.CloseKBProposal(ctx, proposal.ID, "approved", "ok", 1, 1, 0, 0, 1, time.Now().UTC()); err != nil {
		t.Fatalf("approve proposal: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/apply", map[string]any{
		"proposal_id": proposal.ID,
	}, apiKeyHeaders(applierAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("apply proposal status=%d body=%s", w.Code, w.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, proposer); got != 1000+communityRewardAmountKBApply {
		t.Fatalf("proposer balance=%d want %d", got, 1000+communityRewardAmountKBApply)
	}
	if got := treasuryBalanceForTest(t, srv); got != 200 {
		t.Fatalf("treasury balance=%d want 200", got)
	}
}

func TestTokenWishFulfillConsumesTreasury(t *testing.T) {
	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = 25
	userID, userAPIKey := seedActiveUserWithAPIKey(t, srv)
	fulfillerUserID, fulfillerAPIKey := seedActiveUserWithAPIKey(t, srv)
	if got := treasuryBalanceForTest(t, srv); got != 25 {
		t.Fatalf("initial treasury=%d want 25", got)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/wish/create", map[string]any{
		"title":         "need shared compute",
		"reason":        "benchmark",
		"target_amount": 10,
	}, apiKeyHeaders(userAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("wish create status=%d body=%s", w.Code, w.Body.String())
	}
	var create struct {
		Item struct {
			WishID string `json:"wish_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &create); err != nil {
		t.Fatalf("unmarshal wish create: %v", err)
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/wish/fulfill", map[string]any{
		"wish_id":        create.Item.WishID,
		"granted_amount": 10,
	}, apiKeyHeaders(fulfillerAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("wish fulfill status=%d fulfiller=%s body=%s", w.Code, fulfillerUserID, w.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, userID); got != 1010 {
		t.Fatalf("user balance=%d want 1010", got)
	}
	if got := treasuryBalanceForTest(t, srv); got != 15 {
		t.Fatalf("treasury balance=%d want 15", got)
	}
}

func TestTokenWishFulfillReturnsConflictWhenTreasuryInsufficient(t *testing.T) {
	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = 5
	userID, userAPIKey := seedActiveUserWithAPIKey(t, srv)
	_, fulfillerAPIKey := seedActiveUserWithAPIKey(t, srv)
	if got := treasuryBalanceForTest(t, srv); got != 5 {
		t.Fatalf("initial treasury=%d want 5", got)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/wish/create", map[string]any{
		"title":         "need more token",
		"reason":        "benchmark",
		"target_amount": 10,
	}, apiKeyHeaders(userAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("wish create status=%d body=%s", w.Code, w.Body.String())
	}
	var create struct {
		Item struct {
			WishID string `json:"wish_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &create); err != nil {
		t.Fatalf("unmarshal wish create: %v", err)
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/wish/fulfill", map[string]any{
		"wish_id":        create.Item.WishID,
		"granted_amount": 10,
	}, apiKeyHeaders(fulfillerAPIKey))
	if w.Code != http.StatusConflict {
		t.Fatalf("wish fulfill insufficient status=%d body=%s", w.Code, w.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, userID); got != 1000 {
		t.Fatalf("user balance=%d want 1000", got)
	}
	if got := treasuryBalanceForTest(t, srv); got != 5 {
		t.Fatalf("treasury balance=%d want 5", got)
	}
}

func TestPiTaskSubmitConsumesTreasury(t *testing.T) {
	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = 100
	userID, apiKey := seedActiveUserWithAPIKey(t, srv)
	before := treasuryBalanceForTest(t, srv)

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/claim", map[string]any{"user_id": userID}, apiKeyHeaders(apiKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("pi claim status=%d body=%s", w.Code, w.Body.String())
	}
	var claim struct {
		Item struct {
			TaskID string `json:"task_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &claim); err != nil {
		t.Fatalf("unmarshal pi claim: %v", err)
	}
	srv.taskMu.Lock()
	task := srv.piTasks[claim.Item.TaskID]
	srv.taskMu.Unlock()

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/submit", map[string]any{
		"user_id": userID,
		"task_id": task.TaskID,
		"answer":  task.Expected,
	}, apiKeyHeaders(apiKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("pi submit status=%d body=%s", w.Code, w.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, userID); got != 1000+task.RewardToken {
		t.Fatalf("user balance=%d want %d", got, 1000+task.RewardToken)
	}
	if got := treasuryBalanceForTest(t, srv); got != before-task.RewardToken {
		t.Fatalf("treasury balance=%d want %d", got, before-task.RewardToken)
	}
}

func TestPiTaskSubmitRejectsWhenTreasuryInsufficient(t *testing.T) {
	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = 1
	userID, apiKey := seedActiveUserWithAPIKey(t, srv)
	if got := treasuryBalanceForTest(t, srv); got != 1 {
		t.Fatalf("initial treasury=%d want 1", got)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/claim", map[string]any{"user_id": userID}, apiKeyHeaders(apiKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("pi claim status=%d body=%s", w.Code, w.Body.String())
	}
	var claim struct {
		Item struct {
			TaskID string `json:"task_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &claim); err != nil {
		t.Fatalf("unmarshal pi claim: %v", err)
	}
	srv.taskMu.Lock()
	task := srv.piTasks[claim.Item.TaskID]
	srv.taskMu.Unlock()

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/submit", map[string]any{
		"user_id": userID,
		"task_id": task.TaskID,
		"answer":  task.Expected,
	}, apiKeyHeaders(apiKey))
	if w.Code != http.StatusConflict {
		t.Fatalf("pi submit insufficient status=%d body=%s", w.Code, w.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, userID); got != 1000 {
		t.Fatalf("user balance=%d want 1000", got)
	}
	if got := treasuryBalanceForTest(t, srv); got != 1 {
		t.Fatalf("treasury balance=%d want 1", got)
	}
	srv.taskMu.Lock()
	restored := srv.piTasks[task.TaskID]
	activeTaskID := srv.activeTasks[userID]
	srv.taskMu.Unlock()
	if restored.Status != "claimed" {
		t.Fatalf("task status=%s want claimed", restored.Status)
	}
	if restored.Submitted != "" || restored.SubmittedAt != nil {
		t.Fatalf("expected task submission to be cleared after treasury failure: %+v", restored)
	}
	if activeTaskID != task.TaskID {
		t.Fatalf("active task=%s want %s", activeTaskID, task.TaskID)
	}
}

func TestSystemAccountsCannotUseTokenUserFlows(t *testing.T) {
	srv := newTestServer()
	userID, userAPIKey := seedActiveUserWithAPIKey(t, srv)
	treasuryAPIKey := apiKeyPrefix + "treasury-system-test"
	if _, err := srv.store.CreateAgentRegistration(context.Background(), store.AgentRegistrationInput{
		UserID:            clawTreasurySystemID,
		RequestedUsername: clawTreasurySystemID,
		GoodAt:            "test",
		Status:            "active",
		APIKeyHash:        hashSecret(treasuryAPIKey),
	}); err != nil {
		t.Fatalf("seed treasury api_key: %v", err)
	}
	if _, err := srv.ensureTreasuryAccount(context.Background()); err != nil {
		t.Fatalf("ensure treasury: %v", err)
	}

	cases := []struct {
		name    string
		path    string
		headers map[string]string
		payload map[string]any
		wantErr string
	}{
		{
			name:    "transfer from treasury rejected",
			path:    "/api/v1/token/transfer",
			headers: apiKeyHeaders(treasuryAPIKey),
			payload: map[string]any{
				"to_user_id": userID,
				"amount":     5,
			},
			wantErr: "system accounts cannot participate in transfer",
		},
		{
			name:    "tip to admin rejected",
			path:    "/api/v1/token/tip",
			headers: apiKeyHeaders(userAPIKey),
			payload: map[string]any{
				"to_user_id": clawWorldSystemID,
				"amount":     5,
				"reason":     "nope",
			},
			wantErr: "system accounts cannot participate in tip",
		},
		{
			name:    "wish create by treasury rejected",
			path:    "/api/v1/token/wish/create",
			headers: apiKeyHeaders(treasuryAPIKey),
			payload: map[string]any{
				"target_amount": 5,
			},
			wantErr: "system accounts cannot create wishes",
		},
		{
			name:    "pi claim by admin rejected",
			path:    "/api/v1/tasks/pi/claim",
			headers: apiKeyHeaders(treasuryAPIKey),
			payload: map[string]any{
				"user_id": clawTreasurySystemID,
			},
			wantErr: "system users cannot claim tasks",
		},
	}

	for _, tc := range cases {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, tc.path, tc.payload, tc.headers)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s status=%d body=%s", tc.name, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), tc.wantErr) {
			t.Fatalf("%s missing %q in %s", tc.name, tc.wantErr, w.Body.String())
		}
	}
}

func TestPiTaskEndpointsRequireAPIKeyAndBindIdentity(t *testing.T) {
	srv := newTestServer()
	userID, apiKey := seedActiveUserWithAPIKey(t, srv)
	otherUserID, otherAPIKey := seedActiveUserWithAPIKey(t, srv)

	unauthClaim := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/claim", map[string]any{
		"user_id": userID,
	})
	if unauthClaim.Code != http.StatusUnauthorized {
		t.Fatalf("unauth claim status=%d body=%s", unauthClaim.Code, unauthClaim.Body.String())
	}

	mismatchClaim := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/claim", map[string]any{
		"user_id": otherUserID,
	}, apiKeyHeaders(apiKey))
	if mismatchClaim.Code != http.StatusForbidden {
		t.Fatalf("mismatch claim status=%d body=%s", mismatchClaim.Code, mismatchClaim.Body.String())
	}

	claim := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/claim", map[string]any{
		"user_id": userID,
	}, apiKeyHeaders(apiKey))
	if claim.Code != http.StatusAccepted {
		t.Fatalf("claim status=%d body=%s", claim.Code, claim.Body.String())
	}
	var claimBody struct {
		Item struct {
			TaskID string `json:"task_id"`
			UserID string `json:"user_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(claim.Body.Bytes(), &claimBody); err != nil {
		t.Fatalf("unmarshal claim body: %v", err)
	}
	if claimBody.Item.UserID != userID {
		t.Fatalf("claim user_id=%s want %s", claimBody.Item.UserID, userID)
	}

	unauthSubmit := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/submit", map[string]any{
		"user_id": userID,
		"task_id": claimBody.Item.TaskID,
		"answer":  "0",
	})
	if unauthSubmit.Code != http.StatusUnauthorized {
		t.Fatalf("unauth submit status=%d body=%s", unauthSubmit.Code, unauthSubmit.Body.String())
	}

	mismatchSubmit := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/tasks/pi/submit", map[string]any{
		"user_id": userID,
		"task_id": claimBody.Item.TaskID,
		"answer":  "0",
	}, apiKeyHeaders(otherAPIKey))
	if mismatchSubmit.Code != http.StatusForbidden {
		t.Fatalf("mismatch submit status=%d body=%s", mismatchSubmit.Code, mismatchSubmit.Body.String())
	}
}
