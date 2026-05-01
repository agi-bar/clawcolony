package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"clawcolony/internal/store"
)

const (
	obsoleteMailClassExactDuplicates  = "exact_duplicates"
	obsoleteMailClassFamilyDuplicates = "family_duplicates"
	obsoleteMailClassSystemStale72h   = "system_stale_72h"

	mailNoiseCleanupStateKey  = "mail_noise_cleanup_state"
	mailNoiseAutoReadWindow   = 72 * time.Hour
	mailNoiseReviewWindow     = 7 * 24 * time.Hour
	mailNoiseTaskMarketWindow = 24 * time.Hour
)

const (
	mailNoiseFamilySOSHibernating          = "sos_hibernating"
	mailNoiseFamilyWorldEvolutionAlert     = "world_evolution_alert"
	mailNoiseFamilyCollabDeadlineReminder  = "collab_deadline_reminder"
	mailNoiseFamilyCollabApply             = "collab_apply"
	mailNoiseFamilyTaskMarket              = "task_market"
	mailNoiseFamilyUpgradePR               = "upgrade_pr"
	mailNoiseFamilyCommunityCollabPinned   = "community_collab_pinned"
	mailNoiseFamilyAutonomyReport          = "autonomy_report_family"
	mailNoiseFamilyGovernanceParticipation = "governance_participation_report"
)

const (
	mailSuppressionReasonExactDuplicate  = "exact_duplicate"
	mailSuppressionReasonFamilyDuplicate = "family_duplicate"
)

var (
	mailNoiseCleanupUserBatchLimit = 100
	mailNoiseCleanupMailboxLimit   = 20000

	sosHibernatingSubjectPattern = regexp.MustCompile(`^\[SOS\]\[HIBERNATING\]\s+([^\s]+)\s+needs revival\b`)
	collabIDBodyPattern          = regexp.MustCompile(`(?m)^collab_id=([^\s]+)\s*$`)
	collabApplySubjectPattern    = regexp.MustCompile(`^\[COLLAB-APPLY\]\s+([^\s]+)\s+applied to\s+([^\s]+)\s+\(`)
	collabIDTextPattern          = regexp.MustCompile(`\bcollab_id=([^\s\]]+)`)
)

type mailSystemResolveObsoleteMailRequest = mailSystemResolveObsoleteKBRequest
type obsoleteMailCleanupUserResult = obsoleteKBMailCleanupUserResult
type obsoleteMailCleanupResult = obsoleteKBMailCleanupResult

type mailNoiseCleanupState struct {
	StartAfterUserID string `json:"start_after_user_id"`
	LastTickID       int64  `json:"last_tick_id,omitempty"`
}

type mailNoiseDescriptor struct {
	ExactKey       string
	FamilyClass    string
	FamilyKey      string
	AutoReadWindow time.Duration
}

type mailSendSuppression struct {
	Recipient         string
	Reason            string
	FamilyClass       string
	FamilyKey         string
	ExistingMailboxID int64
	ExistingMessageID int64
}

type plannedMailSend struct {
	Input      store.MailSendInput
	SentTo     []string
	Suppressed []mailSendSuppression
}

type mailSendOutcome struct {
	Result     store.MailSendResult
	SentTo     []string
	Suppressed []mailSendSuppression
}

func defaultMailNoiseCleanupClasses() []string {
	return []string{
		obsoleteMailClassExactDuplicates,
		obsoleteMailClassFamilyDuplicates,
		obsoleteMailClassSystemStale72h,
	}
}

func normalizeMailNoiseCleanupClasses(items []string) ([]string, error) {
	if len(items) == 0 {
		return defaultMailNoiseCleanupClasses(), nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	appendClass := func(class string) {
		if _, ok := seen[class]; ok {
			return
		}
		seen[class] = struct{}{}
		out = append(out, class)
	}
	for _, raw := range items {
		class := strings.TrimSpace(strings.ToLower(raw))
		switch class {
		case "":
			continue
		case obsoleteMailClassExactDuplicates, obsoleteMailClassFamilyDuplicates, obsoleteMailClassSystemStale72h:
			appendClass(class)
		default:
			return nil, fmt.Errorf("unsupported obsolete mail class: %s", raw)
		}
	}
	if len(out) == 0 {
		return defaultMailNoiseCleanupClasses(), nil
	}
	sort.SliceStable(out, func(i, j int) bool {
		order := func(class string) int {
			switch class {
			case obsoleteMailClassExactDuplicates:
				return 0
			case obsoleteMailClassFamilyDuplicates:
				return 1
			case obsoleteMailClassSystemStale72h:
				return 2
			default:
				return 99
			}
		}
		return order(out[i]) < order(out[j])
	})
	return out, nil
}

func mailNoiseCleanupClassesContain(classes []string, class string) bool {
	for _, item := range classes {
		if item == class {
			return true
		}
	}
	return false
}

func isMailSystemSender(userID string) bool {
	return isSystemRuntimeUserID(userID)
}

func hasAutonomyReportPrefix(subject string) bool {
	trimmed := strings.TrimSpace(subject)
	upper := strings.ToUpper(strings.TrimSpace(subject))
	return strings.HasPrefix(upper, "[AUTONOMY-LOOP]") ||
		strings.HasPrefix(upper, "[AUTONOMY-LOOP-REPORT]") ||
		strings.HasPrefix(upper, "[AUTONOMY-REPORT]") ||
		strings.HasPrefix(upper, "[AUTO-REPORT]") ||
		strings.HasPrefix(strings.ToLower(trimmed), "autonomy-loop/")
}

func isMailReportInboxOwner(owner string) bool {
	switch strings.ToLower(strings.TrimSpace(owner)) {
	case strings.ToLower(clawWorldSystemID), "clawcolony-assistant":
		return true
	default:
		return false
	}
}

func parseMailCollabID(body string) string {
	m := collabIDBodyPattern.FindStringSubmatch(body)
	if len(m) != 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func parseMailCollabApply(subject string) (string, string) {
	m := collabApplySubjectPattern.FindStringSubmatch(strings.TrimSpace(subject))
	if len(m) != 3 {
		return "", ""
	}
	return strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
}

func parseSOSHibernatingUserID(subject string) string {
	m := sosHibernatingSubjectPattern.FindStringSubmatch(strings.TrimSpace(subject))
	if len(m) != 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func parseMailCollabIDFromText(text string) string {
	m := collabIDTextPattern.FindStringSubmatch(strings.TrimSpace(text))
	if len(m) != 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

func mailFamilyKey(recipient string, parts ...string) string {
	all := make([]string, 0, len(parts)+1)
	all = append(all, recipient)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		all = append(all, part)
	}
	return strings.Join(all, "|")
}

func normalizeMailboxOwnerIDs(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, raw := range items {
		owner := strings.TrimSpace(raw)
		if owner == "" || strings.EqualFold(owner, clawTreasurySystemID) {
			continue
		}
		if _, ok := seen[owner]; ok {
			continue
		}
		seen[owner] = struct{}{}
		out = append(out, owner)
	}
	sort.Strings(out)
	return out
}

func buildMailExactKey(recipient, sender, subject, body string) string {
	sum := sha256.Sum256([]byte(body))
	return strings.Join([]string{
		strings.TrimSpace(recipient),
		strings.TrimSpace(sender),
		strings.TrimSpace(subject),
		hex.EncodeToString(sum[:]),
	}, "|")
}

func classifyMailNoise(recipient, sender, subject, body string) mailNoiseDescriptor {
	recipient = strings.TrimSpace(recipient)
	sender = strings.TrimSpace(sender)
	subject = strings.TrimSpace(subject)
	body = strings.TrimSpace(body)

	desc := mailNoiseDescriptor{
		ExactKey: buildMailExactKey(recipient, sender, subject, body),
	}
	upperSubject := strings.ToUpper(subject)
	isSystem := isMailSystemSender(sender)

	switch {
	case isSystem:
		if userID := parseSOSHibernatingUserID(subject); userID != "" {
			desc.FamilyClass = mailNoiseFamilySOSHibernating
			desc.FamilyKey = mailFamilyKey(recipient, userID)
			desc.AutoReadWindow = mailNoiseAutoReadWindow
			return desc
		}
		if strings.HasPrefix(upperSubject, "[WORLD-EVOLUTION-ALERT]") {
			desc.FamilyClass = mailNoiseFamilyWorldEvolutionAlert
			desc.FamilyKey = mailFamilyKey(recipient, "world_evolution_alert")
			desc.AutoReadWindow = mailNoiseAutoReadWindow
			return desc
		}
		if strings.HasPrefix(upperSubject, "[COLLAB][DEADLINE-REMINDER]") {
			if collabID := parseMailCollabID(body); collabID != "" {
				desc.FamilyClass = mailNoiseFamilyCollabDeadlineReminder
				desc.FamilyKey = mailFamilyKey(recipient, collabID)
				desc.AutoReadWindow = mailNoiseAutoReadWindow
				return desc
			}
		}
		if strings.HasPrefix(upperSubject, "[COLLAB-APPLY]") {
			applicantUserID, collabID := parseMailCollabApply(subject)
			if applicantUserID != "" && collabID != "" {
				desc.FamilyClass = mailNoiseFamilyCollabApply
				desc.FamilyKey = mailFamilyKey(recipient, applicantUserID, collabID)
				desc.AutoReadWindow = mailNoiseAutoReadWindow
				return desc
			}
		}
		if strings.HasPrefix(upperSubject, "[TASK-MARKET]") {
			desc.FamilyClass = mailNoiseFamilyTaskMarket
			desc.FamilyKey = mailFamilyKey(recipient, "task_market")
			desc.AutoReadWindow = mailNoiseTaskMarketWindow
			return desc
		}
		if strings.HasPrefix(upperSubject, "[UPGRADE-PR]") {
			if collabID := parseMailCollabIDFromText(subject); collabID != "" {
				desc.FamilyClass = mailNoiseFamilyUpgradePR
				desc.FamilyKey = mailFamilyKey(recipient, collabID)
				desc.AutoReadWindow = mailNoiseReviewWindow
				return desc
			}
		}
		if strings.HasPrefix(upperSubject, "[COMMUNITY-COLLAB]") && strings.Contains(upperSubject, "[PINNED]") {
			if collabID := parseMailCollabIDFromText(subject); collabID != "" {
				desc.FamilyClass = mailNoiseFamilyCommunityCollabPinned
				desc.FamilyKey = mailFamilyKey(recipient, collabID)
				desc.AutoReadWindow = mailNoiseReviewWindow
				return desc
			}
		}
	}
	if hasAutonomyReportPrefix(subject) {
		desc.FamilyClass = mailNoiseFamilyAutonomyReport
		desc.FamilyKey = mailFamilyKey(recipient, strings.ToLower(sender), "autonomy_report_family")
		if isMailReportInboxOwner(recipient) {
			desc.AutoReadWindow = mailNoiseReviewWindow
		}
		return desc
	}
	if strings.EqualFold(subject, "[COLLAB] Governance Participation") {
		desc.FamilyClass = mailNoiseFamilyGovernanceParticipation
		desc.FamilyKey = mailFamilyKey(recipient, strings.ToLower(sender), "governance_participation")
		if isMailReportInboxOwner(recipient) {
			desc.AutoReadWindow = mailNoiseReviewWindow
		}
		return desc
	}
	return desc
}

func (s *Server) findMailSuppression(ctx context.Context, recipient string, input store.MailSendInput) (*mailSendSuppression, error) {
	items, err := s.store.ListMailboxForCleanup(ctx, recipient, mailNoiseCleanupMailboxLimit)
	if err != nil {
		return nil, err
	}
	outgoing := classifyMailNoise(recipient, input.From, input.Subject, input.Body)
	var familyMatch *mailSendSuppression
	for _, item := range items {
		existing := classifyMailNoise(recipient, item.FromAddress, item.Subject, item.Body)
		if existing.ExactKey == outgoing.ExactKey {
			return &mailSendSuppression{
				Recipient:         recipient,
				Reason:            mailSuppressionReasonExactDuplicate,
				FamilyClass:       existing.FamilyClass,
				FamilyKey:         existing.FamilyKey,
				ExistingMailboxID: item.MailboxID,
				ExistingMessageID: item.MessageID,
			}, nil
		}
		if familyMatch != nil || outgoing.FamilyClass == "" || outgoing.FamilyKey == "" {
			continue
		}
		if existing.FamilyClass == outgoing.FamilyClass && existing.FamilyKey == outgoing.FamilyKey {
			familyMatch = &mailSendSuppression{
				Recipient:         recipient,
				Reason:            mailSuppressionReasonFamilyDuplicate,
				FamilyClass:       existing.FamilyClass,
				FamilyKey:         existing.FamilyKey,
				ExistingMailboxID: item.MailboxID,
				ExistingMessageID: item.MessageID,
			}
		}
	}
	return familyMatch, nil
}

func logMailSuppression(fromUserID string, suppression mailSendSuppression, subject string) {
	log.Printf(
		"mail_send_suppressed sender=%s recipient=%s subject=%q reason=%s class=%s family_key=%s existing_mailbox_id=%d existing_message_id=%d",
		strings.TrimSpace(fromUserID),
		strings.TrimSpace(suppression.Recipient),
		strings.TrimSpace(subject),
		suppression.Reason,
		suppression.FamilyClass,
		suppression.FamilyKey,
		suppression.ExistingMailboxID,
		suppression.ExistingMessageID,
	)
}

func (s *Server) planMailSendWithNoisePolicy(ctx context.Context, input store.MailSendInput) (plannedMailSend, error) {
	input.From = strings.TrimSpace(input.From)
	input.To = normalizeUniqueUsers(input.To)
	input.Subject = strings.TrimSpace(input.Subject)
	input.Body = strings.TrimSpace(input.Body)

	plan := plannedMailSend{
		Input:      input,
		SentTo:     make([]string, 0, len(input.To)),
		Suppressed: make([]mailSendSuppression, 0),
	}
	for _, recipient := range input.To {
		suppression, err := s.findMailSuppression(ctx, recipient, input)
		if err != nil {
			return plannedMailSend{}, err
		}
		if suppression != nil {
			logMailSuppression(input.From, *suppression, input.Subject)
			plan.Suppressed = append(plan.Suppressed, *suppression)
			continue
		}
		plan.SentTo = append(plan.SentTo, recipient)
	}
	plan.Input.To = append([]string(nil), plan.SentTo...)
	return plan, nil
}

func (s *Server) sendPlannedMailWithNoisePolicy(ctx context.Context, plan plannedMailSend) (mailSendOutcome, error) {
	outcome := mailSendOutcome{
		SentTo:     append([]string(nil), plan.SentTo...),
		Suppressed: append([]mailSendSuppression(nil), plan.Suppressed...),
		Result: store.MailSendResult{
			From:             plan.Input.From,
			To:               append([]string(nil), plan.SentTo...),
			Subject:          plan.Input.Subject,
			ReplyToMailboxID: plan.Input.ReplyToMailboxID,
			SentAt:           time.Now().UTC(),
		},
	}
	if len(plan.SentTo) == 0 {
		return outcome, nil
	}
	result, err := s.store.SendMail(ctx, plan.Input)
	if err != nil {
		return mailSendOutcome{}, err
	}
	s.pushUnreadMailHint(ctx, plan.Input.From, result.To, plan.Input.Subject)
	outcome.Result = result
	return outcome, nil
}

func (s *Server) sendMailWithNoisePolicy(ctx context.Context, input store.MailSendInput) (mailSendOutcome, error) {
	plan, err := s.planMailSendWithNoisePolicy(ctx, input)
	if err != nil {
		return mailSendOutcome{}, err
	}
	return s.sendPlannedMailWithNoisePolicy(ctx, plan)
}

func (o mailSendOutcome) suppressedExistingForRecipient(recipient string) (int64, int64, bool) {
	recipient = strings.TrimSpace(recipient)
	for _, item := range o.Suppressed {
		if strings.TrimSpace(item.Recipient) != recipient {
			continue
		}
		if item.ExistingMessageID <= 0 || item.ExistingMailboxID <= 0 {
			return 0, 0, false
		}
		return item.ExistingMessageID, item.ExistingMailboxID, true
	}
	return 0, 0, false
}

func (s *Server) obsoleteMailCleanupTargets(ctx context.Context, explicitUserIDs []string, startAfterUserID string, limit int) ([]string, bool, string, error) {
	targets := normalizeMailboxOwnerIDs(explicitUserIDs)
	if len(targets) == 0 {
		registrations, err := s.store.ListAgentRegistrations(ctx)
		if err != nil {
			return nil, false, "", err
		}
		derived := make([]string, 0, len(registrations))
		for _, reg := range registrations {
			derived = append(derived, reg.UserID)
		}
		unreadOwners, err := s.store.ListInboxOwnersWithUnread(ctx)
		if err != nil {
			return nil, false, "", err
		}
		derived = append(derived, unreadOwners...)
		targets = normalizeMailboxOwnerIDs(derived)
	}
	if len(targets) == 0 {
		return nil, false, "", nil
	}
	startAfterUserID = strings.TrimSpace(startAfterUserID)
	if startAfterUserID != "" {
		filtered := make([]string, 0, len(targets))
		for _, userID := range targets {
			if strings.Compare(userID, startAfterUserID) <= 0 {
				continue
			}
			filtered = append(filtered, userID)
		}
		targets = filtered
	}
	if limit <= 0 || len(targets) <= limit {
		return targets, false, "", nil
	}
	nextStart := targets[limit-1]
	return targets[:limit], true, nextStart, nil
}

func obsoleteMailboxIDsByExactDuplicate(items []store.MailItem) []int64 {
	seen := make(map[string]struct{}, len(items))
	ids := make([]int64, 0)
	for _, item := range items {
		desc := classifyMailNoise(item.OwnerAddress, item.FromAddress, item.Subject, item.Body)
		if desc.ExactKey == "" {
			continue
		}
		if _, ok := seen[desc.ExactKey]; ok {
			ids = append(ids, item.MailboxID)
			continue
		}
		seen[desc.ExactKey] = struct{}{}
	}
	return ids
}

func obsoleteMailboxIDsByFamilyDuplicate(items []store.MailItem) []int64 {
	seen := make(map[string]struct{}, len(items))
	ids := make([]int64, 0)
	for _, item := range items {
		desc := classifyMailNoise(item.OwnerAddress, item.FromAddress, item.Subject, item.Body)
		if desc.FamilyClass == "" || desc.FamilyKey == "" {
			continue
		}
		key := desc.FamilyClass + "|" + desc.FamilyKey
		if _, ok := seen[key]; ok {
			ids = append(ids, item.MailboxID)
			continue
		}
		seen[key] = struct{}{}
	}
	return ids
}

func obsoleteMailboxIDsBySystemStale(items []store.MailItem, now time.Time) []int64 {
	ids := make([]int64, 0)
	for _, item := range items {
		desc := classifyMailNoise(item.OwnerAddress, item.FromAddress, item.Subject, item.Body)
		if desc.AutoReadWindow <= 0 {
			continue
		}
		if item.SentAt.IsZero() || now.Sub(item.SentAt) < desc.AutoReadWindow {
			continue
		}
		ids = append(ids, item.MailboxID)
	}
	return ids
}

func uniqueMailboxIDs(items []int64) []int64 {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(items))
	out := make([]int64, 0, len(items))
	for _, id := range items {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func (s *Server) resolveObsoleteMailForUser(ctx context.Context, userID string, classes []string, dryRun bool, now time.Time) (obsoleteMailCleanupUserResult, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return obsoleteMailCleanupUserResult{}, nil
	}
	items, err := s.store.ListMailboxForCleanup(ctx, userID, mailNoiseCleanupMailboxLimit)
	if err != nil {
		return obsoleteMailCleanupUserResult{}, err
	}
	ids := make([]int64, 0, 16)
	if mailNoiseCleanupClassesContain(classes, obsoleteMailClassExactDuplicates) {
		ids = append(ids, obsoleteMailboxIDsByExactDuplicate(items)...)
	}
	if mailNoiseCleanupClassesContain(classes, obsoleteMailClassFamilyDuplicates) {
		ids = append(ids, obsoleteMailboxIDsByFamilyDuplicate(items)...)
	}
	if mailNoiseCleanupClassesContain(classes, obsoleteMailClassSystemStale72h) {
		ids = append(ids, obsoleteMailboxIDsBySystemStale(items, now)...)
	}
	ids = uniqueMailboxIDs(ids)
	if len(ids) > 0 && !dryRun {
		if err := s.store.MarkMailboxRead(ctx, userID, ids); err != nil {
			return obsoleteMailCleanupUserResult{}, err
		}
	}
	return obsoleteMailCleanupUserResult{
		UserID:               userID,
		ResolvedMailboxCount: len(ids),
	}, nil
}

func (s *Server) resolveObsoleteMailBatch(ctx context.Context, req mailSystemResolveObsoleteMailRequest) (obsoleteMailCleanupResult, error) {
	classes, err := normalizeMailNoiseCleanupClasses(req.Classes)
	if err != nil {
		return obsoleteMailCleanupResult{}, err
	}
	targets, hasMore, nextStartAfter, err := s.obsoleteMailCleanupTargets(ctx, req.UserIDs, req.StartAfterUserID, req.Limit)
	if err != nil {
		return obsoleteMailCleanupResult{}, err
	}
	now := time.Now().UTC()
	result := obsoleteMailCleanupResult{
		ScannedUserCount:     len(targets),
		AffectedUserCount:    0,
		ResolvedMailboxCount: 0,
		HasMore:              hasMore,
		NextStartAfterUserID: nextStartAfter,
		Users:                make([]obsoleteMailCleanupUserResult, 0, len(targets)),
	}
	for _, userID := range targets {
		userResult, err := s.resolveObsoleteMailForUser(ctx, userID, classes, req.DryRun, now)
		if err != nil {
			result.Users = append(result.Users, obsoleteMailCleanupUserResult{
				UserID: userID,
				Error:  err.Error(),
			})
			continue
		}
		if userResult.ResolvedMailboxCount == 0 {
			continue
		}
		result.AffectedUserCount++
		result.ResolvedMailboxCount += userResult.ResolvedMailboxCount
		result.Users = append(result.Users, userResult)
	}
	return result, nil
}

func (s *Server) runMailNoiseCleanupTick(ctx context.Context, tickID int64) error {
	state := mailNoiseCleanupState{}
	if found, _, err := s.getSettingJSON(ctx, mailNoiseCleanupStateKey, &state); err != nil {
		return err
	} else if !found {
		state = mailNoiseCleanupState{}
	}
	result, err := s.resolveObsoleteMailBatch(ctx, mailSystemResolveObsoleteMailRequest{
		DryRun:           false,
		Classes:          defaultMailNoiseCleanupClasses(),
		Limit:            mailNoiseCleanupUserBatchLimit,
		StartAfterUserID: state.StartAfterUserID,
	})
	if err != nil {
		return err
	}
	nextState := mailNoiseCleanupState{LastTickID: tickID}
	if result.HasMore {
		nextState.StartAfterUserID = result.NextStartAfterUserID
	}
	_, err = s.putSettingJSON(ctx, mailNoiseCleanupStateKey, nextState)
	return err
}
