# 实施记录：P3 单元 3.8 artifact 回收

- 日期：2026-10-29
- 结论：完成，质量门全绿；`cleanup/cleanup.go` 105 + `cleanup_test.go` 133
- 真二进制验过：8 天前的 artifact 被删、1 分钟前的保留、日志 `已清理过期的工具输出 count=1`

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `cleanup/cleanup.go` | 105 | `Run`、`runAt`（可注入时钟）、`pruneExpired`、`keepDays=7` |
| `cleanup/cleanup_test.go` | 133 | 5 组：只删过期、边界（不晚于即清）、目录不存在、子目录不递归、`runAt` 走 `BashOutputDir` 的完整链路 |
| `cmd/main.go` | +6 | `logging.Init` 之后调一次；失败 `slog.Warn` 不阻断 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| 只清 **artifact**，不接 logs/sessions/history | logs 的路径来源还在 `logging` 的私有函数里；sessions 要到 P4 才有格式。为了"对称"先给 `logging` 加导出函数没有收益 |
| **不递归、不跟符号链接** | artifact 目录是平的；链接指向哪里不该由回收逻辑替用户决定（`entry.Info()` 本身就是 Lstat，天然跳过链接） |
| 目录不存在视为**无事可做** | 第一次运行时它可能还没被创建过，那不该是错误 |
| 单个删不掉**不中断**整轮 | 其余文件仍该清掉；错误用 `errors.Join` 合并，由调用方一次记日志 |
| 边界是**不晚于 cutoff 即清** | 保留期是"最多留 7 天"，边界偏向清理更符合"别攒垃圾"的意图 |
| 时钟注入用 `runAt(now)` 而不是包级 `nowFunc` 变量 | 后者能在测试里被改、也能被并发测试互相污染；显式参数没有这个问题 |
| 局部变量 `cleanup` 改名 `closeLog` | `logging.Init` 的第一个返回值遮蔽了包名，`cleanup.Run` 直接编译不过 |

## 3. 验证

```
go test ./internal/cleanup/ -v   → 5 组全 PASS
bash scripts/check.sh            → 全部通过
```

端到端（隔离 home，不写脏真实 `~/.sycode`）：造 `~/.sycode/bash/old.txt`（mtime 8 天前）与
`new.txt`，跑真二进制 → 目录里只剩 `new.txt`，日志有 `已清理过期的工具输出 … count=1`。

**没验到的**：删不掉的场景（权限/占用）只能靠代码路径保证，手工造不出来。

## 4. 遗留

- `logs`（14 天）与 `sessions`（14 天）的回收待接：前者需要 `logging` 导出目录函数，后者等 P4 的存储落地
- 回收只在启动时跑一次；长驻进程（P4 之后的 headless 服务模式）需要另定触发时机
