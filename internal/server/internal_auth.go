package server

import (
	"net/http"
	"strings"
)

func internalWriteTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.Header.Get("X-Clawcolony-Internal-Token"))
}

// authorizeInternalWriteRequest allows loopback callers and non-loopback
// callers that present the configured internal sync token.
func (s *Server) authorizeInternalWriteRequest(w http.ResponseWriter, r *http.Request) bool {
	if isLoopbackRemoteAddr(r.RemoteAddr) {
		return true
	}
	expected := strings.TrimSpace(s.cfg.InternalSyncToken)
	if expected == "" {
		writeError(w, http.StatusUnauthorized, "non-loopback requests require internal sync token configuration")
		return false
	}
	got := internalWriteTokenFromRequest(r)
	if got == "" || !secureStringEqual(got, expected) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	return true
}
