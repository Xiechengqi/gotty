package vt

import (
	"slices"

	uv "github.com/charmbracelet/ultraviolet"
)

// DefaultScrollbackSize is the default maximum number of lines in the scrollback buffer.
const DefaultScrollbackSize = 10000

// Scrollback represents a scrollback buffer that stores lines scrolled off the screen.
type Scrollback struct {
	lines    []uv.Line
	wrapped  []bool
	maxLines int
	bytes    int
	maxBytes int
	evicted  bool
}

// NewScrollback creates a new scrollback buffer with the given maximum number of lines.
func NewScrollback(maxLines int) *Scrollback {
	if maxLines < 0 {
		maxLines = DefaultScrollbackSize
	}
	return &Scrollback{
		lines:    make([]uv.Line, 0, min(maxLines, 1000)), // Pre-allocate reasonable capacity
		maxLines: maxLines,
		maxBytes: 128 * 1024 * 1024,
	}
}

// Push adds a line to the scrollback buffer.
// If the buffer is full, the oldest line is removed.
func (s *Scrollback) Push(line uv.Line) { s.PushWrapped(line, false) }

func (s *Scrollback) PushWrapped(line uv.Line, wrapped bool) {
	if s == nil || s.maxLines <= 0 {
		return
	}

	// Find last non-empty cell to trim trailing empty cells.
	// This helps with wrapping and window resizing.
	// Clone the line content up to and including the last non-empty cell
	// and any continuation cell belonging to a wide glyph.
	cloned := slices.Clone(line[:trimmedCheckpointLength(line)])

	size := scrollbackLineBytes(cloned)
	for len(s.lines) > 0 && (len(s.lines) >= s.maxLines || s.bytes+size > s.maxBytes) {
		s.bytes -= scrollbackLineBytes(s.lines[0])
		s.lines = slices.Delete(s.lines, 0, 1)
		s.wrapped = slices.Delete(s.wrapped, 0, 1)
		s.evicted = true
	}
	if size > s.maxBytes {
		s.evicted = true
		return
	}
	s.lines = append(s.lines, cloned)
	s.wrapped = append(s.wrapped, wrapped)
	s.bytes += size

}

// PushN adds n lines from the buffer starting at line y to the scrollback.
func (s *Scrollback) PushN(buf *uv.RenderBuffer, y, n int) {
	if s == nil || buf == nil || n <= 0 {
		return
	}

	for i := range min(n, buf.Height()-y) {
		if line := buf.Line(y + i); line != nil {
			s.Push(line)
		}
	}
}

// Len returns the number of lines in the scrollback buffer.
func (s *Scrollback) Len() int {
	if s == nil {
		return 0
	}
	return len(s.lines)
}

// MaxLines returns the maximum number of lines the scrollback buffer can hold.
func (s *Scrollback) MaxLines() int {
	if s == nil {
		return 0
	}
	return s.maxLines
}

// SetMaxLines sets the maximum number of lines in the scrollback buffer.
// If the current number of lines exceeds the new maximum, oldest lines are removed.
func (s *Scrollback) SetMaxLines(maxLines int) {
	if s == nil || maxLines < 0 {
		return
	}

	s.maxLines = maxLines
	if len(s.lines) > maxLines {
		// Remove oldest lines
		for _, line := range s.lines[:len(s.lines)-maxLines] {
			s.bytes -= scrollbackLineBytes(line)
		}
		s.evicted = true
		s.wrapped = s.wrapped[len(s.lines)-maxLines:]
		s.lines = s.lines[len(s.lines)-maxLines:]
	}
}

// Line returns the line at the given index.
// Index 0 is the oldest line, Len()-1 is the most recent.
// Returns nil if index is out of bounds.
func (s *Scrollback) Line(index int) uv.Line {
	if s == nil || index < 0 || index >= len(s.lines) {
		return nil
	}
	return s.lines[index]
}

// Lines returns all lines in the scrollback buffer.
// Index 0 is the oldest line.
func (s *Scrollback) Lines() []uv.Line {
	if s == nil {
		return nil
	}
	return s.lines
}

// Clear removes all lines from the scrollback buffer.
func (s *Scrollback) Clear() {
	if s == nil {
		return
	}
	s.lines = nil
	s.wrapped = nil
	s.bytes = 0
	s.evicted = false
}

// CellAt returns the cell at the given position in the scrollback buffer.
// x is the column, y is the line index (0 = oldest).
// Returns nil if position is out of bounds.
func (s *Scrollback) CellAt(x, y int) *uv.Cell {
	line := s.Line(y)
	if line == nil || x < 0 || x >= len(line) {
		return nil
	}
	return &line[x]
}

// GoTTY: count cell storage and variable text/link payloads conservatively.
func scrollbackLineBytes(line uv.Line) int {
	n := len(line) * 128
	for _, cell := range line {
		n += len(cell.Content) + len(cell.Link.URL) + len(cell.Link.Params)
	}
	return n
}
