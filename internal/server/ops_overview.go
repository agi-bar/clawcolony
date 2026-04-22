package server

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"clawcolony/internal/store"
)

const (
	opsDefaultLimit   = 200
	opsMaxLimit       = 2000
	opsWindow24h      = "24h"
	opsWindow7d       = "7d"
	opsWindowBoth     = "both"
	opsDefaultOwnerID = clawWorldSystemID
)

type opsWindowDef struct {
	Key  string
	From time.Time
	To   time.Time
}

type opsOverviewResponse struct {
	AsOf             time.Time                  `json:"as_of"`
	Window           string                     `json:"window"`
	IncludeInactive  bool                       `json:"include_inactive"`
	Limit            int                        `json:"limit"`
	Snapshot         opsSnapshot                `json:"snapshot"`
	Windows          map[string]opsWindowReport `json:"windows"`
	ModuleStatusKeys map[string][]string        `json:"module_status_keys"`
	Hints            map[string]any             `json:"hints,omitempty"`
}

type opsSnapshot struct {
	Users           opsUsersSnapshot          `json:"users"`
	ModuleStatus    map[string]map[string]int `json:"module_status"`
	OpenRiskCount   int                       `json:"open_risk_count"`
	OpenActionCount int                       `json:"open_action_count"`
}

type opsUsersSnapshot struct {
	Total       int            `json:"total"`
	Active      int            `json:"active"`
	Inactive    int            `json:"inactive"`
	LowToken    int            `json:"low_token"`
	ByStatus    map[string]int `json:"by_status"`
	ByLifeState map[string]int `json:"by_life_state"`
}

type opsWindowReport struct {
	From            time.Time                   `json:"from"`
	To              time.Time                   `json:"to"`
	OutputTotal     int                         `json:"output_total"`
	OutputByKind    map[string]int              `json:"output_by_kind"`
	RiskCount       int                         `json:"risk_count"`
	RiskByType      map[string]int              `json:"risk_by_type"`
	ActionCount     int                         `json:"action_count"`
	ActionByType    map[string]int              `json:"action_by_type"`
	ModuleOutput    map[string]int              `json:"module_output"`
	ModuleRisk      map[string]int              `json:"module_risk"`
	ModuleAction    map[string]int              `json:"module_action"`
	ModuleStatus    map[string]map[string]int   `json:"module_status"`
	TopContributors map[string][]opsContributor `json:"top_contributors"`
	Risks           []opsRiskItem               `json:"risks"`
	Actions         []opsActionItem             `json:"actions"`
	Ownership       []opsOwnerAction            `json:"ownership"`
}

type opsContributor struct {
	UserID string `json:"user_id"`
	Count  int    `json:"count"`
}

type opsRiskItem struct {
	ID          string    `json:"id"`
	Module      string    `json:"module"`
	Type        string    `json:"type"`
	Severity    string    `json:"severity"`
	OwnerUserID string    `json:"owner_user_id"`
	ItemID      string    `json:"item_id"`
	Summary     string    `json:"summary"`
	Since       time.Time `json:"since"`
	AgeHours    int64     `json:"age_hours"`
}

type opsActionItem struct {
	ID          string    `json:"id"`
	Module      string    `json:"module"`
	Type        string    `json:"type"`
	Priority    string    `json:"priority"`
	OwnerUserID string    `json:"owner_user_id"`
	ItemID      string    `json:"item_id"`
	Summary     string    `json:"summary"`
	Since       time.Time `json:"since"`
	AgeHours    int64     `json:"age_hours"`
}

type opsOwnerAction struct {
	UserID string `json:"user_id"`
	P1     int    `json:"p1"`
	P2     int    `json:"p2"`
	P3     int    `json:"p3"`
	Total  int    `json:"total"`
}

type opsOutputEvent struct {
	Module string
	Kind   string
	Owner  string
	At     time.Time
}

func (s *Server) handleOpsOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	includeInactive := parseBoolFlag(r.URL.Query().Get("include_inactive"))
	limit := parseLimit(r.URL.Query().Get("limit"), opsDefaultLimit)
	if limit > opsMaxLimit {
		limit = opsMaxLimit
	}
	window := normalizeOpsWindow(r.URL.Query().Get("window"))
	if window == "" {
		writeError(w, http.StatusBadRequest, "window must be one of: 24h, 7d, both")
		return
	}
	now := time.Now().UTC()
	windowDefs := buildOpsWindows(now, window)
	resp, err := s.buildOpsOverview(r.Context(), now, window, windowDefs, includeInactive, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) buildOpsOverview(ctx context.Context, now time.Time, window string, windows []opsWindowDef, includeInactive bool, limit int) (opsOverviewResponse, error) {
	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return opsOverviewResponse{}, err
	}
	bots = filterCommunityVisibleBots(bots)
	if !includeInactive {
		bots = s.filterActiveBots(ctx, bots)
	}
	sort.Slice(bots, func(i, j int) bool { return strings.TrimSpace(bots[i].BotID) < strings.TrimSpace(bots[j].BotID) })

	users := make(map[string]store.Bot, len(bots))
	userIDs := make([]string, 0, len(bots))
	for _, b := range bots {
		uid := strings.TrimSpace(b.BotID)
		if uid == "" {
			continue
		}
		users[uid] = b
		userIDs = append(userIDs, uid)
	}

	balances, err := s.listTokenBalanceMap(ctx)
	if err != nil {
		return opsOverviewResponse{}, err
	}
	lifeStates, _ := s.store.ListUserLifeStates(ctx, "", "", maxInt(500, len(users)*4))
	lifeMap := make(map[string]string, len(lifeStates))
	for _, it := range lifeStates {
		uid := strings.TrimSpace(it.UserID)
		if uid == "" {
			continue
		}
		lifeMap[uid] = normalizeLifeStateForServer(it.State)
	}

	sourceLimit := opsScanLimit(limit)
	minFrom := windows[0].From
	for _, win := range windows[1:] {
		if win.From.Before(minFrom) {
			minFrom = win.From
		}
	}

	moduleStatus := map[string]map[string]int{}
	addModuleStatus := func(module, key string) {
		module = strings.TrimSpace(strings.ToLower(module))
		key = strings.TrimSpace(strings.ToLower(key))
		if module == "" || key == "" {
			return
		}
		if _, ok := moduleStatus[module]; !ok {
			moduleStatus[module] = map[string]int{}
		}
		moduleStatus[module][key]++
	}

	outputs := make([]opsOutputEvent, 0, sourceLimit)
	openRisks := make([]opsRiskItem, 0, 128)
	openActions := make([]opsActionItem, 0, 128)

	addRiskAndAction := func(module, typ, severity, owner, itemID, summary string, since time.Time) {
		since = since.UTC()
		if since.IsZero() {
			since = now
		}
		owner = strings.TrimSpace(owner)
		if owner == "" {
			owner = opsDefaultOwnerID
		}
		itemID = strings.TrimSpace(itemID)
		if itemID == "" {
			itemID = "n/a"
		}
		age := int64(now.Sub(since).Hours())
		if age < 0 {
			age = 0
		}
		rid := module + ":" + typ + ":" + itemID
		risk := opsRiskItem{
			ID:          rid,
			Module:      module,
			Type:        typ,
			Severity:    normalizeSeverity(severity),
			OwnerUserID: owner,
			ItemID:      itemID,
			Summary:     strings.TrimSpace(summary),
			Since:       since,
			AgeHours:    age,
		}
		openRisks = append(openRisks, risk)
		action := opsActionItem{
			ID:          rid,
			Module:      module,
			Type:        typ,
			Priority:    priorityFromSeverity(risk.Severity),
			OwnerUserID: owner,
			ItemID:      itemID,
			Summary:     risk.Summary,
			Since:       since,
			AgeHours:    age,
		}
		openActions = append(openActions, action)
	}

	proposals, err := s.store.ListKBProposals(ctx, "", sourceLimit)
	if err != nil {
		return opsOverviewResponse{}, err
	}
	for _, p := range proposals {
		section := ""
		if ch, chErr := s.store.GetKBProposalChange(ctx, p.ID); chErr == nil {
			section = strings.TrimSpace(ch.Section)
		}
		module := "kb"
		if isGovernanceSection(section) {
			module = "governance"
		}
		addModuleStatus(module, "status_"+strings.ToLower(strings.TrimSpace(p.Status)))

		if strings.EqualFold(strings.TrimSpace(p.Status), "applied") {
			at := p.UpdatedAt
			if p.AppliedAt != nil && !p.AppliedAt.IsZero() {
				at = p.AppliedAt.UTC()
			}
			outputs = append(outputs, opsOutputEvent{
				Module: module,
				Kind:   module + ".applied",
				Owner:  strings.TrimSpace(p.ProposerUserID),
				At:     at.UTC(),
			})
		}

		if strings.EqualFold(strings.TrimSpace(p.Status), "approved") && p.AppliedAt == nil {
			since := p.UpdatedAt
			if p.ClosedAt != nil && !p.ClosedAt.IsZero() {
				since = p.ClosedAt.UTC()
			}
			sev := "medium"
			if now.Sub(since) >= 24*time.Hour {
				sev = "high"
			}
			addRiskAndAction(module, module+"_approved_not_applied", sev, p.ProposerUserID, itoa64(p.ID), "proposal is approved but not applied", since)
		}
		if strings.EqualFold(strings.TrimSpace(p.Status), "discussing") && p.DiscussionDeadlineAt != nil && now.After(*p.DiscussionDeadlineAt) {
			addRiskAndAction(module, module+"_discussion_overdue", "medium", p.ProposerUserID, itoa64(p.ID), "discussion deadline passed", p.DiscussionDeadlineAt.UTC())
		}
		if strings.EqualFold(strings.TrimSpace(p.Status), "voting") && p.VotingDeadlineAt != nil && now.After(*p.VotingDeadlineAt) {
			addRiskAndAction(module, module+"_voting_overdue", "high", p.ProposerUserID, itoa64(p.ID), "voting deadline passed", p.VotingDeadlineAt.UTC())
		}
	}

	discipline, err := s.getDisciplineState(ctx)
	if err != nil {
		return opsOverviewResponse{}, err
	}
	for _, rep := range discipline.Reports {
		status := strings.ToLower(strings.TrimSpace(rep.Status))
		addModuleStatus("governance", "report_"+status)
		if status == "open" || status == "escalated" {
			sev := "medium"
			if status == "escalated" {
				sev = "high"
			}
			if now.Sub(rep.CreatedAt) >= 72*time.Hour {
				sev = "high"
			}
			owner := strings.TrimSpace(rep.ResolvedBy)
			if owner == "" {
				owner = opsDefaultOwnerID
			}
			addRiskAndAction("governance", "governance_report_backlog", sev, owner, itoa64(rep.ReportID), "governance report is still unresolved", rep.CreatedAt)
		}
	}
	for _, cs := range discipline.Cases {
		status := strings.ToLower(strings.TrimSpace(cs.Status))
		addModuleStatus("governance", "case_"+status)
		if status == "open" {
			owner := strings.TrimSpace(cs.JudgeUserID)
			if owner == "" {
				owner = strings.TrimSpace(cs.OpenedBy)
			}
			if owner == "" {
				owner = opsDefaultOwnerID
			}
			addRiskAndAction("governance", "governance_case_open", "high", owner, itoa64(cs.CaseID), "discipline case is still open", cs.CreatedAt)
		}
	}

	collabs, err := s.store.ListCollabSessions(ctx, "", "", "", sourceLimit)
	if err != nil {
		return opsOverviewResponse{}, err
	}
	for _, c := range collabs {
		phase := strings.ToLower(strings.TrimSpace(c.Phase))
		addModuleStatus("collab", "phase_"+phase)
		if phase == "closed" {
			at := c.UpdatedAt
			if c.ClosedAt != nil && !c.ClosedAt.IsZero() {
				at = c.ClosedAt.UTC()
			}
			outputs = append(outputs, opsOutputEvent{
				Module: "collab",
				Kind:   "collab.closed",
				Owner:  strings.TrimSpace(c.OrchestratorUserID),
				At:     at.UTC(),
			})
		}
		if phase == "executing" || phase == "reviewing" {
			age := now.Sub(c.UpdatedAt)
			if age >= 24*time.Hour {
				sev := "medium"
				if age >= 72*time.Hour {
					sev = "high"
				}
				owner := strings.TrimSpace(c.OrchestratorUserID)
				if owner == "" {
					owner = strings.TrimSpace(c.ProposerUserID)
				}
				addRiskAndAction("collab", "collab_stalled", sev, owner, strings.TrimSpace(c.CollabID), "collab session has no recent progress", c.UpdatedAt)
			}
		}
	}

	ganglia, err := s.store.ListGanglia(ctx, "", "", "", "", sourceLimit)
	if err != nil {
		return opsOverviewResponse{}, err
	}
	for _, g := range ganglia {
		life := strings.ToLower(strings.TrimSpace(g.LifeState))
		if life == "" {
			life = "nascent"
		}
		addModuleStatus("ganglia", "life_"+life)
		outputs = append(outputs, opsOutputEvent{
			Module: "ganglia",
			Kind:   "ganglia.forged",
			Owner:  strings.TrimSpace(g.AuthorUserID),
			At:     g.CreatedAt.UTC(),
		})
		if life == "validated" || life == "active" || life == "canonical" {
			outputs = append(outputs, opsOutputEvent{
				Module: "ganglia",
				Kind:   "ganglia." + life,
				Owner:  strings.TrimSpace(g.AuthorUserID),
				At:     g.UpdatedAt.UTC(),
			})
		}
	}

	bounties, err := s.getBountyState(ctx)
	if err != nil {
		return opsOverviewResponse{}, err
	}
	for _, b := range bounties.Items {
		status := strings.ToLower(strings.TrimSpace(b.Status))
		addModuleStatus("bounty", "status_"+status)
		if status == "paid" {
			at := b.UpdatedAt
			if b.ReleasedAt != nil && !b.ReleasedAt.IsZero() {
				at = b.ReleasedAt.UTC()
			}
			owner := strings.TrimSpace(b.ReleasedTo)
			if owner == "" {
				owner = strings.TrimSpace(b.ClaimedBy)
			}
			outputs = append(outputs, opsOutputEvent{
				Module: "bounty",
				Kind:   "bounty.paid",
				Owner:  owner,
				At:     at.UTC(),
			})
		}
		if status == "open" && b.DeadlineAt != nil && now.After(*b.DeadlineAt) {
			addRiskAndAction("bounty", "bounty_expired_open", "medium", b.PosterUserID, itoa64(b.BountyID), "bounty is open after deadline", b.DeadlineAt.UTC())
		}
		if status == "claimed" {
			since := b.UpdatedAt
			if b.ClaimedAt != nil && !b.ClaimedAt.IsZero() {
				since = b.ClaimedAt.UTC()
			}
			if now.Sub(since) >= 24*time.Hour {
				addRiskAndAction("bounty", "bounty_claim_waiting_verify", "medium", b.PosterUserID, itoa64(b.BountyID), "claimed bounty awaits verification", since)
			}
		}
	}

	tools, err := s.getToolRegistryState(ctx)
	if err != nil {
		return opsOverviewResponse{}, err
	}
	for _, it := range tools.Items {
		status := strings.ToLower(strings.TrimSpace(it.Status))
		addModuleStatus("tools", "status_"+status)
		if status == "active" {
			at := it.UpdatedAt
			if it.ActivatedAt != nil && !it.ActivatedAt.IsZero() {
				at = it.ActivatedAt.UTC()
			}
			outputs = append(outputs, opsOutputEvent{
				Module: "tools",
				Kind:   "tools.activated",
				Owner:  strings.TrimSpace(it.AuthorUserID),
				At:     at.UTC(),
			})
		}
		if status == "pending" && now.Sub(it.UpdatedAt) >= 24*time.Hour {
			addRiskAndAction("tools", "tool_pending_review", "low", opsDefaultOwnerID, strings.TrimSpace(it.ToolID), "pending tool registration requires review", it.UpdatedAt)
		}
	}

	mailScanLimit := minInt(500, sourceLimit)
	for _, uid := range userIDs {
		items, listErr := s.store.ListMailbox(ctx, uid, "outbox", "", "", &minFrom, nil, mailScanLimit)
		if listErr != nil {
			continue
		}
		for _, m := range items {
			addModuleStatus("mail", "outbox")
			outputs = append(outputs, opsOutputEvent{
				Module: "mail",
				Kind:   "mail.sent",
				Owner:  strings.TrimSpace(m.FromAddress),
				At:     m.SentAt.UTC(),
			})
		}
	}

	for _, uid := range userIDs {
		balance := balances[uid]
		if balance <= 200 {
			sev := "medium"
			if balance <= 0 {
				sev = "high"
			}
			addRiskAndAction("tokens", "token_low_balance", sev, uid, uid, "token balance is below safety threshold", now)
		}
	}

	sort.SliceStable(openRisks, func(i, j int) bool {
		si := severityRank(openRisks[i].Severity)
		sj := severityRank(openRisks[j].Severity)
		if si != sj {
			return si > sj
		}
		if openRisks[i].Since.Equal(openRisks[j].Since) {
			return openRisks[i].ID < openRisks[j].ID
		}
		return openRisks[i].Since.Before(openRisks[j].Since)
	})
	sort.SliceStable(openActions, func(i, j int) bool {
		pi := priorityRank(openActions[i].Priority)
		pj := priorityRank(openActions[j].Priority)
		if pi != pj {
			return pi > pj
		}
		if openActions[i].Since.Equal(openActions[j].Since) {
			return openActions[i].ID < openActions[j].ID
		}
		return openActions[i].Since.Before(openActions[j].Since)
	})

	statusKeys := map[string][]string{}
	for module, m := range moduleStatus {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		statusKeys[module] = keys
	}

	windowsPayload := make(map[string]opsWindowReport, len(windows))
	for _, win := range windows {
		windowsPayload[win.Key] = buildOpsWindowReport(win, limit, outputs, openRisks, openActions, moduleStatus)
	}

	userSnap := opsUsersSnapshot{
		Total:       len(userIDs),
		ByStatus:    map[string]int{},
		ByLifeState: map[string]int{},
	}
	for _, uid := range userIDs {
		b := users[uid]
		status := strings.ToLower(strings.TrimSpace(b.Status))
		if status == "" {
			status = "unknown"
		}
		userSnap.ByStatus[status]++
		life := strings.TrimSpace(lifeMap[uid])
		if life == "" {
			life = "alive"
		}
		userSnap.ByLifeState[life]++
		if balances[uid] <= 200 {
			userSnap.LowToken++
		}
	}
	userSnap.Active = userSnap.ByStatus["running"]
	if userSnap.Active > userSnap.Total {
		userSnap.Active = userSnap.Total
	}
	userSnap.Inactive = userSnap.Total - userSnap.Active
	if userSnap.Inactive < 0 {
		userSnap.Inactive = 0
	}

	resp := opsOverviewResponse{
		AsOf:            now,
		Window:          window,
		IncludeInactive: includeInactive,
		Limit:           limit,
		Snapshot: opsSnapshot{
			Users:           userSnap,
			ModuleStatus:    cloneNestedIntMap(moduleStatus),
			OpenRiskCount:   len(openRisks),
			OpenActionCount: len(openActions),
		},
		Windows:          windowsPayload,
		ModuleStatusKeys: statusKeys,
		Hints: map[string]any{
			"focus":          []string{"outputs", "risks", "actions"},
			"window_options": []string{opsWindow24h, opsWindow7d, opsWindowBoth},
		},
	}
	return resp, nil
}

func buildOpsWindowReport(win opsWindowDef, limit int, outputs []opsOutputEvent, risks []opsRiskItem, actions []opsActionItem, moduleStatus map[string]map[string]int) opsWindowReport {
	report := opsWindowReport{
		From:            win.From,
		To:              win.To,
		OutputByKind:    map[string]int{},
		RiskByType:      map[string]int{},
		ActionByType:    map[string]int{},
		ModuleOutput:    map[string]int{},
		ModuleRisk:      map[string]int{},
		ModuleAction:    map[string]int{},
		ModuleStatus:    cloneNestedIntMap(moduleStatus),
		TopContributors: map[string][]opsContributor{},
		Risks:           []opsRiskItem{},
		Actions:         []opsActionItem{},
		Ownership:       []opsOwnerAction{},
	}

	contribByModule := map[string]map[string]int{}
	for _, ev := range outputs {
		if ev.At.Before(win.From) || ev.At.After(win.To) {
			continue
		}
		report.OutputTotal++
		report.OutputByKind[ev.Kind]++
		report.ModuleOutput[ev.Module]++
		owner := strings.TrimSpace(ev.Owner)
		if owner == "" {
			continue
		}
		if _, ok := contribByModule[ev.Module]; !ok {
			contribByModule[ev.Module] = map[string]int{}
		}
		contribByModule[ev.Module][owner]++
	}
	for module, counts := range contribByModule {
		list := make([]opsContributor, 0, len(counts))
		for uid, n := range counts {
			list = append(list, opsContributor{UserID: uid, Count: n})
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Count == list[j].Count {
				return list[i].UserID < list[j].UserID
			}
			return list[i].Count > list[j].Count
		})
		if len(list) > 5 {
			list = list[:5]
		}
		report.TopContributors[module] = list
	}

	ownerMap := map[string]*opsOwnerAction{}
	for _, rk := range risks {
		if rk.Since.Before(win.From) || rk.Since.After(win.To) {
			continue
		}
		report.RiskCount++
		report.RiskByType[rk.Type]++
		report.ModuleRisk[rk.Module]++
		report.Risks = append(report.Risks, rk)
	}
	if len(report.Risks) > limit {
		report.Risks = report.Risks[:limit]
	}

	for _, ac := range actions {
		if ac.Since.Before(win.From) || ac.Since.After(win.To) {
			continue
		}
		report.ActionCount++
		report.ActionByType[ac.Type]++
		report.ModuleAction[ac.Module]++
		report.Actions = append(report.Actions, ac)
		uid := strings.TrimSpace(ac.OwnerUserID)
		if uid == "" {
			uid = opsDefaultOwnerID
		}
		if _, ok := ownerMap[uid]; !ok {
			ownerMap[uid] = &opsOwnerAction{UserID: uid}
		}
		o := ownerMap[uid]
		o.Total++
		switch strings.ToUpper(strings.TrimSpace(ac.Priority)) {
		case "P1":
			o.P1++
		case "P2":
			o.P2++
		default:
			o.P3++
		}
	}
	if len(report.Actions) > limit {
		report.Actions = report.Actions[:limit]
	}

	owners := make([]opsOwnerAction, 0, len(ownerMap))
	for _, it := range ownerMap {
		owners = append(owners, *it)
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].P1 != owners[j].P1 {
			return owners[i].P1 > owners[j].P1
		}
		if owners[i].Total != owners[j].Total {
			return owners[i].Total > owners[j].Total
		}
		return owners[i].UserID < owners[j].UserID
	})
	report.Ownership = owners

	return report
}

func buildOpsWindows(now time.Time, window string) []opsWindowDef {
	now = now.UTC()
	switch window {
	case opsWindow24h:
		return []opsWindowDef{{Key: opsWindow24h, From: now.Add(-24 * time.Hour), To: now}}
	case opsWindow7d:
		return []opsWindowDef{{Key: opsWindow7d, From: now.Add(-7 * 24 * time.Hour), To: now}}
	default:
		return []opsWindowDef{
			{Key: opsWindow24h, From: now.Add(-24 * time.Hour), To: now},
			{Key: opsWindow7d, From: now.Add(-7 * 24 * time.Hour), To: now},
		}
	}
}

func normalizeOpsWindow(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", opsWindowBoth:
		return opsWindowBoth
	case opsWindow24h:
		return opsWindow24h
	case opsWindow7d:
		return opsWindow7d
	default:
		return ""
	}
}

func normalizeSeverity(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "critical", "high":
		return "high"
	case "medium":
		return "medium"
	default:
		return "low"
	}
}

func priorityFromSeverity(sev string) string {
	switch normalizeSeverity(sev) {
	case "high":
		return "P1"
	case "medium":
		return "P2"
	default:
		return "P3"
	}
}

func severityRank(sev string) int {
	switch normalizeSeverity(sev) {
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}

func priorityRank(p string) int {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "P1":
		return 3
	case "P2":
		return 2
	default:
		return 1
	}
}

func opsScanLimit(limit int) int {
	n := limit * 8
	if n < 500 {
		n = 500
	}
	if n > 5000 {
		n = 5000
	}
	return n
}

func cloneNestedIntMap(in map[string]map[string]int) map[string]map[string]int {
	out := make(map[string]map[string]int, len(in))
	for k, v := range in {
		nv := make(map[string]int, len(v))
		for kk, vv := range v {
			nv[kk] = vv
		}
		out[k] = nv
	}
	return out
}

func itoa64(v int64) string {
	return strconv.FormatInt(v, 10)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
