# 当前状态

更新时间：2026-10-29 17:25 | 阶段：P0（收尾中） | 当前单元：无（文档层完成，待开工 P0.4）

## 已完成（本阶段）

- 文档：PRD、ARCHITECTURE、ROADMAP、WORKFLOW、CODING-STANDARDS、CODE-DESIGN、README/AGENTS/CONTRIBUTING/CHANGELOG
- 留痕：`.ai-interactions/` phase-0 两轮 prompt/output + 三份模板
- 环境：go1.26.3（`D:\Apps\Go`）、gopls v0.23.0、`GOPATH=D:/Golang/code`、`GOPROXY=goproxy.cn`、`GOCACHE` 显式化；`scripts/env.sh` 补 PATH
- 结构调整：包级与常见实现一对齐（`agentcore` + `appstate`，`llm/openai.go` 平铺，无 `internal/arch`）
- 规范补充：注释分级（§12）、中文错误文案（§13）、推进单元与上下文预算（WORKFLOW §4.1）

## 正在进行（未完成）

- 无。上一轮（Code-Design）已收口。

## 下一步第一件事（具体到文件与函数）

**P0.4 工程骨架**，按此顺序：

1. `go.mod`：`module github.com/sy665/sycode`，`go 1.24`
2. `Makefile`：目标 `fmt`/`vet`/`test`/`race`/`check`（`check: fmt vet test race`）
3. `.github/workflows/ci.yml`：`ubuntu/macos/windows` × `go-version: 1.26.x`，跑 `make check`
4. `LICENSE`：待定（MIT / Apache-2.0）
5. 空跑验证：`go build ./...` + `make check` 通过（此时只有 `cmd/main.go` 空壳与 `deps_test.go`）

## 已知坑 / 需要绕过的东西

- 工具 shell 的 PATH 被裁剪：`go` 不可见 → 先 `source scripts/env.sh`（不是没装）
- Windows：`Add-Type` 被安全策略禁用；PowerShell 有批量删除护栏；`& robocopy.exe` 在 PowerShell 报 `CannotActivateDocumentInPipeline`
- Go 模块缓存文件是只读属性，跨盘移动前要清 `ReadOnly`
- 常见实现 `gopls@latest` 要 Go 1.27，本机停在 v0.23.0（用 `GOTOOLCHAIN=local` 安装）

## 待决策（等人拍板，附选项）

| 事项 | 选项 | 我的倾向 |
|---|---|---|
| LICENSE | MIT / Apache-2.0 | MIT（工具类项目，门槛低） |
| memory 容量（P6 上下文专题） | 常见实现 16KiB/8KiB vs 4KiB/2KiB | 4KiB/2KiB，把窗口留给代码 |
| `cli/repl` 子包落地节奏 | P1 一次建 10 个 vs 先建 `stream`/`output`/`theme` 再按需加 | 先建 3 个，其余随功能出现再建（避免空包） |
| 工具循环是否完全照抄常见实现 | 集中一份（现方案）vs 每个 provider 一份 | 集中一份（ADR-001） |
