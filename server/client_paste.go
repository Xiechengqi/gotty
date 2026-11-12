package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"
)

const pasteChunkSize = 4096
const pasteMaxSize = 64 * 1024 * 1024
const pasteIdleTimeout = 30 * time.Second

type contextWriter interface {
	WriteContext(context.Context, []byte) (int, error)
}

type pasteRequest struct {
	ID        string `json:"id"`
	Seq       int    `json:"seq"`
	Size      int    `json:"size"`
	Bracketed bool   `json:"bracketed"`
	Data      string `json:"data"`
	Final     bool   `json:"final"`
	Cancel    bool   `json:"cancel"`
}

type pasteResult struct {
	ID       string `json:"id"`
	Seq      int    `json:"seq"`
	Received int    `json:"received"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type pasteChunk struct {
	seq   int
	data  []byte
	final bool
}

// Receiver bookkeeping is protected by sm.mu; only the worker writes to PTY.
// One paste owns the shared PTY, preventing other users/API input from being
// accidentally inserted inside its bracketed text. Output and replies flow.
type clientPaste struct {
	client             *Client
	id                 string
	size, next, queued int
	bracketed, final   bool
	ctx                context.Context
	cancel             context.CancelFunc
	chunks             chan pasteChunk
	replies            chan []byte
	writer             contextWriter
	release            func()
	failure            string // Protected by sm.mu.
}

func (sm *SessionManager) pasteSupported() bool {
	if sm == nil {
		return false
	}
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	_, ok := sm.slave.(contextWriter)
	return ok
}

func (sm *SessionManager) pasteReplyLocked(client *Client, result pasteResult) {
	if !sm.clients[client] {
		return
	}
	data, _ := json.Marshal(result)
	// Never race a send with removeClientLocked closing the channel. Overflow
	// closes this client, cancels its paste and cannot become false success.
	select {
	case client.send <- append([]byte{'e'}, data...):
	default:
		sm.removeClientLocked(client, time.Now())
	}
}

func (server *Server) handlePaste(client *Client, payload []byte) {
	sm := server.sessionManager
	var req pasteRequest
	if len(payload) > 8192 || json.Unmarshal(payload, &req) != nil || len(req.ID) == 0 || len(req.ID) > 64 {
		return
	}
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if client == nil || !sm.clients[client] {
		return
	}
	reject := func(reason string) {
		sm.pasteReplyLocked(client, pasteResult{ID: req.ID, Seq: req.Seq, Status: "error", Error: reason})
	}
	if client.readOnly || !server.options.PermitWrite {
		reject("Paste permission denied")
		return
	}
	p := sm.paste
	if req.Cancel {
		if p != nil && p.client == client && p.id == req.ID {
			p.cancel()
		}
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil || len(data) == 0 || len(data) > pasteChunkSize || req.Size <= 0 || req.Size > pasteMaxSize || req.Seq < 0 {
		if p != nil && p.client == client && p.id == req.ID {
			p.failure = "Invalid paste request"
			p.cancel()
		} else {
			reject("Invalid paste request")
		}
		return
	}
	if p == nil {
		if sm.userWrites > 0 {
			reject("Terminal is busy")
			return
		}
		writer, ok := sm.slave.(contextWriter)
		if req.Seq != 0 || !ok {
			reject("Invalid paste request")
			return
		}
		release := func() {}
		if server.terminalStatus != nil {
			var accepted bool
			release, accepted = server.terminalStatus.beginPaste()
			if !accepted {
				reject("Terminal is busy")
				return
			}
		}
		ctx, cancel := context.WithCancel(sm.ctx)
		p = &clientPaste{client: client, id: req.ID, size: req.Size, bracketed: req.Bracketed, ctx: ctx, cancel: cancel, chunks: make(chan pasteChunk, 8), replies: make(chan []byte, 16), writer: writer, release: release}
		sm.paste = p
		go sm.runPaste(p)
	}
	if p.client != client || p.id != req.ID {
		reject("Terminal is busy")
		return
	}
	if p.ctx.Err() != nil {
		return
	}
	if p.next != req.Seq || p.size != req.Size || p.bracketed != req.Bracketed || p.final || p.queued+len(data) > p.size || req.Final != (p.queued+len(data) == p.size) {
		p.failure = "Invalid paste request"
		p.cancel()
		return
	}
	select {
	case p.chunks <- pasteChunk{seq: req.Seq, data: data, final: req.Final}:
		client.lastActivity = time.Now()
		p.next++
		p.queued += len(data)
		p.final = req.Final
	default:
		p.failure = "Paste queue is full"
		p.cancel()
	}
}

func writePaste(ctx context.Context, writer contextWriter, data []byte) (int, error) {
	total := 0
	for total < len(data) {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := writer.WriteContext(ctx, data[total:])
		if n < 0 || n > len(data)-total {
			return total, io.ErrShortWrite
		}
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (sm *SessionManager) runPaste(p *clientPaste) {
	result := pasteResult{ID: p.id, Seq: -1, Status: "cancelled"}
	opened := false
	defer func() {
		sm.mu.Lock()
		if p.failure != "" {
			result.Status, result.Error = "error", p.failure
		}
		sm.mu.Unlock()
		// Cleanup is bounded and uses the original PTY, even across restart.
		if opened {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_, err := writePaste(ctx, p.writer, []byte("\x1b[201~"))
			cancel()
			if err != nil {
				result.Status, result.Error = "error", "Paste stopped; bracket cleanup failed"
			}
		}
		// Deliver replies generated during the final chunk after the closing
		// marker. Do not strand a waiting application when the task completes.
		p.cancel()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for {
			sm.mu.Lock()
			if len(p.replies) == 0 || ctx.Err() != nil {
				if len(p.replies) > 0 {
					result.Status, result.Error = "error", "Paste write failed"
				}
				if sm.paste == p {
					sm.paste = nil
				}
				p.release()
				sm.pasteReplyLocked(p.client, result)
				sm.mu.Unlock()
				return
			}
			reply := <-p.replies
			sm.mu.Unlock()
			if _, err := writePaste(ctx, p.writer, reply); err != nil {
				result.Status, result.Error = "error", "Paste write failed"
			}
		}
	}()
	write := func(data []byte) (int, error) {
		ctx, cancel := context.WithTimeout(p.ctx, pasteIdleTimeout)
		defer cancel()
		return writePaste(ctx, p.writer, data)
	}
	if p.bracketed {
		n, err := write([]byte("\x1b[200~"))
		opened = n > 0
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				result.Status, result.Error = "error", "Paste write failed"
			}
			return
		}
	}
	for {
		if p.ctx.Err() != nil {
			return
		}
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(pasteIdleTimeout):
			result.Status, result.Error = "error", "Paste input timed out"
			return
		case reply := <-p.replies:
			if _, err := write(reply); err != nil {
				result.Status, result.Error = "error", "Paste write failed"
				return
			}
		case chunk := <-p.chunks:
			n, err := write(chunk.data)
			result.Seq = chunk.seq
			result.Received += n
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					result.Status, result.Error = "error", "Paste write failed"
				}
				return
			}
			if chunk.final {
				result.Status = "done"
				return
			}
			sm.mu.Lock()
			sm.pasteReplyLocked(p.client, pasteResult{ID: p.id, Seq: chunk.seq, Received: result.Received, Status: "writing"})
			sm.mu.Unlock()
		}
	}
}
