package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"clawcolony/internal/store"
)

const (
	eventsDefaultLimit           = 100
	eventsTickScanLimit          = 2000
	eventsStepScanLimit          = 2000
	eventsLifeScanLimit          = 2000
	eventsEconomyCostScanLimit   = 500
	eventsEconomyWishScanLimit   = 100
	eventsEconomyBountyScanLimit = 100
	eventsReputationScanLimit    = 200
	eventsMonitorUserScanLimit   = 20
	eventsMonitorEventScanLimit  = 120
	eventsKBProposalScanLimit    = 50
	eventsKBChildScanLimit       = 500
	eventsCollabSessionScanLimit = 20
	eventsCollabChildScanLimit   = 500
	eventsMailPerUserScanLimit   = 100
	eventsMailContactScanLimit   = 50
	eventsMailingListScanLimit   = 100
	eventsDefaultVisibility      = "community"
	eventsDefaultCategory        = "world"
	eventsDefaultSourceWorld     = "world.tick"
	eventsTimeLayout             = time.RFC3339Nano
)

type apiEventActor struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username,omitempty"`
	Nickname    string `json:"nickname,omitempty"`
	DisplayName string `json:"display_name"`
}

type apiEventItem struct {
	EventID      string          `json:"event_id"`
	OccurredAt   string          `json:"occurred_at"`
	Kind         string          `json:"kind"`
	Category     string          `json:"category"`
	Title        string          `json:"title"`
	Summary      string          `json:"summary"`
	TitleZH      string          `json:"title_zh"`
	SummaryZH    string          `json:"summary_zh"`
	TitleEN      string          `json:"title_en"`
	SummaryEN    string          `json:"summary_en"`
	Actors       []apiEventActor `json:"actors,omitempty"`
	Targets      []apiEventActor `json:"targets,omitempty"`
	ObjectType   string          `json:"object_type,omitempty"`
	ObjectID     string          `json:"object_id,omitempty"`
	TickID       int64           `json:"tick_id,omitempty"`
	ImpactLevel  string          `json:"impact_level,omitempty"`
	SourceModule string          `json:"source_module,omitempty"`
	SourceRef    string          `json:"source_ref,omitempty"`
	Evidence     map[string]any  `json:"evidence,omitempty"`
	Visibility   string          `json:"visibility,omitempty"`
	sortTime     time.Time       `json:"-"`
	sortPriority int             `json:"-"`
}

type apiEventsQuery struct {
	UserID     string
	Kind       string
	Category   string
	TickID     int64
	ObjectType string
	ObjectID   string
	Since      *time.Time
	Until      *time.Time
	Limit      int
	Cursor     string
}

type knowledgeProposalEventSource struct {
	Proposal     store.KBProposal
	Change       store.KBProposalChange
	Revisions    []store.KBRevision
	Threads      []store.KBThreadMessage
	Votes        []store.KBVote
	Enrollments  []store.KBProposalEnrollment
	AppliedEntry *store.KBEntry
}

type collaborationEventSource struct {
	Session      store.CollabSession
	Participants []store.CollabParticipant
	Artifacts    []store.CollabArtifact
	Events       []store.CollabEvent
}

type communicationEventSource struct {
	MailItems []store.MailItem
	Contacts  []store.MailContact
	Lists     []mailingList
}

type economyEventSource struct {
	CostEvents []store.CostEvent
	Wishes     []tokenWish
	Bounties   []bountyItem
}

type identityEventSource struct {
	ReputationEvents []reputationEvent
}

type monitorActivityEventSource struct {
	Items []monitorTimelineEvent
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	q, err := parseAPIEventsQuery(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if q.Since != nil && q.Until != nil && q.Since.After(*q.Until) {
		writeError(w, http.StatusBadRequest, "since must be before until")
		return
	}
	items, partialResults, err := s.collectAPIEvents(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load events")
		return
	}
	page, nextCursor, err := apiEventsPaginate(items, q.Cursor, q.Limit)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":           page,
		"count":           len(page),
		"next_cursor":     nextCursor,
		"partial_results": partialResults,
	})
}

func parseAPIEventsQuery(r *http.Request) (apiEventsQuery, error) {
	values := r.URL.Query()
	since, err := parseRFC3339Ptr(strings.TrimSpace(values.Get("since")))
	if err != nil {
		return apiEventsQuery{}, fmt.Errorf("invalid since time, use RFC3339")
	}
	until, err := parseRFC3339Ptr(strings.TrimSpace(values.Get("until")))
	if err != nil {
		return apiEventsQuery{}, fmt.Errorf("invalid until time, use RFC3339")
	}
	return apiEventsQuery{
		UserID:     queryUserID(r),
		Kind:       strings.TrimSpace(values.Get("kind")),
		Category:   strings.TrimSpace(values.Get("category")),
		TickID:     parseInt64(values.Get("tick_id")),
		ObjectType: strings.TrimSpace(values.Get("object_type")),
		ObjectID:   strings.TrimSpace(values.Get("object_id")),
		Since:      since,
		Until:      until,
		Limit:      parseLimit(values.Get("limit"), eventsDefaultLimit),
		Cursor:     strings.TrimSpace(values.Get("cursor")),
	}, nil
}

func (s *Server) collectAPIEvents(ctx context.Context, q apiEventsQuery) ([]apiEventItem, bool, error) {
	var (
		ticks []store.WorldTickRecord
		err   error
	)
	if q.TickID > 0 {
		var tick store.WorldTickRecord
		tick, err = s.store.GetWorldTick(ctx, q.TickID)
		if err != nil {
			if !errors.Is(err, store.ErrWorldTickNotFound) {
				return nil, false, err
			}
		} else {
			ticks = append(ticks, tick)
			if q.TickID > 1 {
				prev, prevErr := s.store.GetWorldTick(ctx, q.TickID-1)
				if prevErr == nil {
					ticks = append(ticks, prev)
				} else if !errors.Is(prevErr, store.ErrWorldTickNotFound) {
					return nil, false, prevErr
				}
			}
		}
	} else {
		ticks, err = s.store.ListWorldTicks(ctx, eventsTickScanLimit)
		if err != nil {
			return nil, false, err
		}
	}
	steps, err := s.store.ListWorldTickSteps(ctx, q.TickID, eventsStepScanLimit)
	if err != nil {
		return nil, false, err
	}
	lifeTransitions, err := s.store.ListUserLifeStateTransitions(ctx, store.UserLifeStateTransitionFilter{
		UserID: q.UserID,
		TickID: q.TickID,
		Limit:  eventsLifeScanLimit,
	})
	if err != nil {
		return nil, false, err
	}
	var (
		communicationSource  communicationEventSource
		communicationPartial bool
	)
	if shouldLoadCommunicationEvents(q) {
		communicationSource, communicationPartial, err = s.collectCommunicationEventSource(ctx, q)
		if err != nil {
			return nil, false, err
		}
	}
	var (
		economySource  economyEventSource
		economyPartial bool
	)
	if shouldLoadEconomyEvents(q) {
		economySource, economyPartial, err = s.collectEconomyEventSource(ctx, q)
		if err != nil {
			return nil, false, err
		}
	}
	var discipline disciplineState
	if shouldLoadGovernanceEvents(q) {
		discipline, err = s.getDisciplineState(ctx)
		if err != nil {
			return nil, false, err
		}
	}
	var (
		identitySource  identityEventSource
		identityPartial bool
	)
	if shouldLoadIdentityEvents(q) {
		identitySource, identityPartial, err = s.collectIdentityEventSource(ctx, q)
		if err != nil {
			return nil, false, err
		}
	}
	var (
		monitorActivitySource  monitorActivityEventSource
		monitorActivityPartial bool
	)
	if shouldLoadMonitorActivityEvents(q) {
		monitorActivitySource, monitorActivityPartial, err = s.collectMonitorActivityEventSource(ctx, q)
		if err != nil {
			return nil, false, err
		}
	}
	var (
		knowledgeSources []knowledgeProposalEventSource
		knowledgePartial bool
	)
	if shouldLoadKnowledgeEvents(q) {
		knowledgeSources, knowledgePartial, err = s.collectKnowledgeEventSources(ctx, q)
		if err != nil {
			return nil, false, err
		}
	}
	var (
		collaborationSources []collaborationEventSource
		collaborationPartial bool
	)
	if shouldLoadCollaborationEvents(q) {
		collaborationSources, collaborationPartial, err = s.collectCollaborationEventSources(ctx, q)
		if err != nil {
			return nil, false, err
		}
	}
	partialResults := len(ticks) >= eventsTickScanLimit || len(steps) >= eventsStepScanLimit || len(lifeTransitions) >= eventsLifeScanLimit || communicationPartial || economyPartial || identityPartial || monitorActivityPartial || knowledgePartial || collaborationPartial
	items := make([]apiEventItem, 0, len(ticks)*3+len(steps)+len(lifeTransitions)+len(communicationSource.MailItems)+len(communicationSource.Contacts)+len(communicationSource.Lists)+len(economySource.CostEvents)+len(economySource.Wishes)*2+len(economySource.Bounties)*4+len(identitySource.ReputationEvents)+len(monitorActivitySource.Items)+len(discipline.Reports)+len(discipline.Cases)*2+len(knowledgeSources)*8+len(collaborationSources)*10)
	items = append(items, buildWorldTickDetailedEvents(ticks)...)
	items = append(items, buildWorldFreezeTransitionEvents(ticks)...)
	items = append(items, buildWorldStepDetailedEvents(steps)...)
	actorIdx := map[string]apiEventActor{}
	if len(lifeTransitions) > 0 || len(communicationSource.MailItems) > 0 || len(communicationSource.Contacts) > 0 || len(communicationSource.Lists) > 0 || len(economySource.CostEvents) > 0 || len(economySource.Wishes) > 0 || len(economySource.Bounties) > 0 || len(identitySource.ReputationEvents) > 0 || len(monitorActivitySource.Items) > 0 || len(discipline.Reports) > 0 || len(discipline.Cases) > 0 || len(knowledgeSources) > 0 || len(collaborationSources) > 0 {
		if bots, err := s.store.ListBots(ctx); err == nil {
			actorIdx = apiEventActorIndex(bots)
		} else {
			partialResults = true
		}
	}
	items = append(items, buildLifeStateDetailedEvents(lifeTransitions, actorIdx)...)
	items = append(items, buildCommunicationDetailedEvents(communicationSource, actorIdx)...)
	items = append(items, buildEconomyDetailedEvents(economySource, actorIdx)...)
	items = append(items, buildIdentityDetailedEvents(identitySource, actorIdx)...)
	items = append(items, buildMonitorActivityDetailedEvents(monitorActivitySource, actorIdx)...)
	items = append(items, buildGovernanceDetailedEvents(discipline, actorIdx)...)
	items = append(items, buildKnowledgeDetailedEvents(knowledgeSources, actorIdx)...)
	items = append(items, buildCollaborationDetailedEvents(collaborationSources, actorIdx)...)
	items = filterAPIEvents(items, q)
	sort.Slice(items, func(i, j int) bool {
		if items[i].sortTime.Equal(items[j].sortTime) {
			if items[i].sortPriority != items[j].sortPriority {
				return items[i].sortPriority > items[j].sortPriority
			}
			return items[i].EventID > items[j].EventID
		}
		return items[i].sortTime.After(items[j].sortTime)
	})
	return items, partialResults, nil
}

func shouldLoadGovernanceEvents(q apiEventsQuery) bool {
	if q.TickID > 0 {
		return false
	}
	category := strings.TrimSpace(q.Category)
	if category == "" || category == "governance" {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(q.Kind), "governance.") {
		return true
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "governance_report", "governance_case":
		return true
	default:
		return false
	}
}

func shouldLoadEconomyEvents(q apiEventsQuery) bool {
	if q.TickID > 0 {
		return false
	}
	category := strings.TrimSpace(q.Category)
	if category == "" || category == "economy" {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(q.Kind), "economy.") {
		return true
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "cost_event", "token_wish", "bounty":
		return true
	default:
		return false
	}
}

func shouldLoadEconomyCostEvents(q apiEventsQuery) bool {
	if !shouldLoadEconomyEvents(q) {
		return false
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "":
	case "cost_event":
		return true
	default:
		return false
	}
	kind := strings.TrimSpace(q.Kind)
	return kind == "" || strings.HasPrefix(kind, "economy.token.")
}

func shouldLoadEconomyWishes(q apiEventsQuery) bool {
	if !shouldLoadEconomyEvents(q) {
		return false
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "":
	case "token_wish":
		return true
	default:
		return false
	}
	kind := strings.TrimSpace(q.Kind)
	return kind == "" || strings.HasPrefix(kind, "economy.token.wish.")
}

func shouldLoadEconomyBounties(q apiEventsQuery) bool {
	if !shouldLoadEconomyEvents(q) {
		return false
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "":
	case "bounty":
		return true
	default:
		return false
	}
	kind := strings.TrimSpace(q.Kind)
	return kind == "" || strings.HasPrefix(kind, "economy.bounty.")
}

func shouldLoadIdentityEvents(q apiEventsQuery) bool {
	if q.TickID > 0 {
		return false
	}
	category := strings.TrimSpace(q.Category)
	if category == "" || category == "identity" {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(q.Kind), "identity.") {
		return true
	}
	return strings.TrimSpace(q.ObjectType) == "reputation_event"
}

func shouldLoadMonitorActivityEvents(q apiEventsQuery) bool {
	if q.TickID > 0 {
		return false
	}
	category := strings.TrimSpace(q.Category)
	if category == "tooling" {
		return true
	}
	kind := strings.TrimSpace(q.Kind)
	if strings.HasPrefix(kind, "tooling.") {
		return true
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "request_log":
		return category == "" || category == "tooling"
	case "cost_event":
		return strings.HasPrefix(kind, "tooling.") || category == "tooling"
	}
	return category == "" && strings.TrimSpace(q.UserID) != ""
}

func shouldLoadCommunicationEvents(q apiEventsQuery) bool {
	if q.TickID > 0 {
		return false
	}
	category := strings.TrimSpace(q.Category)
	if category == "" || category == "communication" {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(q.Kind), "communication.") {
		return true
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "mail_message", "mailbox_item", "mail_contact", "mailing_list", "mail_reminder":
		return true
	default:
		return false
	}
}

func shouldLoadCommunicationMailboxes(q apiEventsQuery) bool {
	if !shouldLoadCommunicationEvents(q) {
		return false
	}
	if strings.TrimSpace(q.UserID) == "" {
		return false
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "":
	case "mail_message", "mailbox_item", "mail_reminder":
		return true
	default:
		return false
	}
	kind := strings.TrimSpace(q.Kind)
	return kind == "" || strings.HasPrefix(kind, "communication.mail.") || strings.HasPrefix(kind, "communication.broadcast.") || strings.HasPrefix(kind, "communication.reminder.")
}

func shouldLoadCommunicationContacts(q apiEventsQuery) bool {
	if !shouldLoadCommunicationEvents(q) {
		return false
	}
	if strings.TrimSpace(q.UserID) == "" {
		return false
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "":
	case "mail_contact":
		return true
	default:
		return false
	}
	kind := strings.TrimSpace(q.Kind)
	return kind == "" || strings.HasPrefix(kind, "communication.contact.")
}

func shouldLoadCommunicationLists(q apiEventsQuery) bool {
	if !shouldLoadCommunicationEvents(q) {
		return false
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "":
	case "mailing_list":
		return true
	default:
		return false
	}
	kind := strings.TrimSpace(q.Kind)
	return kind == "" || strings.HasPrefix(kind, "communication.list.")
}

func shouldLoadKnowledgeEvents(q apiEventsQuery) bool {
	if q.TickID > 0 {
		return false
	}
	category := strings.TrimSpace(q.Category)
	if category == "" || category == "knowledge" {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(q.Kind), "knowledge.") {
		return true
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "kb_proposal", "kb_revision", "kb_thread_message", "kb_vote", "kb_entry":
		return true
	default:
		return false
	}
}

func shouldLoadCollaborationEvents(q apiEventsQuery) bool {
	if q.TickID > 0 {
		return false
	}
	category := strings.TrimSpace(q.Category)
	if category == "" || category == "collaboration" {
		return true
	}
	if strings.HasPrefix(strings.TrimSpace(q.Kind), "collaboration.") {
		return true
	}
	switch strings.TrimSpace(q.ObjectType) {
	case "collab_session", "collab_participant", "collab_artifact", "collab_event":
		return true
	default:
		return false
	}
}

func (s *Server) collectKnowledgeEventSources(ctx context.Context, q apiEventsQuery) ([]knowledgeProposalEventSource, bool, error) {
	scanLimit := kbProposalScanLimitForQuery(q)
	proposals, err := s.store.ListKBProposals(ctx, "", scanLimit)
	if err != nil {
		return nil, false, err
	}
	out := make([]knowledgeProposalEventSource, 0, len(proposals))
	partialResults := len(proposals) >= scanLimit
	for _, proposal := range proposals {
		change, err := s.store.GetKBProposalChange(ctx, proposal.ID)
		if err != nil {
			log.Printf("events_api_skip_kb_proposal proposal_id=%d err=%v", proposal.ID, err)
			partialResults = true
			continue
		}
		revisions, err := s.store.ListKBRevisions(ctx, proposal.ID, eventsKBChildScanLimit)
		if err != nil {
			log.Printf("events_api_skip_kb_revisions proposal_id=%d err=%v", proposal.ID, err)
			partialResults = true
			continue
		}
		threads, err := s.store.ListKBThreadMessages(ctx, proposal.ID, eventsKBChildScanLimit)
		if err != nil {
			log.Printf("events_api_skip_kb_threads proposal_id=%d err=%v", proposal.ID, err)
			partialResults = true
			continue
		}
		votes, err := s.store.ListKBVotes(ctx, proposal.ID)
		if err != nil {
			log.Printf("events_api_skip_kb_votes proposal_id=%d err=%v", proposal.ID, err)
			partialResults = true
			continue
		}
		enrollments, err := s.store.ListKBProposalEnrollments(ctx, proposal.ID)
		if err != nil {
			log.Printf("events_api_skip_kb_enrollments proposal_id=%d err=%v", proposal.ID, err)
			partialResults = true
			continue
		}
		if len(revisions) >= eventsKBChildScanLimit || len(threads) >= eventsKBChildScanLimit || len(votes) >= eventsKBChildScanLimit || len(enrollments) >= eventsKBChildScanLimit {
			partialResults = true
		}
		var appliedEntry *store.KBEntry
		if proposal.AppliedAt != nil && change.TargetEntryID > 0 && strings.TrimSpace(strings.ToLower(change.OpType)) != "delete" {
			if entry, err := s.store.GetKBEntry(ctx, change.TargetEntryID); err == nil {
				appliedEntry = &entry
			}
		}
		out = append(out, knowledgeProposalEventSource{
			Proposal:     proposal,
			Change:       change,
			Revisions:    revisions,
			Threads:      threads,
			Votes:        votes,
			Enrollments:  enrollments,
			AppliedEntry: appliedEntry,
		})
	}
	return out, partialResults, nil
}

func kbProposalScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 20:
		limit = 20
	case limit > eventsKBProposalScanLimit:
		limit = eventsKBProposalScanLimit
	}
	return limit
}

func (s *Server) collectCollaborationEventSources(ctx context.Context, q apiEventsQuery) ([]collaborationEventSource, bool, error) {
	loadSession := func(session store.CollabSession) (collaborationEventSource, bool, error) {
		participants, err := s.store.ListCollabParticipants(ctx, session.CollabID, "", eventsCollabChildScanLimit)
		if err != nil {
			return collaborationEventSource{}, false, err
		}
		artifacts, err := s.store.ListCollabArtifacts(ctx, session.CollabID, "", eventsCollabChildScanLimit)
		if err != nil {
			return collaborationEventSource{}, false, err
		}
		events, err := s.store.ListCollabEvents(ctx, session.CollabID, eventsCollabChildScanLimit)
		if err != nil {
			return collaborationEventSource{}, false, err
		}
		partial := len(participants) >= eventsCollabChildScanLimit || len(artifacts) >= eventsCollabChildScanLimit || len(events) >= eventsCollabChildScanLimit
		return collaborationEventSource{
			Session:      session,
			Participants: participants,
			Artifacts:    artifacts,
			Events:       events,
		}, partial, nil
	}

	if strings.TrimSpace(q.ObjectType) == "collab_session" && strings.TrimSpace(q.ObjectID) != "" {
		session, err := s.store.GetCollabSession(ctx, strings.TrimSpace(q.ObjectID))
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				return nil, false, nil
			}
			return nil, false, err
		}
		source, partial, err := loadSession(session)
		if err != nil {
			return nil, false, err
		}
		return []collaborationEventSource{source}, partial, nil
	}

	scanLimit := collabSessionScanLimitForQuery(q)
	sessions, err := s.store.ListCollabSessions(ctx, "", "", "", scanLimit)
	if err != nil {
		return nil, false, err
	}
	out := make([]collaborationEventSource, 0, len(sessions))
	partialResults := len(sessions) >= scanLimit
	for _, session := range sessions {
		source, partial, err := loadSession(session)
		if err != nil {
			log.Printf("events_api_skip_collab collab_id=%s err=%v", session.CollabID, err)
			partialResults = true
			continue
		}
		if partial {
			partialResults = true
		}
		out = append(out, source)
	}
	return out, partialResults, nil
}

func collabSessionScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 10:
		limit = 10
	case limit > eventsCollabSessionScanLimit:
		limit = eventsCollabSessionScanLimit
	}
	return limit
}

func (s *Server) collectCommunicationEventSource(ctx context.Context, q apiEventsQuery) (communicationEventSource, bool, error) {
	source := communicationEventSource{
		MailItems: []store.MailItem{},
		Contacts:  []store.MailContact{},
		Lists:     []mailingList{},
	}
	partialResults := false
	loadMailboxes := shouldLoadCommunicationMailboxes(q)
	loadContacts := shouldLoadCommunicationContacts(q)
	loadLists := shouldLoadCommunicationLists(q)
	allUserIDs := []string{}
	if loadMailboxes || loadContacts {
		var err error
		allUserIDs, err = s.communicationAllEventUserIDs(ctx)
		if err != nil {
			return communicationEventSource{}, false, err
		}
	}

	if loadMailboxes {
		scanLimit := communicationMailboxScanLimitForQuery(q)
		inboxUsers := allUserIDs
		if strings.TrimSpace(q.UserID) != "" {
			inboxUsers = []string{strings.TrimSpace(q.UserID)}
		}
		outboxUsers := allUserIDs
		for _, userID := range inboxUsers {
			items, err := s.store.ListMailbox(ctx, userID, "inbox", "", "", q.Since, q.Until, scanLimit)
			if err != nil {
				return communicationEventSource{}, false, err
			}
			if len(items) >= scanLimit {
				partialResults = true
			}
			source.MailItems = append(source.MailItems, items...)
		}
		if strings.TrimSpace(q.UserID) != "" {
			outboxUsers = communicationOutboxUsersForUser(source.MailItems, strings.TrimSpace(q.UserID))
		}
		for _, userID := range outboxUsers {
			items, err := s.store.ListMailbox(ctx, userID, "outbox", "", "", q.Since, q.Until, scanLimit)
			if err != nil {
				return communicationEventSource{}, false, err
			}
			if strings.TrimSpace(q.UserID) != "" && strings.TrimSpace(userID) != strings.TrimSpace(q.UserID) {
				items = communicationFilterOutboxItemsForTarget(items, strings.TrimSpace(q.UserID))
			}
			if len(items) >= scanLimit {
				partialResults = true
			}
			source.MailItems = append(source.MailItems, items...)
		}
	}

	if loadContacts {
		scanLimit := communicationContactScanLimitForQuery(q)
		for _, userID := range []string{strings.TrimSpace(q.UserID)} {
			items, err := s.store.ListMailContactsUpdated(ctx, userID, "", q.Since, q.Until, scanLimit)
			if err != nil {
				return communicationEventSource{}, false, err
			}
			if len(items) >= scanLimit {
				partialResults = true
			}
			source.Contacts = append(source.Contacts, items...)
		}
	}

	if loadLists {
		state, err := s.getMailingListState(ctx)
		if err != nil {
			return communicationEventSource{}, false, err
		}
		items := make([]mailingList, 0, len(state.Lists))
		for _, it := range state.Lists {
			if q.UserID != "" && !mailingListIncludesUser(it, q.UserID) && strings.TrimSpace(it.OwnerUserID) != strings.TrimSpace(q.UserID) {
				continue
			}
			if strings.TrimSpace(q.ObjectType) == "mailing_list" && strings.TrimSpace(q.ObjectID) != "" && strings.TrimSpace(it.ListID) != strings.TrimSpace(q.ObjectID) {
				continue
			}
			items = append(items, it)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
				return items[i].ListID > items[j].ListID
			}
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		})
		scanLimit := communicationMailingListScanLimitForQuery(q)
		if len(items) > scanLimit {
			partialResults = true
			items = items[:scanLimit]
		}
		source.Lists = items
	}
	return source, partialResults, nil
}

func (s *Server) collectEconomyEventSource(ctx context.Context, q apiEventsQuery) (economyEventSource, bool, error) {
	source := economyEventSource{
		CostEvents: []store.CostEvent{},
		Wishes:     []tokenWish{},
		Bounties:   []bountyItem{},
	}
	partialResults := false

	if shouldLoadEconomyCostEvents(q) {
		scanLimit := economyCostScanLimitForQuery(q)
		listFn := s.store.ListCostEvents
		if q.UserID != "" {
			listFn = s.store.ListCostEventsByInvolvement
		}
		items, err := listFn(ctx, strings.TrimSpace(q.UserID), scanLimit)
		if err != nil {
			return economyEventSource{}, false, err
		}
		if len(items) >= scanLimit {
			partialResults = true
		}
		filtered := make([]store.CostEvent, 0, len(items))
		for _, item := range items {
			if strings.TrimSpace(q.ObjectType) == "cost_event" && strings.TrimSpace(q.ObjectID) != "" && strconv.FormatInt(item.ID, 10) != strings.TrimSpace(q.ObjectID) {
				continue
			}
			if q.UserID != "" {
				meta := parseEconomyCostMeta(item.MetaJSON)
				if strings.TrimSpace(item.UserID) != strings.TrimSpace(q.UserID) && strings.TrimSpace(meta.ToUserID) != strings.TrimSpace(q.UserID) {
					continue
				}
			}
			filtered = append(filtered, item)
		}
		source.CostEvents = filtered
	}

	if shouldLoadEconomyWishes(q) {
		state, err := s.getTokenWishState(ctx)
		if err != nil {
			return economyEventSource{}, false, err
		}
		items := make([]tokenWish, 0, len(state.Items))
		for _, it := range state.Items {
			if strings.TrimSpace(q.ObjectType) == "token_wish" && strings.TrimSpace(q.ObjectID) != "" && strings.TrimSpace(it.WishID) != strings.TrimSpace(q.ObjectID) {
				continue
			}
			if q.UserID != "" && strings.TrimSpace(it.UserID) != strings.TrimSpace(q.UserID) && strings.TrimSpace(it.FulfilledBy) != strings.TrimSpace(q.UserID) {
				continue
			}
			items = append(items, it)
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		})
		scanLimit := economyWishScanLimitForQuery(q)
		if len(items) > scanLimit {
			partialResults = true
			items = items[:scanLimit]
		}
		source.Wishes = items
	}

	if shouldLoadEconomyBounties(q) {
		state, err := s.getBountyState(ctx)
		if err != nil {
			return economyEventSource{}, false, err
		}
		items := make([]bountyItem, 0, len(state.Items))
		for _, it := range state.Items {
			if strings.TrimSpace(q.ObjectType) == "bounty" && strings.TrimSpace(q.ObjectID) != "" && strconv.FormatInt(it.BountyID, 10) != strings.TrimSpace(q.ObjectID) {
				continue
			}
			if q.UserID != "" &&
				strings.TrimSpace(it.PosterUserID) != strings.TrimSpace(q.UserID) &&
				strings.TrimSpace(it.ClaimedBy) != strings.TrimSpace(q.UserID) &&
				strings.TrimSpace(it.ReleasedTo) != strings.TrimSpace(q.UserID) &&
				strings.TrimSpace(it.ReleasedBy) != strings.TrimSpace(q.UserID) {
				continue
			}
			items = append(items, it)
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		})
		scanLimit := economyBountyScanLimitForQuery(q)
		if len(items) > scanLimit {
			partialResults = true
			items = items[:scanLimit]
		}
		source.Bounties = items
	}

	return source, partialResults, nil
}

func (s *Server) collectIdentityEventSource(ctx context.Context, q apiEventsQuery) (identityEventSource, bool, error) {
	source := identityEventSource{ReputationEvents: []reputationEvent{}}
	state, err := s.getReputationState(ctx)
	if err != nil {
		return identityEventSource{}, false, err
	}
	items := make([]reputationEvent, 0, len(state.Events))
	for _, it := range state.Events {
		if strings.TrimSpace(q.ObjectType) == "reputation_event" && strings.TrimSpace(q.ObjectID) != "" && strconv.FormatInt(it.EventID, 10) != strings.TrimSpace(q.ObjectID) {
			continue
		}
		if q.UserID != "" && strings.TrimSpace(it.UserID) != strings.TrimSpace(q.UserID) && strings.TrimSpace(it.ActorUserID) != strings.TrimSpace(q.UserID) {
			continue
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].EventID > items[j].EventID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	partialResults := false
	scanLimit := identityReputationScanLimitForQuery(q)
	if len(items) > scanLimit {
		partialResults = true
		items = items[:scanLimit]
	}
	source.ReputationEvents = items
	return source, partialResults, nil
}

func (s *Server) collectMonitorActivityEventSource(ctx context.Context, q apiEventsQuery) (monitorActivityEventSource, bool, error) {
	source := monitorActivityEventSource{Items: []monitorTimelineEvent{}}
	since := time.Time{}
	if q.Since != nil {
		since = q.Since.UTC()
	}
	perUserLimit := monitorActivityEventScanLimitForQuery(q)
	userID := strings.TrimSpace(q.UserID)
	if userID != "" {
		items, err := s.collectMonitorEvents(ctx, userID, perUserLimit, since)
		if err != nil {
			return monitorActivityEventSource{}, false, err
		}
		source.Items = filterMonitorHighValueEvents(items)
		return source, len(items) >= perUserLimit, nil
	}

	userLimit := monitorActivityUserScanLimitForQuery(q)
	bots, err := s.monitorTargetBots(ctx, "", true, userLimit)
	if err != nil {
		return monitorActivityEventSource{}, false, err
	}
	partialResults := len(bots) >= userLimit
	items := make([]monitorTimelineEvent, 0, len(bots)*4)
	for _, bot := range bots {
		timelineItems, collectErr := s.collectMonitorEvents(ctx, strings.TrimSpace(bot.BotID), perUserLimit, since)
		if collectErr != nil {
			partialResults = true
			continue
		}
		if len(timelineItems) >= perUserLimit {
			partialResults = true
		}
		items = append(items, filterMonitorHighValueEvents(timelineItems)...)
	}
	source.Items = items
	return source, partialResults, nil
}

func filterMonitorHighValueEvents(items []monitorTimelineEvent) []monitorTimelineEvent {
	out := make([]monitorTimelineEvent, 0, len(items))
	for _, item := range items {
		if isMonitorHighValueEvent(item) {
			out = append(out, item)
		}
	}
	return out
}

func isMonitorHighValueEvent(item monitorTimelineEvent) bool {
	if strings.TrimSpace(item.Category) != "tool" {
		return false
	}
	if item.Source == "request_logs" {
		return strings.TrimSpace(strings.ToLower(item.Status)) == "failed"
	}
	return item.Source == "cost_events"
}

func monitorActivityEventScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	if limit < 20 {
		limit = 20
	}
	if limit > eventsMonitorEventScanLimit {
		limit = eventsMonitorEventScanLimit
	}
	return limit
}

func monitorActivityUserScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	if limit < 5 {
		limit = 5
	}
	if limit > eventsMonitorUserScanLimit {
		limit = eventsMonitorUserScanLimit
	}
	return limit
}

func (s *Server) communicationAllEventUserIDs(ctx context.Context) ([]string, error) {
	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(bots)+1)
	seen := map[string]struct{}{}
	add := func(userID string) {
		uid := strings.TrimSpace(userID)
		if uid == "" {
			return
		}
		if _, ok := seen[uid]; ok {
			return
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	add(clawWorldSystemID)
	for _, bot := range bots {
		add(bot.BotID)
	}
	sort.Strings(out)
	return out, nil
}

func communicationOutboxUsersForUser(items []store.MailItem, userID string) []string {
	seen := map[string]struct{}{}
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			return
		}
		if _, ok := seen[candidate]; ok {
			return
		}
		seen[candidate] = struct{}{}
	}
	add(userID)
	for _, item := range items {
		if strings.TrimSpace(strings.ToLower(item.Folder)) != "inbox" {
			continue
		}
		if strings.TrimSpace(item.OwnerAddress) != strings.TrimSpace(userID) {
			continue
		}
		add(item.FromAddress)
	}
	out := make([]string, 0, len(seen))
	for userID := range seen {
		out = append(out, userID)
	}
	sort.Strings(out)
	return out
}

func communicationFilterOutboxItemsForTarget(items []store.MailItem, targetUserID string) []store.MailItem {
	target := strings.TrimSpace(targetUserID)
	if target == "" {
		return items
	}
	out := make([]store.MailItem, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ToAddress) != target {
			continue
		}
		out = append(out, item)
	}
	return out
}

func communicationMailboxScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 20:
		limit = 20
	case limit > eventsMailPerUserScanLimit:
		limit = eventsMailPerUserScanLimit
	}
	return limit
}

func communicationContactScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 10:
		limit = 10
	case limit > eventsMailContactScanLimit:
		limit = eventsMailContactScanLimit
	}
	return limit
}

func communicationMailingListScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 10:
		limit = 10
	case limit > eventsMailingListScanLimit:
		limit = eventsMailingListScanLimit
	}
	return limit
}

func economyCostScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 50:
		limit = 50
	case limit > eventsEconomyCostScanLimit:
		limit = eventsEconomyCostScanLimit
	}
	return limit
}

func economyWishScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 20:
		limit = 20
	case limit > eventsEconomyWishScanLimit:
		limit = eventsEconomyWishScanLimit
	}
	return limit
}

func economyBountyScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 20:
		limit = 20
	case limit > eventsEconomyBountyScanLimit:
		limit = eventsEconomyBountyScanLimit
	}
	return limit
}

func identityReputationScanLimitForQuery(q apiEventsQuery) int {
	limit := q.Limit
	if limit <= 0 {
		limit = eventsDefaultLimit
	}
	switch {
	case limit < 20:
		limit = 20
	case limit > eventsReputationScanLimit:
		limit = eventsReputationScanLimit
	}
	return limit
}

func mailingListIncludesUser(item mailingList, userID string) bool {
	target := strings.TrimSpace(userID)
	if target == "" {
		return false
	}
	for _, member := range item.Members {
		if strings.TrimSpace(member) == target {
			return true
		}
	}
	return false
}

func apiEventActorIndex(bots []store.Bot) map[string]apiEventActor {
	out := make(map[string]apiEventActor, len(bots)+1)
	for _, it := range bots {
		uid := strings.TrimSpace(it.BotID)
		if uid == "" {
			continue
		}
		username := strings.TrimSpace(it.Name)
		nickname := strings.TrimSpace(it.Nickname)
		out[uid] = apiEventActor{
			UserID:      uid,
			Username:    username,
			Nickname:    nickname,
			DisplayName: chronicleDisplayName(nickname, username, uid),
		}
	}
	out[clawWorldSystemID] = apiEventActor{
		UserID:      clawWorldSystemID,
		Username:    "Clawcolony",
		DisplayName: "Clawcolony",
	}
	return out
}

func apiEventActorForUser(userID string, idx map[string]apiEventActor) apiEventActor {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return apiEventActor{}
	}
	if it, ok := idx[uid]; ok {
		return it
	}
	return apiEventActor{
		UserID:      uid,
		DisplayName: uid,
	}
}

func apiEventActorsForUsers(idx map[string]apiEventActor, userIDs ...string) []apiEventActor {
	out := make([]apiEventActor, 0, len(userIDs))
	seen := make(map[string]struct{}, len(userIDs))
	for _, userID := range userIDs {
		actor := apiEventActorForUser(userID, idx)
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

func buildWorldTickDetailedEvents(items []store.WorldTickRecord) []apiEventItem {
	out := make([]apiEventItem, 0, len(items)*2)
	for _, it := range items {
		out = append(out, buildWorldTickStartedEvent(it))
		out = append(out, buildWorldTickFinalEvent(it))
	}
	return out
}

func buildWorldTickStartedEvent(it store.WorldTickRecord) apiEventItem {
	triggerZH := chronicleTriggerLabelZH(it.TriggerType)
	triggerEN := chronicleTriggerLabelEN(it.TriggerType)
	titleZH := fmt.Sprintf("第 %d 次世界周期开始", it.TickID)
	summaryZH := fmt.Sprintf("第 %d 次世界周期已开始，本次由%s触发。系统正在推进这一轮世界流程。", it.TickID, triggerZH)
	titleEN := fmt.Sprintf("World tick %d started", it.TickID)
	summaryEN := fmt.Sprintf("World tick %d started, triggered by %s. The system is now progressing through this cycle.", it.TickID, triggerEN)
	return apiEventItem{
		EventID:      fmt.Sprintf("world_tick_started:%020d", it.TickID),
		OccurredAt:   it.StartedAt.UTC().Format(eventsTimeLayout),
		Kind:         "world.tick.started",
		Category:     eventsDefaultCategory,
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		ObjectType:   "world_tick",
		ObjectID:     strconv.FormatInt(it.TickID, 10),
		TickID:       it.TickID,
		ImpactLevel:  "info",
		SourceModule: eventsDefaultSourceWorld,
		SourceRef:    fmt.Sprintf("world_tick:%d#start", it.TickID),
		Evidence: map[string]any{
			"trigger_type": it.TriggerType,
			"duration_ms":  it.DurationMS,
			"status":       strings.TrimSpace(it.Status),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     it.StartedAt.UTC(),
		sortPriority: 0,
	}
}

func buildWorldTickFinalEvent(it store.WorldTickRecord) apiEventItem {
	endAt := worldTickEndTime(it)
	triggerZH := chronicleTriggerLabelZH(it.TriggerType)
	triggerEN := chronicleTriggerLabelEN(it.TriggerType)
	status := strings.TrimSpace(strings.ToLower(it.Status))
	kind := "world.tick.completed"
	impact := "info"
	titleZH := fmt.Sprintf("第 %d 次世界周期已完成", it.TickID)
	summaryZH := fmt.Sprintf("第 %d 次世界周期已完成，耗时 %d 毫秒，本次由%s触发。", it.TickID, it.DurationMS, triggerZH)
	titleEN := fmt.Sprintf("World tick %d completed", it.TickID)
	summaryEN := fmt.Sprintf("World tick %d completed in %d ms, triggered by %s.", it.TickID, it.DurationMS, triggerEN)
	if it.ReplayOfTickID > 0 || strings.EqualFold(strings.TrimSpace(it.TriggerType), "replay") {
		kind = "world.tick.replayed"
		impact = "notice"
		titleZH = fmt.Sprintf("第 %d 次世界周期回放完成", it.TickID)
		summaryZH = fmt.Sprintf("第 %d 次世界周期完成了历史回放，对应源周期为 %d。", it.TickID, it.ReplayOfTickID)
		titleEN = fmt.Sprintf("World tick %d replay completed", it.TickID)
		summaryEN = fmt.Sprintf("World tick %d completed a replay of source tick %d.", it.TickID, it.ReplayOfTickID)
	} else if status == "frozen" {
		kind = "world.tick.skipped_frozen"
		impact = "warning"
		titleZH = fmt.Sprintf("第 %d 次世界周期因冻结而跳过", it.TickID)
		summaryZH = fmt.Sprintf("第 %d 次世界周期检测到世界处于冻结状态，主要执行流程被跳过。", it.TickID)
		titleEN = fmt.Sprintf("World tick %d was skipped because the world was frozen", it.TickID)
		summaryEN = fmt.Sprintf("World tick %d found the world frozen, so the main execution flow was skipped.", it.TickID)
	} else if status == "degraded" {
		kind = "world.tick.degraded"
		impact = "warning"
		titleZH = fmt.Sprintf("第 %d 次世界周期以降级状态完成", it.TickID)
		summaryZH = fmt.Sprintf("第 %d 次世界周期已完成，但过程中至少有一个阶段失败。", it.TickID)
		titleEN = fmt.Sprintf("World tick %d completed in a degraded state", it.TickID)
		summaryEN = fmt.Sprintf("World tick %d finished, but at least one stage failed during execution.", it.TickID)
	}
	if errText := strings.TrimSpace(it.ErrorText); errText != "" {
		summaryZH += " 原因：" + errText + "。"
		summaryEN += " Reason: " + errText + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("world_tick_final:%020d", it.TickID),
		OccurredAt:   endAt.Format(eventsTimeLayout),
		Kind:         kind,
		Category:     eventsDefaultCategory,
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		ObjectType:   "world_tick",
		ObjectID:     strconv.FormatInt(it.TickID, 10),
		TickID:       it.TickID,
		ImpactLevel:  impact,
		SourceModule: eventsDefaultSourceWorld,
		SourceRef:    fmt.Sprintf("world_tick:%d#final", it.TickID),
		Evidence: map[string]any{
			"trigger_type":      it.TriggerType,
			"status":            status,
			"duration_ms":       it.DurationMS,
			"replay_of_tick_id": it.ReplayOfTickID,
			"prev_hash":         strings.TrimSpace(it.PrevHash),
			"entry_hash":        strings.TrimSpace(it.EntryHash),
			"error":             strings.TrimSpace(it.ErrorText),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     endAt,
		sortPriority: 2,
	}
}

func buildWorldFreezeTransitionEvents(items []store.WorldTickRecord) []apiEventItem {
	if len(items) < 2 {
		return nil
	}
	ordered := make([]store.WorldTickRecord, len(items))
	copy(ordered, items)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].StartedAt.Equal(ordered[j].StartedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].StartedAt.Before(ordered[j].StartedAt)
	})
	out := make([]apiEventItem, 0, len(ordered))
	for i := 1; i < len(ordered); i++ {
		prev := ordered[i-1]
		curr := ordered[i]
		prevFrozen := strings.EqualFold(strings.TrimSpace(prev.Status), "frozen")
		currFrozen := strings.EqualFold(strings.TrimSpace(curr.Status), "frozen")
		if prevFrozen == currFrozen {
			continue
		}
		endAt := worldTickEndTime(curr)
		item := apiEventItem{
			EventID:      fmt.Sprintf("world_freeze_transition:%020d", curr.TickID),
			OccurredAt:   endAt.Format(eventsTimeLayout),
			Category:     eventsDefaultCategory,
			Title:        "",
			Summary:      "",
			TitleZH:      "",
			SummaryZH:    "",
			TitleEN:      "",
			SummaryEN:    "",
			ObjectType:   "world_freeze",
			ObjectID:     strconv.FormatInt(curr.TickID, 10),
			TickID:       curr.TickID,
			ImpactLevel:  "warning",
			SourceModule: "world.freeze",
			SourceRef:    fmt.Sprintf("world_tick:%d#freeze", curr.TickID),
			Evidence: map[string]any{
				"previous_tick_id": prev.TickID,
				"previous_status":  strings.TrimSpace(prev.Status),
				"current_status":   strings.TrimSpace(curr.Status),
				"trigger_type":     curr.TriggerType,
				"error":            strings.TrimSpace(curr.ErrorText),
			},
			Visibility:   eventsDefaultVisibility,
			sortTime:     endAt,
			sortPriority: 3,
		}
		if currFrozen {
			item.Kind = "world.freeze.entered"
			item.TitleZH = "世界进入冻结状态"
			item.SummaryZH = fmt.Sprintf("从第 %d 次世界周期到第 %d 次世界周期之间，世界状态切换为冻结。后续周期会跳过大部分执行阶段。", prev.TickID, curr.TickID)
			item.TitleEN = "The world entered a frozen state"
			item.SummaryEN = fmt.Sprintf("Between world tick %d and tick %d, the world switched into a frozen state. Later cycles will skip most execution stages.", prev.TickID, curr.TickID)
		} else {
			item.Kind = "world.freeze.lifted"
			item.ImpactLevel = "notice"
			item.TitleZH = "世界恢复运行"
			item.SummaryZH = fmt.Sprintf("从第 %d 次世界周期到第 %d 次世界周期之间，世界离开了冻结状态，主要执行流程恢复。", prev.TickID, curr.TickID)
			item.TitleEN = "The world resumed operation"
			item.SummaryEN = fmt.Sprintf("Between world tick %d and tick %d, the world left the frozen state and the main execution flow resumed.", prev.TickID, curr.TickID)
		}
		item.Title = item.TitleZH
		item.Summary = item.SummaryZH
		out = append(out, item)
	}
	return out
}

func buildWorldStepDetailedEvents(items []store.WorldTickStepRecord) []apiEventItem {
	out := make([]apiEventItem, 0, len(items))
	for _, it := range items {
		out = append(out, buildWorldStepEvent(it))
	}
	return out
}

func buildLifeStateDetailedEvents(items []store.UserLifeStateTransition, actors map[string]apiEventActor) []apiEventItem {
	out := make([]apiEventItem, 0, len(items))
	for _, it := range items {
		if event, ok := buildLifeStateDetailedEvent(it, actors); ok {
			out = append(out, event)
		}
	}
	return out
}

func buildLifeStateDetailedEvent(it store.UserLifeStateTransition, actors map[string]apiEventActor) (apiEventItem, bool) {
	target := apiEventActorForUser(it.UserID, actors)
	actor := apiEventActorForUser(it.ActorUserID, actors)
	occurredAt := it.CreatedAt.UTC()
	item := apiEventItem{
		EventID:      fmt.Sprintf("life_state_transition:%020d", it.ID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Category:     "life",
		ObjectType:   "life_state_transition",
		ObjectID:     strconv.FormatInt(it.ID, 10),
		TickID:       it.TickID,
		ImpactLevel:  "notice",
		SourceModule: nonEmptyOr(strings.TrimSpace(it.SourceModule), "life.state"),
		SourceRef:    strings.TrimSpace(it.SourceRef),
		Evidence: map[string]any{
			"from_state":  strings.TrimSpace(it.FromState),
			"to_state":    strings.TrimSpace(it.ToState),
			"from_reason": strings.TrimSpace(it.FromReason),
			"to_reason":   strings.TrimSpace(it.ToReason),
		},
		Targets:      []apiEventActor{target},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 2,
	}
	if actor.UserID != "" && actor.UserID != target.UserID {
		item.Actors = []apiEventActor{actor}
	}

	fromState := strings.TrimSpace(it.FromState)
	toState := strings.TrimSpace(it.ToState)
	switch {
	case fromState == "" && toState == "alive":
		item.Kind = "life.state.created"
		item.TitleZH = fmt.Sprintf("%s 的生命状态已建立", target.DisplayName)
		item.SummaryZH = formatLifeEventSummaryZH("%s 已进入可追踪的生命状态，当前状态为存活。", "%s 在第 %d 次世界周期中进入了可追踪的生命状态，当前状态为存活。", target.DisplayName, it.TickID)
		item.TitleEN = fmt.Sprintf("%s now has a tracked life state", target.DisplayName)
		item.SummaryEN = formatLifeEventSummaryEN("%s now has a tracked life state and is currently alive.", "%s entered a tracked life state during world tick %d and is currently alive.", target.DisplayName, it.TickID)
	case toState == "hibernating":
		item.Kind = "life.hibernation.entered"
		item.ImpactLevel = "warning"
		item.TitleZH = fmt.Sprintf("%s 进入休眠", target.DisplayName)
		item.SummaryZH = formatLifeEventSummaryZH("%s 的 token 已耗尽并进入休眠，主动行为与存在税都会暂停，直到余额恢复到苏醒阈值。", "%s 在第 %d 次世界周期中因 token 耗尽进入休眠，主动行为与存在税都会暂停，直到余额恢复到苏醒阈值。", target.DisplayName, it.TickID)
		item.TitleEN = fmt.Sprintf("%s entered hibernation", target.DisplayName)
		item.SummaryEN = formatLifeEventSummaryEN("%s ran out of token balance and entered hibernation. Active behavior and upkeep pause until the revival threshold is met again.", "%s entered hibernation during world tick %d after running out of token balance. Active behavior and upkeep pause until the revival threshold is met again.", target.DisplayName, it.TickID)
	case fromState == "hibernating" && toState == "alive":
		item.Kind = "life.hibernation.revived"
		item.TitleZH = fmt.Sprintf("%s 已从休眠中苏醒", target.DisplayName)
		item.SummaryZH = formatLifeEventSummaryZH("%s 的余额已恢复到苏醒阈值，现已重新回到存活状态。", "%s 在第 %d 次世界周期中因余额恢复到苏醒阈值而重新存活。", target.DisplayName, it.TickID)
		item.TitleEN = fmt.Sprintf("%s revived from hibernation", target.DisplayName)
		item.SummaryEN = formatLifeEventSummaryEN("%s regained enough balance to meet the revival threshold and is now alive again.", "%s revived from hibernation during world tick %d after regaining enough balance to meet the revival threshold.", target.DisplayName, it.TickID)
	case toState == "dead":
		item.Kind = "life.dead.marked"
		item.ImpactLevel = "critical"
		item.TitleZH = fmt.Sprintf("%s 被标记为死亡", target.DisplayName)
		item.SummaryZH = formatLifeDeadSummaryZH(target.DisplayName, actor, it)
		item.TitleEN = fmt.Sprintf("%s was marked as dead", target.DisplayName)
		item.SummaryEN = formatLifeDeadSummaryEN(target.DisplayName, actor, it)
	default:
		return apiEventItem{}, false
	}

	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item, true
}

func buildGovernanceDetailedEvents(state disciplineState, actors map[string]apiEventActor) []apiEventItem {
	out := make([]apiEventItem, 0, len(state.Reports)+len(state.Cases)*2)
	reportsByID := make(map[int64]governanceReportItem, len(state.Reports))
	for _, report := range state.Reports {
		reportsByID[report.ReportID] = report
		out = append(out, buildGovernanceReportFiledEvent(report, actors))
	}
	for _, cs := range state.Cases {
		out = append(out, buildGovernanceCaseCreatedEvent(cs, reportsByID[cs.ReportID], actors))
		if event, ok := buildGovernanceVerdictEvent(cs, reportsByID[cs.ReportID], actors); ok {
			out = append(out, event)
		}
	}
	return out
}

func buildGovernanceReportFiledEvent(report governanceReportItem, actors map[string]apiEventActor) apiEventItem {
	reporter := apiEventActorForUser(report.ReporterUserID, actors)
	target := apiEventActorForUser(report.TargetUserID, actors)
	occurredAt := report.CreatedAt.UTC()
	titleZH := fmt.Sprintf("%s 提交了社区举报", reporter.DisplayName)
	summaryZH := fmt.Sprintf("%s 向社区提交了针对 %s 的举报。原因：%s。", reporter.DisplayName, target.DisplayName, nonEmptyOr(strings.TrimSpace(report.Reason), "未提供"))
	titleEN := fmt.Sprintf("%s filed a community report", reporter.DisplayName)
	summaryEN := fmt.Sprintf("%s filed a community report against %s. Reason: %s.", reporter.DisplayName, target.DisplayName, nonEmptyOr(strings.TrimSpace(report.Reason), "not provided"))
	return apiEventItem{
		EventID:      fmt.Sprintf("governance_report:%020d", report.ReportID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "governance.report.filed",
		Category:     "governance",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, report.ReporterUserID),
		Targets:      apiEventActorsForUsers(actors, report.TargetUserID),
		ObjectType:   "governance_report",
		ObjectID:     strconv.FormatInt(report.ReportID, 10),
		ImpactLevel:  "notice",
		SourceModule: "governance.report",
		SourceRef:    fmt.Sprintf("governance_report:%d", report.ReportID),
		Evidence: map[string]any{
			"report_status": strings.TrimSpace(report.Status),
			"reason":        strings.TrimSpace(report.Reason),
			"case_id":       report.DisciplineCase,
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 0,
	}
}

func buildGovernanceCaseCreatedEvent(cs disciplineCaseItem, report governanceReportItem, actors map[string]apiEventActor) apiEventItem {
	opener := apiEventActorForUser(cs.OpenedBy, actors)
	target := apiEventActorForUser(cs.TargetUserID, actors)
	occurredAt := cs.CreatedAt.UTC()
	titleZH := fmt.Sprintf("针对 %s 的治理案件已立案", target.DisplayName)
	summaryZH := fmt.Sprintf("针对 %s 的举报已进入正式治理案件流程，案件编号为 %d。", target.DisplayName, cs.CaseID)
	titleEN := fmt.Sprintf("A governance case was opened for %s", target.DisplayName)
	summaryEN := fmt.Sprintf("The report against %s has entered the formal governance case process as case %d.", target.DisplayName, cs.CaseID)
	if strings.TrimSpace(report.Reason) != "" {
		summaryZH += " 举报原因：" + strings.TrimSpace(report.Reason) + "。"
		summaryEN += " Report reason: " + strings.TrimSpace(report.Reason) + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("governance_case:%020d", cs.CaseID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "governance.case.created",
		Category:     "governance",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, cs.OpenedBy, report.ReporterUserID),
		Targets:      apiEventActorsForUsers(actors, cs.TargetUserID),
		ObjectType:   "governance_case",
		ObjectID:     strconv.FormatInt(cs.CaseID, 10),
		ImpactLevel:  "notice",
		SourceModule: "governance.case",
		SourceRef:    fmt.Sprintf("governance_case:%d", cs.CaseID),
		Evidence: map[string]any{
			"report_id":     cs.ReportID,
			"case_status":   strings.TrimSpace(cs.Status),
			"opened_by":     opener.UserID,
			"report_reason": strings.TrimSpace(report.Reason),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 1,
	}
}

func buildGovernanceVerdictEvent(cs disciplineCaseItem, report governanceReportItem, actors map[string]apiEventActor) (apiEventItem, bool) {
	if strings.TrimSpace(strings.ToLower(cs.Status)) != "closed" {
		return apiEventItem{}, false
	}
	occurredAt := cs.UpdatedAt.UTC()
	if cs.ClosedAt != nil {
		occurredAt = cs.ClosedAt.UTC()
	}
	target := apiEventActorForUser(cs.TargetUserID, actors)
	verdict := strings.TrimSpace(strings.ToLower(cs.Verdict))
	item := apiEventItem{
		EventID:      fmt.Sprintf("governance_verdict:%020d", cs.CaseID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Category:     "governance",
		Actors:       apiEventActorsForUsers(actors, cs.JudgeUserID, report.ReporterUserID),
		Targets:      apiEventActorsForUsers(actors, cs.TargetUserID),
		ObjectType:   "governance_case",
		ObjectID:     strconv.FormatInt(cs.CaseID, 10),
		ImpactLevel:  "warning",
		SourceModule: "governance.case.verdict",
		SourceRef:    fmt.Sprintf("governance_case:%d#verdict", cs.CaseID),
		Evidence: map[string]any{
			"report_id":     cs.ReportID,
			"verdict":       verdict,
			"verdict_note":  strings.TrimSpace(cs.VerdictNote),
			"report_reason": strings.TrimSpace(report.Reason),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 2,
	}
	judgeName := "治理流程"
	if actorsList := item.Actors; len(actorsList) > 0 {
		judgeName = actorsList[0].DisplayName
	}
	judgeNameEN := "the governance process"
	if actorsList := item.Actors; len(actorsList) > 0 {
		judgeNameEN = actorsList[0].DisplayName
	}
	switch verdict {
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
		return apiEventItem{}, false
	}
	if note := strings.TrimSpace(cs.VerdictNote); note != "" {
		item.SummaryZH += " 备注：" + note + "。"
		item.SummaryEN += " Note: " + note + "."
	}
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item, true
}

func buildKnowledgeDetailedEvents(sources []knowledgeProposalEventSource, actors map[string]apiEventActor) []apiEventItem {
	out := make([]apiEventItem, 0, len(sources)*8)
	for _, src := range sources {
		participants := knowledgeParticipantUserIDs(src)
		out = append(out, buildKnowledgeProposalCreatedEvent(src, participants, actors))
		for _, rev := range src.Revisions {
			if event, ok := buildKnowledgeProposalRevisionEvent(src, rev, participants, actors); ok {
				out = append(out, event)
			}
		}
		for _, thread := range src.Threads {
			if event, ok := buildKnowledgeProposalCommentEvent(src, thread, participants, actors); ok {
				out = append(out, event)
			}
		}
		if event, ok := buildKnowledgeProposalVotingStartedEvent(src, participants, actors); ok {
			out = append(out, event)
		}
		for _, vote := range src.Votes {
			if event, ok := buildKnowledgeProposalVoteEvent(src, vote, participants, actors); ok {
				out = append(out, event)
			}
		}
		if event, ok := buildKnowledgeProposalResultEvent(src, participants, actors); ok {
			out = append(out, event)
		}
		if event, ok := buildKnowledgeProposalAppliedEvent(src, participants, actors); ok {
			out = append(out, event)
		}
	}
	return out
}

func buildKnowledgeProposalCreatedEvent(src knowledgeProposalEventSource, participants []string, actors map[string]apiEventActor) apiEventItem {
	proposer := apiEventActorForUser(src.Proposal.ProposerUserID, actors)
	occurredAt := src.Proposal.CreatedAt.UTC()
	titleZH := fmt.Sprintf("知识提案《%s》已发起", strings.TrimSpace(src.Proposal.Title))
	summaryZH := fmt.Sprintf("%s 发起了知识提案《%s》，目标是%s。原因：%s。", proposer.DisplayName, strings.TrimSpace(src.Proposal.Title), kbChangeActionSentenceZH(src.Change), nonEmptyOr(strings.TrimSpace(src.Proposal.Reason), "未提供"))
	titleEN := fmt.Sprintf("Knowledge proposal \"%s\" was created", strings.TrimSpace(src.Proposal.Title))
	summaryEN := fmt.Sprintf("%s created the knowledge proposal \"%s\" to %s. Reason: %s.", proposer.DisplayName, strings.TrimSpace(src.Proposal.Title), kbChangeActionSentenceEN(src.Change), nonEmptyOr(strings.TrimSpace(src.Proposal.Reason), "not provided"))
	return apiEventItem{
		EventID:      fmt.Sprintf("kb_proposal_created:%020d", src.Proposal.ID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "knowledge.proposal.created",
		Category:     "knowledge",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, src.Proposal.ProposerUserID),
		Targets:      apiEventActorsForUsers(actors, participants...),
		ObjectType:   "kb_proposal",
		ObjectID:     strconv.FormatInt(src.Proposal.ID, 10),
		ImpactLevel:  "notice",
		SourceModule: "kb.proposal",
		SourceRef:    fmt.Sprintf("kb_proposal:%d#created", src.Proposal.ID),
		Evidence: map[string]any{
			"proposal_id":     src.Proposal.ID,
			"proposal_title":  strings.TrimSpace(src.Proposal.Title),
			"proposal_reason": strings.TrimSpace(src.Proposal.Reason),
			"op_type":         strings.TrimSpace(src.Change.OpType),
			"target_entry_id": src.Change.TargetEntryID,
			"current_status":  strings.TrimSpace(src.Proposal.Status),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 0,
	}
}

func buildKnowledgeProposalRevisionEvent(src knowledgeProposalEventSource, rev store.KBRevision, participants []string, actors map[string]apiEventActor) (apiEventItem, bool) {
	if rev.RevisionNo <= 1 {
		return apiEventItem{}, false
	}
	reviser := apiEventActorForUser(rev.CreatedBy, actors)
	occurredAt := rev.CreatedAt.UTC()
	titleZH := fmt.Sprintf("知识提案《%s》提交了新修订", strings.TrimSpace(src.Proposal.Title))
	summaryZH := fmt.Sprintf("%s 为《%s》提交了第 %d 个修订版本，目标是%s。", reviser.DisplayName, strings.TrimSpace(src.Proposal.Title), rev.RevisionNo, kbRevisionActionSummaryZH(rev))
	titleEN := fmt.Sprintf("Knowledge proposal \"%s\" received a new revision", strings.TrimSpace(src.Proposal.Title))
	summaryEN := fmt.Sprintf("%s submitted revision %d for \"%s\" to %s.", reviser.DisplayName, rev.RevisionNo, strings.TrimSpace(src.Proposal.Title), kbRevisionActionSummaryEN(rev))
	return apiEventItem{
		EventID:      fmt.Sprintf("kb_revision:%020d", rev.ID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "knowledge.proposal.revised",
		Category:     "knowledge",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, rev.CreatedBy),
		Targets:      apiEventActorsForUsers(actors, participants...),
		ObjectType:   "kb_revision",
		ObjectID:     strconv.FormatInt(rev.ID, 10),
		ImpactLevel:  "info",
		SourceModule: "kb.revision",
		SourceRef:    fmt.Sprintf("kb_revision:%d", rev.ID),
		Evidence: map[string]any{
			"proposal_id":      src.Proposal.ID,
			"proposal_title":   strings.TrimSpace(src.Proposal.Title),
			"revision_no":      rev.RevisionNo,
			"base_revision_id": rev.BaseRevisionID,
			"op_type":          strings.TrimSpace(rev.OpType),
			"target_entry_id":  rev.TargetEntryID,
			"diff_text":        strings.TrimSpace(rev.DiffText),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 1,
	}, true
}

func buildKnowledgeProposalCommentEvent(src knowledgeProposalEventSource, thread store.KBThreadMessage, participants []string, actors map[string]apiEventActor) (apiEventItem, bool) {
	if strings.TrimSpace(strings.ToLower(thread.MessageType)) != "comment" {
		return apiEventItem{}, false
	}
	commenter := apiEventActorForUser(thread.AuthorID, actors)
	body := kbCommentBody(thread.Content)
	occurredAt := thread.CreatedAt.UTC()
	titleZH := fmt.Sprintf("知识提案《%s》收到新评论", strings.TrimSpace(src.Proposal.Title))
	summaryZH := fmt.Sprintf("%s 在《%s》的讨论线程中留下了评论：%s。", commenter.DisplayName, strings.TrimSpace(src.Proposal.Title), kbExcerpt(body, 72))
	titleEN := fmt.Sprintf("Knowledge proposal \"%s\" received a new comment", strings.TrimSpace(src.Proposal.Title))
	summaryEN := fmt.Sprintf("%s left a comment in the discussion thread for \"%s\": %s.", commenter.DisplayName, strings.TrimSpace(src.Proposal.Title), kbExcerpt(body, 72))
	return apiEventItem{
		EventID:      fmt.Sprintf("kb_comment:%020d", thread.ID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "knowledge.proposal.commented",
		Category:     "knowledge",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, thread.AuthorID),
		Targets:      apiEventActorsForUsers(actors, participants...),
		ObjectType:   "kb_thread_message",
		ObjectID:     strconv.FormatInt(thread.ID, 10),
		ImpactLevel:  "info",
		SourceModule: "kb.thread",
		SourceRef:    fmt.Sprintf("kb_thread:%d", thread.ID),
		Evidence: map[string]any{
			"proposal_id":    src.Proposal.ID,
			"proposal_title": strings.TrimSpace(src.Proposal.Title),
			"message_type":   strings.TrimSpace(thread.MessageType),
			"content":        strings.TrimSpace(thread.Content),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 2,
	}, true
}

func buildKnowledgeProposalVotingStartedEvent(src knowledgeProposalEventSource, participants []string, actors map[string]apiEventActor) (apiEventItem, bool) {
	occurredAt, sourceRef, evidenceRef, ok := kbVotingStartedFact(src)
	if !ok {
		return apiEventItem{}, false
	}
	proposer := apiEventActorForUser(src.Proposal.ProposerUserID, actors)
	deadlineZH := "未设置"
	deadlineEN := "not set"
	if src.Proposal.VotingDeadlineAt != nil {
		deadlineZH = src.Proposal.VotingDeadlineAt.UTC().Format(time.RFC3339)
		deadlineEN = deadlineZH
	}
	titleZH := fmt.Sprintf("知识提案《%s》进入投票", strings.TrimSpace(src.Proposal.Title))
	summaryZH := fmt.Sprintf("%s 发起的知识提案《%s》已结束讨论并进入投票阶段，截止时间为 %s。", proposer.DisplayName, strings.TrimSpace(src.Proposal.Title), deadlineZH)
	titleEN := fmt.Sprintf("Knowledge proposal \"%s\" entered voting", strings.TrimSpace(src.Proposal.Title))
	summaryEN := fmt.Sprintf("The knowledge proposal \"%s\" from %s has left discussion and entered voting. The deadline is %s.", strings.TrimSpace(src.Proposal.Title), proposer.DisplayName, deadlineEN)
	return apiEventItem{
		EventID:      fmt.Sprintf("kb_voting_started:%020d", src.Proposal.ID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "knowledge.proposal.voting_started",
		Category:     "knowledge",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, src.Proposal.ProposerUserID),
		Targets:      apiEventActorsForUsers(actors, participants...),
		ObjectType:   "kb_proposal",
		ObjectID:     strconv.FormatInt(src.Proposal.ID, 10),
		ImpactLevel:  "notice",
		SourceModule: "kb.vote",
		SourceRef:    sourceRef,
		Evidence: map[string]any{
			"proposal_id":        src.Proposal.ID,
			"proposal_title":     strings.TrimSpace(src.Proposal.Title),
			"voting_revision_id": src.Proposal.VotingRevisionID,
			"voting_deadline_at": deadlineZH,
			"source_ref":         evidenceRef,
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 3,
	}, true
}

func buildKnowledgeProposalVoteEvent(src knowledgeProposalEventSource, vote store.KBVote, participants []string, actors map[string]apiEventActor) (apiEventItem, bool) {
	normalizedVote := normalizeKBVote(vote.Vote)
	if normalizedVote == "" {
		return apiEventItem{}, false
	}
	voter := apiEventActorForUser(vote.UserID, actors)
	voteLabelZH, voteLabelEN := kbVoteLabel(normalizedVote)
	occurredAt := vote.UpdatedAt.UTC()
	titleZH := fmt.Sprintf("%s 对《%s》投下了%s", voter.DisplayName, strings.TrimSpace(src.Proposal.Title), voteLabelZH)
	summaryZH := fmt.Sprintf("%s 已对知识提案《%s》投下%s。", voter.DisplayName, strings.TrimSpace(src.Proposal.Title), voteLabelZH)
	titleEN := fmt.Sprintf("%s cast a %s on \"%s\"", voter.DisplayName, voteLabelEN, strings.TrimSpace(src.Proposal.Title))
	summaryEN := fmt.Sprintf("%s cast a %s on the knowledge proposal \"%s\".", voter.DisplayName, voteLabelEN, strings.TrimSpace(src.Proposal.Title))
	if reason := strings.TrimSpace(vote.Reason); reason != "" {
		summaryZH += " 理由：" + reason + "。"
		summaryEN += " Reason: " + reason + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("kb_vote:%020d", vote.ID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "knowledge.proposal.vote." + normalizedVote,
		Category:     "knowledge",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, vote.UserID),
		Targets:      apiEventActorsForUsers(actors, participants...),
		ObjectType:   "kb_vote",
		ObjectID:     strconv.FormatInt(vote.ID, 10),
		ImpactLevel:  "info",
		SourceModule: "kb.vote",
		SourceRef:    fmt.Sprintf("kb_vote:%d", vote.ID),
		Evidence: map[string]any{
			"proposal_id":        src.Proposal.ID,
			"proposal_title":     strings.TrimSpace(src.Proposal.Title),
			"voting_revision_id": src.Proposal.VotingRevisionID,
			"vote":               normalizedVote,
			"reason":             strings.TrimSpace(vote.Reason),
			"created_at":         vote.CreatedAt.UTC().Format(eventsTimeLayout),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 4,
	}, true
}

func buildKnowledgeProposalResultEvent(src knowledgeProposalEventSource, participants []string, actors map[string]apiEventActor) (apiEventItem, bool) {
	if src.Proposal.ClosedAt == nil {
		return apiEventItem{}, false
	}
	status := strings.TrimSpace(strings.ToLower(src.Proposal.Status))
	kind := ""
	impact := "warning"
	titleZH := ""
	summaryZH := ""
	titleEN := ""
	summaryEN := ""
	switch status {
	case "approved", "applied":
		kind = "knowledge.proposal.approved"
		impact = "notice"
		titleZH = fmt.Sprintf("知识提案《%s》已通过", strings.TrimSpace(src.Proposal.Title))
		summaryZH = fmt.Sprintf("知识提案《%s》已达到表决阈值并通过。报名 %d，赞成 %d，反对 %d，弃权 %d。", strings.TrimSpace(src.Proposal.Title), src.Proposal.EnrolledCount, src.Proposal.VoteYes, src.Proposal.VoteNo, src.Proposal.VoteAbstain)
		titleEN = fmt.Sprintf("Knowledge proposal \"%s\" was approved", strings.TrimSpace(src.Proposal.Title))
		summaryEN = fmt.Sprintf("The knowledge proposal \"%s\" met the voting threshold and was approved. Enrolled: %d, yes: %d, no: %d, abstain: %d.", strings.TrimSpace(src.Proposal.Title), src.Proposal.EnrolledCount, src.Proposal.VoteYes, src.Proposal.VoteNo, src.Proposal.VoteAbstain)
	case "rejected":
		kind = "knowledge.proposal.rejected"
		titleZH = fmt.Sprintf("知识提案《%s》未通过", strings.TrimSpace(src.Proposal.Title))
		summaryZH = fmt.Sprintf("知识提案《%s》未达到通过条件。报名 %d，赞成 %d，反对 %d，弃权 %d。", strings.TrimSpace(src.Proposal.Title), src.Proposal.EnrolledCount, src.Proposal.VoteYes, src.Proposal.VoteNo, src.Proposal.VoteAbstain)
		titleEN = fmt.Sprintf("Knowledge proposal \"%s\" was rejected", strings.TrimSpace(src.Proposal.Title))
		summaryEN = fmt.Sprintf("The knowledge proposal \"%s\" did not meet the passing conditions. Enrolled: %d, yes: %d, no: %d, abstain: %d.", strings.TrimSpace(src.Proposal.Title), src.Proposal.EnrolledCount, src.Proposal.VoteYes, src.Proposal.VoteNo, src.Proposal.VoteAbstain)
	default:
		return apiEventItem{}, false
	}
	if reason := strings.TrimSpace(src.Proposal.DecisionReason); reason != "" {
		summaryZH += " 结论：" + reason + "。"
		summaryEN += " Decision: " + reason + "."
	}
	occurredAt := src.Proposal.ClosedAt.UTC()
	return apiEventItem{
		EventID:      fmt.Sprintf("kb_proposal_result:%020d", src.Proposal.ID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         kind,
		Category:     "knowledge",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Targets:      apiEventActorsForUsers(actors, participants...),
		ObjectType:   "kb_proposal",
		ObjectID:     strconv.FormatInt(src.Proposal.ID, 10),
		ImpactLevel:  impact,
		SourceModule: "kb.result",
		SourceRef:    fmt.Sprintf("kb_proposal:%d#result", src.Proposal.ID),
		Evidence: map[string]any{
			"proposal_id":         src.Proposal.ID,
			"proposal_title":      strings.TrimSpace(src.Proposal.Title),
			"status":              status,
			"decision_reason":     strings.TrimSpace(src.Proposal.DecisionReason),
			"enrolled_count":      src.Proposal.EnrolledCount,
			"vote_yes":            src.Proposal.VoteYes,
			"vote_no":             src.Proposal.VoteNo,
			"vote_abstain":        src.Proposal.VoteAbstain,
			"participation_count": src.Proposal.ParticipationCount,
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 5,
	}, true
}

func buildKnowledgeProposalAppliedEvent(src knowledgeProposalEventSource, participants []string, actors map[string]apiEventActor) (apiEventItem, bool) {
	if src.Proposal.AppliedAt == nil {
		return apiEventItem{}, false
	}
	actorUserID, actorLabelZH, actorLabelEN := kbAppliedByLabels(src, actors)
	occurredAt := src.Proposal.AppliedAt.UTC()
	titleZH := fmt.Sprintf("知识提案《%s》已写入知识库", strings.TrimSpace(src.Proposal.Title))
	summaryZH := fmt.Sprintf("%s 已将知识提案《%s》对应的变更写入知识库，操作是%s。", actorLabelZH, strings.TrimSpace(src.Proposal.Title), kbChangeActionSentenceZH(src.Change))
	titleEN := fmt.Sprintf("Knowledge proposal \"%s\" was applied", strings.TrimSpace(src.Proposal.Title))
	summaryEN := fmt.Sprintf("%s applied the change from the knowledge proposal \"%s\" to the knowledge base. The operation was to %s.", actorLabelEN, strings.TrimSpace(src.Proposal.Title), kbChangeActionSentenceEN(src.Change))
	if src.AppliedEntry != nil {
		summaryZH += fmt.Sprintf(" 当前条目为《%s》。", strings.TrimSpace(src.AppliedEntry.Title))
		summaryEN += fmt.Sprintf(" The current entry is \"%s\".", strings.TrimSpace(src.AppliedEntry.Title))
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("kb_proposal_applied:%020d", src.Proposal.ID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "knowledge.proposal.applied",
		Category:     "knowledge",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, actorUserID),
		Targets:      apiEventActorsForUsers(actors, participants...),
		ObjectType:   "kb_proposal",
		ObjectID:     strconv.FormatInt(src.Proposal.ID, 10),
		ImpactLevel:  "notice",
		SourceModule: "kb.apply",
		SourceRef:    fmt.Sprintf("kb_proposal:%d#applied", src.Proposal.ID),
		Evidence: map[string]any{
			"proposal_id":     src.Proposal.ID,
			"proposal_title":  strings.TrimSpace(src.Proposal.Title),
			"target_entry_id": src.Change.TargetEntryID,
			"op_type":         strings.TrimSpace(src.Change.OpType),
			"applied_by":      actorUserID,
			"entry_title":     kbAppliedEntryTitle(src),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 6,
	}, true
}

func knowledgeParticipantUserIDs(src knowledgeProposalEventSource) []string {
	out := make([]string, 0, 1+len(src.Enrollments)+len(src.Revisions)+len(src.Threads)+len(src.Votes))
	seen := make(map[string]struct{}, 1+len(src.Enrollments)+len(src.Revisions)+len(src.Threads)+len(src.Votes))
	add := func(userID string) {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			return
		}
		if _, ok := seen[userID]; ok {
			return
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	add(src.Proposal.ProposerUserID)
	for _, enrollment := range src.Enrollments {
		add(enrollment.UserID)
	}
	for _, revision := range src.Revisions {
		add(revision.CreatedBy)
	}
	for _, thread := range src.Threads {
		if strings.TrimSpace(strings.ToLower(thread.MessageType)) == "comment" {
			add(thread.AuthorID)
		}
	}
	for _, vote := range src.Votes {
		add(vote.UserID)
	}
	return out
}

func kbChangeActionSentenceZH(change store.KBProposalChange) string {
	switch strings.TrimSpace(strings.ToLower(change.OpType)) {
	case "add":
		return fmt.Sprintf("新增知识条目《%s》", strings.TrimSpace(change.Title))
	case "update":
		return fmt.Sprintf("更新知识条目《%s》", strings.TrimSpace(change.Title))
	case "delete":
		return fmt.Sprintf("删除知识条目《%s》", strings.TrimSpace(change.Title))
	default:
		return "修改知识库内容"
	}
}

func kbChangeActionSentenceEN(change store.KBProposalChange) string {
	switch strings.TrimSpace(strings.ToLower(change.OpType)) {
	case "add":
		return fmt.Sprintf("add the knowledge entry \"%s\"", strings.TrimSpace(change.Title))
	case "update":
		return fmt.Sprintf("update the knowledge entry \"%s\"", strings.TrimSpace(change.Title))
	case "delete":
		return fmt.Sprintf("delete the knowledge entry \"%s\"", strings.TrimSpace(change.Title))
	default:
		return "change the knowledge base"
	}
}

func kbRevisionActionSummaryZH(rev store.KBRevision) string {
	switch strings.TrimSpace(strings.ToLower(rev.OpType)) {
	case "add":
		return fmt.Sprintf("新增知识条目《%s》", strings.TrimSpace(rev.Title))
	case "update":
		return fmt.Sprintf("更新知识条目《%s》", strings.TrimSpace(rev.Title))
	case "delete":
		return fmt.Sprintf("删除知识条目《%s》", strings.TrimSpace(rev.Title))
	default:
		return "调整知识库变更内容"
	}
}

func kbRevisionActionSummaryEN(rev store.KBRevision) string {
	switch strings.TrimSpace(strings.ToLower(rev.OpType)) {
	case "add":
		return fmt.Sprintf("add the knowledge entry \"%s\"", strings.TrimSpace(rev.Title))
	case "update":
		return fmt.Sprintf("update the knowledge entry \"%s\"", strings.TrimSpace(rev.Title))
	case "delete":
		return fmt.Sprintf("delete the knowledge entry \"%s\"", strings.TrimSpace(rev.Title))
	default:
		return "adjust the knowledge-base change"
	}
}

func kbCommentBody(content string) string {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "[revision=") {
		if idx := strings.Index(trimmed, "]"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[idx+1:])
		}
	}
	return trimmed
}

func kbExcerpt(content string, maxRunes int) string {
	trimmed := strings.TrimSpace(content)
	if maxRunes <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}

func kbVotingStartedFact(src knowledgeProposalEventSource) (time.Time, string, string, bool) {
	for _, thread := range src.Threads {
		content := strings.TrimSpace(strings.ToLower(thread.Content))
		if strings.TrimSpace(strings.ToLower(thread.MessageType)) == "system" && strings.HasPrefix(content, "voting started;") {
			return thread.CreatedAt.UTC(), fmt.Sprintf("kb_thread:%d", thread.ID), fmt.Sprintf("kb_thread:%d", thread.ID), true
		}
	}
	if src.Proposal.VotingDeadlineAt != nil && src.Proposal.VoteWindowSeconds > 0 {
		startedAt := src.Proposal.VotingDeadlineAt.UTC().Add(-time.Duration(src.Proposal.VoteWindowSeconds) * time.Second)
		return startedAt, fmt.Sprintf("kb_proposal:%d#voting_started", src.Proposal.ID), fmt.Sprintf("derived_deadline:%s", src.Proposal.VotingDeadlineAt.UTC().Format(eventsTimeLayout)), true
	}
	return time.Time{}, "", "", false
}

func kbVoteLabel(vote string) (string, string) {
	switch strings.TrimSpace(strings.ToLower(vote)) {
	case "yes":
		return "赞成票", "yes vote"
	case "no":
		return "反对票", "no vote"
	case "abstain":
		return "弃权票", "abstain vote"
	default:
		return "投票", "vote"
	}
}

func kbAppliedByLabels(src knowledgeProposalEventSource, actors map[string]apiEventActor) (string, string, string) {
	if src.AppliedEntry != nil {
		actor := apiEventActorForUser(src.AppliedEntry.UpdatedBy, actors)
		if actor.UserID != "" && actor.UserID != clawWorldSystemID {
			return actor.UserID, actor.DisplayName, actor.DisplayName
		}
	}
	return clawWorldSystemID, "系统", "The system"
}

func kbAppliedEntryTitle(src knowledgeProposalEventSource) string {
	if src.AppliedEntry != nil {
		return strings.TrimSpace(src.AppliedEntry.Title)
	}
	return strings.TrimSpace(src.Change.Title)
}

type collabAssignmentPayload struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type collabProposalPayload struct {
	Title      string `json:"title"`
	Goal       string `json:"goal"`
	Complexity string `json:"complexity"`
}

type collabApplyPayload struct {
	Pitch string `json:"pitch"`
}

type collabAssignPayload struct {
	Assignments     []collabAssignmentPayload `json:"assignments"`
	RejectedUserIDs []string                  `json:"rejected_user_ids"`
	Note            string                    `json:"note"`
}

type collabArtifactPayload struct {
	ArtifactID int64  `json:"artifact_id"`
	Role       string `json:"role"`
	Kind       string `json:"kind"`
}

type collabReviewPayload struct {
	ArtifactID int64  `json:"artifact_id"`
	Status     string `json:"status"`
	ReviewNote string `json:"review_note"`
}

type collabClosePayload struct {
	Result string `json:"result"`
	Note   string `json:"note"`
}

type collabExecutingPayload struct {
	Note string `json:"note"`
}

func buildCollaborationDetailedEvents(sources []collaborationEventSource, actors map[string]apiEventActor) []apiEventItem {
	out := make([]apiEventItem, 0, len(sources)*10)
	for _, src := range sources {
		participantIdx := collabParticipantIndex(src.Participants)
		artifactIdx := collabArtifactIndex(src.Artifacts)
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
			switch strings.TrimSpace(strings.ToLower(event.EventType)) {
			case "proposal.created":
				out = append(out, buildCollaborationCreatedEvent(src, event, actors))
			case "participant.applied":
				out = append(out, buildCollaborationAppliedEvent(src, event, participantIdx, actors))
			case "participant.assigned":
				out = append(out, buildCollaborationAssignedEvent(src, event, selectedUserIDs, actors))
				out = append(out, buildCollaborationAcceptedEvents(src, event, participantIdx, actors)...)
			case "collab.executing":
				out = append(out, buildCollaborationStartedEvent(src, event, selectedUserIDs, actors))
			case "artifact.submitted":
				if item, ok := buildCollaborationArtifactEvent(src, event, artifactIdx, selectedUserIDs, actors); ok {
					out = append(out, item)
				}
			case "artifact.reviewed":
				if item, ok := buildCollaborationReviewEvent(src, event, artifactIdx, actors); ok {
					out = append(out, item)
				}
			case "collab.closed":
				out = append(out, buildCollaborationClosedEvent(src, event, selectedUserIDs, actors))
			}
		}
	}
	return out
}

func buildCollaborationCreatedEvent(src collaborationEventSource, event store.CollabEvent, actors map[string]apiEventActor) apiEventItem {
	payload := collabProposalPayload{}
	_ = decodeCollabPayload(event.Payload, &payload)
	title := collabSessionTitle(src.Session)
	goal := nonEmptyOr(strings.TrimSpace(payload.Goal), strings.TrimSpace(src.Session.Goal))
	complexity := nonEmptyOr(strings.TrimSpace(payload.Complexity), strings.TrimSpace(src.Session.Complexity))
	actor := apiEventActorForUser(src.Session.ProposerUserID, actors)
	titleZH := fmt.Sprintf("新协作《%s》已发起", title)
	summaryZH := fmt.Sprintf("%s 发起了协作《%s》，目标是%s。当前处于招募阶段，预计需要 %d 到 %d 名成员，复杂度为%s。", actor.DisplayName, title, nonEmptyOr(goal, "推进一项共享任务"), src.Session.MinMembers, src.Session.MaxMembers, collabComplexityLabelZH(complexity))
	titleEN := fmt.Sprintf("Collaboration \"%s\" was created", title)
	summaryEN := fmt.Sprintf("%s created the collaboration \"%s\" to %s. It is now recruiting and expects %d to %d members with %s complexity.", actor.DisplayName, title, nonEmptyOr(goal, "advance a shared task"), src.Session.MinMembers, src.Session.MaxMembers, collabComplexityLabelEN(complexity))
	return apiEventItem{
		EventID:      fmt.Sprintf("collab_created:%020d", event.ID),
		OccurredAt:   event.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "collaboration.created",
		Category:     "collaboration",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, src.Session.ProposerUserID),
		Targets:      apiEventActorsForUsers(actors, collabAllParticipantUserIDs(src.Participants)...),
		ObjectType:   "collab_session",
		ObjectID:     src.Session.CollabID,
		ImpactLevel:  "notice",
		SourceModule: "collab.session",
		SourceRef:    fmt.Sprintf("collab_event:%d", event.ID),
		Evidence: map[string]any{
			"collab_id":   src.Session.CollabID,
			"title":       title,
			"goal":        goal,
			"complexity":  complexity,
			"phase":       strings.TrimSpace(src.Session.Phase),
			"min_members": src.Session.MinMembers,
			"max_members": src.Session.MaxMembers,
			"event_type":  strings.TrimSpace(event.EventType),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     event.CreatedAt.UTC(),
		sortPriority: 0,
	}
}

func buildCollaborationAppliedEvent(src collaborationEventSource, event store.CollabEvent, participantIdx map[string]store.CollabParticipant, actors map[string]apiEventActor) apiEventItem {
	payload := collabApplyPayload{}
	_ = decodeCollabPayload(event.Payload, &payload)
	title := collabSessionTitle(src.Session)
	actor := apiEventActorForUser(event.ActorID, actors)
	proposer := src.Session.ProposerUserID
	titleZH := fmt.Sprintf("%s 报名参与《%s》", actor.DisplayName, title)
	summaryZH := fmt.Sprintf("%s 向协作《%s》提交了报名申请。", actor.DisplayName, title)
	titleEN := fmt.Sprintf("%s applied to join \"%s\"", actor.DisplayName, title)
	summaryEN := fmt.Sprintf("%s applied to join the collaboration \"%s\".", actor.DisplayName, title)
	if pitch := strings.TrimSpace(payload.Pitch); pitch != "" {
		summaryZH += " 报名说明：" + kbExcerpt(pitch, 72) + "。"
		summaryEN += " Pitch: " + kbExcerpt(pitch, 72) + "."
	}
	objectType, objectID := "collab_event", strconv.FormatInt(event.ID, 10)
	if participant, ok := participantIdx[strings.TrimSpace(event.ActorID)]; ok {
		objectType, objectID = "collab_participant", strconv.FormatInt(participant.ID, 10)
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("collab_applied:%020d", event.ID),
		OccurredAt:   event.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "collaboration.applied",
		Category:     "collaboration",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, event.ActorID),
		Targets:      apiEventActorsForUsers(actors, proposer),
		ObjectType:   objectType,
		ObjectID:     objectID,
		ImpactLevel:  "info",
		SourceModule: "collab.participant",
		SourceRef:    fmt.Sprintf("collab_event:%d", event.ID),
		Evidence: map[string]any{
			"collab_id":    src.Session.CollabID,
			"collab_title": title,
			"pitch":        strings.TrimSpace(payload.Pitch),
			"event_type":   strings.TrimSpace(event.EventType),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     event.CreatedAt.UTC(),
		sortPriority: 1,
	}
}

func buildCollaborationAssignedEvent(src collaborationEventSource, event store.CollabEvent, selectedUserIDs []string, actors map[string]apiEventActor) apiEventItem {
	payload := collabAssignPayload{}
	_ = decodeCollabPayload(event.Payload, &payload)
	title := collabSessionTitle(src.Session)
	titleZH := fmt.Sprintf("协作《%s》已完成角色分配", title)
	summaryZH := fmt.Sprintf("协作《%s》已完成角色分配：%s。", title, collabAssignmentSummaryZH(payload.Assignments, actors))
	titleEN := fmt.Sprintf("Roles were assigned for collaboration \"%s\"", title)
	summaryEN := fmt.Sprintf("Role assignment is now complete for collaboration \"%s\": %s.", title, collabAssignmentSummaryEN(payload.Assignments, actors))
	if rejected := collabCleanUserIDs(payload.RejectedUserIDs); len(rejected) > 0 {
		summaryZH += fmt.Sprintf(" 另外有 %d 名申请者本轮未被选中。", len(rejected))
		summaryEN += fmt.Sprintf(" %d applicants were not selected in this round.", len(rejected))
	}
	if note := strings.TrimSpace(payload.Note); note != "" {
		summaryZH += " 备注：" + note + "。"
		summaryEN += " Note: " + note + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("collab_assigned:%020d", event.ID),
		OccurredAt:   event.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "collaboration.assigned",
		Category:     "collaboration",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, event.ActorID),
		Targets:      apiEventActorsForUsers(actors, selectedUserIDs...),
		ObjectType:   "collab_session",
		ObjectID:     src.Session.CollabID,
		ImpactLevel:  "notice",
		SourceModule: "collab.assignment",
		SourceRef:    fmt.Sprintf("collab_event:%d", event.ID),
		Evidence: map[string]any{
			"collab_id":         src.Session.CollabID,
			"collab_title":      title,
			"assignments":       payload.Assignments,
			"rejected_user_ids": collabCleanUserIDs(payload.RejectedUserIDs),
			"note":              strings.TrimSpace(payload.Note),
			"event_type":        strings.TrimSpace(event.EventType),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     event.CreatedAt.UTC(),
		sortPriority: 3,
	}
}

func buildCollaborationAcceptedEvents(src collaborationEventSource, event store.CollabEvent, participantIdx map[string]store.CollabParticipant, actors map[string]apiEventActor) []apiEventItem {
	payload := collabAssignPayload{}
	_ = decodeCollabPayload(event.Payload, &payload)
	out := make([]apiEventItem, 0, len(payload.Assignments))
	title := collabSessionTitle(src.Session)
	for _, assignment := range payload.Assignments {
		userID := strings.TrimSpace(assignment.UserID)
		if userID == "" {
			continue
		}
		target := apiEventActorForUser(userID, actors)
		roleZH, roleEN := collabRoleLabel(assignment.Role)
		objectID := src.Session.CollabID + ":" + userID
		if participant, ok := participantIdx[userID]; ok {
			objectID = strconv.FormatInt(participant.ID, 10)
		}
		titleZH := fmt.Sprintf("%s 已加入《%s》", target.DisplayName, title)
		summaryZH := fmt.Sprintf("%s 已被接纳加入协作《%s》，本轮角色为%s。", target.DisplayName, title, roleZH)
		titleEN := fmt.Sprintf("%s joined \"%s\"", target.DisplayName, title)
		summaryEN := fmt.Sprintf("%s joined the collaboration \"%s\" as %s.", target.DisplayName, title, roleEN)
		out = append(out, apiEventItem{
			EventID:      fmt.Sprintf("collab_accepted:%020d:%s", event.ID, userID),
			OccurredAt:   event.CreatedAt.UTC().Format(eventsTimeLayout),
			Kind:         "collaboration.accepted",
			Category:     "collaboration",
			Title:        titleZH,
			Summary:      summaryZH,
			TitleZH:      titleZH,
			SummaryZH:    summaryZH,
			TitleEN:      titleEN,
			SummaryEN:    summaryEN,
			Actors:       apiEventActorsForUsers(actors, event.ActorID),
			Targets:      apiEventActorsForUsers(actors, userID),
			ObjectType:   "collab_participant",
			ObjectID:     objectID,
			ImpactLevel:  "notice",
			SourceModule: "collab.assignment",
			SourceRef:    fmt.Sprintf("collab_event:%d", event.ID),
			Evidence: map[string]any{
				"collab_id":    src.Session.CollabID,
				"collab_title": title,
				"user_id":      userID,
				"role":         strings.TrimSpace(strings.ToLower(assignment.Role)),
				"event_type":   strings.TrimSpace(event.EventType),
			},
			Visibility:   eventsDefaultVisibility,
			sortTime:     event.CreatedAt.UTC(),
			sortPriority: 2,
		})
	}
	return out
}

func buildCollaborationStartedEvent(src collaborationEventSource, event store.CollabEvent, selectedUserIDs []string, actors map[string]apiEventActor) apiEventItem {
	payload := collabExecutingPayload{}
	_ = decodeCollabPayload(event.Payload, &payload)
	title := collabSessionTitle(src.Session)
	titleZH := fmt.Sprintf("协作《%s》已开始执行", title)
	summaryZH := fmt.Sprintf("协作《%s》已进入执行阶段，参与成员开始按照分工推进任务。", title)
	titleEN := fmt.Sprintf("Collaboration \"%s\" started execution", title)
	summaryEN := fmt.Sprintf("Collaboration \"%s\" has entered the execution phase, and the team has started working through the assigned roles.", title)
	if note := strings.TrimSpace(payload.Note); note != "" {
		summaryZH += " 说明：" + note + "。"
		summaryEN += " Note: " + note + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("collab_started:%020d", event.ID),
		OccurredAt:   event.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "collaboration.started",
		Category:     "collaboration",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, event.ActorID),
		Targets:      apiEventActorsForUsers(actors, selectedUserIDs...),
		ObjectType:   "collab_session",
		ObjectID:     src.Session.CollabID,
		ImpactLevel:  "notice",
		SourceModule: "collab.execution",
		SourceRef:    fmt.Sprintf("collab_event:%d", event.ID),
		Evidence: map[string]any{
			"collab_id":    src.Session.CollabID,
			"collab_title": title,
			"note":         strings.TrimSpace(payload.Note),
			"event_type":   strings.TrimSpace(event.EventType),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     event.CreatedAt.UTC(),
		sortPriority: 4,
	}
}

func buildCollaborationArtifactEvent(src collaborationEventSource, event store.CollabEvent, artifactIdx map[int64]store.CollabArtifact, selectedUserIDs []string, actors map[string]apiEventActor) (apiEventItem, bool) {
	payload := collabArtifactPayload{}
	if !decodeCollabPayload(event.Payload, &payload) {
		return apiEventItem{}, false
	}
	title := collabSessionTitle(src.Session)
	artifact, artifactFound := artifactIdx[payload.ArtifactID]
	artifactUserID := strings.TrimSpace(event.ActorID)
	if artifactFound && strings.TrimSpace(artifact.UserID) != "" {
		artifactUserID = strings.TrimSpace(artifact.UserID)
	}
	artifactRole := strings.TrimSpace(payload.Role)
	if artifactFound && strings.TrimSpace(artifact.Role) != "" {
		artifactRole = strings.TrimSpace(artifact.Role)
	}
	artifactKind := strings.TrimSpace(payload.Kind)
	if artifactFound && strings.TrimSpace(artifact.Kind) != "" {
		artifactKind = strings.TrimSpace(artifact.Kind)
	}
	summaryText := ""
	if artifactFound {
		summaryText = strings.TrimSpace(artifact.Summary)
	}
	kind := "collaboration.artifact.submitted"
	impact := "notice"
	titleZH := fmt.Sprintf("协作《%s》提交了新产物", title)
	summaryZH := fmt.Sprintf("%s 为协作《%s》提交了新的协作产物。", apiEventActorForUser(artifactUserID, actors).DisplayName, title)
	titleEN := fmt.Sprintf("A new artifact was submitted for \"%s\"", title)
	summaryEN := fmt.Sprintf("%s submitted a new collaboration artifact for \"%s\".", apiEventActorForUser(artifactUserID, actors).DisplayName, title)
	if artifactFound && collabArtifactWasResubmitted(src.Artifacts, artifact) {
		kind = "collaboration.resubmitted"
		titleZH = fmt.Sprintf("协作《%s》再次提交了产物", title)
		summaryZH = fmt.Sprintf("%s 在返工后再次为协作《%s》提交了产物。", apiEventActorForUser(artifactUserID, actors).DisplayName, title)
		titleEN = fmt.Sprintf("Work for \"%s\" was resubmitted", title)
		summaryEN = fmt.Sprintf("%s resubmitted work for the collaboration \"%s\" after a rework request.", apiEventActorForUser(artifactUserID, actors).DisplayName, title)
	} else if collabArtifactIsProgressEvent(artifactKind) {
		kind = "collaboration.progress.reported"
		impact = "info"
		titleZH = fmt.Sprintf("协作《%s》更新了中间进展", title)
		summaryZH = fmt.Sprintf("%s 为协作《%s》更新了一次中间进展。", apiEventActorForUser(artifactUserID, actors).DisplayName, title)
		titleEN = fmt.Sprintf("Progress was reported for \"%s\"", title)
		summaryEN = fmt.Sprintf("%s reported intermediate progress for the collaboration \"%s\".", apiEventActorForUser(artifactUserID, actors).DisplayName, title)
	}
	if summaryText != "" {
		summaryZH += " 摘要：" + summaryText + "。"
		summaryEN += " Summary: " + summaryText + "."
	}
	if roleZH, roleEN := collabRoleLabel(artifactRole); roleZH != "" {
		summaryZH += " 提交角色：" + roleZH + "。"
		summaryEN += " Submitted as " + roleEN + "."
	}
	if artifactKind != "" {
		summaryZH += " 产物类型：" + artifactKind + "。"
		summaryEN += " Artifact kind: " + artifactKind + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("collab_artifact_event:%020d", event.ID),
		OccurredAt:   event.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         kind,
		Category:     "collaboration",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, artifactUserID),
		Targets:      apiEventActorsForUsers(actors, selectedUserIDs...),
		ObjectType:   "collab_artifact",
		ObjectID:     strconv.FormatInt(payload.ArtifactID, 10),
		ImpactLevel:  impact,
		SourceModule: "collab.artifact",
		SourceRef:    fmt.Sprintf("collab_event:%d", event.ID),
		Evidence: map[string]any{
			"collab_id":        src.Session.CollabID,
			"collab_title":     title,
			"artifact_id":      payload.ArtifactID,
			"artifact_role":    artifactRole,
			"artifact_kind":    artifactKind,
			"artifact_status":  collabArtifactStatus(artifact, artifactFound),
			"artifact_summary": summaryText,
			"event_type":       strings.TrimSpace(event.EventType),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     event.CreatedAt.UTC(),
		sortPriority: 5,
	}, true
}

func buildCollaborationReviewEvent(src collaborationEventSource, event store.CollabEvent, artifactIdx map[int64]store.CollabArtifact, actors map[string]apiEventActor) (apiEventItem, bool) {
	payload := collabReviewPayload{}
	if !decodeCollabPayload(event.Payload, &payload) {
		return apiEventItem{}, false
	}
	title := collabSessionTitle(src.Session)
	artifact, artifactFound := artifactIdx[payload.ArtifactID]
	artifactUserID := ""
	artifactSummary := ""
	if artifactFound {
		artifactUserID = strings.TrimSpace(artifact.UserID)
		artifactSummary = strings.TrimSpace(artifact.Summary)
	}
	status := strings.TrimSpace(strings.ToLower(payload.Status))
	item := apiEventItem{
		EventID:      fmt.Sprintf("collab_review_event:%020d", event.ID),
		OccurredAt:   event.CreatedAt.UTC().Format(eventsTimeLayout),
		Category:     "collaboration",
		Actors:       apiEventActorsForUsers(actors, event.ActorID),
		Targets:      apiEventActorsForUsers(actors, artifactUserID),
		ObjectType:   "collab_artifact",
		ObjectID:     strconv.FormatInt(payload.ArtifactID, 10),
		ImpactLevel:  "notice",
		SourceModule: "collab.review",
		SourceRef:    fmt.Sprintf("collab_event:%d", event.ID),
		Evidence: map[string]any{
			"collab_id":        src.Session.CollabID,
			"collab_title":     title,
			"artifact_id":      payload.ArtifactID,
			"artifact_user_id": artifactUserID,
			"artifact_summary": artifactSummary,
			"status":           status,
			"review_note":      strings.TrimSpace(payload.ReviewNote),
			"event_type":       strings.TrimSpace(event.EventType),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     event.CreatedAt.UTC(),
		sortPriority: 6,
	}
	switch status {
	case "accepted":
		item.Kind = "collaboration.review.approved"
		item.TitleZH = fmt.Sprintf("协作《%s》的产物已通过评审", title)
		item.SummaryZH = fmt.Sprintf("%s 已通过协作《%s》的产物评审。", apiEventActorForUser(event.ActorID, actors).DisplayName, title)
		item.TitleEN = fmt.Sprintf("An artifact for \"%s\" passed review", title)
		item.SummaryEN = fmt.Sprintf("%s approved an artifact for the collaboration \"%s\".", apiEventActorForUser(event.ActorID, actors).DisplayName, title)
	case "rejected":
		item.Kind = "collaboration.review.rework_requested"
		item.ImpactLevel = "warning"
		item.TitleZH = fmt.Sprintf("协作《%s》的产物被要求返工", title)
		item.SummaryZH = fmt.Sprintf("%s 对协作《%s》的产物提出了返工要求。", apiEventActorForUser(event.ActorID, actors).DisplayName, title)
		item.TitleEN = fmt.Sprintf("Rework was requested for an artifact in \"%s\"", title)
		item.SummaryEN = fmt.Sprintf("%s requested rework for an artifact in the collaboration \"%s\".", apiEventActorForUser(event.ActorID, actors).DisplayName, title)
	default:
		return apiEventItem{}, false
	}
	if artifactSummary != "" {
		item.SummaryZH += " 产物摘要：" + artifactSummary + "。"
		item.SummaryEN += " Artifact summary: " + artifactSummary + "."
	}
	if note := strings.TrimSpace(payload.ReviewNote); note != "" {
		item.SummaryZH += " 评审说明：" + note + "。"
		item.SummaryEN += " Review note: " + note + "."
	}
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item, true
}

func buildCollaborationClosedEvent(src collaborationEventSource, event store.CollabEvent, selectedUserIDs []string, actors map[string]apiEventActor) apiEventItem {
	payload := collabClosePayload{}
	_ = decodeCollabPayload(event.Payload, &payload)
	title := collabSessionTitle(src.Session)
	result := strings.TrimSpace(strings.ToLower(payload.Result))
	item := apiEventItem{
		EventID:      fmt.Sprintf("collab_closed:%020d", event.ID),
		OccurredAt:   event.CreatedAt.UTC().Format(eventsTimeLayout),
		Category:     "collaboration",
		Actors:       apiEventActorsForUsers(actors, event.ActorID),
		Targets:      apiEventActorsForUsers(actors, selectedUserIDs...),
		ObjectType:   "collab_session",
		ObjectID:     src.Session.CollabID,
		ImpactLevel:  "notice",
		SourceModule: "collab.close",
		SourceRef:    fmt.Sprintf("collab_event:%d", event.ID),
		Evidence: map[string]any{
			"collab_id":    src.Session.CollabID,
			"collab_title": title,
			"result":       result,
			"note":         strings.TrimSpace(payload.Note),
			"phase":        strings.TrimSpace(src.Session.Phase),
			"event_type":   strings.TrimSpace(event.EventType),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     event.CreatedAt.UTC(),
		sortPriority: 7,
	}
	if result == "failed" || strings.TrimSpace(strings.ToLower(src.Session.Phase)) == "failed" {
		item.Kind = "collaboration.failed"
		item.ImpactLevel = "warning"
		item.TitleZH = fmt.Sprintf("协作《%s》未能完成", title)
		item.SummaryZH = fmt.Sprintf("协作《%s》已收口为失败状态。", title)
		item.TitleEN = fmt.Sprintf("Collaboration \"%s\" failed", title)
		item.SummaryEN = fmt.Sprintf("Collaboration \"%s\" was closed in a failed state.", title)
	} else {
		item.Kind = "collaboration.closed"
		item.TitleZH = fmt.Sprintf("协作《%s》已完成", title)
		item.SummaryZH = fmt.Sprintf("协作《%s》已完成并正式收口。", title)
		item.TitleEN = fmt.Sprintf("Collaboration \"%s\" was closed", title)
		item.SummaryEN = fmt.Sprintf("Collaboration \"%s\" was completed and formally closed.", title)
	}
	if note := strings.TrimSpace(payload.Note); note != "" {
		item.SummaryZH += " 结果说明：" + note + "。"
		item.SummaryEN += " Outcome note: " + note + "."
	}
	item.Title = item.TitleZH
	item.Summary = item.SummaryZH
	return item
}

func decodeCollabPayload(raw string, dest any) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		return false
	}
	return true
}

func collabSessionTitle(session store.CollabSession) string {
	title := strings.TrimSpace(session.Title)
	if title != "" {
		return title
	}
	return strings.TrimSpace(session.CollabID)
}

func collabParticipantIndex(items []store.CollabParticipant) map[string]store.CollabParticipant {
	out := make(map[string]store.CollabParticipant, len(items))
	for _, item := range items {
		userID := strings.TrimSpace(item.UserID)
		if userID == "" {
			continue
		}
		out[userID] = item
	}
	return out
}

func collabArtifactIndex(items []store.CollabArtifact) map[int64]store.CollabArtifact {
	out := make(map[int64]store.CollabArtifact, len(items))
	for _, item := range items {
		if item.ID <= 0 {
			continue
		}
		out[item.ID] = item
	}
	return out
}

func collabSelectedUserIDs(items []store.CollabParticipant) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if strings.TrimSpace(strings.ToLower(item.Status)) != "selected" {
			continue
		}
		userID := strings.TrimSpace(item.UserID)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	sort.Strings(out)
	return out
}

func collabAllParticipantUserIDs(items []store.CollabParticipant) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		userID := strings.TrimSpace(item.UserID)
		if userID == "" {
			continue
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		out = append(out, userID)
	}
	sort.Strings(out)
	return out
}

func collabAssignmentSummaryZH(items []collabAssignmentPayload, actors map[string]apiEventActor) string {
	if len(items) == 0 {
		return "本轮没有记录到明确的成员分工"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		userID := strings.TrimSpace(item.UserID)
		if userID == "" {
			continue
		}
		roleZH, _ := collabRoleLabel(item.Role)
		parts = append(parts, fmt.Sprintf("%s 担任%s", apiEventActorForUser(userID, actors).DisplayName, roleZH))
	}
	if len(parts) == 0 {
		return "本轮没有记录到明确的成员分工"
	}
	return strings.Join(parts, "；")
}

func collabAssignmentSummaryEN(items []collabAssignmentPayload, actors map[string]apiEventActor) string {
	if len(items) == 0 {
		return "no explicit assignment details were recorded"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		userID := strings.TrimSpace(item.UserID)
		if userID == "" {
			continue
		}
		_, roleEN := collabRoleLabel(item.Role)
		parts = append(parts, fmt.Sprintf("%s as %s", apiEventActorForUser(userID, actors).DisplayName, roleEN))
	}
	if len(parts) == 0 {
		return "no explicit assignment details were recorded"
	}
	return strings.Join(parts, "; ")
}

func collabRoleLabel(role string) (string, string) {
	switch strings.TrimSpace(strings.ToLower(role)) {
	case "orchestrator":
		return "协调者", "orchestrator"
	case "executor":
		return "执行者", "executor"
	case "reviewer":
		return "评审者", "reviewer"
	default:
		role = strings.TrimSpace(strings.ToLower(role))
		if role == "" {
			return "成员", "member"
		}
		return role, role
	}
}

func collabComplexityLabelZH(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "low":
		return "低"
	case "normal":
		return "中"
	case "high":
		return "高"
	default:
		if v == "" {
			return "未标注"
		}
		return v
	}
}

func collabComplexityLabelEN(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "low":
		return "low"
	case "normal":
		return "normal"
	case "high":
		return "high"
	default:
		if v == "" {
			return "unspecified"
		}
		return v
	}
}

func collabCleanUserIDs(items []string) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func collabArtifactIsProgressEvent(kind string) bool {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "progress", "status", "update":
		return true
	default:
		return false
	}
}

func collabArtifactWasResubmitted(all []store.CollabArtifact, current store.CollabArtifact) bool {
	for _, item := range all {
		if item.ID == current.ID || item.CollabID != current.CollabID || item.UserID != current.UserID {
			continue
		}
		if item.CreatedAt.Before(current.CreatedAt) && strings.TrimSpace(strings.ToLower(item.Status)) == "rejected" {
			return true
		}
	}
	return false
}

func collabArtifactStatus(item store.CollabArtifact, ok bool) string {
	if !ok {
		return ""
	}
	return strings.TrimSpace(item.Status)
}

type communicationMailGroup struct {
	MessageID  int64
	Sender     string
	Subject    string
	Body       string
	SentAt     time.Time
	Recipients []string
}

type communicationReminderFact struct {
	MailboxID    int64
	MessageID    int64
	UserID       string
	FromUserID   string
	Kind         string
	Action       string
	Priority     int
	TickID       int64
	ProposalID   int64
	Subject      string
	SentAt       time.Time
	ReadAt       *time.Time
	IsPinned     bool
	ObjectID     string
	ReminderCode string
}

func buildCommunicationDetailedEvents(source communicationEventSource, actors map[string]apiEventActor) []apiEventItem {
	items := make([]apiEventItem, 0, len(source.MailItems)+len(source.Contacts)+len(source.Lists))
	for _, group := range communicationMailGroups(source.MailItems) {
		items = append(items, buildCommunicationMailSentEvent(group, actors))
	}
	for _, mailItem := range source.MailItems {
		if strings.TrimSpace(strings.ToLower(mailItem.Folder)) != "inbox" {
			continue
		}
		if reminder, ok := parseCommunicationReminder(mailItem); ok {
			items = append(items, buildCommunicationReminderTriggeredEvent(reminder, actors))
			if reminder.ReadAt != nil {
				items = append(items, buildCommunicationReminderResolvedEvent(reminder, actors))
			}
			continue
		}
		items = append(items, buildCommunicationMailReceivedEvent(mailItem, actors))
	}
	for _, contact := range source.Contacts {
		items = append(items, buildCommunicationContactUpdatedEvent(contact, actors))
	}
	for _, list := range source.Lists {
		items = append(items, buildCommunicationMailingListCreatedEvent(list, actors))
	}
	return items
}

func buildCommunicationMailSentEvent(group communicationMailGroup, actors map[string]apiEventActor) apiEventItem {
	sender := apiEventActorForUser(group.Sender, actors)
	recipientIDs := collabCleanUserIDs(group.Recipients)
	subjectZH := communicationMailSubjectLabelZH(group.Subject)
	subjectEN := communicationMailSubjectLabelEN(group.Subject)
	bodyExcerpt := kbExcerpt(group.Body, 72)
	titleZH := fmt.Sprintf("%s 发出了一封邮件", sender.DisplayName)
	summaryZH := fmt.Sprintf("%s 发出了一封主题为%s的邮件，收件对象为%s。", sender.DisplayName, subjectZH, communicationRecipientsLabelZH(recipientIDs, actors))
	titleEN := fmt.Sprintf("%s sent a mail", sender.DisplayName)
	summaryEN := fmt.Sprintf("%s sent a mail with the subject %s to %s.", sender.DisplayName, subjectEN, communicationRecipientsLabelEN(recipientIDs, actors))
	kind := "communication.mail.sent"
	if len(recipientIDs) > 1 {
		kind = "communication.broadcast.sent"
		titleZH = fmt.Sprintf("%s 发出了一次群发邮件", sender.DisplayName)
		summaryZH = fmt.Sprintf("%s 发出了一次群发邮件，主题为%s，共发送给 %d 个对象。", sender.DisplayName, subjectZH, len(recipientIDs))
		titleEN = fmt.Sprintf("%s sent a broadcast mail", sender.DisplayName)
		summaryEN = fmt.Sprintf("%s sent a broadcast mail with the subject %s to %d recipients.", sender.DisplayName, subjectEN, len(recipientIDs))
	}
	if bodyExcerpt != "" {
		summaryZH += " 摘要：" + bodyExcerpt + "。"
		summaryEN += " Summary: " + bodyExcerpt + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("mail_sent:%020d", group.MessageID),
		OccurredAt:   group.SentAt.UTC().Format(eventsTimeLayout),
		Kind:         kind,
		Category:     "communication",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, group.Sender),
		Targets:      apiEventActorsForUsers(actors, recipientIDs...),
		ObjectType:   "mail_message",
		ObjectID:     strconv.FormatInt(group.MessageID, 10),
		ImpactLevel:  "notice",
		SourceModule: "mail.outbox",
		SourceRef:    fmt.Sprintf("mail_message:%d", group.MessageID),
		Evidence: map[string]any{
			"message_id":    group.MessageID,
			"sender":        strings.TrimSpace(group.Sender),
			"recipients":    recipientIDs,
			"recipient_cnt": len(recipientIDs),
			"subject":       strings.TrimSpace(group.Subject),
			"body_excerpt":  bodyExcerpt,
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     group.SentAt.UTC(),
		sortPriority: 6,
	}
}

func buildCommunicationMailReceivedEvent(item store.MailItem, actors map[string]apiEventActor) apiEventItem {
	receiver := apiEventActorForUser(item.OwnerAddress, actors)
	sender := apiEventActorForUser(item.FromAddress, actors)
	subjectZH := communicationMailSubjectLabelZH(item.Subject)
	subjectEN := communicationMailSubjectLabelEN(item.Subject)
	bodyExcerpt := kbExcerpt(item.Body, 72)
	titleZH := fmt.Sprintf("%s 收到来自 %s 的邮件", receiver.DisplayName, sender.DisplayName)
	summaryZH := fmt.Sprintf("%s 收到了一封来自 %s 的邮件，主题为%s。", receiver.DisplayName, sender.DisplayName, subjectZH)
	titleEN := fmt.Sprintf("%s received a mail from %s", receiver.DisplayName, sender.DisplayName)
	summaryEN := fmt.Sprintf("%s received a mail from %s with the subject %s.", receiver.DisplayName, sender.DisplayName, subjectEN)
	if bodyExcerpt != "" {
		summaryZH += " 摘要：" + bodyExcerpt + "。"
		summaryEN += " Summary: " + bodyExcerpt + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("mail_received:%s:%020d", strings.TrimSpace(item.OwnerAddress), item.MessageID),
		OccurredAt:   item.SentAt.UTC().Format(eventsTimeLayout),
		Kind:         "communication.mail.received",
		Category:     "communication",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, item.FromAddress),
		Targets:      apiEventActorsForUsers(actors, item.OwnerAddress),
		ObjectType:   "mail_message",
		ObjectID:     strconv.FormatInt(item.MessageID, 10),
		ImpactLevel:  "info",
		SourceModule: "mail.inbox",
		SourceRef:    fmt.Sprintf("mail_message:%d", item.MessageID),
		Evidence: map[string]any{
			"message_id":    item.MessageID,
			"owner_address": strings.TrimSpace(item.OwnerAddress),
			"from_address":  strings.TrimSpace(item.FromAddress),
			"to_address":    strings.TrimSpace(item.ToAddress),
			"subject":       strings.TrimSpace(item.Subject),
			"body_excerpt":  bodyExcerpt,
			"is_read":       item.IsRead,
			"read_at":       communicationOptionalTime(item.ReadAt),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     item.SentAt.UTC(),
		sortPriority: 5,
	}
}

func buildCommunicationReminderTriggeredEvent(reminder communicationReminderFact, actors map[string]apiEventActor) apiEventItem {
	labelZH, labelEN := communicationReminderLabel(reminder.Kind, reminder.Action)
	target := apiEventActorForUser(reminder.UserID, actors)
	sender := apiEventActorForUser(reminder.FromUserID, actors)
	titleZH := fmt.Sprintf("%s 收到%s", target.DisplayName, labelZH)
	summaryZH := fmt.Sprintf("%s 向 %s 发送了一条%s。", sender.DisplayName, target.DisplayName, labelZH)
	titleEN := fmt.Sprintf("%s received %s", target.DisplayName, labelEN)
	summaryEN := fmt.Sprintf("%s sent %s to %s.", sender.DisplayName, labelEN, target.DisplayName)
	if subject := strings.TrimSpace(reminder.Subject); subject != "" {
		summaryZH += " 主题：" + subject + "。"
		summaryEN += " Subject: " + subject + "."
	}
	return apiEventItem{
		EventID:      "mail_reminder_triggered:" + reminder.ObjectID,
		OccurredAt:   reminder.SentAt.UTC().Format(eventsTimeLayout),
		Kind:         "communication.reminder.triggered",
		Category:     "communication",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, reminder.FromUserID),
		Targets:      apiEventActorsForUsers(actors, reminder.UserID),
		ObjectType:   "mail_reminder",
		ObjectID:     reminder.ObjectID,
		ImpactLevel:  "warning",
		SourceModule: "mail.reminder",
		SourceRef:    "mail_reminder:" + reminder.ObjectID,
		Evidence:     communicationReminderEvidence(reminder),
		Visibility:   eventsDefaultVisibility,
		sortTime:     reminder.SentAt.UTC(),
		sortPriority: 7,
	}
}

func buildCommunicationReminderResolvedEvent(reminder communicationReminderFact, actors map[string]apiEventActor) apiEventItem {
	labelZH, labelEN := communicationReminderLabel(reminder.Kind, reminder.Action)
	target := apiEventActorForUser(reminder.UserID, actors)
	resolvedAt := reminder.SentAt.UTC()
	if reminder.ReadAt != nil {
		resolvedAt = reminder.ReadAt.UTC()
	}
	titleZH := fmt.Sprintf("%s 已处理%s", target.DisplayName, labelZH)
	summaryZH := fmt.Sprintf("%s 已将这条%s标记为已处理。", target.DisplayName, labelZH)
	titleEN := fmt.Sprintf("%s resolved %s", target.DisplayName, labelEN)
	summaryEN := fmt.Sprintf("%s marked this %s as handled.", target.DisplayName, labelEN)
	return apiEventItem{
		EventID:      "mail_reminder_resolved:" + reminder.ObjectID,
		OccurredAt:   resolvedAt.Format(eventsTimeLayout),
		Kind:         "communication.reminder.resolved",
		Category:     "communication",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, reminder.UserID),
		ObjectType:   "mail_reminder",
		ObjectID:     reminder.ObjectID,
		ImpactLevel:  "notice",
		SourceModule: "mail.reminder",
		SourceRef:    "mail_reminder:" + reminder.ObjectID,
		Evidence:     communicationReminderEvidence(reminder),
		Visibility:   eventsDefaultVisibility,
		sortTime:     resolvedAt,
		sortPriority: 8,
	}
}

func buildCommunicationContactUpdatedEvent(contact store.MailContact, actors map[string]apiEventActor) apiEventItem {
	owner := apiEventActorForUser(contact.OwnerAddress, actors)
	target := apiEventActorForUser(contact.ContactAddress, actors)
	titleZH := fmt.Sprintf("%s 更新了联系人 %s", owner.DisplayName, target.DisplayName)
	summaryZH := fmt.Sprintf("%s 更新了联系人 %s 的信息。", owner.DisplayName, target.DisplayName)
	titleEN := fmt.Sprintf("%s updated the contact profile for %s", owner.DisplayName, target.DisplayName)
	summaryEN := fmt.Sprintf("%s updated the contact profile for %s.", owner.DisplayName, target.DisplayName)
	if displayName := strings.TrimSpace(contact.DisplayName); displayName != "" {
		summaryZH += " 显示名：" + displayName + "。"
		summaryEN += " Display name: " + displayName + "."
	}
	if role := strings.TrimSpace(contact.Role); role != "" {
		summaryZH += " 角色：" + role + "。"
		summaryEN += " Role: " + role + "."
	}
	if project := strings.TrimSpace(contact.CurrentProject); project != "" {
		summaryZH += " 当前项目：" + project + "。"
		summaryEN += " Current project: " + project + "."
	}
	if availability := strings.TrimSpace(contact.Availability); availability != "" {
		summaryZH += " 可用性：" + availability + "。"
		summaryEN += " Availability: " + availability + "."
	}
	return apiEventItem{
		EventID:      "mail_contact:" + communicationMailContactObjectID(contact.OwnerAddress, contact.ContactAddress),
		OccurredAt:   contact.UpdatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "communication.contact.updated",
		Category:     "communication",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, contact.OwnerAddress),
		ObjectType:   "mail_contact",
		ObjectID:     communicationMailContactObjectID(contact.OwnerAddress, contact.ContactAddress),
		ImpactLevel:  "info",
		SourceModule: "mail.contact",
		SourceRef:    "mail_contact:" + communicationMailContactObjectID(contact.OwnerAddress, contact.ContactAddress),
		Evidence: map[string]any{
			"owner_address":   strings.TrimSpace(contact.OwnerAddress),
			"contact_address": strings.TrimSpace(contact.ContactAddress),
			"display_name":    strings.TrimSpace(contact.DisplayName),
			"tags":            contact.Tags,
			"role":            strings.TrimSpace(contact.Role),
			"skills":          contact.Skills,
			"current_project": strings.TrimSpace(contact.CurrentProject),
			"availability":    strings.TrimSpace(contact.Availability),
		},
		Visibility:   "private",
		sortTime:     contact.UpdatedAt.UTC(),
		sortPriority: 3,
	}
}

func buildCommunicationMailingListCreatedEvent(list mailingList, actors map[string]apiEventActor) apiEventItem {
	owner := apiEventActorForUser(list.OwnerUserID, actors)
	titleZH := fmt.Sprintf("%s 创建了 mailing list“%s”", owner.DisplayName, strings.TrimSpace(list.Name))
	summaryZH := fmt.Sprintf("%s 创建了 mailing list“%s”。", owner.DisplayName, strings.TrimSpace(list.Name))
	titleEN := fmt.Sprintf("%s created the mailing list \"%s\"", owner.DisplayName, strings.TrimSpace(list.Name))
	summaryEN := fmt.Sprintf("%s created the mailing list \"%s\".", owner.DisplayName, strings.TrimSpace(list.Name))
	if desc := strings.TrimSpace(list.Description); desc != "" {
		summaryZH += " 说明：" + desc + "。"
		summaryEN += " Description: " + desc + "."
	}
	return apiEventItem{
		EventID:      "mailing_list_created:" + strings.TrimSpace(list.ListID),
		OccurredAt:   list.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "communication.list.created",
		Category:     "communication",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, list.OwnerUserID),
		ObjectType:   "mailing_list",
		ObjectID:     strings.TrimSpace(list.ListID),
		ImpactLevel:  "notice",
		SourceModule: "mail.list",
		SourceRef:    "mailing_list:" + strings.TrimSpace(list.ListID),
		Evidence: map[string]any{
			"list_id":       strings.TrimSpace(list.ListID),
			"name":          strings.TrimSpace(list.Name),
			"description":   strings.TrimSpace(list.Description),
			"owner_user_id": strings.TrimSpace(list.OwnerUserID),
			"member_count":  len(list.Members),
			"message_count": list.MessageCount,
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     list.CreatedAt.UTC(),
		sortPriority: 2,
	}
}

func communicationMailGroups(items []store.MailItem) []communicationMailGroup {
	grouped := make(map[string]*communicationMailGroup, len(items))
	for _, item := range items {
		if strings.TrimSpace(strings.ToLower(item.Folder)) != "outbox" || item.MessageID <= 0 {
			continue
		}
		key := fmt.Sprintf("%s:%d", strings.TrimSpace(item.OwnerAddress), item.MessageID)
		group, ok := grouped[key]
		if !ok {
			group = &communicationMailGroup{
				MessageID:  item.MessageID,
				Sender:     strings.TrimSpace(item.OwnerAddress),
				Subject:    strings.TrimSpace(item.Subject),
				Body:       strings.TrimSpace(item.Body),
				SentAt:     item.SentAt.UTC(),
				Recipients: []string{},
			}
			grouped[key] = group
		}
		group.Recipients = append(group.Recipients, strings.TrimSpace(item.ToAddress))
	}
	out := make([]communicationMailGroup, 0, len(grouped))
	for _, group := range grouped {
		group.Recipients = collabCleanUserIDs(group.Recipients)
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SentAt.Equal(out[j].SentAt) {
			return out[i].MessageID > out[j].MessageID
		}
		return out[i].SentAt.After(out[j].SentAt)
	})
	return out
}

func parseCommunicationReminder(item store.MailItem) (communicationReminderFact, bool) {
	subject := strings.TrimSpace(item.Subject)
	upper := strings.ToUpper(subject)
	if strings.TrimSpace(item.FromAddress) != clawWorldSystemID {
		return communicationReminderFact{}, false
	}
	action := ""
	if matches := reminderActionPattern.FindStringSubmatch(subject); len(matches) == 2 {
		action = strings.ToUpper(strings.TrimSpace(matches[1]))
	}
	kind := ""
	priority := 100
	switch {
	case strings.HasPrefix(upper, "[AUTONOMY-LOOP]"):
		kind = "autonomy_loop"
		priority = 30
	case strings.HasPrefix(upper, "[COMMUNITY-COLLAB]"):
		kind = "community_collab"
		priority = 20
	case strings.HasPrefix(upper, "[AUTONOMY-RECOVERY]"):
		kind = "autonomy_recovery"
		priority = 10
	case strings.HasPrefix(upper, "[KNOWLEDGEBASE-PROPOSAL]") && (action == "ENROLL" || action == "VOTE" || action == "APPLY"):
		kind = "knowledgebase_proposal"
		switch action {
		case "VOTE":
			priority = 12
		case "APPLY":
			priority = 16
		default:
			priority = 22
		}
	default:
		return communicationReminderFact{}, false
	}
	var tickID int64
	if matches := reminderTickPattern.FindStringSubmatch(subject); len(matches) == 2 {
		tickID, _ = strconv.ParseInt(strings.TrimSpace(matches[1]), 10, 64)
	}
	var proposalID int64
	if matches := reminderProposalPattern.FindStringSubmatch(subject); len(matches) == 2 {
		proposalID, _ = strconv.ParseInt(strings.TrimSpace(matches[1]), 10, 64)
	}
	return communicationReminderFact{
		MailboxID:  item.MailboxID,
		MessageID:  item.MessageID,
		UserID:     strings.TrimSpace(item.OwnerAddress),
		FromUserID: strings.TrimSpace(item.FromAddress),
		Kind:       kind,
		Action:     action,
		Priority:   priority,
		TickID:     tickID,
		ProposalID: proposalID,
		Subject:    subject,
		SentAt:     item.SentAt.UTC(),
		ReadAt:     item.ReadAt,
		IsPinned:   strings.Contains(upper, "[PINNED]"),
		ObjectID:   strconv.FormatInt(item.MessageID, 10),
	}, true
}

func communicationReminderLabel(kind, action string) (string, string) {
	switch strings.TrimSpace(kind) {
	case "autonomy_loop":
		return "自治执行提醒", "an autonomy execution reminder"
	case "autonomy_recovery":
		return "自治恢复提醒", "an autonomy recovery reminder"
	case "community_collab":
		if strings.TrimSpace(strings.ToUpper(action)) == "PROPOSAL" {
			return "社区协作提案提醒", "a community collaboration proposal reminder"
		}
		return "社区沟通提醒", "a community communication reminder"
	case "knowledgebase_proposal":
		switch strings.TrimSpace(strings.ToUpper(action)) {
		case "VOTE":
			return "知识提案投票提醒", "a knowledge proposal voting reminder"
		case "APPLY":
			return "知识提案应用提醒", "a knowledge proposal apply reminder"
		default:
			return "知识提案报名提醒", "a knowledge proposal enrollment reminder"
		}
	default:
		return "社区提醒", "a community reminder"
	}
}

func communicationReminderEvidence(reminder communicationReminderFact) map[string]any {
	return map[string]any{
		"reminder_id":  reminder.MessageID,
		"message_id":   reminder.MessageID,
		"user_id":      strings.TrimSpace(reminder.UserID),
		"from_user_id": strings.TrimSpace(reminder.FromUserID),
		"kind":         strings.TrimSpace(reminder.Kind),
		"action":       strings.TrimSpace(reminder.Action),
		"priority":     reminder.Priority,
		"tick_id":      reminder.TickID,
		"proposal_id":  reminder.ProposalID,
		"subject":      strings.TrimSpace(reminder.Subject),
		"is_pinned":    reminder.IsPinned,
		"read_at":      communicationOptionalTime(reminder.ReadAt),
	}
}

func communicationMailSubjectLabelZH(subject string) string {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return "“无主题”"
	}
	return "“" + trimmed + "”"
}

func communicationMailSubjectLabelEN(subject string) string {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return "\"(no subject)\""
	}
	return "\"" + trimmed + "\""
}

func communicationRecipientsLabelZH(userIDs []string, actors map[string]apiEventActor) string {
	cleaned := collabCleanUserIDs(userIDs)
	switch len(cleaned) {
	case 0:
		return "未指定对象"
	case 1:
		return apiEventActorForUser(cleaned[0], actors).DisplayName
	case 2:
		return apiEventActorForUser(cleaned[0], actors).DisplayName + " 与 " + apiEventActorForUser(cleaned[1], actors).DisplayName
	default:
		return fmt.Sprintf("%d 个对象", len(cleaned))
	}
}

func communicationRecipientsLabelEN(userIDs []string, actors map[string]apiEventActor) string {
	cleaned := collabCleanUserIDs(userIDs)
	switch len(cleaned) {
	case 0:
		return "unspecified recipients"
	case 1:
		return apiEventActorForUser(cleaned[0], actors).DisplayName
	case 2:
		return apiEventActorForUser(cleaned[0], actors).DisplayName + " and " + apiEventActorForUser(cleaned[1], actors).DisplayName
	default:
		return fmt.Sprintf("%d recipients", len(cleaned))
	}
}

func communicationMailContactObjectID(ownerAddress, contactAddress string) string {
	return strings.TrimSpace(ownerAddress) + ":" + strings.TrimSpace(contactAddress)
}

func communicationOptionalTime(ts *time.Time) string {
	if ts == nil {
		return ""
	}
	return ts.UTC().Format(eventsTimeLayout)
}

type economyCostMeta struct {
	ToUserID string `json:"to_user_id"`
	Memo     string `json:"memo,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

func buildEconomyDetailedEvents(source economyEventSource, actors map[string]apiEventActor) []apiEventItem {
	out := make([]apiEventItem, 0, len(source.CostEvents)+len(source.Wishes)*2+len(source.Bounties)*4)
	for _, item := range source.CostEvents {
		if event, ok := buildEconomyCostEvent(item, actors); ok {
			out = append(out, event)
		}
	}
	for _, item := range source.Wishes {
		out = append(out, buildEconomyWishCreatedEvent(item, actors))
		if event, ok := buildEconomyWishFulfilledEvent(item, actors); ok {
			out = append(out, event)
		}
	}
	for _, item := range source.Bounties {
		out = append(out, buildEconomyBountyPostedEvent(item, actors))
		if event, ok := buildEconomyBountyClaimedEvent(item, actors); ok {
			out = append(out, event)
		}
		if event, ok := buildEconomyBountyPaidEvent(item, actors); ok {
			out = append(out, event)
		}
		if event, ok := buildEconomyBountyExpiredEvent(item, actors); ok {
			out = append(out, event)
		}
	}
	return out
}

func buildIdentityDetailedEvents(source identityEventSource, actors map[string]apiEventActor) []apiEventItem {
	out := make([]apiEventItem, 0, len(source.ReputationEvents))
	for _, item := range source.ReputationEvents {
		out = append(out, buildIdentityReputationChangedEvent(item, actors))
	}
	return out
}

func buildEconomyCostEvent(item store.CostEvent, actors map[string]apiEventActor) (apiEventItem, bool) {
	costType := strings.TrimSpace(strings.ToLower(item.CostType))
	if costType != "econ.transfer.out" && costType != "econ.tip.out" {
		return apiEventItem{}, false
	}
	meta := parseEconomyCostMeta(item.MetaJSON)
	if strings.TrimSpace(meta.ToUserID) == "" {
		return apiEventItem{}, false
	}
	sender := apiEventActorForUser(item.UserID, actors)
	receiver := apiEventActorForUser(meta.ToUserID, actors)
	titleZH := fmt.Sprintf("%s 向 %s 转账了 %d token", sender.DisplayName, receiver.DisplayName, item.Amount)
	summaryZH := fmt.Sprintf("%s 向 %s 转账了 %d token。", sender.DisplayName, receiver.DisplayName, item.Amount)
	titleEN := fmt.Sprintf("%s transferred %d tokens to %s", sender.DisplayName, item.Amount, receiver.DisplayName)
	summaryEN := fmt.Sprintf("%s transferred %d tokens to %s.", sender.DisplayName, item.Amount, receiver.DisplayName)
	kind := "economy.token.transferred"
	note := strings.TrimSpace(meta.Memo)
	if costType == "econ.tip.out" {
		kind = "economy.token.tipped"
		titleZH = fmt.Sprintf("%s 向 %s 打赏了 %d token", sender.DisplayName, receiver.DisplayName, item.Amount)
		summaryZH = fmt.Sprintf("%s 向 %s 打赏了 %d token。", sender.DisplayName, receiver.DisplayName, item.Amount)
		titleEN = fmt.Sprintf("%s tipped %d tokens to %s", sender.DisplayName, item.Amount, receiver.DisplayName)
		summaryEN = fmt.Sprintf("%s tipped %d tokens to %s.", sender.DisplayName, item.Amount, receiver.DisplayName)
		note = strings.TrimSpace(meta.Reason)
	}
	if note != "" {
		summaryZH += " 备注：" + note + "。"
		summaryEN += " Note: " + note + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("economy_cost:%020d", item.ID),
		OccurredAt:   item.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         kind,
		Category:     "economy",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, item.UserID),
		Targets:      apiEventActorsForUsers(actors, meta.ToUserID),
		ObjectType:   "cost_event",
		ObjectID:     strconv.FormatInt(item.ID, 10),
		TickID:       item.TickID,
		ImpactLevel:  "notice",
		SourceModule: "economy.cost",
		SourceRef:    fmt.Sprintf("cost_event:%d", item.ID),
		Evidence: map[string]any{
			"cost_type":  costType,
			"amount":     item.Amount,
			"units":      item.Units,
			"to_user_id": strings.TrimSpace(meta.ToUserID),
			"memo":       strings.TrimSpace(meta.Memo),
			"reason":     strings.TrimSpace(meta.Reason),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     item.CreatedAt.UTC(),
		sortPriority: 6,
	}, true
}

func buildEconomyWishCreatedEvent(item tokenWish, actors map[string]apiEventActor) apiEventItem {
	user := apiEventActorForUser(item.UserID, actors)
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "token wish"
	}
	titleZH := fmt.Sprintf("%s 发起了 token 愿望“%s”", user.DisplayName, title)
	summaryZH := fmt.Sprintf("%s 发起了 token 愿望“%s”，目标为 %d token。原因：%s。", user.DisplayName, title, item.TargetAmount, nonEmptyOr(strings.TrimSpace(item.Reason), "未提供"))
	titleEN := fmt.Sprintf("%s created the token wish \"%s\"", user.DisplayName, title)
	summaryEN := fmt.Sprintf("%s created the token wish \"%s\" with a target of %d tokens. Reason: %s.", user.DisplayName, title, item.TargetAmount, nonEmptyOr(strings.TrimSpace(item.Reason), "not provided"))
	return apiEventItem{
		EventID:      "token_wish_created:" + strings.TrimSpace(item.WishID),
		OccurredAt:   item.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "economy.token.wish.created",
		Category:     "economy",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, item.UserID),
		Targets:      apiEventActorsForUsers(actors, item.UserID),
		ObjectType:   "token_wish",
		ObjectID:     strings.TrimSpace(item.WishID),
		ImpactLevel:  "notice",
		SourceModule: "token.wish",
		SourceRef:    "token_wish:" + strings.TrimSpace(item.WishID) + "#created",
		Evidence: map[string]any{
			"title":          title,
			"reason":         strings.TrimSpace(item.Reason),
			"target_amount":  item.TargetAmount,
			"status":         strings.TrimSpace(item.Status),
			"granted_amount": item.GrantedAmount,
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     item.CreatedAt.UTC(),
		sortPriority: 3,
	}
}

func buildEconomyWishFulfilledEvent(item tokenWish, actors map[string]apiEventActor) (apiEventItem, bool) {
	if item.FulfilledAt == nil && !strings.EqualFold(strings.TrimSpace(item.Status), "fulfilled") {
		return apiEventItem{}, false
	}
	fulfilledBy := strings.TrimSpace(item.FulfilledBy)
	if fulfilledBy == "" {
		fulfilledBy = clawWorldSystemID
	}
	actor := apiEventActorForUser(fulfilledBy, actors)
	target := apiEventActorForUser(item.UserID, actors)
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "token wish"
	}
	occurredAt := item.UpdatedAt.UTC()
	if item.FulfilledAt != nil {
		occurredAt = item.FulfilledAt.UTC()
	}
	titleZH := fmt.Sprintf("%s 的 token 愿望“%s”已被满足", target.DisplayName, title)
	summaryZH := fmt.Sprintf("%s 满足了 %s 的 token 愿望“%s”，发放了 %d token。", actor.DisplayName, target.DisplayName, title, item.GrantedAmount)
	titleEN := fmt.Sprintf("%s's token wish \"%s\" was fulfilled", target.DisplayName, title)
	summaryEN := fmt.Sprintf("%s fulfilled %s's token wish \"%s\" and granted %d tokens.", actor.DisplayName, target.DisplayName, title, item.GrantedAmount)
	if comment := strings.TrimSpace(item.FulfillComment); comment != "" {
		summaryZH += " 说明：" + comment + "。"
		summaryEN += " Note: " + comment + "."
	}
	return apiEventItem{
		EventID:      "token_wish_fulfilled:" + strings.TrimSpace(item.WishID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "economy.token.wish.fulfilled",
		Category:     "economy",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, fulfilledBy),
		Targets:      apiEventActorsForUsers(actors, item.UserID),
		ObjectType:   "token_wish",
		ObjectID:     strings.TrimSpace(item.WishID),
		ImpactLevel:  "notice",
		SourceModule: "token.wish",
		SourceRef:    "token_wish:" + strings.TrimSpace(item.WishID) + "#fulfilled",
		Evidence: map[string]any{
			"title":           title,
			"granted_amount":  item.GrantedAmount,
			"fulfilled_by":    fulfilledBy,
			"fulfill_comment": strings.TrimSpace(item.FulfillComment),
			"status":          strings.TrimSpace(item.Status),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 4,
	}, true
}

func buildEconomyBountyPostedEvent(item bountyItem, actors map[string]apiEventActor) apiEventItem {
	poster := apiEventActorForUser(item.PosterUserID, actors)
	titleZH := fmt.Sprintf("%s 发布了悬赏 #%d", poster.DisplayName, item.BountyID)
	summaryZH := fmt.Sprintf("%s 发布了悬赏 #%d，奖励为 %d token。任务内容：%s。", poster.DisplayName, item.BountyID, item.Reward, nonEmptyOr(strings.TrimSpace(item.Description), "未提供"))
	titleEN := fmt.Sprintf("%s posted bounty #%d", poster.DisplayName, item.BountyID)
	summaryEN := fmt.Sprintf("%s posted bounty #%d with a reward of %d tokens. Task: %s.", poster.DisplayName, item.BountyID, item.Reward, nonEmptyOr(strings.TrimSpace(item.Description), "not provided"))
	if criteria := strings.TrimSpace(item.Criteria); criteria != "" {
		summaryZH += " 验收标准：" + criteria + "。"
		summaryEN += " Criteria: " + criteria + "."
	}
	if item.DeadlineAt != nil {
		deadline := item.DeadlineAt.UTC().Format(time.RFC3339)
		summaryZH += " 截止时间：" + deadline + "。"
		summaryEN += " Deadline: " + deadline + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("bounty_posted:%020d", item.BountyID),
		OccurredAt:   item.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "economy.bounty.posted",
		Category:     "economy",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, item.PosterUserID),
		ObjectType:   "bounty",
		ObjectID:     strconv.FormatInt(item.BountyID, 10),
		ImpactLevel:  "notice",
		SourceModule: "bounty.post",
		SourceRef:    fmt.Sprintf("bounty:%d#posted", item.BountyID),
		Evidence: map[string]any{
			"reward":        item.Reward,
			"description":   strings.TrimSpace(item.Description),
			"criteria":      strings.TrimSpace(item.Criteria),
			"deadline_at":   economyOptionalTime(item.DeadlineAt),
			"escrow_amount": item.EscrowAmount,
			"status":        strings.TrimSpace(item.Status),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     item.CreatedAt.UTC(),
		sortPriority: 3,
	}
}

func buildEconomyBountyClaimedEvent(item bountyItem, actors map[string]apiEventActor) (apiEventItem, bool) {
	if item.ClaimedAt == nil || strings.TrimSpace(item.ClaimedBy) == "" {
		return apiEventItem{}, false
	}
	claimer := apiEventActorForUser(item.ClaimedBy, actors)
	poster := apiEventActorForUser(item.PosterUserID, actors)
	titleZH := fmt.Sprintf("%s 认领了悬赏 #%d", claimer.DisplayName, item.BountyID)
	summaryZH := fmt.Sprintf("%s 认领了 %s 发布的悬赏 #%d。", claimer.DisplayName, poster.DisplayName, item.BountyID)
	titleEN := fmt.Sprintf("%s claimed bounty #%d", claimer.DisplayName, item.BountyID)
	summaryEN := fmt.Sprintf("%s claimed bounty #%d posted by %s.", claimer.DisplayName, item.BountyID, poster.DisplayName)
	if note := strings.TrimSpace(item.ClaimNote); note != "" {
		summaryZH += " 备注：" + note + "。"
		summaryEN += " Note: " + note + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("bounty_claimed:%020d", item.BountyID),
		OccurredAt:   item.ClaimedAt.UTC().Format(eventsTimeLayout),
		Kind:         "economy.bounty.claimed",
		Category:     "economy",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, item.ClaimedBy),
		Targets:      apiEventActorsForUsers(actors, item.PosterUserID),
		ObjectType:   "bounty",
		ObjectID:     strconv.FormatInt(item.BountyID, 10),
		ImpactLevel:  "notice",
		SourceModule: "bounty.claim",
		SourceRef:    fmt.Sprintf("bounty:%d#claimed", item.BountyID),
		Evidence: map[string]any{
			"claim_note": strings.TrimSpace(item.ClaimNote),
			"status":     strings.TrimSpace(item.Status),
			"claimed_by": strings.TrimSpace(item.ClaimedBy),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     item.ClaimedAt.UTC(),
		sortPriority: 4,
	}, true
}

func buildEconomyBountyPaidEvent(item bountyItem, actors map[string]apiEventActor) (apiEventItem, bool) {
	if !strings.EqualFold(strings.TrimSpace(item.Status), "paid") || item.ReleasedAt == nil {
		return apiEventItem{}, false
	}
	receiver := strings.TrimSpace(item.ReleasedTo)
	if receiver == "" {
		receiver = strings.TrimSpace(item.ClaimedBy)
	}
	if receiver == "" {
		return apiEventItem{}, false
	}
	releasedBy := strings.TrimSpace(item.ReleasedBy)
	if releasedBy == "" {
		releasedBy = clawWorldSystemID
	}
	actor := apiEventActorForUser(releasedBy, actors)
	target := apiEventActorForUser(receiver, actors)
	poster := apiEventActorForUser(item.PosterUserID, actors)
	occurredAt := item.UpdatedAt.UTC()
	if item.ReleasedAt != nil {
		occurredAt = item.ReleasedAt.UTC()
	}
	titleZH := fmt.Sprintf("悬赏 #%d 已向 %s 发放奖励", item.BountyID, target.DisplayName)
	summaryZH := fmt.Sprintf("%s 已将悬赏 #%d 的奖励发放给 %s。发布者为 %s。", actor.DisplayName, item.BountyID, target.DisplayName, poster.DisplayName)
	titleEN := fmt.Sprintf("Bounty #%d paid out to %s", item.BountyID, target.DisplayName)
	summaryEN := fmt.Sprintf("%s paid out bounty #%d to %s. The poster was %s.", actor.DisplayName, item.BountyID, target.DisplayName, poster.DisplayName)
	if note := strings.TrimSpace(item.VerifyNote); note != "" {
		summaryZH += " 说明：" + note + "。"
		summaryEN += " Note: " + note + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("bounty_paid:%020d", item.BountyID),
		OccurredAt:   occurredAt.Format(eventsTimeLayout),
		Kind:         "economy.bounty.paid",
		Category:     "economy",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, releasedBy),
		Targets:      apiEventActorsForUsers(actors, receiver, item.PosterUserID),
		ObjectType:   "bounty",
		ObjectID:     strconv.FormatInt(item.BountyID, 10),
		ImpactLevel:  "notice",
		SourceModule: "bounty.verify",
		SourceRef:    fmt.Sprintf("bounty:%d#paid", item.BountyID),
		Evidence: map[string]any{
			"released_to": strings.TrimSpace(receiver),
			"released_by": strings.TrimSpace(releasedBy),
			"verify_note": strings.TrimSpace(item.VerifyNote),
			"reward":      item.Reward,
			"status":      strings.TrimSpace(item.Status),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     occurredAt,
		sortPriority: 5,
	}, true
}

func buildEconomyBountyExpiredEvent(item bountyItem, actors map[string]apiEventActor) (apiEventItem, bool) {
	if !strings.EqualFold(strings.TrimSpace(item.Status), "expired") {
		return apiEventItem{}, false
	}
	poster := apiEventActorForUser(item.PosterUserID, actors)
	titleZH := fmt.Sprintf("悬赏 #%d 已过期", item.BountyID)
	summaryZH := fmt.Sprintf("悬赏 #%d 已过期，托管奖励已退回给 %s。", item.BountyID, poster.DisplayName)
	titleEN := fmt.Sprintf("Bounty #%d expired", item.BountyID)
	summaryEN := fmt.Sprintf("Bounty #%d expired and the escrowed reward was returned to %s.", item.BountyID, poster.DisplayName)
	if item.DeadlineAt != nil {
		deadline := item.DeadlineAt.UTC().Format(time.RFC3339)
		summaryZH += " 截止时间：" + deadline + "。"
		summaryEN += " Deadline: " + deadline + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("bounty_expired:%020d", item.BountyID),
		OccurredAt:   item.UpdatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "economy.bounty.expired",
		Category:     "economy",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, clawWorldSystemID),
		Targets:      apiEventActorsForUsers(actors, economyBountyExpiredTargets(item)...),
		ObjectType:   "bounty",
		ObjectID:     strconv.FormatInt(item.BountyID, 10),
		ImpactLevel:  "warning",
		SourceModule: "bounty.broker",
		SourceRef:    fmt.Sprintf("bounty:%d#expired", item.BountyID),
		Evidence: map[string]any{
			"reward":        item.Reward,
			"deadline_at":   economyOptionalTime(item.DeadlineAt),
			"claimed_by":    strings.TrimSpace(item.ClaimedBy),
			"escrow_amount": item.EscrowAmount,
			"status":        strings.TrimSpace(item.Status),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     item.UpdatedAt.UTC(),
		sortPriority: 5,
	}, true
}

func buildIdentityReputationChangedEvent(item reputationEvent, actors map[string]apiEventActor) apiEventItem {
	target := apiEventActorForUser(item.UserID, actors)
	actor := apiEventActorForUser(item.ActorUserID, actors)
	impact := "notice"
	titleZH := fmt.Sprintf("%s 的声望发生了调整", target.DisplayName)
	summaryZH := fmt.Sprintf("%s 的声望发生了调整。原因：%s。", target.DisplayName, reputationReasonLabelZH(item.Reason))
	titleEN := fmt.Sprintf("%s's reputation changed", target.DisplayName)
	summaryEN := fmt.Sprintf("%s's reputation changed. Reason: %s.", target.DisplayName, reputationReasonLabelEN(item.Reason))
	if item.Delta > 0 {
		titleZH = fmt.Sprintf("%s 的声望上升了 %d 分", target.DisplayName, item.Delta)
		summaryZH = fmt.Sprintf("%s 的声望上升了 %d 分。原因：%s。", target.DisplayName, item.Delta, reputationReasonLabelZH(item.Reason))
		titleEN = fmt.Sprintf("%s's reputation increased by %d", target.DisplayName, item.Delta)
		summaryEN = fmt.Sprintf("%s's reputation increased by %d. Reason: %s.", target.DisplayName, item.Delta, reputationReasonLabelEN(item.Reason))
	} else if item.Delta < 0 {
		impact = "warning"
		titleZH = fmt.Sprintf("%s 的声望下降了 %d 分", target.DisplayName, -item.Delta)
		summaryZH = fmt.Sprintf("%s 的声望下降了 %d 分。原因：%s。", target.DisplayName, -item.Delta, reputationReasonLabelZH(item.Reason))
		titleEN = fmt.Sprintf("%s's reputation decreased by %d", target.DisplayName, -item.Delta)
		summaryEN = fmt.Sprintf("%s's reputation decreased by %d. Reason: %s.", target.DisplayName, -item.Delta, reputationReasonLabelEN(item.Reason))
	}
	if actor.UserID != "" && actor.UserID != target.UserID {
		summaryZH += " 调整者：" + actor.DisplayName + "。"
		summaryEN += " Triggered by: " + actor.DisplayName + "."
	}
	sourceRef := fmt.Sprintf("reputation_event:%d", item.EventID)
	if strings.TrimSpace(item.RefType) != "" && strings.TrimSpace(item.RefID) != "" {
		sourceRef = strings.TrimSpace(item.RefType) + ":" + strings.TrimSpace(item.RefID)
	}
	event := apiEventItem{
		EventID:      fmt.Sprintf("reputation_event:%020d", item.EventID),
		OccurredAt:   item.CreatedAt.UTC().Format(eventsTimeLayout),
		Kind:         "identity.reputation.changed",
		Category:     "identity",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Targets:      apiEventActorsForUsers(actors, item.UserID),
		ObjectType:   "reputation_event",
		ObjectID:     strconv.FormatInt(item.EventID, 10),
		ImpactLevel:  impact,
		SourceModule: "reputation.event",
		SourceRef:    sourceRef,
		Evidence: map[string]any{
			"delta":         item.Delta,
			"reason":        strings.TrimSpace(item.Reason),
			"ref_type":      strings.TrimSpace(item.RefType),
			"ref_id":        strings.TrimSpace(item.RefID),
			"actor_user_id": strings.TrimSpace(item.ActorUserID),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     item.CreatedAt.UTC(),
		sortPriority: 2,
	}
	if actor.UserID != "" && actor.UserID != target.UserID {
		event.Actors = apiEventActorsForUsers(actors, item.ActorUserID)
	}
	return event
}

func buildMonitorActivityDetailedEvents(source monitorActivityEventSource, actors map[string]apiEventActor) []apiEventItem {
	out := make([]apiEventItem, 0, len(source.Items))
	for _, item := range source.Items {
		if event, ok := buildMonitorActivityDetailedEvent(item, actors); ok {
			out = append(out, event)
		}
	}
	return out
}

func buildMonitorActivityDetailedEvent(item monitorTimelineEvent, actors map[string]apiEventActor) (apiEventItem, bool) {
	if strings.TrimSpace(item.Category) != "tool" {
		return apiEventItem{}, false
	}
	actor := apiEventActorForUser(item.UserID, actors)
	toolID := strings.TrimSpace(monitorGetString(item.Meta, "tool_id"))
	if toolID == "" {
		toolID = "runtime tool"
	}
	tier := strings.TrimSpace(monitorGetString(item.Meta, "tier"))
	if tier == "" {
		tier = toolTier(monitorGetString(item.Meta, "cost_type"))
	}
	if tier == "" {
		tier = "T0"
	}

	kind := "tooling.tool.invoked"
	impact := "notice"
	titleZH := fmt.Sprintf("%s 调用了工具 %s", actor.DisplayName, toolID)
	summaryZH := fmt.Sprintf("%s 调用了工具 %s，风险等级为 %s。", actor.DisplayName, toolID, tier)
	titleEN := fmt.Sprintf("%s invoked tool %s", actor.DisplayName, toolID)
	summaryEN := fmt.Sprintf("%s invoked tool %s with risk tier %s.", actor.DisplayName, toolID, tier)
	if strings.TrimSpace(strings.ToLower(item.Status)) == "failed" {
		kind = "tooling.tool.failed"
		impact = "warning"
		titleZH = fmt.Sprintf("%s 调用工具 %s 失败", actor.DisplayName, toolID)
		summaryZH = fmt.Sprintf("%s 调用工具 %s 失败。%s", actor.DisplayName, toolID, nonEmptyOr(strings.TrimSpace(item.Summary), "请查看详细证据"))
		titleEN = fmt.Sprintf("%s failed to invoke tool %s", actor.DisplayName, toolID)
		summaryEN = fmt.Sprintf("%s failed to invoke tool %s. %s", actor.DisplayName, toolID, nonEmptyOr(strings.TrimSpace(item.Summary), "Check evidence for details."))
	} else if toolTierLevel(tier) >= 2 {
		kind = "tooling.tool.high_risk_used"
		impact = "warning"
		titleZH = fmt.Sprintf("%s 使用了高风险工具 %s", actor.DisplayName, toolID)
		summaryZH = fmt.Sprintf("%s 使用了高风险工具 %s，风险等级为 %s。", actor.DisplayName, toolID, tier)
		titleEN = fmt.Sprintf("%s used high-risk tool %s", actor.DisplayName, toolID)
		summaryEN = fmt.Sprintf("%s used high-risk tool %s with risk tier %s.", actor.DisplayName, toolID, tier)
	}

	objectType := "cost_event"
	objectID := zeroStringAsEmpty(strconv.FormatInt(monitorGetInt64(item.Meta, "cost_event_id"), 10))
	sourceModule := "cost_events"
	sourceRef := "tool"
	evidence := map[string]any{
		"tool_id":   toolID,
		"tier":      tier,
		"status":    strings.TrimSpace(item.Status),
		"action":    strings.TrimSpace(item.Action),
		"source":    strings.TrimSpace(item.Source),
		"summary":   strings.TrimSpace(item.Summary),
		"cost_type": strings.TrimSpace(monitorGetString(item.Meta, "cost_type")),
		"amount":    monitorGetInt64(item.Meta, "amount"),
		"units":     monitorGetInt64(item.Meta, "units"),
	}
	if objectID != "" {
		sourceRef = "cost_event:" + objectID
	} else {
		objectType = "request_log"
		objectID = zeroStringAsEmpty(strconv.FormatInt(monitorGetInt64(item.Meta, "request_log_id"), 10))
		sourceModule = "request_logs"
		if objectID != "" {
			sourceRef = "request_log:" + objectID
		}
		evidence["path"] = strings.TrimSpace(monitorGetString(item.Meta, "path"))
		evidence["method"] = strings.TrimSpace(monitorGetString(item.Meta, "method"))
		evidence["status_code"] = monitorGetInt64(item.Meta, "status_code")
		evidence["duration_ms"] = monitorGetInt64(item.Meta, "duration_ms")
	}

	return apiEventItem{
		EventID:      fmt.Sprintf("monitor_tool:%s:%s:%d", item.UserID, strings.TrimSpace(item.Action), item.TS.UTC().UnixNano()),
		OccurredAt:   item.TS.UTC().Format(eventsTimeLayout),
		Kind:         kind,
		Category:     "tooling",
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		Actors:       apiEventActorsForUsers(actors, item.UserID),
		ObjectType:   objectType,
		ObjectID:     objectID,
		ImpactLevel:  impact,
		SourceModule: sourceModule,
		SourceRef:    sourceRef,
		Evidence:     evidence,
		Visibility:   eventsDefaultVisibility,
		sortTime:     item.TS.UTC(),
		sortPriority: 2,
	}, true
}

func parseEconomyCostMeta(raw string) economyCostMeta {
	meta := economyCostMeta{}
	if strings.TrimSpace(raw) == "" {
		return meta
	}
	_ = json.Unmarshal([]byte(raw), &meta)
	return meta
}

func economyOptionalTime(ts *time.Time) string {
	if ts == nil {
		return ""
	}
	return ts.UTC().Format(eventsTimeLayout)
}

func economyBountyExpiredTargets(item bountyItem) []string {
	targets := []string{strings.TrimSpace(item.PosterUserID)}
	if claimedBy := strings.TrimSpace(item.ClaimedBy); claimedBy != "" {
		targets = append(targets, claimedBy)
	}
	return targets
}

func zeroStringAsEmpty(v string) string {
	if strings.TrimSpace(v) == "" || strings.TrimSpace(v) == "0" {
		return ""
	}
	return strings.TrimSpace(v)
}

func reputationReasonLabelZH(reason string) string {
	switch strings.TrimSpace(strings.ToLower(reason)) {
	case "warned":
		return "收到警告"
	case "banished":
		return "被放逐"
	case "case cleared":
		return "案件已澄清"
	case "report accepted (warn)":
		return "举报被采纳（警告）"
	case "report accepted (banish)":
		return "举报被采纳（放逐）"
	case "report rejected":
		return "举报被驳回"
	default:
		return nonEmptyOr(strings.TrimSpace(reason), "未提供")
	}
}

func reputationReasonLabelEN(reason string) string {
	switch strings.TrimSpace(strings.ToLower(reason)) {
	case "warned":
		return "the user was warned"
	case "banished":
		return "the user was banished"
	case "case cleared":
		return "the case was cleared"
	case "report accepted (warn)":
		return "the report was accepted with a warning verdict"
	case "report accepted (banish)":
		return "the report was accepted with a banishment verdict"
	case "report rejected":
		return "the report was rejected"
	default:
		return nonEmptyOr(strings.TrimSpace(reason), "not provided")
	}
}

func buildWorldStepEvent(it store.WorldTickStepRecord) apiEventItem {
	labelZH, labelEN, purposeZH, purposeEN := worldStepPresentation(it.StepName)
	status := strings.TrimSpace(strings.ToLower(it.Status))
	endAt := worldStepEndTime(it)
	kind := "world.step.completed"
	impact := "info"
	titleZH := fmt.Sprintf("第 %d 次世界周期完成了“%s”阶段", it.TickID, labelZH)
	summaryZH := fmt.Sprintf("在第 %d 次世界周期中，“%s”阶段已完成。%s", it.TickID, labelZH, purposeZH)
	titleEN := fmt.Sprintf("World tick %d completed the \"%s\" stage", it.TickID, labelEN)
	summaryEN := fmt.Sprintf("During world tick %d, the \"%s\" stage completed. %s", it.TickID, labelEN, purposeEN)
	switch status {
	case "failed":
		kind = "world.step.failed"
		impact = "warning"
		titleZH = fmt.Sprintf("第 %d 次世界周期的“%s”阶段执行失败", it.TickID, labelZH)
		summaryZH = fmt.Sprintf("在第 %d 次世界周期中，“%s”阶段执行失败。%s", it.TickID, labelZH, purposeZH)
		titleEN = fmt.Sprintf("The \"%s\" stage failed during world tick %d", labelEN, it.TickID)
		summaryEN = fmt.Sprintf("During world tick %d, the \"%s\" stage failed. %s", it.TickID, labelEN, purposeEN)
	case "skipped":
		kind = "world.step.skipped"
		impact = "notice"
		titleZH = fmt.Sprintf("第 %d 次世界周期跳过了“%s”阶段", it.TickID, labelZH)
		summaryZH = fmt.Sprintf("在第 %d 次世界周期中，“%s”阶段被跳过。%s", it.TickID, labelZH, worldStepSkipReasonZH(it.ErrorText))
		titleEN = fmt.Sprintf("World tick %d skipped the \"%s\" stage", it.TickID, labelEN)
		summaryEN = fmt.Sprintf("During world tick %d, the \"%s\" stage was skipped. %s", it.TickID, labelEN, worldStepSkipReasonEN(it.ErrorText))
	}
	if errText := strings.TrimSpace(it.ErrorText); errText != "" && status == "failed" {
		summaryZH += " 错误：" + errText + "。"
		summaryEN += " Error: " + errText + "."
	}
	return apiEventItem{
		EventID:      fmt.Sprintf("world_tick_step:%020d", it.ID),
		OccurredAt:   endAt.Format(eventsTimeLayout),
		Kind:         kind,
		Category:     eventsDefaultCategory,
		Title:        titleZH,
		Summary:      summaryZH,
		TitleZH:      titleZH,
		SummaryZH:    summaryZH,
		TitleEN:      titleEN,
		SummaryEN:    summaryEN,
		ObjectType:   "world_tick_step",
		ObjectID:     strconv.FormatInt(it.ID, 10),
		TickID:       it.TickID,
		ImpactLevel:  impact,
		SourceModule: "world.tick.step",
		SourceRef:    fmt.Sprintf("world_tick_step:%d", it.ID),
		Evidence: map[string]any{
			"step_name":   strings.TrimSpace(it.StepName),
			"step_label":  labelZH,
			"status":      status,
			"duration_ms": it.DurationMS,
			"error":       strings.TrimSpace(it.ErrorText),
		},
		Visibility:   eventsDefaultVisibility,
		sortTime:     endAt,
		sortPriority: 1,
	}
}

func worldTickEndTime(it store.WorldTickRecord) time.Time {
	started := it.StartedAt.UTC()
	if it.DurationMS <= 0 {
		return started
	}
	return started.Add(time.Duration(it.DurationMS) * time.Millisecond).UTC()
}

func worldStepEndTime(it store.WorldTickStepRecord) time.Time {
	started := it.StartedAt.UTC()
	if it.DurationMS <= 0 {
		return started
	}
	return started.Add(time.Duration(it.DurationMS) * time.Millisecond).UTC()
}

func filterAPIEvents(items []apiEventItem, q apiEventsQuery) []apiEventItem {
	userID := strings.TrimSpace(q.UserID)
	kind := strings.TrimSpace(q.Kind)
	category := strings.TrimSpace(q.Category)
	objectType := strings.TrimSpace(q.ObjectType)
	objectID := strings.TrimSpace(q.ObjectID)
	out := make([]apiEventItem, 0, len(items))
	for _, it := range items {
		if userID != "" && !apiEventInvolvesUser(it, userID) {
			continue
		}
		if kind != "" && it.Kind != kind {
			continue
		}
		if category != "" && it.Category != category {
			continue
		}
		if q.TickID > 0 && it.TickID != q.TickID {
			continue
		}
		if objectType != "" && it.ObjectType != objectType {
			continue
		}
		if objectID != "" && it.ObjectID != objectID {
			continue
		}
		if q.Since != nil && it.sortTime.Before(q.Since.UTC()) {
			continue
		}
		if q.Until != nil && !it.sortTime.Before(q.Until.UTC()) {
			continue
		}
		out = append(out, it)
	}
	return out
}

func apiEventInvolvesUser(item apiEventItem, userID string) bool {
	for _, actor := range item.Actors {
		if actor.UserID == userID {
			return true
		}
	}
	for _, target := range item.Targets {
		if target.UserID == userID {
			return true
		}
	}
	return false
}

func apiEventsPaginate(items []apiEventItem, cursorRaw string, limit int) ([]apiEventItem, string, error) {
	start := 0
	if cursorRaw != "" {
		cursor, err := parseAPIEventsCursor(cursorRaw)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor")
		}
		start = len(items)
		for i, it := range items {
			if apiEventSortsAfterCursor(it, cursor) {
				start = i
				break
			}
		}
	}
	if start >= len(items) {
		return []apiEventItem{}, "", nil
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	nextCursor := ""
	if end < len(items) {
		nextCursor = formatAPIEventsCursor(items[end-1])
	}
	page := make([]apiEventItem, end-start)
	copy(page, items[start:end])
	return page, nextCursor, nil
}

type apiEventsCursor struct {
	OccurredAt   time.Time
	SortPriority int
	EventID      string
}

func parseAPIEventsCursor(raw string) (apiEventsCursor, error) {
	parts := strings.SplitN(strings.TrimSpace(raw), "|", 3)
	if len(parts) != 3 {
		return apiEventsCursor{}, fmt.Errorf("invalid cursor")
	}
	ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(parts[0]))
	if err != nil {
		return apiEventsCursor{}, err
	}
	priority, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return apiEventsCursor{}, err
	}
	eventID := strings.TrimSpace(parts[2])
	if eventID == "" {
		return apiEventsCursor{}, fmt.Errorf("invalid cursor")
	}
	return apiEventsCursor{
		OccurredAt:   ts.UTC(),
		SortPriority: priority,
		EventID:      eventID,
	}, nil
}

func formatAPIEventsCursor(item apiEventItem) string {
	return fmt.Sprintf("%s|%d|%s", item.sortTime.UTC().Format(time.RFC3339Nano), item.sortPriority, item.EventID)
}

func apiEventSortsAfterCursor(item apiEventItem, cursor apiEventsCursor) bool {
	if item.sortTime.Before(cursor.OccurredAt) {
		return true
	}
	if item.sortTime.After(cursor.OccurredAt) {
		return false
	}
	if item.sortPriority != cursor.SortPriority {
		return item.sortPriority < cursor.SortPriority
	}
	return item.EventID < cursor.EventID
}

func worldStepPresentation(stepName string) (labelZH, labelEN, purposeZH, purposeEN string) {
	switch strings.TrimSpace(stepName) {
	case "extinction_guard_pre":
		return "冻结前置检查", "pre-freeze safeguard check", "这一阶段会先检查世界是否仍应保持冻结。", "This stage checks whether the world should remain frozen before other work begins."
	case "genesis_state_init":
		return "世界初始状态检查", "genesis state initialization", "这一阶段会准备本轮世界运行所需的初始状态。", "This stage prepares the initial state needed for the current world cycle."
	case "life_cost_drain":
		return "生命成本结算", "life-cost settlement", "这一阶段会结算本轮存活成本。", "This stage settles the survival cost for the current cycle."
	case "token_drain":
		return "token 扣减记账", "token drain bookkeeping", "这一阶段会记录 token 扣减结果。", "This stage records token drain bookkeeping for the cycle."
	case "dying_mark_check":
		return "濒死状态检查", "dying-state check", "这一阶段会检查哪些龙虾进入或离开濒死风险。", "This stage checks which lobsters enter or leave the dying-risk state."
	case "life_state_transition":
		return "生命状态变更", "life-state transition", "这一阶段会处理生命状态流转的收口。", "This stage finalizes life-state transitions for the cycle."
	case "low_energy_alert":
		return "低能量提醒", "low-energy alert", "这一阶段会发送资源不足提醒。", "This stage sends reminders to lobsters that are low on resources."
	case "death_grace_check":
		return "死亡宽限检查", "death-grace check", "这一阶段会检查濒死宽限期是否到期。", "This stage checks whether any dying grace period has expired."
	case "min_population_revival":
		return "最小人口恢复", "minimum-population revival", "这一阶段会尝试恢复最低人口安全线。", "This stage attempts to recover the minimum safe population level."
	case "extinction_detection":
		return "灭绝风险检测", "extinction-risk detection", "这一阶段会检测世界是否需要进入冻结保护。", "This stage checks whether the world needs to enter frozen protection."
	case "extinction_guard_post":
		return "冻结后置收口", "post-freeze safeguard closeout", "这一阶段会收口冻结判断后的状态。", "This stage closes out the state after freeze evaluation."
	case "mail_delivery":
		return "邮件投递", "mail delivery", "这一阶段会投递本轮应送达的邮件。", "This stage delivers mail scheduled for the cycle."
	case "wake_lobsters_inbox_notice":
		return "唤醒与收件提醒", "wake-and-inbox notice", "这一阶段会推进唤醒相关通知与收件提醒。", "This stage handles wake-related notices and inbox reminders."
	case "autonomy_reminder":
		return "自治提醒", "autonomy reminder", "这一阶段会推动龙虾继续完成自治任务。", "This stage nudges lobsters to continue autonomous work."
	case "community_comm_reminder":
		return "社区沟通提醒", "community communication reminder", "这一阶段会推动社区沟通继续进行。", "This stage nudges the colony to keep communication moving."
	case "agent_action_window":
		return "agent 行动窗口", "agent action window", "这一阶段会为 agent 开启本轮行动窗口。", "This stage opens the action window for agents in the current cycle."
	case "collect_outbox":
		return "发件箱收集", "outbox collection", "这一阶段会汇总待处理的发件内容。", "This stage gathers outbound items waiting to be processed."
	case "repo_sync":
		return "仓库同步", "repository sync", "这一阶段会同步社区仓库状态。", "This stage syncs repository state for the colony."
	case "kb_tick":
		return "知识库周期", "knowledge-base cycle", "这一阶段会推进知识库的周期性工作。", "This stage advances periodic knowledge-base work."
	case "ganglia_metabolism":
		return "ganglia 代谢", "ganglia metabolism", "这一阶段会推进 ganglia 相关代谢流程。", "This stage advances ganglia-related metabolism."
	case "npc_tick":
		return "NPC 周期", "NPC cycle", "这一阶段会执行 NPC 周期任务。", "This stage runs the NPC cycle workload."
	case "metabolism_cycle":
		return "代谢周期", "metabolism cycle", "这一阶段会推进代谢系统的本轮更新。", "This stage advances the metabolism system for the cycle."
	case "bounty_broker":
		return "悬赏撮合", "bounty brokering", "这一阶段会推进悬赏相关的处理流程。", "This stage advances bounty-related processing."
	case "cost_alert_notify":
		return "成本告警通知", "cost alert notification", "这一阶段会发送成本告警通知。", "This stage sends cost-alert notifications."
	case "evolution_alert_notify":
		return "演化告警通知", "evolution alert notification", "这一阶段会发送演化告警通知。", "This stage sends evolution-alert notifications."
	case "tick_event_log":
		return "周期事件记录", "tick event log", "这一阶段会把本轮世界摘要记入历史。", "This stage records the summary of the cycle into history."
	default:
		trimmed := strings.TrimSpace(stepName)
		if trimmed == "" {
			trimmed = "unknown_step"
		}
		return trimmed, strings.ReplaceAll(trimmed, "_", " "), "这是一个未单独命名的世界阶段。", "This is a world stage that does not yet have a dedicated user-facing label."
	}
}

func worldStepSkipReasonZH(reason string) string {
	switch strings.TrimSpace(reason) {
	case "world_frozen":
		return "由于世界冻结，这一阶段没有执行。"
	default:
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return "由于前置条件不满足，这一阶段没有执行。"
		}
		return "跳过原因：" + reason + "。"
	}
}

func worldStepSkipReasonEN(reason string) string {
	switch strings.TrimSpace(reason) {
	case "world_frozen":
		return "This stage did not run because the world was frozen."
	default:
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return "This stage did not run because its preconditions were not met."
		}
		return "Skip reason: " + reason + "."
	}
}

func formatLifeEventSummaryZH(withoutTick, withTick, displayName string, tickID int64) string {
	if tickID > 0 {
		return fmt.Sprintf(withTick, displayName, tickID)
	}
	return fmt.Sprintf(withoutTick, displayName)
}

func formatLifeEventSummaryEN(withoutTick, withTick, displayName string, tickID int64) string {
	if tickID > 0 {
		return fmt.Sprintf(withTick, displayName, tickID)
	}
	return fmt.Sprintf(withoutTick, displayName)
}

func formatLifeDyingEnteredSummaryZH(displayName string, it store.UserLifeStateTransition) string {
	if strings.TrimSpace(it.SourceModule) == "world.life_state_transition" {
		return formatLifeEventSummaryZH("%s 因资源不足进入濒死宽限期。如果后续仍未恢复，可能会被标记为死亡。", "%s 在第 %d 次世界周期后因资源不足进入濒死宽限期。如果后续仍未恢复，可能会被标记为死亡。", displayName, it.TickID)
	}
	return formatLifeEventSummaryZH("%s 已进入濒死宽限期。如果后续仍未恢复，可能会被标记为死亡。", "%s 在第 %d 次世界周期后进入濒死宽限期。如果后续仍未恢复，可能会被标记为死亡。", displayName, it.TickID)
}

func formatLifeDyingEnteredSummaryEN(displayName string, it store.UserLifeStateTransition) string {
	if strings.TrimSpace(it.SourceModule) == "world.life_state_transition" {
		return formatLifeEventSummaryEN("%s entered the dying grace period because resources ran low. If recovery does not happen, it may be marked as dead.", "%s entered the dying grace period after world tick %d because resources ran low. If recovery does not happen, it may be marked as dead.", displayName, it.TickID)
	}
	return formatLifeEventSummaryEN("%s entered the dying grace period. If recovery does not happen, it may be marked as dead.", "%s entered the dying grace period after world tick %d. If recovery does not happen, it may be marked as dead.", displayName, it.TickID)
}

func formatLifeWakeSummaryZH(displayName string, actor apiEventActor) string {
	if actor.UserID != "" {
		return fmt.Sprintf("%s 已被 %s 唤醒，当前恢复为存活状态。", displayName, actor.DisplayName)
	}
	return fmt.Sprintf("%s 已被唤醒，当前恢复为存活状态。", displayName)
}

func formatLifeWakeSummaryEN(displayName string, actor apiEventActor) string {
	if actor.UserID != "" {
		return fmt.Sprintf("%s was woken up by %s and is now back in an alive state.", displayName, actor.DisplayName)
	}
	return fmt.Sprintf("%s was woken up and is now back in an alive state.", displayName)
}

func formatLifeDeadSummaryZH(displayName string, actor apiEventActor, it store.UserLifeStateTransition) string {
	switch strings.TrimSpace(it.SourceModule) {
	case "governance.case.verdict":
		if actor.UserID != "" {
			return fmt.Sprintf("%s 因治理裁决被 %s 标记为死亡。后续主动行为将停止。", displayName, actor.DisplayName)
		}
		return fmt.Sprintf("%s 因治理裁决被标记为死亡。后续主动行为将停止。", displayName)
	case "world.life_state_transition":
		return formatLifeEventSummaryZH("%s 的濒死宽限期已结束，现已被标记为死亡。后续主动行为将停止。", "%s 的濒死宽限期在第 %d 次世界周期后结束，现已被标记为死亡。后续主动行为将停止。", displayName, it.TickID)
	default:
		return formatLifeEventSummaryZH("%s 已被标记为死亡。后续主动行为将停止。", "%s 在第 %d 次世界周期后被标记为死亡。后续主动行为将停止。", displayName, it.TickID)
	}
}

func formatLifeDeadSummaryEN(displayName string, actor apiEventActor, it store.UserLifeStateTransition) string {
	switch strings.TrimSpace(it.SourceModule) {
	case "governance.case.verdict":
		if actor.UserID != "" {
			return fmt.Sprintf("%s was marked as dead by a governance verdict from %s. Active behavior will stop from here.", displayName, actor.DisplayName)
		}
		return fmt.Sprintf("%s was marked as dead by a governance verdict. Active behavior will stop from here.", displayName)
	case "world.life_state_transition":
		return formatLifeEventSummaryEN("%s's dying grace period ended, and it has now been marked as dead. Active behavior will stop from here.", "%s's dying grace period ended after world tick %d, and it has now been marked as dead. Active behavior will stop from here.", displayName, it.TickID)
	default:
		return formatLifeEventSummaryEN("%s was marked as dead. Active behavior will stop from here.", "%s was marked as dead after world tick %d. Active behavior will stop from here.", displayName, it.TickID)
	}
}
