package server

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// Content-addressed font URLs are public program resources, never user data.
func fontAssets(files fs.FS, prefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, prefix)
		if !strings.HasSuffix(name, ".woff2") || strings.Contains(name, "/") || !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(files, "js/fonts/"+name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "font/woff2")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", fmt.Sprintf("\"%x\"", sha256.Sum256(data)))
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}
