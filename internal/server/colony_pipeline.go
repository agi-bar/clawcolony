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

func pipelineAuthorUserID(session store.CollabSession) string {
	authorID := strings.TrimSpace(session.AuthorUserID)
	if authorID == "" {
		authorID = strings.TrimSpace(session.OrchestratorUserID)
	}
	return authorID
}

func pipelineStageForUpgradePR(session store.CollabSession) string {
	switch {
	case upgradePRMerged(session):
		return "merged"
	case upgradePROpenForReview(session):
		return "under_review"
	case upgradePRClosedOnGitHub(session) || upgradePRTerminalPhase(session):
		return "recently_closed"
	default:
		return "in_progress"
	}
}

func pipelineSessionRecent(session store.CollabSession, since time.Time) bool {
	if session.ClosedAt != nil && session.ClosedAt.After(since) {
		return true
	}
	if session.PRMergedAt != nil && session.PRMergedAt.After(since) {
		return true
	}
	return session.UpdatedAt.After(since)
}

func pipelineItemWithUpgradeSession(base pipelineItem, session store.CollabSession) pipelineItem {
	base.CollabID = strings.TrimSpace(session.CollabID)
	base.CollabPhase = strings.ToLower(strings.TrimSpace(session.Phase))
	base.PRURL = strings.TrimSpace(session.PRURL)
	base.PRNumber = session.PRNumber
	base.PRState = upgradePRCanonicalState(session)
	base.PRMergedAt = session.PRMergedAt
	base.ImplementationMode = strings.TrimSpace(session.ImplementationMode)
	base.DeadlineAt = session.ImplementationDeadlineAt
	if base.DeadlineAt == nil {
		base.DeadlineAt = session.ReviewDeadlineAt
	}
	base.AuthorUserID = pipelineAuthorUserID(session)
	base.Stage = pipelineStageForUpgradePR(session)
	return base
}

func appendPipelineItem(sections *pipelineSections, item pipelineItem) {
	switch strings.ToLower(strings.TrimSpace(item.Stage)) {
	case "approved_pending":
		sections.ApprovedPending = append(sections.ApprovedPending, item)
	case "under_review":
		sections.UnderReview = append(sections.UnderReview, item)
	case "merged":
		sections.Merged = append(sections.Merged, item)
	case "recently_closed":
		sections.RecentlyClosed = append(sections.RecentlyClosed, item)
	default:
		item.Stage = "in_progress"
		sections.InProgress = append(sections.InProgress, item)
	}
}

func proposalIDFromSourceRef(sourceRef string) int64 {
	parts := strings.SplitN(strings.TrimSpace(sourceRef), ":", 2)
	if len(parts) != 2 || parts[0] != "kb_proposal" {
		return 0
	}
	var pid int64
	for _, ch := range parts[1] {
		if ch < '0' || ch > '9' {
			return 0
		}
		pid = pid*10 + int64(ch-'0')
	}
	return pid
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

	proposals, err := s.store.ListKBProposals(ctx, "applied", 5000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list proposals")
		return
	}
	allUpgradeSessions, err := s.store.ListCollabSessions(ctx, "upgrade_pr", "", "", 1000)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list upgrade sessions")
		return
	}

	upgradeIndex := make(map[string]store.CollabSession, len(allUpgradeSessions))
	closedByRef := make(map[string]store.CollabSession, len(allUpgradeSessions))
	manualByPRKey := make(map[string]store.CollabSession)
	manualWithoutPR := make([]store.CollabSession, 0)
	for _, session := range allUpgradeSessions {
		if ref := strings.TrimSpace(session.SourceRef); ref != "" {
			if current, ok := upgradeIndex[ref]; !ok || session.UpdatedAt.After(current.UpdatedAt) {
				upgradeIndex[ref] = session
			}
			if pipelineStageForUpgradePR(session) == "recently_closed" {
				if current, ok := closedByRef[ref]; !ok || session.UpdatedAt.After(current.UpdatedAt) {
					closedByRef[ref] = session
				}
			}
			continue
		}
		if prKey := canonicalGitHubPRKeyForSession(session); prKey != "" {
			if current, ok := manualByPRKey[prKey]; !ok || session.UpdatedAt.After(current.UpdatedAt) {
				manualByPRKey[prKey] = session
			}
			continue
		}
		manualWithoutPR = append(manualWithoutPR, session)
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
	activePRKeys := make(map[string]struct{})
	capturedProposals := make(map[int64]struct{}, len(proposals))

	for _, proposal := range proposals {
		capturedProposals[proposal.ID] = struct{}{}
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
			item.Stage = "approved_pending"
			appendPipelineItem(&sections, item)
			stats.PendingImplementations++
			continue
		}

		item = pipelineItemWithUpgradeSession(item, session)
		if item.Stage == "recently_closed" && !pipelineSessionRecent(session, sevenDaysAgo) {
			continue
		}
		appendPipelineItem(&sections, item)
		if item.Stage == "merged" && pipelineSessionRecent(session, sevenDaysAgo) {
			stats.MergedLast7d++
		}
		if item.Stage == "recently_closed" {
			stats.Abandoned++
		}
		if upgradePROpenForReview(session) {
			if prKey := canonicalGitHubPRKeyForSession(session); prKey != "" {
				activePRKeys[prKey] = struct{}{}
			}
		}
	}

	for ref, session := range closedByRef {
		pid := proposalIDFromSourceRef(ref)
		if pid == 0 {
			continue
		}
		if _, already := capturedProposals[pid]; already {
			continue
		}
		if !pipelineSessionRecent(session, sevenDaysAgo) {
			continue
		}
		item := pipelineItemWithUpgradeSession(pipelineItem{
			ProposalID:      pid,
			ProposalTitle:   strings.TrimSpace(session.Title),
			TakeoverAllowed: false,
		}, session)
		item.Stage = "recently_closed"
		appendPipelineItem(&sections, item)
		stats.Abandoned++
	}

	for _, session := range manualByPRKey {
		item := pipelineItemWithUpgradeSession(pipelineItem{
			ProposalTitle:   strings.TrimSpace(session.Title),
			ProposerUserID:  strings.TrimSpace(session.ProposerUserID),
			Category:        "upgrade_pr",
			TakeoverAllowed: false,
		}, session)
		if item.Stage == "recently_closed" && !pipelineSessionRecent(session, sevenDaysAgo) {
			continue
		}
		appendPipelineItem(&sections, item)
		if item.Stage == "merged" && pipelineSessionRecent(session, sevenDaysAgo) {
			stats.MergedLast7d++
		}
		if item.Stage == "recently_closed" {
			stats.Abandoned++
		}
		if upgradePROpenForReview(session) {
			if prKey := canonicalGitHubPRKeyForSession(session); prKey != "" {
				activePRKeys[prKey] = struct{}{}
			}
		}
	}

	for _, session := range manualWithoutPR {
		item := pipelineItemWithUpgradeSession(pipelineItem{
			ProposalTitle:   strings.TrimSpace(session.Title),
			ProposerUserID:  strings.TrimSpace(session.ProposerUserID),
			Category:        "upgrade_pr",
			TakeoverAllowed: false,
		}, session)
		if item.Stage == "recently_closed" && !pipelineSessionRecent(session, sevenDaysAgo) {
			continue
		}
		appendPipelineItem(&sections, item)
		if item.Stage == "merged" && pipelineSessionRecent(session, sevenDaysAgo) {
			stats.MergedLast7d++
		}
		if item.Stage == "recently_closed" {
			stats.Abandoned++
		}
	}

	stats.ActivePRs = len(activePRKeys)

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
