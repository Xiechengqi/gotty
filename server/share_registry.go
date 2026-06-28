package server

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
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
	data, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	var records []ShareRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return err
	}
	for _, record := range records {
		if record.ID != "" {
			r.records[record.ID] = record
		}
	}
	return nil
}

func (r *ShareRegistry) List() []ShareRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	records := make([]ShareRecord, 0, len(r.records))
	for _, record := range r.records {
		records = append(records, record)
	}
	return records
}

func (r *ShareRegistry) Get(id string) (ShareRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.records[id]
	return record, ok
}

func (r *ShareRegistry) Upsert(record ShareRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[record.ID] = record
	return r.saveLocked()
}

func (r *ShareRegistry) Update(id string, update func(*ShareRecord)) (ShareRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[id]
	if !ok {
		return ShareRecord{}, os.ErrNotExist
	}
	update(&record)
	r.records[id] = record
	return record, r.saveLocked()
}

func (r *ShareRegistry) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, id)
	return r.saveLocked()
}

func (r *ShareRegistry) MarkStartupState(restore bool) error {
	now := time.Now().UTC()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, record := range r.records {
		if record.Status != ShareStatusActive && record.Status != ShareStatusCreating {
			continue
		}
		if !record.ExpiresAt.IsZero() && !record.ExpiresAt.After(now) {
			record.Status = ShareStatusExpired
		} else if !restore {
			record.Status = ShareStatusLost
			record.LastError = "gotty restarted before this share was stopped"
		}
		r.records[id] = record
	}
	return r.saveLocked()
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
