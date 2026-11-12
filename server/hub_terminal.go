package server

import (
	"encoding/json"
	"errors"
)

// Only display data crosses into the Hub UI. Never serialize Options, which
// also contains the local credential and the ASR/share backend configuration.
func (h *hubManager) terminalPreferences(client *Client) map[string]any {
	prefs := h.server.buildPreferences()
	prefs["client-id"] = client.id
	if h.server.sessionManager.pasteSupported() {
		prefs["paste-protocol"] = 1
	}
	if h.server.options.EnableReconnect {
		prefs["reconnect"] = h.server.options.ReconnectTime
	}
	opts := h.server.options
	prefs["hub-config"] = map[string]any{
		"themes": builtinThemes, "mobile_keys": opts.MobileKeys,
		"show_terminal_state": opts.ShowTerminalState,
		"asr_hold_ms":         opts.ASRHoldMs, "asr_hotkey": opts.ASRHotkey,
		"restart_mode":  effectiveRestartMode(opts.RestartMode),
		"share_enabled": opts.ShareEnabled && h.server.shareManager != nil,
		"upload_ack":    true,
	}
	return prefs
}

type hubControlRequest struct {
	ID      string          `json:"id"`
	Action  string          `json:"action"`
	Payload json.RawMessage `json:"payload"`
}

type hubControlResult struct {
	ID    string `json:"id"`
	Value any    `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

// The caller's role is taken from the authenticated stream's open frame, never
// from a browser request. These are named operations, not an HTTP proxy.
func (h *hubManager) terminalControl(role string, req hubControlRequest) hubControlResult {
	result := hubControlResult{ID: req.ID}
	if role != "owner" {
		result.Error = "Instance owner permission required"
		return result
	}
	var err error
	if req.Action == "restart" {
		if h.cancelRun == nil {
			err = errors.New("server restart is unavailable")
		} else {
			var mode string
			mode, err = h.server.requestSelfRestart(h.cancelRun)
			result.Value = map[string]string{"mode": mode}
		}
	} else if !h.server.options.ShareEnabled || h.server.shareManager == nil {
		err = errors.New("share management is disabled")
	} else {
		manager := h.server.shareManager
		switch req.Action {
		case "shares":
			missing := manager.missingConfig()
			result.Value = shareListResponse{
				Shares: sanitizeShareRecords(manager.List()), DefaultTarget: manager.DefaultTarget(),
				Enabled: true, Configured: len(missing) == 0, MissingConfig: missing,
				PublicDomain: h.server.sharePublicDomainHost(), SubdomainPrefix: shareSubdomainPrefix,
			}
		case "share_create":
			var create shareCreateRequest
			if err = json.Unmarshal(req.Payload, &create); err == nil {
				var record ShareRecord
				record, err = manager.CreateShare(create)
				result.Value = sanitizeShareRecord(record)
			}
		case "share_stop", "share_restart", "share_delete":
			var item struct {
				ID string `json:"id"`
			}
			if err = json.Unmarshal(req.Payload, &item); err == nil {
				var record ShareRecord
				switch req.Action {
				case "share_stop":
					record, err = manager.StopShare(item.ID, ShareStatusStopped)
				case "share_restart":
					record, err = manager.RestartShare(item.ID)
				case "share_delete":
					err = manager.DeleteRecord(item.ID)
				}
				result.Value = sanitizeShareRecord(record)
			}
		default:
			err = errors.New("unsupported terminal control")
		}
	}
	if err != nil {
		result.Value = nil
		result.Error = err.Error()
	}
	return result
}
