package server

import (
	"github.com/pkg/errors"
)

type Options struct {
	Address             string `hcl:"address" flagName:"address" flagSName:"a" flagDescribe:"IP address to listen" default:"0.0.0.0"`
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
	WSOrigin            string `hcl:"ws_origin" flagName:"ws-origin" flagDescribe:"A regular expression that matches origin URLs to be accepted by WebSocket. No cross origin requests are acceptable by default" default:""`
	WSQueryArgs         string `hcl:"ws_query_args" flagName:"ws-query-args" flagDescribe:"Querystring arguments to append to the websocket instantiation" default:""`
	EnableWebGL         bool   `hcl:"enable_webgl" flagName:"enable-webgl" flagDescribe:"Enable WebGL renderer" default:"true"`
	EnableIdleAlert     bool   `hcl:"enable_idle_alert" flagName:"enable-idle-alert" flagDescribe:"Enable idle alert feature (show speaker icon)" default:"false"`
	IdleAlertTimeout    int    `hcl:"idle_alert_timeout" flagName:"idle-alert-timeout" flagDescribe:"Idle alert timeout in seconds" default:"30"`
	Quiet               bool   `hcl:"quiet" flagName:"quiet" flagDescribe:"Don't log" default:"false"`

	EnableASR  bool   `hcl:"enable_asr" flagName:"enable-asr" flagDescribe:"Enable voice input UI and ASR proxy endpoint" default:"false"`
	ASRBackend string `hcl:"asr_backend" flagName:"asr-backend" flagDescribe:"WebSocket address of sherpa-onnx streaming_server (e.g. ws://127.0.0.1:6006)" default:"ws://127.0.0.1:6006"`
	ASRHoldMs  int    `hcl:"asr_hold_ms" flagName:"asr-hold-ms" flagDescribe:"Hold duration (ms) for ASR hotkey to start recording" default:"500"`
	ASRHotkey  string `hcl:"asr_hotkey" flagName:"asr-hotkey" flagDescribe:"KeyboardEvent.code for ASR hold-to-talk hotkey" default:"ShiftRight"`

	TitleVariables map[string]interface{}
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
	return nil
}
