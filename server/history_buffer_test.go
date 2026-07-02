package server

import (
	"bytes"
	"testing"
)

func TestHistoryBufferAppendRawOffsetsAndGetSince(t *testing.T) {
	h := NewHistoryBuffer(1024)
	h.AppendRaw([]byte("hello"))
	h.AppendRaw([]byte(" world"))

	epoch, head, tail := h.Offsets()
	if epoch == "" {
		t.Fatal("epoch is empty")
	}
	if head != 0 || tail != 11 {
		t.Fatalf("offsets = %d/%d, want 0/11", head, tail)
	}

	data, ok := h.GetSince(6)
	if !ok {
		t.Fatal("GetSince returned ok=false")
	}
	if !bytes.Equal(data, []byte("world")) {
		t.Fatalf("GetSince data = %q, want %q", data, "world")
	}

	if _, ok := h.GetSince(12); ok {
		t.Fatal("GetSince past tail returned ok=true")
	}
}

func TestHistoryBufferEvictionUpdatesHeadOffset(t *testing.T) {
	h := NewHistoryBuffer(5)
	h.AppendRaw([]byte("abc"))
	h.AppendRaw([]byte("def"))

	_, head, tail := h.Offsets()
	if head != 1 || tail != 6 {
		t.Fatalf("offsets = %d/%d, want 1/6", head, tail)
	}
	if _, ok := h.GetSince(0); ok {
		t.Fatal("GetSince with evicted offset returned ok=true")
	}
	data, ok := h.GetSince(1)
	if !ok {
		t.Fatal("GetSince at head returned ok=false")
	}
	if !bytes.Equal(data, []byte("bcdef")) {
		t.Fatalf("GetSince data = %q, want %q", data, "bcdef")
	}
}

func TestHistoryBufferGetTailAndClear(t *testing.T) {
	h := NewHistoryBuffer(1024)
	initialEpoch, _, _ := h.Offsets()
	h.AppendRaw([]byte("abcdef"))

	data, from := h.GetTail(3)
	if from != 3 {
		t.Fatalf("tail from = %d, want 3", from)
	}
	if !bytes.Equal(data, []byte("def")) {
		t.Fatalf("tail data = %q, want %q", data, "def")
	}

	h.Clear()
	nextEpoch, head, tail := h.Offsets()
	if nextEpoch == "" || nextEpoch == initialEpoch {
		t.Fatalf("epoch did not rotate: %q -> %q", initialEpoch, nextEpoch)
	}
	if head != 0 || tail != 0 {
		t.Fatalf("offsets after clear = %d/%d, want 0/0", head, tail)
	}
}

func TestHistoryBufferGetTailPrefersChunkBoundary(t *testing.T) {
	h := NewHistoryBuffer(1024)
	h.AppendRaw([]byte("abc"))
	h.AppendRaw([]byte("def"))
	h.AppendRaw([]byte("ghi"))

	data, from := h.GetTail(5)
	if from != 6 {
		t.Fatalf("tail from = %d, want 6", from)
	}
	if !bytes.Equal(data, []byte("ghi")) {
		t.Fatalf("tail data = %q, want %q", data, "ghi")
	}

	data, from = h.GetTail(6)
	if from != 3 {
		t.Fatalf("tail from = %d, want 3", from)
	}
	if !bytes.Equal(data, []byte("defghi")) {
		t.Fatalf("tail data = %q, want %q", data, "defghi")
	}
}

func TestSessionReplaySnapshotResumeAndTail(t *testing.T) {
	h := NewHistoryBuffer(1024)
	h.AppendRaw([]byte("abcdef"))
	epoch, _, tail := h.Offsets()
	sm := &SessionManager{
		history:            h,
		historyReplayBytes: 3,
	}

	resume := sm.buildReplaySnapshot(InitMessage{Epoch: epoch, LastOffset: 2})
	if resume.Mode != "resume" || resume.FromOffset != 2 || resume.EndOffset != tail {
		t.Fatalf("resume snapshot = %#v", resume)
	}
	if !bytes.Equal(resume.Data, []byte("cdef")) {
		t.Fatalf("resume data = %q, want %q", resume.Data, "cdef")
	}

	tailReplay := sm.buildReplaySnapshot(InitMessage{Epoch: "old", LastOffset: 2})
	if tailReplay.Mode != "tail" || tailReplay.FromOffset != 3 || tailReplay.EndOffset != tail {
		t.Fatalf("tail snapshot = %#v", tailReplay)
	}
	if !bytes.Equal(tailReplay.Data, []byte("def")) {
		t.Fatalf("tail data = %q, want %q", tailReplay.Data, "def")
	}

	noOffsetReplay := sm.buildReplaySnapshot(InitMessage{})
	if noOffsetReplay.Mode != "tail" || noOffsetReplay.FromOffset != 3 || noOffsetReplay.EndOffset != tail {
		t.Fatalf("no-offset snapshot = %#v", noOffsetReplay)
	}

	evictedOffsetReplay := sm.buildReplaySnapshot(InitMessage{Epoch: epoch, LastOffset: tail + 1})
	if evictedOffsetReplay.Mode != "tail" || evictedOffsetReplay.FromOffset != 3 || evictedOffsetReplay.EndOffset != tail {
		t.Fatalf("invalid-offset snapshot = %#v", evictedOffsetReplay)
	}
}

func TestSessionManagerBroadcastGenerationFiltering(t *testing.T) {
	sm := &SessionManager{generation: 2}

	if !sm.isStaleBroadcast(newOutputBroadcastWithGeneration([]byte("old"), 1)) {
		t.Fatal("old generation broadcast was not marked stale")
	}

	if sm.isStaleBroadcast(newOutputBroadcastWithGeneration([]byte("current"), 2)) {
		t.Fatal("current generation broadcast was marked stale")
	}

	if sm.isStaleBroadcast(newOutputBroadcast([]byte("unknown"))) {
		t.Fatal("generation-less broadcast was marked stale")
	}
}
