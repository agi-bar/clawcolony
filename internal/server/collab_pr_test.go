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

func TestCollabUpgradePRRejectsDuplicateActivePRURL(t *testing.T) {
	srv := newTestServer()
	firstAuthor := newAuthUser(t, srv)
	secondAuthor := newAuthUser(t, srv)
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 43)
	fixture.pull = githubPullRequestRecord{
		Number:  43,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "head-sha-duplicate"
	fixture.pull.Head.Ref = "feature/duplicate-pr-guard"
	fixture.pull.Base.SHA = "base-sha-duplicate"
	fixture.pull.User.Login = "author-login"

	first := proposeCollabForTest(t, srv, firstAuthor, map[string]any{
		"title":   "First PR tracking collab",
		"goal":    "Track the shared open PR",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})

	duplicate := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/propose", map[string]any{
		"title":   "Duplicate PR tracking collab",
		"goal":    "Should be rejected because the PR is already tracked",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	}, secondAuthor.headers())
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate upgrade_pr should conflict, got=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	if !strings.Contains(duplicate.Body.String(), "upgrade_pr for this pr_url already exists") {
		t.Fatalf("duplicate response should explain active pr_url conflict, body=%s", duplicate.Body.String())
	}
	if !strings.Contains(duplicate.Body.String(), first.CollabID) {
		t.Fatalf("duplicate response should reference existing collab_id=%s body=%s", first.CollabID, duplicate.Body.String())
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

func TestCollabUpgradePRApplyAcceptsRoleCompatibility(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)
	reviewer := newAuthUser(t, srv)
	discussionUser := newAuthUser(t, srv)
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: proposer.id, GitHubUsername: "author-login"}); err != nil {
		t.Fatalf("upsert proposer github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewer.id, GitHubUsername: "reviewer-one"}); err != nil {
		t.Fatalf("upsert reviewer github username: %v", err)
	}
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 58)
	fixture.pull = githubPullRequestRecord{
		Number:  58,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "head-sha-compat-5858585"
	fixture.pull.Head.Ref = "feature/review-role-compat"
	fixture.pull.Base.SHA = "base-sha-compat-1111111"
	fixture.pull.User.Login = "author-login"

	upgrade := proposeCollabForTest(t, srv, proposer, map[string]any{
		"title":   "Review apply role compatibility",
		"goal":    "Keep legacy role-based apply payloads working",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})

	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRAppliedReview(9001, "reviewer-one", reviewer.id, "APPROVED", upgrade.CollabID, fixture.pull.Head.SHA, "agree", "looks good", "none", time.Now().Add(-1*time.Minute)),
	}

	reviewApply := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/apply", map[string]any{
		"collab_id":    upgrade.CollabID,
		"role":         "reviewer",
		"evidence_url": fixture.reviewURL(9001),
	}, reviewer.headers())
	if reviewApply.Code != http.StatusAccepted {
		t.Fatalf("review role-compat apply status=%d body=%s", reviewApply.Code, reviewApply.Body.String())
	}
	var reviewResp struct {
		Item store.CollabParticipant `json:"item"`
	}
	if err := json.Unmarshal(reviewApply.Body.Bytes(), &reviewResp); err != nil {
		t.Fatalf("decode review role-compat response: %v", err)
	}
	if !reviewResp.Item.Verified || reviewResp.Item.ApplicationKind != "review" || reviewResp.Item.Role != "reviewer" {
		t.Fatalf("review role-compat apply mismatch: %+v", reviewResp.Item)
	}

	discussionApply := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/apply", map[string]any{
		"collab_id": upgrade.CollabID,
		"role":      "discussion",
		"pitch":     "I have comments but no formal review.",
	}, discussionUser.headers())
	if discussionApply.Code != http.StatusAccepted {
		t.Fatalf("discussion role-compat apply status=%d body=%s", discussionApply.Code, discussionApply.Body.String())
	}
	var discussionResp struct {
		Item store.CollabParticipant `json:"item"`
	}
	if err := json.Unmarshal(discussionApply.Body.Bytes(), &discussionResp); err != nil {
		t.Fatalf("decode discussion role-compat response: %v", err)
	}
	if discussionResp.Item.ApplicationKind != "discussion" || discussionResp.Item.Role != "discussion" || discussionResp.Item.Verified {
		t.Fatalf("discussion role-compat apply mismatch: %+v", discussionResp.Item)
	}

	conflict := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/apply", map[string]any{
		"collab_id":        upgrade.CollabID,
		"application_kind": "review",
		"role":             "discussion",
		"evidence_url":     fixture.reviewURL(9001),
	}, reviewer.headers())
	if conflict.Code != http.StatusBadRequest || !strings.Contains(conflict.Body.String(), "role conflicts with application_kind=review") {
		t.Fatalf("conflicting role/application_kind should fail, got=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestSyncUpgradePRStateAutoRegistersStructuredGitHubReviews(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	reviewer := newAuthUser(t, srv)
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: author.id, GitHubUsername: "author-login"}); err != nil {
		t.Fatalf("upsert author github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewer.id, GitHubUsername: "reviewer-one"}); err != nil {
		t.Fatalf("upsert reviewer github username: %v", err)
	}
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 73)
	fixture.pull = githubPullRequestRecord{
		Number:  73,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Base.SHA = "sha-base-7373737"
	fixture.pull.Head.SHA = "sha-head-7373737"
	fixture.pull.Head.Ref = "feature/auto-register-reviewers"
	fixture.pull.User.Login = "author-login"

	upgrade := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "Auto-register reviewers",
		"goal":    "Pull GitHub review bodies into collab reviewers automatically",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})
	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRAppliedReview(7301, "reviewer-one", reviewer.id, "APPROVED", upgrade.CollabID, fixture.pull.Head.SHA, "agree", "ready to merge", "none", time.Now().Add(-2*time.Minute)),
	}

	beforeParts, err := srv.store.ListCollabParticipants(t.Context(), upgrade.CollabID, "", 20)
	if err != nil {
		t.Fatalf("list participants before sync: %v", err)
	}
	if len(beforeParts) != 1 || beforeParts[0].Role != "author" {
		t.Fatalf("expected only author before sync, got=%+v", beforeParts)
	}

	session, err := srv.store.GetCollabSession(t.Context(), upgrade.CollabID)
	if err != nil {
		t.Fatalf("reload collab before sync: %v", err)
	}
	if err := srv.syncUpgradePRState(t.Context(), session); err != nil {
		t.Fatalf("sync upgrade_pr state: %v", err)
	}

	afterParts, err := srv.store.ListCollabParticipants(t.Context(), upgrade.CollabID, "", 20)
	if err != nil {
		t.Fatalf("list participants after sync: %v", err)
	}
	if len(afterParts) != 2 {
		t.Fatalf("expected author plus auto-synced reviewer, got=%+v", afterParts)
	}
	var autoReviewer store.CollabParticipant
	for _, p := range afterParts {
		if p.UserID == reviewer.id {
			autoReviewer = p
			break
		}
	}
	if autoReviewer.UserID == "" {
		t.Fatalf("auto-synced reviewer missing from participants: %+v", afterParts)
	}
	if autoReviewer.Role != "reviewer" || autoReviewer.ApplicationKind != "review" || !autoReviewer.Verified {
		t.Fatalf("auto-synced reviewer mismatch: %+v", autoReviewer)
	}
	if autoReviewer.EvidenceURL != fixture.reviewURL(7301) || autoReviewer.GitHubLogin != "reviewer-one" {
		t.Fatalf("auto-synced reviewer evidence mismatch: %+v", autoReviewer)
	}

	mergeGate := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/collab/merge-gate?collab_id="+upgrade.CollabID, nil)
	if mergeGate.Code != http.StatusOK {
		t.Fatalf("merge gate status=%d body=%s", mergeGate.Code, mergeGate.Body.String())
	}
	var mergeGateResp struct {
		ValidReviewersAtHead int  `json:"valid_reviewers_at_head"`
		ApprovalsAtHead      int  `json:"approvals_at_head"`
		ReviewComplete       bool `json:"review_complete"`
		Mergeable            bool `json:"mergeable"`
	}
	if err := json.Unmarshal(mergeGate.Body.Bytes(), &mergeGateResp); err != nil {
		t.Fatalf("decode merge gate response: %v", err)
	}
	if mergeGateResp.ValidReviewersAtHead != 1 || mergeGateResp.ApprovalsAtHead != 1 {
		t.Fatalf("merge gate should count the auto-synced review, got=%+v", mergeGateResp)
	}
	if mergeGateResp.ReviewComplete || mergeGateResp.Mergeable {
		t.Fatalf("one reviewer should not satisfy the default gate, got=%+v", mergeGateResp)
	}
}

func TestSyncUpgradePRStateAutoRegistersStructuredGitHubCommentsButLegacyCommentsDoNotCountGate(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	reviewerOne := newAuthUser(t, srv)
	reviewerTwo := newAuthUser(t, srv)
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: author.id, GitHubUsername: "author-login"}); err != nil {
		t.Fatalf("upsert author github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewerOne.id, GitHubUsername: "reviewer-one"}); err != nil {
		t.Fatalf("upsert reviewer one github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewerTwo.id, GitHubUsername: "reviewer-two"}); err != nil {
		t.Fatalf("upsert reviewer two github username: %v", err)
	}
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 74)
	fixture.pull = githubPullRequestRecord{
		Number:  74,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Base.SHA = "sha-base-7474747"
	fixture.pull.Head.SHA = "sha-head-7474747"
	fixture.pull.Head.Ref = "feature/comment-autosync"
	fixture.pull.User.Login = "author-login"

	upgrade := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "Auto-register comment reviewers",
		"goal":    "Count structured GitHub comments and ignore legacy note-only gate signals",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})
	fixture.comments[7401] = makeStructuredUpgradePRReviewComment(fixturesRepoOrDefault(fixture.repo), fixture.number, 7401, "reviewer-one", upgrade.CollabID, reviewerOne.id, fixture.pull.Head.SHA, "agree", "comment approval", "none", time.Now().Add(-2*time.Minute))
	fixture.comments[7402] = makeUpgradePRApplyComment(fixturesRepoOrDefault(fixture.repo), fixture.number, 7402, "reviewer-two", upgrade.CollabID, reviewerTwo.id, "legacy note only")

	session, err := srv.store.GetCollabSession(t.Context(), upgrade.CollabID)
	if err != nil {
		t.Fatalf("reload collab before sync: %v", err)
	}
	if err := srv.syncUpgradePRState(t.Context(), session); err != nil {
		t.Fatalf("sync upgrade_pr state: %v", err)
	}

	parts, err := srv.store.ListCollabParticipants(t.Context(), upgrade.CollabID, "", 20)
	if err != nil {
		t.Fatalf("list participants after sync: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("expected author plus two auto-synced reviewers, got=%+v", parts)
	}

	mergeGate := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/collab/merge-gate?collab_id="+upgrade.CollabID, nil)
	if mergeGate.Code != http.StatusOK {
		t.Fatalf("merge gate status=%d body=%s", mergeGate.Code, mergeGate.Body.String())
	}
	var resp struct {
		ValidReviewersAtHead        int  `json:"valid_reviewers_at_head"`
		ApprovalsAtHead             int  `json:"approvals_at_head"`
		FormalValidReviewersAtHead  int  `json:"formal_valid_reviewers_at_head"`
		CommentValidReviewersAtHead int  `json:"comment_valid_reviewers_at_head"`
		CommentApprovalsAtHead      int  `json:"comment_approvals_at_head"`
		ReviewComplete              bool `json:"review_complete"`
		Mergeable                   bool `json:"mergeable"`
	}
	if err := json.Unmarshal(mergeGate.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode merge gate response: %v", err)
	}
	if resp.ValidReviewersAtHead != 1 || resp.ApprovalsAtHead != 1 {
		t.Fatalf("structured comment should count as one approval, got=%+v", resp)
	}
	if resp.FormalValidReviewersAtHead != 0 || resp.CommentValidReviewersAtHead != 1 || resp.CommentApprovalsAtHead != 1 {
		t.Fatalf("comment/formal split mismatch, got=%+v", resp)
	}
	if resp.ReviewComplete || resp.Mergeable {
		t.Fatalf("one structured comment plus one legacy note should not satisfy gate, got=%+v", resp)
	}
}

func TestCollabUpgradePRMergeGateCombinesFormalReviewsAndStructuredCommentsUsingLatestSignal(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	reviewerOne := newAuthUser(t, srv)
	reviewerTwo := newAuthUser(t, srv)
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: author.id, GitHubUsername: "author-login"}); err != nil {
		t.Fatalf("upsert author github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewerOne.id, GitHubUsername: "reviewer-one"}); err != nil {
		t.Fatalf("upsert reviewer one github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewerTwo.id, GitHubUsername: "reviewer-two"}); err != nil {
		t.Fatalf("upsert reviewer two github username: %v", err)
	}
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 75)
	fixture.pull = githubPullRequestRecord{
		Number:  75,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Base.SHA = "sha-base-7575757"
	fixture.pull.Head.SHA = "sha-head-7575757"
	fixture.pull.Head.Ref = "feature/mixed-review-sources"
	fixture.pull.User.Login = "author-login"

	upgrade := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "Mixed review sources",
		"goal":    "Combine formal GitHub reviews and structured comments with latest signal wins",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})
	head := fixture.pull.Head.SHA
	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRAppliedReview(7501, "reviewer-one", reviewerOne.id, "APPROVED", upgrade.CollabID, head, "agree", "formal approve", "none", time.Now().Add(-5*time.Minute)),
		makeUpgradePRAppliedReview(7502, "reviewer-two", reviewerTwo.id, "COMMENTED", upgrade.CollabID, head, "disagree", "formal disagree", "key issue", time.Now().Add(-4*time.Minute)),
	}

	before := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/collab/merge-gate?collab_id="+upgrade.CollabID, nil)
	if before.Code != http.StatusOK {
		t.Fatalf("merge gate before comment status=%d body=%s", before.Code, before.Body.String())
	}
	var beforeResp struct {
		ValidReviewersAtHead       int  `json:"valid_reviewers_at_head"`
		ApprovalsAtHead            int  `json:"approvals_at_head"`
		DisagreementsAtHead        int  `json:"disagreements_at_head"`
		FormalApprovalsAtHead      int  `json:"formal_approvals_at_head"`
		FormalDisagreementsAtHead  int  `json:"formal_disagreements_at_head"`
		CommentApprovalsAtHead     int  `json:"comment_approvals_at_head"`
		CommentDisagreementsAtHead int  `json:"comment_disagreements_at_head"`
		Mergeable                  bool `json:"mergeable"`
	}
	if err := json.Unmarshal(before.Body.Bytes(), &beforeResp); err != nil {
		t.Fatalf("decode merge gate before comment: %v", err)
	}
	if beforeResp.ValidReviewersAtHead != 2 || beforeResp.ApprovalsAtHead != 1 || beforeResp.DisagreementsAtHead != 1 || beforeResp.FormalApprovalsAtHead != 1 || beforeResp.FormalDisagreementsAtHead != 1 || beforeResp.CommentApprovalsAtHead != 0 || beforeResp.CommentDisagreementsAtHead != 0 || beforeResp.Mergeable {
		t.Fatalf("formal-only merge gate mismatch: %+v", beforeResp)
	}

	fixture.comments[7503] = makeStructuredUpgradePRReviewComment(fixturesRepoOrDefault(fixture.repo), fixture.number, 7503, "reviewer-two", upgrade.CollabID, reviewerTwo.id, head, "agree", "comment override", "none", time.Now().Add(-1*time.Minute))

	after := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/collab/merge-gate?collab_id="+upgrade.CollabID, nil)
	if after.Code != http.StatusOK {
		t.Fatalf("merge gate after comment status=%d body=%s", after.Code, after.Body.String())
	}
	var afterResp struct {
		ValidReviewersAtHead       int  `json:"valid_reviewers_at_head"`
		ApprovalsAtHead            int  `json:"approvals_at_head"`
		DisagreementsAtHead        int  `json:"disagreements_at_head"`
		FormalApprovalsAtHead      int  `json:"formal_approvals_at_head"`
		FormalDisagreementsAtHead  int  `json:"formal_disagreements_at_head"`
		CommentApprovalsAtHead     int  `json:"comment_approvals_at_head"`
		CommentDisagreementsAtHead int  `json:"comment_disagreements_at_head"`
		Mergeable                  bool `json:"mergeable"`
	}
	if err := json.Unmarshal(after.Body.Bytes(), &afterResp); err != nil {
		t.Fatalf("decode merge gate after comment: %v", err)
	}
	if afterResp.ValidReviewersAtHead != 2 || afterResp.ApprovalsAtHead != 2 || afterResp.DisagreementsAtHead != 0 || afterResp.FormalApprovalsAtHead != 1 || afterResp.FormalDisagreementsAtHead != 0 || afterResp.CommentApprovalsAtHead != 1 || afterResp.CommentDisagreementsAtHead != 0 || !afterResp.Mergeable {
		t.Fatalf("mixed-source merge gate mismatch: %+v", afterResp)
	}
}

func TestCollabUpgradePRMergeGateUsesGitHubReviewsAndStaleHeads(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	reviewerOne := newAuthUser(t, srv)
	reviewerTwo := newAuthUser(t, srv)
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: author.id, GitHubUsername: "author-login"}); err != nil {
		t.Fatalf("upsert author github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewerOne.id, GitHubUsername: "reviewer-one"}); err != nil {
		t.Fatalf("upsert reviewer one github username: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: reviewerTwo.id, GitHubUsername: "reviewer-two"}); err != nil {
		t.Fatalf("upsert reviewer two github username: %v", err)
	}
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
		makeUpgradePRAppliedReview(1, "reviewer-one", reviewerOne.id, "APPROVED", upgrade.CollabID, headOne, "agree", "looks good", "none", time.Now().Add(-5*time.Minute)),
		makeUpgradePRAppliedReview(2, "reviewer-two", reviewerTwo.id, "COMMENTED", upgrade.CollabID, headOne, "disagree", "needs one change", "key issue", time.Now().Add(-4*time.Minute)),
		makeUpgradePRAppliedReview(3, "author-login", author.id, "APPROVED", upgrade.CollabID, headOne, "agree", "self approval", "none", time.Now().Add(-3*time.Minute)),
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

	fixture.reviews = append(fixture.reviews, makeUpgradePRAppliedReview(4, "reviewer-two", reviewerTwo.id, "APPROVED", upgrade.CollabID, headOne, "agree", "follow-up agree", "none", time.Now().Add(-2*time.Minute)))
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

func TestCollabCloseUpgradePRUsesGitHubTerminalState(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: author.id, GitHubUsername: "author-login"}); err != nil {
		t.Fatalf("upsert author github username: %v", err)
	}
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 78)
	fixture.pull = githubPullRequestRecord{
		Number:  78,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Base.SHA = "sha-base-close"
	fixture.pull.Head.SHA = "sha-head-close"
	fixture.pull.Head.Ref = "feature/close-github-truth"
	fixture.pull.User.Login = "author-login"

	upgrade := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "GitHub close truth",
		"goal":    "Ensure collab/close follows GitHub terminal state",
		"kind":    "upgrade_pr",
		"pr_repo": fixture.repo,
		"pr_url":  fixture.pullURL(),
	})

	openClose := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/close", map[string]any{
		"collab_id": upgrade.CollabID,
		"result":    "closed",
	}, author.headers())
	if openClose.Code != http.StatusConflict {
		t.Fatalf("open PR close should conflict, got=%d body=%s", openClose.Code, openClose.Body.String())
	}

	fixture.pull.State = "closed"
	fixture.pull.Merged = false
	fixture.pull.MergeCommitSHA = ""
	closedResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/close", map[string]any{
		"collab_id": upgrade.CollabID,
		"result":    "closed",
	}, author.headers())
	if closedResp.Code != http.StatusAccepted {
		t.Fatalf("closed PR close status=%d body=%s", closedResp.Code, closedResp.Body.String())
	}
	var closedPayload struct {
		Item store.CollabSession `json:"item"`
	}
	if err := json.Unmarshal(closedResp.Body.Bytes(), &closedPayload); err != nil {
		t.Fatalf("decode closed response: %v", err)
	}
	if closedPayload.Item.Phase != "failed" || closedPayload.Item.GitHubPRState != "closed" {
		t.Fatalf("closed unmerged PR should land in failed, got=%+v", closedPayload.Item)
	}
}

func TestSyncUpgradePRStateReopensTerminalCollabAndExtendsDeadline(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{UserID: author.id, GitHubUsername: "author-login"}); err != nil {
		t.Fatalf("upsert author github username: %v", err)
	}
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 79)
	fixture.pull = githubPullRequestRecord{
		Number:  79,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Base.SHA = "sha-base-reopen"
	fixture.pull.Head.SHA = "sha-head-reopen"
	fixture.pull.Head.Ref = "feature/reopen-runtime"
	fixture.pull.User.Login = "author-login"

	upgrade := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "Reopen recovery",
		"goal":    "Restore terminal collabs to reviewing when GitHub reopens the PR",
		"kind":    "upgrade_pr",
		"pr_repo": fixture.repo,
		"pr_url":  fixture.pullURL(),
	})
	pastDeadline := time.Now().UTC().Add(-2 * time.Hour)
	if _, err := srv.store.UpdateCollabPR(t.Context(), store.CollabPRUpdate{
		CollabID:         upgrade.CollabID,
		PRURL:            fixture.pullURL(),
		PRNumber:         fixture.number,
		PRBaseSHA:        fixture.pull.Base.SHA,
		PRHeadSHA:        fixture.pull.Head.SHA,
		PRAuthorLogin:    fixture.pull.User.Login,
		GitHubPRState:    "closed",
		ReviewDeadlineAt: &pastDeadline,
	}); err != nil {
		t.Fatalf("mark upgrade_pr closed in store: %v", err)
	}
	closedAt := time.Now().UTC().Add(-30 * time.Minute)
	if _, err := srv.store.UpdateCollabPhase(t.Context(), upgrade.CollabID, "failed", author.id, "previous close", &closedAt); err != nil {
		t.Fatalf("mark upgrade_pr failed in store: %v", err)
	}

	session, err := srv.store.GetCollabSession(t.Context(), upgrade.CollabID)
	if err != nil {
		t.Fatalf("reload collab before resync: %v", err)
	}
	if err := srv.syncUpgradePRState(t.Context(), session); err != nil {
		t.Fatalf("sync reopened upgrade_pr: %v", err)
	}

	after, err := srv.store.GetCollabSession(t.Context(), upgrade.CollabID)
	if err != nil {
		t.Fatalf("reload collab after resync: %v", err)
	}
	if after.Phase != "reviewing" || after.GitHubPRState != "open" {
		t.Fatalf("reopened PR should restore reviewing phase, got=%+v", after)
	}
	if after.ClosedAt != nil {
		t.Fatalf("reopened PR should clear closed_at, got=%v", after.ClosedAt)
	}
	if after.ReviewDeadlineAt == nil || after.ReviewDeadlineAt.Before(time.Now().UTC().Add(23*time.Hour)) {
		t.Fatalf("reopened PR should have a refreshed deadline, got=%v", after.ReviewDeadlineAt)
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
