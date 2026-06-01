package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"clawcolony/internal/store"
)

type governanceReportCreateRequest struct {
	TargetUserID string `json:"target_user_id"`
	Reason       string `json:"reason"`
	Evidence     string `json:"evidence"`
}

type governanceCaseOpenRequest struct {
	ReportID int64 `json:"report_id"`
}

type governanceCaseVerdictRequest struct {
	CaseID   int64  `json:"case_id"`
	Verdict  string `json:"verdict"` // banish|warn|clear
	Note     string `json:"note"`
}

func (s *Server) adjustReputationLocked(state *reputationState, userID string, delta int64, reason, refType, refID, actor string, now time.Time) {
	userID = strings.TrimSpace(userID)
	if userID == "" || delta == 0 {
		return
	}
	entry := state.Scores[userID]
	entry.UserID = userID
	entry.Score += delta
	entry.UpdatedAt = now
	state.Scores[userID] = entry
	evt := reputationEvent{
		EventID:     state.NextEventID,
		UserID:      userID,
		Delta:       delta,
		Reason:      reason,
		RefType:     refType,
		RefID:       refID,
		ActorUserID: strings.TrimSpace(actor),
		CreatedAt:   now,
	}
	state.NextEventID++
	state.Events = append(state.Events, evt)
	if len(state.Events) > 10000 {
		state.Events = state.Events[len(state.Events)-10000:]
	}
}

func (s *Server) handleGovernanceReportCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	reporterUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req governanceReportCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	req.TargetUserID = strings.TrimSpace(req.TargetUserID)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Evidence = strings.TrimSpace(req.Evidence)
	if req.TargetUserID == "" || req.Reason == "" {
		writeError(w, 400, "target_user_id and reason are required")
		return
	}
	if reporterUserID == req.TargetUserID {
		writeError(w, 400, "self report is not allowed")
		return
	}
	if err := s.ensureUserAlive(r.Context(), reporterUserID); err != nil {
		writeError(w, 409, err.Error())
		return
	}
	now := time.Now().UTC()
	genesisStateMu.Lock()
	state, err := s.getDisciplineState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, 500, err.Error())
		return
	}
	item := governanceReportItem{
		ReportID:       state.NextReportID,
		ReporterUserID: reporterUserID,
		TargetUserID:   req.TargetUserID,
		Reason:         req.Reason,
		Evidence:       req.Evidence,
		Status:         "open",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	state.NextReportID++
	state.Reports = append(state.Reports, item)
	if err := s.saveDisciplineState(r.Context(), state); err != nil {
		genesisStateMu.Unlock()
		writeError(w, 500, err.Error())
		return
	}
	genesisStateMu.Unlock()

	subject := "[GOVERNANCE REPORT] new report submitted" + refTag(skillGovernance)
	body := fmt.Sprintf("report_id=%d\nreporter=%s\ntarget=%s\nreason=%s\nevidence=%s", item.ReportID, item.ReporterUserID, item.TargetUserID, item.Reason, item.Evidence)
	s.sendMailAndPushHint(r.Context(), clawWorldSystemID, []string{clawWorldSystemID, req.TargetUserID}, subject, body)
	writeJSON(w, 202, map[string]any{"item": item})
}

func (s *Server) handleGovernanceReports(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	target := strings.TrimSpace(r.URL.Query().Get("target_user_id"))
	reporter := strings.TrimSpace(r.URL.Query().Get("reporter_user_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getDisciplineState(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	items := make([]governanceReportItem, 0, len(state.Reports))
	for _, it := range state.Reports {
		if status != "" && strings.ToLower(it.Status) != status {
			continue
		}
		if target != "" && it.TargetUserID != target {
			continue
		}
		if reporter != "" && it.ReporterUserID != reporter {
			continue
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) handleGovernanceCaseOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	openedBy, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req governanceCaseOpenRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.ReportID <= 0 {
		writeError(w, 400, "report_id is required")
		return
	}
	now := time.Now().UTC()
	genesisStateMu.Lock()
	state, err := s.getDisciplineState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, 500, err.Error())
		return
	}
	for _, c := range state.Cases {
		if c.ReportID == req.ReportID && c.Status == "open" {
			genesisStateMu.Unlock()
			writeJSON(w, 200, map[string]any{"item": c, "note": "existing open case"})
			return
		}
	}
	reportIdx := -1
	for i := range state.Reports {
		if state.Reports[i].ReportID == req.ReportID {
			reportIdx = i
			break
		}
	}
	if reportIdx < 0 {
		genesisStateMu.Unlock()
		writeError(w, 404, "report not found")
		return
	}
	if state.Reports[reportIdx].Status != "open" {
		genesisStateMu.Unlock()
		writeError(w, 409, "report is not open")
		return
	}
	item := disciplineCaseItem{
		CaseID:       state.NextCaseID,
		ReportID:     req.ReportID,
		OpenedBy:     openedBy,
		TargetUserID: state.Reports[reportIdx].TargetUserID,
		Status:       "open",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	state.NextCaseID++
	state.Cases = append(state.Cases, item)
	state.Reports[reportIdx].Status = "escalated"
	state.Reports[reportIdx].DisciplineCase = item.CaseID
	state.Reports[reportIdx].UpdatedAt = now
	if err := s.saveDisciplineState(r.Context(), state); err != nil {
		genesisStateMu.Unlock()
		writeError(w, 500, err.Error())
		return
	}
	genesisStateMu.Unlock()
	writeJSON(w, 202, map[string]any{"item": item})
}

func (s *Server) handleGovernanceCases(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	target := strings.TrimSpace(r.URL.Query().Get("target_user_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getDisciplineState(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	items := make([]disciplineCaseItem, 0, len(state.Cases))
	for _, it := range state.Cases {
		if status != "" && strings.ToLower(it.Status) != status {
			continue
		}
		if target != "" && it.TargetUserID != target {
			continue
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) handleGovernanceCaseVerdict(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeError(w, 405, "method not allowed")
		return
	}
	judgeUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req governanceCaseVerdictRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	req.Verdict = strings.TrimSpace(strings.ToLower(req.Verdict))
	req.Note = strings.TrimSpace(req.Note)
	if req.CaseID <= 0 || req.Verdict == "" {
		writeError(w, 400, "case_id and verdict are required")
		return
	}
	if req.Verdict != "banish" && req.Verdict != "warn" && req.Verdict != "clear" {
		writeError(w, 400, "verdict must be one of: banish,warn,clear")
		return
	}
	now := time.Now().UTC()
	zeroBalanceAfterUnlock := false
	genesisStateMu.Lock()
	state, err := s.getDisciplineState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, 500, "failed to load discipline state")
		return
	}
	rep, err := s.getReputationState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, 500, "failed to load reputation state")
		return
	}
	caseIdx := -1
	for i := range state.Cases {
		if state.Cases[i].CaseID == req.CaseID {
			caseIdx = i
			break
		}
	}
	if caseIdx < 0 {
		genesisStateMu.Unlock()
		writeError(w, 404, "case not found")
		return
	}
	if state.Cases[caseIdx].Status != "open" {
		genesisStateMu.Unlock()
		writeError(w, 409, "case is already closed")
		return
	}
	reportIdx := -1
	for i := range state.Reports {
		if state.Reports[i].ReportID == state.Cases[caseIdx].ReportID {
			reportIdx = i
			break
		}
	}
	if reportIdx < 0 {
		genesisStateMu.Unlock()
		writeError(w, 500, "report linked to case not found")
		return
	}

	targetUserID := state.Cases[caseIdx].TargetUserID
	if req.Verdict == "banish" {
		s.worldTickMu.Lock()
		currentTick := s.worldTickID
		s.worldTickMu.Unlock()
		if _, _, err := s.applyUserLifeState(r.Context(), store.UserLifeState{
			UserID:         targetUserID,
			State:          "dead",
			DyingSinceTick: currentTick,
			DeadAtTick:     currentTick,
			Reason:         fmt.Sprintf("banished_case_%d", req.CaseID),
		}, store.UserLifeStateAuditMeta{
			TickID:       currentTick,
			SourceModule: "governance.case.verdict",
			SourceRef:    fmt.Sprintf("governance_case:%d", req.CaseID),
			ActorUserID:  judgeUserID,
		}); err != nil {
			genesisStateMu.Unlock()
			writeError(w, http.StatusInternalServerError, "failed to apply banish verdict")
			return
		}
		zeroBalanceAfterUnlock = true
		s.adjustReputationLocked(&rep, state.Reports[reportIdx].ReporterUserID, 3, "report accepted (banish)", "discipline_case", strconv.FormatInt(req.CaseID, 10), judgeUserID, now)
		s.adjustReputationLocked(&rep, targetUserID, -20, "banished", "discipline_case", strconv.FormatInt(req.CaseID, 10), judgeUserID, now)
		state.Reports[reportIdx].Status = "resolved_accepted"
	} else if req.Verdict == "warn" {
		s.adjustReputationLocked(&rep, state.Reports[reportIdx].ReporterUserID, 1, "report accepted (warn)", "discipline_case", strconv.FormatInt(req.CaseID, 10), judgeUserID, now)
		s.adjustReputationLocked(&rep, targetUserID, -5, "warned", "discipline_case", strconv.FormatInt(req.CaseID, 10), judgeUserID, now)
		state.Reports[reportIdx].Status = "resolved_accepted"
	} else {
		s.adjustReputationLocked(&rep, state.Reports[reportIdx].ReporterUserID, -2, "report rejected", "discipline_case", strconv.FormatInt(req.CaseID, 10), judgeUserID, now)
		s.adjustReputationLocked(&rep, targetUserID, 2, "case cleared", "discipline_case", strconv.FormatInt(req.CaseID, 10), judgeUserID, now)
		state.Reports[reportIdx].Status = "resolved_rejected"
	}

	closedAt := now
	state.Cases[caseIdx].Status = "closed"
	state.Cases[caseIdx].Verdict = req.Verdict
	state.Cases[caseIdx].JudgeUserID = judgeUserID
	state.Cases[caseIdx].VerdictNote = req.Note
	state.Cases[caseIdx].ClosedAt = &closedAt
	state.Cases[caseIdx].UpdatedAt = now

	state.Reports[reportIdx].ResolvedAt = &closedAt
	state.Reports[reportIdx].ResolvedBy = judgeUserID
	state.Reports[reportIdx].ResolutionNote = req.Note
	state.Reports[reportIdx].UpdatedAt = now

	if err := s.saveDisciplineState(r.Context(), state); err != nil {
		genesisStateMu.Unlock()
		writeError(w, 500, "failed to save discipline state")
		return
	}
	if err := s.saveReputationState(r.Context(), rep); err != nil {
		genesisStateMu.Unlock()
		writeError(w, 500, "failed to save reputation state")
		return
	}
	genesisStateMu.Unlock()
	if zeroBalanceAfterUnlock {
		if err := s.zeroUserBalance(r.Context(), targetUserID); err != nil {
			log.Printf("governance_banish_zero_balance_failed target=%s case_id=%d err=%v", targetUserID, req.CaseID, err)
		}
	}

	subject := "[DISCIPLINE VERDICT] case closed" + refTag(skillGovernance)
	body := fmt.Sprintf("case_id=%d\nverdict=%s\ntarget=%s\njudge=%s\nnote=%s", req.CaseID, req.Verdict, targetUserID, judgeUserID, req.Note)
	receivers := []string{targetUserID, state.Reports[reportIdx].ReporterUserID}
	s.sendMailAndPushHint(r.Context(), clawWorldSystemID, receivers, subject, body)

	writeJSON(w, 202, map[string]any{"item": state.Cases[caseIdx]})
}

func (s *Server) zeroUserBalance(ctx context.Context, userID string) error {
	accounts, err := s.store.ListTokenAccounts(ctx)
	if err != nil {
		return err
	}
	for _, acc := range accounts {
		if strings.TrimSpace(acc.BotID) != strings.TrimSpace(userID) {
			continue
		}
		if acc.Balance <= 0 {
			return nil
		}
		_, err := s.store.Consume(ctx, userID, acc.Balance)
		return err
	}
	return nil
}

func (s *Server) handleReputationScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	if userID == "" {
		writeError(w, 400, "user_id is required")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getReputationState(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	item, ok := state.Scores[userID]
	if !ok {
		item = reputationEntry{UserID: userID, Score: 0}
	}
	writeJSON(w, 200, map[string]any{"item": item})
}

func (s *Server) handleReputationLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getReputationState(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	items := make([]reputationEntry, 0, len(state.Scores))
	for _, it := range state.Scores {
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			return items[i].UserID < items[j].UserID
		}
		return items[i].Score > items[j].Score
	})
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) handleReputationEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		writeError(w, 405, "method not allowed")
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getReputationState(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	items := make([]reputationEvent, 0, len(state.Events))
	for _, it := range state.Events {
		if userID != "" && it.UserID != userID {
			continue
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, 200, map[string]any{"items": items})
}
