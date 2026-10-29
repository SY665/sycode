# 当前状态

更新时间：2026-10-29 19:35 | 阶段：**P2** | 当前单元：2.5 已完成，下一单元 2.6（重试与错误分类）

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
- 代码 3,632 行（源码 1,764 / 测试 1,868），17 笔提交；直接依赖 6 个（cobra/bubbletea/bubbles/lipgloss/yaml.v3/openai-go）
- 记录：`output-2/3/4/5_impl-*.md`（阶段 2）、`output-1..5_impl-*.md`（阶段 1）

## 下一步第一件事（具体到文件与函数）

**P2 单元 2.6 —— 重试与错误分类**（`internal/llm/retry.go` + `estimate.go`，2 个文件）：

1. `retry.go`：`Do(ctx, attempts int, fn func(context.Context) error, onRetry func(attempt int, err error)) error`
   —— 指数退避（1s/2s/4s，带抖动），只重试 `isRetryable`，最后一次错误原样返回
2. `isRetryable(err error) bool`：429/5xx/网络可重试；400/401/403/404 不重试。判定基于 `httpStatusError.Status`（已在 2.5 就位）
3. `estimate.go`：`Estimate(s string) int`（按 字符数/3 + 4 的粗估，**注释里必须写明是估算**）、`EstimateMessages`
4. **关键设计问题（先想清楚再写）**：`StreamChat` 已经是流式的，"重试"不能简单包一层——
   只有**在产出第一个正文增量之前**失败才能重试；一旦已经吐字，再重试会让用户看到重复内容。
   倾向做法：把重试放进 `pump`（记录 `emitted bool`），重试时发 `EventRetry{Attempt:n}` 让 UI 显示进度。
   这会让 `openai.go` 增加约 30 行，属于本单元而不是 2.5 的债。
5. 验收：429 三次后报错且 UI 收到 3 个 `EventRetry`；400 一次就失败；吐字之后失败不再重试

## 已知坑 / 需要绕过的东西## 已知坑 / 需要绕过的东西

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
