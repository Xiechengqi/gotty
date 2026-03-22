package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWrapAPIAuthBearerOnly(t *testing.T) {
	s := &Server{
		options: &Options{
			Credential: "admin:secret",
		},
	}

	protected := s.wrapAPIAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	t.Run("accepts bearer token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		req.Header.Set("Authorization", "Bearer admin:secret")
		rr := httptest.NewRecorder()

		protected.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d (body=%s)", rr.Code, rr.Body.String())
		}
	})

	t.Run("rejects query token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status?token=admin:secret", nil)
		rr := httptest.NewRecorder()

		protected.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d (body=%s)", rr.Code, rr.Body.String())
		}
	})

	t.Run("rejects missing bearer", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
		rr := httptest.NewRecorder()

		protected.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d (body=%s)", rr.Code, rr.Body.String())
		}
	})
}

func TestHandleAPIExecRejectsNegativeTimeout(t *testing.T) {
	s := &Server{
		options: &Options{
			PermitWrite: true,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", strings.NewReader(`{"command":"echo ok","timeout":-1}`))
	rr := httptest.NewRecorder()

	s.handleAPIExec(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleAPIExecRejectsUnknownFields(t *testing.T) {
	s := &Server{
		options: &Options{
			PermitWrite: true,
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", strings.NewReader(`{"command":"echo ok","unknown":1}`))
	rr := httptest.NewRecorder()

	s.handleAPIExec(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleAPIExecRejectsTooLargeBody(t *testing.T) {
	s := &Server{
		options: &Options{
			PermitWrite: true,
		},
	}

	oversized := `{"command":"` + strings.Repeat("x", maxAPIBodySize) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/exec", strings.NewReader(oversized))
	rr := httptest.NewRecorder()

	s.handleAPIExec(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleAPIOutputLinesUsesLineHistory(t *testing.T) {
	s := &Server{
		sessionManager: &SessionManager{
			lineHistory: NewLineBuffer(10),
		},
	}
	s.sessionManager.lineHistory.Append([]byte("line1\nline"))
	s.sessionManager.lineHistory.Append([]byte("2\nline3"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/output/lines?n=2", nil)
	rr := httptest.NewRecorder()

	s.handleAPIOutputLines(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (body=%s)", rr.Code, rr.Body.String())
	}

	var got struct {
		Lines []string `json:"lines"`
		Total int      `json:"total"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("expected json body, got error: %v", err)
	}
	if got.Total != 2 {
		t.Fatalf("expected total=2, got %d", got.Total)
	}
	want := []string{"line2", "line3"}
	for i := range want {
		if got.Lines[i] != want[i] {
			t.Fatalf("unexpected lines: %#v", got.Lines)
		}
	}
}

func TestWriteAPIErrorJSONShape(t *testing.T) {
	rr := httptest.NewRecorder()
	writeAPIError(rr, http.StatusUnauthorized, "UNAUTHORIZED", "invalid token")

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}

	var got map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("expected json body, got error: %v", err)
	}
	if got["code"] != "UNAUTHORIZED" || got["message"] != "invalid token" {
		t.Fatalf("unexpected body: %#v", got)
	}
}
