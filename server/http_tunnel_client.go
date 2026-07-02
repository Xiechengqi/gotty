package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sorenisanerd/gotty/pkg/homedir"
)

const (
	defaultHTTPTunnelClientPath = "/usr/local/bin/http-tunnel-client"
	httpTunnelControlTimeout    = 15 * time.Second
	httpTunnelStartTimeout      = 30 * time.Second
	httpTunnelStopWait          = 5 * time.Second
)

type HTTPTunnelConfig struct {
	ClientPath  string
	RuntimeDir  string
	ServerURL   string
	TargetURL   string
	Subdomain   string
	CreateToken string
	TTLSeconds  int
}

type HTTPTunnelStartInfo struct {
	PublicURL string
	TunnelID  string
}

type HTTPTunnelClient struct {
	config HTTPTunnelConfig

	cmd      *exec.Cmd
	done     chan struct{}
	stderr   limitedStringBuffer
	mu       sync.Mutex
	waitErr  error
	lastErr  string
	exitData httpTunnelEventData
}

type httpTunnelEvent struct {
	Event string              `json:"event"`
	Data  httpTunnelEventData `json:"data"`
}

type httpTunnelEventData struct {
	PublicURL            string `json:"public_url"`
	Target               string `json:"target"`
	ConnectURL           string `json:"connect_url"`
	TunnelID             string `json:"tunnel_id"`
	Message              string `json:"message"`
	Error                string `json:"error"`
	LastDisconnectReason string `json:"last_disconnect_reason"`
}

type limitedStringBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func NewHTTPTunnelClient(config HTTPTunnelConfig) *HTTPTunnelClient {
	return &HTTPTunnelClient{config: config}
}

func ResolveHTTPTunnelClientPath(configuredPath, runtimeRoot string) (string, error) {
	if configuredPath = strings.TrimSpace(configuredPath); configuredPath != "" {
		path := homedir.Expand(configuredPath)
		if !isExecutableFile(path) {
			return "", fmt.Errorf("http-tunnel-client is not executable: %s", path)
		}
		return path, nil
	}

	if isExecutableFile(defaultHTTPTunnelClientPath) {
		return defaultHTTPTunnelClientPath, nil
	}

	return extractEmbeddedHTTPTunnelClient(runtimeRoot)
}

func (c *HTTPTunnelClient) Start(ctx context.Context) (HTTPTunnelStartInfo, error) {
	if err := os.MkdirAll(c.config.RuntimeDir, 0700); err != nil {
		return HTTPTunnelStartInfo{}, err
	}

	args := []string{
		"connect",
		"--server", normalizeHTTPURL(c.config.ServerURL),
		"--target", c.config.TargetURL,
		"--subdomain", c.config.Subdomain,
		"--json-events",
	}
	if c.config.TTLSeconds > 0 {
		args = append(args, "--ttl-seconds", strconv.Itoa(c.config.TTLSeconds))
	}

	cmd := exec.Command(c.config.ClientPath, args...)
	cmd.Dir = c.config.RuntimeDir
	cmd.Env = httpTunnelEnv(c.config.RuntimeDir, c.config.CreateToken)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return HTTPTunnelStartInfo{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return HTTPTunnelStartInfo{}, err
	}

	startup := make(chan HTTPTunnelStartInfo, 1)
	c.done = make(chan struct{})
	c.stderr.limit = 64 * 1024
	c.cmd = cmd

	if err := cmd.Start(); err != nil {
		return HTTPTunnelStartInfo{}, err
	}

	go c.readEvents(stdout, startup)
	go func() {
		_, _ = io.Copy(&c.stderr, stderr)
	}()
	go func() {
		err := cmd.Wait()
		c.mu.Lock()
		c.waitErr = err
		c.mu.Unlock()
		close(c.done)
	}()

	timer := time.NewTimer(httpTunnelStartTimeout)
	defer timer.Stop()

	select {
	case info := <-startup:
		return info, nil
	case <-c.done:
		return HTTPTunnelStartInfo{}, c.exitError()
	case <-ctx.Done():
		_ = c.kill()
		return HTTPTunnelStartInfo{}, ctx.Err()
	case <-timer.C:
		_ = c.kill()
		return HTTPTunnelStartInfo{}, fmt.Errorf("http-tunnel-client did not report startup within %s", httpTunnelStartTimeout)
	}
}

func (c *HTTPTunnelClient) Serve(ctx context.Context) error {
	if c == nil || c.done == nil {
		return nil
	}
	select {
	case <-c.done:
		return c.exitError()
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *HTTPTunnelClient) Stop(ctx context.Context) error {
	if c == nil {
		return nil
	}

	var errs []error
	if c.done != nil {
		if err := c.runControl(ctx, "disconnect", "--timeout-seconds", "10"); err != nil {
			errs = append(errs, err)
		}
		if err := c.waitOrKill(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := c.runControl(ctx, "release", "--server", normalizeHTTPURL(c.config.ServerURL)); err != nil && !isMissingStoredTunnelError(err) {
		errs = append(errs, err)
	}
	_ = c.runControl(ctx, "runtime", "clean", "--force")
	return errors.Join(errs...)
}

func (c *HTTPTunnelClient) Close() {
	_ = c.Stop(context.Background())
}

func (c *HTTPTunnelClient) LastDisconnectReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exitData.LastDisconnectReason != "" {
		return c.exitData.LastDisconnectReason
	}
	return c.lastErr
}

func (c *HTTPTunnelClient) readEvents(stdout io.Reader, startup chan<- HTTPTunnelStartInfo) {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event httpTunnelEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			c.setLastError(line)
			continue
		}
		c.applyEvent(event)
		if event.Event == "startup" {
			select {
			case startup <- HTTPTunnelStartInfo{
				PublicURL: event.Data.PublicURL,
				TunnelID:  event.Data.TunnelID,
			}:
			default:
			}
		}
	}
	if err := scanner.Err(); err != nil {
		c.setLastError(err.Error())
	}
}

func (c *HTTPTunnelClient) applyEvent(event httpTunnelEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch event.Event {
	case "connection_failed":
		c.lastErr = firstNonEmpty(event.Data.Error, event.Data.Message)
	case "duplicate_instance", "duplicate_replaced", "tunnel_expired":
		c.lastErr = event.Data.Message
	case "exit":
		c.exitData = event.Data
		if event.Data.LastDisconnectReason != "" {
			c.lastErr = event.Data.LastDisconnectReason
		}
	}
}

func (c *HTTPTunnelClient) setLastError(message string) {
	message = strings.TrimSpace(message)
	if message == "" {
		return
	}
	c.mu.Lock()
	c.lastErr = message
	c.mu.Unlock()
}

func (c *HTTPTunnelClient) exitError() error {
	c.mu.Lock()
	waitErr := c.waitErr
	lastErr := c.lastErr
	c.mu.Unlock()

	if waitErr == nil {
		return nil
	}
	stderr := strings.TrimSpace(c.stderr.String())
	if lastErr != "" && stderr != "" {
		return fmt.Errorf("%s: %s: %w", lastErr, stderr, waitErr)
	}
	if lastErr != "" {
		return fmt.Errorf("%s: %w", lastErr, waitErr)
	}
	if stderr != "" {
		return fmt.Errorf("%s: %w", stderr, waitErr)
	}
	return waitErr
}

func (c *HTTPTunnelClient) runControl(parent context.Context, args ...string) error {
	if c.config.ClientPath == "" || c.config.RuntimeDir == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, httpTunnelControlTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.config.ClientPath, args...)
	cmd.Dir = c.config.RuntimeDir
	cmd.Env = httpTunnelEnv(c.config.RuntimeDir, c.config.CreateToken)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return err
		}
		return fmt.Errorf("%s: %w", message, err)
	}
	return nil
}

func (c *HTTPTunnelClient) waitOrKill() error {
	select {
	case <-c.done:
		return nil
	case <-time.After(httpTunnelStopWait):
		if err := c.kill(); err != nil {
			return err
		}
		<-c.done
		return nil
	}
}

func (c *HTTPTunnelClient) kill() error {
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func (b *limitedStringBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		b.limit = 64 * 1024
	}
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = b.data[len(b.data)-b.limit:]
	}
	return len(p), nil
}

func (b *limitedStringBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func httpTunnelEnv(home, createToken string) []string {
	env := os.Environ()
	env = setEnv(env, "HOME", home)
	if strings.TrimSpace(createToken) != "" {
		env = setEnv(env, "HTTP_TUNNEL_CREATE_TOKEN", createToken)
	}
	return env
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func extractEmbeddedHTTPTunnelClient(runtimeRoot string) (string, error) {
	data, err := embeddedHTTPTunnelClientFS.ReadFile(embeddedHTTPTunnelClientPath)
	if err != nil || !bytes.HasPrefix(data, []byte{0x7f, 'E', 'L', 'F'}) {
		return "", fmt.Errorf("http-tunnel-client not found at %s and no embedded linux client is available in this gotty binary", defaultHTTPTunnelClientPath)
	}

	root := homedir.Expand(strings.TrimSpace(runtimeRoot))
	if root == "" {
		root = homedir.Expand("~/.gotty-http-tunnel")
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(binDir, "http-tunnel-client")
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, data) && isExecutableFile(path) {
		return path, nil
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0700); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return path, nil
}

func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode()&0111 != 0
}

func isMissingStoredTunnelError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "tunnel id is required") ||
		strings.Contains(message, "tunnel token is required") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "gone")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
