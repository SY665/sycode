package tools

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

// stubTool 是一个最小工具实现，只为测注册表。
type stubTool struct {
	name  string
	input map[string]any
	// err 非 nil 时 Execute 返回它，用于测错误透传。
	err error
}

func (s *stubTool) Name() string                { return s.name }
func (s *stubTool) Description() string         { return "测试工具 " + s.name }
func (s *stubTool) InputSchema() map[string]any { return s.input }
func (s *stubTool) Execute(context.Context, Input) (Result, error) {
	if s.err != nil {
		return Result{}, s.err
	}
	return Result{Content: s.name + " ok"}, nil
}

func TestRegisterRejectsBadTools(t *testing.T) {
	cases := []struct {
		name string
		tool Tool
		want string
	}{
		{"nil 工具", nil, "为 nil"},
		{"空名", &stubTool{name: "  "}, "工具名不能为空"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := NewRegistry().Register(c.tool)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("期望包含 %q 的错误，实际 %v", c.want, err)
			}
		})
	}
}

func TestRegisterRejectsDuplicate(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(&stubTool{name: "read_file"}); err != nil {
		t.Fatalf("首次注册失败：%v", err)
	}

	err := registry.Register(&stubTool{name: "read_file"})
	if err == nil {
		t.Fatal("重名应报错")
	}
	if !strings.Contains(err.Error(), "重复") {
		t.Errorf("错误应说明重名：%v", err)
	}
	if registry.Len() != 1 {
		t.Errorf("重名不该改变表内容，实际 %d 个", registry.Len())
	}
}

func TestRegistryKeepsRegistrationOrder(t *testing.T) {
	registry := NewRegistry()
	names := []string{"read_file", "glob", "grep", "edit_file"}
	for _, name := range names {
		if err := registry.Register(&stubTool{name: name}); err != nil {
			t.Fatalf("注册 %s 失败：%v", name, err)
		}
	}

	all := registry.All()
	if len(all) != len(names) {
		t.Fatalf("数量不符：期望 %d，实际 %d", len(names), len(all))
	}
	for i, want := range names {
		if all[i].Name() != want {
			t.Errorf("第 %d 个应为 %q，实际 %q（顺序会影响模型的选择倾向）", i, want, all[i].Name())
		}
	}

	schemas := registry.Schemas()
	for i, want := range names {
		if schemas[i].Name != want {
			t.Errorf("schema 顺序不一致：第 %d 个应为 %q，实际 %q", i, want, schemas[i].Name)
		}
	}
}

func TestAllReturnsCopy(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&stubTool{name: "read_file"})

	got := registry.All()
	got[0] = &stubTool{name: "被外部替换"}

	if registry.All()[0].Name() != "read_file" {
		t.Error("All 应返回副本，外部改写不该影响注册表")
	}
}

func TestGet(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&stubTool{name: "glob"})

	tool, ok := registry.Get("glob")
	if !ok || tool.Name() != "glob" {
		t.Fatalf("应能取到 glob，实际 %v %v", tool, ok)
	}
	if _, ok := registry.Get("nope"); ok {
		t.Error("不存在的名字应返回 false")
	}
}

func TestSchemasCarriesNameDescriptionAndInput(t *testing.T) {
	registry := NewRegistry()
	_ = registry.Register(&stubTool{
		name:  "glob",
		input: map[string]any{"pattern": map[string]any{"type": "string"}},
	})

	schemas := registry.Schemas()
	if len(schemas) != 1 {
		t.Fatalf("应导出 1 个 schema，实际 %d", len(schemas))
	}
	schema := schemas[0]
	if schema.Description == "" {
		t.Error("Description 是模型决定用不用的主要依据，不能为空")
	}
	if _, ok := schema.Input["pattern"]; !ok {
		t.Errorf("参数 schema 未透传：%+v", schema.Input)
	}
}

func TestSchemasDoNotAliasToolSchema(t *testing.T) {
	input := map[string]any{"pattern": "x"}
	registry := NewRegistry()
	_ = registry.Register(&stubTool{name: "glob", input: input})

	schemas := registry.Schemas()
	schemas[0].Input["pattern"] = "被改了"
	delete(schemas[0].Input, "pattern")

	// 工具自己的 schema 变量不该被下游改动污染。
	if input["pattern"] != "x" {
		t.Error("Schemas 应返回副本，外部改写影响了工具的 schema")
	}
}

func TestEmptyRegistry(t *testing.T) {
	registry := NewRegistry()
	if registry.Len() != 0 {
		t.Errorf("新表应为空，实际 %d", registry.Len())
	}
	if registry.All() != nil {
		t.Error("空表应返回 nil，便于上层用 len 判断")
	}
	if registry.Schemas() != nil {
		t.Error("空表应返回 nil")
	}
}

func TestExecuteErrorPassesThrough(t *testing.T) {
	sentinel := errors.New("执行失败：磁盘满了")
	registry := NewRegistry()
	_ = registry.Register(&stubTool{name: "write_file", err: sentinel})

	tool, _ := registry.Get("write_file")
	_, err := tool.Execute(context.Background(), Input{"path": "a.go"})
	if !errors.Is(err, sentinel) {
		t.Errorf("错误应原样透传：%v", err)
	}
}

func TestClipKeepsShortContent(t *testing.T) {
	got, truncated := clip("短内容")
	if truncated || got != "短内容" {
		t.Fatalf("短内容不该被动：%q %v", got, truncated)
	}
}

func TestClipTruncatesWithoutSplittingRunes(t *testing.T) {
	// 中文每字 3 字节，正好会跨过上限。
	content := strings.Repeat("一行中文内容\n", maxInlineBytes/7)
	got, truncated := clip(content)

	if !truncated {
		t.Fatal("超长内容应被截断")
	}
	if len(got) > maxInlineBytes {
		t.Errorf("截断后仍超过上限：%d", len(got))
	}
	if !utf8.ValidString(got) {
		t.Error("截断切在了多字节字符中间，会出现替换符")
	}
	// 尽量切在行边界：半行代码比整行缺失更难读。
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("应切在换行处，实际结尾 %q", got[len(got)-10:])
	}
}

func TestClipWithoutNewlines(t *testing.T) {
	// 单行超长（比如压缩过的 JSON）：没有换行可退，只能按字节切，但仍须是合法 UTF-8。
	got, truncated := clip(strings.Repeat("汉", maxInlineBytes))
	if !truncated {
		t.Fatal("应被截断")
	}
	if !utf8.ValidString(got) {
		t.Error("结果必须是合法 UTF-8")
	}
	if len(got) > maxInlineBytes {
		t.Errorf("超限：%d", len(got))
	}
}

func TestDecisionAllowed(t *testing.T) {
	cases := map[Decision]bool{
		DecisionDeny:         false,
		DecisionAllowOnce:    true,
		DecisionAllowSession: true,
	}
	for decision, want := range cases {
		if got := decision.Allowed(); got != want {
			t.Errorf("%d.Allowed() 期望 %v，实际 %v", int(decision), want, got)
		}
	}
	// 零值必须是拒绝：弹窗没给出回答时不能默认放行。
	if Decision(0).Allowed() {
		t.Error("零值（Deny）必须是不放行")
	}
}
