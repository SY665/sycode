package session

import (
	"crypto/sha1"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
)

// maxNamespaceName 是目录名前缀的长度上限。
//
// 会话文件的完整路径还要再嵌两层（`<命名空间>/<会话>/transcript_events.jsonl`），
// 而 Windows 默认有 260 字符的路径上限：前缀留给 32 个字符，剩下的留给哈希与会话 ID。
const maxNamespaceName = 32

// namespaceDirName 把工作目录变成会话目录名：可读的目录名 + 路径哈希。
//
// 两个理由缺一不可：只用目录名会撞车（两个 `~/work/app` 无法区分），
// 只用哈希则没法用眼睛认出这是哪个项目。
func namespaceDirName(workingDir string) string {
	abs := normalizeWorkDir(workingDir)

	sum := sha1.Sum([]byte(abs))
	hash := hex.EncodeToString(sum[:])[:10]

	name := sanitizeName(filepath.Base(abs))
	if name == "" {
		// 根目录（`/`、`C:\`）没有可用的名字，但它的哈希是唯一的，给个固定前缀即可。
		name = "root"
	}
	return name + "-" + hash
}

// normalizeWorkDir 返回判定用的规范路径。
//
// 必须先绝对化再比较：`sycode` 从 `~/work/app` 与 `./app` 启动是同一个项目。
// Windows 的文件系统不区分大小写，同一个目录写成两种大小写必须落进同一个命名空间，
// 否则"同一项目的历史"会分裂成两份——Guard 判目录归属时也是这么折的。
func normalizeWorkDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		// 取不到绝对路径时退回清理后的原值：宁可哈希一个相对路径，也不要让会话存不下来。
		abs = filepath.Clean(dir)
	}
	abs = filepath.Clean(abs)
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs
}

// sanitizeName 把目录名里不适合做文件名的字符换成 `-`。
//
// 只保留字母数字与 `.` `_` `-`：中文目录名会被整体替换成 `-`（长度与可移植性都不可控），
// 但哈希仍然唯一，用户靠 `sycode sessions` 列表里的完整路径认项目。
func sanitizeName(name string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			lastDash = false
		case r == '-':
			if lastDash {
				continue
			}
			b.WriteRune('-')
			lastDash = true
		default:
			// 连续的特殊字符只留一个 `-`，否则名字会变成一串横线。
			if lastDash {
				continue
			}
			b.WriteRune('-')
			lastDash = true
		}
		if b.Len() >= maxNamespaceName {
			break
		}
	}

	return strings.Trim(b.String(), "-_.")
}
