package server

import (
	"log"
	"sort"
	"strings"
	"time"
)

type resizeEvent struct {
	client  *Client
	request clientResizeRequest
}

func validClientKey(key string) bool {
	if len(key) < 16 || len(key) > 128 {
		return false
	}
	return strings.IndexFunc(key, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
	}) == -1
}

func (sm *SessionManager) HandleClientResize(client *Client, req clientResizeRequest) {
	if client == nil || client.readOnly || req.Columns <= 0 || req.Rows <= 0 {
		return
	}
	select {
	case sm.resizeEvents <- resizeEvent{client, req}:
	case <-sm.ctx.Done():
	}
}

func (sm *SessionManager) NoteClientActivity(client *Client) {
	if client == nil {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.clients[client] {
		client.lastActivity = time.Now()
	}
}

func (sm *SessionManager) leaderConnectedLocked() bool {
	for c := range sm.clients {
		if c.id == sm.leaderClientID {
			return true
		}
	}
	return false
}

func (sm *SessionManager) eligibleLocked(c *Client) bool {
	size, ok := sm.clientSizes[c.id]
	return !c.readOnly && ok && size.Visible && size.Columns > 0 && size.Rows > 0
}

func (sm *SessionManager) selectLeaderLocked(now time.Time) {
	if sm.leaderConnectedLocked() {
		return
	}
	// Reclaim a disconnected lease before waiting for fallback grace.
	for c := range sm.clients {
		if c.key != "" && c.key == sm.leaderKey && sm.eligibleLocked(c) {
			sm.setLeaderLocked(c)
			return
		}
	}
	if sm.leaderKey != "" && now.Before(sm.leaderExpires) {
		return
	}
	if sm.leaderClientID != "" && sm.leaderSwitch == "never" {
		return
	}
	var selected *Client
	for c := range sm.clients {
		if !sm.eligibleLocked(c) {
			continue
		}
		// Prefer a reconnect with the original identity before any fallback.
		if c.key != "" && c.key == sm.leaderKey {
			selected = c
			break
		}
		if selected == nil || (sm.leaderSelect == "latest" && sm.clientOrder[c.id] > sm.clientOrder[selected.id]) ||
			(sm.leaderSelect != "latest" && sm.clientOrder[c.id] < sm.clientOrder[selected.id]) {
			selected = c
		}
	}
	if selected != nil {
		sm.setLeaderLocked(selected)
	}
}

func (sm *SessionManager) setLeaderLocked(c *Client) {
	sm.leaderClientID, sm.leaderKey = c.id, c.key
	sm.leaderExpires = time.Time{}
}

func (sm *SessionManager) handleResizeEvent(client *Client, req clientResizeRequest, now time.Time) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if !sm.clients[client] || client.readOnly {
		return
	}
	visible := req.Visible == nil || *req.Visible
	sm.clientSizes[client.id] = resizeDimensions{Columns: clamp(req.Columns, sm.minCols, sm.maxCols), Rows: clamp(req.Rows, sm.minRows, sm.maxRows),
		DeviceClass: req.DeviceClass, PixelRatio: req.PixelRatio, ViewportWidth: req.ViewportWidth, ViewportHeight: req.ViewportHeight, UpdatedAt: now, Visible: visible}
	if !visible {
		if sm.resizePolicy == "median" {
			sm.resizeDue = now.Add(sm.resizeDebounce)
		}
		return
	}
	previousLeader := sm.leaderClientID
	if sm.resizePolicy == "leader" {
		if req.TakeControl {
			sm.setLeaderLocked(client)
		} else {
			sm.selectLeaderLocked(now)
			if sm.leaderSwitch == "on_idle" && sm.leaderClientID != client.id {
				for c := range sm.clients {
					if c.id == sm.leaderClientID && now.Sub(c.lastActivity) >= sm.leaderIdle {
						sm.setLeaderLocked(client)
						break
					}
				}
			}
		}
		if sm.leaderClientID != client.id {
			return
		}
	}
	if previousLeader != sm.leaderClientID {
		sm.publishLocked(sm.terminalStateMessageLocked("size-control"), nil)
	}
	// Trailing debounce: every request replaces the target; the final one is
	// always applied even when no further browser resize event arrives.
	sm.resizeDue = now.Add(sm.resizeDebounce)
	if sm.lastResizeAt.IsZero() || sm.resizeDebounce == 0 || req.TakeControl {
		sm.applyResizeLocked(now)
	}
}

func (sm *SessionManager) flushResize(now time.Time) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if sm.resizeDue.IsZero() || now.Before(sm.resizeDue) {
		return
	}
	previousLeader := sm.leaderClientID
	if sm.resizePolicy == "leader" {
		sm.selectLeaderLocked(now)
	}
	if previousLeader != sm.leaderClientID {
		sm.publishLocked(sm.terminalStateMessageLocked("size-control"), nil)
	}
	sm.applyResizeLocked(now)
}

func (sm *SessionManager) applyResizeLocked(now time.Time) {
	sm.resizeDue = time.Time{}
	cols, rows, source := sm.activeCols, sm.activeRows, "fixed"
	switch sm.resizePolicy {
	case "fixed":
		cols, rows = sm.configuredCols, sm.configuredRows
	case "leader":
		size, ok := sm.clientSizes[sm.leaderClientID]
		if !ok || !size.Visible || !sm.leaderConnectedLocked() {
			return
		}
		cols, rows, source = size.Columns, size.Rows, sm.leaderClientID
	case "median":
		var widths, heights []int
		for c := range sm.clients {
			if !sm.eligibleLocked(c) {
				continue
			}
			size := sm.clientSizes[c.id]
			widths, heights = append(widths, size.Columns), append(heights, size.Rows)
		}
		if len(widths) == 0 {
			return
		}
		cols, rows, source = medianInt(widths), medianInt(heights), "median"
	default:
		return
	}
	cols, rows = clamp(cols, sm.minCols, sm.maxCols), clamp(rows, sm.minRows, sm.maxRows)
	if cols == sm.activeCols && rows == sm.activeRows {
		return
	}
	// Serialize resize with publication and only acknowledge a successful ioctl.
	if err := sm.slave.ResizeTerminal(cols, rows); err != nil {
		log.Printf("failed to resize terminal to %dx%d: %v", cols, rows, err)
		return
	}
	sm.activeCols, sm.activeRows, sm.lastResizeAt, sm.lastResizeSource = cols, rows, now, source
	if sm.stream != nil {
		sm.stream.Resize(cols, rows)
	}
	sm.publishLocked(sm.terminalStateMessageLocked("client-resize"), nil)
}

func medianInt(values []int) int {
	sort.Ints(values)
	mid := len(values) / 2
	if len(values)%2 == 1 {
		return values[mid]
	}
	return (values[mid-1] + values[mid]) / 2
}
