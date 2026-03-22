package server

import "testing"

func TestTerminalStatusWriteAPIInputDoesNotMarkUserActive(t *testing.T) {
	ts := NewTerminalStatus(0)
	defer ts.Stop()

	called := false
	if ok := ts.WriteAPIInput(func() { called = true }); !ok {
		t.Fatalf("expected api input to be allowed")
	}
	if !called {
		t.Fatalf("expected write callback to run")
	}
	if state := ts.GetState(); state != StateIdle {
		t.Fatalf("expected state to remain idle, got %s", state.String())
	}
}

func TestTerminalStatusWriteUserInputMarksUserActive(t *testing.T) {
	ts := NewTerminalStatus(0)
	defer ts.Stop()

	if ok := ts.WriteUserInput(func() {}); !ok {
		t.Fatalf("expected user input to be allowed")
	}
	if state := ts.GetState(); state != StateUserActive {
		t.Fatalf("expected user_active, got %s", state.String())
	}
}
