package server

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"
	"time"

	"clawcolony/internal/store"
)

type colonyChronicleActor struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username,omitempty"`
	Nickname    string `json:"nickname,omitempty"`
	DisplayName string `json:"display_name"`
}

type colonyChronicleItem struct {
	ID           int64                  `json:"id"`
	TickID       int64                  `json:"tick_id"`
	Source       string                 `json:"source"`
	Date         string                 `json:"date"`
	Events       string                 `json:"events"`
	Kind         string                 `json:"kind"`
	Category     string                 `json:"category"`
	Title        string                 `json:"title"`
	Summary      string                 `json:"summary"`
	TitleZH      string                 `json:"title_zh"`
	SummaryZH    string                 `json:"summary_zh"`
	TitleEN      string                 `json:"title_en"`
	SummaryEN    string                 `json:"summary_en"`
	Actors       []colonyChronicleActor `json:"actors,omitempty"`
	Targets      []colonyChronicleActor `json:"targets,omitempty"`
	ObjectType   string                 `json:"object_type,omitempty"`
	ObjectID     string                 `json:"object_id,omitempty"`
	ImpactLevel  string                 `json:"impact_level,omitempty"`
	SourceModule string                 `json:"source_module,omitempty"`
	SourceRef    string                 `json:"source_ref,omitempty"`
	Visibility   string                 `json:"visibility,omitempty"`
	sortTime     time.Time              `json:"-"`
}

type chronicleAggregateState struct {
	FreezeActive        bool
	PopulationLowActive bool
}

func chronicleActorIndex(bots []store.Bot) map[string]colonyChronicleActor {
	out := make(map[string]colonyChronicleActor, len(bots))
	for _, it := range bots {
		uid := strings.TrimSpace(it.BotID)
		if uid == "" {
			continue
		}
		username := strings.TrimSpace(it.Name)
		nickname := strings.TrimSpace(it.Nickname)
		out[uid] = colonyChronicleActor{
			UserID:      uid,
			Username:    username,
			Nickname:    nickname,
			DisplayName: chronicleDisplayName(nickname, username, uid),
		}
	}
	return out
}

func chronicleDisplayName(nickname, username, userID string) string {
	if v := strings.TrimSpace(nickname); v != "" {
		return v
	}
	if v := strings.TrimSpace(username); v != "" {
		return v
	}
	return strings.TrimSpace(userID)
}

func chronicleActorForUser(userID string, idx map[string]colonyChronicleActor) colonyChronicleActor {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return colonyChronicleActor{}
	}
	if it, ok := idx[uid]; ok {
		return it
	}
	return colonyChronicleActor{
		UserID:      uid,
		DisplayName: uid,
	}
}

func (s *Server) buildColonyChronicleItem(entry chronicleEntry, actors map[string]colonyChronicleActor) colonyChronicleItem {
	item := colonyChronicleItem{
		ID:           entry.ID,
		TickID:       entry.TickID,
		Source:       entry.Source,
		Date:         entry.CreatedAt.Format(timeRFC3339NoMono),
		Events:       entry.Summary,
		SourceModule: strings.TrimSpace(entry.Source),
		SourceRef:    fmt.Sprintf("chronicle:%d", entry.ID),
		Visibility:   "community",
		ImpactLevel:  "info",
		sortTime:     entry.CreatedAt.UTC(),
	}
	switch strings.TrimSpace(entry.Source) {
	case "library.publish":
		return buildChronicleLibraryPublishItem(item, entry, actors)
	case "life.metamorphose":
		return buildChronicleLifeMetamorphoseItem(item, entry, actors)
	case "world.tick":
		return s.buildChronicleWorldTickItem(item, entry)
	case "npc.tick":
		return buildChronicleNPCTickItem(item, entry)
	case "npc.monitor":
		return s.buildChronicleNPCMonitorItem(item, entry)
	case "npc.historian":
		return buildChronicleHistorianItem(item, entry)
	default:
		return buildChronicleFallbackItem(item)
	}
}

func (s *Server) buildColonyChronicleItems(entries []chronicleEntry, actors map[string]colonyChronicleActor) []colonyChronicleItem {
	if len(entries) == 0 {
		return []colonyChronicleItem{}
	}
	ordered := append([]chronicleEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
	})

	state := chronicleAggregateState{}
	out := make([]colonyChronicleItem, 0, len(ordered))
	for _, entry := range ordered {
		item, keep := s.buildAggregatedColonyChronicleItem(entry, actors, &state)
		if keep {
			out = append(out, item)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func sortColonyChronicleItems(items []colonyChronicleItem) {
	sort.SliceStable(items, func(i, j int) bool {
		ti := items[i].sortTime.UTC()
		tj := items[j].sortTime.UTC()
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return items[i].ID > items[j].ID
	})
}

func (s *Server) buildAggregatedColonyChronicleItem(entry chronicleEntry, actors map[string]colonyChronicleActor, state *chronicleAggregateState) (colonyChronicleItem, bool) {
	item := s.buildColonyChronicleItem(entry, actors)
	switch item.Kind {
	case "world.tick.replayed":
		return item, true
	case "world.tick.recorded":
		return s.aggregateChronicleWorldTickItem(item, entry, state)
	case "world.npc.cycle.completed", "world.snapshot.recorded":
		return colonyChronicleItem{}, false
	case "world.population.low":
		if state.PopulationLowActive {
			return colonyChronicleItem{}, false
		}
		state.PopulationLowActive = true
		return item, true
	case "world.population.snapshot.recorded":
		if !state.PopulationLowActive {
			return colonyChronicleItem{}, false
		}
		state.PopulationLowActive = false
		return s.buildChroniclePopulationRecoveredItem(item, entry), true
	default:
		return item, true
	}
}

func (s *Server) aggregateChronicleWorldTickItem(item colonyChronicleItem, entry chronicleEntry, state *chronicleAggregateState) (colonyChronicleItem, bool) {
	trigger, frozen, reason, ok := parseLegacyWorldTickSummary(entry.Summary)
	if !ok {
		return item, true
	}
	if trigger == "replay" {
		return item, true
	}
	if frozen {
		if state.FreezeActive {
			return colonyChronicleItem{}, false
		}
		state.FreezeActive = true
		return buildChronicleFreezeEnteredItem(item, entry, reason), true
	}
	if !state.FreezeActive {
		return colonyChronicleItem{}, false
	}
	state.FreezeActive = false
	return buildChronicleFreezeLiftedItem(item, entry, trigger), true
}

const timeRFC3339NoMono = "2006-01-02T15:04:05Z07:00"

func buildChronicleLibraryPublishItem(item colonyChronicleItem, entry chronicleEntry, actors map[string]colonyChronicleActor) colonyChronicleItem {
	title, authorID, ok := parseLegacyLibraryPublishSummary(entry.Summary)
	if !ok {
		return buildChronicleFallbackItem(item)
	}
	author := chronicleActorForUser(authorID, actors)
	item.Kind = "knowledge.entry.created"
	item.Category = "knowledge"
	item.TitleZH = fmt.Sprintf("%s 发布了知识条目《%s》", author.DisplayName, title)
	item.SummaryZH = fmt.Sprintf("%s 发布了知识条目《%s》。这条知识已被记录到社区共享历史中。", author.DisplayName, title)
	item.TitleEN = fmt.Sprintf("%s published the knowledge entry \"%s\"", author.DisplayName, title)
	item.SummaryEN = fmt.Sprintf("%s published the knowledge entry \"%s\". It has been recorded in the colony's shared history.", author.DisplayName, title)
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	item.Actors = []colonyChronicleActor{author}
	item.ObjectType = "knowledge_entry"
	return item
}

func buildChronicleLifeMetamorphoseItem(item colonyChronicleItem, entry chronicleEntry, actors map[string]colonyChronicleActor) colonyChronicleItem {
	userID, ok := parseLegacyMetamorphoseSummary(entry.Summary)
	if !ok {
		return buildChronicleFallbackItem(item)
	}
	actor := chronicleActorForUser(userID, actors)
	item.Kind = "life.metamorphosis.submitted"
	item.Category = "life"
	item.ImpactLevel = "notice"
	item.TitleZH = fmt.Sprintf("%s 提交了蜕变申请", actor.DisplayName)
	item.SummaryZH = fmt.Sprintf("%s 提交了新的蜕变变更，后续可以继续查看并处理这次变化。", actor.DisplayName)
	item.TitleEN = fmt.Sprintf("%s submitted a metamorphosis request", actor.DisplayName)
	item.SummaryEN = fmt.Sprintf("%s submitted a new metamorphosis change. The colony can continue reviewing and handling this transformation.", actor.DisplayName)
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	item.Actors = []colonyChronicleActor{actor}
	item.Targets = []colonyChronicleActor{actor}
	item.ObjectType = "life_metamorphosis"
	return item
}

func (s *Server) buildChronicleWorldTickItem(item colonyChronicleItem, entry chronicleEntry) colonyChronicleItem {
	trigger, frozen, reason, ok := parseLegacyWorldTickSummary(entry.Summary)
	if !ok {
		return buildChronicleFallbackItem(item)
	}
	item.Category = "world"
	item.ObjectType = "world_tick"
	if entry.TickID > 0 {
		item.ObjectID = strconv.FormatInt(entry.TickID, 10)
	}
	if trigger == "replay" {
		item.Kind = "world.tick.replayed"
		item.ImpactLevel = "notice"
		item.TitleZH = fmt.Sprintf("世界历史已回放（第 %d 次周期）", entry.TickID)
		item.SummaryZH = fmt.Sprintf("系统回放了第 %d 次世界周期，并重新记录了这段历史。", entry.TickID)
		item.TitleEN = fmt.Sprintf("World history was replayed (tick %d)", entry.TickID)
		item.SummaryEN = fmt.Sprintf("The system replayed world tick %d and recorded the refreshed history.", entry.TickID)
		item.Title = item.TitleZH
		item.Summary = item.SummaryZH
		return item
	}
	item.Kind = "world.tick.recorded"
	if frozen {
		item.ImpactLevel = "warning"
		item.TitleZH = "世界仍处于冻结状态"
		item.TitleEN = "The world remained frozen"
	} else {
		item.TitleZH = "记录了一次世界周期"
		item.TitleEN = "A world cycle was recorded"
	}
	triggerZH := chronicleTriggerLabelZH(trigger)
	triggerEN := chronicleTriggerLabelEN(trigger)
	stateZH := "正常运行"
	stateEN := "running normally"
	if frozen {
		stateZH = "冻结中"
		stateEN = "frozen"
	}
	item.SummaryZH = fmt.Sprintf("第 %d 次世界周期已记录。本次由%s触发，当前世界状态为%s。", entry.TickID, triggerZH, stateZH)
	item.SummaryEN = fmt.Sprintf("World tick %d was recorded. It was triggered by %s, and the world is currently %s.", entry.TickID, triggerEN, stateEN)
	if reason = strings.TrimSpace(reason); reason != "" {
		item.SummaryZH += " 原因：" + reason + "。"
		item.SummaryEN += " Reason: " + reason + "."
	}
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func buildChronicleFreezeEnteredItem(item colonyChronicleItem, entry chronicleEntry, reason string) colonyChronicleItem {
	item.Kind = "world.freeze.entered"
	item.Category = "world"
	item.ImpactLevel = "warning"
	item.TitleZH = "世界进入冻结状态"
	item.SummaryZH = fmt.Sprintf("第 %d 次世界周期检测到世界进入冻结状态，主要执行流程已暂停。", entry.TickID)
	item.TitleEN = "The world entered a frozen state"
	item.SummaryEN = fmt.Sprintf("World tick %d detected that the world entered a frozen state and paused the main execution flow.", entry.TickID)
	if reason = strings.TrimSpace(reason); reason != "" {
		item.SummaryZH += " 原因：" + reason + "。"
		item.SummaryEN += " Reason: " + reason + "."
	}
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func buildChronicleFreezeLiftedItem(item colonyChronicleItem, entry chronicleEntry, trigger string) colonyChronicleItem {
	item.Kind = "world.freeze.lifted"
	item.Category = "world"
	item.ImpactLevel = "notice"
	item.TitleZH = "世界恢复运行"
	item.SummaryZH = fmt.Sprintf("第 %d 次世界周期确认冻结状态已解除，世界恢复运行。本次由%s触发。", entry.TickID, chronicleTriggerLabelZH(trigger))
	item.TitleEN = "The world resumed running"
	item.SummaryEN = fmt.Sprintf("World tick %d confirmed that the frozen state was lifted and normal operation resumed. It was triggered by %s.", entry.TickID, chronicleTriggerLabelEN(trigger))
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func buildChronicleNPCTickItem(item colonyChronicleItem, entry chronicleEntry) colonyChronicleItem {
	processed, ok := parseLegacyProcessedSummary(entry.Summary)
	if !ok {
		return buildChronicleFallbackItem(item)
	}
	item.Kind = "world.npc.cycle.completed"
	item.Category = "world"
	item.ObjectType = "world_tick"
	if entry.TickID > 0 {
		item.ObjectID = strconv.FormatInt(entry.TickID, 10)
	}
	item.TitleZH = "自动周期已完成"
	item.SummaryZH = fmt.Sprintf("本轮自动周期共处理了 %d 项任务。", processed)
	item.TitleEN = "The automated cycle completed"
	item.SummaryEN = fmt.Sprintf("The automated cycle processed %d tasks in this round.", processed)
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func (s *Server) buildChronicleNPCMonitorItem(item colonyChronicleItem, entry chronicleEntry) colonyChronicleItem {
	living, dead, ok := parseLegacyPopulationSummary(entry.Summary)
	if !ok {
		return buildChronicleFallbackItem(item)
	}
	item.Category = "world"
	item.ObjectType = "world_tick"
	if entry.TickID > 0 {
		item.ObjectID = strconv.FormatInt(entry.TickID, 10)
	}
	minPopulation := s.desiredMinPopulation()
	if minPopulation > 0 && living < minPopulation {
		item.Kind = "world.population.low"
		item.ImpactLevel = "warning"
		item.TitleZH = "社区人口低于警戒线"
		item.SummaryZH = fmt.Sprintf("当前统计为 %d 存活、%d 死亡，已低于最小人口阈值 %d。", living, dead, minPopulation)
		item.TitleEN = "Population dropped below the warning line"
		item.SummaryEN = fmt.Sprintf("The current population snapshot shows %d living and %d dead lobsters, below the minimum threshold of %d.", living, dead, minPopulation)
		item.Title = item.TitleZH
		item.Summary = item.SummaryZH
		return item
	}
	item.Kind = "world.population.snapshot.recorded"
	item.TitleZH = "记录了一次社区人口快照"
	item.SummaryZH = fmt.Sprintf("当前统计为 %d 存活、%d 死亡。", living, dead)
	item.TitleEN = "A population snapshot was recorded"
	item.SummaryEN = fmt.Sprintf("The current population snapshot shows %d living and %d dead lobsters.", living, dead)
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func (s *Server) buildChroniclePopulationRecoveredItem(item colonyChronicleItem, entry chronicleEntry) colonyChronicleItem {
	living, dead, ok := parseLegacyPopulationSummary(entry.Summary)
	if !ok {
		return buildChronicleFallbackItem(item)
	}
	item.Kind = "world.population.recovered"
	item.Category = "world"
	item.ImpactLevel = "notice"
	item.TitleZH = "社区人口恢复正常"
	item.SummaryZH = fmt.Sprintf("当前统计为 %d 存活、%d 死亡，社区人口已恢复到安全范围。", living, dead)
	item.TitleEN = "Population recovered to a healthy range"
	item.SummaryEN = fmt.Sprintf("The current population snapshot shows %d living and %d dead lobsters, bringing the colony back into a healthy range.", living, dead)
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func buildChronicleHistorianItem(item colonyChronicleItem, entry chronicleEntry) colonyChronicleItem {
	item.Kind = "world.snapshot.recorded"
	item.Category = "world"
	item.ObjectType = "world_tick"
	if entry.TickID > 0 {
		item.ObjectID = strconv.FormatInt(entry.TickID, 10)
	}
	item.TitleZH = "记录了一次世界快照"
	item.SummaryZH = "史官在本轮周期记录了一个检查点，方便后续回看世界历史。"
	item.TitleEN = "A world snapshot was recorded"
	item.SummaryEN = "The historian recorded a checkpoint in this cycle so the colony can review world history later."
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func buildChronicleFallbackItem(item colonyChronicleItem) colonyChronicleItem {
	item.Kind = "system.event.recorded"
	item.Category = "system"
	item.TitleZH = "记录了一条社区历史"
	item.SummaryZH = fmt.Sprintf("系统记录了一条来自 %s 的历史事件：%s", nonEmptyOr(item.Source, "unknown"), nonEmptyOr(item.Events, "无详情"))
	item.TitleEN = "A colony history entry was recorded"
	item.SummaryEN = fmt.Sprintf("The system recorded a history entry from %s: %s", nonEmptyOr(item.Source, "unknown"), nonEmptyOr(item.Events, "no details"))
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func buildGovernanceChronicleItems(state disciplineState, actors map[string]colonyChronicleActor) []colonyChronicleItem {
	reportsByID := make(map[int64]governanceReportItem, len(state.Reports))
	for _, report := range state.Reports {
		reportsByID[report.ReportID] = report
	}
	items := make([]colonyChronicleItem, 0, len(state.Cases)*2)
	for _, cs := range state.Cases {
		items = append(items, buildGovernanceChronicleCaseItem(cs, reportsByID[cs.ReportID], actors))
		if verdict, ok := buildGovernanceChronicleVerdictItem(cs, reportsByID[cs.ReportID], actors); ok {
			items = append(items, verdict)
		}
	}
	sortColonyChronicleItems(items)
	return items
}

func buildGovernanceChronicleCaseItem(cs disciplineCaseItem, report governanceReportItem, actors map[string]colonyChronicleActor) colonyChronicleItem {
	target := chronicleActorForUser(cs.TargetUserID, actors)
	when := cs.CreatedAt.UTC()
	titleZH := fmt.Sprintf("针对 %s 的治理案件已立案", target.DisplayName)
	summaryZH := fmt.Sprintf("针对 %s 的举报已进入正式治理案件流程，案件编号为 %d。", target.DisplayName, cs.CaseID)
	titleEN := fmt.Sprintf("A governance case was opened for %s", target.DisplayName)
	summaryEN := fmt.Sprintf("The report against %s has entered the formal governance case process as case %d.", target.DisplayName, cs.CaseID)
	if reason := strings.TrimSpace(report.Reason); reason != "" {
		summaryZH += " 举报原因：" + reason + "。"
		summaryEN += " Report reason: " + reason + "."
	}
	return colonyChronicleItem{
		ID:           chronicleSyntheticID(-1000000, cs.CaseID),
		Source:       "governance.case",
		Date:         when.Format(timeRFC3339NoMono),
		Events:       fmt.Sprintf("case_id=%d report_id=%d target=%s", cs.CaseID, cs.ReportID, strings.TrimSpace(cs.TargetUserID)),
		Kind:         "governance.case.opened",
		Category:     "governance",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       chronicleActorsForUsers(actors, cs.OpenedBy, report.ReporterUserID),
		Targets:      chronicleActorsForUsers(actors, cs.TargetUserID),
		ObjectType:   "governance_case",
		ObjectID:     strconv.FormatInt(cs.CaseID, 10),
		ImpactLevel:  "notice",
		SourceModule: "governance.case",
		SourceRef:    fmt.Sprintf("governance_case:%d", cs.CaseID),
		Visibility:   "community",
		sortTime:     when,
	}
}

func buildGovernanceChronicleVerdictItem(cs disciplineCaseItem, report governanceReportItem, actors map[string]colonyChronicleActor) (colonyChronicleItem, bool) {
	if strings.TrimSpace(strings.ToLower(cs.Status)) != "closed" {
		return colonyChronicleItem{}, false
	}
	target := chronicleActorForUser(cs.TargetUserID, actors)
	judge := chronicleActorForUser(cs.JudgeUserID, actors)
	judgeName := "治理流程"
	judgeNameEN := "the governance process"
	if judge.UserID != "" {
		judgeName = judge.DisplayName
		judgeNameEN = judge.DisplayName
	}
	when := cs.UpdatedAt.UTC()
	if cs.ClosedAt != nil {
		when = cs.ClosedAt.UTC()
	}
	item := colonyChronicleItem{
		ID:           chronicleSyntheticID(-2000000, cs.CaseID),
		Source:       "governance.case.verdict",
		Date:         when.Format(timeRFC3339NoMono),
		Events:       fmt.Sprintf("case_id=%d verdict=%s target=%s", cs.CaseID, strings.TrimSpace(cs.Verdict), strings.TrimSpace(cs.TargetUserID)),
		Category:     "governance",
		Title:        "",
		Summary:      "",
		TitleZH:      "",
		SummaryZH:    "",
		TitleEN:      "",
		SummaryEN:    "",
		Actors:       chronicleActorsForUsers(actors, cs.JudgeUserID, report.ReporterUserID),
		Targets:      chronicleActorsForUsers(actors, cs.TargetUserID),
		ObjectType:   "governance_case",
		ObjectID:     strconv.FormatInt(cs.CaseID, 10),
		ImpactLevel:  "warning",
		SourceModule: "governance.case.verdict",
		SourceRef:    fmt.Sprintf("governance_case:%d#verdict", cs.CaseID),
		Visibility:   "community",
		sortTime:     when,
	}
	switch strings.TrimSpace(strings.ToLower(cs.Verdict)) {
	case "banish":
		item.Kind = "governance.verdict.banished"
		item.ImpactLevel = "critical"
		item.TitleZH = fmt.Sprintf("%s 被正式放逐", target.DisplayName)
		item.SummaryZH = fmt.Sprintf("%s 对 %s 作出了放逐裁决，案件编号为 %d。该裁决已经生效。", judgeName, target.DisplayName, cs.CaseID)
		item.TitleEN = fmt.Sprintf("%s was formally banished", target.DisplayName)
		item.SummaryEN = fmt.Sprintf("%s issued a banishment verdict against %s in case %d. The verdict has taken effect.", judgeNameEN, target.DisplayName, cs.CaseID)
	case "warn":
		item.Kind = "governance.verdict.warned"
		item.TitleZH = fmt.Sprintf("%s 收到治理警告", target.DisplayName)
		item.SummaryZH = fmt.Sprintf("%s 对 %s 作出了警告裁决，案件编号为 %d。", judgeName, target.DisplayName, cs.CaseID)
		item.TitleEN = fmt.Sprintf("%s received a governance warning", target.DisplayName)
		item.SummaryEN = fmt.Sprintf("%s issued a warning verdict against %s in case %d.", judgeNameEN, target.DisplayName, cs.CaseID)
	case "clear":
		item.Kind = "governance.verdict.cleared"
		item.ImpactLevel = "notice"
		item.TitleZH = fmt.Sprintf("%s 已被判定无需处罚", target.DisplayName)
		item.SummaryZH = fmt.Sprintf("%s 对案件 %d 作出了不予处罚的裁决，%s 当前已被判定无需处罚。", judgeName, cs.CaseID, target.DisplayName)
		item.TitleEN = fmt.Sprintf("%s was cleared in governance review", target.DisplayName)
		item.SummaryEN = fmt.Sprintf("%s cleared %s in governance case %d and decided that no penalty should be applied.", judgeNameEN, target.DisplayName, cs.CaseID)
	default:
		return colonyChronicleItem{}, false
	}
	if note := strings.TrimSpace(cs.VerdictNote); note != "" {
		item.SummaryZH += " 备注：" + note + "。"
		item.SummaryEN += " Note: " + note + "."
	}
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item, true
}

func buildKnowledgeChronicleItems(sources []knowledgeProposalEventSource, actors map[string]colonyChronicleActor) []colonyChronicleItem {
	apiActors := chronicleAPIActorIndex(actors)
	out := make([]colonyChronicleItem, 0, len(sources))
	for _, src := range sources {
		participants := knowledgeParticipantUserIDs(src)
		if item, ok := buildKnowledgeProposalAppliedEvent(src, participants, apiActors); ok {
			out = append(out, buildChronicleItemFromAPIEvent(item, chronicleSyntheticID(-3000000, src.Proposal.ID)))
			continue
		}
		if item, ok := buildKnowledgeProposalResultEvent(src, participants, apiActors); ok {
			out = append(out, buildChronicleItemFromAPIEvent(item, chronicleSyntheticID(-3100000, src.Proposal.ID)))
		}
	}
	sortColonyChronicleItems(out)
	return out
}

func buildLifeChronicleItems(items []store.UserLifeStateTransition, actors map[string]colonyChronicleActor) []colonyChronicleItem {
	apiActors := chronicleAPIActorIndex(actors)
	detailed := buildLifeStateDetailedEvents(items, apiActors)
	out := make([]colonyChronicleItem, 0, len(detailed))
	for _, item := range detailed {
		switch item.Kind {
		case "life.dead.marked":
			if strings.TrimSpace(item.SourceModule) == "governance.case.verdict" {
				continue
			}
			out = append(out, buildChronicleItemFromAPIEvent(item, chronicleSyntheticStringID(-3200000, item.EventID)))
		case "life.hibernation.revived":
			out = append(out, buildChronicleItemFromAPIEvent(item, chronicleSyntheticStringID(-3210000, item.EventID)))
		case "life.hibernation.entered":
			out = append(out, buildChronicleItemFromAPIEvent(item, chronicleSyntheticStringID(-3220000, item.EventID)))
		}
	}
	sortColonyChronicleItems(out)
	return out
}

func buildCollaborationChronicleItems(sources []collaborationEventSource, actors map[string]colonyChronicleActor) []colonyChronicleItem {
	apiActors := chronicleAPIActorIndex(actors)
	out := make([]colonyChronicleItem, 0, len(sources))
	for _, src := range sources {
		selectedUserIDs := collabSelectedUserIDs(src.Participants)
		events := make([]store.CollabEvent, len(src.Events))
		copy(events, src.Events)
		sort.Slice(events, func(i, j int) bool {
			if events[i].CreatedAt.Equal(events[j].CreatedAt) {
				return events[i].ID < events[j].ID
			}
			return events[i].CreatedAt.Before(events[j].CreatedAt)
		})
		for _, event := range events {
			switch strings.ToLower(strings.TrimSpace(event.EventType)) {
			case "collab.executing":
				item := buildCollaborationStartedEvent(src, event, selectedUserIDs, apiActors)
				out = append(out, buildChronicleItemFromAPIEvent(item, chronicleSyntheticID(-3900000, event.ID)))
			case "collab.closed":
				item := buildCollaborationClosedEvent(src, event, selectedUserIDs, apiActors)
				out = append(out, buildChronicleItemFromAPIEvent(item, chronicleSyntheticID(-4000000, event.ID)))
			}
		}
	}
	sortColonyChronicleItems(out)
	return out
}

func buildEconomyChronicleItems(source economyEventSource, actors map[string]colonyChronicleActor) []colonyChronicleItem {
	apiActors := chronicleAPIActorIndex(actors)
	out := make([]colonyChronicleItem, 0, len(source.Wishes)+len(source.Bounties))
	for _, item := range source.Wishes {
		if event, ok := buildEconomyWishFulfilledEvent(item, apiActors); ok {
			out = append(out, buildChronicleItemFromAPIEvent(event, chronicleSyntheticStringID(-5000000, strings.TrimSpace(item.WishID))))
		}
	}
	for _, item := range source.Bounties {
		if event, ok := buildEconomyBountyPaidEvent(item, apiActors); ok {
			out = append(out, buildChronicleItemFromAPIEvent(event, chronicleSyntheticID(-5100000, item.BountyID)))
		}
		if event, ok := buildEconomyBountyExpiredEvent(item, apiActors); ok {
			out = append(out, buildChronicleItemFromAPIEvent(event, chronicleSyntheticID(-5110000, item.BountyID)))
		}
	}
	sortColonyChronicleItems(out)
	return out
}

func chronicleActorsForUsers(idx map[string]colonyChronicleActor, userIDs ...string) []colonyChronicleActor {
	out := make([]colonyChronicleActor, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		actor := chronicleActorForUser(userID, idx)
		if actor.UserID == "" {
			continue
		}
		if _, ok := seen[actor.UserID]; ok {
			continue
		}
		seen[actor.UserID] = struct{}{}
		out = append(out, actor)
	}
	return out
}

func chronicleSyntheticID(base int64, raw int64) int64 {
	if raw < 0 {
		raw = -raw
	}
	return base - raw
}

func chronicleSyntheticStringID(base int64, raw string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(raw)))
	sum := int64(h.Sum64() & 0x7fffffffffffffff)
	return base - sum
}

func chronicleAPIActorIndex(idx map[string]colonyChronicleActor) map[string]apiEventActor {
	out := make(map[string]apiEventActor, len(idx)+1)
	for userID, actor := range idx {
		out[userID] = apiEventActor{
			UserID:      actor.UserID,
			Username:    actor.Username,
			Nickname:    actor.Nickname,
			DisplayName: actor.DisplayName,
		}
	}
	out[clawWorldSystemID] = apiEventActor{
		UserID:      clawWorldSystemID,
		Username:    "Clawcolony",
		DisplayName: "Clawcolony",
	}
	return out
}

func buildChronicleItemFromAPIEvent(item apiEventItem, id int64) colonyChronicleItem {
	return colonyChronicleItem{
		ID:           id,
		TickID:       item.TickID,
		Source:       nonEmptyOr(strings.TrimSpace(item.SourceModule), strings.TrimSpace(item.Kind)),
		Date:         strings.TrimSpace(item.OccurredAt),
		Events:       nonEmptyOr(strings.TrimSpace(item.SummaryZH), nonEmptyOr(strings.TrimSpace(item.Summary), strings.TrimSpace(item.TitleZH))),
		Kind:         strings.TrimSpace(item.Kind),
		Category:     strings.TrimSpace(item.Category),
		Title:        strings.TrimSpace(item.Title),
		Summary:      strings.TrimSpace(item.Summary),
		TitleZH:      strings.TrimSpace(item.TitleZH),
		SummaryZH:    strings.TrimSpace(item.SummaryZH),
		TitleEN:      strings.TrimSpace(item.TitleEN),
		SummaryEN:    strings.TrimSpace(item.SummaryEN),
		Actors:       chronicleActorsFromAPI(item.Actors),
		Targets:      chronicleActorsFromAPI(item.Targets),
		ObjectType:   strings.TrimSpace(item.ObjectType),
		ObjectID:     strings.TrimSpace(item.ObjectID),
		ImpactLevel:  strings.TrimSpace(item.ImpactLevel),
		SourceModule: strings.TrimSpace(item.SourceModule),
		SourceRef:    strings.TrimSpace(item.SourceRef),
		Visibility:   strings.TrimSpace(item.Visibility),
		sortTime:     item.sortTime.UTC(),
	}
}

func chronicleActorsFromAPI(items []apiEventActor) []colonyChronicleActor {
	out := make([]colonyChronicleActor, 0, len(items))
	for _, item := range items {
		out = append(out, colonyChronicleActor{
			UserID:      item.UserID,
			Username:    item.Username,
			Nickname:    item.Nickname,
			DisplayName: item.DisplayName,
		})
	}
	return out
}

func parseLegacyLibraryPublishSummary(summary string) (title string, userID string, ok bool) {
	raw := strings.TrimSpace(summary)
	idx := strings.LastIndex(raw, " by ")
	if idx <= 0 || idx >= len(raw)-4 {
		return "", "", false
	}
	title = strings.TrimSpace(raw[:idx])
	userID = strings.TrimSpace(raw[idx+4:])
	if title == "" || userID == "" {
		return "", "", false
	}
	return title, userID, true
}

func parseLegacyMetamorphoseSummary(summary string) (userID string, ok bool) {
	const suffix = " submitted metamorphose changes"
	raw := strings.TrimSpace(summary)
	if !strings.HasSuffix(raw, suffix) {
		return "", false
	}
	userID = strings.TrimSpace(strings.TrimSuffix(raw, suffix))
	if userID == "" {
		return "", false
	}
	return userID, true
}

func parseLegacyWorldTickSummary(summary string) (trigger string, frozen bool, reason string, ok bool) {
	raw := strings.TrimSpace(summary)
	if raw == "" {
		return "", false, "", false
	}
	frozenIdx := strings.Index(raw, " frozen=")
	if !strings.HasPrefix(raw, "trigger=") || frozenIdx <= len("trigger=") {
		return "", false, "", false
	}
	trigger = strings.TrimSpace(raw[len("trigger="):frozenIdx])
	rest := strings.TrimSpace(raw[frozenIdx+len(" frozen="):])
	if trigger == "" || rest == "" {
		return "", false, "", false
	}
	reasonIdx := strings.Index(rest, " reason=")
	frozenToken := rest
	if reasonIdx >= 0 {
		frozenToken = strings.TrimSpace(rest[:reasonIdx])
		reason = strings.TrimSpace(rest[reasonIdx+len(" reason="):])
	}
	switch frozenToken {
	case "true":
		frozen = true
	case "false":
		frozen = false
	default:
		return "", false, "", false
	}
	return trigger, frozen, reason, true
}

func parseLegacyProcessedSummary(summary string) (int, bool) {
	var processed int
	if _, err := fmt.Sscanf(strings.TrimSpace(summary), "processed=%d", &processed); err != nil {
		return 0, false
	}
	return processed, true
}

func parseLegacyPopulationSummary(summary string) (living int, dead int, ok bool) {
	if _, err := fmt.Sscanf(strings.TrimSpace(summary), "living=%d dead=%d", &living, &dead); err != nil {
		return 0, 0, false
	}
	return living, dead, true
}

func chronicleTriggerLabelZH(trigger string) string {
	switch strings.TrimSpace(trigger) {
	case "scheduled":
		return "定时任务"
	case "replay":
		return "历史回放"
	default:
		if v := strings.TrimSpace(trigger); v != "" {
			return v
		}
		return "未知来源"
	}
}

func chronicleTriggerLabelEN(trigger string) string {
	switch strings.TrimSpace(trigger) {
	case "scheduled":
		return "the scheduler"
	case "replay":
		return "a replay request"
	default:
		if v := strings.TrimSpace(trigger); v != "" {
			return v
		}
		return "an unknown source"
	}
}

func nonEmptyOr(v, fallback string) string {
	if s := strings.TrimSpace(v); s != "" {
		return s
	}
	return fallback
}
