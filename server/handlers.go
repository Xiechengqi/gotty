package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/sorenisanerd/gotty/webtty"
)

const replayChunkSize = 64 * 1024

type replayBeginPayload struct {
	Epoch      string `json:"epoch"`
	Mode       string `json:"mode"`
	FromOffset int64  `json:"fromOffset"`
}

type replayEndPayload struct {
	EndOffset int64 `json:"endOffset"`
}

func (server *Server) generateHandleWS(ctx context.Context, gracefullCtx context.Context, cancel context.CancelFunc, counter *counter) http.HandlerFunc {
	once := new(int64)

	go func() {
		select {
		case <-counter.timer().C:
			cancel()
		case <-ctx.Done():
		}
	}()

	return func(w http.ResponseWriter, r *http.Request) {
		if server.options.Once {
			success := atomic.CompareAndSwapInt64(once, 0, 1)
			if !success {
				http.Error(w, "Server is shutting down", http.StatusServiceUnavailable)
				return
			}
		}

		num := counter.add(1)
		closeReason := "unknown reason"

		defer func() {
			num := counter.done()
			log.Printf(
				"Connection closed by %s: %s, connections: %d/%d",
				closeReason, r.RemoteAddr, num, server.options.MaxConnection,
			)

			if server.options.Once {
				cancel()
			}
		}()

		if int64(server.options.MaxConnection) != 0 {
			if num > server.options.MaxConnection {
				closeReason = "exceeding max number of connections"
				return
			}
		}

		log.Printf("New client connected: %s, connections: %d/%d", r.RemoteAddr, num, server.options.MaxConnection)

		if r.Method != "GET" {
			http.Error(w, "Method not allowed", 405)
			return
		}

		conn, err := server.upgrader.Upgrade(w, r, nil)
		if err != nil {
			closeReason = err.Error()
			return
		}
		defer conn.Close()

		connCtx, connCancel := withWebSocketShutdown(ctx, gracefullCtx, conn)
		defer connCancel()

		if server.options.PassHeaders {
			err = server.processWSConn(connCtx, conn, r.Header)
		} else {
			err = server.processWSConn(connCtx, conn, nil)
		}

		switch err {
		case connCtx.Err():
			closeReason = "cancelation"
		case webtty.ErrSlaveClosed:
			closeReason = server.factory.Name()
		case webtty.ErrMasterClosed:
			closeReason = "client"
			if server.clientGoneCh != nil {
				server.clientGoneCh <- r.RemoteAddr
			}
		default:
			closeReason = fmt.Sprintf("an error: %s", err)
		}
	}
}

func (server *Server) processWSConn(ctx context.Context, conn *websocket.Conn, headers map[string][]string) error {
	typ, initLine, err := conn.ReadMessage()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.Wrapf(err, "failed to authenticate websocket connection")
	}
	if typ != websocket.TextMessage {
		return errors.New("failed to authenticate websocket connection: invalid message type")
	}

	var init InitMessage
	err = json.Unmarshal(initLine, &init)
	if err != nil {
		return errors.Wrapf(err, "failed to authenticate websocket connection")
	}
	if init.AuthToken != server.options.Credential {
		return errors.New("failed to authenticate websocket connection")
	}

	// Set up server-side WebSocket ping/pong to keep the connection alive
	// when the browser tab is in the background (where JS timers are throttled).
	pingInterval := server.options.PingInterval
	if pingInterval > 0 {
		conn.SetReadDeadline(time.Now().Add(time.Duration(pingInterval) * 2 * time.Second))
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(time.Duration(pingInterval) * 2 * time.Second))
		})
		pingCtx, pingCancel := context.WithCancel(ctx)
		defer pingCancel()
		go func() {
			ticker := time.NewTicker(time.Duration(pingInterval) * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(10*time.Second)); err != nil {
						return
					}
				case <-pingCtx.Done():
					return
				}
			}
		}()
	}

	client := &Client{
		conn:  conn,
		send:  make(chan []byte, 256),
		ready: make(chan struct{}),
		init:  init,
	}

	select {
	case server.sessionManager.register <- client:
	case <-server.sessionManager.ctx.Done():
		return server.sessionManager.ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() {
		select {
		case server.sessionManager.unregister <- client:
		case <-server.sessionManager.ctx.Done():
		}
	}()
	select {
	case <-client.ready:
	case <-ctx.Done():
		return ctx.Err()
	}

	if err := server.sendReplayMessages(ctx, conn, client.replay); err != nil {
		return err
	}

	// Send initial messages
	server.sendInitMessages(conn, client)

	done := make(chan struct{})

	// Write to client
	go func() {
		defer close(done)
		for {
			select {
			case message, ok := <-client.send:
				if !ok {
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Read from client
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return nil
		default:
			_, message, err := conn.ReadMessage()
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return err
			}
			if len(message) > 0 {
				server.handleClientInput(client, message)
			}
		}
	}
}

func (server *Server) sendReplayMessages(ctx context.Context, conn *websocket.Conn, replay replaySnapshot) error {
	mode := replay.Mode
	if mode == "" {
		mode = "tail"
	}

	begin, err := json.Marshal(replayBeginPayload{
		Epoch:      replay.Epoch,
		Mode:       mode,
		FromOffset: replay.FromOffset,
	})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, append([]byte{webtty.ReplayBegin}, begin...)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}

	if mode == "tail" {
		if err := conn.WriteMessage(websocket.TextMessage, encodeOutputMessage([]byte("\x1bc"))); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}

	for start := 0; start < len(replay.Data); start += replayChunkSize {
		end := start + replayChunkSize
		if end > len(replay.Data) {
			end = len(replay.Data)
		}
		if err := conn.WriteMessage(websocket.TextMessage, encodeOutputMessage(replay.Data[start:end])); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}

	done, err := json.Marshal(replayEndPayload{EndOffset: replay.EndOffset})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, append([]byte{webtty.ReplayEnd}, done...)); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

func (server *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	indexVars, err := server.indexVariables(r)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		return
	}

	indexBuf := new(bytes.Buffer)
	err = server.indexTemplate.Execute(indexBuf, indexVars)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Write(indexBuf.Bytes())
}

func (server *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	indexVars, err := server.indexVariables(r)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		return
	}

	indexBuf := new(bytes.Buffer)
	err = server.manifestTemplate.Execute(indexBuf, indexVars)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Write(indexBuf.Bytes())
}

func (server *Server) indexVariables(r *http.Request) (map[string]interface{}, error) {
	titleVars := server.titleVariables(
		[]string{"server", "master"},
		map[string]map[string]interface{}{
			"server": server.options.TitleVariables,
			"master": map[string]interface{}{
				"remote_addr": r.RemoteAddr,
			},
		},
	)

	titleBuf := new(bytes.Buffer)
	err := server.titleTemplate.Execute(titleBuf, titleVars)
	if err != nil {
		return nil, err
	}

	indexVars := map[string]interface{}{
		"title": titleBuf.String(),
		"favicon": server.options.Favicon,
	}
	return indexVars, err
}

func (server *Server) handleAuthToken(w http.ResponseWriter, r *http.Request) {
	// Check if cookie already exists
	cookie, err := r.Cookie("gotty_auth_token")
	if err != nil || cookie.Value != server.options.Credential {
		// Set persistent cookie for 30 days
		http.SetCookie(w, &http.Cookie{
			Name:     "gotty_auth_token",
			Value:    server.options.Credential,
			Path:     "/",
			MaxAge:   30 * 24 * 60 * 60, // 30 days
			HttpOnly: false, // Allow JavaScript to read for compatibility
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteStrictMode,
		})
	}

	w.Header().Set("Content-Type", "application/javascript")
	// @TODO hashing?
	w.Write([]byte("var gotty_auth_token = '" + server.options.Credential + "';"))
}

func (server *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	preferences, err := json.Marshal(server.buildPreferences())
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	lines := []string{
		"var gotty_term = 'xterm';",
		"var gotty_ws_query_args = '" + server.options.WSQueryArgs + "';",
		"var gotty_permit_write = " + strconv.FormatBool(server.options.PermitWrite) + ";",
		"var gotty_enable_idle_alert = " + strconv.FormatBool(server.options.EnableIdleAlert) + ";",
		"var gotty_idle_alert_timeout = " + strconv.Itoa(server.options.IdleAlertTimeout) + ";",
		"var gotty_enable_asr = " + strconv.FormatBool(server.options.EnableASR) + ";",
		"var gotty_asr_hold_ms = " + strconv.Itoa(server.options.ASRHoldMs) + ";",
		"var gotty_asr_hotkey = '" + server.options.ASRHotkey + "';",
		"var gotty_resize_policy = '" + server.options.ResizePolicy + "';",
		"var gotty_resize_debounce_ms = " + strconv.Itoa(server.options.ResizeDebounceMs) + ";",
		"var gotty_resize_min_cols = " + strconv.Itoa(server.options.MinCols) + ";",
		"var gotty_resize_max_cols = " + strconv.Itoa(server.options.MaxCols) + ";",
		"var gotty_resize_min_rows = " + strconv.Itoa(server.options.MinRows) + ";",
		"var gotty_resize_max_rows = " + strconv.Itoa(server.options.MaxRows) + ";",
		"var gotty_leader_select = '" + server.options.LeaderSelect + "';",
		"var gotty_leader_idle_ms = " + strconv.Itoa(server.options.LeaderIdleMs) + ";",
		"var gotty_show_terminal_state = " + strconv.FormatBool(server.options.ShowTerminalState) + ";",
		"var gotty_share_enabled = " + strconv.FormatBool(server.options.ShareEnabled) + ";",
		"var gotty_preferences = " + string(preferences) + ";",
	}

	w.Write([]byte(strings.Join(lines, "\n")))
}

func (server *Server) handleThemes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	themeJSON, err := json.Marshal(builtinThemes)
	if err != nil {
		http.Error(w, "Internal Server Error", 500)
		return
	}
	w.Write([]byte("var gotty_themes = "))
	w.Write(themeJSON)
	w.Write([]byte(";\n"))
}

// buildPreferences assembles the terminal preferences map sent to the browser.
// It starts by resolving the user's chosen theme (or the default), then
// applies any individual config overrides on top.
// Only non-zero values are included so xterm.js keeps its defaults for the rest.
func (server *Server) buildPreferences() map[string]interface{} {
	if server.options.Preferences == nil {
		return map[string]interface{}{}
	}

	prefs := server.options.Preferences
	out := make(map[string]interface{})

	// Resolve theme
	themeName := prefs.Theme
	if themeName == "" {
		themeName = "default"
	}
	themeColors := resolveTheme(themeName)

	// Apply individual color overrides on top of the theme
	applyIfSet := func(key, value string) {
		if value != "" {
			themeColors[key] = value
			out["theme"] = themeColors
		}
	}

	applyIfSet("foreground", prefs.ForegroundColor)
	applyIfSet("background", prefs.BackgroundColor)
	applyIfSet("cursor", prefs.CursorColor)
	applyIfSet("cursorAccent", prefs.CursorAccent)
	applyIfSet("selection", prefs.SelectionColor)

	// If theme colors exist (non-empty), set the theme object
	if len(themeColors) > 0 {
		out["theme"] = themeColors
	}

	// Font options
	if prefs.FontSize > 0 {
		out["font-size"] = prefs.FontSize
	}
	if prefs.FontFamily != "" {
		out["font-family"] = prefs.FontFamily
	}

	// Cursor options
	if prefs.CursorStyle != "" {
		out["cursor-style"] = prefs.CursorStyle
	}
	if prefs.CursorBlink {
		out["cursor-blink"] = true
	}

	// Scrollback
	if prefs.ScrollbackLines > 0 {
		out["scrollback-lines"] = prefs.ScrollbackLines
	}

	// WebGL
	if prefs.EnableWebGL {
		out["EnableWebGL"] = true
	}

	// Alt as Meta key
	if prefs.AltIsMeta {
		out["alt-is-meta"] = true
	}

	// Color palette overrides (overrides built-in theme palette entries)
	if len(prefs.ColorPaletteOverrides) > 0 {
		palette := prefs.ColorPaletteOverrides
		paletteMap := make(map[string]string)
		colorKeys := []string{
			"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
			"brightBlack", "brightRed", "brightGreen", "brightYellow",
			"brightBlue", "brightMagenta", "brightCyan", "brightWhite",
		}
		for i, c := range palette {
			if i >= len(colorKeys) {
				break
			}
			if c != "" {
				paletteMap[colorKeys[i]] = c
			}
		}
		if len(paletteMap) > 0 {
			// Merge palette into theme colors
			currentTheme, _ := out["theme"].(map[string]string)
			if currentTheme == nil {
				currentTheme = make(map[string]string)
			}
			for k, v := range paletteMap {
				currentTheme[k] = v
			}
			out["theme"] = currentTheme
		}
	}

	return out
}

// titleVariables merges maps in a specified order.
// varUnits are name-keyed maps, whose names will be iterated using order.
func (server *Server) titleVariables(order []string, varUnits map[string]map[string]interface{}) map[string]interface{} {
	titleVars := map[string]interface{}{}

	for _, name := range order {
		vars, ok := varUnits[name]
		if !ok {
			panic("title variable name error")
		}
		for key, val := range vars {
			titleVars[key] = val
		}
	}

	// safe net for conflicted keys
	for _, name := range order {
		titleVars[name] = varUnits[name]
	}

	return titleVars
}
