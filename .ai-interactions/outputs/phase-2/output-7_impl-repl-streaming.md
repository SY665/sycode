# 实施记录：P2 单元 2.7 REPL 流式渲染 + `Esc` 中断

- 日期：2026-10-29
- 结论：完成，质量门全绿；`repl.go` 212→367、`agentcore` 新增 166（分两笔提交）、`main.go` 173、`root.go` 252
- 按 STATE 里的倾向选了 **(a)**：先建 `agentcore` 桩，`appstate` 留到 P3

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/agentcore/agentcore.go` | 30 | `AgentCore` 接口（`Submit`/`Interrupt`/`Messages`/`Clear`） |
| `internal/agentcore/adapter.go` | 136 | P2 直连实现 `Direct`：历史、取消句柄、`forward`、`recordAnswer` |
| `internal/agentcore/adapter_test.go` | 320 | 10 组：历史增长、构造失败不入历史、事件原样转发、空回答不记、中断保留半截回答、`Messages` 返回副本、分片拼接、取消传播 |
| `internal/cli/repl/repl.go` | 367 | `Deps.Core/Ctx`、`submit` 接 Core、`waitEvent`、`handleStreamEvent`、`finishTurn`、`interrupt`、流式渲染 |
| `internal/cli/cmd/root.go` | 252 | `Env{Model, Core}` + `Options.BuildEnv`（原 `ResolveModel`） |
| `cmd/main.go` | 173 | `buildEnv`：配置 → 密钥 → `llm.NewOpenAI` → `systemprompt` → `agentcore.NewDirect`；`promptEnv`/`detectShell` |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **界面绝不 import `llm.Client`**，只认 `agentcore.AgentCore` | 这是 P3 插入 `appstate` 的缝。若 repl 直接调 client，工具循环一上来界面就得跟着改；现在它一行都不用动 |
| `agentcore.Direct` 在 P2 承担"组请求 + 存历史" | 这两件事本来属于 `appstate`，但 P3 才有它。作者把 `Direct` 明确标注为 **P3 会被取代**，接口不变，避免临时实现变成事实标准 |
| 用户消息**在请求成功后才入历史** | 请求都发不出去的那次不该留一条没人回答的提问，否则下一轮模型会看到一个孤零零的用户消息 |
| 中断也把半截回答写进历史 | 用户已经看到的内容，下一轮模型也该看到；否则它会以为上一条回答不存在，接着说出自相矛盾的话（PRD F-LLM-6） |
| 空回答不写历史 | 部分兼容端点会拒绝空 assistant 消息；空回合也不教模型任何东西 |
| `Esc` **不立刻**改 `streaming` | 真正收尾要等流给出终态或关闭。立刻改会让"界面说已中断、其实还在收事件"，两处状态不一致 |
| 流式期间拒绝新输入 | 两股回答会缠在同一个 transcript 里，用户分不清哪句属于谁 |
| `streamEventMsg` 包一层再进 `Update` | `tea.Msg` 是 `any`，直接塞 `llm.StreamEvent` 容易与其它库的消息型别名撞车，也不容易看出是界面自己的消息 |
| channel 关闭但无终态 → 提示"意外结束" | 静默吞掉会让用户以为答案就这么多 |
| `detectShell` 在 main 里先写一份 | ADR-006 要求系统提示带 `Current shell:`，P3 的 `tools.resolveShell` 还没写。顺序照抄 ADR-006 并在注释里写明"P3 接管"，避免两处结论不同 |

## 3. 验证

```
go test ./... → 8 个包全 PASS（agentcore 10 组新增）
bash scripts/check.sh → 全部通过（-race 本机无 gcc 跳过）
```

真实二进制（临时 home 隔离配置）：

| 场景 | 结果 |
|---|---|
| 正常配置 + key | 走到 `错误：交互式界面需要终端（stdin 不是 TTY）` —— 说明装配（客户端 + 引擎 + 系统提示）整条走通了 ✅ |
| `base_url` 写成 `ftp://…` | `错误：端点地址非法 "ftp://example.com/v1"：需要形如 https://api.openai.com/v1 的 http(s) 地址` —— 装配期就拦住 ✅ |

**未做**：真机 TTY 下的目视（工具 shell 没有 TTY），以及真实端点的流式问答。前者需要人开一个终端跑 `sycode`，后者要有可用密钥。

## 4. 遗留

- `appstate`（P3）会把 `Direct` 的"组请求 + 存历史"接过去；届时 `agentcore.New` 取代 `NewDirect`，`repl` 不动。
- `Deps.Ctx` 是权宜：`Run` 把程序 ctx 塞进依赖。P4 引入会话与恢复后，ctx 生命周期可能要跟会话走。
- 界面还没显示 token 用量（`EventUsage` 收到了但只丢弃），状态栏是 P4 的事。
- `agentcore.Direct` 不处理工具调用事件（P2 没有工具）；P3 接线时会改 `forward`。
- `run` 子命令（headless）仍是"尚未实现"（P4），P2 的退出条件里那条路径还没通。
