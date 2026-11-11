package server

// Hub binds this process' SessionManager directly to a terminal-only WebSocket transport.
// It deliberately never accepts a proxy target or connects to local HTTP endpoints.
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var errHubSuperseded = errors.New("Hub device connection superseded")

const hubMaxMessage = 1024 * 1024

type hubFrame struct {
	Kind   string          `json:"kind"`
	Stream string          `json:"stream,omitempty"`
	Data   json.RawMessage `json:"data"`
}
type hubBinding struct {
	URL        string `json:"url"`
	Instance   string `json:"instance_id"`
	Credential string `json:"credential"`
	Username   string `json:"username"`
	Name       string `json:"name,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	Claim      string `json:"claim,omitempty"`
	Paused     bool   `json:"paused,omitempty"`
}
type hubManager struct {
	server           *Server
	ctx              context.Context
	mu               sync.Mutex
	managementMu     sync.Mutex
	binding          hubBinding
	pending          hubBinding
	path             string
	stateLock        *os.File
	closeStateOnce   sync.Once
	stateNotice      string
	superseded       bool
	online           bool
	leases           map[string]time.Time
	lastOutput       time.Time
	attention        bool
	revision         uint64
	color            string
	wake             chan struct{}
	unseenOutput     time.Time
	cancelRun        context.CancelFunc
	connectionCancel context.CancelFunc
}

func hubRandom() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func newHubManager(ctx context.Context, s *Server) (*hubManager, error) {
	userDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return newHubManagerAt(ctx, s, userDir)
}

func newHubManagerAt(ctx context.Context, s *Server, userDir string) (_ *hubManager, err error) {
	p, legacy := hubStatePaths(s.options, userDir)
	lock, p, err := lockHubState(p)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			_ = lock.Close()
		}
	}()
	h := &hubManager{server: s, ctx: ctx, path: p, stateLock: lock, leases: map[string]time.Time{}, color: "gray", wake: make(chan struct{}, 1)}
	if b, e := os.ReadFile(p); e == nil {
		if e = os.Chmod(p, 0600); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &h.binding); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if b, e := os.ReadFile(p + ".pending"); e == nil {
		if e = os.Chmod(p+".pending", 0600); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(b, &h.pending); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	if h.binding.Instance == "" && legacy != "" {
		if _, e := os.Stat(legacy); e == nil {
			h.stateNotice = fmt.Sprintf("发现旧共享身份文件 %s，已保留且未自动复用。请先确认它所属的实例，再用 --hub-state-file 指定该文件恢复原绑定；其他实例应独立接入。", legacy)
			log.Printf("Hub uses isolated identity %s; legacy binding %s was preserved. Select it explicitly with --hub-state-file only after confirming its owner.", p, legacy)
		}
	}
	return h, nil
}
func (h *hubManager) saveLocked() error {
	return saveHubBinding(h.path, h.binding)
}
func saveHubBinding(path string, binding hubBinding) error {
	b, e := json.Marshal(binding)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".hub-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if closeErr := f.Close(); e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
func (h *hubManager) authorize(r *http.Request) bool {
	if h.server.isPublicShareRequest(r) {
		return false
	}
	return h.server.authorizeControlRequest(r)
}
func (h *hubManager) validateHub(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return "", errors.New("Hub URL must be an origin")
	}
	if u.Scheme != "https" && !(h.server.options.HubAllowHTTP && u.Scheme == "http") {
		return "", errors.New("HTTPS required")
	}
	return strings.TrimRight(raw, "/"), nil
}
func (h *hubManager) request(ctx context.Context, base, path string, body any) (map[string]json.RawMessage, error) {
	return h.deviceRequest(ctx, base, path, body, hubBinding{})
}
func (h *hubManager) deviceRequest(ctx context.Context, base, path string, body any, binding hubBinding) (map[string]json.RawMessage, error) {
	b, _ := json.Marshal(body)
	r, e := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	if binding.Instance != "" {
		r.Header.Set("X-Instance-ID", binding.Instance)
		r.Header.Set("Authorization", "Bearer "+binding.Credential)
	}
	client := http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(r)
	if e != nil {
		return nil, errors.New("Hub unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		var problem struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 8192)).Decode(&problem)
		return nil, &hubHTTPError{status: res.StatusCode, code: problem.Error}
	}
	var v map[string]json.RawMessage
	e = json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&v)
	return v, e
}
func hubString(v map[string]json.RawMessage, k string) string {
	var s string
	_ = json.Unmarshal(v[k], &s)
	return s
}

type hubHTTPError struct {
	status int
	code   string
}

func (e *hubHTTPError) Error() string { return fmt.Sprintf("Hub rejected request (%d)", e.status) }
func hubProblem(w http.ResponseWriter, status int, code, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "message": message})
}
func (h *hubManager) setViewing(id string, viewing bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	if viewing {
		h.leases[id] = now.Add(10 * time.Second)
		h.unseenOutput = time.Time{}
		h.attention = false
	} else if until, ok := h.leases[id]; ok && until.After(now) {
		h.leases[id] = now.Add(time.Second)
	} else {
		delete(h.leases, id)
	}
}
func (h *hubManager) activity() {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	h.lastOutput = now
	for _, until := range h.leases {
		if until.After(now) {
			return
		}
	}
	h.unseenOutput = now
	h.attention = true
}
func (h *hubManager) status() hubFrame {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	viewing := false
	for id, expires := range h.leases {
		if expires.After(now) {
			viewing = true
		} else {
			delete(h.leases, id)
		}
	}
	color := "gray"
	if viewing {
		color = "red"
		h.attention = false
		h.unseenOutput = time.Time{}
	} else if !h.unseenOutput.IsZero() && now.Sub(h.unseenOutput) < 6*time.Second {
		color = "yellow"
		h.attention = true
	} else if h.attention {
		color = "green"
	}
	if h.color != color {
		h.color = color
		h.revision++
	}
	if h.revision == 0 {
		h.revision = 1
	}
	b, _ := json.Marshal(map[string]any{"color": color, "revision": h.revision})
	return hubFrame{Kind: "status", Data: b}
}
func (h *hubManager) run() {
	defer h.closeState()
	backoff := time.Second
	for {
		if h.ctx.Err() != nil {
			return
		}
		h.mu.Lock()
		b := h.binding
		h.mu.Unlock()
		if b.Instance != "" && !b.Paused {
			e := h.connect(b)
			h.mu.Lock()
			h.online = false
			for id := range h.leases {
				if !strings.HasPrefix(id, "direct-") {
					delete(h.leases, id)
				}
			}
			h.mu.Unlock()
			if errors.Is(e, errHubSuperseded) {
				h.mu.Lock()
				h.superseded = true
				h.mu.Unlock()
				log.Printf("Hub identity is connected elsewhere; reconnect paused. Check for copied credentials or shared --hub-state-file paths.")
				select {
				case <-h.ctx.Done():
					return
				case <-h.wake:
				}
			} else if e != nil && h.ctx.Err() == nil {
				log.Printf("Hub connection interrupted; reconnect scheduled")
			}
			h.mu.Lock()
			h.online = false
			h.mu.Unlock()
		}
		timer := time.NewTimer(backoff + time.Duration(time.Now().UnixNano()%int64(time.Second)))
		select {
		case <-h.ctx.Done():
			timer.Stop()
			return
		case <-h.wake:
			timer.Stop()
			backoff = time.Second
		case <-timer.C:
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
	}
}
func (h *hubManager) connect(b hubBinding) error {
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	h.mu.Lock()
	if h.binding.Instance != b.Instance || h.binding.Paused {
		h.mu.Unlock()
		return nil
	}
	h.connectionCancel = cancel
	h.mu.Unlock()
	defer func() { h.mu.Lock(); h.connectionCancel = nil; h.mu.Unlock() }()
	if _, e := h.validateHub(b.URL); e != nil {
		return e
	}
	u, _ := url.Parse(b.URL)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path = "/connector/v1/connect"
	headers := http.Header{}
	headers.Set("X-Instance-ID", b.Instance)
	headers.Set("Authorization", "Bearer "+b.Credential)
	c, _, e := websocket.DefaultDialer.DialContext(ctx, u.String(), headers)
	if e != nil {
		return e
	}
	defer c.Close()
	c.SetReadLimit(hubMaxMessage)
	out := make(chan hubFrame, 128)
	send := func(f hubFrame) bool {
		select {
		case out <- f:
			return true
		case <-ctx.Done():
			return false
		default:
			cancel()
			return false
		}
	}
	go func() {
		for {
			select {
			case f := <-out:
				c.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if c.WriteJSON(f) != nil {
					cancel()
					c.Close()
					return
				}
			case <-ctx.Done():
				c.Close()
				return
			}
		}
	}()
	data, _ := json.Marshal(map[string]any{"protocol": 1, "capabilities": map[string]bool{"write": h.server.options.PermitWrite, "replay": true, "files": h.server.options.PermitWrite, "asr": h.server.options.EnableASR, "controls": true}})
	send(hubFrame{Kind: "hello", Data: data})
	var first hubFrame
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	if e = c.ReadJSON(&first); e != nil {
		return e
	}
	var hello struct {
		Protocol int `json:"protocol"`
	}
	if first.Kind != "hello_ack" || json.Unmarshal(first.Data, &hello) != nil || hello.Protocol != 1 {
		return errors.New("protocol rejected")
	}
	h.mu.Lock()
	if ctx.Err() != nil || h.binding.Instance != b.Instance || h.binding.Paused {
		h.mu.Unlock()
		return ctx.Err()
	}
	h.online = true
	h.superseded = false
	h.revision = 0
	h.mu.Unlock()
	asr := map[string]*websocket.Conn{}
	defer func() {
		for _, c := range asr {
			c.Close()
		}
	}()
	clients := map[string]*Client{}
	roles := map[string]string{}
	controlBusy := make(chan struct{}, 1)
	defer func() {
		for _, client := range clients {
			select {
			case h.server.sessionManager.unregister <- client:
			case <-h.ctx.Done():
			}
		}
	}()
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				if !send(h.status()) {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	c.SetPingHandler(func(p string) error {
		c.SetReadDeadline(time.Now().Add(45 * time.Second))
		return c.WriteControl(websocket.PongMessage, []byte(p), time.Now().Add(5*time.Second))
	})
	for {
		c.SetReadDeadline(time.Now().Add(45 * time.Second))
		var f hubFrame
		if e = c.ReadJSON(&f); e != nil {
			return e
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		switch f.Kind {
		case "superseded":
			return errHubSuperseded
		case "revoked":
			h.mu.Lock()
			if h.binding.Instance == b.Instance {
				h.binding = hubBinding{}
				_ = h.saveLocked()
			}
			h.mu.Unlock()
			return errors.New("binding revoked")
		case "rotate":
			var v struct {
				Credential string `json:"credential"`
			}
			if json.Unmarshal(f.Data, &v) != nil || len(v.Credential) != 64 {
				return errors.New("bad rotation")
			}
			h.mu.Lock()
			// A frame queued by the old Hub must not replace the credential of
			// a newly saved binding while that old connection is closing.
			if h.binding.Instance != b.Instance || h.binding.URL != b.URL {
				h.mu.Unlock()
				return errors.New("binding replaced")
			}
			previous := h.binding
			h.binding.Credential = v.Credential
			e = h.saveLocked()
			if e != nil {
				h.binding = previous
			}
			h.mu.Unlock()
			if e != nil {
				return e
			}
			fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(v.Credential)))
			ack, _ := json.Marshal(map[string]string{"fingerprint": fingerprint})
			send(hubFrame{Kind: "rotated", Data: ack})
		case "view":
			var v struct {
				Viewing bool `json:"viewing"`
			}
			_ = json.Unmarshal(f.Data, &v)
			if clients[f.Stream] != nil {
				h.setViewing(f.Stream, v.Viewing)
			}
		case "open":
			var meta struct {
				Channel string `json:"channel"`
				Role    string `json:"role"`
			}
			if json.Unmarshal(f.Data, &meta) != nil {
				return errors.New("bad open")
			}
			if f.Stream == "" || clients[f.Stream] != nil || asr[f.Stream] != nil ||
				(meta.Role != "owner" && meta.Role != "operator" && meta.Role != "observer") ||
				(meta.Channel != "asr" && meta.Channel != "terminal") {
				return errors.New("invalid stream")
			}
			if meta.Channel == "asr" {
				if !h.server.options.EnableASR || !h.server.options.PermitWrite || meta.Role == "observer" || len(asr) >= 16 {
					return errors.New("ASR unavailable")
				}
				dialer := *websocket.DefaultDialer
				dialer.HandshakeTimeout = 10 * time.Second
				upstream, _, err := dialer.DialContext(ctx, h.server.options.ASRBackend, nil)
				if err != nil {
					send(hubFrame{Kind: "close", Stream: f.Stream, Data: json.RawMessage(`{}`)})
					continue
				}
				asr[f.Stream] = upstream
				go func(id string, upstream *websocket.Conn) {
					defer upstream.Close()
					upstream.SetReadLimit(hubMaxMessage)
					for {
						typ, data, e := upstream.ReadMessage()
						if e != nil {
							send(hubFrame{Kind: "close", Stream: id, Data: json.RawMessage(`{}`)})
							return
						}
						payload := string(data)
						if typ == websocket.BinaryMessage {
							payload = base64.StdEncoding.EncodeToString(data)
						}
						body, _ := json.Marshal(map[string]any{"binary": typ == websocket.BinaryMessage, "payload": payload})
						if !send(hubFrame{Kind: "asr_data", Stream: id, Data: body}) {
							return
						}
					}
				}(f.Stream, upstream)
				continue
			}
			if len(clients) >= 64 || clients[f.Stream] != nil {
				return errors.New("stream limit")
			}
			var v struct {
				Role     string `json:"role"`
				Channel  string `json:"channel"`
				Writable bool   `json:"writable"`
				Epoch    string `json:"epoch"`
				Offset   int64  `json:"offset"`
			}
			if json.Unmarshal(f.Data, &v) != nil {
				return errors.New("bad open")
			}
			client := &Client{send: make(chan []byte, 256), ready: make(chan struct{}), readOnly: v.Role == "observer" || !v.Writable, init: InitMessage{Epoch: v.Epoch, LastOffset: v.Offset}}
			select {
			case h.server.sessionManager.register <- client:
			case <-ctx.Done():
				return ctx.Err()
			}
			clients[f.Stream] = client
			roles[f.Stream] = v.Role
			select {
			case <-client.ready:
			case <-ctx.Done():
				return ctx.Err()
			}
			go h.forward(ctx, f.Stream, client, send)
		case "control":
			if clients[f.Stream] == nil || len(f.Data) > maxShareAPIBodySize {
				return errors.New("invalid terminal control")
			}
			var req hubControlRequest
			if json.Unmarshal(f.Data, &req) != nil || req.ID == "" || len(req.ID) > 64 {
				return errors.New("bad terminal control")
			}
			select {
			case controlBusy <- struct{}{}:
				go func(stream, role string, req hubControlRequest) {
					defer func() { <-controlBusy }()
					if ctx.Err() != nil {
						return
					}
					result := h.terminalControl(role, req)
					body, _ := json.Marshal(result)
					send(hubFrame{Kind: "control_result", Stream: stream, Data: body})
				}(f.Stream, roles[f.Stream], req)
			default:
				body, _ := json.Marshal(hubControlResult{ID: req.ID, Error: "Another terminal operation is running"})
				send(hubFrame{Kind: "control_result", Stream: f.Stream, Data: body})
			}
		case "asr_data":
			upstream := asr[f.Stream]
			if upstream == nil {
				continue
			}
			var v struct {
				Binary  bool   `json:"binary"`
				Payload string `json:"payload"`
			}
			if json.Unmarshal(f.Data, &v) != nil {
				return errors.New("bad ASR frame")
			}
			typ := websocket.TextMessage
			data := []byte(v.Payload)
			if v.Binary {
				typ = websocket.BinaryMessage
				var err error
				data, err = base64.StdEncoding.DecodeString(v.Payload)
				if err != nil {
					return err
				}
			}
			upstream.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if e := upstream.WriteMessage(typ, data); e != nil {
				upstream.Close()
				delete(asr, f.Stream)
				send(hubFrame{Kind: "close", Stream: f.Stream, Data: json.RawMessage(`{}`)})
			}
		case "data":
			client := clients[f.Stream]
			if client == nil {
				continue
			}
			var data string
			if json.Unmarshal(f.Data, &data) != nil {
				return errors.New("bad data")
			}
			if len(data) > hubMaxMessage/2 {
				return errors.New("oversize data")
			}
			if len(data) > 0 && strings.ContainsRune("12347", rune(data[0])) {
				h.server.handleClientInput(client, []byte(data))
			}
		case "close":
			if upstream := asr[f.Stream]; upstream != nil {
				upstream.Close()
				delete(asr, f.Stream)
			}
			if client := clients[f.Stream]; client != nil {
				select {
				case h.server.sessionManager.unregister <- client:
				case <-ctx.Done():
					return ctx.Err()
				}
				delete(clients, f.Stream)
				delete(roles, f.Stream)
			}
			h.mu.Lock()
			delete(h.leases, f.Stream)
			h.mu.Unlock()
		default:
			return errors.New("unsupported control message")
		}
	}
}
func (h *hubManager) forward(ctx context.Context, id string, c *Client, send func(hubFrame) bool) {
	emit := func(data []byte) bool {
		b, _ := json.Marshal(string(data))
		return send(hubFrame{Kind: "data", Stream: id, Data: b})
	}
	marshal := func(prefix byte, v any) bool { b, _ := json.Marshal(v); return emit(append([]byte{prefix}, b...)) }
	if !marshal('4', h.terminalPreferences(c)) || !marshal('6', 16384) {
		return
	}
	r := c.replay
	marshal('a', replayBeginPayload{Epoch: r.Epoch, Mode: r.Mode, FromOffset: r.FromOffset})
	if r.Mode != "resume" {
		emit(encodeOutputMessage([]byte("\x1bc")))
	}
	for start := 0; start < len(r.Data); start += replayChunkSize {
		end := start + replayChunkSize
		if end > len(r.Data) {
			end = len(r.Data)
		}
		if !emit(encodeOutputMessage(r.Data[start:end])) {
			return
		}
	}
	marshal('b', replayEndPayload{EndOffset: r.EndOffset})
	for {
		select {
		case m, ok := <-c.send:
			if !ok {
				send(hubFrame{Kind: "close", Stream: id, Data: json.RawMessage(`{}`)})
				return
			}
			if !emit(m) {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
