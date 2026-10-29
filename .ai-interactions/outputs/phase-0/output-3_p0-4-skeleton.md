# 产出：P0.4/P0.5 工程骨架

- 对应：`prompt-3_p0-4-skeleton.md`
- 日期：2026-10-29
- 提交：`f1011ea`（骨架）、`110b226`（修 check.sh）

## 1. 交付物

| 文件 | 说明 |
|---|---|
| `go.mod` | `module github.com/sy665/sycode`，`go 1.24`，零直接依赖 |
| `cmd/main.go` | `main()` 只 `os.Exit(run())`；`run()` 目前打印版本并返回 0，P1 换成 logging + cobra |
| `scripts/check.sh` | 唯一质量门：gofmt → vet → build → test → test -race；本地与 CI 共用 |
| `Makefile` | 薄封装（`make check` = `bash scripts/check.sh`）；本机无 make，仅作便利 |
| `.github/workflows/ci.yml` | ubuntu/macos/windows 三平台，`CGO_ENABLED=1` 跑同一脚本 |
| `LICENSE` | MIT |
| `.gitattributes` | `* text=auto eol=lf`，避免 shell 脚本在 Windows 变成 CRLF 后报 `$'\r': command not found` |

## 2. 过程中发现的两个真实缺陷

### ① `-X` 对 `const` 静默失效

实测：`const version` + `-ldflags "-X main.version=0.1.0"` → 程序仍输出 `dev`；换成 `var version` 才输出 `0.1.0`。
常见实现 `cmd/main.go` 用 `const version = "0.65.0"` 却配 `-X main.version={{.Version}}`，等于版本号只能靠手改常量。
我们改成 `var` 并在注释里写明原因（符合 §12「魔法值/坑必须注释」）。

### ② `go build ./...` 会把二进制写到仓库根目录

`check.sh` 第一次跑就生成了 `cmd.exe`（main 包名取自目录名），并被 `git add -A` 误提交。
修法：`go build -o "<临时目录>/" ./...`，并 `git rm --cached cmd.exe` 后另起一笔提交修脚本——不 amend，保留"发现问题"的痕迹。

## 3. 质量门实测

```
gofmt    通过
go vet   通过
go build 通过
go test  ?  github.com/sy665/sycode/cmd  [no test files]
-race    ?  github.com/sy665/sycode/cmd  [no test files]
全部通过
```

版本注入验证：`go build -ldflags "-X main.version=0.1.0" -o /tmp/sycode.exe ./cmd` → 输出 `sycode 0.1.0：骨架已就绪，命令树见 P1`，退出码 0。

## 4. 未做（有意）

- 未 `git push`：远端仓库需你确认后再推。
- 未建 `internal/` 下任何包：属 P1 单元，按「一个单元一个提交」推进。
- 未加 `deps_test.go`：P1 单元 1.4，届时才有可断言的依赖边。
