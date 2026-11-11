package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

func checkpointTerminal(t *testing.T, input string, suffix string, cols, rows int) {
	t.Helper()
	original := newTerminalStream(cols, rows, 5000)
	defer original.Close()
	original.Append(encodeOutputMessage([]byte(input)), []byte(input))
	snapshot, _ := original.emulator.Checkpoint(terminalSnapshotBytes)
	restored := newTerminalStream(cols, rows, 5000)
	defer restored.Close()
	_, _ = restored.emulator.Write(snapshot)
	compareEmulators(t, original.emulator, restored.emulator)
	_, _ = original.emulator.Write([]byte(suffix))
	_, _ = restored.emulator.Write([]byte(suffix))
	compareEmulators(t, original.emulator, restored.emulator)
}
func compareEmulators(t *testing.T, a, b *vt.Emulator) {
	t.Helper()
	if emulatorText(a) != emulatorText(b) || a.CursorPosition() != b.CursorPosition() || a.IsAltScreen() != b.IsAltScreen() {
		t.Fatalf("checkpoint screen mismatch:\noriginal %q %v alt=%v\nrestored %q %v alt=%v", emulatorText(a), a.CursorPosition(), a.IsAltScreen(), emulatorText(b), b.CursorPosition(), b.IsAltScreen())
	}
	for y := 0; y < a.Height(); y++ {
		for x := 0; x < a.Width(); x++ {
			ac, bc := a.CellAt(x, y), b.CellAt(x, y)
			if !reflect.DeepEqual(ac.Style, bc.Style) {
				t.Fatalf("cell style differs at %d,%d: %#v %#v", x, y, ac, bc)
			}
		}
	}
}
func emulatorText(e *vt.Emulator) string {
	var rows []string
	for y := 0; y < e.Height(); y++ {
		var row strings.Builder
		for x := 0; x < e.Width(); x++ {
			cell := e.CellAt(x, y)
			if cell.Width == 0 {
				continue
			}
			if cell.Content == "" {
				row.WriteByte(' ')
			} else {
				row.WriteString(cell.Content)
			}
		}
		rows = append(rows, strings.TrimRight(row.String(), " "))
	}
	return strings.Join(rows, "\n")
}

func TestTerminalCheckpointStateAndLiveContinuation(t *testing.T) {
	cases := []struct {
		name, input, suffix string
		cols, rows          int
	}{
		{"scrollback", strings.Repeat("history\r\n", 100) + "prompt>", "next\r\n", 80, 24},
		{"alternate", "normal prompt\x1b[?1049h\x1b[2J\x1b[3;7Hvim unsaved\x1b[?25l", "\x1b[?1049lcontinued", 80, 24},
		{"saved cursor and pen", "\x1b[31mred\x1b7\x1b[0m\x1b[4;8Hnormal", "\x1b8saved", 80, 24},
		{"margins and origin", "\x1b[3;10r\x1b[?6h\x1b[2;4Hregion", "\r\n" + strings.Repeat("scroll\r\n", 15), 80, 24},
		{"delayed wrap", "abcdefghijklmnopqrst", "NEXT", 20, 5},
		{"unicode", "中文🙂 café e\u0301\r\nnext", "接着输入", 80, 24},
		{"partial utf8", "text\xe4\xb8", "\xad文", 80, 24},
		{"partial csi", "prompt\x1b[38;2;20;", "30;40mcolored", 80, 24},
		{"charset", "\x1b(0lqqk\x1b[2;1H", "x\x1b(Bnormal", 80, 24},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { checkpointTerminal(t, tc.input, tc.suffix, tc.cols, tc.rows) })
	}
}
func TestTerminalCheckpointRestoresMoreThanLegacyTail(t *testing.T) {
	stream := newTerminalStream(220, 30, 5000)
	defer stream.Close()
	for i := 0; i < 3000; i++ {
		raw := []byte(fmt.Sprintf("HISTORY-%04d-%s\r\n", i, strings.Repeat("x", 200)))
		stream.Append(encodeOutputMessage(raw), raw)
	}
	data, truncated := stream.emulator.Checkpoint(terminalSnapshotBytes)
	if truncated || !bytes.Contains(data, []byte("HISTORY-0000-")) || !bytes.Contains(data, []byte("HISTORY-2999-")) || len(data) < 512*1024 {
		t.Fatalf("lost retained history: %d bytes truncated=%v", len(data), truncated)
	}
	restored := newTerminalStream(220, 30, 5000)
	defer restored.Close()
	_, _ = restored.emulator.Write(data)
	if stream.emulator.ScrollbackLen() != restored.emulator.ScrollbackLen() {
		t.Fatalf("scrollback length mismatch: %d != %d", stream.emulator.ScrollbackLen(), restored.emulator.ScrollbackLen())
	}
}
func TestTerminalCheckpointHasNoHistoricalSideEffectsOrQueryDeadlock(t *testing.T) {
	stream := newTerminalStream(80, 24, 5000)
	defer stream.Close()
	done := make(chan struct{})
	go func() {
		_, _ = stream.emulator.Write([]byte("\x1b[6n\x1b[c\x1bP$qm\x1b\\\x1b]52;c;c2VjcmV0\a\x1b]0;old title\aVISIBLE"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shadow terminal blocked answering a query")
	}
	snapshot, _ := stream.emulator.Checkpoint(terminalSnapshotBytes)
	for _, forbidden := range []string{"c2VjcmV0", "old title", "\x1b[6n", "\x1b[c", "\x1bP$q"} {
		if bytes.Contains(snapshot, []byte(forbidden)) {
			t.Fatalf("historical side effect replayed: %q", forbidden)
		}
	}
}
func TestTerminalEventReplayIncludesResizeAndFallsBackOnGapOrEpochChange(t *testing.T) {
	sm, _ := resizeManager(t)
	epoch, _, _ := sm.history.Offsets()
	sm.publishLocked(encodeOutputMessage([]byte("first")), []byte("first"))
	sequence := sm.stream.sequence
	sm.stream.Resize(90, 25)
	sm.activeCols, sm.activeRows = 90, 25
	sm.publishLocked(sm.terminalStateMessageLocked("client-resize"), nil)
	sm.publishLocked(encodeOutputMessage([]byte("second")), []byte("second"))
	replay := sm.buildReplaySnapshot(InitMessage{Epoch: epoch, ReplayVersion: 2, LastSequence: sequence})
	if replay.Mode != "events" || len(replay.Events) != 2 || replay.Events[0].Data[0] != '8' || replay.Events[1].Data[0] != '1' {
		t.Fatalf("replay lost geometry order: %#v", replay)
	}
	payload := bytes.Repeat([]byte("x"), 1024*1024)
	for i := 0; i < 12; i++ {
		sm.stream.Append(encodeOutputMessage(payload), nil)
	}
	if sm.buildReplaySnapshot(InitMessage{Epoch: epoch, ReplayVersion: 2, LastSequence: sequence}).Mode != "snapshot" {
		t.Fatal("evicted event gap did not request a checkpoint")
	}
	if sm.buildReplaySnapshot(InitMessage{Epoch: "old", ReplayVersion: 2, LastSequence: sm.stream.sequence}).Mode != "snapshot" {
		t.Fatal("old epoch was accepted")
	}
}
func TestCheckpointBudgetAndHistoryDisable(t *testing.T) {
	stream := newTerminalStream(80, 5, 10)
	defer stream.Close()
	_, _ = stream.emulator.Write([]byte(strings.Repeat("OLD-HISTORY\r\n", 50)))
	data, truncated := stream.emulator.Checkpoint(500)
	if !truncated || len(data) > 4000 {
		t.Fatalf("checkpoint is not bounded: bytes=%d truncated=%v", len(data), truncated)
	}
	disabled := newTerminalStream(80, 5, 0)
	defer disabled.Close()
	_, _ = disabled.emulator.Write([]byte(strings.Repeat("line\r\n", 50)))
	if disabled.emulator.ScrollbackLen() != 0 {
		t.Fatal("history-lines=0 did not disable history")
	}
}
func TestSnapshotAndEventWireFrames(t *testing.T) {
	replay := replaySnapshot{Mode: "snapshot", Columns: 80, Rows: 24, Sequence: 9, Data: []byte("\x1bcstate")}
	frames := captureReplayMessages(t, replay)
	if len(frames) != 3 || decodeReplayBegin(t, frames[0]).Columns != 80 || decodeReplayEnd(t, frames[2]).Sequence != 9 {
		t.Fatalf("unexpected checkpoint frames %q", frames)
	}
	replay = replaySnapshot{Mode: "events", FromSequence: 9, Sequence: 10, Events: []streamEvent{{Sequence: 10, Data: "8{}"}}}
	frames = captureReplayMessages(t, replay)
	var event streamEvent
	if len(frames) != 3 || frames[1][0] != 'c' || json.Unmarshal(frames[1][1:], &event) != nil || event.Sequence != 10 {
		t.Fatalf("unexpected event frames %q", frames)
	}
}

func TestCheckpointPreservesTypedSpacesCombiningMarksAndReflow(t *testing.T) {
	s := newTerminalStream(180, 24, 5000)
	defer s.Close()
	for i := 0; i < 100; i++ {
		_, _ = s.emulator.Write([]byte(fmt.Sprintf("ROW-%03d-%s\r\n", i, strings.Repeat("x", 150))))
	}
	_, _ = s.emulator.Write([]byte("prompt> e"))
	_, _ = s.emulator.Write([]byte("\u0301 "))
	for _, width := range []int{60, 180, 90} {
		s.Resize(width, 24)
		data, _ := s.emulator.Checkpoint(terminalSnapshotBytes)
		restored := newTerminalStream(width, 24, 5000)
		_, _ = restored.emulator.Write(data)
		compareEmulators(t, s.emulator, restored.emulator)
		if s.emulator.ScrollbackLen() != restored.emulator.ScrollbackLen() {
			t.Fatalf("reflow history row count changed at width %d", width)
		}
		_, _ = s.emulator.Write([]byte("next"))
		_, _ = restored.emulator.Write([]byte("next"))
		compareEmulators(t, s.emulator, restored.emulator)
		restored.Close()
	}
}

func TestReflowPreservesTheLastWideGlyphOfALogicalLine(t *testing.T) {
	s := newTerminalStream(20, 5, 100)
	defer s.Close()
	_, _ = s.emulator.Write([]byte(strings.Repeat("C", 19) + "中尾\r\n" + strings.Repeat("S", 19) + " 中SPACE\r\n" + strings.Repeat("history\r\n", 10)))
	for _, width := range []int{6, 20, 10, 20} {
		s.Resize(width, 5)
		data, _ := s.emulator.Checkpoint(terminalSnapshotBytes)
		if !bytes.Contains(data, []byte("尾")) || bytes.Count(data, []byte("中")) != 2 {
			t.Fatalf("wide glyph lost when reflowing to %d columns", width)
		}
	}
}
