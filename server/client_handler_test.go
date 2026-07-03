package server

import (
	"encoding/base64"
	"io"
	"sync"
	"testing"
)

type recordingSlave struct {
	mu     sync.Mutex
	writes [][]byte
}

func (s *recordingSlave) Read(p []byte) (int, error) { return 0, io.EOF }
func (s *recordingSlave) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, append([]byte(nil), p...))
	return len(p), nil
}
func (s *recordingSlave) Close() error                                 { return nil }
func (s *recordingSlave) WindowTitleVariables() map[string]interface{} { return nil }
func (s *recordingSlave) ResizeTerminal(width int, height int) error   { return nil }
func (s *recordingSlave) GetWorkingDir() (string, error)               { return "", nil }
func (s *recordingSlave) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writes)
}

func TestHandleClientInputRequiresPermitWrite(t *testing.T) {
	slave := &recordingSlave{}
	server := &Server{
		options:        &Options{PermitWrite: false},
		sessionManager: &SessionManager{slave: slave},
	}

	server.handleClientInput(&Client{}, encodedInput("whoami\n"))

	if slave.writeCount() != 0 {
		t.Fatalf("write count = %d, want 0", slave.writeCount())
	}
}

func TestHandleClientInputReadOnlyBlocksInput(t *testing.T) {
	slave := &recordingSlave{}
	server := &Server{
		options:        &Options{PermitWrite: true},
		sessionManager: &SessionManager{slave: slave},
	}

	server.handleClientInput(&Client{readOnly: true}, encodedInput("whoami\n"))

	if slave.writeCount() != 0 {
		t.Fatalf("write count = %d, want 0", slave.writeCount())
	}
}

func TestHandleClientInputReadOnlyAllowsPing(t *testing.T) {
	client := &Client{readOnly: true, send: make(chan []byte, 1)}
	server := &Server{options: &Options{PermitWrite: true}}

	server.handleClientInput(client, []byte{'2'})

	select {
	case got := <-client.send:
		if string(got) != "2" {
			t.Fatalf("pong = %q, want 2", got)
		}
	default:
		t.Fatal("expected pong for read-only ping")
	}
}

func encodedInput(value string) []byte {
	return append([]byte{'1'}, []byte(base64.StdEncoding.EncodeToString([]byte(value)))...)
}
