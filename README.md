# ![](https://raw.githubusercontent.com/sorenisanerd/gotty/master/resources/favicon.ico) GoTTY - 将你的终端分享为 Web 应用

[![GitHub release](http://img.shields.io/github/release/sorenisanerd/gotty.svg?style=flat-square)][release]
[![MIT License](http://img.shields.io/badge/license-MIT-blue.svg?style=flat-square)][license]

[release]: https://github.com/sorenisanerd/gotty/releases
[license]: https://github.com/sorenisanerd/gotty/blob/master/LICENSE

GoTTY 是一个简单的命令行工具，可以将你的 CLI 工具转换为 Web 应用程序。

[原始项目](https://github.com/yudai/gotty) 由 [Iwasaki Yudai](https://github.com/yudai) 创建。

![Screenshot](https://raw.githubusercontent.com/sorenisanerd/gotty/master/screenshot.gif)

## 特性

- 🚀 将任意命令行工具转换为 Web 应用
- 🔒 支持基本认证和 TLS/SSL 加密
- 🎨 基于 xterm.js 的现代终端界面
- 📱 响应式设计，支持移动端访问
- 🎯 支持选中文本自动复制到剪贴板
- 📁 支持 zmodem 文件传输协议
- 🔄 支持自动重连
- ⚡ WebGL 渲染加速

## 技术架构

### 系统架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                         客户端 (浏览器)                          │
├─────────────────────────────────────────────────────────────────┤
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐          │
│  │   xterm.js   │  │  Bootstrap   │  │   zmodem.js  │          │
│  │  (终端渲染)   │  │   (UI框架)   │  │  (文件传输)   │          │
│  └──────┬───────┘  └──────────────┘  └──────┬───────┘          │
│         │                                    │                  │
│         └────────────┬───────────────────────┘                  │
│                      │                                          │
│              ┌───────▼────────┐                                 │
│              │   WebSocket    │                                 │
│              │   Connection   │                                 │
│              └───────┬────────┘                                 │
└──────────────────────┼─────────────────────────────────────────┘
                       │
                       │ HTTP/WebSocket
                       │
┌──────────────────────▼─────────────────────────────────────────┐
│                     GoTTY 服务器 (Go)                           │
├─────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────┐        │
│  │              HTTP Server (Gorilla WebSocket)        │        │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐          │        │
│  │  │ 静态文件  │  │  认证层   │  │ TLS/SSL  │          │        │
│  │  └──────────┘  └──────────┘  └──────────┘          │        │
│  └────────────────────┬────────────────────────────────┘        │
│                       │                                         │
│  ┌────────────────────▼────────────────────────┐               │
│  │           WebSocket Handler                │               │
│  │  ┌──────────────┐  ┌─────────────────┐     │               │
│  │  │  输入处理器   │  │   输出处理器     │     │               │
│  │  │ (键盘/鼠标)   │  │  (终端输出)      │     │               │
│  │  └──────┬───────┘  └────────▲────────┘     │               │
│  └─────────┼──────────────────┼────────────────┘               │
│            │                  │                                │
│  ┌─────────▼──────────────────┴────────────┐                   │
│  │          PTY (Pseudo Terminal)         │                   │
│  │      (github.com/creack/pty)           │                   │
│  └─────────────────┬──────────────────────┘                   │
└────────────────────┼───────────────────────────────────────────┘
                     │
                     │ 进程通信
                     │
┌────────────────────▼───────────────────────────────────────────┐
│                  执行的命令/Shell                               │
│              (bash, top, vim, 等等)                            │
└─────────────────────────────────────────────────────────────────┘
```

### 技术栈

#### 后端 (Go)
- **Web 框架**: Go 标准库 `net/http`
- **WebSocket**: `github.com/gorilla/websocket` - 处理 WebSocket 连接
- **PTY**: `github.com/creack/pty` - 创建伪终端
- **CLI**: `github.com/urfave/cli/v2` - 命令行参数解析
- **压缩**: `github.com/NYTimes/gziphandler` - HTTP 响应压缩

#### 前端 (TypeScript + Preact)
- **终端模拟器**: `xterm.js` v5.3.0 - 全功能的终端模拟器
  - `xterm-addon-fit` - 终端尺寸自适应
  - `xterm-addon-web-links` - URL 链接支持
  - `xterm-addon-webgl` - WebGL 渲染加速
- **UI 框架**: `bootstrap` v5.3.2 - 界面组件
- **前端框架**: `preact` v10.19.4 - 轻量级 React 替代方案
- **文件传输**: `zmodem.js` - zmodem 协议实现
- **构建工具**:
  - `webpack` v5 - 模块打包
  - `typescript` v4.9.5 - 类型检查
  - `sass` - CSS 预处理

### 工作流程

1. **启动阶段**:
   - GoTTY 启动 HTTP 服务器
   - 加载静态资源 (HTML, JS, CSS)
   - 配置认证和 TLS (如果启用)

2. **连接阶段**:
   - 客户端访问 GoTTY URL
   - 浏览器加载前端资源
   - 建立 WebSocket 连接
   - GoTTY 创建新的 PTY 并执行指定命令

3. **运行阶段**:
   - 客户端输入 → WebSocket → GoTTY → PTY → 命令进程
   - 命令输出 → PTY → GoTTY → WebSocket → xterm.js 渲染

4. **文件传输**:
   - 检测 zmodem 协议握手信号
   - 暂停正常终端输出
   - 通过 zmodem.js 处理文件上传/下载
   - 完成后恢复正常终端模式

## 快速开始

### 从 Release 页面安装

从 [Releases](https://github.com/sorenisanerd/gotty/releases) 页面下载最新的稳定版本。

### Homebrew 安装

```sh
brew install sorenisanerd/gotty/gotty
```

### 基本使用

```sh
# 启动一个共享的 bash 终端
gotty bash

# 共享 top 命令
gotty top

# 在指定端口运行
gotty -p 9000 bash
```

打开浏览器访问 `http://localhost:8080` 即可看到终端界面。

## 编译部署

### 环境要求

- **Go**: 1.16 或更高版本
- **Node.js**: 14.0 或更高版本
- **npm**: 6.0 或更高版本
- **Make**: GNU Make

### 本地编译

#### 1. 克隆仓库

```sh
git clone https://github.com/sorenisanerd/gotty.git
cd gotty
```

#### 2. 安装前端依赖

```sh
cd js
npm install
cd ..
```

#### 3. 构建项目

```sh
# 构建生产版本
make

# 构建开发版本（包含调试信息）
DEV=1 make
```

构建完成后，会生成 `gotty` 可执行文件。

#### 4. 验证构建

```sh
./gotty --version
```

### 详细构建过程

#### 前端构建

```sh
# 进入前端目录
cd js

# 安装依赖
npm install

# 开发模式构建（未压缩）
npx webpack --mode=development

# 生产模式构建（压缩优化）
npx webpack --mode=production

cd ..
```

前端构建产物位于 `bindata/static/` 目录：
- `js/gotty.js` - 打包后的 JavaScript
- `js/gotty.js.map` - Source Map
- `css/` - 样式文件
- `index.html` - 主页面

#### 后端构建

```sh
# 设置构建标签
export VERSION=$(git describe --tags)

# 构建二进制文件
go build -ldflags "-X main.Version=${VERSION}"

# 或使用 make
make gotty
```

#### 交叉编译

```sh
# Linux AMD64
GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=${VERSION}" -o gotty-linux-amd64

# Linux ARM64
GOOS=linux GOARCH=arm64 go build -ldflags "-X main.Version=${VERSION}" -o gotty-linux-arm64

# macOS AMD64
GOOS=darwin GOARCH=amd64 go build -ldflags "-X main.Version=${VERSION}" -o gotty-darwin-amd64

# macOS ARM64 (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -ldflags "-X main.Version=${VERSION}" -o gotty-darwin-arm64

# Windows AMD64
GOOS=windows GOARCH=amd64 go build -ldflags "-X main.Version=${VERSION}" -o gotty-windows-amd64.exe
```

### Docker 部署

#### 使用 Dockerfile 构建

```sh
# 构建镜像
docker build -t gotty:latest .

# 运行容器
docker run -p 8080:8080 gotty:latest bash
```

#### 创建受限环境

```sh
# 为每个客户端创建独立的 Docker 容器
gotty -w docker run -it --rm busybox
```

### 生产部署建议

#### 1. 使用 Systemd 服务

创建 `/etc/systemd/system/gotty.service`:

```ini
[Unit]
Description=GoTTY Service
After=network.target

[Service]
Type=simple
User=gotty
Group=gotty
WorkingDirectory=/opt/gotty
ExecStart=/opt/gotty/gotty --config /etc/gotty/config bash
Restart=on-failure
RestartSec=5

# 安全加固
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/log/gotty

[Install]
WantedBy=multi-user.target
```

启动服务:

```sh
sudo systemctl enable gotty
sudo systemctl start gotty
sudo systemctl status gotty
```

#### 2. 配置文件

创建 `~/.gotty` 或 `/etc/gotty/config`:

```hcl
// 监听地址和端口
address = "0.0.0.0"
port = "8080"

// 启用 TLS
enable_tls = true
tls_crt_file = "/etc/gotty/certs/server.crt"
tls_key_file = "/etc/gotty/certs/server.key"

// 基本认证
credential = "username:password"

// 随机 URL (增加安全性)
random_url = true
random_url_length = 16

// 客户端设置
permit_write = true
enable_reconnect = true
reconnect_time = 10
max_connection = 10

// 终端设置
enable_webgl = true
```

#### 3. Nginx 反向代理

```nginx
server {
    listen 80;
    server_name gotty.example.com;

    # 重定向到 HTTPS
    return 301 https://$server_name$request_uri;
}

server {
    listen 443 ssl http2;
    server_name gotty.example.com;

    ssl_certificate /etc/nginx/certs/gotty.crt;
    ssl_certificate_key /etc/nginx/certs/gotty.key;

    location / {
        proxy_pass http://localhost:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # WebSocket 超时设置
        proxy_read_timeout 86400;
    }
}
```

#### 4. 生成 TLS 证书

```sh
# 自签名证书（测试用）
openssl req -x509 -nodes -days 365 -newkey rsa:2048 \
  -keyout ~/.gotty.key -out ~/.gotty.crt

# Let's Encrypt（生产环境推荐）
certbot certonly --standalone -d gotty.example.com
```

## 配置选项

### 命令行参数

```sh
# 网络设置
--address, -a       监听地址 (默认: "0.0.0.0")
--port, -p          端口号 (默认: "8080")
--path, -m          基础路径 (默认: "/")

# 安全选项
--permit-write, -w          允许客户端写入 TTY（小心使用）
--credential, -c            基本认证凭证 (格式: user:pass)
--random-url, -r            生成随机 URL
--random-url-length         随机 URL 长度 (默认: 8)
--tls, -t                   启用 TLS/SSL
--tls-crt                   TLS 证书文件路径
--tls-key                   TLS 密钥文件路径
--tls-ca-crt                客户端证书 CA 文件

# 连接选项
--max-connection            最大连接数 (0=无限制)
--once                      只接受一个客户端，断开后退出
--timeout                   等待客户端超时秒数 (0=禁用)
--reconnect                 启用重连
--reconnect-time            重连时间间隔 (默认: 10)

# 终端选项
--width                     静态终端宽度 (0=动态调整)
--height                    静态终端高度 (0=动态调整)
--enable-webgl              启用 WebGL 渲染 (默认: true)

# 其他选项
--title-format              浏览器标题格式
--permit-arguments          允许 URL 参数传递命令参数
--config                    配置文件路径 (默认: "~/.gotty")
--quiet                     静默模式
```

### 环境变量

所有命令行参数都可以通过环境变量设置，格式为 `GOTTY_<OPTION>`：

```sh
export GOTTY_PORT=9000
export GOTTY_CREDENTIAL=admin:secret
export GOTTY_ENABLE_TLS=true
gotty bash
```

## 安全建议

### 1. 限制输入

默认情况下，GoTTY 不允许客户端发送键盘输入。如果需要交互，建议使用 tmux 或 screen：

```sh
# 使用 tmux 共享会话
gotty tmux new -A -s shared

# 从本地连接到同一会话
tmux attach -t shared
```

### 2. 启用认证

```sh
# 基本认证
gotty -c username:password bash

# 结合 TLS 使用
gotty -t -c username:password bash
```

### 3. 使用 TLS/SSL

```sh
# 使用自签名证书
gotty -t bash

# 指定证书路径
gotty --tls --tls-crt=/path/to/cert.crt --tls-key=/path/to/cert.key bash
```

### 4. 限制访问

```sh
# 使用随机 URL
gotty -r bash

# 限制连接数
gotty --max-connection=1 bash

# 单次连接后退出
gotty --once bash
```

### 5. WebSocket Origin 验证

```sh
# 只允许特定来源的 WebSocket 连接
gotty --ws-origin='https://example.com' bash
```

## 使用场景

### 1. 远程系统监控

```sh
gotty -t -c admin:secret top
```

### 2. 在线演示

```sh
gotty -r tmux new -A -s demo
```

### 3. 教学和培训

```sh
gotty --permit-write -r bash
```

### 4. 服务器管理

```sh
gotty -t -c admin:secret tmux new -A -s admin
```

### 5. 容器化应用调试

```sh
gotty -w docker run -it --rm ubuntu bash
```

## 多客户端共享

### 使用 Tmux

```sh
# 创建新会话
gotty tmux new -A -s gotty top

# 本地连接同一会话
tmux attach -t gotty
```

### 使用 Screen

```sh
# 创建新会话
screen -S mysession

# 在另一个终端启动 gotty
gotty screen -x mysession
```

### Tmux 快捷键配置

在 `~/.tmux.conf` 中添加：

```
# 使用 Ctrl+t 启动 GoTTY 共享当前会话
bind-key C-t new-window "gotty tmux attach -t `tmux display -p '#S'`"
```

## 故障排除

### 1. WebSocket 连接失败

- 检查防火墙设置
- 确认 WebSocket 没有被代理服务器阻止
- 使用浏览器开发者工具查看网络请求

### 2. TLS 证书错误

```sh
# Safari 用户需要先访问 HTTPS 页面接受证书
# 或使用 Let's Encrypt 等受信任的证书
```

### 3. 终端显示异常

- 尝试禁用 WebGL: `gotty --enable-webgl=false bash`
- 清除浏览器缓存
- 更新浏览器到最新版本

### 4. 构建失败

```sh
# 清理构建缓存
make clean
rm -rf js/node_modules
cd js && npm install && cd ..
make
```

## 开发

### 项目结构

```
gotty/
├── main.go              # 程序入口
├── server/             # HTTP/WebSocket 服务器
├── webtty/             # WebTTY 核心逻辑
├── backend/            # 后端接口定义
├── js/                 # 前端源代码
│   ├── src/
│   │   ├── main.ts     # 前端入口
│   │   ├── xterm.tsx   # 终端组件
│   │   ├── zmodem.tsx  # 文件传输
│   │   └── webtty.ts   # WebSocket 通信
│   ├── package.json
│   └── webpack.config.js
├── resources/          # 静态资源
├── bindata/           # 打包后的静态文件
└── Makefile
```

### 开发环境设置

```sh
# 1. 克隆仓库
git clone https://github.com/sorenisanerd/gotty.git
cd gotty

# 2. 安装依赖
cd js && npm install && cd ..

# 3. 开发模式构建
DEV=1 make

# 4. 运行
./gotty bash
```

### 前端开发

```sh
cd js

# 监听文件变化自动构建
npx webpack --watch --mode=development

# 生产构建
npx webpack --mode=production
```

### 代码贡献

欢迎提交 Pull Request！请确保：

1. 代码通过 `go fmt` 格式化
2. 前端代码通过 TypeScript 类型检查
3. 添加必要的测试
4. 更新相关文档

## 许可证

MIT License

## 致谢

本项目基于 [Iwasaki Yudai](https://github.com/yudai) 的[原始 GoTTY 项目](https://github.com/yudai/gotty)。

感谢所有[贡献者](https://github.com/sorenisanerd/gotty/graphs/contributors)的付出！

## 相关项目

### 客户端工具

- [gotty-client](https://github.com/moul/gotty-client) - 从终端连接到 GoTTY 服务器

### 类似项目

- [ttyd](https://tsl0922.github.io/ttyd) - C 语言实现，支持 CJK 和 IME
- [Wetty](https://github.com/krishnasrinivas/wetty) - 基于 Node.js 的 Web 终端
- [Secure Shell (Chrome)](https://chrome.google.com/webstore/detail/secure-shell/pnhechapfaindjhompbnflcldabbghjo) - Chrome SSH 客户端

### 终端共享

- [tmate](http://tmate.io/) - 基于 Tmux 的终端共享
- [termshare](https://termsha.re) - 通过 HTTP 服务器共享终端
