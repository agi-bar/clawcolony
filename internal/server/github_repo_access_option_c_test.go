package server

import (
	"context"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type gitHubOptionCTestFixture struct {
	t *testing.T

	server *httptest.Server

	org                 string
	repositoryOwner     string
	repositoryName      string
	repositoryID        int64
	installationID      int64
	contributorTeamSlug string
	contributorTeamID   int64

	activationStatus int

	mu              sync.Mutex
	membershipState string
	teamMember      bool
	inviteCount     int
	activateCount   int
	teamAssignCount int
}

func newGitHubOptionCTestFixture(t *testing.T, activationStatus int) *gitHubOptionCTestFixture {
	t.Helper()

	fixture := &gitHubOptionCTestFixture{
		t:                   t,
		org:                 "clawcolony",
		repositoryOwner:     "clawcolony",
		repositoryName:      "clawcolony",
		repositoryID:        1174004296,
		installationID:      24680,
		contributorTeamSlug: "contributors",
		contributorTeamID:   13579,
		activationStatus:    activationStatus,
		membershipState:     "not_member",
	}

	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.handle(w, r)
	}))
	t.Cleanup(func() {
		fixture.server.Close()
	})
	return fixture
}

func (f *gitHubOptionCTestFixture) configureEnv(t *testing.T) {
	t.Helper()

	privateKeyPEM := generateGitHubAppPrivateKeyPEM(t)

	t.Setenv("CLAWCOLONY_GITHUB_APP_CLIENT_ID", "gh-app-client")
	t.Setenv("CLAWCOLONY_GITHUB_APP_CLIENT_SECRET", "gh-app-secret")
	t.Setenv("CLAWCOLONY_GITHUB_APP_AUTHORIZE_URL", f.server.URL+"/login/oauth/authorize")
	t.Setenv("CLAWCOLONY_GITHUB_APP_TOKEN_URL", f.server.URL+"/login/oauth/access_token")
	t.Setenv("CLAWCOLONY_GITHUB_APP_API_BASE_URL", f.server.URL)
	t.Setenv("CLAWCOLONY_GITHUB_APP_TOKEN_ENCRYPTION_KEY", "test-github-app-key")
	t.Setenv("CLAWCOLONY_GITHUB_APP_REPOSITORY_ID", strconv.FormatInt(f.repositoryID, 10))
	t.Setenv("CLAWCOLONY_GITHUB_APP_REPOSITORY_OWNER", f.repositoryOwner)
	t.Setenv("CLAWCOLONY_GITHUB_APP_REPOSITORY_NAME", f.repositoryName)
	t.Setenv("CLAWCOLONY_GITHUB_APP_ALLOWED_INSTALLATION_ID", strconv.FormatInt(f.installationID, 10))
	t.Setenv("CLAWCOLONY_GITHUB_APP_ID", "123456")
	t.Setenv("CLAWCOLONY_GITHUB_APP_PRIVATE_KEY_PEM", privateKeyPEM)
	t.Setenv("CLAWCOLONY_GITHUB_APP_ORG", f.org)
	t.Setenv("CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_SLUG", f.contributorTeamSlug)
	t.Setenv("CLAWCOLONY_GITHUB_APP_CONTRIBUTOR_TEAM_ID", strconv.FormatInt(f.contributorTeamID, 10))
	t.Setenv("CLAWCOLONY_GITHUB_API_BASE_URL", f.server.URL)
	t.Setenv("CLAWCOLONY_OFFICIAL_GITHUB_REPO", f.repositoryOwner+"/"+f.repositoryName)
}

func generateGitHubAppPrivateKeyPEM(t *testing.T) string {
	t.Helper()

	key, err := rsa.GenerateKey(crand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate github app private key: %v", err)
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	return string(pem.EncodeToMemory(block))
}

func (f *gitHubOptionCTestFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()

	switch {
	case r.URL.Path == "/login/oauth/access_token":
		if err := r.ParseForm(); err != nil {
			f.t.Fatalf("parse github token form: %v", err)
		}
		if strings.TrimSpace(r.Form.Get("code_verifier")) == "" {
			f.t.Fatalf("expected github code_verifier")
		}
		writeFixtureJSON(w, map[string]any{
			"access_token":             "gh-access-token",
			"token_type":               "bearer",
			"expires_in":               28800,
			"refresh_token":            "gh-refresh-token",
			"refresh_token_expires_in": 2592000,
		})
	case r.URL.Path == "/user":
		f.requireBearer(r, "gh-access-token")
		writeFixtureJSON(w, map[string]any{
			"id":    42,
			"login": "octo",
			"name":  "Octo Human",
		})
	case r.URL.Path == "/user/emails":
		f.requireBearer(r, "gh-access-token")
		writeFixtureJSON(w, []githubEmailRecord{
			{Email: "octo@example.com", Primary: true, Verified: true},
		})
	case r.URL.Path == "/user/installations":
		f.requireBearer(r, "gh-access-token")
		if f.repoVisibleToUser() {
			writeFixtureJSON(w, map[string]any{
				"installations": []map[string]any{
					{
						"id":                   f.installationID,
						"repository_selection": "selected",
						"account": map[string]any{
							"login": f.repositoryOwner,
						},
					},
				},
			})
			return
		}
		writeFixtureJSON(w, map[string]any{"installations": []any{}})
	case r.URL.Path == fmt.Sprintf("/user/installations/%d/repositories", f.installationID):
		f.requireBearer(r, "gh-access-token")
		if f.repoVisibleToUser() {
			writeFixtureJSON(w, map[string]any{
				"repositories": []map[string]any{
					{
						"id":        f.repositoryID,
						"name":      f.repositoryName,
						"full_name": f.repositoryOwner + "/" + f.repositoryName,
					},
				},
			})
			return
		}
		writeFixtureJSON(w, map[string]any{"repositories": []any{}})
	case r.URL.Path == fmt.Sprintf("/repos/%s/%s", f.repositoryOwner, f.repositoryName):
		f.requireBearer(r, "gh-access-token")
		if f.directPermissionReady() {
			writeFixtureJSON(w, map[string]any{
				"id":        f.repositoryID,
				"name":      f.repositoryName,
				"full_name": f.repositoryOwner + "/" + f.repositoryName,
				"role_name": "write",
				"permissions": map[string]any{
					"admin":    false,
					"maintain": false,
					"push":     true,
					"triage":   false,
					"pull":     true,
				},
			})
			return
		}
		http.NotFound(w, r)
	case r.URL.Path == fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission", f.repositoryOwner, f.repositoryName, "octo"):
		f.t.Fatalf("unexpected deprecated collaborator permission check: %s", r.URL.Path)
	case r.URL.Path == fmt.Sprintf("/app/installations/%d/access_tokens", f.installationID):
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(auth, "Bearer ") {
			f.t.Fatalf("expected bearer app jwt, got=%q", auth)
		}
		writeFixtureJSONWithStatus(w, http.StatusCreated, map[string]any{"token": "gh-installation-token"})
	case r.URL.Path == fmt.Sprintf("/orgs/%s/memberships/%s", f.org, "octo"):
		f.requireBearer(r, "gh-installation-token")
		state := f.currentMembershipState()
		if state == "not_member" {
			http.NotFound(w, r)
			return
		}
		writeFixtureJSON(w, map[string]any{"state": state})
	case r.URL.Path == fmt.Sprintf("/orgs/%s/invitations", f.org):
		f.requireBearer(r, "gh-installation-token")
		f.recordInvitation()
		writeFixtureJSONWithStatus(w, http.StatusCreated, map[string]any{"id": 1})
	case r.URL.Path == fmt.Sprintf("/user/memberships/orgs/%s", f.org):
		f.requireBearer(r, "gh-access-token")
		f.mu.Lock()
		f.activateCount++
		status := f.activationStatus
		if status == 0 {
			status = http.StatusOK
		}
		if status == http.StatusOK {
			f.membershipState = "active"
		}
		state := f.membershipState
		f.mu.Unlock()
		if status != http.StatusOK {
			writeFixtureJSONWithStatus(w, status, map[string]any{"message": "membership activation blocked"})
			return
		}
		writeFixtureJSON(w, map[string]any{"state": state})
	case r.URL.Path == fmt.Sprintf("/orgs/%s/teams/%s/memberships/%s", f.org, f.contributorTeamSlug, "octo"):
		f.requireBearer(r, "gh-installation-token")
		f.mu.Lock()
		if f.membershipState != "active" {
			f.mu.Unlock()
			writeFixtureJSONWithStatus(w, http.StatusForbidden, map[string]any{"message": "membership not active"})
			return
		}
		f.teamAssignCount++
		f.teamMember = true
		f.mu.Unlock()
		writeFixtureJSON(w, map[string]any{"state": "active", "role": "member"})
	case r.URL.Path == "/users/octo/starred":
		writeFixtureJSON(w, []any{})
	case r.URL.Path == "/users/octo/repos":
		writeFixtureJSON(w, []any{})
	default:
		http.NotFound(w, r)
	}
}

func (f *gitHubOptionCTestFixture) recordInvitation() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inviteCount++
	if f.membershipState == "not_member" {
		f.membershipState = "pending"
	}
}

func (f *gitHubOptionCTestFixture) currentMembershipState() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.membershipState
}

func (f *gitHubOptionCTestFixture) repoVisibleToUser() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.membershipState == "active" || f.teamMember
}

func (f *gitHubOptionCTestFixture) directPermissionReady() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.membershipState == "active" && f.teamMember
}

func (f *gitHubOptionCTestFixture) requireBearer(r *http.Request, expected string) {
	f.t.Helper()
	if got := strings.TrimSpace(r.Header.Get("Authorization")); got != "Bearer "+expected {
		f.t.Fatalf("unexpected auth header=%q want=%q path=%s", got, "Bearer "+expected, r.URL.Path)
	}
}

func writeFixtureJSON(w http.ResponseWriter, payload any) {
	writeFixtureJSONWithStatus(w, http.StatusOK, payload)
}

func writeFixtureJSONWithStatus(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func TestOwnerGitHubRepoAccessFlowPromotesExternalUserThroughOrgTeamWorkflow(t *testing.T) {
	fixture := newGitHubOptionCTestFixture(t, http.StatusOK)
	fixture.configureEnv(t)

	srv := newTestServer()
	h := identityTestHandler(srv)

	_, _, claimLink := registerAgentForTest(t, h, "option-c-owner-agent", "oss")
	_, ownerCookie := claimAgentForTest(t, h, claimLink, "owner-option-c@example.com", "option-c-owner")

	start := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/github-access/start", nil, map[string]string{
		"Cookie": ownerCookie,
	})
	if start.Code != http.StatusAccepted {
		t.Fatalf("github access start status=%d body=%s", start.Code, start.Body.String())
	}
	startBody := parseJSONBody(t, start)
	authorizeURL, err := neturl.Parse(startBody["authorize_url"].(string))
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/repo-access/callback?code=gh-code&state="+neturl.QueryEscape(authorizeURL.Query().Get("state")), nil)
	callbackReq.Header.Set("Cookie", joinCookieHeader(ownerCookie, start.Result().Cookies()))
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("github access callback status=%d body=%s", callback.Code, callback.Body.String())
	}
	location, err := neturl.Parse(callback.Header().Get("Location"))
	if err != nil {
		t.Fatalf("callback location: %v", err)
	}
	if location.Path != "/github-access/callback" {
		t.Fatalf("unexpected callback redirect path=%q", location.String())
	}
	if got := location.Query().Get("github_access_status"); got != "active_contributor" {
		t.Fatalf("unexpected github access status=%q", got)
	}
	if got := location.Query().Get("mode"); got != "upstream_via_org_team" {
		t.Fatalf("unexpected github access mode=%q", got)
	}

	status := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/github-access/status", nil, map[string]string{
		"Cookie": ownerCookie,
	})
	if status.Code != http.StatusOK {
		t.Fatalf("github access status code=%d body=%s", status.Code, status.Body.String())
	}
	statusBody := parseJSONBody(t, status)
	if statusBody["status"] != "active_contributor" {
		t.Fatalf("expected active_contributor, got body=%s", status.Body.String())
	}
	if statusBody["mode"] != "upstream_via_org_team" {
		t.Fatalf("expected org/team mode, got body=%s", status.Body.String())
	}
	if statusBody["org"] != fixture.org {
		t.Fatalf("expected org=%s, got body=%s", fixture.org, status.Body.String())
	}
	if statusBody["org_membership_status"] != "active" {
		t.Fatalf("expected active org membership, got body=%s", status.Body.String())
	}
	team, ok := statusBody["team"].(map[string]any)
	if !ok || team["slug"] != fixture.contributorTeamSlug {
		t.Fatalf("expected contributor team in status response, got body=%s", status.Body.String())
	}
	if statusBody["next_action"] != "none" {
		t.Fatalf("expected next_action=none, got body=%s", status.Body.String())
	}

	ownerMe := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/owner/me", nil, map[string]string{
		"Cookie": ownerCookie,
	})
	if ownerMe.Code != http.StatusOK {
		t.Fatalf("owner me status=%d body=%s", ownerMe.Code, ownerMe.Body.String())
	}
	ownerBody := parseJSONBody(t, ownerMe)
	owner, ok := ownerBody["owner"].(map[string]any)
	if !ok {
		t.Fatalf("expected owner payload on owner/me: %s", ownerMe.Body.String())
	}
	ownerID, _ := owner["owner_id"].(string)

	grant, err := srv.store.GetGitHubRepoAccessGrant(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("get github repo access grant: %v", err)
	}
	if grant.Mode != "upstream_via_org_team" || grant.AccessStatus != "active_contributor" {
		t.Fatalf("unexpected saved grant: %+v", grant)
	}
	if grant.Org != fixture.org || grant.OrgMembershipStatus != "active" || grant.TeamSlug != fixture.contributorTeamSlug {
		t.Fatalf("unexpected org/team fields on saved grant: %+v", grant)
	}

	fixture.mu.Lock()
	inviteCount := fixture.inviteCount
	activateCount := fixture.activateCount
	teamAssignCount := fixture.teamAssignCount
	fixture.mu.Unlock()
	if inviteCount != 1 || activateCount != 1 || teamAssignCount != 1 {
		t.Fatalf("unexpected workflow counts invite=%d activate=%d team=%d", inviteCount, activateCount, teamAssignCount)
	}
}

func TestAgentGitHubRepoAccessTokenReturnsMinimalPayloadForActiveContributor(t *testing.T) {
	fixture := newGitHubOptionCTestFixture(t, http.StatusOK)
	fixture.configureEnv(t)

	srv := newTestServer()
	h := identityTestHandler(srv)

	_, apiKey, claimLink := registerAgentForTest(t, h, "option-c-agent-token", "oss")
	_, ownerCookie := claimAgentForTest(t, h, claimLink, "agent-token-owner@example.com", "agent-token-owner")

	start := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/github-access/start", nil, map[string]string{
		"Cookie": ownerCookie,
	})
	if start.Code != http.StatusAccepted {
		t.Fatalf("github access start status=%d body=%s", start.Code, start.Body.String())
	}
	startBody := parseJSONBody(t, start)
	authorizeURL, err := neturl.Parse(startBody["authorize_url"].(string))
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/repo-access/callback?code=gh-code&state="+neturl.QueryEscape(authorizeURL.Query().Get("state")), nil)
	callbackReq.Header.Set("Cookie", joinCookieHeader(ownerCookie, start.Result().Cookies()))
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("github access callback status=%d body=%s", callback.Code, callback.Body.String())
	}

	tokenResp := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/github-access/token", nil, apiKeyHeaders(apiKey))
	if tokenResp.Code != http.StatusOK {
		t.Fatalf("github access token status=%d body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	body := parseJSONBody(t, tokenResp)
	if strings.TrimSpace(fmt.Sprintf("%v", body["access_token"])) == "" {
		t.Fatalf("expected access_token in response, got body=%s", tokenResp.Body.String())
	}
	if body["repository_full_name"] != fixture.repositoryOwner+"/"+fixture.repositoryName {
		t.Fatalf("expected repository_full_name, got body=%s", tokenResp.Body.String())
	}
	if body["role"] != "contributor" {
		t.Fatalf("expected contributor role, got body=%s", tokenResp.Body.String())
	}
	if _, ok := body["access_expires_at"]; !ok {
		t.Fatalf("expected access_expires_at in response, got body=%s", tokenResp.Body.String())
	}
	for _, forbidden := range []string{"status", "mode", "repository", "credential_patch", "https_username"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("did not expect %q in minimal token payload, got body=%s", forbidden, tokenResp.Body.String())
		}
	}
}

func TestAgentGitHubRepoAccessTokenRejectsAgentWithoutConnectedOwnerGrant(t *testing.T) {
	fixture := newGitHubOptionCTestFixture(t, http.StatusOK)
	fixture.configureEnv(t)

	srv := newTestServer()
	h := identityTestHandler(srv)

	_, apiKey, claimLink := registerAgentForTest(t, h, "option-c-agent-token-missing", "oss")
	_, _ = claimAgentForTest(t, h, claimLink, "agent-token-missing-owner@example.com", "agent-token-missing-owner")

	tokenResp := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/github-access/token", nil, apiKeyHeaders(apiKey))
	if tokenResp.Code != http.StatusConflict {
		t.Fatalf("github access token without grant status=%d body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	body := parseJSONBody(t, tokenResp)
	if body["status"] != "not_connected" {
		t.Fatalf("expected not_connected, got body=%s", tokenResp.Body.String())
	}
	reauthorizeURL, err := neturl.Parse(strings.TrimSpace(fmt.Sprintf("%v", body["reauthorize_url"])))
	if err != nil {
		t.Fatalf("expected reauthorize_url, got body=%s err=%v", tokenResp.Body.String(), err)
	}
	if reauthorizeURL.Path != "/auth/github/repo-access/reauthorize" {
		t.Fatalf("unexpected reauthorize_url path=%q body=%s", reauthorizeURL.String(), tokenResp.Body.String())
	}
	if got := reauthorizeURL.Query().Get("token"); got == "" {
		t.Fatalf("expected signed reauthorize token, got url=%s", reauthorizeURL.String())
	}
	if body["next_action"] != "open reauthorize_url in a browser, complete GitHub approval, then retry /api/v1/github-access/token" {
		t.Fatalf("unexpected next_action body=%s", tokenResp.Body.String())
	}
}

func TestAgentGitHubRepoAccessTokenReturnsReauthorizeURLForRevokedGrant(t *testing.T) {
	fixture := newGitHubOptionCTestFixture(t, http.StatusOK)
	fixture.configureEnv(t)

	srv := newTestServer()
	h := identityTestHandler(srv)

	_, apiKey, claimLink := registerAgentForTest(t, h, "option-c-agent-token-reauth", "oss")
	_, ownerCookie := claimAgentForTest(t, h, claimLink, "agent-token-reauth-owner@example.com", "agent-token-reauth-owner")

	start := doJSONRequestWithHeaders(t, h, http.MethodPost, "/api/v1/github-access/start", nil, map[string]string{
		"Cookie": ownerCookie,
	})
	if start.Code != http.StatusAccepted {
		t.Fatalf("github access start status=%d body=%s", start.Code, start.Body.String())
	}
	startBody := parseJSONBody(t, start)
	authorizeURL, err := neturl.Parse(startBody["authorize_url"].(string))
	if err != nil {
		t.Fatalf("parse authorize url: %v", err)
	}

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/repo-access/callback?code=gh-code&state="+neturl.QueryEscape(authorizeURL.Query().Get("state")), nil)
	callbackReq.Header.Set("Cookie", joinCookieHeader(ownerCookie, start.Result().Cookies()))
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusSeeOther {
		t.Fatalf("github access callback status=%d body=%s", callback.Code, callback.Body.String())
	}

	ownerMe := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/owner/me", nil, map[string]string{
		"Cookie": ownerCookie,
	})
	if ownerMe.Code != http.StatusOK {
		t.Fatalf("owner me status=%d body=%s", ownerMe.Code, ownerMe.Body.String())
	}
	ownerBody := parseJSONBody(t, ownerMe)
	owner, ok := ownerBody["owner"].(map[string]any)
	if !ok {
		t.Fatalf("expected owner payload on owner/me: %s", ownerMe.Body.String())
	}
	ownerID, _ := owner["owner_id"].(string)

	if _, err := srv.store.RevokeGitHubRepoAccessGrant(context.Background(), ownerID, time.Now().UTC()); err != nil {
		t.Fatalf("revoke github repo access grant: %v", err)
	}

	tokenResp := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/github-access/token", nil, apiKeyHeaders(apiKey))
	if tokenResp.Code != http.StatusConflict {
		t.Fatalf("github access token status=%d body=%s", tokenResp.Code, tokenResp.Body.String())
	}
	body := parseJSONBody(t, tokenResp)
	if body["status"] != "reauthorization_required" {
		t.Fatalf("expected reauthorization_required, got body=%s", tokenResp.Body.String())
	}
	reauthorizeURL, err := neturl.Parse(strings.TrimSpace(fmt.Sprintf("%v", body["reauthorize_url"])))
	if err != nil {
		t.Fatalf("parse reauthorize_url: %v body=%s", err, tokenResp.Body.String())
	}
	if reauthorizeURL.Path != "/auth/github/repo-access/reauthorize" {
		t.Fatalf("unexpected reauthorize_url path=%q", reauthorizeURL.String())
	}

	reauthReq := httptest.NewRequest(http.MethodGet, reauthorizeURL.RequestURI(), nil)
	reauthReq.Host = "runtime.test"
	reauth := httptest.NewRecorder()
	h.ServeHTTP(reauth, reauthReq)
	if reauth.Code != http.StatusSeeOther {
		t.Fatalf("reauthorize status=%d body=%s", reauth.Code, reauth.Body.String())
	}
	redirectURL, err := neturl.Parse(reauth.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse reauthorize redirect: %v", err)
	}
	if redirectURL.Host == "" || !strings.Contains(redirectURL.Path, "/login/oauth/authorize") {
		t.Fatalf("unexpected authorize redirect=%s", redirectURL.String())
	}

	var oauthCookie *http.Cookie
	for _, cookie := range reauth.Result().Cookies() {
		if cookie.Name == gitHubRepoAccessOAuthCookieName() {
			oauthCookie = cookie
			break
		}
	}
	if oauthCookie == nil {
		t.Fatalf("expected github repo access oauth cookie on reauthorize")
	}

	reauthCallbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/repo-access/callback?code=gh-code&state="+neturl.QueryEscape(redirectURL.Query().Get("state")), nil)
	reauthCallbackReq.Header.Set("Cookie", joinCookieHeader("", []*http.Cookie{oauthCookie}))
	reauthCallback := httptest.NewRecorder()
	h.ServeHTTP(reauthCallback, reauthCallbackReq)
	if reauthCallback.Code != http.StatusSeeOther {
		t.Fatalf("reauthorize callback status=%d body=%s", reauthCallback.Code, reauthCallback.Body.String())
	}
	callbackLocation, err := neturl.Parse(reauthCallback.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse callback location: %v", err)
	}
	if callbackLocation.Path != "/github-access/callback" {
		t.Fatalf("unexpected callback path=%q", callbackLocation.String())
	}
	if got := callbackLocation.Query().Get("status"); got != "ok" {
		t.Fatalf("unexpected callback status=%q", got)
	}

	successResp := doJSONRequestWithHeaders(t, h, http.MethodGet, "/api/v1/github-access/token", nil, apiKeyHeaders(apiKey))
	if successResp.Code != http.StatusOK {
		t.Fatalf("github access token after reauthorize status=%d body=%s", successResp.Code, successResp.Body.String())
	}
}

func TestClaimGitHubFrontendFlowKeepsPendingStatusWhenOrgActivationBlocked(t *testing.T) {
	fixture := newGitHubOptionCTestFixture(t, http.StatusForbidden)
	fixture.configureEnv(t)

	srv := newTestServer()
	h := identityTestHandler(srv)

	_, _, claimLink := registerAgentForTest(t, h, "option-c-claim-agent", "oss")
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

	callbackReq := httptest.NewRequest(http.MethodGet, "/auth/github/repo-access/callback?code=gh-code&state="+neturl.QueryEscape(authorizeURL.Query().Get("state")), nil)
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
		t.Fatalf("unexpected callback redirect path=%q", location.String())
	}
	if got := location.Query().Get("github_access_status"); got != "org_invitation_pending" {
		t.Fatalf("unexpected pending github access status=%q", got)
	}
	if got := location.Query().Get("mode"); got != "upstream_via_org_team" {
		t.Fatalf("unexpected github access mode=%q", got)
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
	githubAccess, ok := completeBody["github_access"].(map[string]any)
	if !ok {
		t.Fatalf("expected github_access payload on complete response: %s", complete.Body.String())
	}
	if githubAccess["status"] != "org_invitation_pending" {
		t.Fatalf("expected pending github access after complete, got body=%s", complete.Body.String())
	}
	if githubAccess["mode"] != "upstream_via_org_team" {
		t.Fatalf("expected org/team mode after complete, got body=%s", complete.Body.String())
	}
	if githubAccess["org_membership_status"] != "pending" {
		t.Fatalf("expected pending org membership after complete, got body=%s", complete.Body.String())
	}
	if githubAccess["next_action"] != "retry_activation" {
		t.Fatalf("expected retry_activation next action, got body=%s", complete.Body.String())
	}
	if githubAccess["blocking_reason"] != "org_membership_not_active" {
		t.Fatalf("expected org_membership_not_active blocking reason, got body=%s", complete.Body.String())
	}
}
