package server

import (
	"github.com/pkg/errors"
)

type Options struct {
	Address             string `hcl:"address" flagName:"address" flagSName:"a" flagDescribe:"IP address(es) to listen (comma-separated for multiple)" default:"0.0.0.0"`
	Port                string `hcl:"port" flagName:"port" flagSName:"p" flagDescribe:"Port number to liten" default:"8080"`
	Path                string `hcl:"path" flagName:"path" flagSName:"m" flagDescribe:"Base path" default:"/"`
	PermitWrite         bool   `hcl:"permit_write" flagName:"permit-write" flagSName:"w" flagDescribe:"Permit clients to write to the TTY (BE CAREFUL)" default:"false"`
	EnableBasicAuth     bool   `hcl:"enable_basic_auth" default:"false"`
	Credential          string `hcl:"credential" flagName:"credential" flagSName:"c" flagDescribe:"Credential for Basic Authentication (ex: user:pass, default disabled)" default:""`
	EnableRandomUrl     bool   `hcl:"enable_random_url" flagName:"random-url" flagSName:"r" flagDescribe:"Add a random string to the URL" default:"false"`
	RandomUrlLength     int    `hcl:"random_url_length" flagName:"random-url-length" flagDescribe:"Random URL length" default:"8"`
	EnableTLS           bool   `hcl:"enable_tls" flagName:"tls" flagSName:"t" flagDescribe:"Enable TLS/SSL" default:"false"`
	TLSCrtFile          string `hcl:"tls_crt_file" flagName:"tls-crt" flagDescribe:"TLS/SSL certificate file path" default:"~/.gotty.crt"`
	TLSKeyFile          string `hcl:"tls_key_file" flagName:"tls-key" flagDescribe:"TLS/SSL key file path" default:"~/.gotty.key"`
	EnableTLSClientAuth bool   `hcl:"enable_tls_client_auth" default:"false"`
	TLSCACrtFile        string `hcl:"tls_ca_crt_file" flagName:"tls-ca-crt" flagDescribe:"TLS/SSL CA certificate file for client certifications" default:"~/.gotty.ca.crt"`
	IndexFile           string `hcl:"index_file" flagName:"index" flagDescribe:"Custom index.html file" default:""`
	TitleFormat         string `hcl:"title_format" flagName:"title-format" flagSName:"" flagDescribe:"Title format of browser window" default:"{{ .command }}@{{ .hostname }}"`
	EnableReconnect     bool   `hcl:"enable_reconnect" flagName:"reconnect" flagDescribe:"Enable reconnection" default:"false"`
	ReconnectTime       int    `hcl:"reconnect_time" flagName:"reconnect-time" flagDescribe:"Time to reconnect" default:"10"`
	MaxConnection       int    `hcl:"max_connection" flagName:"max-connection" flagDescribe:"Maximum connection to gotty" default:"0"`
	Once                bool   `hcl:"once" flagName:"once" flagDescribe:"Accept only one client and exit on disconnection" default:"false"`
	Timeout             int    `hcl:"timeout" flagName:"timeout" flagDescribe:"Timeout seconds for waiting a client(0 to disable)" default:"0"`
	PermitArguments     bool   `hcl:"permit_arguments" flagName:"permit-arguments" flagDescribe:"Permit clients to send command line arguments in URL (e.g. http://example.com:8080/?arg=AAA&arg=BBB)" default:"false"`
	PassHeaders         bool   `hcl:"pass_headers" flagName:"pass-headers" flagDescribe:"Pass HTTP request headers as environment variables (e.g. Cookie becomes HTTP_COOKIE)" default:"false"`
	Width               int    `hcl:"width" flagName:"width" flagDescribe:"Static width of the screen, 0(default) means dynamically resize" default:"0"`
	Height              int    `hcl:"height" flagName:"height" flagDescribe:"Static height of the screen, 0(default) means dynamically resize" default:"0"`
	ResizePolicy        string `hcl:"resize_policy" flagName:"resize-policy" flagDescribe:"Terminal resize policy: fixed, leader, or median" default:"leader"`
	MinCols             int    `hcl:"min_cols" flagName:"min-cols" flagDescribe:"Minimum terminal columns when resizing dynamically" default:"60"`
	MaxCols             int    `hcl:"max_cols" flagName:"max-cols" flagDescribe:"Maximum terminal columns when resizing dynamically" default:"240"`
	MinRows             int    `hcl:"min_rows" flagName:"min-rows" flagDescribe:"Minimum terminal rows when resizing dynamically" default:"20"`
	MaxRows             int    `hcl:"max_rows" flagName:"max-rows" flagDescribe:"Maximum terminal rows when resizing dynamically" default:"80"`
	ResizeDebounceMs    int    `hcl:"resize_debounce_ms" flagName:"resize-debounce-ms" flagDescribe:"Debounce window in milliseconds for terminal resize updates" default:"120"`
	LeaderSelect        string `hcl:"leader_select" flagName:"leader-select" flagDescribe:"Leader selection mode for leader policy: latest or first" default:"latest"`
	LeaderSwitch        string `hcl:"leader_switch" flagName:"leader-switch" flagDescribe:"Leader change mode for leader policy: never, on_disconnect, or on_idle" default:"on_disconnect"`
	LeaderIdleMs        int    `hcl:"leader_idle_ms" flagName:"leader-idle-ms" flagDescribe:"Idle timeout in milliseconds before leader can be replaced in on_idle mode" default:"10000"`
	ShowTerminalState   bool   `hcl:"show_terminal_state" flagName:"show-terminal-state" flagDescribe:"Show terminal resize state overlay in the browser" default:"false"`
	WSOrigin            string `hcl:"ws_origin" flagName:"ws-origin" flagDescribe:"A regular expression that matches origin URLs to be accepted by WebSocket. No cross origin requests are acceptable by default" default:""`
	WSQueryArgs         string `hcl:"ws_query_args" flagName:"ws-query-args" flagDescribe:"Querystring arguments to append to the websocket instantiation" default:""`
	EnableWebGL         bool   `hcl:"enable_webgl" flagName:"enable-webgl" flagDescribe:"Enable WebGL renderer" default:"true"`
	EnableIdleAlert     bool   `hcl:"enable_idle_alert" flagName:"enable-idle-alert" flagDescribe:"Enable idle alert feature (show speaker icon)" default:"false"`
	IdleAlertTimeout    int    `hcl:"idle_alert_timeout" flagName:"idle-alert-timeout" flagDescribe:"Idle alert timeout in seconds" default:"30"`
	Quiet               bool   `hcl:"quiet" flagName:"quiet" flagDescribe:"Don't log" default:"false"`

	EnableAPI      bool   `hcl:"enable_api" flagName:"enable-api" flagDescribe:"Enable REST API for terminal control" default:"false"`
	ProbeTimeoutMs int    `hcl:"probe_timeout_ms" flagName:"api-probe-timeout" flagDescribe:"Shell probe timeout in milliseconds" default:"500"`
	UserIdleMs     int    `hcl:"user_idle_ms" flagName:"api-user-idle-ms" flagDescribe:"User idle timeout in milliseconds for API lock" default:"2000"`
	ExecTimeoutSec int    `hcl:"exec_timeout_sec" flagName:"api-exec-timeout" flagDescribe:"Default API command execution timeout in seconds" default:"30"`

	EnableASR  bool   `hcl:"enable_asr" flagName:"enable-asr" flagDescribe:"Enable voice input UI and ASR proxy endpoint" default:"false"`
	ASRBackend string `hcl:"asr_backend" flagName:"asr-backend" flagDescribe:"WebSocket address of sherpa-onnx streaming_server (e.g. ws://127.0.0.1:6006)" default:"ws://127.0.0.1:6006"`
	ASRHoldMs  int    `hcl:"asr_hold_ms" flagName:"asr-hold-ms" flagDescribe:"Hold duration (ms) for ASR hotkey to start recording" default:"500"`
	ASRHotkey  string `hcl:"asr_hotkey" flagName:"asr-hotkey" flagDescribe:"KeyboardEvent.code for ASR hold-to-talk hotkey" default:"ShiftRight"`

	TitleVariables map[string]interface{}

	// Notify with RemoteAddr when client disconnects (optional)
	ClientGoneCh chan<- string

	// Rewrite the index template (optional)
	IndexRewrite func(string) string

	// Favicon specifies a custom favicon. Accepts a local file path (e.g.
	// "/path/to/favicon.png"), an HTTP(S) URL, or a base64 data URI.
	// A local file path is read at startup, converted to an inline data URI,
	// and injected into the HTML template. Empty string (default) keeps the
	// built-in favicon.ico / icon.svg.
	Favicon string `hcl:"favicon" flagName:"favicon" flagDescribe:"Custom favicon (file path, URL, or data URI)" default:""`

	// Terminal preferences — font, colors, cursor, theme, and palette.
	// These are sent to the browser on each WebSocket connection.
	// Users set them in the config file inside a `preferences { ... }` block.
	Preferences *Preferences `hcl:"preferences"`

	// PingInterval is the interval (in seconds) for WebSocket server-side ping/pong.
	// The server sends a WebSocket Ping frame every PingInterval seconds.
	// This keeps the connection alive through NAT/firewall idle timeouts even
	// when the browser tab is in the background (where JS timers are throttled).
	// Set to 0 to disable.
	PingInterval int `hcl:"ping_interval" flagName:"ping-interval" flagSName:"" flagDescribe:"WebSocket server ping interval in seconds (0 to disable)" default:"30"`

	ShareEnabled                       bool   `hcl:"share_enabled" flagName:"share-enabled" flagDescribe:"Enable Portr share management" default:"false"`
	ShareServerURL                     string `hcl:"share_server_url" flagName:"share-server-url" flagDescribe:"Portr admin server URL for share creation" default:""`
	ShareSSHURL                        string `hcl:"share_ssh_url" flagName:"share-ssh-url" flagDescribe:"Portr SSH tunnel server address" default:""`
	ShareTunnelDomain                  string `hcl:"share_tunnel_domain" flagName:"share-tunnel-domain" flagDescribe:"Public Portr tunnel domain" default:""`
	ShareSecretKey                     string `hcl:"share_secret_key" flagName:"share-secret-key" flagDescribe:"Portr secret key for share creation" default:""`
	ShareDefaultTTLSeconds             int    `hcl:"share_default_ttl_seconds" flagName:"share-default-ttl" flagDescribe:"Default share TTL in seconds" default:"3600"`
	ShareMaxTTLSeconds                 int    `hcl:"share_max_ttl_seconds" flagName:"share-max-ttl" flagDescribe:"Maximum share TTL in seconds" default:"14400"`
	ShareRegistryFile                  string `hcl:"share_registry_file" flagName:"share-registry-file" flagDescribe:"Path to gotty share history registry" default:"~/.gotty-shares.json"`
	ShareRestoreActive                 bool   `hcl:"share_restore_active" flagName:"share-restore-active" flagDescribe:"Restore unexpired shares after gotty restarts" default:"false"`
	ShareMaxActive                     int    `hcl:"share_max_active" flagName:"share-max-active" flagDescribe:"Maximum active shares" default:"3"`
	ShareManageToken                   string `hcl:"share_manage_token" flagName:"share-manage-token" flagDescribe:"Bearer token for share management API" default:""`
	ShareInsecureSkipHostKeyValidation bool   `hcl:"share_insecure_skip_host_key_validation" flagName:"share-insecure-skip-host-key-validation" flagDescribe:"Skip Portr SSH host key validation for share tunnels" default:"true"`
}

// Preferences holds terminal color/font/cursor settings.
// All fields are optional; nil-pointer fields are omitted when sent to the browser.
// Users set these in their .gotty config under a `preferences` block.
type Preferences struct {
	Theme                 string   `hcl:"theme"`
	FontSize              int      `hcl:"font_size"`
	FontFamily            string   `hcl:"font_family"`
	ForegroundColor       string   `hcl:"foreground_color"`
	BackgroundColor       string   `hcl:"background_color"`
	CursorColor           string   `hcl:"cursor_color"`
	CursorAccent          string   `hcl:"cursor_accent"`
	SelectionColor        string   `hcl:"selection_color"`
	CursorStyle           string   `hcl:"cursor_style"`
	CursorBlink           bool     `hcl:"cursor_blink"`
	ScrollbackLines       int      `hcl:"scrollback_lines"`
	EnableWebGL           bool     `hcl:"enable_webgl"`
	AltIsMeta             bool     `hcl:"alt_is_meta"`
	ColorPaletteOverrides []string `hcl:"color_palette_overrides"`
}

func (options *Options) Validate() error {
	if options.EnableTLSClientAuth && !options.EnableTLS {
		return errors.New("TLS client authentication is enabled, but TLS is not enabled")
	}
	if options.EnableIdleAlert && options.IdleAlertTimeout <= 0 {
		return errors.New("idle alert is enabled, but idle-alert-timeout must be > 0")
	}
	if options.EnableASR && options.ASRHoldMs < 0 {
		return errors.New("enable-asr is enabled, but asr-hold-ms must be >= 0")
	}
	switch options.ResizePolicy {
	case "fixed", "leader", "median":
	default:
		return errors.New("resize-policy must be one of: fixed, leader, median")
	}
	switch options.LeaderSwitch {
	case "never", "on_disconnect", "on_idle":
	default:
		return errors.New("leader-switch must be one of: never, on_disconnect, on_idle")
	}
	switch options.LeaderSelect {
	case "latest", "first":
	default:
		return errors.New("leader-select must be one of: latest, first")
	}
	if options.MinCols <= 0 || options.MaxCols <= 0 || options.MinRows <= 0 || options.MaxRows <= 0 {
		return errors.New("min/max terminal bounds must be > 0")
	}
	if options.MinCols > options.MaxCols || options.MinRows > options.MaxRows {
		return errors.New("min terminal bounds must be <= max bounds")
	}
	if options.ResizeDebounceMs < 0 {
		return errors.New("resize-debounce-ms must be >= 0")
	}
	if options.LeaderIdleMs < 0 {
		return errors.New("leader-idle-ms must be >= 0")
	}
	if options.EnableAPI {
		if options.ProbeTimeoutMs <= 0 {
			return errors.New("api-probe-timeout must be > 0 when API is enabled")
		}
		if options.UserIdleMs <= 0 {
			return errors.New("api-user-idle-ms must be > 0 when API is enabled")
		}
		if options.ExecTimeoutSec <= 0 {
			return errors.New("api-exec-timeout must be > 0 when API is enabled")
		}
	}
	if options.ShareEnabled {
		if options.ShareServerURL == "" {
			return errors.New("share-server-url is required when share is enabled")
		}
		if options.ShareSSHURL == "" {
			return errors.New("share-ssh-url is required when share is enabled")
		}
		if options.ShareTunnelDomain == "" {
			return errors.New("share-tunnel-domain is required when share is enabled")
		}
		if options.ShareSecretKey == "" {
			return errors.New("share-secret-key is required when share is enabled")
		}
		if options.ShareDefaultTTLSeconds <= 0 {
			return errors.New("share-default-ttl must be > 0 when share is enabled")
		}
		if options.ShareMaxTTLSeconds <= 0 {
			return errors.New("share-max-ttl must be > 0 when share is enabled")
		}
		if options.ShareDefaultTTLSeconds > options.ShareMaxTTLSeconds {
			return errors.New("share-default-ttl must be <= share-max-ttl")
		}
		if options.ShareMaxActive <= 0 {
			return errors.New("share-max-active must be > 0 when share is enabled")
		}
	}
	return nil
}
