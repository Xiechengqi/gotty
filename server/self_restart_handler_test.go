package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sorenisanerd/gotty/utils"
)

func TestRestartModeValidation(t *testing.T) {
	for _, mode := range []string{"", RestartModeAuto, RestartModeExec, RestartModeFork, RestartModeExit, RestartModeDisabled} {
		options := testRestartOptions(t, mode)
		if err := options.Validate(); err != nil {
			t.Fatalf("Validate restart mode %q: %v", mode, err)
		}
	}

	options := testRestartOptions(t, "bad")
	if err := options.Validate(); err == nil {
		t.Fatal("expected invalid restart mode to fail validation")
	}
}

func testRestartOptions(t *testing.T, mode string) *Options {
	t.Helper()
	options := &Options{}
	if err := utils.ApplyDefaultValues(options); err != nil {
		t.Fatalf("apply defaults: %v", err)
	}
	options.RestartMode = mode
	return options
}

func TestRequestSelfRestartAutoUsesExecPlan(t *testing.T) {
	server := &Server{options: &Options{RestartMode: RestartModeAuto}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mode, err := server.requestSelfRestart(cancel)
	if err != nil {
		t.Fatalf("request restart: %v", err)
	}
	if mode != RestartModeExec {
		t.Fatalf("mode = %q, want exec", mode)
	}
	plan, ok := server.PendingSelfRestart()
	if !ok {
		t.Fatal("expected pending exec restart plan")
	}
	if plan.Mode != RestartModeExec || plan.Binary == "" || len(plan.Args) == 0 || plan.Args[0] != plan.Binary {
		t.Fatalf("bad exec plan: %#v", plan)
	}
	waitCanceled(t, ctx)
}

func TestRequestSelfRestartDisabled(t *testing.T) {
	server := &Server{options: &Options{RestartMode: RestartModeDisabled}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := server.requestSelfRestart(cancel); err != ErrRestartDisabled {
		t.Fatalf("request restart error = %v, want ErrRestartDisabled", err)
	}
	if _, ok := server.PendingSelfRestart(); ok {
		t.Fatal("disabled restart should not create pending plan")
	}
	select {
	case <-ctx.Done():
		t.Fatal("disabled restart should not cancel server")
	case <-time.After(400 * time.Millisecond):
	}
}

func TestRequestSelfRestartExitCancelsWithoutExecPlan(t *testing.T) {
	server := &Server{options: &Options{RestartMode: RestartModeExit}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mode, err := server.requestSelfRestart(cancel)
	if err != nil {
		t.Fatalf("request restart: %v", err)
	}
	if mode != RestartModeExit {
		t.Fatalf("mode = %q, want exit", mode)
	}
	if _, ok := server.PendingSelfRestart(); ok {
		t.Fatal("exit restart should not create pending exec plan")
	}
	waitCanceled(t, ctx)
}

func TestRequestSelfRestartForkUsesScheduler(t *testing.T) {
	var called bool
	var gotEnv map[string]string
	previous := scheduleSelfRestartFunc
	scheduleSelfRestartFunc = func(env map[string]string) error {
		called = true
		gotEnv = env
		return nil
	}
	t.Cleanup(func() {
		scheduleSelfRestartFunc = previous
	})

	manager, err := NewShareManager(context.Background(), &Options{
		ShareEnabled:      false,
		ShareRegistryFile: filepath.Join(t.TempDir(), "shares.json"),
		ShareInstanceID:   "fork-owner",
	})
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	server := &Server{
		options:      &Options{RestartMode: RestartModeFork},
		shareManager: manager,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mode, err := server.requestSelfRestart(cancel)
	if err != nil {
		t.Fatalf("request restart: %v", err)
	}
	if mode != RestartModeFork {
		t.Fatalf("mode = %q, want fork", mode)
	}
	if !called {
		t.Fatal("fork restart did not call scheduler")
	}
	if gotEnv[shareOwnerEnv] != "fork-owner" {
		t.Fatalf("scheduler env owner = %q, want fork-owner", gotEnv[shareOwnerEnv])
	}
	if _, ok := server.PendingSelfRestart(); ok {
		t.Fatal("fork restart should not create pending exec plan")
	}
	waitCanceled(t, ctx)
}

func TestCurrentExecutablePathPrefersArg0PathLookup(t *testing.T) {
	dir := t.TempDir()
	gottyPath := filepath.Join(dir, "gotty")
	if err := os.WriteFile(gottyPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write fake gotty: %v", err)
	}

	previousArgs := os.Args
	os.Args = []string{"gotty", "--version"}
	t.Cleanup(func() {
		os.Args = previousArgs
	})
	t.Setenv("PATH", dir)

	path, err := currentExecutablePath()
	if err != nil {
		t.Fatalf("current executable path: %v", err)
	}
	if path != gottyPath {
		t.Fatalf("path = %q, want %q", path, gottyPath)
	}
}

func waitCanceled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("server context was not canceled")
	}
}
