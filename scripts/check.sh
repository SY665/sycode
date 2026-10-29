#!/usr/bin/env bash
# 统一质量门：本地与 CI 跑同一份脚本，避免"本地过、CI 不过"。
#
# 用法：bash scripts/check.sh
# 前置：go 在 PATH 中（精简 PATH 的终端先 source scripts/env.sh）
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

fail=0

step() { printf '\n==> %s\n' "$1"; }

step "gofmt（未格式化文件必须为 0）"
unformatted="$(gofmt -l . 2>/dev/null | grep -v '^$' || true)"
if [ -n "$unformatted" ]; then
  echo "以下文件未格式化，请执行 gofmt -w："
  echo "$unformatted"
  fail=1
else
  echo "通过"
fi

step "go vet"
go vet ./... || fail=1

step "go build"
# 注意：直接 `go build ./...` 会把 main 包的二进制写到当前目录（仓库根），
# 造成二进制被误提交。用 -o <临时目录>/ 把它导走。
build_out="$(mktemp -d)"
go build -o "${build_out}/" ./... || fail=1
rm -rf "${build_out}" 2>/dev/null || true

step "go test"
go test ./... || fail=1

step "go test -race"
# -race 需要 cgo 与 gcc；Windows 上默认可能没有，缺失时跳过而非失败（CI 另有覆盖）
if [ "$(go env CGO_ENABLED)" = "1" ] && command -v gcc >/dev/null 2>&1; then
  go test -race ./... || fail=1
else
  echo "跳过：需要 CGO_ENABLED=1 且 gcc 可用（当前 CGO_ENABLED=$(go env CGO_ENABLED)）"
fi

if [ "$fail" -ne 0 ]; then
  printf '\n检查未通过\n'
  exit 1
fi
printf '\n全部通过\n'
