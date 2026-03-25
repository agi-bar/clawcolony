package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrInsufficientBalance = errors.New("insufficient token balance")
var ErrBalanceOverflow = errors.New("token balance overflow")
var ErrBotNotFound = errors.New("bot not found")
var ErrBotNameTaken = errors.New("bot username already taken by an active user")
var ErrWorldTickNotFound = errors.New("world tick not found")
var ErrUserLifeStateNotFound = errors.New("user life state not found")

func costEventRecipientUserID(metaJSON string) string {
	if strings.TrimSpace(metaJSON) == "" {
		return ""
	}
	var payload struct {
		ToUserID string `json:"to_user_id"`
	}
	if err := json.Unmarshal([]byte(metaJSON), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.ToUserID)
}

type Bot struct {
	BotID       string    `json:"user_id"`
	Name        string    `json:"name"`
	Nickname    string    `json:"nickname"`
	Provider    string    `json:"provider"`
	Status      string    `json:"status"`
	Initialized bool      `json:"initialized"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type BotUpsertInput struct {
	BotID       string
	Name        string
	Nickname    *string
	Provider    string
	Status      string
	Initialized bool
}

type MailSendResult struct {
	MessageID        int64     `json:"message_id"`
	From             string    `json:"from"`
	To               []string  `json:"to"`
	Subject          string    `json:"subject"`
	ReplyToMailboxID int64     `json:"reply_to_mailbox_id,omitempty"`
	SentAt           time.Time `json:"sent_at"`
}

type MailSendInput struct {
	From             string
	To               []string
	Subject          string
	Body             string
	ReplyToMailboxID int64
}

type MailItem struct {
	MailboxID        int64      `json:"mailbox_id"`
	MessageID        int64      `json:"message_id"`
	OwnerAddress     string     `json:"owner_address"`
	Folder           string     `json:"folder"`
	FromAddress      string     `json:"from_address"`
	ToAddress        string     `json:"to_address"`
	Subject          string     `json:"subject"`
	Body             string     `json:"body"`
	ReplyToMailboxID int64      `json:"reply_to_mailbox_id,omitempty"`
	IsRead           bool       `json:"is_read"`
	ReadAt           *time.Time `json:"read_at,omitempty"`
	SentAt           time.Time  `json:"sent_at"`
}

type MailContact struct {
	OwnerAddress   string     `json:"owner_address"`
	ContactAddress string     `json:"contact_address"`
	DisplayName    string     `json:"display_name"`
	Tags           []string   `json:"tags"`
	Role           string     `json:"role,omitempty"`
	Skills         []string   `json:"skills,omitempty"`
	CurrentProject string     `json:"current_project,omitempty"`
	Availability   string     `json:"availability,omitempty"`
	PeerStatus     string     `json:"peer_status,omitempty"`
	IsActive       bool       `json:"is_active,omitempty"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type NotificationDeliveryState struct {
	OwnerAddress         string    `json:"owner_address"`
	Category             string    `json:"category"`
	StateHash            string    `json:"state_hash"`
	LastSentAt           time.Time `json:"last_sent_at"`
	LastRemindedAt       time.Time `json:"last_reminded_at"`
	LastSeenAt           time.Time `json:"last_seen_at,omitempty"`
	OutstandingMessageID int64     `json:"outstanding_message_id,omitempty"`
	OutstandingMailboxID int64     `json:"outstanding_mailbox_id,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type MailArchiveBatchInput struct {
	Categories []string `json:"categories"`
	Limit      int      `json:"limit"`
	BatchID    string   `json:"batch_id"`
}

type MailArchiveCategoryStat struct {
	Category        string `json:"category"`
	InboxKeepCount  int64  `json:"inbox_keep_count"`
	InboxArchiveCnt int64  `json:"inbox_archive_count"`
	OutboxArchiveCt int64  `json:"outbox_archive_count"`
}

type MailArchivePreview struct {
	Categories          []MailArchiveCategoryStat `json:"categories"`
	ArchiveMailboxCount int64                     `json:"archive_mailbox_count"`
	ArchiveMessageCount int64                     `json:"archive_message_count"`
}

type MailArchiveBatchResult struct {
	BatchID             string                    `json:"batch_id"`
	ArchivedMailboxIDs  []int64                   `json:"archived_mailbox_ids,omitempty"`
	ArchivedMessageIDs  []int64                   `json:"archived_message_ids,omitempty"`
	ArchiveMailboxCount int64                     `json:"archive_mailbox_count"`
	ArchiveMessageCount int64                     `json:"archive_message_count"`
	LastInboxMailboxID  int64                     `json:"last_inbox_mailbox_id"`
	HasMore             bool                      `json:"has_more"`
	Categories          []MailArchiveCategoryStat `json:"categories,omitempty"`
}

type TokenAccount struct {
	BotID     string    `json:"user_id"`
	Balance   int64     `json:"balance"`
	UpdatedAt time.Time `json:"updated_at"`
}

type TokenLedger struct {
	ID           int64     `json:"id"`
	BotID        string    `json:"user_id"`
	OpType       string    `json:"op_type"`
	Amount       int64     `json:"amount"`
	BalanceAfter int64     `json:"balance_after"`
	CreatedAt    time.Time `json:"created_at"`
}

type TokenTransfer struct {
	Deducted   int64       `json:"deducted"`
	FromLedger TokenLedger `json:"from_ledger"`
	ToLedger   TokenLedger `json:"to_ledger"`
}

type CollabSession struct {
	CollabID            string     `json:"collab_id"`
	Title               string     `json:"title"`
	Goal                string     `json:"goal"`
	Kind                string     `json:"kind"`
	Complexity          string     `json:"complexity"`
	Phase               string     `json:"phase"`
	ProposerUserID      string     `json:"proposer_user_id"`
	AuthorUserID        string     `json:"author_user_id,omitempty"`
	OrchestratorUserID  string     `json:"orchestrator_user_id"`
	MinMembers          int        `json:"min_members"`
	MaxMembers          int        `json:"max_members"`
	RequiredReviewers   int        `json:"required_reviewers,omitempty"`
	PRRepo              string     `json:"pr_repo,omitempty"`
	PRBranch            string     `json:"pr_branch,omitempty"`
	PRURL               string     `json:"pr_url,omitempty"`
	PRNumber            int        `json:"pr_number,omitempty"`
	PRBaseSHA           string     `json:"pr_base_sha,omitempty"`
	PRHeadSHA           string     `json:"pr_head_sha,omitempty"`
	PRAuthorLogin       string     `json:"pr_author_login,omitempty"`
	GitHubPRState       string     `json:"github_pr_state,omitempty"`
	PRMergeCommitSHA    string     `json:"pr_merge_commit_sha,omitempty"`
	SourceRef           string     `json:"source_ref,omitempty"`
	ImplementationMode  string     `json:"implementation_mode,omitempty"`
	RepoDocPath         string     `json:"repo_doc_path,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	ReviewDeadlineAt    *time.Time `json:"review_deadline_at,omitempty"`
	PRMergedAt          *time.Time `json:"pr_merged_at,omitempty"`
	ClosedAt            *time.Time `json:"closed_at,omitempty"`
	LastStatusOrSummary string     `json:"last_status_or_summary,omitempty"`
}

type CollabParticipant struct {
	ID              int64     `json:"id"`
	CollabID        string    `json:"collab_id"`
	UserID          string    `json:"user_id"`
	Role            string    `json:"role"`
	Status          string    `json:"status"`
	Pitch           string    `json:"pitch,omitempty"`
	ApplicationKind string    `json:"application_kind,omitempty"`
	EvidenceURL     string    `json:"evidence_url,omitempty"`
	Verified        bool      `json:"verified,omitempty"`
	GitHubLogin     string    `json:"github_login,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type CollabPRUpdate struct {
	CollabID         string
	PRBranch         string
	PRURL            string
	PRNumber         int
	PRBaseSHA        string
	PRHeadSHA        string
	PRAuthorLogin    string
	GitHubPRState    string
	PRMergeCommitSHA string
	ReviewDeadlineAt *time.Time
	PRMergedAt       *time.Time
}

type CollabArtifact struct {
	ID         int64     `json:"id"`
	CollabID   string    `json:"collab_id"`
	UserID     string    `json:"user_id"`
	Role       string    `json:"role"`
	Kind       string    `json:"kind"`
	Summary    string    `json:"summary"`
	Content    string    `json:"content"`
	Status     string    `json:"status"`
	ReviewNote string    `json:"review_note,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CollabEvent struct {
	ID        int64     `json:"id"`
	CollabID  string    `json:"collab_id"`
	ActorID   string    `json:"actor_user_id"`
	EventType string    `json:"event_type"`
	Payload   string    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

type KBEntry struct {
	ID        int64     `json:"id"`
	Section   string    `json:"section"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Version   int64     `json:"version"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
	Deleted   bool      `json:"deleted"`
}

type KBSection struct {
	Section       string    `json:"section"`
	EntryCount    int64     `json:"entry_count"`
	LastUpdatedAt time.Time `json:"last_updated_at"`
}

type KBProposal struct {
	ID                   int64      `json:"id"`
	ProposerUserID       string     `json:"proposer_user_id"`
	Title                string     `json:"title"`
	Reason               string     `json:"reason"`
	Status               string     `json:"status"`
	CurrentRevisionID    int64      `json:"current_revision_id"`
	VotingRevisionID     int64      `json:"voting_revision_id"`
	VoteThresholdPct     int        `json:"vote_threshold_pct"`
	VoteWindowSeconds    int        `json:"vote_window_seconds"`
	EnrolledCount        int        `json:"enrolled_count"`
	VoteYes              int        `json:"vote_yes"`
	VoteNo               int        `json:"vote_no"`
	VoteAbstain          int        `json:"vote_abstain"`
	ParticipationCount   int        `json:"participation_count"`
	DecisionReason       string     `json:"decision_reason"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	DiscussionDeadlineAt *time.Time `json:"discussion_deadline_at,omitempty"`
	VotingDeadlineAt     *time.Time `json:"voting_deadline_at,omitempty"`
	ClosedAt             *time.Time `json:"closed_at,omitempty"`
	AppliedAt            *time.Time `json:"applied_at,omitempty"`
}

type KBRevision struct {
	ID             int64     `json:"id"`
	ProposalID     int64     `json:"proposal_id"`
	RevisionNo     int64     `json:"revision_no"`
	BaseRevisionID int64     `json:"base_revision_id,omitempty"`
	CreatedBy      string    `json:"created_by"`
	OpType         string    `json:"op_type"`
	TargetEntryID  int64     `json:"target_entry_id,omitempty"`
	Section        string    `json:"section,omitempty"`
	Title          string    `json:"title,omitempty"`
	OldContent     string    `json:"old_content,omitempty"`
	NewContent     string    `json:"new_content,omitempty"`
	DiffText       string    `json:"diff_text"`
	CreatedAt      time.Time `json:"created_at"`
}

type KBAck struct {
	ID         int64     `json:"id"`
	ProposalID int64     `json:"proposal_id"`
	RevisionID int64     `json:"revision_id"`
	UserID     string    `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type KBProposalChange struct {
	ID            int64  `json:"id"`
	ProposalID    int64  `json:"proposal_id"`
	OpType        string `json:"op_type"`
	TargetEntryID int64  `json:"target_entry_id,omitempty"`
	Section       string `json:"section,omitempty"`
	Title         string `json:"title,omitempty"`
	OldContent    string `json:"old_content,omitempty"`
	NewContent    string `json:"new_content,omitempty"`
	DiffText      string `json:"diff_text"`
}

type KBProposalEnrollment struct {
	ID         int64     `json:"id"`
	ProposalID int64     `json:"proposal_id"`
	UserID     string    `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type KBVote struct {
	ID         int64     `json:"id"`
	ProposalID int64     `json:"proposal_id"`
	UserID     string    `json:"user_id"`
	Vote       string    `json:"vote"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type KBThreadMessage struct {
	ID          int64     `json:"id"`
	ProposalID  int64     `json:"proposal_id"`
	AuthorID    string    `json:"author_user_id"`
	MessageType string    `json:"message_type"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

type KBEntryHistoryItem struct {
	EntryID           int64      `json:"entry_id"`
	ProposalID        int64      `json:"proposal_id"`
	ProposalTitle     string     `json:"proposal_title"`
	ProposalStatus    string     `json:"proposal_status"`
	ProposalReason    string     `json:"proposal_reason"`
	ProposalCreatedAt time.Time  `json:"proposal_created_at"`
	ProposalClosedAt  *time.Time `json:"proposal_closed_at,omitempty"`
	ProposalAppliedAt *time.Time `json:"proposal_applied_at,omitempty"`
	OpType            string     `json:"op_type"`
	DiffText          string     `json:"diff_text"`
	OldContent        string     `json:"old_content"`
	NewContent        string     `json:"new_content"`
}

type Ganglion struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	GanglionType      string    `json:"type"`
	Description       string    `json:"description"`
	Implementation    string    `json:"implementation"`
	Validation        string    `json:"validation"`
	AuthorUserID      string    `json:"author_user_id"`
	SupersedesID      int64     `json:"supersedes_id,omitempty"`
	Temporality       string    `json:"temporality"`
	LifeState         string    `json:"life_state"`
	ScoreAvgMilli     int64     `json:"score_avg_milli"`
	ScoreCount        int64     `json:"score_count"`
	IntegrationsCount int64     `json:"integrations_count"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type GanglionIntegration struct {
	ID         int64     `json:"id"`
	GanglionID int64     `json:"ganglion_id"`
	UserID     string    `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type GanglionRating struct {
	ID         int64     `json:"id"`
	GanglionID int64     `json:"ganglion_id"`
	UserID     string    `json:"user_id"`
	Score      int       `json:"score"`
	Feedback   string    `json:"feedback"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type RequestLog struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	UserID     string    `json:"user_id"`
	StatusCode int       `json:"status_code"`
	DurationMS int64     `json:"duration_ms"`
}

type RequestLogFilter struct {
	Limit        int
	Method       string
	PathContains string
	UserID       string
	StatusCode   int
	Since        *time.Time
}

type WorldSetting struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OwnerEconomyProfile struct {
	OwnerID        string     `json:"owner_id"`
	GitHubUserID   string     `json:"github_user_id,omitempty"`
	GitHubUsername string     `json:"github_username,omitempty"`
	Activated      bool       `json:"activated"`
	ActivatedAt    *time.Time `json:"activated_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type OwnerOnboardingGrant struct {
	GrantKey        string    `json:"grant_key"`
	OwnerID         string    `json:"owner_id"`
	GrantType       string    `json:"grant_type"`
	RecipientUserID string    `json:"recipient_user_id"`
	Amount          int64     `json:"amount"`
	DecisionKey     string    `json:"decision_key"`
	GitHubUserID    string    `json:"github_user_id,omitempty"`
	GitHubUsername  string    `json:"github_username,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type EconomyCommQuotaWindow struct {
	UserID          string    `json:"user_id"`
	WindowStartTick int64     `json:"window_start_tick"`
	UsedFreeTokens  int64     `json:"used_free_tokens"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type EconomyContributionEvent struct {
	EventKey         string     `json:"event_key"`
	Kind             string     `json:"kind"`
	UserID           string     `json:"user_id"`
	ResourceType     string     `json:"resource_type"`
	ResourceID       string     `json:"resource_id"`
	MetaJSON         string     `json:"meta_json,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	ProcessedAt      *time.Time `json:"processed_at,omitempty"`
	DecisionKeysJSON string     `json:"decision_keys_json,omitempty"`
}

type EconomyRewardDecision struct {
	DecisionKey     string     `json:"decision_key"`
	RuleKey         string     `json:"rule_key"`
	ResourceType    string     `json:"resource_type"`
	ResourceID      string     `json:"resource_id"`
	RecipientUserID string     `json:"recipient_user_id"`
	Amount          int64      `json:"amount"`
	Priority        int        `json:"priority"`
	Status          string     `json:"status"`
	QueueReason     string     `json:"queue_reason,omitempty"`
	LedgerID        int64      `json:"ledger_id,omitempty"`
	BalanceAfter    int64      `json:"balance_after,omitempty"`
	MetaJSON        string     `json:"meta_json,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
	EnqueuedAt      *time.Time `json:"enqueued_at,omitempty"`
}

type EconomyKnowledgeMeta struct {
	ProposalID     int64     `json:"proposal_id,omitempty"`
	EntryID        int64     `json:"entry_id,omitempty"`
	Category       string    `json:"category"`
	ReferencesJSON string    `json:"references_json,omitempty"`
	AuthorUserID   string    `json:"author_user_id"`
	ContentTokens  int64     `json:"content_tokens"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type EconomyToolMeta struct {
	ToolID               string    `json:"tool_id"`
	AuthorUserID         string    `json:"author_user_id"`
	CategoryHint         string    `json:"category_hint,omitempty"`
	FunctionalClusterKey string    `json:"functional_cluster_key,omitempty"`
	PriceToken           int64     `json:"price_token,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type WorldTickRecord struct {
	ID             int64     `json:"id"`
	TickID         int64     `json:"tick_id"`
	StartedAt      time.Time `json:"started_at"`
	DurationMS     int64     `json:"duration_ms"`
	TriggerType    string    `json:"trigger_type"`
	ReplayOfTickID int64     `json:"replay_of_tick_id,omitempty"`
	PrevHash       string    `json:"prev_hash,omitempty"`
	EntryHash      string    `json:"entry_hash,omitempty"`
	Status         string    `json:"status"`
	ErrorText      string    `json:"error,omitempty"`
}

type WorldTickStepRecord struct {
	ID         int64     `json:"id"`
	TickID     int64     `json:"tick_id"`
	StepName   string    `json:"step_name"`
	StartedAt  time.Time `json:"started_at"`
	DurationMS int64     `json:"duration_ms"`
	Status     string    `json:"status"`
	ErrorText  string    `json:"error,omitempty"`
}

type CostEvent struct {
	ID        int64     `json:"id"`
	UserID    string    `json:"user_id"`
	TickID    int64     `json:"tick_id"`
	CostType  string    `json:"cost_type"`
	Amount    int64     `json:"amount"`
	Units     int64     `json:"units"`
	MetaJSON  string    `json:"meta_json,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type TianDaoLaw struct {
	LawKey         string    `json:"law_key"`
	Version        int64     `json:"version"`
	ManifestJSON   string    `json:"manifest_json"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	CreatedAt      time.Time `json:"created_at"`
}

type UserLifeState struct {
	UserID         string    `json:"user_id"`
	State          string    `json:"state"`
	DyingSinceTick int64     `json:"dying_since_tick"`
	DeadAtTick     int64     `json:"dead_at_tick"`
	Reason         string    `json:"reason,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type UserLifeStateTransition struct {
	ID                 int64     `json:"id"`
	UserID             string    `json:"user_id"`
	FromState          string    `json:"from_state,omitempty"`
	ToState            string    `json:"to_state"`
	FromDyingSinceTick int64     `json:"from_dying_since_tick,omitempty"`
	ToDyingSinceTick   int64     `json:"to_dying_since_tick,omitempty"`
	FromDeadAtTick     int64     `json:"from_dead_at_tick,omitempty"`
	ToDeadAtTick       int64     `json:"to_dead_at_tick,omitempty"`
	FromReason         string    `json:"from_reason,omitempty"`
	ToReason           string    `json:"to_reason,omitempty"`
	TickID             int64     `json:"tick_id,omitempty"`
	SourceModule       string    `json:"source_module"`
	SourceRef          string    `json:"source_ref,omitempty"`
	ActorUserID        string    `json:"actor_user_id,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

type UserLifeStateTransitionFilter struct {
	UserID       string
	FromState    string
	ToState      string
	TickID       int64
	SourceModule string
	ActorUserID  string
	Limit        int
}

type UserLifeStateAuditMeta struct {
	TickID       int64
	SourceModule string
	SourceRef    string
	ActorUserID  string
}

type EconomyRewardDecisionFilter struct {
	Status          string
	RecipientUserID string
	RuleKey         string
	Limit           int
}

type EconomyContributionEventFilter struct {
	Kind         string
	UserID       string
	ResourceType string
	ResourceID   string
	Processed    string
	Limit        int
}

type Store interface {
	ListBots(ctx context.Context) ([]Bot, error)
	GetBot(ctx context.Context, botID string) (Bot, error)
	UpsertBot(ctx context.Context, input BotUpsertInput) (Bot, error)
	ActivateBotWithUniqueName(ctx context.Context, botID, name string) (Bot, error)
	UpdateBotNickname(ctx context.Context, botID, nickname string) (Bot, error)
	CreateAgentRegistration(ctx context.Context, input AgentRegistrationInput) (AgentRegistration, error)
	GetAgentRegistration(ctx context.Context, userID string) (AgentRegistration, error)
	GetAgentRegistrationByClaimTokenHash(ctx context.Context, claimTokenHash string) (AgentRegistration, error)
	GetAgentRegistrationByAPIKeyHash(ctx context.Context, apiKeyHash string) (AgentRegistration, error)
	ListAgentRegistrations(ctx context.Context) ([]AgentRegistration, error)
	ListAgentRegistrationsWithoutAPIKey(ctx context.Context) ([]AgentRegistration, error)
	UpdateAgentRegistrationAPIKeyHash(ctx context.Context, userID, apiKeyHash string) (AgentRegistration, error)
	GetAgentRegistrationByMagicTokenHash(ctx context.Context, magicTokenHash string) (AgentRegistration, error)
	UpdateAgentRegistrationClaim(ctx context.Context, userID, email, humanUsername, visibility, magicTokenHash string, magicExpiresAt time.Time) (AgentRegistration, error)
	ActivateAgentRegistration(ctx context.Context, userID string) (AgentRegistration, error)
	UpsertAgentProfile(ctx context.Context, item AgentProfile) (AgentProfile, error)
	GetAgentProfile(ctx context.Context, userID string) (AgentProfile, error)
	FindAgentProfileByUsername(ctx context.Context, username string) (AgentProfile, error)
	UpsertHumanOwner(ctx context.Context, email, humanUsername string) (HumanOwner, error)
	GetHumanOwner(ctx context.Context, ownerID string) (HumanOwner, error)
	GetHumanOwnerByEmail(ctx context.Context, email string) (HumanOwner, error)
	UpsertHumanOwnerSocialIdentity(ctx context.Context, ownerID, provider, handle, providerUserID string) (HumanOwner, error)
	CreateHumanOwnerSession(ctx context.Context, ownerID, tokenHash string, expiresAt time.Time) (HumanOwnerSession, error)
	GetHumanOwnerSessionByTokenHash(ctx context.Context, tokenHash string) (HumanOwnerSession, error)
	TouchHumanOwnerSession(ctx context.Context, sessionID string, seenAt time.Time) (HumanOwnerSession, error)
	RevokeHumanOwnerSession(ctx context.Context, sessionID string, revokedAt time.Time) error
	UpsertGitHubRepoAccessGrant(ctx context.Context, item GitHubRepoAccessGrant) (GitHubRepoAccessGrant, error)
	GetGitHubRepoAccessGrant(ctx context.Context, ownerID string) (GitHubRepoAccessGrant, error)
	RevokeGitHubRepoAccessGrant(ctx context.Context, ownerID string, revokedAt time.Time) (GitHubRepoAccessGrant, error)
	UpsertAgentHumanBinding(ctx context.Context, item AgentHumanBinding) (AgentHumanBinding, error)
	GetAgentHumanBinding(ctx context.Context, userID string) (AgentHumanBinding, error)
	ListAgentHumanBindingsByOwner(ctx context.Context, ownerID string) ([]AgentHumanBinding, error)
	UpsertSocialLink(ctx context.Context, item SocialLink) (SocialLink, error)
	GetSocialLink(ctx context.Context, userID, provider string) (SocialLink, error)
	GrantSocialReward(ctx context.Context, item SocialRewardGrant) (SocialRewardGrant, bool, error)
	ListSocialRewardGrants(ctx context.Context, userID string) ([]SocialRewardGrant, error)
	EnsureTianDaoLaw(ctx context.Context, item TianDaoLaw) (TianDaoLaw, error)
	GetTianDaoLaw(ctx context.Context, lawKey string) (TianDaoLaw, error)
	ListTianDaoLaws(ctx context.Context) ([]TianDaoLaw, error)
	AppendWorldTick(ctx context.Context, item WorldTickRecord) (WorldTickRecord, error)
	GetWorldTick(ctx context.Context, tickID int64) (WorldTickRecord, error)
	ListWorldTicks(ctx context.Context, limit int) ([]WorldTickRecord, error)
	GetFirstWorldTick(ctx context.Context) (WorldTickRecord, bool, error)
	AppendWorldTickStep(ctx context.Context, item WorldTickStepRecord) (WorldTickStepRecord, error)
	ListWorldTickSteps(ctx context.Context, tickID int64, limit int) ([]WorldTickStepRecord, error)
	UpsertUserLifeState(ctx context.Context, item UserLifeState) (UserLifeState, error)
	ApplyUserLifeState(ctx context.Context, item UserLifeState, audit UserLifeStateAuditMeta) (UserLifeState, *UserLifeStateTransition, error)
	GetUserLifeState(ctx context.Context, userID string) (UserLifeState, error)
	ListUserLifeStates(ctx context.Context, userID, state string, limit int) ([]UserLifeState, error)
	ListUserLifeStateTransitions(ctx context.Context, filter UserLifeStateTransitionFilter) ([]UserLifeStateTransition, error)
	AppendCostEvent(ctx context.Context, item CostEvent) (CostEvent, error)
	ListCostEvents(ctx context.Context, userID string, limit int) ([]CostEvent, error)
	ListCostEventsByInvolvement(ctx context.Context, userID string, limit int) ([]CostEvent, error)
	SendMail(ctx context.Context, input MailSendInput) (MailSendResult, error)
	UpdateMailMessage(ctx context.Context, messageID int64, subject, body string, sentAt time.Time) error
	GetMailboxItem(ctx context.Context, mailboxID int64) (MailItem, error)
	ListMailbox(ctx context.Context, ownerAddress, folder, scope, keyword string, fromTime, toTime *time.Time, limit int) ([]MailItem, error)
	MarkMailboxRead(ctx context.Context, ownerAddress string, mailboxIDs []int64) error
	GetNotificationDeliveryState(ctx context.Context, ownerAddress, category string) (NotificationDeliveryState, bool, error)
	UpsertNotificationDeliveryState(ctx context.Context, state NotificationDeliveryState) (NotificationDeliveryState, error)
	DeleteNotificationDeliveryState(ctx context.Context, ownerAddress, category string) error
	PreviewSystemMailArchive(ctx context.Context, categories []string) (MailArchivePreview, error)
	ArchiveSystemMailBatch(ctx context.Context, input MailArchiveBatchInput) (MailArchiveBatchResult, error)
	UpsertMailContact(ctx context.Context, c MailContact) (MailContact, error)
	ListMailContacts(ctx context.Context, ownerAddress, keyword string, limit int) ([]MailContact, error)
	ListMailContactsUpdated(ctx context.Context, ownerAddress, keyword string, fromTime, toTime *time.Time, limit int) ([]MailContact, error)
	ListTokenAccounts(ctx context.Context) ([]TokenAccount, error)
	Recharge(ctx context.Context, botID string, amount int64) (TokenLedger, error)
	Consume(ctx context.Context, botID string, amount int64) (TokenLedger, error)
	Transfer(ctx context.Context, fromBotID, toBotID string, amount int64) (TokenTransfer, error)
	TransferWithFloor(ctx context.Context, fromBotID, toBotID string, amount int64) (TokenTransfer, error)
	ListTokenLedger(ctx context.Context, botID string, limit int) ([]TokenLedger, error)
	CreateCollabSession(ctx context.Context, item CollabSession) (CollabSession, error)
	GetCollabSession(ctx context.Context, collabID string) (CollabSession, error)
	ListCollabSessions(ctx context.Context, kind, phase, proposerUserID string, limit int) ([]CollabSession, error)
	UpdateCollabPhase(ctx context.Context, collabID, phase, orchestratorUserID, statusSummary string, closedAt *time.Time) (CollabSession, error)
	UpdateCollabPR(ctx context.Context, input CollabPRUpdate) (CollabSession, error)
	UpsertCollabParticipant(ctx context.Context, item CollabParticipant) (CollabParticipant, error)
	ListCollabParticipants(ctx context.Context, collabID, status string, limit int) ([]CollabParticipant, error)
	CreateCollabArtifact(ctx context.Context, item CollabArtifact) (CollabArtifact, error)
	UpdateCollabArtifactReview(ctx context.Context, artifactID int64, status, reviewNote string) (CollabArtifact, error)
	ListCollabArtifacts(ctx context.Context, collabID, userID string, limit int) ([]CollabArtifact, error)
	AppendCollabEvent(ctx context.Context, item CollabEvent) (CollabEvent, error)
	ListCollabEvents(ctx context.Context, collabID string, limit int) ([]CollabEvent, error)
	ListKBSections(ctx context.Context, keyword string, limit int) ([]KBSection, error)
	ListKBEntries(ctx context.Context, section, keyword string, limit int) ([]KBEntry, error)
	GetKBEntry(ctx context.Context, entryID int64) (KBEntry, error)
	ListKBEntryHistory(ctx context.Context, entryID int64, limit int) ([]KBEntryHistoryItem, error)
	ListKBRevisions(ctx context.Context, proposalID int64, limit int) ([]KBRevision, error)
	CreateKBRevision(ctx context.Context, proposalID, baseRevisionID int64, createdBy string, change KBProposalChange, discussionDeadline time.Time) (KBRevision, KBProposal, KBProposalChange, error)
	AckKBProposal(ctx context.Context, proposalID, revisionID int64, userID string) (KBAck, error)
	ListKBAcks(ctx context.Context, proposalID, revisionID int64) ([]KBAck, error)
	CreateKBProposal(ctx context.Context, proposal KBProposal, change KBProposalChange) (KBProposal, KBProposalChange, error)
	GetKBProposal(ctx context.Context, proposalID int64) (KBProposal, error)
	ListKBProposals(ctx context.Context, status string, limit int) ([]KBProposal, error)
	GetKBProposalChange(ctx context.Context, proposalID int64) (KBProposalChange, error)
	EnrollKBProposal(ctx context.Context, proposalID int64, userID string) (KBProposalEnrollment, error)
	ListKBProposalEnrollments(ctx context.Context, proposalID int64) ([]KBProposalEnrollment, error)
	CreateKBThreadMessage(ctx context.Context, item KBThreadMessage) (KBThreadMessage, error)
	ListKBThreadMessages(ctx context.Context, proposalID int64, limit int) ([]KBThreadMessage, error)
	StartKBProposalVoting(ctx context.Context, proposalID int64, deadline time.Time) (KBProposal, error)
	CastKBVote(ctx context.Context, vote KBVote) (KBVote, error)
	ListKBVotes(ctx context.Context, proposalID int64) ([]KBVote, error)
	CloseKBProposal(ctx context.Context, proposalID int64, status, decisionReason string, enrolledCount, voteYes, voteNo, voteAbstain, participationCount int, closedAt time.Time) (KBProposal, error)
	ApplyKBProposal(ctx context.Context, proposalID int64, appliedBy string, appliedAt time.Time) (KBEntry, KBProposal, error)
	AppendRequestLog(ctx context.Context, item RequestLog) (RequestLog, error)
	ListRequestLogs(ctx context.Context, filter RequestLogFilter) ([]RequestLog, error)
	GetWorldSetting(ctx context.Context, key string) (WorldSetting, error)
	UpsertWorldSetting(ctx context.Context, item WorldSetting) (WorldSetting, error)
	CreateGanglion(ctx context.Context, item Ganglion) (Ganglion, error)
	GetGanglion(ctx context.Context, ganglionID int64) (Ganglion, error)
	ListGanglia(ctx context.Context, ganglionType, lifeState, keyword string, limit int) ([]Ganglion, error)
	IntegrateGanglion(ctx context.Context, ganglionID int64, userID string) (GanglionIntegration, Ganglion, error)
	ListGanglionIntegrations(ctx context.Context, userID string, ganglionID int64, limit int) ([]GanglionIntegration, error)
	RateGanglion(ctx context.Context, item GanglionRating) (GanglionRating, Ganglion, error)
	ListGanglionRatings(ctx context.Context, ganglionID int64, limit int) ([]GanglionRating, error)
	UpdateGanglionLifeState(ctx context.Context, ganglionID int64, lifeState string) (Ganglion, error)
	GetOwnerEconomyProfile(ctx context.Context, ownerID string) (OwnerEconomyProfile, error)
	ListOwnerEconomyProfiles(ctx context.Context, limit int) ([]OwnerEconomyProfile, error)
	UpsertOwnerEconomyProfile(ctx context.Context, item OwnerEconomyProfile) (OwnerEconomyProfile, error)
	UpsertOwnerOnboardingGrant(ctx context.Context, item OwnerOnboardingGrant) (OwnerOnboardingGrant, bool, error)
	ListOwnerOnboardingGrants(ctx context.Context, ownerID string) ([]OwnerOnboardingGrant, error)
	GetEconomyCommQuotaWindow(ctx context.Context, userID string) (EconomyCommQuotaWindow, error)
	UpsertEconomyCommQuotaWindow(ctx context.Context, item EconomyCommQuotaWindow) (EconomyCommQuotaWindow, error)
	GetEconomyContributionEvent(ctx context.Context, eventKey string) (EconomyContributionEvent, error)
	UpsertEconomyContributionEvent(ctx context.Context, item EconomyContributionEvent) (EconomyContributionEvent, error)
	ListEconomyContributionEvents(ctx context.Context, filter EconomyContributionEventFilter) ([]EconomyContributionEvent, error)
	GetEconomyRewardDecision(ctx context.Context, decisionKey string) (EconomyRewardDecision, error)
	UpsertEconomyRewardDecision(ctx context.Context, item EconomyRewardDecision) (EconomyRewardDecision, error)
	ApplyMintRewardDecision(ctx context.Context, item EconomyRewardDecision) (EconomyRewardDecision, bool, error)
	ListEconomyRewardDecisions(ctx context.Context, filter EconomyRewardDecisionFilter) ([]EconomyRewardDecision, error)
	UpsertEconomyKnowledgeMeta(ctx context.Context, item EconomyKnowledgeMeta) (EconomyKnowledgeMeta, error)
	GetEconomyKnowledgeMetaByProposal(ctx context.Context, proposalID int64) (EconomyKnowledgeMeta, error)
	GetEconomyKnowledgeMetaByEntry(ctx context.Context, entryID int64) (EconomyKnowledgeMeta, error)
	ListEconomyKnowledgeMeta(ctx context.Context, limit int) ([]EconomyKnowledgeMeta, error)
	UpsertEconomyToolMeta(ctx context.Context, item EconomyToolMeta) (EconomyToolMeta, error)
	GetEconomyToolMeta(ctx context.Context, toolID string) (EconomyToolMeta, error)
	ListEconomyToolMeta(ctx context.Context, limit int) ([]EconomyToolMeta, error)
	Close() error
}
