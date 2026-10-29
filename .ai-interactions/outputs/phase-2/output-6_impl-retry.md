# 实施记录：P2 单元 2.6 重试与错误分类

- 日期：2026-10-29
- 结论：完成，质量门全绿；`retry.go` 166 行 + 222 行测试；`estimate.go` 按 §2 第 5 条**主动延后**

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/llm/retry.go` | 166 | `Do`、`isRetryable`、`retryableStatus`、`backoffDelay`、`sleep`、`partialError`、`errStreamTruncated`；常量 `maxRetries=3`/`maxBackoff=30s`、变量 `backoffBase` |
| `internal/llm/retry_test.go` | 222 | 9 组：退避区间与封顶、14 例可重试判定、包装后错误链、重试成功、不可重试只试一次、次数用尽、取消即返回、attempts<1 |
| `internal/llm/openai.go` | 337→368 | `StreamChat` 只建 channel → `run`（重试的唯一位置）+ `pump`（返回 `pumpOutcome` 与错误，不再发终态） |
| `internal/llm/openai_test.go` | 408→520 | +3 组：429 重试后成功且不重复吐字、400 不重试、吐字后截断不重试 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **重试放在 `run`，不包一层 `StreamChat`** | 流式接口一旦吐字就无法"撤回"。只有第一次尝试什么都没产出时，重试对用户才是透明的。`run` 是唯一知道 `emitted` 的地方 |
| 用 `partialError` 表达"吐字后失败" | 需要一个"对 `Translate` 透明、对 `isRetryable` 说 no"的标记：`Unwrap` 保住错误链（用户拿到"服务端故障"的指引），`Error()` 原样透出 |
| `isRetryable` 额外认 `*oai.Error` | **这是本单元唯一的真 bug**：重试判定发生在 `Translate` 之前，此时错误还是 SDK 的原始类型；只认 `httpStatusError` 会让 429 一次就失败——测试 `TestStreamChat_RetriesBeforeFirstToken` 直接暴露（重试事件为空） |
| 抖动"加完也不越过封顶" | 第一版先封顶再加抖动，`backoffDelay(20)` 算出 38s，被 `TestBackoffDelay` 抓到。改成 `delay+jitter > maxBackoff` 时返回 `maxBackoff` |
| 首次重试不加抖动 | 首次等待要可预期（1s），否则用户会觉得"点了没反应还乱等" |
| `maxRetries=3` 含首次 = 4 次请求？**否**：`Do(ctx, maxRetries+1, ...)` | 按 PRD F-LLM-5 的"重试 3 次"字面：1 次首发 + 3 次重试，UI 最多看到 3 个 `EventRetry` |
| `backoffBase` 用变量 | 测试要把它压到毫秒级，否则每个重试用例真等 1 秒起。已在注释里写"生产不要改"——这是本单元唯一为可测性让路的地方 |
| 取消/超时**不**重试 | `Esc` 按了就该停；超时是本端设定，再试仍超时，只是多等一轮 |
| `estimate.go` 延后 | 现在没有任何消费方（`Estimate` 只有测试会调），属于"新增导出却没人用"（CODE-DESIGN §13）。等 P4 上下文预算 / P6 压缩需要时再做，CODE-DESIGN 已改注"延后" |

## 3. 验证

```
go test ./internal/llm/ -v   → 40 个顶层测试（含子测试 78 个）全 PASS
bash scripts/check.sh        → 全部通过（-race 本机无 gcc 跳过）
```

三条边界都有 `httptest` 回放压住：

| 场景 | 期望 | 实测 |
|---|---|---|
| 先 429 再正常流 | 1 个 `EventRetry{Attempt:1}` → 正文只出现一次 → `done`；2 次请求 | ✅ |
| 400 | 不重试、1 次请求、`error` 收尾 | ✅ |
| 先吐"你"再断流 | 不重试、1 次请求、正文只有"你"、错误含"中断" | ✅ |

第三条用的是"若发生重试会返回另一段文本"的服务器：任何多余的重试都会让 `textOf` 断言失败，而不是靠数事件数蒙过去。

## 4. 遗留

- `EventRetry.Attempt` 语义定为"刚失败的第几次尝试"（从 1 开始）。2.7 渲染时文案建议："第 N 次失败，正在重试…"
- REPL 还没消费 `EventRetry`（2.7 一起接）。
- 退避常量未做成配置项：真出现"某网关要求更长退避"再说，现在加配置是提前抽象。
- 端到端（真实端点）仍待 2.7 接线后验证。
