package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"clawcolony/internal/store"
)

func TestLifeStateTransitionAuditRecordsWorldTickTransitions(t *testing.T) {
	srv := newTestServer()
	srv.cfg.HibernationPeriodTicks = 1
	ctx := context.Background()

	userID := seedActiveUser(t, srv)
	if err := srv.runLifeStateTransitions(ctx, 1); err != nil {
		t.Fatalf("run life transitions tick1: %v", err)
	}
	if _, err := srv.store.Consume(ctx, userID, 1000); err != nil {
		t.Fatalf("consume all balance: %v", err)
	}
	if err := srv.runLifeStateTransitions(ctx, 2); err != nil {
		t.Fatalf("run life transitions tick2: %v", err)
	}
	if err := srv.runLifeStateTransitions(ctx, 3); err != nil {
		t.Fatalf("run life transitions tick3: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/world/life-state/transitions?user_id="+userID+"&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("life-state transitions status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []store.UserLifeStateTransition `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode life-state transitions: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("expected 3 transitions, got=%d body=%s", len(resp.Items), w.Body.String())
	}
	if resp.Items[0].FromState != "hibernating" || resp.Items[0].ToState != "dead" || resp.Items[0].TickID != 3 {
		t.Fatalf("expected latest transition to be hibernating->dead at tick 3: %+v", resp.Items[0])
	}
	if resp.Items[0].SourceModule != "world.life_state_transition" || resp.Items[0].SourceRef != "world_tick:3" {
		t.Fatalf("expected world transition source metadata on death: %+v", resp.Items[0])
	}
	if resp.Items[1].FromState != "alive" || resp.Items[1].ToState != "hibernating" || resp.Items[1].TickID != 2 {
		t.Fatalf("expected second transition to be alive->hibernating at tick 2: %+v", resp.Items[1])
	}
	if resp.Items[2].FromState != "" || resp.Items[2].ToState != "alive" || resp.Items[2].TickID != 1 {
		t.Fatalf("expected first transition to initialize alive state at tick 1: %+v", resp.Items[2])
	}
}

func TestLifeStateTransitionAuditRecordsHibernateAndWake(t *testing.T) {
	srv := newTestServer()

	userID, userAPIKey := seedActiveUserWithAPIKey(t, srv)
	_, wakerAPIKey := seedActiveUserWithAPIKey(t, srv)
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/life/hibernate", map[string]any{
		"reason": "manual-rest",
	}, apiKeyHeaders(userAPIKey))
	if w.Code != http.StatusConflict {
		t.Fatalf("hibernate status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/world/life-state?user_id="+userID+"&limit=5", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("life-state status=%d body=%s", w.Code, w.Body.String())
	}
	if containsBody(w.Body.String(), `"state":"hibernating"`) || containsBody(w.Body.String(), `"state":"hibernated"`) {
		t.Fatalf("manual hibernate should not mutate life state under v2: %s", w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/life/wake", map[string]any{
		"user_id": userID,
		"reason":  "manual-wake",
	}, apiKeyHeaders(wakerAPIKey))
	if w.Code != http.StatusConflict {
		t.Fatalf("wake status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/world/life-state/transitions?user_id="+userID+"&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("life-state transitions status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []store.UserLifeStateTransition `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode transitions: %v", err)
	}
	if len(resp.Items) != 0 {
		t.Fatalf("expected no manual hibernate/wake transitions under v2, got=%d body=%s", len(resp.Items), w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/world/life-state/transitions?source_module=life.wake&from_state=hibernating&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("filtered transitions status=%d body=%s", w.Code, w.Body.String())
	}
	var filtered struct {
		Items []store.UserLifeStateTransition `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("decode filtered transitions: %v", err)
	}
	if len(filtered.Items) != 0 {
		t.Fatalf("expected no wake transitions, got=%d body=%s", len(filtered.Items), w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/world/life-state/transitions?from_state=typo", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid state filter should fail, got=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/world/life-state/transitions?tick_id=9999&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unknown tick_id filter should return empty page, got=%d body=%s", w.Code, w.Body.String())
	}
	var empty struct {
		Items []store.UserLifeStateTransition `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty tick response: %v", err)
	}
	if len(empty.Items) != 0 {
		t.Fatalf("unknown tick_id should return no transitions: %+v", empty.Items)
	}
}

func TestApplyUserLifeStateRejectsDeadToAlive(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	userID := seedActiveUser(t, srv)

	if _, _, err := srv.store.ApplyUserLifeState(ctx, store.UserLifeState{
		UserID: userID,
		State:  "dead",
		Reason: "test-dead",
	}, store.UserLifeStateAuditMeta{SourceModule: "test.dead"}); err != nil {
		t.Fatalf("mark dead: %v", err)
	}

	if _, _, err := srv.store.ApplyUserLifeState(ctx, store.UserLifeState{
		UserID: userID,
		State:  "alive",
		Reason: "should-fail",
	}, store.UserLifeStateAuditMeta{SourceModule: "test.alive"}); err == nil {
		t.Fatalf("dead user should not transition back to alive")
	}

	items, err := srv.store.ListUserLifeStateTransitions(ctx, store.UserLifeStateTransitionFilter{
		UserID: userID,
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("list transitions: %v", err)
	}
	if len(items) != 1 || items[0].ToState != "dead" {
		t.Fatalf("failed dead->alive write should not append a new transition: %+v", items)
	}
}

func containsBody(body, want string) bool {
	return len(body) > 0 && len(want) > 0 && strings.Contains(body, want)
}
