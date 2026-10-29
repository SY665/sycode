# 实施记录：单元 1.4 bubbletea 最小 REPL

- 日期：2026-10-29
- 结论：完成，质量门全绿（10 组测试），非 TTY 场景已修

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/cli/repl/repl.go` | +200 | `Deps`/`NewModel`/`Init`/`Update`/`View`/`render`/`submit`/`append`/`visibleTranscript`/`Run`/`isInteractive` |
| `internal/cli/repl/repl_test.go` | +180 | 10 组：提交、空提交、`/exit`、双击 Ctrl+C、其它键解除待确认、渲染内容、窗口裁剪、空设备判定 |
| `internal/cli/cmd/root.go` | ±20 | `Options.StartREPL` 注入点 + `runREPL` 接真实界面 |
| `internal/cli/cmd/root_test.go` | ±25 | 用 stub 替换界面，避免测试把终端交给 Bubble Tea |
| `go.mod` | +5 | bubbletea/v2、bubbles/v2、lipgloss/v2（选型表内已授权） |

## 2. v2 与 v1 的差异（照文档写会直接编译失败）

| 项 | v1 | v2（实际） |
|---|---|---|
| 视图返回值 | `View() string` | **`View() tea.View`**，用 `tea.NewView(s)` 包装 |
| 按键消息 | `tea.KeyMsg` | `tea.KeyPressMsg`（底层是 `Key` 结构，`Code` + `Mod`） |
| import 路径 | `github.com/charmbracelet/...` | `charm.land/...` |

`View` 返回类型变化是编译期就撞上的；为避免测试绑死框架类型，拆出 `render() string`，`View()` 只做包装。

## 3. 修掉的真缺陷：`/dev/null` 被当成终端

第一版 `isInteractive` 只判 `ModeCharDevice`。但 `/dev/null` 与 Windows 的 `NUL` **也是字符设备**，于是：

```
$ ./sycode < /dev/null
（无输出，挂死，直到被外部 SIGTERM）
```

这会让 CI、`< /dev/null`、以及任何重定向场景卡住。修法：额外用 `os.SameFile` 与 `os.DevNull` 比对真实文件索引，跨平台可用、不引新依赖（`golang.org/x/term` 在选型表外，不擅自加）。修复后：

```
$ ./sycode < /dev/null
错误：交互式界面需要终端（stdin 不是 TTY）。请改用 sycode run "提示词"
退出码 1
```

已加回归测试 `TestIsInteractiveFalseForNullDevice`。

## 4. 设计决定

| 决定 | 理由 |
|---|---|
| `Options.StartREPL` 注入点 | 否则"无参数启动"的测试会真的接管终端，在交互式终端里跑 `go test` 会挂住 |
| Enter 提交前解绑 textarea 的 `InsertNewline` | 不解绑就永远是"插入换行"，提交逻辑收不到 Enter |
| 双击 Ctrl+C 才退出 | 单次误触不该丢上下文；按其它键自动解除待确认 |
| `/exit`、`/quit` 都不写入历史 | 命令不是对话内容，写进 transcript 会污染上下文 |
| 没有模型时明确写"（模型尚未接入，P2 生效）" | 不给假反馈，避免用户以为在思考 |

## 5. 验证

- `bash scripts/check.sh` 全绿（fmt / vet / build / test / race）
- 非 TTY：重定向与管道均返回中文错误、退出码 1、不挂死
- 交互路径：由 10 组单测覆盖状态迁移；**真实终端下的观感需人工跑一次**（本环境无 PTY，这是已知验证盲区）

## 6. 遗留

- 界面尚无滚动区与流式渲染（P4 的 `stream/`、`output/` 子包）
- `visibleTranscript` 是行数裁剪，不是真正的 viewport；条数多时会重排整屏
- 上下文占用、`/model` 等命令属 P4
