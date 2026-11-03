package server

import (
	"context"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaxConnection_RefusesWithServiceUnavailable(t *testing.T) {
	// A connection beyond MaxConnection must be refused with 503, not a
	// silent 200 OK with an empty body.
	opts := &Options{
		Address:       "127.0.0.1",
		Port:          "0",
		Quiet:         true,
		MaxConnection: 1,
	}

	server, err := New(&mockFactory{}, opts)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	counter := newCounter(0)
	handler := server.setupHandlers(ctx, ctx, cancel, "/", counter)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"

	// First connection should be accepted.
	firstConn, firstResp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first connection should have been accepted, got error: %v", err)
	}
	defer firstConn.Close()
	if firstResp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("expected first connection to get %d, got %d", http.StatusSwitchingProtocols, firstResp.StatusCode)
	}

	// Second connection should be refused with 503 while the first is held open.
	secondConn, secondResp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		secondConn.Close()
		t.Fatalf("expected second connection to be refused, but it was accepted")
	}
	if secondResp == nil {
		t.Fatalf("expected an HTTP response for the refused connection, got none (err: %v)", err)
	}
	if secondResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected second connection to get %d, got %d", http.StatusServiceUnavailable, secondResp.StatusCode)
	}
}

func TestHandleConfig_EmitsConfirmClose(t *testing.T) {
	// /config.js must expose the confirm-close setting so the browser can decide
	// whether to arm the beforeunload guard.
	for _, tc := range []struct {
		name    string
		confirm bool
		want    string
	}{
		{"enabled", true, "var gotty_confirm_close = true;"},
		{"disabled", false, "var gotty_confirm_close = false;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := New(&mockFactory{}, &Options{Quiet: true, ConfirmClose: tc.confirm})
			if err != nil {
				t.Fatalf("failed to create server: %v", err)
			}

			rec := httptest.NewRecorder()
			srv.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/config.js", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("expected config.js to contain %q, got:\n%s", tc.want, rec.Body.String())
			}
		})
	}
}

func TestHandleConfig_EmitsMobileKeys(t *testing.T) {
	// /config.js must tell the browser whether to show the touch-device key
	// strip. A read-only session ignores input, so the strip is off there too.
	for _, tc := range []struct {
		name  string
		keys  bool
		write bool
		want  string
	}{
		{"enabled with write", true, true, "var gotty_mobile_keys = true;"},
		{"enabled read-only", true, false, "var gotty_mobile_keys = false;"},
		{"disabled", false, true, "var gotty_mobile_keys = false;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := New(&mockFactory{}, &Options{Quiet: true, MobileKeys: tc.keys, PermitWrite: tc.write})
			if err != nil {
				t.Fatalf("failed to create server: %v", err)
			}

			rec := httptest.NewRecorder()
			srv.handleConfig(rec, httptest.NewRequest(http.MethodGet, "/config.js", nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("expected config.js to contain %q, got:\n%s", tc.want, rec.Body.String())
			}
		})
	}
}
