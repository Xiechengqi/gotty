package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// A process restart must retain its identity, while independent listeners must
// never inherit the credentials most recently written by another listener.
func hubStatePaths(options *Options, userDir string) (state, legacy string) {
	if options.HubStateFile != "" {
		if strings.HasPrefix(options.HubStateFile, "~/") {
			return filepath.Join(userDir, options.HubStateFile[2:]), ""
		}
		return options.HubStateFile, ""
	}
	base := filepath.Join(userDir, ".gotty")
	legacy = filepath.Join(base, "hub.json")
	stateDir := filepath.Join(base, "hub")
	if info, err := os.Stat(base); err == nil && !info.IsDir() {
		// .gotty-hub is the Hub server's own data directory. Keep device
		// credentials separate so removing a Hub installation cannot erase them.
		stateDir = filepath.Join(userDir, ".gotty-hub-bindings")
		legacy = filepath.Join(userDir, ".gotty-hub.json")
	}
	addresses := strings.Split(options.Address, ",")
	for i := range addresses {
		addresses[i] = strings.TrimSpace(addresses[i])
		if addresses[i] == "" {
			addresses[i] = "0.0.0.0"
		}
	}
	sort.Strings(addresses)
	port := options.Port
	if port == "" {
		port = "8080"
	}
	identity, _ := json.Marshal([]string{strings.Join(addresses, ","), port, path.Clean("/" + options.Path)})
	sum := sha256.Sum256(identity)
	return filepath.Join(stateDir, hex.EncodeToString(sum[:16])+".json"), legacy
}

// Keep the lock inode across atomic state-file replacements and process exits.
// Removing it would let concurrent processes lock different inodes at one path.
func lockHubState(state string) (*os.File, string, error) {
	abs, err := filepath.Abs(state)
	if err != nil {
		return nil, "", err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, "", err
	}
	if resolved, e := filepath.EvalSymlinks(abs); e == nil {
		abs = resolved
	} else {
		dir, e := filepath.EvalSymlinks(filepath.Dir(abs))
		if e != nil {
			return nil, "", e
		}
		abs = filepath.Join(dir, filepath.Base(abs))
	}
	file, err := os.OpenFile(abs+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, "", err
	}
	if err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, "", fmt.Errorf("Hub identity file %q is already in use or cannot be locked: %w; use a separate --hub-state-file for each GoTTY instance", abs, err)
	}
	return file, abs, nil
}

func (h *hubManager) closeState() {
	h.closeStateOnce.Do(func() {
		if h.stateLock != nil {
			_ = h.stateLock.Close()
		}
	})
}
