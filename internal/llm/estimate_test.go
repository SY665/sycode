package llm

import "testing"

func TestEstimateTokens(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"空串记 0", "", 0},
		{"非空至少 1", "a", 1},
		{"两个字节也至少 1", "ab", 1},
		// 中文一个字 3 字节：除以 3 得到"一个字约一个 token"，这是这个算法的立足点。
		{"中文一字约一 token", "中", 1},
		{"中文三字约三 token", "上下文", 3},
		{"英文按字节算会高估", "abcd", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EstimateTokens(tc.text); got != tc.want {
				t.Errorf("EstimateTokens(%q) = %d，期望 %d", tc.text, got, tc.want)
			}
		})
	}

	// 长文本只验"随长度单调增长"，具体系数由上面几条钉住。
	short := EstimateTokens("hello world")
	long := EstimateTokens("hello world, this is a much longer sentence for the estimate")
	if long <= short {
		t.Errorf("更长的文本应估出更多 token：%d vs %d", long, short)
	}
}

func TestEstimateJSON(t *testing.T) {
	if got := EstimateJSON(nil); got != 0 {
		t.Errorf("nil 应记 0，实际 %d", got)
	}

	schema := map[string]any{
		"type":        "object",
		"description": "读取文件内容，可按行分段",
	}
	got := EstimateJSON(schema)
	if got < 5 {
		t.Errorf("工具 schema 的估算明显偏小：%d", got)
	}
	// 编排错误的值不该让展示层炸掉：估不出来就记 0。
	if got := EstimateJSON(func() {}); got != 0 {
		t.Errorf("无法序列化的值应记 0，实际 %d", got)
	}
}
