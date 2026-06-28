package server

import (
	"context"
	"path/filepath"
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
		ExpiresAt: time.Now().UTC().Add(time.Hour),
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

func TestPublicShareDomainHost(t *testing.T) {
	tests := map[string]string{
		"portr.dev":              "portr.dev",
		".portr.dev":             "portr.dev",
		"https://portr.dev":      "portr.dev",
		"https://portr.dev:8443": "portr.dev",
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
