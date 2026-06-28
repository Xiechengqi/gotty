package server

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type activeShare struct {
	cancel context.CancelFunc
	tunnel *PortrTunnel
}

type ShareManager struct {
	options       *Options
	registry      *ShareRegistry
	active        map[string]activeShare
	creating      int
	defaultTarget string
	defaultPath   string
	mu            sync.Mutex
	ctx           context.Context
	cancel        context.CancelFunc
}

func NewShareManager(parent context.Context, options *Options) (*ShareManager, error) {
	registry, err := NewShareRegistry(options.ShareRegistryFile)
	if err != nil {
		return nil, err
	}
	if err := registry.MarkStartupState(false); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	manager := &ShareManager{
		options:  options,
		registry: registry,
		active:   make(map[string]activeShare),
		ctx:      ctx,
		cancel:   cancel,
	}
	return manager, nil
}

func (m *ShareManager) validateConfig() error {
	var missing []string
	if strings.TrimSpace(m.options.ShareServerURL) == "" {
		missing = append(missing, "share-server-url")
	}
	if strings.TrimSpace(m.options.ShareSSHURL) == "" {
		missing = append(missing, "share-ssh-url")
	}
	if strings.TrimSpace(m.options.ShareTunnelDomain) == "" {
		missing = append(missing, "share-tunnel-domain")
	}
	if strings.TrimSpace(m.options.ShareSecretKey) == "" {
		missing = append(missing, "share-secret-key")
	}
	if len(missing) > 0 {
		return fmt.Errorf("Portr share settings are incomplete: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (m *ShareManager) Close() {
	m.cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, share := range m.active {
		share.cancel()
		share.tunnel.Close()
	}
	m.active = make(map[string]activeShare)
}

func (m *ShareManager) SetDefaultTarget(target, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultTarget = target
	m.defaultPath = path
}

func (m *ShareManager) DefaultTarget() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.defaultTarget
}

func (m *ShareManager) List() []ShareRecord {
	records := m.registry.List()
	now := time.Now().UTC()
	for i := range records {
		if records[i].Status == ShareStatusActive && !records[i].ExpiresAt.After(now) {
			updated, err := m.registry.Update(records[i].ID, func(record *ShareRecord) {
				record.Status = ShareStatusExpired
				stoppedAt := now
				record.StoppedAt = &stoppedAt
			})
			if err == nil {
				records[i] = updated
			} else {
				records[i].Status = ShareStatusExpired
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Status == ShareStatusActive && records[j].Status != ShareStatusActive {
			return true
		}
		if records[i].Status != ShareStatusActive && records[j].Status == ShareStatusActive {
			return false
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})
	return records
}

func (m *ShareManager) CreateShare(req shareCreateRequest) (ShareRecord, error) {
	shareType := req.Type
	if shareType == "" {
		shareType = ShareTypeHTTP
	}
	if shareType != ShareTypeHTTP && shareType != ShareTypeTCP {
		return ShareRecord{}, fmt.Errorf("share type must be http or tcp")
	}
	if err := m.validateConfig(); err != nil {
		return ShareRecord{}, err
	}

	isTerminal := strings.TrimSpace(req.Target) == ""
	target := strings.TrimSpace(req.Target)
	publicPath := ""
	if isTerminal {
		m.mu.Lock()
		target = m.defaultTarget
		publicPath = m.defaultPath
		m.mu.Unlock()
		if target == "" {
			return ShareRecord{}, fmt.Errorf("gotty listener is not ready")
		}
	}

	if err := validateShareTarget(m.ctx, target); err != nil {
		return ShareRecord{}, err
	}

	ttl := req.TTLSeconds
	if ttl <= 0 {
		ttl = m.options.ShareDefaultTTLSeconds
	}
	if ttl > m.options.ShareMaxTTLSeconds {
		ttl = m.options.ShareMaxTTLSeconds
	}

	m.mu.Lock()
	if len(m.active)+m.creating >= m.options.ShareMaxActive {
		m.mu.Unlock()
		return ShareRecord{}, fmt.Errorf("maximum active shares reached")
	}
	m.creating++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.creating > 0 {
			m.creating--
		}
		m.mu.Unlock()
	}()

	if shareType == ShareTypeHTTP {
		var lastErr error
		for i := 0; i < 8; i++ {
			subdomain := randomSubdomain(8)
			record, err := m.createShareWithSubdomain(shareType, target, subdomain, publicPath, ttl, isTerminal)
			if err == nil {
				return record, nil
			}
			lastErr = err
			if !strings.Contains(strings.ToLower(err.Error()), "subdomain") &&
				!strings.Contains(strings.ToLower(err.Error()), "already in use") &&
				!strings.Contains(strings.ToLower(err.Error()), "conflict") {
				break
			}
		}
		return m.saveFailedShare(shareType, target, "", ttl, isTerminal, lastErr)
	}

	record, err := m.createShareWithSubdomain(shareType, target, "", "", ttl, isTerminal)
	if err != nil {
		return m.saveFailedShare(shareType, target, "", ttl, isTerminal, err)
	}
	return record, nil
}

func (m *ShareManager) StopShare(id string, status string) (ShareRecord, error) {
	if status == "" {
		status = ShareStatusStopped
	}

	m.mu.Lock()
	share, ok := m.active[id]
	if ok {
		delete(m.active, id)
	}
	m.mu.Unlock()
	if ok {
		share.cancel()
		share.tunnel.Close()
	}

	now := time.Now().UTC()
	return m.registry.Update(id, func(record *ShareRecord) {
		record.Status = status
		record.StoppedAt = &now
	})
}

func (m *ShareManager) RestartShare(id string) (ShareRecord, error) {
	record, ok := m.registry.Get(id)
	if !ok {
		return ShareRecord{}, fmt.Errorf("share not found")
	}
	return m.CreateShare(shareCreateRequest{
		Type:       record.Type,
		Target:     restartTarget(record),
		TTLSeconds: m.options.ShareDefaultTTLSeconds,
	})
}

func (m *ShareManager) DeleteRecord(id string) error {
	m.mu.Lock()
	_, active := m.active[id]
	m.mu.Unlock()
	if active {
		return fmt.Errorf("active share must be stopped before deleting")
	}
	return m.registry.Delete(id)
}

func (m *ShareManager) createShareWithSubdomain(shareType, target, subdomain, publicPath string, ttl int, isTerminal bool) (ShareRecord, error) {
	now := time.Now().UTC()
	id := randomShareID()
	record := ShareRecord{
		ID:        id,
		Type:      shareType,
		Target:    target,
		Subdomain: subdomain,
		Status:    ShareStatusCreating,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(ttl) * time.Second),
		IsTerminal: isTerminal,
	}

	ctx, cancel := context.WithCancel(m.ctx)
	tunnel := NewPortrTunnel(PortrTunnelConfig{
		Type:                         shareType,
		Target:                       target,
		Subdomain:                    subdomain,
		ServerURL:                     m.options.ShareServerURL,
		SSHURL:                        m.options.ShareSSHURL,
		SecretKey:                     m.options.ShareSecretKey,
		InsecureSkipHostKeyValidation: m.options.ShareInsecureSkipHostKeyValidation,
	})

	connectionID, remotePort, err := tunnel.Start(ctx)
	if err != nil {
		cancel()
		tunnel.Close()
		return ShareRecord{}, err
	}

	record.ConnectionID = connectionID
	record.RemotePort = remotePort
	record.Status = ShareStatusActive
	if shareType == ShareTypeHTTP {
		record.PublicURL = publicHTTPURL(m.options.ShareTunnelDomain, subdomain, publicPath)
	} else {
		record.PublicURL = publicTCPURL(m.options.ShareTunnelDomain, remotePort)
	}
	if err := m.registry.Upsert(record); err != nil {
		cancel()
		tunnel.Close()
		return ShareRecord{}, err
	}

	m.mu.Lock()
	m.active[id] = activeShare{cancel: cancel, tunnel: tunnel}
	m.mu.Unlock()

	go m.runShare(ctx, id, tunnel)
	go m.expireShare(ctx, id, record.ExpiresAt)

	return record, nil
}

func (m *ShareManager) runShare(ctx context.Context, id string, tunnel *PortrTunnel) {
	err := tunnel.Serve(ctx)
	if ctx.Err() != nil {
		return
	}

	m.mu.Lock()
	delete(m.active, id)
	m.mu.Unlock()

	message := "share tunnel stopped"
	if err != nil {
		message = err.Error()
	}
	now := time.Now().UTC()
	_, _ = m.registry.Update(id, func(record *ShareRecord) {
		if record.Status == ShareStatusActive {
			record.Status = ShareStatusFailed
			record.LastError = message
			record.StoppedAt = &now
		}
	})
}

func (m *ShareManager) expireShare(ctx context.Context, id string, expiresAt time.Time) {
	timer := time.NewTimer(time.Until(expiresAt))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
		_, _ = m.StopShare(id, ShareStatusExpired)
	}
}

func (m *ShareManager) saveFailedShare(shareType, target, subdomain string, ttl int, isTerminal bool, err error) (ShareRecord, error) {
	now := time.Now().UTC()
	message := "failed to create share"
	if err != nil {
		message = err.Error()
	}
	record := ShareRecord{
		ID:        randomShareID(),
		Type:      shareType,
		Target:    target,
		Subdomain: subdomain,
		Status:    ShareStatusFailed,
		CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(ttl) * time.Second),
		StoppedAt: &now,
		LastError: message,
		IsTerminal: isTerminal,
	}
	_ = m.registry.Upsert(record)
	return ShareRecord{}, err
}

func (m *ShareManager) RestoreActiveShares() {
	go m.restoreActiveShares()
}

func (m *ShareManager) restoreActiveShares() {
	for _, record := range m.registry.List() {
		if record.Status != ShareStatusLost || !record.ExpiresAt.After(time.Now().UTC()) {
			continue
		}
		ttl := int(time.Until(record.ExpiresAt).Seconds())
		if ttl <= 0 {
			continue
		}
		_, _ = m.CreateShare(shareCreateRequest{
			Type:       record.Type,
			Target:     restartTarget(record),
			TTLSeconds: ttl,
		})
	}
}

func restartTarget(record ShareRecord) string {
	if record.IsTerminal {
		return ""
	}
	return record.Target
}

func validateShareTarget(ctx context.Context, target string) error {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return fmt.Errorf("target must be host:port")
	}
	if host == "" {
		return fmt.Errorf("target host is required")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("target port must be between 1 and 65535")
	}
	if strings.ContainsAny(host, "/\\") {
		return fmt.Errorf("target host is invalid")
	}

	ips, err := resolveTargetIPs(ctx, host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if isDeniedShareIP(ip) {
			return fmt.Errorf("target host resolves to a blocked address")
		}
	}
	return nil
}

func resolveTargetIPs(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.Trim(host, "[]")
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr}, nil
	}

	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return nil, fmt.Errorf("target host could not be resolved")
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		parsed, ok := netip.AddrFromSlice(addr.IP)
		if ok {
			out = append(out, parsed.Unmap())
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("target host did not resolve to an IP address")
	}
	return out, nil
}

func isDeniedShareIP(ip netip.Addr) bool {
	if !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	if ip.Is4() {
		if ip == netip.MustParseAddr("169.254.169.254") {
			return true
		}
		if netip.MustParsePrefix("169.254.0.0/16").Contains(ip) {
			return true
		}
	}
	return false
}
