package server

import (
	"net/http"
	"net/http/httptest"
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

func TestShareAuthBlocksPublicTunnelHost(t *testing.T) {
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

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
}
