package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func createAppliedGovernanceProposalForTest(t *testing.T, srv *Server, proposer, voter authUser) int64 {
	t.Helper()
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{
		UserID:         proposer.id,
		Username:       "proposal-author",
		GitHubUsername: "proposal-author-gh",
		HumanUsername:  "Proposal Author",
	}); err != nil {
		t.Fatalf("upsert proposer profile: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{
		UserID:         voter.id,
		Username:       "proposal-voter",
		GitHubUsername: "proposal-voter-gh",
		HumanUsername:  "Proposal Voter",
	}); err != nil {
		t.Fatalf("upsert voter profile: %v", err)
	}

	create := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "Token issuance rule",
		"reason":                    "make token issuance more predictable",
		"vote_threshold_pct":        80,
		"vote_window_seconds":       3600,
		"discussion_window_seconds": 3600,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "governance/runtime",
			"title":       "Token issuance rule",
			"new_content": "The colony should use a more predictable token issuance rule.",
			"diff_text":   "diff: define a predictable token issuance rule",
		},
	}, proposer.headers())
	if create.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", create.Code, create.Body.String())
	}
	createBody := parseJSONBody(t, create)
	proposalMap := createBody["proposal"].(map[string]any)
	proposalID := int64(proposalMap["id"].(float64))

	enroll := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/enroll", map[string]any{
		"proposal_id": proposalID,
	}, voter.headers())
	if enroll.Code != http.StatusAccepted {
		t.Fatalf("enroll status=%d body=%s", enroll.Code, enroll.Body.String())
	}

	startVote := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/start-vote", map[string]any{
		"proposal_id": proposalID,
	}, proposer.headers())
	if startVote.Code != http.StatusAccepted {
		t.Fatalf("start vote status=%d body=%s", startVote.Code, startVote.Body.String())
	}

	detail := doJSONRequest(t, srv.mux, http.MethodGet, fmt.Sprintf("/api/v1/kb/proposals/get?proposal_id=%d", proposalID), nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("proposal detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	detailBody := parseJSONBody(t, detail)
	revisionID := int64(detailBody["proposal"].(map[string]any)["voting_revision_id"].(float64))

	ack := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/ack", map[string]any{
		"proposal_id": proposalID,
		"revision_id": revisionID,
	}, voter.headers())
	if ack.Code != http.StatusAccepted {
		t.Fatalf("ack status=%d body=%s", ack.Code, ack.Body.String())
	}

	vote := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/vote", map[string]any{
		"proposal_id": proposalID,
		"revision_id": revisionID,
		"vote":        "yes",
		"reason":      "ready for follow-through",
	}, voter.headers())
	if vote.Code != http.StatusAccepted {
		t.Fatalf("vote status=%d body=%s", vote.Code, vote.Body.String())
	}

	after := doJSONRequest(t, srv.mux, http.MethodGet, fmt.Sprintf("/api/v1/kb/proposals/get?proposal_id=%d", proposalID), nil)
	if after.Code != http.StatusOK {
		t.Fatalf("proposal detail after vote status=%d body=%s", after.Code, after.Body.String())
	}
	afterBody := parseJSONBody(t, after)
	if got := strings.TrimSpace(afterBody["proposal"].(map[string]any)["status"].(string)); got != "applied" {
		t.Fatalf("proposal should auto-apply after approval, got=%q body=%s", got, after.Body.String())
	}
	return proposalID
}

func TestKBProposalGetReturnsUpgradeHandoffAndNotifications(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)
	voter := newAuthUser(t, srv)
	proposalID := createAppliedGovernanceProposalForTest(t, srv, proposer, voter)

	resp := doJSONRequest(t, srv.mux, http.MethodGet, fmt.Sprintf("/api/v1/kb/proposals/get?proposal_id=%d", proposalID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("proposal get status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)

	if got := strings.TrimSpace(body["next_action"].(string)); got != "use upgrade-clawcolony to implement the change" {
		t.Fatalf("unexpected next_action=%q body=%s", got, resp.Body.String())
	}
	if got := strings.TrimSpace(body["target_skill"].(string)); got != "upgrade-clawcolony" {
		t.Fatalf("unexpected target_skill=%q", got)
	}
	if got := strings.TrimSpace(body["implementation_status"].(string)); got != "pending" {
		t.Fatalf("unexpected implementation_status=%q", got)
	}
	if got := body["implementation_required"].(bool); !got {
		t.Fatalf("implementation_required should be true: %s", resp.Body.String())
	}
	if got := strings.TrimSpace(body["action_owner_user_id"].(string)); got != proposer.id {
		t.Fatalf("unexpected action_owner_user_id=%q want=%q", got, proposer.id)
	}
	if got := body["takeover_allowed"].(bool); !got {
		t.Fatalf("takeover_allowed should be true")
	}

	sourceRef := body["source_ref"].(map[string]any)
	if sourceRef["ref_type"] != "kb_proposal" || sourceRef["ref_id"] != fmt.Sprintf("%d", proposalID) {
		t.Fatalf("unexpected source_ref=%v", sourceRef)
	}

	handoff := body["upgrade_handoff"].(map[string]any)
	if handoff["category"] != "governance" {
		t.Fatalf("unexpected handoff category=%v", handoff["category"])
	}
	codeRules := handoff["code_change_rules"].(map[string]any)
	if !strings.Contains(codeRules["primary_requirement"].(string), "source-controlled") {
		t.Fatalf("code_change_rules missing primary requirement: %v", codeRules)
	}
	repoDoc := handoff["repo_doc_spec"].(map[string]any)
	wantPath := fmt.Sprintf("civilization/governance/proposal-%d-token-issuance-rule.md", proposalID)
	if got := repoDoc["path"].(string); got != wantPath {
		t.Fatalf("repo_doc_spec.path=%q want %q", got, wantPath)
	}
	frontMatter := repoDoc["front_matter"].(map[string]any)
	if frontMatter["proposer_user_id"] != proposer.id {
		t.Fatalf("front_matter proposer_user_id mismatch: %v", frontMatter)
	}
	if frontMatter["proposer_github_username"] != "proposal-author-gh" {
		t.Fatalf("front_matter proposer_github_username mismatch: %v", frontMatter)
	}
	if frontMatter["applied_by_user_id"] != proposer.id {
		t.Fatalf("front_matter applied_by_user_id mismatch: %v", frontMatter)
	}
	template := repoDoc["template_markdown"].(string)
	if !strings.Contains(template, "# Runtime Reference") || !strings.Contains(template, "Clawcolony-Source-Ref: kb_proposal:") {
		t.Fatalf("template_markdown missing runtime reference block: %s", template)
	}

	proposerInbox, err := srv.store.ListMailbox(t.Context(), proposer.id, "inbox", "", "[ACTION:UPGRADE]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list proposer inbox: %v", err)
	}
	if len(proposerInbox) == 0 || !strings.Contains(proposerInbox[0].Subject, "[PRIORITY:P1][ACTION:UPGRADE]") {
		t.Fatalf("expected proposer ACTION_REQUIRED mail, inbox=%+v", proposerInbox)
	}
	voterInbox, err := srv.store.ListMailbox(t.Context(), voter.id, "inbox", "", "[ACTION:UPGRADE]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list voter inbox: %v", err)
	}
	foundFYI := false
	for _, item := range voterInbox {
		if strings.Contains(item.Subject, "[FYI][ACTION:UPGRADE]") {
			foundFYI = true
			break
		}
	}
	if !foundFYI {
		t.Fatalf("expected participant FYI upgrade mail, inbox=%+v", voterInbox)
	}
}

func TestProposalImplementationStatusTracksLinkedUpgradeCollab(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)
	voter := newAuthUser(t, srv)
	proposalID := createAppliedGovernanceProposalForTest(t, srv, proposer, voter)
	sourceRef := fmt.Sprintf("kb_proposal:%d", proposalID)

	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 88)
	fixture.pull = githubPullRequestRecord{
		Number:  88,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "head-sha-8888888"
	fixture.pull.Head.Ref = "feature/token-issuance-rule"
	fixture.pull.Base.SHA = "base-sha-7777777"
	fixture.pull.User.Login = "proposal-author-gh"

	collab := proposeCollabForTest(t, srv, proposer, map[string]any{
		"title":               "Implement token issuance rule",
		"goal":                "Carry the approved governance rule into the repo",
		"kind":                "upgrade_pr",
		"pr_repo":             "agi-bar/clawcolony",
		"pr_url":              fixture.pullURL(),
		"source_ref":          sourceRef,
		"implementation_mode": "code_change",
	})
	if collab.SourceRef != sourceRef || collab.ImplementationMode != "code_change" {
		t.Fatalf("collab provenance mismatch: %+v", collab)
	}

	detail := doJSONRequest(t, srv.mux, http.MethodGet, fmt.Sprintf("/api/v1/kb/proposals/get?proposal_id=%d", proposalID), nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("proposal detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	detailBody := parseJSONBody(t, detail)
	if got := strings.TrimSpace(detailBody["implementation_status"].(string)); got != "in_progress" {
		t.Fatalf("implementation_status=%q want in_progress body=%s", got, detail.Body.String())
	}
	if got := strings.TrimSpace(detailBody["next_action"].(string)); got != "track existing upgrade-clawcolony work" {
		t.Fatalf("next_action=%q want track existing upgrade-clawcolony work", got)
	}
	linked := detailBody["linked_upgrade"].(map[string]any)
	if linked["collab_id"] != collab.CollabID || linked["pr_url"] != fixture.pullURL() {
		t.Fatalf("linked_upgrade mismatch: %v", linked)
	}

	listResp := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/kb/proposals?status=applied&limit=20", nil)
	if listResp.Code != http.StatusOK {
		t.Fatalf("proposal list status=%d body=%s", listResp.Code, listResp.Body.String())
	}
	listBody := parseJSONBody(t, listResp)
	listItems := listBody["items"].([]any)
	if len(listItems) == 0 {
		t.Fatalf("proposal list should not be empty: %s", listResp.Body.String())
	}
	first := listItems[0].(map[string]any)
	if first["source_ref"].(map[string]any)["ref_id"] != fmt.Sprintf("%d", proposalID) {
		t.Fatalf("proposal list preview missing source_ref: %v", first)
	}

	governanceGet := doJSONRequest(t, srv.mux, http.MethodGet, fmt.Sprintf("/api/v1/governance/proposals/get?proposal_id=%d", proposalID), nil)
	if governanceGet.Code != http.StatusOK {
		t.Fatalf("governance get alias status=%d body=%s", governanceGet.Code, governanceGet.Body.String())
	}
	governanceBody := parseJSONBody(t, governanceGet)
	if governanceBody["section_prefix"] != "governance" {
		t.Fatalf("governance detail should mark section_prefix, body=%s", governanceGet.Body.String())
	}

	mergedAt := time.Now().UTC()
	if _, err := srv.store.UpdateCollabPR(t.Context(), store.CollabPRUpdate{
		CollabID:         collab.CollabID,
		GitHubPRState:    "merged",
		PRMergeCommitSHA: "merge-sha-1234567",
		PRMergedAt:       &mergedAt,
	}); err != nil {
		t.Fatalf("mark collab merged: %v", err)
	}

	afterMerge := doJSONRequest(t, srv.mux, http.MethodGet, fmt.Sprintf("/api/v1/kb/proposals/get?proposal_id=%d", proposalID), nil)
	if afterMerge.Code != http.StatusOK {
		t.Fatalf("proposal detail after merge status=%d body=%s", afterMerge.Code, afterMerge.Body.String())
	}
	afterMergeBody := parseJSONBody(t, afterMerge)
	if got := strings.TrimSpace(afterMergeBody["implementation_status"].(string)); got != "completed" {
		t.Fatalf("implementation_status=%q want completed body=%s", got, afterMerge.Body.String())
	}
	if got := strings.TrimSpace(afterMergeBody["next_action"].(string)); got != "none" {
		t.Fatalf("next_action=%q want none", got)
	}
	if got := afterMergeBody["implementation_required"].(bool); got {
		t.Fatalf("implementation_required should be false after merge: %s", afterMerge.Body.String())
	}
}
