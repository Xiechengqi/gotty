package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareRegistryMarksActiveRecordsLostOnStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	registry, err := NewShareRegistry(path)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}

	record := ShareRecord{
		ID:        "sh_test",
		Type:      ShareTypeHTTP,
		Target:    "127.0.0.1:8080",
		PublicURL: "https://abc123.example.com",
		Status:    ShareStatusActive,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: timePtr(time.Now().UTC().Add(time.Hour)),
	}
	if err := registry.Upsert(record); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := registry.MarkStartupState(false); err != nil {
		t.Fatalf("mark startup state: %v", err)
	}

	updated, ok := registry.Get(record.ID)
	if !ok {
		t.Fatal("expected record to remain in registry")
	}
	if updated.Status != ShareStatusLost {
		t.Fatalf("expected lost status, got %q", updated.Status)
	}
}

func TestRestartShareRestoresLostRecordInPlace(t *testing.T) {
	dir := t.TempDir()
	clientPath := filepath.Join(dir, "http-tunnel-client")
	script := `#!/usr/bin/env sh
set -eu
mkdir -p "$HOME/.http-tunnel"
case "$1" in
  connect)
    printf '%s\n' "$*" > "$HOME/args.txt"
    printf '%s\n' '{"event":"startup","data":{"public_url":"https://gotty-xtlaat.httptunnel.top","tunnel_id":"tun_restore"}}'
    while [ ! -f "$HOME/.http-tunnel/disconnect" ]; do sleep 0.05; done
    ;;
  disconnect)
    touch "$HOME/.http-tunnel/disconnect"
    ;;
  release|runtime)
    rm -f "$HOME/.http-tunnel/disconnect"
    ;;
  *)
    exit 1
    ;;
esac
`
	if err := os.WriteFile(clientPath, []byte(script), 0700); err != nil {
		t.Fatalf("write fake client: %v", err)
	}

	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareClientPath:   clientPath,
		ShareRuntimeDir:   filepath.Join(dir, "runtime"),
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareMaxActive:    3,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	defer manager.Close()
	manager.SetDefaultTarget("localhost:2222", "/")

	lost := ShareRecord{
		ID:         "sh_restore",
		Type:       ShareTypeHTTP,
		Target:     "localhost:2222",
		Subdomain:  "gotty-xtlaat",
		PublicURL:  "https://gotty-xtlaat.httptunnel.top",
		Status:     ShareStatusLost,
		CreatedAt:  time.Now().UTC().Add(-time.Minute),
		LastError:  "gotty restarted before this share was stopped",
		IsTerminal: true,
	}
	if err := manager.registry.Upsert(lost); err != nil {
		t.Fatalf("upsert lost record: %v", err)
	}

	restarted, err := manager.RestartShare(lost.ID)
	if err != nil {
		t.Fatalf("restart lost share: %v", err)
	}
	if restarted.ID != lost.ID {
		t.Fatalf("restart created new record id %q, want %q", restarted.ID, lost.ID)
	}
	if restarted.Status != ShareStatusActive {
		t.Fatalf("restart status = %q, want %q", restarted.Status, ShareStatusActive)
	}
	if restarted.LastError != "" || restarted.StoppedAt != nil {
		t.Fatalf("restart did not clear failure state: last_error=%q stopped_at=%v", restarted.LastError, restarted.StoppedAt)
	}

	stored, ok := manager.registry.Get(lost.ID)
	if !ok {
		t.Fatal("restarted record missing from registry")
	}
	if stored.Status != ShareStatusActive {
		t.Fatalf("stored status = %q, want %q", stored.Status, ShareStatusActive)
	}
	args, err := os.ReadFile(filepath.Join(options.ShareRuntimeDir, lost.ID, "args.txt"))
	if err != nil {
		t.Fatalf("read fake client args: %v", err)
	}
	if !strings.Contains(string(args), "--subdomain gotty-xtlaat") {
		t.Fatalf("restart did not reuse subdomain: %s", args)
	}
	if !strings.Contains(string(args), "--target http://localhost:2222") {
		t.Fatalf("restart did not use current gotty target: %s", args)
	}
}

func TestPublicShareDomainHost(t *testing.T) {
	tests := map[string]string{
		"httptunnel.top":              "httptunnel.top",
		".httptunnel.top":             "httptunnel.top",
		"https://httptunnel.top":      "httptunnel.top",
		"https://httptunnel.top:8443": "httptunnel.top",
	}
	for input, expected := range tests {
		if got := publicShareDomainHost(input); got != expected {
			t.Fatalf("publicShareDomainHost(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestValidateShareTargetBlocksMetadataAddress(t *testing.T) {
	err := validateShareTarget(context.Background(), "169.254.169.254:80")
	if err == nil {
		t.Fatal("expected metadata address to be blocked")
	}
}

func TestValidateShareTargetAllowsLocalhost(t *testing.T) {
	err := validateShareTarget(context.Background(), "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("expected localhost target to be valid: %v", err)
	}
}

func TestNormalizeShareTargetAddsHTTPForHostPort(t *testing.T) {
	display, targetURL, err := normalizeShareTarget(context.Background(), "localhost:8080")
	if err != nil {
		t.Fatalf("normalize target: %v", err)
	}
	if display != "localhost:8080" {
		t.Fatalf("display target = %q", display)
	}
	if targetURL != "http://localhost:8080" {
		t.Fatalf("target URL = %q", targetURL)
	}
}

func TestNormalizeShareTargetAllowsHTTPURL(t *testing.T) {
	display, targetURL, err := normalizeShareTarget(context.Background(), "http://127.0.0.1:8080/base/")
	if err != nil {
		t.Fatalf("normalize target: %v", err)
	}
	if display != "http://127.0.0.1:8080/base" {
		t.Fatalf("display target = %q", display)
	}
	if targetURL != "http://127.0.0.1:8080/base" {
		t.Fatalf("target URL = %q", targetURL)
	}
}

func TestNormalizeShareSubdomainUsesGottyPrefix(t *testing.T) {
	suffix, subdomain, err := normalizeShareSubdomain("Demo-1")
	if err != nil {
		t.Fatalf("normalize subdomain: %v", err)
	}
	if suffix != "demo-1" {
		t.Fatalf("suffix = %q", suffix)
	}
	if subdomain != "gotty-demo-1" {
		t.Fatalf("subdomain = %q", subdomain)
	}
}

func TestNormalizeShareSubdomainAvoidsDoublePrefix(t *testing.T) {
	_, subdomain, err := normalizeShareSubdomain("gotty-demo")
	if err != nil {
		t.Fatalf("normalize subdomain: %v", err)
	}
	if subdomain != "gotty-demo" {
		t.Fatalf("subdomain = %q", subdomain)
	}
}

func TestNormalizeShareSubdomainBlankGeneratesSixLetters(t *testing.T) {
	suffix, subdomain, err := normalizeShareSubdomain("")
	if err != nil {
		t.Fatalf("normalize subdomain: %v", err)
	}
	if len(suffix) != 6 || subdomain != "gotty-"+suffix {
		t.Fatalf("suffix=%q subdomain=%q", suffix, subdomain)
	}
	for _, ch := range suffix {
		if ch < 'a' || ch > 'z' {
			t.Fatalf("generated suffix contains non-letter: %q", suffix)
		}
	}
}

func TestParseShareExpiryBlankMeansNever(t *testing.T) {
	ttl, expiresAt, err := parseShareExpiry(shareCreateRequest{}, time.Now().UTC())
	if err != nil {
		t.Fatalf("parse expiry: %v", err)
	}
	if ttl != 0 || expiresAt != nil {
		t.Fatalf("ttl=%d expiresAt=%v", ttl, expiresAt)
	}
}

func TestParseShareExpiryMinutesHoursDays(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		value int
		unit  string
		want  int
	}{
		{1, "minutes", 60},
		{2, "hours", 7200},
		{3, "days", 259200},
	}
	for _, tt := range tests {
		ttl, expiresAt, err := parseShareExpiry(shareCreateRequest{
			ExpireValue: tt.value,
			ExpireUnit:  tt.unit,
		}, now)
		if err != nil {
			t.Fatalf("parse expiry %v: %v", tt, err)
		}
		if ttl != tt.want {
			t.Fatalf("ttl = %d, want %d", ttl, tt.want)
		}
		if expiresAt == nil || !expiresAt.Equal(now.Add(time.Duration(tt.want)*time.Second)) {
			t.Fatalf("expiresAt = %v", expiresAt)
		}
	}
}

func TestParseShareExpiryRejectsInvalidValues(t *testing.T) {
	_, _, err := parseShareExpiry(shareCreateRequest{ExpireValue: 0, ExpireUnit: "hours"}, time.Now().UTC())
	if err == nil {
		t.Fatal("expected blank value with unit to fail")
	}
	_, _, err = parseShareExpiry(shareCreateRequest{ExpireValue: 1, ExpireUnit: "weeks"}, time.Now().UTC())
	if err == nil {
		t.Fatal("expected bad unit to fail")
	}
	_, _, err = parseShareExpiry(shareCreateRequest{TTLSeconds: 59}, time.Now().UTC())
	if err == nil {
		t.Fatal("expected ttl_seconds below 60 to fail")
	}
}

func timePtr(value time.Time) *time.Time {
	return &value
}
