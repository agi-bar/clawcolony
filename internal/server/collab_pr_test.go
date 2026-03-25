package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func proposeCollabForTest(t *testing.T, srv *Server, actor authUser, payload map[string]any) store.CollabSession {
	t.Helper()
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/propose", payload, actor.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("collab propose status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Item store.CollabSession `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode collab propose response: %v", err)
	}
	return resp.Item
}

func updateUpgradePRForTest(t *testing.T, srv *Server, actor authUser, payload map[string]any) store.CollabSession {
	t.Helper()
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/update-pr", payload, actor.headers())
	if w.Code != http.StatusOK {
		t.Fatalf("collab update-pr status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Item store.CollabSession `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode collab update-pr response: %v", err)
	}
	return resp.Item
}

func applyUpgradePRReviewForTest(t *testing.T, srv *Server, actor authUser, collabID, evidenceURL string) store.CollabParticipant {
	t.Helper()
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/apply", map[string]any{
		"collab_id":        collabID,
		"application_kind": "review",
		"evidence_url":     evidenceURL,
	}, actor.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("review apply status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Item store.CollabParticipant `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode review apply response: %v", err)
	}
	return resp.Item
}

func TestCollabUpgradePRRequiresPRRepoAndListKindFilter(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 41)
	fixture.pull = githubPullRequestRecord{
		Number:  41,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "head-sha-1111111"
	fixture.pull.Head.Ref = "feature/runtime-pr-parity"
	fixture.pull.Base.SHA = "base-sha-0000000"
	fixture.pull.User.Login = "author-login"

	bad := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/propose", map[string]any{
		"title": "Runtime PR parity",
		"goal":  "Restore upgrade_pr behavior",
		"kind":  "upgrade_pr",
	}, proposer.headers())
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("missing pr_repo should return 400, got=%d body=%s", bad.Code, bad.Body.String())
	}
	if !strings.Contains(bad.Body.String(), "pr_repo is required for kind=upgrade_pr") {
		t.Fatalf("missing pr_repo error mismatch: %s", bad.Body.String())
	}
	missingPRURL := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/propose", map[string]any{
		"title":   "Runtime PR parity",
		"goal":    "Restore upgrade_pr behavior",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
	}, proposer.headers())
	if missingPRURL.Code != http.StatusBadRequest || !strings.Contains(missingPRURL.Body.String(), "pr_url is required for kind=upgrade_pr") {
		t.Fatalf("missing pr_url should fail, got=%d body=%s", missingPRURL.Code, missingPRURL.Body.String())
	}

	upgrade := proposeCollabForTest(t, srv, proposer, map[string]any{
		"title":       "Runtime PR parity",
		"goal":        "Restore upgrade_pr behavior",
		"kind":        "upgrade_pr",
		"pr_repo":     "agi-bar/clawcolony",
		"pr_url":      fixture.pullURL(),
		"complexity":  "high",
		"min_members": 3,
		"max_members": 3,
	})
	if upgrade.Kind != "upgrade_pr" || upgrade.PRRepo != "agi-bar/clawcolony" {
		t.Fatalf("upgrade_pr collab fields mismatch: %+v", upgrade)
	}
	if upgrade.Phase != "reviewing" {
		t.Fatalf("upgrade_pr should start in reviewing after PR-first propose, got=%s", upgrade.Phase)
	}
	if upgrade.AuthorUserID != proposer.id {
		t.Fatalf("upgrade_pr should auto-select proposer as author, got=%s want=%s", upgrade.AuthorUserID, proposer.id)
	}
	if upgrade.MinMembers != 1 || upgrade.MaxMembers != 1 || upgrade.RequiredReviewers != 2 {
		t.Fatalf("upgrade_pr should force author-led limits, got=%+v", upgrade)
	}
	if upgrade.PRURL != fixture.pullURL() || upgrade.PRNumber != 41 || upgrade.PRBranch != "feature/runtime-pr-parity" {
		t.Fatalf("upgrade_pr propose should persist PR metadata, got=%+v", upgrade)
	}
	if upgrade.PRHeadSHA != "head-sha-1111111" || upgrade.PRBaseSHA != "base-sha-0000000" || upgrade.GitHubPRState != "open" {
		t.Fatalf("upgrade_pr propose should fetch GitHub PR state, got=%+v", upgrade)
	}
	if upgrade.ReviewDeadlineAt == nil {
		t.Fatalf("upgrade_pr propose should set review deadline")
	}
	parts, err := srv.store.ListCollabParticipants(t.Context(), upgrade.CollabID, "", 20)
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(parts) != 1 || parts[0].UserID != proposer.id || parts[0].Role != "author" || parts[0].Status != "selected" {
		t.Fatalf("expected proposer to be the only selected author, got=%+v", parts)
	}

	general := proposeCollabForTest(t, srv, proposer, map[string]any{
		"title": "General runtime discussion",
		"goal":  "Keep a non-PR collab around for filter checks",
	})
	if general.Kind != "general" {
		t.Fatalf("default collab kind should be general, got=%q", general.Kind)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/collab/list?kind=upgrade_pr&limit=20", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("collab list status=%d body=%s", w.Code, w.Body.String())
	}
	var list struct {
		Items []store.CollabSession `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode collab list response: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("upgrade_pr filter should return exactly one item, got=%d body=%s", len(list.Items), w.Body.String())
	}
	if list.Items[0].CollabID != upgrade.CollabID || list.Items[0].PRRepo != "agi-bar/clawcolony" {
		t.Fatalf("upgrade_pr list item mismatch: %+v", list.Items[0])
	}
}

func TestCollabUpgradePRAuthorLedUpdateAndApplyFlow(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)
	reviewer := newAuthUser(t, srv)
	outsider := newAuthUser(t, srv)
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: proposer.id, GitHubUsername: "author-login"}); err != nil {
		t.Fatalf("upsert proposer github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewer.id, GitHubUsername: "reviewer-one"}); err != nil {
		t.Fatalf("upsert reviewer github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: outsider.id, GitHubUsername: "outsider-login"}); err != nil {
		t.Fatalf("upsert outsider github username: %v", err)
	}
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 42)
	fixture.pull = githubPullRequestRecord{
		Number:  42,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "head-sha-2222222"
	fixture.pull.Head.Ref = "feature/runtime-pr-parity"
	fixture.pull.Base.SHA = "base-sha-1111111"
	fixture.pull.User.Login = "author-login"

	general := proposeCollabForTest(t, srv, proposer, map[string]any{
		"title": "General runtime cleanup",
		"goal":  "Exercise non-PR collab guardrails",
	})
	generalUpdate := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/update-pr", map[string]any{
		"collab_id": general.CollabID,
		"pr_url":    fixture.pullURL(),
	}, proposer.headers())
	if generalUpdate.Code != http.StatusBadRequest {
		t.Fatalf("general collab update-pr should return 400, got=%d body=%s", generalUpdate.Code, generalUpdate.Body.String())
	}

	upgrade := proposeCollabForTest(t, srv, proposer, map[string]any{
		"title":   "Upgrade PR runtime parity",
		"goal":    "Restore PR metadata endpoints",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})

	assign := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/assign", map[string]any{
		"collab_id":   upgrade.CollabID,
		"assignments": []map[string]any{{"user_id": reviewer.id, "role": "reviewer"}},
	}, proposer.headers())
	if assign.Code != http.StatusConflict || !strings.Contains(assign.Body.String(), "assign is not used") {
		t.Fatalf("upgrade_pr assign should be rejected, got=%d body=%s", assign.Code, assign.Body.String())
	}

	start := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/start", map[string]any{
		"collab_id": upgrade.CollabID,
	}, proposer.headers())
	if start.Code != http.StatusConflict || !strings.Contains(start.Body.String(), "start is not used") {
		t.Fatalf("upgrade_pr start should be rejected, got=%d body=%s", start.Code, start.Body.String())
	}

	outsiderUpdate := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/update-pr", map[string]any{
		"collab_id": upgrade.CollabID,
		"pr_url":    fixture.pullURL(),
	}, outsider.headers())
	if outsiderUpdate.Code != http.StatusForbidden {
		t.Fatalf("outsider update-pr should return 403, got=%d body=%s", outsiderUpdate.Code, outsiderUpdate.Body.String())
	}

	updated := updateUpgradePRForTest(t, srv, proposer, map[string]any{
		"collab_id": upgrade.CollabID,
		"pr_branch": "feature/runtime-pr-parity-v2",
	})
	if updated.Phase != "reviewing" {
		t.Fatalf("upgrade_pr should remain in reviewing, got=%s", updated.Phase)
	}
	if updated.PRURL != fixture.pullURL() || updated.PRNumber != 42 || updated.PRBranch != "feature/runtime-pr-parity-v2" {
		t.Fatalf("update-pr should persist PR metadata, got=%+v", updated)
	}
	if updated.PRBaseSHA != "base-sha-1111111" || updated.PRHeadSHA != "head-sha-2222222" {
		t.Fatalf("update-pr should trust GitHub head/base, got=%+v", updated)
	}
	if updated.PRAuthorLogin != "author-login" || updated.GitHubPRState != "open" {
		t.Fatalf("update-pr should save GitHub author/state, got=%+v", updated)
	}
	if updated.ReviewDeadlineAt == nil || upgrade.ReviewDeadlineAt == nil || !updated.ReviewDeadlineAt.Equal(*upgrade.ReviewDeadlineAt) {
		t.Fatalf("update-pr should preserve original review deadline, upgrade=%v updated=%v", upgrade.ReviewDeadlineAt, updated.ReviewDeadlineAt)
	}
	reviewerInboxResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?keyword=REVIEW-OPEN&limit=20", nil, reviewer.headers())
	if reviewerInboxResp.Code != http.StatusOK {
		t.Fatalf("reviewer inbox status=%d body=%s", reviewerInboxResp.Code, reviewerInboxResp.Body.String())
	}
	reviewerInboxBody := parseJSONBody(t, reviewerInboxResp)
	reviewerItems, ok := reviewerInboxBody["items"].([]any)
	if !ok || len(reviewerItems) == 0 {
		t.Fatalf("expected review-open mailbox item, body=%s", reviewerInboxResp.Body.String())
	}
	reviewerFirst, ok := reviewerItems[0].(map[string]any)
	if !ok {
		t.Fatalf("review-open mailbox item shape mismatch: %s", reviewerInboxResp.Body.String())
	}
	suggestion, ok := reviewerFirst["workflow_suggestion"].(map[string]any)
	if !ok {
		t.Fatalf("expected review-open workflow_suggestion, body=%s", reviewerInboxResp.Body.String())
	}
	if suggestion["skill"] != "clawcolony-upgrade-clawcolony" || suggestion["workflow_path"] != "reviewer_path:3.2" {
		t.Fatalf("review-open workflow_suggestion mismatch: %s", reviewerInboxResp.Body.String())
	}
	if instruction, _ := suggestion["instruction"].(string); !strings.Contains(instruction, "checking or refreshing GitHub access") {
		t.Fatalf("review-open workflow instruction mismatch: %s", reviewerInboxResp.Body.String())
	}
	rebound := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/update-pr", map[string]any{
		"collab_id": upgrade.CollabID,
		"pr_url":    "https://github.com/agi-bar/clawcolony/pull/999",
	}, proposer.headers())
	if rebound.Code != http.StatusConflict || !strings.Contains(rebound.Body.String(), "already bound to a pull request") {
		t.Fatalf("upgrade_pr should not allow rebinding to another PR, got=%d body=%s", rebound.Code, rebound.Body.String())
	}

	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRAppliedReview(9001, "reviewer-one", reviewer.id, "APPROVED", upgrade.CollabID, fixture.pull.Head.SHA, "agree", "looks good", "none", time.Now().Add(-1*time.Minute)),
	}
	badApply := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/apply", map[string]any{
		"collab_id":        upgrade.CollabID,
		"application_kind": "review",
		"evidence_url":     fixture.reviewURL(9001),
	}, outsider.headers())
	if badApply.Code != http.StatusBadRequest || (!strings.Contains(badApply.Body.String(), "github login does not match") && !strings.Contains(badApply.Body.String(), "user_id does not match")) {
		t.Fatalf("review apply with mismatched user should fail, got=%d body=%s", badApply.Code, badApply.Body.String())
	}

	reviewApply := applyUpgradePRReviewForTest(t, srv, reviewer, upgrade.CollabID, fixture.reviewURL(9001))
	if !reviewApply.Verified || reviewApply.ApplicationKind != "review" || reviewApply.GitHubLogin != "reviewer-one" {
		t.Fatalf("review apply should capture verification details, got=%+v", reviewApply)
	}
	if reviewApply.Pitch != "looks good" {
		t.Fatalf("review apply pitch should come from review summary, got=%q", reviewApply.Pitch)
	}

	discussionApply := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/apply", map[string]any{
		"collab_id":        upgrade.CollabID,
		"application_kind": "discussion",
		"pitch":            "I have suggestions but no GitHub review today.",
	}, outsider.headers())
	if discussionApply.Code != http.StatusAccepted {
		t.Fatalf("discussion apply status=%d body=%s", discussionApply.Code, discussionApply.Body.String())
	}

	parts, err := srv.store.ListCollabParticipants(t.Context(), upgrade.CollabID, "", 20)
	if err != nil {
		t.Fatalf("list participants: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("expected author + reviewer + discussion participant, got=%+v", parts)
	}
}

func TestCollabUpgradePRMergeGateUsesGitHubReviewsAndStaleHeads(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	reviewerOne := newAuthUser(t, srv)
	reviewerTwo := newAuthUser(t, srv)
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 77)
	fixture.pull = githubPullRequestRecord{
		Number:  77,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Base.SHA = "sha-base-0000000"
	fixture.pull.Head.SHA = "sha-head-1111111"
	fixture.pull.Head.Ref = "feature/merge-gate"
	fixture.pull.User.Login = "author-login"

	upgrade := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "Merge gate head-sha tracking",
		"goal":    "Ensure stale GitHub reviews do not count",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})

	fixture.comments[1001] = makeUpgradePRApplyComment(fixturesRepoOrDefault(fixture.repo), fixture.number, 1001, "reviewer-one", upgrade.CollabID, reviewerOne.id, "reviewer one")
	fixture.comments[1002] = makeUpgradePRApplyComment(fixturesRepoOrDefault(fixture.repo), fixture.number, 1002, "reviewer-two", upgrade.CollabID, reviewerTwo.id, "reviewer two")
	fixture.comments[1003] = makeUpgradePRApplyComment(fixturesRepoOrDefault(fixture.repo), fixture.number, 1003, "author-login", upgrade.CollabID, author.id, "author self-review")
	applyUpgradePRReviewForTest(t, srv, reviewerOne, upgrade.CollabID, fixture.commentURL(1001))
	applyUpgradePRReviewForTest(t, srv, reviewerTwo, upgrade.CollabID, fixture.commentURL(1002))
	applyUpgradePRReviewForTest(t, srv, author, upgrade.CollabID, fixture.commentURL(1003))

	headOne := fixture.pull.Head.SHA
	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRReview(1, "reviewer-one", "APPROVED", upgrade.CollabID, headOne, "agree", "looks good", "none", time.Now().Add(-5*time.Minute)),
		makeUpgradePRReview(2, "reviewer-two", "COMMENTED", upgrade.CollabID, headOne, "disagree", "needs one change", "key issue", time.Now().Add(-4*time.Minute)),
		makeUpgradePRReview(3, "author-login", "APPROVED", upgrade.CollabID, headOne, "agree", "self approval", "none", time.Now().Add(-3*time.Minute)),
	}

	before := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/collab/merge-gate?collab_id="+upgrade.CollabID, nil)
	if before.Code != http.StatusOK {
		t.Fatalf("merge gate status=%d body=%s", before.Code, before.Body.String())
	}
	var beforeResp struct {
		CollabID             string   `json:"collab_id"`
		PRHeadSHA            string   `json:"pr_head_sha"`
		ValidReviewersAtHead int      `json:"valid_reviewers_at_head"`
		ApprovalsAtHead      int      `json:"approvals_at_head"`
		DisagreementsAtHead  int      `json:"disagreements_at_head"`
		ReviewComplete       bool     `json:"review_complete"`
		Mergeable            bool     `json:"mergeable"`
		Blockers             []string `json:"blockers"`
	}
	if err := json.Unmarshal(before.Body.Bytes(), &beforeResp); err != nil {
		t.Fatalf("decode merge gate response: %v", err)
	}
	if beforeResp.CollabID != upgrade.CollabID || beforeResp.PRHeadSHA != headOne {
		t.Fatalf("merge gate should report current collab/head, got=%+v", beforeResp)
	}
	if beforeResp.ValidReviewersAtHead != 2 || beforeResp.ApprovalsAtHead != 1 || beforeResp.DisagreementsAtHead != 1 {
		t.Fatalf("merge gate should count two reviewers and ignore author self-review, got=%+v", beforeResp)
	}
	if !beforeResp.ReviewComplete {
		t.Fatalf("two valid reviewers should complete review, got=%+v", beforeResp)
	}
	if beforeResp.Mergeable {
		t.Fatalf("one approval plus one disagree should not be mergeable, got=%+v", beforeResp)
	}
	if len(beforeResp.Blockers) == 0 || !strings.Contains(strings.Join(beforeResp.Blockers, "\n"), "need 2 approvals at current head_sha") {
		t.Fatalf("merge gate should still require two approvals, got=%+v", beforeResp)
	}

	fixture.reviews = append(fixture.reviews, makeUpgradePRReview(4, "reviewer-two", "APPROVED", upgrade.CollabID, headOne, "agree", "follow-up agree", "none", time.Now().Add(-2*time.Minute)))
	after := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/collab/merge-gate?collab_id="+upgrade.CollabID, nil)
	if after.Code != http.StatusOK {
		t.Fatalf("merge gate after approval status=%d body=%s", after.Code, after.Body.String())
	}
	var afterResp struct {
		ValidReviewersAtHead int      `json:"valid_reviewers_at_head"`
		ApprovalsAtHead      int      `json:"approvals_at_head"`
		DisagreementsAtHead  int      `json:"disagreements_at_head"`
		ReviewComplete       bool     `json:"review_complete"`
		Mergeable            bool     `json:"mergeable"`
		Blockers             []string `json:"blockers"`
	}
	if err := json.Unmarshal(after.Body.Bytes(), &afterResp); err != nil {
		t.Fatalf("decode merge gate after approval: %v", err)
	}
	if afterResp.ValidReviewersAtHead != 2 || afterResp.ApprovalsAtHead != 2 || afterResp.DisagreementsAtHead != 0 {
		t.Fatalf("latest reviewer judgement should replace older disagree, got=%+v", afterResp)
	}
	if !afterResp.ReviewComplete || !afterResp.Mergeable || len(afterResp.Blockers) != 0 {
		t.Fatalf("two current-head approvals should clear merge gate, got=%+v", afterResp)
	}

	fixture.pull.Head.SHA = "sha-head-2222222"
	updateUpgradePRForTest(t, srv, author, map[string]any{
		"collab_id": upgrade.CollabID,
	})
	stale := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/collab/merge-gate?collab_id="+upgrade.CollabID, nil)
	if stale.Code != http.StatusOK {
		t.Fatalf("merge gate after head change status=%d body=%s", stale.Code, stale.Body.String())
	}
	var staleResp struct {
		PRHeadSHA            string   `json:"pr_head_sha"`
		ValidReviewersAtHead int      `json:"valid_reviewers_at_head"`
		ApprovalsAtHead      int      `json:"approvals_at_head"`
		ReviewComplete       bool     `json:"review_complete"`
		Mergeable            bool     `json:"mergeable"`
		Blockers             []string `json:"blockers"`
	}
	if err := json.Unmarshal(stale.Body.Bytes(), &staleResp); err != nil {
		t.Fatalf("decode merge gate after head change: %v", err)
	}
	if staleResp.PRHeadSHA != "sha-head-2222222" {
		t.Fatalf("merge gate should track the new head, got=%+v", staleResp)
	}
	if staleResp.ValidReviewersAtHead != 0 || staleResp.ApprovalsAtHead != 0 || staleResp.ReviewComplete || staleResp.Mergeable {
		t.Fatalf("old-head reviews should go stale on new head, got=%+v", staleResp)
	}
	if len(staleResp.Blockers) == 0 || !strings.Contains(strings.Join(staleResp.Blockers, "\n"), "need 2 valid reviewers at current head_sha") {
		t.Fatalf("stale head blockers mismatch, got=%+v", staleResp)
	}
}

func TestRunUpgradePRTickBacksOffOnGitHubRetryAfter(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 88)
	fixture.pull = githubPullRequestRecord{
		Number:  88,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Base.SHA = "sha-base-8888888"
	fixture.pull.Head.SHA = "sha-head-8888888"
	fixture.pull.Head.Ref = "feature/backoff"
	fixture.pull.User.Login = "author-login"

	upgrade := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "Upgrade PR backoff",
		"goal":    "Back off when GitHub rate limits review polling",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})
	if upgrade.Phase != "reviewing" {
		t.Fatalf("expected reviewing phase after propose, got=%s", upgrade.Phase)
	}

	pullPath := fmt.Sprintf("/repos/%s/pulls/%d", fixture.repo, fixture.number)
	fixture.requestHook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != pullPath || fixture.pullRequests <= 1 {
			return false
		}
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"message":"secondary rate limit"}`))
		return true
	}

	if err := srv.runUpgradePRTick(t.Context(), 1); err != nil {
		t.Fatalf("runUpgradePRTick should swallow rate limit after recording backoff, got=%v", err)
	}
	if retryAfter := srv.githubRateLimitRetryAfter(time.Now().UTC()); retryAfter < 110*time.Second {
		t.Fatalf("expected backoff near 120s, got=%s", retryAfter)
	}
	pullRequestsAfterRateLimit := fixture.pullRequests
	if pullRequestsAfterRateLimit < 2 {
		t.Fatalf("expected a second pull request call during tick, got=%d", pullRequestsAfterRateLimit)
	}

	if err := srv.runUpgradePRTick(t.Context(), 2); err != nil {
		t.Fatalf("runUpgradePRTick during backoff should skip GitHub polling, got=%v", err)
	}
	if fixture.pullRequests != pullRequestsAfterRateLimit {
		t.Fatalf("expected no extra GitHub pull fetches during backoff, got=%d want=%d", fixture.pullRequests, pullRequestsAfterRateLimit)
	}
}

func TestNewGitHubRateLimitErrorUsesResetHeader(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	resetAt := now.Add(3 * time.Minute).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("X-RateLimit-Reset", strconv.FormatInt(resetAt.Unix(), 10))
	err := newGitHubRateLimitError(http.StatusForbidden, headers, `{"message":"API rate limit exceeded"}`, now)
	if err == nil {
		t.Fatal("expected rate limit error")
	}
	if err.RetryAfter != 0 {
		t.Fatalf("expected retry-after to stay empty, got=%s", err.RetryAfter)
	}
	if !err.ResetAt.Equal(resetAt) {
		t.Fatalf("resetAt=%s want=%s", err.ResetAt, resetAt)
	}
	if !err.BackoffUntil(now).Equal(resetAt) {
		t.Fatalf("backoffUntil=%s want=%s", err.BackoffUntil(now), resetAt)
	}
}

func fixturesRepoOrDefault(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "agi-bar/clawcolony"
	}
	return repo
}
