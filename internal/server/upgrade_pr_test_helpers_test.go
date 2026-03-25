package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeUpgradePRGitHub struct {
	t               *testing.T
	repo            string
	number          int
	pull            githubPullRequestRecord
	comments        map[int64]githubIssueCommentRecord
	reviews         []githubPullReviewRecord
	requestHook     func(http.ResponseWriter, *http.Request) bool
	pullRequests    int
	commentRequests int
	reviewRequests  int
	server          *httptest.Server
}

func newFakeUpgradePRGitHub(t *testing.T, repo string, number int) *fakeUpgradePRGitHub {
	t.Helper()
	fixture := &fakeUpgradePRGitHub{
		t:        t,
		repo:     strings.TrimSpace(repo),
		number:   number,
		comments: map[int64]githubIssueCommentRecord{},
		reviews:  []githubPullReviewRecord{},
	}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == fmt.Sprintf("/repos/%s/pulls/%d", fixture.repo, fixture.number):
			fixture.pullRequests++
			if fixture.requestHook != nil && fixture.requestHook(w, r) {
				return
			}
			if err := json.NewEncoder(w).Encode(fixture.pull); err != nil {
				t.Fatalf("encode fake pull: %v", err)
			}
		case strings.HasPrefix(r.URL.Path, fmt.Sprintf("/repos/%s/issues/comments/", fixture.repo)):
			fixture.commentRequests++
			if fixture.requestHook != nil && fixture.requestHook(w, r) {
				return
			}
			idText := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/repos/%s/issues/comments/", fixture.repo))
			id, err := strconv.ParseInt(strings.TrimSpace(idText), 10, 64)
			if err != nil {
				http.Error(w, "bad comment id", http.StatusBadRequest)
				return
			}
			comment, ok := fixture.comments[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if err := json.NewEncoder(w).Encode(comment); err != nil {
				t.Fatalf("encode fake comment: %v", err)
			}
		case r.URL.Path == fmt.Sprintf("/repos/%s/pulls/%d/reviews", fixture.repo, fixture.number):
			fixture.reviewRequests++
			if fixture.requestHook != nil && fixture.requestHook(w, r) {
				return
			}
			if err := json.NewEncoder(w).Encode(fixture.reviews); err != nil {
				t.Fatalf("encode fake reviews: %v", err)
			}
		case strings.HasPrefix(r.URL.Path, fmt.Sprintf("/repos/%s/pulls/%d/reviews/", fixture.repo, fixture.number)):
			idText := strings.TrimPrefix(r.URL.Path, fmt.Sprintf("/repos/%s/pulls/%d/reviews/", fixture.repo, fixture.number))
			id, err := strconv.ParseInt(strings.TrimSpace(idText), 10, 64)
			if err != nil {
				http.Error(w, "bad review id", http.StatusBadRequest)
				return
			}
			for _, review := range fixture.reviews {
				if review.ID == id {
					if err := json.NewEncoder(w).Encode(review); err != nil {
						t.Fatalf("encode fake review: %v", err)
					}
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() {
		fixture.server.Close()
	})
	t.Setenv("CLAWCOLONY_GITHUB_API_BASE_URL", fixture.server.URL)
	return fixture
}

func (f *fakeUpgradePRGitHub) pullURL() string {
	return fmt.Sprintf("https://github.com/%s/pull/%d", f.repo, f.number)
}

func (f *fakeUpgradePRGitHub) commentURL(commentID int64) string {
	return fmt.Sprintf("%s#issuecomment-%d", f.pullURL(), commentID)
}

func (f *fakeUpgradePRGitHub) reviewURL(reviewID int64) string {
	return fmt.Sprintf("%s#pullrequestreview-%d", f.pullURL(), reviewID)
}

func makeUpgradePRApplyComment(repo string, number int, commentID int64, githubLogin, collabID, userID, note string) githubIssueCommentRecord {
	comment := githubIssueCommentRecord{
		ID:       commentID,
		HTMLURL:  fmt.Sprintf("https://github.com/%s/pull/%d#issuecomment-%d", repo, number, commentID),
		IssueURL: fmt.Sprintf("https://api.github.com/repos/%s/issues/%d", repo, number),
		Body: fmt.Sprintf(
			"[clawcolony-review-apply]\ncollab_id=%s\nuser_id=%s\nnote=%s",
			collabID,
			userID,
			note,
		),
	}
	comment.User.Login = githubLogin
	return comment
}

func makeUpgradePRAppliedReview(reviewID int64, githubLogin, userID, state, collabID, headSHA, judgement, summary, findings string, submittedAt time.Time) githubPullReviewRecord {
	review := makeUpgradePRReview(reviewID, githubLogin, state, collabID, headSHA, judgement, summary, findings, submittedAt)
	review.Body = fmt.Sprintf(
		"[clawcolony-review-apply]\ncollab_id=%s\nuser_id=%s\nhead_sha=%s\njudgement=%s\nsummary=%s\nfindings=%s",
		collabID,
		userID,
		headSHA,
		judgement,
		summary,
		findings,
	)
	return review
}

func makeUpgradePRReview(reviewID int64, githubLogin, state, collabID, headSHA, judgement, summary, findings string, submittedAt time.Time) githubPullReviewRecord {
	review := githubPullReviewRecord{
		ID:       reviewID,
		State:    state,
		CommitID: headSHA,
		Body: fmt.Sprintf(
			"collab_id=%s\nhead_sha=%s\njudgement=%s\nsummary=%s\nfindings=%s",
			collabID,
			headSHA,
			judgement,
			summary,
			findings,
		),
	}
	if !submittedAt.IsZero() {
		ts := submittedAt.UTC()
		review.SubmittedAt = &ts
	}
	review.User.Login = githubLogin
	return review
}
