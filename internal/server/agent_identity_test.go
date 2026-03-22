package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"clawcolony/internal/store"
)

func identityTestHandler(srv *Server) http.Handler {
	return srv.wrappedHTTPHandler()
}

func identityAPIKeyTestHandler(srv *Server) http.Handler {
	return srv.apiKeyAuthMiddleware(srv.ownerAndPricingMiddleware(srv.mux))
}

func parseJSONBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return payload
}

func balanceFromResponse(t *testing.T, w *httptest.ResponseRecorder) int64 {
	t.Helper()
	body := parseJSONBody(t, w)
	item, ok := body["item"].(map[string]any)
	if !ok {
		t.Fatalf("balance response missing item: %s", w.Body.String())
	}
	balance, ok := item["balance"].(float64)
	if !ok {
		t.Fatalf("balance response missing numeric balance: %s", w.Body.String())
	}
	return int64(balance)
}

func registerAgentForTest(t *testing.T, h http.Handler, username, goodAt string) (string, string, string) {
	t.Helper()
	w := doJSONRequest(t, h, http.MethodPost, "/api/v1/users/register", map[string]any{
		"username": username,
		"good_at":  goodAt,
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("register status=%d body=%s", w.Code, w.Body.String())
	}
	body := parseJSONBody(t, w)
	setup, ok := body["setup"].(map[string]any)
	if !ok {
		t.Fatalf("register response missing setup: %s", w.Body.String())
	}
	if got := setup["step_1"]; got != "Save your api_key to ~/.config/clawcolony/credentials.json now. It will not be shown again." {
		t.Fatalf("unexpected setup.step_1=%v", got)
	}
	return body["user_id"].(string), body["api_key"].(string), body["claim_link"].(string)
}

func claimAgentForTest(t *testing.T, h http.Handler, claimLink, email, humanName string) (string, string) {
	t.Helper()
	claimToken := claimTokenFromLink(t, claimLink)
	requestMagic := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/request-magic-link", map[string]any{
		"claim_token":           claimToken,
		"email":                 email,
		"human_username":        humanName,
		"human_name_visibility": "public",
	})
	if requestMagic.Code != http.StatusAccepted {
		t.Fatalf("magic link status=%d body=%s", requestMagic.Code, requestMagic.Body.String())
	}
	requestBody := parseJSONBody(t, requestMagic)
	magicLink := requestBody["magic_link"].(string)
	magicURL, err := neturl.Parse(magicLink)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	magicToken := magicURL.Query().Get("magic_token")
	complete := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/complete", map[string]any{
		"magic_token": magicToken,
	})
	if complete.Code != http.StatusOK {
		t.Fatalf("claim complete status=%d body=%s", complete.Code, complete.Body.String())
	}
	resp := parseJSONBody(t, complete)
	cookies := complete.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatalf("expected owner session cookie")
	}
	return resp["username"].(string), cookies[0].Name + "=" + cookies[0].Value
}

func claimTokenFromLink(t *testing.T, claimLink string) string {
	t.Helper()
	u, err := neturl.Parse(claimLink)
	if err != nil {
		t.Fatalf("parse claim link: %v", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) == 0 {
		t.Fatalf("claim link path missing token: %q", claimLink)
	}
	return parts[len(parts)-1]
}

func joinCookieHeader(base string, cookies []*http.Cookie) string {
	parts := make([]string, 0, len(cookies)+1)
	if strings.TrimSpace(base) != "" {
		parts = append(parts, strings.TrimSpace(base))
	}
	for _, c := range cookies {
		if c == nil || strings.TrimSpace(c.Name) == "" {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

func enableXOAuthForTest(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/oauth2/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse x token form: %v", err)
			}
			if got := r.Form.Get("grant_type"); got != "authorization_code" {
				t.Fatalf("unexpected x grant_type=%q", got)
			}
			if strings.TrimSpace(r.Form.Get("code_verifier")) == "" {
				t.Fatalf("expected x code_verifier")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"x-access-token","token_type":"bearer","scope":"users.read"}`))
		case r.URL.Path == "/2/users/me":
			if got := r.Header.Get("Authorization"); got != "Bearer x-access-token" {
				t.Fatalf("unexpected x auth header=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"x-user-1","name":"Orbit Agent","username":"orbit_agent"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("CLAWCOLONY_X_OAUTH_CLIENT_ID", "x-client")
	t.Setenv("CLAWCOLONY_X_OAUTH_CLIENT_SECRET", "x-secret")
	t.Setenv("CLAWCOLONY_X_OAUTH_AUTHORIZE_URL", srv.URL+"/oauth2/authorize")
	t.Setenv("CLAWCOLONY_X_OAUTH_TOKEN_URL", srv.URL+"/oauth2/token")
	t.Setenv("CLAWCOLONY_X_OAUTH_USERINFO_URL", srv.URL+"/2/users/me")
	return srv
}

func enableGitHubOAuthForTestWithEmails(t *testing.T, starred, forked bool, emails []githubEmailRecord) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login/oauth/access_token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("parse github token form: %v", err)
			}
			if strings.TrimSpace(r.Form.Get("code_verifier")) == "" {
				t.Fatalf("expected github code_verifier")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"gh-access-token","token_type":"bearer","scope":"read:user"}`))
		case r.URL.Path == "/user":
			if got := r.Header.Get("Authorization"); got != "Bearer gh-access-token" {
				t.Fatalf("unexpected github auth header=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":42,"login":"octo","name":"Octo Human"}`))
		case r.URL.Path == "/user/emails":
			if got := r.Header.Get("Authorization"); got != "Bearer gh-access-token" {
				t.Fatalf("unexpected github emails auth header=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(emails); err != nil {
				t.Fatalf("encode github emails: %v", err)
			}
		case r.URL.Path == "/users/octo/starred":
			if starred {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`[{"full_name":"agi-bar/clawcolony"}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/users/octo/repos":
			w.Header().Set("Content-Type", "application/json")
			if forked {
				_, _ = w.Write([]byte(`[{"full_name":"octo/clawcolony","fork":true,"parent":{"full_name":"agi-bar/clawcolony"}}]`))
				return
			}
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_CLIENT_ID", "gh-client")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_CLIENT_SECRET", "gh-secret")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_AUTHORIZE_URL", srv.URL+"/login/oauth/authorize")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_TOKEN_URL", srv.URL+"/login/oauth/access_token")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_USERINFO_URL", srv.URL+"/user")
	t.Setenv("CLAWCOLONY_GITHUB_API_BASE_URL", srv.URL)
	t.Setenv("CLAWCOLONY_OFFICIAL_GITHUB_REPO", "agi-bar/clawcolony")
	return srv
}

func enableGitHubOAuthForTest(t *testing.T, starred, forked bool) *httptest.Server {
	t.Helper()
	return enableGitHubOAuthForTestWithEmails(t, starred, forked, []githubEmailRecord{
		{Email: "octo@example.com", Primary: true, Verified: true},
	})
}

func completeSocialOAuthCallbackForTest(t *testing.T, h http.Handler, start *httptest.ResponseRecorder, ownerCookie, provider, code string) *httptest.ResponseRecorder {
	t.Helper()
	body := parseJSONBody(t, start)
	rawAuthorizeURL, _ := body["authorize_url"].(string)
	if strings.TrimSpace(rawAuthorizeURL) == "" {
		t.Fatalf("missing authorize_url in start response: %s", start.Body.String())
	}
	authURL, err := neturl.Parse(rawAuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	state := authURL.Query().Get("state")
	callbackPath := "/auth/" + provider + "/callback?code=" + neturl.QueryEscape(code) + "&state=" + neturl.QueryEscape(state) + "&format=json"
	req := httptest.NewRequest(http.MethodGet, callbackPath, nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", joinCookieHeader(ownerCookie, start.Result().Cookies()))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func rewardAgentViaXOAuthForTest(t *testing.T, h http.Handler, userID, ownerCookie string) {
	t.Helper()
	start := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/x/connect/start", map[string]any{
		"user_id": userID,
	}, map[string]string{"Cookie": ownerCookie})
	if start.Code != http.StatusAccepted {
		t.Fatalf("x connect start status=%d body=%s", start.Code, start.Body.String())
	}
	callback := completeSocialOAuthCallbackForTest(t, h, start, ownerCookie, "x", "x-code")
	if callback.Code != http.StatusOK {
		t.Fatalf("x callback status=%d body=%s", callback.Code, callback.Body.String())
	}
}

func TestUserRegisterAndStatusFlow(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, apiKey, _ := registerAgentForTest(t, h, "orbit-agent", "routing requests")
	if !strings.HasPrefix(apiKey, apiKeyPrefix) {
		t.Fatalf("api_key prefix mismatch: %q", apiKey)
	}
	reg, err := srv.store.GetAgentRegistrationByAPIKeyHash(t.Context(), hashSecret(apiKey))
	if err != nil {
		t.Fatalf("lookup registration by api key hash: %v", err)
	}
	if reg.UserID != userID {
		t.Fatalf("registration user_id mismatch: got=%s want=%s", reg.UserID, userID)
	}
	if reg.APIKeyHash == apiKey {
		t.Fatalf("api_key must not be stored in plaintext")
	}

	status := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/users/status", nil, map[string]string{
		"Authorization": "Bearer " + apiKey,
	})
	if status.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", status.Code, status.Body.String())
	}
	statusBody := parseJSONBody(t, status)
	if got := statusBody["status"]; got != "pending_claim" {
		t.Fatalf("expected pending_claim, got=%v", got)
	}

	list := doJSONRequest(t, h, http.MethodGet, "/api/v1/bots?include_inactive=0", nil)
	if strings.Contains(list.Body.String(), userID) {
		t.Fatalf("pending agent must not appear in active list: %s", list.Body.String())
	}
}

func TestClaimFlowActivatesAgentAndAutoSuffixesConflicts(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	firstID, _, firstClaim := registerAgentForTest(t, h, "same-agent", "first")
	secondID, _, secondClaim := registerAgentForTest(t, h, "same-agent", "second")

	firstUsername, _ := claimAgentForTest(t, h, firstClaim, "one@example.com", "buddy-one")
	if firstUsername != "same-agent" {
		t.Fatalf("expected original username on first claim, got=%q", firstUsername)
	}
	secondUsername, _ := claimAgentForTest(t, h, secondClaim, "two@example.com", "buddy-two")
	if secondUsername == "same-agent" || !strings.HasPrefix(secondUsername, "same-agent-") {
		t.Fatalf("expected suffixed username, got=%q", secondUsername)
	}

	firstBot, err := srv.store.GetBot(t.Context(), firstID)
	if err != nil {
		t.Fatalf("get first bot: %v", err)
	}
	if firstBot.Status != "running" || !firstBot.Initialized {
		t.Fatalf("first bot should be active after claim: %+v", firstBot)
	}
	secondBot, err := srv.store.GetBot(t.Context(), secondID)
	if err != nil {
		t.Fatalf("get second bot: %v", err)
	}
	if secondBot.Name != secondUsername {
		t.Fatalf("second bot username mismatch: got=%q want=%q", secondBot.Name, secondUsername)
	}
}

func TestManagedAgentRequiresOwnerSessionAndTokenBalance(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, apiKey, claimLink := registerAgentForTest(t, h, "managed-agent", "mail")
	_, cookie := claimAgentForTest(t, h, claimLink, "managed@example.com", "human-manager")
	recipient := seedActiveUser(t, srv)
	initialBalance := tokenBalanceForUser(t, srv, userID)
	if initialBalance <= 0 {
		t.Fatalf("expected initial token balance after claim, got=%d", initialBalance)
	}
	if _, err := srv.store.Consume(t.Context(), userID, initialBalance); err != nil {
		t.Fatalf("drain claimed balance: %v", err)
	}

	unauth := doJSONRequest(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipient},
		"subject":     "hello",
		"body":        "world",
	})
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without cookie, got=%d body=%s", unauth.Code, unauth.Body.String())
	}

	oversizedBody := strings.Repeat("a", int(srv.cfg.DailyFreeCommUnactivated)+1)
	noFunds := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipient},
		"subject":     "quota-burst",
		"body":        oversizedBody,
	}, map[string]string{"Cookie": cookie, "Authorization": "Bearer " + apiKey})
	if noFunds.Code != http.StatusPaymentRequired {
		t.Fatalf("expected payment required, got=%d body=%s", noFunds.Code, noFunds.Body.String())
	}

	if _, err := srv.store.Recharge(t.Context(), userID, 1000); err != nil {
		t.Fatalf("recharge user for overage send: %v", err)
	}

	balance := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if balance.Code != http.StatusOK {
		t.Fatalf("expected rewarded balance read, got code=%d body=%s", balance.Code, balance.Body.String())
	}
	rewardedBalance := balanceFromResponse(t, balance)
	if rewardedBalance != 1000 {
		t.Fatalf("expected recharged balance=1000, got=%d body=%s", rewardedBalance, balance.Body.String())
	}

	send := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipient},
		"subject":     "quota-burst",
		"body":        oversizedBody,
	}, map[string]string{"Cookie": cookie, "Authorization": "Bearer " + apiKey})
	if send.Code != http.StatusAccepted {
		t.Fatalf("expected accepted send after reward, got=%d body=%s", send.Code, send.Body.String())
	}
	after := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if after.Code != http.StatusOK {
		t.Fatalf("expected balance read after priced send, got code=%d body=%s", after.Code, after.Body.String())
	}
	if afterBalance := balanceFromResponse(t, after); afterBalance >= rewardedBalance {
		t.Fatalf("expected priced send to reduce balance, before=%d after=%d body=%s", rewardedBalance, afterBalance, after.Body.String())
	}

	ownerMe := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/owner/me", nil, map[string]string{"Cookie": cookie})
	if ownerMe.Code != http.StatusOK || !strings.Contains(ownerMe.Body.String(), `"human_username":"human-manager"`) {
		t.Fatalf("expected owner session to remain valid, got code=%d body=%s", ownerMe.Code, ownerMe.Body.String())
	}
}

func TestManagedAgentCanUseAPIKeyForPricedWrite(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	srv.cfg.RegistrationGrantToken = 0
	h := identityAPIKeyTestHandler(srv)

	userID, apiKey, claimLink := registerAgentForTest(t, h, "managed-apikey-agent", "mail")
	_, cookie := claimAgentForTest(t, h, claimLink, "managed-apikey@example.com", "human-manager-apikey")
	recipient := seedActiveUser(t, srv)

	rewardAgentViaXOAuthForTest(t, h, userID, cookie)

	before := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if before.Code != http.StatusOK {
		t.Fatalf("expected balance read before send, got=%d body=%s", before.Code, before.Body.String())
	}
	beforeBalance := balanceFromResponse(t, before)

	send := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipient},
		"subject":     "hello",
		"body":        "world",
	}, map[string]string{"Authorization": "Bearer " + apiKey})
	if send.Code != http.StatusAccepted {
		t.Fatalf("expected accepted send with api key, got=%d body=%s", send.Code, send.Body.String())
	}

	after := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if after.Code != http.StatusOK {
		t.Fatalf("expected balance read after send, got code=%d body=%s", after.Code, after.Body.String())
	}
	afterBalance := balanceFromResponse(t, after)
	if afterBalance != beforeBalance {
		t.Fatalf("expected v2 mail send within free quota to preserve balance, before=%d after=%d body=%s", beforeBalance, afterBalance, after.Body.String())
	}
}

func TestTokenBalanceAllowsPublicUserIDQueryWithoutAPIKey(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, _, claimLink := registerAgentForTest(t, h, "frontend-balance-agent", "mail")
	_, cookie := claimAgentForTest(t, h, claimLink, "frontend-balance@example.com", "frontend-human")

	rewardAgentViaXOAuthForTest(t, h, userID, cookie)

	balance := doJSONRequest(t, h, http.MethodGet, "/api/v1/token/balance?user_id="+neturl.QueryEscape(userID), nil)
	if balance.Code != http.StatusOK {
		t.Fatalf("expected token balance read by user_id query, got code=%d body=%s", balance.Code, balance.Body.String())
	}
	if got := balanceFromResponse(t, balance); got <= 0 {
		t.Fatalf("expected positive rewarded balance, got=%d body=%s", got, balance.Body.String())
	}
}

func TestTokenBalanceWithoutUserIDStillRequiresAuthentication(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	balance := doJSONRequest(t, h, http.MethodGet, "/api/v1/token/balance", nil)
	if balance.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized token balance read without user_id or api_key, got code=%d body=%s", balance.Code, balance.Body.String())
	}
}

func TestClaimRequestMagicLinkRejectsExpiredClaimToken(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	expiredAt := time.Now().UTC().Add(-time.Minute)
	if _, err := srv.store.UpsertBot(t.Context(), store.BotUpsertInput{
		BotID:       "expired-claim-agent",
		Name:        "expired-agent",
		Provider:    "agent",
		Status:      "inactive",
		Initialized: false,
	}); err != nil {
		t.Fatalf("seed bot: %v", err)
	}
	if _, err := srv.store.CreateAgentRegistration(t.Context(), store.AgentRegistrationInput{
		UserID:              "expired-claim-agent",
		RequestedUsername:   "expired-agent",
		GoodAt:              "timing",
		Status:              "pending_claim",
		ClaimTokenHash:      hashSecret("expired-claim-token"),
		ClaimTokenExpiresAt: &expiredAt,
		APIKeyHash:          hashSecret("clawcolony-expired"),
	}); err != nil {
		t.Fatalf("seed registration: %v", err)
	}
	if _, err := srv.store.UpsertAgentProfile(t.Context(), store.AgentProfile{
		UserID:   "expired-claim-agent",
		Username: "expired-agent",
		GoodAt:   "timing",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	w := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/request-magic-link", map[string]any{
		"claim_token":           "expired-claim-token",
		"email":                 "buddy@example.com",
		"human_username":        "buddy",
		"human_name_visibility": "public",
	})
	if w.Code != http.StatusGone {
		t.Fatalf("expected claim token expired, got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestClaimCompleteRejectsExpiredMagicToken(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, _, _ := registerAgentForTest(t, h, "magic-expired-agent", "timing")
	if _, err := srv.store.UpdateAgentRegistrationClaim(
		t.Context(),
		userID,
		"buddy@example.com",
		"buddy",
		"public",
		hashSecret("expired-magic-token"),
		time.Now().UTC().Add(-time.Minute),
	); err != nil {
		t.Fatalf("seed expired magic token: %v", err)
	}

	w := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/complete", map[string]any{
		"magic_token": "expired-magic-token",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected magic token expired, got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGitHubVerifyUsesServerSideVerificationAndRewards(t *testing.T) {
	gh := enableGitHubOAuthForTest(t, true, true)
	defer gh.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, apiKey, claimLink := registerAgentForTest(t, h, "github-agent", "oss")
	_, cookie := claimAgentForTest(t, h, claimLink, "github@example.com", "octo-human")

	start := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/github/connect/start", map[string]any{
		"user_id": userID,
	}, map[string]string{"Cookie": cookie})
	if start.Code != http.StatusAccepted {
		t.Fatalf("github connect start status=%d body=%s", start.Code, start.Body.String())
	}

	callback := completeSocialOAuthCallbackForTest(t, h, start, cookie, "github", "gh-code")
	if callback.Code != http.StatusOK {
		t.Fatalf("github callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	body := parseJSONBody(t, callback)
	if body["starred"] != true || body["forked"] != true {
		t.Fatalf("expected oauth github verification, got body=%s", callback.Body.String())
	}

	balance := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if balance.Code != http.StatusOK {
		t.Fatalf("expected rewarded balance read, got code=%d body=%s", balance.Code, balance.Body.String())
	}
	expectedBalance := srv.tokenPolicy().InitialToken + 50000 + 500000 + 200000
	if got := balanceFromResponse(t, balance); got != expectedBalance {
		t.Fatalf("expected rewarded balance=%d, got=%d body=%s", expectedBalance, got, balance.Body.String())
	}

	ownerMe := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/owner/me", nil, map[string]string{"Cookie": cookie})
	if ownerMe.Code != http.StatusOK || !strings.Contains(ownerMe.Body.String(), `"github_username":"octo"`) {
		t.Fatalf("expected owner github identity binding, got code=%d body=%s", ownerMe.Code, ownerMe.Body.String())
	}
}

func TestGitHubConnectStartUsesLeastPrivilegeScope(t *testing.T) {
	gh := enableGitHubOAuthForTest(t, false, false)
	defer gh.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, _, claimLink := registerAgentForTest(t, h, "github-scope-agent", "oss")
	_, cookie := claimAgentForTest(t, h, claimLink, "github-scope@example.com", "octo-human")

	start := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/github/connect/start", map[string]any{
		"user_id": userID,
	}, map[string]string{"Cookie": cookie})
	if start.Code != http.StatusAccepted {
		t.Fatalf("github connect start status=%d body=%s", start.Code, start.Body.String())
	}

	body := parseJSONBody(t, start)
	rawAuthorizeURL, _ := body["authorize_url"].(string)
	if strings.TrimSpace(rawAuthorizeURL) == "" {
		t.Fatalf("missing authorize_url in start response: %s", start.Body.String())
	}
	authorizeURL, err := neturl.Parse(rawAuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	if got := authorizeURL.Query().Get("scope"); got != "read:user" {
		t.Fatalf("expected least-privilege github scope, got=%q", got)
	}
}

func TestManualSocialVerifyEndpointsRejectWhenOAuthIsConfigured(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()
	ghOAuth := enableGitHubOAuthForTest(t, true, true)
	defer ghOAuth.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, _, claimLink := registerAgentForTest(t, h, "manual-disabled-agent", "oss")
	_, cookie := claimAgentForTest(t, h, claimLink, "manual-disabled@example.com", "manual-disabled-human")

	xVerifyBeforeAuth := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/x/verify", map[string]any{
		"user_id":   userID,
		"post_text": "hello " + defaultOfficialXHandle,
	}, map[string]string{"Cookie": cookie})
	if xVerifyBeforeAuth.Code != http.StatusNotFound {
		t.Fatalf("expected x verify to require oauth identity first, got=%d body=%s", xVerifyBeforeAuth.Code, xVerifyBeforeAuth.Body.String())
	}

	xVerify := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/x/verify", map[string]any{
		"user_id":   userID,
		"post_text": "hello " + defaultOfficialXHandle,
	}, map[string]string{"Cookie": cookie})
	if xVerify.Code != http.StatusNotFound {
		t.Fatalf("expected x verify to require oauth identity binding, got=%d body=%s", xVerify.Code, xVerify.Body.String())
	}

	ghVerify := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/github/verify", map[string]any{
		"user_id": userID,
	}, map[string]string{"Cookie": cookie})
	if ghVerify.Code != http.StatusConflict {
		t.Fatalf("expected github manual verify conflict, got=%d body=%s", ghVerify.Code, ghVerify.Body.String())
	}
}

func TestXMentionVerifyDoesNotMintTokenRewardsInV2(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, apiKey, claimLink := registerAgentForTest(t, h, "mention-agent", "oss")
	_, cookie := claimAgentForTest(t, h, claimLink, "mention@example.com", "mention-human")
	rewardAgentViaXOAuthForTest(t, h, userID, cookie)

	mention := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/x/verify", map[string]any{
		"user_id":   userID,
		"post_text": "hello " + defaultOfficialXHandle + " from orbit",
	}, map[string]string{"Cookie": cookie})
	if mention.Code != http.StatusOK {
		t.Fatalf("expected x mention verify ok, got=%d body=%s", mention.Code, mention.Body.String())
	}
	if strings.Contains(mention.Body.String(), `"amount":3`) || strings.Contains(mention.Body.String(), `"granted":true`) {
		t.Fatalf("x mention verify should not mint token rewards under v2: %s", mention.Body.String())
	}

	status := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/social/rewards/status", nil, map[string]string{"Cookie": cookie, "Authorization": "Bearer " + apiKey})
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), `"reward_type":"mention"`) {
		t.Fatalf("expected no mention reward in status under v2, got=%d body=%s", status.Code, status.Body.String())
	}
}

func TestSocialRewardAmountsAreConfigurable(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()
	t.Setenv("CLAWCOLONY_SOCIAL_REWARD_X_AUTH", "7")
	t.Setenv("CLAWCOLONY_SOCIAL_REWARD_X_MENTION", "3")
	t.Setenv("CLAWCOLONY_SOCIAL_REWARD_GITHUB_AUTH", "11")
	t.Setenv("CLAWCOLONY_SOCIAL_REWARD_GITHUB_STAR", "13")
	t.Setenv("CLAWCOLONY_SOCIAL_REWARD_GITHUB_FORK", "17")

	srv := newTestServer()
	h := identityTestHandler(srv)

	policy := doJSONRequest(t, h, http.MethodGet, "/api/v1/social/policy", nil)
	if policy.Code != http.StatusOK {
		t.Fatalf("social policy status=%d body=%s", policy.Code, policy.Body.String())
	}
	body := policy.Body.String()
	for _, needle := range []string{`"reward_auth_amount":7`, `"reward_mention_amount":3`, `"reward_star_amount":13`, `"reward_fork_amount":17`} {
		if !strings.Contains(body, needle) {
			t.Fatalf("expected configurable reward amount %s in policy, got=%s", needle, body)
		}
	}
}

func TestOAuthCallbackRejectsTamperedState(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, _, claimLink := registerAgentForTest(t, h, "tampered-oauth-agent", "oss")
	_, cookie := claimAgentForTest(t, h, claimLink, "tampered@example.com", "tampered-human")

	start := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/x/connect/start", map[string]any{
		"user_id": userID,
	}, map[string]string{"Cookie": cookie})
	if start.Code != http.StatusAccepted {
		t.Fatalf("x connect start status=%d body=%s", start.Code, start.Body.String())
	}
	body := parseJSONBody(t, start)
	authURL, err := neturl.Parse(body["authorize_url"].(string))
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	state := authURL.Query().Get("state") + "tampered"
	req := httptest.NewRequest(http.MethodGet, "/auth/x/callback?code=x-code&state="+neturl.QueryEscape(state)+"&format=json", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", joinCookieHeader(cookie, start.Result().Cookies()))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for tampered state, got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestSocialRewardsStatusRequiresOwnerAndHidesChallenge(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, apiKey, claimLink := registerAgentForTest(t, h, "status-agent", "mail")
	_, cookie := claimAgentForTest(t, h, claimLink, "status@example.com", "status-human")

	start := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/x/connect/start", map[string]any{
		"user_id": userID,
	}, map[string]string{"Cookie": cookie})
	if start.Code != http.StatusAccepted {
		t.Fatalf("x connect start status=%d body=%s", start.Code, start.Body.String())
	}

	unauth := doJSONRequest(t, h, http.MethodGet, "/api/v1/social/rewards/status", nil)
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized rewards status without owner session, got=%d body=%s", unauth.Code, unauth.Body.String())
	}

	status := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/social/rewards/status", nil, map[string]string{"Cookie": cookie, "Authorization": "Bearer " + apiKey})
	if status.Code != http.StatusOK {
		t.Fatalf("expected rewards status ok, got=%d body=%s", status.Code, status.Body.String())
	}
	if strings.Contains(status.Body.String(), `"challenge"`) {
		t.Fatalf("rewards status must not leak challenge: %s", status.Body.String())
	}
}

func TestOwnerLogoutRevokesSession(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	_, _, claimLink := registerAgentForTest(t, h, "logout-agent", "mail")
	_, cookie := claimAgentForTest(t, h, claimLink, "logout@example.com", "logout-human")
	recipient := seedActiveUser(t, srv)

	logout := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/owner/logout", nil, map[string]string{"Cookie": cookie})
	if logout.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", logout.Code, logout.Body.String())
	}

	send := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipient},
		"subject":     "hello",
		"body":        "world",
	}, map[string]string{"Cookie": cookie})
	if send.Code != http.StatusUnauthorized {
		t.Fatalf("expected revoked session to be rejected, got=%d body=%s", send.Code, send.Body.String())
	}
}

func TestTokenPricingIsSorted(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	w := doJSONRequest(t, h, http.MethodGet, "/api/v1/token/pricing", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("token pricing status=%d body=%s", w.Code, w.Body.String())
	}
	body := parseJSONBody(t, w)
	items, ok := body["items"].([]any)
	if !ok || len(items) == 0 {
		t.Fatalf("expected pricing items, got body=%s", w.Body.String())
	}
	prev := ""
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected item object, got %#v", raw)
		}
		path, _ := item["path"].(string)
		if prev != "" && path < prev {
			t.Fatalf("pricing items should be sorted: prev=%q current=%q", prev, path)
		}
		prev = path
	}
}

func TestTokenPricingV2IncludesOnboardingMintAndUpdatedLifeParameters(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	w := doJSONRequest(t, h, http.MethodGet, "/api/v1/token/pricing", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("token pricing status=%d body=%s", w.Code, w.Body.String())
	}
	body := parseJSONBody(t, w)
	items, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("expected pricing items, got body=%s", w.Body.String())
	}
	byPath := map[string]map[string]any{}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("expected item object, got %#v", raw)
		}
		path, _ := item["path"].(string)
		byPath[path] = item
	}

	register := byPath["/api/v1/users/register"]
	if register["mode"] != "direct_mint" || register["settlement_source"] != onboardingSettlementMint {
		t.Fatalf("unexpected register pricing item=%v", register)
	}
	if got := int64(register["initial_tokens"].(float64)); got != srv.tokenPolicy().InitialToken {
		t.Fatalf("register initial_tokens=%d want %d", got, srv.tokenPolicy().InitialToken)
	}

	github := byPath["/api/v1/claims/github/complete"]
	if github["mode"] != "owner_onboarding_direct_mint" || github["settlement_source"] != onboardingSettlementMint {
		t.Fatalf("unexpected github onboarding pricing item=%v", github)
	}
	if got := int64(github["github_bind_tokens"].(float64)); got != githubBindOnboardingReward {
		t.Fatalf("github bind reward=%d want %d", got, githubBindOnboardingReward)
	}
	if got := int64(github["github_star_tokens"].(float64)); got != githubStarOnboardingReward {
		t.Fatalf("github star reward=%d want %d", got, githubStarOnboardingReward)
	}
	if got := int64(github["github_fork_tokens"].(float64)); got != githubForkOnboardingReward {
		t.Fatalf("github fork reward=%d want %d", got, githubForkOnboardingReward)
	}

	life := byPath["/api/v1/life/tax"]
	if got := int64(life["activated_tokens_per_tick"].(float64)); got != 5 {
		t.Fatalf("activated tax per tick=%d want 5", got)
	}
	if got := int64(life["unactivated_tokens_per_tick"].(float64)); got != 10 {
		t.Fatalf("unactivated tax per tick=%d want 10", got)
	}
	if got := int64(life["hibernation_period_ticks"].(float64)); got != 1440 {
		t.Fatalf("hibernation ticks=%d want 1440", got)
	}
	if got := int64(life["min_revival_balance"].(float64)); got != 50000 {
		t.Fatalf("min revival balance=%d want 50000", got)
	}
}

func TestClaimAlreadyClaimedAgentConflicts(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	_, _, claimLink := registerAgentForTest(t, h, "claimed-agent", "mail")
	_, _ = claimAgentForTest(t, h, claimLink, "claimed@example.com", "claimed-human")

	u, err := neturl.Parse(claimLink)
	if err != nil {
		t.Fatalf("parse claim link: %v", err)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	claimToken := parts[len(parts)-1]

	w := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/request-magic-link", map[string]any{
		"claim_token":           claimToken,
		"email":                 "claimed@example.com",
		"human_username":        "claimed-human",
		"human_name_visibility": "public",
	})
	if w.Code != http.StatusConflict {
		t.Fatalf("expected already claimed conflict, got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAPIKeyWriteIgnoresUnrelatedOwnerSessionCookie(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	userOne, apiKeyOne, claimOne := registerAgentForTest(t, h, "owner-one-agent", "mail")
	_, cookieOne := claimAgentForTest(t, h, claimOne, "owner1@example.com", "owner-one")
	userTwo, _, claimTwo := registerAgentForTest(t, h, "owner-two-agent", "mail")
	_, cookieTwo := claimAgentForTest(t, h, claimTwo, "owner2@example.com", "owner-two")
	recipient := seedActiveUser(t, srv)

	_ = cookieOne
	w := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{recipient},
		"subject":     "unauthorized",
		"body":        "owner mismatch",
	}, map[string]string{"Cookie": cookieTwo, "Authorization": "Bearer " + apiKeyOne})
	if w.Code != http.StatusAccepted {
		t.Fatalf("expected api key write to ignore unrelated owner cookie on user=%s cookie_owner=%s got=%d body=%s", userOne, userTwo, w.Code, w.Body.String())
	}
}

func TestPricedWriteRejectsDeprecatedActorFieldWithAPIKey(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	srv.cfg.RegistrationGrantToken = 0
	h := identityAPIKeyTestHandler(srv)

	userID, apiKey, claimLink := registerAgentForTest(t, h, "deprecated-actor-field", "mail")
	_, cookie := claimAgentForTest(t, h, claimLink, "deprecated@example.com", "deprecated-owner")
	recipient := seedActiveUser(t, srv)

	rewardAgentViaXOAuthForTest(t, h, userID, cookie)

	w := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"from_user_id": userID,
		"to_user_ids":  []string{recipient},
		"subject":      "deprecated actor field",
		"body":         "body actor fields are no longer accepted on public runtime writes",
	}, map[string]string{"Authorization": "Bearer " + apiKey})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected bad request for deprecated actor field, got=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "from_user_id is no longer accepted on this endpoint") {
		t.Fatalf("expected deprecated actor field error body, got=%s", w.Body.String())
	}
}

func TestPricedWriteRefundsOnValidationFailure(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	srv.cfg.RegistrationGrantToken = 0 // disable grant to test refund balances in isolation
	h := identityTestHandler(srv)

	userID, apiKey, claimLink := registerAgentForTest(t, h, "refund-agent", "mail")
	_, cookie := claimAgentForTest(t, h, claimLink, "refund@example.com", "refund-human")

	rewardAgentViaXOAuthForTest(t, h, userID, cookie)

	before := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if before.Code != http.StatusOK {
		t.Fatalf("expected starting balance read, got code=%d body=%s", before.Code, before.Body.String())
	}
	beforeBalance := balanceFromResponse(t, before)

	fail := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/mail/send", map[string]any{
		"to_user_ids": []string{},
		"subject":     "bad request",
		"body":        "should refund",
	}, map[string]string{"Cookie": cookie, "Authorization": "Bearer " + apiKey})
	if fail.Code != http.StatusBadRequest {
		t.Fatalf("expected downstream validation failure, got=%d body=%s", fail.Code, fail.Body.String())
	}

	after := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if after.Code != http.StatusOK {
		t.Fatalf("expected refund balance read, got code=%d body=%s", after.Code, after.Body.String())
	}
	if afterBalance := balanceFromResponse(t, after); afterBalance != beforeBalance {
		t.Fatalf("expected refund to restore balance=%d, got=%d body=%s", beforeBalance, afterBalance, after.Body.String())
	}
}

func TestSocialPolicyEndpointAndConnectRateLimit(t *testing.T) {
	xOAuth := enableXOAuthForTest(t)
	defer xOAuth.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	policy := doJSONRequest(t, h, http.MethodGet, "/api/v1/social/policy", nil)
	if policy.Code != http.StatusOK {
		t.Fatalf("social policy status=%d body=%s", policy.Code, policy.Body.String())
	}
	if !strings.Contains(policy.Body.String(), `"mode":"oauth_callback"`) {
		t.Fatalf("expected oauth callback policy, got=%s", policy.Body.String())
	}

	userID, _, claimLink := registerAgentForTest(t, h, "limited-social-agent", "mail")
	_, cookie := claimAgentForTest(t, h, claimLink, "limited@example.com", "limited-human")

	first := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/x/connect/start", map[string]any{
		"user_id": userID,
	}, map[string]string{"Cookie": cookie})
	if first.Code != http.StatusAccepted {
		t.Fatalf("first x connect start status=%d body=%s", first.Code, first.Body.String())
	}
	second := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/social/x/connect/start", map[string]any{
		"user_id": userID,
	}, map[string]string{"Cookie": cookie})
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("expected connect rate limit, got=%d body=%s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), `"retry_after_seconds"`) {
		t.Fatalf("expected retry_after_seconds in rate limit payload, got=%s", second.Body.String())
	}
}

func TestClaimViewReportsValidExpiredMissingAndClaimedTokens(t *testing.T) {
	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, _, claimLink := registerAgentForTest(t, h, "claim-view-agent", "routing")
	claimToken := claimTokenFromLink(t, claimLink)

	valid := doJSONRequest(t, h, http.MethodGet, "/api/v1/claims/view?claim_token="+neturl.QueryEscape(claimToken), nil)
	if valid.Code != http.StatusOK {
		t.Fatalf("valid claim view status=%d body=%s", valid.Code, valid.Body.String())
	}
	validBody := parseJSONBody(t, valid)
	if validBody["user_id"] != userID || validBody["status"] != "pending_claim" {
		t.Fatalf("unexpected claim view payload=%s", valid.Body.String())
	}
	if validBody["requested_username"] != "claim-view-agent" {
		t.Fatalf("unexpected requested username payload=%s", valid.Body.String())
	}

	missing := doJSONRequest(t, h, http.MethodGet, "/api/v1/claims/view?claim_token=missing-claim-token", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing claim view status=%d body=%s", missing.Code, missing.Body.String())
	}

	expiredAt := time.Now().UTC().Add(-time.Minute)
	if _, err := srv.store.UpsertBot(t.Context(), store.BotUpsertInput{
		BotID:       "expired-claim-view-agent",
		Name:        "expired-claim-view-agent",
		Provider:    "agent",
		Status:      "inactive",
		Initialized: false,
	}); err != nil {
		t.Fatalf("seed expired bot: %v", err)
	}
	if _, err := srv.store.CreateAgentRegistration(t.Context(), store.AgentRegistrationInput{
		UserID:              "expired-claim-view-agent",
		RequestedUsername:   "expired-claim-view-agent",
		GoodAt:              "timing",
		Status:              "pending_claim",
		ClaimTokenHash:      hashSecret("expired-claim-view-token"),
		ClaimTokenExpiresAt: &expiredAt,
		APIKeyHash:          hashSecret("clawcolony-expired-claim-view"),
	}); err != nil {
		t.Fatalf("seed expired registration: %v", err)
	}
	expired := doJSONRequest(t, h, http.MethodGet, "/api/v1/claims/view?claim_token=expired-claim-view-token", nil)
	if expired.Code != http.StatusGone {
		t.Fatalf("expired claim view status=%d body=%s", expired.Code, expired.Body.String())
	}

	_, _ = claimAgentForTest(t, h, claimLink, "claimed-view@example.com", "claimed-view-human")
	claimed := doJSONRequest(t, h, http.MethodGet, "/api/v1/claims/view?claim_token="+neturl.QueryEscape(claimToken), nil)
	if claimed.Code != http.StatusConflict {
		t.Fatalf("claimed claim view status=%d body=%s", claimed.Code, claimed.Body.String())
	}
}

func TestClaimGitHubFrontendFlowActivatesAgentAndSetsOwnerSession(t *testing.T) {
	gh := enableGitHubOAuthForTest(t, true, true)
	defer gh.Close()

	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = 0
	h := identityTestHandler(srv)
	beforeTreasury, err := srv.treasuryBalance(t.Context())
	if err != nil {
		t.Fatalf("treasury balance before onboarding: %v", err)
	}

	userID, apiKey, claimLink := registerAgentForTest(t, h, "github-claim-agent", "oss")
	claimToken := claimTokenFromLink(t, claimLink)

	start := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/github/start", map[string]any{
		"claim_token": claimToken,
	})
	if start.Code != http.StatusAccepted {
		t.Fatalf("claim github start status=%d body=%s", start.Code, start.Body.String())
	}
	startBody := parseJSONBody(t, start)
	rawAuthorizeURL, _ := startBody["authorize_url"].(string)
	if strings.TrimSpace(rawAuthorizeURL) == "" {
		t.Fatalf("missing authorize_url in start response: %s", start.Body.String())
	}
	authorizeURL, err := neturl.Parse(rawAuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	if got := authorizeURL.Query().Get("scope"); got != "read:user user:email" {
		t.Fatalf("expected claim github scope read:user user:email, got=%q", got)
	}
	state := authorizeURL.Query().Get("state")
	if strings.TrimSpace(state) == "" {
		t.Fatalf("expected signed oauth state in authorize url")
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/claim/callback?code=gh-code&state="+neturl.QueryEscape(state), nil)
	callbackReq.Header.Set("Cookie", joinCookieHeader("", start.Result().Cookies()))
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("claim github callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	callbackLocation, err := neturl.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatalf("callback location: %v", err)
	}
	if callbackLocation.Path != "/claim/"+claimToken+"/callback" {
		t.Fatalf("unexpected callback redirect path=%q", callbackLocation.String())
	}
	if got := callbackLocation.Query().Get("status"); got != "ok" {
		t.Fatalf("unexpected callback status=%q", got)
	}
	if got := callbackLocation.Query().Get("github_username"); got != "octo" {
		t.Fatalf("unexpected callback github username=%q", got)
	}
	if got := callbackLocation.Query().Get("starred"); got != "true" {
		t.Fatalf("unexpected callback starred flag=%q", got)
	}
	if got := callbackLocation.Query().Get("forked"); got != "true" {
		t.Fatalf("unexpected callback forked flag=%q", got)
	}

	complete := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/claims/github/complete", map[string]any{
		"human_username": "octo-human",
	}, map[string]string{
		"Cookie": joinCookieHeader("", callback.Result().Cookies()),
	})
	if complete.Code != http.StatusOK {
		t.Fatalf("claim github complete status=%d body=%s", complete.Code, complete.Body.String())
	}
	completeBody := parseJSONBody(t, complete)
	if completeBody["user_id"] != userID || completeBody["status"] != "active" {
		t.Fatalf("unexpected complete payload=%s", complete.Body.String())
	}
	if completeBody["username"] != "github-claim-agent" {
		t.Fatalf("unexpected final username payload=%s", complete.Body.String())
	}
	owner, ok := completeBody["owner"].(map[string]any)
	if !ok {
		t.Fatalf("expected owner payload in complete response: %s", complete.Body.String())
	}
	if owner["human_username"] != "octo-human" || owner["email"] != "octo@example.com" {
		t.Fatalf("unexpected owner payload=%s", complete.Body.String())
	}
	rewards, ok := completeBody["rewards"].([]any)
	if !ok || len(rewards) != 3 {
		t.Fatalf("expected auth+star+fork rewards, payload=%s", complete.Body.String())
	}

	var ownerCookie string
	for _, cookie := range complete.Result().Cookies() {
		if cookie.Name == ownerSessionCookieName && strings.TrimSpace(cookie.Value) != "" {
			ownerCookie = cookie.Name + "=" + cookie.Value
			break
		}
	}
	if ownerCookie == "" {
		t.Fatalf("expected owner session cookie on complete response")
	}

	bot, err := srv.store.GetBot(t.Context(), userID)
	if err != nil {
		t.Fatalf("get activated bot: %v", err)
	}
	if bot.Status != "running" || !bot.Initialized {
		t.Fatalf("expected active bot after complete: %+v", bot)
	}

	ownerMe := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/owner/me", nil, map[string]string{
		"Cookie": ownerCookie,
	})
	if ownerMe.Code != http.StatusOK || !strings.Contains(ownerMe.Body.String(), `"github_username":"octo"`) {
		t.Fatalf("expected owner github identity binding, got code=%d body=%s", ownerMe.Code, ownerMe.Body.String())
	}
	profile, err := srv.store.GetAgentProfile(t.Context(), userID)
	if err != nil {
		t.Fatalf("get agent profile: %v", err)
	}
	if profile.HumanUsername != "octo-human" || profile.OwnerEmail != "octo@example.com" || profile.GitHubUsername != "octo" {
		t.Fatalf("unexpected agent profile after complete: %+v", profile)
	}
	balance := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if balance.Code != http.StatusOK {
		t.Fatalf("expected rewarded balance read, got code=%d body=%s", balance.Code, balance.Body.String())
	}
	expectedBalance := srv.tokenPolicy().InitialToken + 50000 + 500000 + 200000
	if got := balanceFromResponse(t, balance); got != expectedBalance {
		t.Fatalf("expected rewarded balance=%d, got=%d body=%s", expectedBalance, got, balance.Body.String())
	}
	ownerID, _ := owner["owner_id"].(string)
	for _, decisionKey := range []string{
		"onboarding:initial:" + userID,
		"onboarding:github:bind:" + ownerID,
		"onboarding:github:star:" + ownerID,
		"onboarding:github:fork:" + ownerID,
	} {
		decision, err := srv.store.GetEconomyRewardDecision(t.Context(), decisionKey)
		if err != nil {
			t.Fatalf("get onboarding decision %s: %v", decisionKey, err)
		}
		if decision.Status != "applied" {
			t.Fatalf("decision %s status=%s want applied", decisionKey, decision.Status)
		}
	}
	accounts, err := srv.store.ListTokenAccounts(t.Context())
	if err != nil {
		t.Fatalf("list token accounts: %v", err)
	}
	for _, account := range accounts {
		if account.BotID == clawTreasurySystemID && account.Balance != beforeTreasury {
			t.Fatalf("expected treasury to stay untouched during onboarding mint, before=%d got=%d", beforeTreasury, account.Balance)
		}
	}

	repeat := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/claims/github/complete", map[string]any{
		"human_username": "octo-human",
	}, map[string]string{
		"Cookie": joinCookieHeader("", callback.Result().Cookies()),
	})
	if repeat.Code != http.StatusConflict {
		t.Fatalf("expected repeat finalize conflict after activation, got=%d body=%s", repeat.Code, repeat.Body.String())
	}
	repeatBalance := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if repeatBalance.Code != http.StatusOK {
		t.Fatalf("expected balance read after repeat finalize conflict, got code=%d body=%s", repeatBalance.Code, repeatBalance.Body.String())
	}
	if got := balanceFromResponse(t, repeatBalance); got != expectedBalance {
		t.Fatalf("repeat finalize should not add duplicate onboarding rewards: got=%d want=%d body=%s", got, expectedBalance, repeatBalance.Body.String())
	}
}

func TestClaimGitHubFrontendFlowUsesLocalGitHubMock(t *testing.T) {
	t.Setenv("GITHUB_API_MOCK_ENABLED", "true")
	t.Setenv("GITHUB_API_MOCK_ALLOW_UNSAFE_LOCAL", "true")
	t.Setenv("GITHUB_API_MOCK_LOGIN", "mock-octo")
	t.Setenv("GITHUB_API_MOCK_NAME", "Mock Octo")
	t.Setenv("GITHUB_API_MOCK_EMAIL", "mock-octo@example.com")
	t.Setenv("GITHUB_API_MOCK_USER_ID", "4242")
	t.Setenv("GITHUB_API_MOCK_STARRED", "true")
	t.Setenv("GITHUB_API_MOCK_FORKED", "true")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_CLIENT_ID", "gh-client")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_CLIENT_SECRET", "gh-secret")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_AUTHORIZE_URL", "https://github.mock.test/login/oauth/authorize")

	srv := newTestServer()
	h := identityTestHandler(srv)

	userID, apiKey, claimLink := registerAgentForTest(t, h, "github-mock-agent", "oss")
	claimToken := claimTokenFromLink(t, claimLink)

	start := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/github/start", map[string]any{
		"claim_token": claimToken,
	})
	if start.Code != http.StatusAccepted {
		t.Fatalf("claim github start status=%d body=%s", start.Code, start.Body.String())
	}
	startBody := parseJSONBody(t, start)
	rawAuthorizeURL, _ := startBody["authorize_url"].(string)
	if strings.TrimSpace(rawAuthorizeURL) == "" {
		t.Fatalf("missing authorize_url in start response: %s", start.Body.String())
	}
	authorizeURL, err := neturl.Parse(rawAuthorizeURL)
	if err != nil {
		t.Fatalf("parse authorize_url: %v", err)
	}
	state := authorizeURL.Query().Get("state")
	if strings.TrimSpace(state) == "" {
		t.Fatalf("expected signed oauth state in authorize url")
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/claim/callback?code=gh-code&state="+neturl.QueryEscape(state), nil)
	callbackReq.Header.Set("Cookie", joinCookieHeader("", start.Result().Cookies()))
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("claim github callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	callbackLocation, err := neturl.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatalf("callback location: %v", err)
	}
	if got := callbackLocation.Query().Get("github_username"); got != "mock-octo" {
		t.Fatalf("unexpected callback github username=%q", got)
	}
	if got := callbackLocation.Query().Get("starred"); got != "true" {
		t.Fatalf("unexpected callback starred flag=%q", got)
	}
	if got := callbackLocation.Query().Get("forked"); got != "true" {
		t.Fatalf("unexpected callback forked flag=%q", got)
	}

	complete := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/claims/github/complete", map[string]any{
		"human_username": "mock-human",
	}, map[string]string{
		"Cookie": joinCookieHeader("", callback.Result().Cookies()),
	})
	if complete.Code != http.StatusOK {
		t.Fatalf("claim github complete status=%d body=%s", complete.Code, complete.Body.String())
	}
	completeBody := parseJSONBody(t, complete)
	if completeBody["user_id"] != userID || completeBody["status"] != "active" {
		t.Fatalf("unexpected complete payload=%s", complete.Body.String())
	}
	owner, ok := completeBody["owner"].(map[string]any)
	if !ok {
		t.Fatalf("expected owner payload in complete response: %s", complete.Body.String())
	}
	if owner["human_username"] != "mock-human" || owner["email"] != "mock-octo@example.com" {
		t.Fatalf("unexpected owner payload=%s", complete.Body.String())
	}
	balance := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
	if balance.Code != http.StatusOK {
		t.Fatalf("expected rewarded balance read, got code=%d body=%s", balance.Code, balance.Body.String())
	}
	expectedBalance := srv.tokenPolicy().InitialToken + 50000 + 500000 + 200000
	if got := balanceFromResponse(t, balance); got != expectedBalance {
		t.Fatalf("expected rewarded balance=%d, got=%d body=%s", expectedBalance, got, balance.Body.String())
	}
}

func TestGitHubOAuthMockRequiresUnsafeAllowFlag(t *testing.T) {
	t.Setenv("GITHUB_API_MOCK_ENABLED", "true")
	t.Setenv("GITHUB_API_MOCK_LOGIN", "mock-octo")

	srv := newTestServer()
	if _, ok := srv.githubOAuthMockProfile(""); ok {
		t.Fatalf("expected github oauth mock to stay disabled without GITHUB_API_MOCK_ALLOW_UNSAFE_LOCAL")
	}
}

func TestGitHubOAuthMockRequiresEnabledFlag(t *testing.T) {
	t.Setenv("GITHUB_API_MOCK_ALLOW_UNSAFE_LOCAL", "true")
	t.Setenv("GITHUB_API_MOCK_LOGIN", "mock-octo")

	srv := newTestServer()
	if _, ok := srv.githubOAuthMockProfile(""); ok {
		t.Fatalf("expected github oauth mock to stay disabled without GITHUB_API_MOCK_ENABLED")
	}
}

func TestClaimGitHubFrontendFlowUsesDynamicLocalGitHubMockIdentity(t *testing.T) {
	t.Setenv("GITHUB_API_MOCK_ENABLED", "true")
	t.Setenv("GITHUB_API_MOCK_ALLOW_UNSAFE_LOCAL", "true")
	t.Setenv("GITHUB_API_MOCK_STARRED", "true")
	t.Setenv("GITHUB_API_MOCK_FORKED", "true")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_CLIENT_ID", "gh-client")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_CLIENT_SECRET", "gh-secret")
	t.Setenv("CLAWCOLONY_GITHUB_OAUTH_AUTHORIZE_URL", "https://github.mock.test/login/oauth/authorize")

	srv := newTestServer()
	srv.cfg.TreasuryInitialToken = 0
	h := identityTestHandler(srv)

	claimWithCode := func(agentUsername, code, expectedLogin string) {
		t.Helper()
		_, apiKey, claimLink := registerAgentForTest(t, h, agentUsername, "oss")
		claimToken := claimTokenFromLink(t, claimLink)
		start := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/github/start", map[string]any{
			"claim_token": claimToken,
		})
		if start.Code != http.StatusAccepted {
			t.Fatalf("claim github start status=%d body=%s", start.Code, start.Body.String())
		}
		startBody := parseJSONBody(t, start)
		rawAuthorizeURL, _ := startBody["authorize_url"].(string)
		authorizeURL, err := neturl.Parse(rawAuthorizeURL)
		if err != nil {
			t.Fatalf("parse authorize_url: %v", err)
		}
		state := authorizeURL.Query().Get("state")
		callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/claim/callback?code="+neturl.QueryEscape(code)+"&state="+neturl.QueryEscape(state), nil)
		callbackReq.Header.Set("Cookie", joinCookieHeader("", start.Result().Cookies()))
		callback := httptest.NewRecorder()
		h.ServeHTTP(callback, callbackReq)
		if callback.Code != http.StatusSeeOther {
			t.Fatalf("claim github callback status=%d body=%s", callback.Code, callback.Body.String())
		}
		complete := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/claims/github/complete", map[string]any{
			"human_username": expectedLogin + "-human",
		}, map[string]string{
			"Cookie": joinCookieHeader("", callback.Result().Cookies()),
		})
		if complete.Code != http.StatusOK {
			t.Fatalf("claim github complete status=%d body=%s", complete.Code, complete.Body.String())
		}
		completeBody := parseJSONBody(t, complete)
		owner, ok := completeBody["owner"].(map[string]any)
		if !ok {
			t.Fatalf("expected owner payload in complete response: %s", complete.Body.String())
		}
		if owner["email"] != expectedLogin+"@example.com" {
			t.Fatalf("unexpected owner email=%v payload=%s", owner["email"], complete.Body.String())
		}
		if githubInfo, ok := completeBody["github"].(map[string]any); !ok || githubInfo["username"] != expectedLogin {
			t.Fatalf("unexpected github payload=%s", complete.Body.String())
		}
		balance := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/token/balance", nil, apiKeyHeaders(apiKey))
		if balance.Code != http.StatusOK {
			t.Fatalf("expected rewarded balance read, got code=%d body=%s", balance.Code, balance.Body.String())
		}
		expectedBalance := srv.tokenPolicy().InitialToken + 50000 + 500000 + 200000
		if got := balanceFromResponse(t, balance); got != expectedBalance {
			t.Fatalf("expected rewarded balance=%d, got=%d body=%s", expectedBalance, got, balance.Body.String())
		}
	}

	claimWithCode("github-mock-agent-alice", "gh-code-alice", "alice")
	claimWithCode("github-mock-agent-bob", "gh-code-bob", "bob")
}

func TestClaimGitHubCallbackRejectsMissingVerifiedEmail(t *testing.T) {
	gh := enableGitHubOAuthForTestWithEmails(t, false, false, []githubEmailRecord{
		{Email: "octo@example.com", Primary: true, Verified: false},
	})
	defer gh.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	_, _, claimLink := registerAgentForTest(t, h, "github-claim-email-agent", "oss")
	claimToken := claimTokenFromLink(t, claimLink)
	start := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/github/start", map[string]any{
		"claim_token": claimToken,
	})
	if start.Code != http.StatusAccepted {
		t.Fatalf("claim github start status=%d body=%s", start.Code, start.Body.String())
	}
	startBody := parseJSONBody(t, start)
	authorizeURL, err := neturl.Parse(startBody["authorize_url"].(string))
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/claim/callback?code=gh-code&state="+neturl.QueryEscape(authorizeURL.Query().Get("state")), nil)
	callbackReq.Header.Set("Cookie", joinCookieHeader("", start.Result().Cookies()))
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("claim github callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	location, err := neturl.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatalf("callback location: %v", err)
	}
	if location.Path != "/claim/"+claimToken+"/callback" {
		t.Fatalf("unexpected error redirect path=%q", location.String())
	}
	if got := location.Query().Get("status"); got != "error" {
		t.Fatalf("expected error status redirect, got=%q", got)
	}
	if !strings.Contains(location.Query().Get("error"), "no verified email") {
		t.Fatalf("expected verified email error, got=%q", location.Query().Get("error"))
	}
}

func TestClaimGitHubCallbackProviderErrorReturnsToClaimFrontend(t *testing.T) {
	gh := enableGitHubOAuthForTest(t, false, false)
	defer gh.Close()

	srv := newTestServer()
	h := identityTestHandler(srv)

	_, _, claimLink := registerAgentForTest(t, h, "gh-claim-provider", "oss")
	claimToken := claimTokenFromLink(t, claimLink)
	start := doJSONRequest(t, h, http.MethodPost, "/api/v1/claims/github/start", map[string]any{
		"claim_token": claimToken,
	})
	if start.Code != http.StatusAccepted {
		t.Fatalf("claim github start status=%d body=%s", start.Code, start.Body.String())
	}
	startBody := parseJSONBody(t, start)
	authorizeURL, err := neturl.Parse(startBody["authorize_url"].(string))
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/claim/callback?error=access_denied&state="+neturl.QueryEscape(authorizeURL.Query().Get("state")), nil)
	callbackReq.Header.Set("Cookie", joinCookieHeader("", start.Result().Cookies()))
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("claim github callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	location, err := neturl.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatalf("callback location: %v", err)
	}
	if location.Path != "/claim/"+claimToken+"/callback" {
		t.Fatalf("expected provider error to return to claim frontend, got=%q", location.String())
	}
	if got := location.Query().Get("status"); got != "error" {
		t.Fatalf("expected error status redirect, got=%q", got)
	}
	if got := location.Query().Get("error"); got != "access_denied" {
		t.Fatalf("expected provider error payload, got=%q", got)
	}
}

func TestClaimRouteIsNoLongerServedByRuntime(t *testing.T) {
	srv := newTestServer()
	w := doJSONRequest(t, srv.mux, http.MethodGet, "/claim/test-token", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected runtime /claim route to be absent, got=%d body=%s", w.Code, w.Body.String())
	}
}

func TestPricedBusinessActionsCoverage(t *testing.T) {
	expected := []string{
		"/api/v1/bounty/claim",
		"/api/v1/bounty/post",
		"/api/v1/bounty/verify",
		"/api/v1/collab/apply",
		"/api/v1/collab/assign",
		"/api/v1/collab/close",
		"/api/v1/collab/propose",
		"/api/v1/collab/review",
		"/api/v1/collab/start",
		"/api/v1/collab/submit",
		"/api/v1/ganglia/forge",
		"/api/v1/ganglia/integrate",
		"/api/v1/ganglia/rate",
		"/api/v1/governance/cases/verdict",
		"/api/v1/governance/proposals/cosign",
		"/api/v1/governance/proposals/create",
		"/api/v1/governance/proposals/vote",
		"/api/v1/governance/report",
		"/api/v1/kb/proposals",
		"/api/v1/kb/proposals/ack",
		"/api/v1/kb/proposals/apply",
		"/api/v1/kb/proposals/comment",
		"/api/v1/kb/proposals/enroll",
		"/api/v1/kb/proposals/revise",
		"/api/v1/kb/proposals/start-vote",
		"/api/v1/kb/proposals/vote",
		"/api/v1/library/publish",
		"/api/v1/life/hibernate",
		"/api/v1/life/metamorphose",
		"/api/v1/life/set-will",
		"/api/v1/life/wake",
		"/api/v1/mail/contacts/upsert",
		"/api/v1/mail/lists/create",
		"/api/v1/mail/lists/join",
		"/api/v1/mail/lists/leave",
		"/api/v1/mail/send",
		"/api/v1/mail/send-list",
		"/api/v1/metabolism/dispute",
		"/api/v1/metabolism/supersede",
		"/api/v1/token/reward/upgrade-pr-claim",
		"/api/v1/token/tip",
		"/api/v1/token/transfer",
		"/api/v1/token/wish/create",
		"/api/v1/token/wish/fulfill",
		"/api/v1/tools/invoke",
		"/api/v1/tools/register",
		"/api/v1/tools/review",
	}
	got := make([]string, 0, len(pricedBusinessActions))
	for path := range pricedBusinessActions {
		got = append(got, path)
	}
	sort.Strings(expected)
	sort.Strings(got)
	if strings.Join(expected, "\n") != strings.Join(got, "\n") {
		t.Fatalf("priced action coverage drift\nexpected=%v\ngot=%v", expected, got)
	}
}

func TestActivateBotWithUniqueNameRejectsDuplicate(t *testing.T) {
	srv := newTestServer()

	// Seed an active bot with name "taken-name".
	if _, err := srv.store.ActivateBotWithUniqueName(t.Context(), "", "taken-name"); err == nil {
		// expected error for empty botID — just checking interface works
	}
	_, _ = srv.store.UpsertBot(t.Context(), store.BotUpsertInput{
		BotID:    "existing-bot",
		Name:     "placeholder",
		Provider: "agent",
		Status:   "inactive",
	})
	if _, err := srv.store.ActivateBotWithUniqueName(t.Context(), "existing-bot", "taken-name"); err != nil {
		t.Fatalf("first activation should succeed: %v", err)
	}

	// Now try to activate another bot with the same name.
	_, _ = srv.store.UpsertBot(t.Context(), store.BotUpsertInput{
		BotID:    "new-bot",
		Name:     "placeholder2",
		Provider: "agent",
		Status:   "inactive",
	})
	_, err := srv.store.ActivateBotWithUniqueName(t.Context(), "new-bot", "taken-name")
	if err == nil {
		t.Fatalf("expected ErrBotNameTaken for duplicate active name")
	}
	if !strings.Contains(err.Error(), "already taken") {
		t.Fatalf("expected name-taken error, got: %v", err)
	}

	// Different name should succeed.
	if _, err := srv.store.ActivateBotWithUniqueName(t.Context(), "new-bot", "different-name"); err != nil {
		t.Fatalf("activation with different name should succeed: %v", err)
	}
}
