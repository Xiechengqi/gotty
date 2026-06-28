package server

import (
	"crypto/subtle"
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

		if token := server.options.ShareManageToken; token != "" {
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				provided := strings.TrimPrefix(auth, "Bearer ")
				if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		if isLocalShareRequest(r) && !server.isPublicShareHost(r.Host) {
			next.ServeHTTP(w, r)
			return
		}

		writeShareError(w, http.StatusForbidden, "FORBIDDEN", "share management is not available from this request")
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
	writeShareJSON(w, http.StatusOK, record)
}

func (server *Server) handleShareList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeShareError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET required")
		return
	}
	writeShareJSON(w, http.StatusOK, shareListResponse{
		Shares:        server.shareManager.List(),
		DefaultTarget: server.shareManager.DefaultTarget(),
		Enabled:       true,
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
		record, ok := server.shareManager.registry.Get(id)
		if !ok {
			writeShareError(w, http.StatusNotFound, "NOT_FOUND", "share not found")
			return
		}
		writeShareJSON(w, http.StatusOK, record)
	case r.Method == http.MethodDelete && action == "":
		record, err := server.shareManager.StopShare(id, ShareStatusStopped)
		if err != nil {
			writeShareError(w, http.StatusNotFound, "STOP_FAILED", err.Error())
			return
		}
		writeShareJSON(w, http.StatusOK, record)
	case r.Method == http.MethodPost && action == "restart":
		record, err := server.shareManager.RestartShare(id)
		if err != nil {
			writeShareError(w, http.StatusBadRequest, "RESTART_FAILED", err.Error())
			return
		}
		writeShareJSON(w, http.StatusOK, record)
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

func isLocalShareRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (server *Server) isPublicShareHost(host string) bool {
	host = strings.ToLower(host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	domain := publicShareDomainHost(server.options.ShareTunnelDomain)
	if domain == "" {
		return false
	}
	return host == domain || strings.HasSuffix(host, "."+domain)
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
