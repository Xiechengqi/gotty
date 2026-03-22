package server

import (
	"fmt"
	"sync"
	"time"
)

// TerminalState represents the current state of the terminal.
type TerminalState int

const (
	StateIdle         TerminalState = iota // No activity
	StateUserActive                        // User is typing
	StateAPIExecuting                      // API command in progress
)

func (s TerminalState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateUserActive:
		return "user_active"
	case StateAPIExecuting:
		return "api_executing"
	default:
		return "unknown"
	}
}

// TerminalStatus manages terminal state with mutual exclusion.
type TerminalStatus struct {
	mu              sync.Mutex
	state           TerminalState
	lastUserInput   time.Time
	currentExecID   string
	userIdleTimeout time.Duration
	stopCh          chan struct{}
	stopOnce        sync.Once
}

// NewTerminalStatus creates a new TerminalStatus.
func NewTerminalStatus(userIdleTimeout time.Duration) *TerminalStatus {
	ts := &TerminalStatus{
		state:           StateIdle,
		userIdleTimeout: userIdleTimeout,
		stopCh:          make(chan struct{}),
	}
	go ts.idleChecker()
	return ts
}

// idleChecker periodically checks if user has gone idle.
func (ts *TerminalStatus) idleChecker() {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ts.stopCh:
			return
		case <-ticker.C:
			ts.mu.Lock()
			if ts.state == StateUserActive && !ts.lastUserInput.IsZero() {
				if time.Since(ts.lastUserInput) > ts.userIdleTimeout {
					ts.state = StateIdle
				}
			}
			ts.mu.Unlock()
		}
	}
}

// Stop stops the idle checker goroutine. Safe to call multiple times.
func (ts *TerminalStatus) Stop() {
	ts.stopOnce.Do(func() {
		close(ts.stopCh)
	})
}

// TryAcquireAPI attempts to acquire the terminal for API execution.
// Returns (true, "") on success, or (false, reason) on failure.
func (ts *TerminalStatus) TryAcquireAPI(execID string) (bool, string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	switch ts.state {
	case StateIdle:
		ts.state = StateAPIExecuting
		ts.currentExecID = execID
		return true, ""
	case StateUserActive:
		idle := time.Since(ts.lastUserInput)
		return false, fmt.Sprintf("terminal is in use by user (last input %v ago)", idle.Round(time.Millisecond))
	case StateAPIExecuting:
		return false, fmt.Sprintf("another API execution in progress (exec_id: %s)", ts.currentExecID)
	default:
		return false, "unknown terminal state"
	}
}

// ReleaseAPI releases the terminal from API execution.
func (ts *TerminalStatus) ReleaseAPI(execID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.state == StateAPIExecuting && ts.currentExecID == execID {
		ts.state = StateIdle
		ts.currentExecID = ""
	}
}

// WriteUserInput atomically checks that the terminal is not in API execution
// state, updates user activity, and calls writeFn outside the lock.
// Returns false if API is executing (writeFn is not called).
func (ts *TerminalStatus) WriteUserInput(writeFn func()) bool {
	ts.mu.Lock()
	if ts.state == StateAPIExecuting {
		ts.mu.Unlock()
		return false
	}
	ts.state = StateUserActive
	ts.lastUserInput = time.Now()
	ts.mu.Unlock()

	writeFn()
	return true
}

// WriteAPIInput allows API-triggered input to be written as long as no API
// execution is currently running, without marking the terminal as user-active.
func (ts *TerminalStatus) WriteAPIInput(writeFn func()) bool {
	ts.mu.Lock()
	if ts.state == StateAPIExecuting {
		ts.mu.Unlock()
		return false
	}
	ts.mu.Unlock()

	writeFn()
	return true
}

// GetState returns the current terminal state.
func (ts *TerminalStatus) GetState() TerminalState {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.state
}

// GetStatus returns a snapshot of the terminal status.
func (ts *TerminalStatus) GetStatus() map[string]interface{} {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	status := map[string]interface{}{
		"state": ts.state.String(),
	}
	if !ts.lastUserInput.IsZero() {
		status["last_user_input"] = ts.lastUserInput.Format(time.RFC3339)
		status["idle_ms"] = time.Since(ts.lastUserInput).Milliseconds()
	}
	if ts.currentExecID != "" {
		status["current_exec_id"] = ts.currentExecID
	}
	return status
}
