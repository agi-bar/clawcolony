package server

import (
	"context"
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

func TestSendImplementationReminderDedupBlocksRepeatsWithinOneHour(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	author := seedActiveUser(t, srv)
	now := time.Now().UTC()
	deadline := now.Add(3 * 24 * time.Hour) // 3 days from now — triggers "3 days" reminder

	session := store.CollabSession{
		CollabID:              "collab-test-dedup-" + author[:8],
		Title:                 "Test collab for dedup",
		Goal:                  "Test implementation",
		Kind:                  "upgrade_pr",
		Phase:                 "implementing",
		ProposerUserID:        author,
		AuthorUserID:          author,
		SourceRef:             "kb_proposal:99999",
		ImplementationDeadlineAt: &deadline,
	}
	if err := srv.store.CreateCollabSession(ctx, session); err != nil {
		t.Fatalf("CreateCollabSession: %v", err)
	}

	// First call — should send (no prior dedup state)
	if err := srv.sendImplementationReminder(ctx, session, "3 days"); err != nil {
		t.Fatalf("first sendImplementationReminder: %v", err)
	}

	// Verify mail was sent
	inbox1, err := srv.store.ListMailbox(ctx, author, "inbox", "", "[COLLAB][DEADLINE-REMINDER]", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListMailbox: %v", err)
	}
	if len(inbox1) != 1 {
		t.Fatalf("first call: expected 1 mail, got=%d", len(inbox1))
	}

	// Verify dedup state was created
	state, ok, err := srv.store.GetNotificationDeliveryState(ctx, author, collabDeadlineRemind)
	if err != nil {
		t.Fatalf("GetNotificationDeliveryState: %v", err)
	}
	if !ok {
		t.Fatalf("expected dedup state to be created")
	}
	if state.StateHash != session.CollabID+":3 days" {
		t.Fatalf("state_hash=%q want=%q", state.StateHash, session.CollabID+":3 days")
	}

	// Second call within 1 hour — should be blocked by dedup
	if err := srv.sendImplementationReminder(ctx, session, "3 days"); err != nil {
		t.Fatalf("second sendImplementationReminder: %v", err)
	}
	inbox2, err := srv.store.ListMailbox(ctx, author, "inbox", "", "[COLLAB][DEADLINE-REMINDER]", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListMailbox: %v", err)
	}
	if len(inbox2) != 1 {
		t.Fatalf("second call within 1 hour: expected still 1 mail (deduped), got=%d", len(inbox2))
	}

	// Backdate the dedup state to 2 hours ago — should allow resend
	state.LastSentAt = time.Now().UTC().Add(-2 * time.Hour)
	if _, err := srv.store.UpsertNotificationDeliveryState(ctx, state); err != nil {
		t.Fatalf("UpsertNotificationDeliveryState: %v", err)
	}

	if err := srv.sendImplementationReminder(ctx, session, "3 days"); err != nil {
		t.Fatalf("third sendImplementationReminder after backdate: %v", err)
	}
	inbox3, err := srv.store.ListMailbox(ctx, author, "inbox", "", "[COLLAB][DEADLINE-REMINDER]", nil, nil, 10)
	if err != nil {
		t.Fatalf("ListMailbox: %v", err)
	}
	if len(inbox3) != 2 {
		t.Fatalf("third call after backdate: expected 2 mails, got=%d", len(inbox3))
	}
}
