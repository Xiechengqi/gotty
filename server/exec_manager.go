package server

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
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
		timer := time.NewTimer(250 * time.Millisecond)
		defer timer.Stop()
		select {
		case em.rawOutput <- cp:
		case <-timer.C:
			log.Printf("[API Exec] WARNING: output tap blocked, data dropped after wait (%d bytes)", len(data))
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
	Type    string `json:"type"`
	Content string `json:"content,omitempty"`
	ExecResult
}

func (em *ExecManager) Execute(ctx context.Context, req ExecRequest, defaultTimeout int) (*ExecResult, error) {
	execID := "exec_" + randomstring.Generate(8)
	timeout := time.Duration(defaultTimeout) * time.Second
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	ok, reason := em.status.TryAcquireAPI(execID)
	if !ok {
		return nil, &ExecError{Code: "TERMINAL_BUSY", Message: reason}
	}
	defer em.status.ReleaseAPI(execID)

	if em.notifyFn != nil {
		em.notifyFn(execID, "api_exec_start")
		defer em.notifyFn(execID, "api_exec_end")
	}

	if err := em.probe.Probe(); err != nil {
		return nil, &ExecError{Code: "PROBE_FAILED", Message: err.Error()}
	}

	outputCh := em.enableOutputTap()
	defer em.disableOutputTap()

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()
	startMarker := []byte(fmt.Sprintf("<<<GOTTY_START:%s>>>", execID))
	exitPrefix := []byte(fmt.Sprintf("<<<GOTTY_EXIT:%s:", execID))
	markerPattern := regexp.MustCompile(fmt.Sprintf(`<<<GOTTY_EXIT:%s:(\d+)>>>`, regexp.QuoteMeta(execID)))

	em.broadcastCtrl.PauseFor(timeout + 10*time.Second)
	if err := em.writeFramedCommands(req.Command, string(startMarker), string(exitPrefix)); err != nil {
		em.broadcastCtrl.Resume()
		return nil, fmt.Errorf("failed to write framed command sequence: %w", err)
	}

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
				transcript := extractFramedOutput(buf, startMarker, exitPrefix, true)

				if em.replayFn != nil {
					replayData := buildReplayOutput(transcript)
					if len(replayData) > 0 {
						em.replayFn(concatBytes(apiPrefixSequence(), replayData))
					}
				}

				em.broadcastCtrl.Resume()
				log.Printf("[API Exec] Completed: id=%s exit_code=%d duration=%dms", execID, exitCode, duration)
				return &ExecResult{
					ExecID:     execID,
					Command:    req.Command,
					ExitCode:   exitCode,
					Output:     strings.TrimRight(string(transcript), "\r\n"),
					DurationMs: duration,
				}, nil
			}
		case <-execCtx.Done():
			log.Printf("[API Exec] Timeout: id=%s timeout=%v", execID, timeout)
			_, _ = em.slave.Write([]byte("\x03"))
			drainBuf := em.drainOutput(outputCh, markerPattern, 3*time.Second)
			allBuf := concatBytes(buf, drainBuf)
			if loc := markerPattern.FindSubmatchIndex(allBuf); loc != nil {
				exitCodeStr := string(allBuf[loc[2]:loc[3]])
				exitCode, _ := strconv.Atoi(exitCodeStr)
				duration := time.Since(startTime).Milliseconds()
				transcript := extractFramedOutput(allBuf, startMarker, exitPrefix, true)

				if em.replayFn != nil {
					replayData := buildReplayOutput(transcript)
					if len(replayData) > 0 {
						em.replayFn(concatBytes(apiPrefixSequence(), replayData))
					}
				}

				em.broadcastCtrl.Resume()
				log.Printf("[API Exec] Completed at timeout edge: id=%s exit_code=%d duration=%dms", execID, exitCode, duration)
				return &ExecResult{
					ExecID:     execID,
					Command:    req.Command,
					ExitCode:   exitCode,
					Output:     strings.TrimRight(string(transcript), "\r\n"),
					DurationMs: duration,
				}, nil
			}

			partial := extractFramedOutput(allBuf, startMarker, exitPrefix, true)
			if em.replayFn != nil {
				replayData := buildReplayOutput(partial)
				if len(replayData) > 0 {
					em.replayFn(concatBytes(apiPrefixSequence(), replayData))
				}
			}

			em.broadcastCtrl.Resume()
			_, _ = em.slave.Write([]byte("\r"))

			return &ExecResult{
				ExecID:     execID,
				Command:    req.Command,
				ExitCode:   -1,
				Output:     strings.TrimRight(string(partial), "\r\n"),
				DurationMs: time.Since(startTime).Milliseconds(),
				TimedOut:   true,
			}, nil
		}
	}
}

func (em *ExecManager) ExecuteStream(ctx context.Context, req ExecRequest, defaultTimeout int, eventCh chan<- OutputEvent) error {
	execID := "exec_" + randomstring.Generate(8)
	timeout := time.Duration(defaultTimeout) * time.Second
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	sendEvent := func(ev OutputEvent) bool {
		select {
		case eventCh <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}

	ok, reason := em.status.TryAcquireAPI(execID)
	if !ok {
		return &ExecError{Code: "TERMINAL_BUSY", Message: reason}
	}
	defer em.status.ReleaseAPI(execID)

	if em.notifyFn != nil {
		em.notifyFn(execID, "api_exec_start")
		defer em.notifyFn(execID, "api_exec_end")
	}

	if err := em.probe.Probe(); err != nil {
		return &ExecError{Code: "PROBE_FAILED", Message: err.Error()}
	}

	outputCh := em.enableOutputTap()
	defer em.disableOutputTap()

	if !sendEvent(OutputEvent{Type: "started", ExecResult: ExecResult{ExecID: execID, Command: req.Command}}) {
		return ctx.Err()
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	startTime := time.Now()
	startMarker := []byte(fmt.Sprintf("<<<GOTTY_START:%s>>>", execID))
	exitPrefix := []byte(fmt.Sprintf("<<<GOTTY_EXIT:%s:", execID))
	markerPattern := regexp.MustCompile(fmt.Sprintf(`<<<GOTTY_EXIT:%s:(\d+)>>>`, regexp.QuoteMeta(execID)))

	em.broadcastCtrl.PauseFor(timeout + 10*time.Second)
	if err := em.writeFramedCommands(req.Command, string(startMarker), string(exitPrefix)); err != nil {
		em.broadcastCtrl.Resume()
		return fmt.Errorf("failed to write framed command sequence: %w", err)
	}

	var buf []byte
	sentLen := 0
	for {
		select {
		case data, ok := <-outputCh:
			if !ok {
				em.broadcastCtrl.Resume()
				return fmt.Errorf("output channel closed")
			}
			buf = append(buf, data...)

			transcript := extractFramedOutput(buf, startMarker, exitPrefix, true)
			if len(transcript) > sentLen {
				if !sendEvent(OutputEvent{Type: "output", Content: string(transcript[sentLen:])}) {
					return ctx.Err()
				}
				sentLen = len(transcript)
			}

			if loc := markerPattern.FindSubmatchIndex(buf); loc != nil {
				exitCodeStr := string(buf[loc[2]:loc[3]])
				exitCode, _ := strconv.Atoi(exitCodeStr)
				duration := time.Since(startTime).Milliseconds()
				transcript = extractFramedOutput(buf, startMarker, exitPrefix, true)

				if em.replayFn != nil {
					replayData := buildReplayOutput(transcript)
					if len(replayData) > 0 {
						em.replayFn(concatBytes(apiPrefixSequence(), replayData))
					}
				}

				em.broadcastCtrl.Resume()
				sendEvent(OutputEvent{Type: "completed", ExecResult: ExecResult{ExecID: execID, ExitCode: exitCode, DurationMs: duration}})
				log.Printf("[API Exec Stream] Completed: id=%s exit_code=%d duration=%dms", execID, exitCode, duration)
				return nil
			}
		case <-execCtx.Done():
			_, _ = em.slave.Write([]byte("\x03"))
			drainBuf := em.drainOutput(outputCh, markerPattern, 3*time.Second)
			allBuf := concatBytes(buf, drainBuf)
			transcript := extractFramedOutput(allBuf, startMarker, exitPrefix, true)
			if len(transcript) > sentLen {
				if !sendEvent(OutputEvent{Type: "output", Content: string(transcript[sentLen:])}) {
					return ctx.Err()
				}
				sentLen = len(transcript)
			}
			if loc := markerPattern.FindSubmatchIndex(allBuf); loc != nil {
				exitCodeStr := string(allBuf[loc[2]:loc[3]])
				exitCode, _ := strconv.Atoi(exitCodeStr)
				duration := time.Since(startTime).Milliseconds()

				if em.replayFn != nil {
					replayData := buildReplayOutput(transcript)
					if len(replayData) > 0 {
						em.replayFn(concatBytes(apiPrefixSequence(), replayData))
					}
				}

				em.broadcastCtrl.Resume()
				sendEvent(OutputEvent{Type: "completed", ExecResult: ExecResult{ExecID: execID, ExitCode: exitCode, DurationMs: duration}})
				return nil
			}

			if em.replayFn != nil {
				replayData := buildReplayOutput(transcript)
				if len(replayData) > 0 {
					em.replayFn(concatBytes(apiPrefixSequence(), replayData))
				}
			}

			em.broadcastCtrl.Resume()
			_, _ = em.slave.Write([]byte("\r"))
			sendEvent(OutputEvent{Type: "completed", ExecResult: ExecResult{ExecID: execID, ExitCode: -1, DurationMs: time.Since(startTime).Milliseconds(), TimedOut: true}})
			return nil
		}
	}
}

func (em *ExecManager) enableOutputTap() chan []byte {
	em.mu.Lock()
	defer em.mu.Unlock()
	em.rawOutput = make(chan []byte, 4096)
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

func (em *ExecManager) writeFramedCommands(command, startMarker, exitPrefix string) error {
	startCmd := fmt.Sprintf(" printf '\n%s\n'\r", startMarker)
	endCmd := fmt.Sprintf(` printf '\n%s%%s>>>\n' "$?"\r`, exitPrefix)
	for _, payload := range []string{startCmd, command + "\r", endCmd} {
		if _, err := em.slave.Write([]byte(payload)); err != nil {
			return err
		}
	}
	return nil
}

func extractFramedOutput(buf, startMarker, exitPrefix []byte, allowPartial bool) []byte {
	startIdx := bytes.Index(buf, startMarker)
	if startIdx < 0 {
		return nil
	}

	lineEndRel := bytes.IndexByte(buf[startIdx:], '\n')
	if lineEndRel < 0 {
		return nil
	}
	segmentStart := startIdx + lineEndRel + 1
	segmentEnd := len(buf)

	endIdxRel := bytes.Index(buf[segmentStart:], exitPrefix)
	if endIdxRel >= 0 {
		endIdx := segmentStart + endIdxRel
		if prevNL := bytes.LastIndexByte(buf[:endIdx], '\n'); prevNL >= segmentStart-1 {
			segmentEnd = prevNL + 1
		} else {
			segmentEnd = endIdx
		}
	} else if !allowPartial {
		return nil
	}

	if segmentStart > len(buf) || segmentStart > segmentEnd {
		return nil
	}

	segment := append([]byte(nil), buf[segmentStart:segmentEnd]...)
	segment = stripMarkerLines(segment, startMarker)
	segment = stripMarkerLines(segment, exitPrefix)
	return segment
}

func buildReplayOutput(buf []byte) []byte {
	return bytes.TrimRight(buf, "\r\n")
}

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

func apiPrefixSequence() []byte {
	return []byte("\x1b[s\r\x1b[6@\x1b[1;38;5;208m[API]\x1b[0m\x20\x1b[u\x1b[6C")
}

// ExecError represents an API execution error.
type ExecError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ExecError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func concatBytes(parts ...[]byte) []byte {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
