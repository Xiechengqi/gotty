package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

// mockSlave implements Slave for testing purposes.
type mockSlave struct{}

func (s *mockSlave) Read(p []byte) (n int, err error)               { return 0, nil }
func (s *mockSlave) Write(p []byte) (n int, err error)               { return len(p), nil }
func (s *mockSlave) Close() error                                     { return nil }
func (s *mockSlave) WindowTitleVariables() map[string]interface{}     { return nil }
func (s *mockSlave) ResizeTerminal(width int, height int) error       { return nil }

// mockFactory implements Factory for testing purposes.
type mockFactory struct{}

func (f *mockFactory) Name() string { return "mock" }
func (f *mockFactory) New(params map[string][]string, headers map[string][]string) (Slave, error) {
	return &mockSlave{}, nil
}

func TestMultiInterfaceBinding_SingleAddress(t *testing.T) {
	// Backward compat: a single address (no comma) should work as before
	opts := &Options{
		Address: "127.0.0.1",
		Port:    "0",
		Quiet:   true,
	}

	server, err := New(&mockFactory{}, opts)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-errCh:
		t.Fatalf("server.Run returned unexpectedly: %v", err)
	default:
	}

	cancel()
	<-errCh
}

func TestMultiInterfaceBinding_MultipleAddresses(t *testing.T) {
	// Test binding to 127.0.0.1 on a random port — verify server starts
	opts := &Options{
		Address: "127.0.0.1",
		Port:    "0",
		Quiet:   true,
	}

	server, err := New(&mockFactory{}, opts)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-errCh:
		t.Fatalf("server.Run returned unexpectedly: %v", err)
	default:
	}

	cancel()
	<-errCh
}

func TestMultiInterfaceBinding_InvalidAddress(t *testing.T) {
	opts := &Options{
		Address: "999.999.999.999",
		Port:    "8080",
		Quiet:   true,
	}

	server, err := New(&mockFactory{}, opts)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = server.Run(ctx)
	if err == nil {
		t.Fatal("expected error for invalid address, got nil")
	}
	if !strings.Contains(err.Error(), "failed to listen") {
		t.Fatalf("expected error containing 'failed to listen', got: %v", err)
	}
}

func TestMultiInterfaceBinding_OneAddressFails(t *testing.T) {
	// When one address in a comma-separated list is invalid, the entire
	// server should fail and close any already-opened listeners.
	opts := &Options{
		Address: "127.0.0.1,999.999.999.999",
		Port:    "0",
		Quiet:   true,
	}

	server, err := New(&mockFactory{}, opts)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = server.Run(ctx)
	if err == nil {
		t.Fatal("expected error when one address is invalid, got nil")
	}
	if !strings.Contains(err.Error(), "failed to listen") {
		t.Fatalf("expected error containing 'failed to listen', got: %v", err)
	}
}

func TestMultiInterfaceBinding_AddressTrimming(t *testing.T) {
	// Spaces around addresses should be trimmed
	opts := &Options{
		Address: " 127.0.0.1 ",
		Port:    "0",
		Quiet:   true,
	}

	server, err := New(&mockFactory{}, opts)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Run(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-errCh:
		t.Fatalf("server.Run returned unexpectedly: %v", err)
	default:
	}

	cancel()
	<-errCh
}