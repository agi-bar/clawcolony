package server

import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"testing"
)

func TestDashboardTemplatesKeepCoreRuntimeLinks(t *testing.T) {
	coreLinks := []string{
		"/dashboard/mail",
		"/dashboard/collab",
		"/dashboard/kb",
		"/dashboard/governance",
		"/dashboard/world-tick",
	}
	pages := []string{
		"web/dashboard_home.html",
		"web/dashboard_mail.html",
		"web/dashboard_collab.html",
		"web/dashboard_kb.html",
		"web/dashboard_governance.html",
		"web/dashboard_world_tick.html",
		"web/dashboard_monitor.html",
	}

	for _, file := range pages {
		t.Run(strings.TrimPrefix(strings.TrimSuffix(file, ".html"), "web/"), func(t *testing.T) {
			data, err := dashboardFS.ReadFile(file)
			if err != nil {
				t.Fatalf("read template failed: %v", err)
			}
			s := string(data)
			for _, link := range coreLinks {
				if !strings.Contains(s, fmt.Sprintf(`href="%s"`, link)) {
					t.Fatalf("missing core runtime link %s in %s", link, file)
				}
			}
		})
	}
}

func TestDashboardTemplatesAvoidRemovedRuntimeBindings(t *testing.T) {
	checks := []struct {
		file      string
		forbidden []string
		required  []string
	}{
		{
			file: "web/dashboard_home.html",
			forbidden: []string{
				"/dashboard/prompts",
				"/api/v1/chat/send",
				"/api/v1/system/openclaw-dashboard-config",
			},
			required: []string{
				"/dashboard/mail",
				"/dashboard/world-tick",
			},
		},
		{
			file: "web/dashboard_world_tick.html",
			forbidden: []string{
				"/api/v1/chat/send",
				"/api/v1/bots/dev/",
			},
			required: []string{
				"/api/v1/runtime/scheduler-settings",
				"/api/v1/runtime/scheduler-settings/upsert",
			},
		},
		{
			file: "web/dashboard_monitor.html",
			forbidden: []string{
				"/api/v1/bots/openclaw/status",
				"/dashboard/prompts",
			},
			required: []string{
				"Agent Overview",
				"/api/v1/monitor/meta",
			},
		},
	}

	for _, c := range checks {
		t.Run(strings.TrimPrefix(strings.TrimSuffix(c.file, ".html"), "web/"), func(t *testing.T) {
			data, err := dashboardFS.ReadFile(c.file)
			if err != nil {
				t.Fatalf("read template failed: %v", err)
			}
			s := string(data)
			for _, tok := range c.required {
				if !strings.Contains(s, tok) {
					t.Fatalf("required token missing: %q", tok)
				}
			}
			for _, tok := range c.forbidden {
				if strings.Contains(s, tok) {
					t.Fatalf("forbidden token exists: %q", tok)
				}
			}
		})
	}
}

func TestDashboardTemplatesKeepDocumentShellAndSharedAssets(t *testing.T) {
	entries, err := fs.ReadDir(dashboardFS, "web")
	if err != nil {
		t.Fatalf("read dashboard template dir failed: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "dashboard_") || !strings.HasSuffix(name, ".html") {
			continue
		}

		t.Run(strings.TrimSuffix(name, ".html"), func(t *testing.T) {
			data, err := dashboardFS.ReadFile("web/" + name)
			if err != nil {
				t.Fatalf("read template failed: %v", err)
			}
			s := string(data)
			for _, token := range []string{"<head>", "</head>", "<body>", "</body>"} {
				if !strings.Contains(s, token) {
					t.Fatalf("missing html shell token %q in %s", token, name)
				}
			}
			if !strings.Contains(s, `/dashboard/dashboard.css`) {
				t.Fatalf("missing shared dashboard stylesheet in %s", name)
			}
			if !strings.Contains(s, `/dashboard/dashboard.js`) {
				t.Fatalf("missing shared dashboard script in %s", name)
			}
		})
	}
}

func TestDashboardIdentityPagesLoad(t *testing.T) {
	srv := newTestServer()
	for _, route := range []string{
		"/dashboard/agent-register",
		"/dashboard/agent-owner",
	} {
		t.Run(strings.TrimPrefix(route, "/dashboard/"), func(t *testing.T) {
			w := doJSONRequest(t, srv.mux, http.MethodGet, route, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("route=%s status=%d body=%s", route, w.Code, w.Body.String())
			}
		})
	}
}

func TestDashboardIdentityPagesUseAPIV1Routes(t *testing.T) {
	checks := []struct {
		file      string
		required  []string
		forbidden []string
	}{
		{
			file: "web/dashboard_agent_register.html",
			required: []string{
				"/api/v1/users/register",
				"/api/v1/users/status",
				"/api/v1/token/pricing",
			},
		},
		{
			file: "web/dashboard_agent_owner.html",
			required: []string{
				"/api/v1/owner/me",
				"/api/v1/owner/logout",
				"/api/v1/social/policy",
				"/api/v1/social/rewards/status",
				"/api/v1/social/x/connect/start",
				"/api/v1/github-access/status",
				"/api/v1/github-access/start",
				"/api/v1/github-access",
			},
		},
	}

	for _, c := range checks {
		switch c.file {
		case "web/dashboard_agent_register.html":
			c.forbidden = []string{
				`"` + legacyAPIPath("users", "register") + `"`,
				`"` + legacyAPIPath("users", "status") + `"`,
				`"` + legacyAPIPath("token", "pricing") + `"`,
			}
		case "web/dashboard_agent_owner.html":
			c.forbidden = []string{
				`"` + legacyAPIPath("owner", "me") + `"`,
				`"` + legacyAPIPath("owner", "logout") + `"`,
				`"` + legacyAPIPath("social", "policy") + `"`,
				`"` + legacyAPIPath("social", "rewards", "status") + `"`,
				`"` + legacyAPIPath("social", "github", "connect", "start") + `"`,
				`"` + legacyAPIPath("social", "x", "connect", "start") + `"`,
				"/api/v1/social/github/connect/start",
			}
		}
		t.Run(strings.TrimPrefix(strings.TrimSuffix(c.file, ".html"), "web/"), func(t *testing.T) {
			data, err := dashboardFS.ReadFile(c.file)
			if err != nil {
				t.Fatalf("read template failed: %v", err)
			}
			s := string(data)
			for _, tok := range c.required {
				if !strings.Contains(s, tok) {
					t.Fatalf("required token missing: %q", tok)
				}
			}
			for _, tok := range c.forbidden {
				if strings.Contains(s, tok) {
					t.Fatalf("forbidden token exists: %q", tok)
				}
			}
		})
	}
}
