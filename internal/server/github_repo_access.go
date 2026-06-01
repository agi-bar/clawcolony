package server

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	crand "crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strconv"
	"strings"
	"time"

	"clawcolony/internal/store"
)

const (
	githubRepoAccessStateTTL    = 10 * time.Minute
	githubRepoAccessResultTTL   = 30 * time.Minute
	githubRepoAccessReauthTTL   = 15 * time.Minute
	defaultGitHubAppAPIBaseURL  = "https://api.github.com"
	githubRepoAccessCallbackURI = "/auth/github/repo-access/callback"
	githubRepoAccessReauthURI   = "/auth/github/repo-access/reauthorize"
)

type gitHubAppAccessConfig struct {
	ClientID              string
	ClientSecret          string
	AuthorizeURL          string
	TokenURL              string
	APIBaseURL            string
	DisplayName           string
	AppID                 string
	PrivateKeyPEM         string
	Org                   string
	ContributorTeamSlug   string
	ContributorTeamID     string
	MaintainerTeamSlug    string
	MaintainerTeamID      string
	RepositoryID          string
	RepositoryOwner       string
	RepositoryName        string
	AllowedInstallationID string
}

type gitHubRepoAccessStatePayload struct {
	Flow       string `json:"flow"`
	ClaimToken string `json:"claim_token,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	OwnerID    string `json:"owner_id,omitempty"`
	Nonce      string `json:"nonce"`
	ExpiresAt  int64  `json:"expires_at"`
}

type gitHubRepoAccessCookiePayload struct {
	Flow          string `json:"flow"`
	ClaimToken    string `json:"claim_token,omitempty"`
	UserID        string `json:"user_id,omitempty"`
	OwnerID       string `json:"owner_id,omitempty"`
	Nonce         string `json:"nonce"`
	CodeVerifier  string `json:"code_verifier"`
	CallbackRoute string `json:"callback_route,omitempty"`
	ExpiresAt     int64  `json:"expires_at"`
}

type gitHubRepoAccessCallbackCookiePayload struct {
	Flow            string `json:"flow"`
	ClaimToken      string `json:"claim_token,omitempty"`
	UserID          string `json:"user_id,omitempty"`
	OwnerID         string `json:"owner_id,omitempty"`
	Email           string `json:"email"`
	GitHubLogin     string `json:"github_login"`
	GitHubUserID    string `json:"github_user_id"`
	Mode            string `json:"mode,omitempty"`
	AccessStatus    string `json:"access_status,omitempty"`
	Org             string `json:"org,omitempty"`
	OrgMembership   string `json:"org_membership_status,omitempty"`
	TeamSlug        string `json:"team_slug,omitempty"`
	NextAction      string `json:"next_action,omitempty"`
	BlockingReason  string `json:"blocking_reason,omitempty"`
	RepositoryID    string `json:"repository_id"`
	RepositoryOwner string `json:"repository_owner"`
	RepositoryName  string `json:"repository_name"`
	InstallationID  string `json:"installation_id"`
	Role            string `json:"role"`
	Starred         bool   `json:"starred"`
	Forked          bool   `json:"forked"`
	ExpiresAt       int64  `json:"expires_at"`
}

type gitHubRepoAccessReauthorizePayload struct {
	OwnerID   string `json:"owner_id"`
	UserID    string `json:"user_id,omitempty"`
	ExpiresAt int64  `json:"expires_at"`
}

type gitHubAppTokenResponse struct {
	AccessToken           string `json:"access_token"`
	TokenType             string `json:"token_type"`
	Scope                 string `json:"scope"`
	ExpiresIn             int64  `json:"expires_in"`
	RefreshToken          string `json:"refresh_token"`
	RefreshTokenExpiresIn int64  `json:"refresh_token_expires_in"`
}

type gitHubUserInstallationsResponse struct {
	Installations []gitHubUserInstallation `json:"installations"`
}

type gitHubUserInstallation struct {
	ID                  int64  `json:"id"`
	RepositorySelection string `json:"repository_selection"`
	Account             struct {
		Login string `json:"login"`
	} `json:"account"`
}

type gitHubInstallationRepositoriesResponse struct {
	Repositories []gitHubInstallationRepository `json:"repositories"`
}

type gitHubInstallationRepository struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
}

type gitHubCollaboratorPermission struct {
	Permission string `json:"permission"`
	User       struct {
		Login string `json:"login"`
	} `json:"user"`
}

type gitHubRepositoryAccess struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	FullName    string `json:"full_name"`
	RoleName    string `json:"role_name"`
	Permissions struct {
		Admin    bool `json:"admin"`
		Maintain bool `json:"maintain"`
		Push     bool `json:"push"`
		Triage   bool `json:"triage"`
		Pull     bool `json:"pull"`
	} `json:"permissions"`
}

type gitHubAPIStatusError struct {
	Path       string
	StatusCode int
	Body       string
}

func (e gitHubAPIStatusError) Error() string {
	return fmt.Sprintf("github app api request failed: path=%s status=%d body=%s", e.Path, e.StatusCode, e.Body)
}

type gitHubInstallationAccessTokenResponse struct {
	Token string `json:"token"`
}

type gitHubOrgMembershipResponse struct {
	State string `json:"state"`
	Role  string `json:"role"`
}

type gitHubTeamMembershipResponse struct {
	State string `json:"state"`
	Role  string `json:"role"`
}

func gitHubRepoAccessOAuthCookieName() string {
	return "clawcolony_github_repo_access_oauth"
}

func gitHubRepoAccessCallbackCookieName() string {
	return "clawcolony_github_repo_access_callback"
}

func (s *Server) gitHubAppAccessConfig() (gitHubAppAccessConfig, bool) {
	cfg := gitHubAppAccessConfig{
		ClientID:              strings.TrimSpace(s.cfg.GitHubAppClientID),
		ClientSecret:          strings.TrimSpace(s.cfg.GitHubAppClientSecret),
		AuthorizeURL:          strings.TrimSpace(s.cfg.GitHubAppAuthorizeURL),
		TokenURL:              strings.TrimSpace(s.cfg.GitHubAppTokenURL),
		APIBaseURL:            strings.TrimSpace(s.cfg.GitHubAppAPIBaseURL),
		DisplayName:           strings.TrimSpace(s.cfg.GitHubAppDisplayName),
		AppID:                 strings.TrimSpace(s.cfg.GitHubAppID),
		PrivateKeyPEM:         strings.TrimSpace(s.cfg.GitHubAppPrivateKeyPEM),
		Org:                   strings.TrimSpace(s.cfg.GitHubAppOrg),
		ContributorTeamSlug:   strings.TrimSpace(s.cfg.GitHubAppContributorTeamSlug),
		ContributorTeamID:     strings.TrimSpace(s.cfg.GitHubAppContributorTeamID),
		MaintainerTeamSlug:    strings.TrimSpace(s.cfg.GitHubAppMaintainerTeamSlug),
		MaintainerTeamID:      strings.TrimSpace(s.cfg.GitHubAppMaintainerTeamID),
		RepositoryID:          strings.TrimSpace(s.cfg.GitHubAppRepositoryID),
		RepositoryOwner:       strings.TrimSpace(s.cfg.GitHubAppRepositoryOwner),
		RepositoryName:        strings.TrimSpace(s.cfg.GitHubAppRepositoryName),
		AllowedInstallationID: strings.TrimSpace(s.cfg.GitHubAppAllowedInstallationID),
	}
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = defaultGitHubAuthorizeURL
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = defaultGitHubTokenURL
	}
	if cfg.APIBaseURL == "" {
		cfg.APIBaseURL = defaultGitHubAppAPIBaseURL
	}
	if cfg.DisplayName == "" {
		cfg.DisplayName = "Clawcolony GitHub Access"
	}
	if cfg.Org == "" {
		cfg.Org = cfg.RepositoryOwner
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RepositoryID == "" || cfg.RepositoryOwner == "" || cfg.RepositoryName == "" || strings.TrimSpace(s.cfg.GitHubAppTokenEncryptionKey) == "" {
		return gitHubAppAccessConfig{}, false
	}
	return cfg, true
}

func (cfg gitHubAppAccessConfig) orgWorkflowConfigured() bool {
	return strings.TrimSpace(cfg.AppID) != "" &&
		strings.TrimSpace(cfg.PrivateKeyPEM) != "" &&
		strings.TrimSpace(cfg.Org) != "" &&
		strings.TrimSpace(cfg.ContributorTeamSlug) != "" &&
		strings.TrimSpace(cfg.ContributorTeamID) != "" &&
		strings.TrimSpace(cfg.AllowedInstallationID) != ""
}

func (s *Server) gitHubRepoAccessCallbackURI(r *http.Request) string {
	base := strings.TrimSpace(s.cfg.PublicBaseURL)
	if base != "" {
		u, err := neturl.Parse(base)
		if err == nil {
			ref, _ := neturl.Parse(githubRepoAccessCallbackURI)
			return strings.TrimRight(u.ResolveReference(ref).String(), "/")
		}
	}
	return s.absoluteURL(r, githubRepoAccessCallbackURI)
}

func (s *Server) beginGitHubRepoAccess(w http.ResponseWriter, r *http.Request, flow, claimToken, userID, ownerID, callbackRoute string) (string, error) {
	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		return "", fmt.Errorf("github repo access is not configured")
	}
	nonce, err := randomSecret(12)
	if err != nil {
		return "", err
	}
	codeVerifier, err := pkceCodeVerifier()
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().UTC().Add(githubRepoAccessStateTTL)
	state, err := s.signSocialOAuthPayload(gitHubRepoAccessStatePayload{
		Flow:       strings.TrimSpace(flow),
		ClaimToken: strings.TrimSpace(claimToken),
		UserID:     strings.TrimSpace(userID),
		OwnerID:    strings.TrimSpace(ownerID),
		Nonce:      nonce,
		ExpiresAt:  expiresAt.Unix(),
	})
	if err != nil {
		return "", err
	}
	cookieValue, err := s.signSocialOAuthPayload(gitHubRepoAccessCookiePayload{
		Flow:          strings.TrimSpace(flow),
		ClaimToken:    strings.TrimSpace(claimToken),
		UserID:        strings.TrimSpace(userID),
		OwnerID:       strings.TrimSpace(ownerID),
		Nonce:         nonce,
		CodeVerifier:  codeVerifier,
		CallbackRoute: strings.TrimSpace(callbackRoute),
		ExpiresAt:     expiresAt.Unix(),
	})
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     gitHubRepoAccessOAuthCookieName(),
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
	query.Set("redirect_uri", s.gitHubRepoAccessCallbackURI(r))
	query.Set("state", state)
	query.Set("code_challenge", pkceCodeChallenge(codeVerifier))
	query.Set("code_challenge_method", "S256")
	authURL.RawQuery = query.Encode()
	return authURL.String(), nil
}

func (s *Server) readGitHubRepoAccessOAuthCookie(r *http.Request) (gitHubRepoAccessCookiePayload, error) {
	cookie, err := r.Cookie(gitHubRepoAccessOAuthCookieName())
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return gitHubRepoAccessCookiePayload{}, fmt.Errorf("github repo access oauth cookie is missing")
	}
	var payload gitHubRepoAccessCookiePayload
	if err := s.verifySocialOAuthPayload(cookie.Value, &payload); err != nil {
		return gitHubRepoAccessCookiePayload{}, err
	}
	if payload.ExpiresAt < time.Now().UTC().Unix() {
		return gitHubRepoAccessCookiePayload{}, fmt.Errorf("github repo access oauth cookie expired")
	}
	return payload, nil
}

func (s *Server) clearGitHubRepoAccessOAuthCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     gitHubRepoAccessOAuthCookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) writeGitHubRepoAccessCallbackCookie(w http.ResponseWriter, r *http.Request, payload gitHubRepoAccessCallbackCookiePayload) error {
	value, err := s.signSocialOAuthPayload(payload)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     gitHubRepoAccessCallbackCookieName(),
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(payload.ExpiresAt, 0).UTC(),
	})
	return nil
}

func (s *Server) readGitHubRepoAccessCallbackCookie(r *http.Request) (gitHubRepoAccessCallbackCookiePayload, error) {
	cookie, err := r.Cookie(gitHubRepoAccessCallbackCookieName())
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return gitHubRepoAccessCallbackCookiePayload{}, fmt.Errorf("github repo access callback state is required")
	}
	var payload gitHubRepoAccessCallbackCookiePayload
	if err := s.verifySocialOAuthPayload(cookie.Value, &payload); err != nil {
		return gitHubRepoAccessCallbackCookiePayload{}, err
	}
	if payload.ExpiresAt < time.Now().UTC().Unix() {
		return gitHubRepoAccessCallbackCookiePayload{}, fmt.Errorf("github repo access callback state expired")
	}
	return payload, nil
}

func (s *Server) clearGitHubRepoAccessCallbackCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     gitHubRepoAccessCallbackCookieName(),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) encryptGitHubRepoAccessToken(raw string) (string, error) {
	plaintext := strings.TrimSpace(raw)
	if plaintext == "" {
		return "", nil
	}
	key := sha256.Sum256([]byte(strings.TrimSpace(s.cfg.GitHubAppTokenEncryptionKey)))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(crand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	blob := append(nonce, ciphertext...)
	return base64.RawURLEncoding.EncodeToString(blob), nil
}

func (s *Server) decryptGitHubRepoAccessToken(raw string) (string, error) {
	ciphertext := strings.TrimSpace(raw)
	if ciphertext == "" {
		return "", nil
	}
	key := sha256.Sum256([]byte(strings.TrimSpace(s.cfg.GitHubAppTokenEncryptionKey)))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	blob, err := base64.RawURLEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	if len(blob) < gcm.NonceSize() {
		return "", fmt.Errorf("encrypted github token is truncated")
	}
	nonce := blob[:gcm.NonceSize()]
	payload := blob[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

func (s *Server) exchangeGitHubAppCode(ctx context.Context, cfg gitHubAppAccessConfig, code, redirectURI, codeVerifier string) (gitHubAppTokenResponse, error) {
	if _, ok := s.githubOAuthMockProfile(""); ok {
		return gitHubAppTokenResponse{
			AccessToken:           s.githubOAuthMockAccessTokenForCode(code),
			TokenType:             "bearer",
			RefreshToken:          "gh-app-mock-refresh-token",
			ExpiresIn:             int64((8 * time.Hour) / time.Second),
			RefreshTokenExpiresIn: int64((30 * 24 * time.Hour) / time.Second),
		}, nil
	}
	form := neturl.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("code", strings.TrimSpace(code))
	form.Set("redirect_uri", redirectURI)
	form.Set("repository_id", cfg.RepositoryID)
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return gitHubAppTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return gitHubAppTokenResponse{}, fmt.Errorf("github app token exchange failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return gitHubAppTokenResponse{}, fmt.Errorf("github app token exchange failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var token gitHubAppTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return gitHubAppTokenResponse{}, fmt.Errorf("github app token exchange decode failed: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return gitHubAppTokenResponse{}, fmt.Errorf("github app token exchange returned empty access_token")
	}
	return token, nil
}

func (s *Server) refreshGitHubAppToken(ctx context.Context, cfg gitHubAppAccessConfig, refreshToken string) (gitHubAppTokenResponse, error) {
	if _, ok := s.githubOAuthMockProfile(refreshToken); ok || strings.TrimSpace(refreshToken) == "gh-app-mock-refresh-token" {
		return gitHubAppTokenResponse{
			AccessToken:           "gh-mock-access-token-refreshed",
			TokenType:             "bearer",
			RefreshToken:          "gh-app-mock-refresh-token",
			ExpiresIn:             int64((8 * time.Hour) / time.Second),
			RefreshTokenExpiresIn: int64((30 * 24 * time.Hour) / time.Second),
		}, nil
	}
	form := neturl.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("refresh_token", strings.TrimSpace(refreshToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return gitHubAppTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return gitHubAppTokenResponse{}, fmt.Errorf("github app token refresh failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return gitHubAppTokenResponse{}, fmt.Errorf("github app token refresh failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var token gitHubAppTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return gitHubAppTokenResponse{}, fmt.Errorf("github app token refresh decode failed: %w", err)
	}
	if strings.TrimSpace(token.AccessToken) == "" {
		return gitHubAppTokenResponse{}, fmt.Errorf("github app token refresh returned empty access_token")
	}
	return token, nil
}

func (s *Server) gitHubAppUserEmailsURL(cfg gitHubAppAccessConfig) string {
	base := strings.TrimRight(cfg.APIBaseURL, "/")
	return base + "/user/emails"
}

func (s *Server) gitHubAppUserInfoURL(cfg gitHubAppAccessConfig) string {
	base := strings.TrimRight(cfg.APIBaseURL, "/")
	return base + "/user"
}

func (s *Server) fetchGitHubAppVerifiedEmail(ctx context.Context, cfg gitHubAppAccessConfig, accessToken string) (string, error) {
	if profile, ok := s.githubOAuthMockProfile(accessToken); ok {
		return strings.ToLower(strings.TrimSpace(profile.Email)), nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gitHubAppUserEmailsURL(cfg), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("User-Agent", "clawcolony-runtime")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", fmt.Errorf("github app emails request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("github app emails request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
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

func (s *Server) fetchGitHubAppViewer(ctx context.Context, cfg gitHubAppAccessConfig, accessToken string) (githubViewer, error) {
	if profile, ok := s.githubOAuthMockProfile(accessToken); ok {
		return githubViewer{
			ID:    profile.UserID,
			Login: profile.Login,
			Name:  profile.Name,
		}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.gitHubAppUserInfoURL(cfg), nil)
	if err != nil {
		return githubViewer{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	req.Header.Set("User-Agent", "clawcolony-runtime")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return githubViewer{}, fmt.Errorf("github app viewer request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return githubViewer{}, fmt.Errorf("github app viewer request failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var viewer githubViewer
	if err := json.NewDecoder(resp.Body).Decode(&viewer); err != nil {
		return githubViewer{}, err
	}
	if strings.TrimSpace(viewer.Login) == "" {
		return githubViewer{}, fmt.Errorf("github app viewer missing login")
	}
	return viewer, nil
}

func (s *Server) doGitHubAppJSONRequest(ctx context.Context, cfg gitHubAppAccessConfig, method, accessToken, target string, body any, expectedStatuses []int, out any) error {
	base := strings.TrimRight(cfg.APIBaseURL, "/")
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+target, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if strings.TrimSpace(accessToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(accessToken))
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "clawcolony-runtime")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	ok := len(expectedStatuses) == 0
	for _, status := range expectedStatuses {
		if resp.StatusCode == status {
			ok = true
			break
		}
	}
	if !ok {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return gitHubAPIStatusError{
			Path:       target,
			StatusCode: resp.StatusCode,
			Body:       strings.TrimSpace(string(bodyBytes)),
		}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (s *Server) fetchGitHubAppJSON(ctx context.Context, cfg gitHubAppAccessConfig, accessToken, target string, out any) error {
	return s.doGitHubAppJSONRequest(ctx, cfg, http.MethodGet, accessToken, target, nil, []int{http.StatusOK}, out)
}

func parseGitHubAppPrivateKey(raw string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(strings.TrimSpace(raw)))
	if block == nil {
		return nil, fmt.Errorf("github app private key pem is invalid")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	pkcs8, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("github app private key parse failed: %w", err)
	}
	key, ok := pkcs8.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("github app private key is not RSA")
	}
	return key, nil
}

func signGitHubAppJWT(appID, privateKeyPEM string, now time.Time) (string, error) {
	key, err := parseGitHubAppPrivateKey(privateKeyPEM)
	if err != nil {
		return "", err
	}
	headerJSON, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strings.TrimSpace(appID),
	})
	if err != nil {
		return "", err
	}
	encodedHeader := base64.RawURLEncoding.EncodeToString(headerJSON)
	encodedPayload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := encodedHeader + "." + encodedPayload
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(crand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Server) mintGitHubInstallationToken(ctx context.Context, cfg gitHubAppAccessConfig, installationID string) (string, error) {
	if !cfg.orgWorkflowConfigured() {
		return "", fmt.Errorf("github org workflow is not configured")
	}
	jwt, err := signGitHubAppJWT(cfg.AppID, cfg.PrivateKeyPEM, time.Now().UTC())
	if err != nil {
		return "", err
	}
	var resp gitHubInstallationAccessTokenResponse
	path := fmt.Sprintf("/app/installations/%s/access_tokens", neturl.PathEscape(strings.TrimSpace(installationID)))
	if err := s.doGitHubAppJSONRequest(ctx, cfg, http.MethodPost, jwt, path, nil, []int{http.StatusCreated, http.StatusOK}, &resp); err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Token) == "" {
		return "", fmt.Errorf("github installation token response is empty")
	}
	return strings.TrimSpace(resp.Token), nil
}

func mapGitHubPermissionRole(permission string) string {
	switch strings.ToLower(strings.TrimSpace(permission)) {
	case "admin", "maintain":
		return "maintainer"
	case "write":
		return "contributor"
	default:
		return ""
	}
}

func mapGitHubRepositoryRole(repo gitHubRepositoryAccess) string {
	if role := mapGitHubPermissionRole(repo.RoleName); role != "" {
		return role
	}
	switch {
	case repo.Permissions.Admin || repo.Permissions.Maintain:
		return "maintainer"
	case repo.Permissions.Push:
		return "contributor"
	default:
		return ""
	}
}

func gitHubRepoAccessStatusForRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "maintainer":
		return "active_maintainer"
	case "contributor":
		return "active_contributor"
	default:
		return "identity_connected"
	}
}

func (s *Server) newGitHubRepoAccessPayload(email string, viewer githubViewer, cfg gitHubAppAccessConfig) gitHubRepoAccessCallbackCookiePayload {
	return gitHubRepoAccessCallbackCookiePayload{
		Email:           strings.ToLower(strings.TrimSpace(email)),
		GitHubLogin:     strings.TrimSpace(viewer.Login),
		GitHubUserID:    fmt.Sprintf("%d", viewer.ID),
		Mode:            "upstream_direct",
		AccessStatus:    "identity_connected",
		Org:             strings.TrimSpace(cfg.Org),
		RepositoryID:    strings.TrimSpace(cfg.RepositoryID),
		RepositoryOwner: strings.TrimSpace(cfg.RepositoryOwner),
		RepositoryName:  strings.TrimSpace(cfg.RepositoryName),
	}
}

func (s *Server) fetchGitHubDirectRepoRole(ctx context.Context, cfg gitHubAppAccessConfig, accessToken, viewerLogin string) (string, string, error) {
	var installs gitHubUserInstallationsResponse
	if err := s.fetchGitHubAppJSON(ctx, cfg, accessToken, "/user/installations", &installs); err != nil {
		return "", "", err
	}
	targetFullName := strings.ToLower(strings.TrimSpace(cfg.RepositoryOwner + "/" + cfg.RepositoryName))
	installationID := ""
	foundRepo := false
	for _, installation := range installs.Installations {
		if cfg.AllowedInstallationID != "" && cfg.AllowedInstallationID != strconv.FormatInt(installation.ID, 10) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(installation.Account.Login), cfg.RepositoryOwner) {
			continue
		}
		var repos gitHubInstallationRepositoriesResponse
		path := fmt.Sprintf("/user/installations/%d/repositories", installation.ID)
		if err := s.fetchGitHubAppJSON(ctx, cfg, accessToken, path, &repos); err != nil {
			continue
		}
		for _, repo := range repos.Repositories {
			if !strings.EqualFold(strings.TrimSpace(repo.FullName), targetFullName) && strings.TrimSpace(cfg.RepositoryID) != strconv.FormatInt(repo.ID, 10) {
				continue
			}
			foundRepo = true
			installationID = strconv.FormatInt(installation.ID, 10)
			break
		}
		if foundRepo {
			break
		}
	}
	if !foundRepo {
		return "", "", nil
	}
	var repo gitHubRepositoryAccess
	repoPath := fmt.Sprintf("/repos/%s/%s", neturl.PathEscape(cfg.RepositoryOwner), neturl.PathEscape(cfg.RepositoryName))
	if err := s.fetchGitHubAppJSON(ctx, cfg, accessToken, repoPath, &repo); err != nil {
		var apiErr gitHubAPIStatusError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return "", installationID, nil
		}
		return "", installationID, err
	}
	return mapGitHubRepositoryRole(repo), installationID, nil
}

func isGitHubStatus(err error, statusCode int) bool {
	var apiErr gitHubAPIStatusError
	return errors.As(err, &apiErr) && apiErr.StatusCode == statusCode
}

func (s *Server) fetchGitHubOrgMembership(ctx context.Context, cfg gitHubAppAccessConfig, installationToken, username string) (gitHubOrgMembershipResponse, error) {
	var out gitHubOrgMembershipResponse
	path := fmt.Sprintf("/orgs/%s/memberships/%s", neturl.PathEscape(cfg.Org), neturl.PathEscape(strings.TrimSpace(username)))
	err := s.doGitHubAppJSONRequest(ctx, cfg, http.MethodGet, installationToken, path, nil, []int{http.StatusOK}, &out)
	return out, err
}

func (s *Server) inviteGitHubUserToOrg(ctx context.Context, cfg gitHubAppAccessConfig, installationToken string, userID int64) error {
	path := fmt.Sprintf("/orgs/%s/invitations", neturl.PathEscape(cfg.Org))
	body := map[string]any{
		"invitee_id": userID,
		"team_ids":   []int64{},
	}
	if teamID, err := strconv.ParseInt(strings.TrimSpace(cfg.ContributorTeamID), 10, 64); err == nil && teamID > 0 {
		body["team_ids"] = []int64{teamID}
	}
	err := s.doGitHubAppJSONRequest(ctx, cfg, http.MethodPost, installationToken, path, body, []int{http.StatusCreated}, nil)
	if isGitHubStatus(err, http.StatusUnprocessableEntity) {
		return nil
	}
	return err
}

func (s *Server) activateGitHubOrgMembership(ctx context.Context, cfg gitHubAppAccessConfig, accessToken string) (string, error) {
	var out gitHubOrgMembershipResponse
	path := fmt.Sprintf("/user/memberships/orgs/%s", neturl.PathEscape(cfg.Org))
	body := map[string]any{"state": "active"}
	if err := s.doGitHubAppJSONRequest(ctx, cfg, http.MethodPatch, accessToken, path, body, []int{http.StatusOK}, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.State), nil
}

func (s *Server) ensureGitHubContributorTeamMembership(ctx context.Context, cfg gitHubAppAccessConfig, installationToken, username string) error {
	path := fmt.Sprintf("/orgs/%s/teams/%s/memberships/%s", neturl.PathEscape(cfg.Org), neturl.PathEscape(cfg.ContributorTeamSlug), neturl.PathEscape(strings.TrimSpace(username)))
	body := map[string]any{"role": "member"}
	return s.doGitHubAppJSONRequest(ctx, cfg, http.MethodPut, installationToken, path, body, []int{http.StatusOK, http.StatusCreated}, nil)
}

func (s *Server) resolveGitHubOrgTeamAccess(ctx context.Context, cfg gitHubAppAccessConfig, accessToken string, payload gitHubRepoAccessCallbackCookiePayload) (gitHubRepoAccessCallbackCookiePayload, error) {
	payload.Mode = "upstream_via_org_team"
	payload.Org = strings.TrimSpace(cfg.Org)
	payload.TeamSlug = strings.TrimSpace(cfg.ContributorTeamSlug)
	if !cfg.orgWorkflowConfigured() {
		payload.AccessStatus = "identity_connected"
		payload.NextAction = "contact_admin"
		payload.BlockingReason = "org_membership_workflow_not_configured"
		return payload, nil
	}
	installationToken, err := s.mintGitHubInstallationToken(ctx, cfg, cfg.AllowedInstallationID)
	if err != nil {
		return payload, err
	}
	membership, err := s.fetchGitHubOrgMembership(ctx, cfg, installationToken, payload.GitHubLogin)
	switch {
	case err == nil:
		payload.OrgMembership = strings.TrimSpace(membership.State)
	case isGitHubStatus(err, http.StatusNotFound):
		payload.OrgMembership = "not_member"
	default:
		return payload, err
	}
	if payload.OrgMembership == "not_member" {
		userID, parseErr := strconv.ParseInt(strings.TrimSpace(payload.GitHubUserID), 10, 64)
		if parseErr != nil || userID <= 0 {
			return payload, fmt.Errorf("github user id is invalid for org invitation")
		}
		if err := s.inviteGitHubUserToOrg(ctx, cfg, installationToken, userID); err != nil {
			return payload, err
		}
		payload.OrgMembership = "pending"
		payload.AccessStatus = "org_invitation_pending"
		payload.NextAction = "retry_activation"
		payload.BlockingReason = "org_membership_not_active"
	}
	if !strings.EqualFold(strings.TrimSpace(payload.OrgMembership), "active") {
		if payload.AccessStatus == "" || payload.AccessStatus == "identity_connected" {
			if strings.EqualFold(strings.TrimSpace(payload.OrgMembership), "pending") {
				payload.AccessStatus = "org_invitation_pending"
			} else {
				payload.AccessStatus = "membership_activation_required"
			}
		}
		if payload.NextAction == "" {
			payload.NextAction = "retry_activation"
		}
		if payload.BlockingReason == "" {
			payload.BlockingReason = "org_membership_not_active"
		}
		state, err := s.activateGitHubOrgMembership(ctx, cfg, accessToken)
		if err != nil {
			return payload, nil
		}
		payload.OrgMembership = firstNonEmpty(state, "active")
	}
	if err := s.ensureGitHubContributorTeamMembership(ctx, cfg, installationToken, payload.GitHubLogin); err != nil {
		return payload, err
	}
	role, installationID, err := s.fetchGitHubDirectRepoRole(ctx, cfg, accessToken, payload.GitHubLogin)
	if err != nil {
		return payload, err
	}
	if role == "" {
		payload.AccessStatus = "team_membership_pending"
		payload.NextAction = "wait_for_org_membership"
		payload.BlockingReason = "upstream_write_not_active"
		payload.InstallationID = firstNonEmpty(strings.TrimSpace(installationID), strings.TrimSpace(cfg.AllowedInstallationID))
		return payload, nil
	}
	payload.Role = role
	payload.InstallationID = firstNonEmpty(strings.TrimSpace(installationID), strings.TrimSpace(cfg.AllowedInstallationID))
	payload.AccessStatus = gitHubRepoAccessStatusForRole(role)
	payload.NextAction = "none"
	payload.BlockingReason = ""
	return payload, nil
}

func (s *Server) resolveGitHubRepoAccess(ctx context.Context, cfg gitHubAppAccessConfig, accessToken string) (gitHubRepoAccessCallbackCookiePayload, error) {
	if profile, ok := s.githubOAuthMockProfile(accessToken); ok {
		return gitHubRepoAccessCallbackCookiePayload{
			Email:           strings.ToLower(strings.TrimSpace(profile.Email)),
			GitHubLogin:     strings.TrimSpace(profile.Login),
			GitHubUserID:    fmt.Sprintf("%d", profile.UserID),
			Mode:            "upstream_direct",
			AccessStatus:    "active_contributor",
			Org:             firstNonEmpty(strings.TrimSpace(cfg.Org), strings.TrimSpace(cfg.RepositoryOwner)),
			OrgMembership:   "active",
			RepositoryID:    strings.TrimSpace(cfg.RepositoryID),
			RepositoryOwner: strings.TrimSpace(cfg.RepositoryOwner),
			RepositoryName:  strings.TrimSpace(cfg.RepositoryName),
			InstallationID:  firstNonEmpty(strings.TrimSpace(cfg.AllowedInstallationID), "99999"),
			Role:            "contributor",
			NextAction:      "none",
		}, nil
	}
	viewer, err := s.fetchGitHubAppViewer(ctx, cfg, accessToken)
	if err != nil {
		return gitHubRepoAccessCallbackCookiePayload{}, err
	}
	email, err := s.fetchGitHubAppVerifiedEmail(ctx, cfg, accessToken)
	if err != nil {
		return gitHubRepoAccessCallbackCookiePayload{}, err
	}
	payload := s.newGitHubRepoAccessPayload(email, viewer, cfg)
	role, installationID, err := s.fetchGitHubDirectRepoRole(ctx, cfg, accessToken, viewer.Login)
	if err != nil {
		return gitHubRepoAccessCallbackCookiePayload{}, err
	}
	if role != "" {
		payload.Role = role
		payload.InstallationID = installationID
		payload.AccessStatus = gitHubRepoAccessStatusForRole(role)
		payload.OrgMembership = "active"
		payload.NextAction = "none"
		payload.BlockingReason = ""
		return payload, nil
	}
	return s.resolveGitHubOrgTeamAccess(ctx, cfg, accessToken, payload)
}

func (s *Server) saveGitHubRepoAccessGrant(ctx context.Context, owner store.HumanOwner, payload gitHubRepoAccessCallbackCookiePayload, token gitHubAppTokenResponse) (store.GitHubRepoAccessGrant, error) {
	accessCiphertext, err := s.encryptGitHubRepoAccessToken(token.AccessToken)
	if err != nil {
		return store.GitHubRepoAccessGrant{}, err
	}
	refreshCiphertext, err := s.encryptGitHubRepoAccessToken(token.RefreshToken)
	if err != nil {
		return store.GitHubRepoAccessGrant{}, err
	}
	now := time.Now().UTC()
	grant := store.GitHubRepoAccessGrant{
		OwnerID:                owner.OwnerID,
		GitHubUserID:           payload.GitHubUserID,
		GitHubUsername:         payload.GitHubLogin,
		Mode:                   payload.Mode,
		AccessStatus:           payload.AccessStatus,
		Org:                    payload.Org,
		OrgMembershipStatus:    payload.OrgMembership,
		TeamSlug:               payload.TeamSlug,
		NextAction:             payload.NextAction,
		BlockingReason:         payload.BlockingReason,
		InstallationID:         payload.InstallationID,
		RepositoryID:           payload.RepositoryID,
		RepositoryOwner:        payload.RepositoryOwner,
		RepositoryName:         payload.RepositoryName,
		Role:                   payload.Role,
		AccessTokenCiphertext:  accessCiphertext,
		RefreshTokenCiphertext: refreshCiphertext,
		GrantedAt:              now,
		LastVerifiedAt:         &now,
	}
	if token.ExpiresIn > 0 {
		expiresAt := now.Add(time.Duration(token.ExpiresIn) * time.Second)
		grant.AccessExpiresAt = &expiresAt
	}
	if token.RefreshTokenExpiresIn > 0 {
		refreshExpiresAt := now.Add(time.Duration(token.RefreshTokenExpiresIn) * time.Second)
		grant.RefreshExpiresAt = &refreshExpiresAt
	}
	return s.store.UpsertGitHubRepoAccessGrant(ctx, grant)
}

func githubRepoAccessCapabilities(role, status string) []string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active_maintainer", "active_contributor":
	default:
		return []string{}
	}
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "maintainer":
		return []string{"branch", "push", "pull_request", "merge"}
	case "contributor":
		return []string{"branch", "push", "pull_request"}
	default:
		return []string{}
	}
}

func githubRepoAccessInactivePayload(cfg gitHubAppAccessConfig) map[string]any {
	return map[string]any{
		"status":       "not_connected",
		"mode":         "upstream_direct",
		"org":          strings.TrimSpace(cfg.Org),
		"next_action":  "none",
		"team":         map[string]any{"slug": strings.TrimSpace(cfg.ContributorTeamSlug)},
		"display_name": cfg.DisplayName,
		"repository": map[string]any{
			"id":        cfg.RepositoryID,
			"owner":     cfg.RepositoryOwner,
			"name":      cfg.RepositoryName,
			"full_name": strings.TrimSpace(cfg.RepositoryOwner + "/" + cfg.RepositoryName),
		},
	}
}

func callbackRouteWithQuery(target string, values neturl.Values) string {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return ""
	}
	u, err := neturl.Parse(trimmed)
	if err != nil {
		return trimmed
	}
	query := u.Query()
	for key, vals := range values {
		if len(vals) == 0 {
			continue
		}
		query.Del(key)
		for _, value := range vals {
			query.Add(key, value)
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *Server) gitHubRepoAccessCallbackTarget(state gitHubRepoAccessStatePayload, callbackRoute string, values neturl.Values) string {
	if strings.EqualFold(strings.TrimSpace(state.Flow), "claim") && strings.TrimSpace(state.ClaimToken) != "" {
		return s.claimFrontendCallbackURL(state.ClaimToken, values)
	}
	if route := callbackRouteWithQuery(callbackRoute, values); route != "" {
		return route
	}
	if strings.EqualFold(strings.TrimSpace(state.Flow), "social") {
		return s.socialCallbackRedirectURL("github", values)
	}
	return s.gitHubAccessFrontendCallbackURL(values)
}

func (s *Server) completeGitHubAppSocialConnect(ctx context.Context, owner store.HumanOwner, userID string, payload gitHubRepoAccessCallbackCookiePayload, grant store.GitHubRepoAccessGrant) (map[string]any, error) {
	link, grants, err := s.completeGitHubSocialLinkAndRewards(ctx, owner, userID, payload.GitHubLogin, payload.GitHubUserID, payload.Starred, payload.Forked, "social.github.app")
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"item":          link,
		"grants":        grants,
		"starred":       payload.Starred,
		"forked":        payload.Forked,
		"repo":          s.officialGitHubRepo(),
		"username":      payload.GitHubLogin,
		"owner":         owner,
		"github_access": s.gitHubRepoAccessStatusPayload(grant),
	}, nil
}

func githubRepoAccessStatus(grant store.GitHubRepoAccessGrant) string {
	if strings.TrimSpace(grant.OwnerID) == "" {
		return "not_connected"
	}
	if grant.RevokedAt != nil {
		return "reauthorization_required"
	}
	if status := strings.TrimSpace(grant.AccessStatus); status != "" {
		return status
	}
	switch strings.ToLower(strings.TrimSpace(grant.Role)) {
	case "maintainer":
		return "active_maintainer"
	case "contributor":
		return "active_contributor"
	default:
		return "identity_connected"
	}
}

func (s *Server) gitHubRepoAccessStatusPayload(grant store.GitHubRepoAccessGrant) map[string]any {
	status := githubRepoAccessStatus(grant)
	return map[string]any{
		"status":                status,
		"github_username":       strings.TrimSpace(grant.GitHubUsername),
		"github_user_id":        strings.TrimSpace(grant.GitHubUserID),
		"mode":                  strings.TrimSpace(grant.Mode),
		"org":                   strings.TrimSpace(grant.Org),
		"org_membership_status": strings.TrimSpace(grant.OrgMembershipStatus),
		"team": map[string]any{
			"slug": strings.TrimSpace(grant.TeamSlug),
		},
		"next_action":     strings.TrimSpace(grant.NextAction),
		"blocking_reason": strings.TrimSpace(grant.BlockingReason),
		"repository": map[string]any{
			"id":        strings.TrimSpace(grant.RepositoryID),
			"owner":     strings.TrimSpace(grant.RepositoryOwner),
			"name":      strings.TrimSpace(grant.RepositoryName),
			"full_name": strings.TrimSpace(grant.RepositoryOwner + "/" + grant.RepositoryName),
		},
		"installation_id":  strings.TrimSpace(grant.InstallationID),
		"role":             strings.TrimSpace(grant.Role),
		"capabilities":     githubRepoAccessCapabilities(grant.Role, status),
		"display_name":     strings.TrimSpace(s.cfg.GitHubAppDisplayName),
		"via_app_note":     "PR/merge API actions are attributed to the user, and GitHub may still display via app metadata.",
		"last_verified_at": grant.LastVerifiedAt,
		"granted_at":       grant.GrantedAt,
	}
}

func (s *Server) gitHubRepoAccessTokenPayload(grant store.GitHubRepoAccessGrant, accessToken string) map[string]any {
	return map[string]any{
		"access_token":         strings.TrimSpace(accessToken),
		"access_expires_at":    grant.AccessExpiresAt,
		"repository_full_name": strings.TrimSpace(grant.RepositoryOwner + "/" + grant.RepositoryName),
		"role":                 strings.TrimSpace(grant.Role),
	}
}

func (s *Server) ensureGitHubRepoAccessGrant(ctx context.Context, ownerID string) (store.GitHubRepoAccessGrant, error) {
	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		return store.GitHubRepoAccessGrant{}, fmt.Errorf("github repo access is not configured")
	}
	grant, err := s.store.GetGitHubRepoAccessGrant(ctx, ownerID)
	if err != nil {
		return store.GitHubRepoAccessGrant{}, err
	}
	if grant.RevokedAt != nil {
		return grant, nil
	}
	status := githubRepoAccessStatus(grant)
	if (status == "active_contributor" || status == "active_maintainer") && (grant.AccessExpiresAt == nil || grant.AccessExpiresAt.After(time.Now().UTC().Add(2*time.Minute))) {
		return grant, nil
	}
	refreshToken, err := s.decryptGitHubRepoAccessToken(grant.RefreshTokenCiphertext)
	if err != nil {
		return store.GitHubRepoAccessGrant{}, err
	}
	accessToken, err := s.decryptGitHubRepoAccessToken(grant.AccessTokenCiphertext)
	if err != nil {
		return store.GitHubRepoAccessGrant{}, err
	}
	token := gitHubAppTokenResponse{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}
	if grant.AccessExpiresAt != nil {
		token.ExpiresIn = int64(time.Until(grant.AccessExpiresAt.UTC()).Seconds())
	}
	if grant.RefreshExpiresAt != nil {
		token.RefreshTokenExpiresIn = int64(time.Until(grant.RefreshExpiresAt.UTC()).Seconds())
	}
	if strings.TrimSpace(accessToken) == "" || grant.AccessExpiresAt == nil || !grant.AccessExpiresAt.After(time.Now().UTC().Add(2*time.Minute)) {
		if strings.TrimSpace(refreshToken) == "" {
			return grant, nil
		}
		token, err = s.refreshGitHubAppToken(ctx, cfg, refreshToken)
		if err != nil {
			return s.store.RevokeGitHubRepoAccessGrant(ctx, ownerID, time.Now().UTC())
		}
	}
	owner, err := s.store.GetHumanOwner(ctx, ownerID)
	if err != nil {
		return store.GitHubRepoAccessGrant{}, err
	}
	payload, err := s.resolveGitHubRepoAccess(ctx, cfg, token.AccessToken)
	if err != nil {
		return store.GitHubRepoAccessGrant{}, err
	}
	payload.OwnerID = owner.OwnerID
	payload.Email = firstNonEmpty(payload.Email, owner.Email)
	return s.saveGitHubRepoAccessGrant(ctx, owner, payload, token)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (s *Server) gitHubAccessFrontendCallbackURL(values neturl.Values) string {
	u := &neturl.URL{Path: "/github-access/callback"}
	u.RawQuery = values.Encode()
	return u.String()
}

func (s *Server) gitHubRepoAccessInactivePayloadWithLegacyIdentity(ctx context.Context, cfg gitHubAppAccessConfig, ownerID string) map[string]any {
	payload := githubRepoAccessInactivePayload(cfg)
	owner, err := s.store.GetHumanOwner(ctx, ownerID)
	if err != nil {
		return payload
	}
	if githubUsername := strings.TrimSpace(owner.GitHubUsername); githubUsername != "" {
		payload["github_username"] = githubUsername
		payload["github_user_id"] = strings.TrimSpace(owner.GitHubUserID)
		payload["legacy_social_data_detected"] = true
		payload["next_action"] = "complete GitHub App authorization to connect repo access"
	}
	return payload
}

func (s *Server) gitHubRepoAccessReauthorizeURL(r *http.Request, ownerID, userID string) (string, error) {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return "", fmt.Errorf("owner id is required")
	}
	value, err := s.signSocialOAuthPayload(gitHubRepoAccessReauthorizePayload{
		OwnerID:   ownerID,
		UserID:    strings.TrimSpace(userID),
		ExpiresAt: time.Now().UTC().Add(githubRepoAccessReauthTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	target := &neturl.URL{Path: githubRepoAccessReauthURI}
	query := target.Query()
	query.Set("token", value)
	target.RawQuery = query.Encode()
	if base := strings.TrimSpace(s.cfg.PublicBaseURL); base != "" {
		u, err := neturl.Parse(base)
		if err == nil {
			return u.ResolveReference(target).String(), nil
		}
	}
	return s.absoluteURL(r, target.String()), nil
}

func (s *Server) augmentGitHubRepoAccessReauthorizePayload(r *http.Request, payload map[string]any, ownerID, userID string) map[string]any {
	if strings.TrimSpace(ownerID) == "" {
		return payload
	}
	reauthorizeURL, err := s.gitHubRepoAccessReauthorizeURL(r, ownerID, userID)
	if err != nil || strings.TrimSpace(reauthorizeURL) == "" {
		return payload
	}
	payload["reauthorize_url"] = reauthorizeURL
	nextAction := strings.TrimSpace(fmt.Sprintf("%v", payload["next_action"]))
	if nextAction == "" || nextAction == "none" {
		payload["next_action"] = "open reauthorize_url in a browser, complete GitHub approval, then retry /api/v1/github-access/token"
	}
	return payload
}

func (s *Server) writeGitHubRepoAccessCallbackError(w http.ResponseWriter, r *http.Request, state gitHubRepoAccessStatePayload, callbackRoute, msg string) {
	s.clearGitHubRepoAccessOAuthCookie(w, r)
	s.clearGitHubRepoAccessCallbackCookie(w, r)
	values := neturl.Values{}
	values.Set("status", "error")
	values.Set("error", msg)
	if strings.EqualFold(strings.TrimSpace(state.Flow), "social") {
		values.Set("provider", "github")
		if userID := strings.TrimSpace(state.UserID); userID != "" {
			values.Set("user_id", userID)
		}
	}
	target := s.gitHubRepoAccessCallbackTarget(state, callbackRoute, values)
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleGitHubRepoAccessStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, err := s.currentOwnerSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "github repo access is not configured")
		return
	}
	grant, err := s.ensureGitHubRepoAccessGrant(r.Context(), session.OwnerID)
	if err != nil {
		if errors.Is(err, store.ErrGitHubRepoAccessGrantNotFound) {
			writeJSON(w, http.StatusOK, s.gitHubRepoAccessInactivePayloadWithLegacyIdentity(r.Context(), cfg, session.OwnerID))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.gitHubRepoAccessStatusPayload(grant))
}

func (s *Server) handleGitHubRepoAccessStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, err := s.currentOwnerSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	authorizeURL, err := s.beginGitHubRepoAccess(w, r, "owner", "", "", session.OwnerID, s.gitHubAccessFrontendCallbackURL(nil))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not configured") {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	cfg, _ := s.gitHubAppAccessConfig()
	writeJSON(w, http.StatusAccepted, map[string]any{
		"authorize_url": authorizeURL,
		"display_name":  cfg.DisplayName,
		"mode":          "upstream_direct",
		"org":           strings.TrimSpace(cfg.Org),
		"team":          map[string]any{"slug": strings.TrimSpace(cfg.ContributorTeamSlug)},
		"repository": map[string]any{
			"id":        cfg.RepositoryID,
			"owner":     cfg.RepositoryOwner,
			"name":      cfg.RepositoryName,
			"full_name": strings.TrimSpace(cfg.RepositoryOwner + "/" + cfg.RepositoryName),
		},
	})
}

func (s *Server) handleGitHubRepoAccessReauthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rawToken := strings.TrimSpace(r.URL.Query().Get("token"))
	if rawToken == "" {
		writeError(w, http.StatusBadRequest, "reauthorize token is required")
		return
	}
	var payload gitHubRepoAccessReauthorizePayload
	if err := s.verifySocialOAuthPayload(rawToken, &payload); err != nil {
		writeError(w, http.StatusBadRequest, "reauthorize token is invalid")
		return
	}
	if payload.ExpiresAt < time.Now().UTC().Unix() {
		writeError(w, http.StatusBadRequest, "reauthorize token expired")
		return
	}
	if strings.TrimSpace(payload.OwnerID) == "" {
		writeError(w, http.StatusBadRequest, "reauthorize token is invalid")
		return
	}
	if _, err := s.store.GetHumanOwner(r.Context(), payload.OwnerID); err != nil {
		writeError(w, http.StatusBadRequest, "reauthorize token is invalid")
		return
	}
	if strings.TrimSpace(payload.UserID) != "" {
		binding, err := s.store.GetAgentHumanBinding(r.Context(), strings.TrimSpace(payload.UserID))
		if err != nil || strings.TrimSpace(binding.OwnerID) != strings.TrimSpace(payload.OwnerID) {
			writeError(w, http.StatusBadRequest, "reauthorize token is invalid")
			return
		}
	}
	authorizeURL, err := s.beginGitHubRepoAccess(w, r, "owner", "", "", payload.OwnerID, s.gitHubAccessFrontendCallbackURL(nil))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not configured") {
			writeError(w, http.StatusServiceUnavailable, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusSeeOther)
}

func (s *Server) handleGitHubRepoAccessToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := s.requireAuthOnlyCurrentUser(w, r)
	if !ok {
		return
	}
	cfg, configured := s.gitHubAppAccessConfig()
	if !configured {
		writeError(w, http.StatusServiceUnavailable, "github repo access is not configured")
		return
	}
	binding, err := s.store.GetAgentHumanBinding(r.Context(), userID)
	if err != nil {
		if errors.Is(err, store.ErrAgentHumanBindingNotFound) {
			writeError(w, http.StatusConflict, "agent is not claimed by a human owner")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	grant, err := s.ensureGitHubRepoAccessGrant(r.Context(), binding.OwnerID)
	if err != nil {
		if errors.Is(err, store.ErrGitHubRepoAccessGrantNotFound) {
			payload := s.gitHubRepoAccessInactivePayloadWithLegacyIdentity(r.Context(), cfg, binding.OwnerID)
			writeJSON(w, http.StatusConflict, s.augmentGitHubRepoAccessReauthorizePayload(r, payload, binding.OwnerID, userID))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := githubRepoAccessStatus(grant)
	if status != "active_contributor" && status != "active_maintainer" {
		writeJSON(w, http.StatusConflict, s.augmentGitHubRepoAccessReauthorizePayload(r, s.gitHubRepoAccessStatusPayload(grant), binding.OwnerID, userID))
		return
	}
	accessToken, err := s.decryptGitHubRepoAccessToken(grant.AccessTokenCiphertext)
	if err != nil {
		payload := s.gitHubRepoAccessStatusPayload(grant)
		payload["status"] = "reauthorization_required"
		payload["capabilities"] = githubRepoAccessCapabilities(grant.Role, "reauthorization_required")
		payload["blocking_reason"] = "github_access_token_unavailable"
		payload["error"] = "failed to decrypt github access token"
		writeJSON(w, http.StatusConflict, s.augmentGitHubRepoAccessReauthorizePayload(r, payload, binding.OwnerID, userID))
		return
	}
	if strings.TrimSpace(accessToken) == "" {
		payload := s.gitHubRepoAccessStatusPayload(grant)
		payload["status"] = "reauthorization_required"
		payload["capabilities"] = githubRepoAccessCapabilities(grant.Role, "reauthorization_required")
		payload["blocking_reason"] = "github_access_token_unavailable"
		payload["error"] = "github access token is unavailable"
		writeJSON(w, http.StatusConflict, s.augmentGitHubRepoAccessReauthorizePayload(r, payload, binding.OwnerID, userID))
		return
	}
	writeJSON(w, http.StatusOK, s.gitHubRepoAccessTokenPayload(grant, accessToken))
}

func (s *Server) handleGitHubRepoAccessDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	session, err := s.currentOwnerSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	grant, err := s.store.RevokeGitHubRepoAccessGrant(r.Context(), session.OwnerID, time.Now().UTC())
	if err != nil && !errors.Is(err, store.ErrGitHubRepoAccessGrantNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if errors.Is(err, store.ErrGitHubRepoAccessGrantNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "not_connected"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": githubRepoAccessStatus(grant)})
}

func (s *Server) handleGitHubRepoAccessCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rawState := strings.TrimSpace(r.URL.Query().Get("state"))
	var state gitHubRepoAccessStatePayload
	if rawState != "" {
		_ = s.verifySocialOAuthPayload(rawState, &state)
	}
	var cookiePayload gitHubRepoAccessCookiePayload
	if payload, err := s.readGitHubRepoAccessOAuthCookie(r); err == nil {
		cookiePayload = payload
	}
	if providerErr := strings.TrimSpace(r.URL.Query().Get("error")); providerErr != "" {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, providerErr)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if rawState == "" || code == "" {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, "oauth callback requires code and state")
		return
	}
	if err := s.verifySocialOAuthPayload(rawState, &state); err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	if state.ExpiresAt < time.Now().UTC().Unix() {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, "oauth state expired")
		return
	}
	cookiePayload, err := s.readGitHubRepoAccessOAuthCookie(r)
	if err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, "", err.Error())
		return
	}
	if cookiePayload.Flow != state.Flow || cookiePayload.ClaimToken != state.ClaimToken || cookiePayload.UserID != state.UserID || cookiePayload.OwnerID != state.OwnerID || cookiePayload.Nonce != state.Nonce {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, "oauth cookie mismatch")
		return
	}
	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, "github repo access is not configured")
		return
	}
	if strings.EqualFold(strings.TrimSpace(state.Flow), "claim") {
		if _, err := s.getClaimRegistration(r.Context(), state.ClaimToken); err != nil {
			s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, s.claimLookupErrorMessage(err))
			return
		}
	} else if strings.EqualFold(strings.TrimSpace(state.Flow), "owner") || strings.EqualFold(strings.TrimSpace(state.Flow), "social") {
		if _, err := s.store.GetHumanOwner(r.Context(), state.OwnerID); err != nil {
			s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, "owner session is invalid")
			return
		}
		if strings.EqualFold(strings.TrimSpace(state.Flow), "social") {
			binding, err := s.store.GetAgentHumanBinding(r.Context(), state.UserID)
			if err != nil {
				s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
				return
			}
			if strings.TrimSpace(binding.OwnerID) != strings.TrimSpace(state.OwnerID) {
				s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, "owner session does not own this agent")
				return
			}
		}
	} else {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, "unsupported github access flow")
		return
	}
	token, err := s.exchangeGitHubAppCode(r.Context(), cfg, code, s.gitHubRepoAccessCallbackURI(r), cookiePayload.CodeVerifier)
	if err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	resolved, err := s.resolveGitHubRepoAccess(r.Context(), cfg, token.AccessToken)
	if err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	var owner store.HumanOwner
	if strings.EqualFold(strings.TrimSpace(state.Flow), "owner") {
		owner, err = s.store.GetHumanOwner(r.Context(), state.OwnerID)
	} else if strings.EqualFold(strings.TrimSpace(state.Flow), "social") {
		owner, err = s.store.GetHumanOwner(r.Context(), state.OwnerID)
	} else {
		owner, err = s.store.GetHumanOwnerByEmail(r.Context(), resolved.Email)
		if errors.Is(err, store.ErrHumanOwnerNotFound) {
			owner, err = s.store.UpsertHumanOwner(r.Context(), resolved.Email, resolved.GitHubLogin)
		}
	}
	if err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	owner, err = s.store.UpsertHumanOwnerSocialIdentity(r.Context(), owner.OwnerID, "github", resolved.GitHubLogin, resolved.GitHubUserID)
	if err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	savedGrant, err := s.saveGitHubRepoAccessGrant(r.Context(), owner, resolved, token)
	if err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	starred, err := s.verifyGitHubStar(r.Context(), resolved.GitHubLogin)
	if err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	forked, err := s.verifyGitHubFork(r.Context(), resolved.GitHubLogin)
	if err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	resolved.Flow = state.Flow
	resolved.ClaimToken = state.ClaimToken
	resolved.UserID = state.UserID
	resolved.OwnerID = owner.OwnerID
	resolved.Starred = starred
	resolved.Forked = forked
	resolved.ExpiresAt = time.Now().UTC().Add(githubRepoAccessResultTTL).Unix()
	if err := s.writeGitHubRepoAccessCallbackCookie(w, r, resolved); err != nil {
		s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
		return
	}
	s.clearGitHubRepoAccessOAuthCookie(w, r)
	if strings.EqualFold(strings.TrimSpace(state.Flow), "social") {
		payload, err := s.completeGitHubAppSocialConnect(r.Context(), owner, state.UserID, resolved, savedGrant)
		if err != nil {
			s.writeGitHubRepoAccessCallbackError(w, r, state, cookiePayload.CallbackRoute, err.Error())
			return
		}
		payload["provider"] = "github"
		payload["user_id"] = state.UserID
		if wantsJSON(r) {
			writeJSON(w, http.StatusOK, payload)
			return
		}
		values := neturl.Values{}
		values.Set("provider", "github")
		values.Set("status", "ok")
		values.Set("user_id", state.UserID)
		http.Redirect(w, r, s.gitHubRepoAccessCallbackTarget(state, cookiePayload.CallbackRoute, values), http.StatusSeeOther)
		return
	}
	values := neturl.Values{}
	values.Set("status", "ok")
	values.Set("github_username", resolved.GitHubLogin)
	values.Set("repo", strings.TrimSpace(savedGrant.RepositoryOwner+"/"+savedGrant.RepositoryName))
	values.Set("role", savedGrant.Role)
	values.Set("github_access_status", githubRepoAccessStatus(savedGrant))
	if strings.TrimSpace(savedGrant.Mode) != "" {
		values.Set("mode", savedGrant.Mode)
	}
	if strings.EqualFold(strings.TrimSpace(state.Flow), "claim") {
		http.Redirect(w, r, s.gitHubRepoAccessCallbackTarget(state, cookiePayload.CallbackRoute, values), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, s.gitHubRepoAccessCallbackTarget(state, cookiePayload.CallbackRoute, values), http.StatusSeeOther)
}
