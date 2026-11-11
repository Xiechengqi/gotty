package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestFontAssetsCacheAndConditionalRequests(t *testing.T) {
	var files fs.FS = fstest.MapFS{"js/fonts/test-12345678.woff2": {Data: []byte("font-data")}}
	handler := fontAssets(files, "/prefix/js/fonts/")
	request := httptest.NewRequest(http.MethodGet, "/prefix/js/fonts/test-12345678.woff2", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get("Content-Type") != "font/woff2" || response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected font response: %v", response.Result())
	}
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	handler.ServeHTTP(cached, request)
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatalf("conditional request: %d", cached.Code)
	}
	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/prefix/js/fonts/missing.woff2", nil))
	if missing.Code != 404 {
		t.Fatalf("missing font: %d", missing.Code)
	}
}
