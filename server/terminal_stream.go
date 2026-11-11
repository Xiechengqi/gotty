package server

import (
	"encoding/json"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

const terminalJournalBytes = 10 * 1024 * 1024
const terminalSnapshotBytes = 8 * 1024 * 1024

type streamEvent struct {
	Sequence int64  `json:"sequence"`
	Data     string `json:"data"`
}

// terminalStream is owned by SessionManager.mu. Output and resize events share
// one sequence, so a reconnect can recover geometry as well as printable bytes.
type terminalStream struct {
	emulator      *vt.Emulator
	events        []streamEvent
	bytes         int
	sequence      int64
	historyLines  int
	cursorQueries [2]cursorQuery
}

type cursorQuery struct {
	sequence int64
	at       time.Time
}

func newTerminalStream(cols, rows, lines int) *terminalStream {
	if lines < 0 {
		lines = 5000
	}
	stream := &terminalStream{historyLines: lines}
	stream.Reset(cols, rows)
	return stream
}

func (s *terminalStream) Reset(cols, rows int) {
	if s.emulator != nil {
		_ = s.emulator.Close()
	}
	s.emulator = vt.NewEmulator(cols, rows)
	s.emulator.DiscardResponses()
	s.emulator.SetScrollbackSize(s.historyLines)
	s.emulator.DisableAltScrollback()
	// Keep query counters monotonic across PTY restarts for connected clients.
	for index := range s.cursorQueries {
		s.cursorQueries[index].at = time.Time{}
	}
	for index, command := range []int{'n', ansi.Command('?', 0, 'n')} {
		s.emulator.RegisterCsiHandler(command, func(params ansi.Params) bool {
			if n, _, ok := params.Param(0, 0); ok && n == 6 {
				s.cursorQueries[index].sequence++
				s.cursorQueries[index].at = time.Now()
			}
			return false // The normal VT handler still processes the query.
		})
	}
	s.events, s.bytes, s.sequence = nil, 0, 0
}

func (s *terminalStream) Resize(cols, rows int) { s.emulator.Resize(cols, rows) }
func (s *terminalStream) Close()                { _ = s.emulator.Close() }

func (s *terminalStream) Append(wire, raw []byte) []byte {
	if len(wire) == 0 {
		return nil
	}
	if len(raw) > 0 {
		_, _ = s.emulator.Write(raw)
	}
	s.sequence++
	event := streamEvent{Sequence: s.sequence, Data: string(wire)}
	s.events = append(s.events, event)
	s.bytes += len(event.Data) + 64
	for len(s.events) > 1 && s.bytes > terminalJournalBytes {
		s.bytes -= len(s.events[0].Data) + 64
		s.events[0] = streamEvent{}
		s.events = s.events[1:]
	}
	// Avoid retaining a large backing array after a burst has been evicted.
	if cap(s.events) > 2*len(s.events) && cap(s.events) > 512 {
		s.events = append([]streamEvent(nil), s.events...)
	}
	payload, _ := json.Marshal(event)
	return append([]byte{'c'}, payload...)
}

func (s *terminalStream) Since(sequence int64) ([]streamEvent, bool) {
	if sequence < 0 || sequence > s.sequence {
		return nil, false
	}
	if sequence == s.sequence {
		return []streamEvent{}, true
	}
	if len(s.events) == 0 || sequence < s.events[0].Sequence-1 {
		return nil, false
	}
	from := int(sequence - s.events[0].Sequence + 1)
	return append([]streamEvent(nil), s.events[from:]...), true
}

func (sm *SessionManager) buildStreamReplay(init InitMessage, epoch string, offset int64) replaySnapshot {
	s := sm.stream
	replay := replaySnapshot{Epoch: epoch, EndOffset: offset, Columns: s.emulator.Width(), Rows: s.emulator.Height(), Sequence: s.sequence}
	if init.Epoch == epoch && init.LastSequence > 0 {
		if events, ok := s.Since(init.LastSequence); ok {
			replay.Mode = "events"
			replay.FromSequence = init.LastSequence
			replay.Events = events
			return replay
		}
	}
	replay.Mode = "snapshot"
	replay.Data, replay.Truncated = s.emulator.Checkpoint(terminalSnapshotBytes)
	return replay
}
