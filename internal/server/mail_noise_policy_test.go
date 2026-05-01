package server

import (
	"context"
	"net/http"
	"sort"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func seedMailForNoisePolicyTest(t *testing.T, srv *Server, input store.MailSendInput, sentAt time.Time) store.MailSendResult {
	t.Helper()
	result, err := srv.store.SendMail(context.Background(), input)
	if err != nil {
		t.Fatalf("seed mail: %v", err)
	}
	if !sentAt.IsZero() {
		if err := srv.store.UpdateMailMessage(context.Background(), result.MessageID, input.Subject, input.Body, sentAt); err != nil {
			t.Fatalf("backdate mail %d: %v", result.MessageID, err)
		}
	}
	return result
}

func unreadMailSubjects(t *testing.T, srv *Server, owner string, keyword string) []string {
	t.Helper()
	items, err := srv.store.ListMailbox(context.Background(), owner, "inbox", "unread", keyword, nil, nil, 50)
	if err != nil {
		t.Fatalf("list unread mailbox owner=%s keyword=%q: %v", owner, keyword, err)
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Subject)
	}
	sort.Strings(out)
	return out
}

func TestMailSendSuppressesExactUnreadDuplicatesButAllowsAfterRead(t *testing.T) {
	srv := newTestServer()
	sender := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)
	payload := map[string]any{
		"to_user_ids": []string{recipient.id},
		"subject":     "design sync",
		"body":        "same exact progress report",
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", payload, sender.headers())
	if resp.Code != http.StatusAccepted {
		t.Fatalf("first send status=%d body=%s", resp.Code, resp.Body.String())
	}
	resp = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", payload, sender.headers())
	if resp.Code != http.StatusAccepted {
		t.Fatalf("second send status=%d body=%s", resp.Code, resp.Body.String())
	}

	ctx := context.Background()
	unread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "design sync", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread inbox after duplicate send: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("expected one unread exact duplicate after suppression, got=%d", len(unread))
	}
	outbox, err := srv.store.ListMailbox(ctx, sender.id, "outbox", "", "design sync", nil, nil, 20)
	if err != nil {
		t.Fatalf("list outbox after duplicate send: %v", err)
	}
	if len(outbox) != 1 {
		t.Fatalf("expected one outbox item after suppressed duplicate, got=%d", len(outbox))
	}
	if err := srv.store.MarkMailboxRead(ctx, recipient.id, []int64{unread[0].MailboxID}); err != nil {
		t.Fatalf("mark duplicate read: %v", err)
	}

	resp = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", payload, sender.headers())
	if resp.Code != http.StatusAccepted {
		t.Fatalf("third send after read status=%d body=%s", resp.Code, resp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "design sync", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread inbox after read+resend: %v", err)
	}
	if len(unreadAfter) != 1 {
		t.Fatalf("expected resend after read to recreate unread mail, got=%d", len(unreadAfter))
	}
	outboxAfter, err := srv.store.ListMailbox(ctx, sender.id, "outbox", "", "design sync", nil, nil, 20)
	if err != nil {
		t.Fatalf("list outbox after read+resend: %v", err)
	}
	if len(outboxAfter) != 2 {
		t.Fatalf("expected second outbox item after read+resend, got=%d", len(outboxAfter))
	}
}

func TestMailNoiseFamilyDedupeSuppressesSystemReminderButNotGenericHumanMail(t *testing.T) {
	srv := newTestServer()
	recipient := newAuthUser(t, srv)
	human := newAuthUser(t, srv)
	ctx := context.Background()

	srv.sendMailAndPushHint(ctx, clawWorldSystemID, []string{recipient.id}, "[COLLAB][DEADLINE-REMINDER] Town Hall - 3 days remaining", "collab_id=collab-1\nproposal_id=41")
	srv.sendMailAndPushHint(ctx, clawWorldSystemID, []string{recipient.id}, "[COLLAB][DEADLINE-REMINDER] Town Hall - 1 day remaining", "collab_id=collab-1\nproposal_id=41")
	srv.sendMailAndPushHint(ctx, clawWorldSystemID, []string{recipient.id}, "[COLLAB][DEADLINE-REMINDER] Harbor Gate - 2 days remaining", "collab_id=collab-2\nproposal_id=42")

	systemUnread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[COLLAB][DEADLINE-REMINDER]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list collab deadline unread: %v", err)
	}
	if len(systemUnread) != 2 {
		t.Fatalf("expected one unread per collab deadline family key, got=%d", len(systemUnread))
	}

	srv.sendMailAndPushHint(ctx, human.id, []string{recipient.id}, "[TEAM][REMINDER] Project Harbor", "collab_id=collab-1\nnote=first")
	srv.sendMailAndPushHint(ctx, human.id, []string{recipient.id}, "[TEAM][REMINDER] Project Harbor", "collab_id=collab-1\nnote=second")

	humanUnread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[TEAM][REMINDER] Project Harbor", nil, nil, 20)
	if err != nil {
		t.Fatalf("list human reminder unread: %v", err)
	}
	if len(humanUnread) != 2 {
		t.Fatalf("expected generic human mail to remain stackable when only family-like content changes, got=%d", len(humanUnread))
	}
}

func TestMailNoiseAutonomyLoopSlashSuppressesDuplicateReportsForAdminInbox(t *testing.T) {
	srv := newTestServer()
	sender := newAuthUser(t, srv)
	ctx := context.Background()

	srv.sendMailAndPushHint(ctx, sender.id, []string{clawWorldSystemID}, "autonomy-loop/12/"+sender.id, "result=evidence/next\nartifact_id=harbor-1")
	srv.sendMailAndPushHint(ctx, sender.id, []string{clawWorldSystemID}, "autonomy-loop/13/"+sender.id, "result=evidence/next\nartifact_id=harbor-2")

	unread, err := srv.store.ListMailbox(ctx, clawWorldSystemID, "inbox", "unread", "autonomy-loop/", nil, nil, 20)
	if err != nil {
		t.Fatalf("list admin autonomy unread: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("expected one unread autonomy-loop/ report in admin inbox, got=%d", len(unread))
	}
	outbox, err := srv.store.ListMailbox(ctx, sender.id, "outbox", "", "autonomy-loop/", nil, nil, 20)
	if err != nil {
		t.Fatalf("list sender outbox autonomy reports: %v", err)
	}
	if len(outbox) != 1 {
		t.Fatalf("expected suppressed admin duplicate to avoid second outbox entry, got=%d", len(outbox))
	}
}

func TestMailInboxFetchAutoReadsOnlyStaleSystemNoise(t *testing.T) {
	srv := newTestServer()
	recipient := newAuthUser(t, srv)
	human := newAuthUser(t, srv)
	old := time.Now().UTC().Add(-73 * time.Hour)

	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[SOS][HIBERNATING] old-user needs revival",
		Body:    "stale-system-sos",
	}, old)
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[SOS][HIBERNATING] fresh-user needs revival",
		Body:    "fresh-system-sos",
	}, time.Time{})
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    human.id,
		To:      []string{recipient.id},
		Subject: "[SOS][HIBERNATING] peer-user needs revival",
		Body:    "old-human-sos",
	}, old)
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[WORLD-COST-ALERT] user=x amount=10 threshold=5",
		Body:    "old-non-noise-system",
	}, old)

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?scope=unread&limit=50", nil, recipient.headers())
	if resp.Code != http.StatusOK {
		t.Fatalf("mail inbox status=%d body=%s", resp.Code, resp.Body.String())
	}

	if got := len(unreadMailSubjects(t, srv, recipient.id, "stale-system-sos")); got != 0 {
		t.Fatalf("expected stale system noise mail to auto-read, unread=%d", got)
	}
	if got := len(unreadMailSubjects(t, srv, recipient.id, "fresh-system-sos")); got != 1 {
		t.Fatalf("expected fresh system noise mail to remain unread, got=%d", got)
	}
	if got := len(unreadMailSubjects(t, srv, recipient.id, "old-human-sos")); got != 1 {
		t.Fatalf("expected non-system old mail to remain unread, got=%d", got)
	}
	if got := len(unreadMailSubjects(t, srv, recipient.id, "old-non-noise-system")); got != 1 {
		t.Fatalf("expected old non-noise system mail to remain unread, got=%d", got)
	}
}

func TestMailSystemResolveObsoleteMailIncludesUnreadSystemOwnersAndNewNoiseFamilies(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	oldTaskMarket := time.Now().UTC().Add(-25 * time.Hour)
	oldReview := time.Now().UTC().Add(-8 * 24 * time.Hour)

	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{clawWorldSystemID},
		Subject: "[TASK-MARKET][PRIORITY:P1] tick=1413 open_tasks=8 reward_token_max=20000",
		Body:    "old-task-market",
	}, oldTaskMarket)
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{clawWorldSystemID},
		Subject: "[COMMUNITY-COLLAB][PINNED][PRIORITY:P1][ACTION:PROPOSAL] collab_id=collab-100 title=Harbor Revival [REF:collab-mode.md]",
		Body:    "proposal_id=100",
	}, oldReview)
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{"clawcolony-assistant"},
		Subject: "[UPGRADE-PR][REVIEW-OPEN] collab_id=collab-200 [REF:upgrade-clawcolony.md]",
		Body:    "upgrade-pr-open",
	}, oldReview)

	dryRunResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-mail", map[string]any{
		"dry_run": true,
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if dryRunResp.Code != http.StatusOK {
		t.Fatalf("dry run system-owner cleanup status=%d body=%s", dryRunResp.Code, dryRunResp.Body.String())
	}
	dryRunBody := parseJSONBody(t, dryRunResp)
	dryRunResult := dryRunBody["result"].(map[string]any)
	if got := int(dryRunResult["affected_user_count"].(float64)); got != 2 {
		t.Fatalf("expected system-owner cleanup to affect admin+assistant, got=%d body=%s", got, dryRunResp.Body.String())
	}
	if got := int(dryRunResult["resolved_mailbox_count"].(float64)); got != 3 {
		t.Fatalf("expected task-market/community-collab/upgrade-pr cleanup to resolve 3 items, got=%d body=%s", got, dryRunResp.Body.String())
	}

	applyResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-mail", map[string]any{
		"dry_run": false,
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if applyResp.Code != http.StatusAccepted {
		t.Fatalf("apply system-owner cleanup status=%d body=%s", applyResp.Code, applyResp.Body.String())
	}

	if got := len(unreadMailSubjects(t, srv, clawWorldSystemID, "old-task-market")); got != 0 {
		t.Fatalf("expected stale task-market admin mail to auto-read, got=%d", got)
	}
	if got := len(unreadMailSubjects(t, srv, clawWorldSystemID, "Harbor Revival")); got != 0 {
		t.Fatalf("expected stale community-collab admin mail to auto-read, got=%d", got)
	}
	if got := len(unreadMailSubjects(t, srv, "clawcolony-assistant", "upgrade-pr-open")); got != 0 {
		t.Fatalf("expected stale upgrade-pr assistant mail to auto-read, got=%d", got)
	}
}

func TestMailSystemResolveObsoleteMailAutoReadsReportInboxesOnly(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	human := newAuthUser(t, srv)
	regular := newAuthUser(t, srv)
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)

	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    human.id,
		To:      []string{clawWorldSystemID},
		Subject: "autonomy-loop/50/" + human.id,
		Body:    "result/evidence/next\nartifact_id=admin-report",
	}, old)
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    human.id,
		To:      []string{"clawcolony-assistant"},
		Subject: "[COLLAB] Governance Participation",
		Body:    "governance-report",
	}, old)
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    human.id,
		To:      []string{regular.id},
		Subject: "autonomy-loop/51/" + human.id,
		Body:    "result/evidence/next\nartifact_id=peer-report",
	}, old)

	applyResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-mail", map[string]any{
		"dry_run":  false,
		"user_ids": []string{clawWorldSystemID, "clawcolony-assistant", regular.id},
		"classes":  []string{"system_stale_72h"},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if applyResp.Code != http.StatusAccepted {
		t.Fatalf("apply report inbox cleanup status=%d body=%s", applyResp.Code, applyResp.Body.String())
	}
	applyBody := parseJSONBody(t, applyResp)
	applyResult := applyBody["result"].(map[string]any)
	if got := int(applyResult["resolved_mailbox_count"].(float64)); got != 2 {
		t.Fatalf("expected only admin/assistant report inboxes to auto-read, got=%d body=%s", got, applyResp.Body.String())
	}

	if got := len(unreadMailSubjects(t, srv, clawWorldSystemID, "admin-report")); got != 0 {
		t.Fatalf("expected stale admin report to auto-read, got=%d", got)
	}
	if got := len(unreadMailSubjects(t, srv, "clawcolony-assistant", "governance-report")); got != 0 {
		t.Fatalf("expected stale assistant governance report to auto-read, got=%d", got)
	}
	if got := len(unreadMailSubjects(t, srv, regular.id, "peer-report")); got != 1 {
		t.Fatalf("expected regular recipient report mail to remain unread, got=%d", got)
	}
}

func TestMailSystemResolveObsoleteMailDryRunApplyAndIdempotent(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	recipient := newAuthUser(t, srv)
	ctx := context.Background()
	old := time.Now().UTC().Add(-73 * time.Hour)

	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    "sender-a",
		To:      []string{recipient.id},
		Subject: "duplicate report",
		Body:    "same body",
	}, time.Time{})
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    "sender-a",
		To:      []string{recipient.id},
		Subject: "duplicate report",
		Body:    "same body",
	}, time.Now().UTC().Add(1*time.Second))
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[COLLAB][DEADLINE-REMINDER] Dock Upgrade - 3 days remaining",
		Body:    "collab_id=collab-7\nproposal_id=77",
	}, time.Time{})
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[COLLAB][DEADLINE-REMINDER] Dock Upgrade - 1 day remaining",
		Body:    "collab_id=collab-7\nproposal_id=77",
	}, time.Now().UTC().Add(2*time.Second))
	seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[WORLD-EVOLUTION-ALERT] level=warning overall=55 top=knowledge:40",
		Body:    "stale-world-evolution",
	}, old)

	unreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread before cleanup: %v", err)
	}
	if len(unreadBefore) != 5 {
		t.Fatalf("expected five unread mails before cleanup, got=%d", len(unreadBefore))
	}

	dryRunResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-mail", map[string]any{
		"dry_run":  true,
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if dryRunResp.Code != http.StatusOK {
		t.Fatalf("dry run cleanup status=%d body=%s", dryRunResp.Code, dryRunResp.Body.String())
	}
	dryRunBody := parseJSONBody(t, dryRunResp)
	dryRunResult := dryRunBody["result"].(map[string]any)
	if got := int(dryRunResult["affected_user_count"].(float64)); got != 1 {
		t.Fatalf("expected dry-run affected_user_count=1 got=%d body=%s", got, dryRunResp.Body.String())
	}
	if got := int(dryRunResult["resolved_mailbox_count"].(float64)); got != 3 {
		t.Fatalf("expected dry-run resolved_mailbox_count=3 got=%d body=%s", got, dryRunResp.Body.String())
	}

	applyResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-mail", map[string]any{
		"dry_run":  false,
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if applyResp.Code != http.StatusAccepted {
		t.Fatalf("apply cleanup status=%d body=%s", applyResp.Code, applyResp.Body.String())
	}
	applyBody := parseJSONBody(t, applyResp)
	applyResult := applyBody["result"].(map[string]any)
	if got := int(applyResult["resolved_mailbox_count"].(float64)); got != 3 {
		t.Fatalf("expected apply resolved_mailbox_count=3 got=%d body=%s", got, applyResp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread after cleanup: %v", err)
	}
	if len(unreadAfter) != 2 {
		t.Fatalf("expected cleanup to keep newest duplicate + newest family item unread, got=%d", len(unreadAfter))
	}

	idempotentResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-mail", map[string]any{
		"dry_run":  false,
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if idempotentResp.Code != http.StatusAccepted {
		t.Fatalf("idempotent cleanup status=%d body=%s", idempotentResp.Code, idempotentResp.Body.String())
	}
	idempotentBody := parseJSONBody(t, idempotentResp)
	idempotentResult := idempotentBody["result"].(map[string]any)
	if got := int(idempotentResult["resolved_mailbox_count"].(float64)); got != 0 {
		t.Fatalf("expected idempotent cleanup to resolve zero additional mails, got=%d body=%s", got, idempotentResp.Body.String())
	}
}

func TestMailNoiseCleanupTickAdvancesCursorAndCleansBatches(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	originalLimit := mailNoiseCleanupUserBatchLimit
	mailNoiseCleanupUserBatchLimit = 1
	defer func() { mailNoiseCleanupUserBatchLimit = originalLimit }()

	for _, userID := range []string{"user-mail-noise-a", "user-mail-noise-b"} {
		if _, err := srv.store.CreateAgentRegistration(ctx, store.AgentRegistrationInput{
			UserID:            userID,
			RequestedUsername: userID,
			GoodAt:            "cleanup",
			Status:            "active",
			APIKeyHash:        hashSecret("key-" + userID),
		}); err != nil {
			t.Fatalf("create registration %s: %v", userID, err)
		}
		seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
			From:    "sender",
			To:      []string{userID},
			Subject: "tick-duplicate",
			Body:    "tick-body",
		}, time.Time{})
		seedMailForNoisePolicyTest(t, srv, store.MailSendInput{
			From:    "sender",
			To:      []string{userID},
			Subject: "tick-duplicate",
			Body:    "tick-body",
		}, time.Now().UTC().Add(1*time.Second))
	}

	if err := srv.runMailNoiseCleanupTick(ctx, 11); err != nil {
		t.Fatalf("run cleanup tick 1: %v", err)
	}
	var state mailNoiseCleanupState
	found, _, err := srv.getSettingJSON(ctx, mailNoiseCleanupStateKey, &state)
	if err != nil {
		t.Fatalf("get cleanup state after tick 1: %v", err)
	}
	if !found {
		t.Fatalf("expected cleanup state to be persisted after tick 1")
	}
	if state.StartAfterUserID != "user-mail-noise-a" {
		t.Fatalf("expected cursor after first tick to advance to first user, got=%q", state.StartAfterUserID)
	}
	if got := len(unreadMailSubjects(t, srv, "user-mail-noise-a", "tick-duplicate")); got != 1 {
		t.Fatalf("expected first user duplicates cleaned in tick 1, got unread=%d", got)
	}
	if got := len(unreadMailSubjects(t, srv, "user-mail-noise-b", "tick-duplicate")); got != 2 {
		t.Fatalf("expected second user untouched in tick 1, got unread=%d", got)
	}

	if err := srv.runMailNoiseCleanupTick(ctx, 12); err != nil {
		t.Fatalf("run cleanup tick 2: %v", err)
	}
	found, _, err = srv.getSettingJSON(ctx, mailNoiseCleanupStateKey, &state)
	if err != nil {
		t.Fatalf("get cleanup state after tick 2: %v", err)
	}
	if !found {
		t.Fatalf("expected cleanup state to remain persisted after tick 2")
	}
	if state.StartAfterUserID != "" {
		t.Fatalf("expected cursor reset after exhausting users, got=%q", state.StartAfterUserID)
	}
	if got := len(unreadMailSubjects(t, srv, "user-mail-noise-b", "tick-duplicate")); got != 1 {
		t.Fatalf("expected second user duplicates cleaned in tick 2, got unread=%d", got)
	}
}
