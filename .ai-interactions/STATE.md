# 当前状态

更新时间：2026-10-29 18:55 | 阶段：**P2** | 当前单元：2.4 已完成，下一单元 2.5（`internal/llm/openai.go` SSE）

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
- 代码 2,671 行（源码 1,321 / 测试 1,350），16 笔提交
- 记录：`output-2/3/4_impl-*.md`（阶段 2）、`output-1..5_impl-*.md`（阶段 1）

## 下一步第一件事（具体到文件与函数）

**P2 单元 2.5 —— `internal/llm/openai.go` SSE 流式解析 + `errors.go`**（2–3 个文件）：

1. `OpenAIConfig{BaseURL, APIKey, Model}` + `NewOpenAI(cfg) (*OpenAI, error)`：只校验（baseURL 可解析、model 非空），**不发请求**
2. `StreamChat(ctx, req)`：起 goroutine，返回只读 channel；发送方关闭；`EventDone`/`EventError` 必为最后一个
3. `parseSSE(ctx, r io.Reader, out chan<- StreamEvent) error`：`data:` 分片、跨行块、`[DONE]`、`event:` 行忽略
4. `toOpenAIMessages` / `toOpenAITools`：纯转换，单测直接比结构
5. `errors.go`：`Translate`（英文原始错误 → 中文，`%w` 保留链）+ `httpStatusError{Status, Code, Body}`
6. 验收：`httptest` 回放四类流——正常、跨行分片、中途断开、缺 `[DONE]`

先确认要不要引 `github.com/openai/openai-go`（CODE-DESIGN §5 的 `toOpenAIMessages` 用了它的类型）。
**引第三方 SDK 属于表外依赖，必须先问用户**；不引就自己写 `encoding/json` + `bufio.Scanner` 解析 SSE（约 150 行，可控）。

## 已知坑 / 需要绕过的东西

- 工具 shell 的 PATH 被裁剪：`go` 不可见 → 先 `source scripts/env.sh`（不是没装）
- `go build ./...` 会把 main 包二进制写到当前目录 → `scripts/check.sh` 已改成 `-o <临时目录>/`
- `-race` 需要 cgo + gcc：`/d/mingw64/bin` 进 PATH 后 `CGO_ENABLED=1`；否则脚本跳过并提示
- 版本号必须是 `var version`，`-X` 对 `const` 静默失效
- Windows：`Add-Type` 被安全策略禁用；PowerShell 有批量删除护栏；`robocopy` 要从 bash 调并加 `MSYS2_ARG_CONV_EXCL=*`
- 工具 shell 无 TTY：`sycode` 只能验到"解析通过 → 报需要终端"；界面观感要人工在真终端看
- 手工验配置时用 `env USERPROFILE=<临时home> HOME=<临时home> ./sycode` 隔离 `~/.sycode`，别写脏真实 home
- 当前 shell 里 `VAR=x cmd | other` 只给 `cmd` 生效：要给被测程序传变量，写成 `... | env VAR=x ./prog`

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
