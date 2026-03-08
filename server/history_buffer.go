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
