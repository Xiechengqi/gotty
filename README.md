<h4 align="right"><a href="README_EN.md">English</a> | <strong>简体中文</strong></h4>

<p align="center">
  <img src="resources/icon.svg" alt="GoTTY icon" width="72" height="72">
</p>

<h1 align="center">GoTTY</h1>

<p align="center"><strong>把本地命令或 Shell 安全发布成可访问、可分享、可自动化控制的 Web 终端。</strong></p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26-00add8?style=flat-square&logo=go">
  <img alt="Terminal" src="https://img.shields.io/badge/xterm.js-6.x-111827?style=flat-square">
  <img alt="Protocol" src="https://img.shields.io/badge/HTTP%20%2F%20WebSocket-terminal-2563eb?style=flat-square">
  <img alt="Share" src="https://img.shields.io/badge/share-http--tunnel-16a34a?style=flat-square">
  <img alt="License" src="https://img.shields.io/badge/license-MIT-blue?style=flat-square">
</p>

GoTTY 是一个单二进制 Web 终端服务。它启动一个本地命令，通过 PTY 连接该命令，并把终端画面、输入、文件传输、REST API、分享管理等能力暴露给浏览器。

本仓库基于 [sorenisanerd/gotty](https://github.com/sorenisanerd/gotty) 和原始 [yudai/gotty](https://github.com/yudai/gotty) 演进，当前重点是稳定的远程终端、可恢复连接、浏览器内管理、HTTP tunnel 分享和自动化控制接口。

典型链路：

```text
browser
  -> HTTP / WebSocket
  -> gotty
  -> PTY
  -> local command / shell
```

公网分享链路：

```text
https://gotty-<suffix>.httptunnel.top
  -> http-tunnel server
  -> persistent WebSocket tunnel
  -> http-tunnel-client
  -> http://localhost:<gotty-port>
  -> gotty
```

项目只实现 HTTP / WebSocket 层面的 Web 终端和分享，不实现原始 TCP 穿透、SSH 反向转发、多用户/团队权限系统或容器编排平台。

## 接入 GoTTY Hub

使用 `gotty --permit-write bash` 启动后，点击终端页面右上角“接入 Hub”，在按钮旁展开的面板中填写 Hub 的 HTTP(S) 地址和已注册的用户名；可选填写“Hub 中显示的名字”，留空自动生成。同一用户下不能注册同名实例，名称会去除首尾空白，ASCII 字母不区分大小写。用户名不存在时，页面提示先到 Hub Web 注册并提供链接。Hub 默认开启，可用 `--hub-disabled` 关闭（环境变量 `GOTTY_HUB_DISABLED=true` 或配置 `hub_disabled = true`）；`--permit-write` 仍控制远程终端是否可写，观察者始终只读。

绑定及设备凭据按监听地址、端口和基路径分别保存在权限 `0600` 的 `~/.gotty/hub/<标识>.json`；若 `~/.gotty` 是已有配置文件，则使用 `~/.gotty-hub-bindings/<标识>.json`。重启保持同一身份，可通过 `--hub-state-file` 固定文件路径；同一文件被另一个进程占用时，在启动用户命令前报错。旧 `~/.gotty/hub.json` / `~/.gotty-hub.json` 会保留且不自动复用：先核对其 instance_id 属于哪个实例，只给该实例显式指定旧文件；其他实例独立接入，避免再次顶替。已有 GoTTY 密码时自动复用登录态；无密码且启用 Hub 时，主站访问者可以配置绑定，公开分享页面不能查看或管理 Hub 配置。成功后显示绿色“接入 Hub 地址 成功”提示，输入框变为只读；“编辑”可修改配置和显示名字，“重新接入”复用原实例身份，“断开”立即停止隧道并持久保留配置，重启 GoTTY 后也不会自动恢复已断开的连接。修改 Hub 地址或用户名失败时保留原绑定；跨 Hub 切换后原 Hub 中的记录保持离线，可在原 Hub 解绑。设备请求失败可直接重试，不需要验证码、Hub 密码或额外管理令牌。

GoTTY 默认支持 HTTP 和 HTTPS Hub，分别主动通过 WS 或 WSS 连接，终端直接复用本地 PTY 会话；Hub 连接不代理本地 HTTP 地址。Hub 与 GoTTY 使用相同的 Nord 默认主题、Source Code Pro 字体、14px 默认字号和显示偏好翻译，远程主题目录、字号、光标和回滚行数经专用终端通道传递。Hub 终端右侧提供连接数、显示设置、清屏、原生文件上传、重启和实例设置；重启与分享管理仅实例所有者可用，观察者保持只读。状态点在本地页面和 Hub 间同步：红色为正在查看、黄色为未查看时有新输出、绿色为输出已停止但尚未查看、灰色为无未读输出。可用 `--hub-allow-http=false` 仅允许 HTTPS Hub。

## 特性

- 将任意本地命令发布成 Web 终端，例如 `bash`、`top`、`vim`、`python`、运维脚本或交互式 CLI。
- 默认只读，显式使用 `--permit-write` / `-w` 后才允许浏览器向 PTY 写入输入。
- 多浏览器客户端共享同一个终端会话，右上角显示连接数，并支持最大连接数限制。
- xterm.js 前端，支持 WebGL 渲染、链接识别、Unicode、响应式尺寸和移动端恢复。
- 内置显示设置面板：默认 Nord 主题和 Source Code Pro 字体，支持运行时切换主题、字号和字体并保存到 localStorage。
- 支持选中文本自动复制到剪贴板，复制成功后显示浏览器内通知。
- 支持文件上传按钮和 zmodem 文件传输通道。
- 支持刷新/断线后的自动重连和基于 offset 的终端输出 replay，避免刷新后丢失最近输出。
- 支持动态 resize 策略：固定尺寸、leader 客户端、median 多客户端中位数，并可配置最小/最大行列。
- 支持 Basic Auth、TLS、TLS client auth、随机 URL、WebSocket Origin 校验和自定义 favicon。
- 内置 REST API：模拟输入、执行命令、SSE 流式执行、读取输出行、查询终端状态。
- API 执行期间会用终端状态机阻塞普通用户输入，避免自动化命令和人工输入互相打断。
- 支持可选 ASR 语音输入代理，把浏览器语音输入转发给 sherpa-onnx streaming server。
- 右上角提供 Web restart 按钮，支持 `exec`、`fork`、`exit`、`disabled` 等服务端重启模式。
- 默认启用 Share Manager，使用 `http-tunnel-client` 创建公网 HTTPS 子域名分享。
- Share 支持自定义 target、子域名后缀、分钟/小时/天过期时间、只读模式、免密模式和自定义密码模式。
- Share 记录按 gotty 实例隔离，重启后可恢复未过期 active share，避免多个 gotty 进程互相接管历史。
- 发布构建可内嵌对应架构的 `http-tunnel-client`；运行时优先使用 `/usr/local/bin/http-tunnel-client`，不存在时解压内嵌版本。

## 快速开始

Linux amd64 安装：

```bash
wget https://github.com/Xiechengqi/gotty/releases/download/latest/gotty-linux-amd64 -O /usr/local/bin/gotty && chmod +x /usr/local/bin/gotty
```

Linux arm64 安装：

```bash
wget https://github.com/Xiechengqi/gotty/releases/download/latest/gotty-linux-arm64 -O /usr/local/bin/gotty && chmod +x /usr/local/bin/gotty
```

只读发布一个命令：

```bash
gotty top
```

发布可交互 Shell：

```bash
gotty -w bash
```

指定监听端口：

```bash
gotty -w -p 9000 bash
```

加 Basic Auth：

```bash
gotty -w -p 9000 -c user:pass bash
```

浏览器打开：

```text
http://localhost:8080
```

常用安全组合：

```bash
gotty \
  --permit-write \
  --credential user:pass \
  --random-url \
  --max-connection 3 \
  bash
```

`--credential` 会启用 Basic Auth。生产环境公开访问时建议至少使用认证、HTTPS 或受控内网入口。

## Share Manager

GoTTY 默认启用分享管理：

```bash
gotty -w -p 8080 bash
```

打开页面后点击右上角连接数按钮，进入 Share Manager。默认 target 会填入：

```text
localhost:<gotty实际监听端口>
```

创建 share 后会得到类似地址：

```text
https://gotty-abcxyz.httptunnel.top
```

创建参数：

- `Target`：要暴露的本地 HTTP 服务，默认是当前 gotty，例如 `localhost:8080`。
- `Subdomain suffix`：用户自定义后缀；实际子域名固定使用 `gotty-<suffix>`。留空时自动生成 6 位小写字母。
- `Expiry`：正整数，单位支持 minutes、hours、days；留空表示永不过期。
- `Read only`：打开 share URL 后只能查看终端，不能输入、上传、清历史、重启或继续管理 share。
- `Passwordless`：打开 share URL 不需要 gotty 原始密码。
- `Custom password`：打开 share URL 只需要输入分享密码，不需要用户名。

相关配置：

```bash
gotty \
  --share-enabled \
  --share-server-url https://httptunnel.top \
  --share-max-active 3 \
  --share-runtime-dir ~/.gotty-http-tunnel \
  --share-registry-file ~/.gotty-shares.json \
  -w bash
```

Share 只支持 HTTP / WebSocket 服务，不支持原始 TCP 穿透。http-tunnel server 默认是 `https://httptunnel.top`。

## REST API

API 默认关闭。启用后使用 Bearer token 认证；token 为 `--credential` 的值，未设置 credential 时默认是 `user:pass`。

```bash
gotty --enable-api --permit-write --credential user:pass bash
```

常用接口：

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| `POST` | `/api/v1/input` | 写入文本、单键或 Ctrl 组合键 |
| `POST` | `/api/v1/exec` | 执行命令并返回 JSON 结果 |
| `POST` | `/api/v1/exec/stream` | 通过 SSE 流式返回执行事件 |
| `GET` | `/api/v1/output/lines?n=50` | 读取最近输出行，可用 `strip_ansi=true` 去除 ANSI |
| `GET` | `/api/v1/status` | 查询终端状态、连接数和当前尺寸 |

示例：

```bash
curl -H 'Authorization: Bearer user:pass' \
  http://127.0.0.1:8080/api/v1/status
```

写入文本：

```bash
curl -X POST \
  -H 'Authorization: Bearer user:pass' \
  -H 'Content-Type: application/json' \
  -d '{"type":"text","data":"echo hello\n"}' \
  http://127.0.0.1:8080/api/v1/input
```

执行命令：

```bash
curl -X POST \
  -H 'Authorization: Bearer user:pass' \
  -H 'Content-Type: application/json' \
  -d '{"command":"pwd","timeout":10}' \
  http://127.0.0.1:8080/api/v1/exec
```

## 配置文件

默认配置路径是 `~/.gotty`；如果它是目录（例如启用 Hub 后），则读取 `~/.gotty/config`，文件不存在时使用默认值。也可以用 `--config` 或 `GOTTY_CONFIG` 指定其他配置文件。命令行 flag 对应环境变量采用 `GOTTY_` 前缀，例如 `--permit-write` 对应 `GOTTY_PERMIT_WRITE`。

示例：

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

## 关键配置

| 领域 | 配置 |
| --- | --- |
| 监听入口 | `address`、`port`、`path`、`random-url`、`random-url-length` |
| 写入和连接 | `permit-write`、`max-connection`、`once`、`timeout`、`permit-arguments`、`pass-headers` |
| 安全 | `credential`、`tls`、`tls-crt`、`tls-key`、`tls-ca-crt`、`ws-origin` |
| 重连和回放 | `reconnect`、`reconnect-time`、`history-replay-bytes`、`ping-interval` |
| 终端尺寸 | `width`、`height`、`resize-policy`、`min-cols`、`max-cols`、`min-rows`、`max-rows`、`leader-select`、`leader-switch` |
| 显示偏好 | `preferences.theme`、`font_size`、`font_family`、`cursor_style`、`scrollback_lines`、`alt_is_meta` |
| API | `enable-api`、`api-probe-timeout`、`api-user-idle-ms`、`api-exec-timeout` |
| ASR | `enable-asr`、`asr-backend`、`asr-hold-ms`、`asr-hotkey` |
| Web 重启 | `restart-mode` |
| 分享 | `share-enabled`、`share-server-url`、`share-client-path`、`share-runtime-dir`、`share-registry-file`、`share-instance-id`、`share-restore-active`、`share-max-active` |
| 命令进程 | `close-signal`、`close-timeout` |

## 构建

开发构建需要 Go、Node.js、npm 和 GNU Make。前端构建产物会写入 `bindata/static/`，Go binary 会嵌入这些资源。

```bash
cd js
npm install
cd ..
make
```

开发模式：

```bash
DEV=1 make
```

发布工作流会为 Linux amd64 / arm64 下载对应的 `http-tunnel-client`，放入 `server/http_tunnel_client_bin/http-tunnel-client`，再嵌入 gotty binary。

## 本地验证

开发或发布前建议执行：

```bash
go test ./...
go build ./...
cd js && npx tsc --noEmit
cd js && npx webpack --mode=production
git diff --check
```

基础 smoke：

```bash
gotty -w -p 8080 bash
curl http://127.0.0.1:8080/config.js
curl -H 'Authorization: Bearer user:pass' http://127.0.0.1:8080/api/v1/status
```

## 文件和运行态

| 路径 | 用途 |
| --- | --- |
| `~/.gotty` / `~/.gotty/config` | 默认 HCL 配置文件；前者为目录时使用后者 |
| `~/.gotty/hub/<标识>.json` | 按监听配置隔离的 Hub 身份；`~/.gotty` 为文件时使用 `~/.gotty-hub-bindings/<标识>.json`；可用 `--hub-state-file` 固定路径 |
| `~/.gotty-http-tunnel` | Share Manager 的 http-tunnel-client 运行态目录 |
| `~/.gotty-shares.json` | share 历史、状态、owner 和访问模式记录 |
| `bindata/static/` | 嵌入 gotty binary 的前端静态资源 |
| `server/http_tunnel_client_bin/` | 发布构建时内嵌 http-tunnel-client 的位置 |

## 安全说明

- `--permit-write` 会允许浏览器控制你的 PTY，请只在可信网络或认证保护下开启。
- `--credential` 使用 Basic Auth，也会作为 REST API Bearer token；公开 API 时请设置强密码。
- Share 的 `Passwordless` 会绕过 gotty 原始密码；拿到非只读 share URL 的访问者可以按当前策略使用 Share Manager。
- Share 的自定义密码只保护该 share URL；通过密码后同样不是独立的多用户权限边界。
- 只读 share 会禁用输入、上传、清历史、重启、ASR 和 share 管理。
- 使用 `--pass-headers` 会把 HTTP headers 传给被启动命令的环境变量，请避免把敏感 header 泄露给不可信命令。
- 公开部署建议放在 HTTPS 之后，并设置 `--ws-origin` 限制 WebSocket 来源。

## 项目文件

- [NEWS.md](NEWS.md)：主要变更记录。
- [API_PLAN.md](API_PLAN.md)：REST API 设计背景。
- [LICENSE](LICENSE)：MIT License。

## License

MIT

## 移动端按键与关闭确认

触摸设备打开软键盘时，终端会显示 Esc、Tab、Ctrl、Alt、Shift、方向键、Home/End 和 PgUp/PgDn。修饰键作用于下一次按键；多字符粘贴和输入法提交保留原文。按键栏需要有效写权限，只读分享不会显示。

```hcl
mobile_keys = true
confirm_close = true
```

两个选项默认开启，可用 `--mobile-keys=false` 和 `--confirm-close=false` 关闭。关闭确认仅在 WebSocket 已连接时启用；浏览器通常要求用户先与页面交互，提示文字由浏览器决定。服务重启完成后的主动刷新不会触发确认。

修改字号、字体或恢复保存的显示设置后，终端会重新计算行列；未安装的字体会标记并禁用选择。

## 反向代理

代理到 GoTTY 的 WebSocket 路径需要支持 HTTP/1.1 Upgrade，并转发 `Upgrade` 和 `Connection` 请求头。浏览器开发者工具中 `ws` 请求应返回 `101 Switching Protocols`；失败时结合状态码和服务端日志排查鉴权、Origin、路径及连接上限，超过连接上限返回 `503`。

默认 Origin 校验要求浏览器 Origin 与 GoTTY 收到的 Host 匹配。保留原始 Host（nginx 可配置 `proxy_set_header Host $http_host;`），或使用精确的 `--ws-origin` 正则配置允许来源。子路径部署时保留路径前缀，并与 `--path` 一致。

## 开发验证与发布

使用 Go 1.26 和 Node.js 24：

```bash
cd js && npm ci && cd ..
make typecheck test-js assets
go vet ./...
go test -race ./...
# 首次运行浏览器测试需安装 Chromium 和系统 lrzsz
cd js && npx playwright install --with-deps chromium && cd ..
make test-e2e
```

前端构建使用 esbuild 转译，类型检查由 TypeScript 7 独立执行，两者都需要通过。提交前端源码或依赖变更时，同时提交 `make assets` 生成的 JS、sourcemap 和许可清单。CI 检查整个 `bindata/static` 目录及 xterm 许可条目。

`./scripts/build-linux.sh` 构建 Linux amd64/arm64，并为每个目标嵌入对应的 `http-tunnel-client`；也可指定单个架构。脚本退出时恢复本地原有嵌入文件。`make cross_compile` 使用相同流程；其余平台可自行使用 Go 构建，但没有配套的内嵌隧道客户端发布产物。

Docker 使用 Node.js 24 在构建平台生成前端，Go 按目标架构编译，运行镜像固定为 Alpine 3.24。官方工作流仅发布 Linux amd64/arm64。隧道客户端下载仍跟随其 `latest` 发布，因此完整发布复现还需要固定该外部产物。自动发布及手动发布均先运行统一验证流程。
