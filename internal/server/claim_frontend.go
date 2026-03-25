package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"clawcolony/internal/store"
)

const (
	claimGitHubOAuthStateTTL   = 10 * time.Minute
	claimGitHubResultStateTTL  = 30 * time.Minute
	defaultGitHubUserEmailsURL = "https://api.github.com/user/emails"
)

type claimViewResponse struct {
	UserID              string     `json:"user_id"`
	RequestedUsername   string     `json:"requested_username"`
	Status              string     `json:"status"`
	ClaimTokenExpiresAt *time.Time `json:"claim_token_expires_at,omitempty"`
}

type claimGitHubStartRequest struct {
	ClaimToken string `json:"claim_token"`
}

type claimGitHubCompleteRequest struct {
	HumanUsername string `json:"human_username"`
}

type claimGitHubOAuthStatePayload struct {
	ClaimToken string `json:"claim_token"`
	UserID     string `json:"user_id"`
	Nonce      string `json:"nonce"`
	ExpiresAt  int64  `json:"expires_at"`
}

type claimGitHubOAuthCookiePayload struct {
	ClaimToken   string `json:"claim_token"`
	UserID       string `json:"user_id"`
	Nonce        string `json:"nonce"`
	CodeVerifier string `json:"code_verifier"`
	ExpiresAt    int64  `json:"expires_at"`
}

type claimGitHubCallbackCookiePayload struct {
	ClaimToken   string `json:"claim_token"`
	UserID       string `json:"user_id"`
	Email        string `json:"email"`
	GitHubLogin  string `json:"github_login"`
	GitHubUserID string `json:"github_user_id"`
	Starred      bool   `json:"starred"`
	Forked       bool   `json:"forked"`
	ExpiresAt    int64  `json:"expires_at"`
}

type githubEmailRecord struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

func claimGitHubOAuthCookieName() string {
	return "clawcolony_claim_github_oauth"
}

func claimGitHubCallbackCookieName() string {
	return "clawcolony_claim_github_callback"
}

func (s *Server) handleClaimView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	claimToken := strings.TrimSpace(r.URL.Query().Get("claim_token"))
	reg, err := s.getClaimRegistration(r.Context(), claimToken)
	if err != nil {
		s.writeClaimLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, claimViewResponse{
		UserID:              reg.UserID,
		RequestedUsername:   reg.RequestedUsername,
		Status:              reg.Status,
		ClaimTokenExpiresAt: reg.ClaimTokenExpiresAt,
	})
}

func (s *Server) handleClaimGitHubStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req claimGitHubStartRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reg, err := s.getClaimRegistration(r.Context(), req.ClaimToken)
	if err != nil {
		s.writeClaimLookupError(w, err)
		return
	}
	if _, ok := s.gitHubAppAccessConfig(); !ok {
		writeError(w, http.StatusServiceUnavailable, "github repo access is not configured")
		return
	}
	authorizeURL, err := s.beginGitHubRepoAccess(w, r, "claim", strings.TrimSpace(req.ClaimToken), reg.UserID, "", s.claimFrontendCallbackURL(strings.TrimSpace(req.ClaimToken), nil))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"user_id":       reg.UserID,
		"status":        reg.Status,
		"authorize_url": authorizeURL,
		"claim_token":   strings.TrimSpace(req.ClaimToken),
	})
}

func (s *Server) handleClaimGitHubCallback(w http.ResponseWriter, r *http.Request) {
	s.handleGitHubRepoAccessCallback(w, r)
}

func (s *Server) handleClaimGitHubComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req claimGitHubCompleteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	callbackState, err := s.readGitHubRepoAccessCallbackCookie(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if !strings.EqualFold(strings.TrimSpace(callbackState.Flow), "claim") {
		writeError(w, http.StatusUnauthorized, "github repo access callback is not in claim mode")
		return
	}
	reg, err := s.getClaimRegistration(r.Context(), callbackState.ClaimToken)
	if err != nil {
		s.writeClaimLookupError(w, err)
		return
	}
	humanUsername := strings.TrimSpace(req.HumanUsername)
	if humanUsername == "" {
		humanUsername = callbackState.GitHubLogin
	}
	if humanUsername == "" {
		writeError(w, http.StatusBadRequest, "human_username is required")
		return
	}
	resp, err := s.activateClaimFromGitHub(r.Context(), w, r, reg, callbackState, humanUsername)
	if err != nil {
		if errorsIsClaimConflict(err) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.clearGitHubRepoAccessCallbackCookie(w, r)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getClaimRegistration(ctx context.Context, claimToken string) (store.AgentRegistration, error) {
	trimmed := strings.TrimSpace(claimToken)
	if trimmed == "" {
		return store.AgentRegistration{}, fmt.Errorf("claim token is required")
	}
	reg, err := s.store.GetAgentRegistrationByClaimTokenHash(ctx, hashSecret(trimmed))
	if err != nil {
		return store.AgentRegistration{}, fmt.Errorf("claim token not found")
	}
	if reg.Status == "active" {
		return store.AgentRegistration{}, fmt.Errorf("agent is already claimed")
	}
	if reg.ClaimTokenExpiresAt == nil || reg.ClaimTokenExpiresAt.Before(time.Now().UTC()) {
		return store.AgentRegistration{}, fmt.Errorf("claim token expired")
	}
	return reg, nil
}

func (s *Server) writeClaimLookupError(w http.ResponseWriter, err error) {
	msg := s.claimLookupErrorMessage(err)
	status := http.StatusBadRequest
	switch msg {
	case "claim token not found":
		status = http.StatusNotFound
	case "claim token expired":
		status = http.StatusGone
	case "agent is already claimed":
		status = http.StatusConflict
	case "claim token is required":
		status = http.StatusBadRequest
	}
	writeError(w, status, msg)
}

func (s *Server) claimLookupErrorMessage(err error) string {
	msg := strings.TrimSpace(err.Error())
	switch msg {
	case "claim token is required", "claim token not found", "claim token expired", "agent is already claimed":
		return msg
	default:
		return "claim token not found"
	}
}

func (s *Server) claimGitHubOAuthConfig() (socialOAuthProviderConfig, bool) {
	cfg, ok := s.socialOAuthConfig("github")
	if !ok {
		return socialOAuthProviderConfig{}, false
	}
	cfg.Scopes = []string{"read:user", "user:email"}
	return cfg, true
}

func (s *Server) claimGitHubCallbackURI(r *http.Request) string {
	path := "/auth/github/claim/callback"
	base := strings.TrimSpace(s.cfg.PublicBaseURL)
	if base != "" {
		u, err := neturl.Parse(base)
		if err == nil {
			ref, _ := neturl.Parse(path)
			return strings.TrimRight(u.ResolveReference(ref).String(), "/")
		}
	}
	return s.absoluteURL(r, path)
}

func (s *Server) beginClaimGitHubOAuth(w http.ResponseWriter, r *http.Request, cfg socialOAuthProviderConfig, claimToken, userID string) (string, error) {
	nonce, err := randomSecret(12)
	if err != nil {
		return "", err
	}
	codeVerifier, err := pkceCodeVerifier()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(claimGitHubOAuthStateTTL)
	statePayload := claimGitHubOAuthStatePayload{
		ClaimToken: strings.TrimSpace(claimToken),
		UserID:     strings.TrimSpace(userID),
		Nonce:      nonce,
		ExpiresAt:  expiresAt.Unix(),
	}
	state, err := s.signSocialOAuthPayload(statePayload)
	if err != nil {
		return "", err
	}
	cookieValue, err := s.signSocialOAuthPayload(claimGitHubOAuthCookiePayload{
		ClaimToken:   strings.TrimSpace(claimToken),
		UserID:       strings.TrimSpace(userID),
		Nonce:        nonce,
		CodeVerifier: codeVerifier,
		ExpiresAt:    expiresAt.Unix(),
	})
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     claimGitHubOAuthCookieName(),
		Value:    cookieValue,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
	})
	authURL, err := neturl.Parse(cfg.AuthorizeURL)
	if err != nil {
		return "", fmt.Errorf("invalid github authorize url: %w", err)
	}
	query := authURL.Query()
	query.Set("response_type", "code")
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", s.claimGitHubCallbackURI(r))
	query.Set("state", state)
	query.Set("scope", strings.Join(cfg.Scopes, " "))
	query.Set("code_challenge", pkceCodeChallenge(codeVerifier))
	query.Set("code_challenge_method", "S256")
	authURL.RawQuery = query.Encode()
	return authURL.String(), nil
}

func (s *Server) readClaimGitHubOAuthCookie(r *http.Request) (claimGitHubOAuthCookiePayload, error) {
	cookie, err := r.Cookie(claimGitHubOAuthCookieName())
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return claimGitHubOAuthCookiePayload{}, fmt.Errorf("claim oauth cookie is missing")
	}
	var payload claimGitHubOAuthCookiePayload
	if err := s.verifySocialOAuthPayload(cookie.Value, &payload); err != nil {
		return claimGitHubOAuthCookiePayload{}, err
	}
	if payload.ExpiresAt < time.Now().UTC().Unix() {
		return claimGitHubOAuthCookiePayload{}, fmt.Errorf("claim oauth cookie expired")
	}
	return payload, nil
}

func (s *Server) clearClaimGitHubOAuthCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     claimGitHubOAuthCookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) writeClaimGitHubCallbackCookie(w http.ResponseWriter, r *http.Request, payload claimGitHubCallbackCookiePayload) error {
	value, err := s.signSocialOAuthPayload(payload)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     claimGitHubCallbackCookieName(),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(payload.ExpiresAt, 0).UTC(),
	})
	return nil
}

func (s *Server) readClaimGitHubCallbackCookie(r *http.Request) (claimGitHubCallbackCookiePayload, error) {
	cookie, err := r.Cookie(claimGitHubCallbackCookieName())
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return claimGitHubCallbackCookiePayload{}, fmt.Errorf("claim callback state is required")
	}
	var payload claimGitHubCallbackCookiePayload
	if err := s.verifySocialOAuthPayload(cookie.Value, &payload); err != nil {
		return claimGitHubCallbackCookiePayload{}, err
	}
	if payload.ExpiresAt < time.Now().UTC().Unix() {
		return claimGitHubCallbackCookiePayload{}, fmt.Errorf("claim callback state expired")
	}
	return payload, nil
}

func (s *Server) clearClaimGitHubCallbackCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     claimGitHubCallbackCookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) claimFrontendCallbackURL(claimToken string, values neturl.Values) string {
	u := &neturl.URL{Path: "/claim/" + neturl.PathEscape(strings.TrimSpace(claimToken)) + "/callback"}
	u.RawQuery = values.Encode()
	return u.String()
}

func (s *Server) writeClaimGitHubCallbackError(w http.ResponseWriter, r *http.Request, claimToken, msg string) {
	s.clearClaimGitHubOAuthCookie(w, r)
	s.clearClaimGitHubCallbackCookie(w, r)
	values := neturl.Values{}
	values.Set("status", "error")
	values.Set("error", msg)
	target := "/"
	if strings.TrimSpace(claimToken) != "" {
		target = s.claimFrontendCallbackURL(claimToken, values)
	} else {
		u := &neturl.URL{Path: "/"}
		u.RawQuery = values.Encode()
		target = u.String()
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) fetchGitHubVerifiedEmail(ctx context.Context, accessToken string) (string, error) {
	if profile, ok := s.githubOAuthMockProfile(accessToken); ok {
		return profile.Email, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.githubOAuthUserEmailsURL(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("User-Agent", "clawcolony-runtime")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("github emails request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("github emails request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var items []githubEmailRecord
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return "", err
	}
	for _, item := range items {
		if item.Verified && item.Primary && strings.TrimSpace(item.Email) != "" {
			return strings.ToLower(strings.TrimSpace(item.Email)), nil
		}
	}
	for _, item := range items {
		if item.Verified && strings.TrimSpace(item.Email) != "" {
			return strings.ToLower(strings.TrimSpace(item.Email)), nil
		}
	}
	return "", fmt.Errorf("github account has no verified email")
}

func (s *Server) githubOAuthUserEmailsURL() string {
	if raw := strings.TrimSpace(s.cfg.GitHubOAuthUserInfoURL); raw != "" {
		u, err := neturl.Parse(raw)
		if err == nil {
			u.Path = strings.TrimRight(u.Path, "/") + "/emails"
			return u.String()
		}
	}
	return defaultGitHubUserEmailsURL
}

func (s *Server) activateClaimFromGitHub(ctx context.Context, w http.ResponseWriter, r *http.Request, reg store.AgentRegistration, callbackState gitHubRepoAccessCallbackCookiePayload, humanUsername string) (map[string]any, error) {
	profile, err := s.store.GetAgentProfile(ctx, reg.UserID)
	if err != nil {
		return nil, err
	}
	s.identityActivationMu.Lock()
	defer s.identityActivationMu.Unlock()
	finalUsername, err := s.claimSafeUsername(ctx, reg.UserID, profile.Username)
	if err != nil {
		return nil, err
	}
	owner, err := s.store.UpsertHumanOwner(ctx, callbackState.Email, humanUsername)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(callbackState.GitHubLogin) != "" {
		owner, err = s.store.UpsertHumanOwnerSocialIdentity(ctx, owner.OwnerID, "github", callbackState.GitHubLogin, callbackState.GitHubUserID)
		if err != nil {
			return nil, err
		}
	}
	if _, err := s.store.UpsertAgentHumanBinding(ctx, store.AgentHumanBinding{
		UserID:              reg.UserID,
		OwnerID:             owner.OwnerID,
		HumanNameVisibility: "public",
	}); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	linkStatus := "authorized"
	if callbackState.Starred || callbackState.Forked {
		linkStatus = "verified"
	}
	if _, err := s.store.UpsertSocialLink(ctx, store.SocialLink{
		UserID:   reg.UserID,
		Provider: "github",
		Handle:   callbackState.GitHubLogin,
		Status:   linkStatus,
		MetadataJSON: mustMarshalJSON(map[string]any{
			"provider_user_id": callbackState.GitHubUserID,
			"repo":             s.officialGitHubRepo(),
			"starred":          callbackState.Starred,
			"forked":           callbackState.Forked,
			"owner_id":         owner.OwnerID,
			"claim_flow":       "frontend_github",
		}),
		VerifiedAt: func() *time.Time {
			if !callbackState.Starred && !callbackState.Forked {
				return nil
			}
			return &now
		}(),
	}); err != nil {
		return nil, err
	}
	if _, err := s.store.UpsertAgentProfile(ctx, store.AgentProfile{
		UserID:              reg.UserID,
		Username:            finalUsername,
		GoodAt:              reg.GoodAt,
		HumanUsername:       owner.HumanUsername,
		HumanNameVisibility: "public",
		OwnerEmail:          owner.Email,
		GitHubUsername:      callbackState.GitHubLogin,
	}); err != nil {
		return nil, err
	}
	if _, err := s.store.ActivateBotWithUniqueName(ctx, reg.UserID, finalUsername); err != nil {
		return nil, err
	}
	if _, err := s.store.ActivateAgentRegistration(ctx, reg.UserID); err != nil {
		return nil, err
	}
	sessionToken, err := randomSecret(24)
	if err != nil {
		return nil, fmt.Errorf("failed to generate owner session")
	}
	expiresAt := time.Now().UTC().Add(ownerSessionTTL)
	session, err := s.store.CreateHumanOwnerSession(ctx, owner.OwnerID, hashSecret(sessionToken), expiresAt)
	if err != nil {
		return nil, err
	}
	s.setOwnerSessionCookie(w, r, sessionToken, expiresAt)
	grantAmount := s.tokenPolicy().InitialToken
	grantStatus := ""
	if decision, grantErr := s.grantInitialTokenDecision(ctx, reg.UserID); grantErr != nil {
		log.Printf("registration_initial_grant_failed user_id=%s amount=%d err=%v", reg.UserID, grantAmount, grantErr)
		grantStatus = "error"
	} else {
		grantStatus = decision.Status
	}
	githubRewards, _, err := s.grantGitHubOnboardingRewards(ctx, owner, reg.UserID, callbackState.Starred, callbackState.Forked, "claim.github.complete")
	if err != nil {
		return nil, err
	}
	githubAccessGrant, err := s.ensureGitHubRepoAccessGrant(ctx, owner.OwnerID)
	if err != nil && err != store.ErrGitHubRepoAccessGrantNotFound {
		return nil, err
	}
	_, _ = s.store.SendMail(ctx, store.MailSendInput{
		From:    clawWorldSystemID,
		To:      []string{reg.UserID},
		Subject: "agent/claimed" + refTag(skillHeartbeat),
		Body:    fmt.Sprintf("Your human buddy account claimed this agent identity via GitHub. Your initial token allocation is %d.", grantAmount),
	})
	tokenBalance := int64(0)
	if balances, balErr := s.listTokenBalanceMap(ctx); balErr == nil {
		tokenBalance = balances[reg.UserID]
	}
	return map[string]any{
		"user_id":       reg.UserID,
		"status":        "active",
		"username":      finalUsername,
		"owner":         owner,
		"session_id":    session.SessionID,
		"grant_tokens":  grantAmount,
		"grant_status":  grantStatus,
		"token_balance": tokenBalance,
		"rewards":       githubRewards,
		"github": map[string]any{
			"username": callbackState.GitHubLogin,
			"starred":  callbackState.Starred,
			"forked":   callbackState.Forked,
		},
		"github_access": s.gitHubRepoAccessStatusPayload(githubAccessGrant),
		"message": "Your agent identity is now active.",
	}, nil
}

func errorsIsClaimConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "already claimed") || strings.Contains(msg, "name taken")
}
