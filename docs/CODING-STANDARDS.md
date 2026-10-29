# SyCode 开发规范（CODING STANDARDS）

本文件是**代码层面的强制约定**。与本文冲突的写法一律视为缺陷，评审有权直接打回。
流程（怎么推进、留痕、评审顺序）见 [WORKFLOW.md](./WORKFLOW.md)。

---

## 1. 语言、工具链与格式

| 项 | 规定 |
|---|---|
| Go 版本 | `go.mod` 声明 ≥ 1.24；开发机 1.26.3，CI 与开发机同版本，不使用更旧版本兼容技巧 |
| 格式化 | `gofmt`（不讨论 tab/空格，不引 gofumpt 之外的风格工具） |
| 静态检查 | `go vet ./...` 必须通过；禁止 `//nolint` 除非同一行写明原因 |
| 竞态 | 涉及 goroutine/channel 的包，CI 跑 `go test -race` |
| 依赖 | 直接依赖 ≤ 15 个；新增必须在该 PR 描述里写"标准库/现有依赖为何不行" |
| 生成物 | 如需 `go generate`，生成命令写进 `Makefile`，产物入库并在 PR 说明 |

质量门是**一份脚本** `scripts/check.sh`，本地与 CI 共用（避免"本地过、CI 不过"）：

```bash
bash scripts/check.sh     # 格式检查 → go vet → go build → go test → go test -race
```

有 make 的环境可用 `make check`，它只是薄封装。Windows 默认没有 make，所以以脚本为准。

脚本里的两个必要处理（都不是可选项）：
- `go build` 必须带 `-o <临时目录>/`：直接 `go build ./...` 会把 main 包的二进制写到仓库根目录，造成误提交。
- `-race` 需要 cgo + gcc；缺失时**跳过并提示**，不伪装成功。

## 2. 包与依赖方向

### 2.1 包设计
- **包级结构与常见实现一致**：不新增常见实现里没有的包；需要新目录时先在 `ARCHITECTURE.md` 的目录树里登记，评审通过再建。
- 包名：小写、单数、语义明确。**禁止** `util`、`common`、`helpers`、`shared`、`base`。
- 一个包一个职责；包内文件按"一个功能一个文件"切（`guard.go`、`gitawareness.go`、`read_file.go`），不按层命名（`models.go` 里塞全部结构体是大忌），也不额外套子目录。
- 不导出的类型不写文档注释也可以；**导出标识符必须有以标识符开头的完整句子注释**。

### 2.2 依赖方向（由 `internal/cli/cmd/deps_test.go` 强制）

```
cmd → cli/cmd → cli/repl → agentcore → appstate → {llm, tools, session, config}
                                                 ↘ filesystem
llm → providers, logging
tools → filesystem, logging
session → logging
config → providers
filesystem → （无内部依赖）
```

**禁止边**（CI 会失败）：
- `llm` → `cli`、`agentcore`、`appstate`（provider 不认识 UI 与回合）
- `appstate` → `cli`（状态机不认识 TUI；UI 通过 `AgentCore` 接口反向拿数据）
- `tools` → `llm`（工具不认识模型；schema 转换在 `llm` 侧）
- `filesystem` → 任何内部包
- `session` → `llm`（会话只存 `json.RawMessage` 与基础类型）
- 任何包直接 `os.Open`/`os.WriteFile` 业务文件 → 必须经 `tools` + `Guard`

### 2.3 接口
- **接口定义在消费方**，不在实现方。`llm.Client` 定义在 `appstate` 需要它的地方（v0.1 因跨包使用放在 `llm` 包，但实现包不得再定义同名接口）。
- 接口尽量小（1~3 方法）。只有一个实现的接口，除非用于测试替换或跨包解耦，否则不抽。
- 接受接口、返回具体类型；不返回接口。

## 3. 错误处理

```go
// 包装：保留链路
return fmt.Errorf("读取文件 %q 失败：%w。请确认路径存在且有读权限", path, err)

// 判定：用哨兵错误，不用字符串比较
if errors.Is(err, fs.ErrNotExist) { ... }

// 面向模型的错误：中文 + 保留英文字段名 + 给示例，模型据此自我修正
return Result{}, fmt.Errorf(`参数错误：缺少必填字段 "path"。请带上 {"path":"internal/tools/tool.go"} 重试`)
```

规则：
1. **不 panic**。仅 `main` 与测试允许；库代码 panic 视为缺陷。
2. **不忽略错误**：`_ = err` 必须附注释说明为何可忽略。
3. **错误信息**：小写开头、无标点结尾、不含敏感值（路径可、密钥不可）。
4. **错误分层**：基础设施错误带 `%w` 链；面向用户/模型的错误在边界处转成可读文本（`appstate` 层负责）。
5. **自定义错误类型**用于需要携带结构化字段的场景（如 `ToolsError{Code, Hint}`），否则用哨兵 + wrap。

## 4. 并发

1. **单写者**：TUI 状态只允许在 Bubble Tea 的 `Update` 中修改。其他 goroutine 通过 `tea.Msg` 投递。
2. 每个 goroutine 必须有明确退出路径：`ctx.Done()`、channel 关闭、或 `WaitGroup` 收敛。
3. channel 由**创建者**关闭；接收方永不关闭。
4. 禁止 `time.Sleep` 做同步（测试里也不行）；等待用 channel/`sync`/`context`。
5. 共享可变状态优先用"传递消息"而非 mutex；必须用锁时，锁的粒度写进注释。
6. `context.Context` 是第一个参数，不存进 struct 字段（`Request` 之类纯数据除外）。

## 5. 日志

```go
slog.Debug("tool executed", "tool", name, "duration_ms", d.Milliseconds(), "ok", err == nil)
```

- 用 `log/slog`，键用 `snake_case`，值用原始类型（不要 `fmt.Sprintf` 拼字符串）。
- 级别：`Debug` 开发细节 / `Info` 生命周期节点（启动、会话创建、模型切换）/ `Warn` 可恢复异常 / `Error` 需人工介入。
- **禁止**记录：API key、Authorization header、完整用户 prompt、文件内容正文。记录路径、长度、哈希即可。
- 库代码不初始化全局 logger；logger 从入口注入或使用默认 `slog`。

## 6. 工具实现规范（`internal/tools`）

每个工具一个文件，必须满足：

1. **契约**
   - `Name()` 用 `snake_case`，与常量 `XxxToolName` 一致。
   - `Description()` 写给模型：说明"做什么、什么时候用、不适用于什么"。不超过 6 行。
   - `InputSchema()` 只描述必要字段，`required` 准确，给出 `default`。
2. **输入校验**
   - 实现 `ValidateInput`，缺失必填参数返回 `参数错误：缺少必填字段 "path"。请带上 {"path":"…"} 重试`（§13.2）。
   - 类型错误回 `参数错误：字段 "limit" 必须是整数`——模型据此自我修正。
3. **执行**
   - `Execute` 永不 panic；对 nil/空输入做防御。
   - 文件访问前必须 `Guard.Check(path, op)`；`Pending` 时通过注入的 `PermissionRequester` 询问，不得自行决定放行。
   - 输出上限 32KB，超限截断 + 写 artifact 到 `~/.sycode/bash/`（或对应目录）+ 在 `Result.Artifact` 返回路径。
4. **可测**
   - 用 `t.TempDir()` 造真实文件，不 mock `os`。
   - 权限询问通过接口注入 fake，测试"拒绝时是否真的没写"。
5. **注册**：新增工具 = 1 个文件 + `registry.go` 一行。不改 `llm`、不改 REPL（渲染由 `Display` 字段驱动）。

## 7. 模型交互规范（`internal/llm`、`internal/appstate`）

1. **系统提示词集中**：全部在 `llm/systemprompt.go`，分段构建（身份 / 工具约定 / 环境事实 / 项目指令 / 技能目录），便于按 provider 覆写与后续缓存。
2. **环境事实必须注入**：OS、工作目录、shell 类型（ADR-006）、当前模型、日期。
3. **工具 schema 转换**在 provider 适配层完成，`tools` 包不感知 provider。
4. **流式终态**：`EventDone` 与 `EventError` 必有其一且最后发送。UI/appstate 不得依赖 channel 关闭顺序。
5. **重试**：只在 provider 层做，且只对 429/5xx/网络错误；重试期间发 `EventRetry{Attempt}`，UI 负责提示。
6. **工具循环**上限 25 轮；连续 3 次同工具同参数失败时，向模型注入提示要求改变策略。
7. **token 估算**：本地粗估（字符数/4 上下浮动）即可，但必须**标注为估算**；不因估算触发破坏性裁剪（v0.1 只提示不裁剪）。

## 8. 安全红线（违反即阻断合并）

1. 任何文件读写必须经 `filesystem.Guard`。发现绕过 → 缺陷，不是建议。
2. 路径必须 `filepath.Clean` + 绝对化后再判定；符号链接解析后再判一次（防穿越）。
3. 密钥只从环境变量或 `~/.sycode/` 下的配置文件读取，**永不**写进会话、日志、错误信息、示例。
4. 会话与日志文件创建时权限 `0600`，目录 `0700`。
5. 危险命令模式（`rm -rf`、`git reset --hard`、`git push --force`、`curl|sh`、`> /dev/*`、`mkfs`、`shutdown`）必须二次确认；判定用白名单外的显式模式表，且**表在代码里可读可审**。
6. 写 `MEMORY.md`/配置类文件前，做密钥模式扫描（`sk-`、`Bearer `、`-----BEGIN`、长 base64），命中则拒绝写入。
7. 不在 `init()` 里做 IO 或网络请求。

## 9. 测试规范

| 类型 | 要求 |
|---|---|
| 单测 | 每个含分支的函数至少 1 组表驱动用例；边界（空、超限、非法路径）必须覆盖 |
| 工具测试 | 真实临时目录；覆盖"权限拒绝时副作用为零" |
| provider 测试 | `httptest.Server` 回放 SSE 分片；覆盖分片跨行、`[DONE]`、异常中断、429 重试 |
| 会话测试 | 追加-重放一致性：写入 N 个事件 → 投影出的消息列表与预期逐字段相等 |
| 架构测试 | `internal/cli/cmd/deps_test.go` 断言依赖方向 |
| 覆盖率 | 核心包（`tools`、`filesystem`、`session`、`appstate`）≥ 80%；UI 渲染不设线，但关键状态迁移要有测试 |

写法约定：
- 表驱动 + `t.Run(name, ...)`；子测试并行时注意共享 `t.TempDir()` 的隔离。
- 断言用标准库 `if got != want { t.Fatalf(...) }`，不引第三方断言库。
- 测试名描述行为：`TestEditFile_AmbiguousMatchReturnsCandidates`。
- 禁止依赖网络、真实 `$HOME`、当前时间（时间通过接口注入）。
- 测试数据放 `testdata/`，不以字符串常量内联超过 30 行。

## 10. 命名与代码风格细则

| 场景 | 规范 | 反例 |
|---|---|---|
| 缩写 | 全大写或全小写，不混写 | `userId` → `userID`；`HttpClient` → `HTTPClient` |
| 布尔 | `is/has/can/should` 前缀 | `flag`、`check` |
| 常量 | 工具名/事件类型用 `XxxName`/`EventXxx` | `TOOL_READ` |
| 接收者 | 1~2 字母，全文件一致 | `func (this *Guard)` |
| 文件行数 | 单文件 > 500 行需在 PR 说明为何不拆 | 逻辑分层的 1200 行 handler |
| 函数长度 | > 60 行考虑拆；嵌套 > 3 层必须拆 | 巨型 `Update` 分支 |
| 注释 | 按 §12 分级：意图/约束/坑必写，不复述代码 | `// 把 i 加 1` |
| 魔法值 | 提取为具名常量 | 直接写 `25000`、`32 * 1024` |
| 字符串拼路径 | `filepath.Join`，不用 `+` | `dir + "/" + name` |

**跨平台**：不用 `os.Getenv("HOME")`，用 `os.UserHomeDir()`；路径分隔统一 `filepath`；`bash` 工具的命令字符串在 Windows 下通过 ADR-006 选择的 shell 执行。

## 11. 提交与 PR

- 提交信息见 WORKFLOW.md 第 5 节。
- PR 描述模板（必填 4 段）：`改了什么 / 为什么 / 怎么验证 / 留痕路径`。
- PR 必须满足：`make check` 全绿、diff ≤ 600 行（不含测试）、含 `.ai-interactions/` 文件、CHANGELOG 有 `Unreleased` 条目（用户可见变更）。
- 一个 PR 只干一个 issue 的事。顺手重构请另开 PR，并在描述里说明动机。

## 12. 注释规范

**原则**：注释解释"意图、约束、为什么"，不复述代码。**必要的地方必须写**，其余地方宁可不写。

### 12.1 必须写（缺了算缺陷）

| 位置 | 要求 | 示例 |
|---|---|---|
| 包注释 | 每个包顶部：职责 + **不做什么** + 关键约束 | `// Package session 负责会话事件的追加与重放。`<br>`// 本包不构造 prompt，也不 import llm（见 CODING-STANDARDS §2.2）。` |
| 导出符号 | 完整句子，以符号名开头；写清契约（参数含义、返回、并发安全、是否阻塞） | `// Check 判定 path 上的 op 是否允许。纯函数，不做 IO。` |
| 非显然的意图 | 为什么这样写、为什么不能那样写 | `// 这里故意不重试：4xx 是参数问题，重试只会放大错误。` |
| 边界与坑 | 平台差异、常见实现 bug 的绕过、精度/性能取舍 | `// Windows 下 SysProcAttr 不支持 Setpgid，改用 job object 结束子进程。` |
| 魔法值 | 阈值/上限的来源与依据 | `// 25 轮：超过这个数通常是模型陷入循环，继续只会烧 token。` |
| 并发假设 | 谁写、谁读、锁保护什么 | `// mu 只保护 msgs，不保护 client —— client 自身线程安全。` |
| 危险处 | 统一前缀 `// 注意：`，便于全局 grep | `// 注意：此处路径未经 Guard 校验，调用方必须先 Check。` |

### 12.2 建议写

- 复杂分支前的"这段在解决什么"（一行足够）
- 测试用例为什么重要：`// 回归：v0.1 曾把 CRLF 的 \r 算进行尾，导致锚点漂移`
- 状态机的迁移意图：`// 进入 Pending 后只能转向 Granted 或 Denied，不允许直接回 Idle`

### 12.3 不要写（评审直接删）

| 反模式 | 例 |
|---|---|
| 复述代码 | `// 把 i 加 1` |
| 变更日志式注释 | `// 2026-10-29 改成支持中文错误`（这是 git 的活） |
| 注释掉的代码 | 删掉，git 里有历史 |
| 装饰性内容 | 分隔线、作者署名、`// ===== 工具函数 =====` |
| 中英混写的半句话 | `// check path 是否 ok` |

### 12.4 格式

- 语言：**中文**（标识符本身保持英文）。
- 单句不加句号；两句以上用正常标点。
- `//` 后留一个空格；行宽 ≤ 100。
- 导出符号注释首词是符号名：`// NewGuard 创建…`，不要写成 `// 创建 Guard…`。
- `TODO` 必须带 issue：`// TODO(issue-42): 支持 hashline 锚点`。
- `FIXME` 只用于已知会出错的地方，必须同时写触发条件。

## 13. 语言与错误文案

### 13.1 分层语言规则

| 层 | 语言 | 例 |
|---|---|---|
| 代码标识符（变量/函数/类型/文件名/schema 字段/配置键） | **英文** | `read_file`、`path`、`context_window` |
| 面向用户的输出（TUI、CLI、错误、帮助、提示） | **中文** | `读取文件 "a.go" 失败：文件不存在。请确认路径。` |
| 回灌给模型的工具错误 | **中文**，但保留英文标识符与示例 JSON | `参数错误：缺少必填字段 "path"。请带上 {"path":"..."} 重试。` |
| 日志 | key 英文 `snake_case`，message 中文 | `slog.Warn("会话恢复失败", "session_id", id, "err", err)` |
| 提交信息 / 代码标识符注释外的元数据 | 英文 | `feat(tools): reject ambiguous edit matches` |
| 文档、README、注释 | 中文 | — |

### 13.2 错误文案四要素

每条错误必须能回答：**发生了什么 / 对象是谁 / 为什么 / 下一步做什么**。

```go
// 用户侧：<动作> <对象> 失败：<原因>。请<建议>。
return fmt.Errorf("读取文件 %q 失败：%w。请确认路径存在且有读权限", path, err)

// 工具侧（回灌模型）：参数错误：<哪错>。请<怎么改>，例如 {...}。
return Result{}, fmt.Errorf(`参数错误：缺少必填字段 "path"。请带上 {"path":"internal/tools/tool.go"} 重试`)
```

### 13.3 前缀表（只允许这些）

| 前缀 | 用于 |
|---|---|
| `参数错误：` | 输入缺失/类型错/取值越界 |
| `执行失败：` | 已通过校验，但执行中出错（IO、子进程、解析） |
| `权限不足：` | Guard 判定 Denied，或用户拒绝 |
| `超时：` | 上下文超时、命令超时 |
| `不支持：` | 当前 provider/平台不支持该能力 |

### 13.4 不翻译清单

文件路径、命令、包名/字段名/schema key、HTTP 状态码、供应商错误码、第三方库原文（放在括号内保留）、URL。

> 例：`执行失败：模型服务返回 429（rate limit exceeded）。请稍后重试或换模型。`

### 13.5 禁止

- 错误里回显密钥、token、完整请求体。
- 丢掉底层错误只留中文（必须 `%w` 保留链，否则无法排查）。
- 中英文各写一遍（"file not found 文件不存在"）。
- 在 `fmt.Errorf` 里用 `%v` 包错误（用 `%w`，否则 `errors.Is` 失效）。

### 13.6 cobra 与 provider 的汉化落点

| 位置 | 做法 |
|---|---|
| cobra 帮助/命令描述 | `Short`/`Long` 全中文 |
| cobra 参数错误 | `rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return fmt.Errorf("参数错误：%w", err) })` |
| cobra 英文内置错误（unknown command / required flag） | cobra 无 i18n，在 `run()` 里做一次映射表；映射不到的原样输出并保留原文 |
| 错误时刷帮助 | `SilenceUsage = true`，只在用法错误时打印用法 |
| provider HTTP 错误 | `llm/errors.go` 的 `Translate(err) error`：401/403/429/5xx/网络/超时 → 中文 + 保留原始码 |

### 13.7 测试失败信息

也用中文，且必须同时给出期望值与实际值：

```go
t.Fatalf("权限判定不符：期望 %v，实际 %v（路径 %q）", want, got, path)
```

## 14. 自查清单（提交前 60 秒）

- [ ] `bash scripts/check.sh` 绿
- [ ] 没有新增 `util`/`common` 包
- [ ] 所有文件 IO 走了 `Guard`
- [ ] **所有面向用户的错误都是中文**，且带"下一步做什么"
- [ ] 工具错误用了 §13.3 的前缀，且保留了英文字段名与示例 JSON
- [ ] 错误链用 `%w` 保留，没有 `%v` 包错误
- [ ] 包注释写了"职责 + 不做什么"；坑与魔法值有注释
- [ ] 没有复述代码的注释，没有注释掉的代码
- [ ] 本次推进单元已收口：能编译、测试过、已提交、`STATE.md` 已更新
- [ ] 错误信息里没有密钥、没有绝对家目录路径
- [ ] 新逻辑有测试；修 bug 有回归测试
- [ ] 单文件没超 500 行，函数没超 60 行
- [ ] `.ai-interactions/` 往返完整，output 未被改写
- [ ] 相关 `docs/*.md` 已同步

---

**变更本文件**：需单独 PR，标题 `docs(standards): ...`，说明"为什么要加这条约束"，并检查现有代码是否已违反（违反则同时登记技术债 issue）。
