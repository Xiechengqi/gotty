package server

import (
	"context"
	"errors"
	"testing"
	"time"
)

type resizeSlave struct {
	recordingSlave
	sizes [][2]int
	fail  bool
}

func (s *resizeSlave) ResizeTerminal(cols, rows int) error {
	if s.fail {
		return errors.New("ioctl failed")
	}
	s.sizes = append(s.sizes, [2]int{cols, rows})
	return nil
}
func resizeManager(t *testing.T) (*SessionManager, *resizeSlave) {
	t.Helper()
	slave := &resizeSlave{}
	sm := NewSessionManager(context.Background(), slave, &Options{ResizePolicy: "leader", LeaderSelect: "latest", LeaderSwitch: "on_disconnect", LeaderGraceMs: 5000, MinCols: 20, MaxCols: 240, MinRows: 5, MaxRows: 100, ResizeDebounceMs: 100, HistoryLines: 5000})
	t.Cleanup(sm.stream.Close)
	if err := sm.InitializeTerminal(); err != nil {
		t.Fatal(err)
	}
	return sm, slave
}
func addResizeClient(sm *SessionManager, id, key string, readonly bool) *Client {
	c := &Client{id: id, key: key, readOnly: readonly, send: make(chan []byte, 256), lastActivity: time.Now()}
	sm.clients[c] = true
	sm.nextClientSeq++
	sm.clientOrder[id] = sm.nextClientSeq
	return c
}
func TestSizeOwnerIsStableAndTakeoverIsExplicit(t *testing.T) {
	sm, slave := resizeManager(t)
	now := time.Now()
	desktop := addResizeClient(sm, "desktop", "desktop-browser-123", false)
	mobile := addResizeClient(sm, "mobile", "mobile-browser-1234", false)
	observer := addResizeClient(sm, "observer", "observer-browser-12", true)
	sm.handleResizeEvent(desktop, clientResizeRequest{Columns: 160, Rows: 45}, now)
	sm.flushResize(now.Add(time.Second))
	sm.handleResizeEvent(mobile, clientResizeRequest{Columns: 40, Rows: 15}, now.Add(time.Second))
	sm.handleResizeEvent(observer, clientResizeRequest{Columns: 20, Rows: 5, TakeControl: true}, now.Add(time.Second))
	sm.flushResize(now.Add(2 * time.Second))
	if sm.leaderClientID != "desktop" || sm.activeCols != 160 || len(slave.sizes) != 2 {
		t.Fatalf("new viewers stole size: leader=%s sizes=%v", sm.leaderClientID, slave.sizes)
	}
	sm.handleResizeEvent(mobile, clientResizeRequest{Columns: 40, Rows: 15, TakeControl: true}, now.Add(3*time.Second))
	if sm.leaderClientID != "mobile" || sm.activeCols != 40 {
		t.Fatalf("explicit takeover failed: %s %d", sm.leaderClientID, sm.activeCols)
	}
}
func TestResizeLeaseReconnectAndFallback(t *testing.T) {
	sm, _ := resizeManager(t)
	now := time.Now()
	first := addResizeClient(sm, "first", "same-browser-key-123", false)
	second := addResizeClient(sm, "second", "different-browser-key", false)
	sm.handleResizeEvent(first, clientResizeRequest{Columns: 150, Rows: 40}, now)
	sm.flushResize(now.Add(time.Second))
	sm.handleResizeEvent(second, clientResizeRequest{Columns: 40, Rows: 10}, now)
	sm.removeClientLocked(first, now)
	sm.flushResize(now.Add(time.Second))
	if sm.activeCols != 150 {
		t.Fatal("disconnect grace changed grid")
	}
	refresh := addResizeClient(sm, "refresh", "same-browser-key-123", false)
	sm.handleResizeEvent(refresh, clientResizeRequest{Columns: 145, Rows: 40}, now.Add(2*time.Second))
	sm.flushResize(now.Add(3 * time.Second))
	if sm.leaderClientID != "refresh" || sm.activeCols != 145 {
		t.Fatal("refresh did not reclaim lease")
	}
	sm.removeClientLocked(refresh, now.Add(3*time.Second))
	sm.flushResize(now.Add(9 * time.Second))
	if sm.leaderClientID != "second" || sm.activeCols != 40 {
		t.Fatal("expired lease did not fall back")
	}
}
func TestTrailingResizeAndFailedIoctl(t *testing.T) {
	sm, slave := resizeManager(t)
	now := time.Now()
	c := addResizeClient(sm, "c", "browser-key-1234567", false)
	sm.handleResizeEvent(c, clientResizeRequest{Columns: 120, Rows: 30}, now)
	sm.flushResize(now.Add(time.Second))
	sm.handleResizeEvent(c, clientResizeRequest{Columns: 130, Rows: 32}, now.Add(time.Second))
	sm.handleResizeEvent(c, clientResizeRequest{Columns: 140, Rows: 34}, now.Add(time.Second+10*time.Millisecond))
	sm.flushResize(now.Add(time.Second + 100*time.Millisecond))
	if sm.activeCols != 120 {
		t.Fatal("debounce applied too early")
	}
	sm.flushResize(now.Add(2 * time.Second))
	if sm.activeCols != 140 {
		t.Fatal("last resize was dropped")
	}
	slave.fail = true
	sm.handleResizeEvent(c, clientResizeRequest{Columns: 150, Rows: 36, TakeControl: true}, now.Add(3*time.Second))
	if sm.activeCols != 140 || sm.stream.emulator.Width() != 140 {
		t.Fatal("failed ioctl was acknowledged")
	}
}
func TestNeverAndSlowConsumerKeepOwnershipConsistent(t *testing.T) {
	sm, _ := resizeManager(t)
	sm.leaderSwitch = "never"
	now := time.Now()
	c := addResizeClient(sm, "first", "browser-key-1234567", false)
	other := addResizeClient(sm, "other", "other-browser-12345", false)
	sm.handleResizeEvent(c, clientResizeRequest{Columns: 150, Rows: 40}, now)
	sm.flushResize(now.Add(time.Second))
	sm.handleResizeEvent(other, clientResizeRequest{Columns: 60, Rows: 20}, now)
	for len(c.send) < cap(c.send) {
		c.send <- []byte("2")
	}
	sm.publishLocked(encodeOutputMessage([]byte("live")), []byte("live"))
	if sm.clients[c] {
		t.Fatal("slow consumer not removed")
	}
	sm.flushResize(now.Add(time.Minute))
	if sm.activeCols != 150 {
		t.Fatal("never policy changed grid after slow disconnect")
	}
	sm.handleResizeEvent(other, clientResizeRequest{Columns: 60, Rows: 20, TakeControl: true}, now.Add(time.Minute))
	if sm.activeCols != 60 {
		t.Fatal("never policy prevented explicit takeover")
	}
}
func TestIdleUsesInputActivityAndHiddenViewersCannotLead(t *testing.T) {
	sm, _ := resizeManager(t)
	sm.leaderSwitch = "on_idle"
	sm.leaderIdle = time.Minute
	now := time.Now()
	c := addResizeClient(sm, "first", "browser-key-1234567", false)
	other := addResizeClient(sm, "other", "other-browser-12345", false)
	sm.handleResizeEvent(c, clientResizeRequest{Columns: 150, Rows: 40}, now)
	sm.flushResize(now.Add(time.Second))
	c.lastActivity = now
	hidden := false
	sm.handleResizeEvent(other, clientResizeRequest{Columns: 60, Rows: 20, TakeControl: true, Visible: &hidden}, now.Add(2*time.Minute))
	if sm.leaderClientID != c.id {
		t.Fatal("hidden viewer stole leader")
	}
	sm.handleResizeEvent(other, clientResizeRequest{Columns: 60, Rows: 20}, now.Add(2*time.Minute))
	sm.flushResize(now.Add(3 * time.Minute))
	if sm.leaderClientID != other.id {
		t.Fatal("idle control did not yield after actual inactivity")
	}
}

func TestMedianReconcilesWhenAViewerBecomesHidden(t *testing.T) {
	sm, _ := resizeManager(t)
	sm.resizePolicy = "median"
	now := time.Now()
	desktop := addResizeClient(sm, "desktop", "desktop-browser-key", false)
	mobile := addResizeClient(sm, "mobile", "mobile-browser-key", false)
	observer := addResizeClient(sm, "observer", "observer-browser-key", true)
	sm.handleResizeEvent(desktop, clientResizeRequest{Columns: 160, Rows: 40}, now)
	sm.handleResizeEvent(mobile, clientResizeRequest{Columns: 40, Rows: 20}, now)
	sm.handleResizeEvent(observer, clientResizeRequest{Columns: 20, Rows: 5}, now)
	sm.flushResize(now.Add(time.Second))
	if sm.activeCols != 100 || sm.activeRows != 30 {
		t.Fatal("median included a read-only viewer")
	}
	hidden := false
	sm.handleResizeEvent(mobile, clientResizeRequest{Columns: 40, Rows: 20, Visible: &hidden}, now.Add(time.Second))
	sm.flushResize(now.Add(2 * time.Second))
	if sm.activeCols != 160 || sm.activeRows != 40 {
		t.Fatal("hidden viewer remained in the median")
	}
	if sm.leaderClientID != "" {
		t.Fatal("median geometry elected a size owner")
	}
}
