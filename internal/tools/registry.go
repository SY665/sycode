package tools

import (
	"errors"
	"fmt"
	"strings"
)

// Registry 持有本次运行可用的工具。
//
// 顺序敏感：模型看到工具 schema 的顺序会影响它的选择倾向（排在前面的更容易被用），
// 因此 All/Schemas 一律按注册顺序返回，不做 map 遍历那种随机序。
type Registry struct {
	byName map[string]Tool
	order  []Tool
}

// NewRegistry 造一个空表。
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]Tool)}
}

// Register 加入一个工具。
//
// 重名、空名、nil 一律报错而不是覆盖/忽略：工具表是模型能做什么的全部清单，
// 悄悄少一个或换掉一个都会让行为变得不可解释。
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return errors.New("注册工具失败：工具为 nil")
	}
	name := strings.TrimSpace(t.Name())
	if name == "" {
		return errors.New("注册工具失败：工具名不能为空")
	}
	if _, exists := r.byName[name]; exists {
		return fmt.Errorf("注册工具失败：工具名 %q 重复", name)
	}
	r.byName[name] = t
	r.order = append(r.order, t)
	return nil
}

// Get 按名查找。
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// All 按注册顺序返回全部工具（副本）。
func (r *Registry) All() []Tool {
	if len(r.order) == 0 {
		return nil
	}
	out := make([]Tool, len(r.order))
	copy(out, r.order)
	return out
}

// Len 返回工具数量。
func (r *Registry) Len() int { return len(r.order) }

// Schemas 按注册顺序导出 schema，供 appstate 转成 llm.ToolSchema。
//
// 每次都新建 map 副本：工具实现里的 schema 可能是共享变量，
// 直接传出去被下游改一行就会污染下一次请求。
func (r *Registry) Schemas() []Schema {
	if len(r.order) == 0 {
		return nil
	}
	out := make([]Schema, 0, len(r.order))
	for _, t := range r.order {
		out = append(out, Schema{
			Name:        t.Name(),
			Description: t.Description(),
			Input:       cloneSchema(t.InputSchema()),
		})
	}
	return out
}

// cloneSchema 浅拷贝一层 properties。
//
// 不做深拷贝：嵌套结构由工具作者自己保证不被改写——真要防到底得 JSON 往返一次，
// 每个回合都要跑，代价比收益大。
func cloneSchema(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
