# 当前状态

更新时间：2026-10-29 18:35 | 阶段：**P1 收尾** | 当前单元：**1.4 已完成**，下一单元 1.5（依赖方向断言）

## 已完成（本阶段）

- 文档：PRD、ARCHITECTURE、ROADMAP、WORKFLOW、CODING-STANDARDS、CODE-DESIGN、README/AGENTS/CONTRIBUTING/CHANGELOG
- 留痕：`.ai-interactions/` phase-0 三轮 prompt/output + 三份模板 + 本文件
- 环境：go1.26.3（`D:\Apps\Go`）、gopls v0.23.0、`GOPATH=D:/Golang/code`、`GOPROXY=goproxy.cn`、`GOCACHE` 显式化；`scripts/env.sh` 补 PATH
- 结构调整：包级与常见实现对齐（`agentcore` + `appstate`，`llm/openai.go` 平铺，无 `internal/arch`）
- 规范：注释分级（§12）、中文错误文案（§13）、推进单元与上下文预算（§4.1）
- **P0.4/P0.5**：`go.mod`、`cmd/main.go`（main/run 分离 + ldflags 版本）、`scripts/check.sh`、`Makefile`、三平台 CI、MIT LICENSE、`.gitattributes`；质量门全绿

## 正在进行（未完成）

- 单元 1.1 已收口：`internal/logging`（114 行 + 140 行测试）+ `run()` 接线
- 单元 1.3 已收口：`internal/cli/cmd/root.go` + `main.go` 退出码映射；引入 cobra
- 单元 1.4 已收口：`internal/cli/repl`（200 行 + 180 行测试）；引入 bubbletea/bubbles/lipgloss v2
- 记录：`output-2/3/4_impl-*.md`

## 下一步第一件事（具体到文件与函数）

计划已批后，按 `outputs/phase-1/output-1_plan-unit-1-1.md` 执行：

**单元 1.5 —— 依赖方向断言**（`internal/cli/cmd/deps_test.go`，1 个文件）：

1. 用 `go list -json ./...` 读每个包的 import，断言禁止边：
   `llm → cli/agentcore/appstate`、`tools → llm`、`filesystem → 任意内部包`、`session → llm`、`appstate → cli`
2. 验收：临时在 `tools` 里写一行 `import ".../internal/llm"`，测试必须失败

P1 之后进入 P2（模型接入），详见 `docs/ROADMAP.md`。

## 已知坑 / 需要绕过的东西

- 工具 shell 的 PATH 被裁剪：`go` 不可见 → 先 `source scripts/env.sh`（不是没装）
- `go build ./...` 会把 main 包二进制写到当前目录 → `scripts/check.sh` 已改成 `-o <临时目录>/`
- `-race` 需要 cgo + gcc：`/d/mingw64/bin` 进 PATH 后 `CGO_ENABLED=1`；否则脚本跳过并提示
- 版本号必须是 `var version`，`-X` 对 `const` 静默失效
- Windows：`Add-Type` 被安全策略禁用；PowerShell 有批量删除护栏；`robocopy` 要从 bash 调并加 `MSYS2_ARG_CONV_EXCL=*`

## 已定（原待决策，2026-10-29 采纳推荐）

| 事项 | 结论 |
|---|---|
| LICENSE | **MIT**（已写入） |
| memory 容量（P6 专题） | 项目 4KiB / 全局 2KiB（比常见实现 16/8 小一档，窗口留给代码） |
| `cli/repl` 子包节奏 | P1 只建 `stream`/`output`/`theme`，其余随功能出现再建 |
| 工具循环位置 | 集中在 `appstate/turn.go`，不复制进 provider（ADR-001） |
| 质量门形态 | 以 `scripts/check.sh` 为准，`make` 只是薄封装（本机无 make） |

## 待决策

| 事项 | 选项 | 我的倾向 |
|---|---|---|
| 文档评审 | 五份文档是否 `APPROVED` | 逐份在首行标注，或把意见写进 `prompts/phase-0/prompt-4_review.md` |

**已定：不推远端**（2026-10-29）。仓库只保留在本地 `D:\project\owesome\code\sycode`，`git remote` 为空；
需要备份时由人工决定（压缩目录或自行加远端），开发流程中的提交仍按单元正常进行。
