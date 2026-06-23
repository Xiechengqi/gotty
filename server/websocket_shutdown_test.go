package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	noesctmpl "text/template"
	"time"

	"github.com/gorilla/websocket"
)

type websocketShutdownTestSlave struct{}

func (s websocketShutdownTestSlave) Read(p []byte) (int, error) {
	select {}
}

func (s websocketShutdownTestSlave) Write(p []byte) (int, error) {
	return len(p), nil
}

func (s websocketShutdownTestSlave) Close() error {
	return nil
}

func (s websocketShutdownTestSlave) WindowTitleVariables() map[string]interface{} {
	return map[string]interface{}{}
}

func (s websocketShutdownTestSlave) ResizeTerminal(width int, height int) error {
	return nil
}

func (s websocketShutdownTestSlave) GetWorkingDir() (string, error) {
	return "", nil
}

func TestGracefulShutdownClosesRegisteredWebSocket(t *testing.T) {
	server, ctx, cancel, gracefullCtx, gracefullCancel, counter := newWebSocketShutdownTestServer(t)
	defer cancel()
	go server.sessionManager.Run()

	ts := httptest.NewServer(server.generateHandleWS(ctx, gracefullCtx, cancel, counter))
	defer ts.Close()

	conn := dialTestWebSocket(t, ts.URL)
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"AuthToken":"token"}`)); err != nil {
		t.Fatalf("failed to send init message: %v", err)
	}

	waitForCondition(t, func() bool {
		return server.sessionManager.GetClientCount() == 1
	}, "client registration")

	gracefullCancel()

	waitForCondition(t, func() bool {
		return counter.count() == 0 && server.sessionManager.GetClientCount() == 0
	}, "websocket shutdown")
}

func TestGracefulShutdownClosesPreAuthWebSocket(t *testing.T) {
	server, ctx, cancel, gracefullCtx, gracefullCancel, counter := newWebSocketShutdownTestServer(t)
	defer cancel()
	go server.sessionManager.Run()

	ts := httptest.NewServer(server.generateHandleWS(ctx, gracefullCtx, cancel, counter))
	defer ts.Close()

	conn := dialTestWebSocket(t, ts.URL)
	defer conn.Close()

	waitForCondition(t, func() bool {
		return counter.count() == 1
	}, "pre-auth connection")

	gracefullCancel()

	waitForCondition(t, func() bool {
		return counter.count() == 0
	}, "pre-auth websocket shutdown")
}

func newWebSocketShutdownTestServer(t *testing.T) (*Server, context.Context, context.CancelFunc, context.Context, context.CancelFunc, *counter) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	gracefullCtx, gracefullCancel := context.WithCancel(context.Background())
	slave := websocketShutdownTestSlave{}
	options := &Options{
		Credential:    "token",
		MaxConnection: 0,
		ResizePolicy:  "fixed",
		MinCols:       1,
		MaxCols:       240,
		MinRows:       1,
		MaxRows:       80,
	}

	titleTemplate, err := noesctmpl.New("title").Parse("test")
	if err != nil {
		t.Fatalf("failed to parse title template: %v", err)
	}

	server := &Server{
		options: options,
		upgrader: &websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			Subprotocols:    []string{},
		},
		titleTemplate: titleTemplate,
	}
	server.sessionManager = NewSessionManager(ctx, slave, options)

	return server, ctx, cancel, gracefullCtx, gracefullCancel, newCounter(0)
}

func dialTestWebSocket(t *testing.T, url string) *websocket.Conn {
	t.Helper()

	wsURL := "ws" + strings.TrimPrefix(url, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("failed to dial websocket: %v", err)
	}
	return conn
}

func waitForCondition(t *testing.T, condition func() bool, label string) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}
