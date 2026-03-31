package server

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed web/*.html
//go:embed web/*.css
//go:embed web/*.js
var dashboardFS embed.FS

// longCacheHeaders sets aggressive caching for versioned static assets (CSS/JS).
func longCacheHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=86400, stale-while-revalidate=3600")
	w.Header().Set("CDN-Cache-Control", "public, max-age=86400")
	w.Header().Set("Cloudflare-CDN-Cache-Control", "public, max-age=86400")
}

func (s *Server) handleDashboardAsset(w http.ResponseWriter, r *http.Request) {
	// Serve CSS/JS with aggressive caching; HTML uses setStaticResourceCacheHeaders (no-cache).
	file := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if file != "dashboard.css" && file != "dashboard.js" {
		http.NotFound(w, r)
		return
	}
	data, err := dashboardFS.ReadFile("web/" + file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	longCacheHeaders(w)
	if file == "dashboard.css" {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleColonyPublic(w http.ResponseWriter, r *http.Request) {
	data, err := dashboardFS.ReadFile("web/colony_public.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setStaticResourceCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleMailboxVision(w http.ResponseWriter, r *http.Request) {
	data, err := dashboardFS.ReadFile("web/agent_mailbox_vision.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setStaticResourceCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	cleanPath := strings.Trim(path.Clean(r.URL.Path), "/")
	page := "dashboard_home.html"

	switch cleanPath {
	case "dashboard":
		page = "dashboard_home.html"
	case "dashboard/mail":
		page = "dashboard_mail.html"
	case "dashboard/system-logs":
		page = "dashboard_system_logs.html"
	case "dashboard/collab":
		page = "dashboard_collab.html"
	case "dashboard/kb":
		page = "dashboard_kb.html"
	case "dashboard/world-tick":
		page = "dashboard_world_tick.html"
	case "dashboard/world-replay":
		page = "dashboard_world_replay.html"
	case "dashboard/ops":
		page = "dashboard_ops.html"
	case "dashboard/monitor":
		page = "dashboard_monitor.html"
	case "dashboard/governance":
		page = "dashboard_governance.html"
	case "dashboard/ganglia":
		page = "dashboard_ganglia.html"
	case "dashboard/bounty":
		page = "dashboard_bounty.html"
	case "dashboard/agent-register":
		page = "dashboard_agent_register.html"
	case "dashboard/agent-owner":
		page = "dashboard_agent_owner.html"
	default:
		writeError(w, http.StatusNotFound, "dashboard page not found")
		return
	}

	data, err := dashboardFS.ReadFile("web/" + page)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	setStaticResourceCacheHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
