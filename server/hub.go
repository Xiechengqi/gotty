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
	"github.com/sorenisanerd/gotty/pkg/homedir"
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
}
type hubManager struct {
	server       *Server
	ctx          context.Context
	mu           sync.Mutex
	managementMu sync.Mutex
	binding      hubBinding
	path         string
	online       bool
	leases       map[string]time.Time
	lastOutput   time.Time
	attention    bool
	revision     uint64
	color        string
	wake         chan struct{}
	unseenOutput time.Time
}

func hubRandom() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func newHubManager(ctx context.Context, s *Server) (*hubManager, error) {
	p := homedir.Expand(s.options.HubStateFile)
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		return nil, e
	}
	h := &hubManager{server: s, ctx: ctx, path: p, leases: map[string]time.Time{}, color: "gray", wake: make(chan struct{}, 1)}
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
	return h, nil
}
func (h *hubManager) saveLocked() error {
	b, e := json.Marshal(h.binding)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(h.path), ".hub-*")
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
	return os.Rename(f.Name(), h.path)
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
	b, _ := json.Marshal(body)
	r, e := http.NewRequestWithContext(ctx, "POST", base+path, bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	r.Header.Set("Content-Type", "application/json")
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
func (h *hubManager) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorize(r) {
		hubProblem(w, 403, "forbidden", "无权管理此 GoTTY 的 Hub 配置。")
		return
	}
	if r.Method == "GET" {
		h.mu.Lock()
		v := map[string]any{"url": h.binding.URL, "username": h.binding.Username, "instance_id": h.binding.Instance, "online": h.online}
		h.mu.Unlock()
		_ = json.NewEncoder(w).Encode(v)
		return
	}
	if r.Method != "POST" {
		hubProblem(w, 405, "method_not_allowed", "请求方法不支持。")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		hubProblem(w, 400, "invalid_request", "需要 JSON 请求。")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, e := url.Parse(origin)
		if e != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			hubProblem(w, 403, "forbidden", "请求来源不匹配。")
			return
		}
	}
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		hubProblem(w, 403, "forbidden", "请求来源不匹配。")
		return
	}
	var v struct{ URL, Username string }
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&v); e != nil {
		hubProblem(w, 400, "invalid_request", "请填写 Hub 地址和用户名。")
		return
	}
	base, e := h.validateHub(v.URL)
	if e != nil {
		message := "请输入 Hub 的 HTTP 或 HTTPS 根地址（不含路径、查询参数或账号信息）。"
		if !h.server.options.HubAllowHTTP {
			message = "请输入 Hub 的 HTTPS 根地址（不含路径、查询参数或账号信息）。"
		}
		hubProblem(w, 400, "invalid_hub_url", message)
		return
	}
	username := strings.ToLower(strings.TrimSpace(v.Username))
	valid := len(username) >= 3 && len(username) <= 64
	for i, b := range []byte(username) {
		if !((b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || (i > 0 && (b == '_' || b == '-' || b == '.'))) {
			valid = false
		}
	}
	if !valid {
		hubProblem(w, 400, "invalid_username", "用户名需要 3–64 位字母、数字、点、短横线或下划线，且以字母或数字开头。")
		return
	}
	h.managementMu.Lock()
	defer h.managementMu.Unlock()
	h.mu.Lock()
	binding := h.binding
	if binding.Instance != "" {
		h.mu.Unlock()
		if binding.URL != base || binding.Username != username {
			hubProblem(w, 409, "already_bound", "此 GoTTY 已接入 Hub，请先在原 Hub 解绑。")
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		return
	}
	if binding.URL != base || binding.Username != username || binding.RequestID == "" || binding.Claim == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "gotty"
		}
		if len(host) > 64 {
			host = host[:64]
		}
		requestID := hubRandom()
		previous := binding
		binding = hubBinding{URL: base, Username: username, Name: host + "-" + requestID[:8], RequestID: requestID, Claim: hubRandom()}
		h.binding = binding
		if e := h.saveLocked(); e != nil {
			h.binding = previous
			h.mu.Unlock()
			hubProblem(w, 500, "save_failed", "无法保存 Hub 配置，请检查本地文件权限。")
			return
		}
	}
	h.mu.Unlock()
	res, e := h.request(r.Context(), base, "/api/v1/instances/register", map[string]string{"username": username, "name": binding.Name, "request_id": binding.RequestID, "claim": binding.Claim})
	if e != nil {
		var remote *hubHTTPError
		if errors.As(e, &remote) {
			switch remote.code {
			case "user_not_found":
				hubProblem(w, 404, remote.code, "该用户名尚未在此 Hub 注册，请先前往 Hub Web 注册，再重试接入。")
			case "invalid_username":
				hubProblem(w, 400, remote.code, "Hub 不接受此用户名。")
			case "rate_limit":
				hubProblem(w, 429, remote.code, "接入请求过于频繁，请稍后重试。")
			case "registration_expired":
				h.mu.Lock()
				previous := h.binding
				if h.binding == binding {
					h.binding.Claim = ""
					h.binding.RequestID = ""
				}
				saveErr := h.saveLocked()
				if saveErr != nil {
					h.binding = previous
				}
				h.mu.Unlock()
				if saveErr != nil {
					hubProblem(w, 500, "save_failed", "无法保存重试状态，请检查本地文件权限。")
					return
				}
				hubProblem(w, 409, remote.code, "此接入申请已失效，再次点击接入即可重新注册。")
			default:
				hubProblem(w, 502, "hub_rejected", "Hub 拒绝接入，请确认地址及 Hub 版本。")
			}
		} else {
			hubProblem(w, 502, "hub_unavailable", "无法连接 Hub，请检查地址和网络后重试。")
		}
		return
	}
	next := binding
	next.Instance = hubString(res, "instance_id")
	next.Credential = hubString(res, "credential")
	var protocol int
	_ = json.Unmarshal(res["protocol"], &protocol)
	secret, decodeErr := hex.DecodeString(next.Credential)
	if next.Instance == "" || strings.ContainsAny(next.Instance, "/?#") || protocol != 1 || decodeErr != nil || len(secret) != 32 {
		hubProblem(w, 502, "invalid_hub_response", "Hub 返回的设备信息无效。")
		return
	}
	next.Claim = ""
	next.RequestID = ""
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.binding != binding {
		hubProblem(w, 409, "binding_changed", "配置已变更，请重试。")
		return
	}
	h.binding = next
	if e := h.saveLocked(); e != nil {
		h.binding = binding
		hubProblem(w, 500, "save_failed", "无法保存设备凭据，请检查本地文件权限后重试。")
		return
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
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
	backoff := time.Second
	for {
		if h.ctx.Err() != nil {
			return
		}
		h.mu.Lock()
		b := h.binding
		h.mu.Unlock()
		if b.Instance != "" {
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
				log.Printf("Hub device replaced by another connection; reconnect paused until restart")
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
	c, _, e := websocket.DefaultDialer.DialContext(h.ctx, u.String(), headers)
	if e != nil {
		return e
	}
	defer c.Close()
	c.SetReadLimit(hubMaxMessage)
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
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
	data, _ := json.Marshal(map[string]any{"protocol": 1, "capabilities": map[string]bool{"write": h.server.options.PermitWrite, "replay": true, "files": h.server.options.PermitWrite, "asr": h.server.options.EnableASR}})
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
	h.online = true
	h.revision = 0
	h.mu.Unlock()
	asr := map[string]*websocket.Conn{}
	defer func() {
		for _, c := range asr {
			c.Close()
		}
	}()
	clients := map[string]*Client{}
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
			select {
			case <-client.ready:
			case <-ctx.Done():
				return ctx.Err()
			}
			go h.forward(ctx, f.Stream, client, send)
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
	marshal('4', map[string]any{"client-id": c.id})
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
