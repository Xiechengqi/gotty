package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/sorenisanerd/gotty/webtty"
)

func TestSendReplayMessagesTailFrames(t *testing.T) {
	data := append(bytes.Repeat([]byte("x"), replayChunkSize), []byte("end")...)
	replay := replaySnapshot{
		Epoch:      "epoch-1",
		Mode:       "tail",
		FromOffset: 12,
		EndOffset:  int64(12 + len(data)),
		Data:       data,
	}

	messages := captureReplayMessages(t, replay)
	if len(messages) != 5 {
		t.Fatalf("message count = %d, want 5", len(messages))
	}

	begin := decodeReplayBegin(t, messages[0])
	if begin.Epoch != replay.Epoch || begin.Mode != replay.Mode || begin.FromOffset != replay.FromOffset {
		t.Fatalf("ReplayBegin = %#v, want epoch=%q mode=%q from=%d", begin, replay.Epoch, replay.Mode, replay.FromOffset)
	}

	ris := decodeReplayOutput(t, messages[1])
	if !bytes.Equal(ris, []byte("\x1bc")) {
		t.Fatalf("RIS output = %q, want ESC c", ris)
	}

	firstChunk := decodeReplayOutput(t, messages[2])
	if !bytes.Equal(firstChunk, data[:replayChunkSize]) {
		t.Fatalf("first replay chunk mismatch: got %d bytes", len(firstChunk))
	}

	secondChunk := decodeReplayOutput(t, messages[3])
	if !bytes.Equal(secondChunk, data[replayChunkSize:]) {
		t.Fatalf("second replay chunk = %q, want %q", secondChunk, data[replayChunkSize:])
	}

	end := decodeReplayEnd(t, messages[4])
	if end.EndOffset != replay.EndOffset {
		t.Fatalf("ReplayEnd offset = %d, want %d", end.EndOffset, replay.EndOffset)
	}
}

func TestSendReplayMessagesResumeFrames(t *testing.T) {
	replay := replaySnapshot{
		Epoch:      "epoch-2",
		Mode:       "resume",
		FromOffset: 7,
		EndOffset:  13,
		Data:       []byte("resume"),
	}

	messages := captureReplayMessages(t, replay)
	if len(messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(messages))
	}

	begin := decodeReplayBegin(t, messages[0])
	if begin.Mode != "resume" || begin.FromOffset != replay.FromOffset {
		t.Fatalf("ReplayBegin = %#v", begin)
	}

	output := decodeReplayOutput(t, messages[1])
	if !bytes.Equal(output, replay.Data) {
		t.Fatalf("resume output = %q, want %q", output, replay.Data)
	}

	end := decodeReplayEnd(t, messages[2])
	if end.EndOffset != replay.EndOffset {
		t.Fatalf("ReplayEnd offset = %d, want %d", end.EndOffset, replay.EndOffset)
	}
}

func captureReplayMessages(t *testing.T, replay replaySnapshot) [][]byte {
	t.Helper()

	writeDone := make(chan error, 1)
	upgrader := websocket.Upgrader{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			writeDone <- err
			return
		}
		defer conn.Close()
		writeDone <- (&Server{}).sendReplayMessages(context.Background(), conn, replay)
	}))
	defer ts.Close()

	conn := dialTestWebSocket(t, ts.URL)
	defer conn.Close()

	var messages [][]byte
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("failed to read replay message: %v", err)
		}
		messages = append(messages, append([]byte(nil), message...))
		if len(message) > 0 && message[0] == webtty.ReplayEnd {
			break
		}
	}

	if err := <-writeDone; err != nil {
		t.Fatalf("sendReplayMessages returned error: %v", err)
	}
	return messages
}

func decodeReplayBegin(t *testing.T, message []byte) replayBeginPayload {
	t.Helper()
	if len(message) == 0 || message[0] != webtty.ReplayBegin {
		t.Fatalf("message = %q, want ReplayBegin", message)
	}
	var payload replayBeginPayload
	if err := json.Unmarshal(message[1:], &payload); err != nil {
		t.Fatalf("failed to decode ReplayBegin: %v", err)
	}
	return payload
}

func decodeReplayEnd(t *testing.T, message []byte) replayEndPayload {
	t.Helper()
	if len(message) == 0 || message[0] != webtty.ReplayEnd {
		t.Fatalf("message = %q, want ReplayEnd", message)
	}
	var payload replayEndPayload
	if err := json.Unmarshal(message[1:], &payload); err != nil {
		t.Fatalf("failed to decode ReplayEnd: %v", err)
	}
	return payload
}

func decodeReplayOutput(t *testing.T, message []byte) []byte {
	t.Helper()
	if len(message) == 0 || message[0] != webtty.Output {
		t.Fatalf("message = %q, want Output", message)
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(message)-1))
	n, err := base64.StdEncoding.Decode(decoded, message[1:])
	if err != nil {
		t.Fatalf("failed to decode output frame: %v", err)
	}
	return decoded[:n]
}
