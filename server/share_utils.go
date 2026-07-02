package server

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"strings"
)

const shareSubdomainPrefix = "gotty-"

func normalizeHTTPURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "localhost:") || strings.HasPrefix(raw, "127.0.0.1:") {
		return "http://" + raw
	}
	return "https://" + raw
}

func normalizeShareSubdomain(input string) (string, string, error) {
	suffix := strings.TrimSpace(strings.ToLower(input))
	suffix = strings.TrimPrefix(suffix, shareSubdomainPrefix)
	if suffix == "" {
		suffix = randomLowercaseString(6)
	}
	if len(suffix) > 63-len(shareSubdomainPrefix) {
		return "", "", fmt.Errorf("subdomain suffix is too long")
	}
	if strings.HasPrefix(suffix, "-") || strings.HasSuffix(suffix, "-") {
		return "", "", fmt.Errorf("subdomain suffix must not start or end with '-'")
	}
	for _, ch := range suffix {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '-' {
			continue
		}
		return "", "", fmt.Errorf("subdomain suffix must contain only letters, digits, and '-'")
	}
	return suffix, shareSubdomainPrefix + suffix, nil
}

func publicHTTPURL(serverURL, subdomain, path string) string {
	domain := publicShareDomainHost(serverURL)
	if domain == "" {
		return ""
	}
	scheme := "https"
	normalized := normalizeHTTPURL(serverURL)
	if parsed, err := url.Parse(normalized); err == nil && parsed.Scheme != "" {
		scheme = parsed.Scheme
	}
	return appendPublicPath(fmt.Sprintf("%s://%s.%s", scheme, subdomain, domain), path)
}

func appendPublicPath(base, path string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return ""
	}
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return base
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

func randomShareID() string {
	return "sh_" + randomString("abcdefghijklmnopqrstuvwxyz0123456789", 16)
}

func randomLowercaseString(length int) string {
	return randomString("abcdefghijklmnopqrstuvwxyz", length)
}

func randomString(alphabet string, length int) string {
	if length <= 0 {
		length = 8
	}
	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			out[i] = alphabet[i%len(alphabet)]
			continue
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out)
}
