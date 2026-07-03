package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorenisanerd/gotty/utils"
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
		OwnerID:   "owner-a",
		Status:    ShareStatusActive,
		CreatedAt: time.Now().UTC(),
		ExpiresAt: timePtr(time.Now().UTC().Add(time.Hour)),
	}
	if err := registry.Upsert(record); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := registry.MarkStartupState("owner-a", "127.0.0.1:8080"); err != nil {
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

func TestShareRegistryStartupOnlyMarksCurrentOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	registry, err := NewShareRegistry(path)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	now := time.Now().UTC()
	records := []ShareRecord{
		{
			ID:        "sh_owner_a",
			Type:      ShareTypeHTTP,
			Target:    "localhost:1111",
			PublicURL: "https://owner-a.example.com",
			OwnerID:   "owner-a",
			Status:    ShareStatusActive,
			CreatedAt: now,
		},
		{
			ID:        "sh_owner_b",
			Type:      ShareTypeHTTP,
			Target:    "localhost:2222",
			PublicURL: "https://owner-b.example.com",
			OwnerID:   "owner-b",
			Status:    ShareStatusActive,
			CreatedAt: now,
		},
	}
	for _, record := range records {
		if err := registry.Upsert(record); err != nil {
			t.Fatalf("upsert %s: %v", record.ID, err)
		}
	}

	if err := registry.MarkStartupState("owner-b", "localhost:2222"); err != nil {
		t.Fatalf("mark startup state: %v", err)
	}

	ownerA, _ := registry.Get("sh_owner_a")
	if ownerA.Status != ShareStatusActive {
		t.Fatalf("owner-a status = %q, want active", ownerA.Status)
	}
	ownerB, _ := registry.Get("sh_owner_b")
	if ownerB.Status != ShareStatusLost {
		t.Fatalf("owner-b status = %q, want lost", ownerB.Status)
	}
}

func TestShareRegistryAdoptsOnlyMatchingOwnerlessTerminalRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	registry, err := NewShareRegistry(path)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	now := time.Now().UTC()
	matching := ShareRecord{
		ID:         "sh_matching",
		Type:       ShareTypeHTTP,
		Target:     "localhost:2222",
		Subdomain:  "gotty-matching",
		PublicURL:  "https://gotty-matching.httptunnel.top",
		Status:     ShareStatusActive,
		CreatedAt:  now,
		IsTerminal: true,
	}
	otherPort := ShareRecord{
		ID:         "sh_other_port",
		Type:       ShareTypeHTTP,
		Target:     "localhost:3333",
		Subdomain:  "gotty-other",
		PublicURL:  "https://gotty-other.httptunnel.top",
		Status:     ShareStatusActive,
		CreatedAt:  now,
		IsTerminal: true,
	}
	explicitTarget := ShareRecord{
		ID:        "sh_explicit",
		Type:      ShareTypeHTTP,
		Target:    "localhost:2222",
		PublicURL: "https://gotty-explicit.httptunnel.top",
		Status:    ShareStatusActive,
		CreatedAt: now,
	}
	for _, record := range []ShareRecord{matching, otherPort, explicitTarget} {
		if err := registry.Upsert(record); err != nil {
			t.Fatalf("upsert %s: %v", record.ID, err)
		}
	}

	if err := registry.MarkStartupState("owner-current", "localhost:2222"); err != nil {
		t.Fatalf("mark startup state: %v", err)
	}

	gotMatching, _ := registry.Get(matching.ID)
	if gotMatching.OwnerID != "owner-current" || gotMatching.Status != ShareStatusLost {
		t.Fatalf("matching record = owner:%q status:%q, want owner-current/lost", gotMatching.OwnerID, gotMatching.Status)
	}
	gotOther, _ := registry.Get(otherPort.ID)
	if gotOther.OwnerID != "" || gotOther.Status != ShareStatusActive {
		t.Fatalf("other port record = owner:%q status:%q, want ownerless/active", gotOther.OwnerID, gotOther.Status)
	}
	gotExplicit, _ := registry.Get(explicitTarget.ID)
	if gotExplicit.OwnerID != "" || gotExplicit.Status != ShareStatusActive {
		t.Fatalf("explicit target record = owner:%q status:%q, want ownerless/active", gotExplicit.OwnerID, gotExplicit.Status)
	}
}

func TestShareRegistryPreservesRecordsFromOtherProcesses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shares.json")
	registryA, err := NewShareRegistry(path)
	if err != nil {
		t.Fatalf("new registry A: %v", err)
	}
	registryB, err := NewShareRegistry(path)
	if err != nil {
		t.Fatalf("new registry B: %v", err)
	}
	now := time.Now().UTC()
	if err := registryA.Upsert(ShareRecord{
		ID:        "sh_owner_a",
		Type:      ShareTypeHTTP,
		Target:    "localhost:1111",
		PublicURL: "https://owner-a.example.com",
		OwnerID:   "owner-a",
		Status:    ShareStatusActive,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("upsert owner A: %v", err)
	}
	if err := registryB.Upsert(ShareRecord{
		ID:        "sh_owner_b",
		Type:      ShareTypeHTTP,
		Target:    "localhost:2222",
		PublicURL: "https://owner-b.example.com",
		OwnerID:   "owner-b",
		Status:    ShareStatusActive,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("upsert owner B: %v", err)
	}

	records := registryA.List()
	if len(records) != 2 {
		t.Fatalf("records length = %d, want 2: %#v", len(records), records)
	}
}

func TestRestartShareRestoresLostRecordInPlace(t *testing.T) {
	dir := t.TempDir()
	clientPath := writeFakeShareClient(t, dir)

	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareClientPath:   clientPath,
		ShareRuntimeDir:   filepath.Join(dir, "runtime"),
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareInstanceID:   "owner-restore",
		ShareMaxActive:    3,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	defer manager.Close()
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	lost := ShareRecord{
		ID:           "sh_restore",
		Type:         ShareTypeHTTP,
		Target:       "localhost:2222",
		Subdomain:    "gotty-xtlaat",
		PublicURL:    "https://gotty-xtlaat.httptunnel.top",
		OwnerID:      "owner-restore",
		Status:       ShareStatusLost,
		CreatedAt:    time.Now().UTC().Add(-time.Minute),
		LastError:    "gotty restarted before this share was stopped",
		IsTerminal:   true,
		ReadOnly:     true,
		Passwordless: true,
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
	if !restarted.ReadOnly || !restarted.Passwordless {
		t.Fatalf("restart did not preserve access flags: read_only=%v passwordless=%v", restarted.ReadOnly, restarted.Passwordless)
	}

	stored, ok := manager.registry.Get(lost.ID)
	if !ok {
		t.Fatal("restarted record missing from registry")
	}
	if stored.Status != ShareStatusActive {
		t.Fatalf("stored status = %q, want %q", stored.Status, ShareStatusActive)
	}
	if !stored.ReadOnly || !stored.Passwordless {
		t.Fatalf("stored record did not preserve access flags: read_only=%v passwordless=%v", stored.ReadOnly, stored.Passwordless)
	}
	args, err := os.ReadFile(filepath.Join(options.ShareRuntimeDir, "owner-restore", lost.ID, "args.txt"))
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

func TestCreateShareStoresOwnerAndUsesScopedRuntime(t *testing.T) {
	dir := t.TempDir()
	clientPath := writeFakeShareClient(t, dir)
	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareClientPath:   clientPath,
		ShareRuntimeDir:   filepath.Join(dir, "runtime"),
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareInstanceID:   "owner-create",
		ShareMaxActive:    3,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	defer manager.Close()
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	record, err := manager.CreateShare(shareCreateRequest{Subdomain: "unit", ReadOnly: true, Passwordless: true})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	if record.OwnerID != "owner-create" {
		t.Fatalf("owner id = %q, want owner-create", record.OwnerID)
	}
	if !record.ReadOnly || !record.Passwordless {
		t.Fatalf("access flags = read_only:%v passwordless:%v, want true/true", record.ReadOnly, record.Passwordless)
	}
	stored, ok := manager.registry.Get(record.ID)
	if !ok {
		t.Fatal("created record missing from registry")
	}
	if !stored.ReadOnly || !stored.Passwordless {
		t.Fatalf("stored access flags = read_only:%v passwordless:%v, want true/true", stored.ReadOnly, stored.Passwordless)
	}
	args, err := os.ReadFile(filepath.Join(options.ShareRuntimeDir, "owner-create", record.ID, "args.txt"))
	if err != nil {
		t.Fatalf("read fake client args: %v", err)
	}
	if !strings.Contains(string(args), "--target http://localhost:2222") {
		t.Fatalf("create did not use default target: %s", args)
	}
}

func TestCreateShareStoresCustomPasswordHash(t *testing.T) {
	dir := t.TempDir()
	clientPath := writeFakeShareClient(t, dir)
	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareClientPath:   clientPath,
		ShareRuntimeDir:   filepath.Join(dir, "runtime"),
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareInstanceID:   "owner-password",
		ShareMaxActive:    3,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	defer manager.Close()
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	record, err := manager.CreateShare(shareCreateRequest{
		Subdomain:      "password",
		CustomPassword: true,
		Password:       "letmein",
	})
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	if !record.CustomPassword || record.Passwordless {
		t.Fatalf("access flags = custom_password:%v passwordless:%v, want true/false", record.CustomPassword, record.Passwordless)
	}
	if record.PasswordHash == "" || strings.Contains(record.PasswordHash, "letmein") {
		t.Fatalf("password hash was not stored safely: %q", record.PasswordHash)
	}
	if !verifySharePassword(record, "letmein") {
		t.Fatal("stored password hash does not verify original password")
	}

	stored, ok := manager.registry.Get(record.ID)
	if !ok {
		t.Fatal("created record missing from registry")
	}
	if !stored.CustomPassword || stored.PasswordHash != record.PasswordHash {
		t.Fatalf("stored custom password fields = custom:%v hash:%q, want custom true and matching hash", stored.CustomPassword, stored.PasswordHash)
	}
}

func TestCreateShareRejectsInvalidCustomPasswordOptions(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  shareCreateRequest
	}{
		{
			name: "blank password",
			req: shareCreateRequest{
				CustomPassword: true,
				Password:       "  ",
			},
		},
		{
			name: "passwordless conflict",
			req: shareCreateRequest{
				Passwordless:   true,
				CustomPassword: true,
				Password:       "letmein",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := buildShareAccessOptions(tc.req); err == nil {
				t.Fatal("expected invalid custom password options to fail")
			}
		})
	}
}

func TestGetShareByPublicHostMatchesActiveOwnerRecord(t *testing.T) {
	dir := t.TempDir()
	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareInstanceID:   "owner-host",
		ShareMaxActive:    3,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	defer manager.Close()
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}
	record := ShareRecord{
		ID:           "sh_host",
		Type:         ShareTypeHTTP,
		Target:       "localhost:2222",
		Subdomain:    "gotty-demo",
		PublicURL:    "https://gotty-demo.httptunnel.top",
		OwnerID:      "owner-host",
		Status:       ShareStatusActive,
		CreatedAt:    time.Now().UTC(),
		IsTerminal:   true,
		ReadOnly:     true,
		Passwordless: true,
	}
	if err := manager.registry.Upsert(record); err != nil {
		t.Fatalf("upsert record: %v", err)
	}

	got, ok := manager.GetShareByPublicHost("gotty-demo.httptunnel.top", "httptunnel.top")
	if !ok {
		t.Fatal("expected public host to match share")
	}
	if got.ID != record.ID || !got.ReadOnly || !got.Passwordless {
		t.Fatalf("matched record = %#v", got)
	}

	if _, ok := manager.GetShareByPublicHost("gotty-other.httptunnel.top", "httptunnel.top"); ok {
		t.Fatal("unexpected match for other host")
	}
}

func TestRestoreActiveSharesRestartsLostRecordsInPlace(t *testing.T) {
	dir := t.TempDir()
	clientPath := writeFakeShareClient(t, dir)
	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareClientPath:   clientPath,
		ShareRuntimeDir:   filepath.Join(dir, "runtime"),
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareInstanceID:   "owner-auto",
		ShareMaxActive:    3,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	defer manager.Close()
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	lost := ShareRecord{
		ID:         "sh_auto_restore",
		Type:       ShareTypeHTTP,
		Target:     "localhost:2222",
		Subdomain:  "gotty-auto",
		PublicURL:  "https://gotty-auto.httptunnel.top",
		OwnerID:    "owner-auto",
		Status:     ShareStatusLost,
		CreatedAt:  time.Now().UTC().Add(-time.Minute),
		LastError:  "gotty restarted before this share was stopped",
		IsTerminal: true,
	}
	if err := manager.registry.Upsert(lost); err != nil {
		t.Fatalf("upsert lost record: %v", err)
	}

	manager.restoreActiveShares()

	stored, ok := manager.registry.Get(lost.ID)
	if !ok {
		t.Fatal("restored record missing from registry")
	}
	if stored.ID != lost.ID || stored.Status != ShareStatusActive {
		t.Fatalf("stored record = id:%q status:%q, want id:%q status:%q", stored.ID, stored.Status, lost.ID, ShareStatusActive)
	}
	if stored.LastError != "" {
		t.Fatalf("restore did not clear last error: %q", stored.LastError)
	}
	if len(manager.registry.List()) != 1 {
		t.Fatalf("restore should not create a duplicate record: %#v", manager.registry.List())
	}
}

func TestRestoreActiveSharesSkipsOtherOwners(t *testing.T) {
	dir := t.TempDir()
	clientPath := writeFakeShareClient(t, dir)
	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareClientPath:   clientPath,
		ShareRuntimeDir:   filepath.Join(dir, "runtime"),
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareInstanceID:   "owner-b",
		ShareMaxActive:    3,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	defer manager.Close()
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	other := ShareRecord{
		ID:         "sh_owner_a",
		Type:       ShareTypeHTTP,
		Target:     "localhost:1111",
		Subdomain:  "gotty-owner-a",
		PublicURL:  "https://gotty-owner-a.httptunnel.top",
		OwnerID:    "owner-a",
		Status:     ShareStatusLost,
		CreatedAt:  time.Now().UTC().Add(-time.Minute),
		LastError:  "gotty restarted before this share was stopped",
		IsTerminal: true,
	}
	if err := manager.registry.Upsert(other); err != nil {
		t.Fatalf("upsert other owner: %v", err)
	}

	manager.restoreActiveShares()

	stored, ok := manager.registry.Get(other.ID)
	if !ok {
		t.Fatal("other owner record missing from registry")
	}
	if stored.Status != ShareStatusLost {
		t.Fatalf("other owner status = %q, want lost", stored.Status)
	}
	if got := manager.List(); len(got) != 0 {
		t.Fatalf("manager listed other owner shares: %#v", got)
	}
	if _, err := os.Stat(filepath.Join(options.ShareRuntimeDir, "owner-b", other.ID, "args.txt")); !os.IsNotExist(err) {
		t.Fatalf("other owner share should not be restarted, stat err=%v", err)
	}
}

func TestShareManagerRejectsOtherOwnerRecords(t *testing.T) {
	dir := t.TempDir()
	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareRuntimeDir:   filepath.Join(dir, "runtime"),
		ShareRegistryFile: filepath.Join(dir, "shares.json"),
		ShareInstanceID:   "owner-b",
		ShareMaxActive:    3,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	defer manager.Close()
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	other := ShareRecord{
		ID:        "sh_owner_a",
		Type:      ShareTypeHTTP,
		Target:    "localhost:1111",
		PublicURL: "https://owner-a.example.com",
		OwnerID:   "owner-a",
		Status:    ShareStatusLost,
		CreatedAt: time.Now().UTC(),
	}
	if err := manager.registry.Upsert(other); err != nil {
		t.Fatalf("upsert other owner: %v", err)
	}

	if _, ok := manager.GetShare(other.ID); ok {
		t.Fatal("GetShare returned other owner record")
	}
	if _, err := manager.RestartShare(other.ID); err == nil {
		t.Fatal("RestartShare should reject other owner record")
	}
	if _, err := manager.StopShare(other.ID, ShareStatusStopped); err == nil {
		t.Fatal("StopShare should reject other owner record")
	}
	if err := manager.DeleteRecord(other.ID); err == nil {
		t.Fatal("DeleteRecord should reject other owner record")
	}
	if _, ok := manager.registry.Get(other.ID); !ok {
		t.Fatal("other owner record should remain in registry")
	}
}

func TestShareRestoreActiveDefaultEnabled(t *testing.T) {
	var options Options
	if err := utils.ApplyDefaultValues(&options); err != nil {
		t.Fatalf("apply defaults: %v", err)
	}
	if !options.ShareRestoreActive {
		t.Fatal("ShareRestoreActive default should restore shares after gotty restarts")
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

func writeFakeShareClient(t *testing.T, dir string) string {
	t.Helper()
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
	return clientPath
}

func timePtr(value time.Time) *time.Time {
	return &value
}
