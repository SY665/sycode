#!/usr/bin/env bash
# 为 Git Bash / 精简 PATH 的会话补齐 Go 工具链环境。
# 用法：source scripts/env.sh   （或在 Makefile / CI 里 export 同等变量）
#
# 背景（2026-10-29 实测定稿）：
#   - Go 1.26.3 安装在 D:\Apps\Go，已写入系统与用户 PATH，但部分终端（含 IDE 内置 shell）
#     继承的是裁剪过的 PATH，导致 `go` 不可见。本脚本主要解决这一项。
#   - GOCACHE / GOPATH / GOPROXY 已通过 `go env -w` 持久化到
#     %APPDATA%\go\env，正常 shell 无需再设置；此处仅作兜底（CI 或极端环境）。
#     已持久化的值见文件末尾注释。

GO_WIN_DIR="${GO_WIN_DIR:-D:/Apps/Go}"

# MSYS/Git Bash 的 PATH 需要 POSIX 路径；优先用 cygpath 转换
if command -v cygpath >/dev/null 2>&1; then
  GO_BIN_DIR="$(cygpath -u "$GO_WIN_DIR")/bin"
else
  # 退化：把 D:/x 转成 /d/x
  GO_BIN_DIR="/$(printf '%s' "$GO_WIN_DIR" | sed -E 's#^([A-Za-z]):/#\L\1/#')/bin"
fi

case ":$PATH:" in
  *":$GO_BIN_DIR:"*) ;;
  *) export PATH="$GO_BIN_DIR:$PATH" ;;
esac

export GOCACHE="${GOCACHE:-$HOME/AppData/Local/go-build}"
# 仅在用户未显式设置时兜底，避免覆盖 CI 或个人的代理选择
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

mkdir -p "$GOCACHE"

echo "go:      $(go version 2>/dev/null || echo "未找到（查找路径：$GO_BIN_DIR）")"
echo "GOCACHE: $GOCACHE"
echo "GOPROXY: $GOPROXY"

# 已写入 %APPDATA%\go\env 的持久配置（2026-10-29）：
#   GOCACHE=C:/Users/SY/AppData/Local/go-build
#   GOPATH=D:/Golang/code
#   GOPROXY=https://goproxy.cn,direct
# 回滚单条：go env -u <NAME>；查看：go env <NAME>；清空文件：删除 %APPDATA%\go\env
