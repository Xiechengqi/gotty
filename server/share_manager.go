package server

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sorenisanerd/gotty/pkg/homedir"
)

type activeShare struct {
	cancel context.CancelFunc
	tunnel *HTTPTunnelClient
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

func (m *ShareManager) missingConfig() []string {
	var missing []string
	if strings.TrimSpace(m.options.ShareServerURL) == "" {
		missing = append(missing, "share-server-url")
	}
	return missing
}

func (m *ShareManager) validateConfig() error {
	missing := m.missingConfig()
	if len(missing) > 0 {
		return fmt.Errorf("HTTP tunnel share settings are incomplete: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (m *ShareManager) Close() {
	m.cancel()
	m.mu.Lock()
	active := make([]activeShare, 0, len(m.active))
	for _, share := range m.active {
		active = append(active, share)
	}
	m.active = make(map[string]activeShare)
	m.mu.Unlock()

	for _, share := range active {
		share.cancel()
		share.tunnel.Close()
	}
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
		if records[i].Status == ShareStatusActive && records[i].ExpiresAt != nil && !records[i].ExpiresAt.After(now) {
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
	if shareType != ShareTypeHTTP {
		return ShareRecord{}, fmt.Errorf("share type must be http")
	}
	if err := m.validateConfig(); err != nil {
		return ShareRecord{}, err
	}

	m.mu.Lock()
	defaultTarget := m.defaultTarget
	defaultPath := m.defaultPath
	m.mu.Unlock()

	requestedTarget := strings.TrimSpace(req.Target)
	isTerminal := requestedTarget == "" || requestedTarget == defaultTarget
	target := requestedTarget
	publicPath := ""
	if isTerminal {
		target = defaultTarget
		publicPath = defaultPath
		if target == "" {
			return ShareRecord{}, fmt.Errorf("gotty listener is not ready")
		}
	}

	displayTarget, targetURL, err := normalizeShareTarget(m.ctx, target)
	if err != nil {
		return ShareRecord{}, err
	}

	now := time.Now().UTC()
	ttlSeconds, expiresAt, err := parseShareExpiry(req, now)
	if err != nil {
		return ShareRecord{}, err
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

	autoSubdomain := strings.TrimSpace(req.Subdomain) == ""
	var lastErr error
	for i := 0; i < 8; i++ {
		_, subdomain, err := normalizeShareSubdomain(req.Subdomain)
		if err != nil {
			return ShareRecord{}, err
		}
		record, err := m.createShareWithSubdomain(displayTarget, targetURL, subdomain, publicPath, ttlSeconds, expiresAt, isTerminal)
		if err == nil {
			return record, nil
		}
		lastErr = err
		if !autoSubdomain || !isSubdomainConflict(err) {
			break
		}
	}
	return m.saveFailedShare(shareType, displayTarget, "", ttlSeconds, expiresAt, isTerminal, lastErr)
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

	var stopErr error
	if ok {
		share.cancel()
		stopErr = share.tunnel.Stop(context.Background())
	}

	now := time.Now().UTC()
	return m.registry.Update(id, func(record *ShareRecord) {
		record.Status = status
		record.StoppedAt = &now
		if stopErr != nil {
			record.LastError = stopErr.Error()
		}
	})
}

func (m *ShareManager) RestartShare(id string) (ShareRecord, error) {
	record, ok := m.registry.Get(id)
	if !ok {
		return ShareRecord{}, fmt.Errorf("share not found")
	}
	if record.Status == ShareStatusActive {
		return record, nil
	}
	if record.Type != "" && record.Type != ShareTypeHTTP {
		return ShareRecord{}, fmt.Errorf("share type must be http")
	}
	if err := m.validateConfig(); err != nil {
		return ShareRecord{}, err
	}

	now := time.Now().UTC()
	ttlSeconds := record.TTLSeconds
	var expiresAt *time.Time
	if ttlSeconds > 0 {
		nextExpiresAt := now.Add(time.Duration(ttlSeconds) * time.Second)
		expiresAt = &nextExpiresAt
	}

	m.mu.Lock()
	if _, active := m.active[id]; active {
		m.mu.Unlock()
		return record, nil
	}
	if len(m.active)+m.creating >= m.options.ShareMaxActive {
		m.mu.Unlock()
		return ShareRecord{}, fmt.Errorf("maximum active shares reached")
	}
	defaultTarget := m.defaultTarget
	defaultPath := m.defaultPath
	m.creating++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.creating > 0 {
			m.creating--
		}
		m.mu.Unlock()
	}()

	requestedTarget := strings.TrimSpace(restartTarget(record))
	isTerminal := record.IsTerminal || requestedTarget == "" || requestedTarget == defaultTarget
	target := requestedTarget
	publicPath := ""
	if isTerminal {
		target = defaultTarget
		publicPath = defaultPath
		if target == "" {
			err := fmt.Errorf("gotty listener is not ready")
			m.markShareRestartFailed(id, err)
			return ShareRecord{}, err
		}
	}

	displayTarget, targetURL, err := normalizeShareTarget(m.ctx, target)
	if err != nil {
		m.markShareRestartFailed(id, err)
		return ShareRecord{}, err
	}
	_, subdomain, err := normalizeShareSubdomain(record.Subdomain)
	if err != nil {
		m.markShareRestartFailed(id, err)
		return ShareRecord{}, err
	}
	if err := m.stopStoredShare(id); err != nil {
		m.markShareRestartFailed(id, err)
		return ShareRecord{}, err
	}

	restarted, err := m.startShareWithID(id, displayTarget, targetURL, subdomain, publicPath, ttlSeconds, expiresAt, isTerminal, now)
	if err != nil {
		m.markShareRestartFailed(id, err)
		return ShareRecord{}, err
	}
	return restarted, nil
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

func (m *ShareManager) createShareWithSubdomain(displayTarget, targetURL, subdomain, publicPath string, ttlSeconds int, expiresAt *time.Time, isTerminal bool) (ShareRecord, error) {
	return m.startShareWithID(randomShareID(), displayTarget, targetURL, subdomain, publicPath, ttlSeconds, expiresAt, isTerminal, time.Now().UTC())
}

func (m *ShareManager) startShareWithID(id, displayTarget, targetURL, subdomain, publicPath string, ttlSeconds int, expiresAt *time.Time, isTerminal bool, createdAt time.Time) (ShareRecord, error) {
	now := time.Now().UTC()
	if createdAt.IsZero() {
		createdAt = now
	}
	record := ShareRecord{
		ID:         id,
		Type:       ShareTypeHTTP,
		Target:     displayTarget,
		Subdomain:  subdomain,
		TTLSeconds: ttlSeconds,
		Status:     ShareStatusCreating,
		CreatedAt:  createdAt,
		ExpiresAt:  expiresAt,
		IsTerminal: isTerminal,
	}

	clientPath, err := ResolveHTTPTunnelClientPath(m.options.ShareClientPath, m.options.ShareRuntimeDir)
	if err != nil {
		return ShareRecord{}, err
	}

	ctx, cancel := context.WithCancel(m.ctx)
	tunnel := NewHTTPTunnelClient(HTTPTunnelConfig{
		ClientPath:  clientPath,
		RuntimeDir:  m.shareRuntimeDir(id),
		ServerURL:   m.options.ShareServerURL,
		TargetURL:   targetURL,
		Subdomain:   subdomain,
		CreateToken: m.options.ShareCreateToken,
		TTLSeconds:  ttlSeconds,
	})

	info, err := tunnel.Start(ctx)
	if err != nil {
		cancel()
		tunnel.Close()
		return ShareRecord{}, err
	}

	record.ConnectionID = info.TunnelID
	record.Status = ShareStatusActive
	record.PublicURL = appendPublicPath(info.PublicURL, publicPath)
	if record.PublicURL == "" {
		record.PublicURL = publicHTTPURL(m.options.ShareServerURL, subdomain, publicPath)
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
	if expiresAt != nil {
		go m.expireShare(ctx, id, *expiresAt)
	}

	return record, nil
}

func (m *ShareManager) markShareRestartFailed(id string, err error) {
	if err == nil {
		return
	}
	now := time.Now().UTC()
	_, _ = m.registry.Update(id, func(record *ShareRecord) {
		record.Status = ShareStatusFailed
		record.StoppedAt = &now
		record.LastError = err.Error()
	})
}

func (m *ShareManager) stopStoredShare(id string) error {
	clientPath, err := ResolveHTTPTunnelClientPath(m.options.ShareClientPath, m.options.ShareRuntimeDir)
	if err != nil {
		return err
	}
	tunnel := NewHTTPTunnelClient(HTTPTunnelConfig{
		ClientPath:  clientPath,
		RuntimeDir:  m.shareRuntimeDir(id),
		ServerURL:   m.options.ShareServerURL,
		CreateToken: m.options.ShareCreateToken,
	})
	return tunnel.Stop(context.Background())
}

func (m *ShareManager) runShare(ctx context.Context, id string, tunnel *HTTPTunnelClient) {
	err := tunnel.Serve(ctx)
	if ctx.Err() != nil {
		return
	}

	m.mu.Lock()
	delete(m.active, id)
	m.mu.Unlock()

	now := time.Now().UTC()
	_, _ = m.registry.Update(id, func(record *ShareRecord) {
		if record.Status != ShareStatusActive {
			return
		}
		if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
			record.Status = ShareStatusExpired
		} else if tunnel.LastDisconnectReason() == "tunnel_expired" {
			record.Status = ShareStatusExpired
		} else {
			record.Status = ShareStatusFailed
		}
		if err != nil {
			record.LastError = err.Error()
		} else if reason := tunnel.LastDisconnectReason(); reason != "" {
			record.LastError = reason
		} else {
			record.LastError = "share tunnel stopped"
		}
		record.StoppedAt = &now
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

func (m *ShareManager) saveFailedShare(shareType, target, subdomain string, ttlSeconds int, expiresAt *time.Time, isTerminal bool, err error) (ShareRecord, error) {
	now := time.Now().UTC()
	message := "failed to create share"
	if err != nil {
		message = err.Error()
	}
	record := ShareRecord{
		ID:         randomShareID(),
		Type:       shareType,
		Target:     target,
		Subdomain:  subdomain,
		TTLSeconds: ttlSeconds,
		Status:     ShareStatusFailed,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		StoppedAt:  &now,
		LastError:  message,
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
		if record.Status != ShareStatusLost {
			continue
		}
		ttl := 0
		if record.ExpiresAt != nil {
			if !record.ExpiresAt.After(time.Now().UTC()) {
				continue
			}
			ttl = int(time.Until(*record.ExpiresAt).Seconds())
			if ttl <= 0 {
				continue
			}
		}
		_, _ = m.CreateShare(shareCreateRequest{
			Type:       ShareTypeHTTP,
			Target:     restartTarget(record),
			Subdomain:  record.Subdomain,
			TTLSeconds: ttl,
		})
	}
}

func (m *ShareManager) shareRuntimeDir(id string) string {
	root := strings.TrimSpace(m.options.ShareRuntimeDir)
	if root == "" {
		root = "~/.gotty-http-tunnel"
	}
	return filepath.Join(homedir.Expand(root), id)
}

func restartTarget(record ShareRecord) string {
	if record.IsTerminal {
		return ""
	}
	return record.Target
}

func parseShareExpiry(req shareCreateRequest, now time.Time) (int, *time.Time, error) {
	if req.ExpireValue > 0 {
		seconds, err := expireSeconds(req.ExpireValue, req.ExpireUnit)
		if err != nil {
			return 0, nil, err
		}
		expiresAt := now.Add(time.Duration(seconds) * time.Second)
		return seconds, &expiresAt, nil
	}
	if strings.TrimSpace(req.ExpireUnit) != "" {
		return 0, nil, fmt.Errorf("expiry value must be a positive integer")
	}
	if req.TTLSeconds > 0 {
		if req.TTLSeconds < 60 {
			return 0, nil, fmt.Errorf("ttl_seconds must be at least 60")
		}
		expiresAt := now.Add(time.Duration(req.TTLSeconds) * time.Second)
		return req.TTLSeconds, &expiresAt, nil
	}
	return 0, nil, nil
}

func expireSeconds(value int, unit string) (int, error) {
	if value <= 0 {
		return 0, fmt.Errorf("expiry value must be a positive integer")
	}
	var multiplier int
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "minute", "minutes":
		multiplier = 60
	case "hour", "hours":
		multiplier = 60 * 60
	case "day", "days":
		multiplier = 24 * 60 * 60
	default:
		return 0, fmt.Errorf("expiry unit must be minutes, hours, or days")
	}
	if value > int(^uint(0)>>1)/multiplier {
		return 0, fmt.Errorf("expiry is too large")
	}
	return value * multiplier, nil
}

func normalizeShareTarget(ctx context.Context, target string) (string, string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("target is required")
	}

	if strings.Contains(target, "://") {
		parsed, err := url.Parse(target)
		if err != nil {
			return "", "", fmt.Errorf("target URL is invalid")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", "", fmt.Errorf("target URL scheme must be http or https")
		}
		if parsed.Host == "" {
			return "", "", fmt.Errorf("target host is required")
		}
		if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", fmt.Errorf("target URL must not include credentials, query, or fragment")
		}
		if err := validateShareTargetHostPort(ctx, parsed.Host); err != nil {
			return "", "", err
		}
		return strings.TrimRight(target, "/"), strings.TrimRight(target, "/"), nil
	}

	if err := validateShareTargetHostPort(ctx, target); err != nil {
		return "", "", err
	}
	return target, "http://" + target, nil
}

func validateShareTarget(ctx context.Context, target string) error {
	_, _, err := normalizeShareTarget(ctx, target)
	return err
}

func validateShareTargetHostPort(ctx context.Context, target string) error {
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

func isSubdomainConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "subdomain") ||
		strings.Contains(message, "already in use") ||
		strings.Contains(message, "already reserved") ||
		strings.Contains(message, "duplicate") ||
		strings.Contains(message, "conflict")
}
