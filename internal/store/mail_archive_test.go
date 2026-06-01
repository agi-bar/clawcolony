package store

import (
	"context"
	"testing"
)

func TestInMemoryArchiveSystemMailBatchKeepsLatestPerOwnerAndCategory(t *testing.T) {
	st := NewInMemory()
	ctx := context.Background()

	send := func(subject string) {
		t.Helper()
		if _, err := st.SendMail(ctx, MailSendInput{
			From:    "clawcolony-admin",
			To:      []string{"agent-1"},
			Subject: subject,
			Body:    "system mail archive coverage",
		}); err != nil {
			t.Fatalf("send mail %q: %v", subject, err)
		}
	}

	send("[LOW-TOKEN] first")
	send("[LOW-TOKEN] second")
	send("[LOW-TOKEN] third")
	send("[AUTONOMY-LOOP] first")
	send("[AUTONOMY-LOOP] second")

	preview, err := st.PreviewSystemMailArchive(ctx, []string{"low_token", "autonomy_loop"})
	if err != nil {
		t.Fatalf("preview archive: %v", err)
	}
	if preview.ArchiveMailboxCount != 6 {
		t.Fatalf("expected 6 mailbox rows to archive, got=%d", preview.ArchiveMailboxCount)
	}
	if preview.ArchiveMessageCount != 3 {
		t.Fatalf("expected 3 impacted messages, got=%d", preview.ArchiveMessageCount)
	}

	result, err := st.ArchiveSystemMailBatch(ctx, MailArchiveBatchInput{
		Categories: []string{"low_token", "autonomy_loop"},
		Limit:      10000,
		BatchID:    "test-batch",
	})
	if err != nil {
		t.Fatalf("archive batch: %v", err)
	}
	if result.ArchiveMailboxCount != 6 {
		t.Fatalf("expected 6 archived mailbox rows, got=%d", result.ArchiveMailboxCount)
	}
	if result.ArchiveMessageCount != 3 {
		t.Fatalf("expected 3 archived messages, got=%d", result.ArchiveMessageCount)
	}

	inbox, err := st.ListMailbox(ctx, "agent-1", "inbox", "", "", nil, nil, 20)
	if err != nil {
		t.Fatalf("list inbox after archive: %v", err)
	}
	if len(inbox) != 2 {
		t.Fatalf("expected latest inbox rows for two categories to remain, got=%d", len(inbox))
	}

	outbox, err := st.ListMailbox(ctx, "clawcolony-admin", "outbox", "", "", nil, nil, 20)
	if err != nil {
		t.Fatalf("list outbox after archive: %v", err)
	}
	if len(outbox) != 2 {
		t.Fatalf("expected latest outbox rows for two categories to remain, got=%d", len(outbox))
	}

	remaining, err := st.PreviewSystemMailArchive(ctx, []string{"low_token", "autonomy_loop"})
	if err != nil {
		t.Fatalf("preview remaining archive work: %v", err)
	}
	if remaining.ArchiveMailboxCount != 0 {
		t.Fatalf("expected no remaining archive candidates, got=%d", remaining.ArchiveMailboxCount)
	}
}
