# 实施记录：P3 单元 3.7b 展示渲染

- 日期：2026-10-29
- 结论：完成，质量门全绿（含 `-race`）
- 范围：只动 `appstate` 的文本产出与 `repl` 的渲染，**不改任何契约**

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `appstate/turn.go` | +48 | `summarizeResult` 改为产出「首行摘要 + 展示块」；新增 `displayBlock`（`maxDisplayLines=12` 截断并说明）；`maxDisplayLines` 常量 |
| `appstate/turn_test.go` | +92 | `toolResults`/`mustSubmit` 两个 helper；3 组：展示块随事件下发、超限截断、无 Lines 时保留 Title |
| `cli/repl/repl.go` | +47 | `EventToolResult` 分支、`appendToolResult`、`displayLine`、`addedStyle`/`removedStyle` |
| `cli/repl/repl_test.go` | +78 | 3 组：结果行排在正文之后且 flush 正文、diff 行分别走绿/红/暗三个分支、失败行不用暗色 |
| `llm/stream_events.go` | 注释 | `EventToolResult.Text` 的语义写明是「首行摘要 + 展示块」 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **不给 `llm.StreamEvent` 加 `Display` 字段** | 那要 `llm` 认识"给人看的 diff"（还得再立一套 `llm.Display` 类型以避免 `llm → tools` 依赖）。而 `Display.Lines` 本来就是拼好的文本行，压成字符串不丢信息 |
| 约定只有一条：**首行是摘要** | 界面不必知道展示是 diff 还是列表；`displayLine` 只看行首 `+ `/`- ` |
| 展示块在 **appstate 截断**（12 行），不在界面 | 截断属于"给模型/给人的信息取舍"，和 `clip` 的 32KiB 上限是同一类决定，放一处；界面只管画 |
| `appendToolResult` **先 flush 正文** | 与 `showPermission` 同一个理由：模型说"我来改"→ 工具结果，顺序不能倒 |
| 失败行用**红色**而不是暗色 | 暗色在同一位置上看起来像又一条进展，用户会读漏失败 |
| `tool_start` 不渲染 | 它与 `tool_result` 说的是同一次调用；一起显示就是重复 |
| 标题保留（即使没有 Lines） | `glob` 的"无匹配"只靠这一行；否则界面只剩"glob 完成"，看不出是没找到还是找到了空文件 |

## 3. 验证

```
go test ./internal/appstate/ ./internal/cli/repl/         → 全 PASS（+6 组）
CGO_ENABLED=1 go test -race 同上 + agentcore              → 全 PASS
bash scripts/check.sh                                     → 全部通过
```

**没验到的**：真终端里的观感（绿/红的实际色值、`⎿` 在不同字体的对齐、12 行展示块与窗口高度的配合）。
`displayLine` 的断言比较的是"哪一行走哪个 Style"，不是 ANSI 序列——具体色值要人工看。

## 4. 遗留

- 长路径 / 长行不换行：窄窗口下 diff 行会折行（终端自己折），对齐会乱 → 留给 P4 的展示打磨
- `read_file` 的展示块只有 12 行，用户想看全文仍需自己打开文件；将来可以做成可展开区域
- 3.8 还没做：`internal/cleanup`（`~/.sycode/bash/` 等 artifact 保留 7 天）
