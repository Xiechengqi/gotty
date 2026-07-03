package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

var ErrRestartDisabled = errors.New("server restart is disabled")

func (server *Server) generateHandleSelfRestart(cancel context.CancelFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeRestartError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
			return
		}
		if server.isReadOnlyShareRequest(r) {
			writeRestartError(w, http.StatusForbidden, "READ_ONLY", "server restart is disabled for read-only shares")
			return
		}
		if !server.authorizeControlRequest(r) {
			writeRestartError(w, http.StatusUnauthorized, "UNAUTHORIZED", "restart is not authorized")
			return
		}

		mode, err := server.requestSelfRestart(cancel)
		if errors.Is(err, ErrRestartDisabled) {
			writeRestartError(w, http.StatusForbidden, "RESTART_DISABLED", err.Error())
			return
		}
		if err != nil {
			writeRestartError(w, http.StatusInternalServerError, "RESTART_FAILED", err.Error())
			return
		}

		writeRestartJSON(w, http.StatusAccepted, map[string]interface{}{
			"ok":      true,
			"mode":    mode,
			"message": restartMessage(mode),
		})
	}
}

func (server *Server) requestSelfRestart(cancel context.CancelFunc) (string, error) {
	server.restartMu.Lock()
	defer server.restartMu.Unlock()

	if server.selfRestarting {
		return server.selfRestartMode, nil
	}

	mode := effectiveRestartMode(server.options.RestartMode)
	switch mode {
	case RestartModeDisabled:
		return "", ErrRestartDisabled
	case RestartModeExec:
		plan, err := newExecSelfRestartPlan(server.selfRestartEnv())
		if err != nil {
			return "", err
		}
		server.selfRestartPlan = &plan
	case RestartModeFork:
		if err := scheduleSelfRestartFunc(server.selfRestartEnv()); err != nil {
			return "", err
		}
	case RestartModeExit:
		// Exit after responding; an external supervisor may restart the process.
	default:
		return "", ErrRestartDisabled
	}
	server.selfRestartMode = mode
	server.selfRestarting = true

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	return mode, nil
}

func (server *Server) selfRestartEnv() map[string]string {
	env := make(map[string]string)
	if server.shareManager != nil {
		if ownerID := server.shareManager.OwnerID(); ownerID != "" {
			env[shareOwnerEnv] = ownerID
		}
	}
	return env
}

func (server *Server) PendingSelfRestart() (SelfRestartPlan, bool) {
	server.restartMu.Lock()
	defer server.restartMu.Unlock()

	if server.selfRestartPlan == nil {
		return SelfRestartPlan{}, false
	}
	plan := *server.selfRestartPlan
	plan.Args = append([]string(nil), plan.Args...)
	plan.Env = append([]string(nil), plan.Env...)
	return plan, true
}

func restartMessage(mode string) string {
	switch mode {
	case RestartModeExec:
		return "restart scheduled with in-place exec"
	case RestartModeFork:
		return "restart scheduled with fork"
	case RestartModeExit:
		return "exit scheduled for external restart"
	default:
		return "restart scheduled"
	}
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
