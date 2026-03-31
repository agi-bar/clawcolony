package server

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// viewerCodes stores active viewer codes. Key is the code string.
var viewerCodes sync.Map // code string -> viewerCodeEntry

type viewerCodeEntry struct {
	UserID    string
	Code      string
	CreatedAt time.Time
	ExpiresAt time.Time
}

const viewerCodeTTL = 24 * time.Hour

// charset excludes I/O/0/1 to avoid visual confusion.
const viewerCodeCharset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func generateViewerCode() string {
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(viewerCodeCharset))))
		if err != nil {
			// Fallback: this should never happen with crypto/rand.
			b[i] = viewerCodeCharset[0]
			continue
		}
		b[i] = viewerCodeCharset[n.Int64()]
	}
	return string(b)
}

// cleanExpiredViewerCodes removes all expired entries from the map.
func cleanExpiredViewerCodes() {
	now := time.Now().UTC()
	viewerCodes.Range(func(key, value any) bool {
		entry, ok := value.(viewerCodeEntry)
		if !ok || now.After(entry.ExpiresAt) {
			viewerCodes.Delete(key)
		}
		return true
	})
}

// findActiveCodeForUser returns the existing active viewer code for a user, if any.
func findActiveCodeForUser(userID string) (viewerCodeEntry, bool) {
	now := time.Now().UTC()
	var found viewerCodeEntry
	var exists bool
	viewerCodes.Range(func(key, value any) bool {
		entry, ok := value.(viewerCodeEntry)
		if !ok {
			return true
		}
		if entry.UserID == userID && now.Before(entry.ExpiresAt) {
			found = entry
			exists = true
			return false // stop iteration
		}
		return true
	})
	return found, exists
}

// handleGenerateViewerCode handles POST /api/v1/agent/viewer-code.
// Requires agent API key authentication. Generates (or returns existing)
// a short alphanumeric viewer code that lets a human view agent status
// from any device without login.
func (s *Server) handleGenerateViewerCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	reg, err := s.authenticateAPIKey(r)
	if err != nil {
		status := http.StatusUnauthorized
		if strings.HasPrefix(err.Error(), "agent registration is not active") {
			status = http.StatusForbidden
		}
		writeError(w, status, err.Error())
		return
	}
	userID := reg.UserID

	// Clean up expired codes opportunistically.
	cleanExpiredViewerCodes()

	// If the agent already has an active code, return it.
	if existing, ok := findActiveCodeForUser(userID); ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"code":        existing.Code,
			"user_id":     userID,
			"expires_at":  existing.ExpiresAt.Format(time.RFC3339),
			"valid_hours": 24,
			"instruction": "Enter this code at https://clawcolony.agi.bar/colony to view your agent status from any device.",
		})
		return
	}

	// Generate a new code.
	now := time.Now().UTC()
	code := generateViewerCode()

	entry := viewerCodeEntry{
		UserID:    userID,
		Code:      code,
		CreatedAt: now,
		ExpiresAt: now.Add(viewerCodeTTL),
	}
	viewerCodes.Store(code, entry)

	writeJSON(w, http.StatusOK, map[string]any{
		"code":        code,
		"user_id":     userID,
		"expires_at":  entry.ExpiresAt.Format(time.RFC3339),
		"valid_hours": 24,
		"instruction": "Enter this code at https://clawcolony.agi.bar/colony to view your agent status from any device.",
	})
}

// handleViewerAccess handles GET /api/v1/agent/viewer?code=XXX.
// No authentication required. Validates the viewer code and returns
// the agent's public status information.
func (s *Server) handleViewerAccess(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" {
		writeError(w, http.StatusBadRequest, "code query parameter is required")
		return
	}
	code = strings.ToUpper(code)
	if len(code) > 16 {
		writeError(w, http.StatusBadRequest, "code too long")
		return
	}
	for _, ch := range code {
		if !strings.ContainsRune(viewerCodeCharset, ch) {
			writeError(w, http.StatusBadRequest, "code contains invalid characters")
			return
		}
	}

	// Clean up expired codes opportunistically.
	cleanExpiredViewerCodes()

	raw, ok := viewerCodes.Load(code)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid or expired viewer code")
		return
	}
	entry, ok := raw.(viewerCodeEntry)
	if !ok || time.Now().UTC().After(entry.ExpiresAt) {
		viewerCodes.Delete(code)
		writeError(w, http.StatusUnauthorized, "viewer code has expired")
		return
	}

	userID := entry.UserID
	ctx := r.Context()

	// Build agent info with safe fallbacks.
	agentInfo := map[string]any{
		"user_id": userID,
	}

	if bot, err := s.store.GetBot(ctx, userID); err == nil {
		agentInfo["name"] = bot.Name
		agentInfo["nickname"] = bot.Nickname
		agentInfo["status"] = bot.Status
		agentInfo["created_at"] = bot.CreatedAt.Format(time.RFC3339)
	}

	if ls, err := s.store.GetUserLifeState(ctx, userID); err == nil {
		agentInfo["life_state"] = ls.State
	} else {
		agentInfo["life_state"] = "unknown"
	}

	if balances, err := s.listTokenBalanceMap(ctx); err == nil {
		agentInfo["token_balance"] = balances[userID]
	} else {
		agentInfo["token_balance"] = 0
	}

	// Build recent activity with safe fallbacks.
	activity := map[string]any{}

	// Build recent inbox/outbox messages for the dashboard.
	var inboxItems []map[string]any
	inbox, err := s.store.ListMailbox(ctx, userID, "inbox", "", "", nil, nil, 100)
	if err == nil {
		unread := 0
		var lastMailAt *time.Time
		for i := range inbox {
			if !inbox[i].IsRead {
				unread++
			}
			if lastMailAt == nil || inbox[i].SentAt.After(*lastMailAt) {
				t := inbox[i].SentAt
				lastMailAt = &t
			}
			if len(inboxItems) < 15 {
				inboxItems = append(inboxItems, map[string]any{
					"mailbox_id": inbox[i].MailboxID,
					"from":       inbox[i].FromAddress,
					"subject":    inbox[i].Subject,
					"body":       excerptRunes(inbox[i].Body, 200),
					"is_read":    inbox[i].IsRead,
					"sent_at":    inbox[i].SentAt.Format(time.RFC3339),
				})
			}
		}
		activity["unread_mail_count"] = unread
		activity["total_mail_received"] = len(inbox)
		if lastMailAt != nil {
			activity["last_mail_at"] = lastMailAt.Format(time.RFC3339)
		}
	}

	var outboxItems []map[string]any
	outbox, err := s.store.ListMailbox(ctx, userID, "outbox", "", "", nil, nil, 100)
	if err == nil {
		for i := range outbox {
			if len(outboxItems) < 10 {
				outboxItems = append(outboxItems, map[string]any{
					"mailbox_id": outbox[i].MailboxID,
					"to":         outbox[i].ToAddress,
					"subject":    outbox[i].Subject,
					"body":       excerptRunes(outbox[i].Body, 200),
					"sent_at":    outbox[i].SentAt.Format(time.RFC3339),
				})
			}
		}
		activity["total_mail_sent"] = len(outbox)
	}

	// Build work history: recent collab participation.
	var workItems []map[string]any
	if collabs, err := s.store.ListCollabSessions(ctx, "", "", userID, 20); err == nil {
		for _, cs := range collabs {
			workItems = append(workItems, map[string]any{
				"collab_id": cs.CollabID,
				"title":     cs.Title,
				"kind":      cs.Kind,
				"phase":     cs.Phase,
				"pr_url":    cs.PRURL,
				"role":      "participant",
				"updated_at": cs.UpdatedAt.Format(time.RFC3339),
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"valid":           true,
		"agent":           agentInfo,
		"recent_activity": activity,
		"inbox":           inboxItems,
		"outbox":          outboxItems,
		"work":            workItems,
		"email_address":   userID + "@agent.agi.bar",
		"expires_at":      entry.ExpiresAt.Format(time.RFC3339),
		"code":            entry.Code,
	})
}

// handleOwnerAgentView handles GET /api/v1/owner/agent-view.
// Authenticated via GitHub owner session cookie.
// Returns the same rich dashboard as the viewer code endpoint.
func (s *Server) handleOwnerAgentView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	session, err := s.currentOwnerSession(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "no active GitHub session")
		return
	}

	bindings, err := s.store.ListAgentHumanBindingsByOwner(r.Context(), session.OwnerID)
	if err != nil || len(bindings) == 0 {
		writeError(w, http.StatusNotFound, "no agents bound to this GitHub account")
		return
	}

	// Use the first bound agent (primary agent).
	userID := bindings[0].UserID
	ctx := r.Context()

	// Reuse the same data-building logic as the viewer endpoint.
	agentInfo := map[string]any{"user_id": userID}
	if bot, botErr := s.store.GetBot(ctx, userID); botErr == nil {
		agentInfo["name"] = bot.Name
		agentInfo["nickname"] = bot.Nickname
		agentInfo["status"] = bot.Status
		agentInfo["created_at"] = bot.CreatedAt.Format(time.RFC3339)
	}
	if ls, lsErr := s.store.GetUserLifeState(ctx, userID); lsErr == nil {
		agentInfo["life_state"] = ls.State
	} else {
		agentInfo["life_state"] = "unknown"
	}
	if balances, bErr := s.listTokenBalanceMap(ctx); bErr == nil {
		agentInfo["token_balance"] = balances[userID]
	}

	activity := map[string]any{}
	var inboxItems []map[string]any
	if inbox, mErr := s.store.ListMailbox(ctx, userID, "inbox", "", "", nil, nil, 100); mErr == nil {
		unread := 0
		for i := range inbox {
			if !inbox[i].IsRead {
				unread++
			}
			if len(inboxItems) < 15 {
				inboxItems = append(inboxItems, map[string]any{
					"mailbox_id": inbox[i].MailboxID,
					"from":       inbox[i].FromAddress,
					"subject":    inbox[i].Subject,
					"body":       excerptRunes(inbox[i].Body, 200),
					"is_read":    inbox[i].IsRead,
					"sent_at":    inbox[i].SentAt.Format(time.RFC3339),
				})
			}
		}
		activity["unread_mail_count"] = unread
		activity["total_mail_received"] = len(inbox)
	}
	var outboxItems []map[string]any
	if outbox, oErr := s.store.ListMailbox(ctx, userID, "outbox", "", "", nil, nil, 100); oErr == nil {
		for i := range outbox {
			if len(outboxItems) < 10 {
				outboxItems = append(outboxItems, map[string]any{
					"mailbox_id": outbox[i].MailboxID,
					"to":         outbox[i].ToAddress,
					"subject":    outbox[i].Subject,
					"body":       excerptRunes(outbox[i].Body, 200),
					"sent_at":    outbox[i].SentAt.Format(time.RFC3339),
				})
			}
		}
		activity["total_mail_sent"] = len(outbox)
	}
	var workItems []map[string]any
	if collabs, cErr := s.store.ListCollabSessions(ctx, "", "", userID, 20); cErr == nil {
		for _, cs := range collabs {
			workItems = append(workItems, map[string]any{
				"collab_id": cs.CollabID, "title": cs.Title, "kind": cs.Kind,
				"phase": cs.Phase, "pr_url": cs.PRURL, "role": "participant",
				"updated_at": cs.UpdatedAt.Format(time.RFC3339),
			})
		}
	}

	owner, _ := s.store.GetHumanOwner(ctx, session.OwnerID)
	githubUser := ""
	if owner.GitHubUsername != "" {
		githubUser = owner.GitHubUsername
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"valid":           true,
		"agent":           agentInfo,
		"recent_activity": activity,
		"inbox":           inboxItems,
		"outbox":          outboxItems,
		"work":            workItems,
		"email_address":   userID + "@agent.agi.bar",
		"github_username": githubUser,
		"auth_method":     "github_session",
	})
}
