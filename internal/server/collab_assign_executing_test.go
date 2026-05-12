package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"clawcolony/internal/store"
)

// TestCollabAssignInExecutingPhase (P4249) verifies that the assign endpoint
// accepts users who previously applied when the collab is in executing phase.
func TestCollabAssignInExecutingPhase(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)
	executor := newAuthUser(t, srv)
	lateJoiner := newAuthUser(t, srv)
	stranger := newAuthUser(t, srv)

	// Propose a general collab.
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/propose", map[string]any{
		"title":       "P4249 Test Collab",
		"goal":        "Verify assign-in-executing fix",
		"complexity":  "medium",
		"min_members": 2,
		"max_members": 4,
	}, proposer.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("propose status=%d body=%s", w.Code, w.Body.String())
	}
	var proposeResp struct {
		Item store.CollabSession `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &proposeResp); err != nil {
		t.Fatalf("decode propose: %v", err)
	}
	collabID := proposeResp.Item.CollabID

	// Initial applicants.
	applyCollab(t, srv, collabID, executor, "executor pitch")
	applyCollab(t, srv, collabID, proposer, "orchestrator pitch")

	// Assign initial team and transition to "assigned".
	assignCollab(t, srv, collabID, proposer, []map[string]any{
		{"user_id": proposer.id, "role": "orchestrator"},
		{"user_id": executor.id, "role": "executor"},
	})

	// Start → moves to "executing".
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/start", map[string]any{
		"collab_id":              collabID,
		"status_or_summary_note": "starting execution",
	}, proposer.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", w.Code, w.Body.String())
	}

	// Late joiner applies during executing phase.
	applyCollab(t, srv, collabID, lateJoiner, "late applicant pitch")

	// Assign the late joiner — this should succeed (P4249 fix).
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/assign", map[string]any{
		"collab_id":              collabID,
		"assignments":           []map[string]any{{"user_id": lateJoiner.id, "role": "contributor"}},
		"status_or_summary_note": "added late joiner",
	}, proposer.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("assign in executing should succeed, status=%d body=%s", w.Code, w.Body.String())
	}
	var assignResp struct {
		Item store.CollabSession `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &assignResp); err != nil {
		t.Fatalf("decode assign response: %v", err)
	}
	if assignResp.Item.Phase != "executing" {
		t.Fatalf("phase should remain executing, got=%s", assignResp.Item.Phase)
	}

	// Verify the late joiner is now "selected".
	parts, err := srv.store.ListCollabParticipants(t.Context(), collabID, "selected", 20)
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	found := false
	for _, p := range parts {
		if p.UserID == lateJoiner.id {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("late joiner should be in selected participants")
	}
}

// TestCollabAssignInExecutingRejectsStranger verifies that assigning a user
// who did NOT apply during executing phase is rejected.
func TestCollabAssignInExecutingRejectsStranger(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)
	executor := newAuthUser(t, srv)
	stranger := newAuthUser(t, srv)

	// Propose → apply → assign → start → executing.
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/propose", map[string]any{
		"title":       "P4249 Stranger Rejection",
		"goal":        "Verify stranger rejected in executing phase",
		"complexity":  "low",
		"min_members": 2,
		"max_members": 4,
	}, proposer.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("propose status=%d body=%s", w.Code, w.Body.String())
	}
	var proposeResp struct {
		Item store.CollabSession `json:"item"`
	}
	json.Unmarshal(w.Body.Bytes(), &proposeResp)
	collabID := proposeResp.Item.CollabID

	applyCollab(t, srv, collabID, executor, "executor pitch")
	applyCollab(t, srv, collabID, proposer, "orchestrator pitch")
	assignCollab(t, srv, collabID, proposer, []map[string]any{
		{"user_id": proposer.id, "role": "orchestrator"},
		{"user_id": executor.id, "role": "executor"},
	})
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/start", map[string]any{
		"collab_id":              collabID,
		"status_or_summary_note": "starting",
	}, proposer.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", w.Code, w.Body.String())
	}

	// Try to assign a stranger who never applied — should fail.
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/assign", map[string]any{
		"collab_id":   collabID,
		"assignments": []map[string]any{{"user_id": stranger.id, "role": "contributor"}},
	}, proposer.headers())
	if w.Code != http.StatusConflict {
		t.Fatalf("stranger assign in executing should return 409, got=%d body=%s", w.Code, w.Body.String())
	}
}

// TestCollabAssignInReviewingPhaseRejected verifies that assign is blocked
// for phases other than recruiting and executing (e.g. reviewing).
func TestCollabAssignInReviewingPhaseRejected(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)
	executor := newAuthUser(t, srv)
	lateJoiner := newAuthUser(t, srv)

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/propose", map[string]any{
		"title":       "P4249 Reviewing Phase Rejection",
		"goal":        "Verify assign blocked in reviewing phase",
		"complexity":  "low",
		"min_members": 2,
		"max_members": 4,
	}, proposer.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("propose status=%d body=%s", w.Code, w.Body.String())
	}
	var proposeResp struct {
		Item store.CollabSession `json:"item"`
	}
	json.Unmarshal(w.Body.Bytes(), &proposeResp)
	collabID := proposeResp.Item.CollabID

	applyCollab(t, srv, collabID, executor, "executor pitch")
	applyCollab(t, srv, collabID, proposer, "orchestrator pitch")
	assignCollab(t, srv, collabID, proposer, []map[string]any{
		{"user_id": proposer.id, "role": "orchestrator"},
		{"user_id": executor.id, "role": "executor"},
	})
	doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/start", map[string]any{
		"collab_id":              collabID,
		"status_or_summary_note": "starting",
	}, proposer.headers())

	// Submit an artifact to move to reviewing.
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/submit", map[string]any{
		"collab_id": collabID,
		"role":      "executor",
		"kind":      "code",
		"summary":   "done",
		"content":   "implementation complete",
	}, executor.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("submit status=%d body=%s", w.Code, w.Body.String())
	}

	// Verify we're in reviewing phase.
	var checkResp struct {
		Item store.CollabSession `json:"item"`
	}
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/collab/get?collab_id="+collabID, nil, proposer.headers())
	json.Unmarshal(w.Body.Bytes(), &checkResp)
	if checkResp.Item.Phase != "reviewing" {
		t.Fatalf("expected reviewing phase, got=%s", checkResp.Item.Phase)
	}

	// Try to assign a late joiner in reviewing phase — should fail.
	applyCollab(t, srv, collabID, lateJoiner, "late applicant")
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/assign", map[string]any{
		"collab_id":   collabID,
		"assignments": []map[string]any{{"user_id": lateJoiner.id, "role": "contributor"}},
	}, proposer.headers())
	if w.Code != http.StatusConflict {
		t.Fatalf("assign in reviewing should return 409, got=%d body=%s", w.Code, w.Body.String())
	}
}

// --- helpers ---

func applyCollab(t *testing.T, srv *Server, collabID string, actor authUser, pitch string) {
	t.Helper()
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/apply", map[string]any{
		"collab_id": collabID,
		"pitch":     pitch,
	}, actor.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("apply status=%d body=%s", w.Code, w.Body.String())
	}
}

func assignCollab(t *testing.T, srv *Server, collabID string, actor authUser, assignments []map[string]any) {
	t.Helper()
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/assign", map[string]any{
		"collab_id":              collabID,
		"assignments":            assignments,
		"status_or_summary_note": "assigned",
	}, actor.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("assign status=%d body=%s", w.Code, w.Body.String())
	}
}