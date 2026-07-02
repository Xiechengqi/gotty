package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestShareAuthAllowsGottyHostFromRemoteAddress(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   true,
			ShareServerURL: "https://httptunnel.top",
		},
		shareManager: &ShareManager{},
	}
	handler := server.wrapShareAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/-/shares", nil)
	req.Host = "example.com"
	req.RemoteAddr = "100.83.230.8:55717"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestShareAuthAllowsPublicTunnelHost(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   true,
			ShareServerURL: "https://httptunnel.top",
		},
		shareManager: &ShareManager{},
	}
	handler := server.wrapShareAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/-/shares", nil)
	req.Host = "gotty-demo.httptunnel.top"
	req.RemoteAddr = "100.83.230.8:55717"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestShareAuthReturnsNotFoundWhenDisabled(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   false,
			ShareServerURL: "https://httptunnel.top",
		},
		shareManager: &ShareManager{},
	}
	handler := server.wrapShareAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/-/shares", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestConfigMarksPublicShareHost(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   true,
			ShareServerURL: "https://httptunnel.top",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/config.js", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "var gotty_share_public_host = true;") {
		t.Fatalf("config.js did not mark public share host: %s", rec.Body.String())
	}
}

func TestConfigMarksNormalGottyHostAsPrivate(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   true,
			ShareServerURL: "https://httptunnel.top",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/config.js", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()

	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "var gotty_share_public_host = false;") {
		t.Fatalf("config.js did not mark normal host as private: %s", rec.Body.String())
	}
}
