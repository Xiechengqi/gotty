<h4 align="right"><strong>English</strong> | <a href="README.md">简体中文</a></h4>

<p align="center">
  <img src="resources/icon.svg" alt="GoTTY icon" width="72" height="72">
</p>

<h1 align="center">GoTTY</h1>

<p align="center"><strong>Publish a local command or shell as a secure, shareable, automatable web terminal.</strong></p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26-00add8?style=flat-square&logo=go">
  <img alt="Terminal" src="https://img.shields.io/badge/xterm.js-6.x-111827?style=flat-square">
  <img alt="Protocol" src="https://img.shields.io/badge/HTTP%20%2F%20WebSocket-terminal-2563eb?style=flat-square">
  <img alt="Share" src="https://img.shields.io/badge/share-http--tunnel-16a34a?style=flat-square">
  <img alt="License" src="https://img.shields.io/badge/license-MIT-blue?style=flat-square">
</p>

GoTTY is a single-binary web terminal server. It starts a local command, connects to it through a PTY, and exposes terminal output, input, file transfer, REST API automation, and share management to the browser.

This repository evolved from [sorenisanerd/gotty](https://github.com/sorenisanerd/gotty) and the original [yudai/gotty](https://github.com/yudai/gotty). The current focus is reliable remote terminals, resumable browser sessions, browser-side management, HTTP tunnel sharing, and automation APIs.

Typical request path:

```text
browser
  -> HTTP / WebSocket
  -> gotty
  -> PTY
  -> local command / shell
```

Public share path:

```text
https://gotty-<suffix>.httptunnel.top
  -> http-tunnel server
  -> persistent WebSocket tunnel
  -> http-tunnel-client
  -> http://localhost:<gotty-port>
  -> gotty
```

The project implements an HTTP / WebSocket web terminal and sharing flow. It does not implement raw TCP tunneling, SSH reverse forwarding, multi-user/team authorization, or an orchestration platform.

## Features

- Publish any local command as a web terminal: `bash`, `top`, `vim`, `python`, operational scripts, or interactive CLIs.
- Read-only by default. Browser input is allowed only when `--permit-write` / `-w` is enabled.
- Multiple browser clients share the same terminal session, with a connection counter and optional connection limit.
- xterm.js frontend with WebGL rendering, link detection, Unicode support, responsive sizing, and mobile recovery behavior.
- Built-in display panel: Nord theme and Source Code Pro by default, with runtime theme, font size, and font family switching persisted to localStorage.
- Automatically copies selected terminal text to the clipboard and shows a browser notification after success.
- File upload button and zmodem transfer channel.
- Automatic reconnect and offset-based terminal output replay after refresh or disconnect.
- Dynamic resize policies: fixed size, leader client, or median of multiple clients, with configurable bounds.
- Basic Auth, TLS, TLS client auth, random URL, WebSocket Origin checks, and custom favicon support.
- Built-in REST API for simulated input, command execution, SSE streaming execution, output lines, and terminal status.
- API execution uses a terminal state machine to block normal user input while automation is running.
- Optional ASR voice-input proxy for sherpa-onnx streaming server.
- Web restart button with `exec`, `fork`, `exit`, and `disabled` server restart modes.
- Share Manager is enabled by default and creates public HTTPS subdomain shares through `http-tunnel-client`.
- Shares support custom targets, subdomain suffixes, minute/hour/day expiration, read-only mode, passwordless mode, and custom password mode.
- Share records are isolated by gotty instance and can restore unexpired active shares after gotty restarts.
- Release builds can embed the matching `http-tunnel-client`; runtime prefers `/usr/local/bin/http-tunnel-client` and falls back to the embedded payload.

## Quick Start

Publish a read-only command:

```bash
gotty top
```

Publish an interactive shell:

```bash
gotty -w bash
```

Use a custom port:

```bash
gotty -w -p 9000 bash
```

Add Basic Auth:

```bash
gotty -w -p 9000 -c user:pass bash
```

Open in a browser:

```text
http://localhost:8080
```

A common secured setup:

```bash
gotty \
  --permit-write \
  --credential user:pass \
  --random-url \
  --max-connection 3 \
  bash
```

`--credential` enables Basic Auth. For public access, use authentication, HTTPS, or a controlled private entry point.

## Share Manager

Share management is enabled by default:

```bash
gotty -w -p 8080 bash
```

Open the page and click the connection counter in the top-right corner to open Share Manager. The default target is:

```text
localhost:<actual-gotty-listen-port>
```

Creating a share returns a URL like:

```text
https://gotty-abcxyz.httptunnel.top
```

Create options:

- `Target`: local HTTP service to expose. By default this is the current gotty instance, for example `localhost:8080`.
- `Subdomain suffix`: user-defined suffix. The actual subdomain is always `gotty-<suffix>`. Blank values generate a six-letter lowercase suffix.
- `Expiry`: positive integer with minutes, hours, or days. Blank means never expires.
- `Read only`: visitors can only watch the terminal. Input, upload, clear history, restart, and share management are disabled.
- `Passwordless`: visitors can open the share URL without the original gotty password.
- `Custom password`: visitors only enter the share password. No username is required.

Related configuration:

```bash
gotty \
  --share-enabled \
  --share-server-url https://httptunnel.top \
  --share-max-active 3 \
  --share-runtime-dir ~/.gotty-http-tunnel \
  --share-registry-file ~/.gotty-shares.json \
  -w bash
```

Shares support HTTP / WebSocket services only, not raw TCP tunnels. The default http-tunnel server is `https://httptunnel.top`.

## REST API

The API is disabled by default. When enabled, it uses Bearer authentication. The token is `--credential`; if no credential is configured, the default API token is `user:pass`.

```bash
gotty --enable-api --permit-write --credential user:pass bash
```

Common endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/v1/input` | Write text, keys, or Ctrl combinations |
| `POST` | `/api/v1/exec` | Execute a command and return JSON |
| `POST` | `/api/v1/exec/stream` | Stream execution events through SSE |
| `GET` | `/api/v1/output/lines?n=50` | Read recent output lines; use `strip_ansi=true` to remove ANSI |
| `GET` | `/api/v1/status` | Read terminal state, connection count, and size |

Example:

```bash
curl -H 'Authorization: Bearer user:pass' \
  http://127.0.0.1:8080/api/v1/status
```

Write text:

```bash
curl -X POST \
  -H 'Authorization: Bearer user:pass' \
  -H 'Content-Type: application/json' \
  -d '{"type":"text","data":"echo hello\n"}' \
  http://127.0.0.1:8080/api/v1/input
```

Execute a command:

```bash
curl -X POST \
  -H 'Authorization: Bearer user:pass' \
  -H 'Content-Type: application/json' \
  -d '{"command":"pwd","timeout":10}' \
  http://127.0.0.1:8080/api/v1/exec
```

## Configuration

The default config path is `~/.gotty`. Use `--config` or `GOTTY_CONFIG` to override it. Flags also map to environment variables with a `GOTTY_` prefix, for example `--permit-write` maps to `GOTTY_PERMIT_WRITE`.

Example:

```hcl
address = "0.0.0.0"
port = "8080"
permit_write = true
enable_basic_auth = true
credential = "user:pass"
enable_reconnect = true
history_replay_bytes = 524288
resize_policy = "leader"
restart_mode = "exec"

preferences {
  theme = "nord"
  font_family = "'Source Code Pro', monospace"
  font_size = 14
  cursor_style = "block"
  cursor_blink = true
  scrollback_lines = 5000
}
```

## Key Settings

| Area | Settings |
| --- | --- |
| Listen endpoint | `address`, `port`, `path`, `random-url`, `random-url-length` |
| Input and connections | `permit-write`, `max-connection`, `once`, `timeout`, `permit-arguments`, `pass-headers` |
| Security | `credential`, `tls`, `tls-crt`, `tls-key`, `tls-ca-crt`, `ws-origin` |
| Reconnect and replay | `reconnect`, `reconnect-time`, `history-replay-bytes`, `ping-interval` |
| Terminal size | `width`, `height`, `resize-policy`, `min-cols`, `max-cols`, `min-rows`, `max-rows`, `leader-select`, `leader-switch` |
| Display preferences | `preferences.theme`, `font_size`, `font_family`, `cursor_style`, `scrollback_lines`, `alt_is_meta` |
| API | `enable-api`, `api-probe-timeout`, `api-user-idle-ms`, `api-exec-timeout` |
| ASR | `enable-asr`, `asr-backend`, `asr-hold-ms`, `asr-hotkey` |
| Web restart | `restart-mode` |
| Sharing | `share-enabled`, `share-server-url`, `share-client-path`, `share-runtime-dir`, `share-registry-file`, `share-instance-id`, `share-restore-active`, `share-max-active` |
| Command process | `close-signal`, `close-timeout` |

## Build

Development builds require Go, Node.js, npm, and GNU Make. Frontend build outputs are written to `bindata/static/` and embedded into the Go binary.

```bash
cd js
npm install
cd ..
make
```

Development mode:

```bash
DEV=1 make
```

The release workflow downloads the matching Linux amd64 / arm64 `http-tunnel-client` into `server/http_tunnel_client_bin/http-tunnel-client` before embedding it into the gotty binary.

## Local Verification

Recommended checks before development handoff or release:

```bash
go test ./...
go build ./...
cd js && npx tsc --noEmit
cd js && npx webpack --mode=production
git diff --check
```

Basic smoke:

```bash
gotty -w -p 8080 bash
curl http://127.0.0.1:8080/config.js
curl -H 'Authorization: Bearer user:pass' http://127.0.0.1:8080/api/v1/status
```

## Files and Runtime State

| Path | Purpose |
| --- | --- |
| `~/.gotty` | Default HCL configuration file |
| `~/.gotty-http-tunnel` | http-tunnel-client runtime directory used by Share Manager |
| `~/.gotty-shares.json` | Share history, status, owner, and access-mode registry |
| `bindata/static/` | Frontend assets embedded into the gotty binary |
| `server/http_tunnel_client_bin/` | Location for the embedded http-tunnel-client in release builds |

## Security Notes

- `--permit-write` allows browser users to control your PTY. Enable it only on trusted networks or behind authentication.
- `--credential` enables Basic Auth and is also used as the REST API Bearer token. Use a strong secret for public API access.
- Share `Passwordless` bypasses the original gotty password. Visitors with a non-read-only share URL can use Share Manager under the current policy.
- Share custom passwords protect only that share URL; after password access, they are not a separate multi-user authorization boundary.
- Read-only shares disable input, upload, clear history, restart, ASR, and share management.
- `--pass-headers` passes HTTP headers into the command environment. Avoid exposing sensitive headers to untrusted commands.
- Public deployments should be served through HTTPS and should set `--ws-origin` to restrict WebSocket origins.

## Project Files

- [NEWS.md](NEWS.md): major change history.
- [API_PLAN.md](API_PLAN.md): REST API design background.
- [LICENSE](LICENSE): MIT License.

## License

MIT
