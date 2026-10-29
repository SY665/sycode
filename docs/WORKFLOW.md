# SyCode 开发流程（WORKFLOW）

本文件规定**工作怎么流转**：从想法到合并的每一步、每步产出什么、什么算"做完"、留痕放哪。
编码细节看 [CODING-STANDARDS.md](./CODING-STANDARDS.md)；产品与技术决策见 [PRD.md](./PRD.md)、[ARCHITECTURE.md](./ARCHITECTURE.md)。

---

## 0. 一条铁律

> **人写 prompt 与决策，agent 写代码与文档初稿；每一次往返都留痕。**

这不是风格偏好，是这个项目的可维护性来源：三个月后回看某段代码为什么长这样，答案应该是 `.ai-interactions/` 里的某次对话，而不是某个人的记忆。

## 1. 五步循环

```
Spec ──► Plan ──► Review ──► Task ──► Feedback
 ▲                                      │
 └──────────────────────────────────────┘
```

| 步骤 | 谁主导 | 产出 | 落点 |
|---|---|---|---|
| 1 Spec | 人 | 一段可验收的需求描述（含边界与不做的事） | `.ai-interactions/prompts/<phase>/prompt-N_<slug>.md` |
| 2 Plan | agent | 设计文档 / 任务拆解（不写实现代码） | `.ai-interactions/outputs/<phase>/output-N_<slug>.md` |
| 3 Review | 人 | 评审意见（通过 / 打回 + 理由） | 追加为下一个 `prompt-N+1_*.md` |
| 4 Task | agent | 代码 + 测试 + 文档更新 | 代码仓库 + 任务目录 |
| 5 Feedback | 人 | 验收结论 / 缺陷清单 | 追加 prompt，回到 1 或 4 |

**规则**
- Plan 阶段**不许**直接改代码。若 agent 未经 plan 就动手，视为流程违规，回退。
- Review 必须显式给出结论：`APPROVED` / `CHANGES REQUESTED`（写在该 prompt 文件首行）。
- 同一 `phase` 内编号连续，不跳号、不复用。

## 1.1 预授权模式（2026-10-29 起）

人可一次性把 Review 门的决策权下放给 agent。进入该模式的条件：人明确说过"全交由你开工"一类的话，并记录在本节。

**预授权下的变化**：

- agent 自己出 Spec + Plan，**自审后直接开工**，不再逐单元等待 `APPROVED`
- 评审结论仍要写进 prompt 文件首行，格式 `APPROVED（预授权自审）`，并注明依据
- 人保留**事后复核权**：复核 diff 后若判 `CHANGES REQUESTED`，agent 必须回退该单元，且不得辩解
- 触及以下任一情况时**预授权自动失效**，必须停下等人明确同意：
  1. 改动既有公开契约（`docs/CODE-DESIGN.md` 里的函数签名、`ARCHITECTURE.md` 里的接口）
  2. 新增直接依赖——**例外**：`ARCHITECTURE.md` §7 选型表内的依赖已于 2026-10-29 一次性授权，可直接引入；表外新增仍需停下
  3. 触碰权限、密钥、会话格式（`CODING-STANDARDS.md` §8）
  4. 单元规模超过上限（3 文件 / 300 行）
  5. 连续两次质量门失败且原因不明

**为什么保留这几条例外**：预授权是为了省掉"写 180 行还要等一句批准"的往返，不是把不可逆决策也交出去。

## 2. 留痕目录规范

```
.ai-interactions/
├── prompts/
│   └── phase-N/
│       ├── prd.md                    # 阶段级需求（可选）
│       ├── prompt-1_<slug>.md        # 人发出的话，原文保存
│       └── prompt-2_<slug>.md
├── outputs/
│   └── phase-N/
│       ├── output-1_rfc.md           # agent 产出，原文保存
│       └── output-2_plan.md
└── tasks/
    └── <issue-slug>/
        ├── prompt-1_<slug>.md
        ├── output-1_plan.md
        ├── prompt-2_<slug>.md
        └── output-2_review.md
```

**硬性要求**

1. **原文不改**：output 落盘后不得编辑措辞（可以追加 `## 后续修正` 段落，但不能改原结论）。历史被"美化"过就失去价值。
2. **一次往返一对文件**：prompt-N 与 output-N 编号对应；只发 prompt 没产出，就只留 prompt。
3. **任务级文件夹归属**：绑到具体 issue 的工作放 `tasks/<issue-slug>/`（slug 用 `issue-<编号>-<短描述>`，如 `issue-12-edit-file-anchor`）；无 issue 的探索性工作放 `prompts|outputs/<phase>/`。
4. **不存入对话的产物**：API key、token、私有仓库路径、他人隐私数据。提交前自检。
5. **一句话摘要**：每个 prompt 文件开头写 3 行以内摘要（`目标 / 约束 / 期望产出`），方便检索。

模板：[templates/prompt.md](./templates/prompt.md)、[templates/plan.md](./templates/plan.md)、[templates/review.md](./templates/review.md)。

## 3. 阶段划分与门禁

路线图分 6 期（P0~P5 + 待设计专题），见 [ROADMAP.md](./ROADMAP.md)。每期三道门：

| 门 | 条件 | 谁来判 |
|---|---|---|
| **G1 进入** | 该期 PRD/计划已合并；依赖项（工具链、常见实现决策）就绪 | 人 |
| **G2 实现中** | 每个 issue 走完五步循环；CI 绿；依赖方向测试通过 | 人 + CI |
| **G3 退出** | 该期验收清单逐条勾选；`CHANGELOG` 有条目；文档与实现一致 | 人 |

**G3 不接受"差不多"**：未通过的条目要么降级到下一期（写明原因），要么从范围里删掉（写明原因）。不允许留"待补"的验收项进入下一期。

## 4. Issue 粒度

一个 issue = **一个可以独立验收的行为变化**。

| 合格 | 不合格 |
|---|---|
| "`edit_file` 在多处匹配时返回候选行号并失败" | "实现 edit_file 工具"（太粗，含 5 个行为） |
| "`bash` 输出超过 32KB 时落盘为 artifact" | "优化工具输出"（不可验收） |
| "`/model` 切换后重新计算上下文占用" | "改进 REPL"（无边界） |

**经验值**：一个 issue 对应 diff ≤ 600 行（不含测试与文档）。超了就拆——拆分本身也是 plan 阶段该做的事。

### Definition of Ready（DoR）
- [ ] 有编号的验收标准（能写成"给定…当…则…"）
- [ ] 明确不做什么（防止范围蔓延）
- [ ] 涉及的文件/包已列出
- [ ] 是否触碰权限、密钥、会话格式——若是，标注"需额外评审"

### Definition of Done（DoD）
- [ ] 验收标准逐条通过（结论写进 output）
- [ ] `gofmt` / `go vet` / `go test ./...` 全绿
- [ ] 新增逻辑有测试；修复缺陷有能复现原缺陷的测试
- [ ] 公开行为变化已更新对应 `docs/*.md`
- [ ] `.ai-interactions/` 往返记录完整
- [ ] `CHANGELOG.md` 的 `Unreleased` 有该条目（用户可见的变化才写）

## 4.1 推进单元与上下文预算

实际开发按**模块**或**功能**推进，一次推进多少取决于当前会话的上下文余量。规则如下，目的是**任何时候被打断都能无损续接**。

### 推进单元的两种粒度

| 粒度 | 定义 | 适合 |
|---|---|---|
| 按模块 | 一个包内的若干文件（如 `filesystem` 的 guard + gitawareness） | 骨架期、基础设施 |
| 按功能 | 一个用户可见行为的端到端切片（如 `read_file` 从 schema 到权限到 UI 展示） | 功能期 |

**单元上限**（经验值，超了就再切一刀）：≤ 3 个源文件、≤ 300 行 diff、≤ 1 个新直接依赖、≤ 6 个新函数。

### 每个单元必须收口到"可续接状态"

收口 = 四条同时满足，缺一条就不算完成：

1. `go build ./...` 通过（未实现的部分返回 `执行失败：尚未实现`，**不许留半截语法**）
2. `go test ./...` 通过；本单元新增代码至少有 1 个测试
3. 提交一次（Conventional Commits，一条 commit 只含本单元）
4. 更新 `.ai-interactions/STATE.md`（见下）

### `.ai-interactions/STATE.md`：唯一断点文件

每次收口更新，**只写"现在在哪、下一步做什么"**，不写日志（日志是 git 的事）。固定五段：

```markdown
# 当前状态
更新时间：2026-10-29 17:30 | 阶段：P1 | 当前单元：logging.Init

## 已完成（本阶段）
## 正在进行（未完成，写清卡在哪）
## 下一步第一件事（具体到文件与函数）
## 已知坑 / 需要绕过的东西
## 待决策（等人拍板，附选项）
```

### 上下文紧张时的收尾顺序

按此优先级降级，**不允许为了"写完"而跳过**：

1. 让当前文件恢复可编译（宁可不完整，不可语法错误）
2. 把 `STATE.md` 的"下一步第一件事"写到能直接开工的程度（文件 + 函数 + 验收点）
3. 提交
4. 才开始下一件事

### 新会话续接顺序

`STATE.md` → `git log -3 --stat` → `docs/CODE-DESIGN.md` 对应小节 → 对应 `docs/*.md` 的验收标准。**不要从 PRD 重新读起**，那是浪费预算。

## 5. 分支、提交、合并

| 项 | 规范 |
|---|---|
| 主分支 | `main`，保护分支，禁止直接 push |
| 分支名 | `feat/<issue>-<slug>`、`fix/<issue>-<slug>`、`docs/<slug>`、`chore/<slug>` |
| 同步方式 | `git rebase main`（保持线性历史），不 merge main 进分支 |
| 提交粒度 | 一个逻辑改动一个提交；不出现"fix typo"混在功能提交里 |
| 提交信息 | Conventional Commits：`<type>(<scope>): <summary>`，scope 用包名（`tools`、`repl`、`llm`、`session`） |
| 合并方式 | Squash merge；squash 后的信息用 PR 标题（已符合 Conventional Commits） |
| 版本 | 语义化版本；破坏性变更（如 ADR-003 的锚点格式）升 minor 并在 CHANGELOG 标 `BREAKING` |

**提交类型**：`feat` `fix` `refactor` `perf` `test` `docs` `chore` `build` `ci`

示例：
```
feat(tools): reject ambiguous edit_file matches with candidate lines
fix(repl): drop stale permission prompt after user interrupt
docs(arch): record ADR-006 shell selection order
```

## 6. 评审（Code Review）

评审由人做（agent 可先自审一轮并给出结论），按固定顺序看，**顺序不能颠倒**：

1. **契约**：工具名/参数/schema 变了没有？变了是否同步文档与测试？破坏性变更是否走 ADR？
2. **权限**：有没有绕过 `filesystem.Guard` 的文件访问？新路径是否被阈值覆盖？危险命令是否确认？
3. **错误路径**：失败时模型能看懂吗？会不会死循环重试？中断是否留脏状态？
4. **并发**：谁写 UI 状态？goroutine 有退出路径吗？channel 谁关闭？
5. **测试**：测的是行为还是实现？有没有依赖时间/sleep/网络？
6. **可读性**：命名、注释（只解释"为什么"）、无死代码。
7. **留痕**：`.ai-interactions/` 是否完整、是否原文。

**打回标准**（任一命中即 `CHANGES REQUESTED`）：
- 绕过 Guard 的文件 IO
- 密钥/token 出现在日志、错误信息、会话文件
- 工具 `Execute` 可能 panic
- 无测试的新逻辑，或测试只覆盖 happy path
- 一个 PR 干了多个 issue 的事
- 缺少留痕文件

**评审用语**：指出问题 + 给出可执行修改方向，不做风格说教。风格问题由 `gofmt` + 本仓库规范解决，不进评审讨论。

## 7. 发布

| 环节 | 做法 |
|---|---|
| 触发 | `main` 打 tag `vX.Y.Z` |
| 构建 | GitHub Actions 矩阵：linux/darwin/windows × amd64/arm64，`-trimpath -ldflags "-s -w -X main.version=..."` |
| 产物 | 单二进制 + `checksums.txt`；后续再加 npm 包装与安装脚本（P5） |
| 变更日志 | 从 `Unreleased` 移到对应版本号，写清 用户可见变化 / 内部变化 / BREAKING |
| 回滚 | 保留前两个版本的 tag，可直接重新发布 |

## 8. 文档维护责任

| 文档 | 什么时候必须改 |
|---|---|
| `PRD.md` | 需求范围变化（增/删/降级） |
| `ARCHITECTURE.md` | 新增包、改核心接口、改数据流、新增 ADR |
| `CODE-DESIGN.md` | 新增/删除文件、改动某文件的公开函数签名、改启动顺序 |
| `ROADMAP.md` | 阶段进入/退出、优先级调整 |
| `CODING-STANDARDS.md` | 新增全局约定（少见，需专门 PR） |
| `AGENTS.md` | agent 工作方式变化（保持 ≤ 60 行，只放"必须知道"的） |
| `CHANGELOG.md` | 每个用户可见变更 |

**文档即代码**：文档变更随功能 PR 一起提，不单开"补文档"PR（除非纯错别字）。

## 9. 当前状态（2026-10-29）

- ✅ P0：PRD / ARCHITECTURE / ROADMAP / WORKFLOW / CODING-STANDARDS 初稿
- ✅ P0.3：Go 工具链就绪（go1.26.3，`D:\Apps\Go`）；`scripts/env.sh` 固化 PATH / GOCACHE / GOPROXY
- ✅ P0.4/P0.5：`go.mod`、`scripts/check.sh`、CI、LICENSE(MIT) 就位，质量门全绿
- ⬜ P0 待办：评审上述文档并在首行标注 `APPROVED`
- ⬜ 下一轮讨论：上下文管理、子代理两个专题（各自出 PRD + RFC 后再定 P6 范围）
