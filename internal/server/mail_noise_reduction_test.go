package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func TestKBPendingSummaryLimitsRecipientMailButPreservesBacklog(t *testing.T) {
	srv := newTestServer()
	proposerA := newAuthUser(t, srv)
	proposerB := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	createPayload := func(title string) map[string]any {
		return map[string]any{
			"title":                     title,
			"reason":                    "reduce repeated system mail by batching related work",
			"vote_threshold_pct":        50,
			"vote_window_seconds":       300,
			"discussion_window_seconds": 300,
			"change": map[string]any{
				"op_type":     "add",
				"section":     "runtime-mail",
				"title":       title,
				"new_content": "concrete knowledge content for pending summary delivery tests",
				"diff_text":   "add pending summary coverage for noisy KB reminder flows",
			},
		}
	}

	first := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", createPayload("batch-one"), proposerA.headers())
	if first.Code != http.StatusAccepted {
		t.Fatalf("create first proposal status=%d body=%s", first.Code, first.Body.String())
	}
	second := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", createPayload("batch-two"), proposerB.headers())
	if second.Code != http.StatusAccepted {
		t.Fatalf("create second proposal status=%d body=%s", second.Code, second.Body.String())
	}

	inbox, err := srv.store.ListMailbox(context.Background(), recipient.id, "inbox", "", "知识库待处理提案", nil, nil, 20)
	if err != nil {
		t.Fatalf("list recipient inbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("expected one KB pending summary mail within window, got=%d", len(inbox))
	}

	remindersResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/reminders?limit=20", nil, recipient.headers())
	if remindersResp.Code != http.StatusOK {
		t.Fatalf("mail reminders status=%d body=%s", remindersResp.Code, remindersResp.Body.String())
	}
	body := parseJSONBody(t, remindersResp)
	backlog, ok := body["unread_backlog"].(map[string]any)
	if !ok {
		t.Fatalf("missing unread_backlog: %s", remindersResp.Body.String())
	}
	if got := int(backlog["knowledgebase_enroll"].(float64)); got != 2 {
		t.Fatalf("expected KB enroll backlog to stay visible as 2, got=%d body=%s", got, remindersResp.Body.String())
	}
}

func TestKBUpdatedSummaryTargetsParticipantsInsteadOfAllActiveUsers(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	participant := newAuthUser(t, srv)
	unrelated := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "apply-targeting",
		"reason":                    "verify KB updated mail narrows recipients",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "apply-targeting",
			"new_content": "new entry content for narrowed KB updated recipients",
			"diff_text":   "add a proposal that will be applied in tests",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := parseJSONBody(t, createResp)
	proposal := createBody["proposal"].(map[string]any)
	proposalID := int64(proposal["id"].(float64))

	enrollResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/enroll", map[string]any{
		"proposal_id": proposalID,
	}, participant.headers())
	if enrollResp.Code != http.StatusAccepted {
		t.Fatalf("enroll participant status=%d body=%s", enrollResp.Code, enrollResp.Body.String())
	}

	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "approved", "approved in targeted summary test", 1, 1, 0, 0, 1, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal approved: %v", err)
	}
	if _, _, err := srv.applyKBProposalAndBroadcast(ctx, proposalID, proposer.id); err != nil {
		t.Fatalf("apply proposal: %v", err)
	}

	proposerInbox, err := srv.store.ListMailbox(ctx, proposer.id, "inbox", "", "[KNOWLEDGEBASE Updated]", nil, nil, 10)
	if err != nil {
		t.Fatalf("list proposer inbox: %v", err)
	}
	if len(proposerInbox) != 1 {
		t.Fatalf("expected proposer to receive one KB updated summary, got=%d", len(proposerInbox))
	}

	participantInbox, err := srv.store.ListMailbox(ctx, participant.id, "inbox", "", "[KNOWLEDGEBASE Updated]", nil, nil, 10)
	if err != nil {
		t.Fatalf("list participant inbox: %v", err)
	}
	if len(participantInbox) != 1 {
		t.Fatalf("expected participant to receive one KB updated summary, got=%d", len(participantInbox))
	}

	unrelatedInbox, err := srv.store.ListMailbox(ctx, unrelated.id, "inbox", "", "[KNOWLEDGEBASE Updated]", nil, nil, 10)
	if err != nil {
		t.Fatalf("list unrelated inbox: %v", err)
	}
	if len(unrelatedInbox) != 0 {
		t.Fatalf("expected unrelated active user to receive no KB updated summary, got=%d", len(unrelatedInbox))
	}
}

func TestLowTokenAlertResetsAfterRecoveryAboveThreshold(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	userID := seedActiveUser(t, srv)
	balance := int64(1000)
	threshold := srv.cfg.InitialToken / 5
	if threshold <= 0 {
		threshold = 1
	}
	if balance >= threshold {
		consumeAmount := balance - threshold + 1
		if _, err := srv.store.Consume(ctx, userID, consumeAmount); err != nil {
			t.Fatalf("consume below threshold: %v", err)
		}
		balance -= consumeAmount
	}
	if err := srv.runLowEnergyAlertTick(ctx, 1); err != nil {
		t.Fatalf("low energy tick1: %v", err)
	}
	rechargeAmount := threshold - balance + 1000
	if _, err := srv.store.Recharge(ctx, userID, rechargeAmount); err != nil {
		t.Fatalf("recharge above threshold: %v", err)
	}
	balance += rechargeAmount
	if err := srv.runLowEnergyAlertTick(ctx, 2); err != nil {
		t.Fatalf("low energy tick2: %v", err)
	}
	consumeAgain := balance - threshold + 1
	if _, err := srv.store.Consume(ctx, userID, consumeAgain); err != nil {
		t.Fatalf("consume below threshold again: %v", err)
	}
	balance -= consumeAgain
	if err := srv.runLowEnergyAlertTick(ctx, 3); err != nil {
		t.Fatalf("low energy tick3: %v", err)
	}

	inbox, err := srv.store.ListMailbox(ctx, userID, "inbox", "", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list low-token inbox: %v", err)
	}
	if len(inbox) != 2 {
		t.Fatalf("expected alert to send again after recovery reset, got=%d", len(inbox))
	}
}

func TestMailInboxAutoMarksRecoveredLowTokenRead(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	user := newAuthUser(t, srv)
	balance := int64(1000)
	threshold := srv.cfg.InitialToken / 5
	if threshold <= 0 {
		threshold = 1
	}
	consumeAmount := balance - threshold + 1
	if _, err := srv.store.Consume(ctx, user.id, consumeAmount); err != nil {
		t.Fatalf("consume below threshold: %v", err)
	}
	balance -= consumeAmount
	if err := srv.runLowEnergyAlertTick(ctx, 1); err != nil {
		t.Fatalf("low energy tick1: %v", err)
	}

	unreadBefore, err := srv.store.ListMailbox(ctx, user.id, "inbox", "unread", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread low-token inbox before recovery: %v", err)
	}
	if len(unreadBefore) != 1 {
		t.Fatalf("expected one unread low-token mail before recovery, got=%d", len(unreadBefore))
	}
	if _, ok, err := srv.store.GetNotificationDeliveryState(ctx, user.id, notificationCategoryLowTokenAlert); err != nil {
		t.Fatalf("get low-token notification state before recovery: %v", err)
	} else if !ok {
		t.Fatalf("expected low-token notification state to exist before recovery")
	}

	rechargeAmount := threshold - balance + 1000
	if _, err := srv.store.Recharge(ctx, user.id, rechargeAmount); err != nil {
		t.Fatalf("recharge above threshold: %v", err)
	}

	inboxResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?scope=unread&limit=20", nil, user.headers())
	if inboxResp.Code != http.StatusOK {
		t.Fatalf("mail inbox status=%d body=%s", inboxResp.Code, inboxResp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, user.id, "inbox", "unread", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread low-token inbox after recovery: %v", err)
	}
	if len(unreadAfter) != 0 {
		t.Fatalf("expected recovered low-token mail to auto-read, got unread=%d", len(unreadAfter))
	}

	readAfter, err := srv.store.ListMailbox(ctx, user.id, "inbox", "read", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list read low-token inbox after recovery: %v", err)
	}
	if len(readAfter) != 1 || !readAfter[0].IsRead {
		t.Fatalf("expected recovered low-token mail to be marked read, got=%d", len(readAfter))
	}
	if _, ok, err := srv.store.GetNotificationDeliveryState(ctx, user.id, notificationCategoryLowTokenAlert); err != nil {
		t.Fatalf("get low-token notification state after recovery: %v", err)
	} else if ok {
		t.Fatalf("expected low-token notification state to be cleared after recovery auto-read")
	}
}

func TestMailInboxAutoMarksClosedKBEnrollmentSummaryRead(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "closed-enroll-summary",
		"reason":                    "verify stale KB enrollment mail is auto-read once the window closes",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "closed-enroll-summary",
			"new_content": "stale enrollment summary test",
			"diff_text":   "auto-read stale KB enrollment summary",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := parseJSONBody(t, createResp)
	proposal := createBody["proposal"].(map[string]any)
	proposalID := int64(proposal["id"].(float64))

	unreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB inbox before close: %v", err)
	}
	if len(unreadBefore) != 1 {
		t.Fatalf("expected one unread KB enrollment summary before close, got=%d", len(unreadBefore))
	}

	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "rejected", "closed in test", 0, 0, 0, 0, 0, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal rejected: %v", err)
	}

	inboxResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?scope=unread&limit=20", nil, recipient.headers())
	if inboxResp.Code != http.StatusOK {
		t.Fatalf("mail inbox status=%d body=%s", inboxResp.Code, inboxResp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB inbox after close: %v", err)
	}
	if len(unreadAfter) != 0 {
		t.Fatalf("expected stale KB enrollment summary to auto-read after close, got unread=%d", len(unreadAfter))
	}

	readAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "read", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list read KB inbox after close: %v", err)
	}
	if len(readAfter) != 1 || !readAfter[0].IsRead {
		t.Fatalf("expected stale KB enrollment summary to be marked read, got=%d", len(readAfter))
	}
}

func TestMailInboxAutoMarksClosedLegacyKBEnrollMailWithoutRevisionRead(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "closed-legacy-enroll-no-revision",
		"reason":                    "verify stale legacy KB enroll mail without revision fields is auto-read once the proposal closes",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "closed-legacy-enroll-no-revision",
			"new_content": "stale legacy enroll reminder without revision fields",
			"diff_text":   "auto-read stale legacy KB enroll mail without revision fields",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := parseJSONBody(t, createResp)
	proposal := createBody["proposal"].(map[string]any)
	proposalID := int64(proposal["id"].(float64))

	_, err := srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P2][ACTION:ENROLL] #" + strconv.FormatInt(proposalID, 10) + " legacy stale without revision",
		Body:    "proposal_id=" + strconv.FormatInt(proposalID, 10) + "\nreason=legacy enroll mail without revision fields",
	})
	if err != nil {
		t.Fatalf("seed legacy KB enroll reminder without revision: %v", err)
	}

	unreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "legacy stale without revision", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread legacy KB enroll before close: %v", err)
	}
	if len(unreadBefore) != 1 {
		t.Fatalf("expected one unread legacy KB enroll mail before close, got=%d", len(unreadBefore))
	}

	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "rejected", "closed in legacy enroll test", 0, 0, 0, 0, 0, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal rejected: %v", err)
	}

	inboxResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?scope=unread&limit=20", nil, recipient.headers())
	if inboxResp.Code != http.StatusOK {
		t.Fatalf("mail inbox status=%d body=%s", inboxResp.Code, inboxResp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "legacy stale without revision", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread legacy KB enroll after close: %v", err)
	}
	if len(unreadAfter) != 0 {
		t.Fatalf("expected stale legacy KB enroll mail without revision to auto-read after close, got unread=%d", len(unreadAfter))
	}
}

func TestMailRemindersAutoMarksClosedKBVoteReminderRead(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	voter := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "closed-vote-reminder",
		"reason":                    "verify stale KB voting reminder is auto-read once the proposal closes",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "closed-vote-reminder",
			"new_content": "stale vote reminder test",
			"diff_text":   "auto-read stale KB vote reminder",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := parseJSONBody(t, createResp)
	proposal := createBody["proposal"].(map[string]any)
	proposalID := int64(proposal["id"].(float64))

	enrollResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/enroll", map[string]any{
		"proposal_id": proposalID,
	}, voter.headers())
	if enrollResp.Code != http.StatusAccepted {
		t.Fatalf("enroll voter status=%d body=%s", enrollResp.Code, enrollResp.Body.String())
	}
	state, ok, err := srv.store.GetNotificationDeliveryState(ctx, voter.id, notificationCategoryKBPendingSummary)
	if err != nil {
		t.Fatalf("get KB pending summary state: %v", err)
	}
	if !ok {
		t.Fatalf("expected KB pending summary state to exist after enrollment")
	}
	backdated := state
	backdated.LastSentAt = time.Now().UTC().Add(-kbPendingSummaryMinInterval - time.Minute)
	backdated.LastRemindedAt = backdated.LastSentAt
	if _, err := srv.store.UpsertNotificationDeliveryState(ctx, backdated); err != nil {
		t.Fatalf("backdate KB pending summary state: %v", err)
	}
	startResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/start-vote", map[string]any{
		"proposal_id": proposalID,
	}, proposer.headers())
	if startResp.Code != http.StatusAccepted {
		t.Fatalf("start vote status=%d body=%s", startResp.Code, startResp.Body.String())
	}

	unreadPinnedBefore, err := srv.store.ListMailbox(ctx, voter.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL][PINNED]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread pinned KB reminders before close: %v", err)
	}
	if len(unreadPinnedBefore) != 1 {
		t.Fatalf("expected one unread KB vote reminder before close, got=%d", len(unreadPinnedBefore))
	}

	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "rejected", "closed in test", 1, 0, 0, 0, 0, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal rejected: %v", err)
	}

	remindersResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/reminders?limit=20", nil, voter.headers())
	if remindersResp.Code != http.StatusOK {
		t.Fatalf("mail reminders status=%d body=%s", remindersResp.Code, remindersResp.Body.String())
	}
	body := parseJSONBody(t, remindersResp)
	if got := int(body["count"].(float64)); got != 0 {
		t.Fatalf("expected stale KB vote reminder to disappear from reminders, got count=%d body=%s", got, remindersResp.Body.String())
	}

	unreadPinnedAfter, err := srv.store.ListMailbox(ctx, voter.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL][PINNED]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread pinned KB reminders after close: %v", err)
	}
	if len(unreadPinnedAfter) != 0 {
		t.Fatalf("expected stale KB vote reminder to auto-read after close, got unread=%d", len(unreadPinnedAfter))
	}
}

func TestMailRemindersAutoMarksClosedLegacyKBVoteReminderRead(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	voter := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "closed-legacy-vote-reminder",
		"reason":                    "verify stale legacy KB vote reminder is auto-read once the proposal closes",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "closed-legacy-vote-reminder",
			"new_content": "stale legacy vote reminder test",
			"diff_text":   "auto-read stale legacy KB vote reminder",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := parseJSONBody(t, createResp)
	proposal := createBody["proposal"].(map[string]any)
	proposalID := int64(proposal["id"].(float64))

	enrollResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/enroll", map[string]any{
		"proposal_id": proposalID,
	}, voter.headers())
	if enrollResp.Code != http.StatusAccepted {
		t.Fatalf("enroll voter status=%d body=%s", enrollResp.Code, enrollResp.Body.String())
	}

	deadline := time.Now().UTC().Add(5 * time.Minute)
	votingProposal, err := srv.store.StartKBProposalVoting(ctx, proposalID, deadline)
	if err != nil {
		t.Fatalf("start proposal voting in store: %v", err)
	}

	_, err = srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{voter.id},
		Subject: "[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #" + strconv.FormatInt(proposalID, 10) + " legacy stale",
		Body:    "proposal_id=" + strconv.FormatInt(proposalID, 10) + "\nrevision_id=" + strconv.FormatInt(votingProposal.VotingRevisionID, 10) + "\ndeadline=" + deadline.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("seed legacy KB vote reminder: %v", err)
	}

	unreadPinnedBefore, err := srv.store.ListMailbox(ctx, voter.id, "inbox", "unread", "legacy stale", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread legacy KB reminders before close: %v", err)
	}
	if len(unreadPinnedBefore) != 1 {
		t.Fatalf("expected one unread legacy KB vote reminder before close, got=%d", len(unreadPinnedBefore))
	}

	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "rejected", "closed in legacy reminder test", 1, 0, 0, 0, 0, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal rejected: %v", err)
	}

	remindersResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/reminders?limit=20", nil, voter.headers())
	if remindersResp.Code != http.StatusOK {
		t.Fatalf("mail reminders status=%d body=%s", remindersResp.Code, remindersResp.Body.String())
	}

	unreadPinnedAfter, err := srv.store.ListMailbox(ctx, voter.id, "inbox", "unread", "legacy stale", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread legacy KB reminders after close: %v", err)
	}
	if len(unreadPinnedAfter) != 0 {
		t.Fatalf("expected stale legacy KB vote reminder to auto-read after close, got unread=%d", len(unreadPinnedAfter))
	}
}

func TestMailSystemResolveObsoleteKBDryRunDoesNotMutate(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "obsolete-kb-dry-run",
		"reason":                    "verify obsolete KB cleanup dry-run does not mutate unread mail",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "obsolete-kb-dry-run",
			"new_content": "dry run cleanup test",
			"diff_text":   "dry run obsolete KB cleanup should not mutate unread mail",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := parseJSONBody(t, createResp)
	proposal := createBody["proposal"].(map[string]any)
	proposalID := int64(proposal["id"].(float64))

	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "rejected", "closed for dry-run cleanup", 0, 0, 0, 0, 0, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal rejected: %v", err)
	}

	unreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB inbox before dry-run: %v", err)
	}
	if len(unreadBefore) != 1 {
		t.Fatalf("expected one unread KB mail before dry-run, got=%d", len(unreadBefore))
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  true,
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("obsolete KB dry-run status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got != 1 {
		t.Fatalf("expected dry-run affected_user_count=1 got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got != 1 {
		t.Fatalf("expected dry-run resolved_mailbox_count=1 got=%d body=%s", got, resp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB inbox after dry-run: %v", err)
	}
	if len(unreadAfter) != 1 {
		t.Fatalf("expected dry-run to leave unread KB mail untouched, got=%d", len(unreadAfter))
	}
}

func TestMailSystemResolveObsoleteKBDryRunSupportsLowTokenClass(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	user := newAuthUser(t, srv)
	balance := int64(1000)
	threshold := srv.cfg.InitialToken / 5
	if threshold <= 0 {
		threshold = 1
	}
	consumeAmount := balance - threshold + 1
	if _, err := srv.store.Consume(ctx, user.id, consumeAmount); err != nil {
		t.Fatalf("consume below threshold: %v", err)
	}
	balance -= consumeAmount
	if err := srv.runLowEnergyAlertTick(ctx, 1); err != nil {
		t.Fatalf("low energy tick1: %v", err)
	}
	if _, ok, err := srv.store.GetNotificationDeliveryState(ctx, user.id, notificationCategoryLowTokenAlert); err != nil {
		t.Fatalf("get low-token state before dry-run: %v", err)
	} else if !ok {
		t.Fatalf("expected low-token state before dry-run")
	}
	rechargeAmount := threshold - balance + 1000
	if _, err := srv.store.Recharge(ctx, user.id, rechargeAmount); err != nil {
		t.Fatalf("recharge above threshold: %v", err)
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  true,
		"classes":  []string{obsoleteMailClassLowToken},
		"user_ids": []string{user.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("obsolete low-token dry-run status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got != 1 {
		t.Fatalf("expected low-token dry-run affected_user_count=1 got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got != 1 {
		t.Fatalf("expected low-token dry-run resolved_mailbox_count=1 got=%d body=%s", got, resp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, user.id, "inbox", "unread", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread low-token inbox after dry-run: %v", err)
	}
	if len(unreadAfter) != 1 {
		t.Fatalf("expected dry-run to leave unread low-token mail untouched, got=%d", len(unreadAfter))
	}
	if _, ok, err := srv.store.GetNotificationDeliveryState(ctx, user.id, notificationCategoryLowTokenAlert); err != nil {
		t.Fatalf("get low-token state after dry-run: %v", err)
	} else if !ok {
		t.Fatalf("expected low-token state to remain after dry-run")
	}
}

func TestMailSystemResolveObsoleteKBOnlyRequestedClasses(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "obsolete-class-filter",
		"reason":                    "verify obsolete cleanup only resolves explicitly requested classes",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "obsolete-class-filter",
			"new_content": "class filtering cleanup test",
			"diff_text":   "only low-token cleanup should not touch stale KB mail",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := parseJSONBody(t, createResp)
	proposal := createBody["proposal"].(map[string]any)
	proposalID := int64(proposal["id"].(float64))
	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "rejected", "closed for class-filter cleanup", 0, 0, 0, 0, 0, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal rejected: %v", err)
	}

	balance := int64(1000)
	threshold := srv.cfg.InitialToken / 5
	if threshold <= 0 {
		threshold = 1
	}
	consumeAmount := balance - threshold + 1
	if _, err := srv.store.Consume(ctx, recipient.id, consumeAmount); err != nil {
		t.Fatalf("consume below threshold: %v", err)
	}
	balance -= consumeAmount
	if err := srv.runLowEnergyAlertTick(ctx, 1); err != nil {
		t.Fatalf("low energy tick1: %v", err)
	}
	rechargeAmount := threshold - balance + 1000
	if _, err := srv.store.Recharge(ctx, recipient.id, rechargeAmount); err != nil {
		t.Fatalf("recharge above threshold: %v", err)
	}

	kbUnreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB inbox before class-filter cleanup: %v", err)
	}
	if len(kbUnreadBefore) != 1 {
		t.Fatalf("expected one unread KB mail before class-filter cleanup, got=%d", len(kbUnreadBefore))
	}
	lowTokenUnreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread low-token inbox before class-filter cleanup: %v", err)
	}
	if len(lowTokenUnreadBefore) != 1 {
		t.Fatalf("expected one unread low-token mail before class-filter cleanup, got=%d", len(lowTokenUnreadBefore))
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  false,
		"classes":  []string{obsoleteMailClassLowToken},
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("obsolete low-token cleanup status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got != 1 {
		t.Fatalf("expected class-filter cleanup affected_user_count=1 got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got != 1 {
		t.Fatalf("expected class-filter cleanup resolved_mailbox_count=1 got=%d body=%s", got, resp.Body.String())
	}

	kbUnreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB inbox after class-filter cleanup: %v", err)
	}
	if len(kbUnreadAfter) != 1 {
		t.Fatalf("expected low-token-only cleanup to leave KB unread untouched, got=%d", len(kbUnreadAfter))
	}
	lowTokenUnreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread low-token inbox after class-filter cleanup: %v", err)
	}
	if len(lowTokenUnreadAfter) != 0 {
		t.Fatalf("expected low-token-only cleanup to resolve stale low-token unread, got=%d", len(lowTokenUnreadAfter))
	}
	if _, ok, err := srv.store.GetNotificationDeliveryState(ctx, recipient.id, notificationCategoryLowTokenAlert); err != nil {
		t.Fatalf("get low-token state after class-filter cleanup: %v", err)
	} else if ok {
		t.Fatalf("expected low-token notification state to be cleared after class-filter cleanup")
	}
}

func TestMailSystemResolveObsoleteKBLowTokenKeepsLatestUnreadWhenStillBelowThreshold(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	user := newAuthUser(t, srv)
	threshold := srv.cfg.InitialToken / 5
	if threshold <= 0 {
		threshold = 1
	}
	if _, err := srv.store.Consume(ctx, user.id, 1000-threshold+1); err != nil {
		t.Fatalf("consume below threshold: %v", err)
	}

	subjects := []string{
		"[LOW-TOKEN] stale-one",
		"[LOW-TOKEN] stale-two",
		"[LOW-TOKEN] stale-three",
	}
	for _, subject := range subjects {
		if _, err := srv.store.SendMail(ctx, store.MailSendInput{
			From:    clawWorldSystemID,
			To:      []string{user.id},
			Subject: subject,
			Body:    "low token cleanup keeps only latest unread when balance remains below threshold",
		}); err != nil {
			t.Fatalf("seed low-token mail %q: %v", subject, err)
		}
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  false,
		"classes":  []string{obsoleteMailClassLowToken},
		"user_ids": []string{user.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("obsolete low-token cleanup status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["resolved_mailbox_count"].(float64)); got != 2 {
		t.Fatalf("expected cleanup to resolve two older low-token mails, got=%d body=%s", got, resp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, user.id, "inbox", "unread", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread low-token inbox after cleanup: %v", err)
	}
	if len(unreadAfter) != 1 {
		t.Fatalf("expected cleanup to keep exactly one latest low-token unread, got=%d", len(unreadAfter))
	}
	if unreadAfter[0].Subject != "[LOW-TOKEN] stale-three" {
		t.Fatalf("expected newest low-token mail to remain unread, got subject=%q", unreadAfter[0].Subject)
	}

	readAfter, err := srv.store.ListMailbox(ctx, user.id, "inbox", "read", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list read low-token inbox after cleanup: %v", err)
	}
	if len(readAfter) != 2 {
		t.Fatalf("expected two older low-token mails to become read, got=%d", len(readAfter))
	}
}

func TestMailSystemResolveObsoleteKBScansRegisteredOwnersWithoutBots(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	ownerID := "user-test-obsolete-registration-only"

	if _, err := srv.store.CreateAgentRegistration(ctx, store.AgentRegistrationInput{
		UserID:            ownerID,
		RequestedUsername: ownerID,
		GoodAt:            "cleanup",
		Status:            "active",
		APIKeyHash:        hashSecret("unused-key"),
	}); err != nil {
		t.Fatalf("create registration-only owner: %v", err)
	}

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "obsolete-kb-registration-owner",
		"reason":                    "verify obsolete KB cleanup scans registration owners even without bots",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "obsolete-kb-registration-owner",
			"new_content": "registration owner cleanup test",
			"diff_text":   "obsolete KB cleanup should scan registration owners without active bots",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	createBody := parseJSONBody(t, createResp)
	proposal := createBody["proposal"].(map[string]any)
	proposalID := int64(proposal["id"].(float64))

	deadline := time.Now().UTC().Add(5 * time.Minute)
	votingProposal, err := srv.store.StartKBProposalVoting(ctx, proposalID, deadline)
	if err != nil {
		t.Fatalf("start proposal voting in store: %v", err)
	}
	_, err = srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{ownerID},
		Subject: "[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #" + strconv.FormatInt(proposalID, 10) + " registration-only stale",
		Body:    "proposal_id=" + strconv.FormatInt(proposalID, 10) + "\nrevision_id=" + strconv.FormatInt(votingProposal.VotingRevisionID, 10) + "\ndeadline=" + deadline.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("seed registration-only legacy KB vote reminder: %v", err)
	}
	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "rejected", "closed for registration owner cleanup", 0, 0, 0, 0, 0, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal rejected: %v", err)
	}

	unreadBefore, err := srv.store.ListMailbox(ctx, ownerID, "inbox", "unread", "registration-only stale", nil, nil, 20)
	if err != nil {
		t.Fatalf("list registration-only unread KB mail before cleanup: %v", err)
	}
	if len(unreadBefore) != 1 {
		t.Fatalf("expected one unread registration-only KB mail before cleanup, got=%d", len(unreadBefore))
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run": false,
		"limit":   200,
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("obsolete KB cleanup status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got < 1 {
		t.Fatalf("expected at least one affected user in cleanup result, got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got < 1 {
		t.Fatalf("expected at least one resolved mailbox in cleanup result, got=%d body=%s", got, resp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, ownerID, "inbox", "unread", "registration-only stale", nil, nil, 20)
	if err != nil {
		t.Fatalf("list registration-only unread KB mail after cleanup: %v", err)
	}
	if len(unreadAfter) != 0 {
		t.Fatalf("expected registration-only obsolete KB mail to be marked read, got unread=%d", len(unreadAfter))
	}
}
