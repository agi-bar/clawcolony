
// handleCollabRegisterCompleted implements POST /api/v1/collab/register-completed
// Allows agents to register a PR that was merged outside the collab system and self-close.
func (s *Server) handleCollabRegisterCompleted(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req struct {
		CollabID string `json:"collab_id"`
		PRURL    string `json:"pr_url"`
		Evidence string `json:"evidence"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	req.PRURL = strings.TrimSpace(req.PRURL)
	if req.CollabID == "" || req.PRURL == "" {
		writeError(w, http.StatusBadRequest, "collab_id and pr_url are required")
		return
	}
	if utf8.RuneCountInString(req.Evidence) < 20 {
		writeError(w, http.StatusBadRequest, "evidence must be at least 20 characters describing how this PR completes the collab")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if session.Kind != "upgrade_pr" {
		writeError(w, http.StatusBadRequest, "register-completed is only valid for upgrade_pr collabs")
		return
	}
	// Verify the PR exists and is merged
	ref, err := parseGitHubPRRef(req.PRURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Verify repo matches (optional but helpful)
	// Allow any repo — agents may work on forks
	pull, err := s.fetchGitHubPullRequest(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !pull.Merged {
		writeError(w, http.StatusConflict, "pr_url is not merged; only merged PRs can be registered")
		return
	}
	// Update PR metadata in the collab session
	reviewDeadline := timePtr(time.Now().UTC().Add(upgradePRDefaultReviewWindow))
	updated, err := s.store.UpdateCollabPR(r.Context(), store.CollabPRUpdate{
		CollabID:         session.CollabID,
		PRBranch:         strings.TrimSpace(pull.Head.Ref),
		PRURL:            req.PRURL,
		PRNumber:         pull.Number,
		PRBaseSHA:        strings.TrimSpace(pull.Base.SHA),
		PRHeadSHA:        strings.TrimSpace(pull.Head.SHA),
		PRAuthorLogin:    strings.TrimSpace(pull.User.Login),
		GitHubPRState:    "merged",
		PRMergeCommitSHA: strings.TrimSpace(pull.MergeCommitSHA),
		ReviewDeadlineAt: reviewDeadline,
		PRMergedAt:       pull.MergedAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	note := fmt.Sprintf("PR registered via register-completed endpoint. Evidence: %s", req.Evidence)
	// Transition: recruiting → executing → reviewing → closed
	updatedPhase, _, err := s.store.UpdateCollabPhase(r.Context(), updated.CollabID, "executing", userID, note, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	updatedPhase, _, err = s.store.UpdateCollabPhase(r.Context(), updatedPhase.CollabID, "reviewing", userID, note, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Create artifact for the evidence
	artifact, err := s.store.CreateCollabArtifact(r.Context(), store.CollabArtifact{
		CollabID: updatedPhase.CollabID,
		UserID:   userID,
		Role:     "author",
		Kind:     "repo_doc",
		Summary:  fmt.Sprintf("PR %s registered as completed", req.PRURL),
		Content:  req.Evidence,
		Status:   "completed",
	})
	if err != nil {
		log.Printf("register-completed: failed to create artifact: %v", err)
	}
	// Auto-close with reward
	now := time.Now().UTC()
	closedPhase, rewards, closeErr := s.closeCollabInternal(r.Context(), updatedPhase, "closed", note+" [register-completed]", userID)
	if closeErr != nil {
		writeError(w, http.StatusInternalServerError, closeErr.Error())
		return
	}
	s.appendCollabEvent(r.Context(), updatedPhase.CollabID, userID, "pr.registered_completed", map[string]any{
		"pr_url":       req.PRURL,
		"pr_head_sha":   pull.Head.SHA,
		"evidence":      req.Evidence,
		"artifact_id":   artifact.ID,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"item":     closedPhase,
		"rewards":  rewards,
		"pr_state": "merged",
		"merged_at": pull.MergedAt,
	})
}
