# 变更日志

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added
- `internal/cli/repl`：Bubble Tea v2 最小界面（textarea 输入、`Enter` 提交、`/exit`、双击 `Ctrl+C` 退出）；非 TTY 下给出中文提示而非花屏
- `internal/cli/cmd`：cobra 命令树（`sycode` / `version` / `run`）、中文帮助与错误、退出码 0/1/2 分离；`--version` 输出 `0.1.0 (build <sha>)`
- `internal/logging`：`Init`（返回 cleanup 与日志路径）、`SYCODE_LOG_LEVEL` 解析（未知值回退 info）、`~/.sycode/logs/sycode-<时间戳>.log` 的 JSON 日志；`run()` 已接线，初始化失败退出码 1
- 规划文档：PRD、架构 RFC（含 ADR-001~008）、路线图、开发流程、编码规范；`.ai-interactions` 留痕骨架与三份模板
- `docs/CODE-DESIGN.md`：逐文件的函数清单、启动/关闭顺序、命名动词表、反面清单、与常见实现的函数级对应
- `docs/CODING-STANDARDS.md` §12 注释分级、§13 中文错误文案（分层语言规则、四要素、前缀表、汉化落点）
- `docs/WORKFLOW.md` §4.1 推进单元与上下文预算；`.ai-interactions/STATE.md` 断点文件
- `scripts/env.sh`：为精简 PATH 的终端补齐 Go 工具链环境
- **工程骨架**：`go.mod`（`github.com/sy665/sycode`, go 1.24）、`cmd/main.go`（`main`/`run` 分离）、`scripts/check.sh` 统一质量门、`Makefile` 薄封装、三平台 CI、MIT `LICENSE`、`.gitattributes`

### Fixed
- `scripts/check.sh` 的 `go build ./...` 会把 main 包二进制写到仓库根目录（曾误提交 `cmd.exe`），改为输出到临时目录

### Changed
- 目录结构对齐常见实现：`agentcore` + `appstate` 取代自建 `agent`；`llm/openai/` 收平为 `llm/openai.go`；`cli/repl` 子包补齐至 10 个
- 版本号用 `var` 而非 `const`：`-X` 只改写字符串变量，对 `const` 静默失效

### Removed
- `internal/arch` 包（依赖方向断言移入 `internal/cli/cmd/deps_test.go`）

## 说明

v0.1.0 尚未发布。首次发布前，本节只记录面向实现者的结构决策；用户可见变化从 v0.1.0 起记录。

<!-- 发布时按下列结构整理：
## [0.1.0] - YYYY-MM-DD
### Added
### Changed
### Fixed
### Removed
-->
