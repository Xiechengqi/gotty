package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type pasteSlave struct {
	recordingSlave
	limit int
	block chan struct{}
	fail  bool
}

func (s *pasteSlave) WriteContext(ctx context.Context, data []byte) (int, error) {
	if bytes.HasPrefix(data, []byte("payload")) {
		if s.block != nil {
			close(s.block)
			<-ctx.Done()
			s.Write(data[:3])
			return 3, ctx.Err()
		}
		if s.fail {
			s.Write(data[:2])
			return 2, errors.New("disk/pty error")
		}
	}
	if s.limit > 0 && len(data) > s.limit {
		data = data[:s.limit]
	}
	return s.Write(data)
}
func (s *pasteSlave) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(bytes.Join(s.writes, nil))
}
func pasteServer(t *testing.T, slave *pasteSlave) (*Server, *Client) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	sm := NewSessionManager(ctx, slave, &Options{})
	c := addResizeClient(sm, "paste-client", "", false)
	status := NewTerminalStatus(10 * time.Millisecond)
	t.Cleanup(func() { cancel(); status.Stop(); sm.stream.Close() })
	return &Server{sessionManager: sm, terminalStatus: status, options: &Options{PermitWrite: true}}, c
}
func sendPaste(t *testing.T, server *Server, client *Client, req pasteRequest) {
	t.Helper()
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	server.handleClientInput(client, append([]byte{'5'}, data...))
}
func pasteReceipt(t *testing.T, client *Client) pasteResult {
	t.Helper()
	select {
	case wire := <-client.send:
		var result pasteResult
		if len(wire) < 2 || wire[0] != 'e' || json.Unmarshal(wire[1:], &result) != nil {
			t.Fatalf("bad receipt: %s", wire)
		}
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("paste receipt timed out")
		return pasteResult{}
	}
}
func TestPasteShortWritesUTF8AndOneBracketPair(t *testing.T) {
	slave := &pasteSlave{limit: 1}
	server, client := pasteServer(t, slave)
	parts := []string{"中文🙂\r", "second line"}
	size := len(parts[0]) + len(parts[1])
	for seq, part := range parts {
		sendPaste(t, server, client, pasteRequest{ID: "one", Seq: seq, Size: size, Bracketed: true, Data: base64.StdEncoding.EncodeToString([]byte(part)), Final: seq == 1})
		result := pasteReceipt(t, client)
		if result.Seq != seq || result.Error != "" || (seq == 0 && result.Received != len(part)) || (seq == 1 && (result.Status != "done" || result.Received != size)) {
			t.Fatal(result)
		}
	}
	if slave.text() != "\x1b[200~"+strings.Join(parts, "")+"\x1b[201~" {
		t.Fatalf("incorrect PTY bytes: %q", slave.text())
	}
}
func TestPasteCancelRemainsResponsiveAndClosesBracket(t *testing.T) {
	slave := &pasteSlave{block: make(chan struct{})}
	server, client := pasteServer(t, slave)
	sendPaste(t, server, client, pasteRequest{ID: "cancel", Size: 70, Bracketed: true, Data: base64.StdEncoding.EncodeToString([]byte("payload"))})
	select {
	case <-slave.block:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	// No global session mutex is held by the blocked PTY write.
	server.handleClientInput(client, []byte("2"))
	select {
	case data := <-client.send:
		if string(data) != "2" {
			t.Fatal(string(data))
		}
	case <-time.After(time.Second):
		t.Fatal("ping blocked")
	}
	time.Sleep(250 * time.Millisecond)
	if ok, _ := server.terminalStatus.TryAcquireAPI("during-paste"); ok {
		t.Fatal("idle checker allowed an API command during paste")
	}
	server.handleClientInput(client, encodedInput("must-not-interleave"))
	if result := pasteReceipt(t, client); result.Error != "Terminal is busy" {
		t.Fatal(result)
	}
	sendPaste(t, server, client, pasteRequest{ID: "cancel", Cancel: true})
	result := pasteReceipt(t, client)
	if result.Status != "cancelled" || result.Received != 3 {
		t.Fatal(result)
	}
	if slave.text() != "\x1b[200~pay\x1b[201~" {
		t.Fatalf("incorrect cancellation bytes: %q", slave.text())
	}
}
func TestPastePartialFailureIsNotSuccess(t *testing.T) {
	slave := &pasteSlave{fail: true}
	server, client := pasteServer(t, slave)
	sendPaste(t, server, client, pasteRequest{ID: "failure", Size: 7, Bracketed: true, Data: base64.StdEncoding.EncodeToString([]byte("payload")), Final: true})
	result := pasteReceipt(t, client)
	if result.Status != "error" || result.Received != 2 {
		t.Fatal(result)
	}
	if slave.text() != "\x1b[200~pa\x1b[201~" {
		t.Fatal(slave.text())
	}
}
func TestPastePermissionAndAPIBusy(t *testing.T) {
	for _, denied := range []string{"observer", "permit-write", "api"} {
		t.Run(denied, func(t *testing.T) {
			slave := &pasteSlave{}
			server, client := pasteServer(t, slave)
			if denied == "observer" {
				client.readOnly = true
			}
			if denied == "permit-write" {
				server.options.PermitWrite = false
			}
			if denied == "api" {
				if ok, _ := server.terminalStatus.TryAcquireAPI("busy"); !ok {
					t.Fatal("API setup")
				}
			}
			sendPaste(t, server, client, pasteRequest{ID: "denied", Size: 3, Data: "YWJj", Final: true})
			if result := pasteReceipt(t, client); result.Status != "error" || result.Received != 0 {
				t.Fatal(result)
			}
			if slave.text() != "" {
				t.Fatal("denied paste wrote to PTY")
			}
		})
	}
}
func TestPasteDisconnectDiscardsQueuedData(t *testing.T) {
	slave := &pasteSlave{block: make(chan struct{})}
	server, client := pasteServer(t, slave)
	sendPaste(t, server, client, pasteRequest{ID: "disconnect", Size: 14, Bracketed: true, Data: base64.StdEncoding.EncodeToString([]byte("payload"))})
	select {
	case <-slave.block:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	sendPaste(t, server, client, pasteRequest{ID: "disconnect", Seq: 1, Size: 14, Bracketed: true, Data: base64.StdEncoding.EncodeToString([]byte("payload")), Final: true})
	sm := server.sessionManager
	sm.mu.Lock()
	sm.removeClientLocked(client, time.Now())
	sm.mu.Unlock()
	deadline := time.Now().Add(time.Second)
	for {
		sm.mu.Lock()
		finished := sm.paste == nil
		sm.mu.Unlock()
		if finished {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected paste did not stop")
		}
		time.Sleep(time.Millisecond)
	}
	if slave.text() != "\x1b[200~pay\x1b[201~" {
		t.Fatal(slave.text())
	}
}

func TestPasteSequenceMismatchAndConcurrentClientCannotInterleave(t *testing.T) {
	slave := &pasteSlave{block: make(chan struct{})}
	server, client := pasteServer(t, slave)
	sendPaste(t, server, client, pasteRequest{ID: "sequence", Size: 14, Bracketed: true, Data: base64.StdEncoding.EncodeToString([]byte("payload"))})
	select {
	case <-slave.block:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	sm := server.sessionManager
	sm.mu.Lock()
	other := addResizeClient(sm, "other-paste-client", "", false)
	sm.mu.Unlock()
	sendPaste(t, server, other, pasteRequest{ID: "other", Size: 3, Data: "YWJj", Final: true})
	if result := pasteReceipt(t, other); result.Error != "Terminal is busy" {
		t.Fatal(result)
	}
	sendPaste(t, server, client, pasteRequest{ID: "sequence", Seq: 4, Size: 14, Bracketed: true, Data: base64.StdEncoding.EncodeToString([]byte("payload")), Final: true})
	if result := pasteReceipt(t, client); result.Status != "error" || result.Error != "Invalid paste request" {
		t.Fatal(result)
	}
	if slave.text() != "\x1b[200~pay\x1b[201~" {
		t.Fatal(slave.text())
	}
}

type pasteReplySlave struct {
	pasteSlave
	reply func()
}

func (s *pasteReplySlave) WriteContext(ctx context.Context, data []byte) (int, error) {
	n, err := s.pasteSlave.WriteContext(ctx, data)
	if string(data) == "\x1b[201~" {
		s.reply()
	}
	return n, err
}
func TestPasteFlushesReplyArrivingDuringClosingMarker(t *testing.T) {
	base := &pasteSlave{}
	server, client := pasteServer(t, base)
	slave := &pasteReplySlave{}
	sm := server.sessionManager
	sm.slave = slave
	sm.clientSizes[client.id] = resizeDimensions{Columns: 80, Rows: 24, Visible: true}
	slave.reply = func() { server.handleClientInput(client, encodedInput("\x1b[?1;2c")) }
	sendPaste(t, server, client, pasteRequest{ID: "reply-at-end", Size: 3, Bracketed: true, Data: "YWJj", Final: true})
	if result := pasteReceipt(t, client); result.Status != "done" {
		t.Fatal(result)
	}
	if slave.text() != "\x1b[200~abc\x1b[201~\x1b[?1;2c" {
		t.Fatalf("lost application reply: %q", slave.text())
	}
}
