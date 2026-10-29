# P1 单元 1.1：日志落盘

**目标**：`sycode` 启动后能按 `SYCODE_LOG_LEVEL` 把结构化日志写进 `~/.sycode/logs/`，为后续所有包提供统一出口。
**约束**：只动 3 个文件；不引第三方日志库；错误文案中文；不改动其它包的契约。
**期望产出**：`internal/logging` 包 + 测试 + `run()` 接线。

---

## 原始输入（2026-10-29 17:34）

> 按照我们写的流程推进，

## 背景

P0.4 已让 `cmd/main.go` 可编译、版本可注入，但 `run()` 还只是个打印骨架的占位。
`docs/ROADMAP.md` 的 P1.1（入口接线）与 P1.2（logging 包）互相依赖，合并成一个单元做：
先有包，再接线，一次验收。

施工契约：[CODE-DESIGN.md](../../../docs/CODE-DESIGN.md) §1 第 1 步、§3 `internal/logging/logging.go`。

## 范围

- 新增 `internal/logging/logging.go`：`Init`、`parseLevel`、`logDir`、`createFile`、`newHandler`
- 新增 `internal/logging/logging_test.go`
- 改 `cmd/main.go` 的 `run()`：接入 `Init` + `defer cleanup()`，失败返回 1

## 验收标准

- 给定 `SYCODE_LOG_LEVEL=debug`，当运行 `sycode`，则在 `~/.sycode/logs/` 下生成 `sycode-<时间戳>.log`，内容为 JSON 行且含 `"level":"DEBUG"`
- 给定 `SYCODE_LOG_LEVEL=unknown`，当运行，则**不报错**，按 info 级别记录（拼错级别不该导致程序打不开）
- 给定 `$HOME` 指向不可写目录，当运行，则退出码 1 且输出中文错误"初始化日志失败：…"，不 panic
- 给定 `go test ./internal/logging/ -race`，则全绿

## 明确不做

- 不做按大小轮转（每次启动新建文件，旧文件由 P3 的 `cleanup` 按 14 天回收）
- 不往终端（stderr/stdout）输出日志 —— TUI 渲染期间写 stderr 会花屏；开发期用 `tail -f`
- 不做日志脱敏（密钥过滤统一在 P3 安全红线里做）
- 不引 zap/zerolog 等第三方库

---

**评审结论**（由人填写）：`APPROVED（预授权自审，2026-10-29）`
**意见**：人已声明"全交由你开工"，按 WORKFLOW §1.1 进入预授权模式。自审结论：计划可执行；两处待决点按推荐取值——
① 砍掉终端日志输出（理由：TUI 渲染冲突，`tail -f` 可替代）；② 时间戳用本地时间（文件名已含日期，混淆风险低）。
契约变更（`createFile` 增加返回 `path`、`newHandler` 仅 JSON）已在实现中同步到 CODE-DESIGN。
