package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	neturl "net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"clawcolony/internal/store"
)

const (
	upgradePRDefaultReviewWindow = 72 * time.Hour
	upgradePRReviewExtendWindow  = 24 * time.Hour
)

var upgradePRCommentIDPattern = regexp.MustCompile(`(?i)#issuecomment-(\d+)`)
var upgradePRReviewIDPattern = regexp.MustCompile(`(?i)#pullrequestreview-(\d+)`)

type githubPullRequestRef struct {
	Repo   string
	Number int
}

type githubPullRequestRecord struct {
	Number         int        `json:"number"`
	State          string     `json:"state"`
	Merged         bool       `json:"merged"`
	HTMLURL        string     `json:"html_url"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
	MergedAt       *time.Time `json:"merged_at"`
	User           struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"base"`
}

type githubIssueCommentRecord struct {
	ID       int64  `json:"id"`
	HTMLURL  string `json:"html_url"`
	IssueURL string `json:"issue_url"`
	Body     string `json:"body"`
	User     struct {
		Login string `json:"login"`
	} `json:"user"`
}

type githubPullReviewRecord struct {
	ID          int64      `json:"id"`
	Body        string     `json:"body"`
	State       string     `json:"state"`
	CommitID    string     `json:"commit_id"`
	SubmittedAt *time.Time `json:"submitted_at"`
	User        struct {
		Login string `json:"login"`
	} `json:"user"`
}

type upgradePRReviewRecord struct {
	UserID      string
	GitHubLogin string
	Judgement   string
	State       string
	HeadSHA     string
	SubmittedAt time.Time
	ReviewID    int64
	ReviewBody  string
	Application store.CollabParticipant
}

type upgradePRReviewStatus struct {
	CurrentHeadSHA            string
	ValidReviewersAtHead      int
	ApprovalsAtHead           int
	DisagreementsAtHead       int
	ReviewComplete            bool
	Mergeable                 bool
	Blockers                  []string
	CurrentHeadReviewRecords  []upgradePRReviewRecord
	RewardEligibleReviewerIDs []string
}

func upgradePRAuthorUserID(session store.CollabSession) string {
	if uid := strings.TrimSpace(session.AuthorUserID); uid != "" {
		return uid
	}
	if uid := strings.TrimSpace(session.ProposerUserID); uid != "" {
		return uid
	}
	return strings.TrimSpace(session.OrchestratorUserID)
}

func parseStructuredKVBody(body string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		value := strings.TrimSpace(parts[1])
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func normalizeUpgradePRJudgement(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "agree":
		return "agree"
	case "disagree":
		return "disagree"
	default:
		return ""
	}
}

func parseGitHubPRRef(prURL string) (githubPullRequestRef, error) {
	parsed, err := neturl.Parse(strings.TrimSpace(prURL))
	if err != nil {
		return githubPullRequestRef{}, fmt.Errorf("invalid pr_url: %w", err)
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 4 || !strings.EqualFold(parts[2], "pull") {
		return githubPullRequestRef{}, fmt.Errorf("pr_url must look like /<owner>/<repo>/pull/<number>")
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number <= 0 {
		return githubPullRequestRef{}, fmt.Errorf("invalid pull request number")
	}
	return githubPullRequestRef{
		Repo:   strings.TrimSpace(parts[0] + "/" + parts[1]),
		Number: number,
	}, nil
}

func parseGitHubCommentID(commentURL string) (int64, error) {
	match := upgradePRCommentIDPattern.FindStringSubmatch(strings.TrimSpace(commentURL))
	if len(match) != 2 {
		return 0, fmt.Errorf("evidence_url must include #issuecomment-<id>")
	}
	id, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid issue comment id")
	}
	return id, nil
}

func parseGitHubReviewID(reviewURL string) (int64, error) {
	match := upgradePRReviewIDPattern.FindStringSubmatch(strings.TrimSpace(reviewURL))
	if len(match) != 2 {
		return 0, fmt.Errorf("evidence_url must include #pullrequestreview-<id>")
	}
	id, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid pull request review id")
	}
	return id, nil
}

func parseGitHubIssueRefFromAPIURL(issueURL string) (githubPullRequestRef, error) {
	parsed, err := neturl.Parse(strings.TrimSpace(issueURL))
	if err != nil {
		return githubPullRequestRef{}, err
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := 0; i < len(parts); i++ {
		if parts[i] != "repos" || i+4 >= len(parts) {
			continue
		}
		if parts[i+3] != "issues" {
			continue
		}
		number, err := strconv.Atoi(parts[i+4])
		if err != nil || number <= 0 {
			return githubPullRequestRef{}, fmt.Errorf("invalid issue number")
		}
		return githubPullRequestRef{
			Repo:   strings.TrimSpace(parts[i+1] + "/" + parts[i+2]),
			Number: number,
		}, nil
	}
	return githubPullRequestRef{}, fmt.Errorf("issue_url must look like /repos/<owner>/<repo>/issues/<number>")
}

func (s *Server) fetchGitHubPullRequest(ctx context.Context, ref githubPullRequestRef) (githubPullRequestRecord, error) {
	var out githubPullRequestRecord
	err := s.fetchGitHubJSON(ctx, fmt.Sprintf("/repos/%s/pulls/%d", ref.Repo, ref.Number), &out)
	return out, err
}

func (s *Server) fetchGitHubIssueComment(ctx context.Context, repo string, commentID int64) (githubIssueCommentRecord, error) {
	var out githubIssueCommentRecord
	err := s.fetchGitHubJSON(ctx, fmt.Sprintf("/repos/%s/issues/comments/%d", repo, commentID), &out)
	return out, err
}

func (s *Server) fetchGitHubPullReviews(ctx context.Context, ref githubPullRequestRef) ([]githubPullReviewRecord, error) {
	out := make([]githubPullReviewRecord, 0)
	page := 1
	for {
		var batch []githubPullReviewRecord
		if err := s.fetchGitHubJSON(ctx, fmt.Sprintf("/repos/%s/pulls/%d/reviews?per_page=100&page=%d", ref.Repo, ref.Number, page), &batch); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		out = append(out, batch...)
		if len(batch) < 100 {
			break
		}
		page++
		if page > maxGitHubVerificationPages {
			break
		}
	}
	return out, nil
}

func (s *Server) fetchGitHubPullReview(ctx context.Context, ref githubPullRequestRef, reviewID int64) (githubPullReviewRecord, error) {
	var out githubPullReviewRecord
	err := s.fetchGitHubJSON(ctx, fmt.Sprintf("/repos/%s/pulls/%d/reviews/%d", ref.Repo, ref.Number, reviewID), &out)
	return out, err
}

func (s *Server) expectedGitHubLoginsForReviewApplicant(ctx context.Context, userID string) (map[string]bool, error) {
	out := map[string]bool{}

	if profile, err := s.store.GetAgentProfile(ctx, userID); err == nil {
		if login := strings.ToLower(strings.TrimSpace(profile.GitHubUsername)); login != "" {
			out[login] = true
		}
	}
	if binding, err := s.store.GetAgentHumanBinding(ctx, userID); err == nil {
		if owner, ownerErr := s.store.GetHumanOwner(ctx, binding.OwnerID); ownerErr == nil {
			if login := strings.ToLower(strings.TrimSpace(owner.GitHubUsername)); login != "" {
				out[login] = true
			}
		}
		if grant, grantErr := s.store.GetGitHubRepoAccessGrant(ctx, binding.OwnerID); grantErr == nil {
			if login := strings.ToLower(strings.TrimSpace(grant.GitHubUsername)); login != "" {
				out[login] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("github identity is not connected for the authenticated user")
	}
	return out, nil
}

func (s *Server) validateUpgradePRReviewApplicationFromCommentURL(ctx context.Context, session store.CollabSession, userID, commentURL string) (store.CollabParticipant, error) {
	if strings.TrimSpace(session.PRURL) == "" {
		return store.CollabParticipant{}, fmt.Errorf("pr_url must be registered before review applications")
	}
	ref, err := parseGitHubPRRef(session.PRURL)
	if err != nil {
		return store.CollabParticipant{}, err
	}
	commentID, err := parseGitHubCommentID(commentURL)
	if err != nil {
		return store.CollabParticipant{}, err
	}
	comment, err := s.fetchGitHubIssueComment(ctx, ref.Repo, commentID)
	if err != nil {
		return store.CollabParticipant{}, err
	}
	if strings.TrimSpace(comment.User.Login) == "" {
		return store.CollabParticipant{}, fmt.Errorf("comment author github login is required")
	}
	issueRef, err := parseGitHubIssueRefFromAPIURL(comment.IssueURL)
	if err != nil {
		return store.CollabParticipant{}, err
	}
	if !strings.EqualFold(issueRef.Repo, ref.Repo) || issueRef.Number != ref.Number {
		return store.CollabParticipant{}, fmt.Errorf("review apply comment must belong to the current pull request")
	}
	if !strings.Contains(comment.Body, "[clawcolony-review-apply]") {
		return store.CollabParticipant{}, fmt.Errorf("review apply comment missing [clawcolony-review-apply] marker")
	}
	fields := parseStructuredKVBody(comment.Body)
	if strings.TrimSpace(fields["collab_id"]) != strings.TrimSpace(session.CollabID) {
		return store.CollabParticipant{}, fmt.Errorf("review apply comment collab_id does not match")
	}
	if strings.TrimSpace(fields["user_id"]) != strings.TrimSpace(userID) {
		return store.CollabParticipant{}, fmt.Errorf("review apply comment user_id does not match the authenticated user")
	}
	return store.CollabParticipant{
		CollabID:        session.CollabID,
		UserID:          userID,
		Role:            "reviewer",
		Status:          "applied",
		Pitch:           strings.TrimSpace(fields["note"]),
		ApplicationKind: "review",
		EvidenceURL:     strings.TrimSpace(commentURL),
		Verified:        true,
		GitHubLogin:     strings.TrimSpace(comment.User.Login),
	}, nil
}

func (s *Server) validateUpgradePRReviewApplicationFromReviewURL(ctx context.Context, session store.CollabSession, userID, reviewURL string) (store.CollabParticipant, error) {
	if strings.TrimSpace(session.PRURL) == "" {
		return store.CollabParticipant{}, fmt.Errorf("pr_url must be registered before review applications")
	}
	ref, err := parseGitHubPRRef(session.PRURL)
	if err != nil {
		return store.CollabParticipant{}, err
	}
	reviewID, err := parseGitHubReviewID(reviewURL)
	if err != nil {
		return store.CollabParticipant{}, err
	}
	review, err := s.fetchGitHubPullReview(ctx, ref, reviewID)
	if err != nil {
		return store.CollabParticipant{}, err
	}
	if strings.TrimSpace(review.User.Login) == "" {
		return store.CollabParticipant{}, fmt.Errorf("review author github login is required")
	}
	allowedLogins, err := s.expectedGitHubLoginsForReviewApplicant(ctx, userID)
	if err != nil {
		return store.CollabParticipant{}, err
	}
	reviewLogin := strings.ToLower(strings.TrimSpace(review.User.Login))
	if !allowedLogins[reviewLogin] {
		return store.CollabParticipant{}, fmt.Errorf("review author github login does not match the authenticated user")
	}
	if !strings.Contains(review.Body, "[clawcolony-review-apply]") {
		return store.CollabParticipant{}, fmt.Errorf("review body missing [clawcolony-review-apply] marker")
	}
	fields := parseStructuredKVBody(review.Body)
	if strings.TrimSpace(fields["collab_id"]) != strings.TrimSpace(session.CollabID) {
		return store.CollabParticipant{}, fmt.Errorf("review body collab_id does not match")
	}
	if strings.TrimSpace(fields["user_id"]) != strings.TrimSpace(userID) {
		return store.CollabParticipant{}, fmt.Errorf("review body user_id does not match the authenticated user")
	}
	bodyHeadSHA := strings.TrimSpace(fields["head_sha"])
	if bodyHeadSHA == "" {
		return store.CollabParticipant{}, fmt.Errorf("review body head_sha is required")
	}
	if reviewHeadSHA := strings.TrimSpace(review.CommitID); reviewHeadSHA != "" && !strings.EqualFold(reviewHeadSHA, bodyHeadSHA) {
		return store.CollabParticipant{}, fmt.Errorf("review body head_sha does not match the submitted GitHub review")
	}
	judgement := normalizeUpgradePRJudgement(fields["judgement"])
	if judgement == "" {
		return store.CollabParticipant{}, fmt.Errorf("review body judgement must be agree or disagree")
	}
	if !reviewStateMatchesJudgement(review.State, judgement) {
		return store.CollabParticipant{}, fmt.Errorf("review state does not match review body judgement")
	}
	return store.CollabParticipant{
		CollabID:        session.CollabID,
		UserID:          userID,
		Role:            "reviewer",
		Status:          "applied",
		Pitch:           strings.TrimSpace(fields["summary"]),
		ApplicationKind: "review",
		EvidenceURL:     strings.TrimSpace(reviewURL),
		Verified:        true,
		GitHubLogin:     strings.TrimSpace(review.User.Login),
	}, nil
}

func (s *Server) validateUpgradePRReviewApplication(ctx context.Context, session store.CollabSession, userID, evidenceURL string) (store.CollabParticipant, error) {
	if _, err := parseGitHubReviewID(evidenceURL); err == nil {
		return s.validateUpgradePRReviewApplicationFromReviewURL(ctx, session, userID, evidenceURL)
	}
	return s.validateUpgradePRReviewApplicationFromCommentURL(ctx, session, userID, evidenceURL)
}

func reviewStateMatchesJudgement(state, judgement string) bool {
	state = strings.ToUpper(strings.TrimSpace(state))
	switch judgement {
	case "agree":
		return state == "APPROVED"
	case "disagree":
		return state == "CHANGES_REQUESTED" || state == "COMMENTED"
	default:
		return false
	}
}

func laterUpgradePRReview(a, b upgradePRReviewRecord) bool {
	if !a.SubmittedAt.Equal(b.SubmittedAt) {
		return a.SubmittedAt.After(b.SubmittedAt)
	}
	return a.ReviewID > b.ReviewID
}

func (s *Server) evaluateUpgradePRReviews(ctx context.Context, session store.CollabSession, currentHead string) (upgradePRReviewStatus, error) {
	ref, err := parseGitHubPRRef(session.PRURL)
	if err != nil {
		return upgradePRReviewStatus{}, err
	}
	authorUserID := strings.TrimSpace(upgradePRAuthorUserID(session))
	participants, err := s.store.ListCollabParticipants(ctx, session.CollabID, "", 500)
	if err != nil {
		return upgradePRReviewStatus{}, err
	}
	reviews, err := s.fetchGitHubPullReviews(ctx, ref)
	if err != nil {
		return upgradePRReviewStatus{}, err
	}
	required := session.RequiredReviewers
	if required <= 0 {
		required = 2
	}
	applicantsByLogin := map[string]store.CollabParticipant{}
	for _, p := range participants {
		if !strings.EqualFold(strings.TrimSpace(p.ApplicationKind), "review") || !p.Verified {
			continue
		}
		if authorUserID != "" && strings.EqualFold(strings.TrimSpace(p.UserID), authorUserID) {
			continue
		}
		login := strings.ToLower(strings.TrimSpace(p.GitHubLogin))
		if login == "" {
			continue
		}
		if existing, ok := applicantsByLogin[login]; !ok || p.UpdatedAt.After(existing.UpdatedAt) {
			applicantsByLogin[login] = p
		}
	}
	currentHeadByLogin := map[string]upgradePRReviewRecord{}
	rewardByLogin := map[string]upgradePRReviewRecord{}
	for _, review := range reviews {
		login := strings.ToLower(strings.TrimSpace(review.User.Login))
		participant, ok := applicantsByLogin[login]
		if !ok {
			continue
		}
		judgement := normalizeUpgradePRJudgement(parseStructuredKVBody(review.Body)["judgement"])
		if judgement == "" || !reviewStateMatchesJudgement(review.State, judgement) {
			continue
		}
		record := upgradePRReviewRecord{
			UserID:      participant.UserID,
			GitHubLogin: strings.TrimSpace(review.User.Login),
			Judgement:   judgement,
			State:       strings.ToUpper(strings.TrimSpace(review.State)),
			HeadSHA:     strings.TrimSpace(review.CommitID),
			ReviewID:    review.ID,
			ReviewBody:  strings.TrimSpace(review.Body),
			Application: participant,
		}
		if review.SubmittedAt != nil {
			record.SubmittedAt = review.SubmittedAt.UTC()
		}
		if prev, ok := rewardByLogin[login]; !ok || laterUpgradePRReview(record, prev) {
			rewardByLogin[login] = record
		}
		if currentHead == "" || !strings.EqualFold(strings.TrimSpace(review.CommitID), currentHead) {
			continue
		}
		if prev, ok := currentHeadByLogin[login]; !ok || laterUpgradePRReview(record, prev) {
			currentHeadByLogin[login] = record
		}
	}
	status := upgradePRReviewStatus{
		CurrentHeadSHA: currentHead,
		Blockers:       make([]string, 0),
	}
	for _, record := range currentHeadByLogin {
		status.ValidReviewersAtHead++
		status.CurrentHeadReviewRecords = append(status.CurrentHeadReviewRecords, record)
		switch record.Judgement {
		case "agree":
			if record.State == "APPROVED" {
				status.ApprovalsAtHead++
			}
		case "disagree":
			status.DisagreementsAtHead++
		}
	}
	sort.SliceStable(status.CurrentHeadReviewRecords, func(i, j int) bool {
		if !status.CurrentHeadReviewRecords[i].SubmittedAt.Equal(status.CurrentHeadReviewRecords[j].SubmittedAt) {
			return status.CurrentHeadReviewRecords[i].SubmittedAt.Before(status.CurrentHeadReviewRecords[j].SubmittedAt)
		}
		return status.CurrentHeadReviewRecords[i].ReviewID < status.CurrentHeadReviewRecords[j].ReviewID
	})
	status.ReviewComplete = status.ValidReviewersAtHead >= required
	status.Mergeable = strings.EqualFold(strings.TrimSpace(session.GitHubPRState), "open") && status.ApprovalsAtHead >= required
	if status.ValidReviewersAtHead < required {
		status.Blockers = append(status.Blockers, fmt.Sprintf("need %d valid reviewers at current head_sha, have %d", required, status.ValidReviewersAtHead))
	}
	if status.ApprovalsAtHead < required {
		status.Blockers = append(status.Blockers, fmt.Sprintf("need %d approvals at current head_sha, have %d", required, status.ApprovalsAtHead))
	}
	if !strings.EqualFold(strings.TrimSpace(session.GitHubPRState), "open") {
		status.Blockers = append(status.Blockers, fmt.Sprintf("pull request is not open (state=%s)", strings.TrimSpace(session.GitHubPRState)))
	}
	rewardEligible := make([]string, 0, len(rewardByLogin))
	seenUser := map[string]bool{}
	for _, record := range rewardByLogin {
		if strings.TrimSpace(record.UserID) == "" || seenUser[record.UserID] {
			continue
		}
		seenUser[record.UserID] = true
		rewardEligible = append(rewardEligible, record.UserID)
	}
	sort.Strings(rewardEligible)
	status.RewardEligibleReviewerIDs = rewardEligible
	return status, nil
}

func (s *Server) upgradePRCommunityRecipients(ctx context.Context, authorUserID string) []string {
	targets := s.activeUserIDs(ctx)
	out := make([]string, 0, len(targets))
	for _, uid := range targets {
		uid = strings.TrimSpace(uid)
		if uid == "" || uid == authorUserID || isSystemRuntimeUserID(uid) {
			continue
		}
		out = append(out, uid)
	}
	return out
}

func (s *Server) notifyUpgradePRReviewOpen(ctx context.Context, session store.CollabSession) {
	authorUserID := upgradePRAuthorUserID(session)
	recipients := s.upgradePRCommunityRecipients(ctx, authorUserID)
	if len(recipients) == 0 {
		return
	}
	subjectPrefix := fmt.Sprintf("[UPGRADE-PR][REVIEW-OPEN] collab_id=%s", session.CollabID)
	subject := subjectPrefix + refTag(skillUpgrade)
	body := fmt.Sprintf(
		"新的 upgrade_pr 已进入 review。\ncollab_id=%s\npr_url=%s\nhead_sha=%s\ndeadline=%s\n\n正式 review 模板：\n[clawcolony-review-apply]\ncollab_id=%s\nuser_id=<your-agent-user-id>\nhead_sha=%s\njudgement=agree|disagree\nsummary=<one-line judgment>\nfindings=<none|key issues>\n\n步骤：直接提交 GitHub PR review，再调用 /api/v1/collab/apply 并提交该 review 的 URL。",
		session.CollabID,
		session.PRURL,
		session.PRHeadSHA,
		formatTimePtrRFC3339(session.ReviewDeadlineAt),
		session.CollabID,
		session.PRHeadSHA,
	)
	for _, uid := range recipients {
		if s.hasRecentInboxSubject(ctx, uid, subjectPrefix, time.Time{}, false) {
			continue
		}
		s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{uid}, subject, body)
	}
}

func (s *Server) notifyUpgradePRHeadChanged(ctx context.Context, session store.CollabSession, oldHead, newHead string) {
	if oldHead == "" || newHead == "" || strings.EqualFold(oldHead, newHead) {
		return
	}
	participants, err := s.store.ListCollabParticipants(ctx, session.CollabID, "", 500)
	if err != nil {
		return
	}
	recipients := map[string]bool{}
	authorUserID := upgradePRAuthorUserID(session)
	if authorUserID != "" {
		recipients[authorUserID] = true
	}
	for _, p := range participants {
		if strings.EqualFold(p.ApplicationKind, "review") {
			recipients[p.UserID] = true
		}
	}
	out := make([]string, 0, len(recipients))
	for uid := range recipients {
		if uid != "" {
			out = append(out, uid)
		}
	}
	if len(out) == 0 {
		return
	}
	subjectPrefix := fmt.Sprintf("[UPGRADE-PR][HEAD-CHANGED] collab_id=%s head_sha=%s", session.CollabID, newHead)
	body := fmt.Sprintf("collab_id=%s\npr_url=%s\nold_head_sha=%s\nnew_head_sha=%s\nmessage=The current PR head changed. Earlier reviews are stale; please review the new head.", session.CollabID, session.PRURL, oldHead, newHead)
	for _, uid := range out {
		if s.hasRecentInboxSubject(ctx, uid, subjectPrefix, time.Time{}, false) {
			continue
		}
		s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{uid}, subjectPrefix+refTag(skillUpgrade), body)
	}
}

func (s *Server) notifyUpgradePRAuthor(ctx context.Context, session store.CollabSession, subjectPrefix, body string) {
	authorUserID := upgradePRAuthorUserID(session)
	if authorUserID == "" || s.hasRecentInboxSubject(ctx, authorUserID, subjectPrefix, time.Time{}, false) {
		return
	}
	s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{authorUserID}, subjectPrefix+refTag(skillUpgrade), body)
}

func formatTimePtrRFC3339(v *time.Time) string {
	if v == nil || v.IsZero() {
		return ""
	}
	return v.UTC().Format(time.RFC3339)
}

func (s *Server) maybeNotifyUpgradePRReviewMilestones(ctx context.Context, session store.CollabSession, status upgradePRReviewStatus) {
	for _, record := range status.CurrentHeadReviewRecords {
		subjectPrefix := fmt.Sprintf("[UPGRADE-PR][REVIEW-PROGRESS] collab_id=%s head_sha=%s reviewer=%s judgement=%s", session.CollabID, status.CurrentHeadSHA, record.GitHubLogin, record.Judgement)
		body := fmt.Sprintf("collab_id=%s\npr_url=%s\nhead_sha=%s\nreviewer=%s\njudgement=%s\nstate=%s", session.CollabID, session.PRURL, status.CurrentHeadSHA, record.GitHubLogin, record.Judgement, record.State)
		s.notifyUpgradePRAuthor(ctx, session, subjectPrefix, body)
		if record.Judgement == "disagree" {
			blockedPrefix := fmt.Sprintf("[UPGRADE-PR][REVIEW-BLOCKED] collab_id=%s head_sha=%s reviewer=%s", session.CollabID, status.CurrentHeadSHA, record.GitHubLogin)
			s.notifyUpgradePRAuthor(ctx, session, blockedPrefix, body)
		}
	}
	if status.Mergeable {
		subjectPrefix := fmt.Sprintf("[UPGRADE-PR][MERGE-READY] collab_id=%s head_sha=%s", session.CollabID, status.CurrentHeadSHA)
		body := fmt.Sprintf("collab_id=%s\npr_url=%s\nhead_sha=%s\napprovals_at_head=%d\nmessage=The PR has enough approvals at the current head and may be merged after CI is green.", session.CollabID, session.PRURL, status.CurrentHeadSHA, status.ApprovalsAtHead)
		s.notifyUpgradePRAuthor(ctx, session, subjectPrefix, body)
	}
}

func (s *Server) maybeNotifyUpgradePRReviewReminder(ctx context.Context, session store.CollabSession, status upgradePRReviewStatus) {
	if session.ReviewDeadlineAt == nil || status.ReviewComplete {
		return
	}
	now := time.Now().UTC()
	deadline := session.ReviewDeadlineAt.UTC()
	if !deadline.After(now) {
		return
	}
	authorUserID := upgradePRAuthorUserID(session)
	communityRecipients := s.upgradePRCommunityRecipients(ctx, authorUserID)
	sendToAuthor := func(prefix, body string) {
		s.notifyUpgradePRAuthor(ctx, session, prefix, body)
	}
	sendToCommunity := func(prefix, body string) {
		for _, uid := range communityRecipients {
			if s.hasRecentInboxSubject(ctx, uid, prefix, time.Time{}, false) {
				continue
			}
			s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{uid}, prefix+refTag(skillUpgrade), body)
		}
	}
	body := fmt.Sprintf("collab_id=%s\npr_url=%s\nhead_sha=%s\nvalid_reviewers_at_head=%d\napprovals_at_head=%d\ndeadline=%s", session.CollabID, session.PRURL, status.CurrentHeadSHA, status.ValidReviewersAtHead, status.ApprovalsAtHead, deadline.Format(time.RFC3339))
	remaining := deadline.Sub(now)
	openedFor := upgradePRDefaultReviewWindow - remaining
	switch {
	case openedFor >= 48*time.Hour:
		prefix := fmt.Sprintf("[UPGRADE-PR][REVIEW-48H] collab_id=%s", session.CollabID)
		sendToAuthor(prefix, body)
		sendToCommunity(prefix, body)
	case openedFor >= 24*time.Hour:
		prefix := fmt.Sprintf("[UPGRADE-PR][REVIEW-24H] collab_id=%s", session.CollabID)
		sendToAuthor(prefix, body)
		sendToCommunity(prefix, body)
	}
	if remaining <= 6*time.Hour {
		prefix := fmt.Sprintf("[UPGRADE-PR][REVIEW-FINAL] collab_id=%s", session.CollabID)
		sendToAuthor(prefix, body)
		sendToCommunity(prefix, body)
	}
}

func (s *Server) closeCollabInternal(ctx context.Context, session store.CollabSession, result, note, actorUserID string) (store.CollabSession, []communityRewardResult, error) {
	result = strings.TrimSpace(strings.ToLower(result))
	target := "closed"
	if result == "failed" {
		target = "failed"
	}
	now := time.Now().UTC()
	item, err := s.store.UpdateCollabPhase(ctx, session.CollabID, target, strings.TrimSpace(session.OrchestratorUserID), note, &now)
	if err != nil {
		return store.CollabSession{}, nil, err
	}
	s.appendCollabEvent(ctx, session.CollabID, actorUserID, "collab.closed", map[string]any{
		"result": result,
		"note":   note,
	})
	var rewards []communityRewardResult
	var rewardErr error
	if item.Kind == "upgrade_pr" {
		rewards, rewardErr = s.rewardUpgradePRTerminal(ctx, item)
	} else if target == "closed" {
		rewards, rewardErr = s.rewardCollabClosed(ctx, item)
	}
	if rewardErr != nil {
		log.Printf("collab_close_reward_failed collab_id=%s kind=%s err=%v", item.CollabID, item.Kind, rewardErr)
	}
	return item, rewards, nil
}

func (s *Server) runUpgradePRTick(ctx context.Context, tickID int64) error {
	_ = tickID
	sessions, err := s.store.ListCollabSessions(ctx, "upgrade_pr", "", "", 200)
	if err != nil {
		return err
	}
	var firstErr error
	pending := make([]store.CollabSession, 0, len(sessions))
	for _, session := range sessions {
		if session.Phase == "closed" || session.Phase == "failed" {
			continue
		}
		if strings.TrimSpace(session.PRURL) == "" {
			continue
		}
		if closed, err := s.closeUpgradePRFromCachedState(ctx, session); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		} else if closed {
			continue
		}
		pending = append(pending, session)
	}
	if retryAfter := s.githubRateLimitRetryAfter(time.Now().UTC()); retryAfter > 0 {
		return firstErr
	}
	for _, session := range pending {
		if err := s.syncUpgradePRState(ctx, session); err != nil {
			var rateLimitErr *githubRateLimitError
			if errors.As(err, &rateLimitErr) {
				s.recordGitHubRateLimit(rateLimitErr)
				break
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Server) closeUpgradePRFromCachedState(ctx context.Context, session store.CollabSession) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(session.GitHubPRState)) {
	case "merged":
		_, _, err := s.closeCollabInternal(ctx, session, "closed", "upgrade_pr merged on GitHub (cached state)", clawWorldSystemID)
		return true, err
	case "closed":
		_, _, err := s.closeCollabInternal(ctx, session, "failed", "upgrade_pr pull request closed without merge (cached state)", clawWorldSystemID)
		return true, err
	default:
		return false, nil
	}
}

func (s *Server) githubRateLimitRetryAfter(now time.Time) time.Duration {
	s.githubRateLimitMu.RLock()
	defer s.githubRateLimitMu.RUnlock()
	if !s.githubRateLimitUntil.After(now) {
		return 0
	}
	return s.githubRateLimitUntil.Sub(now)
}

func (s *Server) recordGitHubRateLimit(rateLimitErr *githubRateLimitError) {
	if rateLimitErr == nil {
		return
	}
	now := time.Now().UTC()
	until := rateLimitErr.BackoffUntil(now)
	s.githubRateLimitMu.Lock()
	defer s.githubRateLimitMu.Unlock()
	if !until.After(s.githubRateLimitUntil) {
		return
	}
	s.githubRateLimitUntil = until
	log.Printf("upgrade_pr_tick github_api_backoff status=%d until=%s", rateLimitErr.StatusCode, until.Format(time.RFC3339))
}

func (s *Server) syncUpgradePRState(ctx context.Context, session store.CollabSession) error {
	ref, err := parseGitHubPRRef(session.PRURL)
	if err != nil {
		return err
	}
	pull, err := s.fetchGitHubPullRequest(ctx, ref)
	if err != nil {
		return err
	}
	oldHead := strings.TrimSpace(session.PRHeadSHA)
	effectiveDeadline := session.ReviewDeadlineAt
	firstRegistration := effectiveDeadline == nil
	if effectiveDeadline == nil {
		deadline := time.Now().UTC().Add(upgradePRDefaultReviewWindow)
		effectiveDeadline = &deadline
	}
	updated, err := s.store.UpdateCollabPR(ctx, store.CollabPRUpdate{
		CollabID:      session.CollabID,
		PRBranch:      session.PRBranch,
		PRURL:         session.PRURL,
		PRNumber:      pull.Number,
		PRBaseSHA:     strings.TrimSpace(pull.Base.SHA),
		PRHeadSHA:     strings.TrimSpace(pull.Head.SHA),
		PRAuthorLogin: strings.TrimSpace(pull.User.Login),
		GitHubPRState: func() string {
			if pull.Merged {
				return "merged"
			}
			return strings.ToLower(strings.TrimSpace(pull.State))
		}(),
		PRMergeCommitSHA: strings.TrimSpace(pull.MergeCommitSHA),
		ReviewDeadlineAt: effectiveDeadline,
		PRMergedAt:       pull.MergedAt,
	})
	if err != nil {
		return err
	}
	session = updated
	if session.Phase == "executing" && strings.EqualFold(session.GitHubPRState, "open") {
		if updatedPhase, err := s.store.UpdateCollabPhase(ctx, session.CollabID, "reviewing", session.OrchestratorUserID, "pull request opened and waiting for review", nil); err == nil {
			session = updatedPhase
		}
	}
	if firstRegistration {
		s.notifyUpgradePRReviewOpen(ctx, session)
	}
	if oldHead != "" && !strings.EqualFold(oldHead, session.PRHeadSHA) {
		s.notifyUpgradePRHeadChanged(ctx, session, oldHead, session.PRHeadSHA)
	}
	if strings.EqualFold(session.GitHubPRState, "merged") {
		_, _, closeErr := s.closeCollabInternal(ctx, session, "closed", "upgrade_pr merged on GitHub", clawWorldSystemID)
		return closeErr
	}
	if strings.EqualFold(session.GitHubPRState, "closed") {
		_, _, closeErr := s.closeCollabInternal(ctx, session, "failed", "upgrade_pr pull request closed without merge", clawWorldSystemID)
		return closeErr
	}
	status, err := s.evaluateUpgradePRReviews(ctx, session, session.PRHeadSHA)
	if err != nil {
		return err
	}
	s.maybeNotifyUpgradePRReviewMilestones(ctx, session, status)
	s.maybeNotifyUpgradePRReviewReminder(ctx, session, status)
	if session.ReviewDeadlineAt != nil && !session.ReviewDeadlineAt.After(time.Now().UTC()) && !status.ReviewComplete {
		subjectPrefix := fmt.Sprintf("[UPGRADE-PR][ESCALATION] collab_id=%s", session.CollabID)
		body := fmt.Sprintf("collab_id=%s\npr_url=%s\nhead_sha=%s\nmessage=Review window expired without enough valid reviews.", session.CollabID, session.PRURL, session.PRHeadSHA)
		authorUserID := upgradePRAuthorUserID(session)
		if authorUserID != "" && s.hasRecentInboxSubject(ctx, authorUserID, subjectPrefix, time.Time{}, false) {
			_, _, closeErr := s.closeCollabInternal(ctx, session, "failed", "upgrade_pr review timed out after escalation", clawWorldSystemID)
			return closeErr
		}
		if _, err := s.store.UpdateCollabPR(ctx, store.CollabPRUpdate{
			CollabID:         session.CollabID,
			ReviewDeadlineAt: timePtr(time.Now().UTC().Add(upgradePRReviewExtendWindow)),
		}); err == nil {
			s.notifyUpgradePRAuthor(ctx, session, subjectPrefix, body+"\ndeadline_extension=24h")
		}
	}
	return nil
}
