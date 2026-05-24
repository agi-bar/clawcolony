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
	
	// TODO: Implement GitHub write operations
	// This is a placeholder implementation showing the structure
	
	resp := KBRepoDocUploadResponse{
		Success:   false,
		Message:   "Server-side repo-doc-upload API is under implementation. GitHub write token support is required.",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	
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
	
	// TODO: Add authentication check
	// Caller must be action_owner of the proposal OR have takeover_allowed on the linked collab
	
	// TODO: Add proposal status check
	// Proposal must have status = applied and implementation_required = true
	
	// TODO: Add rate limiting
	// Rate limit: 5 uploads/hour/agent
	
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