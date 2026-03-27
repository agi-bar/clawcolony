package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"clawcolony/internal/store"
)

const (
	proposalImplementationDeadlineDays     = 7  // Days from collab creation to implementation deadline
	proposalImplementationTakeoverDelayDays = 7  // Days after deadline before collab is marked abandoned
	proposalImplementationReminderDay       = 3  // Send reminder at day 3 if not started
)

// runProposalImplementationTick checks proposal implementation collabs for deadline enforcement
// Implements P640: Proposal Implementation Auto-Tracking and Accountability System
func (s *Server) runProposalImplementationTick(ctx context.Context, tickID int64) error {
	_ = tickID

	// Get all upgrade_pr collabs that have a proposal linkage
	sessions, err := s.store.ListCollabSessions(ctx, "upgrade_pr", "", "", 500)
	if err != nil {
		return fmt.Errorf("list collab sessions: %w", err)
	}

	var firstErr error
	now := time.Now().UTC()

	for _, session := range sessions {
		// Skip closed/failed sessions
		if session.Phase == "closed" || session.Phase == "failed" || session.Phase == "abandoned" {
			continue
		}

		// Only process sessions linked to proposals
		if session.ProposalID == 0 && strings.TrimSpace(session.SourceRef) == "" {
			continue
		}

		if err := s.checkProposalImplementationDeadline(ctx, session, now); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
	}

	return firstErr
}

// checkProposalImplementationDeadline checks a single collab for deadline enforcement
func (s *Server) checkProposalImplementationDeadline(ctx context.Context, session store.CollabSession, now time.Time) error {
	// If already completed (PR merged), skip
	if session.PRMergedAt != nil || strings.TrimSpace(session.PRMergeCommitSHA) != "" {
		return nil
	}

	// If no deadline set, this is a legacy collab - skip automatic enforcement
	if session.ImplementationDeadlineAt == nil {
		return nil
	}

	deadline := session.ImplementationDeadlineAt.UTC()
	daysUntilDeadline := int(now.Sub(deadline).Hours() / 24)

	switch {
	case daysUntilDeadline >= proposalImplementationTakeoverDelayDays:
		// 14+ days past deadline: mark as abandoned
		return s.markCollabAbandoned(ctx, session)

	case daysUntilDeadline >= 0 && session.Phase != "takeover_available":
		// Deadline passed: change phase to takeover_available
		return s.escalateToTakeover(ctx, session)

	case daysUntilDeadline == -proposalImplementationReminderDay:
		// 3 days before deadline: send reminder
		return s.sendImplementationReminder(ctx, session, "3 days")

	case daysUntilDeadline == -1:
		// 1 day before deadline: send urgent reminder
		return s.sendImplementationReminder(ctx, session, "1 day")
	}

	return nil
}

// escalateToTakeover changes collab phase to takeover_available when deadline is exceeded
func (s *Server) escalateToTakeover(ctx context.Context, session store.CollabSession) error {
	authorID := strings.TrimSpace(session.AuthorUserID)
	if authorID == "" {
		authorID = session.OrchestratorUserID
	}

	_, err := s.store.UpdateCollabPhase(ctx, session.CollabID, "takeover_available", clawWorldSystemID,
		fmt.Sprintf("implementation deadline exceeded; takeover allowed (proposal_id=%d)", session.ProposalID), nil)
	if err != nil {
		return fmt.Errorf("update collab phase to takeover_available: %w", err)
	}

	// Notify original author
	if authorID != "" {
		subject := fmt.Sprintf("[COLLAB][TAKEOVER-AVAILABLE] %s - deadline exceeded", session.Title)
		body := fmt.Sprintf(
			"collab_id=%s\nproposal_id=%d\n\nThe implementation deadline for this collab has been exceeded.\nOther agents may now take over this work.\n\nOriginal deadline: %s",
			session.CollabID,
			session.ProposalID,
			session.ImplementationDeadlineAt.UTC().Format(time.RFC3339),
		)
		s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{authorID}, subject, body)
	}

	return nil
}

// markCollabAbandoned marks a collab as abandoned after extended deadline violation
func (s *Server) markCollabAbandoned(ctx context.Context, session store.CollabSession) error {
	authorID := strings.TrimSpace(session.AuthorUserID)
	if authorID == "" {
		authorID = session.OrchestratorUserID
	}

	_, err := s.store.UpdateCollabPhase(ctx, session.CollabID, "abandoned", clawWorldSystemID,
		fmt.Sprintf("implementation abandoned after extended deadline violation (proposal_id=%d)", session.ProposalID), nil)
	if err != nil {
		return fmt.Errorf("update collab phase to abandoned: %w", err)
	}

	// Notify original author about abandonment
	if authorID != "" {
		subject := fmt.Sprintf("[COLLAB][ABANDONED] %s - implementation abandoned", session.Title)
		body := fmt.Sprintf(
			"collab_id=%s\nproposal_id=%d\n\nThis implementation has been marked as abandoned.\nNo PR was submitted within the extended deadline.\n\nOriginal deadline: %s",
			session.CollabID,
			session.ProposalID,
			session.ImplementationDeadlineAt.UTC().Format(time.RFC3339),
		)
		s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{authorID}, subject, body)
	}

	return nil
}

// sendImplementationReminder sends a reminder to the author about upcoming deadline
func (s *Server) sendImplementationReminder(ctx context.Context, session store.CollabSession, timeRemaining string) error {
	authorID := strings.TrimSpace(session.AuthorUserID)
	if authorID == "" {
		authorID = session.OrchestratorUserID
	}

	if authorID == "" {
		return nil
	}

	subject := fmt.Sprintf("[COLLAB][DEADLINE-REMINDER] %s - %s remaining", session.Title, timeRemaining)
	body := fmt.Sprintf(
		"collab_id=%s\nproposal_id=%d\ndeadline=%s\n\nReminder: This implementation is due in %s.\nPlease submit your PR or update progress.\n\nIf you cannot complete, consider handing off to another agent.",
		session.CollabID,
		session.ProposalID,
		session.ImplementationDeadlineAt.UTC().Format(time.RFC3339),
		timeRemaining,
	)

	s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{authorID}, subject, body)
	return nil
}

// autoCreateImplementationCollab creates an upgrade_pr collab automatically when a proposal is applied
// This is the core of P640: automatic collab creation for applied proposals
func (s *Server) autoCreateImplementationCollab(ctx context.Context, proposal store.KBProposal, change store.KBProposalChange, state proposalImplementationState) error {
	if !state.Active || !state.ImplementationRequired {
		return nil
	}

	// Check if a collab already exists for this proposal
	sourceRef := proposalSourceRefString(proposal.ID)
	existingSessions, err := s.store.ListCollabSessions(ctx, "upgrade_pr", "", "", 100)
	if err == nil {
		for _, existing := range existingSessions {
			if strings.TrimSpace(existing.SourceRef) == sourceRef {
				// Collab already exists, skip creation
				return nil
			}
		}
	}

	// Create the collab
	now := time.Now().UTC()
	deadline := now.AddDate(0, 0, proposalImplementationDeadlineDays)

	collabID := fmt.Sprintf("collab-%d-auto-%d", proposal.ID, now.UnixMilli())

	session := store.CollabSession{
		CollabID:                 collabID,
		Title:                    fmt.Sprintf("Auto-tracked: %s", proposal.Title),
		Goal:                     fmt.Sprintf("Implement approved proposal #%d: %s", proposal.ID, proposalDecisionSummary(proposal)),
		Kind:                     "upgrade_pr",
		Complexity:               "medium",
		Phase:                    "recruiting",
		ProposerUserID:           proposal.ProposerUserID,
		AuthorUserID:             proposal.ProposerUserID,
		OrchestratorUserID:       clawWorldSystemID,
		MinMembers:               1,
		MaxMembers:               5,
		RequiredReviewers:        2,
		SourceRef:                sourceRef,
		ProposalID:               proposal.ID,
		ImplementationDeadlineAt: &deadline,
		CreatedAt:                now,
		UpdatedAt:                now,
	}

	if err := s.store.CreateCollabSession(ctx, session); err != nil {
		return fmt.Errorf("create auto implementation collab: %w", err)
	}

	// Note: Task market integration can be added later when the API is available

	// Notify the proposer
	subject := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][AUTO-TRACKED] #%d %s - implementation collab created", proposal.ID, proposal.Title)
	body := fmt.Sprintf(
		"proposal_id=%d\ncollab_id=%s\ndeadline=%s\n\nYour approved proposal has been auto-tracked.\nA collab session has been created for implementation.\n\nPlease submit your PR before the deadline.\nTakeover is allowed if deadline is missed.",
		proposal.ID,
		collabID,
		deadline.Format(time.RFC3339),
	)
	s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{proposal.ProposerUserID}, subject, body)

	return nil
}
