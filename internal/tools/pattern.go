package tools

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
)

// 本文件是 glob 模式的匹配器：把模式编译成正则再匹配。
//
// 为什么不上手写回溯匹配：段级回溯要处理 `**` 跨层、`*` 不跨层、字符集与转义，
// 边界多得记不住；而"模式 → 正则"是单向翻译，正则引擎负责匹配，正确性靠表驱动测试压住。
//
// 支持：`*`（段内任意个字符，不跨 /）、`?`（段内单个字符）、`**`（任意层目录）、
// `[abc]` / `[a-z]` / `[!abc]`（字符集）。**不支持** `{a,b}` 花括号展开——记在"已知限制"里。

// compileGlob 把 glob 模式编译成锚定正则。
func compileGlob(pattern string) (*regexp.Regexp, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, fmt.Errorf("参数错误：模式为空。例如 {\"pattern\":\"**/*.go\"}")
	}
	// 顺手把 Windows 风格的分隔符规整掉：模型偶尔会写 `src\*.go`。
	pattern = strings.ReplaceAll(pattern, `\`, "/")

	// 用字节切片而不是 strings.Builder：处理尾部 `/**` 时要退回一个已写入的 `/`，
	// Builder 不支持回退，切片只要重切一下。
	out := make([]byte, 0, len(pattern)+16)
	// Windows 文件系统不区分大小写；模式跟着不区分，否则 `**/*.go` 会漏掉 `Main.GO`。
	if runtime.GOOS == "windows" {
		out = append(out, "(?i)"...)
	}
	out = append(out, '^')

	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++ // 吃掉第二个 *
				// `**/` 可以匹配零层目录，所以 `**/*.go` 也要能命中根目录下的 main.go。
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					out = append(out, "(?:.*/)?"...)
					continue
				}
				// 尾部 `/**`：同样允许零层，即 `a/**` 也要匹配 `a` 本身。
				if i+1 == len(pattern) && len(out) > 0 && out[len(out)-1] == '/' {
					out = out[:len(out)-1]
					out = append(out, "(?:/.*)?"...)
					continue
				}
				out = append(out, ".*"...)
				continue
			}
			out = append(out, "[^/]*"...)

		case '?':
			out = append(out, "[^/]"...)

		case '[':
			closing := strings.IndexByte(pattern[i:], ']')
			if closing < 0 {
				return nil, fmt.Errorf("参数错误：模式 %q 里的字符集没有闭合（缺 ]）", pattern)
			}
			class, err := translateClass(pattern[i : i+closing+1])
			if err != nil {
				return nil, err
			}
			out = append(out, class...)
			i += closing

		default:
			out = append(out, regexp.QuoteMeta(string(c))...)
		}
	}
	out = append(out, '$')

	compiled, err := regexp.Compile(string(out))
	if err != nil {
		return nil, fmt.Errorf("参数错误：模式 %q 无法解析：%w", pattern, err)
	}
	return compiled, nil
}

// translateClass 把 glob 字符集翻成正则字符集。
//
// glob 用 `[!abc]` 表示取反，正则用 `[^abc]`；其余字符原样，但 `\` 与 `]` 要转义，
// 否则一个 `[a\]b]` 就能把正则搞成另一门语言。
func translateClass(class string) (string, error) {
	body := class[1 : len(class)-1]
	if body == "" {
		return "", fmt.Errorf("参数错误：字符集 %q 是空的", class)
	}

	var b strings.Builder
	b.WriteString("[")
	if body[0] == '!' {
		b.WriteString("^")
		body = body[1:]
	}
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '\\', ']', '^':
			b.WriteByte('\\')
			b.WriteByte(body[i])
		default:
			b.WriteByte(body[i])
		}
	}
	b.WriteString("]")
	return b.String(), nil
}

// matchPattern 判断（已用 / 分隔的）相对路径是否匹配模式。
func matchPattern(pattern, path string) bool {
	re, err := compileGlob(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(strings.ReplaceAll(path, `\`, "/"))
}
