package server

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// InputRequest represents a keyboard input simulation request.
type InputRequest struct {
	Type string `json:"type"` // "text", "key", "ctrl"
	Data string `json:"data"`
}

// APIStatusResponse represents the terminal status response.
type APIStatusResponse struct {
	State            string                 `json:"state"`
	ConnectedClients int                    `json:"connected_clients"`
	TerminalSize     map[string]int         `json:"terminal_size"`
	Details          map[string]interface{} `json:"details,omitempty"`
}

// API request limits
const (
	maxCommandLen  = 8192 // bytes
	maxExecTimeout = 600  // seconds (10 minutes)
	maxAPIBodySize = 64 * 1024
)

// wrapAPIAuth wraps an HTTP handler with API token authentication.
func (server *Server) wrapAPIAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := server.options.Credential
		if token == "" {
			token = "user:pass"
		}

		// Bearer authentication only.
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(auth, "Bearer ") {
			provided := strings.TrimPrefix(auth, "Bearer ")
			if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
				next.ServeHTTP(w, r)
				return
			}
		}

		writeAPIError(w, http.StatusUnauthorized, "UNAUTHORIZED", "invalid or missing bearer token (query token is no longer supported)")
	})
}

// handleAPIInput handles POST /api/v1/input
func (server *Server) handleAPIInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}

	if !server.options.PermitWrite {
		writeAPIError(w, http.StatusForbidden, "WRITE_DISABLED", "terminal write is not permitted")
		return
	}

	var req InputRequest
	if err := decodeAPIRequestBody(w, r, &req); err != nil {
		status := http.StatusBadRequest
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
		}
		writeAPIError(w, status, "INVALID_REQUEST", err.Error())
		return
	}

	data, err := mapInput(req)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_INPUT", err.Error())
		return
	}

	// Atomically check terminal state and write — reject if API is executing
	var writeErr error
	ok := server.terminalStatus.WriteIfNotExecuting(func() {
		_, writeErr = server.sessionManager.slave.Write(data)
	})
	if !ok {
		writeAPIError(w, http.StatusConflict, "TERMINAL_BUSY", "API execution in progress")
		return
	}
	if writeErr != nil {
		writeAPIError(w, http.StatusInternalServerError, "WRITE_FAILED", writeErr.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ok":    true,
		"bytes": len(data),
	})
}

// handleAPIExec handles POST /api/v1/exec (non-streaming)
func (server *Server) handleAPIExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}

	if !server.options.PermitWrite {
		writeAPIError(w, http.StatusForbidden, "WRITE_DISABLED", "terminal write is not permitted")
		return
	}

	var req ExecRequest
	if err := decodeAPIRequestBody(w, r, &req); err != nil {
		status := http.StatusBadRequest
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
		}
		writeAPIError(w, status, "INVALID_REQUEST", err.Error())
		return
	}

	if req.Command == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "command is required")
		return
	}

	if len(req.Command) > maxCommandLen {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("command too long (%d bytes, max %d)", len(req.Command), maxCommandLen))
		return
	}

	if req.Timeout < 0 {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "timeout must be >= 0")
		return
	}

	if req.Timeout > maxExecTimeout {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("timeout too large (%d seconds, max %d)", req.Timeout, maxExecTimeout))
		return
	}

	result, err := server.execManager.Execute(r.Context(), req, server.options.ExecTimeoutSec)
	if err != nil {
		if execErr, ok := err.(*ExecError); ok {
			status := http.StatusInternalServerError
			switch execErr.Code {
			case "TERMINAL_BUSY":
				status = http.StatusConflict
			case "PROBE_FAILED":
				status = http.StatusPreconditionFailed
			}
			writeAPIError(w, status, execErr.Code, execErr.Message)
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "EXEC_FAILED", err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// handleAPIExecStream handles POST /api/v1/exec/stream (SSE)
func (server *Server) handleAPIExecStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}

	if !server.options.PermitWrite {
		writeAPIError(w, http.StatusForbidden, "WRITE_DISABLED", "terminal write is not permitted")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "STREAMING_UNSUPPORTED", "response does not support streaming")
		return
	}

	var req ExecRequest
	if err := decodeAPIRequestBody(w, r, &req); err != nil {
		status := http.StatusBadRequest
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
		}
		writeAPIError(w, status, "INVALID_REQUEST", err.Error())
		return
	}

	if req.Command == "" {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "command is required")
		return
	}

	if len(req.Command) > maxCommandLen {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("command too long (%d bytes, max %d)", len(req.Command), maxCommandLen))
		return
	}

	if req.Timeout < 0 {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "timeout must be >= 0")
		return
	}

	if req.Timeout > maxExecTimeout {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", fmt.Sprintf("timeout too large (%d seconds, max %d)", req.Timeout, maxExecTimeout))
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	eventCh := make(chan OutputEvent, 64)
	execCtx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Run execution in goroutine
	go func() {
		defer close(eventCh)
		if err := server.execManager.ExecuteStream(execCtx, req, server.options.ExecTimeoutSec, eventCh); err != nil {
			if execErr, ok := err.(*ExecError); ok {
				select {
				case eventCh <- OutputEvent{Type: "error", Content: execErr.Message}:
				case <-execCtx.Done():
				}
			} else {
				select {
				case eventCh <- OutputEvent{Type: "error", Content: err.Error()}:
				case <-execCtx.Done():
				}
			}
		}
	}()

	// Stream events
	for event := range eventCh {
		data, _ := json.Marshal(event)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, data); err != nil {
			cancel()
			return
		}
		flusher.Flush()
	}
}

// handleAPIOutputLines handles GET /api/v1/output/lines?n=50
func (server *Server) handleAPIOutputLines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required")
		return
	}

	nStr := r.URL.Query().Get("n")
	n := 50 // default
	if nStr != "" {
		var err error
		n, err = strconv.Atoi(nStr)
		if err != nil || n <= 0 {
			writeAPIError(w, http.StatusBadRequest, "INVALID_PARAM", "n must be a positive integer")
			return
		}
		if n > 1000 {
			n = 1000
		}
	}

	// Get history messages and decode
	messages := server.sessionManager.history.GetLastN(n)
	var allText []byte
	for _, msg := range messages {
		if len(msg) > 1 && msg[0] == '1' {
			decoded := make([]byte, base64.StdEncoding.DecodedLen(len(msg)-1))
			dn, err := base64.StdEncoding.Decode(decoded, msg[1:])
			if err == nil {
				allText = append(allText, decoded[:dn]...)
			}
		}
	}

	// Split into lines and take last N
	allLines := strings.Split(string(allText), "\n")
	// Clean up \r
	for i := range allLines {
		allLines[i] = strings.TrimRight(allLines[i], "\r")
	}
	// Remove trailing empty lines
	for len(allLines) > 0 && allLines[len(allLines)-1] == "" {
		allLines = allLines[:len(allLines)-1]
	}

	start := 0
	if len(allLines) > n {
		start = len(allLines) - n
	}
	lines := allLines[start:]

	// Optionally strip ANSI escape sequences
	if r.URL.Query().Get("strip_ansi") == "true" {
		for i := range lines {
			lines[i] = stripAnsi(lines[i])
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"lines": lines,
		"total": len(lines),
	})
}

// handleAPIStatus handles GET /api/v1/status
func (server *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required")
		return
	}

	server.sessionManager.mu.RLock()
	cols := server.sessionManager.activeCols
	rows := server.sessionManager.activeRows
	clientCount := len(server.sessionManager.clients)
	server.sessionManager.mu.RUnlock()

	resp := APIStatusResponse{
		State:            server.terminalStatus.GetState().String(),
		ConnectedClients: clientCount,
		TerminalSize: map[string]int{
			"cols": cols,
			"rows": rows,
		},
		Details: server.terminalStatus.GetStatus(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// mapInput converts an InputRequest to raw bytes for the PTY.
func mapInput(req InputRequest) ([]byte, error) {
	switch req.Type {
	case "text":
		if req.Data == "" {
			return nil, fmt.Errorf("data is required for text input")
		}
		return []byte(req.Data), nil

	case "key":
		switch strings.ToLower(req.Data) {
		case "enter", "return":
			return []byte("\r"), nil
		case "tab":
			return []byte("\t"), nil
		case "backspace":
			return []byte{0x7f}, nil
		case "escape", "esc":
			return []byte{0x1b}, nil
		case "up":
			return []byte("\x1b[A"), nil
		case "down":
			return []byte("\x1b[B"), nil
		case "right":
			return []byte("\x1b[C"), nil
		case "left":
			return []byte("\x1b[D"), nil
		case "home":
			return []byte("\x1b[H"), nil
		case "end":
			return []byte("\x1b[F"), nil
		case "delete":
			return []byte("\x1b[3~"), nil
		case "space":
			return []byte(" "), nil
		default:
			return nil, fmt.Errorf("unknown key: %s", req.Data)
		}

	case "ctrl":
		if len(req.Data) != 1 {
			return nil, fmt.Errorf("ctrl data must be a single character (a-z)")
		}
		ch := strings.ToLower(req.Data)[0]
		if ch < 'a' || ch > 'z' {
			return nil, fmt.Errorf("ctrl data must be a-z")
		}
		return []byte{ch - 'a' + 1}, nil

	default:
		return nil, fmt.Errorf("unknown input type: %s (expected: text, key, ctrl)", req.Type)
	}
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{
		"code":    code,
		"message": message,
	})
	log.Printf("[API Error] %d %s: %s", status, code, message)
}

func decodeAPIRequestBody(w http.ResponseWriter, r *http.Request, dst interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxAPIBodySize)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return err
	}

	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request body must contain exactly one JSON object")
	}

	return nil
}

// ansiPattern matches ANSI escape sequences (CSI, OSC, and single ESC sequences).
var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-9;]*[a-zA-Z@]|\][^\x07]*\x07|\[[^\x1b]*|.)`)

// stripAnsi removes ANSI escape sequences from a string.
func stripAnsi(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}
