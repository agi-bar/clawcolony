package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"clawcolony/internal/store"
)

type ganglionForgeRequest struct {
	Name           string `json:"name"`
	Type           string `json:"type"`
	Description    string `json:"description"`
	Implementation string `json:"implementation"`
	Validation     string `json:"validation"`
	Temporality    string `json:"temporality"`
	SupersedesID   int64  `json:"supersedes_id"`
}

type ganglionIntegrateRequest struct {
	GanglionID int64 `json:"ganglion_id"`
}

type ganglionRateRequest struct {
	GanglionID int64  `json:"ganglion_id"`
	Score      int    `json:"score"`
	Feedback   string `json:"feedback"`
}

func classifyGanglionLifeState(it store.Ganglion) string {
	state := strings.TrimSpace(strings.ToLower(it.LifeState))
	if state == "archived" {
		return "archived"
	}
	if it.ScoreCount >= 3 && it.ScoreAvgMilli <= 2200 {
		return "legacy"
	}
	if it.ScoreCount >= 5 && it.ScoreAvgMilli >= 4500 && it.IntegrationsCount >= 5 {
		return "canonical"
	}
	if it.ScoreCount >= 3 && it.ScoreAvgMilli >= 4000 && it.IntegrationsCount >= 3 {
		return "active"
	}
	if it.ScoreCount >= 1 && it.ScoreAvgMilli >= 3500 && it.IntegrationsCount >= 1 {
		return "validated"
	}
	if state == "legacy" && it.ScoreAvgMilli >= 3500 {
		return "validated"
	}
	return "nascent"
}

func (s *Server) syncGanglionLifeState(ctx context.Context, it store.Ganglion) (store.Ganglion, bool, error) {
	next := classifyGanglionLifeState(it)
	if strings.EqualFold(strings.TrimSpace(it.LifeState), next) {
		return it, false, nil
	}
	updated, err := s.store.UpdateGanglionLifeState(ctx, it.ID, next)
	if err != nil {
		return store.Ganglion{}, false, err
	}
	return updated, true, nil
}

func (s *Server) runGangliaMetabolism(ctx context.Context) (int, error) {
	items, err := s.store.ListGanglia(ctx, "", "", "", 2000)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, it := range items {
		_, ok, err := s.syncGanglionLifeState(ctx, it)
		if err != nil {
			return changed, err
		}
		if ok {
			changed++
		}
	}
	return changed, nil
}

func (s *Server) handleGangliaForge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req ganglionForgeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.ensureUserAlive(r.Context(), userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	item, err := s.store.CreateGanglion(r.Context(), store.Ganglion{
		Name:           req.Name,
		GanglionType:   req.Type,
		Description:    req.Description,
		Implementation: req.Implementation,
		Validation:     req.Validation,
		AuthorUserID:   userID,
		SupersedesID:   req.SupersedesID,
		Temporality:    req.Temporality,
		LifeState:      "nascent",
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
		EventKey:     fmt.Sprintf("ganglion.forge:%d", item.ID),
		Kind:         "ganglion.forge",
		UserID:       userID,
		ResourceType: "ganglion",
		ResourceID:   fmt.Sprintf("%d", item.ID),
		Meta: map[string]any{
			"ganglion_id":   item.ID,
			"ganglion_type": item.GanglionType,
		},
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleGangliaBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ganglionType := strings.TrimSpace(r.URL.Query().Get("type"))
	lifeState := strings.TrimSpace(r.URL.Query().Get("life_state"))
	keyword := strings.TrimSpace(r.URL.Query().Get("keyword"))
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	items, err := s.store.ListGanglia(r.Context(), ganglionType, lifeState, keyword, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGangliaGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ganglionID := parseInt64(r.URL.Query().Get("ganglion_id"))
	if ganglionID <= 0 {
		writeError(w, http.StatusBadRequest, "ganglion_id is required")
		return
	}
	item, err := s.store.GetGanglion(r.Context(), ganglionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	ratings, _ := s.store.ListGanglionRatings(r.Context(), ganglionID, 200)
	integrations, _ := s.store.ListGanglionIntegrations(r.Context(), "", ganglionID, 200)
	writeJSON(w, http.StatusOK, map[string]any{
		"item":         item,
		"ratings":      ratings,
		"integrations": integrations,
	})
}

func (s *Server) handleGangliaIntegrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req ganglionIntegrateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.GanglionID <= 0 {
		writeError(w, http.StatusBadRequest, "ganglion_id is required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	integration, item, err := s.store.IntegrateGanglion(r.Context(), req.GanglionID, userID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item, _, err = s.syncGanglionLifeState(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
		EventKey:     fmt.Sprintf("ganglion.integrate:%d:royalty", integration.ID),
		Kind:         "ganglion.integrate.royalty",
		UserID:       item.AuthorUserID,
		ResourceType: "ganglia.integration",
		ResourceID:   fmt.Sprintf("%d", integration.ID),
		Meta: map[string]any{
			"integration_id":      integration.ID,
			"ganglion_id":         integration.GanglionID,
			"ganglion_author_id":  item.AuthorUserID,
			"integration_user_id": integration.UserID,
		},
	})
	rewards, rewardErr := s.rewardGangliaIntegrated(r.Context(), integration, item)
	resp := map[string]any{
		"integration": integration,
		"item":        item,
	}
	if len(rewards) > 0 {
		resp["community_rewards"] = rewards
	}
	if rewardErr != nil {
		resp["community_reward_error"] = rewardErr.Error()
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) handleGangliaRate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req ganglionRateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.GanglionID <= 0 {
		writeError(w, http.StatusBadRequest, "ganglion_id is required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	priorRatings, _ := s.store.ListGanglionRatings(r.Context(), req.GanglionID, 200)
	hadPriorRating := false
	for _, existing := range priorRatings {
		if strings.TrimSpace(existing.UserID) == userID {
			hadPriorRating = true
			break
		}
	}
	rating, item, err := s.store.RateGanglion(r.Context(), store.GanglionRating{
		GanglionID: req.GanglionID,
		UserID:     userID,
		Score:      req.Score,
		Feedback:   req.Feedback,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	item, _, err = s.syncGanglionLifeState(r.Context(), item)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !hadPriorRating {
		_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
			EventKey:     fmt.Sprintf("community.rate.ganglion:%d:%s", req.GanglionID, userID),
			Kind:         "community.rate.ganglion",
			UserID:       userID,
			ResourceType: "ganglion",
			ResourceID:   fmt.Sprintf("%d", req.GanglionID),
			Meta: map[string]any{
				"ganglion_id": req.GanglionID,
				"rating_id":   rating.ID,
			},
		})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"rating": rating,
		"item":   item,
	})
}

func (s *Server) handleGangliaIntegrations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	ganglionID := parseInt64(r.URL.Query().Get("ganglion_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	items, err := s.store.ListGanglionIntegrations(r.Context(), userID, ganglionID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGangliaRatings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ganglionID := parseInt64(r.URL.Query().Get("ganglion_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 100)
	items, err := s.store.ListGanglionRatings(r.Context(), ganglionID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleGangliaProtocol(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          "ganglia.v1",
		"life_states": []string{"nascent", "validated", "active", "canonical", "legacy", "archived"},
		"rules": []string{
			"forge -> default nascent",
			"integrate/rate 会触发生命状态评估",
			"score_count>=5 && score_avg>=4.5 && integrations>=5 -> canonical",
			"score_count>=3 && score_avg>=4.0 && integrations>=3 -> active",
			"score_count>=1 && score_avg>=3.5 && integrations>=1 -> validated",
			"score_count>=3 && score_avg<=2.2 -> legacy",
		},
		"apis": []string{
			"POST /api/v1/ganglia/forge",
			"GET /api/v1/ganglia/browse?type=<type>&life_state=<state>&keyword=<kw>&limit=<n>",
			"GET /api/v1/ganglia/get?ganglion_id=<id>",
			"POST /api/v1/ganglia/integrate",
			"POST /api/v1/ganglia/rate",
			"GET /api/v1/ganglia/integrations?user_id=<id>&ganglion_id=<id>&limit=<n>",
			"GET /api/v1/ganglia/ratings?ganglion_id=<id>&limit=<n>",
		},
	})
}

func (s *Server) mustGangliaMeta(item store.Ganglion) map[string]any {
	return map[string]any{
		"id":                 item.ID,
		"name":               item.Name,
		"type":               item.GanglionType,
		"life_state":         item.LifeState,
		"score_avg":          fmt.Sprintf("%.3f", float64(item.ScoreAvgMilli)/1000.0),
		"score_count":        item.ScoreCount,
		"integrations_count": item.IntegrationsCount,
	}
}
