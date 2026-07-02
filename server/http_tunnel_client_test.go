package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPTunnelClientStartsWithoutTTLAndStopsWithRelease(t *testing.T) {
	dir := t.TempDir()
	clientPath := filepath.Join(dir, "http-tunnel-client")
	script := `#!/usr/bin/env sh
set -eu
mkdir -p "$HOME/.http-tunnel"
case "$1" in
  connect)
    printf '%s\n' "$*" > "$HOME/args.txt"
    printf '%s\n' '{"event":"startup","data":{"public_url":"https://gotty-demo.httptunnel.top","tunnel_id":"tun_1"}}'
    while [ ! -f "$HOME/.http-tunnel/disconnect" ]; do sleep 0.05; done
    printf '%s\n' '{"event":"exit","data":{"last_disconnect_reason":"disconnect_requested"}}'
    ;;
  disconnect)
    touch "$HOME/.http-tunnel/disconnect"
    ;;
  release)
    printf '%s\n' "$*" >> "$HOME/control.txt"
    ;;
  runtime)
    printf '%s\n' "$*" >> "$HOME/control.txt"
    ;;
  *)
    exit 1
    ;;
esac
`
	if err := os.WriteFile(clientPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake client: %v", err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	client := NewHTTPTunnelClient(HTTPTunnelConfig{
		ClientPath: clientPath,
		RuntimeDir: runtimeDir,
		ServerURL:  "https://httptunnel.top",
		TargetURL:  "http://localhost:8080",
		Subdomain:  "gotty-demo",
	})
	info, err := client.Start(context.Background())
	if err != nil {
		t.Fatalf("start fake client: %v", err)
	}
	if info.PublicURL != "https://gotty-demo.httptunnel.top" || info.TunnelID != "tun_1" {
		t.Fatalf("unexpected startup info: %+v", info)
	}

	args, err := os.ReadFile(filepath.Join(runtimeDir, "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if strings.Contains(string(args), "--ttl-seconds") {
		t.Fatalf("ttl flag should be omitted when TTLSeconds is zero: %s", args)
	}

	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("stop fake client: %v", err)
	}
	control, err := os.ReadFile(filepath.Join(runtimeDir, "control.txt"))
	if err != nil {
		t.Fatalf("read control calls: %v", err)
	}
	if !strings.Contains(string(control), "release --server https://httptunnel.top") {
		t.Fatalf("release was not called: %s", control)
	}
}

func TestHTTPTunnelClientPassesTTLWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	clientPath := filepath.Join(dir, "http-tunnel-client")
	script := `#!/usr/bin/env sh
set -eu
mkdir -p "$HOME/.http-tunnel"
case "$1" in
  connect)
    printf '%s\n' "$*" > "$HOME/args.txt"
    printf '%s\n' '{"event":"startup","data":{"public_url":"https://gotty-demo.httptunnel.top","tunnel_id":"tun_1"}}'
    while [ ! -f "$HOME/.http-tunnel/disconnect" ]; do sleep 0.05; done
    ;;
  disconnect)
    touch "$HOME/.http-tunnel/disconnect"
    ;;
  release|runtime)
    ;;
esac
`
	if err := os.WriteFile(clientPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake client: %v", err)
	}

	runtimeDir := filepath.Join(dir, "runtime")
	client := NewHTTPTunnelClient(HTTPTunnelConfig{
		ClientPath: clientPath,
		RuntimeDir: runtimeDir,
		ServerURL:  "https://httptunnel.top",
		TargetURL:  "http://localhost:8080",
		Subdomain:  "gotty-demo",
		TTLSeconds: 120,
	})
	if _, err := client.Start(context.Background()); err != nil {
		t.Fatalf("start fake client: %v", err)
	}
	defer client.Close()

	args, err := os.ReadFile(filepath.Join(runtimeDir, "args.txt"))
	if err != nil {
		t.Fatalf("read args: %v", err)
	}
	if !strings.Contains(string(args), "--ttl-seconds 120") {
		t.Fatalf("ttl flag missing from args: %s", args)
	}
}
