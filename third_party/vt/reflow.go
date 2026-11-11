package vt

import (
	uv "github.com/charmbracelet/ultraviolet"
)

type checkpointRow struct {
	cells   uv.Line
	wrapped bool
}

// resizeNormal follows xterm's normal-buffer contract: preserve scrollback,
// reflow wrapped logical lines, and leave the cursor's logical line to the
// application (xterm's default reflowCursorLine=false). The alternate screen is
// cropped separately and never contributes scrollback.
func (e *Emulator) resizeNormal(width, height int) {
	s := &e.scrs[0]
	oldWidth, oldHeight := s.Width(), s.Height()
	history := s.scrollback
	var rows []checkpointRow
	if history != nil {
		for i, line := range history.lines {
			rows = append(rows, checkpointRow{line, history.wrapped[i]})
		}
	}
	base := len(rows)
	for y := 0; y < oldHeight; y++ {
		rows = append(rows, checkpointRow{s.buf.Line(y), s.wrapped[y]})
	}
	cursor := base + s.cur.Y
	saved := base + s.saved.Y
	// Shrinking first removes rows below the cursor. Remaining overflow moves
	// into scrollback. Growing pulls history back only when the cursor is at
	// the bottom, matching xterm's viewport behavior.
	for h := oldHeight; h > height; h-- {
		if len(rows) > cursor+1 {
			rows = rows[:len(rows)-1]
		} else {
			base++
		}
	}
	for h := oldHeight; h < height; h++ {
		if base > 0 && len(rows) <= cursor+1 {
			base--
		} else {
			rows = append(rows, checkpointRow{})
		}
	}
	var resized []checkpointRow
	newCursor, newSaved := cursor, saved
	for start := 0; start < len(rows); {
		end := start + 1
		for end < len(rows) && rows[end].wrapped {
			end++
		}
		first := len(resized)
		if width == oldWidth || cursor >= start && cursor < end {
			for _, row := range rows[start:end] {
				resized = append(resized, checkpointRow{fitCheckpointRow(row.cells, width), row.wrapped})
			}
		} else {
			var cells uv.Line
			for i := start; i < end; i++ {
				line := rows[i].cells
				length := trimmedCheckpointLength(line)
				if i+1 < end {
					length = oldWidth
					// A wide glyph moved to the next row leaves one null cell
					// at the right edge; that cell is not logical text.
					if len(line) >= oldWidth && line[oldWidth-1].Content == "" && line[oldWidth-1].Width == 1 && len(rows[i+1].cells) > 0 && rows[i+1].cells[0].Width == 2 {
						length--
					}
				}
				for x := 0; x < length; x++ {
					cell := checkpointNullCell
					if x < len(line) {
						cell = line[x]
					}
					cells = append(cells, cell)
				}
			}
			if len(cells) == 0 {
				resized = append(resized, checkpointRow{fitCheckpointRow(nil, width), rows[start].wrapped})
			}
			for len(cells) > 0 {
				take := min(width, len(cells))
				if take < len(cells) && take > 0 && cells[take-1].Width == 2 {
					take--
				}
				if take == 0 {
					take = min(width, len(cells))
				}
				resized = append(resized, checkpointRow{fitCheckpointRow(cells[:take], width), len(resized) > first || rows[start].wrapped})
				cells = cells[take:]
			}
		}
		if cursor >= start && cursor < end {
			newCursor = first + cursor - start
		}
		if saved >= start && saved < end {
			newSaved = first + min(saved-start, len(resized)-first-1)
		}
		start = end
	}
	for len(resized) < height {
		resized = append(resized, checkpointRow{fitCheckpointRow(nil, width), false})
	}
	base = max(0, len(resized)-height)
	// Content below a cursor must not push that cursor outside the viewport.
	if base > newCursor {
		base = max(0, newCursor)
		resized = resized[:base+height]
	}
	evicted := history != nil && history.evicted
	if history != nil {
		history.Clear()
		for _, row := range resized[:base] {
			history.PushWrapped(row.cells, row.wrapped)
		}
		history.evicted = history.evicted || evicted
	}
	s.buf.Resize(width, height)
	s.wrapped = make([]bool, height)
	for y, row := range resized[base : base+height] {
		copy(s.buf.Line(y), row.cells)
		s.wrapped[y] = row.wrapped
	}
	s.cur.X, s.cur.Y = min(s.cur.X, width-1), max(0, min(newCursor-base, height-1))
	s.saved.X, s.saved.Y = min(s.saved.X, width-1), max(0, min(newSaved-base, height-1))
	s.scroll = s.buf.Bounds()
}

func trimmedCheckpointLength(line uv.Line) int {
	end := len(line)
	for end > 0 {
		c := line[end-1]
		if c.Content != "" || !c.Style.IsZero() || c.Link.URL != "" {
			// A wide glyph occupies its following width-zero cell too. Dropping
			// that cell makes a later reflow split or discard the final glyph.
			end = min(len(line), end-1+max(1, c.Width))
			break
		}
		end--
	}
	return end
}
func fitCheckpointRow(line uv.Line, width int) uv.Line {
	result := make(uv.Line, width)
	for i := range result {
		result[i] = checkpointNullCell
	}
	copy(result, line[:min(width, len(line))])
	// Width-zero cells represent wide-character continuations. Empty cells
	// created by resizing must remain ordinary blanks in a checkpoint.
	for x := range result {
		if result[x].Width == 0 && result[x].Content == "" && (x == 0 || result[x-1].Width != 2) {
			result[x] = checkpointNullCell
		}
	}
	if width > 0 && result[width-1].Width == 2 {
		result[width-1] = checkpointNullCell
	}
	return result
}
