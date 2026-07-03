package server

import (
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

const sharePasswordCookieName = "gotty_share_auth"

var sharePasswordPageTemplate = template.Must(template.New("share-password").Parse(`<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Share password</title>
  <style>
    html, body {
      height: 100%;
      margin: 0;
      background: #111;
      color: #eee;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
    }
    body {
      display: grid;
      place-items: center;
    }
    form {
      width: min(320px, calc(100vw - 40px));
      display: grid;
      gap: 10px;
    }
    h1 {
      margin: 0 0 4px;
      font-size: 18px;
      font-weight: 600;
    }
    input, button {
      box-sizing: border-box;
      width: 100%;
      height: 36px;
      border-radius: 5px;
      font-size: 14px;
    }
    input {
      border: 1px solid rgba(255,255,255,0.18);
      background: rgba(255,255,255,0.08);
      color: #fff;
      padding: 0 10px;
      outline: none;
    }
    input:focus {
      border-color: rgba(255,255,255,0.42);
    }
    button {
      border: 1px solid rgba(255,255,255,0.18);
      background: rgba(255,255,255,0.14);
      color: #fff;
      cursor: pointer;
    }
    button:hover {
      background: rgba(255,255,255,0.2);
    }
    .error {
      min-height: 18px;
      color: #fca5a5;
      font-size: 13px;
    }
  </style>
</head>
<body>
  <form method="post" action="{{ .Action }}">
    <h1>Share password</h1>
    <input type="password" name="password" autocomplete="current-password" placeholder="Password" autofocus>
    <input type="hidden" name="next" value="{{ .Next }}">
    <button type="submit">Continue</button>
    <div class="error">{{ .Error }}</div>
  </form>
</body>
</html>`))

type sharePasswordPageData struct {
	Action string
	Next   string
	Error  string
}

func buildShareAccessOptions(req shareCreateRequest) (shareAccessOptions, error) {
	if req.Passwordless && req.CustomPassword {
		return shareAccessOptions{}, fmt.Errorf("passwordless and custom password cannot both be enabled")
	}

	access := shareAccessOptions{
		readOnly:       req.ReadOnly,
		passwordless:   req.Passwordless,
		customPassword: req.CustomPassword,
	}
	if !req.CustomPassword {
		return access, nil
	}
	if strings.TrimSpace(req.Password) == "" {
		return shareAccessOptions{}, fmt.Errorf("custom password is required")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return shareAccessOptions{}, err
	}
	access.passwordHash = string(hash)
	return access, nil
}

func accessOptionsFromRecord(record ShareRecord) shareAccessOptions {
	return shareAccessOptions{
		readOnly:       record.ReadOnly,
		passwordless:   record.Passwordless,
		customPassword: record.CustomPassword,
		passwordHash:   record.PasswordHash,
	}
}

func verifySharePassword(record ShareRecord, password string) bool {
	if !record.CustomPassword || record.PasswordHash == "" || strings.TrimSpace(password) == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(record.PasswordHash), []byte(password)) == nil
}

func sanitizeShareRecord(record ShareRecord) ShareRecord {
	record.PasswordHash = ""
	return record
}

func sanitizeShareRecords(records []ShareRecord) []ShareRecord {
	sanitized := make([]ShareRecord, len(records))
	for i, record := range records {
		sanitized[i] = sanitizeShareRecord(record)
	}
	return sanitized
}

func (server *Server) isCustomPasswordShareRequest(r *http.Request) bool {
	record, ok := server.publicShareRecordForRequest(r)
	return ok && record.CustomPassword
}

func (server *Server) isSharePasswordAuthenticated(r *http.Request, record ShareRecord) bool {
	if !record.CustomPassword {
		return false
	}
	cookie, err := r.Cookie(sharePasswordCookieName)
	if err != nil {
		return false
	}
	return constantTimeStringEqual(cookie.Value, server.shareAccessToken(record))
}

func (server *Server) wrapSharePasswordAccess(next http.Handler, pathPrefix string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL != nil && r.URL.Path == shareAuthPath(pathPrefix) {
			next.ServeHTTP(w, r)
			return
		}
		record, ok := server.publicShareRecordForRequest(r)
		if !ok || !record.CustomPassword {
			next.ServeHTTP(w, r)
			return
		}
		if server.isSharePasswordAuthenticated(r, record) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			server.renderSharePasswordPage(w, r, pathPrefix, r.URL.RequestURI(), "Password required", http.StatusUnauthorized)
			return
		}
		server.renderSharePasswordPage(w, r, pathPrefix, r.URL.RequestURI(), "", http.StatusOK)
	})
}

func (server *Server) handleSharePasswordAuth(w http.ResponseWriter, r *http.Request, pathPrefix string) {
	if r.Method != http.MethodPost {
		server.renderSharePasswordPage(w, r, pathPrefix, r.URL.RequestURI(), "", http.StatusOK)
		return
	}
	if server.options == nil || !server.options.ShareEnabled || server.shareManager == nil {
		http.NotFound(w, r)
		return
	}
	record, ok := server.publicShareRecordForRequest(r)
	if !ok || !record.CustomPassword {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		server.renderSharePasswordPage(w, r, pathPrefix, pathPrefix, "Invalid request", http.StatusBadRequest)
		return
	}
	next := sanitizeShareNext(r.PostFormValue("next"), pathPrefix)
	if !verifySharePassword(record, r.PostFormValue("password")) {
		server.renderSharePasswordPage(w, r, pathPrefix, next, "Incorrect password", http.StatusUnauthorized)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sharePasswordCookieName,
		Value:    server.shareAccessToken(record),
		Path:     shareCookiePath(pathPrefix),
		MaxAge:   30 * 24 * 60 * 60,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (server *Server) renderSharePasswordPage(w http.ResponseWriter, r *http.Request, pathPrefix, next, message string, status int) {
	if next == "" {
		next = r.URL.RequestURI()
	}
	next = sanitizeShareNext(next, pathPrefix)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = sharePasswordPageTemplate.Execute(w, sharePasswordPageData{
		Action: shareAuthPath(pathPrefix),
		Next:   next,
		Error:  message,
	})
}

func shareAuthPath(pathPrefix string) string {
	pathPrefix = shareCookiePath(pathPrefix)
	if !strings.HasSuffix(pathPrefix, "/") {
		pathPrefix += "/"
	}
	return pathPrefix + "-/share-auth"
}

func shareCookiePath(pathPrefix string) string {
	pathPrefix = strings.TrimSpace(pathPrefix)
	if pathPrefix == "" {
		return "/"
	}
	if !strings.HasPrefix(pathPrefix, "/") {
		pathPrefix = "/" + pathPrefix
	}
	return pathPrefix
}

func sanitizeShareNext(value, pathPrefix string) string {
	value = strings.TrimSpace(value)
	fallback := shareCookiePath(pathPrefix)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return fallback
	}
	if fallback != "/" && !strings.HasPrefix(value, fallback) {
		return fallback
	}
	return value
}
