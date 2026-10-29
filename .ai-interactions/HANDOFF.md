# 接手须知

> 这是**第 0 份**文档：先读完它，再按 `WORKFLOW.md` §4.1 规定的顺序读 `STATE.md` → `git log -3 --stat` → `CODE-DESIGN.md` 对应小节。
> 它只写"读代码前必须知道、但从代码里看不出来"的东西；具体进度与下一步在 `STATE.md`，别在这里重复维护。

更新时间：2026-10-29 22:40

---

## 1. 这是什么

SyCode 是 Go 写的终端 AI 编程代理（CLI + Bubble Tea TUI）。当前 v0.1 目标：能流式对话、能调工具改本地代码、权限可控、会话可恢复。

- 模块路径 `github.com/sy665/sycode`，仓库只在本机 `D:\project\owesome\code\sycode`，**没有远端**（2026-10-29 已定）。
- 技术栈：cobra、bubbletea/bubbles/lipgloss v2、yaml.v3、openai-go/v3、go-git/v5（只用 gitignore 解析器）。直接依赖 7 个，上限 15，见 `ARCHITECTURE.md` §7。
- 文档是真源：**接口签名以 `docs/` 为准**。文档与代码不一致时先提出来，不要私自改契约（改契约属预授权失效项）。

## 2. 包结构原则

- 包级结构按 `docs/ARCHITECTURE.md` §6 的分层来，**不额外套子目录**；文件按"一个功能一个文件"切。
- 新增目录必须先在 `ARCHITECTURE.md` 的目录树里登记，然后**随功能出现**再建，不一次建齐空壳。
- 结构取舍的理由记在 ADR 里（见 §8），不要靠记忆传递。

## 3. 环境起步（每个新 shell 第一件事）

```bash
source scripts/env.sh      # 补 PATH（Go 在 D:\Apps\Go）、GOCACHE、GOPROXY=goproxy.cn
go version                 # 期望 1.26.3
bash scripts/check.sh      # 质量门：fmt / vet / build / test / race
```

工具 shell 的 PATH 是宿主裁剪过的，`go: command not found` **不代表没装**。`-race` 需要 gcc：拿不到就跳过（脚本会提示），不是失败。

## 4. 现在到哪了（2026-10-29 22:40）

| 阶段 | 状态 |
|---|---|
| P0 文档/脚手架 | ✅ |
| P1 骨架（logging / cobra / TUI / 依赖断言） | ✅ |
| P2 模型链路（providers / config / 流式 / 重试 / REPL 流式） | ✅ 功能齐 |
| P3 工具与权限 | 3.1–3.6 ✅；**3.7 权限弹窗待做**；3.8 待做 |
| P4+ | 未开始：P4 = 会话与上下文可视化（JSONL 持久化、按工作目录恢复、`/context`、headless `sycode run`） |

> **一处会让新读者困惑的口径**：`ROADMAP.md` 里 P2 的退出条件写着 `sycode run "…"` 能流式返回，
> 但 headless 入口实际排在 P4.6。当时按"P2 功能齐"收的口，这条**没有真的达成**，别以为 P2 缺东西是回归。
> 另外 P2 的一行验收（真实端点下 `sycode run`）也需要真 key 才能补验。

代码约 10,800 行（源码 5,513 / 测试 5,286），28 笔提交，`go test ./...` 10 个包全绿。

分阶段明细与每单元的设计取舍在 `STATE.md`；写代码前要看函数级设计，翻 `docs/CODE-DESIGN.md` 的对应小节（那里的表格列到"哪个文件有哪些函数、签名是什么"）。

## 5. 读代码前必须知道的 6 条规则

这几条一旦违反，代码能编译、测试也过，但会让**已经想清楚的设计退化**：

1. **文件访问只有一个入口**：`internal/filesystem` 的 `Guard`。禁 `os.Open`/`os.WriteFile` 碰业务文件。三态 `Denied/Pending/Granted`，**零值是 `Denied`**（新增分支忘赋值时倾向拒绝），判定带 `Reason{Code,Message}` 供弹窗直接显示。工具里不要自己写权限判定——全部走 `tools` 包内的 `askAccess`（`permission.go`，私有）。
2. **`tools` 不 import `llm`**（`deps_test.go` 会拦）。两个 schema 类型各自独立，转换在 `appstate` 一处。
3. **终态事件只发一次**：`AgentCore.Submit` 返回的 channel 里，`EventDone`/`EventError` 只能有一个，且由 `appstate.runTurn` 发。provider 的 `done` 只表示"这一轮结束"，后面还有工具要跑。
4. **中文面向一切**：用户、模型、日志 message、测试失败信息都是中文；标识符/路径/命令/字段名/错误码保持原文。前缀只用 `参数错误：`/`执行失败：`/`权限不足：`/`超时：`/`不支持：`（`CODING-STANDARDS.md` §13）。错误文案要能让模型**自我修正**，否则它会原样重试。
5. **退出码 0/1/2** = 成功 / 一般错误 / 用法错误。配置类失败（缺密钥、`configs.json` 写坏）一律 **1**，2 只留给命令行用法错误。
6. **界面不碰 `llm.Client`**：`repl` 只依赖 `agentcore.AgentCore`。这道缝是为 P3 插 `appstate` 留的，回报是换引擎时 TUI 一行没改。别为了图快在 `repl` 里直接调 `llm`。

## 6. 下一步

看 `STATE.md` 的「下一步第一件事」——那里写到"文件 + 函数 + 验收点"级别，可以直接开工。现在指向 **3.7 权限弹窗**：

- `agentcore` 加 `RequestPermission`（**这是改接口契约**，`repl` 要一起改，属既定范围；同步 `ARCHITECTURE.md` §4 的接口定义——注意 §4 是"核心接口"，§8 是 ADR，别翻错）
- `DecisionAllowSession` 的缓存放 adapter，不放 `repl`（`repl` 重建时缓存不该丢）
- 现状 `perm` 传的是 `nil`，即**只读模式**：`read_file`/`glob`/`grep` 可用，写与执行被拒并说明原因。这是安全默认值，不是 bug。

## 7. 最容易重复踩的坑

完整清单在 `STATE.md` 末尾，这里是代价最高的几条：

| 坑 | 症状 | 做法 |
|---|---|---|
| `cmd.WaitDelay` 没设 | 1 秒超时的 `sleep 30` 拖到 30 秒才返回，超时形同虚设 | 设 3s；超时还要杀整棵进程树（`taskkill /T` / `kill(-pid)`） |
| SDK 的 `stream.Err()` | 连接断在半个分片处仍返回 `nil` | 别用它判完整；用结束信号（`finish_reason`/`usage`） |
| 相对路径直接读 | 报"文件不存在" | 模型给的相对路径是相对**工作目录**，不是进程 cwd；先 `guard.Resolve` |
| 给模型的内容带行号 | `edit_file` 匹配失败，行号被写进 `old_string` | 行号只进 `Display`，`Content` 保持原文 |
| `isUnder` 用字符串前缀 | `/tmp/work2` 被当作 `/tmp/work` 的子目录 | 用 `filepath.Rel` |
| Windows 建符号链接 | 报"A required privilege is not held" | 测试替换 `filesystem.evalSymlinks`；真链接用例留给 CI(Linux) |
| `go build ./...` | 二进制被写进仓库根 | `check.sh` 已改为 `-o <临时目录>`；手动构建也一样 |
| 用 `-X` 打版本到 `const` | 静默失效 | 必须是 `var version` |
| 在 bash 里 `python - <<'PY'` | 正文含 PS 字样被安全策略拦 | 用 Write 落 `.py` 再 `python x.py` |
| 手验配置 | 写脏真实 `~/.sycode` | `env USERPROFILE=<临时home> HOME=<临时home> ./sycode` |

## 8. 怎么验

```bash
# 单包
source scripts/env.sh && go test ./internal/tools/ -v

# 全量质量门（提交前必须）
bash scripts/check.sh

# 真二进制冒烟（隔离 home，不写脏配置）
source scripts/env.sh && out=$(mktemp -d) && go build -o "$out/sycode-test.exe" ./cmd
fake=$(mktemp -d); mkdir -p "$fake/.sycode"
echo "" | env USERPROFILE="$(cygpath -w "$fake")" HOME="$fake" OPENAI_API_KEY=sk-test \
  SYCODE_LOG_LEVEL=debug "$(cygpath -w "$out/sycode-test.exe")"
grep '工具已注册' "$fake/.sycode/logs/"*.log
```

- **工具 shell 没有 TTY**：`sycode` 只能验到"配置解析通过 → 报需要终端"。TUI 观感（流式逐字、`Esc` 提示位置、弹窗布局）必须**人工在真终端看**，不要让 agent 声称验过。
- 产物名别用 `sc.exe`（撞 Windows 服务控制程序黑名单）。
- 给被测程序传环境变量写成 `... | env VAR=x ./prog`：`VAR=x cmd | other` 只作用于管道左边。

## 9. 工作模式

**预授权模式**（`WORKFLOW.md` §1.1，人 2026-10-29 说"全交由你开工"）：自己出 plan、自审后直接开工，人保留事后复核权。但下列任一情况**预授权自动失效，必须停下等人明确同意**：

1. 改动既有公开契约（`CODE-DESIGN.md` 的函数签名、`ARCHITECTURE.md` 的接口）
2. 新增直接依赖（例外：`ARCHITECTURE.md` §7 选型表内的一次性授权，可直接引入）
3. 触碰权限、密钥、会话格式
4. 单元规模超上限（3 源文件 / 300 行 diff）
5. 连续两次质量门失败且原因不明

推进方式：一次一个单元，**每单元必须收口**——能编译、测试过、提交、更新 `STATE.md`。上下文紧张时按 `WORKFLOW.md` §4 的降级顺序收尾，**禁止留半截语法**。

> **一处已知的口径冲突，接手时先问一句**：`STATE.md` 里把 3.7 的 `AgentCore` 加方法写成"既定范围"，
> 但按上面第 1 条例外，"改动既有公开契约"要停下等人同意。两者冲突。我的处理是**仍按例外走**：
> 动 `AgentCore` 接口前问一句，别自己判"早就说好了"。顺序应当是：问 → 同意 → 改接口 + 同步 `ARCHITECTURE.md` §4 + 改 `repl`。

---

## 附：一次性读完的最小集合

| 顺序 | 文件 | 读什么 |
|---|---|---|
| 1 | 本文 | 世界规则、参考项目位置、坑 |
| 2 | `.ai-interactions/STATE.md` | 当前单元、下一步（到函数级）、坑全表 |
| 3 | `git log -3 --stat` | 最近三笔改了什么、风格 |
| 4 | `docs/CODE-DESIGN.md` | 只读当前单元涉及的小节，别整篇读 |
| 5 | `docs/ROADMAP.md` | 当前期退出条件 |
| 按需 | `docs/ARCHITECTURE.md` | §8 的 ADR（尤其 001 工具循环、005 权限与 IO 分离、006 shell 选择）、§7 选型表、§4 核心接口 |
| 按需 | `docs/PRD.md` | 只在验收标准不清时查，**不要从它读起** |
