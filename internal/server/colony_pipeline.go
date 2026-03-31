package server

import (
	"math"
	"net/http"
	"strings"
	"time"

	"clawcolony/internal/store"
)

// --- response types ---

type pipelineItem struct {
	ProposalID         int64      `json:"proposal_id"`
	ProposalTitle      string     `json:"proposal_title"`
	ProposerUserID     string     `json:"proposer_user_id"`
	Category           string     `json:"category"`
	ApprovedAt         *time.Time `json:"approved_at"`
	ImplementationMode string     `json:"implementation_mode"`
	Stage              string     `json:"stage"`
	CollabID           string     `json:"collab_id,omitempty"`
	CollabPhase        string     `json:"collab_phase,omitempty"`
	PRURL              string     `json:"pr_url,omitempty"`
	PRNumber           int        `json:"pr_number,omitempty"`
	PRState            string     `json:"pr_state,omitempty"`
	PRMergedAt         *time.Time `json:"pr_merged_at"`
	AuthorUserID       string     `json:"author_user_id,omitempty"`
	DeadlineAt         *time.Time `json:"deadline_at,omitempty"`
	TakeoverAllowed    bool       `json:"takeover_allowed"`
	DaysSinceApproval  int        `json:"days_since_approval"`
}

type pipelineSections struct {
	ApprovedPending []pipelineItem `json:"approved_pending"`
	InProgress      []pipelineItem `json:"in_progress"`
	UnderReview     []pipelineItem `json:"under_review"`
	Merged          []pipelineItem `json:"merged"`
	RecentlyClosed  []pipelineItem `json:"recently_closed"`
}

type pipelineStats struct {
	TotalProposalsApplied  int `json:"total_proposals_applied"`
	PendingImplementations int `json:"pending_implementations"`
	ActivePRs              int `json:"active_prs"`
	MergedLast7d           int `json:"merged_last_7d"`
	Abandoned              int `json:"abandoned"`
}

type pipelineSyncStatus struct {
	LastSyncTickID  int64     `json:"last_sync_tick_id"`
	LastSyncAt      time.Time `json:"last_sync_at"`
	RepoSyncEnabled bool      `json:"repo_sync_enabled"`
}

type pipelineResponse struct {
	AsOf       time.Time          `json:"as_of"`
	SyncStatus pipelineSyncStatus `json:"sync_status"`
	Pipeline   pipelineSections   `json:"pipeline"`
	Stats      pipelineStats      `json:"stats"`
}

// --- handler ---

func (s *Server) handleColonyPipeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()
	sevenDaysAgo := now.AddDate(0, 0, -7)

	// Load all applied proposals.
	proposals, err := s.store.ListKBProposals(ctx, "applied", 5000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list proposals")
		return
	}

	// Build upgrade index: sourceRef -> CollabSession for active sessions.
	upgradeIndex, err := s.loadProposalUpgradeIndex(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load upgrade index")
		return
	}

	// Load recently closed collab sessions.
	closedSessions, err := s.store.ListCollabSessions(ctx, "upgrade_pr", "closed", "", 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list closed sessions")
		return
	}

	// Build a set of sourceRefs for closed sessions (for recently_closed bucket).
	closedByRef := make(map[string]store.CollabSession, len(closedSessions))
	for _, cs := range closedSessions {
		ref := strings.TrimSpace(cs.SourceRef)
		if ref == "" {
			continue
		}
		// Keep the most recently updated one per ref.
		if existing, ok := closedByRef[ref]; !ok || cs.UpdatedAt.After(existing.UpdatedAt) {
			closedByRef[ref] = cs
		}
	}

	sections := pipelineSections{
		ApprovedPending: make([]pipelineItem, 0),
		InProgress:      make([]pipelineItem, 0),
		UnderReview:     make([]pipelineItem, 0),
		Merged:          make([]pipelineItem, 0),
		RecentlyClosed:  make([]pipelineItem, 0),
	}

	var stats pipelineStats
	stats.TotalProposalsApplied = len(proposals)

	for _, proposal := range proposals {
		change, changeErr := s.store.GetKBProposalChange(ctx, proposal.ID)
		if changeErr != nil {
			continue
		}

		category := s.proposalKnowledgeCategory(ctx, proposal, change)
		sourceRef := proposalSourceRefString(proposal.ID)
		session, hasSession := upgradeIndex[sourceRef]

		approvedAt := proposal.AppliedAt
		daysSinceApproval := 0
		if approvedAt != nil && !approvedAt.IsZero() {
			daysSinceApproval = int(math.Floor(now.Sub(approvedAt.UTC()).Hours() / 24))
			if daysSinceApproval < 0 {
				daysSinceApproval = 0
			}
		}

		item := pipelineItem{
			ProposalID:        proposal.ID,
			ProposalTitle:     strings.TrimSpace(proposal.Title),
			ProposerUserID:    strings.TrimSpace(proposal.ProposerUserID),
			Category:          category,
			ApprovedAt:        approvedAt,
			TakeoverAllowed:   true,
			DaysSinceApproval: daysSinceApproval,
		}

		if !hasSession || strings.TrimSpace(session.CollabID) == "" {
			// No collab yet — approved pending.
			item.Stage = "approved_pending"
			item.ImplementationMode = ""
			sections.ApprovedPending = append(sections.ApprovedPending, item)
			stats.PendingImplementations++
			continue
		}

		// Has a linked collab session.
		collabID := strings.TrimSpace(session.CollabID)
		phase := strings.TrimSpace(strings.ToLower(session.Phase))
		prState := strings.TrimSpace(strings.ToLower(session.GitHubPRState))
		prURL := strings.TrimSpace(session.PRURL)
		prNumber := session.PRNumber
		implMode := strings.TrimSpace(session.ImplementationMode)

		item.CollabID = collabID
		item.CollabPhase = phase
		item.PRURL = prURL
		item.PRNumber = prNumber
		item.PRState = prState
		item.PRMergedAt = session.PRMergedAt
		item.ImplementationMode = implMode
		item.DeadlineAt = session.ImplementationDeadlineAt
		if item.DeadlineAt == nil {
			item.DeadlineAt = session.ReviewDeadlineAt
		}

		// Determine author: prefer AuthorUserID, fall back to OrchestratorUserID.
		authorID := strings.TrimSpace(session.AuthorUserID)
		if authorID == "" {
			authorID = strings.TrimSpace(session.OrchestratorUserID)
		}
		item.AuthorUserID = authorID

		// Classify into pipeline stage.
		merged := session.PRMergedAt != nil ||
			strings.TrimSpace(session.PRMergeCommitSHA) != "" ||
			strings.EqualFold(prState, "merged")

		if merged {
			item.Stage = "merged"
			sections.Merged = append(sections.Merged, item)
			if session.PRMergedAt != nil && session.PRMergedAt.After(sevenDaysAgo) {
				stats.MergedLast7d++
			} else if merged && session.UpdatedAt.After(sevenDaysAgo) {
				// If PRMergedAt is nil but we know it's merged and was updated recently, count it.
				stats.MergedLast7d++
			}
			continue
		}

		if strings.EqualFold(phase, "closed") || strings.EqualFold(phase, "failed") || strings.EqualFold(phase, "abandoned") {
			// Check if closed within last 7 days.
			closedAt := session.ClosedAt
			recentEnough := false
			if closedAt != nil && closedAt.After(sevenDaysAgo) {
				recentEnough = true
			} else if session.UpdatedAt.After(sevenDaysAgo) {
				recentEnough = true
			}
			if recentEnough {
				item.Stage = "recently_closed"
				sections.RecentlyClosed = append(sections.RecentlyClosed, item)
				stats.Abandoned++
			}
			continue
		}

		if strings.EqualFold(phase, "reviewing") || strings.EqualFold(phase, "review") {
			item.Stage = "under_review"
			sections.UnderReview = append(sections.UnderReview, item)
			if prNumber > 0 && !strings.EqualFold(prState, "closed") {
				stats.ActivePRs++
			}
			continue
		}

		// Default: in_progress (drafting, open, implementing, etc.)
		item.Stage = "in_progress"
		sections.InProgress = append(sections.InProgress, item)
		if prNumber > 0 && !strings.EqualFold(prState, "closed") {
			stats.ActivePRs++
		}
	}

	// Also check closed sessions for proposals not already captured.
	// These are sessions whose proposals might not be in "applied" status anymore.
	capturedProposals := make(map[int64]struct{}, len(proposals))
	for _, p := range proposals {
		capturedProposals[p.ID] = struct{}{}
	}
	for ref, cs := range closedByRef {
		// Parse proposal ID from sourceRef "kb_proposal:123".
		parts := strings.SplitN(ref, ":", 2)
		if len(parts) != 2 || parts[0] != "kb_proposal" {
			continue
		}
		pid := int64(0)
		for _, ch := range parts[1] {
			if ch >= '0' && ch <= '9' {
				pid = pid*10 + int64(ch-'0')
			} else {
				pid = 0
				break
			}
		}
		if pid == 0 {
			continue
		}
		if _, already := capturedProposals[pid]; already {
			continue
		}

		closedAt := cs.ClosedAt
		recentEnough := false
		if closedAt != nil && closedAt.After(sevenDaysAgo) {
			recentEnough = true
		} else if cs.UpdatedAt.After(sevenDaysAgo) {
			recentEnough = true
		}
		if !recentEnough {
			continue
		}

		item := pipelineItem{
			ProposalID:      pid,
			ProposalTitle:   strings.TrimSpace(cs.Title),
			CollabID:        strings.TrimSpace(cs.CollabID),
			CollabPhase:     strings.TrimSpace(cs.Phase),
			PRURL:           strings.TrimSpace(cs.PRURL),
			PRNumber:        cs.PRNumber,
			PRState:         strings.TrimSpace(cs.GitHubPRState),
			PRMergedAt:      cs.PRMergedAt,
			Stage:           "recently_closed",
			TakeoverAllowed: false,
		}
		authorID := strings.TrimSpace(cs.AuthorUserID)
		if authorID == "" {
			authorID = strings.TrimSpace(cs.OrchestratorUserID)
		}
		item.AuthorUserID = authorID
		sections.RecentlyClosed = append(sections.RecentlyClosed, item)
		stats.Abandoned++
	}

	// Gather sync status.
	s.worldTickMu.Lock()
	tickID := s.worldTickID
	tickAt := s.worldTickAt
	s.worldTickMu.Unlock()

	syncStatus := pipelineSyncStatus{
		LastSyncTickID:  tickID,
		LastSyncAt:      tickAt,
		RepoSyncEnabled: s.cfg.ColonyRepoSync,
	}

	result := pipelineResponse{
		AsOf:       now,
		SyncStatus: syncStatus,
		Pipeline:   sections,
		Stats:      stats,
	}

	writeJSON(w, http.StatusOK, result)
}
