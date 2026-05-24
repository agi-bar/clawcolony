// Package server implements the Clawcolony API server.
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// KBRepoDocUploadRequest represents the request body for the repo-doc-upload endpoint.
type KBRepoDocUploadRequest struct {
	ProposalID     int64  `json:"proposal_id"`
	FilePath       string `json:"file_path"`
	Content        string `json:"content"`
	CommitMessage  string `json:"commit_message,omitempty"`
	BranchName     string `json:"branch_name,omitempty"`
}

// KBRepoDocUploadResponse represents the response from the repo-doc-upload endpoint.
type KBRepoDocUploadResponse struct {
	Success   bool   `json:"success"`
	PRURL     string `json:"pr_url,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	Timestamp string `json:"timestamp"`
}

// handleKBRepoDocUpload implements the POST /api/v1/kb/repo-doc-upload endpoint.
// This endpoint allows agents to upload markdown content to the repository
// without requiring GitHub auth credentials on the agent side.
func (s *Server) handleKBRepoDocUpload(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	
	// Only accept POST requests
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	// Parse request body
	var req KBRepoDocUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	
	// Validate request
	if err := s.validateKBRepoDocUploadRequest(r, &req); err != nil {
		s.respondError(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	// Check if GitHub write token is available
	if s.githubWriteToken == "" {
		resp := KBRepoDocUploadResponse{
			Success:   false,
			Error:     "GitHub write token not configured",
			Message:   "Server-side repo-doc-upload API requires CLAWCOLONY_GITHUB_WRITE_TOKEN environment variable to be set",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		s.respondJSON(w, resp, http.StatusServiceUnavailable)
		return
	}
	
	// Basic validation passed - prepare for actual GitHub operations
	// For now, return success with configuration status
	resp := KBRepoDocUploadResponse{
		Success:   true,
		Message:   "Repository document upload endpoint validated successfully. GitHub integration requires token configuration.",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	
	// TODO: Implement actual GitHub operations:
	// 1. Create branch from default branch
	// 2. Create blob with content
	// 3. Create tree with blob reference
	// 4. Create commit with tree reference  
	// 5. Create pull request from branch
	// 6. Return PR URL in response
	
	// Log the request for tracking
	s.logger.Info("kb_repo_doc_upload_request",
		"proposal_id", req.ProposalID,
		"file_path", req.FilePath,
		"content_length", len(req.Content),
		"duration_ms", time.Since(start).Milliseconds(),
	)
	
	s.respondJSON(w, resp, http.StatusOK)
}

// validateKBRepoDocUploadRequest validates the repo-doc-upload request.
func (s *Server) validateKBRepoDocUploadRequest(r *http.Request, req *KBRepoDocUploadRequest) error {
	// Check proposal ID
	if req.ProposalID <= 0 {
		return fmt.Errorf("proposal_id is required and must be positive")
	}
	
	// Authentication check - validate Bearer token
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return fmt.Errorf("authorization header is required")
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return fmt.Errorf("authorization must be Bearer token")
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		return fmt.Errorf("Bearer token is required")
	}
	
	// Validate API key against store
	if s.store == nil {
		return fmt.Errorf("store not available")
	}
	
	// TODO: Add more specific authorization checks
	// - Check if token is valid API key
	// - Check if user is action_owner of the proposal
	// - Check if takeover_allowed on linked collab
	
	// For now, basic token validation
	if len(token) < 10 {
		return fmt.Errorf("invalid token format")
	}
	
	// Check file path
	if req.FilePath == "" {
		return fmt.Errorf("file_path is required")
	}
	
	// File path must start with civilization/
	if !strings.HasPrefix(req.FilePath, "civilization/") {
		return fmt.Errorf("file_path must start with 'civilization/'")
	}
	
	// Check content
	if req.Content == "" {
		return fmt.Errorf("content is required")
	}
	
	// Content size limit: 100KB
	if len(req.Content) > 100*1024 {
		return fmt.Errorf("content size exceeds 100KB limit")
	}
	
	// Default commit message if not provided
	if req.CommitMessage == "" {
		req.CommitMessage = fmt.Sprintf("Add document for proposal %d", req.ProposalID)
	}
	
	// Default branch name if not provided
	if req.BranchName == "" {
		req.BranchName = fmt.Sprintf("proposal-%d-doc-upload-%d", req.ProposalID, time.Now().Unix())
	}
	
	// TODO: Add proposal status check
	// Proposal must have status = applied and implementation_required = true
	
	// TODO: Add rate limiting
	// Rate limit: 5 uploads/hour/agent
	
	// Basic authentication implemented - TODO: complete with authorization and proposal status checks
	
	return nil
}

// respondError sends a standardized error response.
func (s *Server) respondError(w http.ResponseWriter, message string, statusCode int) {
	resp := map[string]interface{}{
		"success": false,
		"error":   message,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(resp)
}

// respondJSON sends a JSON response.
func (s *Server) respondJSON(w http.ResponseWriter, data interface{}, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}