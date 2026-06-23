package server

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func withWebSocketShutdown(ctx context.Context, gracefulCtx context.Context, conn *websocket.Conn) (context.Context, context.CancelFunc) {
	connCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	var stopOnce sync.Once

	go func() {
		select {
		case <-gracefulCtx.Done():
			cancel()
			closeWebSocket(conn, websocket.CloseGoingAway, "server shutting down")
		case <-ctx.Done():
			cancel()
			closeWebSocket(conn, websocket.CloseGoingAway, "server shutting down")
		case <-stopped:
		}
	}()

	stop := func() {
		stopOnce.Do(func() {
			cancel()
			close(stopped)
		})
	}

	return connCtx, stop
}

func closeWebSocket(conn *websocket.Conn, code int, text string) {
	deadline := time.Now().Add(time.Second)
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(code, text),
		deadline,
	)
	_ = conn.SetReadDeadline(time.Now())
	_ = conn.Close()
}
