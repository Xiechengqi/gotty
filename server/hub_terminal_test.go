package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHubTerminalForwardsDisplayConfigBeforeReplayWithoutSecrets(t *testing.T) {
	h := &hubManager{server: &Server{options: &Options{Preferences: &Preferences{FontSize: 20, FontFamily: "Menlo", ScrollbackLines: 800}, Credential: "never-forward", ASRBackend: "secret-asr-url", MobileKeys: true, ASRHoldMs: 700, ASRHotkey: "AltRight"}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := &Client{id: "c-1", send: make(chan []byte), replay: replaySnapshot{Epoch: "epoch", Mode: "tail", Data: []byte("hello")}}
	frames := []hubFrame{}
	h.forward(ctx, "stream-1", client, func(frame hubFrame) bool {
		frames = append(frames, frame)
		if len(frames) == 2 {
			cancel()
		}
		return true
	})
	var wire string
	if err := json.Unmarshal(frames[0].Data, &wire); err != nil {
		t.Fatal(err)
	}
	if wire[0] != '4' {
		t.Fatal("preferences must precede replay")
	}
	var prefs map[string]any
	if err := json.Unmarshal([]byte(wire[1:]), &prefs); err != nil {
		t.Fatal(err)
	}
	if prefs["font-size"] != float64(20) || prefs["font-family"] != "Menlo" || prefs["scrollback-lines"] != float64(800) {
		t.Fatal(prefs)
	}
	config := prefs["hub-config"].(map[string]any)
	if config["asr_hold_ms"] != float64(700) || config["asr_hotkey"] != "AltRight" || config["themes"] == nil {
		t.Fatal(config)
	}
	if strings.Contains(wire, "never-forward") || strings.Contains(wire, "secret-asr-url") {
		t.Fatal("backend secret escaped")
	}
}
func TestHubTerminalControlCannotElevateOperatorsOrObservers(t *testing.T) {
	h := &hubManager{server: &Server{options: &Options{RestartMode: RestartModeDisabled}}}
	for _, role := range []string{"operator", "observer", "admin", ""} {
		result := h.terminalControl(role, hubControlRequest{ID: "1", Action: "restart"})
		if result.Error == "" || h.server.selfRestarting {
			t.Fatalf("role %s could manage GoTTY", role)
		}
	}
	result := h.terminalControl("owner", hubControlRequest{ID: "1", Action: "restart"})
	if result.Error == "" {
		t.Fatal("disabled restart should fail")
	}
}
func TestHubDisconnectAndReconnectPreserveSavedIdentity(t *testing.T) {
	h := &hubManager{server: &Server{options: &Options{HubAllowHTTP: true}}, path: filepath.Join(t.TempDir(), "hub.json"), wake: make(chan struct{}, 1), online: true, binding: hubBinding{URL: "http://hub.test", Username: "owner", Name: "named", Instance: "instance-1", Credential: strings.Repeat("a", 64)}}
	cancelled := false
	h.connectionCancel = func() { cancelled = true }
	for _, action := range []string{"disconnect", "reconnect"} {
		body := `{"Action":"` + action + `","URL":"http://hub.test","Username":"owner","Name":"named"}`
		request := httptest.NewRequest("POST", "http://localhost/-/hub", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.handler(response, request)
		if response.Code != 200 {
			t.Fatal(response.Code, response.Body.String())
		}
		if h.binding.Instance != "instance-1" || h.binding.Name != "named" || h.binding.Credential == "" {
			t.Fatal("identity changed")
		}
		if h.binding.Paused != (action == "disconnect") {
			t.Fatal("pause state did not persist")
		}
	}
	if !cancelled {
		t.Fatal("active tunnel was not closed")
	}
	h.ctx = context.Background()
	h.server.options.HubStateFile = h.path
	reloaded, err := newHubManager(context.Background(), h.server)
	if err != nil || reloaded.binding != h.binding {
		t.Fatal("binding did not survive reload", err)
	}
}
func TestHubControlRestartUsesServerRestartAndRespectsDisabledMode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &hubManager{server: &Server{options: &Options{RestartMode: RestartModeExit}}, cancelRun: cancel}
	result := h.terminalControl("owner", hubControlRequest{ID: "1", Action: "restart"})
	if result.Error != "" || !h.server.selfRestarting {
		t.Fatal(result)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("server shutdown not scheduled")
	}
}
