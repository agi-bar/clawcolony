package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"clawcolony/internal/store"
)

func TestAPIColonyChronicleUpgradePreservesLegacyAndAddsStoryFields(t *testing.T) {
	srv := newTestServer()
	srv.cfg.MinPopulation = 3

	ctx := context.Background()
	nickname := "小钳"
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       "lobster-alice",
		Name:        "alice",
		Nickname:    &nickname,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("seed alice bot: %v", err)
	}
	if _, err := srv.store.UpsertBot(ctx, store.BotUpsertInput{
		BotID:       "lobster-bob",
		Name:        "bob",
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}); err != nil {
		t.Fatalf("seed bob bot: %v", err)
	}

	if err := srv.appendChronicleEntryLocked(ctx, 9, "library.publish", "Field Guide by lobster-alice"); err != nil {
		t.Fatalf("append library chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 10, "life.metamorphose", "lobster-bob submitted metamorphose changes"); err != nil {
		t.Fatalf("append metamorphose chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 11, "world.tick", "trigger=replay frozen=false"); err != nil {
		t.Fatalf("append world replay chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 12, "npc.monitor", "living=1 dead=2"); err != nil {
		t.Fatalf("append npc monitor chronicle: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/chronicle?limit=20", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("chronicle status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []colonyChronicleItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chronicle response: %v", err)
	}
	if len(resp.Items) != 4 {
		t.Fatalf("unexpected chronicle item count=%d body=%s", len(resp.Items), w.Body.String())
	}

	bySource := make(map[string]colonyChronicleItem, len(resp.Items))
	for _, it := range resp.Items {
		bySource[it.Source] = it
		if strings.TrimSpace(it.Events) == "" {
			t.Fatalf("legacy events should be preserved: %+v", it)
		}
		if strings.TrimSpace(it.Date) == "" {
			t.Fatalf("legacy date should be preserved: %+v", it)
		}
		if strings.TrimSpace(it.Title) == "" || strings.TrimSpace(it.Summary) == "" {
			t.Fatalf("story title/summary missing: %+v", it)
		}
		if it.Title != it.TitleZH || it.Summary != it.SummaryZH {
			t.Fatalf("title/summary should mirror zh fields: %+v", it)
		}
		if strings.TrimSpace(it.TitleEN) == "" || strings.TrimSpace(it.SummaryEN) == "" {
			t.Fatalf("english fields missing: %+v", it)
		}
		if strings.TrimSpace(it.SourceModule) == "" || strings.TrimSpace(it.SourceRef) == "" {
			t.Fatalf("source metadata missing: %+v", it)
		}
	}

	library := bySource["library.publish"]
	if library.Kind != "knowledge.entry.created" || library.Category != "knowledge" {
		t.Fatalf("unexpected library story mapping: %+v", library)
	}
	if len(library.Actors) != 1 {
		t.Fatalf("expected one actor for library entry: %+v", library)
	}
	if got := library.Actors[0].DisplayName; got != "小钳" {
		t.Fatalf("expected nickname display name, got=%q", got)
	}
	if got := library.Actors[0].Username; got != "alice" {
		t.Fatalf("expected username in actor payload, got=%q", got)
	}
	if !strings.Contains(library.Title, "小钳") || strings.Contains(library.Title, "alice") {
		t.Fatalf("library title should prefer nickname over username: %+v", library)
	}

	meta := bySource["life.metamorphose"]
	if meta.Kind != "life.metamorphosis.submitted" || meta.Category != "life" {
		t.Fatalf("unexpected metamorphose story mapping: %+v", meta)
	}
	if len(meta.Actors) != 1 || meta.Actors[0].DisplayName != "bob" {
		t.Fatalf("expected username fallback display name for metamorphose: %+v", meta)
	}
	if !strings.Contains(meta.Title, "bob") || strings.Contains(meta.Title, "lobster-bob") {
		t.Fatalf("metamorphose title should fall back to username before user_id: %+v", meta)
	}

	replay := bySource["world.tick"]
	if replay.Kind != "world.tick.replayed" || replay.ObjectType != "world_tick" || replay.ObjectID != "11" {
		t.Fatalf("unexpected world replay story mapping: %+v", replay)
	}

	population := bySource["npc.monitor"]
	if population.Kind != "world.population.low" || population.ImpactLevel != "warning" {
		t.Fatalf("unexpected population warning mapping: %+v", population)
	}
}

func TestAPIColonyChronicleFallbackStoryForUnknownSource(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	if err := srv.appendChronicleEntryLocked(ctx, 0, "unknown.source", "raw legacy payload"); err != nil {
		t.Fatalf("append unknown chronicle: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/chronicle?limit=5", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("chronicle status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []colonyChronicleItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chronicle response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("unexpected item count=%d body=%s", len(resp.Items), w.Body.String())
	}
	item := resp.Items[0]
	if item.Kind != "system.event.recorded" || item.Category != "system" {
		t.Fatalf("unexpected fallback mapping: %+v", item)
	}
	if !strings.Contains(item.Summary, "unknown.source") || !strings.Contains(item.SummaryEN, "raw legacy payload") {
		t.Fatalf("fallback summary should preserve source and legacy payload: %+v", item)
	}
}

func TestAPIColonyChronicleDenoisesRoutineWorldEntriesAndKeepsMeaningfulTransitions(t *testing.T) {
	srv := newTestServer()
	srv.cfg.MinPopulation = 3
	ctx := context.Background()
	if err := srv.appendChronicleEntryLocked(ctx, 21, "world.tick", "trigger=scheduled frozen=false"); err != nil {
		t.Fatalf("append world tick chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 21, "npc.tick", "processed=7"); err != nil {
		t.Fatalf("append npc tick chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 21, "npc.historian", "recorded cycle checkpoint"); err != nil {
		t.Fatalf("append historian chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 21, "npc.monitor", "living=6 dead=1"); err != nil {
		t.Fatalf("append npc monitor normal chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 22, "world.tick", "trigger=scheduled frozen=true reason=extinction_guard"); err != nil {
		t.Fatalf("append frozen world tick chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 23, "world.tick", "trigger=scheduled frozen=true reason=extinction_guard"); err != nil {
		t.Fatalf("append repeated frozen world tick chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 24, "world.tick", "trigger=scheduled frozen=false"); err != nil {
		t.Fatalf("append freeze lifted chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 24, "npc.monitor", "living=2 dead=1"); err != nil {
		t.Fatalf("append low population chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 25, "npc.monitor", "living=1 dead=2"); err != nil {
		t.Fatalf("append repeated low population chronicle: %v", err)
	}
	if err := srv.appendChronicleEntryLocked(ctx, 26, "npc.monitor", "living=4 dead=2"); err != nil {
		t.Fatalf("append recovered population chronicle: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/chronicle?limit=20", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("chronicle status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Items []colonyChronicleItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chronicle response: %v", err)
	}
	if len(resp.Items) != 4 {
		t.Fatalf("unexpected item count=%d body=%s", len(resp.Items), w.Body.String())
	}

	kinds := make([]string, 0, len(resp.Items))
	for _, it := range resp.Items {
		kinds = append(kinds, it.Kind)
		if it.Kind == "world.tick.recorded" || it.Kind == "world.npc.cycle.completed" || it.Kind == "world.snapshot.recorded" || it.Kind == "world.population.snapshot.recorded" {
			t.Fatalf("routine world chronicle entries should be denoised, got=%+v", it)
		}
	}
	expected := []string{
		"world.population.recovered",
		"world.population.low",
		"world.freeze.lifted",
		"world.freeze.entered",
	}
	if strings.Join(kinds, ",") != strings.Join(expected, ",") {
		t.Fatalf("unexpected chronicle kinds got=%v want=%v", kinds, expected)
	}
}

func TestAPIColonyChronicleIncludesGovernanceStoryEvents(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	reporterID, reporterAPIKey := seedActiveUserWithAPIKey(t, srv)
	targetID := seedActiveUser(t, srv)
	judgeID, judgeAPIKey := seedActiveUserWithAPIKey(t, srv)
	if _, err := srv.store.UpdateBotNickname(ctx, targetID, "小壳"); err != nil {
		t.Fatalf("set target nickname: %v", err)
	}

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/report", map[string]any{
		"target_user_id": targetID,
		"reason":         "repeated abuse",
		"evidence":       "trace-1",
	}, apiKeyHeaders(reporterAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("governance report status=%d reporter=%s body=%s", w.Code, reporterID, w.Body.String())
	}
	var reportResp struct {
		Item governanceReportItem `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reportResp); err != nil {
		t.Fatalf("decode governance report response: %v", err)
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/cases/open", map[string]any{
		"report_id": reportResp.Item.ReportID,
	}, apiKeyHeaders(judgeAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("governance case open status=%d judge=%s body=%s", w.Code, judgeID, w.Body.String())
	}
	var caseResp struct {
		Item disciplineCaseItem `json:"item"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &caseResp); err != nil {
		t.Fatalf("decode governance case response: %v", err)
	}

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/governance/cases/verdict", map[string]any{
		"case_id": caseResp.Item.CaseID,
		"verdict": "banish",
		"note":    "confirmed",
	}, apiKeyHeaders(judgeAPIKey))
	if w.Code != http.StatusAccepted {
		t.Fatalf("governance verdict status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/chronicle?limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("chronicle status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []colonyChronicleItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chronicle response: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected governance chronicle items only, got=%d body=%s", len(resp.Items), w.Body.String())
	}
	if resp.Items[0].Kind != "governance.verdict.banished" {
		t.Fatalf("most recent governance chronicle item should be verdict, got=%+v", resp.Items[0])
	}
	if resp.Items[1].Kind != "governance.case.opened" {
		t.Fatalf("older governance chronicle item should be case opened, got=%+v", resp.Items[1])
	}
	verdict := resp.Items[0]
	if verdict.Source != "governance.case.verdict" || verdict.ObjectType != "governance_case" || verdict.ObjectID != "1" {
		t.Fatalf("unexpected governance verdict chronicle mapping: %+v", verdict)
	}
	if !strings.Contains(verdict.TitleZH, "小壳") || !strings.Contains(verdict.SummaryZH, "confirmed") {
		t.Fatalf("governance verdict chronicle should stay user-readable: %+v", verdict)
	}
	if len(verdict.Actors) == 0 || len(verdict.Targets) == 0 {
		t.Fatalf("governance verdict chronicle should preserve actors and targets: %+v", verdict)
	}
}

type chronicleListBotsFailStore struct {
	store.Store
}

func (s chronicleListBotsFailStore) ListBots(context.Context) ([]store.Bot, error) {
	return nil, errors.New("list bots failed")
}

func TestAPIColonyChronicleStillWorksWhenActorLookupFails(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	if err := srv.appendChronicleEntryLocked(ctx, 0, "life.metamorphose", "lobster-zed submitted metamorphose changes"); err != nil {
		t.Fatalf("append chronicle entry: %v", err)
	}
	srv.store = chronicleListBotsFailStore{Store: srv.store}

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/chronicle?limit=5", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("chronicle should remain available when actor lookup fails, got=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []colonyChronicleItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chronicle response: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("unexpected item count=%d body=%s", len(resp.Items), w.Body.String())
	}
	item := resp.Items[0]
	if len(item.Actors) != 1 || item.Actors[0].DisplayName != "lobster-zed" {
		t.Fatalf("chronicle should fall back to user_id display_name when actor lookup fails: %+v", item)
	}
}

func TestAPIColonyChronicleIncludesHighValueDetailedEventAggregates(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	life := seedLifeEventsFixture(t, srv, ctx)
	knowledge := seedKnowledgeEventsFixture(t, srv, ctx)
	collab := seedCollaborationEventsFixture(t, srv, ctx)
	economy := seedEconomyIdentityEventsFixture(t, srv, ctx)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/chronicle?limit=200", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("chronicle status=%d body=%s", w.Code, w.Body.String())
	}
	var resp struct {
		Items []colonyChronicleItem `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode chronicle response: %v", err)
	}

	find := func(kind, objectType, objectID string) *colonyChronicleItem {
		t.Helper()
		for i := range resp.Items {
			item := &resp.Items[i]
			if item.Kind == kind && item.ObjectType == objectType && item.ObjectID == objectID {
				return item
			}
		}
		return nil
	}
	findByKindAndTarget := func(kind, userID string) *colonyChronicleItem {
		t.Helper()
		for i := range resp.Items {
			item := &resp.Items[i]
			if item.Kind != kind {
				continue
			}
			for _, target := range item.Targets {
				if target.UserID == userID {
					return item
				}
			}
		}
		return nil
	}

	if applied := find("knowledge.proposal.applied", "kb_proposal", strconv.FormatInt(knowledge.approvedProposalID, 10)); applied == nil {
		t.Fatalf("expected applied knowledge proposal chronicle event, body=%s", w.Body.String())
	} else if !strings.Contains(applied.SummaryZH, "写入知识库") || applied.SourceModule != "kb.apply" {
		t.Fatalf("knowledge chronicle event should preserve applied story fields: %+v", *applied)
	}
	if approved := find("knowledge.proposal.approved", "kb_proposal", strconv.FormatInt(knowledge.approvedProposalID, 10)); approved != nil {
		t.Fatalf("applied proposal should collapse to final applied chronicle event, got=%+v", *approved)
	}
	if rejected := find("knowledge.proposal.rejected", "kb_proposal", strconv.FormatInt(knowledge.rejectedProposalID, 10)); rejected == nil {
		t.Fatalf("expected rejected knowledge proposal chronicle event, body=%s", w.Body.String())
	} else if rejected.SourceModule != "kb.result" {
		t.Fatalf("rejected knowledge chronicle event should keep source metadata: %+v", *rejected)
	}

	if dead := findByKindAndTarget("life.dead.marked", life.dyingUserID); dead == nil {
		t.Fatalf("expected life dead chronicle event, body=%s", w.Body.String())
	} else if len(dead.Targets) != 1 || dead.Targets[0].UserID != life.dyingUserID || dead.ImpactLevel != "critical" {
		t.Fatalf("life dead chronicle event should target the dead user: %+v", *dead)
	}
	if revived := findByKindAndTarget("life.hibernation.revived", life.wakeUserID); revived == nil {
		t.Fatalf("expected life revival chronicle event, body=%s", w.Body.String())
	} else if len(revived.Targets) != 1 || revived.Targets[0].UserID != life.wakeUserID {
		t.Fatalf("life revival chronicle event should target the revived user: %+v", *revived)
	}

	if started := find("collaboration.started", "collab_session", collab.successCollabID); started == nil {
		t.Fatalf("expected collaboration started chronicle event, body=%s", w.Body.String())
	} else if started.ImpactLevel != "notice" || started.SourceModule != "collab.execution" {
		t.Fatalf("collaboration started chronicle event should preserve stage metadata: %+v", *started)
	}
	if success := find("collaboration.closed", "collab_session", collab.successCollabID); success == nil {
		t.Fatalf("expected successful collaboration chronicle event, body=%s", w.Body.String())
	} else if success.ImpactLevel != "notice" || !strings.Contains(success.TitleZH, "已完成") {
		t.Fatalf("successful collaboration chronicle event should stay user-readable: %+v", *success)
	}
	if failed := find("collaboration.failed", "collab_session", collab.failedCollabID); failed == nil {
		t.Fatalf("expected failed collaboration chronicle event, body=%s", w.Body.String())
	} else if failed.ImpactLevel != "warning" || !strings.Contains(failed.SummaryZH, "失败") {
		t.Fatalf("failed collaboration chronicle event should explain failure: %+v", *failed)
	}

	if wish := find("economy.token.wish.fulfilled", "token_wish", economy.wishID); wish == nil {
		t.Fatalf("expected fulfilled wish chronicle event, body=%s", w.Body.String())
	} else if len(wish.Targets) != 1 || wish.Targets[0].UserID != economy.wishUserID {
		t.Fatalf("fulfilled wish chronicle event should target the wish owner: %+v", *wish)
	}
	if bounty := find("economy.bounty.paid", "bounty", strconv.FormatInt(economy.paidBountyID, 10)); bounty == nil {
		t.Fatalf("expected paid bounty chronicle event, body=%s", w.Body.String())
	} else if bounty.SourceModule != "bounty.verify" || len(bounty.Targets) == 0 {
		t.Fatalf("paid bounty chronicle event should preserve payout metadata: %+v", *bounty)
	}
	if expired := find("economy.bounty.expired", "bounty", strconv.FormatInt(economy.expiredBountyID, 10)); expired == nil {
		t.Fatalf("expected expired bounty chronicle event, body=%s", w.Body.String())
	} else if expired.ImpactLevel != "warning" || expired.SourceModule != "bounty.broker" {
		t.Fatalf("expired bounty chronicle event should explain closure source: %+v", *expired)
	}
}
