package server

import (
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const maxShareAPIBodySize = 16 * 1024

func (server *Server) registerShareHandlers(mux *http.ServeMux, pathPrefix string) {
	mux.Handle(pathPrefix+"-/share", server.wrapShareAuth(http.HandlerFunc(server.handleShareCreate)))
	mux.Handle(pathPrefix+"-/shares", server.wrapShareAuth(http.HandlerFunc(server.handleShareList)))
	mux.Handle(pathPrefix+"-/shares/", server.wrapShareAuth(http.HandlerFunc(server.handleShareItem)))
}

func (server *Server) wrapShareAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !server.options.ShareEnabled || server.shareManager == nil {
			http.NotFound(w, r)
			return
		}
		if record, ok := server.publicShareRecordForRequest(r); ok {
			if record.ReadOnly {
				writeShareError(w, http.StatusForbidden, "READ_ONLY", "share management is disabled for read-only shares")
				return
			}
			if record.CustomPassword && !server.isSharePasswordAuthenticated(r, record) {
				writeShareError(w, http.StatusUnauthorized, "PASSWORD_REQUIRED", "share password is required")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

func (server *Server) handleShareCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeShareError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required")
		return
	}

	var req shareCreateRequest
	if err := decodeShareRequestBody(w, r, &req); err != nil {
		writeShareError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	record, err := server.shareManager.CreateShare(req)
	if err != nil {
		writeShareError(w, http.StatusBadRequest, "CREATE_FAILED", err.Error())
		return
	}
	writeShareJSON(w, http.StatusOK, sanitizeShareRecord(record))
}

func (server *Server) handleShareList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeShareError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required")
		return
	}
	missing := server.shareManager.missingConfig()
	writeShareJSON(w, http.StatusOK, shareListResponse{
		Shares:          sanitizeShareRecords(server.shareManager.List()),
		DefaultTarget:   server.shareManager.DefaultTarget(),
		Enabled:         true,
		Configured:      len(missing) == 0,
		MissingConfig:   missing,
		PublicDomain:    server.sharePublicDomainHost(),
		SubdomainPrefix: shareSubdomainPrefix,
	})
}

func (server *Server) handleShareItem(w http.ResponseWriter, r *http.Request) {
	prefix := "/-/shares/"
	idx := strings.Index(r.URL.Path, prefix)
	if idx == -1 {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path[idx:], prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case r.Method == http.MethodGet && action == "":
		record, ok := server.shareManager.GetShare(id)
		if !ok {
			writeShareError(w, http.StatusNotFound, "NOT_FOUND", "share not found")
			return
		}
		writeShareJSON(w, http.StatusOK, sanitizeShareRecord(record))
	case r.Method == http.MethodDelete && action == "":
		record, err := server.shareManager.StopShare(id, ShareStatusStopped)
		if err != nil {
			writeShareError(w, http.StatusNotFound, "STOP_FAILED", err.Error())
			return
		}
		writeShareJSON(w, http.StatusOK, sanitizeShareRecord(record))
	case r.Method == http.MethodPost && action == "restart":
		record, err := server.shareManager.RestartShare(id)
		if err != nil {
			writeShareError(w, http.StatusBadRequest, "RESTART_FAILED", err.Error())
			return
		}
		writeShareJSON(w, http.StatusOK, sanitizeShareRecord(record))
	case r.Method == http.MethodDelete && action == "record":
		if err := server.shareManager.DeleteRecord(id); err != nil {
			writeShareError(w, http.StatusBadRequest, "DELETE_FAILED", err.Error())
			return
		}
		writeShareJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeShareError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "unsupported share action")
	}
}

func decodeShareRequestBody(w http.ResponseWriter, r *http.Request, out interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxShareAPIBodySize)
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(out)
}

func writeShareJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeShareError(w http.ResponseWriter, status int, code, message string) {
	writeShareJSON(w, status, map[string]string{
		"code":    code,
		"message": message,
	})
}

func (server *Server) isPublicShareHost(host string) bool {
	if server.options == nil {
		return false
	}
	host = normalizedHost(host)
	domain := server.sharePublicDomainHost()
	if domain == "" {
		return false
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
}

func (server *Server) publicShareRecordForRequest(r *http.Request) (ShareRecord, bool) {
	if r == nil || server.options == nil || !server.options.ShareEnabled || server.shareManager == nil {
		return ShareRecord{}, false
	}
	domain := server.sharePublicDomainHost()
	for _, host := range server.publicShareCandidateHosts(r) {
		if record, ok := server.shareManager.GetShareByPublicHost(host, domain); ok {
			return record, true
		}
	}
	return ShareRecord{}, false
}

func (server *Server) isPublicShareRequest(r *http.Request) bool {
	if r == nil || server.options == nil {
		return false
	}
	for _, host := range server.publicShareCandidateHosts(r) {
		if server.isPublicShareHost(host) {
			return true
		}
	}
	return false
}

func (server *Server) publicShareCandidateHosts(r *http.Request) []string {
	if r == nil {
		return nil
	}
	var hosts []string
	add := func(value string) {
		host := normalizedHost(value)
		if host == "" {
			return
		}
		for _, existing := range hosts {
			if existing == host {
				return
			}
		}
		hosts = append(hosts, host)
	}
	add(r.Host)
	add(r.Header.Get("X-Forwarded-Host"))
	add(r.Header.Get("X-Original-Host"))
	if subdomain := strings.TrimSpace(r.Header.Get("X-Http-Tunnel-Subdomain")); subdomain != "" {
		if domain := server.sharePublicDomainHost(); domain != "" {
			add(subdomain + "." + domain)
		}
	}
	return hosts
}

func (server *Server) isReadOnlyShareRequest(r *http.Request) bool {
	record, ok := server.publicShareRecordForRequest(r)
	return ok && record.ReadOnly
}

func (server *Server) isPasswordlessShareRequest(r *http.Request) bool {
	record, ok := server.publicShareRecordForRequest(r)
	return ok && record.Passwordless
}

func (server *Server) sharePublicDomainHost() string {
	if domain := publicShareDomainHost(server.options.ShareTunnelDomain); domain != "" {
		return domain
	}
	return publicShareDomainHost(server.options.ShareServerURL)
}

func publicShareDomainHost(domain string) string {
	domain = strings.TrimSpace(strings.ToLower(domain))
	domain = strings.TrimPrefix(domain, ".")
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		if parsed, err := url.Parse(domain); err == nil && parsed.Host != "" {
			domain = parsed.Host
		}
	}
	if h, _, err := net.SplitHostPort(domain); err == nil {
		domain = h
	}
	return strings.TrimPrefix(domain, ".")
}
