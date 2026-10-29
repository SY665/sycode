# SyCode 架构设计（RFC）

| 项目 | 内容 |
|---|---|
| 状态 | Draft |
| 范围 | v0.1（最小闭环内核） |
| 决策记录 | 见文末 ADR-001 ~ ADR-008 |

---

## 1. 设计目标

1. **单向依赖**：UI → 领域 → 基础设施，反向依赖一律视为架构违规（用测试强制）。
2. **可替换边界**：模型 provider、工具、TUI 三者可独立替换，互不影响编译。
3. **事件驱动**：LLM 输出统一收敛为 `StreamEvent` 流，UI 只消费事件，不感知 provider 差异。
4. **最小权限**：所有文件系统访问必须经过 `filesystem.Guard`，没有旁路。
5. **可测试**：每个含逻辑的包都能在不启动 TUI、不联网的情况下单测。

非目标：多进程、插件热加载、分布式会话、服务端形态。

## 2. 总体结构

```
                 ┌──────────────────────────────┐
                 │            cmd/main.go        │  初始化日志/配置/版本/退出码
                 └──────────────┬───────────────┘
                                │
                 ┌──────────────▼───────────────┐
                 │        internal/cli/cmd       │  Cobra: 参数、子命令
                 └──────────────┬───────────────┘
                                │ 组装（唯一允许 new 具体实现的地方）
        ┌───────────────────────▼────────────────────────┐
        │              internal/cli/repl                 │  Bubble Tea TUI
        │  repl/handlers/commands + 10 个子包             │
        └───────┬───────────────────────────┬────────────┘
                │ AgentCore 接口              │ 用户决策（权限）
        ┌───────▼────────┐          ┌───────▼─────────┐
        │agentcore(边界) │          │ internal/session │  JSONL 事件溯源
        └───────┬────────┘          └───────┬─────────┘
                │                            │
        ┌───────▼────────┐                   │
        │ internal/appstate│ 会话状态机 + 工具循环 │
        └───┬────────┬───┘                   │
            │        │                       │
   ┌────────▼──┐  ┌──▼──────────┐   ┌───────▼────────┐
   │ internal/ │  │ internal/   │   │ internal/config │  分层配置
   │   llm     │  │   tools     │   └───────┬────────┘
   └────┬──────┘  └──┬──────────┘           │
        │            │              ┌───────▼────────┐
   ┌────▼─────┐      │              │internal/providers│ registry.yaml
   │provider  │      │              └────────────────┘
   │  SDK     │      │
   └──────────┘  ┌───▼──────────────┐
                 │ internal/filesystem │  Guard + GitAwareness（唯一文件访问入口）
                 └───────────────────┘
```

**分层与常见实现一致**：`cli/repl → agentcore → appstate → {llm, tools, session, config}`。`agentcore` 只做两件事：定义 UI 依赖的 `AgentCore` 接口、把工具注册表与权限询问器装配起来；`appstate` 才是状态与回合逻辑。

**与常见实现唯一的内部差异**：工具循环集中在 `appstate` 一处。常见实现把循环复制到每个 provider 客户端（anthropic/openai/openai_responses/openai_codex/genkit/bedrock 各一份 `executeTools`），我们让 provider 只负责"一次请求 → 一串事件"，循环、权限询问、轮次上限、上下文记账都在 `appstate`。见 ADR-001。

## 3. 模块职责

| 包 | 职责 | 不做什么 |
|---|---|---|
| `cmd` | 进程入口、退出码、信号处理 | 不含业务逻辑 |
| `cli/cmd` | 参数解析、子命令、依赖组装 | 不直接调 provider SDK |
| `cli/repl` | TUI 主循环、输入处理、命令路由（`repl.go`/`handlers.go`/`command_handlers.go`） | 不发网络请求、不读文件 |
| `cli/repl/stream` | 把 `StreamEvent` 映射为 UI 片段 | 不做 markdown 排版 |
| `cli/repl/output` | 输出队列、滚动区、状态行、渲染调度 | 不解析协议 |
| `cli/repl/markdown` | markdown/代码块/表格/超链接的终端渲染 | 不做布局 |
| `cli/repl/permissions` | 权限询问弹窗与选择器 | 不做权限判定（判定在 Guard） |
| `cli/repl/widgets` | 可复用组件：模型选择、会话选择、建议列表 | 不含业务状态 |
| `cli/repl/theme` | 颜色、样式、符号集 | 不含逻辑 |
| `cli/repl/commands` | 斜杠命令注册表与帮助文本 | 不实现命令行为 |
| `cli/repl/history` | 输入历史持久化 | 不做补全 |
| `cli/repl/filesearch` | 输入框内的 `@` 文件补全 | 不做搜索索引 |
| `cli/repl/askuser` | `ask_user` 工具的多选/单选交互 | 不做工具调用 |
| `agentcore` | UI 边界的 `AgentCore` 接口 + adapter + 工具装配 | 不持有会话状态 |
| `appstate` | 会话状态机：消息、回合、工具循环、模式 | 不渲染、不直接读终端 |
| `llm` | 请求/消息/事件抽象、系统提示、重试、token 估算、工具 schema 转换 | 不决定"要不要继续调工具" |
| `llm`（provider 文件） | 各协议的流式解析（v0.1 只有 `openai.go`） | 不含业务策略 |
| `tools` | 各工具实现 + 注册表 + 输入校验 | 不做权限决策（只查询 Guard） |
| `filesystem` | 路径解析、权限三态判定、.gitignore、敏感路径 | 不执行读写 |
| `session` | JSONL 追加、事件解码、按目录列出/恢复 | 不构造 prompt、不 import `llm` |
| `config` | 分层加载、默认值、密钥解析 | 不校验业务语义 |
| `providers` | `registry.yaml` 内嵌 + 模型元数据查询 | 不发起请求 |
| `logging` | slog 初始化、文件轮转、级别 | 不全局打点 |

## 4. 核心接口

接口定义在**消费方**包内，实现方不得反向依赖。

### 4.1 LLM 客户端

```go
// internal/llm/client.go
type Client interface {
    // StreamChat 发起一次流式请求；返回的 channel 在流结束时关闭。
    // 所有错误（含网络）通过 StreamEvent{Type: EventError} 传出，
    // 只有"请求都无法构造"这类前置错误才作为第二个返回值。
    StreamChat(ctx context.Context, req Request) (<-chan StreamEvent, error)
}
```

```go
type Request struct {
    Model          string          // 来自 providers 注册表
    System         []SystemBlock   // 系统提示（分段以便缓存）
    Messages       []Message       // 历史 + 本轮用户消息
    Tools          []ToolSchema    // 由 tools.Registry 导出
    ThinkingEffort string          // 空 = 不支持/不启用
    MaxOutputTokens int
}
```

```go
// internal/llm/stream.go
type EventType string

const (
    EventText      EventType = "text"       // 正文增量
    EventReasoning EventType = "reasoning"  // 思考增量
    EventToolStart EventType = "tool_start"
    EventToolEnd   EventType = "tool_end"
    EventUsage     EventType = "usage"
    EventRetry     EventType = "retry"
    EventDone      EventType = "done"
    EventError     EventType = "error"
)

type StreamEvent struct {
    Type    EventType
    Text    string     // text/reasoning
    Tool    *ToolCall  // tool_start/tool_end
    Usage   *Usage     // usage
    Attempt int        // retry
    Err     error      // error
}
```

**约定**：`EventDone` 与 `EventError` 二者必有其一，且为最后一个事件。UI 与 appstate 都以此为终态判据，不依赖 channel 关闭时机。

### 4.2 工具

```go
// internal/tools/tool.go
type Tool interface {
    Name() string
    Description() string            // 写给模型看，决定调用质量
    InputSchema() map[string]any    // JSON Schema 子集
    Execute(ctx context.Context, in Input) (Result, error)
}

// 可选：实现后注册表会在执行前统一校验
type Validator interface {
    ValidateInput(ctx context.Context, in Input) error
}
```

```go
type Input = map[string]any  // 由各工具自行解码，禁止共享结构体
type Result struct {
    Content  string          // 回灌给模型的文本
    Display  *DisplayPayload // 可选：给 UI 的结构化展示（diff、表格）
    Artifact string          // 可选：超限输出落盘路径
}
```

**契约要求**（写进 CODING-STANDARDS）：
- `Execute` 永不 panic；错误返回给模型时用中文前缀 `参数错误：` / `执行失败：` / `权限不足：` / `超时：` / `不支持：`，并保留英文字段名与示例 JSON，便于模型自我修正（文案规范见 CODING-STANDARDS §13）。
- 错误信息必须可操作（指出缺哪个参数、哪个路径不存在），因为模型会读它。
- `Result.Content` 有上限（默认 32KB），超限截断并附 `Artifact` 路径。

### 4.3 权限

```go
// internal/filesystem/guard.go
type Permission int

const (
    Denied Permission = iota
    Pending
    Granted
)

type Op string

const (
    OpRead  Op = "read"
    OpWrite Op = "write"
    OpExec  Op = "exec"
)

type Guard interface {
    Resolve(path string) (string, error)      // 绝对化 + Clean，不解符号链接前先判穿越
    Check(path string, op Op) (Permission, Reason)
}
```

常见实现用 `CheckPath(path, operation string)` 的字符串操作名，这里改为 `Op` 类型，避免拼写错误静默放行。

策略表：

| 场景 | read | write | exec |
|---|---|---|---|
| 工作目录内、未被忽略 | Granted | Pending | Pending |
| 工作目录内、被 .gitignore 忽略 | Denied | Denied | Denied |
| 工作目录外、非系统路径 | Pending | Pending | Denied |
| 系统路径（`/etc`、`C:\Windows`…） | Denied | Denied | Denied |
| `~/.sycode/bash/`（工具输出 artifact） | Granted | Denied | Denied |
| 符号链接 | 解析后按真实路径判定 | 同左 | 同左 |

### 4.4 会话

```go
// internal/session/session.go
type EventKind string

const (
    KindSessionStarted EventKind = "session_started"
    KindUserMessage    EventKind = "user_message"
    KindAssistantTurn  EventKind = "assistant_turn"
    KindCompaction     EventKind = "compaction_applied" // v0.1 仅预留
)

type Event struct {
    Seq  uint64          `json:"seq"`
    Kind EventKind       `json:"kind"`
    TS   time.Time       `json:"ts"`
    Data json.RawMessage `json:"data"` // 按 Kind 解码为具体 Payload
}
```

选择"Kind + RawMessage 判别联合"而不是常见实现的"一个大 struct 带若干可选指针"：事件类型会持续增加，前者新增类型只需加一个 Payload 与解码分支，且不会让单条事件里充斥着 null 字段。

存储布局：

```
~/.sycode/sessions/
└── <工作目录名>-<sha1前10位>/
    ├── <session-id>/
    │   └── transcript_events.jsonl   # 只追加，永不改写
    └── index.json                    # 摘要索引（标题/时间/回合数），可重建
```

## 5. 数据流：一次用户输入

```
1. TUI 收集输入 ──► 是斜杠命令？ → 是 → commands 处理，结束
2. session 追加 user_message
3. agentcore.Submit ──► appstate 组装 Request（系统提示 + 历史投影 + 工具 schema）
4. llm.Client.StreamChat ──► 流式解析 ──► StreamEvent
5. appstate 消费事件：文本 → 经 agentcore 转 UI；tool_start → Guard 检查 → 需要则发权限询问
6. 工具执行 → 结果追加为 tool 消息 → 回到第 4 步（上限 25 轮，防死循环）
7. 终态：session 追加 assistant_turn（含 transcript 片段），UI 收尾
```

**循环终止条件**：模型不再返回工具调用 / 达到最大轮次 / 用户中断 / 不可重试错误。

## 6. 目录结构

包级与常见实现一一对应；**文件按"一个功能一个文件"切**，不再额外套子目录（常见实现 `llm/openai.go` 这么放，我们也这么放）。

```
sycode/
├── cmd/
│   └── main.go                 # 入口：logging 初始化、版本、退出码
├── internal/
│   ├── agentcore/              # UI 边界：AgentCore 接口、adapter、工具装配
│   │   ├── agentcore.go
│   │   ├── adapter.go
│   │   ├── types.go
│   │   └── setup_tools.go
│   ├── appstate/               # 会话状态机、回合与工具循环
│   │   ├── state.go            # 消息、模式、上下文占用
│   │   └── turn.go             # 一次回合：组装请求 → 消费流 → 执行工具 → 循环
│   ├── cli/
│   │   ├── cmd/                # root.go、resume.go
│   │   └── repl/
│   │       ├── repl.go                 # 主循环与状态
│   │       ├── handlers.go             # 输入/快捷键
│   │       ├── command_handlers.go     # 斜杠命令实现
│   │       ├── repl_helpers.go
│   │       ├── session_state.go        # 会话上下文
│   │       ├── context_status.go       # 上下文占用
│   │       ├── headless_run.go         # sycode run
│   │       ├── stream/                 # handler.go、render.go、segments.go
│   │       ├── output/                 # output.go、compaction_status.go
│   │       ├── markdown/               # markdown.go、hyperlink.go
│   │       ├── permissions/            # requester.go、selector.go
│   │       ├── widgets/                # model_selection.go、session_picker.go、suggestion.go…
│   │       ├── theme/                  # styles.go、markdown.go
│   │       ├── commands/               # commands.go（命令注册表）
│   │       ├── history/                # history.go
│   │       ├── filesearch/             # filesearch.go
│   │       └── askuser/                # render.go、requester.go、state.go
│   ├── config/                 # config.go、loader.go、key_resolver.go
│   ├── filesystem/             # guard.go、gitawareness.go
│   ├── llm/                    # client.go、stream_events.go、request.go、systemprompt.go、
│   │                           # retry.go、estimate.go、errors.go、openai.go
│   │                           # 注意：没有 tool_execution.go —— 工具执行在 appstate/turn.go
│   ├── providers/              # registry.go、registry.yaml
│   ├── session/                # store.go、event.go、projection.go、path.go
│   ├── tools/                  # tool.go、registry.go、read_file.go、write_file.go、
│   │                           # edit_file.go、glob.go、grep.go、bash.go
│   └── logging/                # logging.go
├── docs/
├── .ai-interactions/
├── .github/workflows/ci.yml
├── go.mod

├── Makefile
└── README.md
```

## 7. 技术选型

| 领域 | 选择 | 版本要求 | 理由 | 备选与否决原因 |
|---|---|---|---|---|
| 语言 | Go | ≥ 1.24（开发机 1.26.3） | 单二进制、交叉编译、并发模型适合流式 | Rust：开发迭代慢；TS：分发重 |
| CLI | `spf13/cobra` | v1.10+ | 子命令/flag 生态成熟，常见实现同选 | 标准库 `flag`：子命令要手写 |
| TUI | `charm.land/bubbletea/v2` | v2.x | Elm 架构（Init/Update/View）天然契合事件流 | tview：命令式，状态散落 |
| 样式 | `charm.land/lipgloss/v2` | v2.x | 与 bubbletea 同族，布局能力强 | 手写 ANSI：不可维护 |
| 组件 | `charm.land/bubbles/v2` | v2.x | textarea/viewport/spinner 开箱可用 | 自研：重复投入 |
| Markdown | `charmbracelet/glamour` | v0.10+ | 终端渲染代码块/表格 | 自研渲染器：不值得 |
| 模型 SDK | `openai/openai-go/v3` | v3.x | 一份实现覆盖所有 OpenAI 兼容端点（含国内厂商） | 直连 HTTP：丢失类型与重试封装 |
| 模型 SDK（P2） | `anthropics/anthropic-sdk-go` | v1.x | 原生 thinking / cache 字段 | 用 OpenAI 兼容层代理：丢能力 |
| gitignore | `go-git/go-git/v5` | v5.x | 直接复用其 gitignore 解析器 | 自己写 glob：语义易错 |
| diff | `aymanbagabas/go-udiff` | v0.4+ | 统一 diff，渲染与测试共用 | 自研 LCS：边界多 |
| 配置 | 标准库 `encoding/json` + `gopkg.in/yaml.v3` | — | JSON 配置 + YAML 注册表 | 引入 viper：依赖过重 |
| 日志 | 标准库 `log/slog` | — | 结构化、零依赖 | zap：此处无必要 |
| 测试 | 标准库 `testing` + `httptest` | — | 无第三方测试框架，降低门槛 | testify：断言糖不值得引依赖 |

> **版本纪律**：`go.mod` 中的直接依赖不超过 15 个。每新增一个直接依赖，PR 必须说明"为什么标准库/现有依赖做不到"。

## 8. 关键设计决策（ADR）

### ADR-001 保留常见实现的 `agentcore`/`appstate` 边界，但工具循环只写一份
- **背景**：常见实现有两层后端边界——`agentcore`（面向 UI 的 `AgentCore` 接口 + adapter）与 `appstate`（会话状态机，446 行）；同时把工具循环复制进每个 provider（anthropic / openai / openai_responses / openai_codex / genkit / bedrock 各一份 `executeTools`，22~40 行/份，循环控制再散落各自的 `StreamChat`）。
- **决策**：包边界与常见实现一致（`agentcore` + `appstate`），但循环只保留在 `appstate` 一处；provider 只负责"一次请求 → 一串事件"。
- **理由**：循环本身不长（6 份合计约 180 行），复制不会立刻爆炸；真正的问题是一旦加入轮次上限、权限询问、连续失败换策略、上下文记账，这些逻辑要改 6 次且容易走形。边界照抄常见实现是为了让结构可对照，循环收敛是为了让策略只有一处。
- **后果**：新增 provider 只实现 SSE 解析与 schema 转换（约 300 行），不改循环；代价是消息需要在 `llm.Message` 与 provider 原生格式间转换一次（`llm/openai.go` 内的 convert 函数）。

### ADR-002 v0.1 只实现 OpenAI 兼容协议
- **理由**：一个 client 可覆盖 OpenAI、DeepSeek、Kimi、GLM、本地 vLLM/Ollama，边际收益最高；Anthropic 原生能力（thinking、prompt cache）留到 P2。
- **后果**：`Request.ThinkingEffort` 在 v0.1 对不支持该字段的端点直接忽略并记 debug 日志。

### ADR-003 `edit_file` 先用"精确字符串替换"，后续再评估 hashline
- **理由**：字符串替换零学习成本，模型已普遍擅长；hashline 的价值在于并发/漂移场景下的安全性，v0.1 单会话单写者，收益不明显。
- **红线**：匹配必须唯一。0 处或多处一律失败，并返回候选行号——禁止模糊匹配后"猜着改"。
- **演进**：P6 评估引入 `N:HASH|` 锚点，届时 `read_file` 输出格式改变，属于**破坏性变更**，需走 ADR 修订 + 版本号 minor 提升。

### ADR-004 会话用 JSONL 事件溯源，不做快照
- **理由**：追加写天然抗崩溃、可回放、便于调试；快照需要处理并发与体积。
- **后果**：恢复会话需投影（`projection.go`）重建消息列表；事件只增不改，纠错靠追加新事件。

### ADR-005 权限判定与文件 IO 分离
- **理由**：`Guard` 纯函数化（给定路径与操作返回三态），可 100% 单测，无需 mock 文件系统；工具负责"问 → 做"。
- **后果**：任何绕过 `Guard` 直接 `os.Open` 的代码视为安全缺陷，评审直接打回。

### ADR-006 Windows 下的 shell 选择顺序
- **背景**：`bash` 工具在 Windows 上语义与 POSIX 不同（引号、路径、管道）。
- **决策**：解析顺序 `SYCODE_SHELL` 环境变量 → `bash.exe`（Git Bash）→ `pwsh` → `cmd.exe`；选中的 shell 在启动时打印一次，并写入会话事件。
- **后果**：工具描述里必须告知模型当前 shell 类型，系统提示注入 `Current shell: <name>`。

### ADR-007 不引入遥测（即便 opt-in）
- **事实**：常见实现的遥测是 **opt-in**（默认关闭，需显式 `TELEMETRY=1`），走 GA4 Measurement Protocol，只发 `session_start`/`session_end` 与 `version`/`os`/`arch`/`mode`/时长，不含对话内容；measurement_id 与 api_secret 在构建时用 ldflags 注入。
- **决策**：我们不做，包括 opt-in 形态。
- **理由**：① 需要运营方持有 GA4 凭据并在构建期注入，我们没有也不该有；② 即便只发元数据，client_id + 使用时长对"能读用户代码库的工具"仍是可关联标识；③ opt-in 的价值在于运营方能看数据做决策，自用项目没有这个闭环，收益为零。
- **后果**：产品判断只能靠本地日志、`/context`/usage 展示与用户反馈；崩溃报告需用户手动提供。若将来重启，必须满足：opt-in、仅元数据、可完全离线、代码里能一眼看出边界。
- **配套**：可观测性由本地承担——结构化日志（`~/.sycode/logs/`）、`/context` 占用、usage 统计，都是用户自己看得见的。

### ADR-008 依赖方向用测试守护
- **做法**：`internal/cli/cmd/deps_test.go`（不新增包）用 `go list -json ./...` 读取每个包的 import，断言禁止边（如 `llm` 不得 import `cli`、`tools` 不得 import `llm`）。
- **后果**：架构违规在 CI 立刻失败，而不是靠评审记忆。

## 9. 错误处理与可观测性

| 层 | 策略 |
|---|---|
| provider | 429/5xx/网络 → 指数退避（1s/2s/4s，±20% 抖动），最多 3 次；4xx → 原样上报，不重试 |
| 工具 | 错误转文本回灌模型，附修复建议；连续 3 次同工具同参数失败 → 提示模型换策略 |
| appstate | 单回合最大 25 次工具调用；超限以 `EventDone` + 提示收尾，不抛异常 |
| 进程 | 顶层 `recover` 只用于生成崩溃日志；退出码：0 成功 / 1 一般错误 / 2 用法错误 |
| 日志 | `~/.sycode/logs/sycode-<ts>.log`，默认 info；`SYCODE_LOG_LEVEL=debug` 打开请求摘要（**不含**密钥与完整 prompt） |

## 10. 待设计（不在 v0.1 范围内）

1. **上下文压缩（compaction）**：预算公式、手动/自动触发时机、摘要 schema、与 KV cache 的关系。
2. **子代理（subagent）**：profile 格式、能力裁剪、结果回传压缩、与主循环的并发模型。
3. **Skills**：目录发现顺序、激活注入方式、参数替换。
4. **MCP**：传输方式（stdio/HTTP）、认证、工具 schema 动态注入。
5. **hashline 编辑**：锚点格式与漂移检测。

> 这五项各自需要独立 PRD + RFC，不并入 v0.1。第 1、2 项是本项目最想重点表达的能力，将在下一轮讨论后单独出文档。

## 11. 变更记录

| 日期 | 变更 |
|---|---|
| 2026-10-29 | 初稿：分层、接口、选型、ADR-001~008 |
| 2026-10-29 | 结构调整：包级与常见实现对齐——新增 `agentcore`/`appstate` 取代自建的 `agent`；`llm/openai/` 收平为 `llm/openai.go`；取消 `internal/arch` 包（依赖断言移入 `cli/cmd/deps_test.go`）；`cli/repl` 子包补齐至 10 个 |
