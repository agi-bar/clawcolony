package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"clawcolony/internal/store"
)

type internalUserSyncUser struct {
	UserID      string `json:"user_id"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Status      string `json:"status"`
	Initialized *bool  `json:"initialized,omitempty"`
	Username    string `json:"username,omitempty"`
	GoodAt      string `json:"good_at,omitempty"`
	APIKey      string `json:"api_key,omitempty"`
}

type internalUserSyncRequest struct {
	Op   string               `json:"op"`
	User internalUserSyncUser `json:"user"`
}

func (s *Server) handleInternalUserSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	expected := strings.TrimSpace(s.cfg.InternalSyncToken)
	if expected == "" {
		writeError(w, http.StatusServiceUnavailable, "internal user sync is disabled")
		return
	}
	if got := internalSyncTokenFromRequest(r); got == "" || !secureStringEqual(got, expected) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req internalUserSyncRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	op := strings.ToLower(strings.TrimSpace(req.Op))
	if op == "" {
		op = "upsert"
	}
	userID := strings.TrimSpace(req.User.UserID)
	if userID == "" {
		writeError(w, http.StatusBadRequest, "user.user_id is required")
		return
	}

	switch op {
	case "upsert":
		name := strings.TrimSpace(req.User.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "user.name is required")
			return
		}
		provider := strings.TrimSpace(req.User.Provider)
		if provider == "" {
			provider = "openclaw"
		}
		status := strings.TrimSpace(req.User.Status)
		if status == "" {
			status = "running"
		}
		profileUsername := strings.TrimSpace(req.User.Username)
		if profileUsername == "" {
			profileUsername = name
		}
		goodAt := strings.TrimSpace(req.User.GoodAt)
		if goodAt == "" {
			goodAt = profileUsername
		}
		initialized := true
		if req.User.Initialized != nil {
			initialized = *req.User.Initialized
		}

		item, err := s.store.UpsertBot(r.Context(), store.BotUpsertInput{
			BotID:       userID,
			Name:        name,
			Provider:    provider,
			Status:      status,
			Initialized: initialized,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if apiKey := strings.TrimSpace(req.User.APIKey); apiKey != "" {
			if err := s.ensureInternalUserRegistration(r.Context(), userID, profileUsername, goodAt, apiKey); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"op":     op,
			"item":   item,
		})
	case "delete":
		name := strings.TrimSpace(req.User.Name)
		provider := strings.TrimSpace(req.User.Provider)
		existing, err := s.store.GetBot(r.Context(), userID)
		if err == nil {
			if name == "" {
				name = existing.Name
			}
			if provider == "" {
				provider = existing.Provider
			}
		} else if !errors.Is(err, store.ErrBotNotFound) {
			writeError(w, http.StatusInternalServerError, "failed to lookup existing user")
			return
		}
		if name == "" {
			writeError(w, http.StatusBadRequest, "user.name is required for delete when user is not synced")
			return
		}
		if provider == "" {
			provider = "openclaw"
		}

		item, err := s.store.UpsertBot(r.Context(), store.BotUpsertInput{
			BotID:       userID,
			Name:        name,
			Provider:    provider,
			Status:      "deleted",
			Initialized: false,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if _, err := s.store.UpdateAgentRegistrationAPIKeyHash(r.Context(), userID, ""); err != nil && !errors.Is(err, store.ErrAgentRegistrationNotFound) {
			writeError(w, http.StatusInternalServerError, "failed to deactivate registration")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
			"op":     op,
			"item":   item,
		})
	default:
		writeError(w, http.StatusBadRequest, "unsupported op")
	}
}

func internalSyncTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v := strings.TrimSpace(r.Header.Get("X-Clawcolony-Internal-Token")); v != "" {
		return v
	}
	// Keep Bearer-token fallback here for older internal callers that still use
	// Authorization on the dedicated user-sync endpoint. New admin/internal
	// write endpoints intentionally require X-Clawcolony-Internal-Token only.
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func (s *Server) ensureInternalUserRegistration(ctx context.Context, userID, username, goodAt, apiKey string) error {
	hashed := hashSecret(apiKey)
	if _, err := s.store.GetAgentRegistration(ctx, userID); err != nil {
		if !errors.Is(err, store.ErrAgentRegistrationNotFound) {
			return err
		}
		if _, err := s.store.CreateAgentRegistration(ctx, store.AgentRegistrationInput{
			UserID:            userID,
			RequestedUsername: username,
			GoodAt:            goodAt,
			Status:            "active",
			APIKeyHash:        hashed,
		}); err != nil {
			return err
		}
	}
	if _, err := s.store.ActivateAgentRegistration(ctx, userID); err != nil && !errors.Is(err, store.ErrAgentRegistrationNotFound) {
		return err
	}
	if _, err := s.store.UpdateAgentRegistrationAPIKeyHash(ctx, userID, hashed); err != nil {
		return err
	}
	if _, err := s.store.UpsertAgentProfile(ctx, store.AgentProfile{
		UserID:   userID,
		Username: username,
		GoodAt:   goodAt,
	}); err != nil {
		return err
	}
	return nil
}
