# 实施记录：P4 单元 4.4 `/context`

- 日期：2026-10-29
- 结论：完成，质量门全绿；冒烟实测固定开销 **2795 token / 128k**

## 2. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `llm/estimate.go` | 38 | `EstimateTokens`（字节/3）、`EstimateJSON` |
| `providers/registry.go` | +12 | `GetModelContextWindow`（带"是否已知"） |
| `appstate/turn.go` | +7 | 终态前补发 `EventUsage` |
| `cli/repl/context.go` | 130 | `contextUsage`/`contextUsageFor`/`renderContext`/`contextTotalLine`/`formatTokens`/`padLabel` |
| `cli/repl/repl.go` | +12 | `/context` 分支、`lastPromptTokens`、收 `EventUsage`、`Deps` 两个常量 |
| `cli/cmd/root.go` | +8 | `Env`/`Deps` 转交 `ContextBase`/`ContextWindow` |
| `cmd/main.go` | +25 | `baseContextTokens`、窗口查询、debug 日志 |
| 测试 | +190 | llm 2 组、providers 2 组、appstate 2 组、repl 5 组 |

## 3. 设计决定

| 决定 | 理由 |
|---|---|
| 固定开销在**装配时算一次** | 系统提示 + 6 个工具 schema 有 2795 token，每敲一次 `/context` 重算一遍没有意义 |
| `EventUsage` 由 **appstate 补发** | provider 的 usage 是随 done 回来的；界面要一个数字，不该为此扩 `AgentCore` 接口。事件流本来就是唯一通道 |
| 分类明细**界面自己算** | 只有 `/context` 要用；放引擎里等于为展示细节扩接口 |
| 窗口未知 → "上限未知" | 显示 0% 会让人误判"还有一大截"。这是常见实现 `KnownWindow` 的同一判断 |
| 有上报值就**不标 `≈`**，并写"（供应商上报）" | 分类明细仍是估的，所以只在合计行去掉 ≈；说清来源比追求好看重要 |
| `EstimateTokens` 用**字节/3** | 中文一字约一 token 恰好接近；英文高估 1/4，但高估只会让提醒来得早 |
| `/context` 不写进对话记录 | 它是命令不是输入；`submit` 里在 `append("› …")` 之前就分派 |

## 4. 验证

```
go test ./internal/{llm,providers,appstate,cli/repl}/ -v  → 11 组新增全 PASS
bash scripts/check.sh                                    → 全部通过
真二进制冒烟                                              → 上下文占用基线 base_tokens=2795 window=128000 window_known=true
```

**没验到的**：真 TUI 里 `/context` 的对齐效果（中文标签按"字数"补齐，等宽字体下应齐，但不同终端可能差半个字）。

## 5. 遗留

- 没有"剩余额度"行与 70% 提醒：前者在百分比里隐含，后者等 P6 的 `/compact`
- 上报值只在**回合正常结束**时才有；`Esc` 中断的回合没有 usage，`/context` 继续显示估算
- 状态栏常驻显示（常见实现有）我们没做：等 P4.5 的命令表出来再考虑把它挂在输入框上方
