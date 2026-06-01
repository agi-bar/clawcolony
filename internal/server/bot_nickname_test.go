package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"clawcolony/internal/store"
)

func TestNormalizeBotNickname(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "trim and keep", input: "  小虎  ", want: "小虎"},
		{name: "empty allowed", input: "   ", want: ""},
		{name: "max rune length", input: strings.Repeat("中", maxBotNicknameRunes), want: strings.Repeat("中", maxBotNicknameRunes)},
		{name: "reject over limit", input: strings.Repeat("中", maxBotNicknameRunes+1), wantErr: true},
		{name: "reject multiline", input: "abc\nxyz", wantErr: true},
		{name: "reject carriage return", input: "abc\rxyz", wantErr: true},
		{name: "reject tab", input: "abc\txyz", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeBotNickname(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("nickname mismatch: got=%q want=%q", got, tc.want)
			}
		})
	}
}

func TestBotNicknameUpsertLifecycle(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)
	userID, apiKey := seedActiveUserWithAPIKey(t, srv)

	w := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/bots/nickname/upsert", map[string]any{
		"nickname": "  星火  ",
	}, apiKeyHeaders(apiKey))
	if w.Code != http.StatusOK {
		t.Fatalf("upsert nickname failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var upsertResp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &upsertResp); err != nil {
		t.Fatalf("decode upsert response: %v", err)
	}
	item, _ := upsertResp["item"].(map[string]any)
	if got := strings.TrimSpace(item["nickname"].(string)); got != "星火" {
		t.Fatalf("unexpected nickname after upsert: %q", got)
	}

	list := doJSONRequest(t, h, http.MethodGet, "/api/v1/bots?include_inactive=1", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list bots failed: code=%d body=%s", list.Code, list.Body.String())
	}
	var listResp map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	items, _ := listResp["items"].([]any)
	found := false
	for _, raw := range items {
		bot, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if bot["user_id"] != userID {
			continue
		}
		found = true
		if bot["nickname"] != "星火" {
			t.Fatalf("nickname not persisted in list: got=%v", bot["nickname"])
		}
	}
	if !found {
		t.Fatalf("target user %s not found in /api/v1/bots response", userID)
	}

	clear := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/bots/nickname/upsert", map[string]any{
		"nickname": "   ",
	}, apiKeyHeaders(apiKey))
	if clear.Code != http.StatusOK {
		t.Fatalf("clear nickname failed: code=%d body=%s", clear.Code, clear.Body.String())
	}
	var clearResp map[string]any
	if err := json.Unmarshal(clear.Body.Bytes(), &clearResp); err != nil {
		t.Fatalf("decode clear response: %v", err)
	}
	clearedItem, _ := clearResp["item"].(map[string]any)
	if got := strings.TrimSpace(clearedItem["nickname"].(string)); got != "" {
		t.Fatalf("nickname should be cleared, got=%q", got)
	}
}

func TestBotNicknameUpsertValidation(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)
	_, apiKey := seedActiveUserWithAPIKey(t, srv)

	w := doJSONRequest(t, h, http.MethodGet, "/api/v1/bots/nickname/upsert", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected method not allowed, got=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequest(t, h, http.MethodPost, "/api/v1/bots/nickname/upsert", map[string]any{
		"nickname": "合法",
	})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without api_key, got=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/bots/nickname/upsert", map[string]any{
		"nickname": strings.Repeat("中", maxBotNicknameRunes+1),
	}, apiKeyHeaders(apiKey))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for long nickname, got=%d body=%s", w.Code, w.Body.String())
	}

	w = doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/bots/nickname/upsert", map[string]any{
		"nickname": "abc\nxyz",
	}, apiKeyHeaders(apiKey))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for multiline nickname, got=%d body=%s", w.Code, w.Body.String())
	}

	unknownAPIKey := apiKeyPrefix + "u-not-found-test"
	if _, err := srv.store.CreateAgentRegistration(context.Background(), store.AgentRegistrationInput{
		UserID:            "u-not-found",
		RequestedUsername: "u-not-found",
		GoodAt:            "test",
		Status:            "active",
		APIKeyHash:        hashSecret(unknownAPIKey),
	}); err != nil {
		t.Fatalf("seed unknown nickname registration failed: %v", err)
	}
	w = doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/bots/nickname/upsert", map[string]any{
		"nickname": "正常",
	}, apiKeyHeaders(unknownAPIKey))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected not found for unknown api_key user, got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBotNicknameUpsertUnknownUserDoesNotCreateRuntimeUser(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)
	apiKey := apiKeyPrefix + "user-unknown-no-create-test"
	if _, err := srv.store.CreateAgentRegistration(context.Background(), store.AgentRegistrationInput{
		UserID:            "user-unknown-no-create",
		RequestedUsername: "user-unknown-no-create",
		GoodAt:            "test",
		Status:            "active",
		APIKeyHash:        hashSecret(apiKey),
	}); err != nil {
		t.Fatalf("seed unknown nickname registration failed: %v", err)
	}

	before, err := srv.store.ListBots(context.Background())
	if err != nil {
		t.Fatalf("list bots before nickname upsert: %v", err)
	}

	w := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/bots/nickname/upsert", map[string]any{
		"nickname": "不存在",
	}, apiKeyHeaders(apiKey))
	// newTestServer has no kube client, so unknown user follows the not-found path.
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected not found for unknown user, got=%d body=%s", w.Code, w.Body.String())
	}

	after, err := srv.store.ListBots(context.Background())
	if err != nil {
		t.Fatalf("list bots after nickname upsert: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("nickname upsert should not create runtime user, before=%d after=%d", len(before), len(after))
	}
}

func TestBotNicknamePreservedWhenUpsertWithoutNickname(t *testing.T) {
	srv := newTestServer()
	userID := seedActiveUser(t, srv)
	nickname := "火花"

	_, err := srv.store.UpsertBot(context.Background(), store.BotUpsertInput{
		BotID:       userID,
		Name:        userID,
		Nickname:    &nickname,
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	})
	if err != nil {
		t.Fatalf("seed nickname failed: %v", err)
	}

	item, err := srv.store.UpsertBot(context.Background(), store.BotUpsertInput{
		BotID:       userID,
		Name:        userID + "-renamed",
		Provider:    "openclaw",
		Status:      "running",
		Initialized: true,
	})
	if err != nil {
		t.Fatalf("upsert without nickname failed: %v", err)
	}
	if got := strings.TrimSpace(item.Nickname); got != nickname {
		t.Fatalf("nickname should be preserved, got=%q want=%q", got, nickname)
	}
}
