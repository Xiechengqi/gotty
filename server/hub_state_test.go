package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestHubDefaultIdentityIsStableAndIsolated(t *testing.T) {
	userDir := t.TempDir()
	paths := map[string]bool{}
	for port := 2200; port < 2232; port++ {
		s := &Server{options: &Options{Address: "127.0.0.1", Port: fmt.Sprint(port), Path: "/"}}
		h, err := newHubManagerAt(context.Background(), s, userDir)
		if err != nil {
			t.Fatal(err)
		}
		if paths[h.path] {
			t.Fatal("independent listeners share a state file")
		}
		paths[h.path] = true
		h.binding = hubBinding{Instance: fmt.Sprint(port), Credential: "private"}
		if err := h.saveLocked(); err != nil {
			t.Fatal(err)
		}
		h.closeState()
		restored, err := newHubManagerAt(context.Background(), s, userDir)
		if err != nil {
			t.Fatal(err)
		}
		if restored.binding != h.binding {
			t.Fatal("restart changed identity")
		}
		restored.closeState()
	}
	a, _ := hubStatePaths(&Options{Address: "127.0.0.1, ::1", Port: "8080", Path: "shell/"}, userDir)
	b, _ := hubStatePaths(&Options{Address: "::1,127.0.0.1", Port: "8080", Path: "/shell"}, userDir)
	if a != b {
		t.Fatal("equivalent listener configuration changed identity")
	}
	c, _ := hubStatePaths(&Options{Address: "::1,127.0.0.1", Port: "8080", Path: "/other"}, userDir)
	if a == c {
		t.Fatal("different base paths share an identity")
	}
}

func TestHubStateLockRejectsDuplicateAndReleasesAfterClose(t *testing.T) {
	s := &Server{options: &Options{HubStateFile: filepath.Join(t.TempDir(), "binding.json")}}
	h, err := newHubManager(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.closeState)
	if _, err = newHubManager(context.Background(), s); err == nil {
		t.Fatal("duplicate state file was accepted")
	}
	if err = h.saveLocked(); err != nil {
		t.Fatal(err)
	}
	if _, err = newHubManager(context.Background(), s); err == nil {
		t.Fatal("atomic save lost the lock")
	}
	h.closeState()
	next, err := newHubManager(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	next.closeState()
	if err := os.WriteFile(s.options.HubStateFile, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = newHubManager(context.Background(), s); err == nil {
		t.Fatal("corrupt state accepted")
	}
	if err := os.WriteFile(s.options.HubStateFile, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err = newHubManager(context.Background(), s)
	if err != nil {
		t.Fatal("failed initialization leaked the lock", err)
	}
	next.closeState()
}

func TestHubLegacyIdentityRequiresExplicitSelection(t *testing.T) {
	for _, legacyConfig := range []bool{false, true} {
		t.Run(fmt.Sprint(legacyConfig), func(t *testing.T) {
			userDir := t.TempDir()
			if legacyConfig {
				if err := os.WriteFile(filepath.Join(userDir, ".gotty"), []byte("port = \"2222\""), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, legacy := hubStatePaths(&Options{}, userDir)
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			binding := hubBinding{Instance: "original", Credential: "preserve", Username: "owner"}
			if err := saveHubBinding(legacy, binding); err != nil {
				t.Fatal(err)
			}
			h, err := newHubManagerAt(context.Background(), &Server{options: &Options{}}, userDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(h.closeState)
			if h.binding.Instance != "" || h.stateNotice == "" {
				t.Fatal("ambiguous legacy identity was silently adopted or not explained")
			}
			restored, err := newHubManagerAt(context.Background(), &Server{options: &Options{HubStateFile: legacy}}, userDir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(restored.closeState)
			if restored.binding != binding {
				t.Fatal("explicit migration failed to preserve binding")
			}
		})
	}
}
