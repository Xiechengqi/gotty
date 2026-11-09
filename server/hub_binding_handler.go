package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (h *hubManager) bindingStateLocked() map[string]any {
	b := h.binding
	return map[string]any{"url": b.URL, "username": b.Username, "name": b.Name, "instance_id": b.Instance, "online": h.online, "paused": b.Paused}
}
func (h *hubManager) wakeLocked(restart bool) {
	if restart && h.connectionCancel != nil {
		h.connectionCancel()
	}
	if restart {
		h.online = false
	}
	select {
	case h.wake <- struct{}{}:
	default:
	}
}
func (h *hubManager) bindingProblem(w http.ResponseWriter, err error) {
	var remote *hubHTTPError
	if !errors.As(err, &remote) {
		hubProblem(w, 502, "hub_unavailable", "无法连接 Hub，请检查地址和网络后重试。")
		return
	}
	switch remote.code {
	case "instance_name_exists":
		hubProblem(w, 409, remote.code, "该用户名下已存在同名 GoTTY 实例，请修改 Hub 中显示的名字后重试。")
	case "user_not_found":
		hubProblem(w, 404, remote.code, "该用户名尚未在此 Hub 注册，请先前往 Hub Web 注册，再重试接入。")
	case "invalid_username":
		hubProblem(w, 400, remote.code, "Hub 不接受此用户名。")
	case "rate_limit":
		hubProblem(w, 429, remote.code, "接入请求过于频繁，请稍后重试。")
	case "registration_expired":
		hubProblem(w, 409, remote.code, "此接入申请已失效，再次点击接入即可重新注册。")
	default:
		hubProblem(w, 502, "hub_rejected", "Hub 拒绝操作，请确认 Hub 版本及设备是否已解绑。")
	}
}
func (h *hubManager) handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !h.authorize(r) {
		hubProblem(w, 403, "forbidden", "无权管理此 GoTTY 的 Hub 配置。")
		return
	}
	if r.Method == http.MethodGet {
		h.mu.Lock()
		defer h.mu.Unlock()
		_ = json.NewEncoder(w).Encode(h.bindingStateLocked())
		return
	}
	if r.Method != http.MethodPost {
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
	var v struct{ URL, Username, Name, Action string }
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&v) != nil {
		hubProblem(w, 400, "invalid_request", "请填写 Hub 地址和用户名。")
		return
	}
	if v.Action != "" && v.Action != "connect" && v.Action != "reconnect" && v.Action != "disconnect" {
		hubProblem(w, 400, "invalid_request", "不支持此 Hub 操作。")
		return
	}
	h.managementMu.Lock()
	defer h.managementMu.Unlock()
	if v.Action == "disconnect" {
		h.mu.Lock()
		defer h.mu.Unlock()
		previous := h.binding
		h.binding.Paused = true
		if err := h.saveLocked(); err != nil {
			h.binding = previous
			hubProblem(w, 500, "save_failed", "无法保存 Hub 配置，请检查本地文件权限。")
			return
		}
		h.wakeLocked(true)
		_ = json.NewEncoder(w).Encode(h.bindingStateLocked())
		return
	}
	base, err := h.validateHub(v.URL)
	if err != nil {
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
	name := strings.TrimSpace(v.Name)
	if utf8.RuneCountInString(name) > 80 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		hubProblem(w, 400, "invalid_name", "显示名字最多 80 个字符，不能包含控制字符。")
		return
	}
	h.mu.Lock()
	current := h.binding
	if current.Instance != "" && current.URL == base && current.Username == username {
		h.mu.Unlock()
		if name == "" {
			name = current.Name
		}
		if name != current.Name {
			if _, err = h.deviceRequest(r.Context(), base, "/connector/v1/name", map[string]string{"name": name}, current); err != nil {
				h.bindingProblem(w, err)
				return
			}
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		previous := h.binding
		if previous.Instance != current.Instance {
			hubProblem(w, 409, "binding_changed", "配置已变更，请重试。")
			return
		}
		h.binding.Name = name
		h.binding.Paused = false
		if err = h.saveLocked(); err != nil {
			h.binding = previous
			hubProblem(w, 500, "save_failed", "无法保存 Hub 配置，请检查本地文件权限。")
			return
		}
		h.wakeLocked(v.Action == "reconnect" || previous.Paused)
		_ = json.NewEncoder(w).Encode(h.bindingStateLocked())
		return
	}
	// Stage a new target separately so a failed edit never destroys a working
	// binding. Persist the claim before registration, including across restarts.
	store := &h.binding
	path := h.path
	if current.Instance != "" {
		store = &h.pending
		path = h.path + ".pending"
	}
	binding := *store
	if binding.URL != base || binding.Username != username || (name != "" && binding.Name != name) || binding.RequestID == "" || binding.Claim == "" {
		requestID := hubRandom()
		if name == "" {
			host, _ := os.Hostname()
			if host == "" {
				host = "gotty"
			}
			runes := []rune(host)
			if len(runes) > 64 {
				host = string(runes[:64])
			}
			name = host + "-" + requestID[:8]
		}
		previous := *store
		binding = hubBinding{URL: base, Username: username, Name: name, RequestID: requestID, Claim: hubRandom()}
		*store = binding
		if err = saveHubBinding(path, binding); err != nil {
			*store = previous
			h.mu.Unlock()
			hubProblem(w, 500, "save_failed", "无法保存 Hub 配置，请检查本地文件权限。")
			return
		}
	}
	h.mu.Unlock()
	res, err := h.request(r.Context(), base, "/api/v1/instances/register", map[string]string{"username": username, "name": binding.Name, "request_id": binding.RequestID, "claim": binding.Claim})
	if err != nil {
		var remote *hubHTTPError
		if errors.As(err, &remote) && remote.code == "registration_expired" {
			h.mu.Lock()
			previous := *store
			if *store == binding {
				store.Claim = ""
				store.RequestID = ""
			}
			saveErr := saveHubBinding(path, *store)
			if saveErr != nil {
				*store = previous
			}
			h.mu.Unlock()
			if saveErr != nil {
				hubProblem(w, 500, "save_failed", "无法保存重试状态，请检查本地文件权限。")
				return
			}
		}
		h.bindingProblem(w, err)
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
	if *store != binding {
		hubProblem(w, 409, "binding_changed", "配置已变更，请重试。")
		return
	}
	previous := h.binding
	h.binding = next
	if err = h.saveLocked(); err != nil {
		h.binding = previous
		hubProblem(w, 500, "save_failed", "无法保存设备凭据，请检查本地文件权限后重试。")
		return
	}
	h.pending = hubBinding{}
	_ = os.Remove(h.path + ".pending")
	h.wakeLocked(true)
	_ = json.NewEncoder(w).Encode(h.bindingStateLocked())
}
