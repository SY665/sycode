// Package config 负责用户配置的加载与合并。
//
// 本包只做三件事：读取 ~/.sycode/configs.json、把用户选择与内置注册表合并成
// 一个可直接使用的 Resolved、解析密钥从哪个环境变量取。
// 不发请求、不校验模型能力（那是 llm 与 providers 的事）。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sy665/sycode/internal/providers"
)

// ProviderConfig 是用户对某个供应商的覆盖项。
// 留空表示"沿用注册表里的值"，这样升级注册表后能自动生效。
type ProviderConfig struct {
	// BaseURL 覆盖注册表中的端点，用于代理、自建网关或兼容服务。
	BaseURL string `json:"base_url,omitempty"`
	// APIKeyEnv 覆盖注册表中的环境变量名，用于同一个供应商配多份密钥。
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

// Config 是配置文件的内存形态。
type Config struct {
	ActiveProvider string                    `json:"active_provider,omitempty"`
	ActiveModel    string                    `json:"active_model,omitempty"`
	ThinkingEffort string                    `json:"thinking_effort,omitempty"`
	Providers      map[string]ProviderConfig `json:"providers,omitempty"`
}

// Resolved 是"用户选择 + 注册表 + 默认值"合并后的最终结果。
// 下游只认它，不再关心值从哪一层来。
type Resolved struct {
	ProviderID     string
	ModelID        string
	BaseURL        string
	APIKeyEnv      string
	ThinkingEffort string
	ContextWindow  int
}

// DefaultPath 返回配置文件路径 ~/.sycode/configs.json。
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户目录：%w", err)
	}
	return filepath.Join(home, ".sycode", "configs.json"), nil
}

// Load 读取配置文件。
//
// 文件不存在不是错误：首次运行时用户还没有配置，此时应回退到注册表默认值，
// 而不是让程序起不来。文件存在但内容非法才是错误。
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Config{}, nil
		}
		return Config{}, fmt.Errorf("读取配置 %q 失败：%w。请确认文件权限", path, err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("解析配置 %q 失败：%w。请检查 JSON 格式（第 %d 字节附近）", path, err, offsetOf(err))
	}
	return cfg, nil
}

// Resolve 合并配置与注册表，得出最终使用的供应商与模型。
//
// 优先级：显式传入的 provider/model > 配置文件里的 active_* > 注册表 defaults。
// provider/model 传空串表示"没显式指定"，这是命令行的常态。
func (c Config) Resolve(reg *providers.Registry, providerID, modelID string) (Resolved, error) {
	if providerID == "" {
		providerID = c.ActiveProvider
	}
	if modelID == "" {
		modelID = c.ActiveModel
	}
	if providerID == "" || modelID == "" {
		defProvider, defModel := reg.Default()
		if providerID == "" {
			providerID = defProvider
		}
		if modelID == "" {
			modelID = defModel
		}
	}

	provider, model, ok := reg.Find(providerID, modelID)
	if !ok {
		// Find 在"供应商命中、模型未命中"时仍会返回 Provider，
		// 借此区分两种失败：用户该改 active_provider 还是 active_model。
		if provider.ID != "" {
			return Resolved{}, fmt.Errorf("模型 %q 不属于供应商 %q。请检查配置中的 active_model", modelID, providerID)
		}
		return Resolved{}, fmt.Errorf("未知供应商 %q。请检查配置中的 active_provider，或注册表里的 providers", providerID)
	}

	resolved := Resolved{
		ProviderID:     provider.ID,
		ModelID:        model.ID,
		BaseURL:        provider.BaseURL,
		APIKeyEnv:      provider.APIKeyEnv,
		ThinkingEffort: c.ThinkingEffort,
		ContextWindow:  model.ContextWindow,
	}

	// 用户覆盖优先于注册表默认值。
	if override, ok := c.Providers[provider.ID]; ok {
		if override.BaseURL != "" {
			resolved.BaseURL = override.BaseURL
		}
		if override.APIKeyEnv != "" {
			resolved.APIKeyEnv = override.APIKeyEnv
		}
	}
	return resolved, nil
}

// ResolveKey 取出该供应商的密钥。
//
// 返回空串且无错误表示"这个供应商不需要密钥"（如本机服务），调用方不应因此报错。
// 错误信息只提环境变量名，绝不回显密钥内容（CODING-STANDARDS §8）。
func ResolveKey(resolved Resolved) (string, error) {
	if strings.TrimSpace(resolved.APIKeyEnv) == "" {
		return "", nil
	}
	key := strings.TrimSpace(os.Getenv(resolved.APIKeyEnv))
	if key == "" {
		return "", fmt.Errorf("缺少密钥：请设置环境变量 %s（供应商 %q）。可用 export %s=... 临时生效",
			resolved.APIKeyEnv, resolved.ProviderID, resolved.APIKeyEnv)
	}
	return key, nil
}

// offsetOf 从 JSON 语法错误中取出字节偏移，便于用户定位；取不到时返回 -1。
func offsetOf(err error) int64 {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return syntaxErr.Offset
	}
	return -1
}
