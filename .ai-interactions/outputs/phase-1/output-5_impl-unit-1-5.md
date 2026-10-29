# 实施记录：单元 1.5 依赖方向断言

- 日期：2026-10-29
- 结论：完成；已用"注入违规"反证测试有效性，不是空跑通过

## 1. 做法

`internal/cli/cmd/deps_test.go`（123 行）：

1. `go env GOMOD` 定位模块根 → `go list -json ./...` 取真实 import 图
2. 把包路径去掉模块前缀后与规则表比对，命中即 `t.Errorf`
3. 规则表就是 CODING-STANDARDS §2.2 的禁止边，每条带 `why` 字段，失败信息自带理由

## 2. 为什么不用正则扫源码

注释里的 `// import xxx`、字符串常量里的包路径都会被误报；`go list` 输出的是编译器真实解析过的 import，只认真实依赖。

## 3. 反证（关键）

只有"能通过"说明不了什么，必须证明"能被触发"。步骤：

```bash
# 注入一条违规：日志包（叶子）反向依赖 repl
cat > internal/logging/zz_violation_tmp.go <<'EOF'
package logging
import "github.com/sy665/sycode/internal/cli/repl"
var _ = repl.Deps{}
EOF

go test ./internal/cli/cmd/ -run TestDependencyDirection
# → FAIL: 依赖方向违规：internal/logging → internal/cli/repl（日志是叶子包，不依赖任何内部包）

rm internal/logging/zz_violation_tmp.go
go test ./internal/cli/cmd/ -run TestDependencyDirection
# → ok
```

## 4. 当前局限

规则表里多数条目（`llm`、`tools`、`session`、`appstate`、`filesystem`）指向的包还没创建，这些规则暂时不生效。
包一出现即自动开始拦截——这是有意的：规则先立，代码后到，避免"等包写完再补规则"时已经违规。

## 5. P1 收尾状态

| 项 | 结果 |
|---|---|
| 代码规模 | 1,214 行（含测试）；源码 531 行 / 测试 683 行 |
| 提交数 | 11 |
| 质量门 | `bash scripts/check.sh` 全绿（含 `-race`） |
| 验证盲区 | 真实终端下的 TUI 观感需人工跑一次（本环境无 PTY） |
