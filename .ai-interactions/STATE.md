# 当前状态

更新时间：2026-10-29 21:35 | 阶段：**P3** | 当前单元：3.4 已完成，下一单元 3.5（`bash`）

## 已完成（本阶段）

- 文档：PRD、ARCHITECTURE、ROADMAP、WORKFLOW、CODING-STANDARDS、CODE-DESIGN、README/AGENTS/CONTRIBUTING/CHANGELOG
- 留痕：`.ai-interactions/` phase-0 三轮 prompt/output + 三份模板 + 本文件
- 环境：go1.26.3（`D:\Apps\Go`）、gopls v0.23.0、`GOPATH=D:/Golang/code`、`GOPROXY=goproxy.cn`、`GOCACHE` 显式化；`scripts/env.sh` 补 PATH
- 结构调整：包级与常见实现对齐（`agentcore` + `appstate`，`llm/openai.go` 平铺，无 `internal/arch`）
- 规范：注释分级（§12）、中文错误文案（§13）、推进单元与上下文预算（§4.1）
- **P0.4/P0.5**：`go.mod`、`cmd/main.go`（main/run 分离 + ldflags 版本）、`scripts/check.sh`、`Makefile`、三平台 CI、MIT LICENSE、`.gitattributes`；质量门全绿

## 正在进行（未完成）

- 单元 1.1 已收口：`internal/logging`（114 行 + 140 行测试）+ `run()` 接线
- 单元 1.3 已收口：`internal/cli/cmd/root.go` + `main.go` 退出码映射；引入 cobra
- 单元 1.4 已收口：`internal/cli/repl`（212 行 + 184 行测试）；引入 bubbletea/bubbles/lipgloss v2
- 单元 1.5 已收口：`internal/cli/cmd/deps_test.go`（123 行），已用注入违规反证有效性
- 单元 2.1 已收口：`internal/providers`（155 行 + 165 行测试 + registry.yaml）；yaml.v3 已获批
- 单元 2.2 已收口：`internal/config`（205 行 + 215 行测试）：三级优先级、两类查找错误、密钥三态
- 单元 2.3 已收口（接线）：`main.resolveModel()` + `cmd.Options.ResolveModel`（延迟解析，`--version` 不依赖密钥）；
  缺密钥/配置坏退出 **1**（2 只留给用法错误，已改 CODE-DESIGN §1）；模型名进界面标题；调试日志只有 provider/model/base_url
- 单元 2.3 附带修复：`config.Resolve` 只指定供应商时错误沿用 `defaults.model` → 改用新增的 `providers.FirstModel`
- 单元 2.4 已收口（`internal/llm` 核心）：`client.go`/`request.go`/`stream_events.go`/`systemprompt.go`（328 行 + 259 行测试）
  - `System` 用 `string`（弃 `[]SystemBlock`，OpenAI 缓存按前缀自动命中）；`Tools` 用 llm 自有 `ToolSchema`（不 import tools，P3 由 appstate 转换）
  - 事件：8 类 + `IsTerminal`；`newEvent(eventOpt…)`/`errEvent(nil 兜底)`/`doneEvent(usage 可为 nil)`
  - 系统提示中文，段序 身份 → 环境 → `AGENTS.md`；`Current shell:` 保留 ADR-006 原文；8KiB 截断用 `truncateUTF8` 不切汉字
  - **工具约定段故意不写**（P2 无工具，写了会诱发幻觉调用），P3 补
- 单元 2.5 已收口（`openai.go` 337 + `errors.go` 106，测试 408 + 110）：基于已批准的 `openai/openai-go/v3`
  - **不自己写 parseSSE**：SDK 的 `packages/ssestream` 负责解码（CODE-DESIGN 已删 `parseSSE`）
  - **截断识别**：`stream.Err()` 不报"半截断开"（实测 nil），改为"收到非空 `finish_reason` 或 `usage` 才算正常结束"
  - `NewOpenAI` 显式传 key + 关 SDK 重试（防环境变量偷盖配置、防重试次数翻倍且 UI 不可见）
  - 分片工具调用按 `index` 归并；`tool_start` 发快照，`tool_end` 发完整参数
  - `Translate`：已翻译→取消→超时→`*oai.Error`→`*url.Error`→兜底；`httpStatusError` 按状态码给动作指引
- 单元 2.6 已收口（`retry.go` 166 + 测试 222）：`Do`（`maxRetries=3`、封顶 30s、抖动不越界）+ `isRetryable` + `partialError`
  - **重试只在 `run`**：`pump` 只发增量/工具事件并返回 `pumpOutcome{usage, emitted}`；`emitted` 为真即用 `partialError` 阻止重试
  - **真 bug**：重试判定在 `Translate` 之前，错误还是 `*oai.Error`；只认 `httpStatusError` 会让 429 一次就失败（测试抓到）
  - `isRetryable` 认 429/5xx/连接失败/流截断；不认 4xx/取消/超时
  - `estimate.go` **主动延后**：现在没有消费方，CODE-DESIGN 已改注"延后（P4/P6）"
- 单元 2.7 已收口（分两笔提交）：
  - `internal/agentcore`（30 + 136，测试 320）：`AgentCore` 接口 + P2 直连实现 `Direct`。**界面不再 import llm.Client**
  - `repl` 流式：正文增量即渲染、`EventRetry` 提示、`Esc` 保留已生成内容、流意外结束有提示、流式期间拒绝新输入
  - `main.buildEnv` 装配：配置 → 密钥 → `llm.NewOpenAI` → `systemprompt`（含 `detectShell`，ADR-006）→ `agentcore.Direct`
- 单元 3.1 已收口（`guard.go` 368 + `gitawareness.go` 118，测试 442 + 164）：
  - 三态 `Denied/Pending/Granted`（**零值 Denied**）+ `Reason{Code, Message}`；`Check` 纯判定，顺序 artifact → 系统 → 家目录隐藏项 → 工作目录内（`.git` → gitignore）→ 目录外
  - **新增两条拒绝**：`.git/`（远端凭证与钩子）、家目录隐藏项（隐藏即敏感）；策略表已从 6 行补到 8 行
  - `isUnder` 用 `filepath.Rel`（前缀匹配会让 `work2` 混进 `work`）；Windows 折小写比较
  - `evalSymlinks` 是包内变量：Windows 默认建不了真符号链接，测试替换它压穿越（含"父目录是链接、目标不存在"）
  - 新增直接依赖 `go-git/go-git/v5`（选型表已批准，只用 gitignore 解析器）
- 单元 3.2 已收口（`tool.go` 139 + `registry.go` 94，测试 235）：
  - 契约：`Tool`(5 方法)/`Validator`/`PermissionRequester`、`Result{Content, Display, Artifact}`、`Schema`（**独立于 llm.ToolSchema**）
  - `Registry`：nil/空名/重名报错，`All`/`Schemas` 顺序稳定且返回副本
  - `Decision` 零值 = `DecisionDeny`；`clip` 截到 32KiB、按 rune 收尾、切在行边界（**首个消费方是 3.3 的 read_file**）
- 单元 3.3 已收口（`read_file` 245 + `input` 106 + `permission` 75；`pattern` 122 + `glob` 211 + `grep` 338）：
  - `read_file`：分段、二进制探测、16MiB 后建议 grep、32KiB 截断提示；**给模型的内容不带行号**（行号只进 `Display`）
  - `input.go` 不用裸类型断言（`Execute` 永不 panic）；`permission.go` 是三态 → 文案的唯一映射点
  - `pattern.go`：**glob 模式转正则**（`**/` 与尾部 `/**` 都匹配零层，Windows 加 `(?i)`）；不支持 `{a,b}`
  - `glob`/`grep`：跳过 `.git` 与被忽略目录、起点只问一次权限、限量与截断提示；`grep` 单行上限 256KiB 且不破坏后续行号
- 单元 3.4 已收口（`write_file` 213 + `edit_file` 190 + `diff` 97，测试 377）：
  - **原子写**：同目录临时文件 → Write → **Sync** → Chmod → Rename；失败路径清临时文件；写后**回读校验**
  - **先问权限再动盘**：拒绝时零副作用（有测试断言"磁盘上什么都不存在"）；内容未变不碰 mtime
  - `edit_file` 只接受唯一匹配：歧义列候选行号（≤5 + 省略号），0 命中提示"先用 grep 确认"
  - `diff.go`：掐公共前后缀的逐行 diff（不引库、不写 LCS），进 `Display{Kind: DisplayDiff}`
- 代码 9,357 行（源码 4,714 / 测试 4,643），25 笔提交；直接依赖 7 个
- 记录：`output-1..4_impl-*.md`（阶段 3）、`output-2..7_impl-*.md`（阶段 2）、`output-1..5_impl-*.md`（阶段 1）

## 下一步第一件事（具体到文件与函数）

**P3 单元 3.5 —— `bash` 工具**（`bash.go` + `shell.go`，2 个文件）：

1. `shell.go`：`resolveShell() (Shell, error)` —— ADR-006 顺序：`SYCODE_SHELL` → `bash.exe` → PowerShell → `cmd.exe`；
   **把 `main.detectShell` 改成调这里**（消除重复，两处结论必须一致）
2. `bash.go`：`NewBashTool(guard, perm)`；`command` + 可选 `timeout`；`Execute` 走 `OpExec`
   （工作目录内是 `Pending` → 必须问；`Detail` 要带上命令本身，用户要看清在跑什么）
3. `isDangerous(cmd string) bool`：模式表（`rm -rf`、`git reset --hard`、`curl|sh`…）——
   危险命令即使被允许，也要在 `Detail` 里明确标出
4. `truncateOutput(out []byte) (inline, artifact string)`：超过 32KiB 写进 `~/.sycode/bash/…`，
   `Result.Artifact` 给路径，`Content` 给"已落盘"提示
5. 超时：默认 120s、上限 600s；超时后**杀掉整个进程树**（Windows 要 `taskkill /T`，
   否则 `go build` 的子进程会活着）
6. 验收：`go build ./...` 能跑通；超时被杀；长输出落盘且能读回；危险命令的 Detail 有标注

**注意**：`filesystem.bashOutputDir()` 已存在（`NewGuard` 用它做"可读不可写"例外），
落盘时**不要**再拼一次路径——从 `filesystem` 拿，避免两处不一致。

## 已知坑 / 需要绕过的东西

- 工具 shell 的 PATH 被裁剪：`go` 不可见 → 先 `source scripts/env.sh`（不是没装）
- `go build ./...` 会把 main 包二进制写到当前目录 → `scripts/check.sh` 已改成 `-o <临时目录>/`
- `-race` 需要 cgo + gcc：`/d/mingw64/bin` 进 PATH 后 `CGO_ENABLED=1`；否则脚本跳过并提示
- 版本号必须是 `var version`，`-X` 对 `const` 静默失效
- Windows：`Add-Type` 被安全策略禁用；PowerShell 有批量删除护栏；`robocopy` 要从 bash 调并加 `MSYS2_ARG_CONV_EXCL=*`
- 工具 shell 无 TTY：`sycode` 只能验到"解析通过 → 报需要终端"；界面观感要人工在真终端看
- 手工验配置时用 `env USERPROFILE=<临时home> HOME=<临时home> ./sycode` 隔离 `~/.sycode`，别写脏真实 home
- 当前 shell 里 `VAR=x cmd | other` 只给 `cmd` 生效：要给被测程序传变量，写成 `... | env VAR=x ./prog`
- **SDK 的 `stream.Err()` 不报截断**：连接在半个分片处断开时仍返回 nil（`eventStreamDecoder` 静默丢半截事件）。
  别用它判断"是否完整"，用 `openai.go` 里的结束信号（`finish_reason`/`usage`）
- `respjson.Field` 只有 `Valid()`/`Raw()`，没有字符串取值：DeepSeek 的 `delta.reasoning_content` 目前读不到（P4 处理）
- `param.Opt[T]` 没有 `Get()`，用 `Valid()` + `.Value`
- Windows 默认**不给普通用户建符号链接**（`A required privilege is not held by the client`）：涉及链接的测试要么替换 `filesystem.evalSymlinks`，要么 `t.Skip` 并在 CI（Linux）上跑
- `go get go-git/v5` 会顺带拉 billy/ssh-agent 等间接依赖；只用 `plumbing/format/gitignore`，不要顺手用它的 Repository（那会真的去开仓库）

## 已定（原待决策，2026-10-29 采纳推荐）

| 事项 | 结论 |
|---|---|
| LICENSE | **MIT**（已写入） |
| memory 容量（P6 专题） | 项目 4KiB / 全局 2KiB（比常见实现 16/8 小一档，窗口留给代码） |
| `cli/repl` 子包节奏 | P1 只建 `stream`/`output`/`theme`，其余随功能出现再建 |
| 工具循环位置 | 集中在 `appstate/turn.go`，不复制进 provider（ADR-001） |
| 质量门形态 | 以 `scripts/check.sh` 为准，`make` 只是薄封装（本机无 make） |
| 退出码口径 | **0 成功 / 1 一般错误 / 2 用法错误**（配置类失败一律 1）。原「缺密钥退出 2」已改，CODE-DESIGN §1 同步 |

## 待决策

| 事项 | 选项 | 我的倾向 |
|---|---|---|
| 文档评审 | 五份文档是否 `APPROVED` | 逐份在首行标注，或把意见写进 `prompts/phase-0/prompt-4_review.md` |

**已定：不推远端**（2026-10-29）。仓库只保留在本地 `D:\project\owesome\code\sycode`，`git remote` 为空；
需要备份时由人工决定（压缩目录或自行加远端），开发流程中的提交仍按单元正常进行。
