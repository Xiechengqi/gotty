package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareAuthAllowsGottyHostFromRemoteAddress(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   true,
			ShareServerURL: "https://httptunnel.top",
		},
		shareManager: &ShareManager{},
	}
	handler := server.wrapShareAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/-/shares", nil)
	req.Host = "example.com"
	req.RemoteAddr = "100.83.230.8:55717"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestShareAuthAllowsPublicTunnelHost(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   true,
			ShareServerURL: "https://httptunnel.top",
		},
		shareManager: &ShareManager{},
	}
	handler := server.wrapShareAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/-/shares", nil)
	req.Host = "gotty-demo.httptunnel.top"
	req.RemoteAddr = "100.83.230.8:55717"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestShareAuthReturnsNotFoundWhenDisabled(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   false,
			ShareServerURL: "https://httptunnel.top",
		},
		shareManager: &ShareManager{},
	}
	handler := server.wrapShareAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/-/shares", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestConfigMarksPublicShareHost(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   true,
			ShareServerURL: "https://httptunnel.top",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/config.js", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "var gotty_share_public_host = true;") {
		t.Fatalf("config.js did not mark public share host: %s", rec.Body.String())
	}
}

func TestConfigMarksNormalGottyHostAsPrivate(t *testing.T) {
	server := &Server{
		options: &Options{
			ShareEnabled:   true,
			ShareServerURL: "https://httptunnel.top",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/config.js", nil)
	req.Host = "127.0.0.1:8080"
	rec := httptest.NewRecorder()

	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "var gotty_share_public_host = false;") {
		t.Fatalf("config.js did not mark normal host as private: %s", rec.Body.String())
	}
}

func TestConfigMarksReadOnlyPasswordlessShare(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		ReadOnly:     true,
		Passwordless: true,
	})

	server.options.MobileKeys = true

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/config.js", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	server.handleConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"var gotty_share_readonly = true;",
		"var gotty_share_passwordless = true;",
		"var gotty_permit_write = false;",
		"var gotty_mobile_keys = false;",
		"var gotty_enable_asr = false;",
		"var gotty_restart_mode = 'disabled';",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("config.js missing %q: %s", want, body)
		}
	}
}

func TestPasswordlessShareSkipsBasicAuth(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{Passwordless: true})
	handler := server.wrapBasicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), server.options.Credential)

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestNonPasswordlessShareStillRequiresBasicAuth(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{})
	handler := server.wrapBasicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), server.options.Credential)

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestCustomPasswordShareSkipsGlobalBasicAuth(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		CustomPassword: true,
		PasswordHash:   mustSharePasswordHash(t, "open"),
	})
	handler := server.wrapBasicAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), server.options.Credential)

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
}

func TestCustomPasswordSharePageBlocksUntilAuthenticated(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		CustomPassword: true,
		PasswordHash:   mustSharePasswordHash(t, "open"),
	})
	called := false
	handler := server.wrapSharePasswordAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}), "/")

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("protected handler was called before share password authentication")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "Share password") {
		t.Fatalf("password page missing expected title: %s", rec.Body.String())
	}

	authReq := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/", nil)
	authReq.Host = "gotty-demo.httptunnel.top"
	record, ok := server.shareManager.GetShare("sh_access")
	if !ok {
		t.Fatal("share record missing")
	}
	authReq.AddCookie(&http.Cookie{Name: sharePasswordCookieName, Value: server.shareAccessToken(record)})
	authRec := httptest.NewRecorder()

	handler.ServeHTTP(authRec, authReq)

	if !called {
		t.Fatal("protected handler was not called after share password authentication")
	}
	if authRec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", authRec.Code, http.StatusNoContent)
	}
}

func TestCustomPasswordShareAuthSetsSessionCookie(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		CustomPassword: true,
		PasswordHash:   mustSharePasswordHash(t, "open"),
	})

	form := url.Values{}
	form.Set("password", "open")
	form.Set("next", "/config.js")
	req := httptest.NewRequest(http.MethodPost, "https://gotty-demo.httptunnel.top/-/share-auth", strings.NewReader(form.Encode()))
	req.Host = "gotty-demo.httptunnel.top"
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	server.handleSharePasswordAuth(rec, req, "/")

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != "/config.js" {
		t.Fatalf("redirect location = %q, want /config.js", got)
	}
	record, ok := server.shareManager.GetShare("sh_access")
	if !ok {
		t.Fatal("share record missing")
	}
	var found bool
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sharePasswordCookieName {
			found = true
			if cookie.Value != server.shareAccessToken(record) {
				t.Fatalf("cookie value = %q, want share access token", cookie.Value)
			}
			if !cookie.HttpOnly {
				t.Fatal("share password cookie should be HttpOnly")
			}
		}
	}
	if !found {
		t.Fatal("share password session cookie missing")
	}
}

func TestCustomPasswordShareLoginThenAuthToken(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		CustomPassword: true,
		PasswordHash:   mustSharePasswordHash(t, "open"),
	})

	form := url.Values{}
	form.Set("password", "open")
	form.Set("next", "/")
	loginReq := httptest.NewRequest(http.MethodPost, "https://gotty-demo.httptunnel.top/-/share-auth", strings.NewReader(form.Encode()))
	loginReq.Host = "gotty-demo.httptunnel.top"
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()

	server.handleSharePasswordAuth(loginRec, loginReq, "/")

	if loginRec.Code != http.StatusSeeOther {
		t.Fatalf("login status = %d, want %d; body=%s", loginRec.Code, http.StatusSeeOther, loginRec.Body.String())
	}

	authReq := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/auth_token.js", nil)
	authReq.Host = "gotty-demo.httptunnel.top"
	for _, cookie := range loginRec.Result().Cookies() {
		authReq.AddCookie(cookie)
	}
	authRec := httptest.NewRecorder()

	server.handleAuthToken(authRec, authReq)

	body := authRec.Body.String()
	if strings.Contains(body, server.options.Credential) {
		t.Fatalf("auth_token.js leaked credential after share login: %s", body)
	}
	if !strings.Contains(body, shareAccessTokenPrefix) {
		t.Fatalf("auth_token.js did not return share token after login: %s", body)
	}
}

func TestPasswordlessAuthTokenDoesNotExposeCredential(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{Passwordless: true})

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/auth_token.js", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	server.handleAuthToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if strings.Contains(body, server.options.Credential) {
		t.Fatalf("auth_token.js leaked credential: %s", body)
	}
	if !strings.Contains(body, shareAccessTokenPrefix) {
		t.Fatalf("auth_token.js did not return share token: %s", body)
	}
}

func TestCustomPasswordAuthTokenRequiresSharePasswordSession(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		CustomPassword: true,
		PasswordHash:   mustSharePasswordHash(t, "open"),
	})

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/auth_token.js", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	server.handleAuthToken(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	if strings.Contains(body, server.options.Credential) {
		t.Fatalf("auth_token.js leaked credential: %s", body)
	}
	if !strings.Contains(body, `var gotty_auth_token = "";`) {
		t.Fatalf("unauthenticated custom password share should not get token: %s", body)
	}

	record, ok := server.shareManager.GetShare("sh_access")
	if !ok {
		t.Fatal("share record missing")
	}
	authReq := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/auth_token.js", nil)
	authReq.Host = "gotty-demo.httptunnel.top"
	authReq.AddCookie(&http.Cookie{Name: sharePasswordCookieName, Value: server.shareAccessToken(record)})
	authRec := httptest.NewRecorder()

	server.handleAuthToken(authRec, authReq)

	authBody := authRec.Body.String()
	if strings.Contains(authBody, server.options.Credential) {
		t.Fatalf("auth_token.js leaked credential: %s", authBody)
	}
	if !strings.Contains(authBody, shareAccessTokenPrefix) {
		t.Fatalf("authenticated custom password share did not get share token: %s", authBody)
	}
}

func TestReadOnlyShareBlocksShareManagementAPI(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{ReadOnly: true})
	handler := server.wrapShareAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/-/shares", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestCustomPasswordShareBlocksShareManagementAPIUntilAuthenticated(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		CustomPassword: true,
		PasswordHash:   mustSharePasswordHash(t, "open"),
	})
	handler := server.wrapShareAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/-/shares", nil)
	req.Host = "gotty-demo.httptunnel.top"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	record, ok := server.shareManager.GetShare("sh_access")
	if !ok {
		t.Fatal("share record missing")
	}
	authReq := httptest.NewRequest(http.MethodGet, "https://gotty-demo.httptunnel.top/-/shares", nil)
	authReq.Host = "gotty-demo.httptunnel.top"
	authReq.AddCookie(&http.Cookie{Name: sharePasswordCookieName, Value: server.shareAccessToken(record)})
	authRec := httptest.NewRecorder()

	handler.ServeHTTP(authRec, authReq)

	if authRec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", authRec.Code, http.StatusNoContent, authRec.Body.String())
	}
}

func TestShareListDoesNotExposePasswordHash(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		CustomPassword: true,
		PasswordHash:   mustSharePasswordHash(t, "open"),
	})

	req := httptest.NewRequest(http.MethodGet, "http://localhost:2222/-/shares", nil)
	req.Host = "localhost:2222"
	rec := httptest.NewRecorder()

	server.handleShareList(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.Contains(rec.Body.String(), "password_hash") || strings.Contains(rec.Body.String(), "open") {
		t.Fatalf("share list leaked password hash or password: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"custom_password":true`) {
		t.Fatalf("share list did not include custom password flag: %s", rec.Body.String())
	}
}

func TestPublicShareRecordUsesForwardedHost(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{Passwordless: true})

	req := httptest.NewRequest(http.MethodGet, "http://localhost:2222/config.js", nil)
	req.Host = "localhost:2222"
	req.Header.Set("X-Forwarded-Host", "gotty-demo.httptunnel.top")

	record, ok := server.publicShareRecordForRequest(req)
	if !ok {
		t.Fatal("expected forwarded host to match share")
	}
	if record.ID != "sh_access" {
		t.Fatalf("record id = %q, want sh_access", record.ID)
	}
}

func TestPublicShareRecordUsesTunnelSubdomain(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{Passwordless: true})

	req := httptest.NewRequest(http.MethodGet, "http://localhost:2222/config.js", nil)
	req.Host = "localhost:2222"
	req.Header.Set("X-Http-Tunnel-Subdomain", "gotty-demo")

	record, ok := server.publicShareRecordForRequest(req)
	if !ok {
		t.Fatal("expected tunnel subdomain to match share")
	}
	if record.ID != "sh_access" {
		t.Fatalf("record id = %q, want sh_access", record.ID)
	}
}

func TestPasswordlessShareTokenMatchesRecordWithoutHost(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{Passwordless: true})
	record, ok := server.shareManager.GetShare("sh_access")
	if !ok {
		t.Fatal("share record missing")
	}
	token := server.shareAccessToken(record)

	got, ok := server.shareRecordForAccessToken(token)
	if !ok {
		t.Fatal("expected token to match passwordless share")
	}
	if got.ID != record.ID {
		t.Fatalf("record id = %q, want %q", got.ID, record.ID)
	}
}

func TestCustomPasswordShareTokenMatchesRecordWithoutHost(t *testing.T) {
	server := newShareAccessTestServer(t, ShareRecord{
		CustomPassword: true,
		PasswordHash:   mustSharePasswordHash(t, "open"),
	})
	record, ok := server.shareManager.GetShare("sh_access")
	if !ok {
		t.Fatal("share record missing")
	}
	token := server.shareAccessToken(record)

	got, ok := server.shareRecordForAccessToken(token)
	if !ok {
		t.Fatal("expected token to match custom password share")
	}
	if got.ID != record.ID {
		t.Fatalf("record id = %q, want %q", got.ID, record.ID)
	}
}

func newShareAccessTestServer(t *testing.T, overrides ShareRecord) *Server {
	t.Helper()
	options := &Options{
		ShareEnabled:      true,
		ShareServerURL:    "https://httptunnel.top",
		ShareRegistryFile: filepath.Join(t.TempDir(), "shares.json"),
		ShareInstanceID:   "owner-access",
		ShareMaxActive:    3,
		Credential:        "root:secret",
		PermitWrite:       true,
		EnableASR:         true,
		RestartMode:       RestartModeExec,
	}
	manager, err := NewShareManager(context.Background(), options)
	if err != nil {
		t.Fatalf("new share manager: %v", err)
	}
	t.Cleanup(manager.Close)
	if err := manager.ConfigureInstance("localhost:2222", "/"); err != nil {
		t.Fatalf("configure instance: %v", err)
	}

	record := ShareRecord{
		ID:             "sh_access",
		Type:           ShareTypeHTTP,
		Target:         "localhost:2222",
		Subdomain:      "gotty-demo",
		PublicURL:      "https://gotty-demo.httptunnel.top",
		OwnerID:        "owner-access",
		Status:         ShareStatusActive,
		CreatedAt:      time.Now().UTC(),
		IsTerminal:     true,
		ReadOnly:       overrides.ReadOnly,
		Passwordless:   overrides.Passwordless,
		CustomPassword: overrides.CustomPassword,
		PasswordHash:   overrides.PasswordHash,
	}
	if err := manager.registry.Upsert(record); err != nil {
		t.Fatalf("upsert share: %v", err)
	}
	return &Server{options: options, shareManager: manager}
}

func mustSharePasswordHash(t *testing.T, password string) string {
	t.Helper()
	access, err := buildShareAccessOptions(shareCreateRequest{
		CustomPassword: true,
		Password:       password,
	})
	if err != nil {
		t.Fatalf("hash share password: %v", err)
	}
	return access.passwordHash
}
