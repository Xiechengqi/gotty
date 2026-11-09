package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHubManagementRequiresLocalProofWithoutCredential(t *testing.T) {
	s := &Server{options: &Options{HubStateFile: filepath.Join(t.TempDir(), "hub.json")}}
	h, e := newHubManager(context.Background(), s)
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("GET", "http://localhost/-/hub", nil)
	if h.authorize(r) {
		t.Fatal("anonymous binding management accepted")
	}
	r.Header.Set("X-GoTTY-Hub-Admin", h.admin)
	if !h.authorize(r) {
		t.Fatal("local proof rejected")
	}
	st, e := os.Stat(h.path + ".admin")
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("admin token permissions")
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

func TestHubConfirmationSaveFailureKeepsPendingBinding(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"instance_id":"instance","credential":"%s"}`, strings.Repeat("a", 64))
	}))
	defer hub.Close()
	previous := hubBinding{URL: hub.URL, Enrollment: "pending", Claim: "claim"}
	h := &hubManager{server: &Server{options: &Options{}}, admin: strings.Repeat("a", 32), binding: previous, path: filepath.Join(t.TempDir(), "missing", "hub.json")}
	r := httptest.NewRequest("POST", "http://localhost/-/hub", strings.NewReader(`{"Action":"confirm","Code":"123456"}`))
	r.Header.Set("X-GoTTY-Hub-Admin", h.admin)
	w := httptest.NewRecorder()
	h.handler(w, r)
	if w.Code != 500 || h.binding != previous {
		t.Fatalf("failed save changed pending binding: status %d", w.Code)
	}
}
