package server

import (
	"testing"
	"time"
)

func adaptiveManager(t *testing.T) (*SessionManager, *resizeSlave) {
	sm, slave := resizeManager(t)
	sm.resizePolicy = "adaptive"
	return sm, slave
}

func viewport(cols, rows int, input string) clientResizeRequest {
	device := "mobile"
	if input == "fine" {
		device = "desktop"
	}
	return clientResizeRequest{Columns: cols, Rows: rows, InputClass: input, DeviceClass: device}
}

func paneViewport(cols, rows int, input string, page, pane bool) clientResizeRequest {
	req := viewport(cols, rows, input)
	visible := page && pane
	req.Presence, req.Visible, req.PageVisible, req.PaneVisible = true, &visible, &page, &pane
	return req
}

func TestAdaptiveInternalPaneRetainsConfirmedGrid(t *testing.T) {
	sm, slave := adaptiveManager(t)
	now := time.Now()
	desktop := addResizeClient(sm, "desktop", "desktop-browser-key", false)
	phone := addResizeClient(sm, "phone", "phone-browser-key", false)
	sm.handleResizeEvent(desktop, paneViewport(180, 50, "fine", true, true), now)
	sm.handleResizeEvent(phone, paneViewport(60, 25, "coarse", true, true), now)
	sm.flushResize(now.Add(time.Second))
	count := len(slave.sizes)
	// Hide before a pending resize reaches ioctl; retain the confirmed grid.
	sm.handleResizeEvent(desktop, paneViewport(220, 80, "fine", true, true), now.Add(1500*time.Millisecond))
	for i := 2; i < 32; i += 3 {
		at := now.Add(time.Duration(i) * time.Second)
		// Hidden layout measurements cannot shrink the retained viewport.
		sm.handleResizeEvent(desktop, paneViewport(60, 20, "fine", true, false), at)
		sm.handleResizeEvent(phone, paneViewport(240, 80, "coarse", true, true), at)
		sm.flushResize(at.Add(time.Second))
		if sm.leaderClientID != desktop.id || sm.activeCols != 180 || sm.activeRows != 50 {
			t.Fatal("internal pane switch changed the confirmed desktop grid")
		}
	}
	state := sm.terminalStateLocked("test", now.Add(31*time.Second))
	if !state.LeaderRetained || !state.LeaderPageVisible || state.LeaderPaneVisible || state.LeaderVisible {
		t.Fatal("retained geometry diagnostics do not distinguish page and pane")
	}
	sm.handleResizeEvent(desktop, paneViewport(180, 50, "fine", true, true), now.Add(32*time.Second))
	sm.flushResize(now.Add(33 * time.Second))
	if len(slave.sizes) != count || sm.leaderClientID != desktop.id {
		t.Fatal("pane return issued an unnecessary PTY resize")
	}
}

func TestAdaptiveRetainedCandidateUsesConfirmedCapacity(t *testing.T) {
	sm, slave := adaptiveManager(t)
	now := time.Now()
	owner := addResizeClient(sm, "owner", "owner-browser-key", false)
	other := addResizeClient(sm, "other", "other-browser-key", false)
	sm.handleResizeEvent(owner, paneViewport(180, 50, "fine", true, true), now)
	sm.flushResize(now.Add(time.Second))
	sm.handleResizeEvent(owner, paneViewport(220, 80, "fine", true, true), now.Add(2*time.Second))
	sm.handleResizeEvent(owner, paneViewport(220, 80, "fine", true, false), now.Add(2050*time.Millisecond))
	sm.handleResizeEvent(other, paneViewport(160, 70, "fine", true, true), now.Add(2100*time.Millisecond))
	sm.flushResize(now.Add(3 * time.Second))
	if sm.leaderClientID != other.id || sm.activeCols != 160 || sm.activeRows != 70 {
		t.Fatal("unconfirmed hidden capacity blocked a valid same-class candidate")
	}
	for _, size := range slave.sizes {
		if size[0] == 220 {
			t.Fatal("hidden pending grid reached the PTY")
		}
	}
}

func TestAdaptiveBackgroundLeaseExpiresAndHiddenPaneCannotAcquire(t *testing.T) {
	sm, _ := adaptiveManager(t)
	now := time.Now()
	desktop := addResizeClient(sm, "desktop", "desktop-browser-key", false)
	phone := addResizeClient(sm, "phone", "phone-browser-key", false)
	sm.handleResizeEvent(desktop, paneViewport(180, 50, "fine", true, true), now)
	sm.handleResizeEvent(phone, viewport(60, 25, "coarse"), now)
	sm.flushResize(now.Add(time.Second))
	for i := 2; i <= 14; i += 3 {
		// Even a broken client repeatedly reporting a background page cannot
		// renew it. Frozen pages normally send no reports at all.
		sm.handleResizeEvent(desktop, paneViewport(180, 50, "fine", false, true), now.Add(time.Duration(i)*time.Second))
		sm.flushResize(now.Add(time.Duration(i) * time.Second))
		if sm.leaderClientID != desktop.id {
			t.Fatal("background lease yielded before its TTL")
		}
	}
	sm.flushResize(now.Add(15 * time.Second))
	sm.flushResize(now.Add(16 * time.Second))
	if sm.leaderClientID != phone.id || sm.controlReason != "adaptive-expired" {
		t.Fatal("background page did not yield after TTL")
	}
	// Foreground but hidden is renewal-only: it cannot acquire an expired lease.
	sm.handleResizeEvent(desktop, paneViewport(180, 50, "fine", true, false), now.Add(17*time.Second))
	sm.flushResize(now.Add(18 * time.Second))
	if sm.leaderClientID != phone.id {
		t.Fatal("hidden pane acquired geometry")
	}
	sm.handleResizeEvent(desktop, paneViewport(180, 50, "fine", true, true), now.Add(19*time.Second))
	sm.flushResize(now.Add(20 * time.Second))
	if sm.leaderClientID != desktop.id || sm.activeCols != 180 {
		t.Fatal("visible desktop did not reacquire geometry")
	}
}

func TestAdaptiveMobileFirstDesktopRecoversWithoutTakeover(t *testing.T) {
	sm, _ := adaptiveManager(t)
	now := time.Now()
	phone := addResizeClient(sm, "phone", "phone-browser-key", false)
	desktop := addResizeClient(sm, "desktop", "desktop-browser-key", false)
	sm.handleResizeEvent(phone, viewport(99, 20, "coarse"), now)
	sm.flushResize(now.Add(time.Second))
	if sm.activeCols != 99 || sm.activeRows != 20 {
		t.Fatal("phone-only grid did not fit")
	}
	sm.handleResizeEvent(desktop, viewport(180, 50, "fine"), now.Add(time.Second))
	sm.flushResize(now.Add(2 * time.Second))
	if sm.leaderClientID != desktop.id || sm.activeCols != 180 || sm.activeRows != 50 {
		t.Fatal("desktop could not recover a phone-owned grid")
	}
	// Even a large landscape phone and a deliberate takeover cannot shrink the desktop.
	req := viewport(210, 70, "coarse")
	req.TakeControl = true
	sm.handleResizeEvent(phone, req, now.Add(3*time.Second))
	sm.flushResize(now.Add(4 * time.Second))
	if sm.leaderClientID != desktop.id || sm.activeCols != 180 {
		t.Fatal("phone displaced visible desktop")
	}
}

func TestAdaptiveHiddenControllerYieldsAndReturns(t *testing.T) {
	sm, _ := adaptiveManager(t)
	now := time.Now()
	desktop := addResizeClient(sm, "desktop", "desktop-browser-key", false)
	phone := addResizeClient(sm, "phone", "phone-browser-key", false)
	sm.handleResizeEvent(desktop, viewport(180, 50, "fine"), now)
	sm.handleResizeEvent(phone, viewport(60, 25, "coarse"), now)
	sm.flushResize(now.Add(time.Second))
	hidden := false
	req := viewport(180, 50, "fine")
	req.Visible = &hidden
	sm.handleResizeEvent(desktop, req, now.Add(2*time.Second))
	sm.flushResize(now.Add(3 * time.Second))
	if sm.leaderClientID != desktop.id {
		t.Fatal("brief hidden transition lost control")
	}
	sm.flushResize(now.Add(4 * time.Second))
	sm.flushResize(now.Add(5 * time.Second))
	if sm.leaderClientID != phone.id || sm.activeCols != 60 || sm.controlReason != "adaptive-hidden" {
		t.Fatal("hidden controller did not yield")
	}
	sm.handleResizeEvent(desktop, viewport(180, 50, "fine"), now.Add(6*time.Second))
	sm.flushResize(now.Add(7 * time.Second))
	if sm.leaderClientID != desktop.id || sm.activeRows != 50 {
		t.Fatal("returning desktop did not recover")
	}
}

func TestAdaptivePresenceExpiryAndLegacyCompatibility(t *testing.T) {
	for _, leased := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "leased"}[leased], func(t *testing.T) {
			sm, _ := adaptiveManager(t)
			now := time.Now()
			desktop := addResizeClient(sm, "desktop", "desktop-browser-key", false)
			phone := addResizeClient(sm, "phone", "phone-browser-key", false)
			req := viewport(180, 50, "fine")
			req.Presence = leased
			sm.handleResizeEvent(desktop, req, now)
			sm.handleResizeEvent(phone, viewport(60, 25, "coarse"), now)
			sm.flushResize(now.Add(time.Second))
			sm.flushResize(now.Add(15 * time.Second))
			sm.flushResize(now.Add(16 * time.Second))
			if leased && (sm.leaderClientID != phone.id || sm.controlReason != "adaptive-expired") {
				t.Fatal("sleeping client retained expired lease")
			}
			if !leased && sm.leaderClientID != desktop.id {
				t.Fatal("legacy client expired without a renewal protocol")
			}
		})
	}
}

func TestAdaptiveRefreshGraceAndLongDisconnect(t *testing.T) {
	sm, _ := adaptiveManager(t)
	now := time.Now()
	desktop := addResizeClient(sm, "desktop", "desktop-browser-key", false)
	phone := addResizeClient(sm, "phone", "phone-browser-key", false)
	sm.handleResizeEvent(desktop, viewport(180, 50, "fine"), now)
	sm.handleResizeEvent(phone, viewport(60, 25, "coarse"), now)
	sm.flushResize(now.Add(time.Second))
	sm.removeClientLocked(desktop, now.Add(2*time.Second))
	sm.flushResize(now.Add(4 * time.Second))
	if sm.activeCols != 180 {
		t.Fatal("refresh changed grid during grace")
	}
	refresh := addResizeClient(sm, "refresh", "desktop-browser-key", false)
	sm.handleResizeEvent(refresh, viewport(180, 50, "fine"), now.Add(4*time.Second))
	// Run() normally restores the identity before delivering the viewport.
	sm.setLeaderLocked(refresh)
	sm.flushResize(now.Add(6 * time.Second))
	if sm.activeCols != 180 {
		t.Fatal("same-identity refresh shrank grid")
	}
	sm.removeClientLocked(refresh, now.Add(7*time.Second))
	sm.flushResize(now.Add(12 * time.Second))
	sm.flushResize(now.Add(13 * time.Second))
	if sm.leaderClientID != phone.id || sm.activeCols != 60 {
		t.Fatal("expired disconnect did not fall back")
	}
	returned := addResizeClient(sm, "returned", "desktop-browser-key", false)
	sm.handleResizeEvent(returned, viewport(180, 50, "fine"), now.Add(14*time.Second))
	sm.flushResize(now.Add(15 * time.Second))
	if sm.leaderClientID != returned.id {
		t.Fatal("desktop did not recover after fallback")
	}
}

func TestAdaptiveHysteresisWholeGridAndStableMeasurement(t *testing.T) {
	sm, _ := adaptiveManager(t)
	now := time.Now()
	a := addResizeClient(sm, "a", "first-browser-key", false)
	b := addResizeClient(sm, "b", "second-browser-key", false)
	sm.handleResizeEvent(a, viewport(120, 40, "fine"), now)
	sm.flushResize(now.Add(time.Second))
	sm.handleResizeEvent(b, viewport(125, 40, "fine"), now.Add(time.Second))
	sm.flushResize(now.Add(2 * time.Second))
	if sm.leaderClientID != a.id {
		t.Fatal("nearly equal viewport displaced controller")
	}
	sm.handleResizeEvent(b, viewport(110, 70, "fine"), now.Add(3*time.Second))
	sm.handleResizeEvent(b, viewport(111, 70, "fine"), now.Add(3400*time.Millisecond))
	sm.flushResize(now.Add(3600 * time.Millisecond))
	if sm.leaderClientID != a.id {
		t.Fatal("unstable measurement was elected")
	}
	sm.flushResize(now.Add(4 * time.Second))
	if sm.activeCols != 111 || sm.activeRows != 70 {
		t.Fatal("did not choose one real viewport's entire grid")
	}
}

func TestAdaptiveReadonlyHiddenAndRenewalsDoNotResize(t *testing.T) {
	sm, slave := adaptiveManager(t)
	now := time.Now()
	phone := addResizeClient(sm, "phone", "phone-browser-key", false)
	observer := addResizeClient(sm, "observer", "observer-browser-key", true)
	hidden := addResizeClient(sm, "hidden", "hidden-browser-key", false)
	sm.handleResizeEvent(phone, viewport(99, 20, "coarse"), now)
	sm.handleResizeEvent(observer, viewport(240, 80, "fine"), now)
	no := false
	req := viewport(240, 80, "fine")
	req.Visible = &no
	sm.handleResizeEvent(hidden, req, now)
	sm.flushResize(now.Add(time.Second))
	if sm.leaderClientID != phone.id {
		t.Fatal("ineligible client elected")
	}
	count, sequence := len(slave.sizes), sm.stream.sequence
	for i := 2; i < 20; i++ {
		req := viewport(99, 20, "coarse")
		req.Presence = true
		sm.handleResizeEvent(phone, req, now.Add(time.Duration(i)*time.Second))
		sm.flushResize(now.Add(time.Duration(i) * time.Second))
	}
	if len(slave.sizes) != count || sm.stream.sequence != sequence {
		t.Fatal("unchanged presence generated resize or replay events")
	}
	// No visible fallback: retain the last grid, never synthesize a tiny default.
	req = viewport(99, 20, "coarse")
	req.Visible = &no
	sm.handleResizeEvent(phone, req, now.Add(20*time.Second))
	sm.flushResize(now.Add(30 * time.Second))
	if sm.activeCols != 99 || sm.activeRows != 20 {
		t.Fatal("no-viewer state changed grid")
	}
}
