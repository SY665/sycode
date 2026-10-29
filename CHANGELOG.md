# 变更日志

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本号遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### Added
- 规划文档：PRD、架构 RFC（含 ADR-001~008）、路线图、开发流程、编码规范；`.ai-interactions` 留痕骨架与三份模板
- `docs/CODE-DESIGN.md`：逐文件的函数清单、启动/关闭顺序、命名动词表、反面清单、与常见实现的函数级对应
- `docs/CODING-STANDARDS.md` §12 注释分级、§13 中文错误文案（分层语言规则、四要素、前缀表、汉化落点）
- `docs/WORKFLOW.md` §4.1 推进单元与上下文预算；`.ai-interactions/STATE.md` 断点文件
- `scripts/env.sh`：为精简 PATH 的终端补齐 Go 工具链环境

### Changed
- 目录结构对齐常见实现：`agentcore` + `appstate` 取代自建 `agent`；`llm/openai/` 收平为 `llm/openai.go`；`cli/repl` 子包补齐至 10 个

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
