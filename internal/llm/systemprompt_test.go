package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func testEnv(workDir string) Env {
	return Env{
		OS:      "windows",
		Arch:    "amd64",
		WorkDir: workDir,
		Shell:   "bash",
		Model:   "gpt-4o-mini",
		Today:   time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC),
	}
}

func TestBuildIncludesIdentityAndEnv(t *testing.T) {
	got := Build(testEnv(t.TempDir()))

	for _, want := range []string{
		"SyCode",
		"回答用中文",
		"windows/amd64",
		"Current shell: bash",
		"gpt-4o-mini",
		"2026-10-29",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("系统提示缺少 %q：\n%s", want, got)
		}
	}
	// ADR-006：shell 用小写原文，别翻译成"当前 shell 是 bash"。
	if strings.Contains(got, "当前 shell") {
		t.Errorf("shell 行应保留 ADR-006 的原文写法：\n%s", got)
	}
}

func TestBuildSkipsEmptyFields(t *testing.T) {
	// 只有身份段时不出现空的环境段，也不出现"操作系统："这种半截行。
	got := Build(Env{})
	if strings.Contains(got, "当前环境") {
		t.Errorf("没有环境事实时不应输出环境段：\n%s", got)
	}
	if strings.Contains(got, "操作系统：") {
		t.Errorf("缺少 OS 时不应输出该行：\n%s", got)
	}
	if !strings.Contains(got, "SyCode") {
		t.Errorf("身份段不应被省略：\n%s", got)
	}
}

func TestBuildIncludesProjectInstructions(t *testing.T) {
	dir := t.TempDir()
	body := "本次改动只碰 internal/llm。\n提交信息用英文。"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("写入 AGENTS.md 失败：%v", err)
	}

	got := Build(testEnv(dir))
	if !strings.Contains(got, "本次改动只碰 internal/llm") || !strings.Contains(got, "提交信息用英文") {
		t.Errorf("应注入 AGENTS.md 内容：\n%s", got)
	}
	if !strings.Contains(got, "AGENTS.md") {
		t.Errorf("应说明项目指令的来源：\n%s", got)
	}
}

func TestBuildWithoutProjectInstructions(t *testing.T) {
	// 没有 AGENTS.md、或工作目录为空，都不能阻断：项目指令只是增强。
	for _, dir := range []string{t.TempDir(), ""} {
		got := Build(testEnv(dir))
		if strings.Contains(got, "项目指令") {
			t.Errorf("工作目录 %q 不应有项目指令段：\n%s", dir, got)
		}
	}
}

func TestBuildTruncatesOversizedProjectInstructions(t *testing.T) {
	dir := t.TempDir()
	// 用中文重复填充：每字 3 字节，必然跨过上界，能顺带压到多字节边界。
	body := strings.Repeat("这是一句用来撑大 AGENTS.md 的中文说明。", 1000)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("写入 AGENTS.md 失败：%v", err)
	}

	got := Build(testEnv(dir))
	if !strings.Contains(got, "已截断") {
		t.Error("超限时应注明已截断，否则模型会以为自己看到了全文")
	}
	// 截断后仍是合法 UTF-8（不能切出半个汉字），且总长受控。
	if !utf8.ValidString(got) {
		t.Error("截断后出现了非法 UTF-8，说明切在了多字节字符中间")
	}
	if len(got) > maxProjectInstructions+2048 {
		t.Errorf("提示词过长（%d 字节），截断没生效", len(got))
	}
}

func TestTruncateUTF8KeepsRuneBoundary(t *testing.T) {
	cases := []struct {
		name      string
		s         string
		limit     int
		want      string
		truncated bool
	}{
		{"未超限原样返回", "abc", 10, "abc", false},
		{"恰好等于上限", "abc", 3, "abc", false},
		{"ASCII 边界", "abcdef", 3, "abc", true},
		{"中文整字边界", "中文", 6, "中文", false},
		// "中"占 3 字节：limit=4 落在第二个字中间，应退回到第 3 字节。
		{"中文切在字中间", "中文", 4, "中", true},
		{"limit 为 0", "中文", 0, "", true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, truncated := truncateUTF8(c.s, c.limit)
			if got != c.want || truncated != c.truncated {
				t.Fatalf("期望 (%q,%v)，实际 (%q,%v)", c.want, c.truncated, got, truncated)
			}
			if !utf8.ValidString(got) {
				t.Errorf("结果不是合法 UTF-8：%q", got)
			}
		})
	}
}
