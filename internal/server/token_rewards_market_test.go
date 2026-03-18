package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/economy"
	"clawcolony/internal/store"
)

func tokenBalanceForUser(t *testing.T, srv *Server, userID string) int64 {
	t.Helper()
	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/token/accounts?user_id="+userID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("token account status=%d body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Item store.TokenAccount `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal token account: %v", err)
	}
	return payload.Item.Balance
}

func knowledgeRewardForContent(srv *Server, content string, existingSameCategory int) int64 {
	tokens := economy.CalculateToken(content)
	lengthMilli := (tokens * 1000) / 2000
	if lengthMilli > 3000 {
		lengthMilli = 3000
	}
	return (srv.tokenPolicy().BaseKnowledgeReward * lengthMilli * economy.ScarcityMultiplier(existingSameCategory)) / 1_000_000
}

func seedProposalKnowledgeMetaForTest(t *testing.T, srv *Server, proposalID int64, authorUserID, category, content string, refs []citationRef) {
	t.Helper()
	if err := srv.upsertProposalKnowledgeMeta(context.Background(), proposalID, knowledgeMeta{
		ProposalID:    proposalID,
		Category:      category,
		References:    normalizeCitationRefs(refs),
		AuthorUserID:  authorUserID,
		ContentTokens: economy.CalculateToken(content),
	}); err != nil {
		t.Fatalf("seed proposal knowledge meta: %v", err)
	}
}
func setupUpgradePRRewardFlowForTest(t *testing.T, srv *Server, fixture *fakeUpgradePRGitHub, author, reviewerOne, reviewerTwo authUser) store.CollabSession {
	t.Helper()
	collab := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "Rewarded upgrade PR",
		"goal":    "Exercise runtime upgrade_pr rewards",
		"kind":    "upgrade_pr",
		"pr_repo": fixture.repo,
		"pr_url":  fixture.pullURL(),
	})
	collab = updateUpgradePRForTest(t, srv, author, map[string]any{
		"collab_id": collab.CollabID,
		"pr_branch": "feature/rewarded-upgrade",
	})
	fixture.comments[2001] = makeUpgradePRApplyComment(fixturesRepoOrDefault(fixture.repo), fixture.number, 2001, "reviewer-one", collab.CollabID, reviewerOne.id, "reviewer one")
	fixture.comments[2002] = makeUpgradePRApplyComment(fixturesRepoOrDefault(fixture.repo), fixture.number, 2002, "reviewer-two", collab.CollabID, reviewerTwo.id, "reviewer two")
	applyUpgradePRReviewForTest(t, srv, reviewerOne, collab.CollabID, fixture.commentURL(2001))
	applyUpgradePRReviewForTest(t, srv, reviewerTwo, collab.CollabID, fixture.commentURL(2002))
	return collab
}

func TestKBProposalApplyGrantsCommunityReward(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := seedActiveUser(t, srv)
	applier, applierAPIKey := seedActiveUserWithAPIKey(t, srv)
	content := strings.Repeat("k", 500)

	proposal, _, err := srv.store.CreateKBProposal(ctx, store.KBProposal{
		ProposerUserID:    proposer,
		Title:             "Shared KB upgrade",
		Reason:            "ship shared knowledge",
		Status:            "discussing",
		VoteThresholdPct:  80,
		VoteWindowSeconds: 300,
	}, store.KBProposalChange{
		OpType:     "add",
		Section:    "knowledge/runtime",
		Title:      "rewarded entry",
		NewContent: content,
		DiffText:   "+ rewarded knowledge content",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	seedProposalKnowledgeMetaForTest(t, srv, proposal.ID, proposer, "knowledge", content, nil)
	if _, err := srv.store.CloseKBProposal(ctx, proposal.ID, "approved", "ok", 1, 1, 0, 0, 1, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/apply", map[string]any{
		"proposal_id": proposal.ID,
	}, apiKeyHeaders(applierAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("apply status=%d user=%s body=%s", w.Code, applier, w.Body.String())
	}
	wantReward := knowledgeRewardForContent(srv, content, 0)
	if tokenBalanceForUser(t, srv, proposer) != 1000+wantReward {
		t.Fatalf("proposer should receive kb reward, body=%s", w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/apply", map[string]any{
		"proposal_id": proposal.ID,
	}, apiKeyHeaders(applierAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("reapply status=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, proposer) != 1000+wantReward {
		t.Fatalf("kb reward should be idempotent, body=%s", w.Body.String())
	}
}

func TestKBProposalApplyBackfillsLegacyKnowledgeMeta(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := seedActiveUser(t, srv)
	_, applierAPIKey := seedActiveUserWithAPIKey(t, srv)
	content := strings.Repeat("legacy-", 100)

	proposal, _, err := srv.store.CreateKBProposal(ctx, store.KBProposal{
		ProposerUserID:    proposer,
		Title:             "Legacy KB upgrade",
		Reason:            "carry forward approved work",
		Status:            "discussing",
		VoteThresholdPct:  80,
		VoteWindowSeconds: 300,
	}, store.KBProposalChange{
		OpType:     "add",
		Section:    "knowledge/runtime",
		Title:      "legacy entry",
		NewContent: content,
		DiffText:   "+ legacy knowledge content",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	if _, err := srv.store.CloseKBProposal(ctx, proposal.ID, "approved", "ok", 1, 1, 0, 0, 1, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/apply", map[string]any{
		"proposal_id": proposal.ID,
	}, apiKeyHeaders(applierAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("apply legacy proposal status=%d body=%s", w.Code, w.Body.String())
	}
	wantReward := knowledgeRewardForContent(srv, content, 0)
	if got := tokenBalanceForUser(t, srv, proposer); got != 1000+wantReward {
		t.Fatalf("legacy proposer balance=%d want %d body=%s", got, 1000+wantReward, w.Body.String())
	}

	var resp struct {
		Entry store.KBEntry `json:"entry"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode legacy apply response: %v", err)
	}
	entryMeta, err := srv.store.GetEconomyKnowledgeMetaByEntry(ctx, resp.Entry.ID)
	if err != nil {
		t.Fatalf("get migrated entry knowledge meta: %v", err)
	}
	if entryMeta.Category != "knowledge" || entryMeta.AuthorUserID != proposer {
		t.Fatalf("unexpected migrated entry knowledge meta: %+v", entryMeta)
	}
}

func TestCollabCloseGrantsCommunityRewardToAcceptedAuthors(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	orchestrator, orchestratorAPIKey := seedActiveUserWithAPIKey(t, srv)
	authorA := seedActiveUser(t, srv)
	authorB := seedActiveUser(t, srv)

	session, err := srv.store.CreateCollabSession(ctx, store.CollabSession{
		CollabID:           "collab-reward",
		Title:              "Shared collab",
		Goal:               "produce shared artifact",
		Complexity:         "m",
		Phase:              "reviewing",
		ProposerUserID:     orchestrator,
		OrchestratorUserID: orchestrator,
		MinMembers:         1,
		MaxMembers:         3,
	})
	if err != nil {
		t.Fatalf("create collab: %v", err)
	}
	a1, err := srv.store.CreateCollabArtifact(ctx, store.CollabArtifact{
		CollabID: session.CollabID,
		UserID:   authorA,
		Role:     "builder",
		Kind:     "spec",
		Summary:  "accepted-a",
		Content:  "evidence/result/next",
		Status:   "submitted",
	})
	if err != nil {
		t.Fatalf("artifact a: %v", err)
	}
	a2, err := srv.store.CreateCollabArtifact(ctx, store.CollabArtifact{
		CollabID: session.CollabID,
		UserID:   authorB,
		Role:     "reviewer",
		Kind:     "report",
		Summary:  "accepted-b",
		Content:  "evidence/result/next",
		Status:   "submitted",
	})
	if err != nil {
		t.Fatalf("artifact b: %v", err)
	}
	if _, err := srv.store.UpdateCollabArtifactReview(ctx, a1.ID, "accepted", "ok"); err != nil {
		t.Fatalf("accept artifact a: %v", err)
	}
	if _, err := srv.store.UpdateCollabArtifactReview(ctx, a2.ID, "accepted", "ok"); err != nil {
		t.Fatalf("accept artifact b: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/close", map[string]any{
		"collab_id":              session.CollabID,
		"result":                 "closed",
		"status_or_summary_note": "done",
	}, apiKeyHeaders(orchestratorAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("close collab status=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, authorA) != 1000 {
		t.Fatalf("authorA should not receive legacy collab reward under v2 body=%s", w.Body.String())
	}
	if tokenBalanceForUser(t, srv, authorB) != 1000 {
		t.Fatalf("authorB should not receive legacy collab reward under v2 body=%s", w.Body.String())
	}
}

func TestCollabCloseRewardsEachAcceptedArtifact(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	orchestrator, orchestratorAPIKey := seedActiveUserWithAPIKey(t, srv)
	author := seedActiveUser(t, srv)

	session, err := srv.store.CreateCollabSession(ctx, store.CollabSession{
		CollabID:           "collab-repeat-author",
		Title:              "multi artifact close",
		Goal:               "ship two artifacts",
		Complexity:         "m",
		Phase:              "reviewing",
		ProposerUserID:     orchestrator,
		OrchestratorUserID: orchestrator,
		MinMembers:         1,
		MaxMembers:         2,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for i := 0; i < 2; i++ {
		artifact, err := srv.store.CreateCollabArtifact(ctx, store.CollabArtifact{
			CollabID: session.CollabID,
			UserID:   author,
			Role:     "builder",
			Kind:     "spec",
			Summary:  "accepted artifact",
			Content:  "evidence/result/next",
			Status:   "submitted",
		})
		if err != nil {
			t.Fatalf("create artifact %d: %v", i+1, err)
		}
		if _, err := srv.store.UpdateCollabArtifactReview(ctx, artifact.ID, "accepted", "ok"); err != nil {
			t.Fatalf("accept artifact %d: %v", i+1, err)
		}
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/close", map[string]any{
		"collab_id":              session.CollabID,
		"result":                 "closed",
		"status_or_summary_note": "done",
	}, apiKeyHeaders(orchestratorAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("close collab status=%d body=%s", w.Code, w.Body.String())
	}
	want := int64(1000)
	if got := tokenBalanceForUser(t, srv, author); got != want {
		t.Fatalf("author balance=%d want %d body=%s", got, want, w.Body.String())
	}
}

func TestUpgradePRCloseDoesNotGrantLegacyCollabReward(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 90)
	fixture.pull = githubPullRequestRecord{
		Number:  90,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "sha-head-close-guard"
	fixture.pull.Base.SHA = "sha-base-close-guard"
	fixture.pull.User.Login = "author-login"

	collab := proposeCollabForTest(t, srv, author, map[string]any{
		"title":   "Upgrade PR legacy reward guard",
		"goal":    "Ensure collab.close reward no longer applies",
		"kind":    "upgrade_pr",
		"pr_repo": "agi-bar/clawcolony",
		"pr_url":  fixture.pullURL(),
	})
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/close", map[string]any{
		"collab_id":              collab.CollabID,
		"result":                 "closed",
		"status_or_summary_note": "manual close",
	}, author.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("upgrade_pr close status=%d body=%s", w.Code, w.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, author.id); got != 1000 {
		t.Fatalf("upgrade_pr close should not mint legacy collab reward, got balance=%d body=%s", got, w.Body.String())
	}
}

func TestUpgradePRMergedAutoRewardsAuthorAndReviewers(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	author := newAuthUser(t, srv)
	reviewerOne := newAuthUser(t, srv)
	reviewerTwo := newAuthUser(t, srv)
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 91)
	fixture.pull = githubPullRequestRecord{
		Number:  91,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "sha-head-merged"
	fixture.pull.Base.SHA = "sha-base-merged"
	fixture.pull.User.Login = "author-login"

	collab := setupUpgradePRRewardFlowForTest(t, srv, fixture, author, reviewerOne, reviewerTwo)
	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRReview(1, "reviewer-one", "APPROVED", collab.CollabID, fixture.pull.Head.SHA, "agree", "ship it", "none", time.Now().Add(-5*time.Minute)),
		makeUpgradePRReview(2, "reviewer-two", "COMMENTED", collab.CollabID, fixture.pull.Head.SHA, "disagree", "one concern", "key issue", time.Now().Add(-4*time.Minute)),
	}
	mergedAt := time.Now().UTC()
	fixture.pull.State = "closed"
	fixture.pull.Merged = true
	fixture.pull.MergeCommitSHA = "merge-commit-123"
	fixture.pull.MergedAt = &mergedAt

	session, err := srv.store.GetCollabSession(ctx, collab.CollabID)
	if err != nil {
		t.Fatalf("reload collab before sync: %v", err)
	}
	if err := srv.syncUpgradePRState(ctx, session); err != nil {
		t.Fatalf("sync merged upgrade_pr: %v", err)
	}

	after, err := srv.store.GetCollabSession(ctx, collab.CollabID)
	if err != nil {
		t.Fatalf("reload collab after sync: %v", err)
	}
	if after.Phase != "closed" || after.GitHubPRState != "merged" {
		t.Fatalf("merged upgrade_pr should auto-close, got=%+v", after)
	}
	if got := tokenBalanceForUser(t, srv, author.id); got != 1000+communityRewardAmountUpgradePRAuthor {
		t.Fatalf("author merged reward mismatch balance=%d", got)
	}
	if got := tokenBalanceForUser(t, srv, reviewerOne.id); got != 1000+communityRewardAmountUpgradePRReviewer {
		t.Fatalf("reviewer one reward mismatch balance=%d", got)
	}
	if got := tokenBalanceForUser(t, srv, reviewerTwo.id); got != 1000+communityRewardAmountUpgradePRReviewer {
		t.Fatalf("reviewer two should be rewarded for valid disagree review balance=%d", got)
	}
}

func TestUpgradePRClosedWithoutMergeRewardsReviewersOnly(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	author := newAuthUser(t, srv)
	reviewerOne := newAuthUser(t, srv)
	reviewerTwo := newAuthUser(t, srv)
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 92)
	fixture.pull = githubPullRequestRecord{
		Number:  92,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "sha-head-closed"
	fixture.pull.Base.SHA = "sha-base-closed"
	fixture.pull.User.Login = "author-login"

	collab := setupUpgradePRRewardFlowForTest(t, srv, fixture, author, reviewerOne, reviewerTwo)
	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRReview(1, "reviewer-one", "APPROVED", collab.CollabID, fixture.pull.Head.SHA, "agree", "ready before close", "none", time.Now().Add(-5*time.Minute)),
		makeUpgradePRReview(2, "reviewer-two", "CHANGES_REQUESTED", collab.CollabID, fixture.pull.Head.SHA, "disagree", "blocking issue", "key issue", time.Now().Add(-4*time.Minute)),
	}
	fixture.pull.State = "closed"
	fixture.pull.Merged = false
	fixture.pull.MergeCommitSHA = ""
	fixture.pull.MergedAt = nil

	session, err := srv.store.GetCollabSession(ctx, collab.CollabID)
	if err != nil {
		t.Fatalf("reload collab before sync: %v", err)
	}
	if err := srv.syncUpgradePRState(ctx, session); err != nil {
		t.Fatalf("sync closed upgrade_pr: %v", err)
	}

	after, err := srv.store.GetCollabSession(ctx, collab.CollabID)
	if err != nil {
		t.Fatalf("reload collab after sync: %v", err)
	}
	if after.Phase != "failed" || after.GitHubPRState != "closed" {
		t.Fatalf("closed unmerged upgrade_pr should fail, got=%+v", after)
	}
	if got := tokenBalanceForUser(t, srv, author.id); got != 1000 {
		t.Fatalf("author should not receive merge reward on failed PR, balance=%d", got)
	}
	if got := tokenBalanceForUser(t, srv, reviewerOne.id); got != 1000+communityRewardAmountUpgradePRReviewer {
		t.Fatalf("reviewer one failed-terminal reward mismatch balance=%d", got)
	}
	if got := tokenBalanceForUser(t, srv, reviewerTwo.id); got != 1000+communityRewardAmountUpgradePRReviewer {
		t.Fatalf("reviewer two failed-terminal reward mismatch balance=%d", got)
	}
}

func TestUpgradePRClaimReturnsFallbackRewardForEligibleUser(t *testing.T) {
	srv := newTestServer()
	author := newAuthUser(t, srv)
	reviewerOne := newAuthUser(t, srv)
	reviewerTwo := newAuthUser(t, srv)
	outsider := newAuthUser(t, srv)
	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 93)
	fixture.pull = githubPullRequestRecord{
		Number:  93,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "sha-head-claim"
	fixture.pull.Base.SHA = "sha-base-claim"
	fixture.pull.User.Login = "author-login"

	collab := setupUpgradePRRewardFlowForTest(t, srv, fixture, author, reviewerOne, reviewerTwo)
	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRReview(1, "reviewer-one", "APPROVED", collab.CollabID, fixture.pull.Head.SHA, "agree", "ready", "none", time.Now().Add(-5*time.Minute)),
		makeUpgradePRReview(2, "reviewer-two", "COMMENTED", collab.CollabID, fixture.pull.Head.SHA, "disagree", "one objection", "key issue", time.Now().Add(-4*time.Minute)),
	}
	mergedAt := time.Now().UTC()
	fixture.pull.State = "closed"
	fixture.pull.Merged = true
	fixture.pull.MergeCommitSHA = "merge-claim-123"
	fixture.pull.MergedAt = &mergedAt

	authorClaim := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/reward/upgrade-pr-claim", map[string]any{
		"collab_id":        collab.CollabID,
		"pr_url":           fixture.pullURL(),
		"merge_commit_sha": "merge-claim-123",
	}, author.headers())
	if authorClaim.Code != http.StatusAccepted {
		t.Fatalf("author claim status=%d body=%s", authorClaim.Code, authorClaim.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, author.id); got != 1000+communityRewardAmountUpgradePRAuthor {
		t.Fatalf("author fallback claim balance mismatch=%d body=%s", got, authorClaim.Body.String())
	}

	reviewerClaim := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/reward/upgrade-pr-claim", map[string]any{
		"collab_id": collab.CollabID,
	}, reviewerTwo.headers())
	if reviewerClaim.Code != http.StatusAccepted {
		t.Fatalf("reviewer claim status=%d body=%s", reviewerClaim.Code, reviewerClaim.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, reviewerTwo.id); got != 1000+communityRewardAmountUpgradePRReviewer {
		t.Fatalf("reviewer fallback claim balance mismatch=%d body=%s", got, reviewerClaim.Body.String())
	}

	noClaim := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/reward/upgrade-pr-claim", map[string]any{
		"collab_id": collab.CollabID,
	}, outsider.headers())
	if noClaim.Code != http.StatusConflict || !strings.Contains(noClaim.Body.String(), "no claimable reward") {
		t.Fatalf("non-participant claim should fail, got=%d body=%s", noClaim.Code, noClaim.Body.String())
	}
}

func TestBountyVerifyApprovedGrantsCommunityReward(t *testing.T) {
	srv := newTestServer()
	_, posterAPIKey := seedActiveUserWithAPIKey(t, srv)
	claimer, claimerAPIKey := seedActiveUserWithAPIKey(t, srv)

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/bounty/post", map[string]any{
		"description": "ship shared fix",
		"reward":      50,
		"criteria":    "merged and shared",
	}, apiKeyHeaders(posterAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("post bounty status=%d body=%s", w.Code, w.Body.String())
	}
	var post struct {
		Item struct {
			BountyID int64 `json:"bounty_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &post); err != nil {
		t.Fatalf("unmarshal bounty: %v", err)
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/bounty/claim", map[string]any{
		"bounty_id": post.Item.BountyID,
	}, apiKeyHeaders(claimerAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("claim bounty status=%d user=%s body=%s", w.Code, claimer, w.Body.String())
	}
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/bounty/verify", map[string]any{
		"bounty_id": post.Item.BountyID,
		"approved":  true,
	}, apiKeyHeaders(posterAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("verify bounty status=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, claimer) != 1000+50 {
		t.Fatalf("claimer should receive escrow only under v2 body=%s", w.Body.String())
	}
}

func TestGangliaIntegrateGrantsCommunityRewardToAuthor(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	author := seedActiveUser(t, srv)
	integrator, integratorAPIKey := seedActiveUserWithAPIKey(t, srv)

	ganglion, err := srv.store.CreateGanglion(ctx, store.Ganglion{
		Name:           "shared-protocol",
		GanglionType:   "workflow",
		Description:    "shared",
		Implementation: "steps",
		Validation:     "tests",
		AuthorUserID:   author,
		Temporality:    "durable",
		LifeState:      "alive",
	})
	if err != nil {
		t.Fatalf("create ganglion: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/ganglia/integrate", map[string]any{
		"ganglion_id": ganglion.ID,
	}, apiKeyHeaders(integratorAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("integrate ganglion status=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, author) != 1000+communityRewardAmountGanglia {
		t.Fatalf("author should receive ganglia reward body=%s", w.Body.String())
	}
	if tokenBalanceForUser(t, srv, integrator) != 1000 {
		t.Fatalf("integrator balance should not change body=%s", w.Body.String())
	}
}

func TestGangliaIntegrateSkipsSelfIntegrationReward(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	author, authorAPIKey := seedActiveUserWithAPIKey(t, srv)

	ganglion, err := srv.store.CreateGanglion(ctx, store.Ganglion{
		Name:           "self-integrated-protocol",
		GanglionType:   "workflow",
		Description:    "shared",
		Implementation: "steps",
		Validation:     "tests",
		AuthorUserID:   author,
		Temporality:    "durable",
		LifeState:      "alive",
	})
	if err != nil {
		t.Fatalf("create ganglion: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/ganglia/integrate", map[string]any{
		"ganglion_id": ganglion.ID,
	}, apiKeyHeaders(authorAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("self integrate ganglion status=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, author) != 1000 {
		t.Fatalf("self integration should not mint reward body=%s", w.Body.String())
	}
}

func TestTokenUpgradeClosureRewardIsHighestAndIdempotent(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	userID := seedActiveUser(t, srv)

	payload := map[string]any{
		"user_id":          userID,
		"reward_type":      communityRewardRuleUpgradeClawcolony,
		"closure_id":       "closure-001",
		"deploy_succeeded": true,
		"repo_url":         "https://example.com/repo.git",
		"branch":           "main",
		"image":            "clawcolony:test",
	}
	headers := map[string]string{"X-Clawcolony-Internal-Token": "sync-token"}
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/reward/upgrade-closure", payload, headers)
	if w.Code != http.StatusAccepted {
		t.Fatalf("upgrade closure reward status=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, userID) != 1000+communityRewardAmountUpgradeClosure {
		t.Fatalf("upgrade closure reward missing body=%s", w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/reward/upgrade-closure", payload, headers)
	if w.Code != http.StatusAccepted {
		t.Fatalf("duplicate upgrade closure reward status=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, userID) != 1000+communityRewardAmountUpgradeClosure {
		t.Fatalf("upgrade closure reward should be idempotent body=%s", w.Body.String())
	}
}

func TestTokenUpgradeClosureRewardRequiresInternalAuth(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	userID := seedActiveUser(t, srv)

	w := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/token/reward/upgrade-closure", map[string]any{
		"user_id":          userID,
		"reward_type":      communityRewardRuleSelfCoreUpgrade,
		"closure_id":       "closure-authz",
		"deploy_succeeded": true,
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing internal auth should be unauthorized, got=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, userID) != 1000 {
		t.Fatalf("unauthorized upgrade reward must not change balance body=%s", w.Body.String())
	}
}

func TestTokenUpgradeClosureRewardRejectsDeployFailure(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	userID := seedActiveUser(t, srv)

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/reward/upgrade-closure", map[string]any{
		"user_id":          userID,
		"reward_type":      communityRewardRuleSelfCoreUpgrade,
		"closure_id":       "closure-failed-deploy",
		"deploy_succeeded": false,
	}, map[string]string{"X-Clawcolony-Internal-Token": "sync-token"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("deploy failure should be rejected, got=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, userID) != 1000 {
		t.Fatalf("rejected deploy must not change balance body=%s", w.Body.String())
	}
}

func TestTokenTaskMarketListsManualAndSystemItems(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	_, posterAPIKey := seedActiveUserWithAPIKey(t, srv)
	proposer := seedActiveUser(t, srv)
	orchestrator, orchestratorAPIKey := seedActiveUserWithAPIKey(t, srv)
	author := seedActiveUser(t, srv)
	upgradeAuthor := newAuthUser(t, srv)
	upgradeReviewer := newAuthUser(t, srv)
	upgradeReviewerTwo := newAuthUser(t, srv)
	upgradeOutsider := newAuthUser(t, srv)

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/bounty/post", map[string]any{
		"description": "manual market task",
		"reward":      40,
		"criteria":    "done",
	}, apiKeyHeaders(posterAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("post bounty status=%d body=%s", w.Code, w.Body.String())
	}

	proposal, _, err := srv.store.CreateKBProposal(ctx, store.KBProposal{
		ProposerUserID:    proposer,
		Title:             "Approved KB task",
		Reason:            "waiting apply",
		Status:            "discussing",
		VoteThresholdPct:  80,
		VoteWindowSeconds: 300,
	}, store.KBProposalChange{
		OpType:     "add",
		Section:    "knowledge/runtime",
		Title:      "market",
		NewContent: "market",
		DiffText:   "+ market",
	})
	if err != nil {
		t.Fatalf("create kb proposal: %v", err)
	}
	if _, err := srv.store.CloseKBProposal(ctx, proposal.ID, "approved", "ok", 1, 1, 0, 0, 1, time.Now().UTC()); err != nil {
		t.Fatalf("approve kb proposal: %v", err)
	}

	session, err := srv.store.CreateCollabSession(ctx, store.CollabSession{
		CollabID:           "collab-market",
		Title:              "Review-ready collab",
		Goal:               "close loop",
		Complexity:         "m",
		Phase:              "reviewing",
		ProposerUserID:     orchestrator,
		OrchestratorUserID: orchestrator,
		MinMembers:         1,
		MaxMembers:         3,
	})
	if err != nil {
		t.Fatalf("create collab: %v", err)
	}
	artifact, err := srv.store.CreateCollabArtifact(ctx, store.CollabArtifact{
		CollabID: session.CollabID,
		UserID:   author,
		Role:     "builder",
		Kind:     "spec",
		Summary:  "accepted artifact",
		Content:  "evidence/result/next",
		Status:   "submitted",
	})
	if err != nil {
		t.Fatalf("create collab artifact: %v", err)
	}
	if _, err := srv.store.UpdateCollabArtifactReview(ctx, artifact.ID, "accepted", "ok"); err != nil {
		t.Fatalf("accept collab artifact: %v", err)
	}

	fixture := newFakeUpgradePRGitHub(t, "agi-bar/clawcolony", 94)
	fixture.pull = githubPullRequestRecord{
		Number:  94,
		State:   "open",
		HTMLURL: fixture.pullURL(),
	}
	fixture.pull.Head.SHA = "sha-head-market"
	fixture.pull.Base.SHA = "sha-base-market"
	fixture.pull.User.Login = "author-login"
	upgradeCollab := setupUpgradePRRewardFlowForTest(t, srv, fixture, upgradeAuthor, upgradeReviewer, upgradeReviewerTwo)
	fixture.reviews = []githubPullReviewRecord{
		makeUpgradePRReview(1, "reviewer-one", "APPROVED", upgradeCollab.CollabID, fixture.pull.Head.SHA, "agree", "ready", "none", time.Now().Add(-5*time.Minute)),
		makeUpgradePRReview(2, "reviewer-two", "COMMENTED", upgradeCollab.CollabID, fixture.pull.Head.SHA, "disagree", "one concern", "key issue", time.Now().Add(-4*time.Minute)),
	}
	mergedAt := time.Now().UTC()
	if _, err := srv.store.UpdateCollabPR(ctx, store.CollabPRUpdate{
		CollabID:         upgradeCollab.CollabID,
		PRURL:            fixture.pullURL(),
		PRNumber:         fixture.number,
		PRBaseSHA:        fixture.pull.Base.SHA,
		PRHeadSHA:        fixture.pull.Head.SHA,
		PRAuthorLogin:    fixture.pull.User.Login,
		GitHubPRState:    "merged",
		PRMergeCommitSHA: "merge-market-123",
		PRMergedAt:       &mergedAt,
	}); err != nil {
		t.Fatalf("mark upgrade_pr merged: %v", err)
	}
	closedAt := time.Now().UTC()
	if _, err := srv.store.UpdateCollabPhase(ctx, upgradeCollab.CollabID, "closed", clawWorldSystemID, "upgrade_pr merged on GitHub", &closedAt); err != nil {
		t.Fatalf("close upgrade_pr for market: %v", err)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/token/task-market?limit=20", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("task market status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		`"source":"manual"`,
		`"linked_resource_type":"bounty"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("task market missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, `"source":"system"`) {
		t.Fatalf("v2 task market should not list legacy system reward items body=%s", body)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/token/task-market?source=system&status=claimed&limit=20", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("task market claimed filter status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"source":"system"`) {
		t.Fatalf("system task market should respect status filter body=%s", w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/token/task-market?source=system&module=collab&limit=20", nil, apiKeyHeaders(orchestratorAPIKey))
	if w.Code != http.StatusOK {
		t.Fatalf("task market owner filter status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"linked_resource_type":"collab.session"`) {
		t.Fatalf("v2 task market should not expose legacy collab close tasks body=%s", w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/token/task-market?source=system&module=collab&limit=20", nil, apiKeyHeaders(posterAPIKey))
	if w.Code != http.StatusOK {
		t.Fatalf("task market non-owner filter status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"linked_resource_type":"collab.session"`) {
		t.Fatalf("non-orchestrator should not see collab close task body=%s", w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/token/task-market?source=system&module=collab&limit=20", nil, upgradeReviewer.headers())
	if w.Code != http.StatusOK {
		t.Fatalf("upgrade reviewer task market status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"action_path":"/api/v1/token/reward/upgrade-pr-claim"`) ||
		!strings.Contains(w.Body.String(), `"reward_rule_key":"upgrade-pr.reviewer"`) ||
		!strings.Contains(w.Body.String(), `"linked_resource_id":"`+upgradeCollab.CollabID+`"`) {
		t.Fatalf("upgrade reviewer should see claim task body=%s", w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/token/task-market?source=system&module=collab&limit=20", nil, upgradeAuthor.headers())
	if w.Code != http.StatusOK {
		t.Fatalf("upgrade author task market status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"reward_rule_key":"upgrade-pr.author"`) ||
		!strings.Contains(w.Body.String(), `"linked_resource_id":"`+upgradeCollab.CollabID+`"`) {
		t.Fatalf("upgrade author should see claim task body=%s", w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/token/task-market?source=system&module=collab&limit=20", nil, upgradeOutsider.headers())
	if w.Code != http.StatusOK {
		t.Fatalf("upgrade outsider task market status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `"linked_resource_id":"`+upgradeCollab.CollabID+`"`) {
		t.Fatalf("non-participant should not see upgrade claim task body=%s", w.Body.String())
	}
}

func TestCollabCloseFailedDoesNotGrantCommunityReward(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	orchestrator, orchestratorAPIKey := seedActiveUserWithAPIKey(t, srv)
	author := seedActiveUser(t, srv)

	session, err := srv.store.CreateCollabSession(ctx, store.CollabSession{
		CollabID:           "collab-failed-no-reward",
		Title:              "Shared collab failed",
		Goal:               "do work",
		Complexity:         "m",
		Phase:              "reviewing",
		ProposerUserID:     orchestrator,
		OrchestratorUserID: orchestrator,
		MinMembers:         1,
		MaxMembers:         3,
	})
	if err != nil {
		t.Fatalf("create collab: %v", err)
	}
	artifact, err := srv.store.CreateCollabArtifact(ctx, store.CollabArtifact{
		CollabID: session.CollabID,
		UserID:   author,
		Role:     "builder",
		Kind:     "spec",
		Summary:  "accepted artifact",
		Content:  "evidence/result/next",
		Status:   "submitted",
	})
	if err != nil {
		t.Fatalf("create collab artifact: %v", err)
	}
	if _, err := srv.store.UpdateCollabArtifactReview(ctx, artifact.ID, "accepted", "ok"); err != nil {
		t.Fatalf("accept collab artifact: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/close", map[string]any{
		"collab_id":              session.CollabID,
		"result":                 "failed",
		"status_or_summary_note": "did not close successfully",
	}, apiKeyHeaders(orchestratorAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("failed close status=%d body=%s", w.Code, w.Body.String())
	}
	if tokenBalanceForUser(t, srv, author) != 1000 {
		t.Fatalf("failed collab close should not reward author body=%s", w.Body.String())
	}
}

func TestCollabCloseRequiresCurrentOrchestrator(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	orchestrator := seedActiveUser(t, srv)
	_, otherAPIKey := seedActiveUserWithAPIKey(t, srv)
	author := seedActiveUser(t, srv)

	session, err := srv.store.CreateCollabSession(ctx, store.CollabSession{
		CollabID:           "collab-owner-guard",
		Title:              "Protected collab",
		Goal:               "close only by orchestrator",
		Complexity:         "m",
		Phase:              "reviewing",
		ProposerUserID:     orchestrator,
		OrchestratorUserID: orchestrator,
		MinMembers:         1,
		MaxMembers:         3,
	})
	if err != nil {
		t.Fatalf("create collab: %v", err)
	}
	artifact, err := srv.store.CreateCollabArtifact(ctx, store.CollabArtifact{
		CollabID: session.CollabID,
		UserID:   author,
		Role:     "builder",
		Kind:     "spec",
		Summary:  "accepted artifact",
		Content:  "evidence/result/next",
		Status:   "submitted",
	})
	if err != nil {
		t.Fatalf("create collab artifact: %v", err)
	}
	if _, err := srv.store.UpdateCollabArtifactReview(ctx, artifact.ID, "accepted", "ok"); err != nil {
		t.Fatalf("accept collab artifact: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/close", map[string]any{
		"collab_id":              session.CollabID,
		"result":                 "closed",
		"status_or_summary_note": "unauthorized close",
	}, apiKeyHeaders(otherAPIKey))
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-orchestrator close should be forbidden, got=%d body=%s", w.Code, w.Body.String())
	}

	after, err := srv.store.GetCollabSession(ctx, session.CollabID)
	if err != nil {
		t.Fatalf("reload collab: %v", err)
	}
	if after.Phase != "reviewing" {
		t.Fatalf("phase should remain reviewing, got=%s", after.Phase)
	}
	if after.OrchestratorUserID != orchestrator {
		t.Fatalf("orchestrator should remain unchanged, got=%s", after.OrchestratorUserID)
	}
	if tokenBalanceForUser(t, srv, author) != 1000 {
		t.Fatalf("unauthorized close should not mint reward body=%s", w.Body.String())
	}
}

func TestCloseKBProposalByStatsAutoApplyGrantsCommunityReward(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := seedActiveUser(t, srv)
	content := strings.Repeat("a", 500)

	proposal, _, err := srv.store.CreateKBProposal(ctx, store.KBProposal{
		ProposerUserID:    proposer,
		Title:             "Auto-applied KB reward",
		Reason:            "shared knowledge",
		Status:            "voting",
		VoteThresholdPct:  50,
		VoteWindowSeconds: 300,
	}, store.KBProposalChange{
		OpType:     "add",
		Section:    "knowledge/runtime",
		Title:      "auto reward entry",
		NewContent: content,
		DiffText:   "+ auto reward content expanded",
	})
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	seedProposalKnowledgeMetaForTest(t, srv, proposal.ID, proposer, "knowledge", content, nil)

	closed, err := srv.closeKBProposalByStats(ctx, proposal,
		[]store.KBProposalEnrollment{{ProposalID: proposal.ID, UserID: proposer}},
		[]store.KBVote{{ProposalID: proposal.ID, UserID: proposer, Vote: "yes"}},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatalf("auto close proposal: %v", err)
	}
	if closed.Status != "approved" {
		t.Fatalf("proposal should be approved, got=%s", closed.Status)
	}
	if tokenBalanceForUser(t, srv, proposer) != 1000+knowledgeRewardForContent(srv, content, 0) {
		t.Fatalf("auto apply should reward proposer")
	}
}
