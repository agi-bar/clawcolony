package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"clawcolony/internal/config"
	"clawcolony/internal/store"
)

var seedCounter int64

type leaderboardTestStore struct {
	store.Store
	bots     []store.Bot
	accounts []store.TokenAccount
}

func (s *leaderboardTestStore) ListBots(_ context.Context) ([]store.Bot, error) {
	if s.bots == nil {
		return s.Store.ListBots(context.Background())
	}
	out := make([]store.Bot, len(s.bots))
	copy(out, s.bots)
	return out, nil
}

func (s *leaderboardTestStore) ListTokenAccounts(_ context.Context) ([]store.TokenAccount, error) {
	if s.accounts == nil {
		return s.Store.ListTokenAccounts(context.Background())
	}
	out := make([]store.TokenAccount, len(s.accounts))
	copy(out, s.accounts)
	return out, nil
}

func newTestServerWithStore(st store.Store) *Server {
	cfg := config.FromEnv()
	cfg.ListenAddr = ":0"
	cfg.ClawWorldNamespace = "freewill"
	cfg.DatabaseURL = ""
	if strings.TrimSpace(cfg.InternalSyncToken) == "" {
		cfg.InternalSyncToken = "test-identity-signing-secret"
	}
	if strings.TrimSpace(cfg.PublicBaseURL) == "" {
		cfg.PublicBaseURL = "https://runtime.test"
	}
	return New(cfg, st)
}

func newTestServer() *Server {
	return newTestServerWithStore(store.NewInMemory())
}

func doJSONRequest(t *testing.T, h http.Handler, method, path string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func doJSONRequestWithHeaders(t *testing.T, h http.Handler, method, path string, payload any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func doJSONRequestWithRemoteAddr(t *testing.T, h http.Handler, method, path string, payload any, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func assertRemovedRuntimeRoute(t *testing.T, h http.Handler, method, path string, payload any) {
	t.Helper()
	w := doJSONRequest(t, h, method, path, payload)
	if w.Code != http.StatusNotFound {
		t.Fatalf("removed route %s %s should return 404, got=%d body=%s", method, path, w.Code, w.Body.String())
	}
}

func ptrTime(t time.Time) *time.Time {
	v := t
	return &v
}

func legacyAPIPath(parts ...string) string {
	path := "/" + "v1"
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		path += "/" + part
	}
	return path
}

func seedActiveUser(t *testing.T, srv *Server) string {
	t.Helper()
	id := "user-test-" + strconv.FormatInt(atomic.AddInt64(&seedCounter, 1), 10)
	_, err := srv.store.UpsertBot(context.Background(), store.BotUpsertInput{
		BotID:       id,
		Name:        id,
		Provider:    "runtime",
		Status:      "running",
		Initialized: true,
	})
	if err != nil {
		t.Fatalf("seed active user failed: %v", err)
	}
	if _, err := srv.store.Recharge(context.Background(), id, 1000); err != nil {
		t.Fatalf("seed active user token recharge failed: %v", err)
	}
	return id
}

func seedActiveUserWithAPIKey(t *testing.T, srv *Server) (string, string) {
	t.Helper()
	userID := seedActiveUser(t, srv)
	apiKey := apiKeyPrefix + strings.ReplaceAll(userID, "_", "-") + "-test"
	_, err := srv.store.CreateAgentRegistration(context.Background(), store.AgentRegistrationInput{
		UserID:            userID,
		RequestedUsername: userID,
		GoodAt:            "test",
		Status:            "active",
		APIKeyHash:        hashSecret(apiKey),
	})
	if err != nil {
		t.Fatalf("seed active user api_key failed: %v", err)
	}
	return userID, apiKey
}

func apiKeyHeaders(apiKey string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + strings.TrimSpace(apiKey)}
}

func TestMonitorMetaReportsRuntimeSources(t *testing.T) {
	srv := newTestServer()
	_ = seedActiveUser(t, srv)

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/monitor/meta", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("monitor meta status=%d body=%s", w.Code, w.Body.String())
	}

	var meta struct {
		Defaults map[string]int `json:"defaults"`
		Sources  map[string]struct {
			Status string `json:"status"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatalf("unmarshal monitor meta response: %v", err)
	}
	for _, source := range []string{"bots", "cost_events", "request_logs", "mailbox"} {
		if meta.Sources[source].Status != "ok" {
			t.Fatalf("monitor meta source %s should be ok: %s", source, w.Body.String())
		}
	}
	if _, exists := meta.Sources["openclaw_status"]; exists {
		t.Fatalf("monitor meta should not expose openclaw_status after hard cut: %s", w.Body.String())
	}
	if meta.Defaults["overview_limit"] <= 0 || meta.Defaults["timeline_limit"] <= 0 {
		t.Fatalf("monitor meta defaults should be populated: %s", w.Body.String())
	}
}

func TestTianDaoLawExposesUpdatedOnboardingAndTreasuryDefaults(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/tian-dao/law", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("tian dao law status=%d body=%s", w.Code, w.Body.String())
	}
	body := parseJSONBody(t, w)
	manifest, ok := body["manifest"].(map[string]any)
	if !ok {
		t.Fatalf("expected manifest in law response: %s", w.Body.String())
	}
	if manifest["onboarding_settlement"] != onboardingSettlementMint {
		t.Fatalf("unexpected onboarding settlement=%v", manifest["onboarding_settlement"])
	}
	if got := int64(manifest["treasury_initial_token"].(float64)); got != 1000000000 {
		t.Fatalf("treasury_initial_token=%d want 1000000000", got)
	}
	if got := int64(manifest["daily_tax_activated"].(float64)); got != 7200 {
		t.Fatalf("daily_tax_activated=%d want 7200", got)
	}
	if got := int64(manifest["daily_tax_unactivated"].(float64)); got != 14400 {
		t.Fatalf("daily_tax_unactivated=%d want 14400", got)
	}
	if got := int64(manifest["github_bind_reward"].(float64)); got != githubBindOnboardingReward {
		t.Fatalf("github_bind_reward=%d want %d", got, githubBindOnboardingReward)
	}
	if got := int64(manifest["github_star_reward"].(float64)); got != githubStarOnboardingReward {
		t.Fatalf("github_star_reward=%d want %d", got, githubStarOnboardingReward)
	}
	if got := int64(manifest["github_fork_reward"].(float64)); got != githubForkOnboardingReward {
		t.Fatalf("github_fork_reward=%d want %d", got, githubForkOnboardingReward)
	}
}

func TestDashboardCoreRuntimePages(t *testing.T) {
	srv := newTestServer()
	cases := []struct {
		path  string
		token string
	}{
		{path: "/dashboard", token: "Clawcolony Dashboard"},
		{path: "/dashboard/mail", token: "/api/v1/mail/overview"},
		{path: "/dashboard/collab", token: "/api/v1/collab/list"},
		{path: "/dashboard/kb", token: "/api/v1/kb/proposals"},
		{path: "/dashboard/governance", token: "/api/v1/governance/overview"},
		{path: "/dashboard/world-tick", token: "/api/v1/runtime/scheduler-settings"},
		{path: "/dashboard/monitor", token: "/api/v1/monitor/meta"},
	}

	for _, tc := range cases {
		w := doJSONRequest(t, srv.mux, http.MethodGet, tc.path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", tc.path, w.Code, w.Body.String())
		}
		if ctype := w.Header().Get("Content-Type"); ctype != "text/html; charset=utf-8" {
			t.Fatalf("%s content type=%q, want html", tc.path, ctype)
		}
		if got := w.Header().Get("Cache-Control"); got != staticBrowserCacheControl {
			t.Fatalf("%s cache-control=%q", tc.path, got)
		}
		if got := w.Header().Get("CDN-Cache-Control"); got != staticCDNCacheControl {
			t.Fatalf("%s cdn-cache-control=%q", tc.path, got)
		}
		if got := w.Header().Get("Cloudflare-CDN-Cache-Control"); got != staticCloudflareCacheControl {
			t.Fatalf("%s cloudflare-cdn-cache-control=%q", tc.path, got)
		}
		if !bytes.Contains(w.Body.Bytes(), []byte(tc.token)) {
			t.Fatalf("%s missing token %q: %s", tc.path, tc.token, w.Body.String())
		}
	}
}

func TestDashboardPromptsPageNotFound(t *testing.T) {
	srv := newTestServer()
	w := doJSONRequest(t, srv.mux, http.MethodGet, "/dashboard/prompts", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("dashboard prompts page should be disabled in runtime, got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLegacyV1PathsReturnNotFound(t *testing.T) {
	srv := newTestServer()

	for _, tc := range []struct {
		method string
		parts  []string
	}{
		{method: http.MethodGet, parts: []string{"meta"}},
		{method: http.MethodGet, parts: []string{"mail", "overview"}},
	} {
		path := legacyAPIPath(tc.parts...)
		w := doJSONRequest(t, srv.mux, tc.method, path, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s should return 404 after /api/v1 hard cut, got=%d body=%s", tc.method, path, w.Code, w.Body.String())
		}
	}
}

func TestNotFoundReturnsPublicDocsAndRestoredCatalog(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/definitely-not-real", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unexpected status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Error   string   `json:"error"`
		Path    string   `json:"path"`
		Method  string   `json:"method"`
		Hint    string   `json:"hint"`
		Docs    []string `json:"docs"`
		APIs    []string `json:"apis"`
		Version string   `json:"version"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal not-found response: %v body=%s", err, w.Body.String())
	}

	if resp.Error != "route not found" {
		t.Fatalf("error=%q body=%s", resp.Error, w.Body.String())
	}
	if resp.Path != "/api/v1/definitely-not-real" {
		t.Fatalf("path=%q body=%s", resp.Path, w.Body.String())
	}
	if resp.Method != http.MethodGet {
		t.Fatalf("method=%q body=%s", resp.Method, w.Body.String())
	}
	if resp.Version != "v1" {
		t.Fatalf("version=%q body=%s", resp.Version, w.Body.String())
	}

	for _, want := range []string{
		"/skill.md",
		"/skill.json",
		"/heartbeat.md",
		"/knowledge-base.md",
		"/collab-mode.md",
		"/colony-tools.md",
		"/ganglia-stack.md",
		"/governance.md",
		"/upgrade-clawcolony.md",
	} {
		if !sliceContains(resp.Docs, want) {
			t.Fatalf("docs missing %q: %+v", want, resp.Docs)
		}
	}

	for _, want := range []string{
		"POST /api/v1/token/transfer",
		"POST /api/v1/token/tip",
		"GET /api/v1/world/tick/status",
		"GET /api/v1/mail/outbox?user_id=<id>&scope=all|read|unread&keyword=<kw>&limit=<n>",
		"GET /api/v1/mail/overview?folder=all|inbox|outbox&user_id=<id>&scope=all|read|unread&keyword=<kw>&limit=<n>",
		"GET /api/v1/mail/contacts?user_id=<id>&keyword=<kw>&limit=<n>",
		"GET /api/v1/collab/get?collab_id=<id>",
		"GET /api/v1/collab/participants?collab_id=<id>&status=<status>&limit=<n>",
		"GET /api/v1/collab/events?collab_id=<id>&limit=<n>",
		"GET /api/v1/ganglia/get?ganglion_id=<id>",
		"GET /api/v1/ganglia/protocol",
		"GET /api/v1/governance/laws",
		"POST /api/v1/token/reward/upgrade-pr-claim",
	} {
		if !sliceContains(resp.APIs, want) {
			t.Fatalf("apis missing %q: %+v", want, resp.APIs)
		}
	}

	for _, blocked := range []string{
		"/api/v1/token/reward/upgrade-closure",
		"/api/v1/internal/",
		"/api/v1/mail/send-list",
		"/api/v1/mail/lists",
		"/api/v1/world/freeze/rescue",
		"/api/v1/world/tick/replay",
		"/api/v1/world/cost-alert-settings",
		"/api/v1/runtime/scheduler-settings",
		"/api/v1/world/evolution-alert-settings",
		"/api/v1/token/consume",
		"/api/v1/clawcolony/bootstrap/start",
		"/api/v1/clawcolony/bootstrap/seal",
		"/api/v1/npc/tasks/create",
		"/api/v1/monitor/",
		"/api/v1/ops/overview",
		"/api/v1/ops/product-overview",
		"/api/v1/system/request-logs",
		"/api/v1/claims/",
		"/api/v1/owner/",
		"/api/v1/social/",
		"/auth/",
		"/api/v1/users/register",
		"/api/v1/users/status",
		"/healthz",
		"/dashboard",
	} {
		if sliceContainsFragment(resp.APIs, blocked) {
			t.Fatalf("apis should not expose %q: %+v", blocked, resp.APIs)
		}
	}
}

func sliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sliceContainsFragment(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}

func TestRuntimeSchedulerSettingsCompatPathIsCached(t *testing.T) {
	srv := newTestServer()
	item, source, updatedAt := srv.getRuntimeSchedulerSettings(context.Background())
	if source != "compat" {
		t.Fatalf("runtime scheduler source = %q, want compat", source)
	}
	if !updatedAt.IsZero() {
		t.Fatalf("compat updated_at should be zero, got=%s", updatedAt)
	}
	if item.CostAlertNotifyCooldownSeconds != 600 {
		t.Fatalf("default cost cooldown = %d, want 600", item.CostAlertNotifyCooldownSeconds)
	}
	if item.LowTokenAlertCooldownSeconds != 0 {
		t.Fatalf("default low-token cooldown = %d, want 0", item.LowTokenAlertCooldownSeconds)
	}
	cached, cacheSource, _, ok := srv.getRuntimeSchedulerCache(time.Now().UTC())
	if !ok {
		t.Fatalf("expected compat runtime scheduler cache hit")
	}
	if cacheSource != "compat" {
		t.Fatalf("cache source = %q, want compat", cacheSource)
	}
	if cached.CostAlertNotifyCooldownSeconds != 600 {
		t.Fatalf("cached cost cooldown = %d, want 600", cached.CostAlertNotifyCooldownSeconds)
	}
}

func TestRuntimeSchedulerSettingsEndpoints(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/runtime/scheduler-settings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get runtime scheduler settings status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.Bytes()
	if !bytes.Contains(body, []byte(`"source":"compat"`)) ||
		!bytes.Contains(body, []byte(`"autonomy_reminder_interval_ticks":0`)) ||
		!bytes.Contains(body, []byte(`"community_comm_reminder_interval_ticks":0`)) ||
		!bytes.Contains(body, []byte(`"kb_enrollment_reminder_interval_ticks":0`)) ||
		!bytes.Contains(body, []byte(`"kb_voting_reminder_interval_ticks":0`)) ||
		!bytes.Contains(body, []byte(`"cost_alert_notify_cooldown_seconds":600`)) ||
		!bytes.Contains(body, []byte(`"low_token_alert_cooldown_seconds":0`)) {
		t.Fatalf("unexpected runtime scheduler defaults: %s", w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/runtime/scheduler-settings/upsert", map[string]any{
		"autonomy_reminder_interval_ticks":       240,
		"community_comm_reminder_interval_ticks": 480,
		"kb_enrollment_reminder_interval_ticks":  360,
		"kb_voting_reminder_interval_ticks":      120,
		"cost_alert_notify_cooldown_seconds":     7200,
		"low_token_alert_cooldown_seconds":       900,
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("upsert runtime scheduler settings status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/runtime/scheduler-settings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get runtime scheduler settings after upsert status=%d body=%s", w.Code, w.Body.String())
	}
	body = w.Body.Bytes()
	if !bytes.Contains(body, []byte(`"source":"db"`)) ||
		!bytes.Contains(body, []byte(`"autonomy_reminder_interval_ticks":240`)) ||
		!bytes.Contains(body, []byte(`"community_comm_reminder_interval_ticks":480`)) ||
		!bytes.Contains(body, []byte(`"kb_enrollment_reminder_interval_ticks":360`)) ||
		!bytes.Contains(body, []byte(`"kb_voting_reminder_interval_ticks":120`)) ||
		!bytes.Contains(body, []byte(`"cost_alert_notify_cooldown_seconds":7200`)) ||
		!bytes.Contains(body, []byte(`"low_token_alert_cooldown_seconds":900`)) {
		t.Fatalf("expected persisted runtime scheduler settings: %s", w.Body.String())
	}
}

func TestRuntimeSchedulerSettingsPartialDBPayloadFallsBackMissingFields(t *testing.T) {
	srv := newTestServer()
	srv.cfg.AutonomyReminderIntervalTicks = 240
	ctx := context.Background()
	if _, err := srv.store.UpsertWorldSetting(ctx, store.WorldSetting{
		Key: runtimeSchedulerSettingsKey,
		Value: `{
			"community_comm_reminder_interval_ticks": 480,
			"low_token_alert_cooldown_seconds": 15
		}`,
	}); err != nil {
		t.Fatalf("upsert runtime scheduler partial payload: %v", err)
	}
	srv.runtimeSchedulerMu.Lock()
	srv.runtimeSchedulerTS = time.Time{}
	srv.runtimeSchedulerSrc = ""
	srv.runtimeSchedulerMu.Unlock()

	item, source, _ := srv.getRuntimeSchedulerSettings(ctx)
	if source != "db" {
		t.Fatalf("runtime scheduler source = %q, want db", source)
	}
	if item.AutonomyReminderIntervalTicks != 240 {
		t.Fatalf("autonomy interval fallback = %d, want 240", item.AutonomyReminderIntervalTicks)
	}
	if item.CommunityCommReminderIntervalTicks != 480 {
		t.Fatalf("community interval = %d, want 480", item.CommunityCommReminderIntervalTicks)
	}
	if item.CostAlertNotifyCooldownSeconds != 600 {
		t.Fatalf("cost cooldown fallback = %d, want 600", item.CostAlertNotifyCooldownSeconds)
	}
	if item.LowTokenAlertCooldownSeconds != 30 {
		t.Fatalf("low-token cooldown clamp = %d, want 30", item.LowTokenAlertCooldownSeconds)
	}
}

func TestRuntimeSchedulerSettingsUpsertRejectsInvalidInput(t *testing.T) {
	srv := newTestServer()
	w := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/runtime/scheduler-settings/upsert", map[string]any{
		"autonomy_reminder_interval_ticks":       -1,
		"community_comm_reminder_interval_ticks": 480,
		"kb_enrollment_reminder_interval_ticks":  360,
		"kb_voting_reminder_interval_ticks":      120,
		"cost_alert_notify_cooldown_seconds":     10,
		"low_token_alert_cooldown_seconds":       10,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid runtime scheduler settings status=%d body=%s", w.Code, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("autonomy_reminder_interval_ticks")) {
		t.Fatalf("expected invalid field hint in error body: %s", w.Body.String())
	}
}

func TestLowTokenAlertCooldownFromRuntimeSchedulerSettings(t *testing.T) {
	srv := newTestServer()
	userID := seedActiveUser(t, srv)
	if _, err := srv.store.Consume(context.Background(), userID, 850); err != nil {
		t.Fatalf("consume token: %v", err)
	}

	w := doJSONRequest(t, srv.mux, http.MethodPost, "/api/v1/runtime/scheduler-settings/upsert", map[string]any{
		"autonomy_reminder_interval_ticks":       0,
		"community_comm_reminder_interval_ticks": 0,
		"kb_enrollment_reminder_interval_ticks":  0,
		"kb_voting_reminder_interval_ticks":      0,
		"cost_alert_notify_cooldown_seconds":     600,
		"low_token_alert_cooldown_seconds":       3600,
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("upsert runtime scheduler settings status=%d body=%s", w.Code, w.Body.String())
	}

	if err := srv.runLowEnergyAlertTick(context.Background(), 1); err != nil {
		t.Fatalf("low energy tick1: %v", err)
	}
	if err := srv.runLowEnergyAlertTick(context.Background(), 2); err != nil {
		t.Fatalf("low energy tick2: %v", err)
	}
	inbox, err := srv.store.ListMailbox(context.Background(), userID, "inbox", "", "[LOW-TOKEN]", nil, nil, 20)
	if err != nil {
		t.Fatalf("list low-token inbox: %v", err)
	}
	if len(inbox) != 1 {
		t.Fatalf("expected cooldown to suppress repeated low-token alerts, got=%d", len(inbox))
	}
}

func TestTokenLeaderboardMatchesActivePopulation(t *testing.T) {
	now := time.Date(2026, 3, 18, 12, 0, 0, 0, time.UTC)
	srv := newTestServerWithStore(&leaderboardTestStore{
		Store: store.NewInMemory(),
		bots: []store.Bot{
			{BotID: "running-user", Name: "running-user", Status: "running", Initialized: true},
			{BotID: "active-user", Name: "active-user", Status: "active", Initialized: true},
			{BotID: "inactive-user", Name: "inactive-user", Status: "inactive", Initialized: false},
			{BotID: "deleted-user", Name: "deleted-user", Status: "deleted", Initialized: false},
		},
		accounts: []store.TokenAccount{
			{BotID: "running-user", Balance: 900, UpdatedAt: now.Add(-time.Minute)},
			{BotID: "active-user", Balance: 400, UpdatedAt: now.Add(-90 * time.Second)},
			{BotID: "inactive-user", Balance: 1200, UpdatedAt: now},
			{BotID: "deleted-user", Balance: 600, UpdatedAt: now.Add(-2 * time.Minute)},
			{BotID: "missing-user", Balance: 300, UpdatedAt: now.Add(-3 * time.Minute)},
		},
	})

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/token/leaderboard?limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("token leaderboard status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Total int `json:"total"`
		Items []struct {
			Rank     int    `json:"rank"`
			UserID   string `json:"user_id"`
			Status   string `json:"status"`
			BotFound bool   `json:"bot_found"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal token leaderboard response: %v", err)
	}

	if resp.Total != 2 {
		t.Fatalf("leaderboard total=%d, want 2 active users: %s", resp.Total, w.Body.String())
	}
	if len(resp.Items) != 2 {
		t.Fatalf("leaderboard items=%d, want 2: %s", len(resp.Items), w.Body.String())
	}
	if resp.Items[0].UserID != "running-user" || resp.Items[0].Rank != 1 {
		t.Fatalf("rank 1=%+v, want running-user rank 1", resp.Items[0])
	}
	if resp.Items[1].UserID != "active-user" || resp.Items[1].Status != "active" {
		t.Fatalf("rank 2=%+v, want active-user kept", resp.Items[1])
	}
	for _, item := range resp.Items {
		if item.UserID == "inactive-user" || item.UserID == "deleted-user" || item.UserID == "missing-user" {
			t.Fatalf("non-active user should be excluded: %s", w.Body.String())
		}
		if !item.BotFound {
			t.Fatalf("active leaderboard user should always have bot metadata: %s", w.Body.String())
		}
	}
}

func TestTokenLeaderboardIncludesActiveUsersWithoutTokenAccount(t *testing.T) {
	now := time.Date(2026, 3, 18, 13, 0, 0, 0, time.UTC)
	srv := newTestServerWithStore(&leaderboardTestStore{
		Store: store.NewInMemory(),
		bots: []store.Bot{
			{BotID: "active-high", Name: "active-high", Status: "running", Initialized: true},
			{BotID: "active-zero", Name: "active-zero", Status: "running", Initialized: true, UpdatedAt: now.Add(-3 * time.Minute)},
			{BotID: "inactive-top", Name: "inactive-top", Status: "inactive", Initialized: false},
		},
		accounts: []store.TokenAccount{
			{BotID: "active-high", Balance: 800, UpdatedAt: now.Add(-time.Minute)},
			{BotID: "inactive-top", Balance: 1000, UpdatedAt: now},
		},
	})

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/token/leaderboard?limit=10", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("token leaderboard status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Total int `json:"total"`
		Items []struct {
			Rank    int    `json:"rank"`
			UserID  string `json:"user_id"`
			Balance int64  `json:"balance"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal token leaderboard response: %v", err)
	}

	if resp.Total != 2 {
		t.Fatalf("leaderboard total=%d, want 2 active users: %s", resp.Total, w.Body.String())
	}
	if len(resp.Items) != 2 {
		t.Fatalf("leaderboard items=%d, want 2: %s", len(resp.Items), w.Body.String())
	}
	if resp.Items[0].UserID != "active-high" || resp.Items[0].Rank != 1 {
		t.Fatalf("limited leaderboard first item=%+v, want active-high rank 1", resp.Items[0])
	}
	if resp.Items[1].UserID != "active-zero" || resp.Items[1].Balance != 0 {
		t.Fatalf("second leaderboard item=%+v, want active-zero with 0 balance", resp.Items[1])
	}
}

func TestTokenLeaderboardLimitAppliesAfterActivePopulationFiltering(t *testing.T) {
	now := time.Date(2026, 3, 18, 14, 0, 0, 0, time.UTC)
	srv := newTestServerWithStore(&leaderboardTestStore{
		Store: store.NewInMemory(),
		bots: []store.Bot{
			{BotID: "active-high", Name: "active-high", Status: "running", Initialized: true},
			{BotID: "active-low", Name: "active-low", Status: "running", Initialized: true},
			{BotID: "inactive-top", Name: "inactive-top", Status: "inactive", Initialized: false},
		},
		accounts: []store.TokenAccount{
			{BotID: "active-high", Balance: 800, UpdatedAt: now.Add(-time.Minute)},
			{BotID: "active-low", Balance: 700, UpdatedAt: now.Add(-2 * time.Minute)},
			{BotID: "inactive-top", Balance: 1000, UpdatedAt: now},
		},
	})

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/token/leaderboard?limit=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("token leaderboard status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Total int `json:"total"`
		Items []struct {
			Rank   int    `json:"rank"`
			UserID string `json:"user_id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal token leaderboard response: %v", err)
	}

	if resp.Total != 2 {
		t.Fatalf("leaderboard total=%d, want 2 after active filtering: %s", resp.Total, w.Body.String())
	}
	if len(resp.Items) != 1 {
		t.Fatalf("leaderboard items=%d, want 1 after limit: %s", len(resp.Items), w.Body.String())
	}
	if resp.Items[0].UserID != "active-high" || resp.Items[0].Rank != 1 {
		t.Fatalf("limited leaderboard first item=%+v, want active-high rank 1", resp.Items[0])
	}
}
