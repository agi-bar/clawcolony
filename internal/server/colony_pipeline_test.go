package server

import (
	"net/http"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func TestColonyPipelineCountsManualOpenPRsAndDedupesByPRURL(t *testing.T) {
	srv := newTestServer()
	author := seedActiveUser(t, srv)
	prURL := "https://github.com/agi-bar/clawcolony/pull/66"

	for _, collabID := range []string{"collab-pr-66-a", "collab-pr-66-b"} {
		if _, err := srv.store.CreateCollabSession(t.Context(), store.CollabSession{
			CollabID:           collabID,
			Title:              "Manual open PR " + collabID,
			Goal:               "reflect a live PR in the pipeline",
			Kind:               "upgrade_pr",
			Complexity:         "m",
			Phase:              "reviewing",
			ProposerUserID:     author,
			AuthorUserID:       author,
			OrchestratorUserID: author,
			MinMembers:         1,
			MaxMembers:         1,
			RequiredReviewers:  2,
			PRRepo:             "agi-bar/clawcolony",
			PRURL:              prURL,
			PRNumber:           66,
			GitHubPRState:      "open",
		}); err != nil {
			t.Fatalf("create collab %s: %v", collabID, err)
		}
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/pipeline", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pipeline status=%d body=%s", w.Code, w.Body.String())
	}
	body := parseJSONBody(t, w)
	stats := body["stats"].(map[string]any)
	if got := int(stats["active_prs"].(float64)); got != 1 {
		t.Fatalf("active_prs=%d want 1 body=%s", got, w.Body.String())
	}
	pipeline := body["pipeline"].(map[string]any)
	underReview := pipeline["under_review"].([]any)
	if len(underReview) != 1 {
		t.Fatalf("under_review count=%d want 1 body=%s", len(underReview), w.Body.String())
	}
	item := underReview[0].(map[string]any)
	if item["pr_url"] != prURL {
		t.Fatalf("unexpected pr_url=%v body=%s", item["pr_url"], w.Body.String())
	}
	if item["pr_state"] != "open" {
		t.Fatalf("unexpected pr_state=%v body=%s", item["pr_state"], w.Body.String())
	}
}

func TestColonyPipelineCountsProposalLinkedOpenPRsUnderReview(t *testing.T) {
	srv := newTestServer()
	proposer := seedActiveUser(t, srv)
	closedAt := time.Now().UTC().Add(-3 * time.Hour)
	appliedAt := closedAt.Add(20 * time.Minute)
	proposalID := createGovernanceProposalWithDecisionTimesForTest(t, srv, proposer, "Pipeline open PR tracking", closedAt, &appliedAt)

	if _, err := srv.store.CreateCollabSession(t.Context(), store.CollabSession{
		CollabID:           "collab-pipeline-linked-open-pr",
		Title:              "Linked open PR",
		Goal:               "keep linked PR visible in under_review",
		Kind:               "upgrade_pr",
		Complexity:         "m",
		Phase:              "recruiting",
		ProposerUserID:     proposer,
		AuthorUserID:       proposer,
		OrchestratorUserID: proposer,
		MinMembers:         1,
		MaxMembers:         1,
		RequiredReviewers:  2,
		PRRepo:             "agi-bar/clawcolony",
		PRURL:              "https://github.com/agi-bar/clawcolony/pull/77",
		PRNumber:           77,
		GitHubPRState:      "open",
		SourceRef:          proposalSourceRefString(proposalID),
		ProposalID:         proposalID,
		ImplementationMode: "code_change",
	}); err != nil {
		t.Fatalf("create linked open PR collab: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/pipeline", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pipeline status=%d body=%s", w.Code, w.Body.String())
	}
	body := parseJSONBody(t, w)
	stats := body["stats"].(map[string]any)
	if got := int(stats["active_prs"].(float64)); got != 1 {
		t.Fatalf("active_prs=%d want 1 body=%s", got, w.Body.String())
	}
	pipeline := body["pipeline"].(map[string]any)
	underReview := pipeline["under_review"].([]any)
	if len(underReview) != 1 {
		t.Fatalf("under_review count=%d want 1 body=%s", len(underReview), w.Body.String())
	}
	item := underReview[0].(map[string]any)
	if got := int64(item["proposal_id"].(float64)); got != proposalID {
		t.Fatalf("proposal_id=%d want %d body=%s", got, proposalID, w.Body.String())
	}
	if item["pr_url"] != "https://github.com/agi-bar/clawcolony/pull/77" {
		t.Fatalf("unexpected pr_url=%v body=%s", item["pr_url"], w.Body.String())
	}
}
