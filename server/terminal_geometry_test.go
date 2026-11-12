package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGeometryDiagnosticIsAuthenticatedAndReadOnly(t *testing.T) {
	sm, slave := adaptiveManager(t)
	client := addResizeClient(sm, "desktop", "private-browser-identity", false)
	now := time.Now()
	sm.handleResizeEvent(client, viewport(160, 45, "fine"), now)
	sm.flushResize(now.Add(time.Second))
	s := &Server{options: &Options{Credential: "admin:secret"}, sessionManager: sm}
	sequence, sizes, clients := sm.stream.sequence, len(slave.sizes), len(sm.clients)
	for _, tc := range []struct {
		method        string
		authenticated bool
		status        int
	}{{"GET", false, 403}, {"POST", true, 405}, {"GET", true, 200}} {
		req := httptest.NewRequest(tc.method, "http://localhost/-/terminal-state", nil)
		if tc.authenticated {
			req.SetBasicAuth("admin", "secret")
		}
		rr := httptest.NewRecorder()
		s.handleTerminalGeometry(rr, req)
		if rr.Code != tc.status {
			t.Fatalf("status %d, expected %d", rr.Code, tc.status)
		}
		if tc.status == http.StatusOK {
			var state terminalState
			if err := json.Unmarshal(rr.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
			if state.ActiveCols != 160 || state.LeaderDevice != "desktop" || state.ControlReason != "adaptive-initial" || rr.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("incomplete geometry: %+v", state)
			}
			if strings.Contains(rr.Body.String(), client.key) || strings.Contains(rr.Body.String(), "secret") {
				t.Fatal("diagnostic exposed credentials or browser identity")
			}
		}
	}
	if sm.stream.sequence != sequence || len(slave.sizes) != sizes || len(sm.clients) != clients {
		t.Fatal("read-only diagnostic changed terminal state")
	}
}
