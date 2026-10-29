# 按推荐执行：四项决策 + P0.4 工程骨架

**目标**：采纳上一轮全部推荐，把 P0 收尾（go.mod / 质量门 / CI / LICENSE）。
**约束**：本机无 make；Windows；不得引入未在目录树登记的目录。
**期望产出**：可编译的骨架 + 本地与 CI 共用的质量门 + 首笔提交。

---

## 原始输入（2026-10-29 17:26）

> 都按你推荐来做

## 采纳的推荐

| 事项 | 结论 |
|---|---|
| LICENSE | MIT |
| memory 容量（P6） | 项目 4KiB / 全局 2KiB |
| `cli/repl` 子包节奏 | P1 只建 `stream`/`output`/`theme` |
| 工具循环 | 集中 `appstate/turn.go` |
| 质量门形态 | 以 `scripts/check.sh` 为准（本机无 make） |

## 评审结论

`PENDING`
