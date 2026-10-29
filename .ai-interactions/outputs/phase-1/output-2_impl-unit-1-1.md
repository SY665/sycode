# 实施记录：单元 1.1 日志落盘

- 对应：`prompts/phase-1/prompt-1_unit-1-1-logging.md` / `outputs/phase-1/output-1_plan-unit-1-1.md`
- 日期：2026-10-29
- 结论：完成，质量门全绿，端到端验证通过

## 1. 实际改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/logging/logging.go` | +114 | `Init`、`parseLevel`、`logDir`、`createFile`、`newHandler` |
| `internal/logging/logging_test.go` | +140 | 5 组测试（级别 9 例 / JSON 内容 / 级别过滤 / 目录不可写 / 二次 Init） |
| `cmd/main.go` | ±14 | `run()` 接入 `Init` + `defer cleanup()`，失败回 1 |

合计 3 个文件，符合单元上限（≤3 文件 / ≤300 行）。

## 2. 与计划的偏差

| 项 | 计划 | 实际 | 原因 |
|---|---|---|---|
| `createFile` 签名 | `(*os.File, error)` | `(*os.File, string, error)` | 只返回句柄会让 `Init` 重算文件名，两处格式化逻辑容易漂移；一并返回路径，CODE-DESIGN 已同步 |
| 测试数 | 4 组 | 5 组 | 补了「级别过滤」——只测"能写入"不足以证明 `SYCODE_LOG_LEVEL` 起作用 |

## 3. 验证

单测：`go test ./internal/logging/ -race` → ok（0.7s / 1.6s）。

端到端（`SYCODE_LOG_LEVEL=debug`，家目录指向临时目录）：

```json
{"time":"2026-10-29T18:03:05.3462579+08:00","level":"DEBUG","msg":"日志已初始化","path":"…\\.sycode\\logs\\sycode-20261029-180305.log"}
```

退出码 0；文件名时间戳与内容时间一致；未污染真实 `~/.sycode`。

## 4. 已知限制

- Windows 上 `0600` 权限位不生效（实际 `-rw-r--r--`）。这是 OS 语义差异，非缺陷；待 P3 安全红线统一处理时用 ACL 复核。
- 同秒内二次 `Init` 会命中同一文件名（时间戳精确到秒），表现为追加写。已记入测试注释，未做特殊处理——单进程正常路径不会触发。
- 日志未脱敏：当前只写路径与消息，不写 prompt/密钥；集中脱敏在 P3。
