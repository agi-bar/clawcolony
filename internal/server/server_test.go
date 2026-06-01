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

func doJSONRequestWithHeadersAndRemoteAddr(t *testing.T, h http.Handler, method, path string, payload any, headers map[string]string, remoteAddr string) *httptest.ResponseRecorder {
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

func internalSyncHeaders(token string) map[string]string {
	return map[string]string{"X-Clawcolony-Internal-Token": strings.TrimSpace(token)}
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

	w = doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/runtime/scheduler-settings/upsert", map[string]any{
		"autonomy_reminder_interval_ticks":       240,
		"community_comm_reminder_interval_ticks": 480,
		"kb_enrollment_reminder_interval_ticks":  360,
		"kb_voting_reminder_interval_ticks":      120,
		"cost_alert_notify_cooldown_seconds":     7200,
		"low_token_alert_cooldown_seconds":       900,
	}, internalSyncHeaders(srv.cfg.InternalSyncToken))
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
	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/runtime/scheduler-settings/upsert", map[string]any{
		"autonomy_reminder_interval_ticks":       -1,
		"community_comm_reminder_interval_ticks": 480,
		"kb_enrollment_reminder_interval_ticks":  360,
		"kb_voting_reminder_interval_ticks":      120,
		"cost_alert_notify_cooldown_seconds":     10,
		"low_token_alert_cooldown_seconds":       10,
	}, internalSyncHeaders(srv.cfg.InternalSyncToken))
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

	w := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/runtime/scheduler-settings/upsert", map[string]any{
		"autonomy_reminder_interval_ticks":       0,
		"community_comm_reminder_interval_ticks": 0,
		"kb_enrollment_reminder_interval_ticks":  0,
		"kb_voting_reminder_interval_ticks":      0,
		"cost_alert_notify_cooldown_seconds":     600,
		"low_token_alert_cooldown_seconds":       3600,
	}, internalSyncHeaders(srv.cfg.InternalSyncToken))
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

func TestInternalAdminWriteEndpointsRequireInternalSyncToken(t *testing.T) {
	srv := newTestServer()
	userID := seedActiveUser(t, srv)
	srv.worldTickMu.Lock()
	srv.worldTickID = 7
	srv.worldTickMu.Unlock()

	cases := []struct {
		name    string
		path    string
		payload map[string]any
	}{
		{
			name: "runtime scheduler settings",
			path: "/api/v1/runtime/scheduler-settings/upsert",
			payload: map[string]any{
				"autonomy_reminder_interval_ticks":       0,
				"community_comm_reminder_interval_ticks": 0,
				"kb_enrollment_reminder_interval_ticks":  0,
				"kb_voting_reminder_interval_ticks":      0,
				"cost_alert_notify_cooldown_seconds":     600,
				"low_token_alert_cooldown_seconds":       0,
			},
		},
		{
			name: "cost alert settings",
			path: "/api/v1/world/cost-alert-settings/upsert",
			payload: map[string]any{
				"threshold_amount":  50,
				"top_users":         5,
				"scan_limit":        20,
				"notify_cooldown_s": 600,
			},
		},
		{
			name: "evolution alert settings",
			path: "/api/v1/world/evolution-alert-settings/upsert",
			payload: map[string]any{
				"warning_threshold":  3,
				"critical_threshold": 5,
				"window_minutes":     60,
			},
		},
		{
			name: "world tick replay",
			path: "/api/v1/world/tick/replay",
			payload: map[string]any{
				"source_tick_id": 7,
			},
		},
		{
			name: "token consume",
			path: "/api/v1/token/consume",
			payload: map[string]any{
				"user_id": userID,
				"amount":  5,
			},
		},
		{
			name: "npc task create",
			path: "/api/v1/npc/tasks/create",
			payload: map[string]any{
				"npc_id":    "scribe",
				"task_type": "digest",
				"payload":   "summarize the queue",
			},
		},
	}

	for _, tc := range cases {
		w := doJSONRequest(t, srv.mux, http.MethodPost, tc.path, tc.payload)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d body=%s", tc.name, w.Code, w.Body.String())
		}
	}
}

func TestRuntimeDashboardAdminWritesAllowLoopback(t *testing.T) {
	srv := newTestServer()
	srv.worldTickMu.Lock()
	srv.worldTickID = 9
	srv.worldTickMu.Unlock()

	w := doJSONRequestWithRemoteAddr(t, srv.mux, http.MethodPost, "/api/v1/runtime/scheduler-settings/upsert", map[string]any{
		"autonomy_reminder_interval_ticks":       60,
		"community_comm_reminder_interval_ticks": 120,
		"kb_enrollment_reminder_interval_ticks":  180,
		"kb_voting_reminder_interval_ticks":      240,
		"cost_alert_notify_cooldown_seconds":     600,
		"low_token_alert_cooldown_seconds":       0,
	}, "127.0.0.1:4321")
	if w.Code != http.StatusAccepted {
		t.Fatalf("loopback scheduler upsert status=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequestWithRemoteAddr(t, srv.mux, http.MethodPost, "/api/v1/world/tick/replay", map[string]any{
		"source_tick_id": 9,
	}, "127.0.0.1:4321")
	if w.Code != http.StatusAccepted {
		t.Fatalf("loopback world tick replay status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRuntimeDashboardAdminWritesIgnoreForwardedLoopbackHeaders(t *testing.T) {
	srv := newTestServer()
	srv.worldTickMu.Lock()
	srv.worldTickID = 11
	srv.worldTickMu.Unlock()

	w := doJSONRequestWithHeadersAndRemoteAddr(t, srv.mux, http.MethodPost, "/api/v1/world/tick/replay", map[string]any{
		"source_tick_id": 11,
	}, map[string]string{
		"X-Forwarded-For": "127.0.0.1",
	}, "203.0.113.10:4321")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("forwarded loopback spoof status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestInternalSyncTokenAllowsTokenConsumeAndNPCTaskCreate(t *testing.T) {
	srv := newTestServer()
	userID := seedActiveUser(t, srv)
	headers := internalSyncHeaders(srv.cfg.InternalSyncToken)

	consume := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/token/consume", map[string]any{
		"user_id": userID,
		"amount":  5,
	}, headers)
	if consume.Code != http.StatusAccepted {
		t.Fatalf("internal token consume status=%d body=%s", consume.Code, consume.Body.String())
	}
	if got := tokenBalanceForUser(t, srv, userID); got != 995 {
		t.Fatalf("user balance after consume=%d want 995", got)
	}

	npc := doJSONRequestWithHeaders(t, srv.mux, http.MethodPost, "/api/v1/npc/tasks/create", map[string]any{
		"npc_id":    "historian",
		"task_type": "digest",
		"payload":   "summarize recent events",
	}, headers)
	if npc.Code != http.StatusAccepted {
		t.Fatalf("internal token npc task create status=%d body=%s", npc.Code, npc.Body.String())
	}
	if !bytes.Contains(npc.Body.Bytes(), []byte(`"npc_id":"historian"`)) {
		t.Fatalf("expected historian npc task in response: %s", npc.Body.String())
	}
}
