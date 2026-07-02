package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

type historyChunk struct {
	startOffset int64
	data        []byte
}

type HistoryBuffer struct {
	chunks     []historyChunk
	totalSize  int
	maxSize    int
	headOffset int64
	tailOffset int64
	epoch      string
	mu         sync.RWMutex
}

func NewHistoryBuffer(maxSize int) *HistoryBuffer {
	return &HistoryBuffer{
		chunks: make([]historyChunk, 0),
		maxSize: maxSize,
		epoch:   newHistoryEpoch(),
	}
}

func (h *HistoryBuffer) AppendRaw(data []byte) {
	if len(data) == 0 {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	start := h.tailOffset
	h.tailOffset += int64(len(data))

	if h.maxSize <= 0 {
		h.chunks = h.chunks[:0]
		h.totalSize = 0
		h.headOffset = h.tailOffset
		return
	}

	chunkData := append([]byte(nil), data...)
	if len(chunkData) > h.maxSize {
		chunkData = append([]byte(nil), chunkData[len(chunkData)-h.maxSize:]...)
		start = h.tailOffset - int64(len(chunkData))
	}

	h.chunks = append(h.chunks, historyChunk{
		startOffset: start,
		data:        chunkData,
	})
	h.totalSize += len(chunkData)
	h.evictLocked()
}

func (h *HistoryBuffer) GetSince(offset int64) ([]byte, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if offset < h.headOffset || offset > h.tailOffset {
		return nil, false
	}
	if offset == h.tailOffset {
		return []byte{}, true
	}
	return h.copyRangeLocked(offset, h.tailOffset), true
}

func (h *HistoryBuffer) GetTail(maxBytes int) ([]byte, int64) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	from := h.headOffset
	if maxBytes > 0 {
		remaining := maxBytes
		from = h.tailOffset
		for i := len(h.chunks) - 1; i >= 0; i-- {
			chunk := h.chunks[i]
			chunkSize := len(chunk.data)
			if chunkSize <= remaining {
				from = chunk.startOffset
				remaining -= chunkSize
				continue
			}
			if from == h.tailOffset {
				from = chunk.startOffset + int64(chunkSize-remaining)
			}
			break
		}
	}
	if from > h.tailOffset {
		from = h.tailOffset
	}
	return h.copyRangeLocked(from, h.tailOffset), from
}

func (h *HistoryBuffer) Offsets() (string, int64, int64) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.epoch, h.headOffset, h.tailOffset
}

func (h *HistoryBuffer) Clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.chunks = h.chunks[:0]
	h.totalSize = 0
	h.headOffset = 0
	h.tailOffset = 0
	h.epoch = newHistoryEpoch()
}

func (h *HistoryBuffer) evictLocked() {
	for h.totalSize > h.maxSize && len(h.chunks) > 0 {
		over := h.totalSize - h.maxSize
		first := &h.chunks[0]
		if over >= len(first.data) {
			h.totalSize -= len(first.data)
			h.headOffset = first.startOffset + int64(len(first.data))
			h.chunks = h.chunks[1:]
			continue
		}

		first.data = append([]byte(nil), first.data[over:]...)
		first.startOffset += int64(over)
		h.totalSize -= over
		h.headOffset = first.startOffset
		break
	}

	if len(h.chunks) == 0 {
		h.headOffset = h.tailOffset
	}

	if cap(h.chunks) > 2*len(h.chunks) && cap(h.chunks) > 64 {
		compacted := make([]historyChunk, len(h.chunks))
		copy(compacted, h.chunks)
		h.chunks = compacted
	}
}

func (h *HistoryBuffer) copyRangeLocked(from, to int64) []byte {
	if from >= to {
		return []byte{}
	}

	result := make([]byte, 0, to-from)
	for _, chunk := range h.chunks {
		chunkStart := chunk.startOffset
		chunkEnd := chunk.startOffset + int64(len(chunk.data))
		if chunkEnd <= from {
			continue
		}
		if chunkStart >= to {
			break
		}

		start := int64(0)
		if from > chunkStart {
			start = from - chunkStart
		}
		end := int64(len(chunk.data))
		if to < chunkEnd {
			end = to - chunkStart
		}
		if start < end {
			result = append(result, chunk.data[start:end]...)
		}
	}
	return result
}

func newHistoryEpoch() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
