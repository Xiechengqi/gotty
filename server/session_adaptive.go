package server

import "time"

const (
	adaptiveSettle      = 500 * time.Millisecond
	adaptiveHiddenGrace = 2 * time.Second
	adaptivePresenceTTL = 15 * time.Second
)

func geometryDevice(value string) string {
	switch value {
	case "desktop", "tablet", "mobile":
		return value
	default:
		return "unknown"
	}
}

func geometryInput(value string) string {
	if value == "fine" || value == "coarse" {
		return value
	}
	return "unknown"
}

func (size resizeDimensions) fresh(now time.Time) bool {
	// Old clients do not renew presence; retain their connection-based semantics.
	return !size.Presence || now.Sub(size.UpdatedAt) < adaptivePresenceTTL
}

func (size resizeDimensions) priority() int {
	switch size.InputClass {
	case "fine":
		return 2
	case "coarse":
		return 0
	}
	switch size.DeviceClass {
	case "desktop":
		return 2
	case "mobile", "tablet":
		return 0
	default:
		return 1
	}
}

// Called by the session's existing ticker as well as viewport reports. It never
// writes to the PTY: the normal serialized resize path owns ioctl and replay.
func (sm *SessionManager) reconcileAdaptiveLocked(now time.Time) {
	current, hasSize := sm.clientSizes[sm.leaderClientID]
	connected := sm.leaderConnectedLocked()
	if !connected && sm.leaderKey != "" && now.Before(sm.leaderExpires) {
		sm.adaptiveCandidate = ""
		return
	}
	if connected && hasSize && !current.Visible && now.Before(current.HiddenSince.Add(adaptiveHiddenGrace)) {
		sm.adaptiveCandidate = ""
		return
	}
	healthy := connected && hasSize && current.Visible && current.fresh(now)
	var best *Client
	for c := range sm.clients {
		size := sm.clientSizes[c.id]
		if !sm.eligibleLocked(c) || !size.fresh(now) {
			continue
		}
		if best == nil {
			best = c
			continue
		}
		other := sm.clientSizes[best.id]
		area, otherArea := int64(size.Columns)*int64(size.Rows), int64(other.Columns)*int64(other.Rows)
		if size.priority() > other.priority() || (size.priority() == other.priority() && (area > otherArea || (area == otherArea && sm.clientOrder[c.id] < sm.clientOrder[best.id]))) {
			best = c
		}
	}
	if best == nil || best.id == sm.leaderClientID {
		sm.adaptiveCandidate = ""
		return
	}
	next := sm.clientSizes[best.id]
	// Same-class viewers must offer at least 20% more cells; small viewport
	// fluctuations and equal-sized browser tabs cannot repeatedly steal control.
	if healthy && next.priority() <= current.priority() && int64(next.Columns)*int64(next.Rows)*5 < int64(current.Columns)*int64(current.Rows)*6 {
		sm.adaptiveCandidate = ""
		return
	}
	if sm.adaptiveCandidate != best.id {
		sm.adaptiveCandidate, sm.adaptiveSince = best.id, now
		return
	}
	if now.Sub(sm.adaptiveSince) < adaptiveSettle || now.Sub(next.StableSince) < adaptiveSettle {
		return
	}
	reason := "adaptive-preferred"
	switch {
	case sm.leaderClientID == "":
		reason = "adaptive-initial"
	case !connected:
		reason = "adaptive-disconnected"
	case !current.Visible:
		reason = "adaptive-hidden"
	case !current.fresh(now):
		reason = "adaptive-expired"
	}
	sm.setLeaderLocked(best)
	sm.controlReason = reason
	sm.adaptiveCandidate = ""
	sm.resizeDue = now
	sm.publishLocked(sm.terminalStateMessageLocked("size-control"), nil)
}
