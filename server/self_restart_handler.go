package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

func (server *Server) generateHandleSelfRestart(cancel context.CancelFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeRestartError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
			return
		}
		if !server.authorizeControlRequest(r) {
			writeRestartError(w, http.StatusUnauthorized, "UNAUTHORIZED", "restart is not authorized")
			return
		}

		if err := server.requestSelfRestart(cancel); err != nil {
			writeRestartError(w, http.StatusInternalServerError, "RESTART_FAILED", err.Error())
			return
		}

		writeRestartJSON(w, http.StatusAccepted, map[string]interface{}{
			"ok":      true,
			"message": "restart scheduled",
		})
	}
}

func (server *Server) requestSelfRestart(cancel context.CancelFunc) error {
	server.restartMu.Lock()
	defer server.restartMu.Unlock()

	if server.selfRestarting {
		return nil
	}
	if err := scheduleSelfRestart(); err != nil {
		return err
	}
	server.selfRestarting = true

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	return nil
}

func (server *Server) authorizeControlRequest(r *http.Request) bool {
	credential := server.options.Credential
	if credential == "" {
		return true
	}

	if cookie, err := r.Cookie("gotty_auth_token"); err == nil {
		if constantTimeStringEqual(cookie.Value, credential) {
			return true
		}
	}

	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if constantTimeStringEqual(provided, credential) {
			return true
		}
	}

	if user, pass, ok := r.BasicAuth(); ok {
		if constantTimeStringEqual(user+":"+pass, credential) {
			return true
		}
	}

	return false
}

func constantTimeStringEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeRestartJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeRestartError(w http.ResponseWriter, status int, code, message string) {
	writeRestartJSON(w, status, map[string]string{
		"code":    code,
		"message": message,
	})
}
