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
	slave         Slave
	status        *TerminalStatus
	probe         *ProbeManager
	broadcastCtrl *BroadcastController
	notifyFn      func(execID, status string) // notify Web clients
	replayFn      func(raw []byte)            // replay raw output to Web clients

	mu        sync.Mutex
	rawOutput chan []byte // active during execution
}

// NewExecManager creates a new ExecManager.
func NewExecManager(slave Slave, status *TerminalStatus, probe *ProbeManager, bc *BroadcastController, notifyFn func(string, string), replayFn func([]byte)) *ExecManager {
	return &ExecManager{
		slave:         slave,
		status:        status,
		probe:         probe,
		broadcastCtrl: bc,
		notifyFn:      notifyFn,
		replayFn:      replayFn,
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
//
// The command echo is broadcast to Web clients in real-time (broadcast stays ON).
// Once the echo line is fully received, broadcast is paused so the marker command
// and execution output are invisible. After completion (or timeout), the command
// output is replayed to Web clients and broadcast resumes.
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

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()

	// 5. Send user command (broadcast still ON — web sees echo in real-time)
	userCmd := req.Command + "\r"
	if _, err := em.slave.Write([]byte(userCmd)); err != nil {
		return nil, fmt.Errorf("failed to write command: %w", err)
	}

	// 6. Wait for the command echo line to complete, then pause broadcast.
	//    The echo sentinel is the user command text itself in the PTY output.
	if err := em.waitForEcho(outputCh, execCtx, req.Command); err != nil {
		return nil, fmt.Errorf("failed waiting for command echo: %w", err)
	}

	// 7. Pause broadcast — from now on, web clients see nothing until replay.
	em.broadcastCtrl.PauseFor(timeout + 10*time.Second)

	// 8. Send marker command (hidden from web)
	marker := fmt.Sprintf("<<<GOTTY_EXIT:%s:", execID)
	markerPattern := regexp.MustCompile(fmt.Sprintf(`<<<GOTTY_EXIT:%s:(\d+)>>>`, regexp.QuoteMeta(execID)))
	markerCmd := fmt.Sprintf("echo \"%s$?>>>\"\r", marker)
	if _, err := em.slave.Write([]byte(markerCmd)); err != nil {
		em.broadcastCtrl.Resume()
		return nil, fmt.Errorf("failed to write marker command: %w", err)
	}

	// 9. Collect output until marker detected
	var buf []byte
	for {
		select {
		case data, ok := <-outputCh:
			if !ok {
				em.broadcastCtrl.Resume()
				return nil, fmt.Errorf("output channel closed")
			}
			buf = append(buf, data...)

			if loc := markerPattern.FindSubmatchIndex(buf); loc != nil {
				exitCodeStr := string(buf[loc[2]:loc[3]])
				exitCode, _ := strconv.Atoi(exitCodeStr)
				duration := time.Since(startTime).Milliseconds()
				output := extractOutput(buf, marker)

				// Replay command output to Web clients.
				if em.replayFn != nil {
					replayData := buildReplayOutput(buf, marker)
					if len(replayData) > 0 {
						em.replayFn(replayData)
					}
				}

				em.broadcastCtrl.Resume()
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
			// Send Ctrl+C to interrupt, then drain remaining output
			em.slave.Write([]byte("\x03"))
			drainBuf := em.drainOutput(outputCh, markerPattern, 3*time.Second)

			// Replay whatever output was captured to Web clients.
			if em.replayFn != nil {
				allBuf := append(buf, drainBuf...)
				replayData := buildReplayOutput(allBuf, marker)
				if len(replayData) > 0 {
					em.replayFn(replayData)
				}
			}

			em.broadcastCtrl.Resume()
			allBuf := append(buf, drainBuf...)
			return &ExecResult{
				ExecID:     execID,
				Command:    req.Command,
				ExitCode:   -1,
				Output:     extractOutput(allBuf, marker),
				DurationMs: time.Since(startTime).Milliseconds(),
				TimedOut:   true,
			}, nil
		}
	}
}

// ExecuteStream runs a command and streams output via a channel.
//
// The command echo is broadcast in real-time; execution output is paused
// and replayed after completion.
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

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()

	// 6. Send user command (broadcast still ON)
	userCmd := req.Command + "\r"
	if _, err := em.slave.Write([]byte(userCmd)); err != nil {
		return fmt.Errorf("failed to write command: %w", err)
	}

	// 7. Wait for echo, then pause
	if err := em.waitForEcho(outputCh, execCtx, req.Command); err != nil {
		return fmt.Errorf("failed waiting for command echo: %w", err)
	}

	em.broadcastCtrl.PauseFor(timeout + 10*time.Second)

	// 8. Send marker command (hidden)
	marker := fmt.Sprintf("<<<GOTTY_EXIT:%s:", execID)
	markerPattern := regexp.MustCompile(fmt.Sprintf(`<<<GOTTY_EXIT:%s:(\d+)>>>`, regexp.QuoteMeta(execID)))
	markerCmd := fmt.Sprintf("echo \"%s$?>>>\"\r", marker)
	if _, err := em.slave.Write([]byte(markerCmd)); err != nil {
		em.broadcastCtrl.Resume()
		return fmt.Errorf("failed to write marker command: %w", err)
	}

	// 9. Collect and stream output
	var buf []byte
	for {
		select {
		case data, ok := <-outputCh:
			if !ok {
				em.broadcastCtrl.Resume()
				return fmt.Errorf("output channel closed")
			}
			buf = append(buf, data...)

			if loc := markerPattern.FindSubmatchIndex(buf); loc != nil {
				exitCodeStr := string(buf[loc[2]:loc[3]])
				exitCode, _ := strconv.Atoi(exitCodeStr)
				duration := time.Since(startTime).Milliseconds()

				// Replay command output to Web clients.
				if em.replayFn != nil {
					replayData := buildReplayOutput(buf, marker)
					if len(replayData) > 0 {
						em.replayFn(replayData)
					}
				}

				em.broadcastCtrl.Resume()
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

			// Stream output to SSE client
			eventCh <- OutputEvent{
				Type:    "output",
				Content: string(data),
			}

		case <-execCtx.Done():
			// Send Ctrl+C to interrupt, then drain remaining output
			em.slave.Write([]byte("\x03"))
			drainBuf := em.drainOutput(outputCh, markerPattern, 3*time.Second)

			// Replay whatever output was captured to Web clients.
			if em.replayFn != nil {
				allBuf := append(buf, drainBuf...)
				replayData := buildReplayOutput(allBuf, marker)
				if len(replayData) > 0 {
					em.replayFn(replayData)
				}
			}

			em.broadcastCtrl.Resume()
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

// drainOutput reads and discards remaining PTY output until the marker is seen
// or the deadline expires. Returns collected bytes for potential replay.
func (em *ExecManager) drainOutput(outputCh <-chan []byte, markerPattern *regexp.Regexp, deadline time.Duration) []byte {
	timer := time.After(deadline)
	var buf []byte
	for {
		select {
		case data, ok := <-outputCh:
			if !ok {
				return buf
			}
			buf = append(buf, data...)
			if markerPattern.Find(buf) != nil {
				return buf
			}
		case <-timer:
			return buf
		}
	}
}

// waitForEcho reads from the output channel until the command echo is fully
// received (i.e. the command text followed by a newline). Data consumed here
// is still broadcast to Web clients because the broadcast controller is not
// yet paused.
func (em *ExecManager) waitForEcho(outputCh <-chan []byte, ctx context.Context, command string) error {
	needle := []byte(command)
	var buf []byte
	for {
		select {
		case data, ok := <-outputCh:
			if !ok {
				return fmt.Errorf("output channel closed while waiting for echo")
			}
			buf = append(buf, data...)
			// Look for the command text followed by a newline (echo complete).
			idx := bytes.Index(buf, needle)
			if idx >= 0 {
				nlIdx := bytes.IndexByte(buf[idx:], '\n')
				if nlIdx >= 0 {
					return nil
				}
			}
		case <-ctx.Done():
			return ctx.Err()
		}
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

// buildReplayOutput extracts raw PTY bytes to replay to Web clients after an API exec.
// Since the command echo was already broadcast in real-time, this strips ALL lines
// containing <<<GOTTY_EXIT: (line-discipline echo, bash readline re-echo, and marker
// result) and returns the remaining raw bytes verbatim. This handles the case where
// bash produces multiple occurrences of the marker text in the output.
func buildReplayOutput(buf []byte, marker string) []byte {
	return stripMarkerLines(buf, []byte("<<<GOTTY_EXIT:"))
}

// stripMarkerLines removes any lines containing the needle from raw PTY data,
// preserving all other bytes (including ANSI escapes, \r\n, etc.) verbatim.
func stripMarkerLines(data []byte, needle []byte) []byte {
	lines := bytes.Split(data, []byte("\n"))
	var result []byte
	first := true
	for _, line := range lines {
		if bytes.Contains(line, needle) {
			continue
		}
		if !first {
			result = append(result, '\n')
		}
		result = append(result, line...)
		first = false
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
