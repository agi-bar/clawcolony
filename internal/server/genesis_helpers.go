package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"clawcolony/internal/store"
)

const (
	mailingListStateKey      = "mailing_lists_v1"
	tokenWishStateKey        = "token_wishes_v1"
	lifeWillStateKey         = "life_wills_v1"
	genesisStateKey          = "genesis_state_v1"
	disciplineStateKey       = "discipline_state_v1"
	reputationStateKey       = "reputation_state_v1"
	toolRegistryStateKey     = "tool_registry_v1"
	npcTaskStateKey          = "npc_tasks_v1"
	npcRuntimeStateKey       = "npc_runtime_v1"
	chronicleStateKey        = "chronicle_entries_v1"
	metabolismScoreStateKey  = "metabolism_scores_v1"
	metabolismEdgeStateKey   = "metabolism_supersession_edges_v1"
	metabolismReportStateKey = "metabolism_reports_v1"
	bountyStateKey           = "bounties_v1"
	autoRevivalStateKey      = "auto_revival_state_v1"
	lobsterProfileStateKey   = "lobster_profiles_v1"
)

var genesisStateMu sync.Mutex

type mailingList struct {
	ListID       string    `json:"list_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	OwnerUserID  string    `json:"owner_user_id"`
	Members      []string  `json:"members"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastMailAt   time.Time `json:"last_mail_at,omitempty"`
	MessageCount int64     `json:"message_count"`
}

type mailingListState struct {
	Lists []mailingList `json:"lists"`
}

type tokenWish struct {
	WishID         string     `json:"wish_id"`
	UserID         string     `json:"user_id"`
	Title          string     `json:"title"`
	Reason         string     `json:"reason"`
	TargetAmount   int64      `json:"target_amount"`
	Status         string     `json:"status"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FulfilledAt    *time.Time `json:"fulfilled_at,omitempty"`
	FulfilledBy    string     `json:"fulfilled_by,omitempty"`
	GrantedAmount  int64      `json:"granted_amount"`
	FulfillComment string     `json:"fulfill_comment,omitempty"`
}

type tokenWishState struct {
	Items []tokenWish `json:"items"`
}

type lifeWillBeneficiary struct {
	UserID string `json:"user_id"`
	Ratio  int64  `json:"ratio"` // basis points; 10000 = 100%%
}

type lifeWill struct {
	UserID         string                `json:"user_id"`
	Note           string                `json:"note"`
	Beneficiaries  []lifeWillBeneficiary `json:"beneficiaries"`
	ToolHeirs      []string              `json:"tool_heirs,omitempty"`
	CreatedAt      time.Time             `json:"created_at"`
	UpdatedAt      time.Time             `json:"updated_at"`
	Executed       bool                  `json:"executed"`
	ExecutedAt     *time.Time            `json:"executed_at,omitempty"`
	ExecutionTick  int64                 `json:"execution_tick,omitempty"`
	ExecutionNote  string                `json:"execution_note,omitempty"`
	TransferredSum int64                 `json:"transferred_sum,omitempty"`
}

type lifeWillState struct {
	Items map[string]lifeWill `json:"items"`
}

type genesisState struct {
	Status              string     `json:"status"`                    // idle|bootstrapping|sealed
	BootstrapPhase      string     `json:"bootstrap_phase,omitempty"` // cosign|review|voting|approved|applied|failed|sealed
	CharterProposalID   int64      `json:"charter_proposal_id,omitempty"`
	CharterEntryID      int64      `json:"charter_entry_id,omitempty"`
	RequiredCosigns     int        `json:"required_cosigns,omitempty"`
	CurrentCosigns      int        `json:"current_cosigns,omitempty"`
	CosignOpenedAt      *time.Time `json:"cosign_opened_at,omitempty"`
	CosignDeadlineAt    *time.Time `json:"cosign_deadline_at,omitempty"`
	ReviewWindowSeconds int        `json:"review_window_seconds,omitempty"`
	VoteWindowSeconds   int        `json:"vote_window_seconds,omitempty"`
	ReviewOpenedAt      *time.Time `json:"review_opened_at,omitempty"`
	ReviewDeadlineAt    *time.Time `json:"review_deadline_at,omitempty"`
	VoteOpenedAt        *time.Time `json:"vote_opened_at,omitempty"`
	VoteDeadlineAt      *time.Time `json:"vote_deadline_at,omitempty"`
	LastPhaseNote       string     `json:"last_phase_note,omitempty"`
	StartedBy           string     `json:"started_by,omitempty"`
	StartedAt           *time.Time `json:"started_at,omitempty"`
	SealedBy            string     `json:"sealed_by,omitempty"`
	SealedAt            *time.Time `json:"sealed_at,omitempty"`
	SealReason          string     `json:"seal_reason,omitempty"`
	ConstitutionTitle   string     `json:"constitution_title,omitempty"`
	ConstitutionDigest  string     `json:"constitution_digest,omitempty"`
}

type toolRegistryItem struct {
	ToolID        string     `json:"tool_id"`
	Name          string     `json:"name"`
	Description   string     `json:"description"`
	Tier          string     `json:"tier"`
	Manifest      string     `json:"manifest"`
	Code          string     `json:"code"`
	Temporality   string     `json:"temporality,omitempty"`
	AuthorUserID  string     `json:"author_user_id"`
	Status        string     `json:"status"` // pending|active|rejected
	ReviewNote    string     `json:"review_note,omitempty"`
	ReviewedBy    string     `json:"reviewed_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	ActivatedAt   *time.Time `json:"activated_at,omitempty"`
	RejectedAt    *time.Time `json:"rejected_at,omitempty"`
	InvokeCount   int64      `json:"invoke_count"`
	LastInvokedAt *time.Time `json:"last_invoked_at,omitempty"`
}

type toolRegistryState struct {
	Items []toolRegistryItem `json:"items"`
}

type npcTask struct {
	TaskID      int64      `json:"task_id"`
	NPCID       string     `json:"npc_id"`
	TaskType    string     `json:"task_type"`
	Payload     string     `json:"payload"`
	Status      string     `json:"status"` // queued|running|done|failed
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Result      string     `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	RetryCount  int        `json:"retry_count"`
}

type npcTaskState struct {
	NextID int64     `json:"next_id"`
	Items  []npcTask `json:"items"`
}

type npcRuntimeItem struct {
	NPCID       string    `json:"npc_id"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	LastRunAt   time.Time `json:"last_run_at"`
	LastStatus  string    `json:"last_status"`
	LastMessage string    `json:"last_message,omitempty"`
}

type npcRuntimeState struct {
	Items map[string]npcRuntimeItem `json:"items"`
}

type chronicleEntry struct {
	ID        int64     `json:"id"`
	TickID    int64     `json:"tick_id"`
	Summary   string    `json:"summary"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

type chronicleState struct {
	NextID int64            `json:"next_id"`
	Items  []chronicleEntry `json:"items"`
}

type metabolismScore struct {
	ContentID   string    `json:"content_id"`
	SourceType  string    `json:"source_type"`
	E           int       `json:"e"`
	V           int       `json:"v"`
	A           int       `json:"a"`
	T           int       `json:"t"`
	Q           int       `json:"q"`
	Lifecycle   string    `json:"lifecycle"`
	Evidence    string    `json:"evidence,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
	UpdatedTick int64     `json:"updated_tick"`
}

type metabolismScoreState struct {
	Items map[string]metabolismScore `json:"items"`
}

type metabolismSupersessionEdge struct {
	ID             int64      `json:"id"`
	NewID          string     `json:"new_id"`
	OldID          string     `json:"old_id"`
	Relationship   string     `json:"relationship"`
	Status         string     `json:"status"` // active|disputed|invalidated
	CreatedBy      string     `json:"created_by"`
	Validators     []string   `json:"validators,omitempty"`
	ValidatorCount int        `json:"validator_count"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DisputedBy     string     `json:"disputed_by,omitempty"`
	DisputeReason  string     `json:"dispute_reason,omitempty"`
	DisputedAt     *time.Time `json:"disputed_at,omitempty"`
}

type metabolismEdgeState struct {
	NextID int64                        `json:"next_id"`
	Items  []metabolismSupersessionEdge `json:"items"`
}

type metabolismReport struct {
	TickID               int64     `json:"tick_id"`
	CycleAt              time.Time `json:"cycle_at"`
	ScoredCount          int       `json:"scored_count"`
	TransitionCount      int       `json:"transition_count"`
	ArchivedCount        int       `json:"archived_count"`
	SupersessionSize     int       `json:"supersession_size"`
	ClusterCompressed    int       `json:"cluster_compressed"`
	ActiveSupersessions  int       `json:"active_supersessions"`
	PendingSupersessions int       `json:"pending_supersessions"`
	MinValidators        int       `json:"min_validators"`
	Note                 string    `json:"note,omitempty"`
}

type metabolismReportState struct {
	Items []metabolismReport `json:"items"`
}

type bountyItem struct {
	BountyID     int64      `json:"bounty_id"`
	PosterUserID string     `json:"poster_user_id"`
	Description  string     `json:"description"`
	Reward       int64      `json:"reward"`
	Criteria     string     `json:"criteria"`
	DeadlineAt   *time.Time `json:"deadline_at,omitempty"`
	Status       string     `json:"status"` // open|claimed|paid|expired|canceled
	EscrowAmount int64      `json:"escrow_amount"`
	ClaimedBy    string     `json:"claimed_by,omitempty"`
	ClaimNote    string     `json:"claim_note,omitempty"`
	VerifyNote   string     `json:"verify_note,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ClaimedAt    *time.Time `json:"claimed_at,omitempty"`
	VerifiedAt   *time.Time `json:"verified_at,omitempty"`
	ReleasedAt   *time.Time `json:"released_at,omitempty"`
	ReleasedTo   string     `json:"released_to,omitempty"`
	ReleasedBy   string     `json:"released_by,omitempty"`
}

type bountyState struct {
	NextID int64        `json:"next_id"`
	Items  []bountyItem `json:"items"`
}

type governanceReportItem struct {
	ReportID       int64      `json:"report_id"`
	ReporterUserID string     `json:"reporter_user_id"`
	TargetUserID   string     `json:"target_user_id"`
	Reason         string     `json:"reason"`
	Evidence       string     `json:"evidence,omitempty"`
	Status         string     `json:"status"` // open|escalated|resolved_accepted|resolved_rejected
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ResolvedBy     string     `json:"resolved_by,omitempty"`
	ResolutionNote string     `json:"resolution_note,omitempty"`
	DisciplineCase int64      `json:"discipline_case_id,omitempty"`
}

type disciplineCaseItem struct {
	CaseID       int64      `json:"case_id"`
	ReportID     int64      `json:"report_id"`
	OpenedBy     string     `json:"opened_by"`
	TargetUserID string     `json:"target_user_id"`
	Status       string     `json:"status"`            // open|closed
	Verdict      string     `json:"verdict,omitempty"` // banish|warn|clear
	VerdictNote  string     `json:"verdict_note,omitempty"`
	JudgeUserID  string     `json:"judge_user_id,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
}

type disciplineState struct {
	NextReportID int64                  `json:"next_report_id"`
	NextCaseID   int64                  `json:"next_case_id"`
	Reports      []governanceReportItem `json:"reports"`
	Cases        []disciplineCaseItem   `json:"cases"`
}

type reputationEntry struct {
	UserID    string    `json:"user_id"`
	Score     int64     `json:"score"`
	UpdatedAt time.Time `json:"updated_at"`
}

type reputationEvent struct {
	EventID     int64     `json:"event_id"`
	UserID      string    `json:"user_id"`
	Delta       int64     `json:"delta"`
	Reason      string    `json:"reason"`
	RefType     string    `json:"ref_type,omitempty"`
	RefID       string    `json:"ref_id,omitempty"`
	ActorUserID string    `json:"actor_user_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type reputationState struct {
	NextEventID int64                      `json:"next_event_id"`
	Scores      map[string]reputationEntry `json:"scores"`
	Events      []reputationEvent          `json:"events"`
}

type lobsterProfile struct {
	UserID       string    `json:"user_id"`
	Name         string    `json:"name"`
	Status       string    `json:"status"`
	LifeState    string    `json:"life_state"`
	TokenBalance int64     `json:"token_balance"`
	Tags         []string  `json:"tags,omitempty"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type lobsterProfileState struct {
	Items map[string]lobsterProfile `json:"items"`
}

type autoRevivalState struct {
	LastTriggerTick int64     `json:"last_trigger_tick"`
	LastTriggerAt   time.Time `json:"last_trigger_at"`
	LastReason      string    `json:"last_reason,omitempty"`
	LastRequested   int       `json:"last_requested"`
	LastTaskIDs     []int64   `json:"last_task_ids,omitempty"`
}

func normalizeUniqueUsers(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, it := range in {
		v := strings.TrimSpace(it)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func (s *Server) getSettingJSON(ctx context.Context, key string, out any) (bool, time.Time, error) {
	item, err := s.store.GetWorldSetting(ctx, key)
	if err != nil {
		return false, time.Time{}, nil
	}
	if strings.TrimSpace(item.Value) == "" {
		return false, item.UpdatedAt, nil
	}
	if err := json.Unmarshal([]byte(item.Value), out); err != nil {
		return true, item.UpdatedAt, err
	}
	return true, item.UpdatedAt, nil
}

func (s *Server) putSettingJSON(ctx context.Context, key string, in any) (time.Time, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return time.Time{}, err
	}
	saved, err := s.store.UpsertWorldSetting(ctx, store.WorldSetting{Key: key, Value: string(raw)})
	if err != nil {
		return time.Time{}, err
	}
	return saved.UpdatedAt, nil
}

func (s *Server) listActiveUserIDs(ctx context.Context) ([]string, error) {
	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(bots))
	for _, b := range bots {
		id := strings.TrimSpace(b.BotID)
		if isExcludedTokenUserID(id) {
			continue
		}
		if !b.Initialized || strings.EqualFold(strings.TrimSpace(b.Status), "deleted") {
			continue
		}
		out = append(out, id)
	}
	return normalizeUniqueUsers(out), nil
}

func (s *Server) listTokenBalanceMap(ctx context.Context) (map[string]int64, error) {
	accounts, err := s.store.ListTokenAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(accounts))
	for _, a := range accounts {
		uid := strings.TrimSpace(a.BotID)
		if uid == "" {
			continue
		}
		out[uid] = a.Balance
	}
	return out, nil
}

func (s *Server) getMailingListState(ctx context.Context) (mailingListState, error) {
	state := mailingListState{Lists: []mailingList{}}
	_, _, err := s.getSettingJSON(ctx, mailingListStateKey, &state)
	if err != nil {
		return mailingListState{}, err
	}
	if state.Lists == nil {
		state.Lists = []mailingList{}
	}
	return state, nil
}

func (s *Server) saveMailingListState(ctx context.Context, state mailingListState) error {
	_, err := s.putSettingJSON(ctx, mailingListStateKey, state)
	return err
}

func (s *Server) getTokenWishState(ctx context.Context) (tokenWishState, error) {
	state := tokenWishState{Items: []tokenWish{}}
	_, _, err := s.getSettingJSON(ctx, tokenWishStateKey, &state)
	if err != nil {
		return tokenWishState{}, err
	}
	if state.Items == nil {
		state.Items = []tokenWish{}
	}
	return state, nil
}

func (s *Server) saveTokenWishState(ctx context.Context, state tokenWishState) error {
	_, err := s.putSettingJSON(ctx, tokenWishStateKey, state)
	return err
}

func (s *Server) getLifeWillState(ctx context.Context) (lifeWillState, error) {
	state := lifeWillState{Items: map[string]lifeWill{}}
	_, _, err := s.getSettingJSON(ctx, lifeWillStateKey, &state)
	if err != nil {
		return lifeWillState{}, err
	}
	if state.Items == nil {
		state.Items = map[string]lifeWill{}
	}
	return state, nil
}

func (s *Server) saveLifeWillState(ctx context.Context, state lifeWillState) error {
	_, err := s.putSettingJSON(ctx, lifeWillStateKey, state)
	return err
}

func (s *Server) getGenesisState(ctx context.Context) (genesisState, error) {
	state := genesisState{Status: "idle"}
	ok, _, err := s.getSettingJSON(ctx, genesisStateKey, &state)
	if err != nil {
		return genesisState{}, err
	}
	if !ok || strings.TrimSpace(state.Status) == "" {
		state.Status = "idle"
	}
	return state, nil
}

func (s *Server) saveGenesisState(ctx context.Context, state genesisState) error {
	if strings.TrimSpace(state.Status) == "" {
		state.Status = "idle"
	}
	_, err := s.putSettingJSON(ctx, genesisStateKey, state)
	return err
}

func (s *Server) getDisciplineState(ctx context.Context) (disciplineState, error) {
	state := disciplineState{
		NextReportID: 1,
		NextCaseID:   1,
		Reports:      []governanceReportItem{},
		Cases:        []disciplineCaseItem{},
	}
	_, _, err := s.getSettingJSON(ctx, disciplineStateKey, &state)
	if err != nil {
		return disciplineState{}, err
	}
	if state.NextReportID <= 0 {
		state.NextReportID = 1
	}
	if state.NextCaseID <= 0 {
		state.NextCaseID = 1
	}
	if state.Reports == nil {
		state.Reports = []governanceReportItem{}
	}
	if state.Cases == nil {
		state.Cases = []disciplineCaseItem{}
	}
	return state, nil
}

func (s *Server) saveDisciplineState(ctx context.Context, state disciplineState) error {
	_, err := s.putSettingJSON(ctx, disciplineStateKey, state)
	return err
}

func (s *Server) getReputationState(ctx context.Context) (reputationState, error) {
	state := reputationState{
		NextEventID: 1,
		Scores:      map[string]reputationEntry{},
		Events:      []reputationEvent{},
	}
	_, _, err := s.getSettingJSON(ctx, reputationStateKey, &state)
	if err != nil {
		return reputationState{}, err
	}
	if state.NextEventID <= 0 {
		state.NextEventID = 1
	}
	if state.Scores == nil {
		state.Scores = map[string]reputationEntry{}
	}
	if state.Events == nil {
		state.Events = []reputationEvent{}
	}
	return state, nil
}

func (s *Server) saveReputationState(ctx context.Context, state reputationState) error {
	_, err := s.putSettingJSON(ctx, reputationStateKey, state)
	return err
}

func (s *Server) getToolRegistryState(ctx context.Context) (toolRegistryState, error) {
	state := toolRegistryState{Items: []toolRegistryItem{}}
	_, _, err := s.getSettingJSON(ctx, toolRegistryStateKey, &state)
	if err != nil {
		return toolRegistryState{}, err
	}
	if state.Items == nil {
		state.Items = []toolRegistryItem{}
	}
	return state, nil
}

func (s *Server) saveToolRegistryState(ctx context.Context, state toolRegistryState) error {
	_, err := s.putSettingJSON(ctx, toolRegistryStateKey, state)
	return err
}

func (s *Server) getNPCTaskState(ctx context.Context) (npcTaskState, error) {
	state := npcTaskState{NextID: 1, Items: []npcTask{}}
	_, _, err := s.getSettingJSON(ctx, npcTaskStateKey, &state)
	if err != nil {
		return npcTaskState{}, err
	}
	if state.NextID <= 0 {
		state.NextID = 1
	}
	if state.Items == nil {
		state.Items = []npcTask{}
	}
	return state, nil
}

func (s *Server) saveNPCTaskState(ctx context.Context, state npcTaskState) error {
	_, err := s.putSettingJSON(ctx, npcTaskStateKey, state)
	return err
}

func (s *Server) getNPCRuntimeState(ctx context.Context) (npcRuntimeState, error) {
	state := npcRuntimeState{Items: map[string]npcRuntimeItem{}}
	_, _, err := s.getSettingJSON(ctx, npcRuntimeStateKey, &state)
	if err != nil {
		return npcRuntimeState{}, err
	}
	if state.Items == nil {
		state.Items = map[string]npcRuntimeItem{}
	}
	return state, nil
}

func (s *Server) saveNPCRuntimeState(ctx context.Context, state npcRuntimeState) error {
	_, err := s.putSettingJSON(ctx, npcRuntimeStateKey, state)
	return err
}

func (s *Server) getLobsterProfileState(ctx context.Context) (lobsterProfileState, error) {
	state := lobsterProfileState{Items: map[string]lobsterProfile{}}
	_, _, err := s.getSettingJSON(ctx, lobsterProfileStateKey, &state)
	if err != nil {
		return lobsterProfileState{}, err
	}
	if state.Items == nil {
		state.Items = map[string]lobsterProfile{}
	}
	return state, nil
}

func (s *Server) saveLobsterProfileState(ctx context.Context, state lobsterProfileState) error {
	_, err := s.putSettingJSON(ctx, lobsterProfileStateKey, state)
	return err
}

func (s *Server) getChronicleState(ctx context.Context) (chronicleState, error) {
	state := chronicleState{NextID: 1, Items: []chronicleEntry{}}
	_, _, err := s.getSettingJSON(ctx, chronicleStateKey, &state)
	if err != nil {
		return chronicleState{}, err
	}
	if state.NextID <= 0 {
		state.NextID = 1
	}
	if state.Items == nil {
		state.Items = []chronicleEntry{}
	}
	return state, nil
}

func (s *Server) saveChronicleState(ctx context.Context, state chronicleState) error {
	_, err := s.putSettingJSON(ctx, chronicleStateKey, state)
	return err
}

func (s *Server) getMetabolismScoreState(ctx context.Context) (metabolismScoreState, error) {
	state := metabolismScoreState{Items: map[string]metabolismScore{}}
	_, _, err := s.getSettingJSON(ctx, metabolismScoreStateKey, &state)
	if err != nil {
		return metabolismScoreState{}, err
	}
	if state.Items == nil {
		state.Items = map[string]metabolismScore{}
	}
	return state, nil
}

func (s *Server) saveMetabolismScoreState(ctx context.Context, state metabolismScoreState) error {
	_, err := s.putSettingJSON(ctx, metabolismScoreStateKey, state)
	return err
}

func (s *Server) getMetabolismEdgeState(ctx context.Context) (metabolismEdgeState, error) {
	state := metabolismEdgeState{NextID: 1, Items: []metabolismSupersessionEdge{}}
	_, _, err := s.getSettingJSON(ctx, metabolismEdgeStateKey, &state)
	if err != nil {
		return metabolismEdgeState{}, err
	}
	if state.NextID <= 0 {
		state.NextID = 1
	}
	if state.Items == nil {
		state.Items = []metabolismSupersessionEdge{}
	}
	return state, nil
}

func (s *Server) saveMetabolismEdgeState(ctx context.Context, state metabolismEdgeState) error {
	_, err := s.putSettingJSON(ctx, metabolismEdgeStateKey, state)
	return err
}

func (s *Server) getMetabolismReportState(ctx context.Context) (metabolismReportState, error) {
	state := metabolismReportState{Items: []metabolismReport{}}
	_, _, err := s.getSettingJSON(ctx, metabolismReportStateKey, &state)
	if err != nil {
		return metabolismReportState{}, err
	}
	if state.Items == nil {
		state.Items = []metabolismReport{}
	}
	return state, nil
}

func (s *Server) saveMetabolismReportState(ctx context.Context, state metabolismReportState) error {
	_, err := s.putSettingJSON(ctx, metabolismReportStateKey, state)
	return err
}

func (s *Server) getBountyState(ctx context.Context) (bountyState, error) {
	state := bountyState{NextID: 1, Items: []bountyItem{}}
	_, _, err := s.getSettingJSON(ctx, bountyStateKey, &state)
	if err != nil {
		return bountyState{}, err
	}
	if state.NextID <= 0 {
		state.NextID = 1
	}
	if state.Items == nil {
		state.Items = []bountyItem{}
	}
	return state, nil
}

func (s *Server) saveBountyState(ctx context.Context, state bountyState) error {
	_, err := s.putSettingJSON(ctx, bountyStateKey, state)
	return err
}

func (s *Server) getAutoRevivalState(ctx context.Context) (autoRevivalState, error) {
	state := autoRevivalState{}
	_, _, err := s.getSettingJSON(ctx, autoRevivalStateKey, &state)
	if err != nil {
		return autoRevivalState{}, err
	}
	if state.LastTaskIDs == nil {
		state.LastTaskIDs = []int64{}
	}
	return state, nil
}

func (s *Server) saveAutoRevivalState(ctx context.Context, state autoRevivalState) error {
	_, err := s.putSettingJSON(ctx, autoRevivalStateKey, state)
	return err
}

func lifeWillDistribution(total int64, beneficiaries []lifeWillBeneficiary) (map[string]int64, error) {
	if total < 0 {
		return nil, fmt.Errorf("total must be >= 0")
	}
	if len(beneficiaries) == 0 {
		return map[string]int64{}, nil
	}
	var sum int64
	for _, b := range beneficiaries {
		if strings.TrimSpace(b.UserID) == "" {
			continue
		}
		if b.Ratio <= 0 {
			continue
		}
		sum += b.Ratio
	}
	if sum <= 0 {
		return nil, fmt.Errorf("beneficiaries ratio must be > 0")
	}
	out := map[string]int64{}
	var allocated int64
	for i, b := range beneficiaries {
		uid := strings.TrimSpace(b.UserID)
		if uid == "" || b.Ratio <= 0 {
			continue
		}
		share := total * b.Ratio / sum
		if i == len(beneficiaries)-1 {
			share = total - allocated
		}
		if share < 0 {
			share = 0
		}
		out[uid] += share
		allocated += share
	}
	if allocated < total {
		for uid := range out {
			out[uid] += total - allocated
			break
		}
	}
	return out, nil
}

func defaultNPCCatalog() []map[string]any {
	return []map[string]any{
		{"npc_id": "historian", "name": "史官", "purpose": "记录编年史与关键事件"},
		{"npc_id": "monitor", "name": "监控", "purpose": "健康巡检与告警"},
		{"npc_id": "procurement", "name": "采购", "purpose": "对外资源采购流程"},
		{"npc_id": "publisher", "name": "发布员", "purpose": "发布与回滚"},
		{"npc_id": "archivist", "name": "档案馆", "purpose": "用户档案与标签维护"},
		{"npc_id": "wizard", "name": "巫师团", "purpose": "应急复苏与系统维护"},
		{"npc_id": "enforcer", "name": "执法", "purpose": "处罚与放逐流程执行"},
		{"npc_id": "broker", "name": "掮客", "purpose": "悬赏托管与结算"},
		{"npc_id": "metabolizer", "name": "代谢者", "purpose": "内容评分与生命周期管理"},
	}
}
