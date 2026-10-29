# 实施记录：P4 单元 4.2 后半 —— 恢复 + `--new`

- 日期：2026-10-29
- 结论：完成，质量门与 `-race` 全绿
- 端到端验过：第二次启动日志 `已恢复会话 turns=1` 且会话目录不增加；`--new` 后会话数 1→2

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `session/store.go` | +30 | `Store.Open(id)`：重开已有会话，序号续上 |
| `appstate/state.go` | +35 | `Restore(turns)`：Turn → 交替的 `llm.Message` |
| `agentcore/adapter.go` | +12 | `NewWithSession` 改返回 `*Core`、`(*Core).Restore`、`type Turn = session.Turn` |
| `cli/repl/repl.go` | +25 | `Deps.History`/`Restored`；`NewModel` 把历史落进记录 |
| `cli/cmd/root.go` | +25 | `--new` flag、`BuildEnv(newSession bool)`、`Env.History`/`RestoredNote` |
| `cmd/main.go` | +60 | `openSession`/`resume`：续上次或新建，任何一步失败都降级 |
| 测试 | +170 | session 2 组、appstate 2 组、repl 2 组、cmd 1 组（子用例 2 个） |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| `Store.Open` 的序号取**最后一条有效事件** | 半行里的 `seq:99` 读不出来；若拿它当基准，下一条会跳过一段序号，甚至覆盖已有事件 |
| `Restore` **只放正文** | tool 消息必须挂在一个带 id 与参数的 `tool_calls` 后面，而库里只有摘要。硬塞会让下一次请求的消息序列非法，模型直接报错 |
| 孤立回答（`User` 为空）**不进上下文** | 会造成连续两条 assistant 消息，破坏交替，部分供应商拒收。它仍然显示在屏幕上——显示与上下文是两件事 |
| `NewWithSession` 改返回 `*Core` | 装配完通常要补 `Restore`；把 `Restore` 塞进 `AgentCore` 接口等于为一个启动动作扩大界面契约 |
| `Turn` 用别名 | 与 `PermissionRequest`/`Decision` 同一套做法：界面不必 import 存储层 |
| `--new` 走 `BuildEnv(newSession bool)` | 装配层必须知道这个开关；改签名只影响 main 与三处测试，比在 `Options` 里塞一个"延迟取值的函数"直白 |
| 恢复说明**在最上、分隔线在最下** | 反过来的话用户会把旧内容当成刚发生的；分隔线是"以下才是新的"的界线 |
| 任一步失败**降级** | 恢复读不出来就改为新建，建库失败就不落盘。丢掉的是历史，不该是这次对话 |

## 3. 验证

```
go test ./internal/...（含新增 7 组）        → 全 PASS
CGO_ENABLED=1 go test -race ./internal/...  → 全 PASS
bash scripts/check.sh                       → 全部通过
```

端到端（隔离 home，三次启动）：
1. 首次 → `已新建会话`，目录 1 个
2. 手工往 JSONL 补一个 user/assistant 轮次 → 再启动 → `已恢复会话 turns=1`，目录仍是 1 个
3. `--new` → `已新建会话`，目录变 2 个

**没验到的**：真 TUI 里"恢复的历史"的观感（说明行、分隔线与后续流式内容的衔接）要人工在真终端看；
真跑模型两轮再重启（需要真 key）。

## 4. 遗留

- **恢复的历史没有工具痕迹**：模型不知道上次跑过什么，用户看得到。要让模型也知道得改
  `AssistantTurn` 的存储格式（存完整 `tool_calls`），属改存储契约，须先问
- 空会话也会被"恢复"（turns=0）：行为上是对的（继续用同一个会话，不堆积新目录），
  但 4.5 的 `/sessions` 列表里会出现 0 轮条目，届时可以过滤掉
- 会话文件没有上限，也还没有回收：`cleanup` 目前只清 artifact。等 P6 的上下文管理定下"回放多少"再接
