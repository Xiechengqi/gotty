package server

import "time"

const (
	ShareTypeHTTP = "http"

	ShareStatusCreating = "creating"
	ShareStatusActive   = "active"
	ShareStatusExpired  = "expired"
	ShareStatusStopped  = "stopped"
	ShareStatusFailed   = "failed"
	ShareStatusLost     = "lost"
)

type ShareRecord struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"`
	Target       string     `json:"target"`
	Subdomain    string     `json:"subdomain,omitempty"`
	PublicURL    string     `json:"public_url"`
	ConnectionID string     `json:"connection_id,omitempty"`
	RemotePort   int        `json:"remote_port,omitempty"`
	TTLSeconds   int        `json:"ttl_seconds,omitempty"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	StoppedAt    *time.Time `json:"stopped_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	IsTerminal   bool       `json:"is_terminal,omitempty"`
}

type shareCreateRequest struct {
	Type        string `json:"type"`
	Target      string `json:"target"`
	Subdomain   string `json:"subdomain"`
	ExpireValue int    `json:"expire_value"`
	ExpireUnit  string `json:"expire_unit"`
	TTLSeconds  int    `json:"ttl_seconds"`
}

type shareListResponse struct {
	Shares          []ShareRecord `json:"shares"`
	DefaultTarget   string        `json:"default_target"`
	Enabled         bool          `json:"enabled"`
	Configured      bool          `json:"configured"`
	MissingConfig   []string      `json:"missing_config,omitempty"`
	PublicDomain    string        `json:"public_domain,omitempty"`
	SubdomainPrefix string        `json:"subdomain_prefix,omitempty"`
}
