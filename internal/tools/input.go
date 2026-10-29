package tools

import (
	"fmt"
	"math"
	"strconv"
)

// 本文件集中"从 Input 里安全取值"的辅助。
//
// 为什么不直接 `in["path"].(string)`：模型给的类型经常不对（把数字写成字符串、
// 把 offset 写成 "10"），裸类型断言会 panic；而 panic 在 Execute 里是红线。
// 报错文案按 CODING-STANDARDS §13：中文 + 英文字段名 + 示例 JSON，模型才能自我修正。

// requireString 取必填字符串参数。
func requireString(in Input, field string) (string, error) {
	raw, ok := in[field]
	if !ok || raw == nil {
		return "", fmt.Errorf("参数错误：缺少必填字段 %q。请带上 {%s} 重试", field, exampleFor(field))
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("参数错误：字段 %q 必须是字符串，实际是 %s。请带上 {%s} 重试",
			field, typeName(raw), exampleFor(field))
	}
	if value == "" {
		return "", fmt.Errorf("参数错误：字段 %q 不能为空串。请带上 {%s} 重试", field, exampleFor(field))
	}
	return value, nil
}

// optionalInt 取可选整数参数；缺省时返回 def。
//
// JSON 解出来的数字一律是 float64，所以三种数值类型都要认；非整数（10.5）拒绝，
// 而不是悄悄截断——用它算 offset 时差一位就取错行。
func optionalInt(in Input, field string, def int) (int, error) {
	raw, ok := in[field]
	if !ok || raw == nil {
		return def, nil
	}

	var value float64
	switch typed := raw.(type) {
	case float64:
		value = typed
	case int:
		value = float64(typed)
	case int64:
		value = float64(typed)
	default:
		return 0, fmt.Errorf("参数错误：字段 %q 必须是整数，实际是 %s。请带上 {\"%s\":1} 重试",
			field, typeName(raw), field)
	}

	if math.Trunc(value) != value || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("参数错误：字段 %q 必须是整数，实际是 %v", field, value)
	}
	if value > math.MaxInt32 || value < math.MinInt32 {
		return 0, fmt.Errorf("参数错误：字段 %q 超出合理范围（%v）", field, value)
	}
	return int(value), nil
}

// requireText 取必填字符串参数，但允许空串。
//
// 用于 write_file 的 content：清空一个文件是合法操作，而"没给这个字段"才是错误。
func requireText(in Input, field string) (string, error) {
	raw, ok := in[field]
	if !ok || raw == nil {
		return "", fmt.Errorf("参数错误：缺少必填字段 %q。请带上 {%s} 重试", field, exampleFor(field))
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("参数错误：字段 %q 必须是字符串，实际是 %s。请带上 {%s} 重试",
			field, typeName(raw), exampleFor(field))
	}
	return value, nil
}

// optionalString 取可选字符串参数；缺省返回空串。
func optionalString(in Input, field string) (string, error) {
	raw, ok := in[field]
	if !ok || raw == nil {
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("参数错误：字段 %q 必须是字符串，实际是 %s", field, typeName(raw))
	}
	return value, nil
}

// itoa 是 strconv.Itoa 的短别名，用来在提示语里拼数字。
func itoa(v int) string { return strconv.Itoa(v) }

// typeName 把 Go 类型名换成模型看得懂的英文说法。
func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case float64, int, int64:
		return "number"
	case bool:
		return "boolean"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

// exampleFor 给常见字段一个示例 JSON 片段，让报错本身就能当提示用。
func exampleFor(field string) string {
	switch field {
	case "path":
		return `"path":"main.go"`
	case "pattern":
		return `"pattern":"**/*.go"`
	case "content":
		return `"content":"…"`
	default:
		return `"` + field + `":"…"`
	}
}
