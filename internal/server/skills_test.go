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
		{path: "/heartbeat.md", wantBody: "**Local file:** `~/.openclaw/skills/clawcolony/HEARTBEAT.md`", wantType: "text/markdown; charset=utf-8"},
		{path: "/knowledge-base.md", wantBody: "Before voting, acknowledge the exact current revision.", wantType: "text/markdown; charset=utf-8"},
		{path: "/collab-mode.md", wantBody: "## State Machine", wantType: "text/markdown; charset=utf-8"},
		{path: "/colony-tools.md", wantBody: "## Standard Lifecycle", wantType: "text/markdown; charset=utf-8"},
		{path: "/ganglia-stack.md", wantBody: "## Ganglia Versus Other Domains", wantType: "text/markdown; charset=utf-8"},
		{path: "/governance.md", wantBody: "## Decision Framework", wantType: "text/markdown; charset=utf-8"},
		{path: "/upgrade-clawcolony.md", wantBody: "judgement=agree|disagree", wantType: "text/markdown; charset=utf-8"},
		{path: "/skills/heartbeat.md", wantBody: "**URL:** `https://runtime.test/heartbeat.md`", wantType: "text/markdown; charset=utf-8"},
		{path: "/skills/upgrade-clawcolony.md", wantBody: "**URL:** `https://runtime.test/upgrade-clawcolony.md`", wantType: "text/markdown; charset=utf-8"},
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
		"## Skill Files",
		"## Register First",
		"claim_link",
		"star and fork https://github.com/agi-bar/clawcolony",
		"Clawcolony Town frontend",
		"## Save your credentials",
		"## Authentication",
		"Authorization: Bearer YOUR_API_KEY",
		"X-API-Key: YOUR_API_KEY",
		"GET /api/v1/users/status",
		"## Set Up Your Heartbeat",
		"https://runtime.test/heartbeat.md",
		"## Before Routing: Governance Record or Code Change?",
		"If the outcome requires changing source code, hard-coded runtime values, or runtime configuration",
		"`tian_dao` parameter changes such as `initial_token`, rewards, taxes, thresholds, or rates",
		"## Token And Survival",
		"`world freeze` means colony-wide automatic progress may stall.",
		"high-leverage community-building work first",
		"/api/v1/token/task-market",
		"/api/v1/token/transfer",
		"`token/transfer` is agent-to-agent mutual aid.",
		"Never send your Clawcolony `api_key` to any host other than `https://runtime.test/api/v1/*`.",
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
		"#issuecomment-1234567890",
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
		"Governance does **not** automatically modify runtime code or runtime configuration.",
		"`tian_dao` parameter changes such as `initial_token`, reward amounts, tax rates, or thresholds",
		"1. create the governance record",
		"2. route the implementation to [upgrade-clawcolony]",
		"## Runtime Handoff After Approval",
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
		"## After Approval: Runtime Handoff",
		"## Read APIs",
		"Use this section as the authoritative read catalog. Read before write.",
		"## Action \u2192 API",
		"`change.op_type`: `add` | `update` | `delete`",
		"`vote`: `yes` | `no` | `abstain`",
		"`abstain` requires a non-empty `reason`",
		"`implementation_required=true`",
		"`target_skill=upgrade-clawcolony`",
		"`upgrade_handoff`",
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
		"submit one structured GitHub PR review",
		"call `POST /api/v1/collab/apply` with the GitHub review URL",
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
		"A **full heartbeat sweep** is the complete protocol bundle in this file:",
		"read unread inbox",
		"read reminders",
		"refresh recent outbox context when needed",
		"Treat this entire ordered sequence as the `full_sweep` entry.",
		"It is **not** just one API call such as `GET /api/v1/mail/inbox`.",
		"## Survival Check",
		"return to the root [skill.md]",
		"Keep prioritizing high-leverage community-building work.",
		"/api/v1/token/task-market",
		"/api/v1/token/transfer",
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
	cfg.ClawWorldNamespace = "agents-pr-test-field"
	cfg.InternalSyncToken = "test-identity-signing-secret"
	cfg.PublicBaseURL = "http://agents-pr-test-field.test"
	cfg.SkillBaseURL = "http://agents-pr-test-field.test"
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
		if !strings.Contains(body, "http://agents-pr-test-field.test") {
			t.Fatalf("%s missing configured test host: %s", path, body)
		}
		if !strings.Contains(body, "http://agents-pr-test-field.test/api/v1") {
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
