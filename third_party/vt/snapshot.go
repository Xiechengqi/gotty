package vt

import (
	"fmt"
	"image/color"
	"reflect"
	"sort"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

// DiscardResponses makes the shadow emulator incapable of answering queries
// into the real program and avoids blocking on its unused input pipe.
func (e *Emulator) DiscardResponses() { _ = e.pr.Close() }

// DisableAltScrollback matches xterm: the alternate screen has no history.
func (e *Emulator) DisableAltScrollback() { e.scrs[1].scrollback = nil }

// Checkpoint serializes both screens, retained scrollback and parser state as
// generated ANSI. Original OSC/DCS payloads (clipboard, file transfer, queries)
// are never replayed. The caller serializes access with live writes / resizes.
func (e *Emulator) Checkpoint(maxBytes int) ([]byte, bool) {
	var out strings.Builder
	out.WriteString("\x1bc\x1b[?7l")
	// Only current palette values are generated; original OSC payloads and
	// queries never enter a checkpoint.
	for index, c := range e.colors {
		if c != nil {
			r, g, b, _ := c.RGBA()
			fmt.Fprintf(&out, "\x1b]4;%d;#%02x%02x%02x\x1b\\", index, r>>8, g>>8, b>>8)
		}
	}
	for index, c := range []color.Color{e.fgColor, e.bgColor, e.curColor} {
		if c != nil {
			r, g, b, _ := c.RGBA()
			fmt.Fprintf(&out, "\x1b]%d;#%02x%02x%02x\x1b\\", 10+index, r>>8, g>>8, b>>8)
		}
	}
	normal := &e.scrs[0]
	history := normal.scrollback
	truncated := history != nil && history.evicted
	var lines []checkpointRow
	size := 0
	if history != nil {
		for i, line := range history.Lines() {
			lines = append(lines, checkpointRow{line, history.wrapped[i]})
			size += len(checkpointLine(line)) + 2
		}
	}
	// Leave room for both visible screens and state. Keep whole rendered rows.
	for len(lines) > 0 && maxBytes > 0 && size > maxBytes/2 {
		size -= len(checkpointLine(lines[0].cells)) + 2
		lines = lines[1:]
		truncated = true
	}
	for y := 0; y < normal.Height(); y++ {
		lines = append(lines, checkpointRow{normal.buf.Line(y), normal.wrapped[y]})
	}
	out.WriteString("\x1b[?7h")
	for i, row := range lines {
		if i > 0 && !row.wrapped {
			out.WriteString("\r\n")
		}
		if i+1 < len(lines) && lines[i+1].wrapped {
			line := fitCheckpointRow(row.cells, e.Width())
			end := e.Width()
			// A wide glyph wraps before the last null cell. Recreate that
			// wrap naturally instead of turning the unused cell into a space.
			if end > 0 && line[end-1].Content == "" && line[end-1].Width == 1 && len(lines[i+1].cells) > 0 && lines[i+1].cells[0].Width == 2 {
				end--
			}
			out.WriteString(checkpointLineEnd(line, end))
		} else {
			out.WriteString(checkpointLine(row.cells))
		}
	}
	out.WriteString("\x1b[?7l")
	out.WriteString("\x1b[0m")
	// Install tab stops while origin and margins are still at their defaults.
	out.WriteString("\x1b[3g")
	for x := 0; x < e.Width(); x++ {
		if e.tabstops.IsStop(x) {
			fmt.Fprintf(&out, "\x1b[1;%dH\x1bH", x+1)
		}
	}
	e.checkpointCursor(&out, normal)
	if e.IsAltScreen() {
		out.WriteString("\x1b[?47h\x1b[?6l\x1b[?69l\x1b[r\x1b[?7l")
		for y := 0; y < e.Height(); y++ {
			fmt.Fprintf(&out, "\x1b[%d;1H", y+1)
			out.WriteString(checkpointLine(e.scr.buf.Line(y)))
		}
		e.checkpointCursor(&out, e.scr)
	}
	// Restore modes only after painting, so insert/origin/mouse modes cannot
	// interfere with reconstruction. Screen switching is handled above.
	modes := make([]ansi.Mode, 0, len(e.modes))
	for mode := range e.modes {
		modes = append(modes, mode)
	}
	sort.Slice(modes, func(i, j int) bool {
		return fmt.Sprintf("%T:%08d", modes[i], modes[i].Mode()) < fmt.Sprintf("%T:%08d", modes[j], modes[j].Mode())
	})
	for _, mode := range modes {
		if _, dec := mode.(ansi.DECMode); dec {
			switch mode.Mode() {
			case 6, 47, 69, 1047, 1048, 1049:
				continue
			}
		}
		if e.modes[mode].IsSet() {
			out.WriteString(ansi.SetMode(mode))
		} else {
			out.WriteString(ansi.ResetMode(mode))
		}
	}
	// Character sets are restored after drawing Unicode cells.
	for index, set := range e.charsets {
		designator := byte('B')
		if reflect.DeepEqual(set, SpecialDrawing) {
			designator = '0'
		} else if reflect.DeepEqual(set, UK) {
			designator = 'A'
		}
		fmt.Fprintf(&out, "\x1b%c%c", "()*+"[index], designator)
	}
	if e.gl == 1 {
		out.WriteByte(0x0e)
	} else {
		out.WriteByte(0x0f)
	}
	if e.gl == 2 {
		out.WriteString("\x1bn")
	} else if e.gl == 3 {
		out.WriteString("\x1bo")
	}
	switch e.gr {
	case 1:
		out.WriteString("\x1b~")
	case 2:
		out.WriteString("\x1b}")
	case 3:
		out.WriteString("\x1b|")
	}
	// CUP clears delayed wrap. Reprinting the rightmost grapheme restores it
	// without changing the picture; the next live character then wraps once.
	if e.atPhantom && e.isModeSet(ansi.ModeAutoWrap) {
		x, y := e.scr.cur.X, e.scr.cur.Y
		for x > 0 && e.scr.CellAt(x, y).Width == 0 {
			x--
		}
		e.checkpointPosition(&out, e.scr, x, y)
		cell := e.scr.CellAt(x, y)
		out.WriteString(cell.Style.String())
		out.WriteString(cell.Content)
		out.WriteString("\x1b[0m")
		out.WriteString(e.scr.cur.Pen.String())
	}
	if e.gsingle == 2 {
		out.WriteString("\x1bN")
	} else if e.gsingle == 3 {
		out.WriteString("\x1bO")
	}
	out.Write(e.snapshotPending)
	return []byte(out.String()), truncated
}

func checkpointLine(line uv.Line) string {
	end := len(line)
	for end > 0 {
		cell := line[end-1]
		if cell.Content != "" || !cell.Style.IsZero() {
			break
		}
		end--
	}
	return checkpointLineEnd(line, end)
}

func checkpointLineEnd(line uv.Line, end int) string {
	var out strings.Builder
	var style uv.Style
	for _, cell := range line[:end] {
		if cell.Width == 0 {
			continue
		}
		if !cell.Style.Equal(&style) {
			out.WriteString(cell.Style.Diff(&style))
			style = cell.Style
		}
		if cell.Content == "" {
			out.WriteByte(' ')
		} else {
			out.WriteString(cell.Content)
		}
	}
	if !style.IsZero() {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}

func (e *Emulator) checkpointPosition(out *strings.Builder, s *Screen, x, y int) {
	if e.isModeSet(ansi.ModeOrigin) {
		x -= s.scroll.Min.X
		y -= s.scroll.Min.Y
	}
	fmt.Fprintf(out, "\x1b[%d;%dH", max(y, 0)+1, max(x, 0)+1)
}

func (e *Emulator) checkpointCursor(out *strings.Builder, s *Screen) {
	out.WriteString("\x1b[?6l")
	fmt.Fprintf(out, "\x1b[%d;%dr", s.scroll.Min.Y+1, s.scroll.Max.Y)
	if e.isModeSet(ansi.ModeLeftRightMargin) {
		fmt.Fprintf(out, "\x1b[?69h\x1b[%d;%ds", s.scroll.Min.X+1, s.scroll.Max.X)
	}
	if e.isModeSet(ansi.ModeOrigin) {
		out.WriteString("\x1b[?6h")
	}
	e.checkpointPosition(out, s, s.saved.X, s.saved.Y)
	out.WriteString("\x1b[0m")
	out.WriteString(s.saved.Pen.String())
	out.WriteString("\x1b7")
	e.checkpointPosition(out, s, s.cur.X, s.cur.Y)
	out.WriteString("\x1b[0m")
	out.WriteString(s.cur.Pen.String())
	if s.cur.Hidden {
		out.WriteString("\x1b[?25l")
	} else {
		out.WriteString("\x1b[?25h")
	}
	style := int(s.cur.Style)*2 + 1
	if s.cur.Steady {
		style++
	}
	fmt.Fprintf(out, "\x1b[%d q", style)
}
