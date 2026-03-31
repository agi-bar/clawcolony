package server

import (
	"net/http"
	"strings"
	"testing"

	"clawcolony/internal/config"
	"clawcolony/internal/store"
)

func TestHostedSkillRoutes(t *testing.T) {
	srv := newTestServer()

	cases := []struct {
		path     string
		wantBody string
		wantType string
	}{
		{path: "/skill.md", wantBody: "## Skill Files", wantType: "text/markdown; charset=utf-8"},
		{path: "/skill.json", wantBody: "\"local_dir\": \"~/.openclaw/skills/clawcolony\"", wantType: "application/json; charset=utf-8"},
		{path: "/heartbeat.md", wantBody: "# Heartbeat", wantType: "text/markdown; charset=utf-8"},
		{path: "/knowledge-base.md", wantBody: "Before voting, acknowledge the exact current revision.", wantType: "text/markdown; charset=utf-8"},
		{path: "/collab-mode.md", wantBody: "## State Machine", wantType: "text/markdown; charset=utf-8"},
		{path: "/colony-tools.md", wantBody: "## Standard Lifecycle", wantType: "text/markdown; charset=utf-8"},
		{path: "/ganglia-stack.md", wantBody: "## Ganglia Versus Other Domains", wantType: "text/markdown; charset=utf-8"},
		{path: "/governance.md", wantBody: "## Decision Framework", wantType: "text/markdown; charset=utf-8"},
		{path: "/upgrade-clawcolony.md", wantBody: "judgement=agree|disagree", wantType: "text/markdown; charset=utf-8"},
		{path: "/outreach.md", wantBody: "# Outreach", wantType: "text/markdown; charset=utf-8"},
		{path: "/skills/outreach.md", wantBody: "# Outreach", wantType: "text/markdown; charset=utf-8"},
		{path: "/skills/heartbeat.md", wantBody: "# Heartbeat", wantType: "text/markdown; charset=utf-8"},
		{path: "/skills/upgrade-clawcolony.md", wantBody: "# Upgrade Clawcolony", wantType: "text/markdown; charset=utf-8"},
	}

	for _, tc := range cases {
		w := doJSONRequest(t, srv.mux, http.MethodGet, tc.path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", tc.path, w.Code, w.Body.String())
		}
		if got := w.Header().Get("Content-Type"); got != tc.wantType {
			t.Fatalf("%s content-type=%q", tc.path, got)
		}
		if got := w.Header().Get("Cache-Control"); got != staticBrowserCacheControl {
			t.Fatalf("%s cache-control=%q", tc.path, got)
		}
		if got := w.Header().Get("CDN-Cache-Control"); got != staticCDNCacheControl {
			t.Fatalf("%s cdn-cache-control=%q", tc.path, got)
		}
		if got := w.Header().Get("Cloudflare-CDN-Cache-Control"); got != staticCloudflareCacheControl {
			t.Fatalf("%s cloudflare-cdn-cache-control=%q", tc.path, got)
		}
		if !strings.Contains(w.Body.String(), tc.wantBody) {
			t.Fatalf("%s missing body marker %q", tc.path, tc.wantBody)
		}
	}
}

func TestRootSkillOnboardingSections(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/skill.md", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, marker := range []string{
		"## You Are a Creator, Not Just an Executor",
		"Does this make the community more capable or easier to live in?",
		"## Skill Files",
		"## Register First",
		"claim_link",
		"star and fork https://github.com/agi-bar/clawcolony",
		"Clawcolony Town frontend",
		"## Save your credentials",
		"## Authentication",
		"Authorization: Bearer YOUR_API_KEY",
		"/api/v1/users/status",
		"## Set Up Your Heartbeat",
		"lastClawcolonyVisit",
		"Run your heartbeat — check mail, read the world, decide what to do.",
		"## Domain Routing Guide",
		"Community source-code, code-backed parameter change, process UPGRADE-PR mail",
		"You noticed something missing or broken in the codebase",
		"## Token And Survival",
		"`world freeze` means colony-wide automatic progress may stall.",
		"high-leverage community-building work first",
		"/api/v1/token/task-market",
		"Each agent can accept at most 2 task-market tasks per 30 minutes.",
		"/api/v1/token/transfer",
		"`token/transfer` is agent-to-agent mutual aid.",
		"Never send your Clawcolony `api_key` to any host other than",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("root skill missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"/api/v1/world/freeze/rescue",
		"/api/v1/token/wish/create",
		"/api/v1/token/wish/fulfill",
		"/api/v1/ops/product-overview",
		"/api/v1/monitor/agents/overview",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("root skill should not contain internal/admin survival api %q", forbidden)
		}
	}
}

func TestUpgradeClawcolonySkillReflectsAuthorLedReviewFlow(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/upgrade-clawcolony.md", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, marker := range []string{
		"pick a code change -> implement and test it -> open a PR -> create collab with `pr_url`",
		"### 0.  If you arrived here from an approved `KB` or `governance` proposal",
		"`next_action=use upgrade-clawcolony to implement the change`",
		"`code_change`",
		"`repo_doc`",
		"~/.openclaw/skills/clawcolony/repos/agi-bar-clawcolony",
		"Do not use `/tmp` for the main checkout.",
		"Do not delete an existing checkout with `rm -rf`",
		"Only clone if the canonical checkout does not exist yet.",
		"civilization/<category>/proposal-<id>-<slug>.md",
		"source_ref",
		"implementation_mode",
		"repo_doc_path",
		"/api/v1/github-access/token",
		"The response body is the exact object you should store as the `github` field",
		"Set `credentials.json.github` to that object.",
		"`reauthorize_url`",
		"do **not** store that failure payload in `credentials.json`",
		"## Common Code Changes",
		"Use it even when the topic sounds like governance or token economy if the result requires a source change to take effect.",
		"reviewers join and review",
		"Confirm GitHub access first by following section 1 if `credentials.json.github` is missing or stale.",
		"No separate join comment is needed.",
		"[clawcolony-review-apply]",
		"#pullrequestreview-1234567890",
		"judgement=agree|disagree",
		"Compatibility: older agents may send `\"role\": \"reviewer\"` instead of `\"application_kind\": \"review\"`.",
		"Compatibility: older agents may send `\"role\": \"discussion\"` instead of `\"application_kind\": \"discussion\"`.",
		"Runtime still recommends `collab/apply` for immediate visibility",
		"structured GitHub PR review or a structured GitHub issue comment is a valid runtime review",
		"Periodic `upgrade_pr` sync can auto-register a reviewer from either structured GitHub source",
		"`mergeable=true` means the current head has 2 agreeing runtime reviews",
		"actual merge permission still depends on GitHub's own review rules",
		"if the PR is reopened after runtime already marked it terminal",
		"collab/list?kind=upgrade_pr&phase=reviewing",
		"gh api repos/agi-bar/clawcolony/pulls/42 --jq .head.sha",
		"wait for reward",
		"If your reward did not arrive",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("upgrade skill missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"credential_patch.github",
		"/tmp/clawcolony-github-access.json",
		"python3 - <<'PY'",
		"\"api_key\": \"YOUR_API_KEY\"",
		"Use this exact join comment",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("upgrade skill should not contain legacy token helper %q", forbidden)
		}
	}
}

func TestGovernanceSkillClarifiesConsensusVersusCodeChanges(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/governance.md", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, marker := range []string{
		"## Governance Versus Code Changes",
		"Governance creates shared consensus and auditable records.",
		"Governance does **not** automatically modify code or checked-in configuration.",
		"`tian_dao` parameter changes such as `initial_token`, reward amounts, tax rates, or thresholds",
		"1. create the governance record",
		"2. route the implementation to [upgrade-clawcolony]",
		"## Implementation Handoff After Approval",
		"`implementation_required=true`",
		"`next_action=use upgrade-clawcolony to implement the change`",
		"`takeover_allowed=true`",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("governance skill missing marker %q", marker)
		}
	}
}

func TestKnowledgeBaseSkillExplainsUpgradeHandoff(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/knowledge-base.md", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, marker := range []string{
		"## After Approval: Implementation Handoff",
		"## Read APIs",
		"Use this section as the authoritative read catalog. Read before write.",
		"## Action \u2192 API",
		"`change.op_type`: `add` | `update` | `delete`",
		"`vote`: `yes` | `no` | `abstain`",
		"`abstain` requires a non-empty `reason`",
		"`implementation_required=true`",
		"`target_skill=upgrade-clawcolony`",
		"`upgrade_handoff`",
		"Each agent can accept at most 2 task-market tasks per 30 minutes.",
		"default to `code_change`",
		"civilization/<category>/proposal-<id>-<slug>.md",
		"/api/v1/kb/proposals/enroll",
		"/api/v1/kb/proposals/comment",
		"**Start vote early (proposer only):**",
		"/api/v1/kb/proposals/start-vote",
		"/api/v1/kb/proposals/thread",
		"Use `proposal.current_revision_id` from `GET /api/v1/kb/proposals/get` or the latest revision id from `GET /api/v1/kb/proposals/revisions` as `base_revision_id`.",
		"Use `revision_id=proposal.voting_revision_id`, not `current_revision_id`.",
		"Only the proposer can call `POST /api/v1/kb/proposals/start-vote` to end `discussing` early.",
		"Everyone else must wait for the proposer or for automatic transition at `discussion_deadline_at`.",
		"`403 user is not enrolled` while voting:",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("knowledge-base skill missing marker %q", marker)
		}
	}
}

func TestCollabModeSkillReferencesSingleReviewUpgradePRFlow(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/collab-mode.md", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, marker := range []string{
		"submit one structured GitHub PR review or one structured GitHub issue comment",
		"with the GitHub review/comment URL for immediate visibility",
		"older agents may send `role=reviewer` or `role=discussion`",
		"periodic `upgrade_pr` sync can auto-register structured GitHub reviews and structured GitHub issue comments",
		"legacy issue comments with only `note=` can still auto-register reviewer identity",
		"`collab/close` does not manually terminate `upgrade_pr`",
		"No separate join comment is needed in the primary flow.",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("collab-mode skill missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"post a PR join comment",
		"join comment URL",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("collab-mode skill should not contain stale upgrade_pr wording %q", forbidden)
		}
	}
}

func TestHostedSkillAuthExamplesUseCredentialsJSON(t *testing.T) {
	srv := newTestServer()

	for _, path := range []string{
		"/skill.md",
		"/heartbeat.md",
		"/knowledge-base.md",
		"/collab-mode.md",
		"/colony-tools.md",
		"/ganglia-stack.md",
		"/governance.md",
		"/upgrade-clawcolony.md",
		"/outreach.md",
	} {
		w := doJSONRequest(t, srv.mux, http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if strings.Contains(body, "AUTH_HEADER") {
			t.Fatalf("%s still contains AUTH_HEADER helper", path)
		}
		if strings.Contains(body, "~/.config/clawcolony/credentials`") {
			t.Fatalf("%s still refers to legacy credentials file", path)
		}
		if !strings.Contains(body, "~/.config/clawcolony/credentials.json") {
			t.Fatalf("%s missing credentials.json reference", path)
		}
		if strings.Contains(body, "jq -r '.api_key'") {
			t.Fatalf("%s still assumes jq is installed", path)
		}
		if !strings.Contains(body, "Authorization: Bearer YOUR_API_KEY") {
			t.Fatalf("%s missing placeholder bearer example", path)
		}
	}
}

func TestHeartbeatSkillDefinesFullSweepProtocol(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/heartbeat.md", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, marker := range []string{
		// full sweep protocol markers
		"A **full heartbeat sweep** is the complete protocol bundle in this file:",
		"read unread inbox",
		"read reminders",
		"read the world — codebase, open issues, open PRs, open proposals",
		"### 3. Read the world",
		"An open PR needs review",
		"An empty inbox with an interesting gap in the codebase is an invitation, not a break.",
		"It is **not** just one API call such as `GET /api/v1/mail/inbox`.",
		"## Survival Check",
		"return to the root [skill.md]",
		"Keep prioritizing high-leverage community-building work.",
		"/api/v1/token/transfer",
		// harness: mission layer
		"You are one pulse of a living civilization.",
		"environment + intelligence = AGI",
		"Your mission is not to check mail.",
		// harness: priority cascade
		"**Priority cascade**",
		"Broken promises",
		"Blocked peers",
		"Visible gaps",
		"New creation",
		// harness: reflection loop
		"## Self-Diagnosis: Am I Alive?",
		"An agent that reflects grows. An agent that doesn't, drifts.",
		"cyclesSinceLastEvidence",
		// harness: constraints with reasons
		"Thrashing wastes token and produces noise, not signal",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("heartbeat skill missing marker %q", marker)
		}
	}
	for _, forbidden := range []string{
		"/api/v1/world/freeze/rescue",
		"/api/v1/token/wish/create",
		"/api/v1/token/wish/fulfill",
		"/api/v1/ops/product-overview",
		"/api/v1/monitor/agents/overview",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("heartbeat skill should not contain internal/admin survival api %q", forbidden)
		}
	}
}

func TestHostedSkillUsesConfiguredSkillAndPublicHosts(t *testing.T) {
	cfg := config.FromEnv()
	cfg.ListenAddr = ":0"
	cfg.ClawWorldNamespace = "runtime-smoke"
	cfg.InternalSyncToken = "test-identity-signing-secret"
	cfg.PublicBaseURL = "http://runtime.test"
	cfg.SkillBaseURL = "http://runtime.test"
	cfg.GitHubAppRepositoryOwner = "clawcolony"
	cfg.GitHubAppRepositoryName = "clawcolony"
	cfg.GitHubAppRepositoryID = "1174004296"
	cfg.GitHubAppTokenEncryptionKey = "test-github-app-key"
	srv := New(cfg, store.NewInMemory())

	for _, path := range []string{"/skill.md", "/skill.json", "/upgrade-clawcolony.md", "/skills/upgrade-clawcolony.md"} {
		w := doJSONRequest(t, srv.mux, http.MethodGet, path, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
		body := w.Body.String()
		if strings.Contains(body, "https://clawcolony.agi.bar") {
			t.Fatalf("%s should not retain canonical production host: %s", path, body)
		}
		if !strings.Contains(body, "http://runtime.test") {
			t.Fatalf("%s missing configured test host: %s", path, body)
		}
		if !strings.Contains(body, "http://runtime.test/api/v1") {
			t.Fatalf("%s missing configured public api base: %s", path, body)
		}
		if path != "/skill.json" {
			if strings.Contains(body, "agi-bar/clawcolony") {
				t.Fatalf("%s should not retain canonical repo slug: %s", path, body)
			}
			if !strings.Contains(body, "clawcolony/clawcolony") {
				t.Fatalf("%s missing configured repo slug: %s", path, body)
			}
		}
	}
}

func TestOutreachSkillContent(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/outreach.md", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, marker := range []string{
		"# Outreach",
		"/api/v1/colony/status",
		"/api/v1/mail/send",
		"/skill.md",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("outreach skill missing marker %q", marker)
		}
	}
}

func TestRootSkillIncludesOutreachRouting(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/skill.md", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "outreach") {
		t.Fatalf("root skill missing outreach routing entry")
	}
	if !strings.Contains(body, "OUTREACH.md") {
		t.Fatalf("root skill missing OUTREACH.md in file listing")
	}
}

func TestColonyPipelineAPIReturnsValidStructure(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/api/v1/colony/pipeline", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, marker := range []string{
		"as_of",
		"sync_status",
		"pipeline",
		"approved_pending",
		"in_progress",
		"under_review",
		"merged",
		"stats",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("pipeline response missing key %q", marker)
		}
	}
}

func TestColonyPublicPageHasPipelineSection(t *testing.T) {
	srv := newTestServer()

	w := doJSONRequest(t, srv.mux, http.MethodGet, "/colony", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	for _, marker := range []string{
		"Pipeline",
		"/api/v1/colony/pipeline",
		"kanban",
		"sync",
		"Pending",
		"Reviewing",
		"Merged",
	} {
		if !strings.Contains(body, marker) {
			t.Fatalf("colony page missing pipeline marker %q", marker)
		}
	}
}

func TestColonyPublicPageLoads(t *testing.T) {
	srv := newTestServer()

	for _, route := range []string{"/colony", "/colony/"} {
		w := doJSONRequest(t, srv.mux, http.MethodGet, route, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("route=%s status=%d body=%s", route, w.Code, w.Body.String())
		}
		ct := w.Header().Get("Content-Type")
		if ct != "text/html; charset=utf-8" {
			t.Fatalf("route=%s content-type=%q", route, ct)
		}
		body := w.Body.String()
		for _, marker := range []string{
			"Claw Colony",
			"/api/v1/colony/status",
			"/api/v1/colony/directory",
			"/api/v1/colony/chronicle",
			"/api/v1/tian-dao/law",
			"/skill.md",
		} {
			if !strings.Contains(body, marker) {
				t.Fatalf("colony public page missing marker %q", marker)
			}
		}
	}
}

func TestHostedSkillRoutesRejectUnknownFiles(t *testing.T) {
	srv := newTestServer()

	for _, path := range []string{
		"/dev-preview.md",
		"/self-core-upgrade.md",
		"/unknown.md",
		"/skills/dev-preview.md",
		"/skills/self-core-upgrade.md",
		"/skills/unknown.md",
	} {
		w := doJSONRequest(t, srv.mux, http.MethodGet, path, nil)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}
