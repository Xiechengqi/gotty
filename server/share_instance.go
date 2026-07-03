package server

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

const shareOwnerEnv = "GOTTY_SHARE_INSTANCE_ID"

func resolveShareOwnerID(options *Options, defaultTarget, defaultPath string) string {
	if options != nil {
		if ownerID := safeShareOwnerID(options.ShareInstanceID); ownerID != "" {
			return ownerID
		}
	}
	if ownerID := safeShareOwnerID(os.Getenv(shareOwnerEnv)); ownerID != "" {
		return ownerID
	}
	return generatedShareOwnerID(options, defaultTarget, defaultPath)
}

func generatedShareOwnerID(options *Options, defaultTarget, defaultPath string) string {
	parts := []string{
		"gotty-share-owner-v1",
		defaultTarget,
		defaultPath,
	}
	if options != nil {
		parts = append(parts, options.Address, options.Port, options.Path)
	}
	if home, err := os.UserHomeDir(); err == nil {
		parts = append(parts, home)
	}
	if cwd, err := os.Getwd(); err == nil {
		parts = append(parts, filepath.Clean(cwd))
	}
	if exe, err := os.Executable(); err == nil {
		parts = append(parts, filepath.Clean(strings.TrimSuffix(exe, " (deleted)")))
	}
	parts = append(parts, os.Args...)

	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "auto-" + hex.EncodeToString(sum[:8])
}

func safeShareOwnerID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}

	var out strings.Builder
	lastDash := false
	for _, ch := range strings.ToLower(value) {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '.' || ch == '-'
		if valid {
			out.WriteRune(ch)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}

	normalized := strings.Trim(out.String(), "-.")
	if normalized == "" {
		return "id-" + shareOwnerHash(value, 8)
	}
	if len(normalized) > 80 {
		prefix := strings.TrimRight(normalized[:63], "-.")
		if prefix == "" {
			return "id-" + shareOwnerHash(value, 8)
		}
		normalized = prefix + "-" + shareOwnerHash(value, 8)
	}
	return normalized
}

func shareOwnerHash(value string, bytes int) string {
	sum := sha256.Sum256([]byte(value))
	if bytes <= 0 || bytes > len(sum) {
		bytes = len(sum)
	}
	return hex.EncodeToString(sum[:bytes])
}
