# SyCode 代码设计（CODE-DESIGN）

本文规定**每个文件里应该有哪些函数、签名是什么、谁调用谁**。它是写代码前的契约，评审时逐条对照。
目录结构见 [ARCHITECTURE.md](./ARCHITECTURE.md) §6；编码风格见 [CODING-STANDARDS.md](./CODING-STANDARDS.md)。

模块路径：`github.com/sy665/sycode`

---

## 0. 六条总约定

| # | 约定 | 理由 |
|---|---|---|
| 1 | `main()` 只有一行 `os.Exit(run())`，`run() int` 承担全部组装 | 组装逻辑可被测试，退出码集中 |
| 2 | 构造器统一 `NewXxx(deps…)`，只有**可能失败**（IO/校验/网络配置）时才返回 `error` | 调用点一眼看出是否会失败 |
| 3 | 需要全局副作用的包只暴露一个 `Init()`，返回 `cleanup func()` 与关键路径 | 关闭顺序可控，不靠 `init()` |
| 4 | 接口定义在**消费方**；为可测性抽小接口（如 `chatStream`） | 避免实现方反向依赖 |
| 5 | 复杂逻辑拆**包内私有纯函数**（`formatFileContent`），公开方法只做编排 | 纯函数好测，编排好读 |
| 6 | 文件内顺序固定：`const` → `type` → 构造器 → 公开方法 → 私有方法 → 私有函数 | 打开文件先看到"对外是什么" |

**动词表**（命名不许自创同义词）：

| 动作 | 前缀 | 例 |
|---|---|---|
| 构造 | `New` | `NewGuard`、`NewStore` |
| 读外部状态 | `Load` | `config.Load`、`providers.Load` |
| 纯计算/拼装 | `Build`、`Format` | `Build`（系统提示）、`formatFileContent` |
| 判定 | `Check`、`Is`、`Has` | `Check(path, op)`、`IsIgnored` |
| 执行副作用 | `Run`、`Execute`、`Submit` | `cleanup.Run`、`Execute`（工具）、`Submit`（回合） |
| 转换 | `to`、`Parse` | `toOpenAIMessages`、`parseSSE` |
| 回收 | `Prune` | `PruneExpired` |

## 0.1 错误文案契约（全局）

文案规范见 [CODING-STANDARDS.md](./CODING-STANDARDS.md) §13，此处只规定**谁负责产出哪一层文案**：

| 层 | 文案由谁产出 | 语言与形态 | 例 |
|---|---|---|---|
| `filesystem` / `config` / `providers` | 基础包自己产出 | 中文，四要素完整 | `读取配置 "configs.json" 失败：%w。请检查文件权限。` |
| `tools` 的参数校验 | 工具自己（`ValidateInput`） | 中文 + 英文字段名 + 示例 JSON | `参数错误：缺少必填字段 "path"。请带上 {"path":"…"} 重试。` |
| `tools` 的执行错误 | 工具自己 | `执行失败：` / `权限不足：` / `超时：` 前缀 | `权限不足：路径 "…/secret" 不在工作目录内，已被拒绝。` |
| provider HTTP/网络错误 | **`llm/errors.go` 的 `Translate`** 统一转换 | 中文 + 括号内保留原始码 | `执行失败：模型服务返回 429（rate limit exceeded）。请稍后重试。` |
| cobra 参数/未知命令 | `cli/cmd` 统一包装 | 中文，保留原始参数名 | `参数错误：未知参数 "--cwd2"` |
| 顶层未捕获错误 | `run()` | `错误：<原错误>` + 退出码 | — |

**规则**：下层永远不改写上层已产出的中文文案；`Translate` 只处理"原始英文错误"，且必须用 `%w` 保留原错误，供 `errors.Is` 判定。

## 1. 启动与关闭顺序（唯一权威）

```
run() int
 ├─ 1. logging.Init()            → (cleanup, logPath, err)     失败：stderr + 退出 1
 ├─ 2. providers.Load()          → 内嵌 registry.yaml           失败：panic 级配置错误，退出 1
 ├─ 3. config.Load()             → ~/.sycode/configs.json        缺失：走默认，非致命
 ├─ 4. config.Resolve()          → 合并 flag > env > 文件 > 默认
 ├─ 5. config.ResolveKey()       → env → 配置文件                缺失：友好报错，退出 1
 ├─ 6. filesystem.NewGitAwareness(workdir) → NewGuard(workdir, git)
 ├─ 7. llm.NewOpenAI(cfg)        → 只存配置，不发请求
 ├─ 8. session.NewStore(workdir)
 ├─ 9. agentcore.New(...)        → 内部 appstate.New(...)
 ├─ 10. agentcore.SetupTools(...)→ 返回已注入权限/上下文的 Registry
 ├─ 11. cleanup.Run()            → 异步，失败只记日志，不影响主流程
 └─ 12. repl.Run(ctx, deps)      → 阻塞直到用户退出
```

**步骤 2–5 由 `main.resolveModel()` 实现，且延迟到进入界面之前才执行**（注入为 `cmd.Options.ResolveModel`）。
理由：版本与帮助不需要密钥，若在 `run()` 里提前跑完，新用户敲的第一条 `sycode --version`
会直接撞上"缺少密钥"，恰好卡在他还没机会配置的时候。

**退出码口径**（ARCHITECTURE §411）：0 成功 / 1 一般错误 / 2 用法错误。配置类失败（缺密钥、`configs.json` 写坏、
模型不属于供应商）一律**退出 1**——它们不是命令行参数写错，印成"参数错误：… 用法见 --help"只会误导。

关闭：`repl.Run` 返回 → 会话已落盘（每次 `Append` 都是即时的）→ `defer logging.cleanup()` → 返回退出码。

**禁止**：`init()` 里做 IO/网络；`NewXxx` 里发请求；`run()` 里直接读 `os.Args`（交给 cobra）。

## 2. 入口层

### `cmd/main.go`

| 元素 | 签名 | 职责 |
|---|---|---|
| `version` | `var version = "dev"` | 构建时 `-X main.version=` 覆盖。**必须是 var**：`-X` 只改写字符串变量，对 const 静默失效（实测仍输出默认值），构建看似成功但版本号是错的 |
| `main` | `func main()` | 仅 `os.Exit(run())` |
| `run` | `func run() int` | 按 §1 顺序组装；把错误分流到 stderr 与退出码 |
| `resolveModel` | `func resolveModel() (string, error)` | §1 的 2–5 步（注册表 → 配置 → 合并 → 密钥校验），返回模型名；错误原样上抛，不叠前缀 |

不做什么：不解析参数、不打印帮助、不判断"是不是交互式"。

### `internal/cli/cmd/root.go`

| 元素 | 签名 | 职责 |
|---|---|---|
| `Options` | `type Options struct { Version, Commit string; Stdout, Stderr io.Writer; ResolveModel func() (string, error); StartREPL func(model string) error }` | 注入版本、输出与模型解析器；后两项是测试替换点 |
| `NewRootCommand` | `func NewRootCommand(opts Options) *cobra.Command` | 建 root + `version` + `run` 子命令；同时完成汉化 |
| `runREPL` | `func runREPL(opts Options) error` | 先解析模型（失败则不进界面、退出 1），再把模型名交给界面 |
| `resolveModel` | `func resolveModel(opts Options) (string, error)` | 调注入的解析器；未注入视为"没有模型"，只测命令树时不必造配置 |
| `newVersionCmd` | `func newVersionCmd(opts Options) *cobra.Command` | 输出 `0.1.0 (build abc1234)` |
| `newRunCmd` | `func newRunCmd() *cobra.Command` | headless 单回合（P4 实装） |
| `versionString` | `func versionString(opts Options) string` | 有提交号才带 `(build …)`，避免空括号 |
| `ExitCode` | `func ExitCode(err error) int` | 取 `exitError.code`；非该类型按 1 |
| `usageErrorf` / `errNotImplemented` | 内部构造器 | 退出码 2（用法）与 1（未实现）分离 |
| `localizeDefaultFlags` / `translateFlagError` | 汉化辅助 | cobra 无 i18n，默认 flag 与内置错误都要覆盖 |

`resume.go`：`newResumeCmd(store *session.Store) *cobra.Command` + `pickSession(...)`。

## 3. 基础设施层

### `internal/logging/logging.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Init` | `func Init() (cleanup func(), path string, err error)` | 建目录、开文件、装 handler、`slog.SetDefault` |
| `parseLevel` | `func parseLevel(s string) slog.Level` | `SYCODE_LOG_LEVEL` → level，未知值回退 info |
| `logDir` | `func logDir() (string, error)` | `~/.sycode/logs`，权限 0700 |
| `createFile` | `func createFile(dir string) (*os.File, string, error)` | `sycode-20060102-150405.log`，权限 0600；同时返回路径供 `Init` 交给调用方（只返回 `*os.File` 则 `Init` 要重算一遍文件名） |
| `newHandler` | `func newHandler(w io.Writer, level slog.Level) slog.Handler` | 只产出**文件用 JSON** handler。v0.1 不向终端输出日志：Bubble Tea 接管屏幕后写 stderr 会破坏渲染；终端可读格式留给 P4 的 TUI 面板 |

测试点：级别解析全部取值；目录不可写时返回错误而非 panic。

### `internal/config/`

| 文件 | 函数 | 签名 |
|---|---|---|
| `config.go` | `Config` | `type Config struct { ActiveProvider, ActiveModel, ThinkingEffort string; Providers map[string]ProviderConfig }` |
| | `ProviderConfig` | `type ProviderConfig struct { BaseURL, APIKeyEnv string }`（留空=沿用注册表） |
| | `Resolved` | `type Resolved struct { ProviderID, ModelID, BaseURL, APIKeyEnv, ThinkingEffort string; ContextWindow int }` |
| | `Load` | `func Load(path string) (Config, error)`：文件不存在 → 空 Config + nil（首次运行不是错误） |
| | `DefaultPath` | `func DefaultPath() (string, error)` |
| | `Resolve` | `func (c Config) Resolve(reg *providers.Registry, providerID, modelID string) (Resolved, error)`：显式参数 > 配置 `active_*` > 注册表默认 |
| `key_resolver.go` | `ResolveKey` | `func ResolveKey(r Resolved) (string, error)`：按 `APIKeyEnv` 读环境变量；`APIKeyEnv` 为空表示不需要密钥 |

> 与早期计划的差异（2026-10-29）：原计划拆 `loader.go`，实际并入 `config.go`——
> 加载只有两个函数，单独成文件不增加可读性，还把单元推到 4 个文件（超过上限）。

**错误契约**：缺 key 返回的错误必须包含供应商名与"该设哪个环境变量"，且**绝不回显 key**。

### `internal/providers/registry.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Load` | `func Load() (*Registry, error)` | `//go:embed registry.yaml` → 解析 + 校验 |
| `LoadFile` | `func LoadFile(r io.Reader) (*Registry, error)` | 任意来源解析，供测试与将来的自定义注册表；`Load` 也走它 |
| `Find` | `func (r *Registry) Find(providerID, modelID string) (Provider, Model, bool)` | 找不到返回 `false`；供应商命中但模型没命中时**仍返回该 Provider**，调用方据此区分"供应商错"还是"模型错" |
| `Default` | `func (r *Registry) Default() (providerID, modelID string)` | 首次运行的兜底 |
| `parse` / `validate` | 私有 | YAML 解析与自洽性校验（id 唯一、`context_window > 0`、默认值必须存在） |

数据结构（**加法变更，2026-10-29**）：`Provider` 增加 `BaseURL` 与 `APIKeyEnv`。
理由：这两个是供应商固有能力，若放进用户配置，每个用户都要为同一个端点重复填写；
用户配置只负责"选择与覆盖"。

### `internal/filesystem/`

`guard.go`：

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewGuard` | `func NewGuard(workingDir string, git *GitAwareness) *Guard` | 预计算工作目录绝对路径与黑名单 |
| `Check` | `func (g *Guard) Check(path string, op Op) (Permission, Reason)` | **纯判定**，不碰磁盘内容（只做路径解析与模式匹配） |
| `Resolve` | `func (g *Guard) Resolve(path string) (string, error)` | 绝对化 + Clean + 符号链接解析后再判 |
| `IsBlocked` | `func (g *Guard) IsBlocked(resolved string) bool` | 系统目录 / gitignore / 家目录隐藏目录 |
| `defaultBlockedPaths` | `func defaultBlockedPaths() []string` | 与平台相关（Windows 加 `C:\Windows` 等） |
| `bashOutputDir` | `func bashOutputDir() (string, error)` | `~/.sycode/bash`，读授权例外 |

`gitawareness.go`：`NewGitAwareness(root string) (*GitAwareness, error)`（递归载入 `.gitignore`）、`IsIgnored(path string) bool`。

**红线**：`Check` 不允许有副作用；任何文件读写必须 `Check` 之后才执行，且写操作还要 `Pending → 询问`。

## 4. 工具层 `internal/tools/`

### `tool.go`（契约与注册表）

```go
type Input = map[string]any

type Result struct {
    Content  string
    Display  *Display       // 给 UI 的结构化展示（diff / 表格）
    Artifact string         // 超限输出落盘路径
}

type Schema struct {        // 注意：不是 llm.ToolSchema —— tools 不依赖 llm
    Name        string
    Description string
    Input       map[string]any
}

type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Execute(ctx context.Context, in Input) (Result, error)
}

type Validator interface{ ValidateInput(ctx context.Context, in Input) error }

type PermissionRequester interface {   // 由 UI 实现，注入给工具
    RequestPermission(ctx context.Context, req PermissionRequest) (Decision, error)
}
```

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewRegistry` | `func NewRegistry() *Registry` | 空表 + 顺序切片（导出顺序稳定） |
| `Register` | `func (r *Registry) Register(t Tool) error` | 重名/空名报错 |
| `Get` | `func (r *Registry) Get(name string) (Tool, bool)` | |
| `All` | `func (r *Registry) All() []Tool` | 按注册顺序 |
| `Schemas` | `func (r *Registry) Schemas() []Schema` | 给 llm 转换用 |

### `read_file.go`（其余工具同构）

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewReadFileTool` | `func NewReadFileTool(guard *filesystem.Guard, perm PermissionRequester) *ReadFileTool` | 依赖注入，无全局 |
| `Name` | `func (t *ReadFileTool) Name() string` | 常量 `ReadFileToolName` |
| `Description` | `func (t *ReadFileTool) Description() string` | 写给模型：何时用、不适用什么 |
| `InputSchema` | `func (t *ReadFileTool) InputSchema() map[string]any` | 只列 `path`/`offset`/`limit` |
| `ValidateInput` | `func (t *ReadFileTool) ValidateInput(_ context.Context, in Input) error` | 缺参/类型错 → `参数错误：…`（中文，保留英文字段名） |
| `Execute` | `func (t *ReadFileTool) Execute(ctx context.Context, in Input) (Result, error)` | 编排：校验 → Check → （询问）→ 读 → 格式化 |
| `readFileRange` | `func readFileRange(path string, offset, limit int) (content string, total, read int, truncated bool, err error)` | 私有纯逻辑，独立可测 |
| `isBinary` | `func isBinary(b []byte) bool` | NUL 字节探测 |

**每个工具必须恰好有这 5 个方法 + 构造器**；不允许加 `BeforeExecute`、`PostHook` 这类钩子（要加就走 ADR）。

### `edit_file.go` 的额外约定

`Execute` 内部必须走三段：`locateMatches(content, old) ([]int, error)`（0 或 >1 直接失败并返回候选行号）→ `applyReplace(...)` → `buildDiff(old, new) []EditDiffLine`。三个都是私有纯函数，各有单测。

### `bash.go` 的额外约定

`resolveShell() (Shell, error)`（ADR-006 的选择顺序）、`isDangerous(cmd string) bool`（模式表）、`truncateOutput(out []byte) (inline string, artifact string)`。

## 5. 模型层 `internal/llm/`

### `client.go` + `request.go` + `stream_events.go`

```go
type Client interface {
    StreamChat(ctx context.Context, req Request) (<-chan StreamEvent, error)
}

type Request struct {
    Model           string
    System          string
    Messages        []Message
    Tools           []tools.Schema   // 只读，不反向依赖 tools 实现
    ThinkingEffort  string
    MaxOutputTokens int
}

type Message struct {
    Role       Role        // user / assistant / tool
    Content    string
    ToolCalls  []ToolCall
    ToolCallID string
}

type StreamEvent struct { Type EventType; Text string; Tool *ToolCall; Usage *Usage; Attempt int; Err error }
```

| 函数 | 签名 | 职责 |
|---|---|---|
| `newEvent` | `func newEvent(t EventType, opts ...eventOpt) StreamEvent` | 统一构造，避免字段散填 |
| `errEvent` | `func errEvent(err error) StreamEvent` | 终态错误 |
| `doneEvent` | `func doneEvent(usage *Usage) StreamEvent` | 终态完成 |

### `systemprompt.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Env` | `type Env struct { OS, Arch, WorkDir, Shell, Model string; Today time.Time }` | 环境事实 |
| `Build` | `func Build(env Env) string` | 按固定段拼接：身份 → 工具约定 → 环境 → 项目指令(`AGENTS.md`) |
| `identitySection` | `func identitySection() string` | 私有，可单独比对 |
| `envSection` | `func envSection(env Env) string` | 含 `Current shell:`（ADR-006 要求） |
| `projectSection` | `func projectSection(workDir string) string` | 读 `AGENTS.md`，超长截断并注明 |

阈值集中为常量：`maxProjectInstructions = 8 * 1024`。

### `openai.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `OpenAI` | `type OpenAI struct { http *http.Client; baseURL, apiKey, model string }` | |
| `NewOpenAI` | `func NewOpenAI(cfg OpenAIConfig) (*OpenAI, error)` | 校验 baseURL/key，**不发请求** |
| `StreamChat` | `func (c *OpenAI) StreamChat(ctx context.Context, req Request) (<-chan StreamEvent, error)` | 起 goroutine，返回只读 channel |
| `toOpenAIMessages` | `func toOpenAIMessages(msgs []Message) []oai.ChatCompletionMessageParamUnion` | 纯转换 |
| `toOpenAITools` | `func toOpenAITools(schemas []tools.Schema) []oai.ChatCompletionToolUnionParam` | 纯转换 |
| `parseSSE` | `func parseSSE(ctx context.Context, r io.Reader, out chan<- StreamEvent) error` | 解析 `data:` 分片、`[DONE]`、跨行块 |
| `emit` | `func emit(ctx context.Context, out chan<- StreamEvent, ev StreamEvent) bool` | 带 ctx 取消的发送 |

**channel 契约**：`StreamChat` 返回的 channel 由**发送方关闭**；`EventDone`/`EventError` 是最后一个事件。

### `errors.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Translate` | `func Translate(err error) error` | 把 provider/网络的**英文原始错误**转成中文，保留原始信息与状态码，`%w` 链不丢 |
| `httpStatusError` | `type httpStatusError struct { Status int; Code, Body string }` | 携带结构化信息，供上层判定可重试性 |

`Translate` 只处理原始错误；已经中文化的错误原样返回（`errors.As` 判定后直接 return）。

### `retry.go` / `estimate.go`

| 函数 | 签名 |
|---|---|
| `Do` | `func Do(ctx context.Context, attempts int, fn func(context.Context) error, onRetry func(attempt int, err error)) error` |
| `isRetryable` | `func isRetryable(err error) bool`（429/5xx/网络；4xx 不重试） |
| `Estimate` | `func Estimate(s string) int` |
| `EstimateMessages` | `func EstimateMessages(msgs []Message) int`（**标注为估算**，不用于硬裁剪） |

## 6. 会话层 `internal/session/`

| 文件 | 函数 | 签名 |
|---|---|---|
| `store.go` | `NewStore` | `func NewStore(workingDir string) (*Store, error)` |
| | `Create` | `func (s *Store) Create() (*Session, error)`（写 `session_started`） |
| | `Append` | `func (s *Store) Append(sess *Session, ev Event) error`（O_APPEND，赋 seq） |
| | `Load` | `func (s *Store) Load(id string) (*Loaded, error)` |
| | `List` | `func (s *Store) List() ([]Summary, error)` |
| | `Latest` | `func (s *Store) Latest() (Summary, bool, error)` |
| | `PruneExpired` | `func PruneExpired(root string, cutoff time.Time) error` |
| `event.go` | `NewEvent` | `func NewEvent(kind EventKind, payload any) (Event, error)` |
| | `Decode` | `func (e Event) Decode(v any) error` |
| `projection.go` | `BuildTurns` | `func BuildTurns(evs []Event) ([]Turn, error)` |
| `path.go` | `namespaceDirName` | `func namespaceDirName(workingDir string) string`（目录名-sanitized + sha1 前 10 位） |

**关键约束**：`session` 不 import `llm`。投影结果是中立的 `Turn{User, Assistant string; Tools []ToolRecord}`，由 `appstate` 转成 `llm.Message`。常见实现在此处反向依赖 `llm/core`，我们不跟。

## 7. 回合层 `internal/appstate/`

`state.go`（状态，无回合逻辑）：

| 函数 | 签名 | 职责 |
|---|---|---|
| `Deps` | `type Deps struct { Client llm.Client; Tools *tools.Registry; Guard *filesystem.Guard; Perm tools.PermissionRequester; MaxTurns int }` | 显式依赖，不用全局 |
| `New` | `func New(d Deps) *State` | 只存引用 |
| `Messages` | `func (s *State) Messages() []llm.Message` | 返回副本，防外部改写 |
| `Append` | `func (s *State) Append(m llm.Message)` | |
| `Clear` | `func (s *State) Clear()` | 清上下文，不清事件 |

`turn.go`（一回合 + 工具循环，唯一有循环的文件）：

| 函数 | 签名 | 职责 |
|---|---|---|
| `Submit` | `func (s *State) Submit(ctx context.Context, input string) (<-chan llm.StreamEvent, error)` | 起 goroutine，返回事件流 |
| `runTurn` | `func (s *State) runTurn(ctx context.Context, input string, out chan<- llm.StreamEvent)` | 组装 → 请求 → 消费 → 决定是否继续 |
| `buildRequest` | `func (s *State) buildRequest(input string) llm.Request` | 系统提示 + 历史 + 工具 schema |
| `handleEvents` | `func (s *State) handleEvents(ctx context.Context, ch <-chan llm.StreamEvent, out chan<- llm.StreamEvent) (calls []llm.ToolCall, err error)` | 转发 UI、收集工具调用 |
| `executeTools` | `func (s *State) executeTools(ctx context.Context, calls []llm.ToolCall, out chan<- llm.StreamEvent) []llm.Message` | 逐个执行，发 `tool_start`/`tool_end` |
| `executeOne` | `func (s *State) executeOne(ctx context.Context, c llm.ToolCall) (tools.Result, error)` | 查表 → 校验 → Guard → 询问 → 执行 |
| `shouldContinue` | `func (s *State) shouldContinue(turn int, calls []llm.ToolCall) bool` | 轮次上限 25 / 无调用即停 |

**循环只在这里**。`llm` 的每个 provider 只做一次请求，不持有循环。

## 8. 边界层 `internal/agentcore/`

`agentcore.go`（接口，UI 只认它）：

```go
type AgentCore interface {
    Submit(ctx context.Context, input string) (<-chan llm.StreamEvent, error)
    Interrupt()
    Messages() []llm.Message
    Clear()
}
```

`adapter.go`：`New(client llm.Client, r *tools.Registry, g *filesystem.Guard, perm tools.PermissionRequester) AgentCore` + 四个方法（全部转发 `appstate`）。

`setup_tools.go`：把 UI 能力适配成工具需要的窄接口。

| 函数 | 签名 | 职责 |
|---|---|---|
| `SetupTools` | `func SetupTools(deps ToolDeps) *tools.Registry` | 注册 6 个工具，顺序固定 |
| `permissionAdapter` | `type permissionAdapter struct { core AgentCore }` | `RequestPermission` → 调 UI |
| `diffAdapter` | `type diffAdapter struct { core AgentCore }` | `EmitDiff` → 推 UI |

`types.go`：`Mode`、`PermissionRequest`、`Decision` 等跨层类型。

## 9. UI 层 `internal/cli/repl/`

`repl.go` —— Bubble Tea 的四个必备函数 + 一个桥接函数：

| 函数 | 签名 | 职责 |
|---|---|---|
| `Deps` | `type Deps struct { Version string; Model string }`（P2 追加 `Core agentcore.AgentCore`、`Store *session.Store`） | 界面只拿展示所需依赖 |
| `NewModel` | `func NewModel(d Deps) Model` | 只建状态，不做 IO |
| `Init` | `func (m Model) Init() tea.Cmd` | 输入框聚焦；窗口尺寸由框架发 `WindowSizeMsg` |
| `Update` | `func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd)` | **唯一**改状态的地方 |
| `View` | `func (m Model) View() tea.View` | v2 返回 `tea.View` 而非 `string`；实际内容由 `render() string` 产出，便于单测 |
| `Run` | `func Run(ctx context.Context, d Deps) error` | 非 TTY 直接返回中文错误；否则建 `tea.Program` 并阻塞 |
| `isInteractive` | `func isInteractive(f *os.File) bool` | 字符设备 **且** 不是空设备（`os.SameFile` 排除 `/dev/null`、`NUL`） |
| `waitEvent` | `func waitEvent(ch <-chan llm.StreamEvent) tea.Cmd` | **channel → tea.Msg 的桥**，UI 与 agent 解耦的关键 |

子包各自的入口（只列一个代表函数，避免过度规定）：

| 子包 | 入口 |
|---|---|
| `stream` | `func Render(ev llm.StreamEvent, st *State) []Segment` |
| `output` | `func (o *Output) Push(s Segment)`、`func (o *Output) Render(width int) string` |
| `markdown` | `func Render(src string, width int, th theme.Theme) string` |
| `permissions` | `func (p *Prompt) Request(req agentcore.PermissionRequest) tea.Cmd` |
| `widgets` | 每个组件 `NewXxx(...) Model` + `Update` + `View` |
| `commands` | `func Register(c Command)`、`func Lookup(name string) (Command, bool)` |

## 10. 回收 `internal/cleanup/cleanup.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Run` | `func Run() error` | 依次回收 artifact(7d) / logs(14d) / sessions(14d) / history(1000 条) |
| `pruneExpired` | `func pruneExpired(dir string, cutoff time.Time) (removed int, err error)` | 按 mtime，跳过符号链接 |
| `pruneHistory` | `func pruneHistory(path string, keep int) error` | 保留最后 N 条 |

失败必须**只记日志**：回收是尽力而为，不能让启动失败。

## 11. 反面清单（出现即打回）

| 反模式 | 为什么 |
|---|---|
| `init()` 里读文件/发请求 | 不可控、不可测、启动顺序隐式 |
| `NewXxx` 里发网络请求 | 构造器应瞬时完成，"连不上模型"不该发生在 `New` |
| 工具里出现第二个 `Execute` 变体（`ExecuteWithRetry`…） | 契约只能有一套 |
| `llm` 里出现循环调用工具 | 循环只在 `appstate/turn.go`（ADR-001） |
| `session` import `llm` | 破坏单向依赖，用中立 `Turn` 类型 |
| 导出只被用一次的 helper（`FormatPathInternal`） | 私有即可，减少表面积 |
| 名为 `Manager`/`Helper`/`Util` 的类型 | 职责无法命名说明它没有职责 |
| 英文错误文案（`failed to read file`） | 规范要求中文，且要带"下一步"（CODING-STANDARDS §13） |
| 工具里 `fmt.Errorf("...: %v", err)` | 用 `%w` 保留错误链，否则 `errors.Is` 失效 |
| 在 `Update` 之外改 UI 状态 | Bubble Tea 单写者原则 |

## 13. 写完一个文件的检查清单

- [ ] `const`/`type`/构造器/公开方法/私有函数 顺序正确
- [ ] 构造器不失败就不返回 `error`；会失败就在注释里写清失败条件
- [ ] 公开方法里没有超过 20 行的连续逻辑（超了拆私有纯函数）
- [ ] 每个私有纯函数都有单测；编排函数有集成测试
- [ ] 没有新增导出标识符却没人用
- [ ] 文件里没有第二个"入口函数"（`Run` 与 `Main` 只能有一个）

## 14. 变更记录

| 日期 | 变更 |
|---|---|
| 2026-10-29 | 初稿：函数级设计、启动顺序、动词表、反面清单、与常见实现的函数对应 |
