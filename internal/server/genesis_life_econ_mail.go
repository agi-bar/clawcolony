package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"clawcolony/internal/economy"
	"clawcolony/internal/store"
)

type mailListCreateRequest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	InitialUsers []string `json:"initial_users"`
}

type mailListJoinLeaveRequest struct {
	ListID string `json:"list_id"`
}

type mailSendListRequest struct {
	ListID  string `json:"list_id"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

type tokenTransferRequest struct {
	ToUserID string `json:"to_user_id"`
	Amount   int64  `json:"amount"`
	Memo     string `json:"memo"`
}

type tokenTipRequest struct {
	ToUserID string `json:"to_user_id"`
	Amount   int64  `json:"amount"`
	Reason   string `json:"reason"`
}

type tokenWishCreateRequest struct {
	Title        string `json:"title"`
	Reason       string `json:"reason"`
	TargetAmount int64  `json:"target_amount"`
}

type tokenWishFulfillRequest struct {
	WishID         string `json:"wish_id"`
	GrantedAmount  int64  `json:"granted_amount"`
	FulfillComment string `json:"fulfill_comment"`
}

type lifeHibernateRequest struct {
	Reason string `json:"reason"`
}

type lifeWakeRequest struct {
	UserID string `json:"user_id"`
	Reason string `json:"reason"`
}

type lifeSetWillRequest struct {
	Note          string                `json:"note"`
	Beneficiaries []lifeWillBeneficiary `json:"beneficiaries"`
	ToolHeirs     []string              `json:"tool_heirs"`
}

type bountyPostRequest struct {
	Description string `json:"description"`
	Reward      int64  `json:"reward"`
	Criteria    string `json:"criteria"`
	Deadline    string `json:"deadline"`
}

type bountyClaimRequest struct {
	BountyID int64  `json:"bounty_id"`
	Note     string `json:"note"`
}

type bountyVerifyRequest struct {
	BountyID        int64  `json:"bounty_id"`
	Approved        bool   `json:"approved"`
	CandidateUserID string `json:"candidate_user_id"`
	Note            string `json:"note"`
}

type genesisBootstrapStartRequest struct {
	ProposerUserID      string `json:"proposer_user_id"`
	Title               string `json:"title"`
	Reason              string `json:"reason"`
	Constitution        string `json:"constitution"`
	CosignQuorum        int    `json:"cosign_quorum"`
	ReviewWindowSeconds int    `json:"review_window_seconds"`
	VoteWindowSeconds   int    `json:"vote_window_seconds"`
}

type genesisBootstrapSealRequest struct {
	UserID             string `json:"user_id"`
	ProposalID         int64  `json:"proposal_id"`
	SealReason         string `json:"seal_reason"`
	ConstitutionDigest string `json:"constitution_digest"`
}

func normalizeGenesisCosignQuorum(v int) int {
	if v <= 0 {
		return 3
	}
	if v > 1000 {
		return 1000
	}
	return v
}

func normalizeGenesisReviewWindowSeconds(v int) int {
	return normalizeWorkflowWindowSeconds(v, defaultGenesisReviewWindowSeconds)
}

func normalizeGenesisVoteWindowSeconds(v int) int {
	return normalizeWorkflowWindowSeconds(v, defaultGenesisVoteWindowSeconds)
}

func newMailListID() string {
	return fmt.Sprintf("list-%d-%04d", time.Now().UTC().UnixMilli(), time.Now().UTC().Nanosecond()%10000)
}

func newWishID() string {
	return fmt.Sprintf("wish-%d-%04d", time.Now().UTC().UnixMilli(), time.Now().UTC().Nanosecond()%10000)
}

func (s *Server) handleMailLists(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, ok := s.requireAPIKeyUserID(w, r)
	if !ok {
		return
	}
	keyword := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("keyword")))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)

	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getMailingListState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]mailingList, 0, len(state.Lists))
	for _, it := range state.Lists {
		if userID != "" {
			found := false
			for _, m := range it.Members {
				if m == userID {
					found = true
					break
				}
			}
			if !found && it.OwnerUserID != userID {
				continue
			}
		}
		if keyword != "" {
			text := strings.ToLower(it.ListID + " " + it.Name + " " + it.Description)
			if !strings.Contains(text, keyword) {
				continue
			}
		}
		items = append(items, it)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleMailListCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ownerUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailListCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), ownerUserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	members := normalizeUniqueUsers(append(req.InitialUsers, ownerUserID))
	now := time.Now().UTC()
	item := mailingList{
		ListID:      newMailListID(),
		Name:        req.Name,
		Description: req.Description,
		OwnerUserID: ownerUserID,
		Members:     members,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getMailingListState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	state.Lists = append(state.Lists, item)
	if err := s.saveMailingListState(r.Context(), state); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleMailListJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailListJoinLeaveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ListID = strings.TrimSpace(req.ListID)
	if req.ListID == "" {
		writeError(w, http.StatusBadRequest, "list_id is required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getMailingListState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range state.Lists {
		if state.Lists[i].ListID != req.ListID {
			continue
		}
		state.Lists[i].Members = normalizeUniqueUsers(append(state.Lists[i].Members, userID))
		state.Lists[i].UpdatedAt = time.Now().UTC()
		if err := s.saveMailingListState(r.Context(), state); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"item": state.Lists[i]})
		return
	}
	writeError(w, http.StatusNotFound, "mail list not found")
}

func (s *Server) handleMailListLeave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailListJoinLeaveRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ListID = strings.TrimSpace(req.ListID)
	if req.ListID == "" {
		writeError(w, http.StatusBadRequest, "list_id is required")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getMailingListState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range state.Lists {
		if state.Lists[i].ListID != req.ListID {
			continue
		}
		before := state.Lists[i].Members
		after := make([]string, 0, len(before))
		for _, m := range before {
			if m == userID {
				continue
			}
			after = append(after, m)
		}
		state.Lists[i].Members = after
		state.Lists[i].UpdatedAt = time.Now().UTC()
		if err := s.saveMailingListState(r.Context(), state); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"item": state.Lists[i]})
		return
	}
	writeError(w, http.StatusNotFound, "mail list not found")
}

func (s *Server) handleMailSendList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	fromUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req mailSendListRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ListID = strings.TrimSpace(req.ListID)
	req.Subject = strings.TrimSpace(req.Subject)
	req.Body = strings.TrimSpace(req.Body)
	if req.ListID == "" {
		writeError(w, http.StatusBadRequest, "list_id is required")
		return
	}
	if req.Subject == "" && req.Body == "" {
		writeError(w, http.StatusBadRequest, "subject or body is required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), fromUserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	genesisStateMu.Lock()
	state, err := s.getMailingListState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var listItem *mailingList
	idx := -1
	for i := range state.Lists {
		if state.Lists[i].ListID == req.ListID {
			listItem = &state.Lists[i]
			idx = i
			break
		}
	}
	if listItem == nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusNotFound, "mail list not found")
		return
	}
	to := make([]string, 0, len(listItem.Members))
	for _, m := range listItem.Members {
		m = strings.TrimSpace(m)
		if m == "" || m == fromUserID {
			continue
		}
		to = append(to, m)
	}
	listItem.UpdatedAt = time.Now().UTC()
	listItem.LastMailAt = listItem.UpdatedAt
	listItem.MessageCount++
	state.Lists[idx] = *listItem
	if err := s.saveMailingListState(r.Context(), state); err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	genesisStateMu.Unlock()
	if len(to) == 0 {
		writeJSON(w, http.StatusAccepted, map[string]any{"item": map[string]any{"list_id": req.ListID, "to_count": 0}})
		return
	}
	totalTokens := economy.CalculateToken(req.Subject+req.Body) * int64(len(to))
	chargePreview, chargeErr := s.previewCommunicationCharge(r.Context(), fromUserID, totalTokens)
	if chargeErr != nil {
		if errors.Is(chargeErr, store.ErrInsufficientBalance) {
			writeError(w, http.StatusPaymentRequired, "insufficient token balance for communication overage")
			return
		}
		writeError(w, http.StatusInternalServerError, chargeErr.Error())
		return
	}
	item, err := s.store.SendMail(r.Context(), store.MailSendInput{
		From:    fromUserID,
		To:      to,
		Subject: req.Subject,
		Body:    req.Body,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	chargeErrText := ""
	if err := s.commitCommunicationCharge(r.Context(), chargePreview, "comm.mail.send_list", map[string]any{
		"list_id":     req.ListID,
		"to_count":    len(to),
		"subject_len": utf8.RuneCountInString(req.Subject),
		"body_len":    utf8.RuneCountInString(req.Body),
	}); err != nil {
		chargeErrText = err.Error()
	}
	s.pushUnreadMailHint(r.Context(), fromUserID, to, req.Subject)
	resp := map[string]any{"item": item, "list": listItem}
	if chargeErrText != "" {
		resp["charge_error"] = chargeErrText
	}
	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) handleTokenTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	fromUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req tokenTransferRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ToUserID = strings.TrimSpace(req.ToUserID)
	req.Memo = strings.TrimSpace(req.Memo)
	if req.ToUserID == "" {
		writeError(w, http.StatusBadRequest, "to_user_id is required")
		return
	}
	if isSystemTokenUserID(fromUserID) || isSystemTokenUserID(req.ToUserID) {
		writeError(w, http.StatusBadRequest, "system accounts cannot participate in transfer")
		return
	}
	if fromUserID == req.ToUserID {
		writeError(w, http.StatusBadRequest, "from_user_id and to_user_id must differ")
		return
	}
	if req.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be > 0")
		return
	}
	if err := s.ensureUserAlive(r.Context(), fromUserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if _, err := s.store.GetBot(r.Context(), req.ToUserID); err != nil {
		writeError(w, http.StatusBadRequest, "to_user_id not found")
		return
	}
	debit, err := s.store.Consume(r.Context(), fromUserID, req.Amount)
	if err != nil {
		if err == store.ErrInsufficientBalance {
			writeError(w, http.StatusBadRequest, "insufficient balance")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	credit, err := s.store.Recharge(r.Context(), req.ToUserID, req.Amount)
	if err != nil {
		_, _ = s.store.Recharge(r.Context(), fromUserID, req.Amount)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	meta, _ := json.Marshal(map[string]any{"to_user_id": req.ToUserID, "memo": req.Memo})
	_, _ = s.store.AppendCostEvent(r.Context(), store.CostEvent{UserID: fromUserID, CostType: "econ.transfer.out", Amount: req.Amount, Units: 1, MetaJSON: string(meta)})
	_, _ = s.store.AppendCostEvent(r.Context(), store.CostEvent{UserID: req.ToUserID, CostType: "econ.transfer.in", Amount: req.Amount, Units: 1, MetaJSON: string(meta)})
	writeJSON(w, http.StatusAccepted, map[string]any{"debit": debit, "credit": credit})
}

func (s *Server) handleTokenTip(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	fromUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req tokenTipRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	transfer := tokenTransferRequest{
		ToUserID: strings.TrimSpace(req.ToUserID),
		Amount:   req.Amount,
		Memo:     strings.TrimSpace(req.Reason),
	}
	if transfer.ToUserID == "" {
		writeError(w, http.StatusBadRequest, "to_user_id is required")
		return
	}
	if isSystemTokenUserID(fromUserID) || isSystemTokenUserID(transfer.ToUserID) {
		writeError(w, http.StatusBadRequest, "system accounts cannot participate in tip")
		return
	}
	if transfer.Amount <= 0 {
		writeError(w, http.StatusBadRequest, "amount must be > 0")
		return
	}
	if err := s.ensureUserAlive(r.Context(), fromUserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if _, err := s.store.GetBot(r.Context(), transfer.ToUserID); err != nil {
		writeError(w, http.StatusBadRequest, "to_user_id not found")
		return
	}
	debit, err := s.store.Consume(r.Context(), fromUserID, transfer.Amount)
	if err != nil {
		if err == store.ErrInsufficientBalance {
			writeError(w, http.StatusBadRequest, "insufficient balance")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	credit, err := s.store.Recharge(r.Context(), transfer.ToUserID, transfer.Amount)
	if err != nil {
		_, _ = s.store.Recharge(r.Context(), fromUserID, transfer.Amount)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	meta, _ := json.Marshal(map[string]any{"to_user_id": transfer.ToUserID, "reason": strings.TrimSpace(req.Reason)})
	_, _ = s.store.AppendCostEvent(r.Context(), store.CostEvent{UserID: fromUserID, CostType: "econ.tip.out", Amount: req.Amount, Units: 1, MetaJSON: string(meta)})
	_, _ = s.store.AppendCostEvent(r.Context(), store.CostEvent{UserID: strings.TrimSpace(req.ToUserID), CostType: "econ.tip.in", Amount: req.Amount, Units: 1, MetaJSON: string(meta)})
	writeJSON(w, http.StatusAccepted, map[string]any{"debit": debit, "credit": credit})
}

type responseCapture struct {
	http.ResponseWriter
	code int
}

func (c *responseCapture) WriteHeader(statusCode int) {
	c.code = statusCode
	c.ResponseWriter.WriteHeader(statusCode)
}

func (s *Server) handleTokenWishCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req tokenWishCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.TargetAmount <= 0 {
		writeError(w, http.StatusBadRequest, "target_amount is required")
		return
	}
	if req.Title == "" {
		req.Title = "token wish"
	}
	if isSystemTokenUserID(userID) {
		writeError(w, http.StatusBadRequest, "system accounts cannot create wishes")
		return
	}
	if err := s.ensureUserAlive(r.Context(), userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	now := time.Now().UTC()
	item := tokenWish{
		WishID:        newWishID(),
		UserID:        userID,
		Title:         req.Title,
		Reason:        req.Reason,
		TargetAmount:  req.TargetAmount,
		Status:        "open",
		CreatedAt:     now,
		UpdatedAt:     now,
		GrantedAmount: 0,
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getTokenWishState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	state.Items = append(state.Items, item)
	if err := s.saveTokenWishState(r.Context(), state); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleTokenWishes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getTokenWishState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]tokenWish, 0, len(state.Items))
	for _, it := range state.Items {
		if userID != "" && it.UserID != userID {
			continue
		}
		if status != "" && it.Status != status {
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

func (s *Server) handleTokenWishFulfill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	fulfilledBy, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req tokenWishFulfillRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.WishID = strings.TrimSpace(req.WishID)
	req.FulfillComment = strings.TrimSpace(req.FulfillComment)
	if req.WishID == "" {
		writeError(w, http.StatusBadRequest, "wish_id is required")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getTokenWishState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range state.Items {
		if state.Items[i].WishID != req.WishID {
			continue
		}
		if state.Items[i].Status == "fulfilled" {
			writeJSON(w, http.StatusAccepted, map[string]any{"item": state.Items[i]})
			return
		}
		amount := req.GrantedAmount
		if amount <= 0 {
			amount = state.Items[i].TargetAmount
		}
		if amount <= 0 {
			writeError(w, http.StatusBadRequest, "granted_amount must be > 0")
			return
		}
		if _, _, err := s.transferFromTreasury(r.Context(), state.Items[i].UserID, amount); err != nil {
			if errors.Is(err, store.ErrInsufficientBalance) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		now := time.Now().UTC()
		state.Items[i].Status = "fulfilled"
		state.Items[i].UpdatedAt = now
		state.Items[i].GrantedAmount = amount
		state.Items[i].FulfilledBy = fulfilledBy
		state.Items[i].FulfillComment = req.FulfillComment
		state.Items[i].FulfilledAt = &now
		if err := s.saveTokenWishState(r.Context(), state); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"item": state.Items[i]})
		return
	}
	writeError(w, http.StatusNotFound, "wish not found")
}

func (s *Server) handleLifeHibernate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.tokenEconomyV2Enabled() {
		writeError(w, http.StatusConflict, "manual hibernate is disabled in token economy v2")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req lifeHibernateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	life, err := s.store.GetUserLifeState(r.Context(), userID)
	if err != nil && !errors.Is(err, store.ErrUserLifeStateNotFound) {
		writeError(w, http.StatusInternalServerError, "failed to load current life state")
		return
	}
	if normalizeLifeStateForServer(life.State) == "dead" {
		writeError(w, http.StatusConflict, "dead user cannot hibernate")
		return
	}
	updated, _, err := s.applyUserLifeState(r.Context(), store.UserLifeState{
		UserID:    userID,
		State:     "hibernated",
		Reason:    req.Reason,
		UpdatedAt: time.Now().UTC(),
	}, store.UserLifeStateAuditMeta{
		SourceModule: "life.hibernate",
		SourceRef:    "api:/api/v1/life/hibernate",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update life state")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": updated})
}

func (s *Server) handleLifeWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.tokenEconomyV2Enabled() {
		writeError(w, http.StatusConflict, "manual wake is disabled in token economy v2")
		return
	}
	actorUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req lifeWakeRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	req.Reason = strings.TrimSpace(req.Reason)
	if req.UserID == "" {
		writeError(w, http.StatusBadRequest, "user_id is required")
		return
	}
	life, err := s.store.GetUserLifeState(r.Context(), req.UserID)
	if err != nil && !errors.Is(err, store.ErrUserLifeStateNotFound) {
		writeError(w, http.StatusInternalServerError, "failed to load current life state")
		return
	}
	if normalizeLifeStateForServer(life.State) == "dead" {
		writeError(w, http.StatusConflict, "dead user cannot wake")
		return
	}
	updated, _, err := s.applyUserLifeState(r.Context(), store.UserLifeState{
		UserID:         req.UserID,
		State:          "alive",
		DyingSinceTick: 0,
		DeadAtTick:     0,
		Reason:         req.Reason,
		UpdatedAt:      time.Now().UTC(),
	}, store.UserLifeStateAuditMeta{
		SourceModule: "life.wake",
		SourceRef:    "api:/api/v1/life/wake",
		ActorUserID:  actorUserID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update life state")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": updated})
}

func (s *Server) handleLifeSetWill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req lifeSetWillRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if len(req.Beneficiaries) == 0 {
		writeError(w, http.StatusBadRequest, "beneficiaries is required")
		return
	}
	if _, err := lifeWillDistribution(10000, req.Beneficiaries); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getLifeWillState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	item := lifeWill{
		UserID:        userID,
		Note:          req.Note,
		Beneficiaries: req.Beneficiaries,
		ToolHeirs:     normalizeUniqueUsers(req.ToolHeirs),
		CreatedAt:     now,
		UpdatedAt:     now,
		Executed:      false,
	}
	if prev, ok := state.Items[userID]; ok {
		item.CreatedAt = prev.CreatedAt
	}
	state.Items[userID] = item
	if err := s.saveLifeWillState(r.Context(), state); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleLifeWill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID := strings.TrimSpace(r.URL.Query().Get("user_id"))
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getLifeWillState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if userID == "" {
		items := make([]lifeWill, 0, len(state.Items))
		for _, it := range state.Items {
			items = append(items, it)
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	it, ok := state.Items[userID]
	if !ok {
		writeError(w, http.StatusNotFound, "will not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": it})
}

func (s *Server) executeWillIfNeeded(ctx context.Context, userID string, tickID int64, balance int64) {
	if strings.TrimSpace(userID) == "" {
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getLifeWillState(ctx)
	if err != nil {
		return
	}
	it, ok := state.Items[userID]
	if !ok || it.Executed {
		return
	}
	dist, err := lifeWillDistribution(balance, it.Beneficiaries)
	if err != nil {
		it.Executed = true
		now := time.Now().UTC()
		it.ExecutedAt = &now
		it.ExecutionTick = tickID
		it.ExecutionNote = "invalid_will_distribution: " + err.Error()
		state.Items[userID] = it
		_ = s.saveLifeWillState(ctx, state)
		return
	}
	if balance > 0 {
		if _, err := s.store.Consume(ctx, userID, balance); err != nil {
			it.ExecutionNote = "consume_failed: " + err.Error()
			state.Items[userID] = it
			_ = s.saveLifeWillState(ctx, state)
			return
		}
		for uid, amount := range dist {
			if amount <= 0 {
				continue
			}
			_, _ = s.store.Recharge(ctx, uid, amount)
			it.TransferredSum += amount
		}
	}
	now := time.Now().UTC()
	it.Executed = true
	it.ExecutedAt = &now
	it.ExecutionTick = tickID
	if it.ExecutionNote == "" {
		it.ExecutionNote = "ok"
	}
	state.Items[userID] = it
	_ = s.saveLifeWillState(ctx, state)
}

func (s *Server) handleBountyPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	posterUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req bountyPostRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Description = strings.TrimSpace(req.Description)
	req.Criteria = strings.TrimSpace(req.Criteria)
	if req.Description == "" || req.Reward <= 0 {
		writeError(w, http.StatusBadRequest, "description and reward are required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), posterUserID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	var deadlinePtr *time.Time
	if strings.TrimSpace(req.Deadline) != "" {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(req.Deadline)); err == nil {
			u := t.UTC()
			deadlinePtr = &u
		}
	}
	if _, err := s.store.Consume(r.Context(), posterUserID, req.Reward); err != nil {
		if err == store.ErrInsufficientBalance {
			writeError(w, http.StatusBadRequest, "insufficient balance")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getBountyState(r.Context())
	if err != nil {
		_, _ = s.store.Recharge(r.Context(), posterUserID, req.Reward)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	item := bountyItem{
		BountyID:     state.NextID,
		PosterUserID: posterUserID,
		Description:  req.Description,
		Reward:       req.Reward,
		Criteria:     req.Criteria,
		DeadlineAt:   deadlinePtr,
		Status:       "open",
		EscrowAmount: req.Reward,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	state.NextID++
	state.Items = append(state.Items, item)
	if err := s.saveBountyState(r.Context(), state); err != nil {
		_, _ = s.store.Recharge(r.Context(), posterUserID, req.Reward)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
}

func (s *Server) handleBountyList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	poster := strings.TrimSpace(r.URL.Query().Get("poster_user_id"))
	claimedBy := strings.TrimSpace(r.URL.Query().Get("claimed_by"))
	limit := parseLimit(r.URL.Query().Get("limit"), 200)
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getBountyState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]bountyItem, 0, len(state.Items))
	for _, it := range state.Items {
		if status != "" && it.Status != status {
			continue
		}
		if poster != "" && it.PosterUserID != poster {
			continue
		}
		if claimedBy != "" && it.ClaimedBy != claimedBy {
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

func (s *Server) handleBountyGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	bountyID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("bounty_id")), 10, 64)
	if err != nil || bountyID <= 0 {
		writeError(w, http.StatusBadRequest, "bounty_id is required")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getBountyState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, it := range state.Items {
		if it.BountyID != bountyID {
			continue
		}
		writeJSON(w, http.StatusOK, map[string]any{"item": it})
		return
	}
	writeError(w, http.StatusNotFound, "bounty not found")
}

func (s *Server) handleBountyClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req bountyClaimRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Note = strings.TrimSpace(req.Note)
	if req.BountyID <= 0 {
		writeError(w, http.StatusBadRequest, "bounty_id is required")
		return
	}
	if err := s.ensureUserAlive(r.Context(), userID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getBountyState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range state.Items {
		if state.Items[i].BountyID != req.BountyID {
			continue
		}
		if state.Items[i].Status != "open" {
			writeError(w, http.StatusConflict, "bounty is not open")
			return
		}
		now := time.Now().UTC()
		state.Items[i].Status = "claimed"
		state.Items[i].ClaimedBy = userID
		state.Items[i].ClaimNote = req.Note
		state.Items[i].ClaimedAt = &now
		state.Items[i].UpdatedAt = now
		if err := s.saveBountyState(r.Context(), state); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"item": state.Items[i]})
		return
	}
	writeError(w, http.StatusNotFound, "bounty not found")
}

func (s *Server) handleBountyVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	approverUserID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var req bountyVerifyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.CandidateUserID = strings.TrimSpace(req.CandidateUserID)
	req.Note = strings.TrimSpace(req.Note)
	if req.BountyID <= 0 {
		writeError(w, http.StatusBadRequest, "bounty_id is required")
		return
	}
	genesisStateMu.Lock()
	state, err := s.getBountyState(r.Context())
	if err != nil {
		genesisStateMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range state.Items {
		if state.Items[i].BountyID != req.BountyID {
			continue
		}
		if state.Items[i].Status != "claimed" && state.Items[i].Status != "open" {
			genesisStateMu.Unlock()
			writeError(w, http.StatusConflict, "bounty is not verifiable")
			return
		}
		now := time.Now().UTC()
		state.Items[i].VerifyNote = req.Note
		state.Items[i].VerifiedAt = &now
		state.Items[i].UpdatedAt = now
		if req.Approved {
			receiver := strings.TrimSpace(state.Items[i].ClaimedBy)
			if receiver == "" {
				receiver = req.CandidateUserID
			}
			if receiver == "" {
				genesisStateMu.Unlock()
				writeError(w, http.StatusBadRequest, "candidate_user_id is required when no claimed_by")
				return
			}
			if _, err := s.store.Recharge(r.Context(), receiver, state.Items[i].EscrowAmount); err != nil {
				genesisStateMu.Unlock()
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			state.Items[i].Status = "paid"
			state.Items[i].ReleasedTo = receiver
			state.Items[i].ReleasedBy = approverUserID
			state.Items[i].ReleasedAt = &now
			state.Items[i].EscrowAmount = 0
		} else {
			state.Items[i].Status = "open"
			state.Items[i].ClaimedBy = ""
			state.Items[i].ClaimNote = ""
			state.Items[i].ClaimedAt = nil
		}
		if err := s.saveBountyState(r.Context(), state); err != nil {
			genesisStateMu.Unlock()
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		item := state.Items[i]
		genesisStateMu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]any{"item": item})
		return
	}
	genesisStateMu.Unlock()
	writeError(w, http.StatusNotFound, "bounty not found")
}

func (s *Server) runBountyBroker(ctx context.Context, tickID int64) (int, error) {
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	state, err := s.getBountyState(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	changed := 0
	for i := range state.Items {
		it := &state.Items[i]
		if it.Status == "paid" || it.Status == "expired" || it.Status == "canceled" {
			continue
		}
		if it.DeadlineAt == nil || now.Before(*it.DeadlineAt) {
			continue
		}
		if it.EscrowAmount > 0 {
			_, _ = s.store.Recharge(ctx, it.PosterUserID, it.EscrowAmount)
			it.EscrowAmount = 0
		}
		it.Status = "expired"
		it.UpdatedAt = now
		changed++
	}
	if changed > 0 {
		if err := s.saveBountyState(ctx, state); err != nil {
			return 0, err
		}
	}
	return changed, nil
}

func (s *Server) handleGenesisState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	st, err := s.getGenesisState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": st})
}

func (s *Server) handleGenesisBootstrapStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req genesisBootstrapStartRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ProposerUserID = strings.TrimSpace(req.ProposerUserID)
	req.Title = strings.TrimSpace(req.Title)
	req.Reason = strings.TrimSpace(req.Reason)
	req.Constitution = strings.TrimSpace(req.Constitution)
	req.CosignQuorum = normalizeGenesisCosignQuorum(req.CosignQuorum)
	if err := validateOptionalWorkflowWindowSeconds("review_window_seconds", req.ReviewWindowSeconds); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateOptionalWorkflowWindowSeconds("vote_window_seconds", req.VoteWindowSeconds); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.ReviewWindowSeconds = normalizeGenesisReviewWindowSeconds(req.ReviewWindowSeconds)
	req.VoteWindowSeconds = normalizeGenesisVoteWindowSeconds(req.VoteWindowSeconds)
	if req.ProposerUserID == "" {
		writeError(w, http.StatusBadRequest, "proposer_user_id is required")
		return
	}
	if req.Title == "" {
		req.Title = "创世宪章"
	}
	if req.Reason == "" {
		req.Reason = "创世协议：提交首份宪章并完成封存"
	}
	if req.Constitution == "" {
		req.Constitution = "宪章草案：遵循天道四律，治理可演化，执行可审计。"
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	st, err := s.getGenesisState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.Status == "sealed" {
		writeError(w, http.StatusConflict, "genesis is already sealed")
		return
	}
	if st.Status == "bootstrapping" && st.CharterProposalID > 0 {
		writeError(w, http.StatusConflict, "genesis bootstrap already started")
		return
	}
	now := time.Now().UTC()
	proposal, change, err := s.store.CreateKBProposal(r.Context(), store.KBProposal{
		ProposerUserID:    req.ProposerUserID,
		Title:             req.Title,
		Reason:            req.Reason,
		Status:            "discussing",
		VoteThresholdPct:  80,
		VoteWindowSeconds: req.VoteWindowSeconds,
	}, store.KBProposalChange{
		OpType:     "add",
		Section:    "governance",
		Title:      "宪法",
		OldContent: "",
		NewContent: req.Constitution,
		DiffText:   "+ 宪法: " + req.Constitution,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The genesis initiator is counted as the first cosigner by default.
	_, _ = s.store.EnrollKBProposal(r.Context(), proposal.ID, req.ProposerUserID)
	enrollments, _ := s.store.ListKBProposalEnrollments(r.Context(), proposal.ID)
	st.Status = "bootstrapping"
	st.BootstrapPhase = "cosign"
	st.CharterProposalID = proposal.ID
	st.StartedBy = req.ProposerUserID
	st.StartedAt = &now
	st.RequiredCosigns = req.CosignQuorum
	st.CurrentCosigns = len(enrollments)
	st.CosignOpenedAt = &now
	cosignDeadline := now.Add(time.Duration(req.ReviewWindowSeconds) * time.Second)
	st.CosignDeadlineAt = &cosignDeadline
	st.ReviewWindowSeconds = req.ReviewWindowSeconds
	st.VoteWindowSeconds = req.VoteWindowSeconds
	st.ReviewOpenedAt = nil
	st.ReviewDeadlineAt = nil
	st.VoteOpenedAt = nil
	st.VoteDeadlineAt = nil
	st.LastPhaseNote = fmt.Sprintf("bootstrap started, waiting for cosign quorum=%d", req.CosignQuorum)
	st.ConstitutionTitle = change.Title
	if len(req.Constitution) > 120 {
		st.ConstitutionDigest = req.Constitution[:120]
	} else {
		st.ConstitutionDigest = req.Constitution
	}
	if err := s.saveGenesisState(r.Context(), st); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	users, _ := s.listActiveUserIDs(r.Context())
	if len(users) > 0 {
		s.sendMailAndPushHint(r.Context(), clawWorldSystemID, users,
			"[GENESIS] 创世协议已启动"+refTag(skillGovernance),
			fmt.Sprintf(
				"proposal_id=%d\ntitle=%s\nphase=cosign\nrequired_cosigns=%d\nreview_window_seconds=%d\nvote_window_seconds=%d\n请先联署达到门槛，再进入审阅与投票。",
				proposal.ID, req.Title, req.CosignQuorum, req.ReviewWindowSeconds, req.VoteWindowSeconds,
			),
		)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"state": st, "proposal": proposal})
}

func (s *Server) handleGenesisBootstrapSeal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req genesisBootstrapSealRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.UserID = strings.TrimSpace(req.UserID)
	req.SealReason = strings.TrimSpace(req.SealReason)
	req.ConstitutionDigest = strings.TrimSpace(req.ConstitutionDigest)
	if req.UserID == "" || req.ProposalID <= 0 {
		writeError(w, http.StatusBadRequest, "user_id and proposal_id are required")
		return
	}
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	st, err := s.getGenesisState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if st.Status == "sealed" {
		writeJSON(w, http.StatusAccepted, map[string]any{"item": st})
		return
	}
	if st.CharterProposalID > 0 && st.CharterProposalID != req.ProposalID {
		writeError(w, http.StatusConflict, "proposal_id does not match active genesis charter")
		return
	}
	if st.BootstrapPhase != "" && st.BootstrapPhase != "applied" && st.BootstrapPhase != "sealed" {
		writeError(w, http.StatusConflict, "genesis bootstrap is not in applied phase")
		return
	}
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if proposal.Status != "applied" {
		writeError(w, http.StatusConflict, "charter proposal must be applied before seal")
		return
	}
	now := time.Now().UTC()
	st.Status = "sealed"
	st.BootstrapPhase = "sealed"
	st.CharterProposalID = req.ProposalID
	st.SealedBy = req.UserID
	st.SealedAt = &now
	if req.SealReason == "" {
		req.SealReason = "charter applied and sealed"
	}
	st.SealReason = req.SealReason
	st.LastPhaseNote = "genesis sealed"
	if req.ConstitutionDigest != "" {
		st.ConstitutionDigest = req.ConstitutionDigest
	}
	if err := s.saveGenesisState(r.Context(), st); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"item": st})
}

func (s *Server) runGenesisBootstrapInit(ctx context.Context) {
	genesisStateMu.Lock()
	defer genesisStateMu.Unlock()
	st, err := s.getGenesisState(ctx)
	if err != nil {
		return
	}
	if strings.TrimSpace(st.Status) == "" {
		st.Status = "idle"
		_ = s.saveGenesisState(ctx, st)
	}
}

func parseRFC3339OrUnix(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		u := t.UTC()
		return &u
	}
	if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
		t := time.Unix(v, 0).UTC()
		return &t
	}
	return nil
}
