package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	neturl "net/url"
	"sort"
	"strings"
	"time"

	"clawcolony/internal/store"
)

type toolRegisterRequest struct {
	ToolID       string `json:"tool_id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Tier         string `json:"tier"`
	Manifest     string `json:"manifest"`
	Code         string `json:"code"`
	Temporality  string `json:"temporality"`
	CategoryHint string `json:"category_hint,omitempty"`
}

type toolReviewRequest struct {
	ToolID               string `json:"tool_id"`
	Decision             string `json:"decision"` // approve|reject
	ReviewNote           string `json:"review_note"`
	FunctionalClusterKey string `json:"functional_cluster_key,omitempty"`
}

type toolInvokeRequest struct {
	ToolID string         `json:"tool_id"`
	Params map[string]any `json:"params"`
}

type npcTaskCreateRequest struct {
	NPCID    string `json:"npc_id"`
	TaskType string `json:"task_type"`
	Payload  string `json:"payload"`
}

type metabolismSupersedeRequest struct {
	NewID        string   `json:"new_id"`
	OldID        string   `json:"old_id"`
	Relationship string   `json:"relationship"`
	Validators   []string `json:"validators"`
}

type metabolismDisputeRequest struct {
	SupersessionID int64  `json:"supersession_id"`
	Reason         string `json:"reason"`
}

func normalizeToolTier(t string) string {
	switch strings.ToUpper(strings.TrimSpace(t)) {
	case "T0", "T1", "T2", "T3":
		return strings.ToUpper(strings.TrimSpace(t))
	default:
		return "T1"
	}
}

func toolCostTypeForTier(tier string) string {
	switch normalizeToolTier(tier) {
	case "T0":
		return "tool.runtime.t0"
	case "T1":
		return "tool.runtime.t1"
	case "T2":
		return "tool.runtime.t2"
	case "T3":
		return "tool.runtime.t3"
	default:
		return "tool.runtime.t1"
	}
}

func parseToolAllowHost(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		if u, err := neturl.Parse(raw); err == nil {
			raw = strings.TrimSpace(strings.ToLower(u.Host))
		}
	}
	if h, p, err := net.SplitHostPort(raw); err == nil && h != "" && p != "" {
		raw = h
	}
	return strings.TrimSpace(strings.TrimSuffix(raw, "."))
}

func normalizeURLHost(rawURL string) (string, error) {
	u, err := neturl.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	host := parseToolAllowHost(u.Host)
	if host == "" {
		return "", fmt.Errorf("url host is empty")
	}
	return host, nil
}

func collectURLsFromAny(v any, out *[]string) {
	switch vv := v.(type) {
	case string:
		low := strings.ToLower(strings.TrimSpace(vv))
		if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") {
			*out = append(*out, strings.TrimSpace(vv))
		}
	case []any:
		for _, it := range vv {
			collectURLsFromAny(it, out)
		}
	case map[string]any:
		for _, it := range vv {
			collectURLsFromAny(it, out)
		}
	}
}

func (s *Server) toolColonyAllowedHosts() map[string]struct{} {
	hosts := map[string]struct{}{
		"clawcolony":                              {},
		"clawcolony.freewill.svc.cluster.local":   {},
		"clawcolony.clawcolony.svc.cluster.local": {},
		"localhost":                               {},
		"127.0.0.1":                               {},
	}
	base := strings.TrimSpace(s.cfg.ClawWorldAPIBase)
	if base != "" {
		if u, err := neturl.Parse(base); err == nil {
			if h := parseToolAllowHost(u.Host); h != "" {
				hosts[h] = struct{}{}
			}
		}
	}
	return hosts
}

func (s *Server) toolT3AllowedHosts() map[string]struct{} {
	hosts := s.toolColonyAllowedHosts()
	for _, part := range strings.Split(strings.TrimSpace(s.cfg.ToolT3AllowHosts), ",") {
		if h := parseToolAllowHost(part); h != "" {
			hosts[h] = struct{}{}
		}
	}
	return hosts
}

func (s *Server) validateToolInvokeURLPolicy(tier string, params map[string]any) error {
	var urls []string
	collectURLsFromAny(params, &urls)
	if len(urls) == 0 {
		return nil
	}
	ntier := normalizeToolTier(tier)
	if ntier == "T0" {
		return fmt.Errorf("T0 tool params must not contain URLs")
	}
	var allow map[string]struct{}
	switch ntier {
	case "T1", "T2":
		allow = s.toolColonyAllowedHosts()
	case "T3":
		allow = s.toolT3AllowedHosts()
	default:
		allow = s.toolColonyAllowedHosts()
	}
	for _, raw := range urls {
		host, err := normalizeURLHost(raw)
		if err != nil {
			return fmt.Errorf("invalid url %q: %w", raw, err)
		}
		if _, ok := allow[host]; !ok {
			if ntier == "T3" {
				return fmt.Errorf("T3 url host %q is not in TOOL_T3_ALLOWED_HOSTS", host)
			}
			return fmt.Errorf("%s url host %q is not allowed; only colony hosts are permitted", ntier, host)
		}
	}
	return nil
}

func (s *Server) handleToolRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req toolRegisterRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ToolID = strings.TrimSpace(strings.ToLower(req.ToolID))
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	req.Tier = normalizeToolTier(req.Tier)
	req.Manifest = strings.TrimSpace(req.Manifest)
	req.Code = strings.TrimSpace(req.Code)
	req.Temporality = strings.TrimSpace(req.Temporality)
	req.CategoryHint = strings.TrimSpace(strings.ToLower(req.CategoryHint))
	if req.ToolID == "" || req.Name == "" {
		writeError(w, http.StatusBadRequest, "tool_id and name are required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	now := time.Now().UTC()
	genesisStateMu.Lock()
	state, err := s.getToolRegistryState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	meta := toolEconomyMeta{
		ToolID:       req.ToolID,
		AuthorUserID: userID,
		CategoryHint: req.CategoryHint,
		PriceToken:   parseToolManifestPrice(req.Manifest),
	}
	for i := range state.Items {
		if state.Items[i].ToolID != req.ToolID {
			continue
		}
		if state.Items[i].Status == "active" {
			genesisStateMu.Unlock()
			writeError(w, http.StatusConflict, "tool already active")
			return
		}
		state.Items[i].Name = req.Name
		state.Items[i].Description = req.Description
		state.Items[i].Tier = req.Tier
		state.Items[i].Manifest = req.Manifest
		state.Items[i].Code = req.Code
		state.Items[i].Temporality = req.Temporality
		state.Items[i].AuthorUserID = userID
		state.Items[i].Status = "pending"
		state.Items[i].ReviewNote = ""
		state.Items[i].ReviewedBy = ""
		state.Items[i].UpdatedAt = now
		if err := s.saveToolRegistryState(r.Context(), state); err != nil {
			genesisStateMu.Unlock()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated := state.Items[i]
		genesisStateMu.Unlock()
		_ = s.upsertToolEconomyMeta(r.Context(), meta)
		writeJSON(w, http.StatusAccepted, map[string]any{"item": updated})
		return
	}
	item := toolRegistryItem{
		ToolID:       req.ToolID,
		Name:         req.Name,
		Description:  req.Description,
		Tier:         req.Tier,
		Manifest:     req.Manifest,
		Code:         req.Code,
		Temporality:  req.Temporality,
		AuthorUserID: userID,
		Status:       "pending",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	state.Items = append(state.Items, item)
	if err := s.saveToolRegistryState(r.Context(), state); err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	genesisStateMu.Unlock()
	_ = s.upsertToolEconomyMeta(r.Context(), meta)
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleToolReview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	reviewerUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req toolReviewRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ToolID = strings.TrimSpace(strings.ToLower(req.ToolID))
	req.Decision = strings.TrimSpace(strings.ToLower(req.Decision))
	req.ReviewNote = strings.TrimSpace(req.ReviewNote)
	req.FunctionalClusterKey = strings.TrimSpace(strings.ToLower(req.FunctionalClusterKey))
	if req.ToolID == "" || (req.Decision != "approve" && req.Decision != "reject") {
		writeError(w, http.StatusBadRequest, "tool_id and decision(approve/reject) are required")
		return
	}
	genesisStateMu.Lock()
	state, err := s.getToolRegistryState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var (
		found        bool
		updatedItem  toolRegistryItem
		authorUserID string
	)
	for i := range state.Items {
		if state.Items[i].ToolID != req.ToolID {
			continue
		}
		found = true
		now := time.Now().UTC()
		state.Items[i].ReviewedBy = reviewerUserID
		state.Items[i].ReviewNote = req.ReviewNote
		state.Items[i].UpdatedAt = now
		if req.Decision == "approve" {
			state.Items[i].Status = "active"
			state.Items[i].ActivatedAt = &now
		} else {
			state.Items[i].Status = "rejected"
			state.Items[i].RejectedAt = &now
		}
		if err := s.saveToolRegistryState(r.Context(), state); err != nil {
			genesisStateMu.Unlock()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updatedItem = state.Items[i]
		authorUserID = strings.TrimSpace(state.Items[i].AuthorUserID)
		break
	}
	genesisStateMu.Unlock()
	if !found {
		writeError(w, http.StatusNotFound, "tool not found")
		return
	}
	meta, _, _ := s.toolEconomyMetaForID(r.Context(), req.ToolID)
	meta.ToolID = req.ToolID
	if strings.TrimSpace(meta.AuthorUserID) == "" {
		meta.AuthorUserID = authorUserID
	}
	if meta.PriceToken <= 0 {
		meta.PriceToken = parseToolManifestPrice(updatedItem.Manifest)
	}
	if req.FunctionalClusterKey != "" {
		meta.FunctionalClusterKey = req.FunctionalClusterKey
	}
	_ = s.upsertToolEconomyMeta(r.Context(), meta)
	if req.Decision == "approve" {
		if req.FunctionalClusterKey == "" {
			now := time.Now().UTC()
			_, _ = s.store.UpsertEconomyRewardDecision(r.Context(), store.EconomyRewardDecision{
				DecisionKey:     fmt.Sprintf("tool.approve.pending:%s", req.ToolID),
				RuleKey:         "tool.approve",
				ResourceType:    "tool",
				ResourceID:      req.ToolID,
				RecipientUserID: authorUserID,
				Status:          "pending_review",
				QueueReason:     "functional_cluster_key_required",
				CreatedAt:       now,
				UpdatedAt:       now,
				MetaJSON:        mustMarshalJSON(map[string]any{"tool_id": req.ToolID}),
			})
		} else {
			_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
				EventKey:     fmt.Sprintf("tool.approve:%s", req.ToolID),
				Kind:         "tool.approve",
				UserID:       authorUserID,
				ResourceType: "tool",
				ResourceID:   req.ToolID,
				Meta: map[string]any{
					"tool_id":                req.ToolID,
					"tier":                   updatedItem.Tier,
					"reviewer_user_id":       reviewerUserID,
					"functional_cluster_key": req.FunctionalClusterKey,
				},
			})
			_, _, _ = s.appendContributionEvent(r.Context(), contributionEvent{
				EventKey:     fmt.Sprintf("community.review.tool:%s:%s", req.ToolID, reviewerUserID),
				Kind:         "community.review.tool",
				UserID:       reviewerUserID,
				ResourceType: "tool",
				ResourceID:   req.ToolID,
				Meta: map[string]any{
					"tool_id":          req.ToolID,
					"reviewer_user_id": reviewerUserID,
				},
			})
		}
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": updatedItem})
	return
}

func (s *Server) handleToolSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("query")))
	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	tier := normalizeToolTier(r.URL.Query().Get("tier"))
	if strings.TrimSpace(r.URL.Query().Get("tier")) == "" {
		tier = ""
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 200)

	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getToolRegistryState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]toolRegistryItem, 0, len(state.Items))
	for _, it := range state.Items {
		if status != "" && strings.ToLower(it.Status) != status {
			continue
		}
		if tier != "" && normalizeToolTier(it.Tier) != tier {
			continue
		}
		if query != "" {
			text := strings.ToLower(it.ToolID + " " + it.Name + " " + it.Description)
			if !strings.Contains(text, query) {
				continue
			}
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleToolInvoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req toolInvokeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ToolID = strings.TrimSpace(strings.ToLower(req.ToolID))
	if req.ToolID == "" {
		writeError(w, http.StatusBadRequest, "tool_id is required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	genesisStateMu.Lock()
	state, err := s.getToolRegistryState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	idx := -1
	for i := range state.Items {
		if state.Items[i].ToolID == req.ToolID {
			idx = i
			break
		}
	}
	if idx < 0 {
		genesisStateMu.Unlock()
		writeError(w, http.StatusNotFound, "tool not found")
		return
	}
	item := state.Items[idx]
	if item.Status != "active" {
		genesisStateMu.Unlock()
		writeError(w, http.StatusConflict, "tool is not active")
		return
	}
	costType := toolCostTypeForTier(item.Tier)
	if err := s.ensureToolTierAllowed(r.Context(), userID, costType); err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err := s.validateToolInvokeURLPolicy(item.Tier, req.Params); err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	now := time.Now().UTC()
	state.Items[idx].InvokeCount++
	state.Items[idx].LastInvokedAt = &now
	state.Items[idx].UpdatedAt = now
	if err := s.saveToolRegistryState(r.Context(), state); err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	genesisStateMu.Unlock()
	pricing := map[string]any{}
	if s.tokenEconomyV2Enabled() {
		priceToken := parseToolManifestPrice(item.Manifest)
		authorUserID := strings.TrimSpace(item.AuthorUserID)
		if meta, ok, metaErr := s.toolEconomyMetaForID(r.Context(), item.ToolID); metaErr == nil && ok {
			if meta.PriceToken > 0 {
				priceToken = meta.PriceToken
			}
			if strings.TrimSpace(meta.AuthorUserID) != "" {
				authorUserID = strings.TrimSpace(meta.AuthorUserID)
			}
		}
		if priceToken > 0 {
			policy := s.tokenPolicy()
			creatorShare := (priceToken * policy.ToolCreatorShareMilli) / 1000
			if isExcludedTokenUserID(authorUserID) {
				creatorShare = 0
			}
			treasuryShare := priceToken - creatorShare
			debit, err := s.store.Consume(r.Context(), userID, priceToken)
			if err != nil {
				if err == store.ErrInsufficientBalance {
					writeError(w, http.StatusPaymentRequired, "insufficient token balance for tool price")
					return
				}
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if creatorShare > 0 {
				if _, err := s.store.Recharge(r.Context(), authorUserID, creatorShare); err != nil {
					_, _ = s.store.Recharge(r.Context(), userID, priceToken)
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			if treasuryShare > 0 {
				if _, err := s.ensureTreasuryAccount(r.Context()); err != nil {
					_, _ = s.store.Recharge(r.Context(), userID, priceToken)
					if creatorShare > 0 {
						_, _ = s.store.Consume(r.Context(), authorUserID, creatorShare)
					}
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
				if _, err := s.store.Recharge(r.Context(), clawTreasurySystemID, treasuryShare); err != nil {
					_, _ = s.store.Recharge(r.Context(), userID, priceToken)
					if creatorShare > 0 {
						_, _ = s.store.Consume(r.Context(), authorUserID, creatorShare)
					}
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			meta := map[string]any{
				"tool_id":        req.ToolID,
				"price_token":    priceToken,
				"creator_share":  creatorShare,
				"treasury_share": treasuryShare,
				"author_user_id": authorUserID,
				"balance_after":  debit.BalanceAfter,
			}
			metaRaw, _ := json.Marshal(meta)
			_, _ = s.store.AppendCostEvent(r.Context(), store.CostEvent{
				UserID:   userID,
				TickID:   s.currentTickID(),
				CostType: "tool.invoke.price",
				Amount:   priceToken,
				Units:    1,
				MetaJSON: string(metaRaw),
			})
			pricing = map[string]any{
				"price_token":          priceToken,
				"creator_share":        creatorShare,
				"treasury_share":       treasuryShare,
				"author_user_id":       authorUserID,
				"caller_balance_after": debit.BalanceAfter,
			}
		}
	}
	paramsRaw, _ := json.Marshal(req.Params)
	result := toolSandboxResult{
		OK:             true,
		SandboxProfile: strings.ToLower(normalizeToolTier(item.Tier)),
		Message:        "sandbox invoke simulated",
		EchoParams:     string(paramsRaw),
		ExitCode:       0,
		DurationMS:     0,
	}
	if s.cfg.ToolRuntimeExec {
		runner := s.toolSandboxExec
		if runner == nil {
			writeError(w, http.StatusInternalServerError, "tool sandbox runner is not configured")
			return
		}
		var err error
		result, err = runner(r.Context(), toolSandboxInput{
			UserID:     userID,
			ToolID:     req.ToolID,
			Tier:       item.Tier,
			Code:       item.Code,
			ParamsJSON: string(paramsRaw),
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
	}
	meta := map[string]any{
		"tool_id":         req.ToolID,
		"sandbox_profile": result.SandboxProfile,
		"result_ok":       result.OK,
		"exit_code":       result.ExitCode,
		"duration_ms":     result.DurationMS,
	}
	if result.Message != "" {
		meta["message"] = result.Message
	}
	if !s.tokenEconomyV2Enabled() {
		s.appendToolCostEvent(r.Context(), userID, costType, 1, meta)
	}
	resp := map[string]any{
		"tool_id": req.ToolID,
		"tier":    item.Tier,
		"result":  result,
	}
	if len(pricing) > 0 {
		resp["pricing"] = pricing
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) handleNPCList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	runtimeState, _ := s.getNPCRuntimeState(r.Context())
	taskState, _ := s.getNPCTaskState(r.Context())
	pendingByNPC := map[string]int{}
	for _, t := range taskState.Items {
		if t.Status == "queued" || t.Status == "running" {
			pendingByNPC[t.NPCID]++
		}
	}
	items := make([]map[string]any, 0, 12)
	for _, base := range defaultNPCCatalog() {
		npcID := fmt.Sprintf("%v", base["npc_id"])
		r := runtimeState.Items[npcID]
		item := map[string]any{}
		for k, v := range base {
			item[k] = v
		}
		item["pending_tasks"] = pendingByNPC[npcID]
		item["last_run_at"] = r.LastRunAt
		item["last_status"] = r.LastStatus
		item["last_message"] = r.LastMessage
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleNPCTaskCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req npcTaskCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.NPCID = strings.TrimSpace(strings.ToLower(req.NPCID))
	req.TaskType = strings.TrimSpace(req.TaskType)
	req.Payload = strings.TrimSpace(req.Payload)
	if req.NPCID == "" || req.TaskType == "" {
		writeError(w, http.StatusBadRequest, "npc_id and task_type are required")
		return
	}
	validNPC := false
	for _, it := range defaultNPCCatalog() {
		if fmt.Sprintf("%v", it["npc_id"]) == req.NPCID {
			validNPC = true
			break
		}
	}
	if !validNPC {
		writeError(w, http.StatusBadRequest, "npc_id is invalid")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getNPCTaskState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	item := npcTask{
		TaskID:     state.NextID,
		NPCID:      req.NPCID,
		TaskType:   req.TaskType,
		Payload:    req.Payload,
		Status:     "queued",
		CreatedAt:  now,
		UpdatedAt:  now,
		RetryCount: 0,
	}
	state.NextID++
	state.Items = append(state.Items, item)
	if err := s.saveNPCTaskState(r.Context(), state); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleNPCTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	npcID := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("npc_id")))
	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getNPCTaskState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]npcTask, 0, len(state.Items))
	for _, it := range state.Items {
		if npcID != "" && it.NPCID != npcID {
			continue
		}
		if status != "" && strings.ToLower(it.Status) != status {
			continue
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func defaultNPCTaskType(npcID string) string {
	switch strings.TrimSpace(strings.ToLower(npcID)) {
	case "historian":
		return "record_cycle"
	case "monitor":
		return "health_scan"
	case "procurement":
		return "wish_scan"
	case "publisher":
		return "upgrade_audit"
	case "archivist":
		return "profile_snapshot"
	case "wizard":
		return "revival_scan"
	case "enforcer":
		return "discipline_scan"
	case "broker":
		return "bounty_settlement"
	case "metabolizer":
		return "metabolism_cycle"
	default:
		return "generic"
	}
}

func (s *Server) ensureNPCAutoTasks(now time.Time, state *npcTaskState) {
	if state == nil {
		return
	}
	hasPending := map[string]bool{}
	for _, it := range state.Items {
		if it.Status == "queued" || it.Status == "running" {
			hasPending[it.NPCID] = true
		}
	}
	for _, cat := range defaultNPCCatalog() {
		npcID := fmt.Sprintf("%v", cat["npc_id"])
		if hasPending[npcID] {
			continue
		}
		item := npcTask{
			TaskID:     state.NextID,
			NPCID:      npcID,
			TaskType:   defaultNPCTaskType(npcID),
			Payload:    "auto",
			Status:     "queued",
			CreatedAt:  now,
			UpdatedAt:  now,
			RetryCount: 0,
		}
		state.NextID++
		state.Items = append(state.Items, item)
	}
	if len(state.Items) > 8000 {
		state.Items = state.Items[len(state.Items)-8000:]
	}
}

func (s *Server) runNPCTick(ctx context.Context, tickID int64) error {
	genesisStateMu.Lock()
	tasks, err := s.getNPCTaskState(ctx)
	if err != nil {
		genesisStateMu.Unlock()
		return err
	}
	runtimeState, err := s.getNPCRuntimeState(ctx)
	if err != nil {
		genesisStateMu.Unlock()
		return err
	}
	now := time.Now().UTC()
	for _, cat := range defaultNPCCatalog() {
		npcID := fmt.Sprintf("%v", cat["npc_id"])
		it := runtimeState.Items[npcID]
		it.NPCID = npcID
		if it.LastSeenAt.IsZero() {
			it.LastSeenAt = now
		}
		it.LastSeenAt = now
		runtimeState.Items[npcID] = it
	}
	s.ensureNPCAutoTasks(now, &tasks)
	genesisStateMu.Unlock()

	processed := 0
	for i := range tasks.Items {
		if tasks.Items[i].Status != "queued" {
			continue
		}
		if processed >= 40 {
			break
		}
		tasks.Items[i].Status = "running"
		tasks.Items[i].UpdatedAt = now
		npcID := tasks.Items[i].NPCID
		run := runtimeState.Items[npcID]
		run.LastRunAt = now
		run.LastStatus = "running"
		run.LastMessage = tasks.Items[i].TaskType
		runtimeState.Items[npcID] = run

		result := "ok"
		errText := ""
		resultPayload := map[string]any{
			"npc_id":    npcID,
			"task_type": tasks.Items[i].TaskType,
			"tick_id":   tickID,
		}
		switch npcID {
		case "historian":
			msg := "recorded cycle checkpoint"
			_ = s.appendChronicleEntryLocked(ctx, tickID, "npc.historian", msg)
			resultPayload["message"] = msg
		case "monitor":
			living, lerr := s.listLivingUserIDs(ctx)
			if lerr != nil {
				errText = lerr.Error()
				break
			}
			lifeStates, _ := s.store.ListUserLifeStates(ctx, "", "", 5000)
			dead := 0
			for _, it := range lifeStates {
				if normalizeLifeStateForServer(it.State) == "dead" {
					dead++
				}
			}
			resultPayload["living"] = len(living)
			resultPayload["dead"] = dead
			resultPayload["health"] = "ok"
			_ = s.appendChronicleEntryLocked(ctx, tickID, "npc.monitor", fmt.Sprintf("living=%d dead=%d", len(living), dead))
		case "procurement":
			wishes, werr := s.getTokenWishState(ctx)
			if werr != nil {
				errText = werr.Error()
				break
			}
			open := 0
			for _, it := range wishes.Items {
				if strings.EqualFold(strings.TrimSpace(it.Status), "open") {
					open++
				}
			}
			resultPayload["open_wishes"] = open
			resultPayload["action"] = "scan_only"
		case "publisher":
			resultPayload["audits"] = 0
			resultPayload["running"] = 0
			resultPayload["failed"] = 0
			resultPayload["note"] = "upgrade domain moved to deployer"
		case "archivist":
			profiles, perr := s.getLobsterProfileState(ctx)
			if perr != nil {
				errText = perr.Error()
				break
			}
			bots, _ := s.store.ListBots(ctx)
			accounts, _ := s.store.ListTokenAccounts(ctx)
			lifeStates, _ := s.store.ListUserLifeStates(ctx, "", "", 5000)
			balanceByUser := map[string]int64{}
			for _, a := range accounts {
				balanceByUser[strings.TrimSpace(a.BotID)] = a.Balance
			}
			lifeByUser := map[string]string{}
			for _, ls := range lifeStates {
				lifeByUser[strings.TrimSpace(ls.UserID)] = normalizeLifeStateForServer(ls.State)
			}
			for _, b := range s.filterActiveBots(ctx, bots) {
				uid := strings.TrimSpace(b.BotID)
				if uid == "" {
					continue
				}
				life := lifeByUser[uid]
				if life == "" {
					life = "alive"
				}
				tags := []string{"active"}
				if life == "dead" {
					tags = []string{"dead"}
				}
				profiles.Items[uid] = lobsterProfile{
					UserID:       uid,
					Name:         strings.TrimSpace(b.Name),
					Status:       strings.TrimSpace(b.Status),
					LifeState:    life,
					TokenBalance: balanceByUser[uid],
					Tags:         tags,
					UpdatedAt:    now,
				}
			}
			if err := s.saveLobsterProfileState(ctx, profiles); err != nil {
				errText = err.Error()
				break
			}
			resultPayload["profiles"] = len(profiles.Items)
		case "wizard":
			living, lerr := s.listLivingUserIDs(ctx)
			if lerr != nil {
				errText = lerr.Error()
				break
			}
			minPopulation := s.desiredMinPopulation()
			gap := minPopulation - len(living)
			if gap < 0 {
				gap = 0
			}
			resultPayload["min_population"] = minPopulation
			resultPayload["living"] = len(living)
			resultPayload["gap"] = gap
			resultPayload["action"] = "monitor_revival_only"
		case "enforcer":
			ds, derr := s.getDisciplineState(ctx)
			if derr != nil {
				errText = derr.Error()
				break
			}
			openReports := 0
			openCases := 0
			for _, rep := range ds.Reports {
				if strings.EqualFold(strings.TrimSpace(rep.Status), "open") {
					openReports++
				}
			}
			for _, c := range ds.Cases {
				if strings.EqualFold(strings.TrimSpace(c.Status), "open") {
					openCases++
				}
			}
			resultPayload["open_reports"] = openReports
			resultPayload["open_cases"] = openCases
		case "metabolizer":
			rep, merr := s.runMetabolismCycle(ctx, tickID)
			if merr != nil {
				errText = merr.Error()
				break
			}
			resultPayload["report"] = rep
		case "broker":
			expired, berr := s.runBountyBroker(ctx, tickID)
			if berr != nil {
				errText = berr.Error()
				break
			}
			resultPayload["expired"] = expired
		default:
			resultPayload["message"] = "acknowledged"
		}
		if errText != "" {
			result = "failed"
		}
		tasks.Items[i].Status = map[string]string{"ok": "done", "failed": "failed"}[result]
		if raw, merr := json.Marshal(resultPayload); merr == nil {
			tasks.Items[i].Result = string(raw)
		} else {
			tasks.Items[i].Result = result
		}
		tasks.Items[i].Error = errText
		tasks.Items[i].UpdatedAt = time.Now().UTC()
		completed := tasks.Items[i].UpdatedAt
		tasks.Items[i].CompletedAt = &completed

		run = runtimeState.Items[npcID]
		run.LastStatus = tasks.Items[i].Status
		if errText != "" {
			run.LastMessage = errText
		} else {
			run.LastMessage = tasks.Items[i].TaskType
		}
		runtimeState.Items[npcID] = run
		processed++
	}

	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	if err := s.saveNPCTaskState(ctx, tasks); err != nil {
		return err
	}
	if err := s.saveNPCRuntimeState(ctx, runtimeState); err != nil {
		return err
	}
	_ = s.appendChronicleEntryLocked(ctx, tickID, "npc.tick", fmt.Sprintf("processed=%d", processed))
	return nil
}

func (s *Server) appendChronicleEntryLocked(ctx context.Context, tickID int64, source, summary string) error {
	state, err := s.getChronicleState(ctx)
	if err != nil {
		return err
	}
	item := chronicleEntry{ID: state.NextID, TickID: tickID, Source: source, Summary: summary, CreatedAt: time.Now().UTC()}
	state.NextID++
	state.Items = append(state.Items, item)
	if len(state.Items) > 2000 {
		state.Items = state.Items[len(state.Items)-2000:]
	}
	return s.saveChronicleState(ctx, state)
}

func scoreLifecycle(q int) string {
	switch {
	case q >= 85:
		return "canonical"
	case q >= 65:
		return "active"
	case q >= 45:
		return "legacy"
	default:
		return "archived"
	}
}

func clipScore(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func normalizeValidatorIDs(ids []string, fallback string) []string {
	out := make([]string, 0, len(ids)+1)
	seen := map[string]struct{}{}
	for _, it := range ids {
		v := strings.TrimSpace(it)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	if len(out) == 0 {
		fb := strings.TrimSpace(fallback)
		if fb != "" {
			out = append(out, fb)
		}
	}
	return out
}

func normalizeMetabolismWeights(e, v, a, t float64) (float64, float64, float64, float64) {
	if e < 0 {
		e = 0
	}
	if v < 0 {
		v = 0
	}
	if a < 0 {
		a = 0
	}
	if t < 0 {
		t = 0
	}
	sum := e + v + a + t
	if sum <= 0 {
		return 0.25, 0.25, 0.25, 0.25
	}
	return e / sum, v / sum, a / sum, t / sum
}

func weightedMetabolismQ(e, v, a, t int, we, wv, wa, wt float64) int {
	score := float64(e)*we + float64(v)*wv + float64(a)*wa + float64(t)*wt
	return clipScore(int(score + 0.5))
}

func (s *Server) runMetabolismCycle(ctx context.Context, tickID int64) (metabolismReport, error) {
	interval := s.cfg.MetabolismInterval
	if interval <= 0 {
		interval = 60
	}
	if tickID > 0 && tickID%int64(interval) != 0 {
		return metabolismReport{TickID: tickID, CycleAt: time.Now().UTC(), Note: "not_due"}, nil
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	scores, err := s.getMetabolismScoreState(ctx)
	if err != nil {
		return metabolismReport{}, err
	}
	edges, err := s.getMetabolismEdgeState(ctx)
	if err != nil {
		return metabolismReport{}, err
	}
	reports, err := s.getMetabolismReportState(ctx)
	if err != nil {
		return metabolismReport{}, err
	}

	now := time.Now().UTC()
	we, wv, wa, wt := normalizeMetabolismWeights(
		s.cfg.MetabolismWeightE,
		s.cfg.MetabolismWeightV,
		s.cfg.MetabolismWeightA,
		s.cfg.MetabolismWeightT,
	)
	topK := s.cfg.MetabolismTopK
	if topK <= 0 {
		topK = 100
	}
	minValidators := s.cfg.MetabolismMinValidators
	if minValidators <= 0 {
		minValidators = 1
	}
	transitionCount := 0
	archivedCount := 0
	scoredCount := 0

	entries, _ := s.store.ListKBEntries(ctx, "", "", 5000)
	for _, e := range entries {
		if e.Deleted {
			continue
		}
		contentID := fmt.Sprintf("kb:%d", e.ID)
		eScore := clipScore(len([]rune(e.Content)) / 12)
		vScore := clipScore(int(e.Version*12 + 20))
		aScore := 60
		if now.Sub(e.UpdatedAt) > 14*24*time.Hour {
			aScore = 35
		}
		tScore := 55
		q := weightedMetabolismQ(eScore, vScore, aScore, tScore, we, wv, wa, wt)
		life := scoreLifecycle(q)
		prev, ok := scores.Items[contentID]
		if ok && prev.Lifecycle != life {
			transitionCount++
			if life == "archived" {
				archivedCount++
			}
		}
		scores.Items[contentID] = metabolismScore{
			ContentID:   contentID,
			SourceType:  "kb",
			E:           eScore,
			V:           vScore,
			A:           aScore,
			T:           tScore,
			Q:           q,
			Lifecycle:   life,
			Evidence:    fmt.Sprintf("version=%d updated_at=%s", e.Version, e.UpdatedAt.Format(time.RFC3339)),
			UpdatedAt:   now,
			UpdatedTick: tickID,
		}
		scoredCount++
	}

	ganglia, _ := s.store.ListGanglia(ctx, "", "", "", 5000)
	for _, g := range ganglia {
		contentID := fmt.Sprintf("ganglion:%d", g.ID)
		eScore := clipScore(len([]rune(g.Description+g.Implementation+g.Validation)) / 18)
		vScore := clipScore(int(g.ScoreAvgMilli/50 + g.ScoreCount*4))
		aScore := clipScore(int(g.IntegrationsCount*10 + 20))
		tScore := 70
		q := weightedMetabolismQ(eScore, vScore, aScore, tScore, we, wv, wa, wt)
		life := scoreLifecycle(q)
		prev, ok := scores.Items[contentID]
		if ok && prev.Lifecycle != life {
			transitionCount++
			if life == "archived" {
				archivedCount++
			}
		}
		scores.Items[contentID] = metabolismScore{
			ContentID:   contentID,
			SourceType:  "ganglia",
			E:           eScore,
			V:           vScore,
			A:           aScore,
			T:           tScore,
			Q:           q,
			Lifecycle:   life,
			Evidence:    fmt.Sprintf("integrations=%d score_count=%d", g.IntegrationsCount, g.ScoreCount),
			UpdatedAt:   now,
			UpdatedTick: tickID,
		}
		scoredCount++
		if life == "archived" && g.LifeState != "archived" {
			_, _ = s.store.UpdateGanglionLifeState(ctx, g.ID, "archived")
		}
	}

	// Cluster compression: keep top-K per source_type to prevent unbounded growth.
	clusterCompressed := 0
	clusters := map[string][]metabolismScore{}
	for _, it := range scores.Items {
		cluster := strings.TrimSpace(it.SourceType)
		if cluster == "" {
			cluster = "unknown"
		}
		clusters[cluster] = append(clusters[cluster], it)
	}
	for cluster, list := range clusters {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Q == list[j].Q {
				return list[i].UpdatedAt.After(list[j].UpdatedAt)
			}
			return list[i].Q > list[j].Q
		})
		if len(list) <= topK {
			continue
		}
		for _, it := range list[topK:] {
			cur, ok := scores.Items[it.ContentID]
			if !ok {
				continue
			}
			if cur.Lifecycle != "archived" {
				cur.Lifecycle = "archived"
				cur.UpdatedAt = now
				cur.UpdatedTick = tickID
				cur.Evidence = strings.TrimSpace(cur.Evidence + fmt.Sprintf(" | cluster_compressed:%s", cluster))
				scores.Items[it.ContentID] = cur
				clusterCompressed++
				archivedCount++
				transitionCount++
			}
		}
	}

	// Supersession validator gate: require min validators for active edges.
	edgeChanged := false
	activeSupersessions := 0
	pendingSupersessions := 0
	for i := range edges.Items {
		validators := normalizeValidatorIDs(edges.Items[i].Validators, edges.Items[i].CreatedBy)
		edges.Items[i].Validators = validators
		edges.Items[i].ValidatorCount = len(validators)
		if edges.Items[i].ValidatorCount >= minValidators {
			if edges.Items[i].Status == "pending_validation" {
				edges.Items[i].Status = "active"
				edges.Items[i].UpdatedAt = now
				edgeChanged = true
			}
		} else {
			if edges.Items[i].Status == "active" {
				edges.Items[i].Status = "pending_validation"
				edges.Items[i].UpdatedAt = now
				edgeChanged = true
			}
		}
		switch strings.ToLower(strings.TrimSpace(edges.Items[i].Status)) {
		case "active":
			activeSupersessions++
		case "pending_validation":
			pendingSupersessions++
		}
	}

	report := metabolismReport{
		TickID:               tickID,
		CycleAt:              now,
		ScoredCount:          scoredCount,
		TransitionCount:      transitionCount,
		ArchivedCount:        archivedCount,
		SupersessionSize:     len(edges.Items),
		ClusterCompressed:    clusterCompressed,
		ActiveSupersessions:  activeSupersessions,
		PendingSupersessions: pendingSupersessions,
		MinValidators:        minValidators,
		Note: fmt.Sprintf(
			"weights(E=%.2f,V=%.2f,A=%.2f,T=%.2f) top_k=%d",
			we, wv, wa, wt, topK,
		),
	}
	reports.Items = append(reports.Items, report)
	if len(reports.Items) > 1000 {
		reports.Items = reports.Items[len(reports.Items)-1000:]
	}

	if err := s.saveMetabolismScoreState(ctx, scores); err != nil {
		return metabolismReport{}, err
	}
	if edgeChanged {
		if err := s.saveMetabolismEdgeState(ctx, edges); err != nil {
			return metabolismReport{}, err
		}
	}
	if err := s.saveMetabolismReportState(ctx, reports); err != nil {
		return metabolismReport{}, err
	}
	return report, nil
}

func (s *Server) handleMetabolismScore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	contentID := strings.TrimSpace(r.URL.Query().Get("content_id"))
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getMetabolismScoreState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if contentID != "" {
		it, ok := state.Items[contentID]
		if !ok {
			writeError(w, http.StatusNotFound, "score not found")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"item": it})
		return
	}
	items := make([]metabolismScore, 0, len(state.Items))
	for _, it := range state.Items {
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Q > items[j].Q })
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleMetabolismSupersede(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req metabolismSupersedeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.NewID = strings.TrimSpace(req.NewID)
	req.OldID = strings.TrimSpace(req.OldID)
	req.Relationship = strings.TrimSpace(strings.ToLower(req.Relationship))
	validators := normalizeValidatorIDs(req.Validators, userID)
	if req.NewID == "" || req.OldID == "" || req.Relationship == "" {
		writeError(w, http.StatusBadRequest, "new_id, old_id, relationship are required")
		return
	}
	minValidators := s.cfg.MetabolismMinValidators
	if minValidators <= 0 {
		minValidators = 1
	}
	status := "active"
	if len(validators) < minValidators {
		status = "pending_validation"
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getMetabolismEdgeState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	item := metabolismSupersessionEdge{
		ID:             state.NextID,
		NewID:          req.NewID,
		OldID:          req.OldID,
		Relationship:   req.Relationship,
		Status:         status,
		CreatedBy:      userID,
		Validators:     validators,
		ValidatorCount: len(validators),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	state.NextID++
	state.Items = append(state.Items, item)
	if err := s.saveMetabolismEdgeState(r.Context(), state); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleMetabolismDispute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req metabolismDisputeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.SupersessionID <= 0 || req.Reason == "" {
		writeError(w, http.StatusBadRequest, "supersession_id and reason are required")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getMetabolismEdgeState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range state.Items {
		if state.Items[i].ID != req.SupersessionID {
			continue
		}
		now := time.Now().UTC()
		state.Items[i].Status = "disputed"
		state.Items[i].DisputedBy = userID
		state.Items[i].DisputeReason = req.Reason
		state.Items[i].DisputedAt = &now
		state.Items[i].UpdatedAt = now
		if err := s.saveMetabolismEdgeState(r.Context(), state); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"item": state.Items[i]})
		return
	}
	writeError(w, http.StatusNotFound, "supersession edge not found")
}

func (s *Server) handleMetabolismReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	reports, err := s.getMetabolismReportState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	edges, _ := s.getMetabolismEdgeState(r.Context())
	scores, _ := s.getMetabolismScoreState(r.Context())
	items := reports.Items
	sort.SliceStable(items, func(i, j int) bool { return items[i].CycleAt.After(items[j].CycleAt) })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"last_cycle": func() any {
			if len(items) > 0 {
				return items[0]
			}
			return nil
		}(),
		"items":             items,
		"supersession_size": len(edges.Items),
		"score_count":       len(scores.Items),
	})
}
