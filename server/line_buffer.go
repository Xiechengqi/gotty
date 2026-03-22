package server

import (
	"strings"
	"sync"
)

// LineBuffer stores recent terminal output as complete lines while preserving
// the current partial line separately.
type LineBuffer struct {
	mu       sync.RWMutex
	lines    []string
	partial  string
	maxLines int
}

func NewLineBuffer(maxLines int) *LineBuffer {
	if maxLines <= 0 {
		maxLines = 1
	}
	return &LineBuffer{
		lines:    make([]string, 0, maxLines),
		maxLines: maxLines,
	}
}

func (lb *LineBuffer) Append(data []byte) {
	if len(data) == 0 {
		return
	}

	lb.mu.Lock()
	defer lb.mu.Unlock()

	text := lb.partial + string(data)
	parts := strings.Split(text, "\n")
	for _, line := range parts[:len(parts)-1] {
		lb.appendLineLocked(strings.TrimRight(line, "\r"))
	}
	lb.partial = parts[len(parts)-1]
}

func (lb *LineBuffer) GetLastN(n int) []string {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	if n <= 0 {
		return nil
	}

	total := len(lb.lines)
	if lb.partial != "" {
		total++
	}
	if total == 0 {
		return nil
	}

	all := make([]string, 0, total)
	all = append(all, lb.lines...)
	if lb.partial != "" {
		all = append(all, strings.TrimRight(lb.partial, "\r"))
	}

	if n >= len(all) {
		return append([]string(nil), all...)
	}
	return append([]string(nil), all[len(all)-n:]...)
}

func (lb *LineBuffer) appendLineLocked(line string) {
	lb.lines = append(lb.lines, line)
	if len(lb.lines) > lb.maxLines {
		drop := len(lb.lines) - lb.maxLines
		lb.lines = append([]string(nil), lb.lines[drop:]...)
	} else if cap(lb.lines) > 2*len(lb.lines) && cap(lb.lines) > 64 {
		compacted := make([]string, len(lb.lines))
		copy(compacted, lb.lines)
		lb.lines = compacted
	}
}
