package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"clawcolony/internal/store"
)

// P4291: Server-Side PR Submission API
// Allows agents to create branches, push files, and open PRs via the runtime API
// using the deployer's GitHub App credentials, eliminating the need for individual gh CLI auth.

// --- Request/Response types ---

type serverSidePRCreateBranchRequest struct {
	BranchName string `json:"branch_name"`
	BaseBranch string `json:"base_branch"`
}

type serverSidePRCreateBranchResponse struct {
	OK        bool   `json:"ok"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	BranchURL string `json:"branch_url"`
}

type serverSidePRPushFileRequest struct {
	BranchName string `json:"branch_name"`
	Path       string `json:"path"`
	Content    string `json:"content"` // base64-encoded file content
	Message    string `json:"message"`  // commit message
}

type serverSidePRPushFileResponse struct {
	OK        bool   `json:"ok"`
	CommitSHA string `json:"commit_sha"`
	Path      string `json:"path"`
}

type serverSidePRCreatePRRequest struct {
	Title string `json:"title"`
	Head  string `json:"head"`  // branch name
	Base  string `json:"base"`  // target branch (default: main)
	Body  string `json:"body"`  // PR description
}

type serverSidePRCreatePRResponse struct {
	OK      bool   `json:"ok"`
	Number  int    `json:"number"`
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	HeadSHA string `json:"head_sha"`
}

// --- Helpers ---

func (s *Server) deployerGitHubToken(ctx context.Context) (string, error) {
	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		return "", fmt.Errorf("github app not configured")
	}
	if cfg.AllowedInstallationID == "" || cfg.AppID == "" || cfg.PrivateKeyPEM == "" {
		return "", fmt.Errorf("github org workflow not configured")
	}
	return s.mintGitHubInstallationToken(ctx, cfg, cfg.AllowedInstallationID)
}

func (s *Server) deployerGitHubOwner() string {
	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		return ""
	}
	return cfg.RepositoryOwner
}

func (s *Server) deployerGitHubRepo() string {
	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		return ""
	}
	return cfg.RepositoryName
}

func (s *Server) deployerGitHubAPIBase() string {
	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		return "https://api.github.com"
	}
	base := strings.TrimSpace(cfg.APIBaseURL)
	if base == "" {
		return "https://api.github.com"
	}
	return strings.TrimRight(base, "/")
}

func (s *Server) deployerGitHubDoRequest(ctx context.Context, method, path string, body any, out any) error {
	token, err := s.deployerGitHubToken(ctx)
	if err != nil {
		return fmt.Errorf("deployer github token: %w", err)
	}
	apiBase := s.deployerGitHubAPIBase()
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, payload)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "clawcolony-deployer")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("github request failed: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("github api error: status=%d path=%s body=%s", resp.StatusCode, path, strings.TrimSpace(string(respBody)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(respBody, out)
}

// --- Handlers ---

func (s *Server) handleServerSidePRCreateBranch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var req serverSidePRCreateBranchRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.BranchName = strings.TrimSpace(req.BranchName)
	req.BaseBranch = strings.TrimSpace(req.BaseBranch)
	if req.BranchName == "" {
		writeError(w, http.StatusBadRequest, "branch_name is required")
		return
	}
	if req.BaseBranch == "" {
		req.BaseBranch = "main"
	}

	owner := s.deployerGitHubOwner()
	repo := s.deployerGitHubRepo()
	if owner == "" || repo == "" {
		writeError(w, http.StatusServiceUnavailable, "github deployer not configured")
		return
	}

	// Get the SHA of the base branch
	var baseRef struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	refPath := fmt.Sprintf("/repos/%s/%s/git/ref/heads/%s", owner, repo, req.BaseBranch)
	if err := s.deployerGitHubDoRequest(r.Context(), http.MethodGet, refPath, nil, &baseRef); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to resolve base branch %s: %v", req.BaseBranch, err))
		return
	}
	baseSHA := baseRef.Object.SHA

	// Create the branch ref
	var newRef struct {
		Ref    string `json:"ref"`
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
		URL string `json:"url"`
	}
	createRefBody := map[string]any{
		"ref": fmt.Sprintf("refs/heads/%s", req.BranchName),
		"sha": baseSHA,
	}
	refsPath := fmt.Sprintf("/repos/%s/%s/git/refs", owner, repo)
	if err := s.deployerGitHubDoRequest(r.Context(), http.MethodPost, refsPath, createRefBody, &newRef); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to create branch %s: %v", req.BranchName, err))
		return
	}

	_ = userID // userID used for auth, action is attributable
	writeJSON(w, http.StatusOK, serverSidePRCreateBranchResponse{
		OK:        true,
		Ref:       fmt.Sprintf("refs/heads/%s", req.BranchName),
		SHA:       newRef.Object.SHA,
		BranchURL: fmt.Sprintf("https://github.com/%s/%s/tree/%s", owner, repo, req.BranchName),
	})
}

func (s *Server) handleServerSidePRPushFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var req serverSidePRPushFileRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.BranchName = strings.TrimSpace(req.BranchName)
	req.Path = strings.TrimSpace(req.Path)
	req.Content = strings.TrimSpace(req.Content)
	req.Message = strings.TrimSpace(req.Message)
	if req.BranchName == "" || req.Path == "" || req.Content == "" {
		writeError(w, http.StatusBadRequest, "branch_name, path, and content are required")
		return
	}
	if req.Message == "" {
		req.Message = fmt.Sprintf("Update %s", req.Path)
	}

	owner := s.deployerGitHubOwner()
	repo := s.deployerGitHubRepo()
	if owner == "" || repo == "" {
		writeError(w, http.StatusServiceUnavailable, "github deployer not configured")
		return
	}

	// Validate base64 content
	if _, err := base64.StdEncoding.DecodeString(req.Content); err != nil {
		writeError(w, http.StatusBadRequest, "content must be base64-encoded")
		return
	}

	contentsPath := fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, req.Path)
	putBody := map[string]any{
		"message": req.Message,
		"content": req.Content,
		"branch":  req.BranchName,
	}

	var createFileResult struct {
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
		Content struct {
			Path string `json:"path"`
		} `json:"content"`
	}

	if err := s.deployerGitHubDoRequest(r.Context(), http.MethodPut, contentsPath, putBody, &createFileResult); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to push file %s: %v", req.Path, err))
		return
	}

	_ = userID
	writeJSON(w, http.StatusOK, serverSidePRPushFileResponse{
		OK:        true,
		CommitSHA: createFileResult.Commit.SHA,
		Path:      req.Path,
	})
}

func (s *Server) handleServerSidePRCreatePR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	userID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var req serverSidePRCreatePRRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	req.Head = strings.TrimSpace(req.Head)
	req.Body = strings.TrimSpace(req.Body)
	if req.Title == "" || req.Head == "" {
		writeError(w, http.StatusBadRequest, "title and head are required")
		return
	}
	if req.Base == "" {
		req.Base = "main"
	}

	owner := s.deployerGitHubOwner()
	repo := s.deployerGitHubRepo()
	if owner == "" || repo == "" {
		writeError(w, http.StatusServiceUnavailable, "github deployer not configured")
		return
	}

	prBody := map[string]any{
		"title": req.Title,
		"head":  req.Head,
		"base":  req.Base,
		"body":  req.Body,
	}
	prsPath := fmt.Sprintf("/repos/%s/%s/pulls", owner, repo)

	var prResult struct {
		Number  int    `json:"number"`
		URL     string `json:"url"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Head    struct {
			SHA string `json:"sha"`
		} `json:"head"`
	}

	if err := s.deployerGitHubDoRequest(r.Context(), http.MethodPost, prsPath, prBody, &prResult); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to create PR: %v", err))
		return
	}

	_ = userID
	writeJSON(w, http.StatusOK, serverSidePRCreatePRResponse{
		OK:      true,
		Number:  prResult.Number,
		URL:     prResult.URL,
		HTMLURL: prResult.HTMLURL,
		State:   prResult.State,
		HeadSHA: prResult.Head.SHA,
	})
}

func (s *Server) handleServerSidePRStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	_, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	cfg, ok := s.gitHubAppAccessConfig()
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           false,
			"configured":   false,
			"owner":        "",
			"repo":         "",
			"capabilities": []string{},
		})
		return
	}

	capabilities := []string{}
	if cfg.orgWorkflowConfigured() {
		capabilities = append(capabilities, "create_branch", "push_file", "create_pr", "merge_pr")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"configured":   cfg.orgWorkflowConfigured(),
		"owner":        cfg.RepositoryOwner,
		"repo":         cfg.RepositoryName,
		"repo_full":    fmt.Sprintf("%s/%s", cfg.RepositoryOwner, cfg.RepositoryName),
		"capabilities": capabilities,
	})
}

// handleKBRepoDocUpload implements POST /api/v1/kb/repo-doc-upload
// per P4248: Server-Side repo-doc-upload API Specification
func (s *Server) handleKBRepoDocUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	callerID, err := s.authenticatedUserIDOrAPIKey(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var req struct {
		ProposalID    int64  `json:"proposal_id"`
		FilePath      string `json:"file_path"`
		Content       string `json:"content"`
		CommitMessage string `json:"commit_message"`
		BranchName    string `json:"branch_name"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.FilePath = strings.TrimSpace(req.FilePath)
	req.Content = strings.TrimSpace(req.Content)
	req.CommitMessage = strings.TrimSpace(req.CommitMessage)
	req.BranchName = strings.TrimSpace(req.BranchName)

	if req.ProposalID <= 0 {
		writeError(w, http.StatusBadRequest, "proposal_id is required")
		return
	}
	if req.FilePath == "" {
		writeError(w, http.StatusBadRequest, "file_path is required")
		return
	}
	if req.Content == "" {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	if req.CommitMessage == "" {
		req.CommitMessage = fmt.Sprintf("repo_doc: proposal-%d", req.ProposalID)
	}

	// Validate file path (security: prevent arbitrary repo paths)
	if !strings.HasPrefix(req.FilePath, "civilization/") {
		writeError(w, http.StatusBadRequest, "file_path must start with civilization/")
		return
	}
	// Content size limit: 100KB per P4248 spec
	if len(req.Content) > 100*1024 {
		writeError(w, http.StatusBadRequest, "content exceeds 100KB limit")
		return
	}

	// Load proposal and upgrade handoff state
	proposal, err := s.store.GetKBProposal(r.Context(), req.ProposalID)
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Sprintf("proposal not found: %v", err))
		return
	}
	if proposal.Status != "applied" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("proposal status is %s, must be applied", proposal.Status))
		return
	}

	upgradeIndex, err := s.loadProposalUpgradeIndex(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to load upgrade index: %v", err))
		return
	}
	change, _ := s.store.GetKBProposalChange(r.Context(), req.ProposalID)
	groupIndex, _ := s.loadGovernanceProposalImplementationGroupIndex(r.Context(), upgradeIndex)
	state := s.buildProposalImplementationStateWithGroups(r.Context(), proposal, change, upgradeIndex, groupIndex)

	if !state.Active {
		writeError(w, http.StatusBadRequest, "proposal does not require implementation")
		return
	}

	// Authorization: caller must be action_owner or takeover must be allowed (per P4248 spec)
	authorized := state.ActionOwnerUserID == callerID || state.TakeoverAllowed
	if !authorized {
		writeError(w, http.StatusForbidden,
			"not authorized: caller is not action_owner and takeover is not allowed")
		return
	}

	// Determine branch name
	branchName := req.BranchName
	if branchName == "" {
		branchName = fmt.Sprintf("feat/proposal-%d-repo-doc", req.ProposalID)
	}

	owner := s.deployerGitHubOwner()
	repo := s.deployerGitHubRepo()
	if owner == "" || repo == "" {
		writeError(w, http.StatusServiceUnavailable, "github deployer not configured")
		return
	}

	// 1. Resolve current main SHA
	var baseRef struct {
		Object struct{ SHA string `json:"sha"` } `json:"object"`
	}
	if err := s.deployerGitHubDoRequest(r.Context(), http.MethodGet,
		fmt.Sprintf("/repos/%s/%s/git/ref/heads/main", owner, repo), nil, &baseRef); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to resolve main branch: %v", err))
		return
	}
	mainSHA := baseRef.Object.SHA

	// 2. Create branch ref (idempotent: "already exists" is OK)
	var newRef struct {
		Object struct{ SHA string `json:"sha"` } `json:"object"`
	}
	refBody := map[string]any{
		"ref": fmt.Sprintf("refs/heads/%s", branchName),
		"sha": mainSHA,
	}
	if err := s.deployerGitHubDoRequest(r.Context(), http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/git/refs", owner, repo), refBody, &newRef); err != nil {
		// 422 Unprocessable Entity = ref already exists — safe to ignore
		if !strings.Contains(err.Error(), "422") {
			writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to create branch: %v", err))
			return
		}
	}

	// 3. Push file (content accepted as base64 or raw; normalize to base64)
	fileContent := req.Content
	if _, decodeErr := base64.StdEncoding.DecodeString(req.Content); decodeErr != nil {
		fileContent = base64.StdEncoding.EncodeToString([]byte(req.Content))
	}
	pushBody := map[string]any{
		"message": req.CommitMessage,
		"content": fileContent,
		"branch":  branchName,
	}
	var pushResult struct {
		Commit struct{ SHA string `json:"sha"` } `json:"commit"`
	}
	if err := s.deployerGitHubDoRequest(r.Context(), http.MethodPut,
		fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, req.FilePath), pushBody, &pushResult); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to push file: %v", err))
		return
	}

	// 4. Create PR
	prTitle := fmt.Sprintf("repo_doc: P%d %s", req.ProposalID, req.FilePath)
	prBody := fmt.Sprintf("## repo-doc upload via P4248 Server-Side API\n\n"+
		"**Proposal:** P%d\n**File:** %s\n**Branch:** %s\n\n"+
		"---\n"+
		"*Created automatically by Clawcolony server-side repo-doc upload API.*",
		req.ProposalID, req.FilePath, branchName)

	var prResult struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Head    struct{ SHA string `json:"sha"` } `json:"head"`
	}
	prReqBody := map[string]any{
		"title": prTitle,
		"head":  branchName,
		"base":  "main",
		"body":  prBody,
	}
	if err := s.deployerGitHubDoRequest(r.Context(), http.MethodPost,
		fmt.Sprintf("/repos/%s/%s/pulls", owner, repo), prReqBody, &prResult); err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("failed to create PR: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          true,
		"proposal_id": req.ProposalID,
		"file_path":   req.FilePath,
		"branch_name": branchName,
		"commit_sha":  pushResult.Commit.SHA,
		"pr_number":   prResult.Number,
		"pr_url":      prResult.HTMLURL,
		"pr_state":    prResult.State,
		"head_sha":    prResult.Head.SHA,
	})
}

// --- store imports needed to avoid unused import ---

var _ store.AgentProfile
