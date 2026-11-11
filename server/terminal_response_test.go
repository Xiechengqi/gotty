package server

import (
	"context"
	"testing"
	"time"
)

func TestOnlySizeOwnerAnswersSharedTerminalQueries(t *testing.T) {
	slave := &recordingSlave{}
	sm := NewSessionManager(context.Background(), slave, &Options{ResizePolicy: "leader", MinCols: 20, MaxCols: 240, MinRows: 5, MaxRows: 100})
	defer sm.stream.Close()
	owner := addResizeClient(sm, "owner", "owner-browser-12345", false)
	follower := addResizeClient(sm, "viewer", "viewer-browser-1234", false)
	sm.setLeaderLocked(owner)
	sm.clientSizes[owner.id] = resizeDimensions{Columns: 80, Rows: 24, Visible: true}
	sm.clientSizes[follower.id] = resizeDimensions{Columns: 80, Rows: 24, Visible: true}
	server := &Server{sessionManager: sm, options: &Options{PermitWrite: true}}
	before := time.Now().Add(-time.Minute)
	owner.lastActivity = before
	for _, reply := range []string{"\x1b[?1;2c", "\x1b[>0;276;0c", "\x1b[0n", "\x1b[24;80R", "\x1b[?24;80;1R", "\x1b[?1;2$y", "\x1b]11;rgb:1111/2222/3333\x1b\\", "\x1bP1$r0m\x1b\\"} {
		if terminalCursorResponse.MatchString(reply) {
			query := "\x1b[6n"
			if reply[2] == '?' {
				query = "\x1b[?6n"
			}
			// Split query parsing across output packets as a real PTY may do.
			sm.stream.Append(encodeOutputMessage([]byte(query[:3])), []byte(query[:3]))
			sm.stream.Append(encodeOutputMessage([]byte(query[3:])), []byte(query[3:]))
		}
		count := slave.writeCount()
		server.handleClientInput(follower, encodedInput(reply))
		if slave.writeCount() != count {
			t.Fatalf("watcher duplicated reply %q", reply)
		}
		server.handleClientInput(owner, encodedInput(reply))
		if slave.writeCount() != count+1 {
			t.Fatalf("owner reply was suppressed %q", reply)
		}
	}
	if !owner.lastActivity.Equal(before) {
		t.Fatal("automatic query replies marked user activity")
	}
	for _, input := range []string{"i", "中文", "\x1b[A", "\x1bf", "\x1b[1;2R", "\x1b[1;5R", "\x1b[200~paste\x1b[201~"} {
		count := slave.writeCount()
		server.handleClientInput(follower, encodedInput(input))
		if slave.writeCount() != count+1 {
			t.Fatalf("watcher keystroke blocked %q", input)
		}
	}
	sm.removeClientLocked(owner, time.Now())
	count := slave.writeCount()
	server.handleClientInput(follower, encodedInput("\x1b[?1;2c"))
	if slave.writeCount() != count+1 {
		t.Fatal("disconnected size owner prevented a live query reply")
	}
	if sm.leaderClientID != owner.id {
		t.Fatal("query responder stole the size lease")
	}
}
