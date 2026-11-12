package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// A read-only diagnostic; unlike opening a terminal it creates no client,
// elects no leader, and emits no terminal/replay events.
func (server *Server) handleTerminalGeometry(w http.ResponseWriter, r *http.Request) {
	if !server.authorizeControlRequest(r) || server.isPublicShareRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	sm := server.sessionManager
	sm.mu.RLock()
	state := sm.terminalStateLocked("diagnostic", time.Now())
	sm.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(state)
}
