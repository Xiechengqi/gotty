package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHubManagementFollowsLocalControl(t *testing.T) {
	s := &Server{options: &Options{HubStateFile: filepath.Join(t.TempDir(), "hub.json")}}
	h, e := newHubManager(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "http://localhost/-/hub", nil)
	if !h.authorize(r) {
		t.Fatal("passwordless primary site should allow management")
	}
	if _, e := os.Stat(h.path + ".admin"); !os.IsNotExist(e) {
		t.Fatal("obsolete admin file created")
	}
	s.options.Credential = "owner:secret"
	if h.authorize(r) {
		t.Fatal("missing main-site credentials accepted")
	}
	r.Header.Set("Authorization", "Bearer owner:secret")
	if !h.authorize(r) {
		t.Fatal("existing login rejected")
	}
}
func TestHubOriginsAreTerminalOnly(t *testing.T) {
	h := &hubManager{server: &Server{options: &Options{}}}
	for _, raw := range []string{"http://hub.example", "https://user:secret@hub.example", "https://hub.example/target", "https://hub.example?url=http://localhost"} {
		if _, e := h.validateHub(raw); e == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, e := h.validateHub("https://hub.example"); e != nil {
		t.Fatal(e)
	}
	h.server.options.HubAllowHTTP = true
	if _, e := h.validateHub("http://127.0.0.1:8088"); e != nil {
		t.Fatal(e)
	}
}
func TestHubActivityAndViewingState(t *testing.T) {
	h := &hubManager{leases: map[string]time.Time{}, color: "gray"}
	check := func(want string) {
		t.Helper()
		h.status()
		if h.color != want {
			t.Fatalf("want %s got %s", want, h.color)
		}
	}
	check("gray")
	h.activity()
	check("yellow")
	h.unseenOutput = time.Now().Add(-7 * time.Second)
	check("green")
	h.leases["viewer"] = time.Now().Add(time.Second)
	check("red")
	h.leases = map[string]time.Time{}
	check("gray")
}

func TestHubOutputWhileViewingDoesNotBecomeUnread(t *testing.T) {
	h := &hubManager{leases: map[string]time.Time{}, color: "gray"}
	h.setViewing("viewer", true)
	h.activity()
	h.status()
	if h.color != "red" {
		t.Fatal(h.color)
	}
	h.leases = map[string]time.Time{}
	h.status()
	if h.color != "gray" {
		t.Fatalf("viewed output reported unread: %s", h.color)
	}
	h.activity()
	h.status()
	if h.color != "yellow" {
		t.Fatal(h.color)
	}
}

func TestHubUnreadSurvivesDelayedStatus(t *testing.T) {
	h := &hubManager{leases: map[string]time.Time{}, color: "gray"}
	h.activity()
	h.unseenOutput = time.Now().Add(-7 * time.Second)
	h.status()
	if h.color != "green" {
		t.Fatalf("unread output lost before first poll: %s", h.color)
	}
}

func TestHubSaveReplacesStatePrivately(t *testing.T) {
	h := &hubManager{path: filepath.Join(t.TempDir(), "hub.json"), binding: hubBinding{Credential: "secret"}}
	if err := os.WriteFile(h.path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := h.saveLocked(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(h.path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("binding credentials must be private")
	}
}

func TestHubRegistrationSaveFailureCanRetrySameRequest(t *testing.T) {
	var h *hubManager
	requests := []string{}
	originalPath := filepath.Join(t.TempDir(), "hub.json")
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			RequestID string `json:"request_id"`
			Claim     string `json:"claim"`
			Name      string `json:"name"`
			Username  string `json:"username"`
		}
		if e := json.NewDecoder(r.Body).Decode(&v); e != nil {
			t.Error(e)
		}
		if v.Username != "owner" || v.Name == "" || len(v.Claim) != 64 {
			t.Error("registration metadata missing")
		}
		requests = append(requests, v.RequestID)
		if len(requests) == 1 {
			h.mu.Lock()
			h.path = filepath.Join(t.TempDir(), "missing", "hub.json")
			h.mu.Unlock()
		}
		fmt.Fprintf(w, `{"instance_id":"instance","credential":"%s","protocol":1}`, strings.Repeat("a", 64))
	}))
	defer hub.Close()
	h = &hubManager{server: &Server{options: &Options{HubAllowHTTP: true}}, path: originalPath, wake: make(chan struct{}, 1)}
	submit := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/-/hub", strings.NewReader(fmt.Sprintf(`{"URL":%q,"Username":"owner"}`, hub.URL)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.handler(w, r)
		return w
	}
	if w := submit(); w.Code != 500 {
		t.Fatalf("want failed save, got %d", w.Code)
	}
	if h.binding.Instance != "" || len(h.binding.RequestID) != 64 || len(h.binding.Claim) != 64 {
		t.Fatal("retry information lost")
	}
	persisted, e := os.ReadFile(originalPath)
	if e != nil || !strings.Contains(string(persisted), h.binding.Claim) {
		t.Fatal("request was not persisted before remote registration")
	}
	h.path = originalPath
	if w := submit(); w.Code != 200 {
		t.Fatalf("retry failed %d %s", w.Code, w.Body.String())
	}
	if len(requests) != 2 || requests[0] != requests[1] || h.binding.Claim != "" || h.binding.Instance != "instance" {
		t.Fatal("registration retry was not idempotent")
	}
	if w := submit(); w.Code != 200 || len(requests) != 2 {
		t.Fatal("bound instance was registered again")
	}
	r := httptest.NewRequest("GET", "http://localhost/-/hub", nil)
	w := httptest.NewRecorder()
	h.handler(w, r)
	if strings.Contains(w.Body.String(), "credential") || strings.Contains(w.Body.String(), strings.Repeat("a", 64)) {
		t.Fatal("device secret exposed")
	}
}
func TestHubUnknownUserAndCrossSiteRegistration(t *testing.T) {
	calls := 0
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"user_not_found"}`)
	}))
	defer hub.Close()
	h := &hubManager{server: &Server{options: &Options{HubAllowHTTP: true}}, path: filepath.Join(t.TempDir(), "hub.json")}
	body := fmt.Sprintf(`{"URL":%q,"Username":"unknown"}`, hub.URL)
	r := httptest.NewRequest("POST", "http://localhost/-/hub", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://other.example")
	w := httptest.NewRecorder()
	h.handler(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal("cross-origin request reached Hub")
	}
	r = httptest.NewRequest("POST", "http://localhost/-/hub", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.handler(w, r)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "user_not_found") || h.binding.Instance != "" {
		t.Fatalf("unknown user lost: %d %s", w.Code, w.Body.String())
	}
}

func TestHubManagementRejectsPublicShares(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{Passwordless: true})
	server.options.HubEnabled = true
	h := &hubManager{server: server}
	for _, forwarded := range []bool{false, true} {
		r := httptest.NewRequest("GET", "https://gotty-demo.httptunnel.top/config.js", nil)
		r.Host = "gotty-demo.httptunnel.top"
		if forwarded {
			r.Host = "127.0.0.1:8080"
			r.Header.Set("X-Forwarded-Host", "gotty-demo.httptunnel.top")
		}
		r.Header.Set("Authorization", "Bearer "+server.options.Credential)
		if h.authorize(r) {
			t.Fatal("share visitor accepted as Hub manager")
		}
		w := httptest.NewRecorder()
		server.handleConfig(w, r)
		if !strings.Contains(w.Body.String(), "var gotty_hub_enabled = false;") {
			t.Fatal("share page exposes Hub management UI")
		}
	}
}

func TestHubExpiredRegistrationCanBeExplicitlyRetried(t *testing.T) {
	var requests []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var v map[string]string
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Error(err)
		}
		requests = append(requests, v["request_id"])
		if len(requests) == 1 {
			w.WriteHeader(409)
			fmt.Fprint(w, `{"error":"registration_expired"}`)
			return
		}
		fmt.Fprintf(w, `{"instance_id":"instance","credential":"%s","protocol":1}`, strings.Repeat("a", 64))
	}))
	defer hub.Close()
	h := &hubManager{server: &Server{options: &Options{HubAllowHTTP: true}}, path: filepath.Join(t.TempDir(), "hub.json")}
	submit := func() int {
		r := httptest.NewRequest("POST", "http://localhost/-/hub", strings.NewReader(fmt.Sprintf(`{"URL":%q,"Username":"owner"}`, hub.URL)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.handler(w, r)
		return w.Code
	}
	if submit() != 409 || h.binding.Claim != "" || h.binding.RequestID != "" {
		t.Fatal("expired request was not reset")
	}
	if submit() != 200 || len(requests) != 2 || requests[0] == requests[1] {
		t.Fatal("explicit retry did not start a fresh registration")
	}
}
