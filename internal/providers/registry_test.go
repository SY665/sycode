package providers

import (
	"strings"
	"testing"
)

func TestLoadEmbeddedRegistry(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatalf("解析内嵌注册表失败：%v", err)
	}
	if len(reg.Providers) == 0 {
		t.Fatal("内嵌注册表不应为空")
	}

	providerID, modelID := reg.Default()
	p, m, ok := reg.Find(providerID, modelID)
	if !ok {
		t.Fatalf("默认模型不存在：%s/%s", providerID, modelID)
	}
	if p.BaseURL == "" {
		t.Errorf("默认供应商 %q 缺少 base_url", p.ID)
	}
	if m.ContextWindow <= 0 {
		t.Errorf("默认模型 %q 的 context_window 非正：%d", m.ID, m.ContextWindow)
	}
}

func TestFindUnknown(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatalf("解析内嵌注册表失败：%v", err)
	}

	cases := []struct {
		name       string
		providerID string
		modelID    string
		wantProOK  bool // 供应商是否应被找到
	}{
		{"供应商不存在", "nope", "gpt-4o-mini", false},
		{"供应商存在但模型不存在", "openai", "nope", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, _, ok := reg.Find(c.providerID, c.modelID)
			if ok {
				t.Fatal("不应找到该模型")
			}
			// 供应商自身命中时仍要返回，调用方靠它区分"供应商错"还是"模型错"。
			if gotProvider := p.ID != ""; gotProvider != c.wantProOK {
				t.Errorf("供应商命中情况不符：期望 %v，实际 %v", c.wantProOK, gotProvider)
			}
		})
	}
}

func TestFirstModel(t *testing.T) {
	reg, err := Load()
	if err != nil {
		t.Fatalf("解析内嵌注册表失败：%v", err)
	}

	// 内嵌表里 deepseek 只有一个模型，用它断言"取第一个"这个契约。
	if got := reg.FirstModel("deepseek"); got != "deepseek-chat" {
		t.Errorf("deepseek 的第一个模型应为 deepseek-chat，实际 %q", got)
	}
	if got := reg.FirstModel("nope"); got != "" {
		t.Errorf("未知供应商应返回空串，实际 %q", got)
	}
}

func TestLoadFile_Valid(t *testing.T) {
	const doc = `
defaults:
  provider: p1
  model: m1
providers:
  - id: p1
    name: 供应商一
    base_url: https://example.com/v1
    api_key_env: P1_KEY
    models:
      - id: m1
        name: 模型一
        context_window: 1000
        thinking_efforts: ["low", "high"]
`
	reg, err := LoadFile(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("合法注册表不应报错：%v", err)
	}
	if got := reg.Providers[0].Models[0].ThinkingEfforts; len(got) != 2 {
		t.Errorf("thinking_efforts 解析不符：期望 2 项，实际 %v", got)
	}
}

func TestLoadFile_Invalid(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		want string // 期望错误信息包含的片段
	}{
		{
			name: "缺少 defaults",
			doc:  "providers:\n  - id: p1\n    models:\n      - {id: m1, context_window: 10}\n",
			want: "defaults",
		},
		{
			name: "供应商 id 重复",
			doc: `
defaults: {provider: p1, model: m1}
providers:
  - {id: p1, models: [{id: m1, context_window: 10}]}
  - {id: p1, models: [{id: m2, context_window: 10}]}
`,
			want: "供应商 id 重复",
		},
		{
			name: "模型 id 重复",
			doc: `
defaults: {provider: p1, model: m1}
providers:
  - {id: p1, models: [{id: m1, context_window: 10}, {id: m1, context_window: 10}]}
`,
			want: "模型 id 重复",
		},
		{
			name: "上下文窗口非正",
			doc: `
defaults: {provider: p1, model: m1}
providers:
  - {id: p1, models: [{id: m1, context_window: 0}]}
`,
			want: "context_window",
		},
		{
			name: "默认模型不存在",
			doc: `
defaults: {provider: p1, model: missing}
providers:
  - {id: p1, models: [{id: m1, context_window: 10}]}
`,
			want: "默认模型不存在",
		},
		{
			name: "供应商没有模型",
			doc: `
defaults: {provider: p1, model: m1}
providers:
  - {id: p1, models: []}
`,
			want: "没有配置任何模型",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadFile(strings.NewReader(c.doc))
			if err == nil {
				t.Fatal("非法注册表应返回错误")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("错误信息应包含 %q，实际：%v", c.want, err)
			}
		})
	}
}

func TestLoadFile_YAMLSyntaxError(t *testing.T) {
	_, err := LoadFile(strings.NewReader("providers: ["))
	if err == nil {
		t.Fatal("YAML 语法错误应返回错误")
	}
	if !strings.Contains(err.Error(), "解析模型注册表失败") {
		t.Errorf("错误信息应说明是解析失败：%v", err)
	}
}

func TestGetModelContextWindow(t *testing.T) {
	registry, err := Load()
	if err != nil {
		t.Fatalf("加载内置注册表失败：%v", err)
	}

	provider, model, ok := registry.Find("openai", "gpt-4o-mini")
	if !ok {
		t.Fatal("内置注册表里应有 openai/gpt-4o-mini")
	}
	window, known := registry.GetModelContextWindow(provider.ID, model.ID)
	if !known {
		t.Fatal("内置模型应填了上下文窗口")
	}
	if window <= 0 {
		t.Errorf("窗口应为正数，实际 %d", window)
	}

	// 未知模型不能编一个数出来：界面上把未知当 0% 比不显示更误导。
	if _, known := registry.GetModelContextWindow("openai", "不存在的模型"); known {
		t.Error("未知模型应报未知")
	}
}

func TestGetModelContextWindowWithoutValue(t *testing.T) {
	registry := &Registry{Providers: []Provider{{
		ID:     "test",
		Models: []Model{{ID: "no-window"}},
	}}}

	if _, known := registry.GetModelContextWindow("test", "no-window"); known {
		t.Error("没填窗口的模型应报未知，而不是当成 0 已知")
	}
}
