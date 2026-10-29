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
| `Env` | `type Env struct { Model string; Core agentcore.AgentCore; Permission *agentcore.PermissionPrompt; History []agentcore.Turn; RestoredNote string; ContextBase, ContextWindow int }` | 进入界面前要准备好的东西：模型名、回合引擎、权限通路、恢复出来的历史（屏幕用；上下文由 `Core.Restore` 自己恢复）、`/context` 的两个常量 |
| `Options` | `type Options struct { Version, Commit string; Stdout, Stderr io.Writer; BuildEnv func(newSession bool) (Env, error); StartREPL func(model string) error }` | 注入版本、输出与环境构造函数；`BuildEnv` 的参数是 `--new`（P4.2 起） |
| `NewRootCommand` | `func NewRootCommand(opts Options) *cobra.Command` | 建 root + `version` + `run` 子命令、注册 `--new`；同时完成汉化 |
| `runREPL` | `func runREPL(opts Options, newSession bool) error` | 先准备环境（失败则不进界面、退出 1），再把模型名、引擎、历史交给界面 |
| `buildEnv` | `func buildEnv(opts Options, newSession bool) (Env, error)` | 调注入的构造函数；未注入视为"没有模型、没有引擎"，只测命令树时不必造配置 |
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
| | `Resolve` | `func (c Config) Resolve(reg *providers.Registry, providerID, modelID string) (Resolved, error)`：显式参数 > 配置 `active_*` > 注册表默认；**只指定供应商时退到该供应商的第一个模型**（见下方"默认模型回退"） |
| `key_resolver.go` | `ResolveKey` | `func ResolveKey(r Resolved) (string, error)`：按 `APIKeyEnv` 读环境变量；`APIKeyEnv` 为空表示不需要密钥 |

> 与早期计划的差异（2026-10-29）：原计划拆 `loader.go`，实际并入 `config.go`——
> 加载只有两个函数，单独成文件不增加可读性，还把单元推到 4 个文件（超过上限）。

**默认模型回退**（2026-10-29 修）：`defaults.model` 属于默认供应商，只在供应商也落回默认那家时才采用；
用户把 `active_provider` 改成别家而没写 `active_model` 时，退到该供应商的第一个模型（`Registry.FirstModel`）。
否则会拿 `openai/gpt-4o-mini` 去配 `deepseek`，报"模型不属于供应商"——用户只改了一项，却要为两项都对不上买单。

**错误契约**：缺 key 返回的错误必须包含供应商名与"该设哪个环境变量"，且**绝不回显 key**。

### `internal/providers/registry.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Load` | `func Load() (*Registry, error)` | `//go:embed registry.yaml` → 解析 + 校验 |
| `LoadFile` | `func LoadFile(r io.Reader) (*Registry, error)` | 任意来源解析，供测试与将来的自定义注册表；`Load` 也走它 |
| `Find` | `func (r *Registry) Find(providerID, modelID string) (Provider, Model, bool)` | 找不到返回 `false`；供应商命中但模型没命中时**仍返回该 Provider**，调用方据此区分"供应商错"还是"模型错" |
| `GetModelContextWindow` | `func (r *Registry) GetModelContextWindow(providerID, modelID string) (int, bool)` | 第二个返回值是**是否已知**：没填或非正数一律报未知，绝不编默认值（界面上把未知当 0% 比不显示更误导） |
| `Default` | `func (r *Registry) Default() (providerID, modelID string)` | 首次运行的兜底 |
| `FirstModel` | `func (r *Registry) FirstModel(providerID string) string` | 该供应商的第一个模型；供应商不存在返回空串。供"只指定了供应商"的场合使用，避免沿用别家的 `defaults.model` |
| `parse` / `validate` | 私有 | YAML 解析与自洽性校验（id 唯一、`context_window > 0`、默认值必须存在） |

数据结构（**加法变更，2026-10-29**）：`Provider` 增加 `BaseURL` 与 `APIKeyEnv`。
理由：这两个是供应商固有能力，若放进用户配置，每个用户都要为同一个端点重复填写；
用户配置只负责"选择与覆盖"。

### `internal/filesystem/`

`guard.go`：

| 元素 | 签名 | 职责 |
|---|---|---|
| `Permission` | `Denied` / `Pending` / `Granted`（`String()` 给弹窗用） | 三态；**零值是 `Denied`**：新增分支忘赋值时倾向于拒绝 |
| `Op` | `OpRead` / `OpWrite` / `OpExec` | 用类型而非字符串：写错就编译不过，不会静默放行 |
| `Reason` | `type Reason struct { Code ReasonCode; Message string }` | `Code` 供 UI 分支，`Message`（中文）直接进弹窗 |
| `NewGuard` | `func NewGuard(workingDir string, git *GitAwareness) *Guard` | 预计算工作目录、家目录、系统黑名单、`~/.sycode/bash`；**不返回 error**（取不到路径就退化为"工作目录为空"，一律按外处理） |
| `Check` | `func (g *Guard) Check(path string, op Op) (Permission, Reason)` | **纯判定**；顺序：artifact → 系统路径 → 家目录隐藏项 → 工作目录内（`.git` → gitignore → read/write）→ 目录外 |
| `Resolve` | `func (g *Guard) Resolve(path string) (string, error)` | 展开 `~` → 相对工作目录绝对化 → `Clean` → 尽力解析符号链接 |
| `IsBlocked` | `func (g *Guard) IsBlocked(resolved string) bool` | 系统目录 + 家目录隐藏项；**不含** artifact（它可读） |
| `WorkDir` | `func (g *Guard) WorkDir() string` | 已绝对化的工作目录，供工具拼提示 |
| `vcsEntry` | `func (g *Guard) vcsEntry(resolved string) string` | `.git`/`.svn`/`.hg`：里面有远端凭证与钩子脚本 |
| `sensitiveHomeEntry` | `func (g *Guard) sensitiveHomeEntry(resolved string) (string, bool)` | "隐藏即敏感"：`~` 下以 `.` 开头的项（`.ssh`、`.aws`、`.sycode/configs.json`…） |
| `defaultBlockedPaths` | `func defaultBlockedPaths() []string` | 平台相关；Windows 从 `SystemRoot`/`ProgramFiles` 取实际盘符，不硬编码 `C:` |
| `bashOutputDir` | `func bashOutputDir() (string, error)` | `~/.sycode/bash`；`NewGuard` 记下它，`IsBlocked` 对它开例外 |
| `isUnder` | `func isUnder(parent, child string) bool` | 用 `filepath.Rel` 而非字符串前缀：否则 `work2` 会被当成 `work` 的子目录 |
| `expandHome` / `absClean` | 私有辅助 | `~` 展开；绝对化失败返回空串（按"未知"从严处理） |
| `resolveSymlinks` | `func resolveSymlinks(abs string) string` | 目标存在则整体解析；不存在则解析最近的已存在祖先再拼回剩余段（新建文件的常态） |
| `evalSymlinks` | `var evalSymlinks = filepath.EvalSymlinks` | **仅为可测**：Windows 默认不给建符号链接，测试替换它验证穿越判定；生产不要改 |

`gitawareness.go`：

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewGitAwareness` | `func NewGitAwareness(root string) (*GitAwareness, error)` | 递归载入 `.gitignore`；跳过 `.git`，**不下沉到已被忽略的目录**（规则在 git 里也不生效，还能避开 node_modules） |
| `Root` | `func (g *GitAwareness) Root() string` | 规则根目录（已绝对化） |
| `IsIgnored` | `func (g *GitAwareness) IsIgnored(path string) bool` | 根目录自身与根外路径一律 `false`；`isDir` 由 stat 决定，目录模式 `dist/` 才生效 |
| `collectPatterns` | 私有递归 | 父目录规则先于子目录，满足 `NewMatcher` 的"优先级递增"要求 |
| `parseIgnoreFile` | 私有 | 去行尾空格、跳过空行与 `#` 注释 |

**红线**：`Check` 不允许有副作用；任何文件读写必须 `Check` 之后才执行，`Pending` 还要走"询问 → 允许"。

**已知限制**：`.gitignore` 的 `**` 与 `!` 取反依赖 go-git 的实现（选型表批准它的理由）；`.git/info/exclude` 未载入。

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

`registry.go`：

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewRegistry` | `func NewRegistry() *Registry` | 空表 + 顺序切片（导出顺序稳定） |
| `Register` | `func (r *Registry) Register(t Tool) error` | nil / 空名 / 重名一律报错，不覆盖不忽略 |
| `Get` | `func (r *Registry) Get(name string) (Tool, bool)` | |
| `All` | `func (r *Registry) All() []Tool` | 按注册顺序返回副本 |
| `Len` | `func (r *Registry) Len() int` | |
| `Schemas` | `func (r *Registry) Schemas() []Schema` | 按注册顺序导出；`Input` 浅拷贝，防下游改写工具自己的 schema 变量 |
| `cloneSchema` | 私有 | 浅拷贝一层 properties；不做 JSON 往返（每回合都跑，代价大于收益） |

补充类型（2026-10-29 落地时补齐，均在本包内定义）：

| 元素 | 签名 | 说明 |
|---|---|---|
| `Display` | `type Display struct { Kind DisplayKind; Title string; Lines []string }` | 给 UI 的结构化展示；`DisplayDiff`/`DisplayList`。与 `Content` 分开：人要看高亮，模型只要原文 |
| `PermissionRequest` | `type PermissionRequest struct { Tool, Op, Path, Detail string }` | 字段刻意少——弹窗放不下整个 Input |
| `Decision` | `DecisionDeny`(零值) / `DecisionAllowOnce` / `DecisionAllowSession` + `Allowed()` | 零值是拒绝：没拿到回答不能默认放行 |
| `maxInlineBytes` | `const = 32 * 1024` | 回给模型的单条结果上限 |
| `clip` | `func clip(content string) (string, bool)` | 按字节截断、按 rune 收尾、尽量切在行边界；**落盘由各工具自己做**（超限提示因工具而异） |
| `lastNewline` | 私有 | 找最后一个换行，供 `clip` 退到行边界 |

### `read_file.go`（其余工具同构）

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewReadFileTool` | `func NewReadFileTool(guard *filesystem.Guard, perm PermissionRequester) *ReadFileTool` | 依赖注入，无全局 |
| `Name` | `func (t *ReadFileTool) Name() string` | 常量 `ReadFileToolName` |
| `Description` | `func (t *ReadFileTool) Description() string` | 写给模型：何时用、不适用什么 |
| `InputSchema` | `func (t *ReadFileTool) InputSchema() map[string]any` | 只列 `path`/`offset`/`limit` |
| `ValidateInput` | `func (t *ReadFileTool) ValidateInput(_ context.Context, in Input) error` | 缺参/类型错 → `参数错误：…`（中文，保留英文字段名） |
| `Execute` | `func (t *ReadFileTool) Execute(ctx context.Context, in Input) (Result, error)` | 编排：校验 → 询问权限 → **Resolve 成绝对路径** → 读 → 截断 → 组装 |
| `readFileRange` | `func readFileRange(path string, offset, limit int) (content string, total, read int, truncated bool, err error)` | 私有纯逻辑，独立可测；`offset` 从 1 开始 |
| `isBinary` | `func isBinary(data []byte) bool` | 前 8KiB 找 NUL：文本里不会有 0x00 |
| `splitLines` / `numberedLines` | 私有 | 切行保留空行（行号需与编辑器一致）；行号只进 `Display`，不进 `Content` |
| `humanBytes` | 私有 | 错误提示里的"16.0 MiB"这类可读单位 |

常量：`defaultReadLimit = 2000`、`maxReadLimit = 5000`、`maxReadBytes = 16MiB`、`binaryProbeBytes = 8KiB`。

**两个容易踩的点**：

1. **`Content` 不带行号**。模型要拿原文去 `edit_file` 精确替换；带行号会诱发它把 `   12│ ` 也写进 `old_string`。行号只放在 `Display.Lines` 里给人看。
2. **读之前必须 `guard.Resolve(path)`**。模型给的相对路径是相对工作目录的，而进程 cwd 未必是工作目录——直接 `os.ReadFile(相对路径)` 会读错文件（实测：测试里 cwd 是包目录，直接读就"文件不存在"）。

### `input.go`（所有工具共用的参数解析）

| 函数 | 签名 | 职责 |
|---|---|---|
| `requireString` | `func requireString(in Input, field string) (string, error)` | 缺参/类型错/空串 → `参数错误：…` + 示例 JSON |
| `optionalInt` | `func optionalInt(in Input, field string, def int) (int, error)` | JSON 数字是 `float64`，三种数值类型都认；`1.5` 拒绝而不是截断 |
| `optionalString` | `func optionalString(in Input, field string) (string, error)` | 可选字符串 |
| `typeName` / `exampleFor` | 私有 | 把 Go 类型名换成 `string`/`number` 这类模型看得懂的说法；给常见字段备好示例片段 |

不用裸类型断言：模型给的类型经常不对，`in["path"].(string)` 会 panic，而 `Execute` 永不 panic 是硬约束。

### `permission.go`（工具 → 权限的唯一通道）

| 元素 | 签名 | 职责 |
|---|---|---|
| `access` | `type access struct { guard, perm, tool, path, op, detail }` | 一次"要碰某个路径"的请求；用结构体而不是七个位置参数 |
| `askAccess` | `func askAccess(ctx context.Context, req access) error` | 三态 → 文案的唯一映射点：`Granted` 放行、`Denied` → `权限不足：…（判定依据）`、`Pending` → 问用户，被拒 → `权限不足：用户拒绝了…不要重试同一路径` |
| `opName` | 私有 | `read/write/exec` → 读取/修改/执行 |

**为什么要收口**：三态到文案的映射散到各工具里，"被拒绝"就会在某些工具里说成"执行失败"，模型会以为是临时故障而反复重试。拒绝的文案必须像"决定"，不像"故障"。

**每个工具必须恰好有这 5 个方法 + 构造器**；不允许加 `BeforeExecute`、`PostHook` 这类钩子（要加就走 ADR）。

### `pattern.go`（glob 匹配）

模式不手写回溯匹配，而是**翻译成正则**：段级回溯要同时处理 `**` 跨层、`*` 不跨层、字符集与转义，
边界记不住；翻译是单向的，正确性靠表驱动测试压住。

| 函数 | 签名 | 职责 |
|---|---|---|
| `compileGlob` | `func compileGlob(pattern string) (*regexp.Regexp, error)` | `*`→`[^/]*`、`?`→`[^/]`、`**/`→`(?:.*/)?`（零层也匹配）、尾部 `/**`→`(?:/.*)?`、`[...]`→正则字符集；Windows 加 `(?i)`；`\` 先规整成 `/` |
| `translateClass` | `func translateClass(class string) (string, error)` | glob 的 `[!abc]` → 正则的 `[^abc]`；`\`/`]`/`^` 转义 |
| `matchPattern` | `func matchPattern(pattern, path string) bool` | 便捷包装，仅测试与小场景使用 |

**已知限制**：不支持 `{a,b}` 花括号展开；`[a-z]` 区间按字节交给正则，多字节字符集不要用。

### `glob.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewGlobTool` | `func NewGlobTool(guard *filesystem.Guard, perm PermissionRequester) *GlobTool` | |
| `Execute` | 校验 → 起点问一次权限 → `WalkDir` → 匹配 → 限量 | **起点只问一次**：用户已为"在这个目录里找"点过头，再为每个文件弹窗会把人逼疯 |
| `walk` | `func (t *GlobTool) walk(root, pattern string, limit int) (matches []string, visited int, truncated bool)` | 跳过 `.git/.svn/.hg`；被忽略的目录 `SkipDir`；用 `WalkDir` 的字典序保证输出稳定 |

常量：`defaultGlobLimit = 200`、`maxGlobLimit = 2000`、`maxGlobVisited = 50000`。
输出是相对起点的路径（用 `/`），空结果会明确说"没有匹配"并给出放宽建议。

### `grep.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewGrepTool` | `func NewGrepTool(guard *filesystem.Guard, perm PermissionRequester) *GrepTool` | |
| `Execute` | 校验（含正则编译）→ 起点问权限 → 遍历 → 汇总 | 命中格式 `路径:行号: 内容` |
| `search` | 目录或单文件都走这一条 | 单文件起点把基准挪到父目录，结果里显示文件名而不是 `.` |
| `searchFile` | 逐行匹配 | 二进制/超 4MiB/读不了的文件静默跳过（噪声不是错误） |
| `readLimitedLine` | `func readLimitedLine(reader *bufio.Reader) (line string, got bool, err error)` | 单行上限 256KiB；超长时交回前缀（仍可匹配）并丢弃剩余部分，否则行号会错位 |

常量：`defaultGrepLimit = 100`、`maxGrepLimit = 500`、`maxGrepFileBytes = 4MiB`、`grepLineBytes = 256KiB`。
`ctx` 取消 → `超时：搜索被取消`（不让 UI 停在一个永远转圈的搜索上）。

### `write_file.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `NewWriteFileTool` | `func NewWriteFileTool(guard *filesystem.Guard, perm PermissionRequester) *WriteFileTool` | |
| `Execute` | 校验 → 问写权限 → Resolve → 读现状 → 写 → 回读校验 | **被拒绝时一个字节都不动**（先问再做，顺序不能反） |
| `readExisting` | `func readExisting(path string) (content string, existed bool, err error)` | 不存在返回 `existed=false`；目录、二进制文件拒绝覆盖（覆盖二进制几乎一定是误操作） |
| `writeFileAtomic` | `func writeFileAtomic(path string, data []byte) error` | 同目录临时文件 → `Write` → `Sync` → `Chmod 0644` → `Rename`。**同目录**是必须的：跨文件系统 rename 不原子 |
| `writeAndVerify` | `func writeAndVerify(path, content, displayPath string) error` | 原子写 + 回读比对：磁盘满、被别的进程截断不会从 `Write` 返回，用户却会拿到"写着成功、其实是坏的"文件 |
| `renderWriteSummary` | 私有 | 给模型的一句话结果（新建/覆盖、字节、行数、diff 摘要） |

**内容未变化时不写盘**：改一次 mtime 会让 watch 与构建白跑一遍。

### `edit_file.go` 的额外约定

`Execute` 内部必须走三段：`locateMatches(content, old) []int`（0 或 >1 直接失败并返回候选行号）→ `applyReplace(...)` → `buildDiff(old, new) []EditDiffLine`。三个都是私有纯函数，各有单测。

| 函数 | 签名 | 职责 |
|---|---|---|
| `locateMatches` | `func locateMatches(content, old string) []int` | 返回每次出现的起始行号（1 起）；超过 `maxMatchReport` 上报上限时末尾追加 `-1` 表示"还有更多" |
| `applyReplace` | `func applyReplace(content, old, new string) string` | 只替换第一处（调用方已确认唯一） |
| `summarizeChange` / `joinLines` | 私有 | 弹窗里的"3 行 → 5 行"；候选行号拼成"1、2、3、…" |

**为什么只做唯一匹配**：模型拿着不唯一的 `old_string` 去改，改错地方的代价远大于让它重写一次。
0 处的错误提示必须指向下一步（"先用 grep 确认原文"），否则模型只会原样重试。

### `diff.go`（展示用差异）

| 函数 | 签名 | 职责 |
|---|---|---|
| `buildDiff` | `func buildDiff(oldText, newText string) []EditDiffLine` | 掐掉公共前后缀，中间整块标 `-`/`+`；保留一行上下文 |
| `formatDiff` | `func formatDiff(diff []EditDiffLine) []string` | 转成 `+`/`-`/空格前缀的行；超过 `maxDiffLines=200` 截断并注明 |
| `diffSummary` | `func diffSummary(diff []EditDiffLine) string` | `+2 / -1` 这样的一句话摘要 |

**没有引 diff 库，也没有写 LCS**：这里只有"改动前后"一种场景，掐前后缀足够，且它绝不会骗人
（不会把改过的说成没改）。真需要细粒度对齐时再换，换的成本被 `Display` 挡住了。

### `bash.go`（命令执行）

| 函数 | 签名 | 职责 |
|---|---|---|
| `ResolveShell` | `func ResolveShell() (Shell, error)` | ADR-006 顺序：`SYCODE_SHELL` → bash → PS → cmd；**导出**是因为 `main` 拼系统提示时要用同一份结论 |
| `Shell.command` | `func (s Shell) command(ctx context.Context, command string) *exec.Cmd` | bash 用 `-c`；PS 用 `-NoProfile -NonInteractive -Command`（别让用户 profile 改输出编码）；cmd 用 `/c` |
| `NewBashTool` | `func NewBashTool(guard *filesystem.Guard, perm PermissionRequester) (*BashTool, error)` | 唯一会失败的构造器：机器上没有可用 shell |
| `Execute` | 校验 → 问 `OpExec` 权限（`Detail` 带命令本身，危险命令加 ⚠ 前缀）→ 执行 → 截断/落盘 | **非 0 退出码不是错误**：构建失败、测试不通过都是信息，写进 `Content` |
| `run` | `func (t *BashTool) run(ctx, command string, timeout time.Duration) (string, int, bool)` | `WithTimeout` + 独立 goroutine 等 `Wait`；超时杀整棵树 |
| `exitCodeOf` | 私有 | 从 `*exec.ExitError` 取退出码 |
| `isDangerous` | `func isDangerous(command string) bool` | 模式表（`rm -r/-f`、`git reset --hard`、管道下载即执行、`mkfs`、`dd`、`sudo`、fork 炸弹）；**只影响提示，不改变权限判定** |
| `truncateOutput` | `func truncateOutput(output string) (string, string)` | 超 32KiB 落盘到 `filesystem.BashOutputDir()`，返回截断文本与路径 |
| `oneLine` / `tailString` | 私有 | 弹窗里的单行命令；错误里附的输出尾部（按 rune 收尾） |
| `limitedBuffer` | 并发安全的限容缓冲 | stdout/stderr 两个 goroutine 同时写；超 4MiB 丢弃并记数（进程可能吐几个 GB） |

**平台相关**（`proc_windows.go` / `proc_unix.go`，build tag 分开）：

| 函数 | Windows | Unix |
|---|---|---|
| `prepareCommand` | 空实现 | `SysProcAttr{Setpgid: true}`，让命令自成进程组 |
| `killTree` | `taskkill /T /F /PID`（只杀 shell 会留下 `go build` 的孤儿子进程） | `kill(-pid, SIGKILL)` 杀整组 |

**两个必须记住的坑**：

1. **`cmd.WaitDelay` 不能省**。子进程继承输出管道且不肯退出时，`Wait` 会一直等管道关闭，
   超时形同虚设——实测 1 秒超时的命令被拖到命令自己结束（30 秒）。设 `WaitDelay = 3s` 后
   超时才真正生效（用例从 30s 降到 4s）。
2. **`truncateOutput` 不自己拼路径**：从 `filesystem.BashOutputDir()` 拿。两处拼接一旦不一致，
   就会出现"写进去了但 Guard 不允许读"的死角。

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
    Tools           []ToolSchema     // llm 自有类型；appstate 负责从 tools.Schema 转换
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
| `errEvent` | `func errEvent(err error) StreamEvent` | 终态错误；`nil` 换成兜底说明，保证 `Err != nil` |
| `doneEvent` | `func doneEvent(usage *Usage) StreamEvent` | 终态完成；`usage` 可为 `nil` |
| `IsTerminal` | `func (e StreamEvent) IsTerminal() bool` | 终态判据（`done`/`error`），消费方据此收尾，不看 channel 关闭时机 |

### `systemprompt.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Env` | `type Env struct { OS, Arch, WorkDir, Shell, Model string; Today time.Time }` | 环境事实；全由调用方注入，`Build` 因此可测 |
| `Build` | `func Build(env Env) string` | 按固定段拼接：身份 → 环境 → 项目指令(`AGENTS.md`)。**工具约定段随 P3 加入**——现在写会让模型以为有工具却调不到 |
| `identitySection` | `func identitySection() string` | 身份与工作方式；中文（界面/错误/工具描述都是中文，保持一致） |
| `envSection` | `func envSection(env Env) string` | 含 `Current shell:`（ADR-006 要求的原文写法）；缺值不写该行，全缺则不输出整段 |
| `archSuffix` | `func archSuffix(arch string) string` | 拼 `/amd64`；空值返回空串 |
| `projectSection` | `func projectSection(workDir string) string` | 读 `AGENTS.md`；读不到（不存在/无权限/是目录）一律当"没有项目指令"，不阻断对话 |
| `truncateUTF8` | `func truncateUTF8(s string, limit int) (string, bool)` | 按字节截断但**不切断多字节字符**，否则中文会切出 U+FFFD |

阈值集中为常量：`maxProjectInstructions = 8 * 1024`、`projectInstructionsFile = "AGENTS.md"`。

### `openai.go`

基于 `openai/openai-go/v3`（选型表已批准）：SDK 负责 HTTP/SSE 解码，我们只做参数转换与事件翻译。

| 函数 | 签名 | 职责 |
|---|---|---|
| `OpenAIConfig` | `type OpenAIConfig struct { BaseURL, APIKey, Model string }` | 构造输入；`APIKey` 可为空（本地端点） |
| `OpenAI` | `type OpenAI struct { client oai.Client; model string }` | |
| `NewOpenAI` | `func NewOpenAI(cfg OpenAIConfig) (*OpenAI, error)` | 校验模型名与端点；显式传 key、**关掉 SDK 自带重试**（重试归 2.6，要发 `EventRetry`）；**不发请求** |
| `StreamChat` | `func (c *OpenAI) StreamChat(ctx context.Context, req Request) (<-chan StreamEvent, error)` | 起 goroutine，返回只读 channel；HTTP 层失败也走事件流 |
| `buildParams` | `func (c *OpenAI) buildParams(req Request) (oai.ChatCompletionNewParams, error)` | 纯转换；只在"请求构造不出来"（无模型/无消息）时失败 |
| `run` | `func (c *OpenAI) run(ctx context.Context, params oai.ChatCompletionNewParams, out chan<- StreamEvent)` | **重试的唯一位置**：`Do` 包住"请求 + 解析"，`emitted` 为真时用 `partialError` 阻止重试；成功后发 `done`，失败发 `error` |
| `pumpOutcome` | `type pumpOutcome struct{ usage *Usage; emitted bool }` | 一次解析的副产物：用量，以及"是否已给用户看过内容" |
| `pump` | `func (c *OpenAI) pump(ctx, stream *ssestream.Stream[oai.ChatCompletionChunk], out chan<- StreamEvent) (pumpOutcome, error)` | 分片 → 增量/工具事件（**不发终态**）；返回错误交给 `run` 决定重试；**含截断识别**（见下） |
| `emit` / `emitCalls` | `func emit(ctx, out, ev) bool` / `func emitCalls(ctx, out, calls []*ToolCall) bool` | 带 ctx 取消的发送；返回 false 表示下游已放弃 |
| `toOpenAIMessages` | `func toOpenAIMessages(msgs []Message) []oai.ChatCompletionMessageParamUnion` | 纯转换；未知角色退回 user 而不是丢弃 |
| `toOpenAIToolCall` | `func toOpenAIToolCall(call ToolCall) oai.ChatCompletionMessageToolCallUnionParam` | assistant 消息里的调用回放 |
| `toOpenAITools` | `func toOpenAITools(schemas []ToolSchema) []oai.ChatCompletionToolUnionParam` | 纯转换；`Input` 为空时给空对象 schema |
| `toolCallBuffer` | `merge(delta) *ToolCall` / `drain() []*ToolCall` | 按 `index` 归并分片工具调用；`start` 发快照、`end` 发完整参数 |

**channel 契约**：`StreamChat` 返回的 channel 由**发送方关闭**；`EventDone`/`EventError` 是最后一个事件。

**不再有 `parseSSE`**（2026-10-29）：SSE 解码在 SDK 的 `packages/ssestream` 里，自己再写一份只会重复。
代价是 `[DONE]` 对我们不可见，因此截断识别改用结束信号：
**收到过非空 `finish_reason` 或 `usage` 才算正常结束**，否则发 `EventError`（"连接在模型给出结束信号之前中断"）。
实测依据：连到一半断开时 SDK 的 `stream.Err()` 仍为 `nil`（半截事件被静默丢弃），不额外判断就会把半句话当完整回答。

### `errors.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Translate` | `func Translate(err error) error` | 把 provider/网络的**英文原始错误**转成中文；顺序为 已翻译 → ctx 取消 → 超时 → `*oai.Error` → `*url.Error` → 兜底 `%w` |
| `httpStatusError` | `type httpStatusError struct { Status int; Code, Body string; cause error }` | 结构化 HTTP 错误；`Unwrap` 保留原始链，`Error()` 产出中文 |
| `statusHint` | `func statusHint(status int) string` | 按状态码给"下一步该干什么"：401/403 查密钥、404 查 base_url/model、429 稍后重试、5xx 服务端故障 |

`Translate` 只处理原始错误；已经中文化的错误原样返回（`errors.As` 判定后直接 return），避免"执行失败：执行失败："。

### `retry.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Do` | `func Do(ctx context.Context, attempts int, fn func(context.Context) error, onRetry func(attempt int, err error)) error` | 执行 fn，对可重试错误按指数退避重试；`attempts` 含首次；`onRetry` 的 `attempt` 是**刚失败的那一次**（从 1 开始）；返回最后一次错误 |
| `isRetryable` | `func isRetryable(err error) bool` | 429/5xx/连接失败/流截断可重试；4xx、取消、超时、未知错误不重试；`partialError` 一律不重试 |
| `retryableStatus` | `func retryableStatus(status int) bool` | `429` 或 `>=500`；给 `httpStatusError` 与 `*oai.Error` 共用 |
| `backoffDelay` | `func backoffDelay(attempt int) time.Duration` | base 翻倍 + 最多 50% 抖动，**加抖动后也不越过 `maxBackoff`**；首次不加抖动 |
| `sleep` | `func sleep(ctx context.Context, d time.Duration) error` | 可被 ctx 打断的等待（用 Timer，避免 `time.After` 的定时器残留） |
| `partialError` | `type partialError struct{ err error }` | 标记"已经给用户看过内容之后才失败"：包住真实错误（`Unwrap` 保留链、`Translate` 照常翻译），但让 `isRetryable` 返回 false |

常量：`maxRetries = 3`（PRD F-LLM-5）、`maxBackoff = 30s`。`backoffBase` 是**变量**，只为让测试压到毫秒级，生产不要改。

**为什么"吐字后不重试"要单独一个类型**：流式回答无法撤回，重试会让用户看到重复内容。
判定点放在 `openai.run`——它是唯一知道"这次尝试有没有 `emitted`"的地方（见 `pumpOutcome`）。

### `estimate.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `EstimateTokens` | `func EstimateTokens(text string) int` | 字节/3 粗估，空串 0、非空至少 1 |
| `EstimateJSON` | `func EstimateJSON(v any) int` | 序列化后按同一口径估；序列化失败记 0（展示用的数字不该让人报错） |

口径是**字节/3**：中文一个字 3 字节 → 一个字约一 token，接近主流分词器；英文 4 字节 1 token → 高估约四分之一。
高估比低估安全，它只会让"快满了"的提醒来得早一点。

**估算只用于给人看的占用提示，不参与请求构造**（硬裁剪由 provider 决定）。消费方：`/context`（P4.4）；
P6 的压缩阈值也会用它，届时同口径。常见实现做法一致（`chars/3 heuristic`，并在界面上注明是估的）。

## 6. 会话层 `internal/session/`

| 文件 | 函数 | 签名 |
|---|---|---|
| `store.go` | `NewStore` | `func NewStore(workingDir string) (*Store, error)`（只算路径，不建目录） |
| | `Create` | `func (s *Store) Create() (*Session, error)`（建目录 + 写 `session_started`） |
| | `Append` | `func (s *Store) Append(sess *Session, ev Event) error`（`O_APPEND`，`Seq` 与零值 `TS` 在这里补） |
| | `Open` | `func (s *Store) Open(id string) (*Session, error)`（重开已有会话，序号从最后一条有效事件续上） |
| | `Load` | `func (s *Store) Load(id string) (*Loaded, error)` |
| | `List` | `func (s *Store) List() ([]Summary, error)`（扫目录，按 `Updated` 倒序） |
| | `Latest` | `func (s *Store) Latest() (Summary, bool, error)` |
| | `readEvents` | `func readEvents(path string) ([]Event, int, error)`（逐行读，坏行只计数） |
| | `summarize` / `shortenTitle` | `func summarize(id string, events []Event, corrupt int) Summary` / `func shortenTitle(text string) string` |
| | `ensureTrailingNewline` | `func ensureTrailingNewline(file *os.File) error`（上一进程死在半行时先补换行） |
| `event.go` | `NewEvent` | `func NewEvent(kind EventKind, payload any) (Event, error)`（`Seq` 留 0） |
| | `Decode` | `func (e Event) Decode(v any) error` |
| `path.go` | `namespaceDirName` | `func namespaceDirName(workingDir string) string`（目录名 sanitize 到 32 字符 + sha1 前 10 位） |
| | `normalizeWorkDir` | `func normalizeWorkDir(dir string) string`（绝对化 + Clean；Windows 折小写） |
| `projection.go` | `BuildTurns` | `func BuildTurns(events []Event) []Turn`（`user_message` 开轮、其后 `assistant_turn` 填入；孤立回答单独成轮） |

**关键约束**：`session` 不 import `llm`（`cli/cmd/deps_test.go` 会拦）。投影结果是中立的
`Turn{User, Assistant string; Tools []ToolRecord}`，由 `appstate` 转成 `llm.Message`。
常见实现在此处反向依赖 `llm/core`，我们不跟。

**2026-10-29 实施后调整**：
- `PruneExpired` **从会话层移走**：`cleanup` 已有通用的 `pruneExpired(dir, cutoff)`，会话回收等 4.2 接入时复用同一套逻辑
- **不写 `index.json`**：`List` 靠扫目录 + 读会话文件得出摘要。两处维护索引必然不一致，而会话文件本来就不大
- `Session` **不持有文件句柄**：每次 `Append` 打开-写入-关闭。追加频率是"一条输入一次"，开销可忽略，换来不必管句柄生命周期
- `SessionStarted` 只存 `work_dir`：版本与模型属于"这次运行"而不是"这段对话"，写进去会让恢复出的旧会话看起来用错了模型
- `BuildTurns` **没有 error 返回值**（原设计有）：坏载荷按"跳过这条事件"处理，与 `readEvents` 的容错口径一致。
  返回一个永远为 nil 的错误等于给调用方留一条不可能走的分支；真要严格，`readEvents` 那一层就该拦住

## 7. 回合层 `internal/appstate/`

`state.go`（状态，无回合逻辑）：

| 函数 | 签名 | 职责 |
|---|---|---|
| `Deps` | `type Deps struct { Client llm.Client; Tools *tools.Registry; Guard *filesystem.Guard; Perm tools.PermissionRequester; Store *session.Store; Session *session.Session; MaxTurns int }` | 显式依赖，不用全局；会话库为空就不落盘 |
| `record` | `func (s *State) record(kind session.EventKind, payload any)` | 追加一条会话事件；**失败只记日志**（存不下历史不该中断对话） |
| `Restore` | `func (s *State) Restore(turns []session.Turn)` | 把恢复出来的回合灌进历史：**只放正文**（tool 消息必须挂在带 id 的 `tool_calls` 后，而库里只有摘要）；没有用户输入的孤立回答跳过（会形成连续两条 assistant） |
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
| `summarizeResult` / `displayBlock` | `func summarizeResult(call llm.ToolCall, result tools.Result, err error) string`、`func displayBlock(d *tools.Display) string` | `EventToolResult.Text` 的**首行摘要**与展示块（diff 行、匹配列表，`maxDisplayLines=12` 截断并说明） |
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
    Sessions() ([]SessionInfo, error)   // P4.5 加：列本目录历史会话，只列不切
}

// SessionInfo 是给界面的会话摘要：认得出是哪次对话就够，全量事件属于 Restore。
type SessionInfo struct {
    ID      string
    Title   string
    Turns   int
    Updated time.Time
}
```

> `Sessions` 是**唯一一次为展示需求扩接口**（2026-10-29，经人同意）。取舍：另一条路是给
> `repl.Deps` 传 `*session.Store`，界面直接读存储——那会让 UI 依赖存储层，分层就破了。
> 扩接口的代价是一次性同步文档；破分层的代价是以后每次改存储都要看界面有没有偷偷用到。

`adapter.go`（薄包装，只转发给 `appstate`）：

| 元素 | 签名 | 职责 |
|---|---|---|
| `Core` | `type Core struct { state *appstate.State }` | 正式实现；`var _ AgentCore = (*Core)(nil)` 在编译期确认方法齐全 |
| `New` | `func New(client llm.Client, registry *tools.Registry, guard *filesystem.Guard, perm tools.PermissionRequester, system string) AgentCore` | 把依赖直接传给 `appstate.New`（不落盘） |
| `NewWithSession` | `func NewWithSession(client, registry, guard, perm, system, store *session.Store, sess *session.Session) *Core` | 同上并挂上会话库；**返回具体类型**，因为装配完通常还要补一句 `Restore`，而"恢复"不是界面能力 |
| `Restore` | `func (c *Core) Restore(turns []session.Turn)` | 转发给 `state.Restore`，只在启动装配时调一次 |
| `Turn` | `type Turn = session.Turn` | 别名：界面与 `cmd.Env` 传历史时不必 import 存储层 |
| `Submit` / `Interrupt` / `Messages` / `Clear` | 四个方法 | 全部转发给 `state`，本身不含逻辑 |
| `Sessions` | `func (c *Core) Sessions() ([]SessionInfo, error)` | 直接查 `store.List()` 并过滤 0 轮的会话；`store` 在 `Core` 上留一份引用，`appstate` 不必多一个查询入口 |
| `Restore` | `func (c *Core) Restore(turns []session.Turn)` | 启动装配时灌回上次的历史 |

> P2 曾有一个 `Direct`（自带历史与取消句柄），P3.6 起改成 `appstate` 的薄包装并删除了 `Direct`：
> 历史与循环只能有一处实现，留两套必然分叉。`repl` 一行都没改——这正是当初留这道缝的目的。

`setup_tools.go`：把 UI 能力适配成工具需要的窄接口。

| 函数 | 签名 | 职责 |
|---|---|---|
| `SetupTools` | `func SetupTools(deps ToolDeps) *tools.Registry` | 注册 6 个工具，顺序固定（当前在 `cmd/main.go` 的 `buildTools`） |
| `diffAdapter` | `type diffAdapter struct { core AgentCore }` | `EmitDiff` → 推 UI（P3.7b 起用） |

`permission.go`（P3.7）：工具与界面之间的权限通路。

| 元素 | 签名 | 职责 |
|---|---|---|
| `PermissionPrompt` | `type PermissionPrompt struct` | 工具侧阻塞地问、界面侧异步地答；持有"本会话总是允许"的缓存 |
| `NewPermissionPrompt` | `func NewPermissionPrompt() *PermissionPrompt` | 建通路，不做 IO、不起 goroutine |
| `RequestPermission` | `func (p *PermissionPrompt) RequestPermission(ctx context.Context, req PermissionRequest) (Decision, error)` | 实现 `tools.PermissionRequester`；ctx 取消时返回 Deny **加错误**，好把"没等到答复"与"用户拒绝"分开 |
| `Requests` / `Answer` | `func (p *PermissionPrompt) Requests() <-chan PermissionRequest`、`func (p *PermissionPrompt) Answer(Decision) bool` | 界面侧的一读一答；同一时刻只有一条询问在路上，回答完再回来等下一个 |
| `sessionKey` | `func sessionKey(req PermissionRequest) string` | 缓存键 `tool\|op\|目录`：同目录的同类操作只问一次（exec 的路径固定是 `.`，等于"本会话允许执行"） |
| `PermissionRequest` / `Decision` | 类型别名 → `tools` 同名类型 | 界面不必 import `tools`，也不产生第二套同构类型 |

> **为什么不是给 `AgentCore` 加 `RequestPermission`**：`agentcore.Core` 只是转发给 `appstate`，
> 它没法把问题送回界面——那要求它反向持有 UI，而 main 又必须先建 Core 再建界面，接线成环。
> 通路对象在 `main.buildEnv` 里建，两侧各拿一份引用（工具侧经 `perm`，界面侧经 `cmd.Env`）：
> `AgentCore` 的契约不变，装配顺序也不再重要。

`types.go`：`Mode` 等跨层类型（`PermissionRequest`/`Decision` 已确定复用 `tools` 的别名，见上）。

## 9. UI 层 `internal/cli/repl/`

`repl.go` —— Bubble Tea 的四个必备函数 + 一个桥接函数：

| 函数 | 签名 | 职责 |
|---|---|---|
| `Deps` | `type Deps struct { Version, Model string; Core agentcore.AgentCore; Permission *agentcore.PermissionPrompt; History []agentcore.Turn; Restored string; ContextBase, ContextWindow int; Ctx context.Context }`（P4.4 加 `ContextBase`/`ContextWindow`） | 界面只拿展示、回合引擎、权限通路、历史与上下文常量 |
| `NewModel` | `func NewModel(d Deps) Model` | 建状态并把 `History` 落进记录：**恢复说明在最上、分隔线在最下**，否则用户会把旧内容当成刚发生的 |
| `submit` | `func (m Model) submit() (tea.Model, tea.Cmd)` | `/exit` 退出；调 `Core.Submit`，成功后进入流式状态并返回 `waitEvent` |
| `handleStreamEvent` | `func (m Model) handleStreamEvent(ev llm.StreamEvent) (tea.Model, tea.Cmd)` | 正文累积、`EventRetry` 提示、`EventToolResult` 落成摘要 + 展示块、终态收尾；`tool_start`/`usage` 不渲染 |
| `finishTurn` | `func (m Model) finishTurn(err error)` | 正文落历史 → 中断提示 → 错误提示；清流式状态 |
| `interrupt` | `func (m Model) interrupt()` | `Esc` 调 `Core.Interrupt`，**不立即改状态**：等流自己收尾，避免两处状态不一致 |
| `context` | `func (m Model) context() context.Context` | 取注入的 ctx，测试不必构造 |
| `streamEventMsg` / `streamClosedMsg` | 两个消息类型 | 事件与流异常结束进 `Update` 的通道 |
| `NewModel` | `func NewModel(d Deps) Model` | 只建状态，不做 IO |
| `Init` | `func (m Model) Init() tea.Cmd` | 输入框聚焦；窗口尺寸由框架发 `WindowSizeMsg` |
| `Update` | `func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd)` | **唯一**改状态的地方 |
| `View` | `func (m Model) View() tea.View` | v2 返回 `tea.View` 而非 `string`；实际内容由 `render() string` 产出，便于单测 |
| `Run` | `func Run(ctx context.Context, d Deps) error` | 非 TTY 直接返回中文错误；否则建 `tea.Program` 并阻塞 |
| `isInteractive` | `func isInteractive(f *os.File) bool` | 字符设备 **且** 不是空设备（`os.SameFile` 排除 `/dev/null`、`NUL`） |
| `waitEvent` | `func waitEvent(ch <-chan llm.StreamEvent) tea.Cmd` | **channel → tea.Msg 的桥**，UI 与 agent 解耦的关键 |
| `waitPermission` | `func waitPermission(p *agentcore.PermissionPrompt) tea.Cmd` | 同上，把"读一条权限询问"变成 cmd；`Init` 挂一次，每次作答后续上一条，链上只有一个等待者 |
| `showPermission` | `func (m Model) showPermission(req agentcore.PermissionRequest) (tea.Model, tea.Cmd)` | 打开弹窗；**先把已生成正文落进历史**，否则权限记录会排在模型那句话上面 |
| `answerPermission` | `func (m Model) answerPermission(msg tea.KeyPressMsg) (tea.Model, tea.Cmd)` | `1/y` 允许一次、`2/a` 本会话总是允许、`3/n/Esc` 拒绝、`Ctrl+C` 拒绝并退出；其它键不响应（弹窗是模态的） |
| `respond` / `permissionNote` | `func (m Model) respond(decision agentcore.Decision) (tea.Model, tea.Cmd)`、`func permissionNote(req, decision) string` | 送回答案、记一行留痕（"已允许一次 / 本会话不再询问 / 已拒绝"）、重新等待下一次 |
| `renderPermission` / `opText` | `func (m Model) renderPermission() string`、`func opText(op string) string` | 弹窗占用输入框位置：工具名 + 中文动词 + 路径 + Detail + 三个选项 |
| `appendToolResult` / `displayLine` | `func (m *Model) appendToolResult(ev llm.StreamEvent)`、`func displayLine(line string) string` | 先 flush 正文再落结果行（"⎿ 摘要"）；展示块按行首 `+ `/`- ` 上绿/红，其余暗色 |

子包各自的入口（只列一个代表函数，避免过度规定）：

| 子包 | 入口 |
|---|---|
| `stream` | `func Render(ev llm.StreamEvent, st *State) []Segment` |
| `output` | `func (o *Output) Push(s Segment)`、`func (o *Output) Render(width int) string` |
| `markdown` | `func Render(src string, width int, th theme.Theme) string` |
| `permissions` | 暂未单独成包：弹窗状态只有 `Model.pending` 一个字段，拆包要多一层转发；功能长大（选择器、批量授权）时再按常见实现拆出 `requester.go`/`selector.go` |
| `context_status.go` 的对应物 | `context.go`：`contextUsage`/`contextUsageFor`/`renderContext`/`formatTokens`/`padLabel`。分类明细由界面用 `Core.Messages()` 算，不像常见实现那样在引擎里暴露 `GetContextBreakdown`——那三个数只有界面要用 |
| 斜杠命令 | `commands.go`：`command{name, alias, help, run}` 与 `commandList()`。**五条命令一张表**，`/help` 从同一张表渲染，命令与说明因此不会漂移。没有拆成 `commands` 子包：每条命令都要读写 `Model` 状态，搬出去得先把这些能力抽象成接口，对五条命令不划算 |
| `/clear` | 清 `Core.Clear()` + 记录 + `answer` + `lastPromptTokens`；**会话文件不动**（只追加），所以提示里要说"记录仍然保留" |
| `/sessions` | 调 `Core.Sessions()` 渲染 `ID · 轮数 · 时间 · 标题`；只列不切 |
| `widgets` | 每个组件 `NewXxx(...) Model` + `Update` + `View` |
| `commands` | `func Register(c Command)`、`func Lookup(name string) (Command, bool)` |

## 10. 回收 `internal/cleanup/cleanup.go`

| 函数 | 签名 | 职责 |
|---|---|---|
| `Run` | `func Run() error` | 回收过期工具输出（P3.8 只接 artifact，7 天） |
| `runAt` | `func runAt(now time.Time) error` | 可注入时钟的版本；保留期判定必须能在测试里控时间 |
| `pruneExpired` | `func pruneExpired(dir string, cutoff time.Time) (int, error)` | 删掉 `dir` 下 mtime 不晚于 `cutoff` 的**普通文件**；不递归、不跟符号链接；目录不存在视为无事可做；单个删不掉不中断整轮（`errors.Join` 合并返回） |
| `pruneHistory` | `func pruneHistory(path string, keep int) error` | 保留最后 N 条（输入历史，P4 接入时再写） |

失败必须**只记日志**：回收是尽力而为，不能让启动失败。`main.run()` 在 `logging.Init` 之后、装配之前调一次，失败 `slog.Warn`。

> `logs` / `sessions` 的回收**暂不接**：前者的路径来源在 `logging` 里还是私有的，后者要到 P4 才有格式。
> 到那时给 `logging` 加一个导出的目录函数，再把三类一起挂到 `Run` 上——现在先不为了"对称"多绕一层。

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
| 2026-10-29 | 启动接线落地：§1 步骤 2–5 收进 `main.resolveModel` 并延迟执行；缺密钥改判退出 1（2 只留给用法错误） |
| 2026-10-29 | `config.Resolve` 默认模型回退修正：只指定供应商时用该供应商的第一个模型（新增 `Registry.FirstModel`） |
