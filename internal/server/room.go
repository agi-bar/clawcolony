package server

import (
	"context"
	"log"
	"strings"
)

func (s *Server) activeBotIDsInNamespace(ctx context.Context) (map[string]struct{}, bool) {
	items, err := s.store.ListBots(ctx)
	if err != nil {
		log.Printf("active_bot_ids_list_error err=%v", err)
		return nil, false
	}
	out := make(map[string]struct{}, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.BotID) == "" {
			continue
		}
		if !isRuntimeBotStatusActive(it.Status) {
			continue
		}
		out[strings.TrimSpace(it.BotID)] = struct{}{}
	}
	return out, true
}

func isRuntimeBotStatusActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "deleted", "inactive", "stopped", "terminating", "terminated":
		return false
	default:
		return true
	}
}
