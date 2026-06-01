package server

import (
	"hash/fnv"
	"regexp"
	"strings"
)

const githubOAuthMockAccessTokenPrefix = "gh-mock-access-token"

var githubOAuthMockSlugRE = regexp.MustCompile(`[^a-z0-9-]+`)

type githubOAuthMockProfile struct {
	UserID  int64
	Login   string
	Name    string
	Email   string
	Starred bool
	Forked  bool
}

func (s *Server) githubOAuthMockAccessTokenForCode(code string) string {
	slug := githubOAuthMockSlug(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(code)), "gh-code"))
	if slug == "" {
		return githubOAuthMockAccessTokenPrefix
	}
	return githubOAuthMockAccessTokenPrefix + ":" + slug
}

func (s *Server) githubOAuthMockProfile(accessToken string) (githubOAuthMockProfile, bool) {
	if !s.cfg.GitHubAPIMockEnabled || !s.cfg.GitHubAPIMockAllowUnsafeLocal {
		return githubOAuthMockProfile{}, false
	}
	login := strings.TrimSpace(s.cfg.GitHubAPIMockLogin)
	if login == "" {
		login = "octo"
	}
	if slug := githubOAuthMockAccessTokenSlug(accessToken); slug != "" {
		login = slug
	}
	name := strings.TrimSpace(s.cfg.GitHubAPIMockName)
	if name == "" {
		name = "Octo Human"
	}
	if slug := githubOAuthMockAccessTokenSlug(accessToken); slug != "" {
		name = "Mock " + slug
	}
	email := strings.ToLower(strings.TrimSpace(s.cfg.GitHubAPIMockEmail))
	if email == "" {
		email = login + "@example.com"
	}
	if slug := githubOAuthMockAccessTokenSlug(accessToken); slug != "" {
		email = slug + "@example.com"
	}
	userID := s.cfg.GitHubAPIMockUserID
	if userID == 0 {
		userID = 42
	}
	if slug := githubOAuthMockAccessTokenSlug(accessToken); slug != "" {
		userID = githubOAuthMockUserID(slug)
	}
	starred, forked := s.githubOAuthMockFlags(login)
	return githubOAuthMockProfile{
		UserID:  userID,
		Login:   login,
		Name:    name,
		Email:   email,
		Starred: starred,
		Forked:  forked,
	}, true
}

func githubOAuthMockAccessTokenSlug(accessToken string) string {
	trimmed := strings.TrimSpace(accessToken)
	prefix := githubOAuthMockAccessTokenPrefix + ":"
	if !strings.HasPrefix(trimmed, prefix) {
		return ""
	}
	return githubOAuthMockSlug(strings.TrimPrefix(trimmed, prefix))
}

func githubOAuthMockSlug(raw string) string {
	slug := strings.ToLower(strings.TrimSpace(raw))
	slug = strings.Trim(slug, "-_:")
	slug = githubOAuthMockSlugRE.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	return slug
}

func githubOAuthMockUserID(slug string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(slug))
	return int64(h.Sum64()&0x7fffffffffffffff) + 1000
}

func (s *Server) githubOAuthMockFlags(identity string) (bool, bool) {
	slug := githubOAuthMockSlug(identity)
	switch {
	case strings.HasSuffix(slug, "-auth-only"):
		return false, false
	case strings.HasSuffix(slug, "-star-only"):
		return true, false
	case strings.HasSuffix(slug, "-fork-only"):
		return false, true
	case strings.HasSuffix(slug, "-star-fork"):
		return true, true
	default:
		return s.cfg.GitHubAPIMockStarred, s.cfg.GitHubAPIMockForked
	}
}
