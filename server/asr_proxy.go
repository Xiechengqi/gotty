package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/errors"
)

func (server *Server) generateHandleASRWS(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !server.options.EnableASR {
			http.Error(w, "ASR is disabled", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		upgrader := websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     server.upgrader.CheckOrigin,
		}

		clientConn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer clientConn.Close()

		if err := server.authenticateASRWS(clientConn); err != nil {
			_ = clientConn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.ClosePolicyViolation, err.Error()),
				time.Now().Add(1*time.Second),
			)
			return
		}

		backendConn, _, err := websocket.DefaultDialer.Dial(server.options.ASRBackend, nil)
		if err != nil {
			log.Printf("ASR backend dial failed: %v", err)
			_ = clientConn.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseTryAgainLater, "ASR backend unavailable"),
				time.Now().Add(1*time.Second),
			)
			return
		}
		defer backendConn.Close()

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			server.proxyWS(ctx, backendConn, clientConn)
			cancel()
		}()

		go func() {
			defer wg.Done()
			server.proxyWS(ctx, clientConn, backendConn)
			cancel()
		}()

		<-ctx.Done()
		_ = clientConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		_ = backendConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		wg.Wait()
	}
}

func (server *Server) authenticateASRWS(conn *websocket.Conn) error {
	typ, data, err := conn.ReadMessage()
	if err != nil {
		return errors.Wrap(err, "failed to read init message")
	}
	if typ != websocket.TextMessage {
		return errors.New("invalid init message type")
	}

	var init InitMessage
	if err := json.Unmarshal(data, &init); err != nil {
		return errors.Wrap(err, "failed to parse init message")
	}
	if init.AuthToken != server.options.Credential {
		return errors.New("authentication failed")
	}

	return nil
}

func (server *Server) proxyWS(ctx context.Context, dst *websocket.Conn, src *websocket.Conn) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		msgType, data, err := src.ReadMessage()
		if err != nil {
			return
		}
		if err := dst.WriteMessage(msgType, data); err != nil {
			return
		}
	}
}
