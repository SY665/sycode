# 贡献指南

这个仓库的开发过程本身是留痕的：**人写 prompt 与决策，agent 写代码与文档初稿**。贡献时请保持这一点。

## 环境

- **Go 1.26.3**（已装于 `D:\Apps\Go`；最低要求 1.24）。注意：部分终端继承的 PATH 被裁剪，`go` 可能不可见 —— 不是没装，是没在 PATH 里。
- 任一 AI 编码代理（Cursor / Claude Code / Codex CLI / GitHub Copilot / WorkBuddy 等）
- 至少一个 OpenAI 兼容端点的 API key

```bash
source scripts/env.sh      # 补 PATH（GOCACHE/GOPATH/GOPROXY 已持久化到 go env）
go mod download
go test ./...
go run ./cmd
```

本机 Go 环境的持久配置（`%APPDATA%\go\env`，2026-10-29 写入，换机器需重建）：

| 项 | 值 | 原因 |
|---|---|---|
| `GOPATH` | `D:/Golang/code` | 沿用旧工作区；其 `bin/` 已在 PATH 中（gopls、benchstat、swag 在此） |
| `GOPROXY` | `https://goproxy.cn,direct` | 默认 `proxy.golang.org` 在本网络返回 Bad Gateway |
| `GOCACHE` | `C:/Users/SY/AppData/Local/go-build` | 部分 shell 的 `%LocalAppData%` 为空，Go 找不到默认缓存目录 |

回滚：`go env -u <NAME>`；查看全部：`go env`。

### 已知环境限制（2026-10-29 实测）

| 现象 | 原因 | 应对 |
|---|---|---|
| `gopls@latest`（v0.24.0）装不上 | 它要求 Go ≥ 1.27，触发 `GOTOOLCHAIN` 自动切换；切换后报 `missing GOSUMDB`，且 toolchain 缓存目录里的 `.partial` 收尾时被占用 | 用 `GOTOOLCHAIN=local go install golang.org/x/tools/gopls@v0.23.0`（当前已装此版，兼容 Go 1.26） |
| `go build` 报 `error obtaining VCS status: exit status 128` | 上层目录存在残缺 `.git`（曾出现在 `C:\Users\SY\.git`，空目录） | 从构建目录向上逐层检查 `.git`，删除或改名 |
| 依赖拉取报 `Bad Gateway` | 默认 `proxy.golang.org` 不可达 | 已持久化 `GOPROXY=https://goproxy.cn,direct` |

## 调试

结构化日志基于 `log/slog`：

```bash
SYCODE_LOG_LEVEL=debug go run ./cmd
```

日志写入 `~/.sycode/logs/`。`debug` 级别会记录请求摘要，但**不含**密钥与完整 prompt 正文。

## 提交流程

1. 开 issue，写明验收标准（能写成"给定…当…则…"）。
2. 让 agent 先出 plan，存到 `.ai-interactions/tasks/<issue-slug>/output-1_plan.md`。
3. 你评审 plan，结论写到下一个 prompt 文件首行（`APPROVED` / `CHANGES REQUESTED`）。
4. agent 实现；每轮往返都存 prompt 与 output。
5. `make check` 全绿后开 PR，PR 描述必须包含：改了什么 / 为什么 / 怎么验证 / 留痕路径。

## 留痕命名

```
.ai-interactions/tasks/issue-<编号>-<短描述>/
├── prompt-1_<slug>.md
├── output-1_plan.md
├── prompt-2_<slug>.md
└── output-2_review.md
```

output 落盘后**不要改写**（可追加"后续修正"段落，但不能改原结论）。

## 会被打回的写法

- 绕过 `filesystem.Guard` 的文件读写
- 工具 `Execute` 可能 panic
- 密钥/token 出现在日志、错误信息、会话文件
- 一个 PR 干多个 issue 的事
- 新逻辑无测试，或测试只覆盖 happy path
- 缺少 `.ai-interactions/` 往返记录

完整规范见 [docs/CODING-STANDARDS.md](./docs/CODING-STANDARDS.md) 与 [docs/WORKFLOW.md](./docs/WORKFLOW.md)。
