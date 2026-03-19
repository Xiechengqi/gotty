# GoTTY API 功能实现计划

## 一、概述

为 GoTTY 增加 REST API，支持：
- 模拟键盘输入（文本、回车、Ctrl 组合键）
- 执行命令并流式获取输出（SSE）
- 获取最近 N 行终端输出
- 查询终端状态

核心约束：
- API 执行命令在 Web 终端**必须可见**
- API 与用户输入**严格互斥**，不排队，冲突立即拒绝
- 执行前通过**静默探测命令**验证 shell 环境
- 探测命令对 Web 用户**不可见**（暂停广播）
- Web 右上角显示 API 执行提示

---

## 二、文件变更清单

### 新增文件（Go 后端）

| 文件 | 职责 |
|------|------|
| `server/terminal_state.go` | 终端状态机（Idle/UserActive/APIExecuting）+ 互斥锁 |
| `server/broadcast_controller.go` | 广播控制器（Pause/Resume），探测期间暂停向 Web 客户端广播 |
| `server/probe.go` | 探测管理器，发送探测命令并验证 shell 环境 |
| `server/api_handler.go` | API 路由注册 + 所有 HTTP handler |
| `server/exec_manager.go` | 命令执行管理器，标记注入、输出捕获、退出码检测 |

### 修改文件（Go 后端）

| 文件 | 变更 |
|------|------|
| `server/server.go` | Server 结构体增加字段；`setupHandlers` 注册 API 路由 |
| `server/options.go` | Options 增加 `EnableAPI`、`APIToken`、`ProbeTimeout` 等字段 |
| `server/session_manager.go` | 集成 BroadcastController；增加 `SendToSlave` 方法；增加 `NotifyClients` 方法 |
| `server/slave_reader.go` | `readSlaveOutput` 集成广播控制器和探测监听 |
| `server/client_handler.go` | `handleClientInput` 中检查终端状态，API 执行期间阻塞用户输入 |
| `server/history_buffer.go` | 增加 `GetLastN(n)` 方法 |
| `webtty/message_types.go` | 增加 `APINotification = '9'` 消息类型 |

### 新增文件（前端）

| 文件 | 职责 |
|------|------|
| `js/src/api-indicator.ts` | API 执行指示器（右上角提示） |

### 修改文件（前端）

| 文件 | 变更 |
|------|------|
| `js/src/xterm.tsx` | 集成 API 指示器；增加 `showAPIIndicator`/`hideAPIIndicator` 方法 |
| `js/src/webtty.tsx` | `onReceive` 增加 `'9'` 消息类型处理；Terminal 接口增加方法 |
| `resources/xterm_customize.css` | 增加 `.api-indicator` 样式 |

---

## 三、详细设计

### 3.1 终端状态机 (`server/terminal_state.go`)

```go
type TerminalState int
const (
    StateIdle         TerminalState = iota // 空闲
    StateUserActive                        // 用户活动中
    StateAPIExecuting                      // API 执行中
)

type TerminalStatus struct {
    mu              sync.Mutex
    state           TerminalState
    lastUserInput   time.Time
    currentExecID   string
    userIdleTimeout time.Duration // 默认 2s
}
```

方法：
- `UpdateUserActivity()` — 用户输入时调用，设为 UserActive
- `TryAcquireAPI(execID) (bool, string)` — API 尝试获取锁
- `ReleaseAPI(execID)` — API 释放锁
- `GetState() TerminalState` — 查询当前状态
- `IsIdle() bool` — 后台定时检查，UserActive 超过 idle timeout 自动转 Idle

状态转换规则：
```
Idle + 用户输入 → UserActive
UserActive + 2s无输入 → Idle
Idle + API请求 → APIExecuting
APIExecuting + 命令完成 → Idle
UserActive + API请求 → 拒绝 409
APIExecuting + API请求 → 拒绝 409
APIExecuting + 用户输入 → 静默丢弃
```

### 3.2 广播控制器 (`server/broadcast_controller.go`)

```go
type BroadcastController struct {
    mu       sync.Mutex
    paused   bool
    internal chan []byte // 暂停期间内部接收 PTY 输出
}
```

方法：
- `Pause()` — 暂停广播，创建 internal channel
- `Resume()` — 恢复广播，关闭 internal channel（幂等）
- `ShouldBroadcast(data []byte) bool` — 返回 false 时数据转发到 internal

集成到 `slave_reader.go`：
```go
func (server *Server) readSlaveOutput(ctx context.Context) {
    buffer := make([]byte, 1024)
    for {
        n, err := server.sessionManager.slave.Read(buffer)
        // ... 编码 ...

        if server.broadcastCtrl.ShouldBroadcast(encoded) {
            server.sessionManager.broadcast <- encoded
        }
    }
}
```

安全保障：
- `defer broadcastCtrl.Resume()` 确保异常时恢复
- 最大暂停时间 2s 强制恢复

### 3.3 探测管理器 (`server/probe.go`)

探测命令：
```bash
echo "GOTTY_PROBE:{probeID}:$(date +%s%N)"
```

流程：
1. 暂停广播（Web 不可见）
2. 发送探测命令到 PTY
3. 从 internal channel 读取 PTY 输出
4. 正则匹配 `GOTTY_PROBE:{probeID}:\d+`
5. 匹配成功 → shell 环境，清理终端行（`\r\033[K`）
6. 超时(500ms) → 可能在交互程序中
7. 恢复广播

```go
type ProbeManager struct {
    slave           Slave
    broadcastCtrl   *BroadcastController
    timeout         time.Duration // 500ms
}

func (pm *ProbeManager) Probe() error {
    // 返回 nil 表示 shell 环境
    // 返回 error 表示非 shell 环境或超时
}
```

### 3.4 命令执行管理器 (`server/exec_manager.go`)

标记注入格式（OSC 序列，Web 不可见）：
```bash
printf '\033]1337;GottyExecStart=%s\007'; {command}; EXIT_CODE=$?; printf '\033]1337;GottyExecEnd=%s:%d\007' "$EXIT_CODE"
```

但 OSC 序列在 xterm.js 中也不可见，GoTTY 需要在 `readSlaveOutput` 中拦截。

实际方案：使用可见的唯一标记（因为命令执行本身就要在 Web 显示）：
```bash
{command}
echo "GOTTY_EXIT:{execID}:$?"
```

```go
type ExecManager struct {
    slave         Slave
    status        *TerminalStatus
    broadcastCtrl *BroadcastController
    probe         *ProbeManager
}

type ExecRequest struct {
    Command string `json:"command"`
    Timeout int    `json:"timeout"` // 秒，默认 30
}

type ExecResult struct {
    ExecID   string `json:"exec_id"`
    ExitCode int    `json:"exit_code"`
    Output   string `json:"output"`
    Duration int64  `json:"duration_ms"`
    Timeout  bool   `json:"timeout"`
}
```

执行流程：
1. 检查互斥锁 → 失败返回 409
2. 获取锁 → StateAPIExecuting
3. 通知 Web 客户端（显示 API 指示器）
4. 静默探测（暂停广播 → 探测 → 恢复广播）
5. 探测失败 → 释放锁，返回 412
6. 构造带标记的命令
7. 写入 PTY（正常广播，Web 可见）
8. 监听输出，检测结束标记 `GOTTY_EXIT:{execID}:\d+`
9. 提取退出码
10. 通知 Web 客户端（隐藏 API 指示器）
11. 释放锁 → StateIdle
12. 返回结果

### 3.5 API Handler (`server/api_handler.go`)

#### 端点列表

| 方法 | 路径 | 功能 |
|------|------|------|
| POST | `/api/v1/input` | 模拟键盘输入 |
| POST | `/api/v1/exec` | 执行命令（非流式） |
| POST | `/api/v1/exec/stream` | 执行命令（SSE 流式） |
| GET  | `/api/v1/output/lines?n=50` | 获取最近 N 行输出 |
| GET  | `/api/v1/status` | 查询终端状态 |

#### POST /api/v1/input

```go
type InputRequest struct {
    Type string `json:"type"` // "text", "key", "ctrl"
    Data string `json:"data"`
}
```

键映射：
- `type=text`: 直接写入 PTY
- `type=key`: enter→`\r`, tab→`\t`, backspace→`\x7f`, escape→`\x1b`
- `type=ctrl`: ctrl+c→`\x03`, ctrl+d→`\x04`, ctrl+z→`\x1a` (data 为字母)

同样需要互斥检查。

#### POST /api/v1/exec (非流式)

等待命令完成后一次性返回结果。

#### POST /api/v1/exec/stream (SSE 流式)

```
Content-Type: text/event-stream

event: started
data: {"exec_id":"abc123","command":"ls -la"}

event: output
data: {"content":"file1.txt\n","seq":1}

event: completed
data: {"exec_id":"abc123","exit_code":0,"duration_ms":150}
```

#### GET /api/v1/output/lines?n=50

从 HistoryBuffer 获取最近 N 行，需要 base64 解码后按 `\n` 分割。

#### GET /api/v1/status

```json
{
  "state": "idle",
  "last_user_input": "2026-03-19T02:30:00Z",
  "current_exec_id": null,
  "connected_clients": 2,
  "terminal_size": {"cols": 120, "rows": 35}
}
```

#### 认证

所有 API 端点检查 `Authorization: Bearer {token}` 或 `?token={token}`。
Token 复用 Options.Credential，或新增 Options.APIToken。

### 3.6 用户输入阻塞

修改 `client_handler.go`：

```go
func (server *Server) handleClientInput(client *Client, message []byte) {
    if len(message) == 0 {
        return
    }

    switch message[0] {
    case '1': // Input data
        // 检查终端状态
        if server.terminalStatus.GetState() == StateAPIExecuting {
            return // 静默丢弃
        }
        // 更新用户活动时间
        server.terminalStatus.UpdateUserActivity()
        // 原有逻辑...
    }
}
```

### 3.7 Web 通知机制

新增消息类型 `'9'` (APINotification)：

`webtty/message_types.go`:
```go
const APINotification = '9'
```

服务端发送：
```go
func (sm *SessionManager) NotifyAPIExecution(execID, status string) {
    payload := map[string]string{
        "type":    status, // "api_exec_start" | "api_exec_end"
        "exec_id": execID,
    }
    data, _ := json.Marshal(payload)
    msg := append([]byte{'9'}, data...)

    sm.mu.RLock()
    for client := range sm.clients {
        select {
        case client.send <- msg:
        default:
        }
    }
    sm.mu.RUnlock()
}
```

### 3.8 前端 API 指示器

`js/src/api-indicator.ts`:
- 创建固定定位 div，位于右上角（top:10px, right:10px 旁边或下方）
- 橙色背景 + 旋转动画 + 文字提示
- 显示/隐藏方法

`js/src/xterm.tsx` 修改：
- 构造函数中创建 API 指示器元素
- 增加 `showAPIIndicator()` / `hideAPIIndicator()` 方法

`js/src/webtty.tsx` 修改：
```typescript
case '9': // API Notification
    const notification = JSON.parse(payload);
    if (notification.type === 'api_exec_start') {
        this.term.showAPIIndicator?.();
    } else if (notification.type === 'api_exec_end') {
        this.term.hideAPIIndicator?.();
    }
    break;
```

指示器样式（`resources/xterm_customize.css`）：
```css
.api-indicator {
    position: fixed;
    top: 10px;
    right: 140px;
    background: rgba(255, 165, 0, 0.9);
    color: white;
    padding: 4px 12px;
    border-radius: 4px;
    font-size: 12px;
    z-index: 1000;
    display: flex;
    align-items: center;
    gap: 6px;
    animation: fadeIn 0.3s ease-out;
}
.api-indicator .spinner { /* 旋转动画 */ }
```

### 3.9 HistoryBuffer 增强

`server/history_buffer.go` 增加：
```go
func (h *HistoryBuffer) GetLastN(n int) [][]byte {
    h.mu.RLock()
    defer h.mu.RUnlock()

    start := len(h.messages) - n
    if start < 0 {
        start = 0
    }
    result := make([][]byte, len(h.messages)-start)
    for i, msg := range h.messages[start:] {
        result[i] = make([]byte, len(msg))
        copy(result[i], msg)
    }
    return result
}
```

### 3.10 Options 扩展

```go
// server/options.go 新增字段
EnableAPI       bool   `hcl:"enable_api" flagName:"enable-api" flagDescribe:"Enable REST API" default:"false"`
APIToken        string `hcl:"api_token" flagName:"api-token" flagDescribe:"API authentication token" default:""`
ProbeTimeoutMs  int    `hcl:"probe_timeout_ms" flagName:"api-probe-timeout" flagDescribe:"Probe timeout in ms" default:"500"`
UserIdleMs      int    `hcl:"user_idle_ms" flagName:"api-user-idle-ms" flagDescribe:"User idle timeout in ms for API lock" default:"2000"`
ExecTimeoutSec  int    `hcl:"exec_timeout_sec" flagName:"api-exec-timeout" flagDescribe:"Default API exec timeout in seconds" default:"30"`
```

### 3.11 路由注册

`server/server.go` 的 `setupHandlers` 中：
```go
if server.options.EnableAPI {
    apiMux := http.NewServeMux()
    apiMux.HandleFunc(pathPrefix+"api/v1/input", server.handleAPIInput)
    apiMux.HandleFunc(pathPrefix+"api/v1/exec", server.handleAPIExec)
    apiMux.HandleFunc(pathPrefix+"api/v1/exec/stream", server.handleAPIExecStream)
    apiMux.HandleFunc(pathPrefix+"api/v1/output/lines", server.handleAPIOutputLines)
    apiMux.HandleFunc(pathPrefix+"api/v1/status", server.handleAPIStatus)
    // 注册到 wsMux
    wsMux.Handle(pathPrefix+"api/", server.wrapAPIAuth(apiMux))
}
```

---

## 四、数据流图

### 4.1 API 执行命令完整流程

```
API Client                    GoTTY Server                    PTY              Web Client
    │                              │                           │                    │
    │ POST /api/v1/exec/stream     │                           │                    │
    │─────────────────────────────>│                           │                    │
    │                              │                           │                    │
    │                              │ 1. TryAcquireAPI()        │                    │
    │                              │    state → APIExecuting   │                    │
    │                              │                           │                    │
    │                              │ 2. NotifyClients('9')     │                    │
    │                              │───────────────────────────│───────────────────>│
    │                              │                           │          显示API指示器│
    │                              │                           │                    │
    │                              │ 3. Pause broadcast        │                    │
    │                              │                           │                    │
    │                              │ 4. Send probe cmd         │                    │
    │                              │──────────────────────────>│                    │
    │                              │                           │                    │
    │                              │ 5. Read internal (验证)    │                    │
    │                              │<──────────────────────────│         ╳ 不广播    │
    │                              │                           │                    │
    │                              │ 6. Clean terminal line    │                    │
    │                              │──────────────────────────>│                    │
    │                              │                           │                    │
    │                              │ 7. Resume broadcast       │                    │
    │                              │                           │                    │
    │                              │ 8. Send actual command    │                    │
    │                              │──────────────────────────>│                    │
    │                              │                           │                    │
    │  SSE: event:output           │ 9. PTY output (广播)      │                    │
    │<─────────────────────────────│<──────────────────────────│───────────────────>│
    │                              │                           │          终端显示命令│
    │  SSE: event:output           │ 10. 更多输出...            │                    │
    │<─────────────────────────────│<──────────────────────────│───────────────────>│
    │                              │                           │                    │
    │                              │ 11. 检测到结束标记         │                    │
    │                              │     GOTTY_EXIT:id:0       │                    │
    │                              │                           │                    │
    │  SSE: event:completed        │ 12. NotifyClients('9')    │                    │
    │<─────────────────────────────│───────────────────────────│───────────────────>│
    │                              │                           │          隐藏API指示器│
    │                              │ 13. ReleaseAPI()          │                    │
    │                              │     state → Idle          │                    │
```

### 4.2 冲突拒绝流程

```
API Client                    GoTTY Server
    │                              │
    │ POST /api/v1/exec            │
    │─────────────────────────────>│
    │                              │ TryAcquireAPI()
    │                              │ state == UserActive
    │  409 Conflict                │
    │<─────────────────────────────│
    │  {"code":"USER_ACTIVE",...}  │
```

---

## 五、实施顺序

### Phase 1：基础框架
1. `server/terminal_state.go` — 状态机和互斥锁
2. `server/options.go` — 增加 API 配置选项
3. `server/api_handler.go` — API 路由框架 + 认证中间件
4. `server/server.go` — 注册路由，初始化新组件
5. `server/client_handler.go` — 用户输入阻塞

### Phase 2：探测机制
6. `server/broadcast_controller.go` — 广播控制器
7. `server/slave_reader.go` — 集成广播控制器
8. `server/probe.go` — 探测管理器

### Phase 3：命令执行
9. `server/exec_manager.go` — 命令执行 + 标记检测
10. `server/history_buffer.go` — GetLastN 方法
11. API handler 实现：input、exec、exec/stream、output/lines、status

### Phase 4：前端通知
12. `webtty/message_types.go` — 增加 '9'
13. `server/session_manager.go` — NotifyClients 方法
14. `js/src/api-indicator.ts` — 指示器组件
15. `js/src/xterm.tsx` — 集成指示器
16. `js/src/webtty.tsx` — 处理 '9' 消息
17. `resources/xterm_customize.css` — 指示器样式

### Phase 5：构建和测试
18. 重新构建前端 (`cd js && npm run build`)
19. 重新生成 bindata
20. 编译测试
