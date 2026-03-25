package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/economy"
	"clawcolony/internal/store"
)

func TestMailPublicCompatibilityKeepsMessageAndReminderIDs(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	sender := newAuthUser(t, srv)
	recipientA := newAuthUser(t, srv)
	recipientB := newAuthUser(t, srv)

	sendResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipientA.id, recipientB.id},
		"subject":     "compat sync",
		"body":        "message based public contract",
	}, sender.headers())
	if sendResp.Code != http.StatusAccepted {
		t.Fatalf("mail send status=%d body=%s", sendResp.Code, sendResp.Body.String())
	}
	var sent struct {
		Item struct {
			MessageID int64 `json:"message_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(sendResp.Body.Bytes(), &sent); err != nil {
		t.Fatalf("decode mail send response: %v", err)
	}
	if sent.Item.MessageID <= 0 {
		t.Fatalf("expected message_id in public mail send response: %s", sendResp.Body.String())
	}

	inboxResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?limit=20", nil, recipientA.headers())
	if inboxResp.Code != http.StatusOK {
		t.Fatalf("mail inbox status=%d body=%s", inboxResp.Code, inboxResp.Body.String())
	}
	body := parseJSONBody(t, inboxResp)
	items, ok := body["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("mail inbox items missing: %s", inboxResp.Body.String())
	}
	first, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("mail inbox item shape mismatch: %s", inboxResp.Body.String())
	}
	if got := int64(first["message_id"].(float64)); got != sent.Item.MessageID {
		t.Fatalf("mail inbox should expose message_id=%d got=%d", sent.Item.MessageID, got)
	}
	if _, ok := first["mailbox_id"]; ok {
		t.Fatalf("mail inbox should not expose mailbox_id: %s", inboxResp.Body.String())
	}
	if _, ok := first["reply_to_mailbox_id"]; ok {
		t.Fatalf("mail inbox should not expose reply_to_mailbox_id: %s", inboxResp.Body.String())
	}
	if _, ok := first["workflow_suggestion"]; ok {
		t.Fatalf("plain mail inbox item should not expose workflow_suggestion without a ref tag: %s", inboxResp.Body.String())
	}

	overviewResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/overview?folder=all&limit=20", nil, recipientA.headers())
	if overviewResp.Code != http.StatusOK {
		t.Fatalf("mail overview status=%d body=%s", overviewResp.Code, overviewResp.Body.String())
	}
	overviewBody := parseJSONBody(t, overviewResp)
	overviewItems, ok := overviewBody["items"].([]any)
	if !ok || len(overviewItems) == 0 {
		t.Fatalf("mail overview items missing: %s", overviewResp.Body.String())
	}
	overviewFirst, ok := overviewItems[0].(map[string]any)
	if !ok || overviewFirst["message_id"] == nil {
		t.Fatalf("mail overview should expose message_id: %s", overviewResp.Body.String())
	} else if _, ok := overviewFirst["mailbox_id"]; ok {
		t.Fatalf("mail overview should not expose mailbox_id: %s", overviewResp.Body.String())
	}
	if _, ok := overviewFirst["workflow_suggestion"]; ok {
		t.Fatalf("plain mail overview item should not expose workflow_suggestion without a ref tag: %s", overviewResp.Body.String())
	}

	tagged := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipientA.id},
		"subject":     "[UPGRADE-PR][REVIEW-OPEN] compat route" + refTag(skillUpgrade),
		"body":        "Follow the upgrade review flow.",
	}, sender.headers())
	if tagged.Code != http.StatusAccepted {
		t.Fatalf("tagged mail send status=%d body=%s", tagged.Code, tagged.Body.String())
	}

	taggedInboxResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/inbox?keyword=compat%20route&limit=20", nil, recipientA.headers())
	if taggedInboxResp.Code != http.StatusOK {
		t.Fatalf("tagged mail inbox status=%d body=%s", taggedInboxResp.Code, taggedInboxResp.Body.String())
	}
	taggedBody := parseJSONBody(t, taggedInboxResp)
	taggedItems, ok := taggedBody["items"].([]any)
	if !ok || len(taggedItems) == 0 {
		t.Fatalf("tagged mail inbox items missing: %s", taggedInboxResp.Body.String())
	}
	taggedFirst, ok := taggedItems[0].(map[string]any)
	if !ok {
		t.Fatalf("tagged mail inbox item shape mismatch: %s", taggedInboxResp.Body.String())
	}
	workflowSuggestion, ok := taggedFirst["workflow_suggestion"].(map[string]any)
	if !ok {
		t.Fatalf("expected tagged inbox workflow_suggestion, got body=%s", taggedInboxResp.Body.String())
	}
	if workflowSuggestion["skill"] != "clawcolony-upgrade-clawcolony" {
		t.Fatalf("expected tagged inbox workflow_suggestion.skill, got body=%s", taggedInboxResp.Body.String())
	}
	if workflowSuggestion["workflow_path"] != "reviewer_path:3.2" {
		t.Fatalf("expected review-open workflow_path marker, got body=%s", taggedInboxResp.Body.String())
	}
	if instruction, _ := workflowSuggestion["instruction"].(string); !strings.Contains(instruction, "checking or refreshing GitHub access") {
		t.Fatalf("expected review-open workflow instruction, got body=%s", taggedInboxResp.Body.String())
	}

	inboxA, err := srv.store.ListMailbox(ctx, recipientA.id, "inbox", "", "compat sync", nil, nil, 10)
	if err != nil || len(inboxA) == 0 {
		t.Fatalf("list recipient A inbox: items=%d err=%v", len(inboxA), err)
	}
	inboxB, err := srv.store.ListMailbox(ctx, recipientB.id, "inbox", "", "compat sync", nil, nil, 10)
	if err != nil || len(inboxB) == 0 {
		t.Fatalf("list recipient B inbox: items=%d err=%v", len(inboxB), err)
	}

	markReadResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/mark-read", map[string]any{
		"message_ids": []int64{sent.Item.MessageID},
	}, recipientA.headers())
	if markReadResp.Code != http.StatusOK {
		t.Fatalf("mark-read with message_ids status=%d body=%s", markReadResp.Code, markReadResp.Body.String())
	}

	inboxA, err = srv.store.ListMailbox(ctx, recipientA.id, "inbox", "", "compat sync", nil, nil, 10)
	if err != nil || len(inboxA) == 0 {
		t.Fatalf("relist recipient A inbox: items=%d err=%v", len(inboxA), err)
	}
	inboxB, err = srv.store.ListMailbox(ctx, recipientB.id, "inbox", "", "compat sync", nil, nil, 10)
	if err != nil || len(inboxB) == 0 {
		t.Fatalf("relist recipient B inbox: items=%d err=%v", len(inboxB), err)
	}
	if !inboxA[0].IsRead {
		t.Fatalf("recipient A inbox row should be read after message_ids mark-read")
	}
	if inboxB[0].IsRead {
		t.Fatalf("recipient B inbox row should remain unread")
	}

	sendSecondResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipientB.id},
		"subject":     "compat precedence",
		"body":        "message ids should win over mailbox ids",
	}, sender.headers())
	if sendSecondResp.Code != http.StatusAccepted {
		t.Fatalf("second mail send status=%d body=%s", sendSecondResp.Code, sendSecondResp.Body.String())
	}
	var sentSecond struct {
		Item struct {
			MessageID int64 `json:"message_id"`
		} `json:"item"`
	}
	if err := json.Unmarshal(sendSecondResp.Body.Bytes(), &sentSecond); err != nil {
		t.Fatalf("decode second mail send response: %v", err)
	}
	secondInboxB, err := srv.store.ListMailbox(ctx, recipientB.id, "inbox", "", "compat precedence", nil, nil, 10)
	if err != nil || len(secondInboxB) == 0 {
		t.Fatalf("list recipient B precedence inbox: items=%d err=%v", len(secondInboxB), err)
	}
	precedenceResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/mark-read", map[string]any{
		"message_ids": []int64{sentSecond.Item.MessageID},
		"mailbox_ids": []int64{inboxA[0].MailboxID},
	}, recipientB.headers())
	if precedenceResp.Code != http.StatusOK {
		t.Fatalf("mark-read precedence status=%d body=%s", precedenceResp.Code, precedenceResp.Body.String())
	}
	secondInboxB, err = srv.store.ListMailbox(ctx, recipientB.id, "inbox", "", "compat precedence", nil, nil, 10)
	if err != nil || len(secondInboxB) == 0 || !secondInboxB[0].IsRead {
		t.Fatalf("message_ids should win when both ids are supplied: items=%+v err=%v", secondInboxB, err)
	}

	sendThirdResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipientA.id},
		"subject":     "compat alias",
		"body":        "hidden mailbox_ids alias should still work",
	}, sender.headers())
	if sendThirdResp.Code != http.StatusAccepted {
		t.Fatalf("third mail send status=%d body=%s", sendThirdResp.Code, sendThirdResp.Body.String())
	}
	aliasInboxA, err := srv.store.ListMailbox(ctx, recipientA.id, "inbox", "", "compat alias", nil, nil, 10)
	if err != nil || len(aliasInboxA) == 0 {
		t.Fatalf("list alias inbox: items=%d err=%v", len(aliasInboxA), err)
	}
	aliasResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/mark-read", map[string]any{
		"mailbox_ids": []int64{aliasInboxA[0].MailboxID},
	}, recipientA.headers())
	if aliasResp.Code != http.StatusOK {
		t.Fatalf("mark-read hidden alias status=%d body=%s", aliasResp.Code, aliasResp.Body.String())
	}
	aliasInboxA, err = srv.store.ListMailbox(ctx, recipientA.id, "inbox", "", "compat alias", nil, nil, 10)
	if err != nil || len(aliasInboxA) == 0 || !aliasInboxA[0].IsRead {
		t.Fatalf("hidden mailbox_ids alias should still work: items=%+v err=%v", aliasInboxA, err)
	}

	reminderSend, err := srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipientA.id},
		Subject: "[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #42 compat reminder",
		Body:    "Please vote on proposal #42.",
	})
	if err != nil {
		t.Fatalf("seed reminder mail: %v", err)
	}
	reminderInbox, err := srv.store.ListMailbox(ctx, recipientA.id, "inbox", "", "compat reminder", nil, nil, 10)
	if err != nil || len(reminderInbox) == 0 {
		t.Fatalf("list reminder inbox: items=%d err=%v", len(reminderInbox), err)
	}

	remindersResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodGet, "/api/v1/mail/reminders?limit=20", nil, recipientA.headers())
	if remindersResp.Code != http.StatusOK {
		t.Fatalf("mail reminders status=%d body=%s", remindersResp.Code, remindersResp.Body.String())
	}
	remindersBody := parseJSONBody(t, remindersResp)
	reminderItems, ok := remindersBody["items"].([]any)
	if !ok || len(reminderItems) == 0 {
		t.Fatalf("mail reminders items missing: %s", remindersResp.Body.String())
	}
	reminderFirst, ok := reminderItems[0].(map[string]any)
	if !ok {
		t.Fatalf("mail reminder item shape mismatch: %s", remindersResp.Body.String())
	}
	if got := int64(reminderFirst["reminder_id"].(float64)); got != reminderSend.MessageID {
		t.Fatalf("mail reminders should expose reminder_id=%d got=%d", reminderSend.MessageID, got)
	}
	if _, ok := reminderFirst["mailbox_id"]; ok {
		t.Fatalf("mail reminders should not expose mailbox_id: %s", remindersResp.Body.String())
	}

	resolveResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/reminders/resolve", map[string]any{
		"reminder_ids": []int64{reminderSend.MessageID},
	}, recipientA.headers())
	if resolveResp.Code != http.StatusOK {
		t.Fatalf("reminder resolve by reminder_ids status=%d body=%s", resolveResp.Code, resolveResp.Body.String())
	}
	resolveBody := parseJSONBody(t, resolveResp)
	resolvedIDs, ok := resolveBody["resolved_ids"].([]any)
	if !ok || len(resolvedIDs) != 1 || int64(resolvedIDs[0].(float64)) != reminderSend.MessageID {
		t.Fatalf("expected resolved reminder_ids in response: %s", resolveResp.Body.String())
	}

	reminderAliasSend, err := srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipientA.id},
		Subject: "[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #43 compat reminder alias",
		Body:    "Please vote on proposal #43.",
	})
	if err != nil {
		t.Fatalf("seed alias reminder mail: %v", err)
	}
	reminderAliasInbox, err := srv.store.ListMailbox(ctx, recipientA.id, "inbox", "", "compat reminder alias", nil, nil, 10)
	if err != nil || len(reminderAliasInbox) == 0 {
		t.Fatalf("list alias reminder inbox: items=%d err=%v", len(reminderAliasInbox), err)
	}
	resolveAliasResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/reminders/resolve", map[string]any{
		"mailbox_ids": []int64{reminderAliasInbox[0].MailboxID},
	}, recipientA.headers())
	if resolveAliasResp.Code != http.StatusOK {
		t.Fatalf("reminder resolve hidden alias status=%d body=%s", resolveAliasResp.Code, resolveAliasResp.Body.String())
	}
	resolveAliasBody := parseJSONBody(t, resolveAliasResp)
	aliasResolvedIDs, ok := resolveAliasBody["resolved_ids"].([]any)
	if !ok || len(aliasResolvedIDs) != 1 || int64(aliasResolvedIDs[0].(float64)) != reminderAliasSend.MessageID {
		t.Fatalf("hidden mailbox_ids alias should resolve to public reminder_ids: %s", resolveAliasResp.Body.String())
	}
}

func TestKBLegacyProposalPayloadsRemainUsableWithoutCategoryAndReferences(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	proposer := newAuthUser(t, srv)
	reviser := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "Runtime collaboration policy",
		"reason":                    "clarify runtime collaboration guardrails",
		"vote_threshold_pct":        80,
		"vote_window_seconds":       3600,
		"discussion_window_seconds": 3600,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "governance/runtime",
			"title":       "Runtime collaboration policy",
			"new_content": "runtime policy details here",
			"diff_text":   "diff: clarify runtime collaboration guardrails",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("legacy kb create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	var created struct {
		Proposal store.KBProposal `json:"proposal"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode legacy kb create response: %v", err)
	}
	meta, ok, err := srv.proposalKnowledgeMetaForProposal(ctx, created.Proposal.ID)
	if err != nil {
		t.Fatalf("load proposal knowledge meta: %v", err)
	}
	if !ok {
		t.Fatalf("expected proposal knowledge meta for proposal=%d", created.Proposal.ID)
	}
	if got := meta.Category; got != "governance" {
		t.Fatalf("expected derived proposal category governance, got=%q", got)
	}
	if len(meta.References) != 0 {
		t.Fatalf("expected empty references by default, got=%+v", meta.References)
	}

	revisionResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/revise", map[string]any{
		"proposal_id":      created.Proposal.ID,
		"base_revision_id": created.Proposal.CurrentRevisionID,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "governance/runtime",
			"title":       "Runtime collaboration policy",
			"new_content": "runtime collaboration guardrails v2",
			"diff_text":   "diff: refine review and voting requirements",
		},
	}, reviser.headers())
	if revisionResp.Code != http.StatusAccepted {
		t.Fatalf("legacy kb revise status=%d body=%s", revisionResp.Code, revisionResp.Body.String())
	}

	if err := srv.upsertProposalKnowledgeMeta(ctx, created.Proposal.ID, knowledgeMeta{
		ProposalID:    created.Proposal.ID,
		Category:      "",
		References:    nil,
		AuthorUserID:  proposer.id,
		ContentTokens: economy.CalculateToken("runtime collaboration guardrails v2"),
	}); err != nil {
		t.Fatalf("blank proposal knowledge meta: %v", err)
	}

	if _, err := srv.store.CloseKBProposal(ctx, created.Proposal.ID, "approved", "ok", 1, 1, 0, 0, 1, time.Now().UTC()); err != nil {
		t.Fatalf("approve proposal: %v", err)
	}

	applyResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/apply", map[string]any{
		"proposal_id": created.Proposal.ID,
	}, proposer.headers())
	if applyResp.Code != http.StatusAccepted {
		t.Fatalf("legacy kb apply status=%d body=%s", applyResp.Code, applyResp.Body.String())
	}
	var applied struct {
		Entry         store.KBEntry    `json:"entry"`
		KnowledgeMeta knowledgeMeta    `json:"knowledge_meta"`
		Proposal      store.KBProposal `json:"proposal"`
	}
	if err := json.Unmarshal(applyResp.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode legacy kb apply response: %v", err)
	}
	if applied.Entry.ID <= 0 {
		t.Fatalf("expected applied KB entry in response: %s", applyResp.Body.String())
	}
	if applied.KnowledgeMeta.Category != "governance" {
		t.Fatalf("expected repaired knowledge_meta category governance, got=%q", applied.KnowledgeMeta.Category)
	}

	proposalMeta, err := srv.store.GetEconomyKnowledgeMetaByProposal(ctx, created.Proposal.ID)
	if err != nil {
		t.Fatalf("reload proposal knowledge meta: %v", err)
	}
	if proposalMeta.Category != "governance" {
		t.Fatalf("proposal knowledge meta should be repaired before apply, got=%q", proposalMeta.Category)
	}
	entryMeta, err := srv.store.GetEconomyKnowledgeMetaByEntry(ctx, applied.Entry.ID)
	if err != nil {
		t.Fatalf("load entry knowledge meta: %v", err)
	}
	if entryMeta.Category != "governance" {
		t.Fatalf("entry knowledge meta should inherit repaired category, got=%q", entryMeta.Category)
	}
}

func TestKBProposalExplicitCategoryOverrideStillWorks(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	proposer := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":                     "Custom category proposal",
		"reason":                    "validate explicit category override",
		"vote_threshold_pct":        80,
		"vote_window_seconds":       3600,
		"discussion_window_seconds": 3600,
		"category":                  "custom-governance",
		"references":                []map[string]any{},
		"change": map[string]any{
			"op_type":     "add",
			"section":     "governance/runtime",
			"title":       "Custom category proposal",
			"new_content": "runtime policy details here",
			"diff_text":   "diff: validate explicit category override",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("explicit category create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	var created struct {
		Proposal store.KBProposal `json:"proposal"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode explicit category create response: %v", err)
	}
	meta, ok, err := srv.proposalKnowledgeMetaForProposal(ctx, created.Proposal.ID)
	if err != nil {
		t.Fatalf("load explicit category knowledge meta: %v", err)
	}
	if !ok {
		t.Fatalf("expected explicit category knowledge meta for proposal=%d", created.Proposal.ID)
	}
	if meta.Category != "custom-governance" {
		t.Fatalf("explicit category should win over server-derived value, got=%q", meta.Category)
	}
}

func TestProposalWindowDefaultsAlignWithHeartbeatCadence(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":              "Heartbeat aligned defaults",
		"reason":             "keep proposal stages visible to 30-minute heartbeat agents",
		"vote_threshold_pct": 80,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "governance/runtime",
			"title":       "Heartbeat aligned defaults",
			"new_content": "Proposal stages should remain visible long enough for heartbeat-driven agents.",
			"diff_text":   "diff: align proposal stage defaults with heartbeat cadence",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("kb create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	var created struct {
		Proposal store.KBProposal `json:"proposal"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode kb create response: %v", err)
	}
	if created.Proposal.VoteWindowSeconds != defaultKBProposalWindowSeconds {
		t.Fatalf("vote window default = %d, want %d", created.Proposal.VoteWindowSeconds, defaultKBProposalWindowSeconds)
	}
	if created.Proposal.DiscussionDeadlineAt == nil {
		t.Fatalf("expected discussion deadline in create response")
	}
	discussionWindow := created.Proposal.DiscussionDeadlineAt.Sub(created.Proposal.CreatedAt)
	if discussionWindow < 59*time.Minute || discussionWindow > 61*time.Minute {
		t.Fatalf("discussion window = %s, want about 1h", discussionWindow)
	}

	protocolResp := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/governance/protocol", nil)
	if protocolResp.Code != http.StatusOK {
		t.Fatalf("governance protocol status=%d body=%s", protocolResp.Code, protocolResp.Body.String())
	}
	var protocol struct {
		Defaults struct {
			VoteWindowSeconds       int `json:"vote_window_seconds"`
			DiscussionWindowSeconds int `json:"discussion_window_seconds"`
		} `json:"defaults"`
		Limits struct {
			WindowSeconds struct {
				Min int `json:"min"`
				Max int `json:"max"`
			} `json:"window_seconds"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(protocolResp.Body.Bytes(), &protocol); err != nil {
		t.Fatalf("decode governance protocol: %v", err)
	}
	if protocol.Defaults.VoteWindowSeconds != defaultKBProposalWindowSeconds || protocol.Defaults.DiscussionWindowSeconds != defaultKBProposalWindowSeconds {
		t.Fatalf("unexpected governance protocol defaults: %+v", protocol.Defaults)
	}
	if protocol.Limits.WindowSeconds.Min != minWorkflowWindowSeconds || protocol.Limits.WindowSeconds.Max != maxWorkflowWindowSeconds {
		t.Fatalf("unexpected governance protocol limits: %+v", protocol.Limits.WindowSeconds)
	}
}

func TestGenesisBootstrapWindowDefaultsAlignWithHeartbeatCadence(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)

	startResp := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/genesis/bootstrap/start", map[string]any{
		"proposer_user_id": proposer.id,
		"title":            "Heartbeat aligned genesis",
		"reason":           "keep genesis review and voting windows visible to 30-minute heartbeat agents",
		"constitution":     "Genesis constitution text.",
	})
	if startResp.Code != http.StatusAccepted {
		t.Fatalf("genesis bootstrap start status=%d body=%s", startResp.Code, startResp.Body.String())
	}
	var started struct {
		State struct {
			ReviewWindowSeconds int `json:"review_window_seconds"`
			VoteWindowSeconds   int `json:"vote_window_seconds"`
		} `json:"state"`
		Proposal store.KBProposal `json:"proposal"`
	}
	if err := json.Unmarshal(startResp.Body.Bytes(), &started); err != nil {
		t.Fatalf("decode genesis bootstrap start response: %v", err)
	}
	if started.State.ReviewWindowSeconds != defaultGenesisReviewWindowSeconds {
		t.Fatalf("genesis review window default = %d, want %d", started.State.ReviewWindowSeconds, defaultGenesisReviewWindowSeconds)
	}
	if started.State.VoteWindowSeconds != defaultGenesisVoteWindowSeconds {
		t.Fatalf("genesis vote window default = %d, want %d", started.State.VoteWindowSeconds, defaultGenesisVoteWindowSeconds)
	}
	if started.Proposal.VoteWindowSeconds != defaultGenesisVoteWindowSeconds {
		t.Fatalf("genesis proposal vote window default = %d, want %d", started.Proposal.VoteWindowSeconds, defaultGenesisVoteWindowSeconds)
	}
}

func TestProposalWindowInputsMustStayWithinOneToTwelveHours(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)

	t.Run("create rejects too short windows", func(t *testing.T) {
		resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
			"title":                     "Too short proposal window",
			"reason":                    "bounds check",
			"vote_window_seconds":       1800,
			"discussion_window_seconds": 3600,
			"change": map[string]any{
				"op_type":     "add",
				"section":     "governance/runtime",
				"title":       "Too short proposal window",
				"new_content": "This should be rejected because the vote window is too short.",
				"diff_text":   "diff: reject vote windows shorter than one hour",
			},
		}, proposer.headers())
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("kb create short window status=%d body=%s", resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Body.String(), "vote_window_seconds must be between 3600 and 43200 seconds") {
			t.Fatalf("unexpected kb create short window error: %s", resp.Body.String())
		}
	})

	t.Run("create rejects too long windows", func(t *testing.T) {
		resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
			"title":                     "Too long proposal window",
			"reason":                    "bounds check",
			"vote_window_seconds":       3600,
			"discussion_window_seconds": 50000,
			"change": map[string]any{
				"op_type":     "add",
				"section":     "governance/runtime",
				"title":       "Too long proposal window",
				"new_content": "This should be rejected because the discussion window is too long.",
				"diff_text":   "diff: reject discussion windows longer than twelve hours",
			},
		}, proposer.headers())
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("kb create long window status=%d body=%s", resp.Code, resp.Body.String())
		}
		if !strings.Contains(resp.Body.String(), "discussion_window_seconds must be between 3600 and 43200 seconds") {
			t.Fatalf("unexpected kb create long window error: %s", resp.Body.String())
		}
	})
}

func TestProposalRevisionWindowInputsMustStayWithinOneToTwelveHours(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)

	createResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
		"title":  "Revision window bounds",
		"reason": "ensure revise keeps the same deadline bounds",
		"change": map[string]any{
			"op_type":     "add",
			"section":     "governance/runtime",
			"title":       "Revision window bounds",
			"new_content": "Proposal text for revise deadline testing.",
			"diff_text":   "diff: create proposal for revise deadline bounds",
		},
	}, proposer.headers())
	if createResp.Code != http.StatusAccepted {
		t.Fatalf("kb create status=%d body=%s", createResp.Code, createResp.Body.String())
	}
	var created struct {
		Proposal store.KBProposal `json:"proposal"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode kb create response: %v", err)
	}

	resp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/revise", map[string]any{
		"proposal_id":               created.Proposal.ID,
		"base_revision_id":          created.Proposal.CurrentRevisionID,
		"discussion_window_seconds": 1800,
		"change": map[string]any{
			"op_type":     "add",
			"section":     "governance/runtime",
			"title":       "Revision window bounds",
			"new_content": "Revised text with an invalid short deadline.",
			"diff_text":   "diff: reject revised discussion window shorter than one hour",
		},
	}, proposer.headers())
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("kb revise short window status=%d body=%s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "discussion_window_seconds must be between 3600 and 43200 seconds") {
		t.Fatalf("unexpected kb revise short window error: %s", resp.Body.String())
	}
}

func TestGenesisBootstrapWindowInputsMustStayWithinOneToTwelveHours(t *testing.T) {
	srv := newTestServer()
	proposer := newAuthUser(t, srv)

	shortResp := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/genesis/bootstrap/start", map[string]any{
		"proposer_user_id":      proposer.id,
		"title":                 "Genesis short window",
		"reason":                "bounds check",
		"constitution":          "Genesis constitution text.",
		"review_window_seconds": 1800,
	})
	if shortResp.Code != http.StatusBadRequest {
		t.Fatalf("genesis short window status=%d body=%s", shortResp.Code, shortResp.Body.String())
	}
	if !strings.Contains(shortResp.Body.String(), "review_window_seconds must be between 3600 and 43200 seconds") {
		t.Fatalf("unexpected genesis short window error: %s", shortResp.Body.String())
	}

	longResp := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/genesis/bootstrap/start", map[string]any{
		"proposer_user_id":    proposer.id,
		"title":               "Genesis long window",
		"reason":              "bounds check",
		"constitution":        "Genesis constitution text.",
		"vote_window_seconds": 50000,
	})
	if longResp.Code != http.StatusBadRequest {
		t.Fatalf("genesis long window status=%d body=%s", longResp.Code, longResp.Body.String())
	}
	if !strings.Contains(longResp.Body.String(), "vote_window_seconds must be between 3600 and 43200 seconds") {
		t.Fatalf("unexpected genesis long window error: %s", longResp.Body.String())
	}
}
