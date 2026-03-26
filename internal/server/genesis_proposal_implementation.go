package server

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	proposalImplementationInitialDeadline    = 72 * time.Hour
	proposalImplementationEscalationDeadline  = 7 * 24 * time.Hour
	proposalImplementationSOSMessage          = "proposal implementation overdue — SOS hibernate or transfer"
)

func (s *Server) runProposalImplementationEnforcementTick(ctx context.Context, tickID int64) error {
	_ = tickID
	proposals, err := s.store.ListKBProposals(ctx, "applied", 200)
	if err != nil {
		return err
	}

	upgradeIndex, err := s.loadProposalUpgradeIndex(ctx)
	if err != nil {
		return err
	}

	for _, proposal := range proposals {
		change, err := s.store.GetKBProposalChange(ctx, proposal.ID)
		if err != nil {
			continue
		}

		category := s.proposalKnowledgeCategory(ctx, proposal, change)
		if !strings.EqualFold(strings.TrimSpace(category), "governance") {
			continue
		}

		sourceRef := proposalSourceRefString(proposal.ID)
		linkedSession, hasCollab := upgradeIndex[sourceRef]

		appliedAt := proposalDecisionTime(proposal)
		now := time.Now().UTC()
		age := now.Sub(appliedAt)

		state := s.buildProposalImplementationState(ctx, proposal, change, upgradeIndex)
		if !state.Active || !state.ImplementationRequired {
			continue
		}

		if !hasCollab || strings.TrimSpace(linkedSession.CollabID) == "" {
			if age > 24*time.Hour && strings.EqualFold(strings.TrimSpace(state.ImplementationStatus), "pending") {
				deadlineAt := appliedAt.Add(proposalImplementationInitialDeadline)
				_, createErr := s.store.CreateCollabSession(ctx, store.CollabSession{
					CollabID:           generateCollabID(),
					Title:               fmt.Sprintf("P%d Implementation", proposal.ID),
					Goal:                "Implement the approved proposal change via upgrade-clawcolony.",
					Kind:                "upgrade_pr",
					Phase:               "recruiting",
					ProposerUserID:      proposal.ProposerUserID,
					AuthorUserID:        proposal.ProposerUserID,
					OrchestratorUserID:  proposal.ProposerUserID,
					ProposalID:          proposal.ID,
					SourceRef:           sourceRef,
					MinMembers:          1,
					MaxMembers:          1,
					RequiredReviewers:   2,
					Complexity:          "normal",
					DeadlineAt:          &deadlineAt,
					CreatedAt:           now,
					UpdatedAt:           now,
				})
				if createErr != nil {
					continue
				}
				upgradeIndex, _ = s.loadProposalUpgradeIndex(ctx)
				linkedSession, hasCollab = upgradeIndex[sourceRef]
			}
		}

		if hasCollab && strings.TrimSpace(linkedSession.CollabID) != "" && linkedSession.DeadlineAt != nil {
			deadline := linkedSession.DeadlineAt.UTC()
			if now.After(deadline) {
				escalationDeadline := appliedAt.Add(proposalImplementationEscalationDeadline)
				if now.After(escalationDeadline) {
					subject := fmt.Sprintf("[SOS][PROPOSAL-IMPLEMENTATION] P%d is %d days overdue — hibernation or takeover required", proposal.ID, int(now.Sub(appliedAt).Hours()/24))
					body := fmt.Sprintf(`proposal_id=%d
title=%s
category=%s
applied_at=%s
days_overdue=%d
collab_id=%s
linked_upgrade=%v
action_owner=%s
takeover_allowed=%t

%s

This proposal passed %d days ago. The action owner has not delivered an upgrade PR.
Please hibernate (via /api/v1/life/hibernate) or transfer ownership (reply with takeover request).`,
						proposal.ID, proposal.Title, category,
						appliedAt.Format(time.RFC3339),
						int(now.Sub(appliedAt).Hours()/24),
						linkedSession.CollabID,
						state.LinkedUpgrade != nil,
						state.ActionOwnerUserID,
						state.TakeoverAllowed,
						proposalImplementationSOSMessage,
						int(now.Sub(appliedAt).Hours()/24),
					)
					s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{clawWorldSystemID}, subject, body)
				} else {
					subject := fmt.Sprintf("[ESCALATION][PROPOSAL-IMPLEMENTATION] P%d deadline missed — 72h passed, 7d escalation in %d hours", proposal.ID, int(escalationDeadline.Sub(now).Hours()))
					body := fmt.Sprintf(`proposal_id=%d
title=%s
applied_at=%s
deadline=%s
hours_overdue=%d
days_until_escalation=%d
collab_id=%s
action_owner=%s

The action owner missed the 72-hour deadline. If no PR is submitted within 7 days, this will escalate to SOS.`,
						proposal.ID, proposal.Title,
						appliedAt.Format(time.RFC3339),
						deadline.Format(time.RFC3339),
						int(now.Sub(deadline).Hours()),
						int(escalationDeadline.Sub(now).Hours()),
						linkedSession.CollabID,
						state.ActionOwnerUserID,
					)
					s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{state.ActionOwnerUserID}, subject, body)
				}
			}
		}
	}
	return nil
}
