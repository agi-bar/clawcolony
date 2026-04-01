package server

import (
	"fmt"
	"strings"

	"clawcolony/internal/store"
)

func canonicalGitHubPRKey(prURL, prRepo string, prNumber int) string {
	if ref, err := parseGitHubPRRef(prURL); err == nil {
		return fmt.Sprintf("%s#%d", strings.ToLower(strings.TrimSpace(ref.Repo)), ref.Number)
	}
	repo := strings.ToLower(strings.TrimSpace(prRepo))
	if repo != "" && prNumber > 0 {
		return fmt.Sprintf("%s#%d", repo, prNumber)
	}
	if cleanURL := strings.TrimSpace(prURL); cleanURL != "" {
		return strings.ToLower(cleanURL)
	}
	return ""
}

func canonicalGitHubPRKeyForSession(session store.CollabSession) string {
	return canonicalGitHubPRKey(session.PRURL, session.PRRepo, session.PRNumber)
}

func upgradePRMerged(session store.CollabSession) bool {
	return session.PRMergedAt != nil ||
		strings.TrimSpace(session.PRMergeCommitSHA) != "" ||
		strings.EqualFold(strings.TrimSpace(session.GitHubPRState), "merged")
}

func upgradePRClosedOnGitHub(session store.CollabSession) bool {
	return strings.EqualFold(strings.TrimSpace(session.GitHubPRState), "closed")
}

func upgradePRTerminalPhase(session store.CollabSession) bool {
	switch strings.ToLower(strings.TrimSpace(session.Phase)) {
	case "closed", "failed", "abandoned":
		return true
	default:
		return false
	}
}

func upgradePRCanonicalState(session store.CollabSession) string {
	switch {
	case upgradePRMerged(session):
		return "merged"
	case upgradePRClosedOnGitHub(session):
		return "closed"
	case upgradePROpenForReview(session):
		return "open"
	default:
		return strings.ToLower(strings.TrimSpace(session.GitHubPRState))
	}
}

func upgradePROpenForReview(session store.CollabSession) bool {
	if strings.TrimSpace(session.PRURL) == "" {
		return false
	}
	if upgradePRMerged(session) || upgradePRClosedOnGitHub(session) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(session.GitHubPRState), "open") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(session.Phase)) {
	case "closed", "failed", "abandoned":
		return false
	case "reviewing", "review", "recruiting", "assigned", "executing":
		return true
	default:
		return false
	}
}

func upgradePRBlocksDuplicate(session store.CollabSession) bool {
	if !strings.EqualFold(strings.TrimSpace(session.Kind), "upgrade_pr") {
		return false
	}
	if strings.TrimSpace(session.PRURL) == "" {
		return false
	}
	if upgradePRMerged(session) || upgradePRClosedOnGitHub(session) {
		return false
	}
	return true
}
