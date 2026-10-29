# AGENTS.md

SyCode 是 Go 编写的终端 AI 编程代理。你（AI 编码代理）在此仓库工作时，按以下约束行动。

## 动手前

0. **先修环境**：本机 Go 1.26.3 在 `D:\Apps\Go`，但精简 PATH 的终端里 `go` 可能不可见——不是没装。每个新 shell 先 `source scripts/env.sh`（同时设置 GOCACHE 与 `GOPROXY=https://goproxy.cn,direct`）。
1. 读 `docs/ROADMAP.md` 确认当前期与退出条件；不要实现"待设计专题"里的内容（上下文压缩、子代理、MCP、Skills、hashline）。
2. 读 `docs/ARCHITECTURE.md` 的相关章节；接口签名以文档为准，文档与代码不一致时先提出，不要私自改契约。
3. 读 `docs/CODE-DESIGN.md`：**每个文件该有哪些函数、启动顺序、命名动词表都在里面**。写新文件前先对照该文件对应小节；不在文档里的文件，先补文档再写代码。
3. 读 `docs/WORKFLOW.md`：**先出 plan，再写代码**。plan 存 `.ai-interactions/outputs/<phase>/output-N_<slug>.md`。

## 硬约束

- 文件访问必须经 `internal/filesystem` 的 `Guard`，禁止直接 `os.Open`/`os.WriteFile` 业务文件。
- **错误输出用中文**：面向用户、面向模型、日志 message、测试失败信息都是中文；标识符、路径、命令、字段名、错误码保持原文。格式见 `CODING-STANDARDS.md` §13（前缀只用 `参数错误：`/`执行失败：`/`权限不足：`/`超时：`/`不支持：`）。
- **注释写必要的**：包注释（职责 + 不做什么）、导出符号契约、非显然的意图、平台坑、魔法值来源、并发假设、危险处 `// 注意：`。不复述代码、不留注释掉的代码。见 §12。
- 依赖方向见 `CODING-STANDARDS.md` 第 2 节，`internal/cli/cmd/deps_test.go` 会拦你。
- 不打印、不记录、不提交任何密钥或会话正文。
- 工具 `Execute` 不 panic；错误信息要能让模型自我修正。
- 新增工具 = 1 个文件 + `registry.go` 一行注册；不改 `llm`，不改 REPL 渲染。
- 单 PR 只解决一个 issue，diff ≤ 600 行（不含测试）。
- 提交前 `bash scripts/check.sh`（fmt / vet / build / test / race；有 make 时 `make check`）。

## 推进方式

按模块或功能推进，一次一个"单元"（≤ 3 个源文件 / ≤ 300 行 diff）。**每个单元必须收口**：能编译（未实现就返回 `执行失败：尚未实现`）、测试过、提交、更新 `.ai-interactions/STATE.md`。

上下文紧张时按此顺序降级：先让文件可编译 → 把 `STATE.md` 的"下一步第一件事"写到能直接开工 → 提交 → 再停。**禁止留半截语法**。

新会话续接：读 `.ai-interactions/STATE.md` → `git log -3 --stat` → `CODE-DESIGN.md` 对应小节，不要从 PRD 重新读起。

## 目录速查

```
cmd/main.go            入口：日志、版本、退出码
internal/agentcore/    UI 边界：AgentCore 接口 + adapter + 工具装配
internal/appstate/     会话状态机、回合与工具循环
internal/cli/cmd/      Cobra 命令与依赖组装（含 deps_test.go 依赖方向断言）
internal/cli/repl/     Bubble Tea TUI：repl/handlers/commands + stream/ output/ markdown/
                       permissions/ widgets/ theme/ commands/ history/ filesearch/ askuser/
internal/llm/          请求与事件抽象、系统提示、重试、工具执行；openai.go 为协议实现
internal/tools/        工具实现与注册表
internal/filesystem/   Guard、gitignore
internal/session/      JSONL 事件溯源
internal/config/       分层配置
internal/providers/    registry.yaml 模型元数据
docs/                  产品/架构/流程/规范
.ai-interactions/      人机协作留痕（原文保存，不改写）
```

## 输出风格

- 中文回复，代码标识符与提交信息用英文。
- 先给结论，再给依据；不写"很好的问题"这类客套。
- 不确定的地方标出来并给两种方案，不要含糊带过。
