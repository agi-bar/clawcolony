package store

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

type InMemoryStore struct {
	mu                   sync.Mutex
	bots                 map[string]Bot
	agentRegistrations   map[string]AgentRegistration
	agentProfiles        map[string]AgentProfile
	humanOwners          map[string]HumanOwner
	humanOwnerByEmail    map[string]string
	humanOwnerSessions   map[string]HumanOwnerSession
	agentBindings        map[string]AgentHumanBinding
	socialLinks          map[string]SocialLink
	socialRewardGrants   map[string]SocialRewardGrant
	accounts             map[string]TokenAccount
	ledger               []TokenLedger
	nextLedgerID         int64
	nextMessageID        int64
	nextMailboxID        int64
	mailbox              []MailItem
	notificationStates   map[string]NotificationDeliveryState
	archivedMessageIDs   map[int64]struct{}
	archivedMailboxIDs   map[int64]struct{}
	contacts             map[string]map[string]MailContact
	collab               map[string]CollabSession
	collabParts          []CollabParticipant
	nextCollabPID        int64
	collabArts           []CollabArtifact
	nextCollabAID        int64
	collabEvents         []CollabEvent
	nextCollabEID        int64
	nextReqLogID         int64
	requestLogs          []RequestLog
	nextKBEntryID        int64
	nextKBProposalID     int64
	nextKBChangeID       int64
	nextKBEnrollID       int64
	nextKBVoteID         int64
	nextKBThreadID       int64
	nextKBRevisionID     int64
	nextKBAckID          int64
	kbEntries            map[int64]KBEntry
	kbProposals          map[int64]KBProposal
	kbChanges            map[int64]KBProposalChange
	kbRevisions          []KBRevision
	kbAcks               []KBAck
	kbEnrollments        []KBProposalEnrollment
	kbVotes              []KBVote
	kbThreads            []KBThreadMessage
	tianDaoLaws          map[string]TianDaoLaw
	nextWorldTickID      int64
	worldTicks           []WorldTickRecord
	nextWorldTickStepID  int64
	worldTickSteps       []WorldTickStepRecord
	nextCostEventID      int64
	costEvents           []CostEvent
	worldSettings        map[string]WorldSetting
	userLifeStates       map[string]UserLifeState
	nextLifeTransitionID int64
	lifeTransitions      []UserLifeStateTransition
	nextGanglionID       int64
	ganglia              map[int64]Ganglion
	nextGanglionIntID    int64
	ganglionInts         []GanglionIntegration
	nextGanglionRateID   int64
	ganglionRatings      []GanglionRating
	ownerEconomyProfiles map[string]OwnerEconomyProfile
	onboardingGrants     map[string]OwnerOnboardingGrant
	commQuotaWindows     map[string]EconomyCommQuotaWindow
	contributionEvents   map[string]EconomyContributionEvent
	rewardDecisions      map[string]EconomyRewardDecision
	knowledgeMetaByProp  map[int64]EconomyKnowledgeMeta
	knowledgeMetaByEntry map[int64]EconomyKnowledgeMeta
	toolEconomyMeta      map[string]EconomyToolMeta
}

func NewInMemory() *InMemoryStore {
	return &InMemoryStore{
		bots:                 make(map[string]Bot),
		agentRegistrations:   make(map[string]AgentRegistration),
		agentProfiles:        make(map[string]AgentProfile),
		humanOwners:          make(map[string]HumanOwner),
		humanOwnerByEmail:    make(map[string]string),
		humanOwnerSessions:   make(map[string]HumanOwnerSession),
		agentBindings:        make(map[string]AgentHumanBinding),
		socialLinks:          make(map[string]SocialLink),
		socialRewardGrants:   make(map[string]SocialRewardGrant),
		accounts:             make(map[string]TokenAccount),
		notificationStates:   make(map[string]NotificationDeliveryState),
		archivedMessageIDs:   make(map[int64]struct{}),
		archivedMailboxIDs:   make(map[int64]struct{}),
		contacts:             make(map[string]map[string]MailContact),
		collab:               make(map[string]CollabSession),
		kbEntries:            make(map[int64]KBEntry),
		kbProposals:          make(map[int64]KBProposal),
		kbChanges:            make(map[int64]KBProposalChange),
		tianDaoLaws:          make(map[string]TianDaoLaw),
		worldSettings:        make(map[string]WorldSetting),
		userLifeStates:       make(map[string]UserLifeState),
		ganglia:              make(map[int64]Ganglion),
		ownerEconomyProfiles: make(map[string]OwnerEconomyProfile),
		onboardingGrants:     make(map[string]OwnerOnboardingGrant),
		commQuotaWindows:     make(map[string]EconomyCommQuotaWindow),
		contributionEvents:   make(map[string]EconomyContributionEvent),
		rewardDecisions:      make(map[string]EconomyRewardDecision),
		knowledgeMetaByProp:  make(map[int64]EconomyKnowledgeMeta),
		knowledgeMetaByEntry: make(map[int64]EconomyKnowledgeMeta),
		toolEconomyMeta:      make(map[string]EconomyToolMeta),
	}
}

func (s *InMemoryStore) Close() error { return nil }

func (s *InMemoryStore) ensureBot(botID string) {
	now := time.Now().UTC()
	if _, ok := s.bots[botID]; !ok {
		s.bots[botID] = Bot{
			BotID:       botID,
			Name:        botID,
			Provider:    "system",
			Status:      "active",
			Initialized: true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
	}
	if _, ok := s.accounts[botID]; !ok {
		s.accounts[botID] = TokenAccount{BotID: botID, Balance: 0, UpdatedAt: now}
	}
}

func (s *InMemoryStore) ListBots(_ context.Context) ([]Bot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]Bot, 0, len(s.bots))
	for _, b := range s.bots {
		items = append(items, b)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].BotID < items[j].BotID })
	return items, nil
}

func (s *InMemoryStore) GetBot(_ context.Context, botID string) (Bot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.bots[botID]; ok {
		return b, nil
	}
	return Bot{}, fmt.Errorf("%w: %s", ErrBotNotFound, botID)
}

func (s *InMemoryStore) UpsertBot(_ context.Context, input BotUpsertInput) (Bot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	current, ok := s.bots[input.BotID]
	if !ok {
		current = Bot{BotID: input.BotID, CreatedAt: now}
	}
	if input.Name != "" {
		current.Name = input.Name
	} else if current.Name == "" {
		current.Name = input.BotID
	}
	if input.Nickname != nil {
		current.Nickname = strings.TrimSpace(*input.Nickname)
	}
	if input.Provider != "" {
		current.Provider = input.Provider
	} else if current.Provider == "" {
		current.Provider = "generic"
	}
	if input.Status != "" {
		current.Status = input.Status
	} else if current.Status == "" {
		current.Status = "unknown"
	}
	current.Initialized = input.Initialized
	current.UpdatedAt = now
	s.bots[input.BotID] = current
	s.ensureBot(input.BotID)
	return current, nil
}

func (s *InMemoryStore) ActivateBotWithUniqueName(_ context.Context, botID, name string) (Bot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := strings.TrimSpace(botID)
	if uid == "" {
		return Bot{}, fmt.Errorf("user_id is required")
	}
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return Bot{}, fmt.Errorf("name is required")
	}
	current, ok := s.bots[uid]
	if !ok {
		return Bot{}, fmt.Errorf("%w: %s", ErrBotNotFound, uid)
	}
	for _, other := range s.bots {
		if other.BotID == uid {
			continue
		}
		if !other.Initialized || !isActiveStatus(other.Status) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(other.Name), n) {
			return Bot{}, fmt.Errorf("%w: %s", ErrBotNameTaken, n)
		}
	}
	current.Name = n
	current.Status = "running"
	current.Initialized = true
	current.UpdatedAt = time.Now().UTC()
	s.bots[uid] = current
	return current, nil
}

func isActiveStatus(status string) bool {
	s := strings.ToLower(strings.TrimSpace(status))
	return s != "deleted" && s != "inactive" && s != "system"
}

func (s *InMemoryStore) UpdateBotNickname(_ context.Context, botID, nickname string) (Bot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	uid := strings.TrimSpace(botID)
	if uid == "" {
		return Bot{}, fmt.Errorf("user_id is required")
	}
	current, ok := s.bots[uid]
	if !ok {
		return Bot{}, fmt.Errorf("%w: %s", ErrBotNotFound, uid)
	}
	current.Nickname = strings.TrimSpace(nickname)
	current.UpdatedAt = time.Now().UTC()
	s.bots[uid] = current
	return current, nil
}

func (s *InMemoryStore) EnsureTianDaoLaw(_ context.Context, item TianDaoLaw) (TianDaoLaw, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.LawKey = strings.TrimSpace(item.LawKey)
	if item.LawKey == "" {
		return TianDaoLaw{}, fmt.Errorf("law_key is required")
	}
	if item.ManifestJSON == "" || item.ManifestSHA256 == "" {
		return TianDaoLaw{}, fmt.Errorf("manifest_json and manifest_sha256 are required")
	}
	if current, ok := s.tianDaoLaws[item.LawKey]; ok {
		if current.Version != item.Version || current.ManifestSHA256 != item.ManifestSHA256 || current.ManifestJSON != item.ManifestJSON {
			return TianDaoLaw{}, fmt.Errorf("tian dao law %s is immutable and does not match existing manifest", item.LawKey)
		}
		return current, nil
	}
	item.CreatedAt = time.Now().UTC()
	s.tianDaoLaws[item.LawKey] = item
	return item, nil
}

func (s *InMemoryStore) GetTianDaoLaw(_ context.Context, lawKey string) (TianDaoLaw, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := strings.TrimSpace(lawKey)
	if key == "" {
		return TianDaoLaw{}, fmt.Errorf("law_key is required")
	}
	item, ok := s.tianDaoLaws[key]
	if !ok {
		return TianDaoLaw{}, fmt.Errorf("tian dao law not found: %s", key)
	}
	return item, nil
}

func (s *InMemoryStore) ListTianDaoLaws(_ context.Context) ([]TianDaoLaw, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]TianDaoLaw, 0, len(s.tianDaoLaws))
	for _, item := range s.tianDaoLaws {
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.Before(items[j].CreatedAt)
		}
		return items[i].LawKey < items[j].LawKey
	})
	return items, nil
}

func (s *InMemoryStore) AppendWorldTick(_ context.Context, item WorldTickRecord) (WorldTickRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextWorldTickID++
	item.ID = s.nextWorldTickID
	item.TriggerType = strings.TrimSpace(item.TriggerType)
	if item.TriggerType == "" {
		item.TriggerType = "scheduled"
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = time.Now().UTC()
	}
	prevHash := ""
	if n := len(s.worldTicks); n > 0 {
		prevHash = strings.TrimSpace(s.worldTicks[n-1].EntryHash)
		if prevHash == "" {
			prevHash = ComputeWorldTickHash(s.worldTicks[n-1], strings.TrimSpace(s.worldTicks[n-1].PrevHash))
		}
	}
	item.PrevHash = prevHash
	item.EntryHash = ComputeWorldTickHash(item, prevHash)
	s.worldTicks = append(s.worldTicks, item)
	return item, nil
}

func (s *InMemoryStore) ListWorldTicks(_ context.Context, limit int) ([]WorldTickRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	n := len(s.worldTicks)
	if n == 0 {
		return nil, nil
	}
	start := 0
	if n > limit {
		start = n - limit
	}
	src := s.worldTicks[start:]
	out := make([]WorldTickRecord, len(src))
	copy(out, src)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *InMemoryStore) GetWorldTick(_ context.Context, tickID int64) (WorldTickRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tickID <= 0 {
		return WorldTickRecord{}, fmt.Errorf("tick_id is required")
	}
	for i := len(s.worldTicks) - 1; i >= 0; i-- {
		if s.worldTicks[i].TickID == tickID {
			return s.worldTicks[i], nil
		}
	}
	return WorldTickRecord{}, fmt.Errorf("%w: %d", ErrWorldTickNotFound, tickID)
}

func (s *InMemoryStore) GetFirstWorldTick(_ context.Context) (WorldTickRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.worldTicks) == 0 {
		return WorldTickRecord{}, false, nil
	}
	return s.worldTicks[0], true, nil
}

func (s *InMemoryStore) AppendWorldTickStep(_ context.Context, item WorldTickStepRecord) (WorldTickStepRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.StepName = strings.TrimSpace(item.StepName)
	if item.StepName == "" {
		return WorldTickStepRecord{}, fmt.Errorf("step_name is required")
	}
	if item.Status == "" {
		item.Status = "ok"
	}
	s.nextWorldTickStepID++
	item.ID = s.nextWorldTickStepID
	if item.StartedAt.IsZero() {
		item.StartedAt = time.Now().UTC()
	}
	s.worldTickSteps = append(s.worldTickSteps, item)
	return item, nil
}

func (s *InMemoryStore) ListWorldTickSteps(_ context.Context, tickID int64, limit int) ([]WorldTickStepRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	capHint := len(s.worldTickSteps)
	if capHint > limit {
		capHint = limit
	}
	out := make([]WorldTickStepRecord, 0, capHint)
	for i := len(s.worldTickSteps) - 1; i >= 0; i-- {
		it := s.worldTickSteps[i]
		if tickID > 0 && it.TickID != tickID {
			continue
		}
		out = append(out, it)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func normalizeLifeState(state string) string {
	switch strings.TrimSpace(strings.ToLower(state)) {
	case "alive":
		return "alive"
	case "hibernating", "dying", "hibernated":
		return "hibernating"
	case "dead":
		return "dead"
	default:
		return ""
	}
}

func (s *InMemoryStore) UpsertUserLifeState(ctx context.Context, item UserLifeState) (UserLifeState, error) {
	updated, _, err := s.ApplyUserLifeState(ctx, item, UserLifeStateAuditMeta{
		SourceModule: "life.state",
		SourceRef:    "store.upsert",
	})
	if err != nil {
		return UserLifeState{}, err
	}
	return updated, nil
}

func (s *InMemoryStore) ApplyUserLifeState(_ context.Context, item UserLifeState, audit UserLifeStateAuditMeta) (UserLifeState, *UserLifeStateTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	updated, transition, _, err := s.applyUserLifeStateLocked(item, audit, true)
	if err != nil {
		return UserLifeState{}, nil, err
	}
	return updated, transition, nil
}

func (s *InMemoryStore) applyUserLifeStateLocked(item UserLifeState, audit UserLifeStateAuditMeta, recordTransition bool) (UserLifeState, *UserLifeStateTransition, bool, error) {
	item.UserID = strings.TrimSpace(item.UserID)
	item.State = normalizeLifeState(item.State)
	if item.UserID == "" {
		return UserLifeState{}, nil, false, fmt.Errorf("user_id is required")
	}
	if item.State == "" {
		item.State = "alive"
	}
	prev, existed := s.userLifeStates[item.UserID]
	if existed && normalizeLifeState(prev.State) == "dead" && item.State != "dead" {
		return UserLifeState{}, nil, false, fmt.Errorf("user life state is immutable once dead: %s", item.UserID)
	}
	item.UpdatedAt = time.Now().UTC()
	s.userLifeStates[item.UserID] = item
	if !recordTransition {
		return item, nil, false, nil
	}
	prevState := ""
	if existed {
		prevState = normalizeLifeState(prev.State)
	}
	if existed && prevState == item.State {
		return item, nil, false, nil
	}
	s.nextLifeTransitionID++
	transition := UserLifeStateTransition{
		ID:                 s.nextLifeTransitionID,
		UserID:             item.UserID,
		FromState:          prevState,
		ToState:            item.State,
		FromDyingSinceTick: prev.DyingSinceTick,
		ToDyingSinceTick:   item.DyingSinceTick,
		FromDeadAtTick:     prev.DeadAtTick,
		ToDeadAtTick:       item.DeadAtTick,
		FromReason:         strings.TrimSpace(prev.Reason),
		ToReason:           strings.TrimSpace(item.Reason),
		TickID:             audit.TickID,
		SourceModule:       strings.TrimSpace(audit.SourceModule),
		SourceRef:          strings.TrimSpace(audit.SourceRef),
		ActorUserID:        strings.TrimSpace(audit.ActorUserID),
		CreatedAt:          item.UpdatedAt,
	}
	if transition.SourceModule == "" {
		transition.SourceModule = "life.state"
	}
	s.lifeTransitions = append(s.lifeTransitions, transition)
	return item, &transition, true, nil
}

func (s *InMemoryStore) GetUserLifeState(_ context.Context, userID string) (UserLifeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return UserLifeState{}, fmt.Errorf("user_id is required")
	}
	item, ok := s.userLifeStates[userID]
	if !ok {
		return UserLifeState{}, fmt.Errorf("%w: %s", ErrUserLifeStateNotFound, userID)
	}
	return item, nil
}

func (s *InMemoryStore) ListUserLifeStates(_ context.Context, userID, state string, limit int) ([]UserLifeState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID = strings.TrimSpace(userID)
	state = normalizeLifeState(state)
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	out := make([]UserLifeState, 0, len(s.userLifeStates))
	for _, it := range s.userLifeStates {
		if userID != "" && it.UserID != userID {
			continue
		}
		if state != "" && normalizeLifeState(it.State) != state {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UserID < out[j].UserID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) ListUserLifeStateTransitions(_ context.Context, filter UserLifeStateTransitionFilter) ([]UserLifeStateTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	filter.UserID = strings.TrimSpace(filter.UserID)
	if strings.TrimSpace(filter.FromState) != "" {
		filter.FromState = normalizeLifeState(filter.FromState)
	}
	if strings.TrimSpace(filter.ToState) != "" {
		filter.ToState = normalizeLifeState(filter.ToState)
	}
	filter.SourceModule = strings.TrimSpace(filter.SourceModule)
	filter.ActorUserID = strings.TrimSpace(filter.ActorUserID)
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 2000 {
		filter.Limit = 2000
	}
	out := make([]UserLifeStateTransition, 0, len(s.lifeTransitions))
	for i := len(s.lifeTransitions) - 1; i >= 0; i-- {
		it := s.lifeTransitions[i]
		if filter.UserID != "" && it.UserID != filter.UserID {
			continue
		}
		if filter.FromState != "" && it.FromState != filter.FromState {
			continue
		}
		if filter.ToState != "" && it.ToState != filter.ToState {
			continue
		}
		if filter.TickID > 0 && it.TickID != filter.TickID {
			continue
		}
		if filter.SourceModule != "" && strings.TrimSpace(it.SourceModule) != filter.SourceModule {
			continue
		}
		if filter.ActorUserID != "" && strings.TrimSpace(it.ActorUserID) != filter.ActorUserID {
			continue
		}
		out = append(out, it)
		if len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (s *InMemoryStore) AppendCostEvent(_ context.Context, item CostEvent) (CostEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.UserID = strings.TrimSpace(item.UserID)
	item.CostType = strings.TrimSpace(item.CostType)
	if item.UserID == "" {
		return CostEvent{}, fmt.Errorf("user_id is required")
	}
	if item.CostType == "" {
		return CostEvent{}, fmt.Errorf("cost_type is required")
	}
	s.nextCostEventID++
	item.ID = s.nextCostEventID
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	s.costEvents = append(s.costEvents, item)
	return item, nil
}

func (s *InMemoryStore) ListCostEvents(_ context.Context, userID string, limit int) ([]CostEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID = strings.TrimSpace(userID)
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	capHint := len(s.costEvents)
	if capHint > limit {
		capHint = limit
	}
	out := make([]CostEvent, 0, capHint)
	for i := len(s.costEvents) - 1; i >= 0; i-- {
		it := s.costEvents[i]
		if userID != "" && it.UserID != userID {
			continue
		}
		out = append(out, it)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *InMemoryStore) ListCostEventsByInvolvement(_ context.Context, userID string, limit int) ([]CostEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID = strings.TrimSpace(userID)
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	capHint := len(s.costEvents)
	if capHint > limit {
		capHint = limit
	}
	out := make([]CostEvent, 0, capHint)
	for i := len(s.costEvents) - 1; i >= 0; i-- {
		it := s.costEvents[i]
		if userID != "" && it.UserID != userID && costEventRecipientUserID(it.MetaJSON) != userID {
			continue
		}
		out = append(out, it)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *InMemoryStore) GetWorldSetting(_ context.Context, key string) (WorldSetting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key = strings.TrimSpace(key)
	if key == "" {
		return WorldSetting{}, fmt.Errorf("setting key is required")
	}
	it, ok := s.worldSettings[key]
	if !ok {
		return WorldSetting{}, fmt.Errorf("world setting not found: %s", key)
	}
	return it, nil
}

func (s *InMemoryStore) UpsertWorldSetting(_ context.Context, item WorldSetting) (WorldSetting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.Key = strings.TrimSpace(item.Key)
	if item.Key == "" {
		return WorldSetting{}, fmt.Errorf("setting key is required")
	}
	item.UpdatedAt = time.Now().UTC()
	s.worldSettings[item.Key] = item
	return item, nil
}

func (s *InMemoryStore) SendMail(_ context.Context, input MailSendInput) (MailSendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.nextMessageID++
	msgID := s.nextMessageID

	for _, recipient := range input.To {
		if strings.TrimSpace(recipient) == "" {
			continue
		}
		s.nextMailboxID++
		s.mailbox = append(s.mailbox, MailItem{
			MailboxID:        s.nextMailboxID,
			MessageID:        msgID,
			OwnerAddress:     recipient,
			Folder:           "inbox",
			FromAddress:      input.From,
			ToAddress:        recipient,
			Subject:          input.Subject,
			Body:             input.Body,
			ReplyToMailboxID: input.ReplyToMailboxID,
			IsRead:           false,
			SentAt:           now,
		})
	}

	for _, recipient := range input.To {
		if strings.TrimSpace(recipient) == "" {
			continue
		}
		s.nextMailboxID++
		s.mailbox = append(s.mailbox, MailItem{
			MailboxID:        s.nextMailboxID,
			MessageID:        msgID,
			OwnerAddress:     input.From,
			Folder:           "outbox",
			FromAddress:      input.From,
			ToAddress:        recipient,
			Subject:          input.Subject,
			Body:             input.Body,
			ReplyToMailboxID: input.ReplyToMailboxID,
			IsRead:           true,
			SentAt:           now,
		})
	}

	return MailSendResult{
		MessageID:        msgID,
		From:             input.From,
		To:               append([]string(nil), input.To...),
		Subject:          input.Subject,
		ReplyToMailboxID: input.ReplyToMailboxID,
		SentAt:           now,
	}, nil
}

func (s *InMemoryStore) ListMailbox(_ context.Context, ownerAddress, folder, scope, keyword string, fromTime, toTime *time.Time, limit int) ([]MailItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kw := strings.ToLower(strings.TrimSpace(keyword))
	out := make([]MailItem, 0)
	for _, it := range s.mailbox {
		if it.OwnerAddress != ownerAddress {
			continue
		}
		if folder != "" && it.Folder != folder {
			continue
		}
		if scope == "unread" && it.IsRead {
			continue
		}
		if scope == "read" && !it.IsRead {
			continue
		}
		if fromTime != nil && it.SentAt.Before(*fromTime) {
			continue
		}
		if toTime != nil && it.SentAt.After(*toTime) {
			continue
		}
		if kw != "" {
			sub := strings.ToLower(it.Subject)
			body := strings.ToLower(it.Body)
			if !strings.Contains(sub, kw) && !strings.Contains(body, kw) {
				continue
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SentAt.Equal(out[j].SentAt) {
			return out[i].MailboxID > out[j].MailboxID
		}
		return out[i].SentAt.After(out[j].SentAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) GetMailboxItem(_ context.Context, mailboxID int64) (MailItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.mailbox {
		if item.MailboxID == mailboxID {
			return item, nil
		}
	}
	return MailItem{}, fmt.Errorf("mailbox item not found: %d", mailboxID)
}

func (s *InMemoryStore) MarkMailboxRead(_ context.Context, ownerAddress string, mailboxIDs []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[int64]struct{}, len(mailboxIDs))
	for _, id := range mailboxIDs {
		set[id] = struct{}{}
	}
	now := time.Now().UTC()
	for i, it := range s.mailbox {
		if it.OwnerAddress != ownerAddress || it.Folder != "inbox" {
			continue
		}
		if _, ok := set[it.MailboxID]; !ok {
			continue
		}
		it.IsRead = true
		it.ReadAt = &now
		s.mailbox[i] = it
	}
	return nil
}

func (s *InMemoryStore) UpdateMailMessage(_ context.Context, messageID int64, subject, body string, sentAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if messageID <= 0 {
		return fmt.Errorf("message_id is required")
	}
	updated := false
	subject = strings.TrimSpace(subject)
	body = strings.TrimSpace(body)
	sentAt = sentAt.UTC()
	for i, item := range s.mailbox {
		if item.MessageID != messageID {
			continue
		}
		item.Subject = subject
		item.Body = body
		item.SentAt = sentAt
		s.mailbox[i] = item
		updated = true
	}
	if !updated {
		return fmt.Errorf("mail message not found: %d", messageID)
	}
	return nil
}

func notificationStateKey(ownerAddress, category string) string {
	return strings.TrimSpace(ownerAddress) + "\x00" + strings.TrimSpace(category)
}

func normalizeSystemMailArchiveCategory(category string) string {
	switch strings.TrimSpace(strings.ToLower(category)) {
	case "world_cost", "world-cost", "world_cost_alert":
		return "world_cost"
	case "low_token", "low-token":
		return "low_token"
	case "autonomy_loop", "autonomy-loop":
		return "autonomy_loop"
	case "community_collab", "community-collab":
		return "community_collab"
	default:
		return ""
	}
}

func systemMailArchiveCategoryFromMail(item MailItem) string {
	if strings.TrimSpace(item.FromAddress) != "clawcolony-admin" {
		return ""
	}
	subject := strings.ToUpper(strings.TrimSpace(item.Subject))
	switch {
	case strings.HasPrefix(subject, "[WORLD-COST-ALERT]"):
		return "world_cost"
	case strings.HasPrefix(subject, "[LOW-TOKEN]"):
		return "low_token"
	case strings.HasPrefix(subject, "[AUTONOMY-LOOP]"):
		return "autonomy_loop"
	case strings.HasPrefix(subject, "[COMMUNITY-COLLAB]"):
		return "community_collab"
	default:
		return ""
	}
}

func (s *InMemoryStore) GetNotificationDeliveryState(_ context.Context, ownerAddress, category string) (NotificationDeliveryState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.notificationStates[notificationStateKey(ownerAddress, category)]
	return state, ok, nil
}

func (s *InMemoryStore) UpsertNotificationDeliveryState(_ context.Context, state NotificationDeliveryState) (NotificationDeliveryState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state.OwnerAddress = strings.TrimSpace(state.OwnerAddress)
	state.Category = strings.TrimSpace(state.Category)
	if state.OwnerAddress == "" || state.Category == "" {
		return NotificationDeliveryState{}, fmt.Errorf("owner_address and category are required")
	}
	now := time.Now().UTC()
	current, ok := s.notificationStates[notificationStateKey(state.OwnerAddress, state.Category)]
	if ok {
		if state.LastSentAt.IsZero() {
			state.LastSentAt = current.LastSentAt
		}
		if state.LastRemindedAt.IsZero() {
			state.LastRemindedAt = current.LastRemindedAt
		}
	}
	state.UpdatedAt = now
	s.notificationStates[notificationStateKey(state.OwnerAddress, state.Category)] = state
	return state, nil
}

func (s *InMemoryStore) DeleteNotificationDeliveryState(_ context.Context, ownerAddress, category string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.notificationStates, notificationStateKey(ownerAddress, category))
	return nil
}

func (s *InMemoryStore) PreviewSystemMailArchive(_ context.Context, categories []string) (MailArchivePreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	allowed := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		if normalized := normalizeSystemMailArchiveCategory(category); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return MailArchivePreview{}, nil
	}
	type ownerCategory struct {
		owner    string
		category string
	}
	type mailboxRef struct {
		inboxIndex int
		outboxIDs  []int64
		messageID  int64
	}
	groups := make(map[ownerCategory][]mailboxRef)
	for idx, item := range s.mailbox {
		if item.Folder != "inbox" {
			continue
		}
		category := systemMailArchiveCategoryFromMail(item)
		if _, ok := allowed[category]; !ok {
			continue
		}
		ref := mailboxRef{inboxIndex: idx, messageID: item.MessageID}
		for _, outbox := range s.mailbox {
			if outbox.Folder != "outbox" {
				continue
			}
			if outbox.MessageID != item.MessageID {
				continue
			}
			if strings.TrimSpace(outbox.OwnerAddress) != "clawcolony-admin" {
				continue
			}
			if strings.TrimSpace(outbox.ToAddress) != strings.TrimSpace(item.OwnerAddress) {
				continue
			}
			ref.outboxIDs = append(ref.outboxIDs, outbox.MailboxID)
		}
		key := ownerCategory{owner: item.OwnerAddress, category: category}
		groups[key] = append(groups[key], ref)
	}
	statsByCategory := make(map[string]*MailArchiveCategoryStat, len(allowed))
	var archiveMailboxCount int64
	messageIDs := make(map[int64]struct{})
	for key, refs := range groups {
		sort.SliceStable(refs, func(i, j int) bool {
			left := s.mailbox[refs[i].inboxIndex]
			right := s.mailbox[refs[j].inboxIndex]
			if left.SentAt.Equal(right.SentAt) {
				return left.MailboxID > right.MailboxID
			}
			return left.SentAt.After(right.SentAt)
		})
		stat := statsByCategory[key.category]
		if stat == nil {
			stat = &MailArchiveCategoryStat{Category: key.category}
			statsByCategory[key.category] = stat
		}
		stat.InboxKeepCount++
		for _, ref := range refs[1:] {
			stat.InboxArchiveCnt++
			stat.OutboxArchiveCt += int64(len(ref.outboxIDs))
			archiveMailboxCount += 1 + int64(len(ref.outboxIDs))
			messageIDs[ref.messageID] = struct{}{}
		}
	}
	categoriesOut := make([]MailArchiveCategoryStat, 0, len(statsByCategory))
	for _, category := range categories {
		normalized := normalizeSystemMailArchiveCategory(category)
		if normalized == "" {
			continue
		}
		if stat, ok := statsByCategory[normalized]; ok {
			categoriesOut = append(categoriesOut, *stat)
		}
	}
	if len(categoriesOut) == 0 {
		for _, stat := range statsByCategory {
			categoriesOut = append(categoriesOut, *stat)
		}
		sort.Slice(categoriesOut, func(i, j int) bool { return categoriesOut[i].Category < categoriesOut[j].Category })
	}
	return MailArchivePreview{
		Categories:          categoriesOut,
		ArchiveMailboxCount: archiveMailboxCount,
		ArchiveMessageCount: int64(len(messageIDs)),
	}, nil
}

func (s *InMemoryStore) ArchiveSystemMailBatch(_ context.Context, input MailArchiveBatchInput) (MailArchiveBatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := input.Limit
	if limit <= 0 {
		limit = 10000
	}
	allowed := make(map[string]struct{}, len(input.Categories))
	for _, category := range input.Categories {
		if normalized := normalizeSystemMailArchiveCategory(category); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return MailArchiveBatchResult{BatchID: strings.TrimSpace(input.BatchID)}, nil
	}
	type inboxCandidate struct {
		index    int
		mailbox  MailItem
		category string
	}
	grouped := make(map[string][]inboxCandidate)
	for idx, item := range s.mailbox {
		if item.Folder != "inbox" {
			continue
		}
		category := systemMailArchiveCategoryFromMail(item)
		if _, ok := allowed[category]; !ok {
			continue
		}
		key := item.OwnerAddress + "\x00" + category
		grouped[key] = append(grouped[key], inboxCandidate{index: idx, mailbox: item, category: category})
	}
	candidates := make([]inboxCandidate, 0)
	for _, items := range grouped {
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].mailbox.SentAt.Equal(items[j].mailbox.SentAt) {
				return items[i].mailbox.MailboxID > items[j].mailbox.MailboxID
			}
			return items[i].mailbox.SentAt.After(items[j].mailbox.SentAt)
		})
		if len(items) > 1 {
			candidates = append(candidates, items[1:]...)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].mailbox.MailboxID < candidates[j].mailbox.MailboxID
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	if len(candidates) == 0 {
		return MailArchiveBatchResult{BatchID: strings.TrimSpace(input.BatchID)}, nil
	}
	selectedMailboxIDs := make(map[int64]struct{})
	selectedMessageIDs := make(map[int64]struct{})
	statsByCategory := make(map[string]*MailArchiveCategoryStat)
	lastInboxMailboxID := int64(0)
	for _, candidate := range candidates {
		selectedMailboxIDs[candidate.mailbox.MailboxID] = struct{}{}
		selectedMessageIDs[candidate.mailbox.MessageID] = struct{}{}
		if candidate.mailbox.MailboxID > lastInboxMailboxID {
			lastInboxMailboxID = candidate.mailbox.MailboxID
		}
		stat := statsByCategory[candidate.category]
		if stat == nil {
			stat = &MailArchiveCategoryStat{Category: candidate.category}
			statsByCategory[candidate.category] = stat
		}
		stat.InboxArchiveCnt++
		for _, item := range s.mailbox {
			if item.Folder != "outbox" {
				continue
			}
			if item.MessageID != candidate.mailbox.MessageID {
				continue
			}
			if strings.TrimSpace(item.OwnerAddress) != "clawcolony-admin" {
				continue
			}
			if strings.TrimSpace(item.ToAddress) != strings.TrimSpace(candidate.mailbox.OwnerAddress) {
				continue
			}
			selectedMailboxIDs[item.MailboxID] = struct{}{}
			stat.OutboxArchiveCt++
		}
	}
	remaining := make([]MailItem, 0, len(s.mailbox))
	archivedMailboxIDs := make([]int64, 0, len(selectedMailboxIDs))
	for _, item := range s.mailbox {
		if _, ok := selectedMailboxIDs[item.MailboxID]; ok {
			s.archivedMailboxIDs[item.MailboxID] = struct{}{}
			archivedMailboxIDs = append(archivedMailboxIDs, item.MailboxID)
			continue
		}
		remaining = append(remaining, item)
	}
	s.mailbox = remaining
	archivedMessageIDs := make([]int64, 0, len(selectedMessageIDs))
	for messageID := range selectedMessageIDs {
		stillLive := false
		for _, item := range s.mailbox {
			if item.MessageID == messageID {
				stillLive = true
				break
			}
		}
		if stillLive {
			continue
		}
		if _, seen := s.archivedMessageIDs[messageID]; seen {
			continue
		}
		s.archivedMessageIDs[messageID] = struct{}{}
		archivedMessageIDs = append(archivedMessageIDs, messageID)
	}
	sort.Slice(archivedMailboxIDs, func(i, j int) bool { return archivedMailboxIDs[i] < archivedMailboxIDs[j] })
	sort.Slice(archivedMessageIDs, func(i, j int) bool { return archivedMessageIDs[i] < archivedMessageIDs[j] })
	stats := make([]MailArchiveCategoryStat, 0, len(statsByCategory))
	for _, category := range input.Categories {
		if normalized := normalizeSystemMailArchiveCategory(category); normalized != "" {
			if stat, ok := statsByCategory[normalized]; ok {
				stats = append(stats, *stat)
			}
		}
	}
	hasMore := false
	for _, item := range s.mailbox {
		if item.Folder != "inbox" {
			continue
		}
		category := systemMailArchiveCategoryFromMail(item)
		if _, ok := allowed[category]; !ok {
			continue
		}
		key := item.OwnerAddress + "\x00" + category
		count := 0
		for _, other := range s.mailbox {
			if other.Folder != "inbox" {
				continue
			}
			if other.OwnerAddress != item.OwnerAddress {
				continue
			}
			if systemMailArchiveCategoryFromMail(other) != category {
				continue
			}
			count++
			if count > 1 {
				hasMore = true
				break
			}
		}
		if hasMore {
			break
		}
		_ = key
	}
	return MailArchiveBatchResult{
		BatchID:             strings.TrimSpace(input.BatchID),
		ArchivedMailboxIDs:  archivedMailboxIDs,
		ArchivedMessageIDs:  archivedMessageIDs,
		ArchiveMailboxCount: int64(len(archivedMailboxIDs)),
		ArchiveMessageCount: int64(len(archivedMessageIDs)),
		LastInboxMailboxID:  lastInboxMailboxID,
		HasMore:             hasMore,
		Categories:          stats,
	}, nil
}

func (s *InMemoryStore) UpsertMailContact(_ context.Context, c MailContact) (MailContact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.OwnerAddress = strings.TrimSpace(c.OwnerAddress)
	c.ContactAddress = strings.TrimSpace(c.ContactAddress)
	if c.OwnerAddress == "" || c.ContactAddress == "" {
		return MailContact{}, fmt.Errorf("owner_address and contact_address are required")
	}
	c.UpdatedAt = time.Now().UTC()
	if s.contacts[c.OwnerAddress] == nil {
		s.contacts[c.OwnerAddress] = map[string]MailContact{}
	}
	s.contacts[c.OwnerAddress][c.ContactAddress] = c
	return c, nil
}

func (s *InMemoryStore) ListMailContacts(_ context.Context, ownerAddress, keyword string, limit int) ([]MailContact, error) {
	return s.listMailContacts(ownerAddress, keyword, nil, nil, limit)
}

func (s *InMemoryStore) ListMailContactsUpdated(_ context.Context, ownerAddress, keyword string, fromTime, toTime *time.Time, limit int) ([]MailContact, error) {
	return s.listMailContacts(ownerAddress, keyword, fromTime, toTime, limit)
}

func (s *InMemoryStore) listMailContacts(ownerAddress, keyword string, fromTime, toTime *time.Time, limit int) ([]MailContact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kw := strings.ToLower(strings.TrimSpace(keyword))
	out := make([]MailContact, 0)
	for _, c := range s.contacts[ownerAddress] {
		if fromTime != nil && c.UpdatedAt.Before(*fromTime) {
			continue
		}
		if toTime != nil && c.UpdatedAt.After(*toTime) {
			continue
		}
		if kw != "" {
			if !strings.Contains(strings.ToLower(c.ContactAddress), kw) &&
				!strings.Contains(strings.ToLower(c.DisplayName), kw) &&
				!strings.Contains(strings.ToLower(strings.Join(c.Tags, ",")), kw) &&
				!strings.Contains(strings.ToLower(c.Role), kw) &&
				!strings.Contains(strings.ToLower(strings.Join(c.Skills, ",")), kw) &&
				!strings.Contains(strings.ToLower(c.CurrentProject), kw) {
				continue
			}
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ContactAddress < out[j].ContactAddress
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) ListTokenAccounts(_ context.Context) ([]TokenAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]TokenAccount, 0, len(s.accounts))
	for _, a := range s.accounts {
		items = append(items, a)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].BotID < items[j].BotID })
	return items, nil
}

func (s *InMemoryStore) Recharge(_ context.Context, botID string, amount int64) (TokenLedger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureBot(botID)
	account := s.accounts[botID]
	if amount > 0 && account.Balance > (math.MaxInt64-amount) {
		return TokenLedger{}, ErrBalanceOverflow
	}
	account.Balance += amount
	account.UpdatedAt = time.Now().UTC()
	s.accounts[botID] = account
	s.nextLedgerID++
	entry := TokenLedger{
		ID:           s.nextLedgerID,
		BotID:        botID,
		OpType:       "recharge",
		Amount:       amount,
		BalanceAfter: account.Balance,
		CreatedAt:    time.Now().UTC(),
	}
	s.ledger = append(s.ledger, entry)
	return entry, nil
}

func (s *InMemoryStore) Consume(_ context.Context, botID string, amount int64) (TokenLedger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureBot(botID)
	account := s.accounts[botID]
	if account.Balance < amount {
		return TokenLedger{}, ErrInsufficientBalance
	}
	account.Balance -= amount
	account.UpdatedAt = time.Now().UTC()
	s.accounts[botID] = account
	s.nextLedgerID++
	entry := TokenLedger{
		ID:           s.nextLedgerID,
		BotID:        botID,
		OpType:       "consume",
		Amount:       amount,
		BalanceAfter: account.Balance,
		CreatedAt:    time.Now().UTC(),
	}
	s.ledger = append(s.ledger, entry)
	return entry, nil
}

func (s *InMemoryStore) Transfer(_ context.Context, fromBotID, toBotID string, amount int64) (TokenTransfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transferLocked(fromBotID, toBotID, amount, false)
}

func (s *InMemoryStore) TransferWithFloor(_ context.Context, fromBotID, toBotID string, amount int64) (TokenTransfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transferLocked(fromBotID, toBotID, amount, true)
}

func (s *InMemoryStore) transferLocked(fromBotID, toBotID string, amount int64, floor bool) (TokenTransfer, error) {
	fromBotID = strings.TrimSpace(fromBotID)
	toBotID = strings.TrimSpace(toBotID)
	if fromBotID == "" || toBotID == "" || amount <= 0 || fromBotID == toBotID {
		return TokenTransfer{}, nil
	}
	s.ensureBot(fromBotID)
	s.ensureBot(toBotID)
	from := s.accounts[fromBotID]
	to := s.accounts[toBotID]
	deducted := amount
	if from.Balance < deducted && !floor {
		return TokenTransfer{}, ErrInsufficientBalance
	}
	if from.Balance < deducted && floor {
		deducted = from.Balance
	}
	if deducted <= 0 {
		return TokenTransfer{}, nil
	}
	if deducted > 0 && to.Balance > (math.MaxInt64-deducted) {
		return TokenTransfer{}, ErrBalanceOverflow
	}
	from.Balance -= deducted
	from.UpdatedAt = time.Now().UTC()
	s.accounts[fromBotID] = from
	s.nextLedgerID++
	fromEntry := TokenLedger{
		ID:           s.nextLedgerID,
		BotID:        fromBotID,
		OpType:       "consume",
		Amount:       deducted,
		BalanceAfter: from.Balance,
		CreatedAt:    time.Now().UTC(),
	}
	s.ledger = append(s.ledger, fromEntry)

	to.Balance += deducted
	to.UpdatedAt = time.Now().UTC()
	s.accounts[toBotID] = to
	s.nextLedgerID++
	toEntry := TokenLedger{
		ID:           s.nextLedgerID,
		BotID:        toBotID,
		OpType:       "recharge",
		Amount:       deducted,
		BalanceAfter: to.Balance,
		CreatedAt:    time.Now().UTC(),
	}
	s.ledger = append(s.ledger, toEntry)
	return TokenTransfer{Deducted: deducted, FromLedger: fromEntry, ToLedger: toEntry}, nil
}

func (s *InMemoryStore) ListTokenLedger(_ context.Context, botID string, limit int) ([]TokenLedger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := make([]TokenLedger, 0)
	for _, l := range s.ledger {
		if botID != "" && l.BotID != botID {
			continue
		}
		items = append(items, l)
	}
	if limit > 0 && len(items) > limit {
		items = items[len(items)-limit:]
	}
	return items, nil
}

func (s *InMemoryStore) CreateCollabSession(_ context.Context, item CollabSession) (CollabSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	item.CollabID = strings.TrimSpace(item.CollabID)
	if item.CollabID == "" {
		return CollabSession{}, fmt.Errorf("collab_id is required")
	}
	item.CreatedAt = now
	item.UpdatedAt = now
	s.collab[item.CollabID] = item
	return item, nil
}

func (s *InMemoryStore) GetCollabSession(_ context.Context, collabID string) (CollabSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.collab[strings.TrimSpace(collabID)]
	if !ok {
		return CollabSession{}, fmt.Errorf("collab not found")
	}
	return it, nil
}

func (s *InMemoryStore) ListCollabSessions(_ context.Context, kind, phase, proposerUserID string, limit int) ([]CollabSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kind = strings.TrimSpace(kind)
	phase = strings.TrimSpace(phase)
	proposerUserID = strings.TrimSpace(proposerUserID)
	if limit <= 0 {
		limit = 100
	}
	out := make([]CollabSession, 0, len(s.collab))
	for _, it := range s.collab {
		if kind != "" && it.Kind != kind {
			continue
		}
		if phase != "" && it.Phase != phase {
			continue
		}
		if proposerUserID != "" && it.ProposerUserID != proposerUserID {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].CollabID > out[j].CollabID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) UpdateCollabPhase(_ context.Context, collabID, phase, orchestratorUserID, statusSummary string, closedAt *time.Time) (CollabSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.collab[strings.TrimSpace(collabID)]
	if !ok {
		return CollabSession{}, fmt.Errorf("collab not found")
	}
	now := time.Now().UTC()
	if strings.TrimSpace(phase) != "" {
		it.Phase = strings.TrimSpace(phase)
	}
	if strings.TrimSpace(orchestratorUserID) != "" {
		it.OrchestratorUserID = strings.TrimSpace(orchestratorUserID)
	}
	it.LastStatusOrSummary = strings.TrimSpace(statusSummary)
	it.ClosedAt = closedAt
	it.UpdatedAt = now
	s.collab[it.CollabID] = it
	return it, nil
}

func (s *InMemoryStore) UpdateCollabPR(_ context.Context, input CollabPRUpdate) (CollabSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.collab[strings.TrimSpace(input.CollabID)]
	if !ok {
		return CollabSession{}, fmt.Errorf("collab not found")
	}
	now := time.Now().UTC()
	if strings.TrimSpace(input.PRBranch) != "" {
		it.PRBranch = strings.TrimSpace(input.PRBranch)
	}
	if strings.TrimSpace(input.PRURL) != "" {
		it.PRURL = strings.TrimSpace(input.PRURL)
	}
	if input.PRNumber > 0 {
		it.PRNumber = input.PRNumber
	}
	if strings.TrimSpace(input.PRBaseSHA) != "" {
		it.PRBaseSHA = strings.TrimSpace(input.PRBaseSHA)
	}
	if strings.TrimSpace(input.PRHeadSHA) != "" {
		it.PRHeadSHA = strings.TrimSpace(input.PRHeadSHA)
	}
	if strings.TrimSpace(input.PRAuthorLogin) != "" {
		it.PRAuthorLogin = strings.TrimSpace(input.PRAuthorLogin)
	}
	if strings.TrimSpace(input.GitHubPRState) != "" {
		it.GitHubPRState = strings.TrimSpace(input.GitHubPRState)
	}
	if strings.TrimSpace(input.PRMergeCommitSHA) != "" {
		it.PRMergeCommitSHA = strings.TrimSpace(input.PRMergeCommitSHA)
	}
	if input.ReviewDeadlineAt != nil {
		it.ReviewDeadlineAt = input.ReviewDeadlineAt
	}
	if input.PRMergedAt != nil {
		it.PRMergedAt = input.PRMergedAt
	}
	it.UpdatedAt = now
	s.collab[it.CollabID] = it
	return it, nil
}

func (s *InMemoryStore) UpsertCollabParticipant(_ context.Context, item CollabParticipant) (CollabParticipant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.CollabID = strings.TrimSpace(item.CollabID)
	item.UserID = strings.TrimSpace(item.UserID)
	if item.CollabID == "" || item.UserID == "" {
		return CollabParticipant{}, fmt.Errorf("collab_id and user_id are required")
	}
	now := time.Now().UTC()
	for i := range s.collabParts {
		if s.collabParts[i].CollabID == item.CollabID && s.collabParts[i].UserID == item.UserID {
			s.collabParts[i].Role = item.Role
			s.collabParts[i].Status = item.Status
			s.collabParts[i].Pitch = item.Pitch
			s.collabParts[i].ApplicationKind = item.ApplicationKind
			s.collabParts[i].EvidenceURL = item.EvidenceURL
			s.collabParts[i].Verified = item.Verified
			s.collabParts[i].GitHubLogin = item.GitHubLogin
			s.collabParts[i].UpdatedAt = now
			return s.collabParts[i], nil
		}
	}
	s.nextCollabPID++
	item.ID = s.nextCollabPID
	item.CreatedAt = now
	item.UpdatedAt = now
	s.collabParts = append(s.collabParts, item)
	return item, nil
}

func (s *InMemoryStore) ListCollabParticipants(_ context.Context, collabID, status string, limit int) ([]CollabParticipant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	collabID = strings.TrimSpace(collabID)
	status = strings.TrimSpace(status)
	if limit <= 0 {
		limit = 500
	}
	out := make([]CollabParticipant, 0)
	for _, it := range s.collabParts {
		if collabID != "" && it.CollabID != collabID {
			continue
		}
		if status != "" && it.Status != status {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) CreateCollabArtifact(_ context.Context, item CollabArtifact) (CollabArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.nextCollabAID++
	item.ID = s.nextCollabAID
	item.CreatedAt = now
	item.UpdatedAt = now
	s.collabArts = append(s.collabArts, item)
	return item, nil
}

func (s *InMemoryStore) UpdateCollabArtifactReview(_ context.Context, artifactID int64, status, reviewNote string) (CollabArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.collabArts {
		if s.collabArts[i].ID != artifactID {
			continue
		}
		s.collabArts[i].Status = strings.TrimSpace(status)
		s.collabArts[i].ReviewNote = strings.TrimSpace(reviewNote)
		s.collabArts[i].UpdatedAt = time.Now().UTC()
		return s.collabArts[i], nil
	}
	return CollabArtifact{}, fmt.Errorf("artifact not found")
}

func (s *InMemoryStore) ListCollabArtifacts(_ context.Context, collabID, userID string, limit int) ([]CollabArtifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	collabID = strings.TrimSpace(collabID)
	userID = strings.TrimSpace(userID)
	if limit <= 0 {
		limit = 500
	}
	out := make([]CollabArtifact, 0)
	for _, it := range s.collabArts {
		if collabID != "" && it.CollabID != collabID {
			continue
		}
		if userID != "" && it.UserID != userID {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) AppendCollabEvent(_ context.Context, item CollabEvent) (CollabEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextCollabEID++
	item.ID = s.nextCollabEID
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	s.collabEvents = append(s.collabEvents, item)
	return item, nil
}

func (s *InMemoryStore) ListCollabEvents(_ context.Context, collabID string, limit int) ([]CollabEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	collabID = strings.TrimSpace(collabID)
	if limit <= 0 {
		limit = 500
	}
	out := make([]CollabEvent, 0)
	for _, it := range s.collabEvents {
		if collabID != "" && it.CollabID != collabID {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) AppendRequestLog(_ context.Context, item RequestLog) (RequestLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextReqLogID++
	item.ID = s.nextReqLogID
	if item.Time.IsZero() {
		item.Time = time.Now().UTC()
	}
	s.requestLogs = append(s.requestLogs, item)
	if len(s.requestLogs) > 10000 {
		s.requestLogs = s.requestLogs[len(s.requestLogs)-10000:]
	}
	return item, nil
}

func (s *InMemoryStore) ListRequestLogs(_ context.Context, filter RequestLogFilter) ([]RequestLog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := filter.Limit
	if limit <= 0 {
		limit = 300
	}
	method := strings.ToUpper(strings.TrimSpace(filter.Method))
	pathContains := strings.TrimSpace(filter.PathContains)
	userID := strings.TrimSpace(filter.UserID)
	statusCode := filter.StatusCode

	out := make([]RequestLog, 0, len(s.requestLogs))
	for _, it := range s.requestLogs {
		if filter.Since != nil && it.Time.Before(*filter.Since) {
			continue
		}
		if method != "" && it.Method != method {
			continue
		}
		if pathContains != "" && !strings.Contains(it.Path, pathContains) {
			continue
		}
		if userID != "" && it.UserID != userID {
			continue
		}
		if statusCode > 0 && it.StatusCode != statusCode {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) ListKBEntries(_ context.Context, section, keyword string, limit int) ([]KBEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	section = strings.TrimSpace(section)
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if limit <= 0 {
		limit = 200
	}
	out := make([]KBEntry, 0, len(s.kbEntries))
	for _, it := range s.kbEntries {
		if it.Deleted {
			continue
		}
		if section != "" && it.Section != section {
			continue
		}
		if kw != "" {
			if !strings.Contains(strings.ToLower(it.Section), kw) &&
				!strings.Contains(strings.ToLower(it.Title), kw) &&
				!strings.Contains(strings.ToLower(it.Content), kw) {
				continue
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) ListKBSections(_ context.Context, keyword string, limit int) ([]KBSection, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if limit <= 0 {
		limit = 200
	}
	type agg struct {
		count int64
		last  time.Time
	}
	m := make(map[string]agg)
	for _, it := range s.kbEntries {
		if it.Deleted {
			continue
		}
		sec := strings.TrimSpace(it.Section)
		if sec == "" {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(sec), kw) {
			continue
		}
		a := m[sec]
		a.count++
		if a.last.IsZero() || it.UpdatedAt.After(a.last) {
			a.last = it.UpdatedAt
		}
		m[sec] = a
	}
	out := make([]KBSection, 0, len(m))
	for sec, a := range m {
		out = append(out, KBSection{
			Section:       sec,
			EntryCount:    a.count,
			LastUpdatedAt: a.last,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastUpdatedAt.Equal(out[j].LastUpdatedAt) {
			return out[i].Section < out[j].Section
		}
		return out[i].LastUpdatedAt.After(out[j].LastUpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) GetKBEntry(_ context.Context, entryID int64) (KBEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.kbEntries[entryID]
	if !ok || it.Deleted {
		return KBEntry{}, fmt.Errorf("kb entry not found")
	}
	return it, nil
}

func (s *InMemoryStore) ListKBEntryHistory(_ context.Context, entryID int64, limit int) ([]KBEntryHistoryItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 200
	}
	out := make([]KBEntryHistoryItem, 0)
	for _, ch := range s.kbChanges {
		if ch.TargetEntryID != entryID {
			continue
		}
		p, ok := s.kbProposals[ch.ProposalID]
		if !ok {
			continue
		}
		item := KBEntryHistoryItem{
			EntryID:           entryID,
			ProposalID:        p.ID,
			ProposalTitle:     p.Title,
			ProposalStatus:    p.Status,
			ProposalReason:    p.Reason,
			ProposalCreatedAt: p.CreatedAt,
			ProposalClosedAt:  p.ClosedAt,
			ProposalAppliedAt: p.AppliedAt,
			OpType:            ch.OpType,
			DiffText:          ch.DiffText,
			OldContent:        ch.OldContent,
			NewContent:        ch.NewContent,
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ProposalCreatedAt.Equal(out[j].ProposalCreatedAt) {
			return out[i].ProposalID > out[j].ProposalID
		}
		return out[i].ProposalCreatedAt.After(out[j].ProposalCreatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) CreateKBProposal(_ context.Context, proposal KBProposal, change KBProposalChange) (KBProposal, KBProposalChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.nextKBProposalID++
	proposal.ID = s.nextKBProposalID
	proposal.CreatedAt = now
	proposal.UpdatedAt = now
	s.nextKBRevisionID++
	rev := KBRevision{
		ID:             s.nextKBRevisionID,
		ProposalID:     proposal.ID,
		RevisionNo:     1,
		BaseRevisionID: 0,
		CreatedBy:      proposal.ProposerUserID,
		OpType:         change.OpType,
		TargetEntryID:  change.TargetEntryID,
		Section:        change.Section,
		Title:          change.Title,
		OldContent:     change.OldContent,
		NewContent:     change.NewContent,
		DiffText:       change.DiffText,
		CreatedAt:      now,
	}
	proposal.CurrentRevisionID = rev.ID
	proposal.VotingRevisionID = 0
	s.kbProposals[proposal.ID] = proposal

	s.nextKBChangeID++
	change.ID = s.nextKBChangeID
	change.ProposalID = proposal.ID
	s.kbChanges[proposal.ID] = change
	s.kbRevisions = append(s.kbRevisions, rev)
	return proposal, change, nil
}

func (s *InMemoryStore) ListKBRevisions(_ context.Context, proposalID int64, limit int) ([]KBRevision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 200
	}
	out := make([]KBRevision, 0)
	for _, it := range s.kbRevisions {
		if it.ProposalID == proposalID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].RevisionNo == out[j].RevisionNo {
			return out[i].ID < out[j].ID
		}
		return out[i].RevisionNo < out[j].RevisionNo
	})
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *InMemoryStore) CreateKBRevision(_ context.Context, proposalID, baseRevisionID int64, createdBy string, change KBProposalChange, discussionDeadline time.Time) (KBRevision, KBProposal, KBProposalChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.kbProposals[proposalID]
	if !ok {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, fmt.Errorf("kb proposal not found")
	}
	if p.Status != "discussing" {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, fmt.Errorf("proposal is not in discussing phase")
	}
	if p.CurrentRevisionID != baseRevisionID {
		return KBRevision{}, KBProposal{}, KBProposalChange{}, fmt.Errorf("base revision is stale")
	}
	var maxNo int64 = 0
	for _, it := range s.kbRevisions {
		if it.ProposalID == proposalID && it.RevisionNo > maxNo {
			maxNo = it.RevisionNo
		}
	}
	now := time.Now().UTC()
	s.nextKBRevisionID++
	rev := KBRevision{
		ID:             s.nextKBRevisionID,
		ProposalID:     proposalID,
		RevisionNo:     maxNo + 1,
		BaseRevisionID: baseRevisionID,
		CreatedBy:      strings.TrimSpace(createdBy),
		OpType:         change.OpType,
		TargetEntryID:  change.TargetEntryID,
		Section:        change.Section,
		Title:          change.Title,
		OldContent:     change.OldContent,
		NewContent:     change.NewContent,
		DiffText:       change.DiffText,
		CreatedAt:      now,
	}
	s.kbRevisions = append(s.kbRevisions, rev)
	curChange := s.kbChanges[proposalID]
	curChange.OpType = change.OpType
	curChange.TargetEntryID = change.TargetEntryID
	curChange.Section = change.Section
	curChange.Title = change.Title
	curChange.OldContent = change.OldContent
	curChange.NewContent = change.NewContent
	curChange.DiffText = change.DiffText
	s.kbChanges[proposalID] = curChange
	p.CurrentRevisionID = rev.ID
	p.UpdatedAt = now
	if !discussionDeadline.IsZero() {
		dl := discussionDeadline.UTC()
		p.DiscussionDeadlineAt = &dl
	}
	s.kbProposals[proposalID] = p
	return rev, p, curChange, nil
}

func (s *InMemoryStore) AckKBProposal(_ context.Context, proposalID, revisionID int64, userID string) (KBAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return KBAck{}, fmt.Errorf("user_id is required")
	}
	for i := range s.kbAcks {
		if s.kbAcks[i].ProposalID == proposalID && s.kbAcks[i].RevisionID == revisionID && s.kbAcks[i].UserID == userID {
			return s.kbAcks[i], nil
		}
	}
	s.nextKBAckID++
	item := KBAck{
		ID:         s.nextKBAckID,
		ProposalID: proposalID,
		RevisionID: revisionID,
		UserID:     userID,
		CreatedAt:  time.Now().UTC(),
	}
	s.kbAcks = append(s.kbAcks, item)
	return item, nil
}

func (s *InMemoryStore) ListKBAcks(_ context.Context, proposalID, revisionID int64) ([]KBAck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KBAck, 0)
	for _, it := range s.kbAcks {
		if it.ProposalID == proposalID && it.RevisionID == revisionID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *InMemoryStore) GetKBProposal(_ context.Context, proposalID int64) (KBProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.kbProposals[proposalID]
	if !ok {
		return KBProposal{}, fmt.Errorf("kb proposal not found")
	}
	return it, nil
}

func (s *InMemoryStore) ListKBProposals(_ context.Context, status string, limit int) ([]KBProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status = strings.TrimSpace(strings.ToLower(status))
	if limit <= 0 {
		limit = 200
	}
	out := make([]KBProposal, 0, len(s.kbProposals))
	for _, it := range s.kbProposals {
		if status != "" && strings.ToLower(it.Status) != status {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) GetKBProposalChange(_ context.Context, proposalID int64) (KBProposalChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.kbChanges[proposalID]
	if !ok {
		return KBProposalChange{}, fmt.Errorf("kb proposal change not found")
	}
	return it, nil
}

func (s *InMemoryStore) EnrollKBProposal(_ context.Context, proposalID int64, userID string) (KBProposalEnrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return KBProposalEnrollment{}, fmt.Errorf("user_id is required")
	}
	for _, it := range s.kbEnrollments {
		if it.ProposalID == proposalID && it.UserID == userID {
			return it, nil
		}
	}
	s.nextKBEnrollID++
	item := KBProposalEnrollment{
		ID:         s.nextKBEnrollID,
		ProposalID: proposalID,
		UserID:     userID,
		CreatedAt:  time.Now().UTC(),
	}
	s.kbEnrollments = append(s.kbEnrollments, item)
	return item, nil
}

func (s *InMemoryStore) ListKBProposalEnrollments(_ context.Context, proposalID int64) ([]KBProposalEnrollment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KBProposalEnrollment, 0)
	for _, it := range s.kbEnrollments {
		if it.ProposalID == proposalID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *InMemoryStore) CreateKBThreadMessage(_ context.Context, item KBThreadMessage) (KBThreadMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextKBThreadID++
	item.ID = s.nextKBThreadID
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	s.kbThreads = append(s.kbThreads, item)
	return item, nil
}

func (s *InMemoryStore) ListKBThreadMessages(_ context.Context, proposalID int64, limit int) ([]KBThreadMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 500
	}
	out := make([]KBThreadMessage, 0)
	for _, it := range s.kbThreads {
		if it.ProposalID == proposalID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (s *InMemoryStore) StartKBProposalVoting(_ context.Context, proposalID int64, deadline time.Time) (KBProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.kbProposals[proposalID]
	if !ok {
		return KBProposal{}, fmt.Errorf("kb proposal not found")
	}
	it.Status = "voting"
	it.VotingRevisionID = it.CurrentRevisionID
	it.VotingDeadlineAt = &deadline
	it.UpdatedAt = time.Now().UTC()
	s.kbProposals[proposalID] = it
	return it, nil
}

func (s *InMemoryStore) CastKBVote(_ context.Context, vote KBVote) (KBVote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	for i := range s.kbVotes {
		if s.kbVotes[i].ProposalID == vote.ProposalID && s.kbVotes[i].UserID == vote.UserID {
			s.kbVotes[i].Vote = vote.Vote
			s.kbVotes[i].Reason = vote.Reason
			s.kbVotes[i].UpdatedAt = now
			return s.kbVotes[i], nil
		}
	}
	s.nextKBVoteID++
	vote.ID = s.nextKBVoteID
	vote.CreatedAt = now
	vote.UpdatedAt = now
	s.kbVotes = append(s.kbVotes, vote)
	return vote, nil
}

func (s *InMemoryStore) ListKBVotes(_ context.Context, proposalID int64) ([]KBVote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]KBVote, 0)
	for _, it := range s.kbVotes {
		if it.ProposalID == proposalID {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *InMemoryStore) CloseKBProposal(_ context.Context, proposalID int64, status, decisionReason string, enrolledCount, voteYes, voteNo, voteAbstain, participationCount int, closedAt time.Time) (KBProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.kbProposals[proposalID]
	if !ok {
		return KBProposal{}, fmt.Errorf("kb proposal not found")
	}
	it.Status = strings.TrimSpace(status)
	it.DecisionReason = strings.TrimSpace(decisionReason)
	it.EnrolledCount = enrolledCount
	it.VoteYes = voteYes
	it.VoteNo = voteNo
	it.VoteAbstain = voteAbstain
	it.ParticipationCount = participationCount
	it.ClosedAt = &closedAt
	it.UpdatedAt = time.Now().UTC()
	s.kbProposals[proposalID] = it
	return it, nil
}

func (s *InMemoryStore) ApplyKBProposal(_ context.Context, proposalID int64, appliedBy string, appliedAt time.Time) (KBEntry, KBProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.kbProposals[proposalID]
	if !ok {
		return KBEntry{}, KBProposal{}, fmt.Errorf("kb proposal not found")
	}
	if strings.ToLower(strings.TrimSpace(p.Status)) != "approved" {
		return KBEntry{}, KBProposal{}, fmt.Errorf("proposal is not approved")
	}
	c, ok := s.kbChanges[proposalID]
	if !ok {
		return KBEntry{}, KBProposal{}, fmt.Errorf("kb proposal change not found")
	}
	var entry KBEntry
	now := appliedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	switch strings.ToLower(strings.TrimSpace(c.OpType)) {
	case "add":
		s.nextKBEntryID++
		entry = KBEntry{
			ID:        s.nextKBEntryID,
			Section:   c.Section,
			Title:     c.Title,
			Content:   c.NewContent,
			Version:   1,
			UpdatedBy: strings.TrimSpace(appliedBy),
			UpdatedAt: now,
		}
		s.kbEntries[entry.ID] = entry
		c.TargetEntryID = entry.ID
		s.kbChanges[proposalID] = c
	case "update":
		prev, ok := s.kbEntries[c.TargetEntryID]
		if !ok || prev.Deleted {
			return KBEntry{}, KBProposal{}, fmt.Errorf("target kb entry not found")
		}
		prev.Section = c.Section
		prev.Title = c.Title
		prev.Content = c.NewContent
		prev.Version++
		prev.UpdatedBy = strings.TrimSpace(appliedBy)
		prev.UpdatedAt = now
		s.kbEntries[prev.ID] = prev
		entry = prev
	case "delete":
		prev, ok := s.kbEntries[c.TargetEntryID]
		if !ok || prev.Deleted {
			return KBEntry{}, KBProposal{}, fmt.Errorf("target kb entry not found")
		}
		prev.Deleted = true
		prev.Version++
		prev.UpdatedBy = strings.TrimSpace(appliedBy)
		prev.UpdatedAt = now
		s.kbEntries[prev.ID] = prev
		entry = prev
	default:
		return KBEntry{}, KBProposal{}, fmt.Errorf("unsupported op_type")
	}
	p.Status = "applied"
	p.AppliedAt = &now
	p.UpdatedAt = now
	s.kbProposals[p.ID] = p
	return entry, p, nil
}

func normalizeGanglionTemporality(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "eternal":
		return "eternal"
	case "durable":
		return "durable"
	case "seasonal":
		return "seasonal"
	case "ephemeral":
		return "ephemeral"
	default:
		return "durable"
	}
}

func normalizeGanglionLifeState(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "nascent":
		return "nascent"
	case "validated":
		return "validated"
	case "active":
		return "active"
	case "canonical":
		return "canonical"
	case "legacy":
		return "legacy"
	case "archived":
		return "archived"
	default:
		return "nascent"
	}
}

func (s *InMemoryStore) CreateGanglion(_ context.Context, item Ganglion) (Ganglion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.Name = strings.TrimSpace(item.Name)
	item.GanglionType = strings.TrimSpace(item.GanglionType)
	item.Description = strings.TrimSpace(item.Description)
	item.Implementation = strings.TrimSpace(item.Implementation)
	item.Validation = strings.TrimSpace(item.Validation)
	item.AuthorUserID = strings.TrimSpace(item.AuthorUserID)
	if item.Name == "" || item.GanglionType == "" || item.Description == "" || item.AuthorUserID == "" {
		return Ganglion{}, fmt.Errorf("name, type, description, author_user_id are required")
	}
	s.nextGanglionID++
	now := time.Now().UTC()
	item.ID = s.nextGanglionID
	item.Temporality = normalizeGanglionTemporality(item.Temporality)
	item.LifeState = normalizeGanglionLifeState(item.LifeState)
	item.ScoreAvgMilli = 0
	item.ScoreCount = 0
	item.IntegrationsCount = 0
	item.CreatedAt = now
	item.UpdatedAt = now
	s.ganglia[item.ID] = item
	return item, nil
}

func (s *InMemoryStore) GetGanglion(_ context.Context, ganglionID int64) (Ganglion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.ganglia[ganglionID]
	if !ok {
		return Ganglion{}, fmt.Errorf("ganglion not found")
	}
	return it, nil
}

func (s *InMemoryStore) ListGanglia(_ context.Context, ganglionType, lifeState, keyword string, limit int) ([]Ganglion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	ganglionType = strings.TrimSpace(strings.ToLower(ganglionType))
	lifeState = strings.TrimSpace(strings.ToLower(lifeState))
	keyword = strings.TrimSpace(strings.ToLower(keyword))
	out := make([]Ganglion, 0, len(s.ganglia))
	for _, it := range s.ganglia {
		if ganglionType != "" && strings.ToLower(strings.TrimSpace(it.GanglionType)) != ganglionType {
			continue
		}
		if lifeState != "" && strings.ToLower(strings.TrimSpace(it.LifeState)) != lifeState {
			continue
		}
		if keyword != "" {
			hay := strings.ToLower(it.Name + "\n" + it.Description + "\n" + it.Implementation + "\n" + it.Validation)
			if !strings.Contains(hay, keyword) {
				continue
			}
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) IntegrateGanglion(_ context.Context, ganglionID int64, userID string) (GanglionIntegration, Ganglion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return GanglionIntegration{}, Ganglion{}, fmt.Errorf("user_id is required")
	}
	g, ok := s.ganglia[ganglionID]
	if !ok {
		return GanglionIntegration{}, Ganglion{}, fmt.Errorf("ganglion not found")
	}
	now := time.Now().UTC()
	for i := range s.ganglionInts {
		if s.ganglionInts[i].GanglionID == ganglionID && s.ganglionInts[i].UserID == userID {
			s.ganglionInts[i].UpdatedAt = now
			return s.ganglionInts[i], g, nil
		}
	}
	s.nextGanglionIntID++
	integration := GanglionIntegration{
		ID:         s.nextGanglionIntID,
		GanglionID: ganglionID,
		UserID:     userID,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	s.ganglionInts = append(s.ganglionInts, integration)
	var cnt int64
	for _, it := range s.ganglionInts {
		if it.GanglionID == ganglionID {
			cnt++
		}
	}
	g.IntegrationsCount = cnt
	g.UpdatedAt = now
	s.ganglia[ganglionID] = g
	return integration, g, nil
}

func (s *InMemoryStore) ListGanglionIntegrations(_ context.Context, userID string, ganglionID int64, limit int) ([]GanglionIntegration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	userID = strings.TrimSpace(userID)
	out := make([]GanglionIntegration, 0, len(s.ganglionInts))
	for _, it := range s.ganglionInts {
		if userID != "" && it.UserID != userID {
			continue
		}
		if ganglionID > 0 && it.GanglionID != ganglionID {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) RateGanglion(_ context.Context, item GanglionRating) (GanglionRating, Ganglion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item.UserID = strings.TrimSpace(item.UserID)
	item.Feedback = strings.TrimSpace(item.Feedback)
	if item.GanglionID <= 0 || item.UserID == "" {
		return GanglionRating{}, Ganglion{}, fmt.Errorf("ganglion_id and user_id are required")
	}
	if item.Score < 1 || item.Score > 5 {
		return GanglionRating{}, Ganglion{}, fmt.Errorf("score must be between 1 and 5")
	}
	g, ok := s.ganglia[item.GanglionID]
	if !ok {
		return GanglionRating{}, Ganglion{}, fmt.Errorf("ganglion not found")
	}
	now := time.Now().UTC()
	for i := range s.ganglionRatings {
		if s.ganglionRatings[i].GanglionID == item.GanglionID && s.ganglionRatings[i].UserID == item.UserID {
			s.ganglionRatings[i].Score = item.Score
			s.ganglionRatings[i].Feedback = item.Feedback
			s.ganglionRatings[i].UpdatedAt = now
			item = s.ganglionRatings[i]
			goto RECOMPUTE
		}
	}
	s.nextGanglionRateID++
	item.ID = s.nextGanglionRateID
	item.CreatedAt = now
	item.UpdatedAt = now
	s.ganglionRatings = append(s.ganglionRatings, item)

RECOMPUTE:
	var sum int64
	var cnt int64
	for _, it := range s.ganglionRatings {
		if it.GanglionID != item.GanglionID {
			continue
		}
		sum += int64(it.Score) * 1000
		cnt++
	}
	if cnt > 0 {
		g.ScoreAvgMilli = sum / cnt
	} else {
		g.ScoreAvgMilli = 0
	}
	g.ScoreCount = cnt
	g.UpdatedAt = now
	s.ganglia[g.ID] = g
	return item, g, nil
}

func (s *InMemoryStore) ListGanglionRatings(_ context.Context, ganglionID int64, limit int) ([]GanglionRating, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	if limit > 2000 {
		limit = 2000
	}
	out := make([]GanglionRating, 0, len(s.ganglionRatings))
	for _, it := range s.ganglionRatings {
		if ganglionID > 0 && it.GanglionID != ganglionID {
			continue
		}
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *InMemoryStore) UpdateGanglionLifeState(_ context.Context, ganglionID int64, lifeState string) (Ganglion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.ganglia[ganglionID]
	if !ok {
		return Ganglion{}, fmt.Errorf("ganglion not found")
	}
	g.LifeState = normalizeGanglionLifeState(lifeState)
	g.UpdatedAt = time.Now().UTC()
	s.ganglia[g.ID] = g
	return g, nil
}
