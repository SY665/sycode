package llm

import "encoding/json"

// 估算只用于"给人看的占用提示"，不参与请求构造——真正的截断由 provider 决定。
//
// 算法刻意选最简单的那个：字节数 / 3。理由是它能在文档里一行写清、谁都能复现，
// 而任何更细的分词器都要跟供应商版本走，估错了反而更难解释。
//
// 为什么按字节而不是字符：中文一个字 3 字节（UTF-8），除以 3 就是"一个字约一个 token"，
// 接近主流分词器对中文的表现；英文 4 字节 1 token，除 3 会高估约四分之一。
// 高估比低估安全——它只会让"上下文快满了"的提醒来得早一点。

// EstimateTokens 估算一段文本的 token 数。空串记 0，非空至少 1。
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	return max(1, (len(text)+2)/3)
}

// EstimateJSON 估算任意值序列化成 JSON 后的 token 数。
//
// 工具 schema 的体积几乎全在这里：描述文本之外，还有一整棵 JSON Schema。
func EstimateJSON(v any) int {
	if v == nil {
		return 0
	}
	raw, err := json.Marshal(v)
	if err != nil {
		// 估不出来就记 0：这是展示用的数字，不该因为它让 /context 报错。
		return 0
	}
	return EstimateTokens(string(raw))
}
