# 实施记录：P2 单元 2.5 `internal/llm/openai.go` 流式客户端与错误翻译

- 日期：2026-10-29
- 结论：完成，质量门全绿；4 文件 443 行 + 518 行测试；新增 1 个直接依赖（选型表已批准）
- 与前一份计划的一处偏离：**没有自己写 SSE 解析**（见 §2 第 1 条）

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/llm/openai.go` | 337 | `OpenAIConfig`/`OpenAI`/`NewOpenAI`、`StreamChat`、`buildParams`、`pump`、`emit`/`emitCalls`、`toOpenAIMessages`/`toOpenAIToolCall`/`toOpenAITools`、`toolCallBuffer` |
| `internal/llm/errors.go` | 106 | `httpStatusError`（含 `Unwrap` 与按状态码的 `statusHint`）、`Translate` |
| `internal/llm/openai_test.go` | 408 | 10 组：正常流 + usage、缺 `[DONE]`、截断、401、分片工具调用、取消不泄漏、空请求、构造校验 5 例、`buildParams` 2 例、消息转换 |
| `internal/llm/errors_test.go` | 110 | 7 组：7 种状态码的指引、code/body 去重、取消与超时、连接失败、nil、幂等、未知错误保留链 |
| `go.mod` | — | +`github.com/openai/openai-go/v3 v3.90.0`（直接依赖 6 个，上限 15） |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **不自己写 `parseSSE`**，用 SDK 的 `packages/ssestream` | 选型表就是为"一份实现覆盖所有兼容端点"选的 SDK（ADR 里也明确否决了"直连 HTTP"）。自己再写一份 SSE 解码只会重复，且 SDK 那份已经把分块边界、`\r\n`、超大 token 都处理了。CODE-DESIGN 已删掉 `parseSSE` 并注明原因 |
| 截断识别用"结束信号"而不是 `stream.Err()` | **实测**：连接在半个分片处断掉时 SDK 的 `Err()` 仍是 `nil`——`eventStreamDecoder.Next` 在扫描结束后只要 `scn.Err()` 为空就返回 false，没空行收尾的半截事件被静默丢弃。于是改用"收到过非空 `finish_reason` 或 `usage` 才算正常结束"，否则发 `EventError`。宁可提示"可能不完整"，也不能把半句话当完整回答 |
| `NewOpenAI` 显式 `option.WithAPIKey`，即便为空 | SDK 默认会读 `OPENAI_API_KEY` / `OPENAI_BASE_URL`。不显式覆盖就会出现"改了 configs.json 却不生效、因为环境变量在后面偷笑"这种极难排查的问题 |
| `option.WithMaxRetries(0)` | 重试是 2.6 的职责，且要发 `EventRetry` 让 UI 显示第几次。SDK 自己重试会让次数翻倍，而用户看不到 |
| HTTP 层失败也走事件流 | 契约写的是"只有请求构造不出来才返回 error"。让 401/连不上也走 `EventError`，消费方只需处理一条路径 |
| `toolCallBuffer` 按 `index` 归并，`tool_start` 发**快照** | 分片协议里 id/name 只来一次、`arguments` 逐段拼。若 `tool_start` 持有指针，后续追加会把已发出的事件改写（有测试压住 `{"pa` → `{"path":"a.go"}`） |
| 未知角色退回 `user` | 丢弃一条消息会让模型看到断掉的历史，比角色不精确危险得多 |
| `errEvent` 文案走 `Translate` 而不是直接透传 | 401 的英文原文对用户无用；`Translate` 给"该去查密钥" |
| `statusHint` 按状态码分类 | 用户要的是动作不是语义。401/403 → 密钥、404 → base_url/model、429 → 稍后重试、5xx → 服务端故障 |

## 3. 抓到的坑

1. **`stream.Err()` 不报截断**（见上）。这是本单元最值得记的一条：SDK 的"正常结束"与"半截中断"在 API 上是同一个返回值。
2. **`respjson.Field` 没有字符串取值方法**，只有 `Valid()` 与 `Raw()`（返回带引号的原始 JSON）。
   影响：DeepSeek 的 `delta.reasoning_content` 暂时读不到，`EventReasoning` 在 P2 不会出现。
   留给 P4 做 thinking 档位时一并处理（届时用 `Raw()` + `json.Unmarshal`，或升级 SDK 看有没有 typed 字段）。
3. `param.Opt[T]` 没有 `Get()`，用 `Valid()` + `.Value`（测试第一版写错，编译期抓到）。

## 4. 验证

```
go test ./internal/llm/ -v   → 29 组（含子测试）全 PASS
bash scripts/check.sh        → 全部通过（-race 本机无 gcc 跳过；cmd 包的 deps_test 仍通过）
```

`httptest` 回放覆盖：正常流含 usage、缺 `[DONE]`（应正常收尾）、半点截断（应报错）、401 JSON（中文 + 状态码 + 指引）、
分片工具调用（start 一次、end 带完整参数）、取消后 channel 关闭（goroutine 不泄漏）。

## 5. 遗留

- 重试（429 退避、`EventRetry`）与 `isRetryable` 属 2.6。
- `reasoning_content`（DeepSeek 思考通道）读不到，见 §3 第 2 条。
- `ThinkingEffort` 直接映射 `reasoning_effort`；注册表里 `thinking_efforts` 与它的取值校验还没接（P4 `/model` 切换时一起做）。
- 真实端点未做端到端验证：P2 退出条件是 `sycode run "..."` 流式返回，需要 REPL/appstate 接线（2.7）与 headless 入口（P4）。
