package server

import (
	"bytes"
	"fmt"
	"log"
	"time"

	"github.com/sorenisanerd/gotty/pkg/randomstring"
)

// ProbeManager verifies the terminal is in a shell environment
// before executing API commands. The probe is invisible to Web clients.
type ProbeManager struct {
	slave         Slave
	broadcastCtrl *BroadcastController
	timeout       time.Duration
}

// NewProbeManager creates a new ProbeManager.
func NewProbeManager(slave Slave, bc *BroadcastController, timeout time.Duration) *ProbeManager {
	return &ProbeManager{
		slave:         slave,
		broadcastCtrl: bc,
		timeout:       timeout,
	}
}

// Probe sends a test command and verifies the shell responds correctly.
// Returns nil if the terminal is in a shell environment.
// The entire probe is invisible to Web clients (broadcast is paused).
func (pm *ProbeManager) Probe() error {
	probeID := "probe_" + randomstring.Generate(8)
	marker := fmt.Sprintf("<<<GOTTY_PROBE:%s>>>", probeID)
	probeCmd := fmt.Sprintf(" printf '\n%s\n'\r", marker)

	// 1. Pause broadcast — Web clients see nothing
	pm.broadcastCtrl.Pause()
	defer pm.broadcastCtrl.Resume()

	internal := pm.broadcastCtrl.Internal()
	if internal == nil {
		return fmt.Errorf("failed to get internal channel")
	}

	// 2. Send probe command to PTY
	if _, err := pm.slave.Write([]byte(probeCmd)); err != nil {
		return fmt.Errorf("failed to write probe command: %w", err)
	}

	// 3. Wait for expected response
	deadline := time.After(pm.timeout)
	var buf []byte

	for {
		select {
		case data, ok := <-internal:
			if !ok {
				return fmt.Errorf("internal channel closed unexpectedly")
			}
			buf = append(buf, data...)
			if bytes.Contains(buf, []byte(marker)) {
				log.Printf("[Probe] Shell environment confirmed (id=%s, %d bytes read)", probeID, len(buf))
				// 4. Clean up: wait briefly for prompt to settle
				pm.drainAndClean(internal)
				return nil
			}
		case <-deadline:
			return fmt.Errorf("probe timeout (%v) — terminal may be in an interactive application (vim, less, etc.)", pm.timeout)
		}
	}
}

// drainAndClean drains remaining output and sends a clean-line sequence.
func (pm *ProbeManager) drainAndClean(internal <-chan []byte) {
	// Drain any remaining output for a short window
	drainDeadline := time.After(100 * time.Millisecond)
	for {
		select {
		case _, ok := <-internal:
			if !ok {
				return
			}
		case <-drainDeadline:
			return
		}
	}
}
