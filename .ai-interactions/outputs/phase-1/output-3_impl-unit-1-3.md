# 实施记录：单元 1.3 cobra 命令树

- 日期：2026-10-29
- 结论：完成，质量门全绿，7 组测试通过

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/cli/cmd/root.go` | +175 | `Options`、`NewRootCommand`、`runREPL`、`newVersionCmd`、`newRunCmd`、`versionString`、`ExitCode`、`exitError`/`usageErrorf`/`errNotImplemented`、`localizeDefaultFlags`/`translateFlagError`、中文帮助模板 |
| `internal/cli/cmd/root_test.go` | +150 | 7 组：`--version`、`version` 子命令无提交号、未知命令、未知参数、run 缺参、run 未实现、帮助页汉化、默认 flag 汉化 |
| `cmd/main.go` | ±30 | 改成 `NewRootCommand(...).Execute()` + 退出码映射；新增 `commit` 注入 |
| `go.mod` | +3 | 引入 `spf13/cobra v1.10.2`（选型表内，已一次性授权） |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| `exitError` 携带退出码，`main` 用 `errors.As` 读取 | 不靠字符串判断"是用法错误还是功能未做"，脚本可依赖退出码分支 |
| 未实现用退出码 1、用法错误用 2 | 用户敲错（2）与功能没做（1）必须可区分，否则脚本会把"没做"当成"敲错"重试 |
| root 带 `RunE` + `Args: ArbitraryArgs` 自定义未知命令 | cobra 默认报 `unknown command "bogu" for "sycode"`（英文），接管后可给中文并保留退出码语义 |
| 关掉 cobra 自带 `completion` 命令 | 英文 + 用不上，且污染帮助页 |
| 手写中文 help/usage 模板 | cobra 无 i18n；只改 `Short/Long` 不够，`Usage:`/`Available Commands:`/`Flags:` 都是模板里的英文 |

## 3. 踩到的坑

**① cobra 默认 flag 的说明改不掉（第一版没生效）**

`root.Flags().Lookup("version")` 在 `InitDefaultVersionFlag()` 之前是 nil，而把覆盖代码放在 `SetHelpTemplate` 附近时又因为 flag 尚未建立而空转。最终方案：显式 `InitDefaultHelpFlag()` + `InitDefaultVersionFlag()`，再用 `localizeDefaultFlags` 同时查 `Flags()` 与 `PersistentFlags()`；并补 `TestDefaultFlagsAreLocalized` 直接断言 `f.Usage`，不再依赖"跑一遍看输出"。

**② 二进制名撞上系统黑名单**

验证时把产物命名为 `sc.exe`（当时图短），被安全策略拦下——`sc.exe` 是 Windows 服务控制程序，在内置黑名单里。改用 `sycode-cli.exe` 后正常。这条已记入项目记忆。

## 4. 端到端验证

```
$ sycode --version          sycode 0.1.0 (build 76519be)        退出码 0
$ sycode version            同上                                 退出码 0
$ sycode bogu               参数错误：未知命令 "bogu"…            退出码 2
$ sycode --cwd2 /tmp        参数错误：未知参数 --cwd2             退出码 2
$ sycode run                参数错误：run 需要且只需要一个提示词…   退出码 2
$ sycode run "hi"           错误：headless 模式尚未实现（见 …P4）  退出码 1
$ sycode --help             全中文，无 completion 命令
```

## 5. 遗留

- 帮助页仍有一处 `sycode [flags]`（cobra 由 `UseLine` 生成），可接受；若要彻底汉化需自写 usage 模板的该行。
- `run` 子命令的真实实现属 P4。
- 交互界面（P1.4）尚未开始，`sycode` 无参数时返回"交互式界面尚未实现"。
