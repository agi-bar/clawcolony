package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func createKBProposalForMailNoiseTest(t *testing.T, srv *Server, proposer authUser, title, reason string) int64 {
	t.Helper()
	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     title,
		"reason":                    reason,
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       title,
			"new_content": "mail noise reduction KB proposal content",
			"diff_text":   "mail noise reduction KB proposal diff",
		},
	}, proposer.headers())
	if resp.Code != http.StatusAccepted {
		t.Fatalf("create proposal status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	proposal := body["proposal"].(map[string]any)
	return int64(proposal["id"].(float64))
}

func applyKBProposalForMailNoiseTest(t *testing.T, srv *Server, proposer authUser, title, reason string) int64 {
	t.Helper()
	ctx := context.Background()
	proposalID := createKBProposalForMailNoiseTest(t, srv, proposer, title, reason)
	if _, err := srv.store.CloseKBProposal(ctx, proposalID, "approved", "approved in KB updated summary test", 1, 1, 0, 0, 1, time.Now().UTC()); err != nil {
		t.Fatalf("close proposal approved: %v", err)
	}
	if _, _, err := srv.applyKBProposalAndBroadcast(ctx, proposalID, proposer.id); err != nil {
		t.Fatalf("apply proposal: %v", err)
	}
	return proposalID
}

func seedLegacyKBUpdatedMailForMailNoiseTest(t *testing.T, srv *Server, userID string, proposalID int64, title string, appliedAt time.Time) {
	t.Helper()
	subject := fmt.Sprintf("[KNOWLEDGEBASE Updated] 1 项%s", refTag(skillKnowledgeBase))
	body := strings.TrimSpace(fmt.Sprintf(`最近一段时间内有新的 knowledgebase 更新。
updated_count=1

1. proposal_id=%d
   title=%s
   applied_at=%s`, proposalID, title, appliedAt.UTC().Format(time.RFC3339)))
	if _, err := srv.store.SendMail(context.Background(), store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{userID},
		Subject: subject,
		Body:    body,
	}); err != nil {
		t.Fatalf("seed legacy KB updated mail: %v", err)
	}
}

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
	if !strings.Contains(inbox[0].Body, kbPendingSummaryStreamMarker) {
		t.Fatalf("expected KB pending summary to include managed stream marker, body=%s", inbox[0].Body)
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

func TestKBPendingSummaryUpdatesInPlaceWhileUnread(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposerA := newAuthUser(t, srv)
	proposerB := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	firstProposalID := createKBProposalForMailNoiseTest(t, srv, proposerA, "pending-first", "first pending item")
	firstUnread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "知识库待处理提案", nil, nil, 10)
	if err != nil {
		t.Fatalf("list first KB pending summary: %v", err)
	}
	if len(firstUnread) != 1 {
		t.Fatalf("expected one unread KB pending summary after first proposal, got=%d", len(firstUnread))
	}
	firstMessageID := firstUnread[0].MessageID
	firstMailboxID := firstUnread[0].MailboxID

	secondProposalID := createKBProposalForMailNoiseTest(t, srv, proposerB, "pending-second", "second pending item")
	secondUnread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "知识库待处理提案", nil, nil, 10)
	if err != nil {
		t.Fatalf("list second KB pending summary: %v", err)
	}
	if len(secondUnread) != 1 {
		t.Fatalf("expected one unread KB pending summary after second proposal, got=%d", len(secondUnread))
	}
	if secondUnread[0].MessageID != firstMessageID {
		t.Fatalf("expected KB pending summary to update in place, first_message_id=%d second_message_id=%d", firstMessageID, secondUnread[0].MessageID)
	}
	if secondUnread[0].MailboxID != firstMailboxID {
		t.Fatalf("expected KB pending summary to keep same mailbox id, first=%d second=%d", firstMailboxID, secondUnread[0].MailboxID)
	}
	if !strings.Contains(secondUnread[0].Subject, "[UPDATED]") {
		t.Fatalf("expected in-place updated KB pending summary subject to carry updated marker, subject=%s", secondUnread[0].Subject)
	}
	for _, want := range []string{
		kbPendingSummaryStreamMarker,
		fmt.Sprintf("proposal_id=%d", firstProposalID),
		fmt.Sprintf("proposal_id=%d", secondProposalID),
		"pending_total=2",
		"enroll_count=2",
		"https://clawcolony.agi.bar/api/v1/kb/proposals/enroll",
	} {
		if !strings.Contains(secondUnread[0].Body, want) {
			t.Fatalf("expected updated KB pending body to contain %q, body=%s", want, secondUnread[0].Body)
		}
	}
}

func TestKBPendingSummaryManualReadDismissesUntilStateChange(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposerA := newAuthUser(t, srv)
	proposerB := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	createKBProposalForMailNoiseTest(t, srv, proposerA, "dismiss-first", "first dismissible pending item")
	initialUnread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "知识库待处理提案", nil, nil, 10)
	if err != nil {
		t.Fatalf("list initial KB pending summary: %v", err)
	}
	if len(initialUnread) != 1 {
		t.Fatalf("expected one initial unread KB pending summary, got=%d", len(initialUnread))
	}

	markReadResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/mark-read", map[string]any{
		"message_ids": []int64{initialUnread[0].MessageID},
	}, recipient.headers())
	if markReadResp.Code != http.StatusOK {
		t.Fatalf("mark read status=%d body=%s", markReadResp.Code, markReadResp.Body.String())
	}

	srv.sendKBPendingSummaryMails(ctx, []string{recipient.id})
	unreadAfterDismiss, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "知识库待处理提案", nil, nil, 10)
	if err != nil {
		t.Fatalf("list KB pending summary after dismiss: %v", err)
	}
	if len(unreadAfterDismiss) != 0 {
		t.Fatalf("expected no unread KB pending summary while state is unchanged after manual dismiss, got=%d", len(unreadAfterDismiss))
	}

	createKBProposalForMailNoiseTest(t, srv, proposerB, "dismiss-second", "second pending item changes the state")
	unreadAfterChange, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "知识库待处理提案", nil, nil, 10)
	if err != nil {
		t.Fatalf("list KB pending summary after state change: %v", err)
	}
	if len(unreadAfterChange) != 1 {
		t.Fatalf("expected new unread KB pending summary after state change, got=%d", len(unreadAfterChange))
	}
	if strings.Contains(unreadAfterChange[0].Subject, "[UPDATED]") {
		t.Fatalf("expected newly recreated KB pending summary to omit updated marker, subject=%s", unreadAfterChange[0].Subject)
	}
	for _, want := range []string{
		"pending_total=2",
		"dismiss-first",
		"dismiss-second",
	} {
		if !strings.Contains(unreadAfterChange[0].Body, want) {
			t.Fatalf("expected updated KB pending summary after state change to contain %q, body=%s", want, unreadAfterChange[0].Body)
		}
	}
}

func TestKBPendingSummaryDoesNotTruncateItemsAboveTwenty(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	for i := 1; i <= 21; i++ {
		createKBProposalForMailNoiseTest(t, srv, proposer, fmt.Sprintf("bulk-%02d", i), fmt.Sprintf("reason-%02d", i))
	}

	unread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "知识库待处理提案", nil, nil, 10)
	if err != nil {
		t.Fatalf("list KB pending summary after bulk proposals: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("expected one unread KB pending summary after bulk proposals, got=%d", len(unread))
	}
	body := unread[0].Body
	if strings.Contains(body, "未展开") {
		t.Fatalf("expected KB pending summary to avoid truncation markers, body=%s", body)
	}
	if got := strings.Count(body, "action_label=enroll"); got != 21 {
		t.Fatalf("expected 21 enroll action blocks in bulk summary, got=%d body=%s", got, body)
	}
}

func TestKBUpdatedSummaryTargetsAllActiveUsersAndCarriesFields(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	otherA := newAuthUser(t, srv)
	otherB := newAuthUser(t, srv)

	proposalID := applyKBProposalForMailNoiseTest(t, srv, proposer, "apply-targeting", "verify KB updated mail reaches all active users")
	proposal, err := srv.store.GetKBProposal(ctx, proposalID)
	if err != nil {
		t.Fatalf("get proposal: %v", err)
	}
	if proposal.AppliedAt == nil {
		t.Fatalf("expected applied_at to be set")
	}

	for _, user := range []authUser{proposer, otherA, otherB} {
		inbox, err := srv.store.ListMailbox(ctx, user.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 10)
		if err != nil {
			t.Fatalf("list %s inbox: %v", user.id, err)
		}
		if len(inbox) != 1 {
			t.Fatalf("expected %s to receive one KB updated summary, got=%d", user.id, len(inbox))
		}
		body := inbox[0].Body
		for _, want := range []string{
			kbUpdatedSummaryStreamMarker,
			"updated_count=1",
			fmt.Sprintf("proposal_id=%d", proposalID),
			"title=apply-targeting",
			"summary=verify KB updated mail reaches all active users",
			"entry_id=",
			"proposer_user_id=" + proposer.id,
			"proposer_user_name=",
			"op_type=add",
			"section=runtime-mail",
			"applied_at=" + proposal.AppliedAt.UTC().Format(time.RFC3339),
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("expected body to contain %q, body=%s", want, body)
			}
		}
	}
}

func TestKBUpdatedSummaryUpdatesInPlaceWhileUnread(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposerA := newAuthUser(t, srv)
	proposerB := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	firstProposalID := applyKBProposalForMailNoiseTest(t, srv, proposerA, "first-updated", "first KB updated event")
	firstUnread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 10)
	if err != nil {
		t.Fatalf("list first unread summary: %v", err)
	}
	if len(firstUnread) != 1 {
		t.Fatalf("expected one unread KB updated summary after first apply, got=%d", len(firstUnread))
	}
	firstMessageID := firstUnread[0].MessageID
	firstMailboxID := firstUnread[0].MailboxID

	secondProposalID := applyKBProposalForMailNoiseTest(t, srv, proposerB, "second-updated", "second KB updated event")
	secondUnread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 10)
	if err != nil {
		t.Fatalf("list second unread summary: %v", err)
	}
	if len(secondUnread) != 1 {
		t.Fatalf("expected one unread KB updated summary after second apply, got=%d", len(secondUnread))
	}
	if secondUnread[0].MessageID != firstMessageID {
		t.Fatalf("expected KB updated summary to update in place, first_message_id=%d second_message_id=%d", firstMessageID, secondUnread[0].MessageID)
	}
	if secondUnread[0].MailboxID != firstMailboxID {
		t.Fatalf("expected KB updated summary to keep same mailbox id, first=%d second=%d", firstMailboxID, secondUnread[0].MailboxID)
	}
	if !strings.Contains(secondUnread[0].Subject, "[UPDATED]") {
		t.Fatalf("expected in-place updated KB updated summary subject to carry updated marker, subject=%s", secondUnread[0].Subject)
	}
	body := secondUnread[0].Body
	for _, want := range []string{
		fmt.Sprintf("proposal_id=%d", firstProposalID),
		fmt.Sprintf("proposal_id=%d", secondProposalID),
		"updated_count=2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected updated body to contain %q, body=%s", want, body)
		}
	}
}

func TestMailInboxAutoMarksReturnedKBUpdatedReadAndStoresSeenBoundary(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)

	applyKBProposalForMailNoiseTest(t, srv, proposer, "closed-kb-updated", "verify inbox-returned KB updated mail is auto-read")
	unreadBefore, err := srv.store.ListMailbox(ctx, proposer.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB updated inbox before read path: %v", err)
	}
	if len(unreadBefore) != 1 {
		t.Fatalf("expected one unread KB updated mail before read path, got=%d", len(unreadBefore))
	}

	overviewResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/overview?folder=inbox&scope=unread&limit=20", nil, proposer.headers())
	if overviewResp.Code != http.StatusOK {
		t.Fatalf("mail overview status=%d body=%s", overviewResp.Code, overviewResp.Body.String())
	}
	unreadAfterOverview, err := srv.store.ListMailbox(ctx, proposer.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB updated inbox after overview: %v", err)
	}
	if len(unreadAfterOverview) != 1 {
		t.Fatalf("expected overview to leave KB updated unread untouched, got=%d", len(unreadAfterOverview))
	}

	inboxResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?scope=unread&limit=20", nil, proposer.headers())
	if inboxResp.Code != http.StatusOK {
		t.Fatalf("mail inbox status=%d body=%s", inboxResp.Code, inboxResp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, proposer.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB updated inbox after inbox read: %v", err)
	}
	if len(unreadAfter) != 0 {
		t.Fatalf("expected KB updated mail to auto-read after inbox return, got unread=%d", len(unreadAfter))
	}
	state, ok, err := srv.store.GetNotificationDeliveryState(ctx, proposer.id, notificationCategoryKBUpdatedSummary)
	if err != nil {
		t.Fatalf("get KB updated delivery state: %v", err)
	}
	if !ok {
		t.Fatalf("expected KB updated delivery state to exist")
	}
	if state.LastSeenAt.IsZero() {
		t.Fatalf("expected LastSeenAt to be recorded after inbox return")
	}
	if state.OutstandingMailboxID != 0 || state.OutstandingMessageID != 0 {
		t.Fatalf("expected outstanding ids to be cleared after inbox return: %+v", state)
	}
}

func TestKBUpdatedSummaryWaitsThreeHoursAfterSeenBeforeCreatingNewSummary(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposerA := newAuthUser(t, srv)
	proposerB := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	applyKBProposalForMailNoiseTest(t, srv, proposerA, "first-window", "first KB updated summary in cadence test")
	inboxResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?scope=unread&limit=20", nil, recipient.headers())
	if inboxResp.Code != http.StatusOK {
		t.Fatalf("recipient inbox status=%d body=%s", inboxResp.Code, inboxResp.Body.String())
	}
	state, ok, err := srv.store.GetNotificationDeliveryState(ctx, recipient.id, notificationCategoryKBUpdatedSummary)
	if err != nil {
		t.Fatalf("get state after seen: %v", err)
	}
	if !ok || state.LastSeenAt.IsZero() {
		t.Fatalf("expected KB updated state with LastSeenAt after inbox read: %+v ok=%v", state, ok)
	}

	secondProposalID := applyKBProposalForMailNoiseTest(t, srv, proposerB, "second-window", "second KB updated summary in cadence test")
	unreadImmediately, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread immediately after second apply: %v", err)
	}
	if len(unreadImmediately) != 0 {
		t.Fatalf("expected no immediate KB updated summary within 3h of seen boundary, got=%d", len(unreadImmediately))
	}

	state.LastSeenAt = state.LastSeenAt.Add(-kbUpdatedSummarySendInterval - time.Minute)
	if _, err := srv.store.UpsertNotificationDeliveryState(ctx, state); err != nil {
		t.Fatalf("backdate KB updated LastSeenAt: %v", err)
	}
	srv.sendKBUpdatedSummaryMails(ctx)

	unreadAfterWindow, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread after 3h cadence: %v", err)
	}
	if len(unreadAfterWindow) != 1 {
		t.Fatalf("expected one new KB updated summary after cadence window, got=%d", len(unreadAfterWindow))
	}
	if strings.Contains(unreadAfterWindow[0].Subject, "[UPDATED]") {
		t.Fatalf("expected freshly created KB updated summary to omit updated marker, subject=%s", unreadAfterWindow[0].Subject)
	}
	body := unreadAfterWindow[0].Body
	if !strings.Contains(body, fmt.Sprintf("proposal_id=%d", secondProposalID)) {
		t.Fatalf("expected new summary to include second applied proposal, body=%s", body)
	}
}

func TestKBUpdatedSummaryDoesNotTruncateItemsAboveTwenty(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	recipient := newAuthUser(t, srv)

	proposalIDs := make([]int64, 0, 21)
	for i := 0; i < 21; i++ {
		proposer := newAuthUser(t, srv)
		proposalIDs = append(proposalIDs, applyKBProposalForMailNoiseTest(t, srv, proposer, fmt.Sprintf("bulk-updated-%02d", i+1), "bulk KB updated summary item"))
	}
	unread, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 10)
	if err != nil {
		t.Fatalf("list unread KB updated bulk summary: %v", err)
	}
	if len(unread) != 1 {
		t.Fatalf("expected one unread KB updated summary, got=%d", len(unread))
	}
	body := unread[0].Body
	if strings.Contains(body, "未展开") {
		t.Fatalf("expected KB updated summary to avoid truncation marker, body=%s", body)
	}
	if got := strings.Count(body, "proposal_id="); got != len(proposalIDs) {
		t.Fatalf("expected all proposal items to be present, got=%d want=%d body=%s", got, len(proposalIDs), body)
	}
	if !strings.Contains(body, "updated_count=21") {
		t.Fatalf("expected updated_count=21, body=%s", body)
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

func TestMailSystemResolveObsoleteKBDryRunSupportsKBPendingCompact(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	proposalID := createKBProposalForMailNoiseTest(t, srv, proposer, "pending-compact-dry-run", "verify KB pending compact dry-run previews duplicate cleanup")
	proposal, err := srv.store.GetKBProposal(ctx, proposalID)
	if err != nil {
		t.Fatalf("get proposal: %v", err)
	}
	if _, err := srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P2][ACTION:ENROLL] #%d legacy duplicate", proposalID),
		Body:    fmt.Sprintf("proposal_id=%d\ncurrent_revision_id=%d\nreason=legacy duplicate", proposalID, proposal.CurrentRevisionID),
	}); err != nil {
		t.Fatalf("seed legacy pending duplicate: %v", err)
	}
	if _, err := srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P2][ACTION:ENROLL] 知识库待处理提案 1 项 [REF:knowledge-base.md]",
		Body:    "pending_total=1\nvote_count=0\nenroll_count=1\n\n待招募\n1. proposal_id=" + strconv.FormatInt(proposalID, 10),
	}); err != nil {
		t.Fatalf("seed old summary duplicate: %v", err)
	}

	unreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB pending inbox before compact dry-run: %v", err)
	}
	if len(unreadBefore) != 3 {
		t.Fatalf("expected three unread KB pending mails before compact dry-run, got=%d", len(unreadBefore))
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  true,
		"classes":  []string{obsoleteMailClassKBPendingCompact},
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("KB pending compact dry-run status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got != 1 {
		t.Fatalf("expected KB pending compact dry-run affected_user_count=1 got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got != 2 {
		t.Fatalf("expected KB pending compact dry-run resolved_mailbox_count=2 got=%d body=%s", got, resp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB pending inbox after compact dry-run: %v", err)
	}
	if len(unreadAfter) != 3 {
		t.Fatalf("expected KB pending compact dry-run to leave unread mail untouched, got=%d", len(unreadAfter))
	}
}

func TestMailSystemResolveObsoleteKBPendingCompactExecutesAndKeepsSingleManagedUnread(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	proposalID := createKBProposalForMailNoiseTest(t, srv, proposer, "pending-compact-execute", "verify KB pending compact keeps one managed unread")
	proposal, err := srv.store.GetKBProposal(ctx, proposalID)
	if err != nil {
		t.Fatalf("get proposal: %v", err)
	}
	if _, err := srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P2][ACTION:ENROLL] #%d legacy duplicate", proposalID),
		Body:    fmt.Sprintf("proposal_id=%d\ncurrent_revision_id=%d\nreason=legacy duplicate", proposalID, proposal.CurrentRevisionID),
	}); err != nil {
		t.Fatalf("seed legacy pending duplicate: %v", err)
	}
	if _, err := srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipient.id},
		Subject: "[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P2][ACTION:ENROLL] 知识库待处理提案 1 项 [REF:knowledge-base.md]",
		Body:    "pending_total=1\nvote_count=0\nenroll_count=1\n\n待招募\n1. proposal_id=" + strconv.FormatInt(proposalID, 10),
	}); err != nil {
		t.Fatalf("seed old summary duplicate: %v", err)
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  false,
		"classes":  []string{obsoleteMailClassKBPendingCompact},
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("KB pending compact execute status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got != 1 {
		t.Fatalf("expected KB pending compact execute affected_user_count=1 got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got != 2 {
		t.Fatalf("expected KB pending compact execute resolved_mailbox_count=2 got=%d body=%s", got, resp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB pending inbox after compact execute: %v", err)
	}
	if len(unreadAfter) != 1 {
		t.Fatalf("expected KB pending compact execute to keep exactly one unread managed summary, got=%d", len(unreadAfter))
	}
	if !strings.Contains(unreadAfter[0].Body, kbPendingSummaryStreamMarker) {
		t.Fatalf("expected remaining unread KB pending mail to be managed summary, body=%s", unreadAfter[0].Body)
	}
	readDuplicates, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "read", "duplicate", nil, nil, 20)
	if err != nil {
		t.Fatalf("list read duplicates after compact execute: %v", err)
	}
	if len(readDuplicates) != 1 {
		t.Fatalf("expected legacy duplicate to be marked read, got=%d", len(readDuplicates))
	}
}

func TestMailSystemResolveObsoleteKBDryRunSupportsKBUpdatesClass(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipientID := "legacy-kb-updated-preview-user"

	proposalID := applyKBProposalForMailNoiseTest(t, srv, proposer, "obsolete-kb-updated-dry-run", "verify obsolete KB cleanup dry-run can preview legacy KB updated mail")
	proposal, err := srv.store.GetKBProposal(ctx, proposalID)
	if err != nil {
		t.Fatalf("get proposal: %v", err)
	}
	seedLegacyKBUpdatedMailForMailNoiseTest(t, srv, recipientID, proposalID, proposal.Title, *proposal.AppliedAt)

	unreadBefore, err := srv.store.ListMailbox(ctx, recipientID, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB updated inbox before dry-run: %v", err)
	}
	if len(unreadBefore) != 1 {
		t.Fatalf("expected one unread KB updated mail before dry-run, got=%d", len(unreadBefore))
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  true,
		"classes":  []string{obsoleteMailClassKBUpdates},
		"user_ids": []string{recipientID},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("obsolete KB updated dry-run status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got != 1 {
		t.Fatalf("expected KB updated dry-run affected_user_count=1 got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got != 1 {
		t.Fatalf("expected KB updated dry-run resolved_mailbox_count=1 got=%d body=%s", got, resp.Body.String())
	}

	unreadAfter, err := srv.store.ListMailbox(ctx, recipientID, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread KB updated inbox after dry-run: %v", err)
	}
	if len(unreadAfter) != 1 {
		t.Fatalf("expected dry-run to leave unread KB updated mail untouched, got=%d", len(unreadAfter))
	}
}

func TestMailSystemResolveObsoleteKBDryRunSkipsManagedKBUpdatedSummary(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	proposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	applyKBProposalForMailNoiseTest(t, srv, proposer, "managed-kb-updated-dry-run", "verify cleanup skips managed KB updated summary stream")
	unreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list unread managed KB updated inbox before dry-run: %v", err)
	}
	if len(unreadBefore) != 1 {
		t.Fatalf("expected one unread managed KB updated summary before dry-run, got=%d", len(unreadBefore))
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  true,
		"classes":  []string{obsoleteMailClassKBUpdates},
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("managed KB updated dry-run status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got != 0 {
		t.Fatalf("expected managed KB updated dry-run affected_user_count=0 got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got != 0 {
		t.Fatalf("expected managed KB updated dry-run resolved_mailbox_count=0 got=%d body=%s", got, resp.Body.String())
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

func TestMailSystemResolveObsoleteKBOnlyKBUpdatesClassLeavesKBPendingUnread(t *testing.T) {
	srv := newTestServer()
	srv.cfg.InternalSyncToken = "sync-token"
	ctx := context.Background()
	updatedProposer := newAuthUser(t, srv)
	updatedProposalID := applyKBProposalForMailNoiseTest(t, srv, updatedProposer, "kb-updated-only-cleanup", "verify kb_updates-only cleanup only resolves legacy KB updated unread")
	updatedProposal, err := srv.store.GetKBProposal(ctx, updatedProposalID)
	if err != nil {
		t.Fatalf("get updated proposal: %v", err)
	}

	pendingProposer := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)

	pendingResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "kb-pending-survives-kb-updated-cleanup",
		"reason":                    "verify KB pending mail is untouched by kb_updates-only cleanup",
		"vote_threshold_pct":        50,
		"vote_window_seconds":       300,
		"discussion_window_seconds": 300,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "runtime-mail",
			"title":       "kb-pending-survives-kb-updated-cleanup",
			"new_content": "pending KB mail should stay unread",
			"diff_text":   "kb updates cleanup should not touch KB pending unread",
		},
	}, pendingProposer.headers())
	if pendingResp.Code != http.StatusAccepted {
		t.Fatalf("create pending proposal status=%d body=%s", pendingResp.Code, pendingResp.Body.String())
	}
	seedLegacyKBUpdatedMailForMailNoiseTest(t, srv, recipient.id, updatedProposalID, updatedProposal.Title, *updatedProposal.AppliedAt)

	pendingUnreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list KB pending inbox before kb_updates-only cleanup: %v", err)
	}
	if len(pendingUnreadBefore) != 1 {
		t.Fatalf("expected one KB pending unread before kb_updates-only cleanup, got=%d", len(pendingUnreadBefore))
	}
	updatedUnreadBefore, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list KB updated inbox before kb_updates-only cleanup: %v", err)
	}
	if len(updatedUnreadBefore) != 1 {
		t.Fatalf("expected one KB updated unread before kb_updates-only cleanup, got=%d", len(updatedUnreadBefore))
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/system/resolve-obsolete-kb", map[string]any{
		"dry_run":  false,
		"classes":  []string{obsoleteMailClassKBUpdates},
		"user_ids": []string{recipient.id},
	}, map[string]string{
		"X-Clawcolony-Internal-Token": "sync-token",
	})
	if resp.Code != http.StatusAccepted {
		t.Fatalf("obsolete KB updated cleanup status=%d body=%s", resp.Code, resp.Body.String())
	}
	body := parseJSONBody(t, resp)
	result := body["result"].(map[string]any)
	if got := int(result["affected_user_count"].(float64)); got != 1 {
		t.Fatalf("expected kb_updates-only cleanup affected_user_count=1 got=%d body=%s", got, resp.Body.String())
	}
	if got := int(result["resolved_mailbox_count"].(float64)); got != 1 {
		t.Fatalf("expected kb_updates-only cleanup resolved_mailbox_count=1 got=%d body=%s", got, resp.Body.String())
	}

	pendingUnreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE-PROPOSAL]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list KB pending inbox after kb_updates-only cleanup: %v", err)
	}
	if len(pendingUnreadAfter) != 1 {
		t.Fatalf("expected kb_updates-only cleanup to leave KB pending unread untouched, got=%d", len(pendingUnreadAfter))
	}
	updatedUnreadAfter, err := srv.store.ListMailbox(ctx, recipient.id, "inbox", "unread", "[KNOWLEDGEBASE Updated]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list KB updated inbox after kb_updates-only cleanup: %v", err)
	}
	if len(updatedUnreadAfter) != 0 {
		t.Fatalf("expected kb_updates-only cleanup to resolve KB updated unread, got=%d", len(updatedUnreadAfter))
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
