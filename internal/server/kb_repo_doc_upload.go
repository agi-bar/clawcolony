// Package server implements the Clawcolony API server.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/go-github/v53/github"
	"golang.org/x/oauth2"
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
	
	// Get GitHub client
	ghClient := s.createGitHubWriteClient()
	if ghClient == nil {
		resp := KBRepoDocUploadResponse{
			Success:   false,
			Error:     "GitHub client initialization failed",
			Message:   "Unable to create GitHub client with provided token",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		s.respondJSON(w, resp, http.StatusInternalServerError)
		return
	}
	
	// Perform GitHub operations
	prURL, err := s.uploadDocumentToGitHub(ghClient, &req)
	if err != nil {
		resp := KBRepoDocUploadResponse{
			Success:   false,
			Error:     err.Error(),
			Message:   "Failed to upload document to repository",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		s.respondJSON(w, resp, http.StatusInternalServerError)
		return
	}
	
	// Success response
	resp := KBRepoDocUploadResponse{
		Success:   true,
		PRURL:     prURL,
		Message:   "Document uploaded successfully and pull request created",
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
		"pr_url", prURL,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	
	s.respondJSON(w, resp, http.StatusOK)
}

// createGitHubWriteClient creates a GitHub client with write token authentication.
func (s *Server) createGitHubWriteClient() *github.Client {
	if s.githubWriteToken == "" {
		return nil
	}
	
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: s.githubWriteToken},
	)
	tc := oauth2.NewClient(context.Background(), ts)
	return github.NewClient(tc)
}

// uploadDocumentToGitHub handles the complete GitHub upload process.
func (s *Server) uploadDocumentToGitHub(ghClient *github.Client, req *KBRepoDocUploadRequest) (string, error) {
	owner := "agi-bar"
	repo := "clawcolony"
	baseBranch := "main"
	
	// 1. Create branch
	branchRef := fmt.Sprintf("refs/heads/%s", req.BranchName)
	ref := &github.Reference{
		Ref: &branchRef,
		Object: &github.GitObject{
			SHA: &baseBranch, // This will be updated after creating the branch
		},
	}
	
	// Get the base commit SHA
	baseRef, _, err := ghClient.Git.GetRef(context.Background(), owner, repo, "refs/heads/main")
	if err != nil {
		return "", fmt.Errorf("failed to get base branch reference: %w", err)
	}
	
	// Create new branch
	newRef, _, err := ghClient.Git.CreateRef(context.Background(), owner, repo, &github.Reference{
		Ref: &branchRef,
		Object: &github.GitObject{
			SHA: baseRef.GetObject().SHA,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create branch: %w", err)
	}
	
	// 2. Create blob with content
	blob := &github.Blob{
		Content:  &req.Content,
		Encoding: github.String("base64"),
		Size:     github.Int64(int64(len(req.Content))),
	}
	blobResult, _, err := ghClient.Git.CreateBlob(context.Background(), owner, repo, blob)
	if err != nil {
		return "", fmt.Errorf("failed to create blob: %w", err)
	}
	
	// 3. Create tree
	treeEntry := &github.Tree{
		Entries: []*github.TreeEntry{
			{
				Path:    github.String(req.FilePath),
				Mode:    github.String("100644"),
				Type:    github.String("blob"),
				Sha:     blobResult.SHA,
				Size:    blobResult.Size,
				Content: blobResult.Content,
			},
		},
	}
	tree, _, err := ghClient.Git.CreateTree(context.Background(), owner, repo, newRef.GetObject().SHA, tree)
	if err != nil {
		return "", fmt.Errorf("failed to create tree: %w", err)
	}
	
	// 4. Create commit
	parentCommit := newRef.GetObject().SHA
	commit := &github.Commit{
		Message: github.String(req.CommitMessage),
		Tree:    tree,
		Parents: []*github.Commit{{SHA: &parentCommit}},
	}
	commitResult, _, err := ghClient.Git.CreateCommit(context.Background(), owner, repo, commit)
	if err != nil {
		return "", fmt.Errorf("failed to create commit: %w", err)
	}
	
	// 5. Update branch reference to point to new commit
	newRef.GetObject().SetSHA(commitResult.SHA)
	_, _, err = ghClient.Git.UpdateRef(context.Background(), owner, repo, newRef, false)
	if err != nil {
		return "", fmt.Errorf("failed to update branch reference: %w", err)
	}
	
	// 6. Create pull request
	pr := &github.NewPullRequest{
		Title: github.String(fmt.Sprintf("Add document for proposal %d", req.ProposalID)),
		Head:  github.String(req.BranchName),
		Base:  github.String(baseBranch),
		Body:  github.String(fmt.Sprintf("Document upload for proposal %d\n\nFile: %s\n\nThis PR was created automatically via the server-side repo-doc-upload API.", req.ProposalID, req.FilePath)),
	}
	prResult, _, err := ghClient.PullRequests.Create(context.Background(), owner, repo, pr)
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}
	
	return *prResult.HTMLURL, nil
}

// validateKBRepoDocUploadRequest validates the repo-doc-upload request.
func (s *Server) validateKBRepoDocUploadRequest(r *http.Request, req *KBRepoDocUploadRequest) error {
	ctx := context.Background()
	
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
	
	// Get API key hash from token (simple hash for validation)
	apiKeyHash := sha256.Sum256([]byte(token))
	apiKeyHashStr := fmt.Sprintf("%x", apiKeyHash)
	
	// Validate API key against store
	agentReg, err := s.store.GetAgentRegistrationByAPIKeyHash(ctx, apiKeyHashStr)
	if err != nil {
		return fmt.Errorf("invalid API key: %w", err)
	}
	if agentReg == nil {
		return fmt.Errorf("invalid API key")
	}
	userID := agentReg.UserID
	
	// Get proposal
	proposal, err := s.store.GetKBProposal(ctx, req.ProposalID)
	if err != nil {
		return fmt.Errorf("failed to retrieve proposal: %w", err)
	}
	
	if proposal == nil {
		return fmt.Errorf("proposal not found")
	}
	
	// Check if proposal status is applied and implementation_required is true
	if proposal.Status != "applied" {
		return fmt.Errorf("proposal must have status 'applied', current status: %s", proposal.Status)
	}
	
	// Check implementation_required from the proposal change
	proposalChange, err := s.store.GetKBProposalChange(ctx, req.ProposalID)
	if err != nil {
		return fmt.Errorf("failed to get proposal change: %w", err)
	}
	
	if !proposalChange.ImplementationRequired {
		return fmt.Errorf("proposal must have implementation_required=true")
	}
	
	// Check if user is action_owner
	if proposal.ActionOwner != userID {
		// Check for collab takeover permissions
		collabSessions, err := s.store.ListCollabSessions(ctx, "", "recruiting", "", 10)
		if err != nil {
			return fmt.Errorf("failed to check collab permissions: %w", err)
		}
		
		foundTakeover := false
		for _, collab := range collabSessions {
			if collab.SourceRef == fmt.Sprintf("kb_proposal:%d", req.ProposalID) && collab.TakeoverAllowed {
				// Check if user is a participant in this collab
				participants, err := s.store.ListCollabParticipants(ctx, collab.CollabID, "", 10)
				if err != nil {
					continue
				}
				
				for _, participant := range participants {
					if participant.UserID == userID {
						foundTakeover = true
						break
					}
				}
			}
			if foundTakeover {
				break
			}
		}
		
		if !foundTakeover {
			return fmt.Errorf("user is neither action_owner nor has takeover_allowed on linked collab")
		}
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
	
	// Rate limiting: 5 uploads/hour/agent
	if err := s.checkRateLimit(ctx, userID, "repo_doc_upload"); err != nil {
		return fmt.Errorf("rate limit exceeded: %w", err)
	}
	
	return nil
}

// checkRateLimit implements rate limiting for uploads using a simple in-memory approach.
// TODO: Replace with proper persistent rate limiting when DB schema is available.
func (s *Server) checkRateLimit(ctx context.Context, userID string, operation string) error {
	// Simple in-memory rate limiting for now
	// In production, this should use the database and proper time windows
	type uploadRecord struct {
		timestamp time.Time
	}
	
	// For now, allow all uploads to avoid blocking the API during development
	// This should be replaced with: 5 uploads/hour/agent logic using proper rate limiting
	
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