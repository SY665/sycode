# 实施记录：P3 单元 3.1 `internal/filesystem` 权限判定

- 日期：2026-10-29
- 结论：完成，质量门全绿；`guard.go` 368 + `gitawareness.go` 118，测试 442 + 164
- 新增直接依赖 1 个：`github.com/go-git/go-git/v5`（选型表已批准，只用它的 gitignore 解析器）

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `internal/filesystem/guard.go` | 368 | `Permission`/`Op`/`Reason`/`ReasonCode`、`NewGuard`、`Resolve`、`Check`、`IsBlocked`、`WorkDir`、`vcsEntry`/`sensitiveHomeEntry`/`blockedPrefix`/`isArtifact`/`insideWorkDir`、`defaultBlockedPaths`、`bashOutputDir`、`expandHome`/`absClean`/`isUnder`/`normalizeCase`/`resolveSymlinks` |
| `internal/filesystem/gitawareness.go` | 118 | `NewGitAwareness`、`Root`、`IsIgnored`、`collectPatterns`、`parseIgnoreFile`、`isDir` |
| `guard_test.go` | 442 | 24 组：策略表逐行、前缀陷阱、`~` 展开、未知操作、空路径、无 git / 无工作目录、符号链接穿越（真链接 + 替身两路） |
| `gitawareness_test.go` | 164 | 8 组：无规则、通配/取反/目录模式/锚定、嵌套 domain、子目录覆盖根、跳过 `.git`、根外路径、注释与空行 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| `Permission` 零值 = `Denied` | 新增分支忘记赋值时，行为是"拒绝"而不是"放行"。安全相关枚举的默认值必须靠得住 |
| 判定结果带 `Reason{Code, Message}` | `Code` 让 UI 能分支（如"因忽略而拒绝"要不要提示改 `.gitignore`），`Message` 直接进权限弹窗。只返三态会让弹窗里只剩"需要确认"，用户不知道在确认什么 |
| 判定顺序 artifact → 系统 → 家目录隐藏 → 工作目录内 → 目录外 | artifact 必须最先判：它在 `~/.sycode` 里，晚一步就会被"家目录隐藏项"误伤成 Denied |
| **新增两条拒绝规则**：`.git/` 与"家目录隐藏即敏感" | 前者存远端地址（可能带 token）与钩子脚本；后者是凭证集中地，逐个列文件必然漏。策略表已同步补两行（ARCHITECTURE §4.3） |
| `isUnder` 用 `filepath.Rel` 而非字符串前缀 | 前缀匹配会把 `/tmp/work2` 当成 `/tmp/work` 的子目录——换个目录名就绕过了权限。有专门的回归用例 |
| Windows 下比较前折小写 | 那里路径不分大小写，不折会把 `C:\Work` 与 `c:\work\x` 判成不同目录，结果是把内部路径当成外部（多问一次）或反过来 |
| `NewGuard` 不返回 error | 取不到工作目录就退化为"工作目录为空"，于是所有路径都按"目录外"处理。构造期失败不该让程序起不来，但也不能放行 |
| `Resolve` 目标不存在时解析最近的已存在祖先 | `write_file` 的目标通常还不存在；只解析"存在的路径"会漏掉"父目录是符号链接"这条穿越途径 |
| **`evalSymlinks` 做成包内变量** | Windows 默认不给普通用户建符号链接（实测 `A required privilege is not held by the client`），而这正是最该压住的攻击面。测试替换解析器即可模拟穿越；真链接用例保留给允许建链接的平台 |
| `collectPatterns` 不下沉到已忽略的目录 | 那些目录里的 `.gitignore` 在 git 语义里也不生效；跳过还能避免为了遍历 `node_modules` 把启动拖慢 |
| `.git` 不参与 gitignore 判定 | 它由 `vcsEntry` 独立拒绝；两套规则叠加会让"为什么被拒"变模糊 |

## 3. 验证

```
go test ./internal/filesystem/ -v → 32 个顶层测试（含子测试），真链接用例在 Windows 上 SKIP
bash scripts/check.sh             → 全部通过（-race 本机无 gcc 跳过）
```

策略表 8 行逐条有对应用例；额外压住的边界：

- 符号链接指向工作目录外 → `Pending/Outside`（不是 `Granted`），指向系统目录 → `Denied/System`
- 符号链接 + 目标文件不存在（新建）→ 仍判为目录外
- `work` vs `work2` 前缀陷阱 → 判为目录外
- `~/.ssh/id_rsa`、`~/.sycode/configs.json` → `Denied/Sensitive`
- `~/.sycode/bash/x` → 读 `Granted`、写与执行 `Denied`
- 根目录 `*.log` 被子目录 `!readme.txt` 取反覆盖 → 子目录优先
- 子目录规则不影响兄弟目录

## 4. 遗留

- `bashOutputDir` 目前只有 `NewGuard` 在用；bash 工具（3.5）会把超长输出落在这里。
- `.git/info/exclude` 与全局 `core.excludesFile` 未载入（README 级说明写在 CODE-DESIGN 的"已知限制"）。
- Windows 的 `Pending` 判定依赖 `normalizeCase`；如果将来支持 UNC 路径（`\\server\share`），`filepath.Rel` 的行为要重新验。
- `Check` 不判断"文件是否存在"：读不存在的文件仍是 `Granted`，由工具自己报"文件不存在"——权限与存在性是两件事。
