# 实施记录：P2 单元 2.4 `internal/llm` 核心类型与系统提示

- 日期：2026-10-29
- 结论：完成，质量门全绿；4 文件 328 行 + 259 行测试，不含 HTTP/SSE（属 2.5）

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/llm/client.go` | 20 | 包注释 + `Client` 接口与三条 channel 契约 |
| `internal/llm/request.go` | 65 | `Role`(+3 常量)、`Message`、`ToolCall`、`ToolSchema`、`Request`（纯类型） |
| `internal/llm/stream_events.go` | 92 | `EventType`(8 类)、`Usage`、`StreamEvent`、`IsTerminal`、`eventOpt` + `newEvent`/`errEvent`/`doneEvent` |
| `internal/llm/systemprompt.go` | 151 | `Env`、`Build`、`identitySection`/`envSection`/`archSuffix`/`projectSection`/`truncateUTF8` |
| `stream_events_test.go` | 126 | 6 组：字段互不污染、用量按值拷贝、`errEvent` 错误链与 nil 兜底、`doneEvent` 无用量、终态判定排除增量 |
| `systemprompt_test.go` | 133 | 6 组：身份+环境齐备、空字段省略、注入/缺失 AGENTS.md、超限截断、`truncateUTF8` 6 个边界 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| `Request.System` 用 `string`，不按 ARCHITECTURE §4.1 的 `[]SystemBlock` | 分段原本为提示缓存，但 OpenAI 兼容协议的缓存按前缀自动命中，分段无收益；`Build` 内部仍分段拼装，逐段可测。已回改 ARCHITECTURE §4.1 |
| `Request.Tools` 用 llm 自有的 `ToolSchema`，不 import `tools` | `tools` 包要到 P3 才存在（本单元编译都过不了）；且 llm→tools 会让"工具加字段"牵动模型层。转换由 appstate 组装请求时做一次，`deps_test` 的 `tools → llm` 禁边因此更干净 |
| `ToolCall.Arguments` 存原始 JSON 文本 | 模型会给出非法/半截 JSON，解析失败该由工具层报"参数错误"并让模型重试；llm 层不该替他决定怎么补救 |
| 系统提示用中文 | 界面、错误、将来的工具描述都是中文；提示词跟着统一，模型不会一半中文一半英文。要换语言只改 `identitySection` |
| `errEvent(nil)` 兜底成一句说明 | 让消费方可以假定"error 事件的 `Err` 一定非 nil"，少一处 nil 判断就少一个空指针 |
| `withUsage` 收值类型并取址 | 事件发出后调用方再改自己的变量，不影响已入队的事件（有测试压住） |
| 工具约定段**不写**（尽管 CODE-DESIGN 顺序里有） | P2 阶段没有工具，写了模型会以为自己能调，反而产生幻觉调用。P3 与工具一起进 |
| `truncateUTF8` 而不是 `s[:limit]` | 中文按 3 字节算，直接切会切出半个汉字变 U+FFFD；这条有 6 个边界的表驱动测试 |

## 3. 验证

```
go test ./internal/llm/ -v   → 12 组全 PASS
bash scripts/check.sh        → 全部通过（-race 本机无 gcc 跳过）
```

人工看了一眼拼装结果（临时 test 打印后删除），本仓库自身的 `AGENTS.md`（4,049 字节）被完整注入，
段落顺序为 身份 → 环境 → 项目指令，`Current shell: bash` 保留 ADR-006 的英文原文。

## 4. 遗留

- `request.go` 只有类型、没有函数，因此没有测试文件；输入校验（模型名空、消息角色非法）
  属 `NewOpenAI` 的职责，放 2.5，避免现在写一份没人调用的 `Validate`。
- 工具约定段待 P3 补，位置在身份段之后、环境段之前（保持 CODE-DESIGN 的段序）。
- 系统提示目前没有任何"输出格式"约束（如 markdown 与否）；等 REPL 渲染（2.7）定了再补。
