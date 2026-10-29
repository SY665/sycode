# 实施记录：P3 单元 3.7a 权限弹窗（通路 + 弹窗）

- 日期：2026-10-29
- 结论：完成，质量门全绿（含 `-race`，用 `/d/mingw64/bin` + `CGO_ENABLED=1`）
- 开工前问过人两件事：① 权限通路是否改用独立对象（**是**）；② 3.7 是否拆 a/b 两笔（**是**）

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `agentcore/permission.go` | 140 | `PermissionPrompt`（`RequestPermission` / `Requests` / `Answer`）、`sessionKey`、`PermissionRequest`/`Decision` 别名 |
| `agentcore/permission_test.go` | 180 | 6 组：问与答、会话缓存命中、跨目录重新询问、ctx 取消、「无请求时 Answer」、并发询问快速失败 |
| `agentcore/permission_integration_test.go` | 84 | 真 `write_file` + 真 `Guard`：允许后真的落盘、拒绝后磁盘零副作用 |
| `cli/repl/repl.go` | +129 | `Deps.Permission`、`Model.pending`、`Init` 挂等待、`showPermission`/`answerPermission`/`respond`/`permissionNote`/`opText`/`renderPermission`/`waitPermission`、弹窗顶替输入框 |
| `cli/repl/repl_test.go` | +171 | 6 组：弹窗内容、吞掉按键、作答回传、会话级留痕、Esc 拒绝不退出、Ctrl+C 拒绝并退出 |
| `cli/cmd/root.go` | +10 | `Env.Permission` 转交 `repl.Deps` |
| `cmd/main.go` | +31/-6 | `NewPermissionPrompt` 建通路、`buildTools(guard, perm)` **把 perm 真正传给 6 个工具** |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **不给 `AgentCore` 加 `RequestPermission`** | `Core` 只转发 `appstate`，没法把问题送回界面；那要求反向持有 UI，而 main 必须先建 Core 再建界面 → 接线成环。独立通路对象由 `cmd.Env` 一并转交两侧，接口契约不变 |
| 通路对象**同时是权限缓存** | `DecisionAllowSession` 的语义是"本会话"，缓存跟着通路走（repl 重建不丢），也不需要一个只为转发存在的 adapter |
| 缓存键按**目录**而非文件 | 一次任务常连着改同目录好几个文件；逐个弹窗只会训练用户闭眼点允许。exec 的路径固定 `.`，等于"本会话允许执行"，弹窗文案因此必须把范围说清 |
| ctx 取消返回 **Deny + 错误** | 把"没等到答复"与"用户明确拒绝"分开：前者是中断，后者是个决定，回给模型的文案不一样 |
| 并发询问**快速失败** | 覆盖等待槽会让第一个请求永远等下去；串行假设被打破时宁可报错 |
| `PermissionRequest`/`Decision` 用 **tools 的类型别名** | 界面不必 import `tools`，也不产生第二套同构类型（对比：`llm.ToolSchema` 与 `tools.Schema` 是两套，那是因为 `tools` 不能 import `llm`；这里没有环） |
| 弹窗**直答**，不做「移动选中 + 回车」 | 弹窗是阻塞的，多一层选中态就多一类状态不一致（答案与高亮不同步）；三个选项一看就能选 |
| `showPermission` **先 flush 正文** | 模型说"我来改这个文件" → 弹窗 → 记录，顺序必须对；不 flush 会让权限记录排在模型那句话上面 |
| 弹窗期间按键**不透给输入框** | 否则用户以为在打字，实际把回答漏掉了 |
| Esc 在弹窗里是**拒绝**、不是中断回合 | 用户想取消的是这个操作，不是整段回答 |

## 3. 验证

```
go test ./internal/agentcore/ ./internal/cli/repl/          → 全 PASS
CGO_ENABLED=1 go test -race 同上两个包                       → 全 PASS
bash scripts/check.sh                                       → 全部通过
```

关键一条是 `TestWriteToolAsksThenWrites`：它验的是**接线**而不是通路本身——工具构造时拿到的
perm 必须真是界面那条通路。这正是本次修掉的真 bug：此前 `buildTools` 给 6 个工具全传 `nil`，
于是"只读模式"并不是设计选择，而是接线漏了。

**没验到的**：TUI 观感（弹窗布局、按键手感、流式正文与弹窗的视觉衔接）需要人工在真终端看；
工具 shell 无 TTY，只能验到"报需要终端"。

## 4. 遗留

- `Display` 的 diff/list 还没渲染，`EventToolResult` 也没在界面显示 → 3.7b
- `appstate.Deps.Perm` 目前没有被循环使用（权限判定在工具内部），保留是给 P4 的 headless 入口留位
- 弹窗宽度固定文案，超长路径不换行（3.7b 与 Display 渲染一起处理）
- 本单元源码 diff ≈ 310 行，略超 300 行上限；拆开的话会留下"弹窗没有人回答"的半截状态，故一笔做完（已获同意）
