package server

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/sorenisanerd/gotty/pkg/randomstring"
)

// ExecManager handles API command execution with marker-based completion detection.
type ExecManager struct {
	slave    Slave
	status   *TerminalStatus
	probe    *ProbeManager
	notifyFn func(execID, status string) // notify Web clients

	mu        sync.Mutex
	rawOutput chan []byte // active during execution
}

// NewExecManager creates a new ExecManager.
func NewExecManager(slave Slave, status *TerminalStatus, probe *ProbeManager, notifyFn func(string, string)) *ExecManager {
	return &ExecManager{
		slave:    slave,
		status:   status,
		probe:    probe,
		notifyFn: notifyFn,
	}
}

// FeedOutput is called by slave_reader to feed raw PTY output.
// The send is performed under the lock to prevent sending on a closed channel.
func (em *ExecManager) FeedOutput(data []byte) {
	em.mu.Lock()
	defer em.mu.Unlock()

	if em.rawOutput != nil {
		cp := make([]byte, len(data))
		copy(cp, data)
		select {
		case em.rawOutput <- cp:
		default:
		}
	}
}

// ExecRequest represents an API command execution request.
type ExecRequest struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"` // seconds, 0 = use default
}

// ExecResult represents the result of a command execution.
type ExecResult struct {
	ExecID     string `json:"exec_id"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Output     string `json:"output"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
}

// OutputEvent is sent during streaming execution.
type OutputEvent struct {
	Type    string `json:"type"`              // "output", "completed", "error"
	Content string `json:"content,omitempty"` // raw output chunk
	ExecResult
}

// Execute runs a command and waits for completion. Non-streaming.
func (em *ExecManager) Execute(ctx context.Context, req ExecRequest, defaultTimeout int) (*ExecResult, error) {
	execID := "exec_" + randomstring.Generate(8)
	timeout := time.Duration(defaultTimeout) * time.Second
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	// 1. Acquire lock
	ok, reason := em.status.TryAcquireAPI(execID)
	if !ok {
		return nil, &ExecError{Code: "TERMINAL_BUSY", Message: reason}
	}
	defer em.status.ReleaseAPI(execID)

	// 2. Notify Web clients
	if em.notifyFn != nil {
		em.notifyFn(execID, "api_exec_start")
		defer em.notifyFn(execID, "api_exec_end")
	}

	// 3. Probe shell environment (silent)
	if err := em.probe.Probe(); err != nil {
		return nil, &ExecError{Code: "PROBE_FAILED", Message: err.Error()}
	}

	// 4. Enable output tap
	outputCh := em.enableOutputTap()
	defer em.disableOutputTap()

	// 5. Send command with end marker
	marker := fmt.Sprintf("<<<GOTTY_EXIT:%s:", execID)
	fullCmd := fmt.Sprintf("%s; echo \"%s$?>>>\"", req.Command, marker)
	startTime := time.Now()

	if _, err := em.slave.Write([]byte(fullCmd + "\r")); err != nil {
		return nil, fmt.Errorf("failed to write command: %w", err)
	}

	// 6. Collect output until marker or timeout
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var buf []byte
	markerPattern := regexp.MustCompile(fmt.Sprintf(`<<<GOTTY_EXIT:%s:(\d+)>>>`, regexp.QuoteMeta(execID)))

	for {
		select {
		case data, ok := <-outputCh:
			if !ok {
				return nil, fmt.Errorf("output channel closed")
			}
			buf = append(buf, data...)

			if loc := markerPattern.FindSubmatchIndex(buf); loc != nil {
				exitCodeStr := string(buf[loc[2]:loc[3]])
				exitCode, _ := strconv.Atoi(exitCodeStr)
				duration := time.Since(startTime).Milliseconds()

				// Extract output: everything before the marker line
				output := extractOutput(buf, marker)

				log.Printf("[API Exec] Completed: id=%s exit_code=%d duration=%dms", execID, exitCode, duration)
				return &ExecResult{
					ExecID:     execID,
					Command:    req.Command,
					ExitCode:   exitCode,
					Output:     output,
					DurationMs: duration,
				}, nil
			}

		case <-execCtx.Done():
			log.Printf("[API Exec] Timeout: id=%s timeout=%v", execID, timeout)
			return &ExecResult{
				ExecID:     execID,
				Command:    req.Command,
				ExitCode:   -1,
				Output:     string(buf),
				DurationMs: time.Since(startTime).Milliseconds(),
				TimedOut:   true,
			}, nil
		}
	}
}

// ExecuteStream runs a command and streams output via a channel.
func (em *ExecManager) ExecuteStream(ctx context.Context, req ExecRequest, defaultTimeout int, eventCh chan<- OutputEvent) error {
	execID := "exec_" + randomstring.Generate(8)
	timeout := time.Duration(defaultTimeout) * time.Second
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	// 1. Acquire lock
	ok, reason := em.status.TryAcquireAPI(execID)
	if !ok {
		return &ExecError{Code: "TERMINAL_BUSY", Message: reason}
	}
	defer em.status.ReleaseAPI(execID)

	// 2. Notify Web clients
	if em.notifyFn != nil {
		em.notifyFn(execID, "api_exec_start")
		defer em.notifyFn(execID, "api_exec_end")
	}

	// 3. Probe
	if err := em.probe.Probe(); err != nil {
		return &ExecError{Code: "PROBE_FAILED", Message: err.Error()}
	}

	// 4. Enable output tap
	outputCh := em.enableOutputTap()
	defer em.disableOutputTap()

	// 5. Send started event
	eventCh <- OutputEvent{Type: "started", ExecResult: ExecResult{ExecID: execID, Command: req.Command}}

	// 6. Send command
	marker := fmt.Sprintf("<<<GOTTY_EXIT:%s:", execID)
	fullCmd := fmt.Sprintf("%s; echo \"%s$?>>>\"", req.Command, marker)
	startTime := time.Now()

	if _, err := em.slave.Write([]byte(fullCmd + "\r")); err != nil {
		return fmt.Errorf("failed to write command: %w", err)
	}

	// 7. Stream output
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var buf []byte
	markerPattern := regexp.MustCompile(fmt.Sprintf(`<<<GOTTY_EXIT:%s:(\d+)>>>`, regexp.QuoteMeta(execID)))

	for {
		select {
		case data, ok := <-outputCh:
			if !ok {
				return fmt.Errorf("output channel closed")
			}
			buf = append(buf, data...)

			// Check for marker BEFORE sending output event
			if loc := markerPattern.FindSubmatchIndex(buf); loc != nil {
				exitCodeStr := string(buf[loc[2]:loc[3]])
				exitCode, _ := strconv.Atoi(exitCodeStr)
				duration := time.Since(startTime).Milliseconds()

				// Send any output before the marker (strip marker from this chunk)
				cleanData := markerPattern.ReplaceAll(data, nil)
				if len(cleanData) > 0 {
					eventCh <- OutputEvent{
						Type:    "output",
						Content: string(cleanData),
					}
				}

				eventCh <- OutputEvent{
					Type: "completed",
					ExecResult: ExecResult{
						ExecID:     execID,
						ExitCode:   exitCode,
						DurationMs: duration,
					},
				}
				log.Printf("[API Exec Stream] Completed: id=%s exit_code=%d duration=%dms", execID, exitCode, duration)
				return nil
			}

			// No marker found yet, safe to send full chunk
			eventCh <- OutputEvent{
				Type:    "output",
				Content: string(data),
			}

		case <-execCtx.Done():
			eventCh <- OutputEvent{
				Type: "completed",
				ExecResult: ExecResult{
					ExecID:     execID,
					ExitCode:   -1,
					DurationMs: time.Since(startTime).Milliseconds(),
					TimedOut:   true,
				},
			}
			return nil
		}
	}
}

func (em *ExecManager) enableOutputTap() chan []byte {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.rawOutput = make(chan []byte, 256)
	return em.rawOutput
}

func (em *ExecManager) disableOutputTap() {
	em.mu.Lock()
	defer em.mu.Unlock()
	if em.rawOutput != nil {
		close(em.rawOutput)
		em.rawOutput = nil
	}
}

// extractOutput extracts command output, stripping the command echo and marker line.
func extractOutput(buf []byte, marker string) string {
	lines := bytes.Split(buf, []byte("\n"))
	var output []string
	started := false

	for _, line := range lines {
		lineStr := string(bytes.TrimRight(line, "\r"))

		// Skip lines containing the marker (both echo and result)
		if bytes.Contains(line, []byte(marker)) {
			started = true
			continue
		}

		// Skip lines containing the command echo (with full marker prefix)
		if bytes.Contains(line, []byte("<<<GOTTY_EXIT:")) {
			started = true
			continue
		}

		// Skip lines before the command echo (prompt, etc.)
		if !started {
			continue
		}

		output = append(output, lineStr)
	}

	// Join and trim trailing empty lines
	result := ""
	if len(output) > 0 {
		result = joinTrimmed(output)
	}
	return result
}

func joinTrimmed(lines []string) string {
	// Remove trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

// ExecError represents an API execution error.
type ExecError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
