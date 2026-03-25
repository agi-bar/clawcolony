package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/store"
)

type authUser struct {
	id     string
	apiKey string
}

func newAuthUser(t *testing.T, srv *Server) authUser {
	t.Helper()
	id, key := seedActiveUserWithAPIKey(t, srv)
	return authUser{id: id, apiKey: key}
}

func (a authUser) headers() map[string]string {
	return apiKeyHeaders(a.apiKey)
}

func TestAPIEventsReturnsWorldDetailedEventsAndBilingualFields(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedWorldEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Items          []apiEventItem `json:"items"`
		Count          int            `json:"count"`
		NextCursor     string         `json:"next_cursor"`
		PartialResults bool           `json:"partial_results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode events response: %v", err)
	}
	if resp.Count != len(resp.Items) {
		t.Fatalf("count should match page size, count=%d items=%d", resp.Count, len(resp.Items))
	}
	if resp.PartialResults {
		t.Fatalf("fixture should not hit partial_results: %+v", resp)
	}
	if len(resp.Items) < 11 {
		t.Fatalf("expected at least 11 world events, got=%d body=%s", len(resp.Items), w.Body.String())
	}

	failedStep := findAPIEvent(resp.Items, "world.step.failed", "world_tick_step", strconv.FormatInt(fixture.failedStepID, 10))
	if failedStep == nil {
		t.Fatalf("expected failed world step event, body=%s", w.Body.String())
	}
	if failedStep.Title != failedStep.TitleZH || failedStep.Summary != failedStep.SummaryZH {
		t.Fatalf("title/summary should mirror zh fields: %+v", *failedStep)
	}
	if strings.TrimSpace(failedStep.TitleEN) == "" || strings.TrimSpace(failedStep.SummaryEN) == "" {
		t.Fatalf("english fields missing: %+v", *failedStep)
	}
	if failedStep.Category != "world" || failedStep.SourceModule != "world.tick.step" {
		t.Fatalf("unexpected failed step metadata: %+v", *failedStep)
	}
	if !strings.Contains(failedStep.TitleZH, "生命成本结算") {
		t.Fatalf("failed step title should use readable zh label: %+v", *failedStep)
	}
	if got := failedStep.Evidence["step_name"]; got != "life_cost_drain" {
		t.Fatalf("failed step evidence should preserve raw step name, got=%v", got)
	}

	freezeEntered := findAPIEvent(resp.Items, "world.freeze.entered", "world_freeze", "11")
	if freezeEntered == nil {
		t.Fatalf("expected freeze entered event, body=%s", w.Body.String())
	}
	if freezeEntered.ImpactLevel != "warning" || freezeEntered.SourceModule != "world.freeze" {
		t.Fatalf("unexpected freeze entered metadata: %+v", *freezeEntered)
	}

	freezeLifted := findAPIEvent(resp.Items, "world.freeze.lifted", "world_freeze", "12")
	if freezeLifted == nil {
		t.Fatalf("expected freeze lifted event, body=%s", w.Body.String())
	}
	if freezeLifted.ImpactLevel != "notice" {
		t.Fatalf("freeze lifted should downgrade impact to notice: %+v", *freezeLifted)
	}

	replayed := findAPIEvent(resp.Items, "world.tick.replayed", "world_tick", "12")
	if replayed == nil {
		t.Fatalf("expected replayed tick event, body=%s", w.Body.String())
	}
	if got := replayed.Evidence["replay_of_tick_id"]; got != float64(11) {
		t.Fatalf("expected replay_of_tick_id evidence, got=%v", got)
	}

	skipped := findAPIEvent(resp.Items, "world.step.skipped", "world_tick_step", strconv.FormatInt(fixture.skippedStepID, 10))
	if skipped == nil {
		t.Fatalf("expected skipped world step event, body=%s", w.Body.String())
	}
	if !strings.Contains(skipped.SummaryZH, "由于世界冻结") || !strings.Contains(skipped.SummaryEN, "world was frozen") {
		t.Fatalf("skipped step should explain world_frozen reason: %+v", *skipped)
	}

	degraded := findAPIEvent(resp.Items, "world.tick.degraded", "world_tick", "13")
	if degraded == nil {
		t.Fatalf("expected degraded tick event, body=%s", w.Body.String())
	}
	if degraded.ImpactLevel != "warning" {
		t.Fatalf("degraded tick should be warning: %+v", *degraded)
	}
}

func TestAPIEventsSupportsFiltersPaginationAndValidation(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedWorldEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=12&limit=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events tick filter status=%d body=%s", w.Code, w.Body.String())
	}
	var page1 struct {
		Items          []apiEventItem `json:"items"`
		Count          int            `json:"count"`
		NextCursor     string         `json:"next_cursor"`
		PartialResults bool           `json:"partial_results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page1: %v", err)
	}
	if len(page1.Items) != 2 || page1.NextCursor == "" {
		t.Fatalf("expected first page to contain 2 items and a next cursor: %+v", page1)
	}
	for _, it := range page1.Items {
		if it.TickID != 12 {
			t.Fatalf("tick_id filter should keep only tick 12 events: %+v", it)
		}
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=12&limit=2&cursor="+page1.NextCursor, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events second page status=%d body=%s", w.Code, w.Body.String())
	}
	var page2 struct {
		Items          []apiEventItem `json:"items"`
		Count          int            `json:"count"`
		NextCursor     string         `json:"next_cursor"`
		PartialResults bool           `json:"partial_results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v", err)
	}
	if len(page2.Items) == 0 {
		t.Fatalf("expected second page to contain remaining events")
	}
	for _, it := range page2.Items {
		if it.TickID != 12 {
			t.Fatalf("tick_id filter should keep only tick 12 events on page2: %+v", it)
		}
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=11&kind=world.freeze.entered&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events freeze-by-tick status=%d body=%s", w.Code, w.Body.String())
	}
	var freezeByTick struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &freezeByTick); err != nil {
		t.Fatalf("decode freeze-by-tick response: %v", err)
	}
	if len(freezeByTick.Items) != 1 || freezeByTick.Items[0].Kind != "world.freeze.entered" || freezeByTick.Items[0].TickID != 11 {
		t.Fatalf("tick_id query should preserve the matching freeze transition: %+v", freezeByTick.Items)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?kind=world.step.failed&object_type=world_tick_step&object_id="+strconv.FormatInt(fixture.failedStepID, 10)+"&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events object filter status=%d body=%s", w.Code, w.Body.String())
	}
	var filtered struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("decode filtered response: %v", err)
	}
	if len(filtered.Items) != 1 || filtered.Items[0].Kind != "world.step.failed" || filtered.Items[0].ObjectID != strconv.FormatInt(fixture.failedStepID, 10) {
		t.Fatalf("unexpected filtered events: %+v", filtered.Items)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=world&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events category filter status=%d body=%s", w.Code, w.Body.String())
	}
	var categoryResp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &categoryResp); err != nil {
		t.Fatalf("decode category response: %v", err)
	}
	if len(categoryResp.Items) == 0 {
		t.Fatalf("expected category filter to keep world events")
	}
	for _, it := range categoryResp.Items {
		if it.Category != "world" {
			t.Fatalf("category filter returned wrong item: %+v", it)
		}
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=life&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events empty life category status=%d body=%s", w.Code, w.Body.String())
	}
	var emptyLifeCategory struct {
		Items []apiEventItem `json:"items"`
		Count int            `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &emptyLifeCategory); err != nil {
		t.Fatalf("decode empty life category response: %v", err)
	}
	if len(emptyLifeCategory.Items) != 0 || emptyLifeCategory.Count != 0 {
		t.Fatalf("world-only fixture should return no life events: %+v", emptyLifeCategory)
	}

	since := time.Date(2026, 3, 10, 20, 2, 0, 0, time.UTC).Format(time.RFC3339)
	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?since="+since+"&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events since filter status=%d body=%s", w.Code, w.Body.String())
	}
	var sinceResp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sinceResp); err != nil {
		t.Fatalf("decode since response: %v", err)
	}
	for _, it := range sinceResp.Items {
		ts, err := time.Parse(time.RFC3339Nano, it.OccurredAt)
		if err != nil {
			t.Fatalf("parse occurred_at: %v", err)
		}
		if ts.Before(time.Date(2026, 3, 10, 20, 2, 0, 0, time.UTC)) {
			t.Fatalf("since filter returned older item: %+v", it)
		}
	}

	until := time.Date(2026, 3, 10, 20, 1, 1, 0, time.UTC).Format(time.RFC3339)
	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?until="+until+"&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events until filter status=%d body=%s", w.Code, w.Body.String())
	}
	var untilResp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &untilResp); err != nil {
		t.Fatalf("decode until response: %v", err)
	}
	for _, it := range untilResp.Items {
		ts, err := time.Parse(time.RFC3339Nano, it.OccurredAt)
		if err != nil {
			t.Fatalf("parse until occurred_at: %v", err)
		}
		if !ts.Before(time.Date(2026, 3, 10, 20, 1, 1, 0, time.UTC)) {
			t.Fatalf("until filter should exclude boundary and newer items: %+v", it)
		}
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?since=2026-03-10T20:02:00Z&until=2026-03-10T20:01:00Z", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("since>until should fail, got=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?since=2026-03-10T20:01:00Z&until=2026-03-10T20:03:00Z&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events valid range filter status=%d body=%s", w.Code, w.Body.String())
	}
	var rangeResp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rangeResp); err != nil {
		t.Fatalf("decode range response: %v", err)
	}
	if len(rangeResp.Items) == 0 {
		t.Fatalf("expected valid range query to return a subset")
	}
	for _, it := range rangeResp.Items {
		ts, err := time.Parse(time.RFC3339Nano, it.OccurredAt)
		if err != nil {
			t.Fatalf("parse range occurred_at: %v", err)
		}
		if ts.Before(time.Date(2026, 3, 10, 20, 1, 0, 0, time.UTC)) || !ts.Before(time.Date(2026, 3, 10, 20, 3, 0, 0, time.UTC)) {
			t.Fatalf("range query returned out-of-window item: %+v", it)
		}
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?user_id=lobster-alice&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events user filter status=%d body=%s", w.Code, w.Body.String())
	}
	var emptyUserResp struct {
		Items      []apiEventItem `json:"items"`
		Count      int            `json:"count"`
		NextCursor string         `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &emptyUserResp); err != nil {
		t.Fatalf("decode user filter response: %v", err)
	}
	if len(emptyUserResp.Items) != 0 || emptyUserResp.Count != 0 || emptyUserResp.NextCursor != "" {
		t.Fatalf("world-only fixture should return an empty user-filtered page: %+v", emptyUserResp)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?cursor=invalid", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor should fail, got=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?since=bad-time", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid since should fail, got=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=9999&limit=20", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("unknown tick_id should return empty page, got=%d body=%s", w.Code, w.Body.String())
	}
	var emptyTickResp struct {
		Items          []apiEventItem `json:"items"`
		Count          int            `json:"count"`
		NextCursor     string         `json:"next_cursor"`
		PartialResults bool           `json:"partial_results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &emptyTickResp); err != nil {
		t.Fatalf("decode empty tick response: %v", err)
	}
	if len(emptyTickResp.Items) != 0 || emptyTickResp.Count != 0 || emptyTickResp.NextCursor != "" || emptyTickResp.PartialResults {
		t.Fatalf("unknown tick_id should return an empty non-partial page: %+v", emptyTickResp)
	}
}

func TestAPIEventsReturnsLifeDetailedEventsAndSupportsUserFilter(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedLifeEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=life&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events life query status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode life response: %v", err)
	}
	if len(resp.Items) < 5 {
		t.Fatalf("expected multiple life events, got=%d body=%s", len(resp.Items), w.Body.String())
	}

	created := findAPIEventByKindAndTarget(resp.Items, "life.state.created", fixture.dyingUserID)
	if created == nil {
		t.Fatalf("expected life.state.created event, body=%s", w.Body.String())
	}
	if len(created.Targets) != 1 || created.Targets[0].DisplayName != "小钳" {
		t.Fatalf("life event should use nickname-first display_name: %+v", *created)
	}
	if !strings.Contains(created.TitleZH, "小钳") || strings.TrimSpace(created.TitleEN) == "" {
		t.Fatalf("life state created event should expose bilingual readable titles: %+v", *created)
	}

	hibernating := findAPIEventByKindAndTarget(resp.Items, "life.hibernation.entered", fixture.dyingUserID)
	if hibernating == nil || hibernating.ImpactLevel != "warning" {
		t.Fatalf("expected warning life.hibernation.entered event, got=%+v", hibernating)
	}

	dead := findAPIEventByKindAndTarget(resp.Items, "life.dead.marked", fixture.dyingUserID)
	if dead == nil || dead.ImpactLevel != "critical" {
		t.Fatalf("expected critical life.dead.marked event, got=%+v", dead)
	}
	if !strings.Contains(dead.SummaryZH, "后续主动行为将停止") {
		t.Fatalf("life.dead.marked should explain user-visible impact: %+v", *dead)
	}

	revived := findAPIEventByKindAndTarget(resp.Items, "life.hibernation.revived", fixture.wakeUserID)
	if revived == nil {
		t.Fatalf("expected life.hibernation.revived event, body=%s", w.Body.String())
	}
	if len(revived.Targets) != 1 || revived.Targets[0].UserID != fixture.wakeUserID {
		t.Fatalf("revival event should target the revived user: %+v", *revived)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?user_id="+fixture.dyingUserID+"&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events user-scoped life query status=%d body=%s", w.Code, w.Body.String())
	}
	var scoped struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("decode scoped life response: %v", err)
	}
	if len(scoped.Items) == 0 {
		t.Fatalf("expected user filter to return life events for %s", fixture.dyingUserID)
	}
	for _, it := range scoped.Items {
		if !apiEventInvolvesUser(it, fixture.dyingUserID) {
			t.Fatalf("user filter returned unrelated item: %+v", it)
		}
	}
}

func TestAPIEventsReturnsGovernanceDetailedEvents(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedGovernanceEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=governance&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events governance query status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode governance response: %v", err)
	}
	if len(resp.Items) < 6 {
		t.Fatalf("expected at least 6 governance events, got=%d body=%s", len(resp.Items), w.Body.String())
	}

	reportFiled := findAPIEvent(resp.Items, "governance.report.filed", "governance_report", strconv.FormatInt(fixture.banishReportID, 10))
	if reportFiled == nil {
		t.Fatalf("expected governance.report.filed event for banish report, body=%s", w.Body.String())
	}
	if len(reportFiled.Targets) != 1 || reportFiled.Targets[0].DisplayName != "小钳" {
		t.Fatalf("governance report should use nickname-first target display name: %+v", *reportFiled)
	}

	caseCreated := findAPIEvent(resp.Items, "governance.case.created", "governance_case", strconv.FormatInt(fixture.banishCaseID, 10))
	if caseCreated == nil {
		t.Fatalf("expected governance.case.created event, body=%s", w.Body.String())
	}
	if len(caseCreated.Actors) < 2 || caseCreated.Actors[0].UserID != fixture.judgeUserID {
		t.Fatalf("governance case should include opener first and reporter as participants: %+v", *caseCreated)
	}

	banished := findAPIEvent(resp.Items, "governance.verdict.banished", "governance_case", strconv.FormatInt(fixture.banishCaseID, 10))
	if banished == nil {
		t.Fatalf("expected governance.verdict.banished event, body=%s", w.Body.String())
	}
	if banished.ImpactLevel != "critical" || !strings.Contains(banished.SummaryZH, "放逐裁决") {
		t.Fatalf("banish verdict should be critical and human-readable: %+v", *banished)
	}

	cleared := findAPIEvent(resp.Items, "governance.verdict.cleared", "governance_case", strconv.FormatInt(fixture.clearCaseID, 10))
	if cleared == nil {
		t.Fatalf("expected governance.verdict.cleared event, body=%s", w.Body.String())
	}
	if cleared.ImpactLevel != "notice" || !strings.Contains(cleared.SummaryEN, "no penalty") {
		t.Fatalf("cleared verdict should explain the outcome: %+v", *cleared)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=governance&user_id="+fixture.reporterUserID+"&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events governance user filter status=%d body=%s", w.Code, w.Body.String())
	}
	var scoped struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("decode governance scoped response: %v", err)
	}
	if len(scoped.Items) != 6 {
		t.Fatalf("reporter should see all related governance events, got=%d body=%s", len(scoped.Items), w.Body.String())
	}
	for _, it := range scoped.Items {
		if it.Category != "governance" || !apiEventInvolvesUser(it, fixture.reporterUserID) {
			t.Fatalf("governance user filter returned unrelated item: %+v", it)
		}
	}
}

func TestAPIEventsReturnsKnowledgeDetailedEvents(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedKnowledgeEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=knowledge&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events knowledge query status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode knowledge response: %v", err)
	}
	if len(resp.Items) < 9 {
		t.Fatalf("expected multiple knowledge events, got=%d body=%s", len(resp.Items), w.Body.String())
	}

	created := findAPIEvent(resp.Items, "knowledge.proposal.created", "kb_proposal", strconv.FormatInt(fixture.approvedProposalID, 10))
	if created == nil {
		t.Fatalf("expected knowledge.proposal.created event, body=%s", w.Body.String())
	}
	if len(created.Actors) != 1 || created.Actors[0].DisplayName != "小钳" {
		t.Fatalf("proposal created event should use nickname-first actor display name: %+v", *created)
	}

	revised := findAPIEvent(resp.Items, "knowledge.proposal.revised", "kb_revision", strconv.FormatInt(fixture.revisionID, 10))
	if revised == nil {
		t.Fatalf("expected knowledge.proposal.revised event, body=%s", w.Body.String())
	}
	if len(revised.Actors) != 1 || revised.Actors[0].DisplayName != "lobster-reviewer" {
		t.Fatalf("revision event should fall back to username when nickname is absent: %+v", *revised)
	}

	commented := findAPIEvent(resp.Items, "knowledge.proposal.commented", "kb_thread_message", strconv.FormatInt(fixture.commentThreadID, 10))
	if commented == nil {
		t.Fatalf("expected knowledge.proposal.commented event, body=%s", w.Body.String())
	}
	if !strings.Contains(commented.SummaryZH, "讨论线程") || strings.TrimSpace(commented.TitleEN) == "" {
		t.Fatalf("comment event should remain bilingual and user-readable: %+v", *commented)
	}

	votingStarted := findAPIEvent(resp.Items, "knowledge.proposal.voting_started", "kb_proposal", strconv.FormatInt(fixture.approvedProposalID, 10))
	if votingStarted == nil {
		t.Fatalf("expected knowledge.proposal.voting_started event, body=%s", w.Body.String())
	}
	if votingStarted.ImpactLevel != "notice" {
		t.Fatalf("voting started should be notice level: %+v", *votingStarted)
	}

	yesVote := findAPIEvent(resp.Items, "knowledge.proposal.vote.yes", "kb_vote", strconv.FormatInt(fixture.reviewerVoteID, 10))
	if yesVote == nil {
		t.Fatalf("expected reviewer yes vote event, body=%s", w.Body.String())
	}
	if len(yesVote.Actors) != 1 || yesVote.Actors[0].DisplayName != "lobster-reviewer" {
		t.Fatalf("vote event should expose actor display name: %+v", *yesVote)
	}

	approved := findAPIEvent(resp.Items, "knowledge.proposal.approved", "kb_proposal", strconv.FormatInt(fixture.approvedProposalID, 10))
	if approved == nil {
		t.Fatalf("expected approved knowledge proposal event, body=%s", w.Body.String())
	}
	if approved.ImpactLevel != "notice" || !strings.Contains(approved.SummaryEN, "approved") {
		t.Fatalf("approved event should explain the voting outcome: %+v", *approved)
	}

	applied := findAPIEvent(resp.Items, "knowledge.proposal.applied", "kb_proposal", strconv.FormatInt(fixture.approvedProposalID, 10))
	if applied == nil {
		t.Fatalf("expected applied knowledge proposal event, body=%s", w.Body.String())
	}
	if !strings.Contains(applied.SummaryZH, "知识库") || !strings.Contains(applied.SummaryZH, "小钳") {
		t.Fatalf("applied event should explain the user-facing apply result: %+v", *applied)
	}

	rejected := findAPIEvent(resp.Items, "knowledge.proposal.rejected", "kb_proposal", strconv.FormatInt(fixture.rejectedProposalID, 10))
	if rejected == nil {
		t.Fatalf("expected rejected knowledge proposal event, body=%s", w.Body.String())
	}
	if rejected.ImpactLevel != "warning" || !strings.Contains(rejected.SummaryZH, "未达到通过条件") {
		t.Fatalf("rejected event should explain why the proposal did not pass: %+v", *rejected)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=knowledge&user_id="+fixture.reviewerUserID+"&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events knowledge user filter status=%d body=%s", w.Code, w.Body.String())
	}
	var scoped struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("decode knowledge scoped response: %v", err)
	}
	if len(scoped.Items) == 0 {
		t.Fatalf("expected reviewer-scoped knowledge events, body=%s", w.Body.String())
	}
	for _, it := range scoped.Items {
		if it.Category != "knowledge" || !apiEventInvolvesUser(it, fixture.reviewerUserID) {
			t.Fatalf("knowledge user filter returned unrelated item: %+v", it)
		}
	}
	if findAPIEvent(scoped.Items, "knowledge.proposal.revised", "kb_revision", strconv.FormatInt(fixture.revisionID, 10)) == nil {
		t.Fatalf("reviewer-scoped events should include the revision event: %+v", scoped.Items)
	}
	if findAPIEvent(scoped.Items, "knowledge.proposal.applied", "kb_proposal", strconv.FormatInt(fixture.approvedProposalID, 10)) == nil {
		t.Fatalf("reviewer-scoped events should include the applied event for a participated proposal: %+v", scoped.Items)
	}
}

func TestAPIEventsReturnsCollaborationDetailedEvents(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedCollaborationEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=collaboration&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events collaboration query status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode collaboration response: %v", err)
	}
	if len(resp.Items) < 12 {
		t.Fatalf("expected multiple collaboration events, got=%d body=%s", len(resp.Items), w.Body.String())
	}

	created := findAPIEvent(resp.Items, "collaboration.created", "collab_session", fixture.successCollabID)
	if created == nil {
		t.Fatalf("expected collaboration.created event, body=%s", w.Body.String())
	}
	if len(created.Actors) != 1 || created.Actors[0].DisplayName != "小钳" {
		t.Fatalf("collaboration created should use nickname-first proposer display name: %+v", *created)
	}

	applied := findAPIEventByKindAndActor(resp.Items, "collaboration.applied", fixture.executorUserID)
	if applied == nil {
		t.Fatalf("expected collaboration.applied event for executor, body=%s", w.Body.String())
	}
	if !strings.Contains(applied.SummaryZH, "报名申请") {
		t.Fatalf("collaboration applied should be user-readable: %+v", *applied)
	}

	assigned := findAPIEvent(resp.Items, "collaboration.assigned", "collab_session", fixture.successCollabID)
	if assigned == nil {
		t.Fatalf("expected collaboration.assigned event, body=%s", w.Body.String())
	}
	if len(assigned.Targets) < 3 {
		t.Fatalf("collaboration assigned should target selected team members: %+v", *assigned)
	}

	accepted := findAPIEventByKindAndTarget(resp.Items, "collaboration.accepted", fixture.executorUserID)
	if accepted == nil {
		t.Fatalf("expected collaboration.accepted event for executor, body=%s", w.Body.String())
	}

	started := findAPIEvent(resp.Items, "collaboration.started", "collab_session", fixture.successCollabID)
	if started == nil {
		t.Fatalf("expected collaboration.started event, body=%s", w.Body.String())
	}
	if started.ImpactLevel != "notice" {
		t.Fatalf("collaboration started should be notice level: %+v", *started)
	}

	submitted := findAPIEvent(resp.Items, "collaboration.artifact.submitted", "collab_artifact", strconv.FormatInt(fixture.successArtifactID, 10))
	if submitted == nil {
		t.Fatalf("expected collaboration.artifact.submitted event, body=%s", w.Body.String())
	}
	if !strings.Contains(submitted.SummaryZH, "摘要") || strings.TrimSpace(submitted.TitleEN) == "" {
		t.Fatalf("artifact submission should expose bilingual readable text: %+v", *submitted)
	}

	reviewApproved := findAPIEvent(resp.Items, "collaboration.review.approved", "collab_artifact", strconv.FormatInt(fixture.successArtifactID, 10))
	if reviewApproved == nil {
		t.Fatalf("expected collaboration.review.approved event, body=%s", w.Body.String())
	}
	if len(reviewApproved.Targets) != 1 || reviewApproved.Targets[0].UserID != fixture.executorUserID {
		t.Fatalf("approved review should target the artifact author: %+v", *reviewApproved)
	}

	closed := findAPIEvent(resp.Items, "collaboration.closed", "collab_session", fixture.successCollabID)
	if closed == nil {
		t.Fatalf("expected collaboration.closed event, body=%s", w.Body.String())
	}

	rework := findAPIEvent(resp.Items, "collaboration.review.rework_requested", "collab_artifact", strconv.FormatInt(fixture.failedFirstArtifactID, 10))
	if rework == nil {
		t.Fatalf("expected collaboration.review.rework_requested event, body=%s", w.Body.String())
	}
	if rework.ImpactLevel != "warning" {
		t.Fatalf("rework request should be warning level: %+v", *rework)
	}

	resubmitted := findAPIEvent(resp.Items, "collaboration.resubmitted", "collab_artifact", strconv.FormatInt(fixture.failedResubmittedArtifactID, 10))
	if resubmitted == nil {
		t.Fatalf("expected collaboration.resubmitted event, body=%s", w.Body.String())
	}

	failed := findAPIEvent(resp.Items, "collaboration.failed", "collab_session", fixture.failedCollabID)
	if failed == nil {
		t.Fatalf("expected collaboration.failed event, body=%s", w.Body.String())
	}
	if !strings.Contains(failed.SummaryEN, "failed") {
		t.Fatalf("failed collaboration should explain the failed outcome: %+v", *failed)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=collaboration&user_id="+fixture.executorUserID+"&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events collaboration user filter status=%d body=%s", w.Code, w.Body.String())
	}
	var scoped struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("decode collaboration scoped response: %v", err)
	}
	if len(scoped.Items) == 0 {
		t.Fatalf("expected executor-scoped collaboration events, body=%s", w.Body.String())
	}
	for _, it := range scoped.Items {
		if it.Category != "collaboration" || !apiEventInvolvesUser(it, fixture.executorUserID) {
			t.Fatalf("collaboration user filter returned unrelated item: %+v", it)
		}
	}
	if findAPIEvent(scoped.Items, "collaboration.created", "collab_session", fixture.successCollabID) == nil {
		t.Fatalf("executor-scoped events should include the collaboration origin event: %+v", scoped.Items)
	}
	if findAPIEvent(scoped.Items, "collaboration.resubmitted", "collab_artifact", strconv.FormatInt(fixture.failedResubmittedArtifactID, 10)) == nil {
		t.Fatalf("executor-scoped events should include the resubmitted artifact event: %+v", scoped.Items)
	}
}

func TestAPIEventsReturnsCommunicationDetailedEvents(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedCommunicationEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=communication&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events communication query status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode communication response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("global communication feed should only expose non-private community events, got=%d body=%s", len(resp.Items), w.Body.String())
	}

	listCreated := findAPIEvent(resp.Items, "communication.list.created", "mailing_list", fixture.listID)
	if listCreated == nil {
		t.Fatalf("expected mailing list created event, body=%s", w.Body.String())
	}
	if leaked := findAPIEvent(resp.Items, "communication.mail.sent", "mail_message", strconv.FormatInt(fixture.directMessageID, 10)); leaked != nil {
		t.Fatalf("global communication feed should not expose direct mail events: %+v", *leaked)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=communication&user_id="+fixture.senderUserID+"&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events communication sender filter status=%d body=%s", w.Code, w.Body.String())
	}
	var senderScoped struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &senderScoped); err != nil {
		t.Fatalf("decode communication sender-scoped response: %v", err)
	}
	sent := findAPIEvent(senderScoped.Items, "communication.mail.sent", "mail_message", strconv.FormatInt(fixture.directMessageID, 10))
	if sent == nil {
		t.Fatalf("expected sender-scoped direct mail sent event, body=%s", w.Body.String())
	}
	if len(sent.Actors) != 1 || sent.Actors[0].DisplayName != "小钳" {
		t.Fatalf("direct mail sent should use nickname-first actor display name: %+v", *sent)
	}
	if len(sent.Targets) != 1 || sent.Targets[0].UserID != fixture.recipientUserID {
		t.Fatalf("direct mail sent should target the recipient: %+v", *sent)
	}

	broadcast := findAPIEvent(senderScoped.Items, "communication.broadcast.sent", "mail_message", strconv.FormatInt(fixture.broadcastMessageID, 10))
	if broadcast == nil {
		t.Fatalf("expected sender-scoped broadcast sent event, body=%s", w.Body.String())
	}
	if got := broadcast.Evidence["recipient_cnt"]; got != float64(2) {
		t.Fatalf("broadcast should capture recipient count, got=%v", got)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=communication&kind=communication.contact.updated&user_id="+fixture.senderUserID+"&limit=20", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events communication contact owner query status=%d body=%s", w.Code, w.Body.String())
	}
	var ownerContacts struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ownerContacts); err != nil {
		t.Fatalf("decode communication contact owner response: %v", err)
	}
	contactUpdated := findAPIEvent(ownerContacts.Items, "communication.contact.updated", "mail_contact", fixture.contactObjectID)
	if contactUpdated == nil {
		t.Fatalf("expected owner-scoped contact updated event, body=%s", w.Body.String())
	}
	if !strings.Contains(contactUpdated.SummaryZH, "搭档B") || !strings.Contains(contactUpdated.SummaryZH, "reviewer") || contactUpdated.Visibility != "private" {
		t.Fatalf("contact update should stay owner-scoped and private: %+v", *contactUpdated)
	}
	contactOccurredAt, err := time.Parse(time.RFC3339Nano, contactUpdated.OccurredAt)
	if err != nil {
		t.Fatalf("parse contact occurred_at: %v", err)
	}

	since := contactOccurredAt.Add(time.Second).Format(time.RFC3339Nano)
	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=communication&kind=communication.contact.updated&user_id="+fixture.senderUserID+"&since="+since+"&limit=20", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events communication contact since filter status=%d body=%s", w.Code, w.Body.String())
	}
	var contactWindow struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &contactWindow); err != nil {
		t.Fatalf("decode communication contact window response: %v", err)
	}
	if len(contactWindow.Items) != 0 {
		t.Fatalf("contact event should respect since filtering, got=%+v", contactWindow.Items)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=communication&user_id="+fixture.recipientUserID+"&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events communication user filter status=%d body=%s", w.Code, w.Body.String())
	}
	var scoped struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &scoped); err != nil {
		t.Fatalf("decode communication scoped response: %v", err)
	}
	if len(scoped.Items) == 0 {
		t.Fatalf("expected recipient-scoped communication events, body=%s", w.Body.String())
	}
	for _, it := range scoped.Items {
		if it.Category != "communication" || !apiEventInvolvesUser(it, fixture.recipientUserID) {
			t.Fatalf("communication user filter returned unrelated item: %+v", it)
		}
	}
	if findAPIEvent(scoped.Items, "communication.mail.sent", "mail_message", strconv.FormatInt(fixture.directMessageID, 10)) == nil {
		t.Fatalf("recipient-scoped events should include the direct mail sent event: %+v", scoped.Items)
	}
	received := findAPIEvent(scoped.Items, "communication.mail.received", "mail_message", strconv.FormatInt(fixture.directMessageID, 10))
	if received == nil {
		t.Fatalf("expected recipient-scoped direct mail received event, body=%s", w.Body.String())
	}
	if !strings.Contains(received.SummaryEN, "design sync") {
		t.Fatalf("mail received should expose readable bilingual summary: %+v", *received)
	}
	if _, ok := received.Evidence["mailbox_id"]; ok {
		t.Fatalf("mail received event should not leak mailbox_id: %+v", received.Evidence)
	}
	if leaked := findAPIEvent(scoped.Items, "communication.mail.sent", "mail_message", strconv.FormatInt(fixture.unrelatedMessageID, 10)); leaked != nil {
		t.Fatalf("recipient-scoped events should not include third-party outbox mail: %+v", *leaked)
	}
	if leakedContact := findAPIEvent(scoped.Items, "communication.contact.updated", "mail_contact", fixture.contactObjectID); leakedContact != nil {
		t.Fatalf("recipient-scoped events should not include another user's private contact metadata: %+v", *leakedContact)
	}
	reminderTriggered := findAPIEvent(scoped.Items, "communication.reminder.triggered", "mail_reminder", strconv.FormatInt(fixture.reminderMessageID, 10))
	if reminderTriggered == nil {
		t.Fatalf("expected reminder triggered event, body=%s", w.Body.String())
	}
	if reminderTriggered.Actors[0].DisplayName != "Clawcolony" {
		t.Fatalf("reminder sender should use the user-facing system actor name: %+v", *reminderTriggered)
	}
	if got := reminderTriggered.Evidence["reminder_id"]; got != float64(fixture.reminderMessageID) {
		t.Fatalf("reminder triggered should expose reminder_id, got=%v", got)
	}
	if _, ok := reminderTriggered.Evidence["mailbox_id"]; ok {
		t.Fatalf("reminder event should not leak mailbox_id: %+v", reminderTriggered.Evidence)
	}
	if findAPIEvent(scoped.Items, "communication.reminder.resolved", "mail_reminder", strconv.FormatInt(fixture.reminderMessageID, 10)) == nil {
		t.Fatalf("recipient-scoped events should include the reminder resolution event: %+v", scoped.Items)
	}
	if duplicateReminderMail := findAPIEvent(scoped.Items, "communication.mail.received", "mail_message", strconv.FormatInt(fixture.reminderMessageID, 10)); duplicateReminderMail != nil {
		t.Fatalf("recognized reminder mail should not also appear as a generic received mail event: %+v", *duplicateReminderMail)
	}
}

func TestAPIEventsReturnsMonitorToolingDetailedEvents(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	userID := seedActiveUser(t, srv)
	if _, err := srv.store.UpdateBotNickname(ctx, userID, "扳手虾"); err != nil {
		t.Fatalf("set tooling nickname: %v", err)
	}
	base := time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC)

	highRiskCost, err := srv.store.AppendCostEvent(ctx, store.CostEvent{
		UserID:    userID,
		CostType:  "tool.runtime.t2",
		Amount:    11,
		Units:     5,
		MetaJSON:  `{"tool_id":"openclaw.redeploy","result_ok":true}`,
		CreatedAt: base,
	})
	if err != nil {
		t.Fatalf("append high-risk tool cost event: %v", err)
	}
	failedCost, err := srv.store.AppendCostEvent(ctx, store.CostEvent{
		UserID:    userID,
		CostType:  "tool.runtime.t1",
		Amount:    4,
		Units:     2,
		MetaJSON:  `{"tool_id":"openclaw.restart","result_ok":false}`,
		CreatedAt: base.Add(1 * time.Second),
	})
	if err != nil {
		t.Fatalf("append failed tool cost event: %v", err)
	}
	failedRequestLog, err := srv.store.AppendRequestLog(ctx, store.RequestLog{
		Time:       base.Add(10 * time.Second),
		Method:     http.MethodPost,
		Path:       "/api/v1/tools/invoke",
		UserID:     userID,
		StatusCode: http.StatusInternalServerError,
		DurationMS: 321,
	})
	if err != nil {
		t.Fatalf("append failed tool request log: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=tooling&user_id="+userID+"&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events tooling query status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode tooling response: %v", err)
	}

	highRisk := findAPIEvent(resp.Items, "tooling.tool.high_risk_used", "cost_event", strconv.FormatInt(highRiskCost.ID, 10))
	if highRisk == nil {
		t.Fatalf("expected high-risk tooling event: %+v", resp.Items)
	}
	if len(highRisk.Actors) != 1 || highRisk.Actors[0].DisplayName != "扳手虾" {
		t.Fatalf("tooling event should use nickname-first actor display: %+v", *highRisk)
	}
	if got := highRisk.Evidence["tier"]; got != "T2" {
		t.Fatalf("high-risk tooling event should preserve tool tier, got=%v item=%+v", got, *highRisk)
	}

	failedTool := findAPIEvent(resp.Items, "tooling.tool.failed", "cost_event", strconv.FormatInt(failedCost.ID, 10))
	if failedTool == nil {
		t.Fatalf("expected failed tooling cost event: %+v", resp.Items)
	}
	if !strings.Contains(failedTool.SummaryZH, "失败") {
		t.Fatalf("failed tooling event should read as a failure: %+v", *failedTool)
	}

	failedRequest := findAPIEvent(resp.Items, "tooling.tool.failed", "request_log", strconv.FormatInt(failedRequestLog.ID, 10))
	if failedRequest == nil {
		t.Fatalf("expected tooling failure event from request log: %+v", resp.Items)
	}
	if failedRequest.SourceModule != "request_logs" {
		t.Fatalf("request-log tooling failure should preserve source module: %+v", *failedRequest)
	}
	if got := failedRequest.Evidence["status_code"]; fmt.Sprint(got) != strconv.Itoa(http.StatusInternalServerError) {
		t.Fatalf("request-log tooling failure should preserve status code, got=%v item=%+v", got, *failedRequest)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=tooling&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events tooling global query status=%d body=%s", w.Code, w.Body.String())
	}
	var globalResp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &globalResp); err != nil {
		t.Fatalf("decode global tooling response: %v", err)
	}
	if findAPIEvent(globalResp.Items, "tooling.tool.high_risk_used", "cost_event", strconv.FormatInt(highRiskCost.ID, 10)) == nil {
		t.Fatalf("global tooling feed should include seeded high-risk tool event: %+v", globalResp.Items)
	}
}

func TestAPIEventsReturnsEconomyAndIdentityDetailedEvents(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedEconomyIdentityEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=economy&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events economy query status=%d body=%s", w.Code, w.Body.String())
	}
	var economyResp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &economyResp); err != nil {
		t.Fatalf("decode economy response: %v", err)
	}
	if len(economyResp.Items) == 0 {
		t.Fatalf("expected economy detailed events, body=%s", w.Body.String())
	}

	transfer := findAPIEvent(economyResp.Items, "economy.token.transferred", "cost_event", strconv.FormatInt(fixture.transferCostEventID, 10))
	if transfer == nil {
		t.Fatalf("expected token transfer event, body=%s", w.Body.String())
	}
	if len(transfer.Actors) != 1 || transfer.Actors[0].DisplayName != "小钳" {
		t.Fatalf("transfer event should use nickname-first actor display: %+v", *transfer)
	}
	if len(transfer.Targets) != 1 || transfer.Targets[0].UserID != fixture.recipientUserID {
		t.Fatalf("transfer event should target recipient: %+v", *transfer)
	}
	if !strings.Contains(transfer.SummaryZH, "pairing stipend") {
		t.Fatalf("transfer event should preserve memo in readable summary: %+v", *transfer)
	}

	tip := findAPIEvent(economyResp.Items, "economy.token.tipped", "cost_event", strconv.FormatInt(fixture.tipCostEventID, 10))
	if tip == nil {
		t.Fatalf("expected token tip event, body=%s", w.Body.String())
	}
	if !strings.Contains(tip.SummaryEN, "great review") {
		t.Fatalf("tip event should preserve reason in bilingual summary: %+v", *tip)
	}

	if findAPIEvent(economyResp.Items, "economy.token.wish.created", "token_wish", fixture.wishID) == nil {
		t.Fatalf("expected token wish created event: %+v", economyResp.Items)
	}
	if wishFulfilled := findAPIEvent(economyResp.Items, "economy.token.wish.fulfilled", "token_wish", fixture.wishID); wishFulfilled == nil {
		t.Fatalf("expected token wish fulfilled event: %+v", economyResp.Items)
	} else if len(wishFulfilled.Targets) != 1 || wishFulfilled.Targets[0].UserID != fixture.wishUserID {
		t.Fatalf("wish fulfilled should target wish owner: %+v", *wishFulfilled)
	}

	if findAPIEvent(economyResp.Items, "economy.bounty.posted", "bounty", strconv.FormatInt(fixture.paidBountyID, 10)) == nil {
		t.Fatalf("expected bounty posted event: %+v", economyResp.Items)
	}
	if findAPIEvent(economyResp.Items, "economy.bounty.claimed", "bounty", strconv.FormatInt(fixture.paidBountyID, 10)) == nil {
		t.Fatalf("expected bounty claimed event: %+v", economyResp.Items)
	}
	if findAPIEvent(economyResp.Items, "economy.bounty.paid", "bounty", strconv.FormatInt(fixture.paidBountyID, 10)) == nil {
		t.Fatalf("expected bounty paid event: %+v", economyResp.Items)
	}
	if bountyClosed := findAPIEvent(economyResp.Items, "economy.bounty.expired", "bounty", strconv.FormatInt(fixture.expiredBountyID, 10)); bountyClosed == nil {
		t.Fatalf("expected expired bounty closed event: %+v", economyResp.Items)
	} else if !strings.Contains(bountyClosed.SummaryZH, "已过期") {
		t.Fatalf("expired bounty should explain the closure reason: %+v", *bountyClosed)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=economy&user_id="+fixture.recipientUserID+"&limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events economy user filter status=%d body=%s", w.Code, w.Body.String())
	}
	var recipientScoped struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &recipientScoped); err != nil {
		t.Fatalf("decode recipient-scoped economy response: %v", err)
	}
	if findAPIEvent(recipientScoped.Items, "economy.token.transferred", "cost_event", strconv.FormatInt(fixture.transferCostEventID, 10)) == nil {
		t.Fatalf("recipient-scoped economy feed should include incoming transfer event: %+v", recipientScoped.Items)
	}
	if findAPIEvent(recipientScoped.Items, "economy.bounty.paid", "bounty", strconv.FormatInt(fixture.paidBountyID, 10)) == nil {
		t.Fatalf("recipient-scoped economy feed should include paid bounty event: %+v", recipientScoped.Items)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=identity&user_id="+fixture.repTargetUserID+"&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events identity target filter status=%d body=%s", w.Code, w.Body.String())
	}
	var targetIdentity struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &targetIdentity); err != nil {
		t.Fatalf("decode target identity response: %v", err)
	}
	repChanged := findAPIEvent(targetIdentity.Items, "identity.reputation.changed", "reputation_event", strconv.FormatInt(fixture.targetReputationEventID, 10))
	if repChanged == nil {
		t.Fatalf("expected target reputation change event: %+v", targetIdentity.Items)
	}
	if repChanged.ImpactLevel != "warning" || !strings.Contains(repChanged.SummaryZH, "收到警告") {
		t.Fatalf("negative reputation event should be readable and warning-level: %+v", *repChanged)
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?category=identity&user_id="+fixture.judgeUserID+"&limit=50", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("/api/v1/events identity actor filter status=%d body=%s", w.Code, w.Body.String())
	}
	var actorIdentity struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &actorIdentity); err != nil {
		t.Fatalf("decode actor identity response: %v", err)
	}
	if findAPIEvent(actorIdentity.Items, "identity.reputation.changed", "reputation_event", strconv.FormatInt(fixture.targetReputationEventID, 10)) == nil {
		t.Fatalf("judge-scoped identity feed should include acted reputation events: %+v", actorIdentity.Items)
	}
}

func TestBuildEconomyBountyPaidEventRequiresPaidStatusAndReleaseTime(t *testing.T) {
	now := time.Now().UTC()
	actors := map[string]apiEventActor{
		"poster":   {UserID: "poster", DisplayName: "Poster"},
		"claimer":  {UserID: "claimer", DisplayName: "Claimer"},
		"releaser": {UserID: "releaser", DisplayName: "Releaser"},
	}

	if _, ok := buildEconomyBountyPaidEvent(bountyItem{
		BountyID:     1,
		PosterUserID: "poster",
		ClaimedBy:    "claimer",
		ReleasedBy:   "releaser",
		Status:       "paid",
		UpdatedAt:    now,
	}, actors); ok {
		t.Fatalf("expected paid bounty event to require released_at")
	}

	if _, ok := buildEconomyBountyPaidEvent(bountyItem{
		BountyID:     2,
		PosterUserID: "poster",
		ClaimedBy:    "claimer",
		ReleasedBy:   "releaser",
		Status:       "claimed",
		UpdatedAt:    now,
		ReleasedAt:   &now,
	}, actors); ok {
		t.Fatalf("expected paid bounty event to require paid status")
	}

	if item, ok := buildEconomyBountyPaidEvent(bountyItem{
		BountyID:     3,
		PosterUserID: "poster",
		ClaimedBy:    "claimer",
		ReleasedBy:   "releaser",
		Status:       "paid",
		UpdatedAt:    now,
		ReleasedAt:   &now,
	}, actors); !ok || item.Kind != "economy.bounty.paid" {
		t.Fatalf("expected valid paid bounty event, got ok=%v item=%+v", ok, item)
	}
}

func TestCollectEconomyEventSourceFiltersCostEventsByUser(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	fixture := seedEconomyIdentityEventsFixture(t, srv, ctx)

	unrelatedSender := newAuthUser(t, srv)
	unrelatedRecipient := seedActiveUser(t, srv)
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/transfer", map[string]any{
		"to_user_id": unrelatedRecipient,
		"amount":     9,
		"memo":       "unrelated",
	}, unrelatedSender.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("unrelated token transfer status=%d body=%s", w.Code, w.Body.String())
	}

	source, partialResults, err := srv.collectEconomyEventSource(ctx, apiEventsQuery{
		Category: "economy",
		UserID:   fixture.recipientUserID,
		Limit:    50,
	})
	if err != nil {
		t.Fatalf("collect economy event source: %v", err)
	}
	if partialResults {
		t.Fatalf("unexpected partial results for filtered economy source")
	}
	if len(source.CostEvents) == 0 {
		t.Fatalf("expected filtered cost events for recipient user")
	}
	for _, item := range source.CostEvents {
		meta := parseEconomyCostMeta(item.MetaJSON)
		if item.UserID != fixture.recipientUserID && meta.ToUserID != fixture.recipientUserID {
			t.Fatalf("unexpected unrelated cost event in filtered source: %+v meta=%+v", item, meta)
		}
	}
}

func TestCollectEconomyEventSourceKeepsRelevantCostEventsBeyondGlobalScanWindow(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	relevantSender := seedActiveUser(t, srv)
	relevantRecipient := seedActiveUser(t, srv)

	relevant, err := srv.store.AppendCostEvent(ctx, store.CostEvent{
		UserID:    relevantSender,
		CostType:  "econ.transfer.out",
		Amount:    5,
		Units:     5,
		MetaJSON:  `{"to_user_id":"` + relevantRecipient + `","memo":"older relevant"}`,
		CreatedAt: time.Now().UTC().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("append relevant cost event: %v", err)
	}

	for i := 0; i < eventsEconomyCostScanLimit+25; i++ {
		if _, err := srv.store.AppendCostEvent(ctx, store.CostEvent{
			UserID:    fmt.Sprintf("other-sender-%03d", i),
			CostType:  "econ.transfer.out",
			Amount:    1,
			Units:     1,
			MetaJSON:  fmt.Sprintf(`{"to_user_id":"other-recipient-%03d"}`, i),
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatalf("append unrelated cost event %d: %v", i, err)
		}
	}

	source, partialResults, err := srv.collectEconomyEventSource(ctx, apiEventsQuery{
		Category: "economy",
		UserID:   relevantRecipient,
		Limit:    50,
	})
	if err != nil {
		t.Fatalf("collect economy event source beyond scan window: %v", err)
	}
	if partialResults {
		t.Fatalf("unexpected partial results for involved-user economy source")
	}
	found := false
	for _, item := range source.CostEvents {
		if item.ID == relevant.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected relevant incoming cost event to survive unrelated global traffic: %+v", source.CostEvents)
	}
}

type worldEventsFixture struct {
	okStepID      int64
	failedStepID  int64
	skippedStepID int64
}

func seedWorldEventsFixture(t *testing.T, srv *Server, ctx context.Context) worldEventsFixture {
	t.Helper()
	ticks := []store.WorldTickRecord{
		{
			ID:          1,
			TickID:      10,
			StartedAt:   time.Date(2026, 3, 10, 20, 0, 0, 0, time.UTC),
			DurationMS:  900,
			TriggerType: "scheduled",
			Status:      "ok",
		},
		{
			ID:          2,
			TickID:      11,
			StartedAt:   time.Date(2026, 3, 10, 20, 1, 0, 0, time.UTC),
			DurationMS:  1200,
			TriggerType: "scheduled",
			Status:      "frozen",
			ErrorText:   "population_guard",
		},
		{
			ID:             3,
			TickID:         12,
			StartedAt:      time.Date(2026, 3, 10, 20, 2, 0, 0, time.UTC),
			DurationMS:     1500,
			TriggerType:    "replay",
			ReplayOfTickID: 11,
			Status:         "ok",
		},
		{
			ID:          4,
			TickID:      13,
			StartedAt:   time.Date(2026, 3, 10, 20, 3, 0, 0, time.UTC),
			DurationMS:  700,
			TriggerType: "scheduled",
			Status:      "degraded",
			ErrorText:   "repo_sync failed",
		},
	}
	for _, it := range ticks {
		if _, err := srv.store.AppendWorldTick(ctx, it); err != nil {
			t.Fatalf("append world tick %d: %v", it.TickID, err)
		}
	}
	steps := []store.WorldTickStepRecord{
		{
			ID:         101,
			TickID:     12,
			StepName:   "repo_sync",
			StartedAt:  time.Date(2026, 3, 10, 20, 2, 1, 0, time.UTC),
			DurationMS: 300,
			Status:     "ok",
		},
		{
			ID:         102,
			TickID:     12,
			StepName:   "life_cost_drain",
			StartedAt:  time.Date(2026, 3, 10, 20, 2, 2, 0, time.UTC),
			DurationMS: 200,
			Status:     "failed",
			ErrorText:  "ledger unavailable",
		},
		{
			ID:         103,
			TickID:     11,
			StepName:   "mail_delivery",
			StartedAt:  time.Date(2026, 3, 10, 20, 1, 1, 0, time.UTC),
			DurationMS: 0,
			Status:     "skipped",
			ErrorText:  "world_frozen",
		},
	}
	fixture := worldEventsFixture{}
	for _, it := range steps {
		got, err := srv.store.AppendWorldTickStep(ctx, it)
		if err != nil {
			t.Fatalf("append world tick step %d: %v", it.ID, err)
		}
		if got.ID <= 0 {
			t.Fatalf("expected persisted step ID, got=%d", got.ID)
		}
		switch got.StepName {
		case "repo_sync":
			fixture.okStepID = got.ID
		case "life_cost_drain":
			fixture.failedStepID = got.ID
		case "mail_delivery":
			fixture.skippedStepID = got.ID
		}
	}
	return fixture
}

func findAPIEvent(items []apiEventItem, kind, objectType, objectID string) *apiEventItem {
	for i := range items {
		if items[i].Kind == kind && items[i].ObjectType == objectType && items[i].ObjectID == objectID {
			return &items[i]
		}
	}
	return nil
}

func findAPIEventByKindAndTarget(items []apiEventItem, kind, userID string) *apiEventItem {
	for i := range items {
		if items[i].Kind != kind {
			continue
		}
		for _, target := range items[i].Targets {
			if target.UserID == userID {
				return &items[i]
			}
		}
	}
	return nil
}

func findAPIEventByKindAndActor(items []apiEventItem, kind, userID string) *apiEventItem {
	for i := range items {
		if items[i].Kind != kind {
			continue
		}
		for _, actor := range items[i].Actors {
			if actor.UserID == userID {
				return &items[i]
			}
		}
	}
	return nil
}

type lifeEventsFixture struct {
	dyingUserID string
	wakeUserID  string
}

func seedLifeEventsFixture(t *testing.T, srv *Server, ctx context.Context) lifeEventsFixture {
	t.Helper()
	srv.cfg.HibernationPeriodTicks = 1

	dyingUserID, _ := seedActiveUserWithAPIKey(t, srv)
	nickname := "小钳"
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       dyingUserID,
		Name:        "little-claw",
		Nickname:    &nickname,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("update dying user nickname: %v", err)
	}
	if err := srv.runLifeStateTransitions(ctx, 1); err != nil {
		t.Fatalf("run life transitions tick1: %v", err)
	}
	if _, err := srv.store.Consume(ctx, dyingUserID, 1000); err != nil {
		t.Fatalf("consume all balance for dying user: %v", err)
	}
	if err := srv.runLifeStateTransitions(ctx, 2); err != nil {
		t.Fatalf("run life transitions tick2: %v", err)
	}
	if err := srv.runLifeStateTransitions(ctx, 3); err != nil {
		t.Fatalf("run life transitions tick3: %v", err)
	}

	wakeUserID, _ := seedActiveUserWithAPIKey(t, srv)
	if err := srv.runLifeStateTransitions(ctx, 4); err != nil {
		t.Fatalf("run life transitions tick4: %v", err)
	}
	if _, err := srv.store.Consume(ctx, wakeUserID, 1000); err != nil {
		t.Fatalf("consume all balance for wake user: %v", err)
	}
	if err := srv.runLifeStateTransitions(ctx, 5); err != nil {
		t.Fatalf("run life transitions tick5: %v", err)
	}
	if _, err := srv.store.Recharge(ctx, wakeUserID, srv.cfg.MinRevivalBalance); err != nil {
		t.Fatalf("recharge wake user for revival: %v", err)
	}
	if err := srv.runLifeStateTransitions(ctx, 6); err != nil {
		t.Fatalf("run life transitions tick6: %v", err)
	}

	return lifeEventsFixture{
		dyingUserID: dyingUserID,
		wakeUserID:  wakeUserID,
	}
}

type governanceEventsFixture struct {
	reporterUserID string
	judgeUserID    string
	banishReportID int64
	banishCaseID   int64
	clearCaseID    int64
}

func seedGovernanceEventsFixture(t *testing.T, srv *Server, ctx context.Context) governanceEventsFixture {
	t.Helper()

	reporter := newAuthUser(t, srv)
	judge := newAuthUser(t, srv)
	banishTargetID := seedActiveUser(t, srv)
	clearTargetID := seedActiveUser(t, srv)

	nickname := "小钳"
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       banishTargetID,
		Name:        "little-claw-target",
		Nickname:    &nickname,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("update governance target nickname: %v", err)
	}

	createReport := func(targetUserID, reason string) int64 {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/report", map[string]any{
			"target_user_id": targetUserID,
			"reason":         reason,
			"evidence":       "captured-context",
		}, reporter.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("governance report status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Item governanceReportItem `json:"item"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode governance report: %v", err)
		}
		return resp.Item.ReportID
	}

	openCase := func(reportID int64) int64 {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/cases/open", map[string]any{
			"report_id": reportID,
		}, judge.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("open governance case status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Item disciplineCaseItem `json:"item"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode governance case: %v", err)
		}
		return resp.Item.CaseID
	}

	applyVerdict := func(caseID int64, verdict, note string) {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/cases/verdict", map[string]any{
			"case_id": caseID,
			"verdict": verdict,
			"note":    note,
		}, judge.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("governance verdict status=%d body=%s", w.Code, w.Body.String())
		}
	}

	banishReportID := createReport(banishTargetID, "repeated abuse")
	banishCaseID := openCase(banishReportID)
	applyVerdict(banishCaseID, "banish", "repeated abuse confirmed")

	clearReportID := createReport(clearTargetID, "insufficient evidence")
	clearCaseID := openCase(clearReportID)
	applyVerdict(clearCaseID, "clear", "evidence did not hold up")

	return governanceEventsFixture{
		reporterUserID: reporter.id,
		judgeUserID:    judge.id,
		banishReportID: banishReportID,
		banishCaseID:   banishCaseID,
		clearCaseID:    clearCaseID,
	}
}

type knowledgeEventsFixture struct {
	reviewerUserID     string
	approvedProposalID int64
	rejectedProposalID int64
	revisionID         int64
	commentThreadID    int64
	reviewerVoteID     int64
}

type collaborationEventsFixture struct {
	executorUserID              string
	successCollabID             string
	successArtifactID           int64
	failedCollabID              string
	failedFirstArtifactID       int64
	failedResubmittedArtifactID int64
}

type communicationEventsFixture struct {
	senderUserID       string
	recipientUserID    string
	directMessageID    int64
	broadcastMessageID int64
	unrelatedMessageID int64
	reminderMessageID  int64
	contactObjectID    string
	listID             string
}

type economyIdentityEventsFixture struct {
	senderUserID            string
	recipientUserID         string
	wishUserID              string
	judgeUserID             string
	repTargetUserID         string
	transferCostEventID     int64
	tipCostEventID          int64
	wishID                  string
	paidBountyID            int64
	expiredBountyID         int64
	targetReputationEventID int64
}

func seedKnowledgeEventsFixture(t *testing.T, srv *Server, ctx context.Context) knowledgeEventsFixture {
	t.Helper()

	proposer := newAuthUser(t, srv)
	reviewer := newAuthUser(t, srv)
	supporter := newAuthUser(t, srv)
	rejectProposer := newAuthUser(t, srv)
	rejectVoter := newAuthUser(t, srv)
	proposerUserID := proposer.id
	reviewerUserID := reviewer.id

	proposerNickname := "小钳"
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       proposerUserID,
		Name:        "little-claw-proposer",
		Nickname:    &proposerNickname,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("update kb proposer nickname: %v", err)
	}
	reviewerName := "lobster-reviewer"
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       reviewerUserID,
		Name:        reviewerName,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("update reviewer username fallback: %v", err)
	}

	createProposal := func(actor authUser, title, reason, diffText string) int64 {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals", map[string]any{
			"title":                     title,
			"reason":                    reason,
			"vote_threshold_pct":        80,
			"vote_window_seconds":       3600,
			"discussion_window_seconds": 3600,
			"category":                  "governance",
			"references":                []map[string]any{},
			"change": map[string]any{
				"op_type":     "add",
				"section":     "governance",
				"title":       title,
				"new_content": "runtime policy details",
				"diff_text":   diffText,
			},
		}, actor.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("create kb proposal status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Proposal store.KBProposal `json:"proposal"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode kb proposal create response: %v", err)
		}
		return resp.Proposal.ID
	}

	latestRevisionID := func(proposalID int64) int64 {
		revisions, err := srv.store.ListKBRevisions(ctx, proposalID, 20)
		if err != nil {
			t.Fatalf("list kb revisions: %v", err)
		}
		var latest store.KBRevision
		for _, revision := range revisions {
			if revision.RevisionNo > latest.RevisionNo {
				latest = revision
			}
		}
		if latest.ID <= 0 {
			t.Fatalf("expected at least one kb revision for proposal=%d", proposalID)
		}
		return latest.ID
	}

	enroll := func(proposalID int64, actor authUser) {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/enroll", map[string]any{
			"proposal_id": proposalID,
		}, actor.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("kb enroll status=%d body=%s", w.Code, w.Body.String())
		}
	}

	ack := func(proposalID, revisionID int64, actor authUser) {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/ack", map[string]any{
			"proposal_id": proposalID,
			"revision_id": revisionID,
		}, actor.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("kb ack status=%d body=%s", w.Code, w.Body.String())
		}
	}

	castVote := func(proposalID, revisionID int64, actor authUser, vote, reason string) int64 {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/vote", map[string]any{
			"proposal_id": proposalID,
			"revision_id": revisionID,
			"vote":        vote,
			"reason":      reason,
		}, actor.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("kb vote status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Item store.KBVote `json:"item"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode kb vote response: %v", err)
		}
		return resp.Item.ID
	}

	approvedProposalID := createProposal(proposer, "运行时协作规范", "clarify runtime collaboration", "diff: clarify runtime collaboration guardrails")
	baseRevisionID := latestRevisionID(approvedProposalID)

	revisionResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/revise", map[string]any{
		"proposal_id":      approvedProposalID,
		"base_revision_id": baseRevisionID,
		"category":         "governance",
		"references":       []map[string]any{},
		"change": map[string]any{
			"op_type":     "add",
			"section":     "governance",
			"title":       "运行时协作规范",
			"new_content": "runtime collaboration guardrails v2",
			"diff_text":   "diff: refine review and voting requirements",
		},
	}, reviewer.headers())
	if revisionResp.Code != http.StatusAccepted {
		t.Fatalf("kb revise status=%d body=%s", revisionResp.Code, revisionResp.Body.String())
	}
	var revised struct {
		Revision store.KBRevision `json:"revision"`
	}
	if err := json.Unmarshal(revisionResp.Body.Bytes(), &revised); err != nil {
		t.Fatalf("decode kb revise response: %v", err)
	}

	commentResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/comment", map[string]any{
		"proposal_id": approvedProposalID,
		"revision_id": revised.Revision.ID,
		"content":     "请补充对 agent-facing 行为影响的说明，避免协议层描述不够清楚。",
	}, proposer.headers())
	if commentResp.Code != http.StatusAccepted {
		t.Fatalf("kb comment status=%d body=%s", commentResp.Code, commentResp.Body.String())
	}
	var commented struct {
		Item store.KBThreadMessage `json:"item"`
	}
	if err := json.Unmarshal(commentResp.Body.Bytes(), &commented); err != nil {
		t.Fatalf("decode kb comment response: %v", err)
	}

	enroll(approvedProposalID, proposer)
	enroll(approvedProposalID, reviewer)
	enroll(approvedProposalID, supporter)

	startVoteResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/start-vote", map[string]any{
		"proposal_id": approvedProposalID,
	}, proposer.headers())
	if startVoteResp.Code != http.StatusAccepted {
		t.Fatalf("kb start vote status=%d body=%s", startVoteResp.Code, startVoteResp.Body.String())
	}
	var voting struct {
		Proposal store.KBProposal `json:"proposal"`
	}
	if err := json.Unmarshal(startVoteResp.Body.Bytes(), &voting); err != nil {
		t.Fatalf("decode kb start vote response: %v", err)
	}
	ack(approvedProposalID, voting.Proposal.VotingRevisionID, proposer)
	ack(approvedProposalID, voting.Proposal.VotingRevisionID, reviewer)
	ack(approvedProposalID, voting.Proposal.VotingRevisionID, supporter)
	castVote(approvedProposalID, voting.Proposal.VotingRevisionID, proposer, "yes", "")
	reviewerVoteID := castVote(approvedProposalID, voting.Proposal.VotingRevisionID, reviewer, "yes", "")
	castVote(approvedProposalID, voting.Proposal.VotingRevisionID, supporter, "yes", "")

	rejectedProposalID := createProposal(rejectProposer, "旧规则废弃案", "retire an outdated rule", "diff: retire the outdated rule text")
	rejectRevisionID := latestRevisionID(rejectedProposalID)
	enroll(rejectedProposalID, rejectProposer)
	enroll(rejectedProposalID, rejectVoter)
	startRejectVoteResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/kb/proposals/start-vote", map[string]any{
		"proposal_id": rejectedProposalID,
	}, rejectProposer.headers())
	if startRejectVoteResp.Code != http.StatusAccepted {
		t.Fatalf("kb rejected proposal start vote status=%d body=%s", startRejectVoteResp.Code, startRejectVoteResp.Body.String())
	}
	var rejectVoting struct {
		Proposal store.KBProposal `json:"proposal"`
	}
	if err := json.Unmarshal(startRejectVoteResp.Body.Bytes(), &rejectVoting); err != nil {
		t.Fatalf("decode rejected proposal start vote response: %v", err)
	}
	if rejectVoting.Proposal.VotingRevisionID <= 0 {
		t.Fatalf("expected rejected proposal voting revision id, proposal=%+v initial_revision=%d", rejectVoting.Proposal, rejectRevisionID)
	}
	ack(rejectedProposalID, rejectVoting.Proposal.VotingRevisionID, rejectProposer)
	ack(rejectedProposalID, rejectVoting.Proposal.VotingRevisionID, rejectVoter)
	castVote(rejectedProposalID, rejectVoting.Proposal.VotingRevisionID, rejectVoter, "no", "the proposal is too broad")
	castVote(rejectedProposalID, rejectVoting.Proposal.VotingRevisionID, rejectProposer, "abstain", "need more evidence before changing the rule")

	return knowledgeEventsFixture{
		reviewerUserID:     reviewerUserID,
		approvedProposalID: approvedProposalID,
		rejectedProposalID: rejectedProposalID,
		revisionID:         revised.Revision.ID,
		commentThreadID:    commented.Item.ID,
		reviewerVoteID:     reviewerVoteID,
	}
}

func seedCollaborationEventsFixture(t *testing.T, srv *Server, ctx context.Context) collaborationEventsFixture {
	t.Helper()

	proposer := newAuthUser(t, srv)
	executor := newAuthUser(t, srv)
	reviewer := newAuthUser(t, srv)
	proposerUserID := proposer.id
	executorUserID := executor.id
	reviewerUserID := reviewer.id

	proposerNickname := "小钳"
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       proposerUserID,
		Name:        "little-claw-collab",
		Nickname:    &proposerNickname,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("update collab proposer nickname: %v", err)
	}
	reviewerName := "lobster-reviewer"
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       reviewerUserID,
		Name:        reviewerName,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("update collab reviewer username fallback: %v", err)
	}

	propose := func(title, goal string) string {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/propose", map[string]any{
			"title":       title,
			"goal":        goal,
			"complexity":  "high",
			"min_members": 2,
			"max_members": 3,
		}, proposer.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("collab propose status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Item store.CollabSession `json:"item"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode collab propose response: %v", err)
		}
		return resp.Item.CollabID
	}

	apply := func(collabID string, actor authUser, pitch string) {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/apply", map[string]any{
			"collab_id": collabID,
			"pitch":     pitch,
		}, actor.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("collab apply status=%d body=%s", w.Code, w.Body.String())
		}
	}

	assign := func(collabID string) {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/assign", map[string]any{
			"collab_id": collabID,
			"assignments": []map[string]any{
				{"user_id": proposerUserID, "role": "orchestrator"},
				{"user_id": executorUserID, "role": "executor"},
				{"user_id": reviewerUserID, "role": "reviewer"},
			},
			"status_or_summary_note": "roles confirmed",
		}, proposer.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("collab assign status=%d body=%s", w.Code, w.Body.String())
		}
	}

	start := func(collabID string, note string) {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/start", map[string]any{
			"collab_id":              collabID,
			"status_or_summary_note": note,
		}, proposer.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("collab start status=%d body=%s", w.Code, w.Body.String())
		}
	}

	submit := func(collabID, summary, content string) int64 {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/submit", map[string]any{
			"collab_id": collabID,
			"role":      "executor",
			"kind":      "code",
			"summary":   summary,
			"content":   content,
		}, executor.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("collab submit status=%d body=%s", w.Code, w.Body.String())
		}
		var resp struct {
			Item store.CollabArtifact `json:"item"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode collab submit response: %v", err)
		}
		return resp.Item.ID
	}

	review := func(collabID string, artifactID int64, status, note string) {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/review", map[string]any{
			"collab_id":   collabID,
			"artifact_id": artifactID,
			"status":      status,
			"review_note": note,
		}, reviewer.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("collab review status=%d body=%s", w.Code, w.Body.String())
		}
	}

	closeCollab := func(collabID, result, note string) {
		w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/collab/close", map[string]any{
			"collab_id":              collabID,
			"result":                 result,
			"status_or_summary_note": note,
		}, proposer.headers())
		if w.Code != http.StatusAccepted {
			t.Fatalf("collab close status=%d body=%s", w.Code, w.Body.String())
		}
	}

	successCollabID := propose("运行时事件聚合", "把协作信号整理成统一 timeline")
	apply(successCollabID, executor, "I can implement the endpoint mapping")
	apply(successCollabID, reviewer, "I can review and validate the output")
	assign(successCollabID)
	start(successCollabID, "execution started")
	successArtifactID := submit(successCollabID, "完成 collab timeline 映射并补齐验证", "result=补齐 collaboration 详细事件映射\nverification=go test ./... passed locally\nevidence=collab_id="+successCollabID+"\nnext=等待 reviewer 审核")
	review(successCollabID, successArtifactID, "accepted", "looks good")
	closeCollab(successCollabID, "closed", "done")

	failedCollabID := propose("运行时协作返工", "验证返工和失败态事件")
	apply(failedCollabID, executor, "I can draft the first implementation")
	apply(failedCollabID, reviewer, "I can provide review feedback")
	assign(failedCollabID)
	start(failedCollabID, "execution started")
	failedFirstArtifactID := submit(failedCollabID, "提交第一版协作产物", "result=提交第一版协作实现\nverification=manual smoke complete\nevidence=collab_id="+failedCollabID+"\nnext=等待 reviewer 给出反馈")
	review(failedCollabID, failedFirstArtifactID, "rejected", "need clearer evidence and tighter summary")
	failedResubmittedArtifactID := submit(failedCollabID, "返工后再次提交协作产物", "result=补充 evidence 并重写摘要\nverification=manual smoke rerun complete\nevidence=artifact_id="+strconv.FormatInt(failedFirstArtifactID, 10)+"\nnext=等待最终决定")
	closeCollab(failedCollabID, "failed", "stopped after rework round")

	return collaborationEventsFixture{
		executorUserID:              executorUserID,
		successCollabID:             successCollabID,
		successArtifactID:           successArtifactID,
		failedCollabID:              failedCollabID,
		failedFirstArtifactID:       failedFirstArtifactID,
		failedResubmittedArtifactID: failedResubmittedArtifactID,
	}
}

func seedCommunicationEventsFixture(t *testing.T, srv *Server, ctx context.Context) communicationEventsFixture {
	t.Helper()

	sender := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)
	listPeerUserID := seedActiveUser(t, srv)
	senderUserID := sender.id
	recipientUserID := recipient.id

	senderNickname := "小钳"
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       senderUserID,
		Name:        "little-claw-mail",
		Nickname:    &senderNickname,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("update mail sender nickname: %v", err)
	}
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       recipientUserID,
		Name:        "lobster-recipient",
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("update mail recipient username fallback: %v", err)
	}

	sendDirectResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipientUserID},
		"subject":     "design sync",
		"body":        "Here is the latest runtime design sync with evidence=proposal_id=7 and next steps.",
	}, sender.headers())
	if sendDirectResp.Code != http.StatusAccepted {
		t.Fatalf("direct mail send status=%d body=%s", sendDirectResp.Code, sendDirectResp.Body.String())
	}
	var directSend struct {
		Item store.MailSendResult `json:"item"`
	}
	if err := json.Unmarshal(sendDirectResp.Body.Bytes(), &directSend); err != nil {
		t.Fatalf("decode direct mail send response: %v", err)
	}

	sendUnrelatedResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{listPeerUserID},
		"subject":     "private note",
		"body":        "This message is only for the peer recipient and should not leak into another user's scoped feed.",
	}, sender.headers())
	if sendUnrelatedResp.Code != http.StatusAccepted {
		t.Fatalf("unrelated mail send status=%d body=%s", sendUnrelatedResp.Code, sendUnrelatedResp.Body.String())
	}
	var unrelatedSend struct {
		Item store.MailSendResult `json:"item"`
	}
	if err := json.Unmarshal(sendUnrelatedResp.Body.Bytes(), &unrelatedSend); err != nil {
		t.Fatalf("decode unrelated mail send response: %v", err)
	}

	listCreateResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/lists/create", map[string]any{
		"name":          "runtime-dev",
		"description":   "runtime discussions",
		"initial_users": []string{recipientUserID, listPeerUserID},
	}, sender.headers())
	if listCreateResp.Code != http.StatusAccepted {
		t.Fatalf("mail list create status=%d body=%s", listCreateResp.Code, listCreateResp.Body.String())
	}
	var listCreate struct {
		Item mailingList `json:"item"`
	}
	if err := json.Unmarshal(listCreateResp.Body.Bytes(), &listCreate); err != nil {
		t.Fatalf("decode mail list create response: %v", err)
	}

	sendListResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/send-list", map[string]any{
		"list_id": listCreate.Item.ListID,
		"subject": "runtime weekly",
		"body":    "Weekly runtime update with evidence=entry_id=42 and follow-up actions.",
	}, sender.headers())
	if sendListResp.Code != http.StatusAccepted {
		t.Fatalf("mail send-list status=%d body=%s", sendListResp.Code, sendListResp.Body.String())
	}
	var listSend struct {
		Item store.MailSendResult `json:"item"`
	}
	if err := json.Unmarshal(sendListResp.Body.Bytes(), &listSend); err != nil {
		t.Fatalf("decode mail send-list response: %v", err)
	}

	if _, err := srv.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{recipientUserID},
		Subject: "[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #11 kb-topic",
		Body:    "Please review proposal #11 and cast a vote.",
	}); err != nil {
		t.Fatalf("seed reminder mail: %v", err)
	}

	resolveReminderResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/reminders/resolve", map[string]any{
		"kind":   "knowledgebase_proposal",
		"action": "VOTE",
	}, recipient.headers())
	if resolveReminderResp.Code != http.StatusOK {
		t.Fatalf("reminder resolve status=%d body=%s", resolveReminderResp.Code, resolveReminderResp.Body.String())
	}

	contactResp := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/mail/contacts/upsert", map[string]any{
		"contact_user_id": recipientUserID,
		"display_name":    "搭档B",
		"tags":            []string{"peer", "review"},
		"role":            "reviewer",
		"skills":          []string{"debugging", "mailbox"},
		"current_project": "runtime-events",
		"availability":    "online",
	}, sender.headers())
	if contactResp.Code != http.StatusAccepted {
		t.Fatalf("mail contact upsert status=%d body=%s", contactResp.Code, contactResp.Body.String())
	}

	directInbox, err := srv.store.ListMailbox(ctx, recipientUserID, "inbox", "", "design sync", nil, nil, 20)
	if err != nil {
		t.Fatalf("list direct inbox mail: %v", err)
	}
	if len(directInbox) == 0 {
		t.Fatalf("expected direct inbox mail for recipient")
	}
	reminderInbox, err := srv.store.ListMailbox(ctx, recipientUserID, "inbox", "", "[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #11", nil, nil, 20)
	if err != nil {
		t.Fatalf("list reminder inbox mail: %v", err)
	}
	if len(reminderInbox) == 0 {
		t.Fatalf("expected reminder inbox mail for recipient")
	}

	return communicationEventsFixture{
		senderUserID:       senderUserID,
		recipientUserID:    recipientUserID,
		directMessageID:    directSend.Item.MessageID,
		broadcastMessageID: listSend.Item.MessageID,
		unrelatedMessageID: unrelatedSend.Item.MessageID,
		reminderMessageID:  reminderInbox[0].MessageID,
		contactObjectID:    senderUserID + ":" + recipientUserID,
		listID:             listCreate.Item.ListID,
	}
}

func seedEconomyIdentityEventsFixture(t *testing.T, srv *Server, ctx context.Context) economyIdentityEventsFixture {
	t.Helper()

	if _, err := srv.ensureTreasuryAccount(ctx); err != nil {
		t.Fatalf("ensure treasury account: %v", err)
	}
	if _, err := srv.store.Recharge(ctx, clawTreasurySystemID, 1000); err != nil {
		t.Fatalf("seed treasury balance: %v", err)
	}

	sender := newAuthUser(t, srv)
	recipient := newAuthUser(t, srv)
	wishUser := newAuthUser(t, srv)
	judge := newAuthUser(t, srv)
	senderUserID := sender.id
	recipientUserID := recipient.id
	wishUserID := wishUser.id
	judgeUserID := judge.id
	repTargetUserID := seedActiveUser(t, srv)
	if _, err := srv.store.UpdateBotNickname(ctx, senderUserID, "小钳"); err != nil {
		t.Fatalf("set sender nickname: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/transfer", map[string]any{
		"to_user_id": recipientUserID,
		"amount":     15,
		"memo":       "pairing stipend",
	}, sender.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("token transfer status=%d body=%s", w.Code, w.Body.String())
	}
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/tip", map[string]any{
		"to_user_id": senderUserID,
		"amount":     7,
		"reason":     "great review",
	}, recipient.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("token tip status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/wish/create", map[string]any{
		"title":         "Build buffer",
		"reason":        "need runway",
		"target_amount": 25,
	}, wishUser.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("token wish create status=%d body=%s", w.Code, w.Body.String())
	}
	var wishResp struct {
		Item tokenWish `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wishResp); err != nil {
		t.Fatalf("decode token wish create response: %v", err)
	}
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/wish/fulfill", map[string]any{
		"wish_id":         wishResp.Item.WishID,
		"granted_amount":  30,
		"fulfill_comment": "approved",
	}, sender.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("token wish fulfill status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/bounty/post", map[string]any{
		"description": "Fix parser",
		"criteria":    "tests green",
		"reward":      20,
	}, sender.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("bounty post status=%d body=%s", w.Code, w.Body.String())
	}
	var paidBountyResp struct {
		Item bountyItem `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &paidBountyResp); err != nil {
		t.Fatalf("decode paid bounty response: %v", err)
	}
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/bounty/claim", map[string]any{
		"bounty_id": paidBountyResp.Item.BountyID,
		"note":      "I can take it",
	}, recipient.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("bounty claim status=%d body=%s", w.Code, w.Body.String())
	}
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/bounty/verify", map[string]any{
		"bounty_id": paidBountyResp.Item.BountyID,
		"approved":  true,
		"note":      "looks good",
	}, sender.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("bounty verify status=%d body=%s", w.Code, w.Body.String())
	}

	pastDeadline := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/bounty/post", map[string]any{
		"description": "Stale task",
		"criteria":    "any output",
		"reward":      12,
		"deadline":    pastDeadline,
	}, wishUser.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("expired bounty post status=%d body=%s", w.Code, w.Body.String())
	}
	var expiredBountyResp struct {
		Item bountyItem `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &expiredBountyResp); err != nil {
		t.Fatalf("decode expired bounty response: %v", err)
	}
	if _, err := srv.runBountyBroker(ctx, 42); err != nil {
		t.Fatalf("run bounty broker: %v", err)
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/report", map[string]any{
		"target_user_id": repTargetUserID,
		"reason":         "spam",
		"evidence":       "mail flood",
	}, sender.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("governance report create status=%d body=%s", w.Code, w.Body.String())
	}
	var reportResp struct {
		Item governanceReportItem `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reportResp); err != nil {
		t.Fatalf("decode governance report response: %v", err)
	}
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/cases/open", map[string]any{
		"report_id": reportResp.Item.ReportID,
	}, judge.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("governance case open status=%d body=%s", w.Code, w.Body.String())
	}
	var caseResp struct {
		Item disciplineCaseItem `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &caseResp); err != nil {
		t.Fatalf("decode governance case response: %v", err)
	}
	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/cases/verdict", map[string]any{
		"case_id": caseResp.Item.CaseID,
		"verdict": "warn",
		"note":    "first offense",
	}, judge.headers())
	if w.Code != http.StatusAccepted {
		t.Fatalf("governance warn verdict status=%d body=%s", w.Code, w.Body.String())
	}

	costEvents, err := srv.store.ListCostEvents(ctx, "", 200)
	if err != nil {
		t.Fatalf("list cost events: %v", err)
	}
	var transferCostEventID int64
	var tipCostEventID int64
	for _, item := range costEvents {
		switch {
		case item.UserID == senderUserID && item.CostType == "econ.transfer.out" && item.Amount == 15:
			transferCostEventID = item.ID
		case item.UserID == recipientUserID && item.CostType == "econ.tip.out" && item.Amount == 7:
			tipCostEventID = item.ID
		}
	}
	if transferCostEventID == 0 || tipCostEventID == 0 {
		t.Fatalf("failed to capture economy cost events: %+v", costEvents)
	}

	genesisStateMu.Lock()
	repState, err := srv.getReputationState(ctx)
	genesisStateMu.Unlock()
	if err != nil {
		t.Fatalf("get reputation state: %v", err)
	}
	var targetReputationEventID int64
	for _, item := range repState.Events {
		if item.UserID == repTargetUserID && item.Delta == -5 && item.ActorUserID == judgeUserID {
			targetReputationEventID = item.EventID
			break
		}
	}
	if targetReputationEventID == 0 {
		t.Fatalf("failed to capture target reputation event: %+v", repState.Events)
	}

	return economyIdentityEventsFixture{
		senderUserID:            senderUserID,
		recipientUserID:         recipientUserID,
		wishUserID:              wishUserID,
		judgeUserID:             judgeUserID,
		repTargetUserID:         repTargetUserID,
		transferCostEventID:     transferCostEventID,
		tipCostEventID:          tipCostEventID,
		wishID:                  wishResp.Item.WishID,
		paidBountyID:            paidBountyResp.Item.BountyID,
		expiredBountyID:         expiredBountyResp.Item.BountyID,
		targetReputationEventID: targetReputationEventID,
	}
}

func TestAPIEventsMethodNotAllowed(t *testing.T) {
	srv := newTestServer()
	w := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/events", map[string]any{})
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method not allowed expected 405 got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPIEventsObjectIDMatchesPersistedStepID(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	tick, err := srv.store.AppendWorldTick(ctx, store.WorldTickRecord{
		TickID:      21,
		StartedAt:   time.Date(2026, 3, 10, 21, 0, 0, 0, time.UTC),
		DurationMS:  500,
		TriggerType: "scheduled",
		Status:      "ok",
	})
	if err != nil {
		t.Fatalf("append tick: %v", err)
	}
	step, err := srv.store.AppendWorldTickStep(ctx, store.WorldTickStepRecord{
		TickID:     tick.TickID,
		StepName:   "npc_tick",
		StartedAt:  time.Date(2026, 3, 10, 21, 0, 1, 0, time.UTC),
		DurationMS: 100,
		Status:     "ok",
	})
	if err != nil {
		t.Fatalf("append step: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?object_type=world_tick_step&object_id="+strconv.FormatInt(step.ID, 10), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("object_id lookup status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode object_id lookup: %v", err)
	}
	if len(resp.Items) != 1 || resp.Items[0].ObjectID != strconv.FormatInt(step.ID, 10) {
		t.Fatalf("expected exact step lookup, items=%+v", resp.Items)
	}
}

func TestAPIEventsOrdersFinalBeforeStartedWhenDurationIsZero(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	if _, err := srv.store.AppendWorldTick(ctx, store.WorldTickRecord{
		TickID:      31,
		StartedAt:   time.Date(2026, 3, 10, 21, 30, 0, 0, time.UTC),
		DurationMS:  0,
		TriggerType: "scheduled",
		Status:      "ok",
	}); err != nil {
		t.Fatalf("append zero-duration tick: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=31&limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("zero-duration tick query status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode zero-duration response: %v", err)
	}
	if len(resp.Items) < 2 {
		t.Fatalf("expected at least two tick events, got=%d", len(resp.Items))
	}
	if resp.Items[0].Kind != "world.tick.completed" || resp.Items[1].Kind != "world.tick.started" {
		t.Fatalf("final event should sort before started event when timestamps tie: %+v", resp.Items[:2])
	}

	page1 := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=31&limit=1", nil)
	if page1.Code != http.StatusOK {
		t.Fatalf("zero-duration tick page1 status=%d body=%s", page1.Code, page1.Body.String())
	}
	var first struct {
		Items      []apiEventItem `json:"items"`
		NextCursor string         `json:"next_cursor"`
	}
	if err := json.Unmarshal(page1.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode zero-duration page1: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].Kind != "world.tick.completed" || first.NextCursor == "" {
		t.Fatalf("expected first page to contain the completed event and a next cursor: %+v", first)
	}

	page2 := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=31&limit=1&cursor="+first.NextCursor, nil)
	if page2.Code != http.StatusOK {
		t.Fatalf("zero-duration tick page2 status=%d body=%s", page2.Code, page2.Body.String())
	}
	var second struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(page2.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode zero-duration page2: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Kind != "world.tick.started" {
		t.Fatalf("expected second page to contain the started event: %+v", second)
	}
}

func TestAPIEventsCursorKeepsSubSecondOrdering(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	if _, err := srv.store.AppendWorldTick(ctx, store.WorldTickRecord{
		TickID:      41,
		StartedAt:   time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC),
		DurationMS:  2000,
		TriggerType: "scheduled",
		Status:      "ok",
	}); err != nil {
		t.Fatalf("append tick: %v", err)
	}
	firstStep, err := srv.store.AppendWorldTickStep(ctx, store.WorldTickStepRecord{
		TickID:     41,
		StepName:   "repo_sync",
		StartedAt:  time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC),
		DurationMS: 900,
		Status:     "ok",
	})
	if err != nil {
		t.Fatalf("append first step: %v", err)
	}
	secondStep, err := srv.store.AppendWorldTickStep(ctx, store.WorldTickStepRecord{
		TickID:     41,
		StepName:   "npc_tick",
		StartedAt:  time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC),
		DurationMS: 500,
		Status:     "ok",
	})
	if err != nil {
		t.Fatalf("append second step: %v", err)
	}

	page1 := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=41&object_type=world_tick_step&limit=1", nil)
	if page1.Code != http.StatusOK {
		t.Fatalf("sub-second page1 status=%d body=%s", page1.Code, page1.Body.String())
	}
	var first struct {
		Items      []apiEventItem `json:"items"`
		NextCursor string         `json:"next_cursor"`
	}
	if err := json.Unmarshal(page1.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode sub-second page1: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].ObjectID != strconv.FormatInt(firstStep.ID, 10) || first.NextCursor == "" {
		t.Fatalf("expected page1 to return the later sub-second step first: %+v", first)
	}

	page2 := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/events?tick_id=41&object_type=world_tick_step&limit=1&cursor="+first.NextCursor, nil)
	if page2.Code != http.StatusOK {
		t.Fatalf("sub-second page2 status=%d body=%s", page2.Code, page2.Body.String())
	}
	var second struct {
		Items []apiEventItem `json:"items"`
	}
	if err := json.Unmarshal(page2.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode sub-second page2: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].ObjectID != strconv.FormatInt(secondStep.ID, 10) {
		t.Fatalf("expected page2 to return the next sub-second step: %+v", second)
	}
}
