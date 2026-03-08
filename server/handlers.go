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

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"

	"github.com/sorenisanerd/gotty/webtty"
)

func (server *Server) generateHandleWS(ctx context.Context, cancel context.CancelFunc, counter *counter) http.HandlerFunc {
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

		if server.options.PassHeaders {
			err = server.processWSConn(ctx, conn, r.Header)
		} else {
			err = server.processWSConn(ctx, conn, nil)
		}

		switch err {
		case ctx.Err():
			closeReason = "cancelation"
		case webtty.ErrSlaveClosed:
			closeReason = server.factory.Name()
		case webtty.ErrMasterClosed:
			closeReason = "client"
		default:
			closeReason = fmt.Sprintf("an error: %s", err)
		}
	}
}

func (server *Server) processWSConn(ctx context.Context, conn *websocket.Conn, headers map[string][]string) error {
	typ, initLine, err := conn.ReadMessage()
	if err != nil {
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

	client := &Client{
		conn:  conn,
		send:  make(chan []byte, 256),
		ready: make(chan struct{}),
	}

	server.sessionManager.register <- client
	defer func() {
		server.sessionManager.unregister <- client
	}()
	select {
	case <-client.ready:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Send history
	history := server.sessionManager.history.GetAll()
	for _, msg := range history {
		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return err
		}
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
				return err
			}
			if len(message) > 0 {
				server.handleClientInput(client, message)
			}
		}
	}
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
	}
	return indexVars, err
}

func (server *Server) handleAuthToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	// @TODO hashing?
	w.Write([]byte("var gotty_auth_token = '" + server.options.Credential + "';"))
}

func (server *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
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
	}

	w.Write([]byte(strings.Join(lines, "\n")))
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
