# 实施记录：P3 单元 3.3 只读工具（`read_file` / `glob` / `grep`）

- 日期：2026-10-29
- 结论：完成，质量门全绿；两笔提交（读文件 / 找与搜）；`tools` 包 48 个顶层测试
- `**` 按用户决定**自己实现**，未引入表外依赖

## 1. 改动

| 文件 | 行数 | 内容 |
|---|---|---|
| `input.go` | 106 | `requireString` / `optionalInt` / `optionalString` / `typeName` / `exampleFor` |
| `permission.go` | 75 | `access` 结构 + `askAccess`（三态 → 文案的唯一映射）+ `opName` |
| `read_file.go` | 245 | `ReadFileTool`、`readFileRange`、`isBinary`、`splitLines`/`numberedLines`、`humanBytes` |
| `pattern.go` | 122 | `compileGlob`（模式 → 正则）、`translateClass`、`matchPattern` |
| `glob.go` | 211 | `GlobTool`、`walk` |
| `grep.go` | 338 | `GrepTool`、`search`、`searchFile`、`readLimitedLine`、`compileOptionalGlob` |
| `*_test.go` | 656（read_file / search 两个测试文件） | 用例见 §3 |

## 2. 设计决定

| 决定 | 理由 |
|---|---|
| **给模型的内容不带行号**，行号只进 `Display` | 模型要拿原文去 `edit_file` 精确替换；带 `  12│ ` 会诱发它把行号也写进 `old_string` |
| 读之前 `guard.Resolve(path)` 成绝对路径 | 相对路径是相对**工作目录**的，而进程 cwd 未必是工作目录。第一版直接 `os.ReadFile("main.go")` 就报了"文件不存在"——测试里的 cwd 是包目录 |
| 16MiB 以上拒绝并建议 grep | 把 100MB 日志读进内存，失败现场比一条明话难查得多 |
| **glob 模式转正则**，不手写回溯 | `**` 跨层 + `*` 不跨层 + 字符集 + 转义，回溯实现的边界记不住；翻译是单向的，正确性用表驱动测试压 |
| `**/` 与尾部 `/**` 都要能匹配零层 | 否则 `**/*.go` 漏掉根目录的 `main.go`、`a/**` 匹配不到 `a`（第一版就是这里失败，测试抓到） |
| Windows 上 `(?i)` | 文件系统不区分大小写，模式跟着不区分，否则 `Main.GO` 会漏 |
| 起点**只问一次**权限 | 用户已为"在这个目录里找"点过头；为每个文件弹窗会把人逼疯 |
| `grep` 单文件起点把基准挪到父目录 | 否则结果里显示 `.` 而不是文件名 |
| 超长行（>256KiB）丢弃剩余部分再继续 | 否则下一次读会接着读到同一行的尾巴，**后面所有行号都错位** |
| 二进制/过大/读不了的文件静默跳过 | 它们是噪声不是错误；让一次 grep 因为一个权限不足的产物文件失败没有意义 |
| `ctx` 取消 → `超时：搜索被取消` | 不让界面停在一个永远转圈的搜索上 |

## 3. 验证

```
go test ./internal/tools/ -v → 48 个顶层测试全 PASS
bash scripts/check.sh        → 全部通过
```

| 组 | 覆盖 |
|---|---|
| `read_file` 17 组 | 整读/分段/末段无提示/offset 越界/空文件/缺失/目录/二进制/超大/截断/权限拒绝/询问允许/询问拒绝/无通道/询问出错/校验表/取消 |
| `pattern` 2 组 | 17 条模式表（零层 `**`、`?`、字符集、取反、Windows 分隔符、`{a,b}` 不支持）+ 3 条非法模式 |
| `glob` 7 组 | 字典序稳定、limit 截断提示、跳过忽略与 `.git`、子目录起点、无匹配建议、文件当起点、起点权限只问一次 |
| `grep` 8 组 | include 过滤、无 include 全搜、跳过二进制与忽略、单文件、limit 尾注、无命中建议、非法正则、校验表、取消 |

## 4. 遗留

- `write_file` / `edit_file`（3.4）还没做：`Display` 的 diff 形态届时才真正用上。
- glob 不支持 `{a,b}`；`[a-z]` 区间按字节交给正则，多字节字符集不要用（已写进 CODE-DESIGN"已知限制"）。
- 三类工具都还没接进 `appstate` 的工具循环（3.6），所以现在只能单测，没有端到端。
- `grep` 是逐行正则，不是全文多行匹配；`(?s)` 之类的跨行模式在逐行读的模型下不会生效——真需要时再改成整块读。
