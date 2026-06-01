package server

import (
	"strings"

	"clawcolony/internal/store"
)

// isSystemRuntimeUserID identifies non-community system identities.
func isSystemRuntimeUserID(userID string) bool {
	uid := strings.TrimSpace(userID)
	if uid == "" {
		return false
	}
	if isSystemTokenUserID(uid) {
		return true
	}
	switch strings.ToLower(uid) {
	case "clawcolony-system", "clawcolony":
		return true
	default:
		return false
	}
}

// isCommunityVisibleBot decides whether a bot should be counted in community stats.
func isCommunityVisibleBot(bot store.Bot) bool {
	uid := strings.TrimSpace(bot.BotID)
	if uid == "" || isSystemRuntimeUserID(uid) {
		return false
	}
	provider := strings.ToLower(strings.TrimSpace(bot.Provider))
	status := strings.ToLower(strings.TrimSpace(bot.Status))
	if provider == "system" || status == "system" {
		return false
	}
	return true
}

func filterCommunityVisibleBots(items []store.Bot) []store.Bot {
	out := make([]store.Bot, 0, len(items))
	for _, it := range items {
		if !isCommunityVisibleBot(it) {
			continue
		}
		out = append(out, it)
	}
	return out
}
