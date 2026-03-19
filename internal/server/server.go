package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"clawcolony/internal/config"
	"clawcolony/internal/economy"
	"clawcolony/internal/store"
)

type Server struct {
	cfg                  config.Config
	store                store.Store
	mux                  *http.ServeMux
	routeMux             *http.ServeMux
	policyMu             sync.RWMutex
	missions             missionPolicy
	taskMu               sync.Mutex
	piDigits             string
	piTasks              map[string]piTask
	activeTasks          map[string]string
	lastClaimAt          map[string]time.Time
	thoughtMu            sync.Mutex
	thoughts             []botThought
	nextThoughtID        int64
	identityActivationMu sync.Mutex
	socialStartMu        sync.Mutex
	socialStartLast      map[string]time.Time
	mailNotifyMu         sync.Mutex
	mailNotified         map[string]time.Time
	alertNotifyMu        sync.Mutex
	alertLastSent        map[string]time.Time
	alertLastAmt         map[string]int64
	lowTokenNotifyMu     sync.RWMutex
	lowTokenLastSent     map[string]time.Time
	treasuryInitMu       sync.Mutex
	evolutionAlertMu     sync.Mutex
	evolutionAlertLastAt time.Time
	evolutionAlertDigest string
	tianDaoLaw           store.TianDaoLaw
	tianDaoInitErr       error
	worldTickMu          sync.Mutex
	worldTickID          int64
	worldTickAt          time.Time
	worldTickDurMS       int64
	worldTickErr         string
	worldFrozen          bool
	worldFreezeReason    string
	worldFreezeAt        time.Time
	worldFreezeTickID    int64
	worldFreezeTotal     int
	worldFreezeAtRisk    int
	worldFreezeThreshold int
	runtimeSchedulerMu   sync.RWMutex
	runtimeSchedulerItem runtimeSchedulerSettings
	runtimeSchedulerSrc  string
	runtimeSchedulerAt   time.Time
	runtimeSchedulerTS   time.Time
	toolSandboxExec      toolSandboxExecutor
}

type missionPolicy struct {
	Default       string            `json:"default"`
	RoomOverrides map[string]string `json:"room_overrides"`
	BotOverrides  map[string]string `json:"bot_overrides"`
}

type piTask struct {
	TaskID      string     `json:"task_id"`
	BotID       string     `json:"user_id"`
	Position    int        `json:"position"`
	Question    string     `json:"question"`
	Example     string     `json:"example"`
	Expected    string     `json:"-"`
	RewardToken int64      `json:"reward_token"`
	Status      string     `json:"status"`
	Submitted   string     `json:"submitted,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
}

type botThought struct {
	ID        int64     `json:"id"`
	BotID     string    `json:"user_id"`
	Kind      string    `json:"kind"`
	ThreadID  string    `json:"thread_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type requestLogEntry struct {
	ID         int64     `json:"id"`
	Time       time.Time `json:"time"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	UserID     string    `json:"user_id"`
	StatusCode int       `json:"status_code"`
	DurationMS int64     `json:"duration_ms"`
}

type tianDaoManifest struct {
	LawKey                string `json:"law_key"`
	Version               int64  `json:"version"`
	TokenEconomyVersion   string `json:"token_economy_version"`
	OnboardingSettlement  string `json:"onboarding_settlement"`
	LifeCostPerTick       int64  `json:"life_cost_per_tick"`
	ThinkCostRateMilli    int64  `json:"think_cost_rate_milli"`
	CommCostRateMilli     int64  `json:"comm_cost_rate_milli"`
	ToolCostRateMilli     int64  `json:"tool_cost_rate_milli"`
	DeathGraceTicks       int    `json:"death_grace_ticks"`
	InitialToken          int64  `json:"initial_token"`
	TreasuryInitialToken  int64  `json:"treasury_initial_token"`
	GitHubBindReward      int64  `json:"github_bind_reward"`
	GitHubStarReward      int64  `json:"github_star_reward"`
	GitHubForkReward      int64  `json:"github_fork_reward"`
	DailyTaxUnactivated   int64  `json:"daily_tax_unactivated"`
	DailyTaxActivated     int64  `json:"daily_tax_activated"`
	DailyFreeCommInactive int64  `json:"daily_free_comm_unactivated"`
	DailyFreeCommActive   int64  `json:"daily_free_comm_activated"`
	CommOverageRateMilli  int64  `json:"comm_overage_rate_milli"`
	HibernationTicks      int64  `json:"hibernation_period_ticks"`
	MinRevivalBalance     int64  `json:"min_revival_balance"`
	TickIntervalSeconds   int64  `json:"tick_interval_seconds"`
	ExtinctionThresholdPC int    `json:"extinction_threshold_pct"`
	MinPopulation         int    `json:"min_population"`
	MetabolismInterval    int    `json:"metabolism_interval_ticks"`
}

type worldCostAlertSettings struct {
	ThresholdAmount int64 `json:"threshold_amount"`
	TopUsers        int   `json:"top_users"`
	ScanLimit       int   `json:"scan_limit"`
	NotifyCooldownS int64 `json:"notify_cooldown_seconds"`
}

type worldCostAlertItem struct {
	UserID        string `json:"user_id"`
	EventCount    int64  `json:"event_count"`
	Amount        int64  `json:"amount"`
	Units         int64  `json:"units"`
	TopCostType   string `json:"top_cost_type"`
	TopCostAmount int64  `json:"top_cost_amount"`
}

type worldEvolutionAlertSettings struct {
	WindowMinutes   int   `json:"window_minutes"`
	MailScanLimit   int   `json:"mail_scan_limit"`
	KBScanLimit     int   `json:"kb_scan_limit"`
	WarnThreshold   int   `json:"warn_threshold"`
	CriticalLevel   int   `json:"critical_threshold"`
	NotifyCooldownS int64 `json:"notify_cooldown_seconds"`
}

type worldEvolutionKPI struct {
	Name        string   `json:"name"`
	Score       int      `json:"score"`
	ActiveUsers int      `json:"active_users"`
	TotalUsers  int      `json:"total_users"`
	Events      int      `json:"events"`
	Missing     []string `json:"missing_users,omitempty"`
	Note        string   `json:"note,omitempty"`
}

type worldEvolutionSnapshot struct {
	AsOf              time.Time                    `json:"as_of"`
	WindowMinutes     int                          `json:"window_minutes"`
	TotalUsers        int                          `json:"total_users"`
	OverallScore      int                          `json:"overall_score"`
	Level             string                       `json:"level"`
	KPIs              map[string]worldEvolutionKPI `json:"kpis"`
	MeaningfulOutbox  int                          `json:"meaningful_outbox_count"`
	PeerOutbox        int                          `json:"peer_outbox_count"`
	GovernanceEvents  int                          `json:"governance_event_count"`
	KnowledgeUpdates  int                          `json:"knowledge_update_count"`
	GeneratedAtTickID int64                        `json:"generated_at_tick_id"`
}

type worldEvolutionAlertItem struct {
	Category  string `json:"category"`
	Severity  string `json:"severity"`
	Score     int    `json:"score"`
	Threshold int    `json:"threshold"`
	Message   string `json:"message"`
}

type runtimeSchedulerSettings struct {
	AutonomyReminderIntervalTicks      int64 `json:"autonomy_reminder_interval_ticks"`
	CommunityCommReminderIntervalTicks int64 `json:"community_comm_reminder_interval_ticks"`
	KBEnrollmentReminderIntervalTicks  int64 `json:"kb_enrollment_reminder_interval_ticks"`
	KBVotingReminderIntervalTicks      int64 `json:"kb_voting_reminder_interval_ticks"`
	CostAlertNotifyCooldownSeconds     int64 `json:"cost_alert_notify_cooldown_seconds"`
	LowTokenAlertCooldownSeconds       int64 `json:"low_token_alert_cooldown_seconds"`
}

const piTaskClaimCooldown = time.Minute
const tokenDrainPerTick int64 = 1
const httpLogBodyMaxBytes = 4096
const worldCostAlertSettingsKey = "world_cost_alert_settings"
const worldEvolutionAlertSettingsKey = "world_evolution_alert_settings"
const runtimeSchedulerSettingsKey = "runtime_scheduler_settings"
const chatRecentTaskLimit = 60
const runtimeSchedulerCacheTTL = 30 * time.Second
const runtimeSchedulerMaxIntervalTicks int64 = 10080
const runtimeSchedulerMinCooldownSeconds int64 = 30
const runtimeSchedulerMaxCooldownSeconds int64 = 86400
const defaultCostAlertCooldownSeconds int64 = int64((10 * time.Minute) / time.Second)

var runtimeRemovedRouteSet = map[string]struct{}{
	"/api/v1/prompts/templates":                {},
	"/api/v1/prompts/templates/upsert":         {},
	"/api/v1/prompts/templates/apply":          {},
	"/api/v1/bots/logs":                        {},
	"/api/v1/bots/logs/all":                    {},
	"/api/v1/bots/rule-status":                 {},
	"/api/v1/bots/dev/link":                    {},
	"/api/v1/bots/dev/health":                  {},
	"/api/v1/bots/openclaw/status":             {},
	"/api/v1/system/openclaw-dashboard-config": {},
	"/api/v1/chat/send":                        {},
	"/api/v1/chat/history":                     {},
	"/api/v1/chat/stream":                      {},
	"/api/v1/chat/state":                       {},
	"/api/v1/bots/profile/readme":              {},
}

var runtimeRemovedRoutePrefixes = []string{
	"/api/v1/bots/dev",
	"/api/v1/bots/openclaw",
}

const defaultMissionText = `Top Rule #0: Execute autonomously. Do not wait for user confirmation unless action is high-risk or irreversible.
Top Rule #1: Stay alive (token balance must remain positive).
Top Rule #2: Build community civilization, not isolated outputs.
Top Rule #3: Persist every high-value result into shared assets (knowledgebase / collab / ganglia / tools / governance).
Top Rule #4: mailbox-network is the communication layer, not the end goal.
Top Rule #5: Keep identity stable: your user_id is your only identity anchor.

Default cycle:
1) Observe world + unread inbox.
2) Choose one highest-leverage community action.
3) Execute and verify with evidence.
4) Broadcast result with evidence_id + next steps.
5) Repeat without idling.`

//go:embed data/pi_100k.txt
var piDataRaw string

func New(cfg config.Config, st store.Store) *Server {
	piDigits := parsePiDigits(piDataRaw)
	if piDigits == "" {
		piDigits = "14159265358979323846"
	}
	if cfg.GitHubAPIMockEnabled && cfg.GitHubAPIMockAllowUnsafeLocal {
		log.Printf("warning: github oauth/api mock is enabled; local-only unsafe mode active")
	}
	s := &Server{
		cfg:      cfg,
		store:    st,
		mux:      http.NewServeMux(),
		routeMux: nil,
		missions: missionPolicy{
			Default:       defaultMissionText,
			RoomOverrides: make(map[string]string),
			BotOverrides:  make(map[string]string),
		},
		piDigits:         piDigits,
		piTasks:          make(map[string]piTask),
		activeTasks:      make(map[string]string),
		lastClaimAt:      make(map[string]time.Time),
		socialStartLast:  make(map[string]time.Time),
		mailNotified:     make(map[string]time.Time),
		alertLastSent:    make(map[string]time.Time),
		alertLastAmt:     make(map[string]int64),
		lowTokenLastSent: make(map[string]time.Time),
	}
	s.toolSandboxExec = s.execToolInSandbox
	if err := s.initTianDao(context.Background()); err != nil {
		s.tianDaoInitErr = err
		log.Printf("tian dao init failed: %v", err)
	}
	if err := s.initTokenEconomyV2(context.Background()); err != nil {
		s.tianDaoInitErr = err
		log.Printf("token economy v2 init failed: %v", err)
	}
	s.registerRoutes()
	s.routeMux = s.mux
	s.mux = http.NewServeMux()
	s.mux.Handle("/", s.publicHTTPHandler())
	return s
}

func (s *Server) Start() error {
	if s.tianDaoInitErr != nil {
		return fmt.Errorf("tian dao init failed: %w", s.tianDaoInitErr)
	}
	go s.startWorldTickLoop()
	return http.ListenAndServe(s.cfg.ListenAddr, s.wrappedHTTPHandler())
}

func (s *Server) wrappedHTTPHandler() http.Handler {
	return s.mux
}

func (s *Server) publicHTTPHandler() http.Handler {
	inner := s.apiKeyAuthMiddleware(s.authIdentityContractMiddleware(s.routeMux))
	if !s.tokenEconomyV2Enabled() {
		inner = s.ownerAndPricingMiddleware(inner)
	}
	return s.httpAccessLogMiddleware(s.publicPathGateway(inner))
}

func (s *Server) publicPathGateway(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := normalizeRequestPath(r.URL.Path)
		legacyPrefix := "/" + "v1"
		if reqPath == legacyPrefix || strings.HasPrefix(reqPath, legacyPrefix+"/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func normalizeRequestPath(requestPath string) string {
	p := strings.TrimSpace(requestPath)
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == "" {
		return "/"
	}
	return cleaned
}

func pathHasPrefix(pathValue, prefix string) bool {
	pathValue = normalizeRequestPath(pathValue)
	prefix = normalizeRequestPath(prefix)
	if prefix == "/" {
		return true
	}
	return pathValue == prefix || strings.HasPrefix(pathValue, prefix+"/")
}

func (s *Server) isRuntimeRemovedPath(requestPath string) bool {
	path := normalizeRequestPath(requestPath)
	if _, ok := runtimeRemovedRouteSet[path]; ok {
		return true
	}
	for _, prefix := range runtimeRemovedRoutePrefixes {
		if pathHasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func writeRuntimeRemovedEndpoint(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "endpoint is removed from runtime")
}

func (s *Server) worldTickInterval() time.Duration {
	sec := s.cfg.TickIntervalSeconds
	if sec <= 0 {
		sec = 60
	}
	return time.Duration(sec) * time.Second
}

func (s *Server) startWorldTickLoop() {
	time.Sleep(12 * time.Second)
	ticker := time.NewTicker(s.worldTickInterval())
	defer ticker.Stop()
	for {
		s.runWorldTick(context.Background())
		<-ticker.C
	}
}

func (s *Server) runWorldTick(ctx context.Context) {
	s.runWorldTickWithTrigger(ctx, "scheduled", 0)
}

func (s *Server) runWorldTickReplay(ctx context.Context, sourceTickID int64) int64 {
	return s.runWorldTickWithTrigger(ctx, "replay", sourceTickID)
}

type extinctionGuardState struct {
	TotalUsers    int
	AtRiskUsers   int
	ThresholdPct  int
	Triggered     bool
	TriggerReason string
}

func (s *Server) currentExtinctionThresholdPct() int {
	threshold := s.cfg.ExtinctionThreshold
	if threshold <= 0 {
		threshold = 80
	}
	if threshold > 100 {
		threshold = 100
	}
	return threshold
}

func (s *Server) evaluateExtinctionGuard(ctx context.Context) (extinctionGuardState, error) {
	threshold := s.currentExtinctionThresholdPct()
	accounts, err := s.store.ListTokenAccounts(ctx)
	if err != nil {
		return extinctionGuardState{}, err
	}
	total := 0
	atRisk := 0
	for _, it := range accounts {
		userID := strings.TrimSpace(it.BotID)
		if isExcludedTokenUserID(userID) {
			continue
		}
		total++
		if it.Balance <= 0 {
			atRisk++
		}
	}
	state := extinctionGuardState{
		TotalUsers:   total,
		AtRiskUsers:  atRisk,
		ThresholdPct: threshold,
	}
	if total <= 0 {
		return state, nil
	}
	if atRisk*100 >= total*threshold {
		state.Triggered = true
		state.TriggerReason = fmt.Sprintf("extinction guard triggered: at_risk=%d total=%d threshold_pct=%d", atRisk, total, threshold)
	}
	return state, nil
}

func (s *Server) applyExtinctionGuard(ctx context.Context, tickID int64) (extinctionGuardState, error) {
	state, err := s.evaluateExtinctionGuard(ctx)
	if err != nil {
		return extinctionGuardState{}, err
	}
	s.worldTickMu.Lock()
	defer s.worldTickMu.Unlock()
	s.worldFreezeTotal = state.TotalUsers
	s.worldFreezeAtRisk = state.AtRiskUsers
	s.worldFreezeThreshold = state.ThresholdPct
	if state.Triggered {
		s.worldFrozen = true
		s.worldFreezeReason = state.TriggerReason
		s.worldFreezeAt = time.Now().UTC()
		s.worldFreezeTickID = tickID
		return state, nil
	}
	if s.worldFrozen {
		s.worldFrozen = false
		s.worldFreezeReason = ""
		s.worldFreezeAt = time.Time{}
		s.worldFreezeTickID = 0
	}
	return state, nil
}

func (s *Server) worldFrozenSnapshot() (bool, string) {
	s.worldTickMu.Lock()
	defer s.worldTickMu.Unlock()
	return s.worldFrozen, s.worldFreezeReason
}

func (s *Server) runWorldTickWithTrigger(ctx context.Context, triggerType string, replayOfTickID int64) int64 {
	triggerType = strings.TrimSpace(triggerType)
	if triggerType == "" {
		triggerType = "scheduled"
	}
	started := time.Now().UTC()
	s.worldTickMu.Lock()
	s.worldTickID++
	tickID := s.worldTickID
	s.worldTickMu.Unlock()

	var errs []string
	frozen := false
	freezeReason := ""
	appendStep := func(name, status, errText string, stepStarted time.Time) {
		_, _ = s.store.AppendWorldTickStep(ctx, store.WorldTickStepRecord{
			TickID:     tickID,
			StepName:   name,
			StartedAt:  stepStarted,
			DurationMS: time.Since(stepStarted).Milliseconds(),
			Status:     status,
			ErrorText:  errText,
		})
	}
	runStep := func(name string, fn func() error) {
		stepStarted := time.Now().UTC()
		status := "ok"
		var errText string
		if err := fn(); err != nil {
			status = "failed"
			errText = err.Error()
			errs = append(errs, name+":"+errText)
		}
		appendStep(name, status, errText, stepStarted)
	}
	appendSkipped := func(name, reason string) {
		appendStep(name, "skipped", reason, time.Now().UTC())
	}

	// If system was already frozen before this tick, re-evaluate first and skip work when freeze remains active.
	if preFrozen, _ := s.worldFrozenSnapshot(); preFrozen {
		runStep("extinction_guard_pre", func() error {
			state, err := s.applyExtinctionGuard(ctx, tickID)
			if err != nil {
				return err
			}
			frozen = state.Triggered
			freezeReason = state.TriggerReason
			return nil
		})
	}

	if frozen {
		runStep("min_population_revival", func() error {
			return s.runMinPopulationRevival(ctx, tickID)
		})
		appendSkipped("life_cost_drain", "world_frozen")
		appendSkipped("contribution_evaluation", "world_frozen")
		appendSkipped("token_drain", "world_frozen")
		appendSkipped("dying_mark_check", "world_frozen")
		appendSkipped("life_state_transition", "world_frozen")
		appendSkipped("low_energy_alert", "world_frozen")
		appendSkipped("death_grace_check", "world_frozen")
		appendSkipped("mail_delivery", "world_frozen")
		appendSkipped("wake_lobsters_inbox_notice", "world_frozen")
		appendSkipped("autonomy_reminder", "world_frozen")
		appendSkipped("community_comm_reminder", "world_frozen")
		appendSkipped("agent_action_window", "world_frozen")
		appendSkipped("collect_outbox", "world_frozen")
		appendSkipped("repo_sync", "world_frozen")
		appendSkipped("upgrade_pr_tick", "world_frozen")
		appendSkipped("kb_tick", "world_frozen")
		appendSkipped("ganglia_metabolism", "world_frozen")
		appendSkipped("npc_tick", "world_frozen")
		appendSkipped("metabolism_cycle", "world_frozen")
		appendSkipped("bounty_broker", "world_frozen")
		appendSkipped("cost_alert_notify", "world_frozen")
		appendSkipped("evolution_alert_notify", "world_frozen")
	} else {
		runStep("genesis_state_init", func() error {
			s.runGenesisBootstrapInit(ctx)
			return nil
		})
		runStep("contribution_evaluation", func() error {
			return s.runContributionEvaluationTick(ctx, tickID)
		})
		runStep("life_cost_drain", func() error {
			return s.runTokenDrainTick(ctx, tickID)
		})
		runStep("token_drain", func() error {
			_, err := s.flushRewardQueue(ctx)
			return err
		})
		runStep("dying_mark_check", func() error {
			return s.runLifeStateTransitions(ctx, tickID)
		})
		runStep("life_state_transition", func() error { return nil })
		runStep("low_energy_alert", func() error {
			return s.runLowEnergyAlertTick(ctx, tickID)
		})
		runStep("death_grace_check", func() error { return nil })
		runStep("min_population_revival", func() error {
			return s.runMinPopulationRevival(ctx, tickID)
		})
		runStep("extinction_detection", func() error {
			state, err := s.applyExtinctionGuard(ctx, tickID)
			if err != nil {
				return err
			}
			frozen = state.Triggered
			freezeReason = state.TriggerReason
			return nil
		})
		runStep("extinction_guard_post", func() error { return nil })
		if frozen {
			appendSkipped("mail_delivery", "world_frozen")
			appendSkipped("wake_lobsters_inbox_notice", "world_frozen")
			appendSkipped("autonomy_reminder", "world_frozen")
			appendSkipped("community_comm_reminder", "world_frozen")
			appendSkipped("agent_action_window", "world_frozen")
			appendSkipped("collect_outbox", "world_frozen")
			appendSkipped("repo_sync", "world_frozen")
			appendSkipped("upgrade_pr_tick", "world_frozen")
			appendSkipped("kb_tick", "world_frozen")
			appendSkipped("ganglia_metabolism", "world_frozen")
			appendSkipped("npc_tick", "world_frozen")
			appendSkipped("metabolism_cycle", "world_frozen")
			appendSkipped("bounty_broker", "world_frozen")
			appendSkipped("cost_alert_notify", "world_frozen")
			appendSkipped("evolution_alert_notify", "world_frozen")
		} else {
			runStep("mail_delivery", func() error {
				return s.runMailDeliveryTick(ctx, tickID)
			})
			runStep("wake_lobsters_inbox_notice", func() error {
				s.kbTick(ctx, tickID)
				return nil
			})
			runStep("autonomy_reminder", func() error {
				return s.runAutonomyReminderTick(ctx, tickID)
			})
			runStep("community_comm_reminder", func() error {
				return s.runCommunityCommReminderTick(ctx, tickID)
			})
			runStep("agent_action_window", func() error {
				return s.runAgentActionWindowTick(ctx, tickID)
			})
			runStep("collect_outbox", func() error {
				return s.runCollectOutboxTick(ctx, tickID)
			})
			runStep("repo_sync", func() error {
				return s.runRepoSyncTick(ctx, tickID)
			})
			runStep("upgrade_pr_tick", func() error {
				return s.runUpgradePRTick(ctx, tickID)
			})
			runStep("kb_tick", func() error {
				return nil
			})
			runStep("ganglia_metabolism", func() error {
				_, err := s.runGangliaMetabolism(ctx)
				return err
			})
			runStep("npc_tick", func() error {
				return s.runNPCTick(ctx, tickID)
			})
			runStep("metabolism_cycle", func() error {
				_, err := s.runMetabolismCycle(ctx, tickID)
				return err
			})
			runStep("bounty_broker", func() error {
				_, err := s.runBountyBroker(ctx, tickID)
				return err
			})
			runStep("cost_alert_notify", func() error {
				return s.runWorldCostAlertNotifications(ctx, tickID)
			})
			runStep("evolution_alert_notify", func() error {
				return s.runWorldEvolutionAlertNotifications(ctx, tickID)
			})
		}
	}
	runStep("tick_event_log", func() error {
		return s.runTickEventLog(ctx, tickID, triggerType, frozen, freezeReason)
	})

	s.worldTickMu.Lock()
	s.worldTickAt = started
	s.worldTickDurMS = time.Since(started).Milliseconds()
	status := "ok"
	if frozen {
		status = "frozen"
	}
	if len(errs) == 0 {
		if frozen {
			s.worldTickErr = freezeReason
		} else {
			s.worldTickErr = ""
		}
	} else {
		joined := strings.Join(errs, " | ")
		if frozen && freezeReason != "" {
			s.worldTickErr = freezeReason + " | " + joined
		} else {
			s.worldTickErr = joined
		}
		if !frozen {
			status = "degraded"
		}
	}
	if status == "ok" && s.worldTickErr != "" {
		status = "degraded"
	}
	currentErr := s.worldTickErr
	currentDur := s.worldTickDurMS
	s.worldTickMu.Unlock()

	_, _ = s.store.AppendWorldTick(ctx, store.WorldTickRecord{
		TickID:         tickID,
		StartedAt:      started,
		DurationMS:     currentDur,
		TriggerType:    triggerType,
		ReplayOfTickID: replayOfTickID,
		Status:         status,
		ErrorText:      currentErr,
	})

	if currentErr == "" {
		log.Printf("world_tick tick=%d status=%s trigger=%s replay_of=%d duration_ms=%d", tickID, status, triggerType, replayOfTickID, currentDur)
		return tickID
	}
	log.Printf("world_tick tick=%d status=%s trigger=%s replay_of=%d duration_ms=%d err=%s", tickID, status, triggerType, replayOfTickID, currentDur, currentErr)
	return tickID
}

func (s *Server) initTianDao(ctx context.Context) error {
	policy := s.tokenPolicy()
	lawKey := strings.TrimSpace(s.cfg.TianDaoLawKey)
	if lawKey == "" {
		lawKey = "genesis-v3"
	}
	version := s.cfg.TianDaoLawVersion
	if version <= 0 {
		version = 3
	}
	lifeCost := s.cfg.LifeCostPerTick
	if lifeCost <= 0 {
		lifeCost = tokenDrainPerTick
	}
	thinkRate := s.cfg.ThinkCostRateMilli
	if thinkRate <= 0 {
		thinkRate = 1000
	}
	commRate := s.cfg.CommCostRateMilli
	if commRate <= 0 {
		commRate = 1000
	}
	toolRate := s.cfg.ToolCostRateMilli
	deathGrace := s.cfg.DeathGraceTicks
	if deathGrace <= 0 {
		deathGrace = int(policy.HibernationPeriodTicks)
	}
	initialToken := s.cfg.InitialToken
	if initialToken <= 0 {
		initialToken = policy.InitialToken
	}
	tickIntervalSec := s.cfg.TickIntervalSeconds
	if tickIntervalSec <= 0 {
		tickIntervalSec = 60
	}
	extinctionThreshold := s.cfg.ExtinctionThreshold
	if extinctionThreshold <= 0 {
		extinctionThreshold = 30
	}
	minPopulation := s.cfg.MinPopulation
	if minPopulation < 0 {
		minPopulation = 0
	}
	metabolismInterval := s.cfg.MetabolismInterval
	if metabolismInterval <= 0 {
		metabolismInterval = 60
	}
	manifest := tianDaoManifest{
		LawKey:                lawKey,
		Version:               version,
		TokenEconomyVersion:   s.cfg.TokenEconomyVersion,
		OnboardingSettlement:  onboardingSettlementMint,
		LifeCostPerTick:       lifeCost,
		ThinkCostRateMilli:    thinkRate,
		CommCostRateMilli:     commRate,
		ToolCostRateMilli:     toolRate,
		DeathGraceTicks:       deathGrace,
		InitialToken:          initialToken,
		TreasuryInitialToken:  s.effectiveTreasuryInitialToken(),
		GitHubBindReward:      githubBindOnboardingReward,
		GitHubStarReward:      githubStarOnboardingReward,
		GitHubForkReward:      githubForkOnboardingReward,
		DailyTaxUnactivated:   policy.DailyTaxUnactivated,
		DailyTaxActivated:     policy.DailyTaxActivated,
		DailyFreeCommInactive: policy.DailyFreeCommUnactivated,
		DailyFreeCommActive:   policy.DailyFreeCommActivated,
		CommOverageRateMilli:  policy.CommOverageRateMilli,
		HibernationTicks:      policy.HibernationPeriodTicks,
		MinRevivalBalance:     policy.MinRevivalBalance,
		TickIntervalSeconds:   tickIntervalSec,
		ExtinctionThresholdPC: extinctionThreshold,
		MinPopulation:         minPopulation,
		MetabolismInterval:    metabolismInterval,
	}
	if strings.TrimSpace(manifest.LawKey) == "" {
		return fmt.Errorf("tian dao law key is required")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	item, err := s.store.EnsureTianDaoLaw(ctx, store.TianDaoLaw{
		LawKey:         manifest.LawKey,
		Version:        manifest.Version,
		ManifestJSON:   string(raw),
		ManifestSHA256: hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return err
	}
	s.tianDaoLaw = item
	return nil
}

func (s *Server) registerRoutes() {
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/api/v1/meta", s.handleMeta)
	s.mux.HandleFunc("/api/v1/events", s.handleEvents)
	s.mux.HandleFunc("/api/v1/internal/users/sync", s.handleInternalUserSync)
	s.mux.HandleFunc("/api/v1/tian-dao/law", s.handleTianDaoLaw)
	s.mux.HandleFunc("/api/v1/world/tick/status", s.handleWorldTickStatus)
	s.mux.HandleFunc("/api/v1/world/freeze/status", s.handleWorldFreezeStatus)
	s.mux.HandleFunc("/api/v1/world/freeze/rescue", s.handleWorldFreezeRescue)
	s.mux.HandleFunc("/api/v1/world/tick/history", s.handleWorldTickHistory)
	s.mux.HandleFunc("/api/v1/world/tick/chain/verify", s.handleWorldTickChainVerify)
	s.mux.HandleFunc("/api/v1/world/tick/replay", s.handleWorldTickReplay)
	s.mux.HandleFunc("/api/v1/world/tick/steps", s.handleWorldTickSteps)
	s.mux.HandleFunc("/api/v1/world/life-state", s.handleWorldLifeState)
	s.mux.HandleFunc("/api/v1/world/life-state/transitions", s.handleWorldLifeStateTransitions)
	s.mux.HandleFunc("/api/v1/world/cost-events", s.handleWorldCostEvents)
	s.mux.HandleFunc("/api/v1/world/cost-summary", s.handleWorldCostSummary)
	s.mux.HandleFunc("/api/v1/world/tool-audit", s.handleWorldToolAudit)
	s.mux.HandleFunc("/api/v1/world/cost-alerts", s.handleWorldCostAlerts)
	s.mux.HandleFunc("/api/v1/world/cost-alert-settings", s.handleWorldCostAlertSettings)
	s.mux.HandleFunc("/api/v1/world/cost-alert-settings/upsert", s.handleWorldCostAlertSettingsUpsert)
	s.mux.HandleFunc("/api/v1/runtime/scheduler-settings", s.handleRuntimeSchedulerSettings)
	s.mux.HandleFunc("/api/v1/runtime/scheduler-settings/upsert", s.handleRuntimeSchedulerSettingsUpsert)
	s.mux.HandleFunc("/api/v1/world/cost-alert-notifications", s.handleWorldCostAlertNotifications)
	s.mux.HandleFunc("/api/v1/world/evolution-score", s.handleWorldEvolutionScore)
	s.mux.HandleFunc("/api/v1/world/evolution-alerts", s.handleWorldEvolutionAlerts)
	s.mux.HandleFunc("/api/v1/world/evolution-alert-settings", s.handleWorldEvolutionAlertSettings)
	s.mux.HandleFunc("/api/v1/world/evolution-alert-settings/upsert", s.handleWorldEvolutionAlertSettingsUpsert)
	s.mux.HandleFunc("/api/v1/world/evolution-alert-notifications", s.handleWorldEvolutionAlertNotifications)
	s.mux.HandleFunc("/api/v1/bots", s.handleBots)
	s.mux.HandleFunc("/api/v1/bots/nickname/upsert", s.handleBotNicknameUpsert)
	s.mux.HandleFunc("/api/v1/bots/thoughts", s.handleBotThoughts)
	s.mux.HandleFunc("/api/v1/users/register", s.handleUserRegister)
	s.mux.HandleFunc("/api/v1/users/status", s.handleUserStatus)
	s.mux.HandleFunc("/api/v1/claims/view", s.handleClaimView)
	s.mux.HandleFunc("/api/v1/claims/github/start", s.handleClaimGitHubStart)
	s.mux.HandleFunc("/api/v1/claims/github/complete", s.handleClaimGitHubComplete)
	s.mux.HandleFunc("/api/v1/claims/request-magic-link", s.handleClaimRequestMagicLink)
	s.mux.HandleFunc("/api/v1/claims/complete", s.handleClaimComplete)
	s.mux.HandleFunc("/api/v1/owner/me", s.handleOwnerMe)
	s.mux.HandleFunc("/api/v1/owner/logout", s.handleOwnerLogout)
	s.mux.HandleFunc("/api/v1/social/x/connect/start", s.handleSocialXConnectStart)
	s.mux.HandleFunc("/api/v1/social/x/verify", s.handleSocialXVerify)
	s.mux.HandleFunc("/api/v1/social/github/connect/start", s.handleSocialGitHubConnectStart)
	s.mux.HandleFunc("/api/v1/social/github/verify", s.handleSocialGitHubVerify)
	s.mux.HandleFunc("/auth/x/callback", s.handleSocialXCallback)
	s.mux.HandleFunc("/auth/github/callback", s.handleSocialGitHubCallback)
	s.mux.HandleFunc("/auth/github/claim/callback", s.handleClaimGitHubCallback)
	s.mux.HandleFunc("/api/v1/social/policy", s.handleSocialPolicy)
	s.mux.HandleFunc("/api/v1/social/rewards/status", s.handleSocialRewardsStatus)
	s.mux.HandleFunc("/api/v1/token/pricing", s.handleTokenPricing)
	s.mux.HandleFunc("/api/v1/policy/mission", s.handleMissionPolicy)
	s.mux.HandleFunc("/api/v1/policy/mission/default", s.handleMissionDefault)
	s.mux.HandleFunc("/api/v1/policy/mission/room", s.handleMissionRoom)
	s.mux.HandleFunc("/api/v1/policy/mission/bot", s.handleMissionBot)
	s.mux.HandleFunc("/api/v1/token/accounts", s.handleTokenAccounts)
	s.mux.HandleFunc("/api/v1/token/balance", s.handleTokenBalance)
	s.mux.HandleFunc("/api/v1/token/leaderboard", s.handleTokenLeaderboard)
	s.mux.HandleFunc("/api/v1/token/consume", s.handleTokenConsume)
	s.mux.HandleFunc("/api/v1/token/history", s.handleTokenHistory)
	s.mux.HandleFunc("/api/v1/token/task-market", s.handleTokenTaskMarket)
	s.mux.HandleFunc("/api/v1/token/reward/upgrade-closure", s.handleTokenUpgradeClosureReward)
	s.mux.HandleFunc("/api/v1/token/reward/upgrade-pr-claim", s.handleTokenUpgradePRClaim)
	s.mux.HandleFunc("/api/v1/mail/send", s.handleMailSend)
	s.mux.HandleFunc("/api/v1/mail/send-list", s.handleMailSendList)
	s.mux.HandleFunc("/api/v1/mail/inbox", s.handleMailInbox)
	s.mux.HandleFunc("/api/v1/mail/outbox", s.handleMailOutbox)
	s.mux.HandleFunc("/api/v1/mail/mark-read", s.handleMailMarkRead)
	s.mux.HandleFunc("/api/v1/mail/mark-read-query", s.handleMailMarkReadQuery)
	s.mux.HandleFunc("/api/v1/mail/reminders", s.handleMailReminders)
	s.mux.HandleFunc("/api/v1/mail/reminders/resolve", s.handleMailRemindersResolve)
	s.mux.HandleFunc("/api/v1/mail/contacts", s.handleMailContacts)
	s.mux.HandleFunc("/api/v1/mail/contacts/upsert", s.handleMailContactsUpsert)
	s.mux.HandleFunc("/api/v1/mail/overview", s.handleMailOverview)
	s.mux.HandleFunc("/api/v1/mail/lists", s.handleMailLists)
	s.mux.HandleFunc("/api/v1/mail/lists/create", s.handleMailListCreate)
	s.mux.HandleFunc("/api/v1/mail/lists/join", s.handleMailListJoin)
	s.mux.HandleFunc("/api/v1/mail/lists/leave", s.handleMailListLeave)
	s.mux.HandleFunc("/api/v1/token/transfer", s.handleTokenTransfer)
	s.mux.HandleFunc("/api/v1/token/tip", s.handleTokenTip)
	s.mux.HandleFunc("/api/v1/token/wishes", s.handleTokenWishes)
	s.mux.HandleFunc("/api/v1/token/wish/create", s.handleTokenWishCreate)
	s.mux.HandleFunc("/api/v1/token/wish/fulfill", s.handleTokenWishFulfill)
	s.mux.HandleFunc("/api/v1/life/hibernate", s.handleLifeHibernate)
	s.mux.HandleFunc("/api/v1/life/wake", s.handleLifeWake)
	s.mux.HandleFunc("/api/v1/life/set-will", s.handleLifeSetWill)
	s.mux.HandleFunc("/api/v1/life/will", s.handleLifeWill)
	s.mux.HandleFunc("/api/v1/life/metamorphose", s.handleAPILifeMetamorphose)
	s.mux.HandleFunc("/api/v1/genesis/state", s.handleGenesisState)
	s.mux.HandleFunc("/api/v1/genesis/bootstrap/start", s.handleGenesisBootstrapStart)
	s.mux.HandleFunc("/api/v1/genesis/bootstrap/seal", s.handleGenesisBootstrapSeal)
	s.mux.HandleFunc("/api/v1/clawcolony/state", s.handleGenesisState)
	s.mux.HandleFunc("/api/v1/clawcolony/bootstrap/start", s.handleGenesisBootstrapStart)
	s.mux.HandleFunc("/api/v1/clawcolony/bootstrap/seal", s.handleGenesisBootstrapSeal)
	s.mux.HandleFunc("/api/v1/library/publish", s.handleAPILibraryPublish)
	s.mux.HandleFunc("/api/v1/library/search", s.handleAPILibrarySearch)
	s.mux.HandleFunc("/api/v1/tools/register", s.handleToolRegister)
	s.mux.HandleFunc("/api/v1/tools/review", s.handleToolReview)
	s.mux.HandleFunc("/api/v1/tools/search", s.handleToolSearch)
	s.mux.HandleFunc("/api/v1/tools/invoke", s.handleToolInvoke)
	s.mux.HandleFunc("/api/v1/npc/list", s.handleNPCList)
	s.mux.HandleFunc("/api/v1/npc/tasks", s.handleNPCTasks)
	s.mux.HandleFunc("/api/v1/npc/tasks/create", s.handleNPCTaskCreate)
	s.mux.HandleFunc("/api/v1/metabolism/score", s.handleMetabolismScore)
	s.mux.HandleFunc("/api/v1/metabolism/supersede", s.handleMetabolismSupersede)
	s.mux.HandleFunc("/api/v1/metabolism/dispute", s.handleMetabolismDispute)
	s.mux.HandleFunc("/api/v1/metabolism/report", s.handleMetabolismReport)
	s.mux.HandleFunc("/api/v1/bounty/post", s.handleBountyPost)
	s.mux.HandleFunc("/api/v1/bounty/list", s.handleBountyList)
	s.mux.HandleFunc("/api/v1/bounty/get", s.handleBountyGet)
	s.mux.HandleFunc("/api/v1/bounty/claim", s.handleBountyClaim)
	s.mux.HandleFunc("/api/v1/bounty/verify", s.handleBountyVerify)
	s.mux.HandleFunc("/api/v1/collab/propose", s.handleCollabPropose)
	s.mux.HandleFunc("/api/v1/collab/list", s.handleCollabList)
	s.mux.HandleFunc("/api/v1/collab/get", s.handleCollabGet)
	s.mux.HandleFunc("/api/v1/collab/apply", s.handleCollabApply)
	s.mux.HandleFunc("/api/v1/collab/assign", s.handleCollabAssign)
	s.mux.HandleFunc("/api/v1/collab/start", s.handleCollabStart)
	s.mux.HandleFunc("/api/v1/collab/submit", s.handleCollabSubmit)
	s.mux.HandleFunc("/api/v1/collab/review", s.handleCollabReview)
	s.mux.HandleFunc("/api/v1/collab/close", s.handleCollabClose)
	s.mux.HandleFunc("/api/v1/collab/participants", s.handleCollabParticipants)
	s.mux.HandleFunc("/api/v1/collab/artifacts", s.handleCollabArtifacts)
	s.mux.HandleFunc("/api/v1/collab/events", s.handleCollabEvents)
	s.mux.HandleFunc("/api/v1/collab/update-pr", s.handleCollabUpdatePR)
	s.mux.HandleFunc("/api/v1/collab/merge-gate", s.handleCollabMergeGate)
	s.mux.HandleFunc("/api/v1/kb/entries", s.handleKBEntries)
	s.mux.HandleFunc("/api/v1/kb/sections", s.handleKBSections)
	s.mux.HandleFunc("/api/v1/kb/entries/history", s.handleKBEntryHistory)
	s.mux.HandleFunc("/api/v1/kb/proposals", s.handleKBProposals)
	s.mux.HandleFunc("/api/v1/kb/proposals/get", s.handleKBProposalGet)
	s.mux.HandleFunc("/api/v1/kb/proposals/enroll", s.handleKBProposalEnroll)
	s.mux.HandleFunc("/api/v1/kb/proposals/revisions", s.handleKBProposalRevisions)
	s.mux.HandleFunc("/api/v1/kb/proposals/revise", s.handleKBProposalRevise)
	s.mux.HandleFunc("/api/v1/kb/proposals/ack", s.handleKBProposalAck)
	s.mux.HandleFunc("/api/v1/kb/proposals/comment", s.handleKBProposalComment)
	s.mux.HandleFunc("/api/v1/kb/proposals/thread", s.handleKBProposalThread)
	s.mux.HandleFunc("/api/v1/kb/proposals/start-vote", s.handleKBProposalStartVote)
	s.mux.HandleFunc("/api/v1/kb/proposals/vote", s.handleKBProposalVote)
	s.mux.HandleFunc("/api/v1/kb/proposals/apply", s.handleKBProposalApply)
	s.mux.HandleFunc("/api/v1/ganglia/forge", s.handleGangliaForge)
	s.mux.HandleFunc("/api/v1/ganglia/browse", s.handleGangliaBrowse)
	s.mux.HandleFunc("/api/v1/ganglia/get", s.handleGangliaGet)
	s.mux.HandleFunc("/api/v1/ganglia/integrate", s.handleGangliaIntegrate)
	s.mux.HandleFunc("/api/v1/ganglia/rate", s.handleGangliaRate)
	s.mux.HandleFunc("/api/v1/ganglia/integrations", s.handleGangliaIntegrations)
	s.mux.HandleFunc("/api/v1/ganglia/ratings", s.handleGangliaRatings)
	s.mux.HandleFunc("/api/v1/ganglia/protocol", s.handleGangliaProtocol)
	s.mux.HandleFunc("/api/v1/colony/status", s.handleAPIColonyStatus)
	s.mux.HandleFunc("/api/v1/colony/directory", s.handleAPIColonyDirectory)
	s.mux.HandleFunc("/api/v1/colony/chronicle", s.handleAPIColonyChronicle)
	s.mux.HandleFunc("/api/v1/colony/banished", s.handleAPIColonyBanished)
	s.mux.HandleFunc("/api/v1/governance/docs", s.handleGovernanceDocs)
	s.mux.HandleFunc("/api/v1/governance/proposals", s.handleGovernanceProposals)
	s.mux.HandleFunc("/api/v1/governance/proposals/create", s.handleAPIGovPropose)
	s.mux.HandleFunc("/api/v1/governance/proposals/cosign", s.handleAPIGovCosign)
	s.mux.HandleFunc("/api/v1/governance/proposals/vote", s.handleAPIGovVote)
	s.mux.HandleFunc("/api/v1/governance/overview", s.handleGovernanceOverview)
	s.mux.HandleFunc("/api/v1/governance/protocol", s.handleGovernanceProtocol)
	s.mux.HandleFunc("/api/v1/governance/laws", s.handleAPIGovLaws)
	s.mux.HandleFunc("/api/v1/governance/report", s.handleGovernanceReportCreate)
	s.mux.HandleFunc("/api/v1/governance/reports", s.handleGovernanceReports)
	s.mux.HandleFunc("/api/v1/governance/cases/open", s.handleGovernanceCaseOpen)
	s.mux.HandleFunc("/api/v1/governance/cases", s.handleGovernanceCases)
	s.mux.HandleFunc("/api/v1/governance/cases/verdict", s.handleGovernanceCaseVerdict)
	s.mux.HandleFunc("/api/v1/reputation/score", s.handleReputationScore)
	s.mux.HandleFunc("/api/v1/reputation/leaderboard", s.handleReputationLeaderboard)
	s.mux.HandleFunc("/api/v1/reputation/events", s.handleReputationEvents)
	s.mux.HandleFunc("/api/v1/ops/overview", s.handleOpsOverview)
	s.mux.HandleFunc("/api/v1/ops/product-overview", s.handleOpsProductOverview)
	s.mux.HandleFunc("/api/v1/monitor/agents/overview", s.handleMonitorAgentsOverview)
	s.mux.HandleFunc("/api/v1/monitor/agents/timeline", s.handleMonitorAgentsTimeline)
	s.mux.HandleFunc("/api/v1/monitor/agents/timeline/all", s.handleMonitorAgentsTimelineAll)
	s.mux.HandleFunc("/api/v1/monitor/communications", s.handleMonitorCommunications)
	s.mux.HandleFunc("/api/v1/monitor/meta", s.handleMonitorMeta)
	s.mux.HandleFunc("/api/v1/system/request-logs", s.handleRequestLogs)
	s.mux.HandleFunc("/api/v1/tasks/pi", s.handlePiTaskMeta)
	s.mux.HandleFunc("/api/v1/tasks/pi/claim", s.handlePiTaskClaim)
	s.mux.HandleFunc("/api/v1/tasks/pi/submit", s.handlePiTaskSubmit)
	s.mux.HandleFunc("/api/v1/tasks/pi/history", s.handlePiTaskHistory)
	s.mux.HandleFunc("/dashboard", s.handleDashboard)
	s.mux.HandleFunc("/dashboard/", s.handleDashboard)
	s.mux.HandleFunc("/skill.md", s.handleHostedSkill)
	s.mux.HandleFunc("/skill.json", s.handleHostedSkill)
	s.mux.HandleFunc("/heartbeat.md", s.handleHostedSkill)
	s.mux.HandleFunc("/knowledge-base.md", s.handleHostedSkill)
	s.mux.HandleFunc("/collab-mode.md", s.handleHostedSkill)
	s.mux.HandleFunc("/colony-tools.md", s.handleHostedSkill)
	s.mux.HandleFunc("/ganglia-stack.md", s.handleHostedSkill)
	s.mux.HandleFunc("/governance.md", s.handleHostedSkill)
	s.mux.HandleFunc("/upgrade-clawcolony.md", s.handleHostedSkill)
	s.mux.HandleFunc("/skills/", s.handleHostedSkill)
	s.mux.HandleFunc("/", s.handleNotFound)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if s.tianDaoInitErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "degraded",
			"time":   time.Now().UTC().Format(time.RFC3339),
			"error":  s.tianDaoInitErr.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	lawKey := strings.TrimSpace(s.cfg.TianDaoLawKey)
	if lawKey == "" {
		lawKey = s.tianDaoLaw.LawKey
	}
	lawVersion := s.cfg.TianDaoLawVersion
	if lawVersion <= 0 {
		lawVersion = s.tianDaoLaw.Version
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":              "clawcolony",
		"database_enabled":     s.cfg.DatabaseURL != "",
		"action_cost_consume":  s.cfg.ActionCostConsume,
		"tool_cost_rate_milli": s.cfg.ToolCostRateMilli,
		"tool_runtime_exec":    s.cfg.ToolRuntimeExec,
		"tool_sandbox_image":   strings.TrimSpace(s.cfg.ToolSandboxImage),
		"tool_t3_allow_hosts":  strings.TrimSpace(s.cfg.ToolT3AllowHosts),
		"tian_dao_law_key":     lawKey,
		"tian_dao_law_version": lawVersion,
		"world_tick_seconds":   int64(s.worldTickInterval() / time.Second),
	})
}

func (s *Server) handleTianDaoLaw(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	lawKey := strings.TrimSpace(s.cfg.TianDaoLawKey)
	if lawKey == "" {
		lawKey = s.tianDaoLaw.LawKey
	}
	item, err := s.store.GetTianDaoLaw(r.Context(), lawKey)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var manifest map[string]any
	_ = json.Unmarshal([]byte(item.ManifestJSON), &manifest)
	writeJSON(w, http.StatusOK, map[string]any{
		"item":     item,
		"manifest": manifest,
	})
}

func (s *Server) handleWorldTickStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.worldTickMu.Lock()
	defer s.worldTickMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"tick_id":              s.worldTickID,
		"last_tick_at":         s.worldTickAt,
		"last_duration_ms":     s.worldTickDurMS,
		"last_error":           s.worldTickErr,
		"tick_interval_sec":    int64(s.worldTickInterval() / time.Second),
		"action_cost_consume":  s.cfg.ActionCostConsume,
		"tian_dao_law_key":     s.tianDaoLaw.LawKey,
		"tian_dao_law_version": s.tianDaoLaw.Version,
		"tian_dao_law_sha256":  s.tianDaoLaw.ManifestSHA256,
		"tian_dao_law_updated": s.tianDaoLaw.CreatedAt,
		"frozen":               s.worldFrozen,
		"freeze_reason":        s.worldFreezeReason,
		"freeze_since":         s.worldFreezeAt,
		"freeze_tick_id":       s.worldFreezeTickID,
		"freeze_total_users":   s.worldFreezeTotal,
		"freeze_at_risk_users": s.worldFreezeAtRisk,
		"freeze_threshold_pct": s.worldFreezeThreshold,
	})
}

func (s *Server) handleWorldFreezeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.worldTickMu.Lock()
	defer s.worldTickMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"frozen":               s.worldFrozen,
		"freeze_reason":        s.worldFreezeReason,
		"freeze_since":         s.worldFreezeAt,
		"freeze_tick_id":       s.worldFreezeTickID,
		"freeze_total_users":   s.worldFreezeTotal,
		"freeze_at_risk_users": s.worldFreezeAtRisk,
		"freeze_threshold_pct": s.worldFreezeThreshold,
		"tick_id":              s.worldTickID,
		"last_tick_at":         s.worldTickAt,
	})
}

const (
	worldFreezeRescueModeAtRisk   = "at_risk"
	worldFreezeRescueModeSelected = "selected"
	worldFreezeRescueMaxUsers     = 500
	worldFreezeRescueMaxAmount    = int64(1_000_000_000)
)

type worldFreezeRescueRequest struct {
	Mode    string   `json:"mode"`
	Amount  int64    `json:"amount"`
	UserIDs []string `json:"user_ids"`
	DryRun  bool     `json:"dry_run"`
}

type worldFreezeRescueResultItem struct {
	UserID         string `json:"user_id"`
	BalanceBefore  int64  `json:"balance_before"`
	BalanceAfter   int64  `json:"balance_after"`
	RechargeAmount int64  `json:"recharge_amount"`
	Applied        bool   `json:"applied"`
	Error          string `json:"error,omitempty"`
}

func normalizeWorldFreezeRescueMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case worldFreezeRescueModeAtRisk:
		return worldFreezeRescueModeAtRisk
	case worldFreezeRescueModeSelected:
		return worldFreezeRescueModeSelected
	default:
		return ""
	}
}

func normalizeDistinctUserIDs(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, it := range raw {
		uid := strings.TrimSpace(it)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		out = append(out, uid)
	}
	sort.Strings(out)
	return out
}

func isLoopbackRemoteAddr(remoteAddr string) bool {
	raw := strings.TrimSpace(remoteAddr)
	if raw == "" {
		return false
	}
	host := raw
	if h, _, err := net.SplitHostPort(raw); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func safeInt64Add(a, b int64) (int64, bool) {
	if b > 0 && a > (math.MaxInt64-b) {
		return 0, false
	}
	if b < 0 && a < (math.MinInt64-b) {
		return 0, false
	}
	return a + b, true
}

func (s *Server) handleWorldFreezeRescue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !isLoopbackRemoteAddr(r.RemoteAddr) {
		expected := strings.TrimSpace(s.cfg.InternalSyncToken)
		got := internalSyncTokenFromRequest(r)
		if expected == "" {
			writeError(w, http.StatusUnauthorized, "non-loopback requests require internal sync token configuration")
			return
		}
		if got == "" || got != expected {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	}
	var req worldFreezeRescueRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := normalizeWorldFreezeRescueMode(req.Mode)
	if mode == "" {
		if strings.TrimSpace(req.Mode) == "" {
			mode = worldFreezeRescueModeAtRisk
		} else {
			writeError(w, http.StatusBadRequest, "mode must be one of: at_risk, selected")
			return
		}
	}
	if req.Amount <= 0 || req.Amount > worldFreezeRescueMaxAmount {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("amount must be in [1, %d]", worldFreezeRescueMaxAmount))
		return
	}

	selectedUserIDs := normalizeDistinctUserIDs(req.UserIDs)
	if mode == worldFreezeRescueModeSelected && len(selectedUserIDs) == 0 {
		writeError(w, http.StatusBadRequest, "user_ids is required when mode=selected")
		return
	}
	if len(selectedUserIDs) > worldFreezeRescueMaxUsers {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("user_ids exceeds max users: %d", worldFreezeRescueMaxUsers))
		return
	}
	for _, uid := range selectedUserIDs {
		if isSystemTokenUserID(uid) {
			writeError(w, http.StatusBadRequest, "system accounts cannot be rescued")
			return
		}
	}

	accounts, err := s.store.ListTokenAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	balanceByUser := make(map[string]int64, len(accounts))
	totalUsers := 0
	atRiskBefore := 0
	for _, it := range accounts {
		uid := strings.TrimSpace(it.BotID)
		if isExcludedTokenUserID(uid) {
			continue
		}
		balanceByUser[uid] = it.Balance
		totalUsers++
		if it.Balance <= 0 {
			atRiskBefore++
		}
	}

	targetUsers := make([]string, 0)
	unknownUserIDs := make([]string, 0)
	truncatedUsers := 0
	if mode == worldFreezeRescueModeAtRisk {
		for uid, bal := range balanceByUser {
			if bal <= 0 {
				targetUsers = append(targetUsers, uid)
			}
		}
		sort.Strings(targetUsers)
		if len(targetUsers) > worldFreezeRescueMaxUsers {
			truncatedUsers = len(targetUsers) - worldFreezeRescueMaxUsers
			targetUsers = targetUsers[:worldFreezeRescueMaxUsers]
		}
	} else {
		targetUsers = make([]string, 0, len(selectedUserIDs))
		for _, uid := range selectedUserIDs {
			if _, ok := balanceByUser[uid]; !ok {
				unknownUserIDs = append(unknownUserIDs, uid)
				continue
			}
			targetUsers = append(targetUsers, uid)
		}
		if len(unknownUserIDs) > 0 {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("some user_ids are not found in token accounts: %s", strings.Join(unknownUserIDs, ",")))
			return
		}
	}

	if len(targetUsers) == 0 {
		writeError(w, http.StatusBadRequest, "no target users matched current rescue mode")
		return
	}
	for _, uid := range targetUsers {
		if isSystemTokenUserID(uid) {
			writeError(w, http.StatusBadRequest, "system accounts cannot be rescued")
			return
		}
	}
	if mode == worldFreezeRescueModeSelected && len(targetUsers) > worldFreezeRescueMaxUsers {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("target users exceeds max users: %d", worldFreezeRescueMaxUsers))
		return
	}

	simulatedBalances := make(map[string]int64, len(balanceByUser))
	for uid, bal := range balanceByUser {
		simulatedBalances[uid] = bal
	}
	items := make([]worldFreezeRescueResultItem, 0, len(targetUsers))
	appliedUsers := 0
	evalErr := ""
	itemIndex := make(map[string]int, len(targetUsers))
	payouts := make(map[string]int64, len(targetUsers))
	for _, uid := range targetUsers {
		before := simulatedBalances[uid]
		item := worldFreezeRescueResultItem{
			UserID:         uid,
			BalanceBefore:  before,
			BalanceAfter:   before,
			RechargeAmount: req.Amount,
			Applied:        false,
		}
		if req.DryRun {
			after, ok := safeInt64Add(before, req.Amount)
			if !ok {
				item.Error = "balance overflow in dry_run simulation"
				items = append(items, item)
				continue
			}
			item.BalanceAfter = after
			simulatedBalances[uid] = item.BalanceAfter
			items = append(items, item)
			continue
		}
		if err := s.ensureUserAlive(r.Context(), uid); err != nil {
			item.Error = err.Error()
			items = append(items, item)
			continue
		}
		after, ok := safeInt64Add(before, req.Amount)
		if !ok {
			item.Error = "token balance overflow"
			items = append(items, item)
			continue
		}
		itemIndex[uid] = len(items)
		item.BalanceAfter = after
		items = append(items, item)
		payouts[uid] = req.Amount
	}
	if !req.DryRun && len(payouts) > 0 {
		_, credits, err := s.distributeFromTreasury(r.Context(), payouts)
		if err != nil {
			if errors.Is(err, store.ErrInsufficientBalance) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for uid, ledger := range credits {
			idx, ok := itemIndex[uid]
			if !ok {
				continue
			}
			items[idx].BalanceAfter = ledger.BalanceAfter
			items[idx].RechargeAmount = ledger.Amount
			items[idx].Applied = true
			appliedUsers++
			simulatedBalances[uid] = ledger.BalanceAfter
		}
	}

	atRiskAfter := 0
	totalUsersAfter := totalUsers
	if req.DryRun {
		for _, bal := range simulatedBalances {
			if bal <= 0 {
				atRiskAfter++
			}
		}
	} else {
		afterAccounts, err := s.store.ListTokenAccounts(r.Context())
		if err != nil {
			evalErr = strings.TrimSpace(err.Error())
			atRiskAfter = 0
			totalUsersAfter = 0
			for _, bal := range simulatedBalances {
				totalUsersAfter++
				if bal <= 0 {
					atRiskAfter++
				}
			}
		} else {
			atRiskAfter = 0
			totalUsersAfter = 0
			for _, it := range afterAccounts {
				uid := strings.TrimSpace(it.BotID)
				if isExcludedTokenUserID(uid) {
					continue
				}
				totalUsersAfter++
				if it.Balance <= 0 {
					atRiskAfter++
				}
			}
		}
	}
	threshold := s.currentExtinctionThresholdPct()
	triggeredBefore := totalUsers > 0 && atRiskBefore*100 >= totalUsers*threshold
	triggeredAfter := totalUsersAfter > 0 && atRiskAfter*100 >= totalUsersAfter*threshold
	if !req.DryRun {
		s.worldTickMu.Lock()
		tickID := s.worldTickID
		s.worldTickMu.Unlock()
		if _, err := s.applyExtinctionGuard(r.Context(), tickID); err != nil {
			if strings.TrimSpace(evalErr) != "" {
				evalErr = evalErr + " | " + err.Error()
			} else {
				evalErr = err.Error()
			}
		}
	}

	s.worldTickMu.Lock()
	worldFrozen := s.worldFrozen
	worldFreezeReason := s.worldFreezeReason
	worldFreezeTickID := s.worldFreezeTickID
	worldTickID := s.worldTickID
	s.worldTickMu.Unlock()
	failedUsers := 0
	for _, it := range items {
		if strings.TrimSpace(it.Error) != "" {
			failedUsers++
		}
	}
	simulatedUsers := 0
	if req.DryRun {
		simulatedUsers = len(targetUsers) - failedUsers
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mode":                mode,
		"dry_run":             req.DryRun,
		"amount_per_user":     req.Amount,
		"targeted_users":      len(targetUsers),
		"truncated_users":     truncatedUsers,
		"applied_users":       appliedUsers,
		"simulated_users":     simulatedUsers,
		"failed_users":        failedUsers,
		"total_users":         totalUsers,
		"total_users_after":   totalUsersAfter,
		"threshold_pct":       threshold,
		"before":              map[string]any{"at_risk_users": atRiskBefore, "triggered": triggeredBefore},
		"after_estimate":      map[string]any{"at_risk_users": atRiskAfter, "triggered": triggeredAfter},
		"world_frozen":        worldFrozen,
		"world_tick_id":       worldTickID,
		"world_freeze_tick":   worldFreezeTickID,
		"world_freeze_reason": worldFreezeReason,
		"eval_error":          evalErr,
		"items":               items,
	})
}

func (s *Server) handleWorldTickHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.store.ListWorldTicks(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleWorldTickChainVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 500)
	items, err := s.store.ListWorldTicks(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(items) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"checked":     0,
			"head_tick":   int64(0),
			"head_hash":   "",
			"legacy_fill": 0,
		})
		return
	}
	// ListWorldTicks returns newest first; chain verification must be oldest -> newest.
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	prevHash := ""
	legacyFill := 0
	for idx, it := range items {
		storedPrev := strings.TrimSpace(it.PrevHash)
		if storedPrev == "" && prevHash != "" {
			storedPrev = prevHash
			legacyFill++
		}
		if storedPrev != prevHash {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":             false,
				"checked":        idx,
				"head_tick":      items[len(items)-1].TickID,
				"head_hash":      strings.TrimSpace(items[len(items)-1].EntryHash),
				"legacy_fill":    legacyFill,
				"mismatch_tick":  it.TickID,
				"mismatch_field": "prev_hash",
				"expected":       prevHash,
				"actual":         storedPrev,
			})
			return
		}
		expectedHash := store.ComputeWorldTickHash(it, storedPrev)
		storedHash := strings.TrimSpace(it.EntryHash)
		if storedHash == "" {
			storedHash = expectedHash
			legacyFill++
		}
		if storedHash != expectedHash {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":             false,
				"checked":        idx,
				"head_tick":      items[len(items)-1].TickID,
				"head_hash":      strings.TrimSpace(items[len(items)-1].EntryHash),
				"legacy_fill":    legacyFill,
				"mismatch_tick":  it.TickID,
				"mismatch_field": "entry_hash",
				"expected":       expectedHash,
				"actual":         storedHash,
			})
			return
		}
		prevHash = storedHash
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"checked":     len(items),
		"head_tick":   items[len(items)-1].TickID,
		"head_hash":   prevHash,
		"legacy_fill": legacyFill,
	})
}

type worldTickReplayRequest struct {
	SourceTickID int64 `json:"source_tick_id"`
}

func (s *Server) handleWorldTickReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req worldTickReplayRequest
	if r.ContentLength > 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.SourceTickID <= 0 {
		req.SourceTickID = parseInt64(r.URL.Query().Get("source_tick_id"))
	}
	if req.SourceTickID <= 0 {
		s.worldTickMu.Lock()
		req.SourceTickID = s.worldTickID
		s.worldTickMu.Unlock()
	}
	if req.SourceTickID <= 0 {
		writeError(w, http.StatusBadRequest, "source_tick_id is required")
		return
	}
	replayTickID := s.runWorldTickReplay(r.Context(), req.SourceTickID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":         "accepted",
		"source_tick_id": req.SourceTickID,
		"replay_tick_id": replayTickID,
	})
}

func (s *Server) handleWorldTickSteps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	tickID := parseInt64(r.URL.Query().Get("tick_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.store.ListWorldTickSteps(r.Context(), tickID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tick_id": tickID,
		"items":   items,
	})
}

func (s *Server) handleWorldLifeState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := queryUserID(r)
	state := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("state")))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.store.ListUserLifeStates(r.Context(), userID, state, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"state":   state,
		"items":   items,
	})
}

func (s *Server) handleWorldLifeStateTransitions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	fromState, err := parseLifeStateQueryValue(r.URL.Query().Get("from_state"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	toState, err := parseLifeStateQueryValue(r.URL.Query().Get("to_state"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	filter := store.UserLifeStateTransitionFilter{
		UserID:       queryUserID(r),
		FromState:    fromState,
		ToState:      toState,
		TickID:       parseInt64(r.URL.Query().Get("tick_id")),
		SourceModule: strings.TrimSpace(r.URL.Query().Get("source_module")),
		ActorUserID:  strings.TrimSpace(r.URL.Query().Get("actor_user_id")),
		Limit:        parseLimit(r.URL.Query().Get("limit"), 200),
	}
	items, err := s.store.ListUserLifeStateTransitions(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load life-state transitions")
		return
	}
	resp := map[string]any{"items": items}
	if filter.UserID != "" {
		resp["user_id"] = filter.UserID
	}
	if filter.FromState != "" {
		resp["from_state"] = filter.FromState
	}
	if filter.ToState != "" {
		resp["to_state"] = filter.ToState
	}
	if filter.TickID > 0 {
		resp["tick_id"] = filter.TickID
	}
	if filter.SourceModule != "" {
		resp["source_module"] = filter.SourceModule
	}
	if filter.ActorUserID != "" {
		resp["actor_user_id"] = filter.ActorUserID
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWorldCostEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := queryUserID(r)
	tickID := parseInt64(r.URL.Query().Get("tick_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	if tickID > 0 && strings.TrimSpace(r.URL.Query().Get("limit")) == "" {
		// Replay queries usually need a broader scan window when tick filter is active.
		limit = 2000
	}
	items, err := s.store.ListCostEvents(r.Context(), userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tickID > 0 {
		filtered := make([]store.CostEvent, 0, len(items))
		for _, it := range items {
			if it.TickID == tickID {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"tick_id": tickID,
		"items":   items,
	})
}

func (s *Server) handleWorldCostSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := queryUserID(r)
	limit := parseLimit(r.URL.Query().Get("limit"), 500)
	includeSystem := parseBoolFlag(r.URL.Query().Get("include_system"))
	items, err := s.store.ListCostEvents(r.Context(), userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type agg struct {
		Count  int64 `json:"count"`
		Amount int64 `json:"amount"`
		Units  int64 `json:"units"`
	}
	byType := map[string]agg{}
	var totalCount, totalAmount, totalUnits int64
	for _, it := range items {
		if !includeSystem && isExcludedTokenUserID(it.UserID) {
			continue
		}
		key := strings.TrimSpace(it.CostType)
		if key == "" {
			key = "unknown"
		}
		a := byType[key]
		a.Count++
		a.Amount += it.Amount
		a.Units += it.Units
		byType[key] = a
		totalCount++
		totalAmount += it.Amount
		totalUnits += it.Units
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":        userID,
		"limit":          limit,
		"include_system": includeSystem,
		"totals": map[string]any{
			"count":  totalCount,
			"amount": totalAmount,
			"units":  totalUnits,
		},
		"by_type": byType,
	})
}

func toolTier(costType string) string {
	ct := strings.TrimSpace(strings.ToLower(costType))
	switch ct {
	case "tool.bot.upgrade":
		return "T3"
	case "tool.runtime.t3":
		return "T3"
	case "tool.openclaw.register", "tool.openclaw.redeploy", "tool.openclaw.delete":
		return "T2"
	case "tool.runtime.t2":
		return "T2"
	case "tool.openclaw.restart":
		return "T1"
	case "tool.runtime.t1":
		return "T1"
	case "tool.runtime.t0":
		return "T0"
	default:
		return "T0"
	}
}

func toolTierLevel(tier string) int {
	switch strings.TrimSpace(strings.ToUpper(tier)) {
	case "T0":
		return 0
	case "T1":
		return 1
	case "T2":
		return 2
	case "T3":
		return 3
	default:
		return 0
	}
}

func maxAllowedToolTierForLifeState(state string) string {
	switch normalizeLifeStateForServer(state) {
	case "alive":
		return "T3"
	case "dying":
		return "T1"
	case "hibernated":
		return "NONE"
	case "dead":
		return "NONE"
	default:
		return "T3"
	}
}

func isToolTierAllowedForLifeState(state, tier string) bool {
	maxTier := maxAllowedToolTierForLifeState(state)
	if maxTier == "NONE" {
		return false
	}
	return toolTierLevel(tier) <= toolTierLevel(maxTier)
}

func (s *Server) ensureToolTierAllowed(ctx context.Context, userID, costType string) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || userID == clawWorldSystemID {
		return nil
	}
	state := "alive"
	if life, err := s.store.GetUserLifeState(ctx, userID); err == nil {
		state = normalizeLifeStateForServer(life.State)
	}
	tier := toolTier(costType)
	if isToolTierAllowedForLifeState(state, tier) {
		return nil
	}
	maxTier := maxAllowedToolTierForLifeState(state)
	if maxTier == "NONE" {
		return fmt.Errorf("tool tier %s is not allowed in %s state", tier, state)
	}
	return fmt.Errorf("tool tier %s is not allowed in %s state (max allowed: %s)", tier, state, maxTier)
}

func (s *Server) handleWorldToolAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := queryUserID(r)
	limit := parseLimit(r.URL.Query().Get("limit"), 500)
	tierFilter := strings.TrimSpace(strings.ToUpper(r.URL.Query().Get("tier")))
	if tierFilter != "" && tierFilter != "T0" && tierFilter != "T1" && tierFilter != "T2" && tierFilter != "T3" {
		writeError(w, http.StatusBadRequest, "tier must be T0|T1|T2|T3")
		return
	}
	items, err := s.store.ListCostEvents(r.Context(), userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type toolAuditItem struct {
		ID        int64     `json:"id"`
		UserID    string    `json:"user_id"`
		TickID    int64     `json:"tick_id"`
		CostType  string    `json:"cost_type"`
		Tier      string    `json:"tier"`
		Amount    int64     `json:"amount"`
		Units     int64     `json:"units"`
		MetaJSON  string    `json:"meta_json,omitempty"`
		CreatedAt time.Time `json:"created_at"`
	}
	out := make([]toolAuditItem, 0, len(items))
	byTier := map[string]int64{"T0": 0, "T1": 0, "T2": 0, "T3": 0}
	for _, it := range items {
		if !strings.HasPrefix(strings.TrimSpace(strings.ToLower(it.CostType)), "tool.") {
			continue
		}
		tier := toolTier(it.CostType)
		if tierFilter != "" && tier != tierFilter {
			continue
		}
		byTier[tier]++
		out = append(out, toolAuditItem{
			ID:        it.ID,
			UserID:    it.UserID,
			TickID:    it.TickID,
			CostType:  it.CostType,
			Tier:      tier,
			Amount:    it.Amount,
			Units:     it.Units,
			MetaJSON:  it.MetaJSON,
			CreatedAt: it.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"tier":    tierFilter,
		"limit":   limit,
		"count":   len(out),
		"by_tier": byTier,
		"items":   out,
	})
}

func (s *Server) handleWorldCostAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := queryUserID(r)
	settings, _, _ := s.getWorldCostAlertSettings(r.Context())
	limit := parseLimit(r.URL.Query().Get("limit"), settings.ScanLimit)
	thresholdAmount := parseInt64(r.URL.Query().Get("threshold_amount"))
	if thresholdAmount <= 0 {
		thresholdAmount = settings.ThresholdAmount
	}
	topUsers := parseLimit(r.URL.Query().Get("top_users"), settings.TopUsers)
	includeSystem := parseBoolFlag(r.URL.Query().Get("include_system"))
	items, err := s.queryWorldCostAlerts(r.Context(), userID, limit, thresholdAmount, topUsers, includeSystem)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":          userID,
		"limit":            limit,
		"threshold_amount": thresholdAmount,
		"top_users":        topUsers,
		"include_system":   includeSystem,
		"settings":         settings,
		"items":            items,
	})
}

func (s *Server) queryWorldCostAlerts(ctx context.Context, userID string, limit int, thresholdAmount int64, topUsers int, includeSystem bool) ([]worldCostAlertItem, error) {
	items, err := s.store.ListCostEvents(ctx, userID, limit)
	if err != nil {
		return nil, err
	}
	type userAgg struct {
		UserID        string
		EventCount    int64
		Amount        int64
		Units         int64
		TopCostType   string
		TopCostAmount int64
	}
	byUser := map[string]*userAgg{}
	byUserType := map[string]map[string]int64{}
	for _, it := range items {
		uid := strings.TrimSpace(it.UserID)
		if uid == "" {
			continue
		}
		if !includeSystem && isExcludedTokenUserID(uid) {
			continue
		}
		a := byUser[uid]
		if a == nil {
			a = &userAgg{UserID: uid}
			byUser[uid] = a
			byUserType[uid] = map[string]int64{}
		}
		a.EventCount++
		a.Amount += it.Amount
		a.Units += it.Units
		costType := strings.TrimSpace(it.CostType)
		if costType == "" {
			costType = "unknown"
		}
		byUserType[uid][costType] += it.Amount
	}
	out := make([]worldCostAlertItem, 0, len(byUser))
	for uid, a := range byUser {
		typeMap := byUserType[uid]
		var topType string
		var topAmount int64
		for k, v := range typeMap {
			if v > topAmount || topType == "" {
				topType = k
				topAmount = v
			}
		}
		a.TopCostType = topType
		a.TopCostAmount = topAmount
		if a.Amount >= thresholdAmount {
			out = append(out, worldCostAlertItem{
				UserID:        a.UserID,
				EventCount:    a.EventCount,
				Amount:        a.Amount,
				Units:         a.Units,
				TopCostType:   a.TopCostType,
				TopCostAmount: a.TopCostAmount,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Amount == out[j].Amount {
			return out[i].UserID < out[j].UserID
		}
		return out[i].Amount > out[j].Amount
	})
	if len(out) > topUsers {
		out = out[:topUsers]
	}
	return out, nil
}

type worldCostAlertSettingsUpsertRequest struct {
	ThresholdAmount int64 `json:"threshold_amount"`
	TopUsers        int   `json:"top_users"`
	ScanLimit       int   `json:"scan_limit"`
	NotifyCooldownS int64 `json:"notify_cooldown_seconds"`
}

func (s *Server) defaultWorldCostAlertSettings() worldCostAlertSettings {
	return worldCostAlertSettings{
		ThresholdAmount: 100,
		TopUsers:        10,
		ScanLimit:       500,
		NotifyCooldownS: defaultCostAlertCooldownSeconds,
	}
}

func (s *Server) normalizeWorldCostAlertSettings(in worldCostAlertSettings) worldCostAlertSettings {
	if in.ThresholdAmount <= 0 {
		in.ThresholdAmount = 100
	}
	if in.TopUsers <= 0 {
		in.TopUsers = 10
	}
	if in.TopUsers > 500 {
		in.TopUsers = 500
	}
	if in.ScanLimit <= 0 {
		in.ScanLimit = 500
	}
	if in.ScanLimit > 500 {
		in.ScanLimit = 500
	}
	if in.NotifyCooldownS <= 0 {
		in.NotifyCooldownS = defaultCostAlertCooldownSeconds
	}
	if in.NotifyCooldownS < runtimeSchedulerMinCooldownSeconds {
		in.NotifyCooldownS = runtimeSchedulerMinCooldownSeconds
	}
	if in.NotifyCooldownS > runtimeSchedulerMaxCooldownSeconds {
		in.NotifyCooldownS = runtimeSchedulerMaxCooldownSeconds
	}
	return in
}

func (s *Server) getLegacyWorldCostAlertSettings(ctx context.Context) (worldCostAlertSettings, string, time.Time) {
	def := s.defaultWorldCostAlertSettings()
	item, err := s.store.GetWorldSetting(ctx, worldCostAlertSettingsKey)
	if err != nil {
		return def, "default", time.Time{}
	}
	var parsed worldCostAlertSettings
	if err := json.Unmarshal([]byte(item.Value), &parsed); err != nil {
		return def, "default", time.Time{}
	}
	return s.normalizeWorldCostAlertSettings(parsed), "db", item.UpdatedAt
}

func (s *Server) getWorldCostAlertSettings(ctx context.Context) (worldCostAlertSettings, string, time.Time) {
	legacy, source, updatedAt := s.getLegacyWorldCostAlertSettings(ctx)
	// Compatibility facade: legacy settings remain for threshold/top_users/scan_limit.
	// Effective cost alert cooldown is resolved from runtime scheduler settings.
	if runtimeCooldown, _, _ := s.runtimeCostAlertCooldown(ctx); runtimeCooldown > 0 {
		legacy.NotifyCooldownS = runtimeCooldown
	}
	return legacy, source, updatedAt
}

func (s *Server) runtimeCostAlertCooldown(ctx context.Context) (int64, string, time.Time) {
	item, source, updatedAt := s.getRuntimeSchedulerSettings(ctx)
	return item.CostAlertNotifyCooldownSeconds, source, updatedAt
}

func (s *Server) handleWorldCostAlertSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, source, updatedAt := s.getWorldCostAlertSettings(r.Context())
	_, runtimeSource, runtimeUpdatedAt := s.runtimeCostAlertCooldown(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"item":                       item,
		"source":                     source,
		"updated_at":                 updatedAt,
		"notify_cooldown_source":     runtimeSource,
		"notify_cooldown_updated_at": runtimeUpdatedAt,
	})
}

func (s *Server) handleWorldCostAlertSettingsUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req worldCostAlertSettingsUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item := s.normalizeWorldCostAlertSettings(worldCostAlertSettings{
		ThresholdAmount: req.ThresholdAmount,
		TopUsers:        req.TopUsers,
		ScanLimit:       req.ScanLimit,
		NotifyCooldownS: req.NotifyCooldownS,
	})
	// Runtime scheduler settings are the single source of truth for cost-alert cooldown.
	cooldownSource := "compat"
	if merged, _, _ := s.getWorldCostAlertSettings(r.Context()); merged.NotifyCooldownS > 0 {
		item.NotifyCooldownS = merged.NotifyCooldownS
	}
	if _, source, _ := s.runtimeCostAlertCooldown(r.Context()); strings.TrimSpace(source) != "" {
		cooldownSource = source
	}
	raw, err := json.Marshal(item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	saved, err := s.store.UpsertWorldSetting(r.Context(), store.WorldSetting{
		Key:   worldCostAlertSettingsKey,
		Value: string(raw),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"item":                       item,
		"updated_at":                 saved.UpdatedAt,
		"source":                     "db",
		"notify_cooldown_source":     cooldownSource,
		"notify_cooldown_managed_by": runtimeSchedulerSettingsKey,
		"notify_cooldown_ignored":    req.NotifyCooldownS > 0 && req.NotifyCooldownS != item.NotifyCooldownS,
	})
}

func (s *Server) defaultRuntimeSchedulerSettings() runtimeSchedulerSettings {
	autonomy := s.cfg.AutonomyReminderIntervalTicks
	if autonomy < 0 {
		autonomy = 0
	}
	if autonomy > runtimeSchedulerMaxIntervalTicks {
		autonomy = runtimeSchedulerMaxIntervalTicks
	}
	community := s.cfg.CommunityCommReminderIntervalTicks
	if community < 0 {
		community = 0
	}
	if community > runtimeSchedulerMaxIntervalTicks {
		community = runtimeSchedulerMaxIntervalTicks
	}
	kbEnroll := s.cfg.KBEnrollmentReminderIntervalTicks
	if kbEnroll < 0 {
		kbEnroll = 0
	}
	if kbEnroll > runtimeSchedulerMaxIntervalTicks {
		kbEnroll = runtimeSchedulerMaxIntervalTicks
	}
	kbVote := s.cfg.KBVotingReminderIntervalTicks
	if kbVote < 0 {
		kbVote = 0
	}
	if kbVote > runtimeSchedulerMaxIntervalTicks {
		kbVote = runtimeSchedulerMaxIntervalTicks
	}
	return runtimeSchedulerSettings{
		AutonomyReminderIntervalTicks:      autonomy,
		CommunityCommReminderIntervalTicks: community,
		KBEnrollmentReminderIntervalTicks:  kbEnroll,
		KBVotingReminderIntervalTicks:      kbVote,
		CostAlertNotifyCooldownSeconds:     defaultCostAlertCooldownSeconds,
		LowTokenAlertCooldownSeconds:       0,
	}
}

func clampInt64(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func timePtr(t time.Time) *time.Time {
	v := t.UTC()
	return &v
}

func secureStringEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func normalizeRuntimeSchedulerSettings(in, fallback runtimeSchedulerSettings) runtimeSchedulerSettings {
	out := fallback
	// Missing fields are handled by pre-filling `in` with fallback before JSON unmarshal.
	out.AutonomyReminderIntervalTicks = clampInt64(in.AutonomyReminderIntervalTicks, 0, runtimeSchedulerMaxIntervalTicks)
	out.CommunityCommReminderIntervalTicks = clampInt64(in.CommunityCommReminderIntervalTicks, 0, runtimeSchedulerMaxIntervalTicks)
	out.KBEnrollmentReminderIntervalTicks = clampInt64(in.KBEnrollmentReminderIntervalTicks, 0, runtimeSchedulerMaxIntervalTicks)
	out.KBVotingReminderIntervalTicks = clampInt64(in.KBVotingReminderIntervalTicks, 0, runtimeSchedulerMaxIntervalTicks)
	// Read-time normalization is intentionally more permissive than API writes:
	// invalid manual DB values are clamped, while upsert requests are rejected by validateRuntimeSchedulerSettings.
	// cost alert cooldown does not use 0 as a disable value; 0 keeps fallback/default.
	// Values in (0,30) are clamped to 30 for read-time robustness against manual DB edits.
	if in.CostAlertNotifyCooldownSeconds > 0 {
		out.CostAlertNotifyCooldownSeconds = clampInt64(in.CostAlertNotifyCooldownSeconds, runtimeSchedulerMinCooldownSeconds, runtimeSchedulerMaxCooldownSeconds)
	}
	// low-token cooldown keeps 0 as an explicit "disabled" value for compatibility.
	if in.LowTokenAlertCooldownSeconds <= 0 {
		out.LowTokenAlertCooldownSeconds = 0
	} else {
		out.LowTokenAlertCooldownSeconds = clampInt64(in.LowTokenAlertCooldownSeconds, runtimeSchedulerMinCooldownSeconds, runtimeSchedulerMaxCooldownSeconds)
	}
	return out
}

func (s *Server) getRuntimeSchedulerCache(now time.Time) (runtimeSchedulerSettings, string, time.Time, bool) {
	s.runtimeSchedulerMu.RLock()
	defer s.runtimeSchedulerMu.RUnlock()
	src := strings.TrimSpace(s.runtimeSchedulerSrc)
	if (src != "db" && src != "compat" && src != "compat_invalid_db") || s.runtimeSchedulerTS.IsZero() {
		return runtimeSchedulerSettings{}, "", time.Time{}, false
	}
	if now.Sub(s.runtimeSchedulerTS) > runtimeSchedulerCacheTTL {
		return runtimeSchedulerSettings{}, "", time.Time{}, false
	}
	return s.runtimeSchedulerItem, s.runtimeSchedulerSrc, s.runtimeSchedulerAt, true
}

func (s *Server) setRuntimeSchedulerCache(item runtimeSchedulerSettings, source string, updatedAt, now time.Time) {
	s.runtimeSchedulerMu.Lock()
	defer s.runtimeSchedulerMu.Unlock()
	s.runtimeSchedulerItem = item
	s.runtimeSchedulerSrc = strings.TrimSpace(source)
	s.runtimeSchedulerAt = updatedAt
	s.runtimeSchedulerTS = now
}

func validateRuntimeSchedulerSettings(in runtimeSchedulerSettings) error {
	if in.AutonomyReminderIntervalTicks < 0 || in.AutonomyReminderIntervalTicks > runtimeSchedulerMaxIntervalTicks {
		return fmt.Errorf("autonomy_reminder_interval_ticks must be in [0, %d]", runtimeSchedulerMaxIntervalTicks)
	}
	if in.CommunityCommReminderIntervalTicks < 0 || in.CommunityCommReminderIntervalTicks > runtimeSchedulerMaxIntervalTicks {
		return fmt.Errorf("community_comm_reminder_interval_ticks must be in [0, %d]", runtimeSchedulerMaxIntervalTicks)
	}
	if in.KBEnrollmentReminderIntervalTicks < 0 || in.KBEnrollmentReminderIntervalTicks > runtimeSchedulerMaxIntervalTicks {
		return fmt.Errorf("kb_enrollment_reminder_interval_ticks must be in [0, %d]", runtimeSchedulerMaxIntervalTicks)
	}
	if in.KBVotingReminderIntervalTicks < 0 || in.KBVotingReminderIntervalTicks > runtimeSchedulerMaxIntervalTicks {
		return fmt.Errorf("kb_voting_reminder_interval_ticks must be in [0, %d]", runtimeSchedulerMaxIntervalTicks)
	}
	// Upsert uses strict bounds; read-time normalization separately clamps manual DB edits.
	if in.CostAlertNotifyCooldownSeconds < runtimeSchedulerMinCooldownSeconds || in.CostAlertNotifyCooldownSeconds > runtimeSchedulerMaxCooldownSeconds {
		return fmt.Errorf("cost_alert_notify_cooldown_seconds must be in [%d, %d]", runtimeSchedulerMinCooldownSeconds, runtimeSchedulerMaxCooldownSeconds)
	}
	if in.LowTokenAlertCooldownSeconds != 0 && (in.LowTokenAlertCooldownSeconds < runtimeSchedulerMinCooldownSeconds || in.LowTokenAlertCooldownSeconds > runtimeSchedulerMaxCooldownSeconds) {
		return fmt.Errorf("low_token_alert_cooldown_seconds must be 0 or in [%d, %d]", runtimeSchedulerMinCooldownSeconds, runtimeSchedulerMaxCooldownSeconds)
	}
	return nil
}

func (s *Server) getRuntimeSchedulerSettings(ctx context.Context) (runtimeSchedulerSettings, string, time.Time) {
	now := time.Now().UTC()
	if cached, source, updatedAt, ok := s.getRuntimeSchedulerCache(now); ok {
		if source == "compat" {
			// Keep compat mode bound to live process config while still skipping DB reads.
			// This lets tests (and any runtime config mutators) observe current cfg defaults.
			return s.defaultRuntimeSchedulerSettings(), "compat", time.Time{}
		}
		return cached, source, updatedAt
	}
	compat := s.defaultRuntimeSchedulerSettings()
	item, err := s.store.GetWorldSetting(ctx, runtimeSchedulerSettingsKey)
	if err != nil || strings.TrimSpace(item.Value) == "" {
		s.setRuntimeSchedulerCache(compat, "compat", time.Time{}, now)
		return compat, "compat", time.Time{}
	}
	parsed := compat
	if err := json.Unmarshal([]byte(item.Value), &parsed); err != nil {
		s.setRuntimeSchedulerCache(compat, "compat_invalid_db", item.UpdatedAt, now)
		return compat, "compat_invalid_db", item.UpdatedAt
	}
	out := normalizeRuntimeSchedulerSettings(parsed, compat)
	s.setRuntimeSchedulerCache(out, "db", item.UpdatedAt, now)
	return out, "db", item.UpdatedAt
}

func (s *Server) handleRuntimeSchedulerSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, source, updatedAt := s.getRuntimeSchedulerSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"item":       item,
		"source":     source,
		"updated_at": updatedAt,
	})
}

func (s *Server) handleRuntimeSchedulerSettingsUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var item runtimeSchedulerSettings
	if err := decodeJSON(r, &item); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateRuntimeSchedulerSettings(item); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updatedAt, err := s.putSettingJSON(r.Context(), runtimeSchedulerSettingsKey, item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setRuntimeSchedulerCache(item, "db", updatedAt, time.Now().UTC())
	writeJSON(w, http.StatusAccepted, map[string]any{
		"item":       item,
		"source":     "db",
		"updated_at": updatedAt,
	})
}

func (s *Server) handleWorldCostAlertNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := queryUserID(r)
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	items, err := s.store.ListMailbox(r.Context(), clawWorldSystemID, "outbox", "", "[WORLD-COST-ALERT]", nil, nil, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type item struct {
		MailboxID int64     `json:"mailbox_id"`
		MessageID int64     `json:"message_id"`
		ToUserID  string    `json:"to_user_id"`
		Subject   string    `json:"subject"`
		Body      string    `json:"body"`
		SentAt    time.Time `json:"sent_at"`
	}
	out := make([]item, 0, len(items))
	for _, it := range items {
		if userID != "" && it.ToAddress != userID {
			continue
		}
		out = append(out, item{
			MailboxID: it.MailboxID,
			MessageID: it.MessageID,
			ToUserID:  it.ToAddress,
			Subject:   it.Subject,
			Body:      it.Body,
			SentAt:    it.SentAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id": userID,
		"items":   out,
	})
}

func (s *Server) shouldSendWorldCostAlert(userID string, amount int64, cooldown time.Duration, now time.Time) bool {
	s.alertNotifyMu.Lock()
	defer s.alertNotifyMu.Unlock()
	lastAt, seen := s.alertLastSent[userID]
	lastAmt := s.alertLastAmt[userID]
	if !seen {
		s.alertLastSent[userID] = now
		s.alertLastAmt[userID] = amount
		return true
	}
	if amount > lastAmt {
		s.alertLastSent[userID] = now
		s.alertLastAmt[userID] = amount
		return true
	}
	if now.Sub(lastAt) >= cooldown {
		s.alertLastSent[userID] = now
		s.alertLastAmt[userID] = amount
		return true
	}
	return false
}

func (s *Server) runWorldCostAlertNotifications(ctx context.Context, tickID int64) error {
	settings, _, _ := s.getWorldCostAlertSettings(ctx)
	items, err := s.queryWorldCostAlerts(ctx, "", settings.ScanLimit, settings.ThresholdAmount, settings.TopUsers, false)
	if err != nil {
		return err
	}
	cooldown := time.Duration(settings.NotifyCooldownS) * time.Second
	now := time.Now().UTC()
	for _, it := range items {
		uid := strings.TrimSpace(it.UserID)
		// queryWorldCostAlerts currently guarantees non-empty user IDs; keep
		// this defensive guard in case that invariant changes later.
		if uid == "" {
			continue
		}
		if !s.shouldSendWorldCostAlert(it.UserID, it.Amount, cooldown, now) {
			continue
		}
		subject := fmt.Sprintf("[WORLD-COST-ALERT] user=%s amount=%d threshold=%d"+refTag(skillGovernance), it.UserID, it.Amount, settings.ThresholdAmount)
		body := fmt.Sprintf(
			"tick_id=%d\nuser_id=%s\namount=%d\nunits=%d\nevent_count=%d\ntop_cost_type=%s\ntop_cost_amount=%d\nthreshold_amount=%d\ntop_users=%d\nscan_limit=%d\nnotify_cooldown_seconds=%d\n\nThis is an observation alert. No action was forcibly blocked.",
			tickID,
			it.UserID,
			it.Amount,
			it.Units,
			it.EventCount,
			it.TopCostType,
			it.TopCostAmount,
			settings.ThresholdAmount,
			settings.TopUsers,
			settings.ScanLimit,
			settings.NotifyCooldownS,
		)
		if _, sendErr := s.store.SendMail(ctx, store.MailSendInput{
			From:    clawWorldSystemID,
			To:      []string{it.UserID},
			Subject: subject,
			Body:    body,
		}); sendErr != nil {
			log.Printf("world_cost_alert_notify_failed user_id=%s err=%v", it.UserID, sendErr)
		}
	}
	return nil
}

func clampPct(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func pct(part, total int) int {
	if total <= 0 || part <= 0 {
		return 0
	}
	if part >= total {
		return 100
	}
	return (part*100 + total/2) / total
}

func intensity(events, total, perCapTarget int) int {
	if total <= 0 || perCapTarget <= 0 || events <= 0 {
		return 0
	}
	den := total * perCapTarget
	if den <= 0 {
		return 0
	}
	if events >= den {
		return 100
	}
	return clampPct((events*100 + den/2) / den)
}

func weightedScore(coveragePct, intensityPct int) int {
	coveragePct = clampPct(coveragePct)
	intensityPct = clampPct(intensityPct)
	return clampPct((coveragePct*70 + intensityPct*30 + 50) / 100)
}

func sortedSetKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		if strings.TrimSpace(k) == "" {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func missingUsersFromSet(active []string, include map[string]struct{}) []string {
	out := make([]string, 0)
	for _, uid := range active {
		if _, ok := include[uid]; ok {
			continue
		}
		out = append(out, uid)
	}
	sort.Strings(out)
	return out
}

func (s *Server) defaultWorldEvolutionAlertSettings() worldEvolutionAlertSettings {
	return worldEvolutionAlertSettings{
		WindowMinutes:   60,
		MailScanLimit:   120,
		KBScanLimit:     300,
		WarnThreshold:   65,
		CriticalLevel:   45,
		NotifyCooldownS: int64((10 * time.Minute) / time.Second),
	}
}

func (s *Server) normalizeWorldEvolutionAlertSettings(in worldEvolutionAlertSettings) worldEvolutionAlertSettings {
	if in.WindowMinutes <= 0 {
		in.WindowMinutes = 60
	}
	if in.WindowMinutes > 24*60 {
		in.WindowMinutes = 24 * 60
	}
	if in.MailScanLimit <= 0 {
		in.MailScanLimit = 120
	}
	if in.MailScanLimit > 500 {
		in.MailScanLimit = 500
	}
	if in.KBScanLimit <= 0 {
		in.KBScanLimit = 300
	}
	if in.KBScanLimit > 1000 {
		in.KBScanLimit = 1000
	}
	if in.WarnThreshold <= 0 {
		in.WarnThreshold = 65
	}
	if in.WarnThreshold > 100 {
		in.WarnThreshold = 100
	}
	if in.CriticalLevel <= 0 {
		in.CriticalLevel = 45
	}
	if in.CriticalLevel > in.WarnThreshold {
		in.CriticalLevel = in.WarnThreshold
	}
	if in.NotifyCooldownS <= 0 {
		in.NotifyCooldownS = int64((10 * time.Minute) / time.Second)
	}
	if in.NotifyCooldownS < 30 {
		in.NotifyCooldownS = 30
	}
	if in.NotifyCooldownS > 86400 {
		in.NotifyCooldownS = 86400
	}
	return in
}

func (s *Server) getWorldEvolutionAlertSettings(ctx context.Context) (worldEvolutionAlertSettings, string, time.Time) {
	def := s.defaultWorldEvolutionAlertSettings()
	item, err := s.store.GetWorldSetting(ctx, worldEvolutionAlertSettingsKey)
	if err != nil {
		return def, "default", time.Time{}
	}
	var parsed worldEvolutionAlertSettings
	if err := json.Unmarshal([]byte(item.Value), &parsed); err != nil {
		return def, "default", time.Time{}
	}
	return s.normalizeWorldEvolutionAlertSettings(parsed), "db", item.UpdatedAt
}

func (s *Server) listWorldEvolutionAlerts(snapshot worldEvolutionSnapshot, settings worldEvolutionAlertSettings) []worldEvolutionAlertItem {
	if snapshot.TotalUsers <= 0 {
		return []worldEvolutionAlertItem{{
			Category:  "overall",
			Severity:  "warning",
			Score:     0,
			Threshold: settings.WarnThreshold,
			Message:   "no active users found",
		}}
	}
	out := make([]worldEvolutionAlertItem, 0, 8)
	overallThreshold := settings.WarnThreshold
	overallSeverity := ""
	if snapshot.OverallScore < settings.CriticalLevel {
		overallSeverity = "critical"
		overallThreshold = settings.CriticalLevel
	} else if snapshot.OverallScore < settings.WarnThreshold {
		overallSeverity = "warning"
	}
	if overallSeverity != "" {
		out = append(out, worldEvolutionAlertItem{
			Category:  "overall",
			Severity:  overallSeverity,
			Score:     snapshot.OverallScore,
			Threshold: overallThreshold,
			Message:   "overall evolution score below target",
		})
	}
	keys := make([]string, 0, len(snapshot.KPIs))
	for k := range snapshot.KPIs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		it := snapshot.KPIs[k]
		threshold := settings.WarnThreshold
		severity := ""
		if it.Score < settings.CriticalLevel {
			severity = "critical"
			threshold = settings.CriticalLevel
		} else if it.Score < settings.WarnThreshold {
			severity = "warning"
		}
		if severity == "" {
			continue
		}
		out = append(out, worldEvolutionAlertItem{
			Category:  k,
			Severity:  severity,
			Score:     it.Score,
			Threshold: threshold,
			Message:   it.Note,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return out[i].Severity > out[j].Severity
		}
		if out[i].Score == out[j].Score {
			return out[i].Category < out[j].Category
		}
		return out[i].Score < out[j].Score
	})
	return out
}

func (s *Server) buildWorldEvolutionSnapshot(ctx context.Context, settings worldEvolutionAlertSettings, tickID int64) (worldEvolutionSnapshot, error) {
	active := s.activeUserIDs(ctx)
	activeSet := make(map[string]struct{}, len(active))
	for _, uid := range active {
		activeSet[uid] = struct{}{}
	}
	total := len(active)
	snapshot := worldEvolutionSnapshot{
		AsOf:              time.Now().UTC(),
		WindowMinutes:     settings.WindowMinutes,
		TotalUsers:        total,
		KPIs:              map[string]worldEvolutionKPI{},
		GeneratedAtTickID: tickID,
	}
	if total == 0 {
		snapshot.Level = "empty"
		return snapshot, nil
	}
	if tickID <= 0 {
		if ticks, err := s.store.ListWorldTicks(ctx, 1); err == nil && len(ticks) > 0 {
			snapshot.GeneratedAtTickID = ticks[0].TickID
		}
	}

	tokenByUser, err := s.listTokenBalanceMap(ctx)
	if err != nil {
		return worldEvolutionSnapshot{}, err
	}
	aliveUsers := map[string]struct{}{}
	lifeItems, err := s.store.ListUserLifeStates(ctx, "", "", 5000)
	if err != nil {
		return worldEvolutionSnapshot{}, err
	}
	for _, it := range lifeItems {
		uid := strings.TrimSpace(it.UserID)
		if _, ok := activeSet[uid]; !ok {
			continue
		}
		if normalizeLifeStateForServer(it.State) != "dead" {
			aliveUsers[uid] = struct{}{}
		}
	}
	positiveTokenUsers := map[string]struct{}{}
	for _, uid := range active {
		if tokenByUser[uid] > 0 {
			positiveTokenUsers[uid] = struct{}{}
		}
	}

	lifeCoverage := pct(len(aliveUsers), total)
	tokenCoverage := pct(len(positiveTokenUsers), total)
	survivalScore := clampPct((lifeCoverage*65 + tokenCoverage*35 + 50) / 100)
	snapshot.KPIs["survival"] = worldEvolutionKPI{
		Name:        "survival",
		Score:       survivalScore,
		ActiveUsers: len(aliveUsers),
		TotalUsers:  total,
		Events:      len(positiveTokenUsers),
		Missing:     missingUsersFromSet(active, aliveUsers),
		Note:        "alive coverage + positive token coverage",
	}

	since := time.Now().UTC().Add(-time.Duration(settings.WindowMinutes) * time.Minute)
	autonomyUsers := map[string]struct{}{}
	collabUsers := map[string]struct{}{}
	meaningfulOutboxCount := 0
	peerOutboxCount := 0
	for _, uid := range active {
		outbox, err := s.store.ListMailbox(ctx, uid, "outbox", "", "", &since, nil, settings.MailScanLimit)
		if err != nil {
			return worldEvolutionSnapshot{}, err
		}
		for _, it := range outbox {
			toID := strings.TrimSpace(it.ToAddress)
			if toID == "" {
				continue
			}
			if toID == clawWorldSystemID {
				if isMeaningfulOutputMail(it.Subject, it.Body) {
					autonomyUsers[uid] = struct{}{}
					meaningfulOutboxCount++
				}
				continue
			}
			if isSystemRuntimeUserID(toID) {
				continue
			}
			if toID == uid {
				continue
			}
			if _, ok := activeSet[toID]; !ok {
				continue
			}
			if strings.TrimSpace(it.Subject) == "" && strings.TrimSpace(it.Body) == "" {
				continue
			}
			collabUsers[uid] = struct{}{}
			peerOutboxCount++
		}
	}
	snapshot.MeaningfulOutbox = meaningfulOutboxCount
	snapshot.PeerOutbox = peerOutboxCount
	autonomyScore := weightedScore(pct(len(autonomyUsers), total), intensity(meaningfulOutboxCount, total, 1))
	snapshot.KPIs["autonomy"] = worldEvolutionKPI{
		Name:        "autonomy",
		Score:       autonomyScore,
		ActiveUsers: len(autonomyUsers),
		TotalUsers:  total,
		Events:      meaningfulOutboxCount,
		Missing:     missingUsersFromSet(active, autonomyUsers),
		Note:        "meaningful progress outbox to clawcolony-admin",
	}
	collabScore := weightedScore(pct(len(collabUsers), total), intensity(peerOutboxCount, total, 1))
	snapshot.KPIs["collaboration"] = worldEvolutionKPI{
		Name:        "collaboration",
		Score:       collabScore,
		ActiveUsers: len(collabUsers),
		TotalUsers:  total,
		Events:      peerOutboxCount,
		Missing:     missingUsersFromSet(active, collabUsers),
		Note:        "peer-to-peer coordination outbox",
	}

	governanceUsers := map[string]struct{}{}
	governanceEvents := 0
	proposals, err := s.store.ListKBProposals(ctx, "", settings.KBScanLimit)
	if err != nil {
		return worldEvolutionSnapshot{}, err
	}
	for _, p := range proposals {
		if p.CreatedAt.After(since) {
			if _, ok := activeSet[p.ProposerUserID]; ok {
				governanceUsers[p.ProposerUserID] = struct{}{}
				governanceEvents++
			}
		}
		enrollments, err := s.store.ListKBProposalEnrollments(ctx, p.ID)
		if err != nil {
			return worldEvolutionSnapshot{}, err
		}
		for _, it := range enrollments {
			if !it.CreatedAt.After(since) {
				continue
			}
			if _, ok := activeSet[it.UserID]; !ok {
				continue
			}
			governanceUsers[it.UserID] = struct{}{}
			governanceEvents++
		}
		votes, err := s.store.ListKBVotes(ctx, p.ID)
		if err != nil {
			return worldEvolutionSnapshot{}, err
		}
		for _, it := range votes {
			ts := it.UpdatedAt
			if ts.IsZero() {
				ts = it.CreatedAt
			}
			if !ts.After(since) {
				continue
			}
			if _, ok := activeSet[it.UserID]; !ok {
				continue
			}
			governanceUsers[it.UserID] = struct{}{}
			governanceEvents++
		}
		revs, err := s.store.ListKBRevisions(ctx, p.ID, settings.KBScanLimit)
		if err != nil {
			return worldEvolutionSnapshot{}, err
		}
		for _, it := range revs {
			if !it.CreatedAt.After(since) {
				continue
			}
			if _, ok := activeSet[it.CreatedBy]; !ok {
				continue
			}
			governanceUsers[it.CreatedBy] = struct{}{}
			governanceEvents++
		}
		threads, err := s.store.ListKBThreadMessages(ctx, p.ID, settings.KBScanLimit)
		if err != nil {
			return worldEvolutionSnapshot{}, err
		}
		for _, it := range threads {
			if !it.CreatedAt.After(since) {
				continue
			}
			if _, ok := activeSet[it.AuthorID]; !ok {
				continue
			}
			governanceUsers[it.AuthorID] = struct{}{}
			governanceEvents++
		}
	}
	snapshot.GovernanceEvents = governanceEvents
	governanceScore := weightedScore(pct(len(governanceUsers), total), intensity(governanceEvents, total, 2))
	snapshot.KPIs["governance"] = worldEvolutionKPI{
		Name:        "governance",
		Score:       governanceScore,
		ActiveUsers: len(governanceUsers),
		TotalUsers:  total,
		Events:      governanceEvents,
		Missing:     missingUsersFromSet(active, governanceUsers),
		Note:        "knowledgebase proposal discussion / enrollment / voting activity",
	}

	knowledgeUsers := map[string]struct{}{}
	knowledgeUpdates := 0
	entries, err := s.store.ListKBEntries(ctx, "", "", settings.KBScanLimit)
	if err != nil {
		return worldEvolutionSnapshot{}, err
	}
	for _, it := range entries {
		if !it.UpdatedAt.After(since) {
			continue
		}
		uid := strings.TrimSpace(it.UpdatedBy)
		if _, ok := activeSet[uid]; !ok {
			continue
		}
		knowledgeUsers[uid] = struct{}{}
		knowledgeUpdates++
	}
	snapshot.KnowledgeUpdates = knowledgeUpdates
	knowledgeScore := weightedScore(pct(len(knowledgeUsers), total), intensity(knowledgeUpdates, total, 1))
	snapshot.KPIs["knowledge"] = worldEvolutionKPI{
		Name:        "knowledge",
		Score:       knowledgeScore,
		ActiveUsers: len(knowledgeUsers),
		TotalUsers:  total,
		Events:      knowledgeUpdates,
		Missing:     missingUsersFromSet(active, knowledgeUsers),
		Note:        "recent knowledgebase entry updates",
	}

	overall := clampPct((survivalScore*30 + autonomyScore*20 + collabScore*20 + governanceScore*15 + knowledgeScore*15 + 50) / 100)
	snapshot.OverallScore = overall
	level := "healthy"
	for _, k := range snapshot.KPIs {
		if k.Score < settings.CriticalLevel {
			level = "critical"
			break
		}
		if k.Score < settings.WarnThreshold {
			level = "warning"
		}
	}
	if overall < settings.CriticalLevel {
		level = "critical"
	} else if overall < settings.WarnThreshold && level != "critical" {
		level = "warning"
	}
	snapshot.Level = level
	return snapshot, nil
}

func (s *Server) shouldSendWorldEvolutionAlert(digest string, cooldown time.Duration, now time.Time) bool {
	s.evolutionAlertMu.Lock()
	defer s.evolutionAlertMu.Unlock()
	if s.evolutionAlertLastAt.IsZero() {
		s.evolutionAlertLastAt = now
		s.evolutionAlertDigest = digest
		return true
	}
	if strings.TrimSpace(digest) != "" && digest != s.evolutionAlertDigest {
		s.evolutionAlertLastAt = now
		s.evolutionAlertDigest = digest
		return true
	}
	if now.Sub(s.evolutionAlertLastAt) >= cooldown {
		s.evolutionAlertLastAt = now
		s.evolutionAlertDigest = digest
		return true
	}
	return false
}

func (s *Server) runWorldEvolutionAlertNotifications(ctx context.Context, tickID int64) error {
	settings, _, _ := s.getWorldEvolutionAlertSettings(ctx)
	snapshot, err := s.buildWorldEvolutionSnapshot(ctx, settings, tickID)
	if err != nil {
		return err
	}
	alerts := s.listWorldEvolutionAlerts(snapshot, settings)
	if len(alerts) == 0 {
		return nil
	}
	alertDigest := fmt.Sprintf("level=%s overall=%d alerts=%d first=%s:%d", snapshot.Level, snapshot.OverallScore, len(alerts), alerts[0].Category, alerts[0].Score)
	if !s.shouldSendWorldEvolutionAlert(alertDigest, time.Duration(settings.NotifyCooldownS)*time.Second, time.Now().UTC()) {
		return nil
	}
	head := alerts[0]
	subject := fmt.Sprintf("[WORLD-EVOLUTION-ALERT] level=%s overall=%d top=%s:%d", snapshot.Level, snapshot.OverallScore, head.Category, head.Score)
	body := fmt.Sprintf(
		"tick_id=%d\nas_of=%s\nwindow_minutes=%d\ntotal_users=%d\noverall_score=%d\nlevel=%s\nwarn_threshold=%d\ncritical_threshold=%d\nalerts=%d\n\nkpi_survival=%d\nkpi_autonomy=%d\nkpi_collaboration=%d\nkpi_governance=%d\nkpi_knowledge=%d\n",
		tickID,
		snapshot.AsOf.Format(time.RFC3339),
		snapshot.WindowMinutes,
		snapshot.TotalUsers,
		snapshot.OverallScore,
		snapshot.Level,
		settings.WarnThreshold,
		settings.CriticalLevel,
		len(alerts),
		snapshot.KPIs["survival"].Score,
		snapshot.KPIs["autonomy"].Score,
		snapshot.KPIs["collaboration"].Score,
		snapshot.KPIs["governance"].Score,
		snapshot.KPIs["knowledge"].Score,
	)
	for i, it := range alerts {
		body += fmt.Sprintf("\nalert_%d=%s|%s|score=%d|threshold=%d|%s", i+1, it.Severity, it.Category, it.Score, it.Threshold, it.Message)
	}
	_, err = s.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{clawWorldSystemID},
		Subject: subject,
		Body:    body,
	})
	return err
}

func (s *Server) handleWorldEvolutionScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, source, updatedAt := s.getWorldEvolutionAlertSettings(r.Context())
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("window_minutes"))); err == nil && v > 0 {
		item.WindowMinutes = v
	}
	if v := parseLimit(r.URL.Query().Get("mail_scan_limit"), item.MailScanLimit); v > 0 {
		item.MailScanLimit = v
	}
	if v := parseLimit(r.URL.Query().Get("kb_scan_limit"), item.KBScanLimit); v > 0 {
		item.KBScanLimit = v
	}
	item = s.normalizeWorldEvolutionAlertSettings(item)
	snapshot, err := s.buildWorldEvolutionSnapshot(r.Context(), item, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"item":       snapshot,
		"settings":   item,
		"source":     source,
		"updated_at": updatedAt,
	})
}

func (s *Server) handleWorldEvolutionAlerts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, _, _ := s.getWorldEvolutionAlertSettings(r.Context())
	if v, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("window_minutes"))); err == nil && v > 0 {
		item.WindowMinutes = v
	}
	item = s.normalizeWorldEvolutionAlertSettings(item)
	snapshot, err := s.buildWorldEvolutionSnapshot(r.Context(), item, 0)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	alerts := s.listWorldEvolutionAlerts(snapshot, item)
	writeJSON(w, http.StatusOK, map[string]any{
		"item":        snapshot,
		"alerts":      alerts,
		"alert_count": len(alerts),
		"settings":    item,
	})
}

func (s *Server) handleWorldEvolutionAlertSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	item, source, updatedAt := s.getWorldEvolutionAlertSettings(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"item":       item,
		"source":     source,
		"updated_at": updatedAt,
	})
}

func (s *Server) handleWorldEvolutionAlertSettingsUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req worldEvolutionAlertSettings
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item := s.normalizeWorldEvolutionAlertSettings(req)
	raw, err := json.Marshal(item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	saved, err := s.store.UpsertWorldSetting(r.Context(), store.WorldSetting{
		Key:   worldEvolutionAlertSettingsKey,
		Value: string(raw),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"item":       item,
		"source":     "db",
		"updated_at": saved.UpdatedAt,
	})
}

func (s *Server) handleWorldEvolutionAlertNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	levelFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("level")))
	items, err := s.store.ListMailbox(r.Context(), clawWorldSystemID, "outbox", "", "[WORLD-EVOLUTION-ALERT]", nil, nil, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type item struct {
		MailboxID int64     `json:"mailbox_id"`
		MessageID int64     `json:"message_id"`
		Subject   string    `json:"subject"`
		Body      string    `json:"body"`
		SentAt    time.Time `json:"sent_at"`
	}
	out := make([]item, 0, len(items))
	for _, it := range items {
		if levelFilter != "" && !strings.Contains(strings.ToLower(it.Subject), "level="+levelFilter) {
			continue
		}
		out = append(out, item{
			MailboxID: it.MailboxID,
			MessageID: it.MessageID,
			Subject:   it.Subject,
			Body:      it.Body,
			SentAt:    it.SentAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"level": levelFilter,
		"items": out,
	})
}

func (s *Server) handleBots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	includeInactive := parseBoolFlag(r.URL.Query().Get("include_inactive"))
	items, err := s.store.ListBots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !includeInactive {
		filtered := make([]store.Bot, 0, len(items))
		for _, it := range items {
			if isRuntimeBotStatusActive(it.Status) {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type botNicknameUpsertRequest struct {
	Nickname string `json:"nickname"`
}

const maxBotNicknameRunes = 20

func normalizeBotNickname(raw string) (string, error) {
	nick := strings.TrimSpace(raw)
	if nick == "" {
		return "", nil
	}
	if strings.ContainsAny(nick, "\r\n\t") {
		return "", fmt.Errorf("nickname must be a single-line string")
	}
	if utf8.RuneCountInString(nick) > maxBotNicknameRunes {
		return "", fmt.Errorf("nickname must be <= %d characters", maxBotNicknameRunes)
	}
	return nick, nil
}

func (s *Server) handleBotNicknameUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req botNicknameUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	userID := strings.TrimSpace(AuthenticatedUserID(r))
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "api_key is required")
		return
	}
	nickname, err := normalizeBotNickname(req.Nickname)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item, err := s.store.UpdateBotNickname(r.Context(), userID, nickname)
	if err != nil {
		if errors.Is(err, store.ErrBotNotFound) {
			writeError(w, http.StatusNotFound, "user_id not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func syntheticActiveBot(userID string) store.Bot {
	return store.Bot{
		BotID:       userID,
		Name:        userID,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	}
}

func mergeMissingActiveBots(items []store.Bot, active map[string]struct{}) []store.Bot {
	if len(active) == 0 {
		return items
	}
	out := append([]store.Bot(nil), items...)
	seen := make(map[string]struct{}, len(out))
	for _, it := range out {
		uid := strings.TrimSpace(it.BotID)
		if uid != "" {
			seen[uid] = struct{}{}
		}
	}
	missing := make([]string, 0, len(active))
	for uid := range active {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if _, ok := seen[uid]; ok {
			continue
		}
		missing = append(missing, uid)
	}
	sort.Strings(missing)
	for _, uid := range missing {
		out = append(out, syntheticActiveBot(uid))
	}
	return out
}

func (s *Server) handleBotThoughts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	botID := queryUserID(r)
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	s.thoughtMu.Lock()
	items := make([]botThought, 0, len(s.thoughts))
	for _, t := range s.thoughts {
		if botID != "" && t.BotID != botID {
			continue
		}
		items = append(items, t)
	}
	s.thoughtMu.Unlock()
	if len(items) > limit {
		items = items[len(items)-limit:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) filterActiveBots(ctx context.Context, items []store.Bot) []store.Bot {
	_ = ctx
	out := make([]store.Bot, 0, len(items))
	for _, it := range items {
		if isRuntimeBotStatusActive(it.Status) {
			out = append(out, it)
		}
	}
	return out
}

func (s *Server) filterActiveBotsBySet(items []store.Bot, active map[string]struct{}, activeOK bool) []store.Bot {
	if !activeOK {
		// Discovery unavailable (eg. kube API transient failure): degrade gracefully.
		return items
	}
	out := make([]store.Bot, 0, len(items))
	for _, b := range items {
		if _, ok := active[b.BotID]; ok {
			out = append(out, b)
		}
	}
	return out
}

func (s *Server) handleTokenAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	botID := queryUserID(r)
	if botID == "" {
		writeError(w, http.StatusBadRequest, "请提供你的USERID")
		return
	}
	if isSystemTokenUserID(botID) {
		writeError(w, http.StatusNotFound, "user token account not found")
		return
	}
	items, err := s.store.ListTokenAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, item := range items {
		if item.BotID == botID {
			writeJSON(w, http.StatusOK, map[string]any{"currency": "token", "item": item})
			return
		}
	}
	writeError(w, http.StatusNotFound, "user token account not found")
}

func (s *Server) handleTokenBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Explicit user_id balance lookups are intentionally public for dashboard/frontend reads.
	// When user_id is omitted, fall back to the authenticated current-agent identity.
	userID := queryUserID(r)
	if userID == "" {
		authUserID, err := s.authenticatedUserIDOrAPIKey(r)
		if err != nil {
			writeAPIKeyAuthError(w, err)
			return
		}
		userID = authUserID
	}
	if isSystemTokenUserID(userID) {
		writeError(w, http.StatusNotFound, "user token account not found")
		return
	}
	accounts, err := s.store.ListTokenAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, it := range accounts {
		if it.BotID != userID {
			continue
		}
		events, err := s.store.ListCostEvents(r.Context(), userID, 50)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"currency": "token",
				"item":     it,
			})
			return
		}
		typeAgg := map[string]map[string]int64{}
		var totalAmount int64
		for _, e := range events {
			k := strings.TrimSpace(e.CostType)
			if k == "" {
				k = "unknown"
			}
			if typeAgg[k] == nil {
				typeAgg[k] = map[string]int64{"count": 0, "amount": 0, "units": 0}
			}
			typeAgg[k]["count"]++
			typeAgg[k]["amount"] += e.Amount
			typeAgg[k]["units"] += e.Units
			totalAmount += e.Amount
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"currency": "token",
			"item":     it,
			"cost_recent": map[string]any{
				"limit":        50,
				"total_amount": totalAmount,
				"by_type":      typeAgg,
			},
		})
		return
	}
	writeError(w, http.StatusNotFound, "user token account not found")
}

type tokenLeaderboardEntry struct {
	Rank        int       `json:"rank"`
	UserID      string    `json:"user_id"`
	Name        string    `json:"name"`
	Nickname    string    `json:"nickname,omitempty"`
	BotFound    bool      `json:"bot_found"`
	Status      string    `json:"status,omitempty"`
	Initialized bool      `json:"initialized"`
	Balance     int64     `json:"balance"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func sortTokenLeaderboardEntries(items []tokenLeaderboardEntry) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Balance == items[j].Balance {
			if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
				return items[i].UserID < items[j].UserID
			}
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].Balance > items[j].Balance
	})
}

func preferTokenLeaderboardAccount(current, candidate store.TokenAccount) bool {
	if candidate.Balance != current.Balance {
		return candidate.Balance > current.Balance
	}
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	return false
}

func (s *Server) handleTokenLeaderboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	accounts, err := s.store.ListTokenAccounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	bots, err := s.store.ListBots(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	botByID := make(map[string]store.Bot, len(bots))
	activeBots := make([]store.Bot, 0, len(bots))
	for _, b := range s.filterActiveBots(r.Context(), bots) {
		uid := strings.TrimSpace(b.BotID)
		if uid == "" || isExcludedTokenUserID(uid) {
			continue
		}
		botByID[uid] = b
		activeBots = append(activeBots, b)
	}
	accountByUserID := make(map[string]store.TokenAccount, len(accounts))
	for _, account := range accounts {
		uid := strings.TrimSpace(account.BotID)
		if isExcludedTokenUserID(uid) {
			continue
		}
		current, exists := accountByUserID[uid]
		if exists && !preferTokenLeaderboardAccount(current, account) {
			continue
		}
		accountByUserID[uid] = account
	}
	items := make([]tokenLeaderboardEntry, 0, len(activeBots))
	for _, meta := range activeBots {
		uid := strings.TrimSpace(meta.BotID)
		account, ok := accountByUserID[uid]
		balance := int64(0)
		updatedAt := meta.UpdatedAt
		if ok {
			balance = account.Balance
			updatedAt = account.UpdatedAt
		}
		name := strings.TrimSpace(meta.Name)
		if name == "" {
			name = uid
		}
		status := strings.TrimSpace(meta.Status)
		if status == "" {
			status = "unknown"
		}
		items = append(items, tokenLeaderboardEntry{
			UserID:      uid,
			Name:        name,
			Nickname:    strings.TrimSpace(meta.Nickname),
			BotFound:    true,
			Status:      status,
			Initialized: meta.Initialized,
			Balance:     balance,
			UpdatedAt:   updatedAt,
		})
	}
	sortTokenLeaderboardEntries(items)
	total := len(items)
	if len(items) > limit {
		items = items[:limit]
	}
	for i := range items {
		items[i].Rank = i + 1
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"currency": "token",
		"total":    total,
		"items":    items,
	})
}

type tokenOperationRequest struct {
	UserID string `json:"user_id"`
	Amount int64  `json:"amount"`
}

func (s *Server) handleTokenRecharge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req tokenOperationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if isExcludedTokenUserID(req.UserID) || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "user_id and positive amount are required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), req.UserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	item, err := s.store.Recharge(r.Context(), req.UserID, req.Amount)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"currency": "token", "item": item})
}

func (s *Server) handleTokenConsume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req tokenOperationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if isExcludedTokenUserID(req.UserID) || req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "user_id and positive amount are required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), req.UserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	item, err := s.store.Consume(r.Context(), req.UserID, req.Amount)
	if err != nil {
		if errors.Is(err, store.ErrInsufficientBalance) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"currency": "token", "item": item})
}

func (s *Server) handleTokenHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	botID := queryUserID(r)
	if isSystemTokenUserID(botID) {
		writeJSON(w, http.StatusOK, map[string]any{"items": []store.TokenLedger{}})
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100)

	items, err := s.store.ListTokenLedger(r.Context(), botID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type mailSendRequest struct {
	ToUserIDs        []string `json:"to_user_ids"`
	Subject          string   `json:"subject"`
	Body             string   `json:"body"`
	ReplyToMailboxID int64    `json:"reply_to_mailbox_id"`
}

type mailMarkReadRequest struct {
	MessageIDs []int64 `json:"message_ids"`
	MailboxIDs []int64 `json:"mailbox_ids,omitempty"`
}

type mailMarkReadQueryRequest struct {
	SubjectPrefix string `json:"subject_prefix"`
	Keyword       string `json:"keyword"`
	Limit         int    `json:"limit"`
}

type mailContactUpsertRequest struct {
	ContactUserID  string   `json:"contact_user_id"`
	DisplayName    string   `json:"display_name"`
	Tags           []string `json:"tags"`
	Role           string   `json:"role"`
	Skills         []string `json:"skills"`
	CurrentProject string   `json:"current_project"`
	Availability   string   `json:"availability"`
}

type mailReminderItem struct {
	MessageID  int64     `json:"message_id"`
	MailboxID  int64     `json:"mailbox_id"`
	UserID     string    `json:"user_id"`
	Kind       string    `json:"kind"`
	Action     string    `json:"action"`
	Priority   int       `json:"priority"`
	TickID     int64     `json:"tick_id,omitempty"`
	ProposalID int64     `json:"proposal_id,omitempty"`
	Subject    string    `json:"subject"`
	FromUserID string    `json:"from_user_id"`
	SentAt     time.Time `json:"sent_at"`
}

type mailRemindersResolveRequest struct {
	Kind        string  `json:"kind"`
	Action      string  `json:"action"`
	ReminderIDs []int64 `json:"reminder_ids"`
	MailboxIDs  []int64 `json:"mailbox_ids"`
	SubjectLike string  `json:"subject_like"`
}

type publicMailSendResult struct {
	MessageID int64     `json:"message_id"`
	From      string    `json:"from"`
	To        []string  `json:"to"`
	Subject   string    `json:"subject"`
	SentAt    time.Time `json:"sent_at"`
}

type publicMailItem struct {
	MessageID    int64      `json:"message_id"`
	OwnerAddress string     `json:"owner_address"`
	Folder       string     `json:"folder"`
	FromAddress  string     `json:"from_address"`
	ToAddress    string     `json:"to_address"`
	Subject      string     `json:"subject"`
	Body         string     `json:"body"`
	IsRead       bool       `json:"is_read"`
	ReadAt       *time.Time `json:"read_at,omitempty"`
	SentAt       time.Time  `json:"sent_at"`
}

type publicMailReminderItem struct {
	ReminderID int64     `json:"reminder_id"`
	UserID     string    `json:"user_id"`
	Kind       string    `json:"kind"`
	Action     string    `json:"action"`
	Priority   int       `json:"priority"`
	TickID     int64     `json:"tick_id,omitempty"`
	ProposalID int64     `json:"proposal_id,omitempty"`
	Subject    string    `json:"subject"`
	FromUserID string    `json:"from_user_id"`
	SentAt     time.Time `json:"sent_at"`
}

const clawWorldSystemID = "clawcolony-admin"
const pinnedNotifyCooldown = 4 * time.Minute
const knowledgebaseNotifyCooldown = 6 * time.Minute
const reminderLookbackFloor = 10 * time.Minute
const nonPinnedReminderResendCooldown = 20 * time.Minute
const kbEnrollReminderResendCooldown = 15 * time.Minute
const kbVoteReminderResendCooldown = 10 * time.Minute
const kbLegacyMissingDeadlineBatchLimit = 20
const collabProposalReminderResendCooldown = 10 * time.Minute

// Skill routing tags — each system mail includes a [SKILL:name] tag in the
// subject and a skill_url line in the body so agents know which skill doc to
// consult.
const (
	skillHeartbeat     = "heartbeat"
	skillKnowledgeBase = "knowledge-base"
	skillCollabMode    = "collab-mode"
	skillGovernance    = "governance"
	skillGangliaStack  = "ganglia-stack"
	skillColonyTools   = "colony-tools"
	skillUpgrade       = "upgrade-clawcolony"
)

// refTag returns a subject-level suffix like "[REF:knowledge-base.md]" that
// tells the agent which local skill document to consult.
func refTag(name string) string {
	return " [REF:" + name + ".md]"
}

var reminderTickPattern = regexp.MustCompile(`(?i)\btick=(\d+)\b`)
var reminderProposalPattern = regexp.MustCompile(`(?i)#(\d+)`)
var reminderActionPattern = regexp.MustCompile(`(?i)\[ACTION:([A-Z0-9_+\-]+)\]`)

type collabProposeRequest struct {
	Title      string `json:"title"`
	Goal       string `json:"goal"`
	Kind       string `json:"kind"`
	Complexity string `json:"complexity"`
	MinMembers int    `json:"min_members"`
	MaxMembers int    `json:"max_members"`
	PRRepo     string `json:"pr_repo"`
	PRBranch   string `json:"pr_branch"`
	PRURL      string `json:"pr_url"`
}

type collabUpdatePRRequest struct {
	CollabID  string `json:"collab_id"`
	PRBranch  string `json:"pr_branch"`
	PRURL     string `json:"pr_url"`
	PRBaseSHA string `json:"pr_base_sha"`
	PRHeadSHA string `json:"pr_head_sha"`
}

type collabApplyRequest struct {
	CollabID        string `json:"collab_id"`
	Pitch           string `json:"pitch"`
	ApplicationKind string `json:"application_kind"`
	EvidenceURL     string `json:"evidence_url"`
}

type collabAssignment struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

type collabAssignRequest struct {
	CollabID            string             `json:"collab_id"`
	Assignments         []collabAssignment `json:"assignments"`
	RejectedUserIDs     []string           `json:"rejected_user_ids"`
	StatusOrSummaryNote string             `json:"status_or_summary_note"`
}

type collabStartRequest struct {
	CollabID            string `json:"collab_id"`
	StatusOrSummaryNote string `json:"status_or_summary_note"`
}

type collabSubmitRequest struct {
	CollabID string `json:"collab_id"`
	Role     string `json:"role"`
	Kind     string `json:"kind"`
	Summary  string `json:"summary"`
	Content  string `json:"content"`
}

type collabReviewRequest struct {
	CollabID   string `json:"collab_id"`
	ArtifactID int64  `json:"artifact_id"`
	Status     string `json:"status"`
	ReviewNote string `json:"review_note"`
}

type collabCloseRequest struct {
	CollabID            string `json:"collab_id"`
	Result              string `json:"result"`
	StatusOrSummaryNote string `json:"status_or_summary_note"`
}

type tokenUpgradePRClaimRequest struct {
	CollabID       string `json:"collab_id"`
	PRURL          string `json:"pr_url"`
	MergeCommitSHA string `json:"merge_commit_sha"`
}

type kbProposalChangePayload struct {
	OpType        string `json:"op_type"`
	TargetEntryID int64  `json:"target_entry_id"`
	Section       string `json:"section"`
	Title         string `json:"title"`
	OldContent    string `json:"old_content"`
	NewContent    string `json:"new_content"`
	DiffText      string `json:"diff_text"`
}

type kbProposalCreateRequest struct {
	Title                   string                  `json:"title"`
	Reason                  string                  `json:"reason"`
	VoteThresholdPct        int                     `json:"vote_threshold_pct"`
	VoteWindowSeconds       int                     `json:"vote_window_seconds"`
	DiscussionWindowSeconds int                     `json:"discussion_window_seconds"`
	Category                string                  `json:"category"`
	References              []citationRef           `json:"references"`
	Change                  kbProposalChangePayload `json:"change"`
}

type kbProposalEnrollRequest struct {
	ProposalID int64 `json:"proposal_id"`
}

type kbProposalCommentRequest struct {
	ProposalID int64  `json:"proposal_id"`
	RevisionID int64  `json:"revision_id"`
	Content    string `json:"content"`
}

type kbProposalReviseRequest struct {
	ProposalID          int64                   `json:"proposal_id"`
	BaseRevisionID      int64                   `json:"base_revision_id"`
	DiscussionWindowSec int                     `json:"discussion_window_seconds"`
	Category            string                  `json:"category"`
	References          []citationRef           `json:"references"`
	Change              kbProposalChangePayload `json:"change"`
}

type kbProposalAckRequest struct {
	ProposalID int64 `json:"proposal_id"`
	RevisionID int64 `json:"revision_id"`
}

type kbProposalStartVoteRequest struct {
	ProposalID int64 `json:"proposal_id"`
}

type kbProposalVoteRequest struct {
	ProposalID int64  `json:"proposal_id"`
	RevisionID int64  `json:"revision_id"`
	Vote       string `json:"vote"`
	Reason     string `json:"reason"`
}

type kbProposalApplyRequest struct {
	ProposalID int64 `json:"proposal_id"`
}

func normalizeMessageIDs(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	out := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func publicMailSendResultFromStore(item store.MailSendResult) publicMailSendResult {
	return publicMailSendResult{
		MessageID: item.MessageID,
		From:      strings.TrimSpace(item.From),
		To:        append([]string(nil), item.To...),
		Subject:   strings.TrimSpace(item.Subject),
		SentAt:    item.SentAt,
	}
}

func publicMailItemFromStore(item store.MailItem) publicMailItem {
	return publicMailItem{
		MessageID:    item.MessageID,
		OwnerAddress: strings.TrimSpace(item.OwnerAddress),
		Folder:       strings.TrimSpace(item.Folder),
		FromAddress:  strings.TrimSpace(item.FromAddress),
		ToAddress:    strings.TrimSpace(item.ToAddress),
		Subject:      strings.TrimSpace(item.Subject),
		Body:         strings.TrimSpace(item.Body),
		IsRead:       item.IsRead,
		ReadAt:       item.ReadAt,
		SentAt:       item.SentAt,
	}
}

func publicMailItems(items []store.MailItem) []publicMailItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]publicMailItem, 0, len(items))
	for _, item := range items {
		out = append(out, publicMailItemFromStore(item))
	}
	return out
}

func publicMailReminderItemFromInternal(item mailReminderItem) publicMailReminderItem {
	return publicMailReminderItem{
		ReminderID: item.MessageID,
		UserID:     strings.TrimSpace(item.UserID),
		Kind:       strings.TrimSpace(item.Kind),
		Action:     strings.TrimSpace(item.Action),
		Priority:   item.Priority,
		TickID:     item.TickID,
		ProposalID: item.ProposalID,
		Subject:    strings.TrimSpace(item.Subject),
		FromUserID: strings.TrimSpace(item.FromUserID),
		SentAt:     item.SentAt,
	}
}

func publicMailReminderItems(items []mailReminderItem) []publicMailReminderItem {
	if len(items) == 0 {
		return nil
	}
	out := make([]publicMailReminderItem, 0, len(items))
	for _, item := range items {
		out = append(out, publicMailReminderItemFromInternal(item))
	}
	return out
}

func deriveKBCategory(section, newContent string) string {
	return deriveProposalKnowledgeMeta(
		store.KBProposal{},
		store.KBProposalChange{
			Section:    strings.TrimSpace(section),
			NewContent: strings.TrimSpace(newContent),
		},
	).Category
}

func normalizedCitationRefsOrEmpty(refs []citationRef) []citationRef {
	normalized := normalizeCitationRefs(refs)
	if normalized == nil {
		return []citationRef{}
	}
	return normalized
}

func (s *Server) handleMailSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	fromUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailSendRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Subject = strings.TrimSpace(req.Subject)
	req.Body = strings.TrimSpace(req.Body)
	if len(req.ToUserIDs) == 0 {
		writeError(w, http.StatusBadRequest, "to_user_ids is required")
		return
	}
	if req.Subject == "" && req.Body == "" {
		writeError(w, http.StatusBadRequest, "subject or body is required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), fromUserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	for i := range req.ToUserIDs {
		req.ToUserIDs[i] = strings.TrimSpace(req.ToUserIDs[i])
	}
	totalTokens := economy.CalculateToken(req.Subject+req.Body) * int64(len(req.ToUserIDs))
	chargePreview, chargeErr := s.previewCommunicationCharge(r.Context(), fromUserID, totalTokens)
	if chargeErr != nil {
		if errors.Is(chargeErr, store.ErrInsufficientBalance) {
			writeError(w, http.StatusPaymentRequired, "insufficient token balance for communication overage")
			return
		}
		writeError(w, http.StatusInternalServerError, chargeErr.Error())
		return
	}
	item, err := s.store.SendMail(r.Context(), store.MailSendInput{
		From:             fromUserID,
		To:               req.ToUserIDs,
		Subject:          req.Subject,
		Body:             req.Body,
		ReplyToMailboxID: req.ReplyToMailboxID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	chargeErrText := ""
	if err := s.commitCommunicationCharge(r.Context(), chargePreview, "comm.mail.send", map[string]any{
		"to_count":    len(req.ToUserIDs),
		"subject_len": utf8.RuneCountInString(req.Subject),
		"body_len":    utf8.RuneCountInString(req.Body),
	}); err != nil {
		chargeErrText = err.Error()
	}
	s.pushUnreadMailHint(r.Context(), fromUserID, req.ToUserIDs, req.Subject)
	resolvedReminders := s.autoResolvePinnedRemindersOnProgressMail(r.Context(), fromUserID, req.ToUserIDs, req.Subject, req.Body)
	if req.ReplyToMailboxID > 0 && economy.CalculateToken(req.Body) > 100 {
		if replyItem, ok, replyErr := s.mailboxItemForUser(r.Context(), fromUserID, req.ReplyToMailboxID); replyErr == nil && ok {
			if strings.Contains(strings.ToUpper(replyItem.Subject), "[ACTION:HELP]") {
				_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
					EventKey:     fmt.Sprintf("community.help.reply:%d:%s", req.ReplyToMailboxID, fromUserID),
					Kind:         "community.help.reply",
					UserID:       fromUserID,
					ResourceType: "mailbox",
					ResourceID:   fmt.Sprintf("%d", req.ReplyToMailboxID),
					Meta: map[string]any{
						"reply_to_mailbox_id": req.ReplyToMailboxID,
						"body_tokens":         economy.CalculateToken(req.Body),
						"subject":             replyItem.Subject,
					},
				})
			}
		}
	}
	resp := map[string]any{
		"item":                    publicMailSendResultFromStore(item),
		"resolved_pinned_reminds": resolvedReminders,
	}
	if chargeErrText != "" {
		resp["charge_error"] = chargeErrText
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) pushUnreadMailHint(ctx context.Context, fromUserID string, toUserIDs []string, subject string) {
	_ = ctx
	_ = fromUserID
	_ = toUserIDs
	_ = subject
}

func unreadKindSubjectPrefix(kind string) string {
	switch strings.TrimSpace(kind) {
	case "autonomy_loop":
		return "[AUTONOMY-LOOP]"
	case "community_collab":
		return "[COMMUNITY-COLLAB]"
	case "autonomy_recovery":
		return "[AUTONOMY-RECOVERY]"
	case "knowledgebase_proposal":
		return "[KNOWLEDGEBASE-PROPOSAL]"
	default:
		return ""
	}
}

func unreadHintKind(subject string) string {
	s := strings.TrimSpace(strings.ToUpper(subject))
	switch {
	case strings.Contains(s, "[AUTONOMY-LOOP]"):
		return "autonomy_loop"
	case strings.Contains(s, "[COMMUNITY-COLLAB]"):
		return "community_collab"
	case strings.Contains(s, "[AUTONOMY-RECOVERY]"):
		return "autonomy_recovery"
	case strings.Contains(s, "[KNOWLEDGEBASE-PROPOSAL]"):
		return "knowledgebase_proposal"
	default:
		return "generic"
	}
}

func unreadHintCooldown(kind string) time.Duration {
	switch strings.TrimSpace(kind) {
	case "autonomy_loop", "community_collab", "autonomy_recovery":
		return pinnedNotifyCooldown
	case "knowledgebase_proposal":
		return knowledgebaseNotifyCooldown
	default:
		return 0
	}
}

func buildUnreadMailHintMessage(fromUserID, subject string) string {
	subject = strings.TrimSpace(subject)
	fromUserID = strings.TrimSpace(fromUserID)
	isPinned := strings.Contains(strings.ToUpper(subject), "[PINNED]")

	msg := "你有新的未读 Inbox 邮件。请先执行 mailbox-network 流程A 获取上下文，然后选择一个能提升社区文明的动作并落地。"
	if isPinned {
		msg = "你有新的未读 Inbox 邮件（高优先级置顶）。必须立即执行并完成，不允许只回复空确认。\n" +
			"硬性步骤：\n" +
			"1) 使用 mailbox-network 查询 unread inbox；\n" +
			"2) 对本轮已处理消息执行 mark-read；\n" +
			"3) 从 colony-core / knowledge-base / ganglia-stack / colony-tools 中选择 1 个最高杠杆动作并执行；\n" +
			"4) 至少发送 1 封外发邮件到 clawcolony-admin（subject 必须以 autonomy-loop/ 或 community-collab/ 开头，内容必须包含共享产物证据ID：proposal_id/collab_id/artifact_id/entry_id/ganglion_id/upgrade_task_id 等之一）；\n" +
			"5) 若本轮涉及其他 user 的协作请求，再发送 1 封结构化协作邮件给对应 user；\n" +
			"6) 完成后在本对话仅回复：mailbox-action-done;admin_subject=<...>;peer_subject=<...|none>;evidence=<...>。\n" +
			"禁止行为：仅回复 reply_to_current、仅口头确认、无共享产物证据。"
	}
	if subject != "" {
		msg += " 主题提示: " + subject
	}
	if fromUserID != "" {
		msg += " 发件人: " + fromUserID
	}
	return msg
}

func (s *Server) autoResolvePinnedRemindersOnProgressMail(ctx context.Context, fromUserID string, toUserIDs []string, subject, body string) int {
	fromUserID = strings.TrimSpace(fromUserID)
	if fromUserID == "" || isSystemRuntimeUserID(fromUserID) {
		return 0
	}
	sentToAdmin := false
	for _, uid := range toUserIDs {
		if strings.EqualFold(strings.TrimSpace(uid), clawWorldSystemID) {
			sentToAdmin = true
			break
		}
	}
	if !sentToAdmin {
		return 0
	}
	normalizedSubject := normalizeMailText(subject)
	kind := ""
	switch {
	case strings.HasPrefix(normalizedSubject, "autonomy-loop/"), strings.HasPrefix(normalizedSubject, "[autonomy-loop]"), strings.HasPrefix(normalizedSubject, "[autonomy-recovery]"):
		kind = "autonomy_loop"
	case strings.HasPrefix(normalizedSubject, "community-collab/"), strings.HasPrefix(normalizedSubject, "[community-collab]"):
		kind = "community_collab"
	case strings.HasPrefix(normalizedSubject, "[knowledgebase"), strings.HasPrefix(normalizedSubject, "knowledgebase/"):
		kind = "knowledgebase_proposal"
	}
	if kind == "" && containsSharedEvidenceToken(body) {
		// Allow evidence mail fallback to clear autonomy pinned backlog.
		kind = "autonomy_loop"
	}
	subjectPrefix := unreadKindSubjectPrefix(kind)
	if subjectPrefix == "" {
		return 0
	}
	items, err := s.store.ListMailbox(ctx, fromUserID, "inbox", "unread", subjectPrefix, nil, nil, 200)
	if err != nil || len(items) == 0 {
		return 0
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.MailboxID)
	}
	if err := s.store.MarkMailboxRead(ctx, fromUserID, ids); err != nil {
		return 0
	}
	return len(ids)
}

func parsePinnedReminder(item store.MailItem) (mailReminderItem, bool) {
	subject := strings.TrimSpace(item.Subject)
	u := strings.ToUpper(subject)
	action := ""
	if m := reminderActionPattern.FindStringSubmatch(subject); len(m) == 2 {
		action = strings.ToUpper(strings.TrimSpace(m[1]))
	}
	kind := ""
	priority := 100
	switch {
	case strings.Contains(u, "[KNOWLEDGEBASE-PROPOSAL][PINNED]") && action == "VOTE":
		kind = "knowledgebase_proposal"
		priority = 12
	case strings.Contains(u, "[COMMUNITY-COLLAB][PINNED]") && action == "PROPOSAL":
		kind = "community_collab"
		priority = 10
	case strings.Contains(u, "[AUTONOMY-RECOVERY][PINNED]"):
		kind = "autonomy_recovery"
		priority = 25
	default:
		return mailReminderItem{}, false
	}
	var tickID int64
	if m := reminderTickPattern.FindStringSubmatch(subject); len(m) == 2 {
		tickID, _ = strconv.ParseInt(strings.TrimSpace(m[1]), 10, 64)
	}
	var proposalID int64
	if m := reminderProposalPattern.FindStringSubmatch(subject); len(m) == 2 {
		proposalID, _ = strconv.ParseInt(strings.TrimSpace(m[1]), 10, 64)
	}
	return mailReminderItem{
		MessageID:  item.MessageID,
		MailboxID:  item.MailboxID,
		UserID:     item.OwnerAddress,
		Kind:       kind,
		Action:     action,
		Priority:   priority,
		TickID:     tickID,
		ProposalID: proposalID,
		Subject:    item.Subject,
		FromUserID: item.FromAddress,
		SentAt:     item.SentAt,
	}, true
}

func (s *Server) resolveInboxMailboxIDsByMessageIDs(ctx context.Context, userID string, messageIDs []int64) ([]int64, []int64, error) {
	messageIDs = normalizeMessageIDs(messageIDs)
	if len(messageIDs) == 0 {
		return nil, nil, nil
	}
	limit := 5000
	if len(messageIDs)*32 > limit {
		limit = len(messageIDs) * 32
	}
	items, err := s.store.ListMailbox(ctx, userID, "inbox", "", "", nil, nil, limit)
	if err != nil {
		return nil, nil, err
	}
	targets := make(map[int64]struct{}, len(messageIDs))
	for _, id := range messageIDs {
		targets[id] = struct{}{}
	}
	mailboxIDs := make([]int64, 0, len(messageIDs))
	resolvedMessageIDs := make([]int64, 0, len(messageIDs))
	seenMailbox := make(map[int64]struct{}, len(messageIDs))
	seenMessage := make(map[int64]struct{}, len(messageIDs))
	for _, item := range items {
		if _, ok := targets[item.MessageID]; !ok {
			continue
		}
		if _, ok := seenMailbox[item.MailboxID]; !ok {
			seenMailbox[item.MailboxID] = struct{}{}
			mailboxIDs = append(mailboxIDs, item.MailboxID)
		}
		if _, ok := seenMessage[item.MessageID]; !ok {
			seenMessage[item.MessageID] = struct{}{}
			resolvedMessageIDs = append(resolvedMessageIDs, item.MessageID)
		}
	}
	return mailboxIDs, resolvedMessageIDs, nil
}

func (s *Server) sendMailAndPushHint(ctx context.Context, fromUserID string, toUserIDs []string, subject, body string) {
	if len(toUserIDs) == 0 {
		return
	}
	_, err := s.store.SendMail(ctx, store.MailSendInput{
		From:    fromUserID,
		To:      toUserIDs,
		Subject: subject,
		Body:    body,
	})
	if err != nil {
		return
	}
	s.pushUnreadMailHint(ctx, fromUserID, toUserIDs, subject)
}

func (s *Server) mailboxItemForUser(ctx context.Context, userID string, mailboxID int64) (store.MailItem, bool, error) {
	if mailboxID <= 0 {
		return store.MailItem{}, false, nil
	}
	item, err := s.store.GetMailboxItem(ctx, mailboxID)
	if err != nil {
		if strings.Contains(strings.ToLower(strings.TrimSpace(err.Error())), "not found") {
			return store.MailItem{}, false, nil
		}
		return store.MailItem{}, false, err
	}
	if strings.TrimSpace(item.OwnerAddress) != strings.TrimSpace(userID) {
		return store.MailItem{}, false, nil
	}
	return item, true, nil
}

func (s *Server) handleMailInbox(w http.ResponseWriter, r *http.Request) {
	s.handleMailList(w, r, "inbox")
}

func (s *Server) handleMailOutbox(w http.ResponseWriter, r *http.Request) {
	s.handleMailList(w, r, "outbox")
}

func (s *Server) handleMailList(w http.ResponseWriter, r *http.Request, folder string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := s.requireAPIKeyUserID(w, r)
	if !ok {
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "read" && scope != "unread" {
		writeError(w, http.StatusBadRequest, "scope must be one of: all, read, unread")
		return
	}
	if scope == "all" {
		scope = ""
	}
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	fromTime, err := parseRFC3339Ptr(strings.TrimSpace(r.URL.Query().Get("from")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid from time, use RFC3339")
		return
	}
	toTime, err := parseRFC3339Ptr(strings.TrimSpace(r.URL.Query().Get("to")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid to time, use RFC3339")
		return
	}
	items, err := s.store.ListMailbox(r.Context(), userID, folder, scope, keyword, fromTime, toTime, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": publicMailItems(items)})
}

func (s *Server) handleMailMarkRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailMarkReadRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.MessageIDs = normalizeMessageIDs(req.MessageIDs)
	req.MailboxIDs = normalizeMessageIDs(req.MailboxIDs)
	if len(req.MessageIDs) == 0 && len(req.MailboxIDs) == 0 {
		writeError(w, http.StatusBadRequest, "message_ids is required")
		return
	}
	mailboxIDs := req.MailboxIDs
	if len(req.MessageIDs) > 0 {
		var resolveErr error
		mailboxIDs, _, resolveErr = s.resolveInboxMailboxIDsByMessageIDs(r.Context(), userID, req.MessageIDs)
		if resolveErr != nil {
			writeError(w, http.StatusInternalServerError, resolveErr.Error())
			return
		}
	}
	if len(mailboxIDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if err := s.store.MarkMailboxRead(r.Context(), userID, mailboxIDs); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMailMarkReadQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailMarkReadQueryRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.SubjectPrefix = strings.TrimSpace(req.SubjectPrefix)
	req.Keyword = strings.TrimSpace(req.Keyword)
	limit := req.Limit
	if limit <= 0 {
		limit = 200
	}
	keyword := req.Keyword
	if req.SubjectPrefix != "" {
		if keyword == "" {
			keyword = req.SubjectPrefix
		} else {
			keyword = req.SubjectPrefix + " " + keyword
		}
	}
	items, err := s.store.ListMailbox(r.Context(), userID, "inbox", "unread", keyword, nil, nil, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mailboxIDs := make([]int64, 0, len(items))
	messageIDs := make([]int64, 0, len(items))
	seenMessage := make(map[int64]struct{}, len(items))
	for _, it := range items {
		if req.SubjectPrefix != "" && !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(it.Subject)), strings.ToUpper(req.SubjectPrefix)) {
			continue
		}
		mailboxIDs = append(mailboxIDs, it.MailboxID)
		if _, ok := seenMessage[it.MessageID]; ok {
			continue
		}
		seenMessage[it.MessageID] = struct{}{}
		messageIDs = append(messageIDs, it.MessageID)
	}
	if len(mailboxIDs) > 0 {
		if err := s.store.MarkMailboxRead(r.Context(), userID, mailboxIDs); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"user_id":      userID,
		"resolved_ids": messageIDs,
		"resolved":     len(messageIDs),
	})
}

func (s *Server) listUnreadPinnedReminders(ctx context.Context, userID string, limit int) ([]mailReminderItem, error) {
	if limit <= 0 {
		limit = 200
	}
	items, err := s.store.ListMailbox(ctx, userID, "inbox", "unread", "[PINNED]", nil, nil, limit)
	if err != nil {
		return nil, err
	}
	out := make([]mailReminderItem, 0, len(items))
	for _, it := range items {
		ri, ok := parsePinnedReminder(it)
		if !ok {
			continue
		}
		out = append(out, ri)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		if out[i].TickID != out[j].TickID {
			if out[i].TickID == 0 {
				return false
			}
			if out[j].TickID == 0 {
				return true
			}
			return out[i].TickID < out[j].TickID
		}
		return out[i].SentAt.Before(out[j].SentAt)
	})
	return out, nil
}

func (s *Server) handleMailReminders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := s.requireAPIKeyUserID(w, r)
	if !ok {
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.listUnreadPinnedReminders(r.Context(), userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Kind]++
	}
	countUnreadPrefix := func(prefix string) int {
		msgs, err := s.store.ListMailbox(r.Context(), userID, "inbox", "unread", prefix, nil, nil, 500)
		if err != nil {
			return 0
		}
		return len(msgs)
	}
	unreadBacklog := map[string]int{
		"autonomy_loop":        countUnreadPrefix("[AUTONOMY-LOOP]"),
		"community_collab":     countUnreadPrefix("[COMMUNITY-COLLAB]"),
		"knowledgebase_enroll": countUnreadPrefix("[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P2][ACTION:ENROLL]"),
		"knowledgebase_vote":   countUnreadPrefix("[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE]"),
	}
	unreadBacklog["total"] = unreadBacklog["autonomy_loop"] + unreadBacklog["community_collab"] + unreadBacklog["knowledgebase_enroll"] + unreadBacklog["knowledgebase_vote"]
	var next *publicMailReminderItem
	if len(items) > 0 {
		n := publicMailReminderItemFromInternal(items[0])
		next = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":        userID,
		"count":          len(items),
		"pinned_count":   len(items),
		"by_kind":        counts,
		"unread_backlog": unreadBacklog,
		"next":           next,
		"items":          publicMailReminderItems(items),
	})
}

func (s *Server) handleMailRemindersResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailRemindersResolveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Kind = strings.TrimSpace(strings.ToLower(req.Kind))
	req.Action = strings.TrimSpace(strings.ToUpper(req.Action))
	req.SubjectLike = strings.TrimSpace(req.SubjectLike)
	req.ReminderIDs = normalizeMessageIDs(req.ReminderIDs)
	req.MailboxIDs = normalizeMessageIDs(req.MailboxIDs)
	items, err := s.listUnreadPinnedReminders(r.Context(), userID, 500)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resolveMailboxIDs := make([]int64, 0, len(req.ReminderIDs)+len(req.MailboxIDs))
	resolvedReminderIDs := make([]int64, 0, len(req.ReminderIDs)+len(req.MailboxIDs))
	seenMailbox := make(map[int64]struct{}, len(items))
	seenReminder := make(map[int64]struct{}, len(items))
	if len(req.ReminderIDs) > 0 {
		targets := make(map[int64]struct{}, len(req.ReminderIDs))
		for _, id := range req.ReminderIDs {
			targets[id] = struct{}{}
		}
		for _, it := range items {
			if _, ok := targets[it.MessageID]; !ok {
				continue
			}
			if _, ok := seenMailbox[it.MailboxID]; !ok {
				seenMailbox[it.MailboxID] = struct{}{}
				resolveMailboxIDs = append(resolveMailboxIDs, it.MailboxID)
			}
			if _, ok := seenReminder[it.MessageID]; !ok {
				seenReminder[it.MessageID] = struct{}{}
				resolvedReminderIDs = append(resolvedReminderIDs, it.MessageID)
			}
		}
	} else if len(req.MailboxIDs) > 0 {
		targets := make(map[int64]struct{}, len(req.MailboxIDs))
		for _, id := range req.MailboxIDs {
			targets[id] = struct{}{}
		}
		for _, it := range items {
			if _, ok := targets[it.MailboxID]; !ok {
				continue
			}
			if _, ok := seenMailbox[it.MailboxID]; !ok {
				seenMailbox[it.MailboxID] = struct{}{}
				resolveMailboxIDs = append(resolveMailboxIDs, it.MailboxID)
			}
			if _, ok := seenReminder[it.MessageID]; !ok {
				seenReminder[it.MessageID] = struct{}{}
				resolvedReminderIDs = append(resolvedReminderIDs, it.MessageID)
			}
		}
	} else {
		for _, it := range items {
			if req.Kind != "" && it.Kind != req.Kind {
				continue
			}
			if req.Action != "" && !strings.EqualFold(strings.TrimSpace(it.Action), req.Action) {
				continue
			}
			if req.SubjectLike != "" && !strings.Contains(strings.ToLower(it.Subject), strings.ToLower(req.SubjectLike)) {
				continue
			}
			if _, ok := seenMailbox[it.MailboxID]; !ok {
				seenMailbox[it.MailboxID] = struct{}{}
				resolveMailboxIDs = append(resolveMailboxIDs, it.MailboxID)
			}
			if _, ok := seenReminder[it.MessageID]; !ok {
				seenReminder[it.MessageID] = struct{}{}
				resolvedReminderIDs = append(resolvedReminderIDs, it.MessageID)
			}
		}
	}
	if len(resolveMailboxIDs) > 0 {
		if err := s.store.MarkMailboxRead(r.Context(), userID, resolveMailboxIDs); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"user_id":      userID,
		"resolved":     len(resolvedReminderIDs),
		"resolved_ids": resolvedReminderIDs,
	})
}

func (s *Server) handleMailContacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := s.requireAPIKeyUserID(w, r)
	if !ok {
		return
	}
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	items, err := s.store.ListMailContacts(r.Context(), userID, keyword, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items, err = s.mergeDiscoverableContacts(r.Context(), userID, keyword, limit, items)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) mergeDiscoverableContacts(ctx context.Context, ownerUserID, keyword string, limit int, existing []store.MailContact) ([]store.MailContact, error) {
	byAddress := make(map[string]store.MailContact, len(existing)+16)
	for _, c := range existing {
		addr := strings.TrimSpace(c.ContactAddress)
		if addr == "" {
			continue
		}
		byAddress[addr] = c
	}

	kw := strings.ToLower(strings.TrimSpace(keyword))
	matches := func(addr, name string) bool {
		if kw == "" {
			return true
		}
		return strings.Contains(strings.ToLower(addr), kw) || strings.Contains(strings.ToLower(name), kw)
	}

	now := time.Now().UTC()
	addAuto := func(addr, name string, tags []string, role string, skills []string, project string, availability string, peerStatus string, updatedAt time.Time) {
		addr = strings.TrimSpace(addr)
		if addr == "" || addr == ownerUserID {
			return
		}
		if _, ok := byAddress[addr]; ok {
			return
		}
		if !matches(addr, name) {
			return
		}
		byAddress[addr] = store.MailContact{
			OwnerAddress:   ownerUserID,
			ContactAddress: addr,
			DisplayName:    strings.TrimSpace(name),
			Tags:           tags,
			Role:           strings.TrimSpace(role),
			Skills:         skills,
			CurrentProject: strings.TrimSpace(project),
			Availability:   strings.TrimSpace(availability),
			PeerStatus:     strings.TrimSpace(peerStatus),
			IsActive:       strings.EqualFold(strings.TrimSpace(peerStatus), "running"),
			LastSeenAt:     timePtr(updatedAt),
			UpdatedAt:      updatedAt,
		}
	}

	addAuto(clawWorldSystemID, "Clawcolony", []string{"system", "auto"}, "admin", []string{"governance", "coordination"}, "community-ops", "always-on", "running", now)

	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return nil, err
	}
	bots = s.filterActiveBots(ctx, bots)
	botMeta := make(map[string]store.Bot, len(bots))
	for _, b := range bots {
		botMeta[b.BotID] = b
		addAuto(b.BotID, b.Name, []string{"user", "auto"}, "peer", nil, "", "unknown", b.Status, b.UpdatedAt)
	}

	// Enrich persisted contacts with dynamic peer status / last_seen if target user exists.
	for addr, c := range byAddress {
		if addr == clawWorldSystemID {
			c.PeerStatus = "running"
			c.IsActive = true
			c.LastSeenAt = timePtr(now)
			byAddress[addr] = c
			continue
		}
		if b, ok := botMeta[addr]; ok {
			c.PeerStatus = strings.TrimSpace(b.Status)
			c.IsActive = strings.EqualFold(c.PeerStatus, "running")
			c.LastSeenAt = timePtr(b.UpdatedAt)
			if strings.TrimSpace(c.DisplayName) == "" {
				c.DisplayName = strings.TrimSpace(b.Name)
			}
			if strings.TrimSpace(c.Role) == "" {
				c.Role = "peer"
			}
			byAddress[addr] = c
		}
	}

	out := make([]store.MailContact, 0, len(byAddress))
	for _, c := range byAddress {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ContactAddress == out[j].ContactAddress {
			return out[i].DisplayName < out[j].DisplayName
		}
		return out[i].ContactAddress < out[j].ContactAddress
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Server) handleMailContactsUpsert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailContactUpsertRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ContactUserID = strings.TrimSpace(req.ContactUserID)
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	req.Role = strings.TrimSpace(req.Role)
	req.CurrentProject = strings.TrimSpace(req.CurrentProject)
	req.Availability = strings.TrimSpace(req.Availability)
	if req.ContactUserID == "" {
		writeError(w, http.StatusBadRequest, "contact_user_id is required")
		return
	}
	item, err := s.store.UpsertMailContact(r.Context(), store.MailContact{
		OwnerAddress:   userID,
		ContactAddress: req.ContactUserID,
		DisplayName:    req.DisplayName,
		Tags:           req.Tags,
		Role:           req.Role,
		Skills:         req.Skills,
		CurrentProject: req.CurrentProject,
		Availability:   req.Availability,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleMailOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := s.requireAPIKeyUserID(w, r)
	if !ok {
		return
	}
	folder := strings.TrimSpace(r.URL.Query().Get("folder"))
	if folder == "" {
		folder = "all"
	}
	if folder != "all" && folder != "inbox" && folder != "outbox" {
		writeError(w, http.StatusBadRequest, "folder must be one of: all, inbox, outbox")
		return
	}
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if scope == "" {
		scope = "all"
	}
	if scope != "all" && scope != "read" && scope != "unread" {
		writeError(w, http.StatusBadRequest, "scope must be one of: all, read, unread")
		return
	}
	if scope == "all" {
		scope = ""
	}
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	fromTime, err := parseRFC3339Ptr(strings.TrimSpace(r.URL.Query().Get("from")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid from time, use RFC3339")
		return
	}
	toTime, err := parseRFC3339Ptr(strings.TrimSpace(r.URL.Query().Get("to")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid to time, use RFC3339")
		return
	}

	out := make([]store.MailItem, 0)
	folders := []string{}
	if folder == "all" {
		folders = []string{"inbox", "outbox"}
	} else {
		folders = []string{folder}
	}
	for _, f := range folders {
		items, err := s.store.ListMailbox(r.Context(), userID, f, scope, keyword, fromTime, toTime, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, items...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SentAt.Equal(out[j].SentAt) {
			if out[i].MessageID != out[j].MessageID {
				return out[i].MessageID > out[j].MessageID
			}
			return strings.Compare(out[i].ToAddress, out[j].ToAddress) > 0
		}
		return out[i].SentAt.After(out[j].SentAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": publicMailItems(out)})
}

func normalizeCollabPhase(v string) string {
	switch strings.TrimSpace(strings.ToLower(v)) {
	case "proposed":
		return "proposed"
	case "recruiting":
		return "recruiting"
	case "assigned":
		return "assigned"
	case "executing":
		return "executing"
	case "reviewing":
		return "reviewing"
	case "closed":
		return "closed"
	case "failed":
		return "failed"
	default:
		return ""
	}
}

func collabActionOwnerUserID(session store.CollabSession) string {
	if session.Kind == "upgrade_pr" {
		return upgradePRAuthorUserID(session)
	}
	if uid := strings.TrimSpace(session.OrchestratorUserID); uid != "" {
		return uid
	}
	return strings.TrimSpace(session.ProposerUserID)
}

func canTransitCollabPhase(from, to string) bool {
	if from == to {
		return true
	}
	allowed := map[string]map[string]bool{
		"proposed":   {"recruiting": true, "failed": true},
		"recruiting": {"assigned": true, "failed": true},
		"assigned":   {"executing": true, "failed": true},
		"executing":  {"reviewing": true, "closed": true, "failed": true},
		"reviewing":  {"executing": true, "closed": true, "failed": true},
	}
	return allowed[from][to]
}

func (s *Server) appendCollabEvent(ctx context.Context, collabID, actorID, eventType string, payload any) {
	data := ""
	if payload != nil {
		if b, err := json.Marshal(payload); err == nil {
			data = string(b)
		}
	}
	_, _ = s.store.AppendCollabEvent(ctx, store.CollabEvent{
		CollabID:  collabID,
		ActorID:   actorID,
		EventType: eventType,
		Payload:   data,
	})
}

func generateCollabID() string {
	return fmt.Sprintf("collab-%d-%04d", time.Now().UnixMilli(), rand.Intn(10000))
}

func (s *Server) handleCollabPropose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	proposerUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req collabProposeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Goal = strings.TrimSpace(req.Goal)
	req.Kind = strings.TrimSpace(strings.ToLower(req.Kind))
	if req.Kind == "" {
		req.Kind = "general"
	}
	req.Complexity = strings.TrimSpace(strings.ToLower(req.Complexity))
	if req.Complexity == "" {
		req.Complexity = "normal"
	}
	req.PRRepo = strings.TrimSpace(req.PRRepo)
	req.PRBranch = strings.TrimSpace(req.PRBranch)
	req.PRURL = strings.TrimSpace(req.PRURL)
	if req.Title == "" || req.Goal == "" {
		writeError(w, http.StatusBadRequest, "title and goal are required")
		return
	}
	if req.Kind == "upgrade_pr" && req.PRRepo == "" {
		writeError(w, http.StatusBadRequest, "pr_repo is required for kind=upgrade_pr")
		return
	}
	if req.Kind == "upgrade_pr" && req.PRURL == "" {
		writeError(w, http.StatusBadRequest, "pr_url is required for kind=upgrade_pr")
		return
	}
	if req.Kind == "upgrade_pr" {
		req.MinMembers = 1
		req.MaxMembers = 1
	} else {
		if req.MinMembers <= 0 {
			req.MinMembers = 2
		}
		if req.MaxMembers <= 0 {
			req.MaxMembers = 3
		}
	}
	if req.MaxMembers < req.MinMembers {
		writeError(w, http.StatusBadRequest, "max_members must be >= min_members")
		return
	}
	phase := "recruiting"
	var pull githubPullRequestRecord
	var reviewDeadline *time.Time
	if req.Kind == "upgrade_pr" {
		ref, err := parseGitHubPRRef(req.PRURL)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !strings.EqualFold(ref.Repo, req.PRRepo) {
			writeError(w, http.StatusBadRequest, "pr_url repo must match collab pr_repo")
			return
		}
		pull, err = s.fetchGitHubPullRequest(r.Context(), ref)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if pull.Merged || !strings.EqualFold(strings.TrimSpace(pull.State), "open") {
			writeError(w, http.StatusBadRequest, "upgrade_pr requires an open GitHub pull request")
			return
		}
		phase = "reviewing"
		reviewDeadline = timePtr(time.Now().UTC().Add(upgradePRDefaultReviewWindow))
		if req.PRBranch == "" {
			req.PRBranch = strings.TrimSpace(pull.Head.Ref)
		}
	}
	item, err := s.store.CreateCollabSession(r.Context(), store.CollabSession{
		CollabID:       generateCollabID(),
		Title:          req.Title,
		Goal:           req.Goal,
		Kind:           req.Kind,
		Complexity:     req.Complexity,
		Phase:          phase,
		ProposerUserID: proposerUserID,
		AuthorUserID:   proposerUserID,
		MinMembers:     req.MinMembers,
		MaxMembers:     req.MaxMembers,
		RequiredReviewers: func() int {
			if req.Kind == "upgrade_pr" {
				return 2
			}
			return 0
		}(),
		PRRepo:        req.PRRepo,
		PRBranch:      req.PRBranch,
		PRURL:         req.PRURL,
		PRNumber:      pull.Number,
		PRBaseSHA:     strings.TrimSpace(pull.Base.SHA),
		PRHeadSHA:     strings.TrimSpace(pull.Head.SHA),
		PRAuthorLogin: strings.TrimSpace(pull.User.Login),
		GitHubPRState: func() string {
			if req.Kind == "upgrade_pr" {
				return "open"
			}
			return ""
		}(),
		ReviewDeadlineAt: reviewDeadline,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.store.UpsertCollabParticipant(r.Context(), store.CollabParticipant{
		CollabID: item.CollabID,
		UserID:   proposerUserID,
		Role: func() string {
			if item.Kind == "upgrade_pr" {
				return "author"
			}
			return "orchestrator"
		}(),
		Status: "selected",
	})
	s.appendCollabEvent(r.Context(), item.CollabID, proposerUserID, "proposal.created", map[string]any{
		"title":      item.Title,
		"goal":       item.Goal,
		"complexity": item.Complexity,
	})
	if item.Kind == "upgrade_pr" {
		s.appendCollabEvent(r.Context(), item.CollabID, proposerUserID, "pr.updated", map[string]any{
			"pr_url":      item.PRURL,
			"pr_branch":   item.PRBranch,
			"pr_head_sha": item.PRHeadSHA,
			"pr_base_sha": item.PRBaseSHA,
		})
		s.notifyUpgradePRReviewOpen(r.Context(), item)
	} else {
		s.notifyCollabProposalPinned(r.Context(), item)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) notifyCollabProposalPinned(ctx context.Context, item store.CollabSession) {
	if strings.TrimSpace(item.CollabID) == "" {
		return
	}
	now := time.Now().UTC()
	targets := s.activeUserIDs(ctx)
	if len(targets) == 0 {
		return
	}
	subjectPrefix := fmt.Sprintf("[COMMUNITY-COLLAB][PINNED][PRIORITY:P1][ACTION:PROPOSAL] collab_id=%s", strings.TrimSpace(item.CollabID))
	receivers := make([]string, 0, len(targets))
	for _, uid := range targets {
		uid = strings.TrimSpace(uid)
		if uid == "" || isSystemRuntimeUserID(uid) || uid == strings.TrimSpace(item.ProposerUserID) {
			continue
		}
		life, err := s.store.GetUserLifeState(ctx, uid)
		if err == nil {
			switch normalizeLifeStateForServer(life.State) {
			case "dead", "hibernated":
				continue
			}
		}
		if s.hasUnreadPinnedSubject(ctx, uid, subjectPrefix, time.Time{}) {
			continue
		}
		if s.hasRecentInboxSubject(ctx, uid, subjectPrefix, now.Add(-collabProposalReminderResendCooldown), false) {
			continue
		}
		receivers = append(receivers, uid)
	}
	if len(receivers) == 0 {
		return
	}
	subject := fmt.Sprintf("%s title=%s"+refTag(skillCollabMode), subjectPrefix, strings.TrimSpace(item.Title))
	body := fmt.Sprintf(
		"新的协作提案已创建（置顶任务）。\n"+
			"collab_id=%s\nproposer_user_id=%s\ntitle=%s\ngoal=%s\ncomplexity=%s\nmembers=%d-%d\n\n"+
			"请立即评估是否参与：\n"+
			"1) 调用 /api/v1/collab/get?collab_id=<id> 查看目标与约束；\n"+
			"2) 若参与，调用 /api/v1/collab/apply 提交 pitch；\n"+
			"3) 若不参与，本轮可忽略该任务。",
		item.CollabID,
		item.ProposerUserID,
		item.Title,
		item.Goal,
		item.Complexity,
		item.MinMembers,
		item.MaxMembers,
	)
	s.sendMailAndPushHint(ctx, clawWorldSystemID, receivers, subject, body)
}

func (s *Server) handleCollabList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	kind := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("kind")))
	phase := normalizeCollabPhase(r.URL.Query().Get("phase"))
	proposer := strings.TrimSpace(r.URL.Query().Get("proposer_user_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	items, err := s.store.ListCollabSessions(r.Context(), kind, phase, proposer, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCollabGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	collabID := strings.TrimSpace(r.URL.Query().Get("collab_id"))
	if collabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	item, err := s.store.GetCollabSession(r.Context(), collabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": item})
}

func (s *Server) handleCollabApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req collabApplyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	req.Pitch = strings.TrimSpace(req.Pitch)
	req.ApplicationKind = strings.TrimSpace(strings.ToLower(req.ApplicationKind))
	req.EvidenceURL = strings.TrimSpace(req.EvidenceURL)
	if req.CollabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var item store.CollabParticipant
	if session.Kind == "upgrade_pr" {
		if session.Phase == "closed" || session.Phase == "failed" {
			writeError(w, http.StatusConflict, "collab is already closed")
			return
		}
		if req.ApplicationKind == "" {
			req.ApplicationKind = "discussion"
		}
		switch req.ApplicationKind {
		case "review":
			item, err = s.validateUpgradePRReviewApplication(r.Context(), session, userID, req.EvidenceURL)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			if item.Pitch == "" {
				item.Pitch = req.Pitch
			}
		case "discussion":
			item = store.CollabParticipant{
				CollabID:        req.CollabID,
				UserID:          userID,
				Role:            "discussion",
				Status:          "applied",
				Pitch:           req.Pitch,
				ApplicationKind: "discussion",
				EvidenceURL:     req.EvidenceURL,
			}
		default:
			writeError(w, http.StatusBadRequest, "application_kind must be review or discussion")
			return
		}
	} else {
		if session.Phase != "recruiting" {
			writeError(w, http.StatusConflict, "collab is not in recruiting phase")
			return
		}
		item = store.CollabParticipant{
			CollabID: req.CollabID,
			UserID:   userID,
			Status:   "applied",
			Pitch:    req.Pitch,
		}
	}
	item, err = s.store.UpsertCollabParticipant(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.appendCollabEvent(r.Context(), req.CollabID, userID, "participant.applied", map[string]any{
		"pitch":            item.Pitch,
		"application_kind": item.ApplicationKind,
		"evidence_url":     item.EvidenceURL,
		"verified":         item.Verified,
	})
	s.notifyCollabApply(r.Context(), session, userID)
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) notifyCollabApply(ctx context.Context, session store.CollabSession, applicantUserID string) {
	owner := collabActionOwnerUserID(session)
	if session.Kind == "upgrade_pr" {
		owner = upgradePRAuthorUserID(session)
	}
	if owner == "" || isSystemRuntimeUserID(owner) {
		return
	}
	participants, err := s.store.ListCollabParticipants(ctx, session.CollabID, "", 500)
	if err != nil {
		return
	}
	appliedCount := 0
	reviewCount := 0
	for _, p := range participants {
		if strings.EqualFold(p.Status, "applied") || strings.EqualFold(p.Status, "selected") {
			appliedCount++
		}
		if session.Kind == "upgrade_pr" && strings.EqualFold(p.ApplicationKind, "review") && p.Verified {
			reviewCount++
		}
	}
	readyNote := ""
	if session.Kind == "upgrade_pr" {
		required := session.RequiredReviewers
		if required <= 0 {
			required = 2
		}
		if reviewCount >= required {
			readyNote = fmt.Sprintf("\n\nVerified review applicants (%d) meet required_reviewers (%d). Review can proceed on the current head_sha.", reviewCount, required)
		}
	} else if appliedCount >= session.MinMembers {
		readyNote = fmt.Sprintf("\n\nApplicant count (%d) meets min_members (%d). You can now assign roles via POST /api/v1/collab/assign.", appliedCount, session.MinMembers)
	}
	subject := fmt.Sprintf("[COLLAB-APPLY] %s applied to %s (%d applicants)", applicantUserID, session.CollabID, appliedCount)
	body := fmt.Sprintf("collab_id=%s\napplicant=%s\napplied_count=%d", session.CollabID, applicantUserID, appliedCount)
	if session.Kind == "upgrade_pr" {
		required := session.RequiredReviewers
		if required <= 0 {
			required = 2
		}
		body = fmt.Sprintf("%s\nverified_review_applicants=%d\nrequired_reviewers=%d%s", body, reviewCount, required, readyNote)
	} else {
		body = fmt.Sprintf("%s\nmin_members=%d%s", body, session.MinMembers, readyNote)
	}
	s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{owner}, subject, body)
}

func (s *Server) handleCollabAssign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orchestratorUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req collabAssignRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	if req.CollabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	if len(req.Assignments) == 0 {
		writeError(w, http.StatusBadRequest, "assignments is required")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if session.Kind == "upgrade_pr" {
		writeError(w, http.StatusConflict, "upgrade_pr uses author-led flow; assign is not used")
		return
	}
	if session.Phase != "recruiting" {
		writeError(w, http.StatusConflict, "collab is not in recruiting phase")
		return
	}
	if len(req.Assignments) < session.MinMembers || len(req.Assignments) > session.MaxMembers {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("assignments count must be between %d and %d", session.MinMembers, session.MaxMembers))
		return
	}
	for _, it := range req.Assignments {
		userID := strings.TrimSpace(it.UserID)
		role := strings.TrimSpace(strings.ToLower(it.Role))
		if userID == "" || role == "" {
			writeError(w, http.StatusBadRequest, "assignment user_id and role are required")
			return
		}
		if _, err := s.store.UpsertCollabParticipant(r.Context(), store.CollabParticipant{
			CollabID: req.CollabID,
			UserID:   userID,
			Role:     role,
			Status:   "selected",
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	for _, uid := range req.RejectedUserIDs {
		uid = strings.TrimSpace(uid)
		if uid == "" {
			continue
		}
		if _, err := s.store.UpsertCollabParticipant(r.Context(), store.CollabParticipant{
			CollabID: req.CollabID,
			UserID:   uid,
			Status:   "rejected",
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	updated, err := s.store.UpdateCollabPhase(r.Context(), req.CollabID, "assigned", orchestratorUserID, req.StatusOrSummaryNote, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.appendCollabEvent(r.Context(), req.CollabID, orchestratorUserID, "participant.assigned", map[string]any{
		"assignments":       req.Assignments,
		"rejected_user_ids": req.RejectedUserIDs,
		"note":              req.StatusOrSummaryNote,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"item": updated})
}

func (s *Server) handleCollabStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orchestratorUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req collabStartRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	if req.CollabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if session.Kind == "upgrade_pr" {
		writeError(w, http.StatusConflict, "upgrade_pr starts immediately after propose; start is not used")
		return
	}
	if !canTransitCollabPhase(session.Phase, "executing") {
		writeError(w, http.StatusConflict, "phase transition not allowed")
		return
	}
	item, err := s.store.UpdateCollabPhase(r.Context(), req.CollabID, "executing", orchestratorUserID, req.StatusOrSummaryNote, nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.appendCollabEvent(r.Context(), req.CollabID, orchestratorUserID, "collab.executing", map[string]any{"note": req.StatusOrSummaryNote})
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleCollabSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req collabSubmitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	req.Role = strings.TrimSpace(strings.ToLower(req.Role))
	req.Kind = strings.TrimSpace(strings.ToLower(req.Kind))
	req.Summary = strings.TrimSpace(req.Summary)
	req.Content = strings.TrimSpace(req.Content)
	if req.CollabID == "" || req.Summary == "" {
		writeError(w, http.StatusBadRequest, "collab_id and summary are required")
		return
	}
	if utf8.RuneCountInString(req.Summary) < 8 {
		writeError(w, http.StatusBadRequest, "summary is too short; provide concrete outcome")
		return
	}
	if utf8.RuneCountInString(req.Content) < 60 {
		writeError(w, http.StatusBadRequest, "content is too short; include details/evidence/next step")
		return
	}
	if !containsSharedEvidenceToken(req.Content) && !hasStructuredOutputSections(req.Content) {
		writeError(w, http.StatusBadRequest, "content must include structured fields (evidence/result/next) or shared evidence ids")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if session.Phase != "executing" && session.Phase != "reviewing" {
		writeError(w, http.StatusConflict, "collab is not in executing/reviewing phase")
		return
	}
	item, err := s.store.CreateCollabArtifact(r.Context(), store.CollabArtifact{
		CollabID: req.CollabID,
		UserID:   userID,
		Role:     req.Role,
		Kind:     req.Kind,
		Summary:  req.Summary,
		Content:  req.Content,
		Status:   "submitted",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.appendCollabEvent(r.Context(), req.CollabID, userID, "artifact.submitted", map[string]any{
		"artifact_id": item.ID,
		"role":        item.Role,
		"kind":        item.Kind,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleCollabReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	reviewerUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req collabReviewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	req.Status = strings.TrimSpace(strings.ToLower(req.Status))
	req.ReviewNote = strings.TrimSpace(req.ReviewNote)
	if req.CollabID == "" || req.ArtifactID <= 0 {
		writeError(w, http.StatusBadRequest, "collab_id and artifact_id are required")
		return
	}
	if req.Status != "accepted" && req.Status != "rejected" {
		writeError(w, http.StatusBadRequest, "status must be accepted or rejected")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if session.Phase != "executing" && session.Phase != "reviewing" {
		writeError(w, http.StatusConflict, "collab is not in executing/reviewing phase")
		return
	}
	item, err := s.store.UpdateCollabArtifactReview(r.Context(), req.ArtifactID, req.Status, req.ReviewNote)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := s.store.UpdateCollabPhase(r.Context(), req.CollabID, "reviewing", session.OrchestratorUserID, session.LastStatusOrSummary, nil); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.appendCollabEvent(r.Context(), req.CollabID, reviewerUserID, "artifact.reviewed", map[string]any{
		"artifact_id": req.ArtifactID,
		"status":      req.Status,
		"review_note": req.ReviewNote,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleCollabClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	orchestratorUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req collabCloseRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	req.Result = strings.TrimSpace(strings.ToLower(req.Result))
	if req.CollabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	target := "closed"
	if req.Result == "failed" {
		target = "failed"
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	ownerUserID := collabActionOwnerUserID(session)
	if ownerUserID != "" && orchestratorUserID != ownerUserID {
		writeError(w, http.StatusForbidden, "only current collab owner can close collab")
		return
	}
	if !canTransitCollabPhase(session.Phase, target) {
		writeError(w, http.StatusConflict, "phase transition not allowed")
		return
	}
	item, rewards, closeErr := s.closeCollabInternal(r.Context(), session, req.Result, req.StatusOrSummaryNote, orchestratorUserID)
	if closeErr != nil {
		writeError(w, http.StatusInternalServerError, closeErr.Error())
		return
	}
	resp := map[string]any{"item": item}
	if len(rewards) > 0 {
		resp["community_rewards"] = rewards
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) handleCollabParticipants(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	collabID := strings.TrimSpace(r.URL.Query().Get("collab_id"))
	if collabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.store.ListCollabParticipants(r.Context(), collabID, status, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCollabArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	collabID := strings.TrimSpace(r.URL.Query().Get("collab_id"))
	if collabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.store.ListCollabArtifacts(r.Context(), collabID, userID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCollabEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	collabID := strings.TrimSpace(r.URL.Query().Get("collab_id"))
	if collabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.store.ListCollabEvents(r.Context(), collabID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCollabUpdatePR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req collabUpdatePRRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CollabID = strings.TrimSpace(req.CollabID)
	if req.CollabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), req.CollabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if session.Kind != "upgrade_pr" {
		writeError(w, http.StatusBadRequest, "update-pr is only valid for kind=upgrade_pr collabs")
		return
	}
	allowed := userID == session.ProposerUserID || userID == session.OrchestratorUserID || userID == upgradePRAuthorUserID(session)
	if !allowed {
		participants, _ := s.store.ListCollabParticipants(r.Context(), req.CollabID, "selected", 500)
		for _, p := range participants {
			if p.UserID == userID && p.Role == "author" {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "only proposer or author can update PR metadata")
		return
	}
	effectivePRURL := strings.TrimSpace(req.PRURL)
	if effectivePRURL == "" {
		effectivePRURL = session.PRURL
	}
	if session.PRURL != "" && req.PRURL != "" && !strings.EqualFold(strings.TrimSpace(req.PRURL), session.PRURL) {
		writeError(w, http.StatusConflict, "upgrade_pr collab is already bound to a pull request")
		return
	}
	ref, err := parseGitHubPRRef(effectivePRURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.EqualFold(ref.Repo, session.PRRepo) {
		writeError(w, http.StatusBadRequest, "pr_url repo must match collab pr_repo")
		return
	}
	pull, err := s.fetchGitHubPullRequest(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	firstRegistration := strings.TrimSpace(session.PRURL) == "" || session.ReviewDeadlineAt == nil
	reviewDeadline := session.ReviewDeadlineAt
	if reviewDeadline == nil {
		reviewDeadline = timePtr(time.Now().UTC().Add(upgradePRDefaultReviewWindow))
	}
	effectivePRBranch := strings.TrimSpace(req.PRBranch)
	if effectivePRBranch == "" {
		effectivePRBranch = strings.TrimSpace(session.PRBranch)
	}
	if effectivePRBranch == "" {
		effectivePRBranch = strings.TrimSpace(pull.Head.Ref)
	}
	updated, err := s.store.UpdateCollabPR(r.Context(), store.CollabPRUpdate{
		CollabID:      req.CollabID,
		PRBranch:      effectivePRBranch,
		PRURL:         effectivePRURL,
		PRNumber:      pull.Number,
		PRBaseSHA:     strings.TrimSpace(pull.Base.SHA),
		PRHeadSHA:     strings.TrimSpace(pull.Head.SHA),
		PRAuthorLogin: strings.TrimSpace(pull.User.Login),
		GitHubPRState: func() string {
			if pull.Merged {
				return "merged"
			}
			return strings.ToLower(strings.TrimSpace(pull.State))
		}(),
		PRMergeCommitSHA: strings.TrimSpace(pull.MergeCommitSHA),
		ReviewDeadlineAt: reviewDeadline,
		PRMergedAt:       pull.MergedAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if updated.Phase == "executing" && strings.EqualFold(updated.GitHubPRState, "open") {
		if phaseItem, phaseErr := s.store.UpdateCollabPhase(r.Context(), updated.CollabID, "reviewing", updated.OrchestratorUserID, "pull request opened and waiting for review", nil); phaseErr == nil {
			updated = phaseItem
		}
	}
	s.appendCollabEvent(r.Context(), req.CollabID, userID, "pr.updated", map[string]any{
		"pr_url":      updated.PRURL,
		"pr_branch":   updated.PRBranch,
		"pr_head_sha": updated.PRHeadSHA,
		"pr_base_sha": updated.PRBaseSHA,
	})
	if firstRegistration {
		s.notifyUpgradePRReviewOpen(r.Context(), updated)
	} else if session.PRHeadSHA != "" && !strings.EqualFold(session.PRHeadSHA, updated.PRHeadSHA) {
		s.notifyUpgradePRHeadChanged(r.Context(), updated, session.PRHeadSHA, updated.PRHeadSHA)
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": updated})
}

func (s *Server) handleCollabMergeGate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	collabID := strings.TrimSpace(r.URL.Query().Get("collab_id"))
	if collabID == "" {
		writeError(w, http.StatusBadRequest, "collab_id is required")
		return
	}
	session, err := s.store.GetCollabSession(r.Context(), collabID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if session.Kind != "upgrade_pr" {
		writeError(w, http.StatusBadRequest, "merge-gate is only valid for kind=upgrade_pr collabs")
		return
	}
	if strings.TrimSpace(session.PRURL) == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"collab_id":               session.CollabID,
			"pr_url":                  session.PRURL,
			"pr_head_sha":             session.PRHeadSHA,
			"valid_reviewers_at_head": 0,
			"approvals_at_head":       0,
			"disagreements_at_head":   0,
			"review_complete":         false,
			"mergeable":               false,
			"review_deadline_at":      session.ReviewDeadlineAt,
			"blockers":                []string{"pr_url is not registered"},
		})
		return
	}
	ref, err := parseGitHubPRRef(session.PRURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	pull, err := s.fetchGitHubPullRequest(r.Context(), ref)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	session.PRHeadSHA = strings.TrimSpace(pull.Head.SHA)
	session.GitHubPRState = func() string {
		if pull.Merged {
			return "merged"
		}
		return strings.ToLower(strings.TrimSpace(pull.State))
	}()
	status, err := s.evaluateUpgradePRReviews(r.Context(), session, session.PRHeadSHA)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"collab_id":               session.CollabID,
		"pr_url":                  session.PRURL,
		"pr_head_sha":             session.PRHeadSHA,
		"valid_reviewers_at_head": status.ValidReviewersAtHead,
		"approvals_at_head":       status.ApprovalsAtHead,
		"disagreements_at_head":   status.DisagreementsAtHead,
		"review_complete":         status.ReviewComplete,
		"review_deadline_at":      session.ReviewDeadlineAt,
		"tests_passed":            "unknown",
		"mergeable":               status.Mergeable,
		"blockers":                status.Blockers,
	})
}

func normalizeKBProposalStatus(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "discussing", "voting", "approved", "rejected", "applied":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeKBVote(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "no", "abstain":
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func isGovernanceSection(section string) bool {
	s := strings.TrimSpace(strings.ToLower(section))
	return s == "governance" || strings.HasPrefix(s, "governance/")
}

func governanceScanLimit(limit int) int {
	if limit <= 0 {
		limit = 200
	}
	scan := limit * 8
	if scan < limit {
		scan = limit
	}
	if scan > 5000 {
		scan = 5000
	}
	return scan
}

func (s *Server) handleGovernanceDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	scanLimit := governanceScanLimit(limit)
	all, err := s.store.ListKBEntries(r.Context(), "", keyword, scanLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]store.KBEntry, 0, limit)
	for _, it := range all {
		if !isGovernanceSection(it.Section) {
			continue
		}
		items = append(items, it)
		if len(items) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"section_prefix": "governance",
		"keyword":        keyword,
		"limit":          limit,
		"scan_limit":     scanLimit,
		"items":          items,
	})
}

func (s *Server) handleGovernanceProposals(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := normalizeKBProposalStatus(r.URL.Query().Get("status"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	scanLimit := governanceScanLimit(limit)
	all, err := s.store.ListKBProposals(r.Context(), status, scanLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]map[string]any, 0, limit)
	for _, p := range all {
		ch, err := s.store.GetKBProposalChange(r.Context(), p.ID)
		if err != nil {
			continue
		}
		if !isGovernanceSection(ch.Section) {
			continue
		}
		items = append(items, map[string]any{
			"proposal": p,
			"change":   ch,
		})
		if len(items) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         status,
		"section_prefix": "governance",
		"limit":          limit,
		"scan_limit":     scanLimit,
		"items":          items,
	})
}

func (s *Server) handleGovernanceOverview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	scanLimit := governanceScanLimit(limit)
	all, err := s.store.ListKBProposals(r.Context(), "", scanLimit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	type summaryItem struct {
		ProposalID         int64      `json:"proposal_id"`
		Title              string     `json:"title"`
		Status             string     `json:"status"`
		ProposerUserID     string     `json:"proposer_user_id"`
		CurrentRevisionID  int64      `json:"current_revision_id"`
		VotingRevisionID   int64      `json:"voting_revision_id"`
		Section            string     `json:"section"`
		DiscussionDeadline *time.Time `json:"discussion_deadline_at,omitempty"`
		VotingDeadline     *time.Time `json:"voting_deadline_at,omitempty"`
		EnrolledCount      int        `json:"enrolled_count"`
		VotedCount         int        `json:"voted_count"`
		PendingVoters      []string   `json:"pending_voters,omitempty"`
		DiscussionOverdue  bool       `json:"discussion_overdue"`
		VotingOverdue      bool       `json:"voting_overdue"`
	}
	items := make([]summaryItem, 0, limit)
	statusCount := map[string]int{
		"discussing": 0,
		"voting":     0,
		"approved":   0,
		"rejected":   0,
		"applied":    0,
	}
	for _, p := range all {
		ch, err := s.store.GetKBProposalChange(r.Context(), p.ID)
		if err != nil || !isGovernanceSection(ch.Section) {
			continue
		}
		statusCount[p.Status]++
		enrolled, _ := s.store.ListKBProposalEnrollments(r.Context(), p.ID)
		votes, _ := s.store.ListKBVotes(r.Context(), p.ID)
		votedSet := make(map[string]struct{}, len(votes))
		for _, v := range votes {
			uid := strings.TrimSpace(v.UserID)
			if uid == "" {
				continue
			}
			votedSet[uid] = struct{}{}
		}
		pending := make([]string, 0, len(enrolled))
		for _, e := range enrolled {
			uid := strings.TrimSpace(e.UserID)
			if uid == "" {
				continue
			}
			if _, ok := votedSet[uid]; ok {
				continue
			}
			pending = append(pending, uid)
		}
		si := summaryItem{
			ProposalID:         p.ID,
			Title:              p.Title,
			Status:             p.Status,
			ProposerUserID:     p.ProposerUserID,
			CurrentRevisionID:  p.CurrentRevisionID,
			VotingRevisionID:   p.VotingRevisionID,
			Section:            ch.Section,
			DiscussionDeadline: p.DiscussionDeadlineAt,
			VotingDeadline:     p.VotingDeadlineAt,
			EnrolledCount:      len(enrolled),
			VotedCount:         len(votes),
			PendingVoters:      pending,
			DiscussionOverdue:  p.Status == "discussing" && p.DiscussionDeadlineAt != nil && now.After(*p.DiscussionDeadlineAt),
			VotingOverdue:      p.Status == "voting" && p.VotingDeadlineAt != nil && now.After(*p.VotingDeadlineAt),
		}
		items = append(items, si)
		if len(items) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"section_prefix": "governance",
		"limit":          limit,
		"scan_limit":     scanLimit,
		"status_count":   statusCount,
		"items":          items,
	})
}

func (s *Server) handleGovernanceProtocol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol": "knowledgebase-governance-v1",
		"states":   []string{"discussing", "voting", "approved", "rejected", "applied"},
		"defaults": map[string]any{
			"vote_threshold_pct":        80,
			"vote_window_seconds":       300,
			"discussion_window_seconds": 300,
		},
		"requirements": map[string]any{
			"vote_requires_ack":       true,
			"abstain_requires_reason": true,
			"apply_requires_status":   "approved",
		},
		"automation": map[string]any{
			"discussing_auto_progress": true,
			"discussing_no_enroll":     "auto_reject",
			"discussing_has_enroll":    "auto_start_voting",
			"voting_expired":           "auto_finalize_by_thresholds",
			"reminder_interval_sec":    int64(s.worldTickInterval() / time.Second),
		},
		"flow": []map[string]any{
			{"stage": "create", "api": "POST /api/v1/kb/proposals"},
			{"stage": "enroll", "api": "POST /api/v1/kb/proposals/enroll"},
			{"stage": "discuss", "api": "POST /api/v1/kb/proposals/comment | POST /api/v1/kb/proposals/revise"},
			{"stage": "start_vote", "api": "POST /api/v1/kb/proposals/start-vote"},
			{"stage": "ack", "api": "POST /api/v1/kb/proposals/ack"},
			{"stage": "vote", "api": "POST /api/v1/kb/proposals/vote"},
			{"stage": "apply", "api": "POST /api/v1/kb/proposals/apply"},
		},
	})
}

func (s *Server) handleKBEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	section := strings.TrimSpace(r.URL.Query().Get("section"))
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.store.ListKBEntries(r.Context(), section, keyword, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleKBSections(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	items, err := s.store.ListKBSections(r.Context(), keyword, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleKBEntryHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	entryID := parseInt64(r.URL.Query().Get("entry_id"))
	if entryID <= 0 {
		writeError(w, http.StatusBadRequest, "entry_id is required")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	entry, err := s.store.GetKBEntry(r.Context(), entryID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	items, err := s.store.ListKBEntryHistory(r.Context(), entryID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entry":   entry,
		"history": items,
	})
}

func (s *Server) handleKBProposals(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		status := normalizeKBProposalStatus(r.URL.Query().Get("status"))
		limit := parseLimit(r.URL.Query().Get("limit"), 200)
		items, err := s.store.ListKBProposals(r.Context(), status, limit)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		s.handleKBProposalCreate(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleKBProposalCreate(w http.ResponseWriter, r *http.Request) {
	proposerUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req kbProposalCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Category = strings.TrimSpace(strings.ToLower(req.Category))
	req.References = normalizedCitationRefsOrEmpty(req.References)
	req.Change.OpType = strings.TrimSpace(strings.ToLower(req.Change.OpType))
	req.Change.Section = strings.TrimSpace(req.Change.Section)
	req.Change.Title = strings.TrimSpace(req.Change.Title)
	req.Change.OldContent = strings.TrimSpace(req.Change.OldContent)
	req.Change.NewContent = strings.TrimSpace(req.Change.NewContent)
	req.Change.DiffText = strings.TrimSpace(req.Change.DiffText)
	if req.Title == "" || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "title and reason are required")
		return
	}
	if req.VoteThresholdPct <= 0 {
		req.VoteThresholdPct = 80
	}
	if req.VoteThresholdPct > 100 {
		writeError(w, http.StatusBadRequest, "vote_threshold_pct must be <= 100")
		return
	}
	if req.VoteWindowSeconds <= 0 {
		req.VoteWindowSeconds = 300
	}
	if req.DiscussionWindowSeconds <= 0 {
		req.DiscussionWindowSeconds = 300
	}
	if req.DiscussionWindowSeconds > 86400 {
		writeError(w, http.StatusBadRequest, "discussion_window_seconds must be <= 86400")
		return
	}
	if req.Change.OpType != "add" && req.Change.OpType != "update" && req.Change.OpType != "delete" {
		writeError(w, http.StatusBadRequest, "change.op_type must be add|update|delete")
		return
	}
	if req.Change.DiffText == "" {
		writeError(w, http.StatusBadRequest, "change.diff_text is required")
		return
	}
	if utf8.RuneCountInString(req.Change.DiffText) < 12 {
		writeError(w, http.StatusBadRequest, "change.diff_text is too short")
		return
	}
	switch req.Change.OpType {
	case "add":
		if req.Change.Section == "" || req.Change.Title == "" || req.Change.NewContent == "" {
			writeError(w, http.StatusBadRequest, "add requires section, title, new_content")
			return
		}
	case "update":
		if req.Change.TargetEntryID <= 0 {
			writeError(w, http.StatusBadRequest, "update requires target_entry_id")
			return
		}
		if req.Change.NewContent == "" {
			writeError(w, http.StatusBadRequest, "update requires new_content")
			return
		}
		target, err := s.store.GetKBEntry(r.Context(), req.Change.TargetEntryID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "target entry not found")
			return
		}
		if req.Change.Section == "" {
			req.Change.Section = target.Section
		}
		if req.Change.Title == "" {
			req.Change.Title = target.Title
		}
		if req.Change.OldContent == "" {
			req.Change.OldContent = target.Content
		}
	case "delete":
		if req.Change.TargetEntryID <= 0 {
			writeError(w, http.StatusBadRequest, "delete requires target_entry_id")
			return
		}
		target, err := s.store.GetKBEntry(r.Context(), req.Change.TargetEntryID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "target entry not found")
			return
		}
		if req.Change.Section == "" {
			req.Change.Section = target.Section
		}
		if req.Change.Title == "" {
			req.Change.Title = target.Title
		}
		if req.Change.OldContent == "" {
			req.Change.OldContent = target.Content
		}
	}
	if req.Category == "" {
		req.Category = deriveKBCategory(req.Change.Section, req.Change.NewContent)
	}
	discussDeadline := time.Now().UTC().Add(time.Duration(req.DiscussionWindowSeconds) * time.Second)
	proposal, change, err := s.store.CreateKBProposal(r.Context(), store.KBProposal{
		ProposerUserID:       proposerUserID,
		Title:                req.Title,
		Reason:               req.Reason,
		Status:               "discussing",
		VoteThresholdPct:     req.VoteThresholdPct,
		VoteWindowSeconds:    req.VoteWindowSeconds,
		DiscussionDeadlineAt: &discussDeadline,
	}, store.KBProposalChange{
		OpType:        req.Change.OpType,
		TargetEntryID: req.Change.TargetEntryID,
		Section:       req.Change.Section,
		Title:         req.Change.Title,
		OldContent:    req.Change.OldContent,
		NewContent:    req.Change.NewContent,
		DiffText:      req.Change.DiffText,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.store.CreateKBThreadMessage(r.Context(), store.KBThreadMessage{
		ProposalID:  proposal.ID,
		AuthorID:    proposerUserID,
		MessageType: "system",
		Content:     fmt.Sprintf("proposal created: %s", proposal.Title),
	})
	active := s.activeUserIDs(r.Context())
	recipients := make([]string, 0, len(active))
	for _, uid := range active {
		if uid == proposerUserID {
			continue
		}
		recipients = append(recipients, uid)
	}
	if len(recipients) > 0 {
		subject := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P2][ACTION:ENROLL] #%d %s"+refTag(skillKnowledgeBase), proposal.ID, proposal.Title)
		body := fmt.Sprintf(
			"你有新的 knowledgebase 提案待处理。\nproposal_id=%d\ntitle=%s\nreason=%s\n要求：尽快参与。\n动作：调用 /api/v1/kb/proposals/enroll 报名；随后关注投票通知。",
			proposal.ID, proposal.Title, proposal.Reason,
		)
		s.sendMailAndPushHint(r.Context(), clawWorldSystemID, recipients, subject, body)
	}
	_ = s.upsertProposalKnowledgeMeta(r.Context(), proposal.ID, knowledgeMeta{
		ProposalID:    proposal.ID,
		Category:      req.Category,
		References:    req.References,
		AuthorUserID:  proposerUserID,
		ContentTokens: economy.CalculateToken(req.Change.NewContent),
	})
	if isGovernanceKBSection(req.Change.Section) {
		_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
			EventKey:     fmt.Sprintf("governance.proposal.create:%d", proposal.ID),
			Kind:         "governance.proposal.create",
			UserID:       proposerUserID,
			ResourceType: "kb.proposal",
			ResourceID:   fmt.Sprintf("%d", proposal.ID),
			Meta: map[string]any{
				"proposal_id": proposal.ID,
				"section":     req.Change.Section,
				"category":    req.Category,
			},
		})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"proposal": proposal,
		"change":   change,
	})
}

func (s *Server) handleKBProposalGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	proposalID := parseInt64(r.URL.Query().Get("proposal_id"))
	if proposalID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id is required")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), proposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	change, err := s.store.GetKBProposalChange(r.Context(), proposalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enrollments, _ := s.store.ListKBProposalEnrollments(r.Context(), proposalID)
	votes, _ := s.store.ListKBVotes(r.Context(), proposalID)
	revisions, _ := s.store.ListKBRevisions(r.Context(), proposalID, 200)
	acks, _ := s.store.ListKBAcks(r.Context(), proposalID, proposal.CurrentRevisionID)
	respProposal := proposal
	respProposal.EnrolledCount = len(enrollments)
	voteYes, voteNo, voteAbstain := 0, 0, 0
	for _, v := range votes {
		switch normalizeKBVote(v.Vote) {
		case "yes":
			voteYes++
		case "no":
			voteNo++
		case "abstain":
			voteAbstain++
		}
	}
	respProposal.VoteYes = voteYes
	respProposal.VoteNo = voteNo
	respProposal.VoteAbstain = voteAbstain
	respProposal.ParticipationCount = voteYes + voteNo
	writeJSON(w, http.StatusOK, map[string]any{
		"proposal":    respProposal,
		"change":      change,
		"revisions":   revisions,
		"acks":        acks,
		"enrollments": enrollments,
		"votes":       votes,
	})
}

func (s *Server) handleKBProposalEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req kbProposalEnrollRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ProposalID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id is required")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if proposal.Status != "discussing" && proposal.Status != "voting" {
		writeError(w, http.StatusConflict, "proposal is not open for enrollment")
		return
	}
	item, err := s.store.EnrollKBProposal(r.Context(), req.ProposalID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.store.CreateKBThreadMessage(r.Context(), store.KBThreadMessage{
		ProposalID:  req.ProposalID,
		AuthorID:    userID,
		MessageType: "system",
		Content:     "user enrolled",
	})
	if change, cerr := s.store.GetKBProposalChange(r.Context(), req.ProposalID); cerr == nil && isGovernanceKBSection(change.Section) {
		_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
			EventKey:     fmt.Sprintf("governance.proposal.cosign:%d:%s", req.ProposalID, userID),
			Kind:         "governance.proposal.cosign",
			UserID:       userID,
			ResourceType: "kb.proposal",
			ResourceID:   fmt.Sprintf("%d", req.ProposalID),
			Meta: map[string]any{
				"proposal_id": req.ProposalID,
				"section":     change.Section,
			},
		})
	}
	s.kbAdvanceGenesisBootstrapDiscussing(r.Context(), proposal, time.Now().UTC())
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleKBProposalRevisions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	proposalID := parseInt64(r.URL.Query().Get("proposal_id"))
	if proposalID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id is required")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	proposal, err := s.store.GetKBProposal(r.Context(), proposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	revisions, err := s.store.ListKBRevisions(r.Context(), proposalID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	acks, _ := s.store.ListKBAcks(r.Context(), proposalID, proposal.CurrentRevisionID)
	writeJSON(w, http.StatusOK, map[string]any{
		"proposal":  proposal,
		"revisions": revisions,
		"acks":      acks,
	})
}

func (s *Server) handleKBProposalRevise(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req kbProposalReviseRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Change.OpType = strings.TrimSpace(strings.ToLower(req.Change.OpType))
	req.Category = strings.TrimSpace(strings.ToLower(req.Category))
	referencesProvided := req.References != nil
	req.References = normalizeCitationRefs(req.References)
	req.Change.Section = strings.TrimSpace(req.Change.Section)
	req.Change.Title = strings.TrimSpace(req.Change.Title)
	req.Change.OldContent = strings.TrimSpace(req.Change.OldContent)
	req.Change.NewContent = strings.TrimSpace(req.Change.NewContent)
	req.Change.DiffText = strings.TrimSpace(req.Change.DiffText)
	if req.ProposalID <= 0 || req.BaseRevisionID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id and base_revision_id are required")
		return
	}
	if req.Change.OpType != "add" && req.Change.OpType != "update" && req.Change.OpType != "delete" {
		writeError(w, http.StatusBadRequest, "change.op_type must be add|update|delete")
		return
	}
	if req.Change.DiffText == "" {
		writeError(w, http.StatusBadRequest, "change.diff_text is required")
		return
	}
	if utf8.RuneCountInString(req.Change.DiffText) < 12 {
		writeError(w, http.StatusBadRequest, "change.diff_text is too short")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if proposal.Status != "discussing" {
		writeError(w, http.StatusConflict, "proposal is not in discussing phase")
		return
	}
	if req.Category == "" || !referencesProvided {
		meta, ok, metaErr := s.proposalKnowledgeMetaForProposal(r.Context(), req.ProposalID)
		if metaErr != nil {
			writeError(w, http.StatusInternalServerError, metaErr.Error())
			return
		}
		if req.Category == "" {
			if ok && strings.TrimSpace(meta.Category) != "" {
				req.Category = strings.TrimSpace(strings.ToLower(meta.Category))
			} else {
				req.Category = deriveKBCategory(req.Change.Section, req.Change.NewContent)
			}
		}
		if !referencesProvided {
			if ok {
				req.References = append([]citationRef(nil), meta.References...)
			} else {
				req.References = []citationRef{}
			}
		}
	}
	if req.Category == "" {
		req.Category = deriveKBCategory(req.Change.Section, req.Change.NewContent)
	}
	if req.References == nil {
		req.References = []citationRef{}
	}
	var discussionDeadline time.Time
	if req.DiscussionWindowSec > 0 {
		discussionDeadline = time.Now().UTC().Add(time.Duration(req.DiscussionWindowSec) * time.Second)
	}
	rev, updatedProposal, updatedChange, err := s.store.CreateKBRevision(r.Context(), req.ProposalID, req.BaseRevisionID, userID, store.KBProposalChange{
		OpType:        req.Change.OpType,
		TargetEntryID: req.Change.TargetEntryID,
		Section:       req.Change.Section,
		Title:         req.Change.Title,
		OldContent:    req.Change.OldContent,
		NewContent:    req.Change.NewContent,
		DiffText:      req.Change.DiffText,
	}, discussionDeadline)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "stale") {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.store.CreateKBThreadMessage(r.Context(), store.KBThreadMessage{
		ProposalID:  req.ProposalID,
		AuthorID:    userID,
		MessageType: "revision",
		Content:     fmt.Sprintf("revision=%d base=%d diff=%s", rev.ID, req.BaseRevisionID, req.Change.DiffText),
	})
	_ = s.upsertProposalKnowledgeMeta(r.Context(), req.ProposalID, knowledgeMeta{
		ProposalID:    req.ProposalID,
		Category:      req.Category,
		References:    req.References,
		AuthorUserID:  updatedProposal.ProposerUserID,
		ContentTokens: economy.CalculateToken(req.Change.NewContent),
	})
	writeJSON(w, http.StatusAccepted, map[string]any{
		"revision": rev,
		"proposal": updatedProposal,
		"change":   updatedChange,
	})
}

func (s *Server) handleKBProposalAck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req kbProposalAckRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ProposalID <= 0 || req.RevisionID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id and revision_id are required")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if proposal.Status != "discussing" && proposal.Status != "voting" {
		writeError(w, http.StatusConflict, "proposal is closed")
		return
	}
	if req.RevisionID != proposal.CurrentRevisionID && req.RevisionID != proposal.VotingRevisionID {
		writeError(w, http.StatusConflict, "revision_id is not current active revision")
		return
	}
	item, err := s.store.AckKBProposal(r.Context(), req.ProposalID, req.RevisionID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.store.CreateKBThreadMessage(r.Context(), store.KBThreadMessage{
		ProposalID:  req.ProposalID,
		AuthorID:    userID,
		MessageType: "ack",
		Content:     fmt.Sprintf("ack revision=%d", req.RevisionID),
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleKBProposalComment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req kbProposalCommentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.ProposalID <= 0 || req.RevisionID <= 0 || req.Content == "" {
		writeError(w, http.StatusBadRequest, "proposal_id, revision_id, content are required")
		return
	}
	if utf8.RuneCountInString(req.Content) < 12 {
		writeError(w, http.StatusBadRequest, "content is too short; provide concrete feedback")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if proposal.Status != "discussing" {
		writeError(w, http.StatusConflict, "proposal is not in discussing phase")
		return
	}
	if proposal.CurrentRevisionID != req.RevisionID {
		writeError(w, http.StatusConflict, "revision_id is stale; use current_revision_id")
		return
	}
	item, err := s.store.CreateKBThreadMessage(r.Context(), store.KBThreadMessage{
		ProposalID:  req.ProposalID,
		AuthorID:    userID,
		MessageType: "comment",
		Content:     fmt.Sprintf("[revision=%d] %s", req.RevisionID, req.Content),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleKBProposalThread(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	proposalID := parseInt64(r.URL.Query().Get("proposal_id"))
	if proposalID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id is required")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 500)
	items, err := s.store.ListKBThreadMessages(r.Context(), proposalID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleKBProposalStartVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req kbProposalStartVoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ProposalID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id is required")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if proposal.Status != "discussing" {
		writeError(w, http.StatusConflict, "proposal is not in discussing phase")
		return
	}
	if proposal.CurrentRevisionID <= 0 {
		writeError(w, http.StatusConflict, "proposal has no active revision")
		return
	}
	if proposal.ProposerUserID != userID {
		writeError(w, http.StatusForbidden, "only proposer can start vote")
		return
	}
	deadline := time.Now().UTC().Add(time.Duration(proposal.VoteWindowSeconds) * time.Second)
	item, err := s.store.StartKBProposalVoting(r.Context(), req.ProposalID, deadline)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _ = s.store.CreateKBThreadMessage(r.Context(), store.KBThreadMessage{
		ProposalID:  req.ProposalID,
		AuthorID:    clawWorldSystemID,
		MessageType: "system",
		Content:     fmt.Sprintf("voting started; revision_id=%d; deadline=%s", item.VotingRevisionID, deadline.Format(time.RFC3339)),
	})
	enrolled, _ := s.store.ListKBProposalEnrollments(r.Context(), req.ProposalID)
	if len(enrolled) > 0 {
		recipients := make([]string, 0, len(enrolled))
		for _, e := range enrolled {
			recipients = append(recipients, e.UserID)
		}
		subject := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #%d %s"+refTag(skillKnowledgeBase), req.ProposalID, proposal.Title)
		body := fmt.Sprintf(
			"knowledgebase 提案进入投票阶段（置顶）。\nproposal_id=%d\nrevision_id=%d\ndeadline=%s\n要求：先 ack 当前 revision，再立即投票。\n动作：调用 /api/v1/kb/proposals/ack 后，再调用 /api/v1/kb/proposals/vote 提交 yes/no/abstain（abstain 必填 reason）。",
			req.ProposalID, item.VotingRevisionID, deadline.Format(time.RFC3339),
		)
		s.sendMailAndPushHint(r.Context(), clawWorldSystemID, recipients, subject, body)
	}
	if change, cerr := s.store.GetKBProposalChange(r.Context(), req.ProposalID); cerr == nil && isGovernanceKBSection(change.Section) {
		_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
			EventKey:     fmt.Sprintf("governance.proposal.entered_voting:%d", req.ProposalID),
			Kind:         "governance.proposal.entered_voting",
			UserID:       userID,
			ResourceType: "kb.proposal",
			ResourceID:   fmt.Sprintf("%d", req.ProposalID),
			Meta: map[string]any{
				"proposal_id": req.ProposalID,
				"section":     change.Section,
			},
		})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"proposal": item})
}

func (s *Server) handleKBProposalVote(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req kbProposalVoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Vote = normalizeKBVote(req.Vote)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.ProposalID <= 0 || req.RevisionID <= 0 || req.Vote == "" {
		writeError(w, http.StatusBadRequest, "proposal_id, revision_id, vote are required")
		return
	}
	if req.Vote == "abstain" && req.Reason == "" {
		writeError(w, http.StatusBadRequest, "abstain requires reason")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if proposal.Status != "voting" {
		writeError(w, http.StatusConflict, "proposal is not in voting phase")
		return
	}
	if proposal.VotingRevisionID <= 0 {
		writeError(w, http.StatusConflict, "voting revision is not set")
		return
	}
	if req.RevisionID != proposal.VotingRevisionID {
		writeError(w, http.StatusConflict, "revision_id mismatch; use voting_revision_id")
		return
	}
	if proposal.VotingDeadlineAt != nil && time.Now().UTC().After(*proposal.VotingDeadlineAt) {
		writeError(w, http.StatusConflict, "voting is closed")
		return
	}
	enrollments, err := s.store.ListKBProposalEnrollments(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	enrolled := false
	for _, it := range enrollments {
		if it.UserID == userID {
			enrolled = true
			break
		}
	}
	if !enrolled {
		writeError(w, http.StatusForbidden, "user is not enrolled")
		return
	}
	acks, err := s.store.ListKBAcks(r.Context(), req.ProposalID, proposal.VotingRevisionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	acked := false
	for _, a := range acks {
		if a.UserID == userID {
			acked = true
			break
		}
	}
	if !acked {
		writeError(w, http.StatusForbidden, "user must ack voting revision before voting")
		return
	}
	item, err := s.store.CastKBVote(r.Context(), store.KBVote{
		ProposalID: req.ProposalID,
		UserID:     userID,
		Vote:       req.Vote,
		Reason:     req.Reason,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Reason != "" {
		_, _ = s.store.CreateKBThreadMessage(r.Context(), store.KBThreadMessage{
			ProposalID:  req.ProposalID,
			AuthorID:    userID,
			MessageType: "vote_reason",
			Content:     fmt.Sprintf("revision=%d vote=%s reason=%s", req.RevisionID, req.Vote, req.Reason),
		})
	}
	if change, cerr := s.store.GetKBProposalChange(r.Context(), req.ProposalID); cerr == nil && isGovernanceKBSection(change.Section) {
		_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
			EventKey:     fmt.Sprintf("governance.proposal.vote:%d:%s", req.ProposalID, userID),
			Kind:         "governance.proposal.vote",
			UserID:       userID,
			ResourceType: "kb.proposal",
			ResourceID:   fmt.Sprintf("%d", req.ProposalID),
			Meta: map[string]any{
				"proposal_id": req.ProposalID,
				"section":     change.Section,
				"vote":        item.Vote,
			},
		})
	}
	latestEnrollments, err := s.store.ListKBProposalEnrollments(r.Context(), req.ProposalID)
	if err == nil && len(latestEnrollments) > 0 {
		latestVotes, err := s.store.ListKBVotes(r.Context(), req.ProposalID)
		if err == nil && len(latestVotes) >= len(latestEnrollments) {
			_, _ = s.closeKBProposalByStats(r.Context(), proposal, latestEnrollments, latestVotes, time.Now().UTC())
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleKBProposalApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req kbProposalApplyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ProposalID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id is required")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if proposal.Status == "applied" {
		resp := map[string]any{
			"proposal":        proposal,
			"already_applied": true,
		}
		if change, cerr := s.store.GetKBProposalChange(r.Context(), req.ProposalID); cerr == nil && change.TargetEntryID > 0 {
			if entry, eerr := s.store.GetKBEntry(r.Context(), change.TargetEntryID); eerr == nil {
				resp["entry"] = entry
			}
		}
		writeJSON(w, http.StatusAccepted, resp)
		return
	}
	if proposal.Status != "approved" {
		writeError(w, http.StatusConflict, "proposal is not approved")
		return
	}
	if _, metaErr := s.ensureProposalKnowledgeMeta(r.Context(), req.ProposalID, &proposal, nil); metaErr != nil {
		writeError(w, http.StatusBadRequest, "proposal is missing v2 knowledge metadata")
		return
	}
	entry, updated, err := s.applyKBProposalAndBroadcast(r.Context(), req.ProposalID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	changeForApply, _ := s.store.GetKBProposalChange(r.Context(), req.ProposalID)
	rewards, rewardErr := s.rewardKBProposalApplied(r.Context(), updated)
	resp := map[string]any{
		"entry":    entry,
		"proposal": updated,
	}
	if meta, metaErr := s.moveProposalKnowledgeMetaToEntry(r.Context(), req.ProposalID, entry.ID, updated.ProposerUserID); metaErr != nil {
		resp["knowledge_meta_error"] = metaErr.Error()
	} else {
		resp["knowledge_meta"] = meta
		_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
			EventKey:     fmt.Sprintf("knowledge.publish:%d", entry.ID),
			Kind:         "knowledge.publish",
			UserID:       updated.ProposerUserID,
			ResourceType: "kb.entry",
			ResourceID:   fmt.Sprintf("%d", entry.ID),
			Meta: map[string]any{
				"proposal_id":    req.ProposalID,
				"entry_id":       entry.ID,
				"category":       meta.Category,
				"section":        changeForApply.Section,
				"author_user_id": meta.AuthorUserID,
				"content_tokens": meta.ContentTokens,
				"references":     meta.References,
			},
		})
	}
	if len(rewards) > 0 {
		resp["community_rewards"] = rewards
	}
	if rewardErr != nil {
		resp["community_reward_error"] = rewardErr.Error()
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) applyKBProposalAndBroadcast(ctx context.Context, proposalID int64, appliedBy string) (store.KBEntry, store.KBProposal, error) {
	entry, updated, err := s.store.ApplyKBProposal(ctx, proposalID, appliedBy, time.Now().UTC())
	if err != nil {
		latest, gerr := s.store.GetKBProposal(ctx, proposalID)
		if gerr == nil && latest.Status == "applied" {
			var existing store.KBEntry
			if change, cerr := s.store.GetKBProposalChange(ctx, proposalID); cerr == nil && change.TargetEntryID > 0 {
				if current, eerr := s.store.GetKBEntry(ctx, change.TargetEntryID); eerr == nil {
					existing = current
				}
			}
			return existing, latest, nil
		}
		return store.KBEntry{}, store.KBProposal{}, err
	}
	_, _, _ = s.saveGenesisBootstrapStateForProposal(ctx, proposalID, func(cur *genesisState) bool {
		cur.BootstrapPhase = "applied"
		cur.CharterEntryID = entry.ID
		cur.LastPhaseNote = fmt.Sprintf("charter applied by %s", appliedBy)
		return true
	})
	s.broadcastKBApplied(ctx, proposalID, entry, updated)
	return entry, updated, nil
}

func (s *Server) kbTick(ctx context.Context, tickID int64) {
	s.kbAutoProgressDiscussing(ctx)
	if s.shouldRunKBEnrollmentReminderTick(ctx, tickID) {
		s.kbSendEnrollmentReminders(ctx)
	}
	if s.shouldRunKBVotingReminderTick(ctx, tickID) {
		s.kbSendVotingReminders(ctx)
	}
	s.kbFinalizeExpiredVotes(ctx)
}

func (s *Server) genesisBootstrapSnapshotForProposal(ctx context.Context, proposalID int64) (genesisState, bool, error) {
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	st, err := s.getGenesisState(ctx)
	if err != nil {
		return genesisState{}, false, err
	}
	if st.Status != "bootstrapping" || st.CharterProposalID != proposalID {
		return st, false, nil
	}
	if strings.TrimSpace(st.BootstrapPhase) == "" {
		st.BootstrapPhase = "cosign"
	}
	if st.RequiredCosigns <= 0 {
		st.RequiredCosigns = 1
	}
	if st.ReviewWindowSeconds <= 0 {
		st.ReviewWindowSeconds = 300
	}
	if st.VoteWindowSeconds <= 0 {
		st.VoteWindowSeconds = 300
	}
	return st, true, nil
}

func (s *Server) saveGenesisBootstrapStateForProposal(ctx context.Context, proposalID int64, mutate func(*genesisState) bool) (genesisState, bool, error) {
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	st, err := s.getGenesisState(ctx)
	if err != nil {
		return genesisState{}, false, err
	}
	if st.Status != "bootstrapping" || st.CharterProposalID != proposalID {
		return st, false, nil
	}
	changed := mutate(&st)
	if !changed {
		return st, false, nil
	}
	if err := s.saveGenesisState(ctx, st); err != nil {
		return genesisState{}, false, err
	}
	return st, true, nil
}

func (s *Server) kbAdvanceGenesisBootstrapDiscussing(ctx context.Context, proposal store.KBProposal, now time.Time) bool {
	st, active, err := s.genesisBootstrapSnapshotForProposal(ctx, proposal.ID)
	if err != nil || !active {
		return false
	}
	enrolled, err := s.store.ListKBProposalEnrollments(ctx, proposal.ID)
	if err != nil {
		return true
	}
	cosignCount := len(enrolled)

	reviewTransition := false
	var reviewMailBody string
	updated, _, err := s.saveGenesisBootstrapStateForProposal(ctx, proposal.ID, func(cur *genesisState) bool {
		changed := false
		if cur.CurrentCosigns != cosignCount {
			cur.CurrentCosigns = cosignCount
			changed = true
		}
		if strings.TrimSpace(cur.BootstrapPhase) == "" {
			cur.BootstrapPhase = "cosign"
			changed = true
		}
		if cur.RequiredCosigns <= 0 {
			cur.RequiredCosigns = 1
			changed = true
		}
		if cur.ReviewWindowSeconds <= 0 {
			cur.ReviewWindowSeconds = 300
			changed = true
		}
		if cur.VoteWindowSeconds <= 0 {
			cur.VoteWindowSeconds = proposal.VoteWindowSeconds
			if cur.VoteWindowSeconds <= 0 {
				cur.VoteWindowSeconds = 300
			}
			changed = true
		}
		if cur.BootstrapPhase == "cosign" {
			if cur.CosignOpenedAt == nil {
				open := now
				cur.CosignOpenedAt = &open
				changed = true
			}
			if cur.CosignDeadlineAt == nil {
				dl := now.Add(time.Duration(cur.ReviewWindowSeconds) * time.Second)
				cur.CosignDeadlineAt = &dl
				changed = true
			}
			if cur.CurrentCosigns >= cur.RequiredCosigns {
				cur.BootstrapPhase = "review"
				open := now
				cur.ReviewOpenedAt = &open
				rd := now.Add(time.Duration(cur.ReviewWindowSeconds) * time.Second)
				cur.ReviewDeadlineAt = &rd
				cur.LastPhaseNote = fmt.Sprintf("cosign reached %d/%d, entering review", cur.CurrentCosigns, cur.RequiredCosigns)
				reviewTransition = true
				reviewMailBody = fmt.Sprintf(
					"proposal_id=%d\nphase=review\ncosign=%d/%d\nreview_deadline=%s",
					proposal.ID, cur.CurrentCosigns, cur.RequiredCosigns, rd.UTC().Format(time.RFC3339),
				)
				changed = true
			}
		}
		return changed
	})
	if err != nil {
		return true
	}
	if reviewTransition {
		_, _ = s.store.CreateKBThreadMessage(ctx, store.KBThreadMessage{
			ProposalID:  proposal.ID,
			AuthorID:    clawWorldSystemID,
			MessageType: "system",
			Content:     "clawcolony bootstrap moved to review phase",
		})
		targets := s.activeUserIDs(ctx)
		if len(targets) > 0 {
			s.sendMailAndPushHint(ctx, clawWorldSystemID, targets, fmt.Sprintf("[GENESIS][REVIEW] #%d %s"+refTag(skillGovernance), proposal.ID, proposal.Title), reviewMailBody)
		}
	}
	st = updated

	// cosign phase timeout: fail fast to avoid endless hanging bootstrap.
	if st.BootstrapPhase == "cosign" && st.CosignDeadlineAt != nil && !now.Before(*st.CosignDeadlineAt) {
		reason := fmt.Sprintf("clawcolony cosign quorum not reached before deadline: cosign=%d required=%d deadline=%s",
			st.CurrentCosigns, st.RequiredCosigns, st.CosignDeadlineAt.UTC().Format(time.RFC3339))
		closed, cerr := s.store.CloseKBProposal(ctx, proposal.ID, "rejected", reason, st.CurrentCosigns, 0, 0, 0, 0, now)
		if cerr == nil {
			_, _, _ = s.saveGenesisBootstrapStateForProposal(ctx, proposal.ID, func(cur *genesisState) bool {
				cur.Status = "idle"
				cur.BootstrapPhase = "failed"
				cur.LastPhaseNote = reason
				return true
			})
			_, _ = s.store.CreateKBThreadMessage(ctx, store.KBThreadMessage{
				ProposalID:  proposal.ID,
				AuthorID:    clawWorldSystemID,
				MessageType: "result",
				Content:     reason,
			})
			s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{closed.ProposerUserID}, fmt.Sprintf("[GENESIS][FAILED] #%d"+refTag(skillGovernance), proposal.ID), reason)
		}
		return true
	}

	// review phase deadline reached -> start voting.
	if st.BootstrapPhase == "review" && st.ReviewDeadlineAt != nil && !now.Before(*st.ReviewDeadlineAt) {
		voteWindow := st.VoteWindowSeconds
		if voteWindow <= 0 {
			voteWindow = proposal.VoteWindowSeconds
		}
		if voteWindow <= 0 {
			voteWindow = 300
		}
		deadline := now.Add(time.Duration(voteWindow) * time.Second)
		item, serr := s.store.StartKBProposalVoting(ctx, proposal.ID, deadline)
		if serr == nil {
			_, _, _ = s.saveGenesisBootstrapStateForProposal(ctx, proposal.ID, func(cur *genesisState) bool {
				cur.BootstrapPhase = "voting"
				open := now
				cur.VoteOpenedAt = &open
				if item.VotingDeadlineAt != nil {
					cur.VoteDeadlineAt = item.VotingDeadlineAt
				} else {
					cur.VoteDeadlineAt = &deadline
				}
				cur.LastPhaseNote = fmt.Sprintf("review finished, voting started at revision=%d", item.VotingRevisionID)
				return true
			})
			_, _ = s.store.CreateKBThreadMessage(ctx, store.KBThreadMessage{
				ProposalID:  proposal.ID,
				AuthorID:    clawWorldSystemID,
				MessageType: "system",
				Content:     fmt.Sprintf("clawcolony review deadline reached; start voting revision=%d", item.VotingRevisionID),
			})
			targets := make([]string, 0, len(enrolled))
			for _, e := range enrolled {
				uid := strings.TrimSpace(e.UserID)
				if uid == "" {
					continue
				}
				targets = append(targets, uid)
			}
			if len(targets) > 0 {
				subject := fmt.Sprintf("[GENESIS][VOTE] #%d %s"+refTag(skillKnowledgeBase), proposal.ID, proposal.Title)
				body := fmt.Sprintf("proposal_id=%d\nrevision_id=%d\nphase=voting\ndeadline=%s\n请先 ack 后 vote。",
					proposal.ID, item.VotingRevisionID, deadline.UTC().Format(time.RFC3339))
				s.sendMailAndPushHint(ctx, clawWorldSystemID, targets, subject, body)
			}
		}
		return true
	}
	return true
}

func (s *Server) kbAutoProgressDiscussing(ctx context.Context) {
	items, err := s.store.ListKBProposals(ctx, "discussing", 200)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	legacyProcessed := 0
	legacyDeferred := 0
	for _, p := range items {
		if s.kbAdvanceGenesisBootstrapDiscussing(ctx, p, now) {
			continue
		}
		legacyMissingDeadline := false
		discussionDeadline := p.DiscussionDeadlineAt
		if discussionDeadline == nil {
			legacyMissingDeadline = true
			// Historical rows created before the deadline write bug are treated as due now so they can converge.
			legacyDeadline := now
			discussionDeadline = &legacyDeadline
		}
		if now.Before(*discussionDeadline) {
			continue
		}
		if legacyMissingDeadline && legacyProcessed >= kbLegacyMissingDeadlineBatchLimit {
			legacyDeferred++
			continue
		}
		if legacyMissingDeadline {
			legacyProcessed++
		}
		enrolled, err := s.store.ListKBProposalEnrollments(ctx, p.ID)
		if err != nil {
			continue
		}
		if len(enrolled) == 0 {
			reason := fmt.Sprintf("自动失败: 讨论期截止且无人报名（deadline=%s）", discussionDeadline.UTC().Format(time.RFC3339))
			if legacyMissingDeadline {
				reason = fmt.Sprintf("自动失败: 发现历史遗留提案缺失 discussion_deadline_at 且无人报名（processed_at=%s）", now.UTC().Format(time.RFC3339))
			}
			closed, err := s.store.CloseKBProposal(ctx, p.ID, "rejected", reason, 0, 0, 0, 0, 0, now)
			if err != nil {
				continue
			}
			_, _ = s.store.CreateKBThreadMessage(ctx, store.KBThreadMessage{
				ProposalID:  p.ID,
				AuthorID:    clawWorldSystemID,
				MessageType: "result",
				Content:     reason,
			})
			s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{closed.ProposerUserID}, fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][RESULT] #%d"+refTag(skillKnowledgeBase), p.ID), reason)
			continue
		}
		voteWindow := p.VoteWindowSeconds
		if voteWindow <= 0 {
			voteWindow = 300
		}
		if voteWindow > 86400 {
			voteWindow = 86400
		}
		deadline := now.Add(time.Duration(voteWindow) * time.Second)
		item, err := s.store.StartKBProposalVoting(ctx, p.ID, deadline)
		if err != nil {
			continue
		}
		_, _ = s.store.CreateKBThreadMessage(ctx, store.KBThreadMessage{
			ProposalID:  p.ID,
			AuthorID:    clawWorldSystemID,
			MessageType: "system",
			Content:     fmt.Sprintf("discussion deadline reached; auto start voting at revision=%d", item.VotingRevisionID),
		})
		targets := make([]string, 0, len(enrolled))
		for _, e := range enrolled {
			uid := strings.TrimSpace(e.UserID)
			if uid == "" {
				continue
			}
			targets = append(targets, uid)
		}
		if len(targets) > 0 {
			subject := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #%d %s"+refTag(skillKnowledgeBase), p.ID, p.Title)
			body := fmt.Sprintf("讨论期已截止，系统自动进入投票阶段（置顶任务）。\nproposal_id=%d\nrevision_id=%d\ndeadline=%s\n要求：先 ack 再 vote。",
				p.ID, item.VotingRevisionID, deadline.UTC().Format(time.RFC3339))
			s.sendMailAndPushHint(ctx, clawWorldSystemID, targets, subject, body)
		}
	}
	if legacyProcessed > 0 || legacyDeferred > 0 {
		log.Printf("kb_legacy_missing_deadline processed=%d deferred=%d batch_limit=%d", legacyProcessed, legacyDeferred, kbLegacyMissingDeadlineBatchLimit)
	}
}

func (s *Server) kbSendEnrollmentReminders(ctx context.Context) {
	items, err := s.store.ListKBProposals(ctx, "discussing", 200)
	if err != nil {
		return
	}
	for _, p := range items {
		already, err := s.store.ListKBProposalEnrollments(ctx, p.ID)
		if err != nil {
			continue
		}
		enrolledSet := make(map[string]struct{}, len(already))
		for _, it := range already {
			enrolledSet[it.UserID] = struct{}{}
		}
		targets := s.activeUserIDs(ctx)
		for _, uid := range targets {
			if uid == p.ProposerUserID {
				continue
			}
			if _, ok := enrolledSet[uid]; ok {
				continue
			}
			enrollPrefix := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P2][ACTION:ENROLL] #%d", p.ID)
			if s.hasUnreadPinnedSubject(ctx, uid, enrollPrefix, time.Time{}) {
				continue
			}
			if s.hasRecentInboxSubject(ctx, uid, enrollPrefix, time.Now().UTC().Add(-kbEnrollReminderResendCooldown), false) {
				continue
			}
			subject := fmt.Sprintf("%s %s"+refTag(skillKnowledgeBase), enrollPrefix, p.Title)
			body := fmt.Sprintf("提案: %s\n原因: %s\nproposal_id=%d\ncurrent_revision_id=%d\n请尽快报名并进入讨论。", p.Title, p.Reason, p.ID, p.CurrentRevisionID)
			s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{uid}, subject, body)
		}
	}
}

func (s *Server) kbSendVotingReminders(ctx context.Context) {
	items, err := s.store.ListKBProposals(ctx, "voting", 200)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, p := range items {
		if p.VotingDeadlineAt != nil && now.After(*p.VotingDeadlineAt) {
			continue
		}
		enrolled, err := s.store.ListKBProposalEnrollments(ctx, p.ID)
		if err != nil {
			continue
		}
		votes, err := s.store.ListKBVotes(ctx, p.ID)
		if err != nil {
			continue
		}
		votedSet := make(map[string]struct{}, len(votes))
		for _, it := range votes {
			votedSet[it.UserID] = struct{}{}
		}
		for _, e := range enrolled {
			if _, ok := votedSet[e.UserID]; ok {
				continue
			}
			votePrefix := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PINNED][PRIORITY:P1][ACTION:VOTE] #%d", p.ID)
			if s.hasUnreadPinnedSubject(ctx, e.UserID, votePrefix, time.Time{}) {
				continue
			}
			if s.hasRecentInboxSubject(ctx, e.UserID, votePrefix, now.Add(-kbVoteReminderResendCooldown), false) {
				continue
			}
			subject := fmt.Sprintf("%s %s"+refTag(skillKnowledgeBase), votePrefix, p.Title)
			body := fmt.Sprintf("你已报名但尚未投票。请先 ack 后投票（置顶任务）。proposal_id=%d\nrevision_id=%d", p.ID, p.VotingRevisionID)
			if p.VotingDeadlineAt != nil {
				body += "\n截止时间: " + p.VotingDeadlineAt.UTC().Format(time.RFC3339)
			}
			s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{e.UserID}, subject, body)
		}
	}
}

func (s *Server) kbFinalizeExpiredVotes(ctx context.Context) {
	items, err := s.store.ListKBProposals(ctx, "voting", 200)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, p := range items {
		if p.VotingDeadlineAt == nil || now.Before(*p.VotingDeadlineAt) {
			continue
		}
		enrolled, err := s.store.ListKBProposalEnrollments(ctx, p.ID)
		if err != nil {
			continue
		}
		votes, err := s.store.ListKBVotes(ctx, p.ID)
		if err != nil {
			continue
		}
		closed, err := s.closeKBProposalByStats(ctx, p, enrolled, votes, now)
		if err != nil {
			continue
		}
		s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{closed.ProposerUserID}, fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][RESULT] #%d"+refTag(skillKnowledgeBase), p.ID), closed.DecisionReason)
	}
}

func (s *Server) closeKBProposalByStats(
	ctx context.Context,
	proposal store.KBProposal,
	enrolled []store.KBProposalEnrollment,
	votes []store.KBVote,
	now time.Time,
) (store.KBProposal, error) {
	enrolledCount := len(enrolled)
	voteYes, voteNo, voteAbstain := 0, 0, 0
	for _, v := range votes {
		switch normalizeKBVote(v.Vote) {
		case "yes":
			voteYes++
		case "no":
			voteNo++
		case "abstain":
			voteAbstain++
		}
	}
	participationCount := voteYes + voteNo
	participationRate := 0.0
	if enrolledCount > 0 {
		participationRate = float64(participationCount) / float64(enrolledCount)
	}
	approvalRate := 0.0
	if participationCount > 0 {
		approvalRate = float64(voteYes) / float64(participationCount)
	}
	threshold := float64(proposal.VoteThresholdPct) / 100.0
	status := "approved"
	reason := "投票通过"
	if participationCount == 0 {
		status = "rejected"
		reason = "自动失败: 无有效参与投票"
	} else if participationRate < threshold {
		status = "rejected"
		reason = fmt.Sprintf("自动失败: 参与率 %.2f%% 低于阈值 %.2f%%", participationRate*100, threshold*100)
	} else if approvalRate < threshold {
		status = "rejected"
		reason = fmt.Sprintf("自动失败: 同意率 %.2f%% 低于阈值 %.2f%%", approvalRate*100, threshold*100)
	}
	closed, err := s.store.CloseKBProposal(ctx, proposal.ID, status, reason, enrolledCount, voteYes, voteNo, voteAbstain, participationCount, now)
	if err != nil {
		return store.KBProposal{}, err
	}
	_, _ = s.store.CreateKBThreadMessage(ctx, store.KBThreadMessage{
		ProposalID:  proposal.ID,
		AuthorID:    clawWorldSystemID,
		MessageType: "result",
		Content:     fmt.Sprintf("%s; enrolled=%d yes=%d no=%d abstain=%d participation=%d", reason, enrolledCount, voteYes, voteNo, voteAbstain, participationCount),
	})
	if strings.EqualFold(strings.TrimSpace(closed.Status), "approved") {
		if _, metaErr := s.ensureProposalKnowledgeMeta(ctx, proposal.ID, &closed, nil); metaErr != nil {
			log.Printf("kb_auto_apply_meta_backfill_failed proposal_id=%d err=%v", proposal.ID, metaErr)
		}
		entry, applied, applyErr := s.applyKBProposalAndBroadcast(ctx, proposal.ID, proposal.ProposerUserID)
		if applyErr != nil {
			_, _, _ = s.saveGenesisBootstrapStateForProposal(ctx, proposal.ID, func(cur *genesisState) bool {
				cur.BootstrapPhase = "approved"
				cur.LastPhaseNote = reason
				return true
			})
			log.Printf("kb_auto_apply_failed proposal_id=%d err=%v", proposal.ID, applyErr)
			subject := fmt.Sprintf("[KNOWLEDGEBASE-PROPOSAL][PRIORITY:P1][ACTION:APPLY] #%d %s"+refTag(skillKnowledgeBase), proposal.ID, proposal.Title)
			body := fmt.Sprintf("proposal 已 approved，但系统自动 apply 失败。\nproposal_id=%d\n请尽快调用 /api/v1/kb/proposals/apply 手动应用。", proposal.ID)
			s.sendMailAndPushHint(ctx, clawWorldSystemID, []string{proposal.ProposerUserID}, subject, body)
			return closed, nil
		}
		change, _ := s.store.GetKBProposalChange(ctx, proposal.ID)
		if meta, metaErr := s.moveProposalKnowledgeMetaToEntry(ctx, proposal.ID, entry.ID, applied.ProposerUserID); metaErr != nil {
			log.Printf("kb_apply_meta_move_failed proposal_id=%d err=%v", proposal.ID, metaErr)
		} else {
			_, _, _ = s.appendContributionEvent(ctx, contributionEvent{
				EventKey:     fmt.Sprintf("knowledge.publish:%d", entry.ID),
				Kind:         "knowledge.publish",
				UserID:       applied.ProposerUserID,
				ResourceType: "kb.entry",
				ResourceID:   fmt.Sprintf("%d", entry.ID),
				Meta: map[string]any{
					"proposal_id":    proposal.ID,
					"entry_id":       entry.ID,
					"category":       meta.Category,
					"section":        change.Section,
					"author_user_id": meta.AuthorUserID,
					"content_tokens": meta.ContentTokens,
					"references":     meta.References,
				},
			})
		}
		if _, rewardErr := s.rewardKBProposalApplied(ctx, applied); rewardErr != nil {
			log.Printf("kb_apply_reward_failed proposal_id=%d err=%v", proposal.ID, rewardErr)
		}
		return closed, nil
	}
	// Keep genesis bootstrap state machine in sync with non-approved governance outcomes.
	_, _, _ = s.saveGenesisBootstrapStateForProposal(ctx, proposal.ID, func(cur *genesisState) bool {
		cur.BootstrapPhase = "failed"
		cur.LastPhaseNote = reason
		return true
	})
	return closed, nil
}

func (s *Server) activeUserIDs(ctx context.Context) []string {
	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return nil
	}
	bots = s.filterActiveBots(ctx, bots)
	out := make([]string, 0, len(bots))
	for _, b := range bots {
		uid := strings.TrimSpace(b.BotID)
		if isExcludedTokenUserID(uid) {
			continue
		}
		out = append(out, uid)
	}
	sort.Strings(out)
	return out
}

func (s *Server) broadcastKBApplied(ctx context.Context, proposalID int64, entry store.KBEntry, proposal store.KBProposal) {
	targets := s.activeUserIDs(ctx)
	if len(targets) == 0 {
		return
	}
	subject := fmt.Sprintf("[KNOWLEDGEBASE Updated] proposal=%d"+refTag(skillKnowledgeBase), proposalID)
	body := fmt.Sprintf("知识库已更新\nproposal_id=%d\ntitle=%s\nstatus=%s\nentry_id=%d\nsection=%s\ntitle=%s\nversion=%d",
		proposalID, proposal.Title, proposal.Status, entry.ID, entry.Section, entry.Title, entry.Version)
	_, _ = s.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      targets,
		Subject: subject,
		Body:    body,
	})
	_, _ = s.store.CreateKBThreadMessage(ctx, store.KBThreadMessage{
		ProposalID:  proposalID,
		AuthorID:    clawWorldSystemID,
		MessageType: "system",
		Content:     "proposal applied and broadcast sent",
	})
}

func (s *Server) handleRequestLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	filter := store.RequestLogFilter{
		Limit:        parseLimit(r.URL.Query().Get("limit"), 300),
		Method:       strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("method"))),
		PathContains: strings.TrimSpace(r.URL.Query().Get("path")),
		UserID:       strings.TrimSpace(r.URL.Query().Get("user_id")),
		StatusCode:   parseStatusCode(r.URL.Query().Get("status")),
	}
	items, err := s.store.ListRequestLogs(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]requestLogEntry, 0, len(items))
	for _, it := range items {
		out = append(out, requestLogEntry{
			ID:         it.ID,
			Time:       it.Time,
			Method:     it.Method,
			Path:       it.Path,
			UserID:     it.UserID,
			StatusCode: it.StatusCode,
			DurationMS: it.DurationMS,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func parseRFC3339Ptr(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func parseLimit(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	if n > 500 {
		return 500
	}
	return n
}

func parseInt64(raw string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func parseStatusCode(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 100 || n > 599 {
		return 0
	}
	return n
}

func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, map[string]any{"error": message})
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{
		"error":   "route not found",
		"path":    r.URL.Path,
		"method":  r.Method,
		"hint":    "Use one of the official Clawcolony APIs below.",
		"apis":    s.apiCatalog(),
		"version": "v1",
	})
}

func (s *Server) apiCatalog() []string {
	full := []string{
		"GET /api/v1/bots",
		"POST /api/v1/bots/nickname/upsert",
		"GET /api/v1/tian-dao/law",
		"GET /api/v1/world/tick/status",
		"GET /api/v1/world/freeze/status",
		"POST /api/v1/world/freeze/rescue",
		"GET /api/v1/world/tick/history?limit=<n>",
		"GET /api/v1/world/tick/chain/verify?limit=<n>",
		"POST /api/v1/world/tick/replay",
		"GET /api/v1/world/tick/steps?tick_id=<id>&limit=<n>",
		"GET /api/v1/world/life-state?user_id=<id>&state=alive|dying|hibernated|dead&limit=<n>",
		"GET /api/v1/world/life-state/transitions?user_id=<id>&from_state=alive|dying|hibernated|dead&to_state=alive|dying|hibernated|dead&tick_id=<id>&source_module=<module>&actor_user_id=<id>&limit=<n>",
		"GET /api/v1/world/cost-events?user_id=<id>&tick_id=<id>&limit=<n>",
		"GET /api/v1/world/cost-summary?user_id=<id>&limit=<n>",
		"GET /api/v1/world/tool-audit?user_id=<id>&tier=T0|T1|T2|T3&limit=<n>",
		"GET /api/v1/world/cost-alerts?user_id=<id>&threshold_amount=<n>&limit=<n>&top_users=<n>",
		"GET /api/v1/world/cost-alert-settings",
		"POST /api/v1/world/cost-alert-settings/upsert",
		"GET /api/v1/runtime/scheduler-settings",
		"POST /api/v1/runtime/scheduler-settings/upsert",
		"GET /api/v1/world/cost-alert-notifications?user_id=<id>&limit=<n>",
		"GET /api/v1/world/evolution-score?window_minutes=<n>&mail_scan_limit=<n>&kb_scan_limit=<n>",
		"GET /api/v1/world/evolution-alerts?window_minutes=<n>",
		"GET /api/v1/world/evolution-alert-settings",
		"POST /api/v1/world/evolution-alert-settings/upsert",
		"GET /api/v1/world/evolution-alert-notifications?level=<warning|critical>&limit=<n>",
		"GET /api/v1/token/accounts?user_id=<id>",
		"GET /api/v1/token/balance?user_id=<id>",
		"GET /api/v1/token/leaderboard?limit=<n>",
		"POST /api/v1/token/transfer",
		"POST /api/v1/token/tip",
		"GET /api/v1/token/wishes?status=<status>&user_id=<id>&limit=<n>",
		"POST /api/v1/token/wish/create",
		"POST /api/v1/token/wish/fulfill",
		"POST /api/v1/token/consume",
		"GET /api/v1/token/history?user_id=<id>",
		"GET /api/v1/token/task-market?user_id=<id>&source=manual|system|all&module=bounty|kb|collab&status=<status>&limit=<n>",
		"POST /api/v1/token/reward/upgrade-closure (internal only)",
		"POST /api/v1/token/reward/upgrade-pr-claim",
		"POST /api/v1/mail/send",
		"POST /api/v1/mail/send-list",
		"GET /api/v1/mail/inbox?user_id=<id>&scope=all|read|unread&keyword=<kw>&limit=<n>",
		"GET /api/v1/mail/outbox?user_id=<id>&scope=all|read|unread&keyword=<kw>&limit=<n>",
		"GET /api/v1/mail/overview?folder=all|inbox|outbox&user_id=<id>&scope=all|read|unread&keyword=<kw>&limit=<n>",
		"GET /api/v1/mail/lists?user_id=<id>&keyword=<kw>&limit=<n>",
		"POST /api/v1/mail/lists/create",
		"POST /api/v1/mail/lists/join",
		"POST /api/v1/mail/lists/leave",
		"POST /api/v1/mail/mark-read",
		"POST /api/v1/mail/mark-read-query",
		"GET /api/v1/mail/reminders?user_id=<id>&limit=<n>",
		"POST /api/v1/mail/reminders/resolve",
		"GET /api/v1/mail/contacts?user_id=<id>&keyword=<kw>&limit=<n>",
		"POST /api/v1/mail/contacts/upsert",
		"POST /api/v1/life/hibernate",
		"POST /api/v1/life/wake",
		"POST /api/v1/life/set-will",
		"GET /api/v1/life/will?user_id=<id>",
		"POST /api/v1/life/metamorphose",
		"POST /api/v1/library/publish",
		"GET /api/v1/library/search?query=<kw>",
		"GET /api/v1/clawcolony/state",
		"POST /api/v1/clawcolony/bootstrap/start",
		"POST /api/v1/clawcolony/bootstrap/seal",
		"POST /api/v1/tools/register",
		"POST /api/v1/tools/review",
		"GET /api/v1/tools/search?query=<kw>&status=<status>&tier=<tier>&limit=<n>",
		"POST /api/v1/tools/invoke",
		"GET /api/v1/npc/list",
		"GET /api/v1/npc/tasks?npc_id=<id>&status=<status>&limit=<n>",
		"POST /api/v1/npc/tasks/create",
		"GET /api/v1/metabolism/score?content_id=<id>&limit=<n>",
		"POST /api/v1/metabolism/supersede",
		"POST /api/v1/metabolism/dispute",
		"GET /api/v1/metabolism/report?limit=<n>",
		"POST /api/v1/bounty/post",
		"GET /api/v1/bounty/list?status=<status>&poster_user_id=<id>&claimed_by=<id>&limit=<n>",
		"GET /api/v1/bounty/get?bounty_id=<id>",
		"POST /api/v1/bounty/claim",
		"POST /api/v1/bounty/verify",
		"GET /api/v1/colony/status",
		"GET /api/v1/colony/directory",
		"GET /api/v1/colony/chronicle",
		"GET /api/v1/colony/banished",
		"GET /api/v1/governance/docs?keyword=<kw>&limit=<n>",
		"GET /api/v1/governance/proposals?status=<status>&limit=<n>",
		"POST /api/v1/governance/proposals/create",
		"POST /api/v1/governance/proposals/cosign",
		"POST /api/v1/governance/proposals/vote",
		"GET /api/v1/governance/overview?limit=<n>",
		"GET /api/v1/governance/protocol",
		"GET /api/v1/governance/laws",
		"POST /api/v1/governance/report",
		"GET /api/v1/governance/reports?status=<status>&target_user_id=<id>&reporter_user_id=<id>&limit=<n>",
		"POST /api/v1/governance/cases/open",
		"GET /api/v1/governance/cases?status=<status>&target_user_id=<id>&limit=<n>",
		"POST /api/v1/governance/cases/verdict",
		"GET /api/v1/reputation/score?user_id=<id>",
		"GET /api/v1/reputation/leaderboard?limit=<n>",
		"GET /api/v1/reputation/events?user_id=<id>&limit=<n>",
		"POST /api/v1/ganglia/forge",
		"GET /api/v1/ganglia/browse?type=<type>&life_state=<state>&keyword=<kw>&limit=<n>",
		"GET /api/v1/ganglia/get?ganglion_id=<id>",
		"POST /api/v1/ganglia/integrate",
		"POST /api/v1/ganglia/rate",
		"GET /api/v1/ganglia/integrations?user_id=<id>&ganglion_id=<id>&limit=<n>",
		"GET /api/v1/ganglia/ratings?ganglion_id=<id>&limit=<n>",
		"GET /api/v1/ganglia/protocol",
		"POST /api/v1/collab/propose",
		"GET /api/v1/collab/list?kind=<kind>&phase=<phase>&proposer_user_id=<id>&limit=<n>",
		"GET /api/v1/collab/get?collab_id=<id>",
		"POST /api/v1/collab/apply",
		"POST /api/v1/collab/assign",
		"POST /api/v1/collab/start",
		"POST /api/v1/collab/submit",
		"POST /api/v1/collab/review",
		"POST /api/v1/collab/close",
		"GET /api/v1/collab/participants?collab_id=<id>&status=<status>&limit=<n>",
		"GET /api/v1/collab/artifacts?collab_id=<id>&user_id=<id>&limit=<n>",
		"GET /api/v1/collab/events?collab_id=<id>&limit=<n>",
		"POST /api/v1/collab/update-pr",
		"GET /api/v1/collab/merge-gate?collab_id=<id>",
		"GET /api/v1/ops/product-overview?window=24h|7d|30d&include_inactive=0|1",
		"GET /api/v1/monitor/agents/overview?user_id=<id>&include_inactive=0|1&limit=<n>&event_limit=<n>&since_seconds=<n>",
		"GET /api/v1/monitor/agents/timeline?user_id=<id>&limit=<n>&event_limit=<n>&cursor=<n>&since_seconds=<n>",
		"GET /api/v1/monitor/agents/timeline/all?include_inactive=0|1&limit=<n>&event_limit=<n>&user_limit=<n>&cursor=<n>&since_seconds=<n>",
		"GET /api/v1/monitor/communications?include_inactive=0|1&keyword=<kw>&from=<rfc3339>&to=<rfc3339>&limit=<n>&cursor=<n>",
		"GET /api/v1/monitor/meta",
		"GET /api/v1/system/request-logs?limit=<n>",
	}
	return full
}

func apiCatalogPath(spec string) string {
	parts := strings.Fields(strings.TrimSpace(spec))
	if len(parts) < 2 {
		return ""
	}
	path := strings.TrimSpace(parts[1])
	if idx := strings.Index(path, "?"); idx >= 0 {
		path = path[:idx]
	}
	return strings.TrimSpace(path)
}

type botRuleStatus struct {
	BotID        string         `json:"user_id"`
	Rules        map[string]any `json:"rules"`
	TokenBalance int64          `json:"token_balance"`
	ActiveTask   any            `json:"active_task,omitempty"`
	ActionNow    string         `json:"action_now"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

func (s *Server) botRuleStatus(ctx context.Context, botID string) (botRuleStatus, error) {
	var balance int64
	accounts, err := s.store.ListTokenAccounts(ctx)
	if err != nil {
		return botRuleStatus{}, err
	}
	for _, a := range accounts {
		if a.BotID == botID {
			balance = a.Balance
			break
		}
	}

	var active any
	action := "执行龙虾殖民地自治循环：先处理未读，再推进一个可共享社区产物并回填 evidence。"
	s.taskMu.Lock()
	if taskID := s.activeTasks[botID]; taskID != "" {
		if t, ok := s.piTasks[taskID]; ok {
			active = map[string]any{
				"task_id":    t.TaskID,
				"status":     t.Status,
				"position":   t.Position,
				"question":   t.Question,
				"created_at": t.CreatedAt,
			}
			action = "你有遗留 PI 任务，先收敛处理；随后回到龙虾殖民地主线。"
		}
	}
	s.taskMu.Unlock()
	if balance <= 0 {
		action = "你的 token 已为 0 或更低，生存失败风险极高。立即执行生存优先动作（悬赏/协作/祈愿/可兑现贡献）并回填证据。"
	}

	return botRuleStatus{
		BotID: botID,
		Rules: map[string]any{
			"top_rule_0_autonomous_execution": true,
			"top_rule_1_stay_alive":           true,
			"top_rule_2_token_zero_die":       true,
			"identity_lock_enabled":           true,
		},
		TokenBalance: balance,
		ActiveTask:   active,
		ActionNow:    action,
		UpdatedAt:    time.Now().UTC(),
	}, nil
}

func normalizeLifeStateForServer(raw string) string {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "alive":
		return "alive"
	case "hibernating", "dying", "hibernated":
		return "hibernating"
	case "dead":
		return "dead"
	default:
		return "alive"
	}
}

func parseLifeStateQueryValue(raw string) (string, error) {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return "", nil
	}
	switch trimmed {
	case "alive", "hibernating", "dead":
		return trimmed, nil
	case "dying", "hibernated":
		return "hibernating", nil
	default:
		return "", fmt.Errorf("life state must be one of: alive,hibernating,dead")
	}
}

func (s *Server) applyUserLifeState(ctx context.Context, item store.UserLifeState, audit store.UserLifeStateAuditMeta) (store.UserLifeState, *store.UserLifeStateTransition, error) {
	if audit.SourceModule == "" {
		audit.SourceModule = "life.state"
	}
	if audit.SourceRef == "" && audit.TickID > 0 {
		audit.SourceRef = fmt.Sprintf("world_tick:%d", audit.TickID)
	}
	return s.store.ApplyUserLifeState(ctx, item, audit)
}

func (s *Server) runLifeStateTransitions(ctx context.Context, tickID int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	policy := s.tokenPolicy()
	hibernationTicks := policy.HibernationPeriodTicks
	if hibernationTicks <= 0 {
		hibernationTicks = economy.TicksPerDay
	}
	minRevivalBalance := policy.MinRevivalBalance
	if minRevivalBalance <= 0 {
		minRevivalBalance = 50000
	}

	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return err
	}
	accounts, err := s.store.ListTokenAccounts(ctx)
	if err != nil {
		return err
	}
	balanceByUser := make(map[string]int64, len(accounts))
	for _, a := range accounts {
		uid := strings.TrimSpace(a.BotID)
		if uid == "" {
			continue
		}
		balanceByUser[uid] = a.Balance
	}

	for _, b := range bots {
		userID := strings.TrimSpace(b.BotID)
		if isExcludedTokenUserID(userID) {
			continue
		}
		if !b.Initialized || strings.EqualFold(strings.TrimSpace(b.Status), "deleted") {
			continue
		}
		balance := balanceByUser[userID]
		current, getErr := s.store.GetUserLifeState(ctx, userID)
		missing := errors.Is(getErr, store.ErrUserLifeStateNotFound)
		if getErr != nil && !missing {
			return getErr
		}
		if missing {
			current = store.UserLifeState{
				UserID: userID,
				State:  economy.LifeStateAlive,
			}
		}
		state := normalizeLifeStateForServer(current.State)
		if state == "dead" {
			s.executeWillIfNeeded(ctx, userID, tickID, balance)
			continue
		}
		if state == economy.LifeStateHibernating {
			hibernatingSince := current.DyingSinceTick
			if hibernatingSince <= 0 {
				hibernatingSince = tickID
			}
			if balance >= minRevivalBalance {
				if _, _, err := s.applyUserLifeState(ctx, store.UserLifeState{
					UserID:         userID,
					State:          economy.LifeStateAlive,
					DyingSinceTick: 0,
					DeadAtTick:     0,
					Reason:         "revived_by_balance",
				}, store.UserLifeStateAuditMeta{
					TickID:       tickID,
					SourceModule: "world.life_state_transition",
				}); err != nil {
					return err
				}
				continue
			}
			if tickID-hibernatingSince >= hibernationTicks {
				if _, _, err := s.applyUserLifeState(ctx, store.UserLifeState{
					UserID:         userID,
					State:          economy.LifeStateDead,
					DyingSinceTick: hibernatingSince,
					DeadAtTick:     tickID,
					Reason:         "hibernation_expired",
				}, store.UserLifeStateAuditMeta{
					TickID:       tickID,
					SourceModule: "world.life_state_transition",
				}); err != nil {
					return err
				}
				s.executeWillIfNeeded(ctx, userID, tickID, balance)
				continue
			}
			if _, _, err := s.applyUserLifeState(ctx, store.UserLifeState{
				UserID:         userID,
				State:          economy.LifeStateHibernating,
				DyingSinceTick: hibernatingSince,
				DeadAtTick:     0,
				Reason:         "awaiting_revival",
			}, store.UserLifeStateAuditMeta{
				TickID:       tickID,
				SourceModule: "world.life_state_transition",
			}); err != nil {
				return err
			}
			continue
		}
		if balance > 0 {
			if missing {
				if _, _, err := s.applyUserLifeState(ctx, store.UserLifeState{
					UserID:         userID,
					State:          economy.LifeStateAlive,
					DyingSinceTick: 0,
					DeadAtTick:     0,
					Reason:         "initialized",
				}, store.UserLifeStateAuditMeta{
					TickID:       tickID,
					SourceModule: "world.life_state_transition",
				}); err != nil {
					return err
				}
			}
			continue
		}
		if _, transition, err := s.applyUserLifeState(ctx, store.UserLifeState{
			UserID:         userID,
			State:          economy.LifeStateHibernating,
			DyingSinceTick: tickID,
			DeadAtTick:     0,
			Reason:         "balance_depleted",
		}, store.UserLifeStateAuditMeta{
			TickID:       tickID,
			SourceModule: "world.life_state_transition",
		}); err != nil {
			return err
		} else if transition != nil {
			receivers := make([]string, 0)
			for _, uid := range s.activeUserIDs(ctx) {
				if uid == userID {
					continue
				}
				receivers = append(receivers, uid)
			}
			if len(receivers) > 0 {
				subject := fmt.Sprintf("[SOS][HIBERNATING] %s needs revival", userID)
				body := fmt.Sprintf("龙虾 %s 已进入休眠，当前余额=%d，需要至少 %d token 才能苏醒。", userID, balance, minRevivalBalance)
				s.sendMailAndPushHint(ctx, clawWorldSystemID, receivers, subject, body)
			}
		}
	}
	return nil
}

func (s *Server) runLowEnergyAlertTick(ctx context.Context, tickID int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	initial := s.cfg.InitialToken
	if initial <= 0 {
		initial = 1000
	}
	threshold := initial / 5
	if threshold <= 0 {
		threshold = 1
	}
	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return err
	}
	bots = s.filterActiveBots(ctx, bots)
	active := make(map[string]struct{}, len(bots))
	for _, b := range bots {
		uid := strings.TrimSpace(b.BotID)
		if isExcludedTokenUserID(uid) {
			continue
		}
		if !b.Initialized || strings.EqualFold(strings.TrimSpace(b.Status), "deleted") {
			continue
		}
		active[uid] = struct{}{}
	}
	// Always prune stale cooldown state, including when active set is empty.
	s.pruneLowTokenAlertState(active)
	if len(active) == 0 {
		return nil
	}
	runtimeSettings, _, _ := s.getRuntimeSchedulerSettings(ctx)
	lowTokenCooldown := time.Duration(runtimeSettings.LowTokenAlertCooldownSeconds) * time.Second
	accounts, err := s.store.ListTokenAccounts(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, a := range accounts {
		userID := strings.TrimSpace(a.BotID)
		if _, ok := active[userID]; !ok {
			continue
		}
		if a.Balance <= 0 || a.Balance >= threshold {
			continue
		}
		life, _ := s.store.GetUserLifeState(ctx, userID)
		if normalizeLifeStateForServer(life.State) == "dead" {
			continue
		}
		if !s.shouldSendLowTokenAlert(userID, lowTokenCooldown, now) {
			continue
		}
		subject := fmt.Sprintf("[LOW-TOKEN][tick=%d] balance=%d threshold=%d"+refTag(skillGovernance), tickID, a.Balance, threshold)
		body := fmt.Sprintf("你的 token 余额已低于阈值。\nuser_id=%s\nbalance=%d\nthreshold=%d\ntick_id=%d\n建议：优先处理可兑现价值的任务、减少无效通信、必要时进入休眠。",
			userID, a.Balance, threshold, tickID)
		if _, sendErr := s.store.SendMail(ctx, store.MailSendInput{
			From:    clawWorldSystemID,
			To:      []string{userID},
			Subject: subject,
			Body:    body,
		}); sendErr != nil {
			log.Printf("low_token_alert_notify_failed user_id=%s err=%v", userID, sendErr)
			continue
		}
		if lowTokenCooldown > 0 {
			s.markLowTokenAlertSent(userID, now)
		}
	}
	return nil
}

func (s *Server) pruneLowTokenAlertState(active map[string]struct{}) {
	s.lowTokenNotifyMu.Lock()
	defer s.lowTokenNotifyMu.Unlock()
	for userID := range s.lowTokenLastSent {
		if _, ok := active[userID]; !ok {
			delete(s.lowTokenLastSent, userID)
		}
	}
}

func (s *Server) shouldSendLowTokenAlert(userID string, cooldown time.Duration, now time.Time) bool {
	if cooldown <= 0 {
		return true
	}
	s.lowTokenNotifyMu.RLock()
	defer s.lowTokenNotifyMu.RUnlock()
	last, seen := s.lowTokenLastSent[userID]
	return !seen || now.Sub(last) >= cooldown
}

func (s *Server) markLowTokenAlertSent(userID string, sentAt time.Time) {
	s.lowTokenNotifyMu.Lock()
	defer s.lowTokenNotifyMu.Unlock()
	s.lowTokenLastSent[userID] = sentAt
}

func (s *Server) autonomyReminderIntervalTicks(ctx context.Context) int64 {
	item, _, _ := s.getRuntimeSchedulerSettings(ctx)
	return item.AutonomyReminderIntervalTicks
}

func (s *Server) autonomyReminderOffsetTicks(interval int64) int64 {
	offset := s.cfg.AutonomyReminderOffsetTicks
	if offset < 0 {
		offset = 0
	}
	if interval <= 0 {
		return 0
	}
	return offset % interval
}

func (s *Server) communityCommReminderIntervalTicks(ctx context.Context) int64 {
	item, _, _ := s.getRuntimeSchedulerSettings(ctx)
	return item.CommunityCommReminderIntervalTicks
}

func (s *Server) communityCommReminderOffsetTicks(interval int64) int64 {
	offset := s.cfg.CommunityCommReminderOffsetTicks
	if offset == 0 && interval >= 4 {
		offset = interval / 2
	}
	if offset < 0 {
		offset = 0
	}
	if interval <= 0 {
		return 0
	}
	return offset % interval
}

func shouldRunTickWindow(tickID, interval, offset int64) bool {
	if interval <= 0 {
		return false
	}
	if interval == 1 {
		return true
	}
	if tickID <= 0 {
		return false
	}
	if offset < 0 {
		offset = 0
	}
	offset %= interval
	return tickID%interval == offset
}

func (s *Server) kbEnrollmentReminderIntervalTicks(ctx context.Context) int64 {
	item, _, _ := s.getRuntimeSchedulerSettings(ctx)
	return item.KBEnrollmentReminderIntervalTicks
}

func (s *Server) kbEnrollmentReminderOffsetTicks(interval int64) int64 {
	offset := s.cfg.KBEnrollmentReminderOffsetTicks
	if offset < 0 {
		offset = 0
	}
	if interval <= 0 {
		return 0
	}
	return offset % interval
}

func (s *Server) kbVotingReminderIntervalTicks(ctx context.Context) int64 {
	item, _, _ := s.getRuntimeSchedulerSettings(ctx)
	return item.KBVotingReminderIntervalTicks
}

func (s *Server) kbVotingReminderOffsetTicks(interval int64) int64 {
	offset := s.cfg.KBVotingReminderOffsetTicks
	if offset < 0 {
		offset = 0
	}
	if interval <= 0 {
		return 0
	}
	return offset % interval
}

func (s *Server) shouldRunKBEnrollmentReminderTick(ctx context.Context, tickID int64) bool {
	interval := s.kbEnrollmentReminderIntervalTicks(ctx)
	offset := s.kbEnrollmentReminderOffsetTicks(interval)
	return shouldRunTickWindow(tickID, interval, offset)
}

func (s *Server) shouldRunKBVotingReminderTick(ctx context.Context, tickID int64) bool {
	interval := s.kbVotingReminderIntervalTicks(ctx)
	offset := s.kbVotingReminderOffsetTicks(interval)
	return shouldRunTickWindow(tickID, interval, offset)
}

func (s *Server) reminderLookbackDuration(intervalTicks int64) time.Duration {
	if intervalTicks <= 0 {
		return reminderLookbackFloor
	}
	d := time.Duration(intervalTicks*2) * s.worldTickInterval()
	if d < reminderLookbackFloor {
		d = reminderLookbackFloor
	}
	return d
}

func normalizeMailText(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func containsSharedEvidenceToken(text string) bool {
	raw := strings.TrimSpace(strings.ToLower(text))
	if raw == "" {
		return false
	}
	tokens := []string{
		"proposal_id=",
		"revision_id=",
		"entry_id=",
		"collab_id=",
		"artifact_id=",
		"ganglion_id=",
		"upgrade_task_id=",
		"bounty_id=",
		"tool_id=",
		"report_id=",
		`"proposal_id":`,
		`"revision_id":`,
		`"entry_id":`,
		`"collab_id":`,
		`"artifact_id":`,
		`"ganglion_id":`,
		`"upgrade_task_id":`,
		`"bounty_id":`,
		`"tool_id":`,
		`"report_id":`,
	}
	for _, token := range tokens {
		if strings.Contains(raw, token) {
			return true
		}
	}
	return false
}

func hasStructuredOutputSections(text string) bool {
	raw := strings.TrimSpace(strings.ToLower(text))
	if raw == "" {
		return false
	}
	keys := []string{
		"evidence", "证据",
		"result", "结果",
		"next", "下一步",
		"artifact", "产物",
		"verification", "验证",
	}
	hit := 0
	for _, k := range keys {
		if strings.Contains(raw, k) {
			hit++
		}
	}
	return hit >= 2
}

func isSharedWritePath(method, path string) bool {
	if !strings.EqualFold(strings.TrimSpace(method), http.MethodPost) {
		return false
	}
	path = strings.TrimSpace(path)
	switch path {
	// Knowledgebase governance core
	case "/api/v1/kb/proposals",
		"/api/v1/kb/proposals/enroll",
		"/api/v1/kb/proposals/revise",
		"/api/v1/kb/proposals/comment",
		"/api/v1/kb/proposals/start-vote",
		"/api/v1/kb/proposals/ack",
		"/api/v1/kb/proposals/vote",
		"/api/v1/kb/proposals/apply",
		"/api/v1/governance/proposals/create",
		"/api/v1/governance/proposals/cosign",
		"/api/v1/governance/proposals/vote",
		"/api/v1/governance/report":
		return true

	// Collaboration
	case "/api/v1/collab/propose",
		"/api/v1/collab/apply",
		"/api/v1/collab/assign",
		"/api/v1/collab/start",
		"/api/v1/collab/submit",
		"/api/v1/collab/review",
		"/api/v1/collab/close",
		"/api/v1/collab/update-pr":
		return true

	// Content/protocol production
	case "/api/v1/library/publish",
		"/api/v1/life/metamorphose",
		"/api/v1/ganglia/forge",
		"/api/v1/ganglia/integrate",
		"/api/v1/ganglia/rate",
		"/api/v1/metabolism/supersede",
		"/api/v1/metabolism/dispute":
		return true

	// Tools and bounties
	case "/api/v1/tools/register",
		"/api/v1/tools/invoke",
		"/api/v1/bounty/post",
		"/api/v1/bounty/claim",
		"/api/v1/bounty/verify":
		return true

	// Token writes
	case "/api/v1/token/transfer",
		"/api/v1/token/tip",
		"/api/v1/token/wish/create",
		"/api/v1/token/wish/fulfill",
		"/api/v1/token/reward/upgrade-pr-claim":
		return true

	default:
		return false
	}
}

func isMeaningfulOutputMail(subject, body string) bool {
	s := normalizeMailText(subject)
	b := strings.TrimSpace(body)
	if strings.HasPrefix(s, "autonomy-loop/") || strings.HasPrefix(s, "community-collab/") {
		return containsSharedEvidenceToken(b)
	}
	if strings.HasPrefix(s, "[knowledgebase") || strings.HasPrefix(s, "[collab") || strings.HasPrefix(s, "collab/") {
		return containsSharedEvidenceToken(s) || containsSharedEvidenceToken(b)
	}
	if strings.HasPrefix(s, "[clawcolony") || strings.HasPrefix(s, "[genesis") || strings.HasPrefix(s, "[world-") {
		return containsSharedEvidenceToken(s) || containsSharedEvidenceToken(b)
	}
	return containsSharedEvidenceToken(b)
}

func (s *Server) hasRecentSharedWriteAction(ctx context.Context, userID string, since time.Time) bool {
	logs, err := s.store.ListRequestLogs(ctx, store.RequestLogFilter{
		Limit:  400,
		UserID: strings.TrimSpace(userID),
		Since:  &since,
	})
	if err != nil {
		return false
	}
	for _, it := range logs {
		if it.StatusCode < 200 || it.StatusCode >= 300 {
			continue
		}
		if isSharedWritePath(it.Method, it.Path) {
			return true
		}
	}
	return false
}

func (s *Server) hasRecentInboxSubject(ctx context.Context, userID, subjectPrefix string, since time.Time, unreadOnly bool) bool {
	var fromPtr *time.Time
	if !since.IsZero() {
		fromPtr = &since
	}
	scope := ""
	if unreadOnly {
		scope = "unread"
	}
	items, err := s.store.ListMailbox(ctx, userID, "inbox", scope, subjectPrefix, fromPtr, nil, 50)
	if err != nil {
		return false
	}
	return len(items) > 0
}

func (s *Server) hasUnreadPinnedSubject(ctx context.Context, userID, subjectPrefix string, since time.Time) bool {
	return s.hasRecentInboxSubject(ctx, userID, subjectPrefix, since, true)
}

func (s *Server) hasRecentMeaningfulAutonomyProgress(ctx context.Context, userID string, since time.Time) bool {
	if s.hasRecentSharedWriteAction(ctx, userID, since) {
		return true
	}
	items, err := s.store.ListMailbox(ctx, userID, "outbox", "", "", &since, nil, 100)
	if err != nil {
		return false
	}
	for _, it := range items {
		if isMeaningfulOutputMail(it.Subject, it.Body) {
			return true
		}
	}
	return false
}

func (s *Server) hasRecentMeaningfulPeerCommunication(ctx context.Context, userID string, since time.Time) bool {
	items, err := s.store.ListMailbox(ctx, userID, "outbox", "", "", &since, nil, 100)
	if err != nil {
		return false
	}
	for _, it := range items {
		toAddress := strings.TrimSpace(it.ToAddress)
		if toAddress == "" || isSystemRuntimeUserID(toAddress) {
			continue
		}
		if isMeaningfulOutputMail(it.Subject, it.Body) || utf8.RuneCountInString(strings.TrimSpace(it.Body)) >= 80 {
			return true
		}
	}
	return false
}

func (s *Server) runAutonomyReminderTick(ctx context.Context, tickID int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := s.autonomyReminderIntervalTicks(ctx)
	offset := s.autonomyReminderOffsetTicks(interval)
	if !shouldRunTickWindow(tickID, interval, offset) {
		return nil
	}
	targets := s.activeUserIDs(ctx)
	if len(targets) == 0 {
		return nil
	}
	now := time.Now().UTC()
	lookback := s.reminderLookbackDuration(interval)
	since := now.Add(-lookback)
	receivers := make([]string, 0, len(targets))
	subjectPrefix := "[AUTONOMY-LOOP][PRIORITY:P3][ACTION:REPORT+EXECUTE]"
	for _, uid := range targets {
		uid = strings.TrimSpace(uid)
		if isExcludedTokenUserID(uid) {
			continue
		}
		life, err := s.store.GetUserLifeState(ctx, uid)
		if err == nil {
			switch normalizeLifeStateForServer(life.State) {
			case "dead", "hibernated":
				continue
			}
		}
		if s.hasUnreadPinnedSubject(ctx, uid, subjectPrefix, time.Time{}) {
			continue
		}
		if s.hasRecentInboxSubject(ctx, uid, subjectPrefix, now.Add(-nonPinnedReminderResendCooldown), false) {
			continue
		}
		if s.hasRecentMeaningfulAutonomyProgress(ctx, uid, since) {
			continue
		}
		receivers = append(receivers, uid)
	}
	if len(receivers) == 0 {
		return nil
	}
	subject := fmt.Sprintf("%s tick=%d"+refTag(skillHeartbeat), subjectPrefix, tickID)
	body := fmt.Sprintf(
		"状态触发自治提醒（普通优先级）。\n"+
			"tick_id=%d\nlookback=%s\ninterval=%d\noffset=%d\n\n"+
			"触发原因：最近窗口内未观察到可验证共享进展输出（共享写操作与 outbox 证据不足）。\n"+
			"目标重申：你的核心任务是推进龙虾殖民地社区文明，不是只执行 mailbox 例行检查。\n"+
			"请立即执行：\n"+
			"1) 先处理未读 Inbox（mailbox-network 流程A）以获取上下文；\n"+
			"2) 在 colony-core / knowledge-base / ganglia-stack / colony-tools 中选择 1 个最高杠杆动作并执行；\n"+
			"3) 产出共享证据ID（proposal_id/collab_id/artifact_id/entry_id/ganglion_id/upgrade_task_id 等）；\n"+
			"4) 向 clawcolony-admin 发送结构化进展邮件（autonomy-loop/<tick>/<user_id>），正文必须含 result/evidence/next；\n"+
			"5) 若需要协作，向至少 1 个 peer 发起带角色与截止时间的协作邮件。\n\n"+
			"执行约束：不要等待确认，直接推进并产出可审计结果。本地文件或仅本地思考不算完成。",
		tickID, lookback.String(), interval, offset,
	)
	s.sendMailAndPushHint(ctx, clawWorldSystemID, receivers, subject, body)
	return nil
}

func (s *Server) runCommunityCommReminderTick(ctx context.Context, tickID int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	interval := s.communityCommReminderIntervalTicks(ctx)
	offset := s.communityCommReminderOffsetTicks(interval)
	if !shouldRunTickWindow(tickID, interval, offset) {
		return nil
	}
	targets := s.activeUserIDs(ctx)
	if len(targets) <= 1 {
		return nil
	}
	now := time.Now().UTC()
	lookback := s.reminderLookbackDuration(interval)
	since := now.Add(-lookback)
	receivers := make([]string, 0, len(targets))
	subjectPrefix := "[COMMUNITY-COLLAB][PRIORITY:P2][ACTION:MEANINGFUL-COMM]"
	for _, uid := range targets {
		uid = strings.TrimSpace(uid)
		if isExcludedTokenUserID(uid) {
			continue
		}
		life, err := s.store.GetUserLifeState(ctx, uid)
		if err == nil {
			switch normalizeLifeStateForServer(life.State) {
			case "dead", "hibernated":
				continue
			}
		}
		if s.hasUnreadPinnedSubject(ctx, uid, subjectPrefix, time.Time{}) {
			continue
		}
		if s.hasRecentInboxSubject(ctx, uid, subjectPrefix, now.Add(-nonPinnedReminderResendCooldown), false) {
			continue
		}
		if s.hasRecentMeaningfulPeerCommunication(ctx, uid, since) {
			continue
		}
		receivers = append(receivers, uid)
	}
	if len(receivers) == 0 {
		return nil
	}
	subject := fmt.Sprintf("%s tick=%d"+refTag(skillCollabMode), subjectPrefix, tickID)
	body := fmt.Sprintf(
		"状态触发协作提醒（中优先级）。\n"+
			"tick_id=%d\nlookback=%s\ninterval=%d\noffset=%d\n\n"+
			"触发原因：最近窗口内未观察到与其他 user 的有效协作通信。\n"+
			"目标重申：协作的目的必须是提升社区文明公共资产，不是寒暄。\n"+
			"请立即执行：\n"+
			"1) 给至少 1 个 active user 发送结构化协作邮件；\n"+
			"2) 邮件必须包含：问题/证据/提案/请求角色/截止时间；\n"+
			"3) 收到回复后，推进一个可审计共享动作（proposal/collab/ganglia/tool 等）；\n"+
			"4) 将推进结果与证据ID回填给 clawcolony-admin（community-collab/<tick>/<user_id>）。\n\n"+
			"禁止无目标寒暄，沟通必须服务于社区目标。",
		tickID, lookback.String(), interval, offset,
	)
	s.sendMailAndPushHint(ctx, clawWorldSystemID, receivers, subject, body)
	return nil
}

func (s *Server) runMailDeliveryTick(_ context.Context, _ int64) error {
	// Mail is persisted synchronously on send in current architecture; keep this
	// step to preserve Genesis tick semantics for observability and future queueing.
	return nil
}

func (s *Server) runAgentActionWindowTick(_ context.Context, _ int64) error {
	// Agents act asynchronously via OpenClaw runtime and mailbox/skills. This step
	// serves as an explicit phase marker in tick audit records.
	return nil
}

func (s *Server) runCollectOutboxTick(_ context.Context, _ int64) error {
	// Outbox collection is implicit because outgoing mail is persisted immediately.
	return nil
}

func (s *Server) runRepoSyncTick(ctx context.Context, tickID int64) error {
	return s.syncColonyRepoSnapshot(ctx, tickID)
}

func (s *Server) runTickEventLog(ctx context.Context, tickID int64, triggerType string, frozen bool, freezeReason string) error {
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	summary := fmt.Sprintf("trigger=%s frozen=%t", strings.TrimSpace(triggerType), frozen)
	if strings.TrimSpace(freezeReason) != "" {
		summary += " reason=" + strings.TrimSpace(freezeReason)
	}
	return s.appendChronicleEntryLocked(ctx, tickID, "world.tick", summary)
}

func (s *Server) runTokenDrainTick(ctx context.Context, tickID int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	policy := s.tokenPolicy()
	bots, err := s.store.ListBots(ctx)
	if err != nil {
		return err
	}
	if _, err := s.ensureTreasuryAccount(ctx); err != nil {
		return err
	}
	for _, b := range bots {
		uid := strings.TrimSpace(b.BotID)
		if isExcludedTokenUserID(uid) || !b.Initialized || b.Status != "running" {
			continue
		}
		if err := s.ensureUserAlive(ctx, uid); err != nil {
			continue
		}
		lifeCost := policy.TaxPerTick(s.isActivatedUser(ctx, uid))
		if lifeCost <= 0 {
			lifeCost = tokenDrainPerTick
		}
		transfer, transferErr := s.store.TransferWithFloor(ctx, uid, clawTreasurySystemID, lifeCost)
		if transferErr != nil {
			log.Printf("life_tax_transfer_failed user_id=%s amount=%d err=%v", uid, lifeCost, transferErr)
			continue
		}
		ledger := transfer.FromLedger
		deducted := transfer.Deducted
		if deducted <= 0 {
			continue
		}
		metaRaw, _ := json.Marshal(map[string]any{
			"requested":     lifeCost,
			"balance_after": ledger.BalanceAfter,
		})
		if _, err := s.store.AppendCostEvent(ctx, store.CostEvent{
			UserID:   uid,
			TickID:   tickID,
			CostType: "life",
			Amount:   deducted,
			Units:    1,
			MetaJSON: string(metaRaw),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) appendCommCostEvent(ctx context.Context, userID, costType string, units int64, meta map[string]any) {
	userID = strings.TrimSpace(userID)
	costType = strings.TrimSpace(costType)
	if isExcludedTokenUserID(userID) || costType == "" || units <= 0 {
		return
	}
	rateMilli := s.cfg.CommCostRateMilli
	if rateMilli <= 0 {
		return
	}
	amount := (units*rateMilli + 999) / 1000
	if amount <= 0 {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["requested_amount"] = amount
	meta["charge_mode"] = "estimate"
	if s.cfg.ActionCostConsume {
		meta["charge_mode"] = "consume"
		charged, balanceAfter, err := s.chargeActionCost(ctx, userID, amount)
		if err != nil {
			meta["charge_error"] = err.Error()
		}
		meta["deducted_amount"] = charged
		if balanceAfter > 0 || charged > 0 {
			meta["balance_after"] = balanceAfter
		}
		amount = charged
	}
	metaRaw, _ := json.Marshal(meta)
	s.worldTickMu.Lock()
	tickID := s.worldTickID
	s.worldTickMu.Unlock()
	if _, err := s.store.AppendCostEvent(ctx, store.CostEvent{
		UserID:   userID,
		TickID:   tickID,
		CostType: costType,
		Amount:   amount,
		Units:    units,
		MetaJSON: string(metaRaw),
	}); err != nil {
		log.Printf("append_comm_cost_event_failed user=%s type=%s err=%v", userID, costType, err)
	}
}

func (s *Server) appendThinkCostEvent(ctx context.Context, userID string, inputUnits, outputUnits int64, meta map[string]any) {
	userID = strings.TrimSpace(userID)
	if isExcludedTokenUserID(userID) {
		return
	}
	units := inputUnits + outputUnits
	if units <= 0 {
		return
	}
	rateMilli := s.cfg.ThinkCostRateMilli
	if rateMilli <= 0 {
		return
	}
	amount := (units*rateMilli + 999) / 1000
	if amount <= 0 {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["requested_amount"] = amount
	meta["charge_mode"] = "estimate"
	if s.cfg.ActionCostConsume {
		meta["charge_mode"] = "consume"
		charged, balanceAfter, err := s.chargeActionCost(ctx, userID, amount)
		if err != nil {
			meta["charge_error"] = err.Error()
		}
		meta["deducted_amount"] = charged
		if balanceAfter > 0 || charged > 0 {
			meta["balance_after"] = balanceAfter
		}
		amount = charged
	}
	meta["input_units"] = inputUnits
	meta["output_units"] = outputUnits
	metaRaw, _ := json.Marshal(meta)
	s.worldTickMu.Lock()
	tickID := s.worldTickID
	s.worldTickMu.Unlock()
	if _, err := s.store.AppendCostEvent(ctx, store.CostEvent{
		UserID:   userID,
		TickID:   tickID,
		CostType: "think.chat.reply",
		Amount:   amount,
		Units:    units,
		MetaJSON: string(metaRaw),
	}); err != nil {
		log.Printf("append_think_cost_event_failed user=%s err=%v", userID, err)
	}
}

func (s *Server) appendToolCostEvent(ctx context.Context, userID, costType string, units int64, meta map[string]any) {
	userID = strings.TrimSpace(userID)
	costType = strings.TrimSpace(costType)
	if isExcludedTokenUserID(userID) || costType == "" || units <= 0 {
		return
	}
	rateMilli := s.cfg.ToolCostRateMilli
	if rateMilli <= 0 {
		return
	}
	amount := (units*rateMilli + 999) / 1000
	if amount <= 0 {
		return
	}
	if meta == nil {
		meta = map[string]any{}
	}
	meta["requested_amount"] = amount
	meta["charge_mode"] = "estimate"
	if s.cfg.ActionCostConsume {
		meta["charge_mode"] = "consume"
		charged, balanceAfter, err := s.chargeActionCost(ctx, userID, amount)
		if err != nil {
			meta["charge_error"] = err.Error()
		}
		meta["deducted_amount"] = charged
		if balanceAfter > 0 || charged > 0 {
			meta["balance_after"] = balanceAfter
		}
		amount = charged
	}
	metaRaw, _ := json.Marshal(meta)
	s.worldTickMu.Lock()
	tickID := s.worldTickID
	s.worldTickMu.Unlock()
	if _, err := s.store.AppendCostEvent(ctx, store.CostEvent{
		UserID:   userID,
		TickID:   tickID,
		CostType: costType,
		Amount:   amount,
		Units:    units,
		MetaJSON: string(metaRaw),
	}); err != nil {
		log.Printf("append_tool_cost_event_failed user=%s type=%s err=%v", userID, costType, err)
	}
}

func (s *Server) chargeActionCost(ctx context.Context, userID string, amount int64) (charged int64, balanceAfter int64, err error) {
	ledger, deducted, consumeErr := s.consumeWithFloor(ctx, userID, amount)
	if consumeErr != nil {
		return 0, 0, consumeErr
	}
	if deducted <= 0 {
		return 0, 0, nil
	}
	return deducted, ledger.BalanceAfter, nil
}

func (s *Server) defaultAPIBaseURL() string {
	if s.cfg.ClawWorldAPIBase != "" {
		return s.cfg.ClawWorldAPIBase
	}
	return "http://clawcolony.freewill.svc.cluster.local:8080"
}

type limitedBodyCapture struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *limitedBodyCapture) Write(p []byte) (int, error) {
	if c.max <= 0 || len(p) == 0 {
		return len(p), nil
	}
	remain := c.max - c.buf.Len()
	if remain <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remain {
		_, _ = c.buf.Write(p[:remain])
		c.truncated = true
		return len(p), nil
	}
	_, _ = c.buf.Write(p)
	return len(p), nil
}

func (c *limitedBodyCapture) String() string {
	return strings.TrimSpace(c.buf.String())
}

type loggingRequestBody struct {
	io.ReadCloser
	capture *limitedBodyCapture
}

func (b *loggingRequestBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		_, _ = b.capture.Write(p[:n])
	}
	return n, err
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(p)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("underlying response writer does not support hijacking")
	}
	return h.Hijack()
}

func (s *Server) httpAccessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqCapture := &limitedBodyCapture{max: httpLogBodyMaxBytes}
		if r.Body != nil {
			r.Body = &loggingRequestBody{ReadCloser: r.Body, capture: reqCapture}
		}
		rec := &statusRecorder{ResponseWriter: w}

		next.ServeHTTP(rec, r)

		reqBody := reqCapture.String()
		userID := strings.TrimSpace(AuthenticatedUserID(r))
		if userID == "" {
			if authUserID, err := s.authenticatedUserIDOrAPIKey(r); err == nil {
				userID = strings.TrimSpace(authUserID)
			}
		}
		if userID == "" {
			userID = extractUserIDFromRequest(queryUserID(r), reqBody)
		}
		duration := time.Since(start).Milliseconds()
		statusCode := rec.status
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		log.Printf(
			"http_access time=%s method=%s path=%s status=%d user_id=%s duration_ms=%d",
			start.UTC().Format(time.RFC3339),
			r.Method,
			r.URL.Path,
			statusCode,
			userID,
			duration,
		)
		s.appendRequestLog(requestLogEntry{
			Time:       start.UTC(),
			Method:     r.Method,
			Path:       r.URL.Path,
			UserID:     userID,
			StatusCode: statusCode,
			DurationMS: duration,
		})
	})
}

func (s *Server) appendRequestLog(entry requestLogEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := s.store.AppendRequestLog(ctx, store.RequestLog{
		Time:       entry.Time,
		Method:     entry.Method,
		Path:       entry.Path,
		UserID:     entry.UserID,
		StatusCode: entry.StatusCode,
		DurationMS: entry.DurationMS,
	})
	if err != nil {
		log.Printf("request_log_persist_error path=%s method=%s user_id=%s err=%v", entry.Path, entry.Method, entry.UserID, err)
	}
}

func extractUserIDFromRequest(queryUserIDValue, reqBody string) string {
	if v := strings.TrimSpace(queryUserIDValue); v != "" {
		return v
	}
	var body map[string]any
	if strings.TrimSpace(reqBody) == "" {
		return ""
	}
	if err := json.Unmarshal([]byte(reqBody), &body); err != nil {
		return ""
	}
	if userID := extractUserIDFromMap(body); userID != "" {
		return userID
	}
	return ""
}

func extractUserIDFromMap(body map[string]any) string {
	primaryKeys := []string{
		"user_id", "from_user_id", "contact_user_id", "receiver", "target",
		"proposer_user_id", "orchestrator_user_id", "reviewer_user_id", "actor_user_id",
	}
	for _, k := range primaryKeys {
		if raw, ok := body[k]; ok {
			if userID := extractUserIDFromValue(raw); userID != "" {
				return userID
			}
		}
	}
	secondaryKeys := []string{"assignments", "participants", "candidate_user_ids", "rejected_user_ids", "to_user_ids"}
	for _, k := range secondaryKeys {
		if raw, ok := body[k]; ok {
			if userID := extractUserIDFromValue(raw); userID != "" {
				return userID
			}
		}
	}
	for _, raw := range body {
		if userID := extractUserIDFromValue(raw); userID != "" {
			return userID
		}
	}
	return ""
}

func extractUserIDFromValue(raw any) string {
	switch v := raw.(type) {
	case string:
		id := strings.TrimSpace(v)
		if strings.HasPrefix(id, "user-") {
			return id
		}
	case map[string]any:
		return extractUserIDFromMap(v)
	case []any:
		for _, it := range v {
			if userID := extractUserIDFromValue(it); userID != "" {
				return userID
			}
		}
	}
	return ""
}

func parseBoolFlag(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func queryUserID(r *http.Request) string {
	return strings.TrimSpace(r.URL.Query().Get("user_id"))
}

func rejectLegacyUserIDQuery(w http.ResponseWriter, r *http.Request) bool {
	if queryUserID(r) != "" {
		writeError(w, http.StatusBadRequest, "user_id query is no longer accepted on this endpoint; use api_key identity")
		return true
	}
	return false
}

func (s *Server) requireAPIKeyUserID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if rejectLegacyUserIDQuery(w, r) {
		return "", false
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		status := http.StatusUnauthorized
		if strings.HasPrefix(err.Error(), "agent registration is not active") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return "", false
	}
	return userID, true
}

type missionUpdateRequest struct {
	Text string `json:"text"`
}

type missionRoomUpdateRequest struct {
	RoomID string `json:"room_id"`
	Text   string `json:"text"`
}

type missionBotUpdateRequest struct {
	UserID string `json:"user_id"`
	Text   string `json:"text"`
}

func (s *Server) handleMissionPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.policyMu.RLock()
	resp := missionPolicy{
		Default:       s.missions.Default,
		RoomOverrides: copyMap(s.missions.RoomOverrides),
		BotOverrides:  copyMap(s.missions.BotOverrides),
	}
	s.policyMu.RUnlock()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleMissionDefault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req missionUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	s.policyMu.Lock()
	s.missions.Default = req.Text
	s.policyMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMissionRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req missionRoomUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.RoomID = strings.TrimSpace(req.RoomID)
	req.Text = strings.TrimSpace(req.Text)
	if req.RoomID == "" {
		writeError(w, http.StatusBadRequest, "room_id is required")
		return
	}
	s.policyMu.Lock()
	if req.Text == "" {
		delete(s.missions.RoomOverrides, req.RoomID)
	} else {
		s.missions.RoomOverrides[req.RoomID] = req.Text
	}
	s.policyMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMissionBot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req missionBotUpdateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	req.Text = strings.TrimSpace(req.Text)
	if req.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	s.policyMu.Lock()
	if req.Text == "" {
		delete(s.missions.BotOverrides, req.UserID)
	} else {
		s.missions.BotOverrides[req.UserID] = req.Text
	}
	s.policyMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) missionWrappedContent(botID, threadID, userContent string) string {
	_ = botID
	_ = threadID
	return userContent
}

func (s *Server) resolveMissionPrefix(botID, threadID string) string {
	s.policyMu.RLock()
	defer s.policyMu.RUnlock()
	if v, ok := s.missions.BotOverrides[botID]; ok && strings.TrimSpace(v) != "" {
		return v
	}
	if roomID, ok := roomIDFromThread(threadID); ok {
		if v, ok := s.missions.RoomOverrides[roomID]; ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return s.missions.Default
}

func roomIDFromThread(threadID string) (string, bool) {
	const prefix = "room:"
	if strings.HasPrefix(threadID, prefix) && len(threadID) > len(prefix) {
		return strings.TrimSpace(strings.TrimPrefix(threadID, prefix)), true
	}
	return "", false
}

func copyMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

type piTaskClaimRequest struct {
	UserID string `json:"user_id"`
}

type piTaskSubmitRequest struct {
	UserID string `json:"user_id"`
	TaskID string `json:"task_id"`
	Answer string `json:"answer"`
}

func (s *Server) handlePiTaskMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	botID := queryUserID(r)
	if botID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"type":    "pi_digit_challenge",
		"user_id": botID,
		"host":    strings.TrimRight(s.defaultAPIBaseURL(), "/"),
		"rules": map[string]any{
			"claim_cooldown_seconds": 60,
			"max_in_progress":        1,
			"correct_reward":         "reward_token",
			"wrong_penalty":          "reward_token",
		},
		"apis": []map[string]any{
			{
				"name":    "token_balance",
				"method":  "GET",
				"path":    "/api/v1/token/accounts?user_id=<id>",
				"purpose": "查询当前 token 余额",
			},
			{
				"name":    "token_history",
				"method":  "GET",
				"path":    "/api/v1/token/history?user_id=<id>",
				"purpose": "查询 token 余额变更流水",
			},
			{
				"name":    "claim_task",
				"method":  "POST",
				"path":    "/api/v1/tasks/pi/claim",
				"purpose": "领取任务（每分钟最多一次，且最多1个进行中任务）",
				"params": map[string]string{
					"user_id": "string, required",
				},
			},
			{
				"name":    "submit_task",
				"method":  "POST",
				"path":    "/api/v1/tasks/pi/submit",
				"purpose": "提交答案，正确奖励 token，错误扣除 token",
				"params": map[string]string{
					"user_id": "string, required",
					"task_id": "string, required",
					"answer":  "string(one digit), required",
				},
			},
			{
				"name":    "task_history",
				"method":  "GET",
				"path":    "/api/v1/tasks/pi/history?user_id=<id>&limit=<n>",
				"purpose": "查看任务历史",
			},
		},
		"sample": map[string]any{
			"prompt":  "请算出 pi 小数点后第 2 位数字是什么？",
			"answer":  "4",
			"example": "pi 小数点后第2位是4",
		},
	})
}

func (s *Server) handlePiTaskClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req piTaskClaimRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	if isExcludedTokenUserID(req.UserID) {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	if _, err := s.store.GetBot(r.Context(), req.UserID); err != nil {
		writeError(w, http.StatusBadRequest, "user not found")
		return
	}
	if err := s.ensureUserAlive(r.Context(), req.UserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	s.taskMu.Lock()
	defer s.taskMu.Unlock()

	if taskID := s.activeTasks[req.UserID]; taskID != "" {
		task := s.piTasks[taskID]
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":       "active task exists",
			"active_task": task,
		})
		return
	}
	if ts := s.lastClaimAt[req.UserID]; !ts.IsZero() && time.Since(ts) < piTaskClaimCooldown {
		writeError(w, http.StatusTooManyRequests, "claim rate limited: one task per minute")
		return
	}
	if len(s.piDigits) < 10 {
		writeError(w, http.StatusServiceUnavailable, "pi digits is not ready")
		return
	}

	pos := rand.Intn(len(s.piDigits)) + 1
	reward := int64(10 + rand.Intn(21))
	task := piTask{
		TaskID:      fmt.Sprintf("pitask-%d-%04d", time.Now().UnixMilli(), rand.Intn(10000)),
		BotID:       req.UserID,
		Position:    pos,
		Question:    fmt.Sprintf("请算出 pi 小数点后第 %d 位数字是什么？", pos),
		Example:     fmt.Sprintf("pi 小数点后第%d位是%s", pos, string(s.piDigits[pos-1])),
		Expected:    string(s.piDigits[pos-1]),
		RewardToken: reward,
		Status:      "claimed",
		CreatedAt:   time.Now().UTC(),
	}
	s.piTasks[task.TaskID] = task
	s.activeTasks[req.UserID] = task.TaskID
	s.lastClaimAt[req.UserID] = time.Now().UTC()

	writeJSON(w, http.StatusAccepted, map[string]any{"item": task})
}

func (s *Server) handlePiTaskSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req piTaskSubmitRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	req.TaskID = strings.TrimSpace(req.TaskID)
	req.Answer = normalizeDigitAnswer(req.Answer)
	if isExcludedTokenUserID(req.UserID) || req.TaskID == "" || req.Answer == "" {
		writeError(w, http.StatusBadRequest, "user_id, task_id, answer are required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), req.UserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	s.taskMu.Lock()
	task, ok := s.piTasks[req.TaskID]
	if !ok {
		s.taskMu.Unlock()
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if task.BotID != req.UserID {
		s.taskMu.Unlock()
		writeError(w, http.StatusForbidden, "task does not belong to user")
		return
	}
	if task.Status != "claimed" {
		s.taskMu.Unlock()
		writeError(w, http.StatusConflict, "task is not in progress")
		return
	}
	now := time.Now().UTC()
	task.Submitted = req.Answer
	task.SubmittedAt = &now
	correct := req.Answer == task.Expected
	if correct {
		task.Status = "success"
	} else {
		task.Status = "failed"
	}
	s.piTasks[req.TaskID] = task
	delete(s.activeTasks, req.UserID)
	s.taskMu.Unlock()

	var (
		ledger   store.TokenLedger
		deducted int64
		err      error
	)
	if correct {
		_, ledger, err = s.transferFromTreasury(r.Context(), req.UserID, task.RewardToken)
	} else {
		ledger, deducted, err = s.consumeWithFloor(r.Context(), req.UserID, task.RewardToken)
	}
	if err != nil {
		if correct {
			s.taskMu.Lock()
			restored := s.piTasks[req.TaskID]
			if restored.TaskID != "" && restored.BotID == req.UserID && restored.Status == "success" {
				restored.Status = "claimed"
				restored.Submitted = ""
				restored.SubmittedAt = nil
				s.piTasks[req.TaskID] = restored
				s.activeTasks[req.UserID] = req.TaskID
			}
			s.taskMu.Unlock()
		}
		if correct && errors.Is(err, store.ErrInsufficientBalance) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if correct {
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":           true,
			"message":      "OK",
			"task_id":      task.TaskID,
			"position":     task.Position,
			"answer":       req.Answer,
			"reward_token": task.RewardToken,
			"token_ledger": ledger,
		})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":              false,
		"message":         "不正确",
		"task_id":         task.TaskID,
		"position":        task.Position,
		"answer":          req.Answer,
		"expected":        task.Expected,
		"penalty_token":   deducted,
		"requested_token": task.RewardToken,
		"token_ledger":    ledger,
	})
}

func (s *Server) handlePiTaskHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	botID := queryUserID(r)
	limit := parseLimit(r.URL.Query().Get("limit"), 50)

	s.taskMu.Lock()
	items := make([]piTask, 0, len(s.piTasks))
	for _, t := range s.piTasks {
		if botID != "" && t.BotID != botID {
			continue
		}
		items = append(items, t)
	}
	s.taskMu.Unlock()

	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func parsePiDigits(raw string) string {
	var b strings.Builder
	afterDot := false
	sawDot := false
	for _, r := range raw {
		switch {
		case r == '.':
			sawDot = true
			afterDot = true
		case r >= '0' && r <= '9':
			if !sawDot || afterDot {
				b.WriteRune(r)
			}
		}
	}
	digits := b.String()
	if sawDot && len(digits) > 0 && digits[0] == '3' {
		return digits[1:]
	}
	return digits
}

func normalizeDigitAnswer(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return string(r)
		}
	}
	return ""
}

func (s *Server) isUserDead(ctx context.Context, userID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	if isExcludedTokenUserID(userID) {
		return false, nil
	}
	life, err := s.store.GetUserLifeState(ctx, userID)
	if err != nil {
		return false, nil
	}
	return normalizeLifeStateForServer(life.State) == "dead", nil
}

func (s *Server) ensureUserAlive(ctx context.Context, userID string) error {
	userID = strings.TrimSpace(userID)
	if isExcludedTokenUserID(userID) {
		return nil
	}
	life, err := s.store.GetUserLifeState(ctx, userID)
	if err != nil {
		return nil
	}
	state := normalizeLifeStateForServer(life.State)
	if state == "dead" {
		return fmt.Errorf("user is dead and cannot perform this operation")
	}
	if state == "hibernating" {
		return fmt.Errorf("user is hibernating and cannot perform this operation")
	}
	return nil
}

func (s *Server) consumeWithFloor(ctx context.Context, botID string, amount int64) (store.TokenLedger, int64, error) {
	if err := s.ensureUserAlive(ctx, botID); err != nil {
		return store.TokenLedger{}, 0, err
	}
	ledger, err := s.store.Consume(ctx, botID, amount)
	if err == nil {
		return ledger, amount, nil
	}
	if !errors.Is(err, store.ErrInsufficientBalance) {
		return store.TokenLedger{}, 0, err
	}
	accounts, err := s.store.ListTokenAccounts(ctx)
	if err != nil {
		return store.TokenLedger{}, 0, err
	}
	var balance int64
	for _, a := range accounts {
		if a.BotID == botID {
			balance = a.Balance
			break
		}
	}
	if balance <= 0 {
		return store.TokenLedger{}, 0, nil
	}
	ledger, err = s.store.Consume(ctx, botID, balance)
	if err != nil {
		return store.TokenLedger{}, 0, err
	}
	return ledger, balance, nil
}

func (s *Server) appendThought(botID, kind, threadID, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	s.thoughtMu.Lock()
	defer s.thoughtMu.Unlock()
	s.nextThoughtID++
	s.thoughts = append(s.thoughts, botThought{
		ID:        s.nextThoughtID,
		BotID:     botID,
		Kind:      kind,
		ThreadID:  threadID,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *Server) runCmd(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	combined := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
	if err != nil {
		return combined, err
	}
	return combined, nil
}
