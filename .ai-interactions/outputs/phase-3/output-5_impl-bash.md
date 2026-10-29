# 实施记录：P3 单元 3.5 `bash` 工具

- 日期：2026-10-29
- 结论：完成，质量门全绿；`bash.go` 369 + 两个平台文件 25+25，测试 223；`tools` 包累计 85 个顶层测试

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `bash.go` | 369 | `Shell`/`ResolveShell`、`BashTool`、`command`、`Execute`、`run`、`exitCodeOf`、`isDangerous`、`truncateOutput`、`oneLine`/`tailString`、`limitedBuffer` |
| `proc_windows.go` | 25 | `prepareCommand`（空）、`killTree`（`taskkill /T /F`） |
| `proc_unix.go` | 25 | `prepareCommand`（`Setpgid`）、`killTree`（`kill(-pid)`） |
| `bash_test.go` | 223 | 15 组用例 |
| `filesystem/guard.go` | ±4 | `bashOutputDir` → **导出** `BashOutputDir`（落盘方必须与 Guard 用同一路径） |
| `cmd/main.go` | −18 | `detectShell` 改为调 `tools.ResolveShell()`，删掉重复的 ADR-006 顺序 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **超时要杀整棵进程树** | `Process.Kill()` 只杀 shell，`go build` / `npm test` 的子进程会变孤儿继续占文件与 CPU。Windows 用 `taskkill /T`，Unix 用进程组 `kill(-pid)` |
| **必须设 `cmd.WaitDelay = 3s`** | 这是本单元最重要的一条：子进程继承输出管道且不肯退出时，`Wait` 会一直等管道关闭，超时形同虚设。实测 1 秒超时的 `sleep 30` 被拖到 **30 秒**才返回；加 `WaitDelay` 后 4 秒收敛 |
| 非 0 退出码**不是错误** | 构建失败、测试不通过正是模型要看的信息；当成工具故障会诱导它"重试"而不是"读输出改代码" |
| 危险命令**只加提示，不改判定** | 能不能执行由 Guard 三态决定。`isDangerous` 的用处是让弹窗把 ⚠ 写在脸上，而不是自己造一套权限规则 |
| `truncateOutput` 从 `filesystem.BashOutputDir()` 取路径 | 落盘方与 Guard 必须同源；各拼一次一旦不一致，就会"写进去但 Guard 不许读" |
| `ResolveShell` **导出**，`main` 复用它 | 系统提示那行 `Current shell:` 与真正执行必须是同一个结论。两处分叉的后果是模型按 bash 写命令、实际在 cmd 里跑 |
| `limitedBuffer` 有 4MiB 上限并记丢弃量 | 进程可能吐几个 GB；限容既保护内存，也让"被截了"这件事在输出里可见 |
| `SYCODE_SHELL` 指到不存在的程序时报错 | 悄悄换成别的 shell 会让命令语法全错，比直接失败更难查 |

## 3. 验证

```
go test ./internal/tools/ -v → 85 个顶层测试全 PASS（本单元 15 组）
bash scripts/check.sh        → 全部通过
```

覆盖：跑命令、工作目录生效、stderr 合并、非 0 退出码带提示、超时 4s 内收敛、
3,000 行输出落盘（断言文件真实存在且 >32KiB，放在 `~/.sycode/bash/`）、危险命令弹窗带 ⚠、
12 条 `isDangerous` 表、参数校验、拒绝后命令未执行、`limitedBuffer` 丢弃计数、`oneLine` 压行与截断。

## 4. 遗留

- `appstate` 工具循环（3.6）还没接：目前工具只能单测，没有"模型真的调它"的端到端。
- 超时用 `time.After(5s)` 兜底等 `Wait`；极少数情况下 goroutine 会留到进程退出（已避免阻塞主流程）。
- 环境变量在命令里继承：`SYCODE_*` 与用户环境都会传给子进程，没有做白名单（P4 讨论，可能有需求要挡住密钥变量）。
- Windows 上 `taskkill` 若被安全策略拦下，只剩 `Process.Kill()`；此时孙进程可能残留——日志里没有专门标记，靠 `WaitDelay` 兜住主流程。
- 落盘文件不做自动清理：`internal/cleanup`（3.8）会按 7 天保留期回收。
