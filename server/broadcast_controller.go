package server

import (
	"sync"
	"time"
)

// BroadcastController controls whether PTY output is broadcast to Web clients.
// During probe, broadcasting is paused so probe commands are invisible to users.
type BroadcastController struct {
	mu       sync.Mutex
	paused   bool
	internal chan []byte
	maxPause time.Duration
	timer    *time.Timer
}

// NewBroadcastController creates a new BroadcastController.
func NewBroadcastController() *BroadcastController {
	return &BroadcastController{
		maxPause: 2 * time.Second,
	}
}

// Pause stops broadcasting to Web clients. PTY output during pause
// is redirected to the internal channel for probe validation.
func (bc *BroadcastController) Pause() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if bc.paused {
		return
	}
	bc.paused = true
	bc.internal = make(chan []byte, 64)

	// Safety: force resume after maxPause
	bc.timer = time.AfterFunc(bc.maxPause, func() {
		bc.Resume()
	})
}

// Resume restores broadcasting to Web clients. Idempotent.
func (bc *BroadcastController) Resume() {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if !bc.paused {
		return
	}
	bc.paused = false
	if bc.timer != nil {
		bc.timer.Stop()
		bc.timer = nil
	}
	if bc.internal != nil {
		close(bc.internal)
		bc.internal = nil
	}
}

// IsPaused returns whether broadcasting is currently paused.
func (bc *BroadcastController) IsPaused() bool {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.paused
}

// HandleOutput decides what to do with PTY output.
// Returns true if the data should be broadcast to Web clients.
// When paused, data is sent to the internal channel instead.
func (bc *BroadcastController) HandleOutput(raw []byte) bool {
	bc.mu.Lock()
	defer bc.mu.Unlock()

	if !bc.paused {
		return true
	}

	// Send raw data to internal channel for probe listener
	if bc.internal != nil {
		cp := make([]byte, len(raw))
		copy(cp, raw)
		select {
		case bc.internal <- cp:
		default:
			// channel full, drop
		}
	}
	return false
}

// Internal returns the internal channel for reading probe responses.
// Only valid while paused.
func (bc *BroadcastController) Internal() <-chan []byte {
	bc.mu.Lock()
	defer bc.mu.Unlock()
	return bc.internal
}
