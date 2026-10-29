# SyCode 路线图

原则：**每期都能独立跑起来并验收**。不追求一次写完，追求每一期结束时代码库是可运行、可测试、可解释的。

图例：⬜ 未开始 · 🟨 进行中 · ✅ 完成 · ⏸ 待设计（需要先出 PRD/RFC）

---

## P0 — 规划与地基 ✅（0.2 文档评审仍欠）

**目标**：文档齐备，工具链可用，第一次 `go test` 能跑。

| # | 交付物 | 验收 |
|---|---|---|
| 0.1 | PRD / ARCHITECTURE / ROADMAP / WORKFLOW / CODING-STANDARDS | ✅ 五份文档落盘，交叉引用无误 |
| 0.2 | 文档评审 | ⬜ 每份文档首行标注 `APPROVED` 或列出修改意见 |
| 0.3 | Go 工具链 | ✅ go1.26.3 windows/amd64（`D:\Apps\Go`）。验证：空模块 `go build` + `go test` 通过；`charm.land/bubbletea/v2@v2.0.2` 可拉取并编译 |
| 0.3.1 | 环境固化 | ✅ `go env -w` 持久化 `GOPATH=D:/Golang/code`、`GOPROXY=https://goproxy.cn,direct`、`GOCACHE=...`；`scripts/env.sh` 保留补 PATH 能力。同时清理家目录空 `.git`（曾导致 `error obtaining VCS status`，见风险登记） |
| 0.4 | 仓库初始化 | ✅ `go.mod`（`github.com/sy665/sycode`, go 1.24）、`Makefile`、`scripts/check.sh`、`.github/workflows/ci.yml`（三平台）、`LICENSE`（MIT）、`.gitattributes` |
| 0.5 | 空跑验证 | ✅ `bash scripts/check.sh` 全绿（gofmt / vet / build / test / race）；`-X main.version=0.1.0` 注入生效 |

> 环境注意：`go` 已写入系统与用户 PATH，但部分终端（含 IDE 内置 shell）继承的是裁剪过的 PATH，直接调用可能报 command not found。统一用 `source scripts/env.sh` 进入开发会话。

**退出条件**：`go build ./...` 成功、CI 绿、文档评审通过。

---

## P1 — 骨架与入口 ✅

> 施工契约：[CODE-DESIGN.md](./CODE-DESIGN.md) §1（启动顺序）、§2（入口层）、§3（基础设施层）。
> 开工前先按契约写函数骨架（签名 + 空实现 + 测试文件名），再填实现。

**目标**：`sycode --version` 能跑，`sycode` 能进入一个空 REPL（能输入、能退出）。

| # | 内容 | 验收 |
|---|---|---|
| 1.1 | `cmd/main.go`：日志初始化、版本注入、退出码 | ✅ `--version` 输出 `0.1.0 (build abcdef)`，退出码 0/1/2 分离 |
| 1.2 | `internal/logging`：slog + 文件命名 + `SYCODE_LOG_LEVEL` | ✅ debug 级别写出 `~/.sycode/logs/sycode-<ts>.log`（JSON） |
| 1.3 | `internal/cli/cmd`：cobra root + `version` + `run` 占位 + 全中文帮助 | ✅ `sycode run "hi"` 返回未实现错误，退出码 1；`bogu` 退出码 2 |
| 1.4 | `internal/cli/repl`：bubbletea 最小程序（textarea + 退出） | ✅ `Ctrl+C` 两次退出、`/exit` 退出；非 TTY 友好报错不挂死 |
| 1.5 | `internal/cli/cmd/deps_test.go`：依赖方向断言 | ✅ 注入 `logging→cli/repl` 时测试失败，撤回后恢复 |
| 1.6 | CI：三平台 `fmt/vet/test/race` | ✅ 工作流已就位（待推送后首次验证） |

**退出条件**：P1 全部勾选；`go test -race ./...` 无数据竞争。

> 状态（2026-10-29）：**P1 已完成**，代码 1,214 行（含测试），`bash scripts/check.sh` 全绿。

---

## P2 — 模型接入（单 provider 打通）✅

**目标**：能把一段对话发给模型并流式打印出来（还没有工具）。

| # | 内容 | 验收 |
|---|---|---|
| 2.1 | `internal/providers`：`registry.yaml` + 加载 + 查询 | ✅ 内嵌表解析 + 6 类非法输入校验；未命中返回 `false` 不 panic |
| 2.2 | `internal/config`：分层加载 + 密钥解析 | ✅ 三级优先级；缺 key 报"请设置环境变量 X"，错误中无密钥内容 |
| 2.3 | 启动接线：`run()` 串起注册表 → 配置 → 密钥 → 界面 | ✅ 配好 key 时标题显示模型、日志只记 provider/model/base_url；缺 key 退出 1 并指出环境变量名，不进入界面；`--version` 不依赖密钥 |
| 2.4 | `internal/llm`：`Request`/`Message`/`StreamEvent` + 系统提示 | ✅ 单测覆盖分段构建与终态事件（每段可单独断言；`done`/`error` 是唯一终态，`errEvent(nil)` 有兜底） |
| 2.5 | `internal/llm/openai.go`：SSE 流式解析 | ✅ `httptest` 回放四类：正常（含 usage）、缺 `[DONE]`、截断（判为错误）、取消后 goroutine 不泄漏；分片工具调用按 `index` 归并 |
| 2.6 | 重试与错误分类 | ✅ 429 退避重试 3 次后报错并逐次发 `EventRetry`；400/401 一次即失败；**吐字之后不再重试**（`partialError`） |
| 2.7 | REPL 流式渲染 + `Esc` 中断 | ✅ 正文增量即渲染；重试显示「第 N 次失败，正在重试…」；`Esc` 保留已生成内容并标注已中断；流异常结束有提示；流式期间拒绝新输入 |

**退出条件**：真实端点下 `sycode run "列出当前目录文件"` 能流式返回（此时模型会说"我没有工具"——正常）。

> 状态（2026-10-29）：**P2 进行中**，2.1–2.3 已完成（`providers` / `config` / 启动接线），下一步 2.4。

---

## P3 — 工具层与权限 ✅

**目标**：模型能真正读写文件、跑命令，且每次越界都要人点头。

| # | 内容 | 验收 |
|---|---|---|
| 3.1 | `filesystem.Guard` + `GitAwareness` | ✅ 策略表 8 种场景全测；符号链接穿越（含「父目录是链接、目标还不存在」）用可替换解析器覆盖——Windows 默认不给建真链接；`work2` vs `work` 前缀陷阱有回归 |
| 3.2 | `tools.Registry` + schema 导出 | ✅ nil/空名/重名报错且不改表；`All`/`Schemas` 与注册顺序一致；`Schemas` 返回副本（外部改写不污染工具 schema）；`clip` 按 rune 收尾并切在行边界 |
| 3.3 | `read_file` / `glob` / `grep` | ✅ 分段读 + 二进制探测 + 16MiB 上限；`**` 自己实现（模式转正则，零层也匹配）；include 过滤、忽略项与 `.git` 跳过、限量与截断提示；三类工具共用一条权限通道 |
| 3.4 | `write_file` / `edit_file` | ✅ 原子写（同目录临时文件 + Sync + Rename）与回读校验；拒绝时副作用为零；内容未变不碰文件；`edit_file` 唯一匹配、歧义列候选行号、0 命中提示用 grep；diff 进 `Display` |
| 3.5 | `bash` | ✅ 超时（默认 120s / 上限 600s，杀整棵进程树）、超 32KiB 落盘到 `~/.sycode/bash`、危险命令在弹窗标 ⚠、shell 选择按 ADR-006（与系统提示同源）、非 0 退出码当信息而非故障 |
| 3.6 | `appstate` 工具循环（`agentcore` 装配工具） | ✅ 多轮调用（模型要工具 → 执行 → 回灌 → 再请求）、25 轮上限、连续 3 次失败追加换做法提示；工具失败也回灌；`agentcore.New` 取代 `Direct`，`repl` 未改一行 |
| 3.7 | 权限弹窗 | ✅ 允许一次 / 本会话总是允许 / 拒绝；拒绝时副作用为零（真 `write_file` + 真 `Guard` 覆盖"允许后落盘 / 拒绝后不碰盘"）；弹窗期间按键不进输入框，`Esc` 拒绝、`Ctrl+C` 拒绝并退出；顺带把 `Display` 接进界面（3.7b：`⎿` 结果行、diff 绿/红、列表缩进、超 12 行省略） |
| 3.8 | `internal/cleanup`：artifact 回收（`~/.sycode/bash/` 等，保留 7 天） | ✅ 7 天前的 artifact 在下次启动被清理（真二进制验过：8 天前的被删、1 分钟前的保留、日志留 `count`）；单测覆盖空目录 / 边界（不晚于即清）/ 子目录不递归 |

**退出条件**：能完成"读 README → 改一个字符串 → 跑 `go build` 验证"的端到端流程。

---

## P4 — 会话与上下文可视化 ✅

**目标**：关掉再打开，对话还在；知道上下文用掉多少。

| # | 内容 | 验收 |
|---|---|---|
| 4.1 | `session.Store`：JSONL 追加、事件解码 | ✅ 追加-重放一致（逐字段相等）；坏行（半截 JSON / `seq=0`）跳过并计数，空行不算；上一进程死在半行时先补换行再追加；`List` 按 `Updated` 倒序、`Latest` 取最新；`namespaceDirName` 区分同名不同路径、同目录不同写法归一（Windows 折大小写） |
| 4.2 | 会话发现与恢复（按工作目录命名空间） | ✅ 同目录二次启动自动续上（真二进制验过：第二次启动日志 `已恢复会话 turns=1`，会话目录不增加）；`--new` 新建（会话数 1→2）；恢复的历史画在屏幕上并有 `已恢复 <ID>（N 轮）` 与分隔线；回合写库用 `defer`，正常/出错/`Esc` 中断都留痕 |
| 4.3 | `projection`：事件 → 消息列表 | ✅ 事件 → 中立 `Turn{User, Assistant, Tools}`：`user_message` 开轮、其后 `assistant_turn` 填入（中途多段用空行连接，不覆盖）；无 user 打头的回答单独成轮不丢弃；元信息/未知类型/坏载荷跳过。**注：先于 4.2 做**——恢复要先有投影 |
| 4.4 | `/context` 占用显示 | ✅ `/context` 输出分类明细（系统提示与工具 / 用户 / 助手 / 工具结果）与合计 `/ 窗口（%）`；**窗口未知显示"上限未知"而不是 0%**（`GetModelContextWindow` 返回是否已知）；有供应商上报的 `PromptTokens` 时以它为准并标注来源，否则显示 `≈` 与"（估算）"。基数（系统提示 + 工具 schema ≈2795 token）在装配时算一次传下去 |
| 4.5 | `/clear`、`/sessions`、`/help` | ✅ 五条命令一张表（`/exit` 带 `/quit` 别名），`/help` 从同一张表渲染；`/clear` 清上下文与会话缓冲但**不碰会话文件**（提示里说明"记录仍然保留"）；`/sessions` 列出本目录会话（`ID · 轮数 · 时间 · 标题`，过滤 0 轮空壳），**只列不切**；未知斜杠命令按普通输入处理（现状，有测试钉住） |
| 4.6 | headless `sycode run` 完整实现 + `--format json` | ✅ `run "<提示词>"` 走同一个 `AgentCore`：text 边生成边写、json 输出 `{text, tools[], usage}`（工具摘要只取首行）。**不恢复、不落盘**，冒烟确认 `~/.sycode` 下只多了 `logs`。权限默认拒绝（无人确认），`--allow-write` 才放行，且只放开"本来要问用户"的那一类——`Guard` 硬拒的路径仍然拒绝。退出码：缺参/非法格式 2，执行失败 1 且保留已生成的半句话 |
| 4.7 | 保留策略落地：会话 14 天、日志 14 天、输入历史 1000 条上限 | 启动时按策略回收；`~/.sycode/` 体积不随时长单调增长 |

**退出条件**：一次真实任务跨两次启动完成，历史完整。

---

## P5 — 分发与打磨 ⬜（卡在 5.0 决策）

**目标**：从"本机能跑"到"别人装得上、用得明白"。这一期**不扩张功能**，只补交付缺口。

### 5.0 先定分发渠道（**开工前必须定，否则 5.1/5.2 无从下手**）

仓库目前**没有远端**（2026-10-29 定的）。没有可下载的发布地址，安装脚本与 npm 包装都只是空壳——它们能写出来，但指向一个 404。

| 选项 | 含义 | 代价 |
|---|---|---|
| **A. 推到公开仓库** | 由 CI 出 release，安装脚本/npm 从 release 下载 | 需要远端与 CI 权限；项目对外可见 |
| B. 只做本地打包 | `scripts/package.sh` 产出带平台名的压缩包，自己传给自己 | 不对外，安装靠手工 |
| C. 只走 `go install` | 依赖模块路径能被 proxy 抓到 | 用户必须先装 Go 工具链 |

**建议 A**：装不上的 install.sh 比没有更糟。但这一步要人点头（对外可见 + 远端地址）。

### 5.1 ~ 5.5

| # | 内容 | 验收 |
|---|---|---|
| 5.1 | 构建产物：`darwin`/`linux`/`windows` × `amd64`/`arm64`，`CGO_ENABLED=0`，`-s -w -X main.version -X main.commit`；windows 出 `zip`、其余 `tar.gz`；`checksums.txt`（sha256） | 快照构建能在 CI 上产出全部平台产物；解压后任一产物的 `--version` 显示注入的版本与提交号；每个产物都在校验文件里 |
| 5.2 | 安装路径（按 5.0 二选一）：`scripts/install.sh`（检测平台 → 下载 → **校验 sha256** → 装到 `~/.local/bin`）或 npm 包装（`postinstall` 下载 + 校验） | 干净环境一键装完能 `sycode --version`；校验不通过必须**报错退出**，不能留一个半坏的二进制；重复执行是幂等的 |
| 5.3 | 启动与体积：先量后优化。基线（2026-10-29，Windows）：`--version` 单次约 **320ms**，二进制 **41MB**（已 `-s -w`） | 三平台各测一次 p50 并写进本文；体积记录趋势而不设硬指标——真正该省的是用户下载时间，不是数字好看 |
| 5.4 | 交付文档：README 的安装与首次使用、CHANGELOG 的 v0.1.0 定版、`docs/` 五份文档的评审状态（P0.2 还欠着） | 新人照 README 能走到"改一处 → 跑测试"；每份文档首行有 `APPROVED` 或明确意见 |
| 5.5 | 交互打磨（按"用户会撞到"排序，来自 P3/P4 的遗留）：未知斜杠命令给提示（现在当普通输入发出去，白花一次请求）；窄终端下弹窗与 diff 换行；`/sessions` 标出当前会话；逐条复查用户可见错误是否带"下一步做什么" | 每一项都有测试或可复现的检查步骤；**不夹带新功能** |

**退出条件（v0.1 可交付）**：

- 干净机器装得上；`sycode`（交互）与 `sycode run "…"`（无界面）都能跑
- "读 README → 改一处 → 跑 `go build` 验证"端到端成立（P3 的退出条件）
- 关掉再打开，同一目录的会话接得上（P4 的退出条件）
- 文档与代码一致：`deps_test` 绿、`CODE-DESIGN` 的函数表与代码对得上、CHANGELOG 有 v0.1.0 条目

**不在 P5 做的**：

- 上下文管理与子代理——P6 的待设计专题，各自要 PRD/RFC
- `/sessions` 的会话切换——要先定"进行中的回合怎么办""权限缓存是否沿用"
- 恢复的历史带工具痕迹——要改 `AssistantTurn` 的存储格式，属契约变更
- 任何"顺手加的工具"：工具集上限 10 个（PRD 非目标），现在 6 个，加之前先说清缺什么

---

## P6 — 待设计专题 ⏸

这两项是本项目最想表达的能力，**不并入 v0.1**，各自需要 PRD + RFC：

| 专题 | 需要先回答的问题 |
|---|---|
| **上下文管理** | 预算公式怎么定？手动 `/compact` 与自动触发的边界？摘要 schema（目标/约束/发现/已完成/相关文件）？压缩后如何保住 KV cache 收益？ |
| **子代理** | profile 格式（provider/model/权限/超时）？父子上下文如何裁剪？结果回传只给结论还是给证据？并发还是串行？失败如何向上暴露？ |

后续候选（未排期）：Skills 系统、MCP 接入、hashline 编辑（ADR-003 演进）、`/btw` 侧问、hooks。

**明确不做**：云端任务编排、远程执行、内置遥测、大量垂直工具打包。

---

## 风险登记

| 风险 | 触发信号 | 应对 |
|---|---|---|
| **TUI 是最大工作量**（常见实现占源码 43%，13.8k 行） | P1 之后 `repl.go` 迅速逼近千行 | 从 P1 就按常见实现分包落地（`stream/` `output/` `markdown/` `widgets/`…），不接受"先塞进 repl.go 以后再拆" |
| 上层目录存在残缺 `.git` | `error obtaining VCS status: exit status 128`（且 `git rev-parse` 报 not a repository） | 向上逐层查 `.git`；改名或删除。家目录空 `.git` 已于 2026-10-29 处理 |
| 依赖要求高于本机 Go 版本，触发 toolchain 自动下载 | `requires go >= 1.xx; switching to go1.xx`，随后 `missing GOSUMDB` 或缓存 `.partial` 占用 | 优先用 `GOTOOLCHAIN=local` 装兼容版本的开发工具；必要时单独排查 toolchain 缓存 |
| 终端 PATH 裁剪导致 `go` 不可见 | `go: command not found`（工具链实际已装） | `source scripts/env.sh`；不要误判为未安装 |
| 默认 GOPROXY 不可达 | `proxy.golang.org ... Bad Gateway` | 用 `https://goproxy.cn,direct`（已写入 env.sh） |
| 模型工具调用不稳定 | 同一任务频繁 3 次失败 | 收敛工具描述、加示例、必要时按模型覆写系统提示 |
| Windows shell 差异 | 命令在 CI 与本地行为不一 | ADR-006 + Windows CI 常态跑 |
| 范围蔓延 | P4 里开始写 compaction | 严格执行"待设计专题不提前动工"，违规回退 |
| **P5 卡在分发渠道未定** | 5.1/5.2 写完了却指向 404 | 先定 5.0；定不下来就只做 B（本地打包），别写半截的 install.sh |
| **本机没有包管理器** | GoReleaser 装不上，`winget`/`scoop`/`choco` 都没有 | 发布流程放 CI 跑；本机只验 `go build` 与快照产物 |
| 二进制 41MB，下载慢 | 用户装上要等 | `install.sh` 先打印体积再下载；必要时按平台拆 optional 依赖，但先量再说 |
| 文档与代码漂移 | 接口签名对不上 | 评审第 1 项就查契约；`docs` 变更与代码同 PR |
