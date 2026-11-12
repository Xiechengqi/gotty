package localcommand

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestBlockedPTYWriteCancelsAfterResize(t *testing.T) {
	command, err := New("sh", []string{"-c", "stty raw -echo; printf READY; sleep 30"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	command.closeTimeout = 100 * time.Millisecond
	t.Cleanup(func() { command.Close() })
	ready := make([]byte, 5)
	if _, err := io.ReadFull(command, ready); err != nil || string(ready) != "READY" {
		t.Fatal(string(ready), err)
	}
	if err := command.ResizeTerminal(120, 40); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	n, err := command.WriteContext(ctx, bytes.Repeat([]byte("x"), 1024*1024))
	if !errors.Is(err, context.DeadlineExceeded) || n >= 1024*1024 || time.Since(started) > time.Second {
		t.Fatalf("blocked write: n=%d err=%v elapsed=%v", n, err, time.Since(started))
	}
}
