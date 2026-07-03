package server

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sorenisanerd/gotty/pkg/homedir"
)

type ShareRegistry struct {
	path    string
	records map[string]ShareRecord
	mu      sync.RWMutex
}

func NewShareRegistry(path string) (*ShareRegistry, error) {
	if path == "" {
		path = "~/.gotty-shares.json"
	}
	registry := &ShareRegistry{
		path:    homedir.Expand(path),
		records: make(map[string]ShareRecord),
	}
	if err := registry.load(); err != nil {
		return nil, err
	}
	return registry, nil
}

func (r *ShareRegistry) load() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.loadLocked()
}

func (r *ShareRegistry) loadLocked() error {
	records := make(map[string]ShareRecord)
	data, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r.records = records
			return nil
		}
		return err
	}
	if len(data) == 0 {
		r.records = records
		return nil
	}
	var list []ShareRecord
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	for _, record := range list {
		if record.ID != "" {
			records[record.ID] = record
		}
	}
	r.records = records
	return nil
}

func (r *ShareRegistry) List() []ShareRecord {
	r.mu.Lock()
	defer r.mu.Unlock()

	_ = r.loadLocked()

	records := make([]ShareRecord, 0, len(r.records))
	for _, record := range r.records {
		records = append(records, record)
	}
	return records
}

func (r *ShareRegistry) Get(id string) (ShareRecord, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_ = r.loadLocked()

	record, ok := r.records[id]
	return record, ok
}

func (r *ShareRegistry) Upsert(record ShareRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.withExclusiveFileLock(func() error {
		if err := r.loadLocked(); err != nil {
			return err
		}
		r.records[record.ID] = record
		return r.saveLocked()
	})
}

func (r *ShareRegistry) Update(id string, update func(*ShareRecord)) (ShareRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var updated ShareRecord
	err := r.withExclusiveFileLock(func() error {
		if err := r.loadLocked(); err != nil {
			return err
		}
		record, ok := r.records[id]
		if !ok {
			return os.ErrNotExist
		}
		update(&record)
		r.records[id] = record
		updated = record
		return r.saveLocked()
	})
	if err != nil {
		return ShareRecord{}, err
	}
	return updated, nil
}

func (r *ShareRegistry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.withExclusiveFileLock(func() error {
		if err := r.loadLocked(); err != nil {
			return err
		}
		delete(r.records, id)
		return r.saveLocked()
	})
}

func (r *ShareRegistry) MarkStartupState(ownerID, defaultTarget string) error {
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.withExclusiveFileLock(func() error {
		if err := r.loadLocked(); err != nil {
			return err
		}
		for id, record := range r.records {
			if shouldAdoptOwnerlessShare(record, defaultTarget) {
				record.OwnerID = ownerID
			}
			if record.OwnerID != ownerID {
				r.records[id] = record
				continue
			}
			if record.Status != ShareStatusActive && record.Status != ShareStatusCreating {
				r.records[id] = record
				continue
			}
			if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
				record.Status = ShareStatusExpired
			} else {
				record.Status = ShareStatusLost
				record.LastError = "gotty restarted before this share was stopped"
			}
			r.records[id] = record
		}
		return r.saveLocked()
	})
}

func (r *ShareRegistry) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	records := make([]ShareRecord, 0, len(r.records))
	for _, record := range r.records {
		records = append(records, record)
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}

func (r *ShareRegistry) withExclusiveFileLock(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return err
	}
	lockPath := r.path + ".lock"
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)

	return fn()
}

func shouldAdoptOwnerlessShare(record ShareRecord, defaultTarget string) bool {
	if strings.TrimSpace(record.OwnerID) != "" || strings.TrimSpace(defaultTarget) == "" {
		return false
	}
	if record.Type != "" && record.Type != ShareTypeHTTP {
		return false
	}
	if !record.IsTerminal {
		return false
	}
	return shareTargetHost(record.Target) == shareTargetHost(defaultTarget)
}

func shareTargetHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(normalizeHTTPURL(raw))
	if err != nil || parsed.Host == "" {
		return strings.ToLower(raw)
	}
	return strings.ToLower(parsed.Host)
}
