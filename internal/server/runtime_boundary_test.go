package server

import (
	"net/http"
	"testing"
)

func TestRuntimeDoesNotExposeLegacyManagementEndpoints(t *testing.T) {
	srv := newTestServer()

	cases := []struct {
		method string
		path   string
		body   map[string]any
	}{
		{method: http.MethodPost, path: "/api/v1/bots/register", body: map[string]any{"provider": "openclaw"}},
		{method: http.MethodGet, path: "/api/v1/dashboard-admin/openclaw/admin/overview"},
	}

	for _, tc := range cases {
		w := doJSONRequest(t, srv.mux, tc.method, tc.path, tc.body)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s %s should be hidden from runtime, got=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
