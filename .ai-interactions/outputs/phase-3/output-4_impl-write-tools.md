# 实施记录：P3 单元 3.4 写类工具（`write_file` / `edit_file` / `diff`）

- 日期：2026-10-29
- 结论：完成，质量门全绿；`write_file` 213 + `edit_file` 190 + `diff` 97，测试 377；`tools` 包累计 71 个顶层测试

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `write_file.go` | 213 | `WriteFileTool`、`readExisting`、`writeFileAtomic`、`writeAndVerify`、`renderWriteSummary` |
| `edit_file.go` | 190 | `EditFileTool`、`locateMatches`、`applyReplace`、`summarizeChange`、`joinLines` |
| `diff.go` | 97 | `EditDiffLine`、`buildDiff`、`formatDiff`、`diffSummary` |
| `input.go` | +20 | `requireText`（必填但允许空串）与 `itoa` |
| `write_test.go` | 377 | 21 组用例（见 §3） |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **先问权限，再读再写** | 被拒绝时文件必须一个字节都没动，否则"拒绝"就是句空话。有两条测试专门断言"拒绝后磁盘上什么都不存在" |
| **原子写：同目录临时文件 → Write → Sync → Chmod → Rename** | 直接 `os.WriteFile` 写到一半失败会留下半截源码；半截源码比"没改"危险得多——它可能还能编译通过一部分。临时文件**必须同目录**：跨文件系统 rename 不原子 |
| 写后**回读校验** | 磁盘满、被别的进程截断不会从 `Write` 返回，用户却会拿到"写着成功、其实是坏的"文件 |
| 内容未变化时**不写盘** | 改 mtime 会让 watch 与构建白跑一遍；也让"我改了吗"这件事在日志里更清晰 |
| 拒绝覆盖二进制文件 | 覆盖二进制几乎一定是误操作（模型把它当文本了），而且回读校验对它没意义 |
| `edit_file` 只接受**唯一匹配** | 模型拿着不唯一的 `old_string` 去改，改错地方的代价远大于让它重写一次。歧义时列出候选行号（最多 5 处 + 省略号），让它补上下文 |
| 0 命中的提示必须指向下一步 | 只说"没找到"，模型会原样重试；说"先用 grep 确认原文"才会换策略 |
| `old_string` 空串直接拒绝 | 空串在任何位置都匹配，替换只会随机毁掉文件 |
| **不引 diff 库、不写 LCS** | 只有"改动前后"一种场景，掐公共前后缀足够；它不会把改动说成没改（LCS 才需要精细对齐）。真需要时再换，成本被 `Display` 挡住 |
| `diffSummary` 进标题 | 弹窗标题带 `+2 / -1`，用户不用读完整个 diff 就知道动了多少 |

## 3. 验证

```
go test ./internal/tools/ -v → 71 个顶层测试全 PASS（本单元新增 21）
bash scripts/check.sh        → 全部通过
```

| 组 | 覆盖 |
|---|---|
| `write_file` 11 组 | 新建 + 建父目录 + 无残留临时文件、覆盖、内容未变跳过、工作目录外拒绝（无通道 / 用户拒）/ 零副作用、工作目录内必须询问、目录拒绝、二进制拒绝、允许空串清空、参数校验、diff 展示 |
| `edit_file` 9 组 | 唯一替换且保留其余内容、删除（`new_string` 空）、0 命中提示 grep、歧义列行号且不改文件、拒绝零副作用、文件不存在提示用 write_file、空 `old_string` 拒绝、内容未变跳过、diff 展示 |
| `diff` 3 组 | 5 种 diff 形态、摘要、`locateMatches` 上报上限与省略号 |

## 4. 遗留

- `bash` 工具（3.5）还没做；`Artifact` 落盘路径目前只有 `read_file` 的截断提示提到，没有真正写 artifact。
- 原子写在 Windows 上依赖 `os.Rename` 覆盖已存在文件（Go 用 `MoveFileEx(REPLACE_EXISTING)`）；如果目标被其它进程以独占方式打开，rename 会失败并给出中文错误——没有做重试。
- 权限"本会话总是允许"（`DecisionAllowSession`）只是枚举值，缓存逻辑在 3.7。
- 写操作只在单测里验证过；接进 `appstate`（3.6）后才有端到端。
