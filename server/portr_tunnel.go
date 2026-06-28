package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type PortrTunnelConfig struct {
	Type                             string
	Target                           string
	Subdomain                        string
	ServerURL                         string
	SSHURL                            string
	SecretKey                         string
	InsecureSkipHostKeyValidation     bool
}

type PortrTunnel struct {
	config     PortrTunnelConfig
	client     *ssh.Client
	listener   net.Listener
	remotePort int
	mu         sync.Mutex
}

func NewPortrTunnel(config PortrTunnelConfig) *PortrTunnel {
	return &PortrTunnel{config: config}
}

func (t *PortrTunnel) Start(ctx context.Context) (string, int, error) {
	connectionID, err := t.createConnection(ctx)
	if err != nil {
		return "", 0, err
	}

	sshConfig := &ssh.ClientConfig{
		User:            fmt.Sprintf("%s:%s", connectionID, t.config.SecretKey),
		Auth:            []ssh.AuthMethod{ssh.Password("")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	if !t.config.InsecureSkipHostKeyValidation {
		return "", 0, fmt.Errorf("share SSH host key validation is not configured")
	}

	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 15 * time.Second}
	rawConn, err := dialer.DialContext(ctx, "tcp", t.config.SSHURL)
	if err != nil {
		return "", 0, err
	}

	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = rawConn.Close()
		case <-handshakeDone:
		}
	}()
	_ = rawConn.SetDeadline(time.Now().Add(10 * time.Second))
	conn, chans, reqs, err := ssh.NewClientConn(rawConn, t.config.SSHURL, sshConfig)
	close(handshakeDone)
	_ = rawConn.SetDeadline(time.Time{})
	if err != nil {
		_ = rawConn.Close()
		return "", 0, err
	}

	client := ssh.NewClient(conn, chans, reqs)
	listener, remotePort, err := t.listenRemote(client)
	if err != nil {
		_ = client.Close()
		return "", 0, err
	}

	t.mu.Lock()
	t.client = client
	t.listener = listener
	t.remotePort = remotePort
	t.mu.Unlock()

	return connectionID, remotePort, nil
}

func (t *PortrTunnel) Serve(ctx context.Context) error {
	t.mu.Lock()
	listener := t.listener
	client := t.client
	t.mu.Unlock()

	if listener == nil || client == nil {
		return fmt.Errorf("share tunnel is not started")
	}

	go t.keepAlive(ctx, client)
	go func() {
		<-ctx.Done()
		t.Close()
	}()

	for {
		remoteConn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		go t.forward(remoteConn)
	}
}

func (t *PortrTunnel) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.listener != nil {
		_ = t.listener.Close()
		t.listener = nil
	}
	if t.client != nil {
		_ = t.client.Close()
		t.client = nil
	}
}

func (t *PortrTunnel) createConnection(ctx context.Context) (string, error) {
	type createRequest struct {
		ConnectionType string  `json:"connection_type"`
		SecretKey      string  `json:"secret_key"`
		Subdomain      *string `json:"subdomain"`
	}
	type createResponse struct {
		ConnectionID string `json:"connection_id"`
		Message      string `json:"message"`
		Error        string `json:"error"`
	}

	var subdomain *string
	if t.config.Type == ShareTypeHTTP {
		subdomain = &t.config.Subdomain
	}
	payload := createRequest{
		ConnectionType: t.config.Type,
		SecretKey:      t.config.SecretKey,
		Subdomain:      subdomain,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	endpoint := strings.TrimRight(normalizeHTTPURL(t.config.ServerURL), "/") + "/api/v1/connections/"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var parsed createResponse
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&parsed)

	if resp.StatusCode != http.StatusOK {
		message := parsed.Message
		if message == "" {
			message = parsed.Error
		}
		if message == "" {
			message = resp.Status
		}
		return "", fmt.Errorf("portr create connection failed: %s", message)
	}
	if parsed.ConnectionID == "" {
		return "", fmt.Errorf("portr create connection response missing connection_id")
	}
	return parsed.ConnectionID, nil
}

func (t *PortrTunnel) listenRemote(client *ssh.Client) (net.Listener, int, error) {
	var lastErr error
	for _, port := range randomRemotePorts(t.config.Type) {
		listener, err := client.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err == nil {
			return listener, port, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no remote ports available")
	}
	return nil, 0, lastErr
}

func (t *PortrTunnel) keepAlive(ctx context.Context, client *ssh.Client) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _, err := client.SendRequest("keepalive@openssh.com", false, nil)
			if err != nil {
				t.Close()
				return
			}
		}
	}
}

func (t *PortrTunnel) forward(remoteConn net.Conn) {
	defer remoteConn.Close()

	targetConn, err := net.DialTimeout("tcp", t.config.Target, 10*time.Second)
	if err != nil {
		return
	}
	defer targetConn.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(targetConn, remoteConn)
		_ = closeWrite(targetConn)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(remoteConn, targetConn)
		_ = closeWrite(remoteConn)
		done <- struct{}{}
	}()
	<-done
}

func closeWrite(conn net.Conn) error {
	type closeWriter interface {
		CloseWrite() error
	}
	if cw, ok := conn.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return conn.Close()
}

func normalizeHTTPURL(raw string) string {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "localhost:") || strings.HasPrefix(raw, "127.0.0.1:") {
		return "http://" + raw
	}
	return "https://" + raw
}

func publicHTTPURL(domain, subdomain, path string) string {
	base := fmt.Sprintf("https://%s.%s", subdomain, publicShareDomainHost(domain))
	if path == "" || path == "/" {
		return base
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func publicTCPURL(domain string, remotePort int) string {
	return fmt.Sprintf("%s:%d", publicShareDomainHost(domain), remotePort)
}

func randomSubdomain(length int) string {
	if length <= 0 {
		length = 8
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			out[i] = alphabet[int(time.Now().UnixNano()%int64(len(alphabet)))]
			continue
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out)
}

func randomShareID() string {
	return "sh_" + randomSubdomain(16)
}

func randomRemotePorts(shareType string) []int {
	start, end := 20000, 30000
	if shareType == ShareTypeTCP {
		start, end = 30001, 40001
	}
	const count = 10
	ports := make([]int, 0, count)
	used := make(map[int]bool)
	for len(ports) < count {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(end-start)))
		if err != nil {
			port := start + len(ports)
			ports = append(ports, port)
			continue
		}
		port := start + int(n.Int64())
		if used[port] {
			continue
		}
		used[port] = true
		ports = append(ports, port)
	}
	return ports
}
