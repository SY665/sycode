# SyCode

终端里的 AI 编程代理。用 Go 写，单二进制，模型通过 OpenAI 兼容协议接入。

> **当前状态：P0 已完成**。工程骨架（`go.mod` / 质量门 / CI / LICENSE）就绪，`cmd/main.go` 是可编译的空壳；功能实现从 [ROADMAP.md](./docs/ROADMAP.md) 的 P1 开始。

## 它是什么

在终端里，用自然语言让它读代码、改文件、跑命令、看结果——把"需求"推进到"可运行的改动"。工具集刻意保持精简（6 个），权限默认最小，所有文件访问经统一守卫。

## 设计取向

| 取向 | 具体表现 |
|---|---|
| 可读懂 | 核心链路：输入 → 一次 LLM 请求 → 工具循环 → 落盘。分层单向依赖，用测试强制 |
| 可托付 | 工作区内读取免打扰；任何写入必须确认；系统路径直接拒绝；`.gitignore` 内的路径不可触碰 |
| 可被 agent 开发 | 仓库按"人写 prompt、agent 写代码"推进，每一次往返留在 `.ai-interactions/` |

## 文档

| 文档 | 读它来了解 |
|---|---|
| [docs/PRD.md](./docs/PRD.md) | 做什么、不做什么、v0.1 的每条验收标准 |
| [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md) | 分层、核心接口、数据流、选型理由、ADR |
| [docs/CODE-DESIGN.md](./docs/CODE-DESIGN.md) | **每个文件该有哪些函数**、启动顺序、命名动词表、反面清单 |
| [docs/ROADMAP.md](./docs/ROADMAP.md) | 六期计划与退出条件、风险登记 |
| [docs/WORKFLOW.md](./docs/WORKFLOW.md) | 五步循环、留痕规范、DoR/DoD、评审与发布 |
| [docs/CODING-STANDARDS.md](./docs/CODING-STANDARDS.md) | Go 编码约定、工具契约、安全红线、测试要求 |
| [AGENTS.md](./AGENTS.md) | 给 AI 编码代理的仓库级须知 |

## 快速开始（P1 完成后可用）

```bash
git clone https://github.com/SY665/sycode && cd sycode
source scripts/env.sh        # 补齐 go / GOCACHE / GOPROXY（精简 PATH 的终端必需）
go mod download
go run ./cmd
```

前置：Go ≥ 1.24（开发机 go1.26.3，装在 `D:\Apps\Go`）；`OPENAI_API_KEY`（或兼容端点的 key）；首次运行会在 `~/.sycode/` 生成配置。

## 计划中的命令

```
sycode                    # 进入交互式 REPL
sycode run "<prompt>"     # 单回合，非交互
sycode --version
```

REPL 内：`/help` `/model` `/context` `/clear` `/sessions` `/exit`

## 许可

MIT，见 [LICENSE](./LICENSE)。
