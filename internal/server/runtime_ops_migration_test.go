package server

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"clawcolony/internal/store"
)

func TestRuntimeRemovedEndpointsReturn404(t *testing.T) {
	srv := newTestServer()

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/prompts/templates"},
		{http.MethodPut, "/api/v1/prompts/templates/upsert"},
		{http.MethodPost, "/api/v1/prompts/templates/apply"},
		{http.MethodGet, "/api/v1/bots/logs"},
		{http.MethodGet, "/api/v1/bots/logs/all"},
		{http.MethodGet, "/api/v1/bots/rule-status"},
		{http.MethodPost, "/api/v1/bots/dev/link"},
		{http.MethodGet, "/api/v1/bots/dev/health"},
		{http.MethodGet, "/api/v1/bots/openclaw/status"},
		{http.MethodGet, "/api/v1/system/openclaw-dashboard-config"},
		{http.MethodPost, "/api/v1/chat/send"},
		{http.MethodGet, "/api/v1/chat/history"},
		{http.MethodGet, "/api/v1/chat/stream"},
		{http.MethodGet, "/api/v1/chat/state"},
		{http.MethodGet, "/api/v1/bots/profile/readme"},
	}
	for _, tc := range cases {
		w := doJSONRequest(t, srv.mux, tc.method, tc.path, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s expected 404 got=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestRuntimeRemovedPrefixEndpointsReturn404(t *testing.T) {
	srv := newTestServer()

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/bots/dev/u1/p/3000/"},
		{http.MethodHead, "/api/v1/bots/dev/u1/p/3000/"},
		{http.MethodOptions, "/api/v1/bots/dev/u1/p/3000/"},
		{http.MethodGet, "/api/v1/bots/openclaw/u1/"},
	}
	for _, tc := range cases {
		w := doJSONRequest(t, srv.mux, tc.method, tc.path, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s expected 404 got=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestRuntimeIdentityEndpointsStillAvailable(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)
	apiKey := apiKeyPrefix + "u-test-runtime-identity"
	if _, err := srv.store.CreateAgentRegistration(context.Background(), store.AgentRegistrationInput{
		UserID:            "u-test",
		RequestedUsername: "u-test",
		GoodAt:            "test",
		Status:            "active",
		APIKeyHash:        hashSecret(apiKey),
	}); err != nil {
		t.Fatalf("seed runtime identity registration: %v", err)
	}

	list := doJSONRequest(t, h, http.MethodGet, "/api/v1/bots?include_inactive=1", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/bots expected 200 got=%d body=%s", list.Code, list.Body.String())
	}

	nick := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/bots/nickname/upsert", map[string]any{
		"nickname": "Nick",
	}, apiKeyHeaders(apiKey))
	if nick.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v1/bots/nickname/upsert expected 404(bot not found) got=%d body=%s", nick.Code, nick.Body.String())
	}
}

func TestRuntimeBotsListUsesDBStatusFilter(t *testing.T) {
	srv := newTestServer()

	_, _ = srv.store.UpsertBot(context.Background(), store.BotUpsertInput{
		BotID:       "u-active",
		Name:        "u-active",
		Provider:    "runtime",
		Status:      "running",
		Initialized: true,
	})
	_, _ = srv.store.UpsertBot(context.Background(), store.BotUpsertInput{
		BotID:       "u-deleted",
		Name:        "u-deleted",
		Provider:    "runtime",
		Status:      "deleted",
		Initialized: false,
	})

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/bots?include_inactive=0", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/bots expected 200 got=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "u-active") {
		t.Fatalf("expected active bot in response: %s", body)
	}
	if strings.Contains(body, "u-deleted") {
		t.Fatalf("deleted bot should be filtered from include_inactive=0: %s", body)
	}
}
