// Package providers 提供内置的模型注册表：供应商、端点、模型元数据。
//
// 本包只做"查表"：解析内嵌的 registry.yaml、按 id 查找、给出默认值。
// 不发请求、不读用户配置、不解析密钥——那些属于 config 与 llm。
package providers

import (
	_ "embed"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// registryYAML 随二进制发布：用户不需要额外文件就能开箱使用。
//
//go:embed registry.yaml
var registryYAML []byte

// Model 是一个可被选择的模型。
type Model struct {
	ID              string   `yaml:"id"`
	Name            string   `yaml:"name"`
	ContextWindow   int      `yaml:"context_window"`
	ThinkingEfforts []string `yaml:"thinking_efforts"`
}

// Provider 是一个可接入的供应商端点。
//
// BaseURL 与 APIKeyEnv 放在注册表而不是用户配置里：它们是供应商的固有属性，
// 让每个用户都重复配置一遍没有意义；用户配置只负责覆盖与选择。
type Provider struct {
	ID        string  `yaml:"id"`
	Name      string  `yaml:"name"`
	BaseURL   string  `yaml:"base_url"`
	APIKeyEnv string  `yaml:"api_key_env"`
	Models    []Model `yaml:"models"`
}

// Registry 是解析后的完整注册表。
type Registry struct {
	DefaultProvider string     `yaml:"default_provider"`
	DefaultModel    string     `yaml:"default_model"`
	Providers       []Provider `yaml:"providers"`
}

// registryFile 对应 registry.yaml 的顶层结构。
type registryFile struct {
	Defaults struct {
		Provider string `yaml:"provider"`
		Model    string `yaml:"model"`
	} `yaml:"defaults"`
	Providers []Provider `yaml:"providers"`
}

// Load 解析内嵌注册表。启动期调用一次，失败属于打包错误，应直接终止。
func Load() (*Registry, error) {
	return parse(registryYAML)
}

// LoadFile 从任意来源解析注册表，供测试与将来的自定义注册表使用。
func LoadFile(r io.Reader) (*Registry, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("读取注册表失败：%w", err)
	}
	return parse(data)
}

// Find 按 id 查找模型。找不到返回 false，由调用方决定是报错还是回退默认值。
func (r *Registry) Find(providerID, modelID string) (Provider, Model, bool) {
	for _, p := range r.Providers {
		if p.ID != providerID {
			continue
		}
		for _, m := range p.Models {
			if m.ID == modelID {
				return p, m, true
			}
		}
		return p, Model{}, false
	}
	return Provider{}, Model{}, false
}

// Default 返回默认的供应商与模型 id。
func (r *Registry) Default() (providerID, modelID string) {
	return r.DefaultProvider, r.DefaultModel
}

// parse 解析并校验。校验失败一律返回错误而不是静默忽略：
// 注册表是手改文件，写错字段名时最需要的是立刻报错。
func parse(data []byte) (*Registry, error) {
	var file registryFile
	if err := yaml.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("解析模型注册表失败：%w", err)
	}

	reg := &Registry{
		DefaultProvider: file.Defaults.Provider,
		DefaultModel:    file.Defaults.Model,
		Providers:       file.Providers,
	}
	if err := reg.validate(); err != nil {
		return nil, err
	}
	return reg, nil
}

// validate 检查注册表的自洽性：id 唯一、窗口为正、默认值存在。
func (r *Registry) validate() error {
	if len(r.Providers) == 0 {
		return fmt.Errorf("模型注册表为空：至少需要一个供应商")
	}

	seen := make(map[string]struct{}, len(r.Providers))
	for _, p := range r.Providers {
		if p.ID == "" {
			return fmt.Errorf("模型注册表存在缺少 id 的供应商")
		}
		if _, dup := seen[p.ID]; dup {
			return fmt.Errorf("模型注册表中供应商 id 重复：%q", p.ID)
		}
		seen[p.ID] = struct{}{}

		if len(p.Models) == 0 {
			return fmt.Errorf("供应商 %q 没有配置任何模型", p.ID)
		}
		modelSeen := make(map[string]struct{}, len(p.Models))
		for _, m := range p.Models {
			if m.ID == "" {
				return fmt.Errorf("供应商 %q 存在缺少 id 的模型", p.ID)
			}
			if _, dup := modelSeen[m.ID]; dup {
				return fmt.Errorf("供应商 %q 的模型 id 重复：%q", p.ID, m.ID)
			}
			modelSeen[m.ID] = struct{}{}

			if m.ContextWindow <= 0 {
				return fmt.Errorf("模型 %q 的 context_window 必须为正数，当前为 %d", m.ID, m.ContextWindow)
			}
		}
	}

	if r.DefaultProvider == "" || r.DefaultModel == "" {
		return fmt.Errorf("模型注册表缺少 defaults.provider 或 defaults.model")
	}
	if _, _, ok := r.Find(r.DefaultProvider, r.DefaultModel); !ok {
		return fmt.Errorf("默认模型不存在：%s/%s", r.DefaultProvider, r.DefaultModel)
	}
	return nil
}
