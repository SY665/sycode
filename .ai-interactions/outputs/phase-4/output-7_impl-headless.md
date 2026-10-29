# 实施记录：P4 单元 4.6 headless `sycode run`

- 日期：2026-10-29
- 结论：完成，质量门与 `-race` 全绿；`repl/headless.go` 165 + `agentcore/autoapprove.go` 29，测试 +190
- 冒烟三条：缺参 → 2、`--format yaml` → 2（文案含可选值）、端点不可达 → 1 且中文错误可操作；
  `~/.sycode` 下只多出 `logs`，**没有 sessions**（印证不落盘）

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `repl/headless.go` | 165 | `HeadlessFormat`/`HeadlessOptions`/`HeadlessResult`/`ParseFormat`/`RunHeadless`/`writeHeadlessJSON` |
| `agentcore/autoapprove.go` | 29 | `AutoApprove`（实现 `tools.PermissionRequester`） |
| `cli/cmd/root.go` | +45 | `BuildOptions`、`newRunCmd` 实装、`BuildEnv` 改收结构体 |
| `cli/cmd/root_test.go` | ±60 | 删"未实现"用例，加"无引擎/非法格式/--allow-write 传递"三组 |
| `repl/headless_test.go` | 130 | 7 组 |
| `cmd/main.go` | +30 | 按 `BuildOptions` 决定权限通道与会话 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **不恢复、不落盘** | `run` 是"跑一次就结束"的语义。落盘会让 `/sessions` 堆满两三条事件的一次性记录，而脚本用户根本不会去翻 |
| **权限默认拒绝** | 非交互模式下没有人能确认。默认放行等于"脚本里的一次误解就删了文件"。要写就显式 `--allow-write` |
| `--allow-write` 用 `AutoApprove` 而非 nil | 放行的只是"本来要问用户"的那一类；`Guard` 判 Denied 的（系统路径、`.git/`、家目录隐藏项）根本不走 perm，所以它**不是**"全放行" |
| `AutoApprove` 是空结构体 | 需要放行的地方显式构造它，读代码时一眼能看出"这一段没有人在把关" |
| text 模式**边生成边写** | 脚本能更早拿到第一段，不必等整句结束；代价是出错时正文已经出去了——所以出错前先补换行 |
| json 模式**最后一次性输出** | 半截 JSON 无法解析。但出错时仍输出一个**合法对象**（含已生成的 text），调用方既知道失败又拿到正文 |
| 工具摘要**只取首行** | `EventToolResult.Text` 的第二行起是给人当场看的展示块（diff/列表），脚本不需要 |
| `BuildEnv` 参数改成结构体 | 三个开关（`--new`/headless/`--allow-write`）都是"能不能动用户东西"，放一起显式给出比加三个 bool 参数清楚 |
| `ParseFormat` 的错误**不带**"参数错误：" | 命令行层会统一加前缀，自带前缀会打印成"参数错误：参数错误：…"（冒烟时真踩到） |

## 3. 验证

```
go test ./internal/cli/repl/ -run "TestRunHeadless|TestParseFormat"  → 7 组全 PASS
CGO_ENABLED=1 go test -race ./internal/cli/...                       → 全 PASS
bash scripts/check.sh                                                → 全部通过
```

二进制冒烟（隔离 home，配置把 base_url 指向不可达端口，不需要真 key）：
- `run`（缺参）→ `参数错误：run 需要且只需要一个提示词参数…`，退出 2
- `run --format yaml "hi"` → `参数错误：不支持的输出格式 "yaml"（可选 text / json）`，退出 2
- `run "列出文件"` → `错误：执行失败：无法连接模型服务（…）。请检查网络与配置里的 base_url。`，退出 1

**没验到的**：真 key 下的一次成功执行（本机没有可用密钥）；`--allow-write` 下真的写文件
（需要模型驱动，只能靠 `AutoApprove` 的单测与 `Guard` 的既有测试间接保证）。

## 4. 遗留

- 没有 `--resume` / `--session`：headless 不能接着某个会话跑。要做的话得同时定"续跑时是否落盘"
- 没有 `--model` 覆盖：`config.Resolve` 已预留参数，接上是小事，但要另测优先级
- `--allow-write` 没有"只放行读/只放行指定工具"的细分；等真实脚本用法出来再说
