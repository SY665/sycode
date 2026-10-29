package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sy665/sycode/internal/providers"
)

// loadRegistry 取内嵌注册表；测试依赖它包含 openai/gpt-4o-mini 与 local/local-model。
func loadRegistry(t *testing.T) *providers.Registry {
	t.Helper()
	reg, err := providers.Load()
	if err != nil {
		t.Fatalf("加载内嵌注册表失败：%v", err)
	}
	return reg
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "configs.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("写入测试配置失败：%v", err)
	}
	return path
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "不存在.json"))
	if err != nil {
		t.Fatalf("文件不存在不应报错（首次运行场景）：%v", err)
	}
	if cfg.ActiveProvider != "" || cfg.ActiveModel != "" {
		t.Errorf("缺失文件应得到空配置，实际 %+v", cfg)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	path := writeConfig(t, `{"active_provider":`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
	if !strings.Contains(err.Error(), "解析配置") {
		t.Errorf("错误信息应说明是解析失败：%v", err)
	}
	// 语法错误要带字节偏移，用户才知道改哪里。
	if !strings.Contains(err.Error(), "字节") {
		t.Errorf("错误信息应包含定位信息：%v", err)
	}
}

func TestResolveFallsBackToRegistryDefaults(t *testing.T) {
	reg := loadRegistry(t)
	got, err := Config{}.Resolve(reg, "", "")
	if err != nil {
		t.Fatalf("解析默认值失败：%v", err)
	}

	wantProvider, wantModel := reg.Default()
	if got.ProviderID != wantProvider || got.ModelID != wantModel {
		t.Errorf("默认值不符：期望 %s/%s，实际 %s/%s", wantProvider, wantModel, got.ProviderID, got.ModelID)
	}
	if got.BaseURL == "" || got.ContextWindow <= 0 {
		t.Errorf("解析结果缺少端点或窗口：%+v", got)
	}
}

func TestResolveUsesConfigWhenArgsEmpty(t *testing.T) {
	reg := loadRegistry(t)
	cfg := Config{ActiveProvider: "local", ActiveModel: "local-model", ThinkingEffort: "low"}

	got, err := cfg.Resolve(reg, "", "")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got.ProviderID != "local" || got.ModelID != "local-model" {
		t.Errorf("应使用配置文件里的选择，实际 %s/%s", got.ProviderID, got.ModelID)
	}
	if got.ThinkingEffort != "low" {
		t.Errorf("thinking_effort 应透传，实际 %q", got.ThinkingEffort)
	}
}

func TestResolveArgsOverrideConfig(t *testing.T) {
	reg := loadRegistry(t)
	cfg := Config{ActiveProvider: "local", ActiveModel: "local-model"}

	got, err := cfg.Resolve(reg, "openai", "gpt-4o")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got.ProviderID != "openai" || got.ModelID != "gpt-4o" {
		t.Errorf("显式参数应优先于配置，实际 %s/%s", got.ProviderID, got.ModelID)
	}
}

func TestResolveProviderOverride(t *testing.T) {
	reg := loadRegistry(t)
	cfg := Config{Providers: map[string]ProviderConfig{
		"openai": {BaseURL: "https://proxy.example.com/v1", APIKeyEnv: "MY_OPENAI_KEY"},
	}}

	got, err := cfg.Resolve(reg, "openai", "gpt-4o-mini")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got.BaseURL != "https://proxy.example.com/v1" {
		t.Errorf("base_url 覆盖未生效：%q", got.BaseURL)
	}
	if got.APIKeyEnv != "MY_OPENAI_KEY" {
		t.Errorf("api_key_env 覆盖未生效：%q", got.APIKeyEnv)
	}
}

func TestResolveUnknownProvider(t *testing.T) {
	reg := loadRegistry(t)

	_, err := Config{}.Resolve(reg, "nope", "gpt-4o-mini")
	if err == nil {
		t.Fatal("未知供应商应返回错误")
	}
	if !strings.Contains(err.Error(), "未知供应商") {
		t.Errorf("错误信息应指明未知供应商：%v", err)
	}
}

func TestResolveModelNotBelongingToProvider(t *testing.T) {
	reg := loadRegistry(t)

	_, err := Config{}.Resolve(reg, "deepseek", "gpt-4o-mini")
	if err == nil {
		t.Fatal("模型不属于该供应商时应返回错误")
	}
	// 必须能区分于"未知供应商"，否则用户不知道该改哪一项。
	if !strings.Contains(err.Error(), "不属于供应商") {
		t.Errorf("错误信息应说明模型与供应商不匹配：%v", err)
	}
	if strings.Contains(err.Error(), "未知供应商") {
		t.Errorf("不应误报为未知供应商：%v", err)
	}
}

func TestResolveProviderOnlyUsesThatProvidersFirstModel(t *testing.T) {
	reg := loadRegistry(t)
	// 用户只把 active_provider 改成 deepseek、没写 active_model：
	// 此时不能沿用 defaults.model（属于 openai），否则会报"模型不属于供应商"。
	got, err := Config{ActiveProvider: "deepseek"}.Resolve(reg, "", "")
	if err != nil {
		t.Fatalf("只指定供应商时应能解析出模型：%v", err)
	}
	if got.ModelID != "deepseek-chat" {
		t.Errorf("应退到该供应商的第一个模型 deepseek-chat，实际 %q", got.ModelID)
	}
	// 端点与密钥变量也必须跟着换家，否则会拿 deepseek 的模型去打 openai。
	if got.APIKeyEnv != "DEEPSEEK_API_KEY" {
		t.Errorf("api_key_env 应来自 deepseek，实际 %q", got.APIKeyEnv)
	}
}

func TestResolveKey(t *testing.T) {
	t.Run("读取环境变量", func(t *testing.T) {
		t.Setenv("SYCODE_TEST_KEY", "sk-test")
		got, err := ResolveKey(Resolved{ProviderID: "openai", APIKeyEnv: "SYCODE_TEST_KEY"})
		if err != nil {
			t.Fatalf("应成功读取密钥：%v", err)
		}
		if got != "sk-test" {
			t.Errorf("密钥不符：%q", got)
		}
	})

	t.Run("缺失时给出可操作错误", func(t *testing.T) {
		key, err := ResolveKey(Resolved{ProviderID: "openai", APIKeyEnv: "SYCODE_TEST_MISSING"})
		if err == nil {
			t.Fatal("缺少密钥应返回错误")
		}
		if key != "" {
			t.Errorf("失败时不应返回密钥，实际 %q", key)
		}
		for _, want := range []string{"SYCODE_TEST_MISSING", "openai", "缺少密钥"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("错误信息应包含 %q：%v", want, err)
			}
		}
	})

	t.Run("不需要密钥的供应商", func(t *testing.T) {
		got, err := ResolveKey(Resolved{ProviderID: "local", APIKeyEnv: ""})
		if err != nil {
			t.Fatalf("无密钥供应商不应报错：%v", err)
		}
		if got != "" {
			t.Errorf("应返回空密钥，实际 %q", got)
		}
	})
}

func TestDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path, err := DefaultPath()
	if err != nil {
		t.Fatalf("取默认路径失败：%v", err)
	}
	if want := filepath.Join(home, ".sycode", "configs.json"); path != want {
		t.Errorf("默认路径不符：期望 %q，实际 %q", want, path)
	}
}
