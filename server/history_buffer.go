package server

import "sync"

type HistoryBuffer struct {
	messages  [][]byte
	totalSize int
	maxSize   int
	mu        sync.RWMutex
}

func NewHistoryBuffer(maxSize int) *HistoryBuffer {
	return &HistoryBuffer{
		messages:  make([][]byte, 0),
		totalSize: 0,
		maxSize:   maxSize,
	}
}

func (h *HistoryBuffer) Append(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	msg := make([]byte, len(data))
	copy(msg, data)

	h.messages = append(h.messages, msg)
	h.totalSize += len(msg)

	for h.totalSize > h.maxSize && len(h.messages) > 0 {
		h.totalSize -= len(h.messages[0])
		h.messages = h.messages[1:]
	}

	// Compact: if we've evicted more than half the capacity, reallocate
	// to release the unreachable prefix of the underlying array.
	if cap(h.messages) > 2*len(h.messages) && cap(h.messages) > 64 {
		compacted := make([][]byte, len(h.messages))
		copy(compacted, h.messages)
		h.messages = compacted
	}
}

func (h *HistoryBuffer) GetAll() [][]byte {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make([][]byte, len(h.messages))
	for i, msg := range h.messages {
		result[i] = make([]byte, len(msg))
		copy(result[i], msg)
	}
	return result
}

// GetLastN returns the last n messages from the buffer.
func (h *HistoryBuffer) GetLastN(n int) [][]byte {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if n <= 0 {
		return nil
	}

	start := len(h.messages) - n
	if start < 0 {
		start = 0
	}
	result := make([][]byte, len(h.messages)-start)
	for i, msg := range h.messages[start:] {
		result[i] = make([]byte, len(msg))
		copy(result[i], msg)
	}
	return result
}
