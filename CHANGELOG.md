# 变更日志

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added
- `internal/appstate`：回合状态与**唯一的工具循环**（ADR-001）。请求 → 读事件 → 有工具就执行并回灌 → 再请求；25 轮上限、连续 3 次失败追加换做法提示；工具失败也回灌给模型；`EventToolResult` 让界面看到调用结果
- `internal/agentcore`：`New` 装配 `appstate`（取代 P2 的 `Direct`，已删除）；`main.buildEnv` 装配 `filesystem.Guard` 与 6 个工具并注册
- `llm.StreamEvent` 新增 `EventToolResult`：区分「模型要调什么」与「执行得到什么」
- `internal/tools/bash.go`（+ `proc_windows.go` / `proc_unix.go`）：命令执行工具。超时杀整棵进程树（`taskkill /T`、`kill(-pid)`）、`WaitDelay` 兜底防超时失效、超 32KiB 输出落盘到 `~/.sycode/bash`、危险命令在权限弹窗标 ⚠；`ResolveShell` 按 ADR-006 选定解释器，`main` 的系统提示改为复用它（消除两处重复）
- `internal/tools/write_file.go` + `edit_file.go` + `diff.go`：写类工具。**原子写**（同目录临时文件 → Sync → Rename）、写后回读校验、被拒时零副作用、内容未变不碰文件；`edit_file` 只接受唯一匹配（歧义时列出候选行号，0 命中时提示先用 grep）；改动以 `Display{Kind: DisplayDiff}` 呈现
- `internal/tools/glob.go` + `pattern.go`：`glob` 工具与**自己实现的 glob 匹配**（模式转正则：`*`/`?`/`**`（零层也匹配）/`[abc]`/`[!abc]`，Windows 大小写不敏感）；跳过 `.git` 与被忽略目录，输出字典序（两次调用结果一致）
- `internal/tools/grep.go`：`grep` 工具（Go 正则、`include` glob 过滤、`路径:行号: 内容`）；跳过二进制、超 4MiB 文件与忽略项；单行上限 256KiB 且超长行不影响后续行号
- `internal/tools/read_file.go`：`read_file` 工具（分段 `offset`/`limit`、16MiB 上限后建议用 grep、二进制探测、32KiB 截断并提示续读）；**给模型的内容不带行号**（避免行号被写进 `old_string`），行号只进 `Display`
- `internal/tools/input.go` + `permission.go`：参数解析（不裸断言，类型错给中文示例）与权限通道收口（三态 → 文案只此一处，拒绝的文案像决定而不像故障）
- `internal/tools`：`Tool`/`Validator`/`PermissionRequester` 契约、`Result`（含 `Display` 结构化展示与 `Artifact` 落盘路径）、`Schema`；`Registry`（注册顺序稳定、重名报错、`Schemas` 返回副本）；`clip` 把超长结果截到 32KiB 且不切断多字节字符
- `internal/filesystem`：`Guard`（权限三态 `Denied/Pending/Granted` + `Reason{Code,Message}`）与 `GitAwareness`（递归载入 `.gitignore`，跳过已忽略目录）。判定顺序 artifact → 系统路径 → 家目录隐藏项 → 工作目录内（`.git` → gitignore → read/write）→ 目录外；`Resolve` 展开 `~`、解析符号链接（含「父目录是链接、目标还不存在」）
- **REPL 流式渲染**：`repl.Deps` 接入 `agentcore.AgentCore`，正文增量即渲染；重试显示「第 N 次失败，正在重试…」；`Esc` 中断保留已生成内容；流意外结束（无终态）有提示；流式期间拒绝新输入（避免两股回答缠在一起）
- **启动装配**：`cmd.Env{Model, Core}` + `Options.BuildEnv`；`main.buildEnv` 把解析结果、`llm.OpenAI` 与系统提示（OS/工作目录/shell/日期）组装成 `agentcore.Direct`
- `internal/agentcore`：`AgentCore` 接口（`Submit`/`Interrupt`/`Messages`/`Clear`）+ P2 的直连实现 `NewDirect`——界面不再直接碰 `llm.Client`；中断后已产出文本仍入历史（PRD F-LLM-6），请求都发不出去的那次不入历史
- `internal/llm/retry.go`：`Do` 指数退避重试（`maxRetries=3`、封顶 30s、加抖动且不越界）；`isRetryable` 认 429/5xx/连接失败/流截断，不认 4xx/取消/超时；`partialError` 让"已吐字之后"的失败不再重试
- `internal/llm/openai.go` 重试接线：重试只发生在 `run`，每次失败发 `EventRetry{Attempt:n}` 供 UI 显示进度
- `internal/llm/openai.go`：OpenAI 兼容流式客户端（`openai/openai-go/v3`）。`NewOpenAI` 只校验、关掉 SDK 自带重试；`StreamChat` 返回由发送方关闭的事件 channel，HTTP 错误也走事件流；分片工具调用按 `index` 归并（`tool_start` 发快照、`tool_end` 发完整参数）；**截断识别**——没收到 `finish_reason`/`usage` 就结束时报错，不把半句话当完整回答
- `internal/llm/errors.go`：`Translate` 把供应商/网络英文错误翻成中文（取消/超时/HTTP 状态/连接失败各有文案），`httpStatusError` 按状态码给"下一步该干什么"并用 `Unwrap` 保住错误链；已翻译的错误不二次包装
- `internal/llm`：`Client` 契约、`Request`/`Message`/`Role`/`ToolCall`/`ToolSchema`、`StreamEvent`（8 类事件 + `IsTerminal`）、三个事件构造器；`systemprompt.Build` 按 身份 → 环境 → `AGENTS.md` 拼装（`Current shell:` 按 ADR-006 原样，8KiB 截断且不切断多字节字符）
- **启动接线**：`run()` 经 `resolveModel` 串起注册表 → 配置 → 合并 → 密钥校验；解析成功把模型名交给界面标题，`slog.Debug` 只记 provider/model/base_url；解析失败（缺密钥、配置写坏、模型与供应商不匹配）不进入界面、退出码 1 并给出可操作文案；`--version` / `--help` 不依赖密钥
- `internal/config`：`configs.json` 加载（缺文件不报错）、三级优先级合并（参数 > 配置 > 注册表）、密钥按环境变量名解析且不回显
- `internal/providers`：内嵌 `registry.yaml`（3 供应商 / 4 模型）、`Load`/`LoadFile`/`Find`/`Default`，含 6 类非法注册表校验

### Fixed
- `config.Resolve` 只指定 `active_provider` 时错误沿用了 `defaults.model`（属于默认供应商），导致切换到 deepseek 报"模型不属于供应商"；改为退到该供应商的第一个模型（新增 `providers.Registry.FirstModel`）
- `internal/cli/cmd/deps_test.go`：用 `go list` 读取真实 import 图，断言 `CODING-STANDARDS §2.2` 的禁止边（已做反证：注入违规会失败）
- `internal/cli/repl`：Bubble Tea v2 最小界面（textarea 输入、`Enter` 提交、`/exit`、双击 `Ctrl+C` 退出）；非 TTY 下给出中文提示而非花屏
- `internal/cli/cmd`：cobra 命令树（`sycode` / `version` / `run`）、中文帮助与错误、退出码 0/1/2 分离；`--version` 输出 `0.1.0 (build <sha>)`
- `internal/logging`：`Init`（返回 cleanup 与日志路径）、`SYCODE_LOG_LEVEL` 解析（未知值回退 info）、`~/.sycode/logs/sycode-<时间戳>.log` 的 JSON 日志；`run()` 已接线，初始化失败退出码 1
- 规划文档：PRD、架构 RFC（含 ADR-001~008）、路线图、开发流程、编码规范；`.ai-interactions` 留痕骨架与三份模板
- `docs/CODE-DESIGN.md`：逐文件的函数清单、启动/关闭顺序、命名动词表、反面清单、与常见实现的函数级对应
- `docs/CODING-STANDARDS.md` §12 注释分级、§13 中文错误文案（分层语言规则、四要素、前缀表、汉化落点）
- `docs/WORKFLOW.md` §4.1 推进单元与上下文预算；`.ai-interactions/STATE.md` 断点文件
- `scripts/env.sh`：为精简 PATH 的终端补齐 Go 工具链环境
- **工程骨架**：`go.mod`（`github.com/sy665/sycode`, go 1.24）、`cmd/main.go`（`main`/`run` 分离）、`scripts/check.sh` 统一质量门、`Makefile` 薄封装、三平台 CI、MIT `LICENSE`、`.gitattributes`

### Fixed
- `scripts/check.sh` 的 `go build ./...` 会把 main 包二进制写到仓库根目录（曾误提交 `cmd.exe`），改为输出到临时目录

### Changed
- 目录结构对齐常见实现：`agentcore` + `appstate` 取代自建 `agent`；`llm/openai/` 收平为 `llm/openai.go`；`cli/repl` 子包补齐至 10 个
- 版本号用 `var` 而非 `const`：`-X` 只改写字符串变量，对 `const` 静默失效

### Removed
- `internal/arch` 包（依赖方向断言移入 `internal/cli/cmd/deps_test.go`）

## 说明

v0.1.0 尚未发布。首次发布前，本节只记录面向实现者的结构决策；用户可见变化从 v0.1.0 起记录。

<!-- 发布时按下列结构整理：
## [0.1.0] - YYYY-MM-DD
### Added
### Changed
### Fixed
### Removed
-->
