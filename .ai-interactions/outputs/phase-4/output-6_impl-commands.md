# 实施记录：P4 单元 4.5 斜杠命令表

- 日期：2026-10-29
- 结论：完成，质量门与 `-race` 全绿；`repl/commands.go` 150 + 测试 150，`agentcore` 接口 +25
- **接口变更已获用户同意**（`AgentCore` 加 `Sessions()`，属预授权例外 1）

## 1. 改动

| 文件 | 内容 |
|---|---|
| `agentcore/agentcore.go` | `SessionInfo`、`AgentCore.Sessions()` |
| `agentcore/adapter.go` | `Core.store` 字段、`(*Core).Sessions()`（过滤 0 轮） |
| `repl/commands.go` | `command`、`commandList()`、`lookupCommand`、五个 `run` 方法 |
| `repl/repl.go` | `submit` 改为先查命令表；删掉 `/exit`、`/context` 两处特判 |
| `repl/context.go` | `padLabel` 抽出通用的 `pad(s, width)`，供 `/help` 复用 |
| 测试 | repl 6 组、agentcore 2 组 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **五条命令一张表**，`/help` 从同一张表渲染 | 写死两份"命令清单"，早晚出现"照帮助敲却没反应"。测试直接遍历表断言每条都出现在帮助里 |
| `commandList()` 是**函数**不是包级变量 | `showHelp` 读表、表里的 `run` 又指向 `showHelp` → 包级变量构成初始化环（编译期 `initialization cycle`）。每次新建 5 元素切片，用户敲命令时才走，不值得 `sync.Once` |
| **不拆 `commands` 子包** | 每条命令都要读写 `Model`（记录、流式标志、输入框）；搬出去得先抽象这些能力。对五条命令，抽象成本远大于收益。文档已改口径：等命令需要互相组合时再拆 |
| `/clear` **不碰会话文件** | 文件是只追加的账本，删内容只能靠新建会话。提示必须写"记录仍然保留"，否则用户以为磁盘上也空了 |
| `/sessions` 过滤 **0 轮**会话 | 启动过但一句话没说的空壳，列出来只会让人以为有东西可恢复 |
| `store` 留在 `Core` 上 | 列会话是"读存储"，与回合状态机无关；放进 `appstate` 等于给状态机加一个查询入口 |
| 未知斜杠命令**按普通输入处理** | v0.1 的现状。要加"未知命令"提示时先改那条把现状钉住的测试——这是刻意的"行为有据可查" |
| `/help` 附带快捷键说明 | 帮助页是唯一"用户会去读"的地方；`Esc` 与 `Ctrl+C` 的差别（中断 vs 退出）不写在这里没别处可写 |

## 3. 验证

```
go test ./internal/cli/repl/ ./internal/agentcore/ -v   → 8 组新增全 PASS
CGO_ENABLED=1 go test -race 同上                         → 全 PASS
bash scripts/check.sh                                    → 全部通过
```

覆盖：`/help` 与命令表同源、`/quit` 别名、`/clear` 清缓冲但不删文件、`/sessions` 渲染与空/错分支、
未知斜杠命令当输入、`Sessions()` 无库返回空、过滤 0 轮会话。

**没验到的**：真 TUI 里 `/help` 与 `/sessions` 的列对齐（按字数补齐，等宽字体下应齐）。

## 4. 遗留

- 会话**只列不切**：切换要先定"进行中的回合怎么办"、"切换后工具权限缓存是否沿用"，留给 P5
- 未知斜杠命令没有提示：敲错 `/sessons` 会当成一句话发给模型，还要花一次请求
- `SessionInfo` 没有"当前会话"标记：`/sessions` 里分不出哪条是正在写的这条
