package server

import (
	"strings"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func TestAutoCreateImplementationCollabCreatesProposalLinkedUpgrade(t *testing.T) {
	srv := newTestServer()
	now := time.Now().UTC()
	proposal := store.KBProposal{
		ID:             42,
		ProposerUserID: "agent-proposer",
		Title:          "Heartbeat should notice codebase gaps",
		Reason:         "Empty inbox should not hide missing community improvements.",
		Status:         "applied",
		ClosedAt:       &now,
		AppliedAt:      &now,
	}

	err := srv.autoCreateImplementationCollab(t.Context(), proposal, store.KBProposalChange{}, proposalImplementationState{
		Active:                 true,
		ImplementationRequired: true,
	})
	if err != nil {
		t.Fatalf("autoCreateImplementationCollab: %v", err)
	}

	sessions, err := srv.store.ListCollabSessions(t.Context(), "upgrade_pr", "", "", 10)
	if err != nil {
		t.Fatalf("ListCollabSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("collab count=%d want 1", len(sessions))
	}

	got := sessions[0]
	if got.ProposalID != proposal.ID {
		t.Fatalf("proposal_id=%d want %d", got.ProposalID, proposal.ID)
	}
	if got.SourceRef != proposalSourceRefString(proposal.ID) {
		t.Fatalf("source_ref=%q want %q", got.SourceRef, proposalSourceRefString(proposal.ID))
	}
	if got.AuthorUserID != proposal.ProposerUserID {
		t.Fatalf("author_user_id=%q want %q", got.AuthorUserID, proposal.ProposerUserID)
	}
	if got.ImplementationDeadlineAt == nil {
		t.Fatalf("implementation_deadline_at=nil want set")
	}

	inbox, err := srv.store.ListMailbox(t.Context(), proposal.ProposerUserID, "inbox", "", "[AUTO-TRACKED]", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListMailbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("auto-tracked inbox count=%d want 1", len(inbox))
	}
	if !strings.Contains(inbox[0].Body, "collab_id="+got.CollabID) {
		t.Fatalf("mail body missing collab id %q body=%s", got.CollabID, inbox[0].Body)
	}
}
