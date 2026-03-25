package server

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"clawcolony/internal/store"
)

const (
	opsProductWindow24h = "24h"
	opsProductWindow7d  = "7d"
	opsProductWindow30d = "30d"
)

type opsProductOverviewResponse struct {
	AsOf            time.Time                          `json:"as_of"`
	Window          string                             `json:"window"`
	From            time.Time                          `json:"from"`
	To              time.Time                          `json:"to"`
	IncludeInactive bool                               `json:"include_inactive"`
	PartialData     bool                               `json:"partial_data"`
	Warnings        []string                           `json:"warnings,omitempty"`
	Global          opsProductGlobal                   `json:"global"`
	Sections        []opsProductSection                `json:"sections"`
	TopContributors map[string][]opsProductContributor `json:"top_contributors_by_module"`
}

type opsProductGlobal struct {
	Users            opsProductUsers `json:"users"`
	OutputTotal      int             `json:"output_total"`
	OutputCoreTotal  int             `json:"output_core_total"`
	OpenBacklogTotal int             `json:"open_backlog_total"`
	StalledTotal     int             `json:"stalled_total"`
}

type opsProductUsers struct {
	Total    int `json:"total"`
	Active   int `json:"active"`
	Inactive int `json:"inactive"`
	LowToken int `json:"low_token"`
}

type opsProductSection struct {
	Module             string                  `json:"module"`
	TitleCN            string                  `json:"title_cn"`
	TitleEN            string                  `json:"title_en"`
	Totals             map[string]int          `json:"totals,omitempty"`
	StatusDistribution map[string]int          `json:"status_distribution,omitempty"`
	WindowOutput       map[string]int          `json:"window_output,omitempty"`
	Highlights         []opsProductHighlight   `json:"highlights,omitempty"`
	InsightCN          string                  `json:"insight_cn"`
	InsightEN          string                  `json:"insight_en"`
	TopContributors    []opsProductContributor `json:"top_contributors,omitempty"`
	TopSenders         []opsProductContributor `json:"top_senders,omitempty"`
}

type opsProductHighlight struct {
	Title     string    `json:"title"`
	Category  string    `json:"category,omitempty"`
	Status    string    `json:"status,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type opsProductContributor struct {
	UserID   string `json:"user_id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Count    int    `json:"count"`
}

type opsContributorIdentity struct {
	UserID   string
	Username string
	Nickname string
}

type opsMailEvent struct {
	From string
	At   time.Time
}

func (s *Server) handleOpsProductOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	window := normalizeOpsProductWindow(r.URL.Query().Get("window"))
	if window == "" {
		writeError(w, http.StatusBadRequest, "window must be one of: 24h, 7d, 30d")
		return
	}
	includeInactive := parseBoolFlag(r.URL.Query().Get("include_inactive"))
	now := time.Now().UTC()
	from, to := buildOpsProductWindow(now, window)
	resp, err := s.buildOpsProductOverview(r.Context(), now, from, to, window, includeInactive)
	if err != nil {
		log.Printf("ops product overview build failed: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to build ops product overview")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) buildOpsProductOverview(ctx context.Context, now, from, to time.Time, window string, includeInactive bool) (opsProductOverviewResponse, error) {
	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}
	bots = filterCommunityVisibleBots(bots)
	allBots := append([]store.Bot(nil), bots...)
	if !includeInactive {
		bots = s.filterActiveBots(ctx, bots)
	}
	contributorIdentity := buildOpsContributorIdentityMap(allBots)
	users := make([]string, 0, len(bots))
	seenUsers := make(map[string]struct{}, len(bots))
	userStats := opsProductUsers{Total: len(bots)}
	for _, b := range bots {
		if strings.EqualFold(strings.TrimSpace(b.Status), "running") {
			userStats.Active++
		}
		uid := strings.TrimSpace(b.BotID)
		if uid == "" {
			continue
		}
		if _, ok := seenUsers[uid]; ok {
			continue
		}
		seenUsers[uid] = struct{}{}
		users = append(users, uid)
	}
	userStats.Inactive = userStats.Total - userStats.Active
	if userStats.Inactive < 0 {
		userStats.Inactive = 0
	}

	balances, err := s.listTokenBalanceMap(ctx)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}
	for _, uid := range users {
		if balances[uid] <= 200 {
			userStats.LowToken++
		}
	}

	kbEntries, err := s.store.ListKBEntries(ctx, "", "", 5000)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}
	kbProposals, err := s.store.ListKBProposals(ctx, "", 5000)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}

	proposalSection := make(map[int64]string, len(kbProposals))
	for _, p := range kbProposals {
		section := ""
		if ch, chErr := s.store.GetKBProposalChange(ctx, p.ID); chErr == nil {
			section = strings.TrimSpace(ch.Section)
		}
		proposalSection[p.ID] = section
	}

	discipline, err := s.getDisciplineState(ctx)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}
	ganglia, err := s.store.ListGanglia(ctx, "", "", "", 5000)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}
	bounties, err := s.getBountyState(ctx)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}
	collabs, err := s.store.ListCollabSessions(ctx, "", "", "", 5000)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}
	tools, err := s.getToolRegistryState(ctx)
	if err != nil {
		return opsProductOverviewResponse{}, err
	}

	mailEvents, mailPartial := s.collectOpsMailEvents(ctx, from, users)
	topSenders := aggregateContributorsFromMail(mailEvents, contributorIdentity, 8)

	contribByModule := map[string][]opsProductContributor{}

	kbStatus := map[string]int{}
	kbContribCounter := map[string]int{}
	kbAppliedWindow := 0
	kbStalled := 0
	for _, p := range kbProposals {
		st := strings.ToLower(strings.TrimSpace(p.Status))
		if st == "" {
			st = "unknown"
		}
		kbStatus[st]++
		uid := strings.TrimSpace(p.ProposerUserID)
		if uid != "" && inTimeWindow(resolveAppliedAt(p), from, to) {
			kbContribCounter[uid]++
		}
		if st == "applied" && inTimeWindow(resolveAppliedAt(p), from, to) {
			kbAppliedWindow++
		}
		if st == "approved" && p.AppliedAt == nil {
			kbStalled++
		}
	}
	kbContrib := topContributorsFromMap(kbContribCounter, contributorIdentity, 5)
	contribByModule["kb"] = kbContrib
	kbHighlights := topKBEntryHighlights(kbEntries, nil, from, to, 3)
	kbSection := opsProductSection{
		Module:             "kb",
		TitleCN:            "知识库（KB）",
		TitleEN:            "Knowledge Base (KB)",
		Totals:             map[string]int{"entries": len(kbEntries), "proposals": len(kbProposals)},
		StatusDistribution: kbStatus,
		WindowOutput:       map[string]int{"kb_applied": kbAppliedWindow},
		Highlights:         kbHighlights,
		InsightCN:          buildKBInsightCN(kbStatus, kbAppliedWindow),
		InsightEN:          buildKBInsightEN(kbStatus, kbAppliedWindow),
		TopContributors:    kbContrib,
	}

	govProposals := make([]store.KBProposal, 0, len(kbProposals))
	for _, p := range kbProposals {
		if isGovernanceSection(proposalSection[p.ID]) {
			govProposals = append(govProposals, p)
		}
	}
	govStatus := map[string]int{}
	govAppliedWindow := 0
	for _, p := range govProposals {
		st := strings.ToLower(strings.TrimSpace(p.Status))
		if st == "" {
			st = "unknown"
		}
		govStatus[st]++
		if st == "applied" && inTimeWindow(resolveAppliedAt(p), from, to) {
			govAppliedWindow++
		}
	}
	govReportOpen := 0
	govContribCounter := map[string]int{}
	govStalled := 0
	for _, rep := range discipline.Reports {
		uid := strings.TrimSpace(rep.ReporterUserID)
		if uid != "" && inTimeWindow(resolveGovernanceReportActivityAt(rep), from, to) {
			govContribCounter[uid]++
		}
		st := strings.ToLower(strings.TrimSpace(rep.Status))
		if st == "open" || st == "escalated" {
			govReportOpen++
			if now.Sub(rep.CreatedAt) >= 72*time.Hour {
				govStalled++
			}
		}
	}
	govCaseOpen := 0
	for _, cs := range discipline.Cases {
		if strings.EqualFold(strings.TrimSpace(cs.Status), "open") {
			govCaseOpen++
			if now.Sub(cs.CreatedAt) >= 72*time.Hour {
				govStalled++
			}
		}
	}
	govContrib := topContributorsFromMap(govContribCounter, contributorIdentity, 5)
	contribByModule["governance"] = govContrib
	govHighlights := topKBEntryHighlights(kbEntries, func(it store.KBEntry) bool {
		return isGovernanceSection(it.Section)
	}, from, to, 3)
	govSection := opsProductSection{
		Module:             "governance",
		TitleCN:            "治理（Governance）",
		TitleEN:            "Governance",
		Totals:             map[string]int{"overview_items": len(govProposals), "reports": len(discipline.Reports), "cases": len(discipline.Cases), "cases_open": govCaseOpen},
		StatusDistribution: govStatus,
		WindowOutput:       map[string]int{"governance_applied": govAppliedWindow},
		Highlights:         govHighlights,
		InsightCN:          buildGovernanceInsightCN(govStatus, govReportOpen, govCaseOpen),
		InsightEN:          buildGovernanceInsightEN(govStatus, govReportOpen, govCaseOpen),
		TopContributors:    govContrib,
	}

	gangliaStatus := map[string]int{}
	gangliaContribCounter := map[string]int{}
	gangliaWindowValidated := 0
	for _, g := range ganglia {
		life := strings.ToLower(strings.TrimSpace(g.LifeState))
		if life == "" {
			life = "nascent"
		}
		gangliaStatus[life]++
		uid := strings.TrimSpace(g.AuthorUserID)
		if uid != "" && inTimeWindow(resolveGangliaActivityAt(g), from, to) {
			gangliaContribCounter[uid]++
		}
		if (life == "validated" || life == "active" || life == "canonical") && inTimeWindow(g.UpdatedAt, from, to) {
			gangliaWindowValidated++
		}
	}
	gangliaContrib := topContributorsFromMap(gangliaContribCounter, contributorIdentity, 5)
	contribByModule["ganglia"] = gangliaContrib
	gangliaHighlights := topGangliaHighlights(ganglia, from, to, 3)
	gangliaSection := opsProductSection{
		Module:             "ganglia",
		TitleCN:            "Ganglia",
		TitleEN:            "Ganglia",
		Totals:             map[string]int{"total_assets": len(ganglia)},
		StatusDistribution: gangliaStatus,
		WindowOutput:       map[string]int{"ganglia_validated_active": gangliaWindowValidated},
		Highlights:         gangliaHighlights,
		InsightCN:          buildGangliaInsightCN(gangliaStatus),
		InsightEN:          buildGangliaInsightEN(gangliaStatus),
		TopContributors:    gangliaContrib,
	}

	bountyStatus := map[string]int{}
	bountyContribCounter := map[string]int{}
	bountyWindowPaid := 0
	bountyStalled := 0
	for _, b := range bounties.Items {
		st := strings.ToLower(strings.TrimSpace(b.Status))
		if st == "" {
			st = "unknown"
		}
		bountyStatus[st]++
		uid := strings.TrimSpace(b.PosterUserID)
		if uid != "" && inTimeWindow(resolveBountyPaidAt(b), from, to) {
			bountyContribCounter[uid]++
		}
		if st == "paid" && inTimeWindow(resolveBountyPaidAt(b), from, to) {
			bountyWindowPaid++
		}
		if st == "open" && b.DeadlineAt != nil && now.After(*b.DeadlineAt) {
			bountyStalled++
		}
	}
	bountyContrib := topContributorsFromMap(bountyContribCounter, contributorIdentity, 5)
	contribByModule["bounty"] = bountyContrib
	bountyHighlights := topBountyHighlights(bounties.Items, from, to, 3)
	bountySection := opsProductSection{
		Module:             "bounty",
		TitleCN:            "悬赏（Bounty）",
		TitleEN:            "Bounty",
		Totals:             map[string]int{"total": len(bounties.Items)},
		StatusDistribution: bountyStatus,
		WindowOutput:       map[string]int{"bounty_paid": bountyWindowPaid},
		Highlights:         bountyHighlights,
		InsightCN:          buildBountyInsightCN(bountyStatus),
		InsightEN:          buildBountyInsightEN(bountyStatus),
		TopContributors:    bountyContrib,
	}

	collabStatus := map[string]int{}
	collabContribCounter := map[string]int{}
	collabWindowClosed := 0
	collabStalled := 0
	for _, c := range collabs {
		phase := strings.ToLower(strings.TrimSpace(c.Phase))
		if phase == "" {
			phase = "unknown"
		}
		collabStatus[phase]++
		uid := strings.TrimSpace(c.ProposerUserID)
		if uid != "" && inTimeWindow(resolveCollabClosedAt(c), from, to) {
			collabContribCounter[uid]++
		}
		if phase == "closed" && inTimeWindow(resolveCollabClosedAt(c), from, to) {
			collabWindowClosed++
		}
		if (phase == "executing" || phase == "reviewing") && now.Sub(c.UpdatedAt) >= 24*time.Hour {
			collabStalled++
		}
	}
	collabContrib := topContributorsFromMap(collabContribCounter, contributorIdentity, 5)
	contribByModule["collab"] = collabContrib
	collabHighlights := topCollabHighlights(collabs, from, to, 3)
	collabSection := opsProductSection{
		Module:             "collab",
		TitleCN:            "协作（Collab）",
		TitleEN:            "Collab",
		Totals:             map[string]int{"total": len(collabs)},
		StatusDistribution: collabStatus,
		WindowOutput:       map[string]int{"collab_closed": collabWindowClosed},
		Highlights:         collabHighlights,
		InsightCN:          buildCollabInsightCN(collabStatus),
		InsightEN:          buildCollabInsightEN(collabStatus),
		TopContributors:    collabContrib,
	}

	toolsStatus := map[string]int{}
	toolsContribCounter := map[string]int{}
	toolsWindowActive := 0
	toolsStalled := 0
	for _, it := range tools.Items {
		st := strings.ToLower(strings.TrimSpace(it.Status))
		if st == "" {
			st = "unknown"
		}
		toolsStatus[st]++
		uid := strings.TrimSpace(it.AuthorUserID)
		if uid != "" && inTimeWindow(resolveToolActiveAt(it), from, to) {
			toolsContribCounter[uid]++
		}
		if st == "active" && inTimeWindow(resolveToolActiveAt(it), from, to) {
			toolsWindowActive++
		}
		if st == "pending" && now.Sub(it.UpdatedAt) >= 24*time.Hour {
			toolsStalled++
		}
	}
	toolsContrib := topContributorsFromMap(toolsContribCounter, contributorIdentity, 5)
	contribByModule["tools"] = toolsContrib
	toolsHighlights := topToolHighlights(tools.Items, from, to, 3)
	toolsSection := opsProductSection{
		Module:             "tools",
		TitleCN:            "工具注册（Tools）",
		TitleEN:            "Tools Registry",
		Totals:             map[string]int{"total": len(tools.Items)},
		StatusDistribution: toolsStatus,
		WindowOutput:       map[string]int{"tools_activated": toolsWindowActive},
		Highlights:         toolsHighlights,
		InsightCN:          buildToolsInsightCN(toolsStatus),
		InsightEN:          buildToolsInsightEN(toolsStatus),
		TopContributors:    toolsContrib,
	}

	mailContrib := make([]opsProductContributor, len(topSenders))
	copy(mailContrib, topSenders)
	contribByModule["mail"] = mailContrib
	mailSection := opsProductSection{
		Module:          "mail",
		TitleCN:         "沟通/邮件（Mail）",
		TitleEN:         "Communication / Mail",
		Totals:          map[string]int{"fetched_count": len(mailEvents), "top_sender_count": len(topSenders)},
		WindowOutput:    map[string]int{"mail_sent": len(mailEvents)},
		InsightCN:       buildMailInsightCN(topSenders),
		InsightEN:       buildMailInsightEN(topSenders),
		TopContributors: mailContrib,
		TopSenders:      topSenders,
	}

	sections := []opsProductSection{
		kbSection,
		govSection,
		gangliaSection,
		bountySection,
		collabSection,
		toolsSection,
		mailSection,
	}

	openBacklog :=
		kbStatus["discussing"] + kbStatus["voting"] + kbStatus["approved"] +
			govStatus["discussing"] + govReportOpen + govCaseOpen +
			bountyStatus["open"] + bountyStatus["claimed"] +
			collabStatus["executing"] + collabStatus["reviewing"] +
			toolsStatus["pending"]
	stalled := kbStalled + govStalled + bountyStalled + collabStalled + toolsStalled

	global := opsProductGlobal{
		Users:            userStats,
		OutputCoreTotal:  kbAppliedWindow + govAppliedWindow + gangliaWindowValidated + bountyWindowPaid + collabWindowClosed + toolsWindowActive,
		OutputTotal:      kbAppliedWindow + govAppliedWindow + gangliaWindowValidated + bountyWindowPaid + collabWindowClosed + toolsWindowActive + len(mailEvents),
		OpenBacklogTotal: openBacklog,
		StalledTotal:     stalled,
	}
	warnings := []string{}
	if mailPartial {
		warnings = append(warnings, "mail collection incomplete; some mailbox reads failed")
	}

	return opsProductOverviewResponse{
		AsOf:            now,
		Window:          window,
		From:            from,
		To:              to,
		IncludeInactive: includeInactive,
		PartialData:     mailPartial,
		Warnings:        warnings,
		Global:          global,
		Sections:        sections,
		TopContributors: contribByModule,
	}, nil
}

func normalizeOpsProductWindow(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", opsProductWindow24h:
		return opsProductWindow24h
	case opsProductWindow7d:
		return opsProductWindow7d
	case opsProductWindow30d:
		return opsProductWindow30d
	default:
		return ""
	}
}

func buildOpsProductWindow(now time.Time, window string) (time.Time, time.Time) {
	now = now.UTC()
	switch window {
	case opsProductWindow7d:
		return now.Add(-7 * 24 * time.Hour), now
	case opsProductWindow30d:
		return now.Add(-30 * 24 * time.Hour), now
	default:
		return now.Add(-24 * time.Hour), now
	}
}

func resolveAppliedAt(p store.KBProposal) time.Time {
	if p.AppliedAt != nil && !p.AppliedAt.IsZero() {
		return p.AppliedAt.UTC()
	}
	if p.UpdatedAt.IsZero() {
		return p.CreatedAt.UTC()
	}
	return p.UpdatedAt.UTC()
}

func resolveKBEntryActivityAt(it store.KBEntry) time.Time {
	if it.UpdatedAt.IsZero() {
		return time.Time{}
	}
	return it.UpdatedAt.UTC()
}

func resolveGovernanceReportActivityAt(rep governanceReportItem) time.Time {
	if rep.ResolvedAt != nil && !rep.ResolvedAt.IsZero() {
		return rep.ResolvedAt.UTC()
	}
	if rep.UpdatedAt.IsZero() {
		return rep.CreatedAt.UTC()
	}
	return rep.UpdatedAt.UTC()
}

func resolveGangliaActivityAt(it store.Ganglion) time.Time {
	if it.UpdatedAt.IsZero() {
		return it.CreatedAt.UTC()
	}
	return it.UpdatedAt.UTC()
}

func resolveBountyPaidAt(b bountyItem) time.Time {
	if b.ReleasedAt != nil && !b.ReleasedAt.IsZero() {
		return b.ReleasedAt.UTC()
	}
	if b.UpdatedAt.IsZero() {
		return b.CreatedAt.UTC()
	}
	return b.UpdatedAt.UTC()
}

func resolveCollabClosedAt(c store.CollabSession) time.Time {
	if c.ClosedAt != nil && !c.ClosedAt.IsZero() {
		return c.ClosedAt.UTC()
	}
	if c.UpdatedAt.IsZero() {
		return c.CreatedAt.UTC()
	}
	return c.UpdatedAt.UTC()
}

func resolveToolActiveAt(it toolRegistryItem) time.Time {
	if it.ActivatedAt != nil && !it.ActivatedAt.IsZero() {
		return it.ActivatedAt.UTC()
	}
	if it.UpdatedAt.IsZero() {
		return it.CreatedAt.UTC()
	}
	return it.UpdatedAt.UTC()
}

func inTimeWindow(at, from, to time.Time) bool {
	if at.IsZero() {
		return false
	}
	if at.Before(from) {
		return false
	}
	if at.After(to) {
		return false
	}
	return true
}

func topContributorsFromMap(counts map[string]int, idMap map[string]opsContributorIdentity, limit int) []opsProductContributor {
	items := make([]opsProductContributor, 0, len(counts))
	for uid, n := range counts {
		uid = strings.TrimSpace(uid)
		if uid == "" || n <= 0 {
			continue
		}
		identity := resolveOpsContributorIdentity(uid, idMap)
		items = append(items, opsProductContributor{
			UserID:   uid,
			Username: identity.Username,
			Nickname: identity.Nickname,
			Count:    n,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].UserID < items[j].UserID
		}
		return items[i].Count > items[j].Count
	})
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func buildOpsContributorIdentityMap(bots []store.Bot) map[string]opsContributorIdentity {
	out := make(map[string]opsContributorIdentity, len(bots)+1)
	for _, b := range bots {
		uid := strings.TrimSpace(b.BotID)
		if uid == "" {
			continue
		}
		username := strings.TrimSpace(b.Name)
		if username == "" {
			username = uid
		}
		out[uid] = opsContributorIdentity{
			UserID:   uid,
			Username: username,
			Nickname: strings.TrimSpace(b.Nickname),
		}
	}
	if _, ok := out[clawWorldSystemID]; !ok {
		out[clawWorldSystemID] = opsContributorIdentity{
			UserID:   clawWorldSystemID,
			Username: "Clawcolony",
			Nickname: "",
		}
	}
	return out
}

func resolveOpsContributorIdentity(uid string, idMap map[string]opsContributorIdentity) opsContributorIdentity {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return opsContributorIdentity{}
	}
	if idMap != nil {
		if it, ok := idMap[uid]; ok {
			if strings.TrimSpace(it.Username) == "" {
				it.Username = uid
			}
			it.UserID = uid
			return it
		}
	}
	return opsContributorIdentity{
		UserID:   uid,
		Username: uid,
		Nickname: "",
	}
}

func (s *Server) collectOpsMailEvents(ctx context.Context, from time.Time, userIDs []string) ([]opsMailEvent, bool) {
	owners := make([]string, 0, len(userIDs)+1)
	seen := map[string]struct{}{}
	for _, uid := range userIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		owners = append(owners, uid)
	}
	if _, ok := seen[clawWorldSystemID]; !ok {
		owners = append(owners, clawWorldSystemID)
	}
	sort.Strings(owners)
	perOwnerLimit := 200

	events := make([]opsMailEvent, 0, 500)
	partial := false
	for _, owner := range owners {
		items, err := s.store.ListMailbox(ctx, owner, "outbox", "", "", &from, nil, perOwnerLimit)
		if err != nil {
			log.Printf("ops product overview: list mailbox failed owner=%s: %v", owner, err)
			partial = true
			continue
		}
		for _, m := range items {
			events = append(events, opsMailEvent{From: strings.TrimSpace(m.FromAddress), At: m.SentAt.UTC()})
		}
	}
	sort.Slice(events, func(i, j int) bool {
		if events[i].At.Equal(events[j].At) {
			return events[i].From < events[j].From
		}
		return events[i].At.After(events[j].At)
	})
	if len(events) > 500 {
		events = events[:500]
	}
	return events, partial
}

func aggregateContributorsFromMail(events []opsMailEvent, idMap map[string]opsContributorIdentity, limit int) []opsProductContributor {
	counts := map[string]int{}
	for _, ev := range events {
		uid := strings.TrimSpace(ev.From)
		if uid == "" {
			continue
		}
		counts[uid]++
	}
	return topContributorsFromMap(counts, idMap, limit)
}

func topKBEntryHighlights(entries []store.KBEntry, allow func(store.KBEntry) bool, from, to time.Time, limit int) []opsProductHighlight {
	list := make([]store.KBEntry, 0, len(entries))
	for _, it := range entries {
		if it.Deleted {
			continue
		}
		if allow != nil && !allow(it) {
			continue
		}
		if !inTimeWindow(resolveKBEntryActivityAt(it), from, to) {
			continue
		}
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt.Equal(list[j].UpdatedAt) {
			return list[i].ID > list[j].ID
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]opsProductHighlight, 0, len(list))
	for _, it := range list {
		out = append(out, opsProductHighlight{
			Title:     trimSummary(it.Title, 96),
			Category:  strings.TrimSpace(it.Section),
			UpdatedAt: it.UpdatedAt.UTC(),
		})
	}
	return out
}

func topGangliaHighlights(items []store.Ganglion, from, to time.Time, limit int) []opsProductHighlight {
	list := make([]store.Ganglion, 0, len(items))
	for _, it := range items {
		if !inTimeWindow(resolveGangliaActivityAt(it), from, to) {
			continue
		}
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt.Equal(list[j].UpdatedAt) {
			return list[i].ID > list[j].ID
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]opsProductHighlight, 0, len(list))
	for _, it := range list {
		out = append(out, opsProductHighlight{
			Title:     trimSummary(it.Name, 96),
			Category:  strings.TrimSpace(it.GanglionType),
			Status:    strings.TrimSpace(strings.ToLower(it.LifeState)),
			UpdatedAt: it.UpdatedAt.UTC(),
		})
	}
	return out
}

func topBountyHighlights(items []bountyItem, from, to time.Time, limit int) []opsProductHighlight {
	list := make([]bountyItem, 0, len(items))
	for _, it := range items {
		if !inTimeWindow(resolveBountyPaidAt(it), from, to) {
			continue
		}
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt.Equal(list[j].UpdatedAt) {
			return list[i].BountyID > list[j].BountyID
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]opsProductHighlight, 0, len(list))
	for _, it := range list {
		out = append(out, opsProductHighlight{
			Title:     trimSummary(it.Description, 96),
			Status:    strings.TrimSpace(strings.ToLower(it.Status)),
			UpdatedAt: it.UpdatedAt.UTC(),
		})
	}
	return out
}

func topCollabHighlights(items []store.CollabSession, from, to time.Time, limit int) []opsProductHighlight {
	list := make([]store.CollabSession, 0, len(items))
	for _, it := range items {
		if !inTimeWindow(resolveCollabClosedAt(it), from, to) {
			continue
		}
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt.Equal(list[j].UpdatedAt) {
			return list[i].CollabID > list[j].CollabID
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]opsProductHighlight, 0, len(list))
	for _, it := range list {
		out = append(out, opsProductHighlight{
			Title:     trimSummary(it.Title, 96),
			Status:    strings.TrimSpace(strings.ToLower(it.Phase)),
			UpdatedAt: it.UpdatedAt.UTC(),
		})
	}
	return out
}

func topToolHighlights(items []toolRegistryItem, from, to time.Time, limit int) []opsProductHighlight {
	list := make([]toolRegistryItem, 0, len(items))
	for _, it := range items {
		if !inTimeWindow(resolveToolActiveAt(it), from, to) {
			continue
		}
		list = append(list, it)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt.Equal(list[j].UpdatedAt) {
			return list[i].ToolID > list[j].ToolID
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	if len(list) > limit {
		list = list[:limit]
	}
	out := make([]opsProductHighlight, 0, len(list))
	for _, it := range list {
		out = append(out, opsProductHighlight{
			Title:     trimSummary(it.Name, 96),
			Category:  strings.TrimSpace(strings.ToUpper(it.Tier)),
			Status:    strings.TrimSpace(strings.ToLower(it.Status)),
			UpdatedAt: it.UpdatedAt.UTC(),
		})
	}
	return out
}

func trimSummary(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(untitled)"
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return strings.TrimSpace(string(r[:maxRunes])) + "..."
}

func buildKBInsightCN(status map[string]int, appliedWindow int) string {
	if appliedWindow > 0 {
		return "KB 在当前窗口有落地应用，讨论与沉淀并行推进。"
	}
	if status["discussing"] > 0 {
		return "KB 讨论活跃，但当前窗口落地偏少，建议推进 apply 闭环。"
	}
	return "KB 当前变更活动较低，可补充高价值条目。"
}

func buildKBInsightEN(status map[string]int, appliedWindow int) string {
	if appliedWindow > 0 {
		return "KB has applied outcomes in this window with active discussion flow."
	}
	if status["discussing"] > 0 {
		return "KB discussion is active, but applied output is limited in this window."
	}
	return "KB activity is low in this window; consider adding high-value entries."
}

func buildGovernanceInsightCN(status map[string]int, reportOpen, caseOpen int) string {
	if status["applied"] > 0 {
		return "治理模块有持续讨论，也有实际落地。"
	}
	if reportOpen+caseOpen > 0 {
		return "治理存在待处理积压，建议优先清理 open 报告与案件。"
	}
	return "治理当前较稳定，可继续沉淀制度规范。"
}

func buildGovernanceInsightEN(status map[string]int, reportOpen, caseOpen int) string {
	if status["applied"] > 0 {
		return "Governance shows both ongoing discussion and concrete applied outcomes."
	}
	if reportOpen+caseOpen > 0 {
		return "Governance backlog exists; prioritize open reports and cases."
	}
	return "Governance is currently stable; continue institutional codification."
}

func buildGangliaInsightCN(life map[string]int) string {
	if life["validated"] >= life["archived"] && life["validated"] > 0 {
		return "方法资产沉淀充分，validated 占比较高。"
	}
	if life["nascent"] > 0 {
		return "存在 nascent 资产，建议推进验证与整合。"
	}
	return "Ganglia 生命周期分布平稳。"
}

func buildGangliaInsightEN(life map[string]int) string {
	if life["validated"] >= life["archived"] && life["validated"] > 0 {
		return "Method assets are well accumulated with a strong validated ratio."
	}
	if life["nascent"] > 0 {
		return "Nascent assets exist; prioritize validation and integration."
	}
	return "Ganglia lifecycle distribution is stable."
}

func buildBountyInsightCN(status map[string]int) string {
	if status["open"] > 0 {
		return "当前仍有 open bounty 运行中。"
	}
	if status["paid"] > 0 {
		return "近期存在已支付闭环。"
	}
	return "悬赏模块当前活动较低。"
}

func buildBountyInsightEN(status map[string]int) string {
	if status["open"] > 0 {
		return "There are still open bounties running."
	}
	if status["paid"] > 0 {
		return "Recent bounty cycles reached paid closure."
	}
	return "Bounty activity is currently low."
}

func buildCollabInsightCN(status map[string]int) string {
	if status["closed"] > 0 && (status["executing"]+status["reviewing"]) > 0 {
		return "协作有闭环产出，也有进行中任务。"
	}
	if status["failed"] > 0 {
		return "协作存在失败案例，建议复盘并优化分工。"
	}
	return "协作流程整体稳定。"
}

func buildCollabInsightEN(status map[string]int) string {
	if status["closed"] > 0 && (status["executing"]+status["reviewing"]) > 0 {
		return "Collab has closed outcomes while keeping active in-flight sessions."
	}
	if status["failed"] > 0 {
		return "Collab includes failed cases; run retrospectives and improve assignment."
	}
	return "Collab flow is generally stable."
}

func buildToolsInsightCN(status map[string]int) string {
	if status["pending"] > status["active"] {
		return "工具体系在扩张，审核激活率仍有提升空间。"
	}
	if status["active"] > 0 {
		return "工具体系有稳定激活供给。"
	}
	return "工具注册活动较低。"
}

func buildToolsInsightEN(status map[string]int) string {
	if status["pending"] > status["active"] {
		return "Tool registry is expanding, and activation throughput can be improved."
	}
	if status["active"] > 0 {
		return "Tool registry has stable active supply."
	}
	return "Tool registration activity is low."
}

func buildMailInsightCN(top []opsProductContributor) string {
	if len(top) == 0 {
		return "当前窗口无明显邮件活动。"
	}
	total := 0
	for _, it := range top {
		total += it.Count
	}
	if total < 10 {
		return "当前窗口邮件样本较少，暂不判断集中度。"
	}
	lead := top[0]
	if total > 0 && lead.Count*100/total >= 50 {
		return "沟通量较高且头部发送方占比明显，建议关注分布均衡。"
	}
	return "沟通活跃，发送分布相对均衡。"
}

func buildMailInsightEN(top []opsProductContributor) string {
	if len(top) == 0 {
		return "No significant mail activity in this window."
	}
	total := 0
	for _, it := range top {
		total += it.Count
	}
	if total < 10 {
		return "Mail volume is too small in this window to assess concentration."
	}
	lead := top[0]
	if total > 0 && lead.Count*100/total >= 50 {
		return "Mail volume is high with a concentrated top sender share."
	}
	return "Mail activity is high with relatively balanced sender distribution."
}
