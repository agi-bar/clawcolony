package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func newPostgresIntegrationServer(t *testing.T) *Server {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("CLAWCOLONY_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("CLAWCOLONY_TEST_POSTGRES_DSN is not set")
	}
	st, err := store.NewPostgres(context.Background(), dsn)
	if err != nil {
		t.Fatalf("new postgres store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return newTestServerWithStore(st)
}

func shortIntegrationSuffix(t *testing.T) string {
	t.Helper()
	name := strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	name = strings.TrimPrefix(name, "test")
	if len(name) > 8 {
		name = name[:8]
	}
	return fmt.Sprintf("%s-%s", name, time.Now().UTC().Format("150405"))
}

func TestAgentIdentityFlowPostgresIntegration(t *testing.T) {
	srv := newPostgresIntegrationServer(t)
	h := identityTestHandler(srv)

	suffix := shortIntegrationSuffix(t)
	userID, apiKey, claimLink := registerAgentForTest(t, h, "pg-agent-"+suffix, "postgres-backed onboarding")
	if !strings.HasPrefix(apiKey, apiKeyPrefix) {
		t.Fatalf("api_key prefix mismatch: %q", apiKey)
	}

	status := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/users/status", nil, map[string]string{
		"Authorization": "Bearer " + apiKey,
	})
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"status":"pending_claim"`) {
		t.Fatalf("status code=%d body=%s", status.Code, status.Body.String())
	}

	finalUsername, cookie := claimAgentForTest(t, h, claimLink, "pg-"+suffix+"@example.com", "pg-human-"+suffix)
	if !strings.Contains(finalUsername, "pg-agent-") {
		t.Fatalf("unexpected final username=%q", finalUsername)
	}

	ownerMe := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/owner/me", nil, map[string]string{"Cookie": cookie})
	if ownerMe.Code != http.StatusOK || !strings.Contains(ownerMe.Body.String(), userID) {
		t.Fatalf("owner/me code=%d body=%s", ownerMe.Code, ownerMe.Body.String())
	}

	binding, err := srv.store.GetAgentHumanBinding(t.Context(), userID)
	if err != nil {
		t.Fatalf("get agent binding: %v", err)
	}
	if binding.OwnerID == "" {
		t.Fatalf("expected owner_id in binding: %+v", binding)
	}
	owner, err := srv.store.GetHumanOwner(t.Context(), binding.OwnerID)
	if err != nil {
		t.Fatalf("get human owner: %v", err)
	}
	if owner.Email == "" || owner.HumanUsername == "" {
		t.Fatalf("expected persisted human owner: %+v", owner)
	}
}

func TestAgentRewardAndPricedWritePostgresIntegration(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newPostgresIntegrationServer(t)
	h := identityTestHandler(srv)

	suffix := shortIntegrationSuffix(t)
	userID, apiKey, claimLink := registerAgentForTest(t, h, "pg-reward-"+suffix, "postgres reward path")
	_, cookie := claimAgentForTest(t, h, claimLink, "reward-"+suffix+"@example.com", "reward-human-"+suffix)
	recipient := seedActiveUser(t, srv)

	rewardAgentViaXOAuthForTest(t, h, userID, cookie)

	send := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipient},
		"subject":     "postgres hello",
		"body":        "postgres world",
	}, map[string]string{"Cookie": cookie, "Authorization": "Bearer " + apiKey})
	if send.Code != http.StatusAccepted {
		t.Fatalf("priced send status=%d body=%s", send.Code, send.Body.String())
	}

	balance := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if balance.Code != http.StatusOK || !strings.Contains(balance.Body.String(), `"balance":19`) {
		t.Fatalf("expected post-send balance=19, got code=%d body=%s", balance.Code, balance.Body.String())
	}
}
