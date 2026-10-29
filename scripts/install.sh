#!/usr/bin/env bash
# 安装 sycode：下载对应平台的发布包，校验 sha256，装到 INSTALL_DIR。
#
#   curl -fsSL https://raw.githubusercontent.com/sy665/sycode/main/scripts/install.sh | bash
#
# 可用环境变量：
#   SYCODE_VERSION   指定版本（如 0.1.0），默认取 latest
#   INSTALL_DIR      安装目录，默认 $HOME/.local/bin
#   SYCODE_REPO      仓库（默认 sy665/sycode），只在使用 fork 时需要
#   SYCODE_BASE_URL  下载基址，覆盖默认的 release 地址（离线镜像/本地测试用）
#   SYCODE_OS / SYCODE_ARCH  强制平台，跳过自动探测（交叉安装与测试用）
#
# 原则：**校验不过就退出**。宁可这次没装上，也不能把一个下载到一半的二进制留在 PATH 里。
set -euo pipefail

REPO="${SYCODE_REPO:-sy665/sycode}"
BIN="sycode"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
DRY_RUN=0

for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -h|--help)
      sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) echo "错误：未知参数 $arg（--help 查看用法）" >&2; exit 2 ;;
  esac
done

die() { echo "错误：$*" >&2; exit 1; }
info() { echo "$*" >&2; }

detect_os() {
  if [ -n "${SYCODE_OS:-}" ]; then printf '%s' "$SYCODE_OS"; return; fi
  case "$(uname -s)" in
    Darwin) printf 'darwin' ;;
    Linux) printf 'linux' ;;
    # Windows 上通常用 Git Bash / MSYS 跑这个脚本
    MINGW*|MSYS*|CYGWIN*) printf 'windows' ;;
    *) die "不支持的操作系统：$(uname -s)" ;;
  esac
}

detect_arch() {
  if [ -n "${SYCODE_ARCH:-}" ]; then printf '%s' "$SYCODE_ARCH"; return; fi
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) die "不支持的架构：$(uname -m)" ;;
  esac
}

latest_version() {
  if [ -n "${SYCODE_VERSION:-}" ]; then printf '%s' "$SYCODE_VERSION"; return; fi
  # 跟着 releases/latest 的重定向走，比调 API 少一次解析，也不吃 API 限流。
  local url="https://github.com/$REPO/releases/latest" effective
  effective=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$url") ||
    die "无法访问 $url（检查网络，或用 SYCODE_VERSION 指定版本）"
  case "$effective" in
    */tag/v*) printf '%s' "${effective##*/tag/v}" ;;
    *) die "还没发布过版本，无法确定 latest（可用 SYCODE_VERSION 指定）" ;;
  esac
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    # Windows 上的 coreutils 会给二进制模式的摘要加 `\` 前缀，去掉它再比
    sha256sum "$1" | awk '{print $1}' | tr -d '\\'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "找不到 sha256 工具（需要 sha256sum 或 shasum）"
  fi
}

OS=$(detect_os)
ARCH=$(detect_arch)
VERSION=$(latest_version)
EXT="tar.gz"
[ "$OS" = "windows" ] && EXT="zip"

ARCHIVE="${BIN}_${VERSION}_${OS}_${ARCH}.${EXT}"
BASE="${SYCODE_BASE_URL:-https://github.com/$REPO/releases/download/v$VERSION}"

info "即将安装 sycode ${VERSION}（${OS}/${ARCH}）到 ${INSTALL_DIR}"

if [ "$DRY_RUN" -eq 1 ]; then
  info "  --dry-run：将下载 $BASE/$ARCHIVE"
  info "  --dry-run：将校验 checksums.txt 里的 sha256"
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp" 2>/dev/null || true' EXIT
mkdir -p "$tmp/pkg"

info "下载 $ARCHIVE"
curl -fsSL "$BASE/$ARCHIVE" -o "$tmp/$ARCHIVE" ||
  die "下载失败：$BASE/$ARCHIVE（版本或平台可能不存在）"

info "校验 sha256"
curl -fsSL "$BASE/checksums.txt" -o "$tmp/checksums.txt" ||
  die "下载校验文件失败：$BASE/checksums.txt"

expected=$(awk -v name="$ARCHIVE" '$2 == name || $2 == "*"name {print $1}' "$tmp/checksums.txt" | head -n1)
[ -n "$expected" ] || die "校验文件里没有 $ARCHIVE"
actual=$(sha256_of "$tmp/$ARCHIVE")
[ "$expected" = "$actual" ] ||
  die "校验失败：$ARCHIVE 的 sha256 不匹配
  期望 $expected
  实际 $actual
  下载可能被截断或被篡改，已中止安装"

info "解压"
# 一定要 cd 进去用相对文件名：Windows 上 GNU tar 会把 `C://...` 当远程主机（host:path 语法）。
case "$EXT" in
  zip)
    command -v unzip >/dev/null 2>&1 || die "解压 zip 需要 unzip（或用 SYCODE_OS 指定别的平台）"
    (cd "$tmp" && unzip -q -o "$ARCHIVE" -d pkg)
    ;;
  *)
    (cd "$tmp" && tar -xzf "$ARCHIVE" -C pkg)
    ;;
esac

[ -f "$tmp/pkg/$BIN" ] || [ -f "$tmp/pkg/${BIN}.exe" ] ||
  die "压缩包里没有 $BIN（产物结构可能变了）"

info "安装到 ${INSTALL_DIR}"
mkdir -p "$INSTALL_DIR"
if [ -f "$tmp/pkg/${BIN}.exe" ]; then
  cp -f "$tmp/pkg/${BIN}.exe" "$INSTALL_DIR/${BIN}.exe"
  TARGET="$INSTALL_DIR/${BIN}.exe"
else
  cp -f "$tmp/pkg/$BIN" "$INSTALL_DIR/$BIN"
  TARGET="$INSTALL_DIR/$BIN"
fi
chmod 0755 "$TARGET"

info "已安装：$TARGET"
"$TARGET" --version >&2 || die "装上的二进制跑不起来"

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) info "提示：$INSTALL_DIR 不在 PATH 里，把它加进去才能直接敲 sycode" ;;
esac
