# 实施记录：P3 单元 3.6 `appstate` 工具循环

- 日期：2026-10-29
- 结论：完成，质量门全绿；`state.go` 102 + `turn.go` 319（测试 579），`agentcore` 换成薄包装（50 + 161）
- 真机冒烟：二进制启动后 `工具已注册 count=6`

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `appstate/state.go` | 102 | `Deps`、`State`、`New`、`Messages`/`Clear`/`Interrupt`、`maxTurns`/`appendMessage`/`setCancel` |
| `appstate/turn.go` | 319 | `Submit`、`runTurn`、`buildRequest`、`toLLMTools`、`handleEvents`、`executeTools`、`executeOne`、`parseInput`/`decodeJSON`、`summarizeResult`/`firstLine`/`itoaBytes`、`toolNames`、`emit` |
| `appstate/turn_test.go` | 579 | 17 组：单轮、工具一轮、失败回灌、连续失败提示、轮次上限、流错误、构造失败、空输入、无客户端、未知工具、坏参数、schema 顺序、中断、清空、副本、真实 Guard + read_file |
| `agentcore/adapter.go` | 50 | `Core`（转发 `appstate`）+ `New`；**删除 P2 的 `Direct`** |
| `agentcore/adapter_test.go` | 161 | 4 组：转发、Clear、Interrupt、系统提示确实传进去了 |
| `llm/stream_events.go` | +7 | `EventToolResult`（appstate 发：执行结束） |
| `cmd/main.go` | +45 | `buildTools`（注册 6 个工具）；`promptEnv` 收 `workDir` 参数；装配 Guard/注册表/`agentcore.New` |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **终态事件只在 `runTurn` 发一次** | provider 的 done/error 只是"这一轮流结束"，中间还要经过工具执行。直接透传会让界面在工具执行前就收尾 |
| `handleEvents` 不转发 done/error | 同上；它只转发增量、`tool_start`、`EventRetry` 等，并在终态时把结果交回循环 |
| assistant 消息**必须带 `ToolCalls` 整条入历史** | 否则下一轮消息序列不合法：tool 消息找不到对应的调用 |
| **工具失败也回灌给模型** | 只往界面报错、模型那边一片空白，它下一轮只会重复同一个调用；把 `参数错误：…` 交给它，它才知道怎么改 |
| 连续 3 次失败在结果里追加提示 | 模型最常见的坏习惯是换个措辞重试同一条命令；与其让它继续试，不如直接点出来 |
| **循环里不 `recover`** | 工具被要求永不 panic；用 recover 兜住会把真 bug 变成"工具老是失败"，更难查 |
| `EventToolResult` 单开一个事件类型 | `tool_start`/`tool_end` 说的是"模型要调什么"，执行结果是另一件事；混用会让界面靠猜 |
| 权限判定留在工具内部（`askAccess`） | 循环里按工具名分支会越长越乱；Guard 三态到文案的映射只该有一处（ADR-005） |
| **删除 `Direct`** | 历史与循环只能有一处实现，留两套必然分叉。`repl` 一行没改，验证了 P2 留缝的价值 |
| P3.6 的 `perm` 传 nil | 还没有权限弹窗；nil = 只读模式（写/执行被拒并说明原因）。这是安全默认值，3.7 接弹窗 |

## 3. 验证

```
go test ./internal/appstate/ ./internal/agentcore/ → 17 + 4 组全 PASS
bash scripts/check.sh                              → 全部通过（含 deps_test 依赖方向断言）
```

二进制冒烟（临时 home 隔离配置）：启动后日志 `工具已注册 count=6`，随后按预期停在 TTY 检查 —— 说明
Guard、注册表、引擎整条装配都通了。

端到端关键一条：`TestSubmitRunsToolThenAnswers` 用假客户端跑了两轮，断言第二轮请求里
`user / assistant(带 tool_calls) / tool` 三条都在，且工具结果真的进了 tool 消息。
另有 `TestToolUsesRealGuardAndFilesystem` 用真 `read_file` + 真 Guard 跑通一次完整回合。

## 4. 遗留

- `perm` 为 nil → 写与执行目前一律被拒（只读模式）。3.7 的权限弹窗会把它接上。
- 会话落盘（`session`）还没接：历史只在内存里，退出即丢（P4）。
- 上下文预算/压缩没有：历史无限增长，先靠 25 轮上限兜住（P4/P6 专题）。
- `Display` 里的 diff/list 目前界面还没渲染（`repl` 只渲染正文），3.7 一起做。
