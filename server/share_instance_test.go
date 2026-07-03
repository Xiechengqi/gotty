package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveShareOwnerIDPrefersConfiguredValue(t *testing.T) {
	t.Setenv(shareOwnerEnv, "env-owner")
	options := &Options{ShareInstanceID: "Config Owner"}

	if got := resolveShareOwnerID(options, "localhost:2222", "/"); got != "config-owner" {
		t.Fatalf("owner id = %q, want config-owner", got)
	}
}

func TestResolveShareOwnerIDUsesRestartEnvironment(t *testing.T) {
	t.Setenv(shareOwnerEnv, "Restart Owner!")

	if got := resolveShareOwnerID(&Options{}, "localhost:2222", "/"); got != "restart-owner" {
		t.Fatalf("owner id = %q, want restart-owner", got)
	}
}

func TestGeneratedShareOwnerIDChangesWithTarget(t *testing.T) {
	options := &Options{Address: "127.0.0.1", Port: "0", Path: "/"}
	first := generatedShareOwnerID(options, "localhost:2222", "/")
	second := generatedShareOwnerID(options, "localhost:3333", "/")

	if first == second {
		t.Fatalf("generated owner ids should differ by target: %q", first)
	}
	if !strings.HasPrefix(first, "auto-") || len(first) != len("auto-")+16 {
		t.Fatalf("generated owner id has unexpected format: %q", first)
	}
}

func TestSafeShareOwnerIDCapsLength(t *testing.T) {
	got := safeShareOwnerID(strings.Repeat("A", 120))
	if len(got) > 80 {
		t.Fatalf("owner id length = %d, want <= 80", len(got))
	}
}

func TestSelfRestartEnvIncludesShareOwnerID(t *testing.T) {
	dir := t.TempDir()
	manager, err := NewShareManager(context.Background(), &Options{
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareInstanceID:   "restart-owner",
	})
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	server := &Server{shareManager: manager}
	if got := server.selfRestartEnv()[shareOwnerEnv]; got != "restart-owner" {
		t.Fatalf("restart owner env = %q, want restart-owner", got)
	}
}
