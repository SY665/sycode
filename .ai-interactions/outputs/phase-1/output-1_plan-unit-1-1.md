# 实现计划：P1 单元 1.1 日志落盘

- 对应：[prompt-1_unit-1-1-logging.md](../../prompts/phase-1/prompt-1_unit-1-1-logging.md)
- 关联需求：ROADMAP P1.1（部分）+ P1.2；PRD F-CLI-1、非功能「可观测」
- 规模预估：3 个文件 / ~180 行（含测试）

## 1. 结论先行

照 CODE-DESIGN §3 的契约实现 5 个函数，`Init` 返回 `cleanup` 让 `run()` 用 `defer` 收尾。
**一处契约需要修订**：原契约写"终端用可读格式、文件用 JSON"，实际只做文件 JSON——理由见 §3.4。

## 2. 现状

- `internal/logging` 不存在
- `cmd/main.go` 的 `run()` 只 `fmt.Fprintf(os.Stderr, ...)` 并返回 0
- `go.mod` 零依赖，`scripts/check.sh` 已能跑全套检查

## 3. 方案

### 3.1 文件与函数

| 文件 | 元素 | 说明 |
|---|---|---|
| `internal/logging/logging.go` | `Init() (cleanup func(), path string, err error)` | 幂等性：**不保证**；重复调用会新建文件并替换 default logger，前一次的 `cleanup` 负责关旧句柄 |
| | `parseLevel(s string) slog.Level` | `debug/info/warn/warning/error`，大小写不敏感；未知或空 → info |
| | `logDir() (string, error)` | `os.UserHomeDir()` + `.sycode/logs`，`MkdirAll(0700)` |
| | `createFile(dir string) (*os.File, error)` | `sycode-20060102-150405.log`，`O_CREATE\|O_WRONLY\|O_APPEND`，权限 0600 |
| | `newHandler(w io.Writer, level slog.Level) slog.Handler` | `slog.NewJSONHandler`，`AddSource: false` |
| `internal/logging/logging_test.go` | 4 组测试 | 见 §3.3 |
| `cmd/main.go` | `run()` | `Init` → 失败打印中文 + 返回 1；成功 `defer cleanup()` + `slog.Debug` 记录路径 |

### 3.2 `run()` 改动后的形状

```go
func run() int {
    cleanup, path, err := logging.Init()
    if err != nil {
        fmt.Fprintf(os.Stderr, "错误：初始化日志失败：%v\n", err)
        return 1
    }
    defer cleanup()
    slog.Debug("日志已初始化", "path", path)
    fmt.Fprintf(os.Stderr, "sycode %s：骨架已就绪，命令树见 P1\n", version)
    return 0
}
```

### 3.3 测试计划

| 用例 | 断言 |
|---|---|
| `TestParseLevel` | 9 个输入（含大写、`warning`、空串、乱码）→ 期望 level |
| `TestInit_WritesJSONL` | 把 `HOME` 指到 `t.TempDir()`，`Init` 后写一行 debug，读回文件断言含 `"level":"DEBUG"` 与 `"msg"` |
| `TestInit_LogDirUnwritable` | 父目录只读 → 返回 error（Windows 上跳过，权限位不生效，注释说明） |
| `TestInit_CleanupTwice` | 连续两次 `Init` 并各自 `cleanup` → 不 panic、不泄漏句柄 |

> 注：`Init` 依赖 `os.UserHomeDir()`，测试通过 `t.Setenv("HOME", temp)`（Windows 同时设 `USERPROFILE`）隔离，避免污染真实家目录。

### 3.4 需要修订的契约（请一并批准）

CODE-DESIGN §3 里 `newHandler` 的职责写的是"终端用可读格式、文件用 JSON"。改为：

> `newHandler` 只产出文件用 JSON handler。v0.1 不向终端输出日志：Bubble Tea 接管屏幕后写 stderr 会破坏渲染；
> 终端可读格式留给 P4 的 `/log` 面板（在 TUI 内渲染），不写 stderr。

理由：现在不砍，P1 就得实现"多 handler 转发 + TTY 检测"，约 40 行 + 一套测试，而它的唯一用途是 P1 阶段调试——用 `tail -f` 即可替代。

## 4. 边界与失败路径

| 场景 | 期望行为 |
|---|---|
| `$HOME` 取不到 | 返回 `初始化日志失败：无法确定用户目录`，退出 1 |
| 目录存在但不可写 | 返回错误，退出 1，不 panic |
| 磁盘满 | `createFile` 返回错误，同上 |
| `SYCODE_LOG_LEVEL` 乱填 | 静默回退 info（不阻塞启动） |
| 同一进程二次 `Init` | 两个文件句柄，各自 `cleanup` 关闭；不 panic |

## 5. 风险

| 风险 | 缓解 |
|---|---|
| 测试改 `HOME` 污染并行用例 | 不用 `t.Parallel()`，或用 `t.Setenv`（自动阻止并行） |
| Windows 权限位不可测 | 权限断言只在非 Windows 执行，注释写明原因 |
| `~/.sycode/` 落盘位置与后续 config/sessions 冲突 | 统一用 `logDir()` 这类小函数，路径不散落 |

## 6. 需要人决策的点

1. 批准 §3.4 的契约修订（砍掉终端日志输出）。
2. 日志时间戳用本地时间（便于对照操作时间）而非 UTC —— 我认为本地时间更实用，但会影响跨时区排查。
