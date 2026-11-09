package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type bindingTransport func(*http.Request) (*http.Response, error)

func (f bindingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestHubNameEditingAndFailedRebindKeepActiveIdentity(t *testing.T) {
	transport := http.DefaultTransport
	defer func() { http.DefaultTransport = transport }()
	mode := "duplicate"
	http.DefaultTransport = bindingTransport(func(r *http.Request) (*http.Response, error) {
		response := httptest.NewRecorder()
		if r.URL.Path == "/connector/v1/name" {
			if r.Header.Get("X-Instance-ID") != "instance-1" || r.Header.Get("Authorization") != "Bearer credential" {
				t.Fatal("rename did not use device identity")
			}
			if mode == "duplicate" {
				response.WriteHeader(409)
				fmt.Fprint(response, `{"error":"instance_name_exists"}`)
			} else {
				fmt.Fprint(response, `{"name":"renamed"}`)
			}
		} else {
			response.WriteHeader(409)
			fmt.Fprint(response, `{"error":"instance_name_exists"}`)
		}
		return response.Result(), nil
	})
	h := &hubManager{server: &Server{options: &Options{HubAllowHTTP: true}}, path: filepath.Join(t.TempDir(), "hub.json"), wake: make(chan struct{}, 1), online: true, binding: hubBinding{URL: "http://hub.test", Username: "owner", Name: "original", Instance: "instance-1", Credential: "credential"}}
	submit := func(url, name string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/-/hub", strings.NewReader(fmt.Sprintf(`{"URL":%q,"Username":"owner","Name":%q}`, url, name)))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.handler(w, r)
		return w
	}
	if w := submit("http://hub.test", "renamed"); w.Code != 409 || !strings.Contains(w.Body.String(), "instance_name_exists") {
		t.Fatal(w.Code, w.Body.String())
	}
	if h.binding.Name != "original" || !h.online {
		t.Fatal("failed rename changed active binding")
	}
	mode = "success"
	if w := submit("http://hub.test", "renamed"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if h.binding.Name != "renamed" || h.binding.Instance != "instance-1" {
		t.Fatal("rename replaced identity")
	}
	before := h.binding
	if w := submit("http://another-hub.test", "new-target"); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	if h.binding != before || !h.online {
		t.Fatal("failed edit destroyed active binding")
	}
	h.server.options.HubStateFile = h.path
	reloaded, err := newHubManager(context.Background(), h.server)
	if err != nil || reloaded.binding != before || reloaded.pending.RequestID == "" {
		t.Fatal("staged retry not preserved", err)
	}
	r := httptest.NewRequest("GET", "http://localhost/-/hub", nil)
	w := httptest.NewRecorder()
	h.handler(w, r)
	var state map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &state)
	if state["name"] != "renamed" || strings.Contains(w.Body.String(), "credential") || strings.Contains(w.Body.String(), reloaded.pending.Claim) {
		t.Fatal("internal state exposed")
	}
}
