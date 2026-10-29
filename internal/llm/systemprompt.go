package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// maxProjectInstructions 是 AGENTS.md 的注入上限。
	//
	// 项目指令再重要，也不能挤占对话窗口：一份 40KiB 的 AGENTS.md 会让每回合都多花上万 token。
	// 超出部分截断并注明，让模型知道自己看到的不全（它会自己去读原文）。
	maxProjectInstructions = 8 * 1024

	// projectInstructionsFile 与常见实现及主流工具的约定同名，用户不必学第二套。
	projectInstructionsFile = "AGENTS.md"
)

// Env 是系统提示要用到的环境事实。
//
// 全部由调用方注入而不是在这里读 os/runtime：提示词因此可测，
// 也便于将来"在同一进程里模拟另一个工作目录"。
type Env struct {
	OS      string
	Arch    string
	WorkDir string
	// Shell 是 bash 工具会用的解释器名（ADR-006）。ADR 要求原文 "Current shell: <name>"，
	// 因为工具描述与模型行为都按这个名字分支，翻译成中文反而会让模型猜不到是哪种 shell。
	Shell string
	Model string
	// Today 为零值时省略日期行：模型对日期不敏感，宁缺勿造。
	Today time.Time
}

// Build 拼出完整系统提示：身份 → 环境 → 项目指令。
//
// 工具约定段（"有哪些工具、什么时候用"）随 P3 的工具一起进来，现在写只会让模型
// 以为自己有工具却调不到。
func Build(env Env) string {
	sections := []string{
		identitySection(),
		envSection(env),
		projectSection(env.WorkDir),
	}

	// 空段不参与拼接：否则项目没有 AGENTS.md 时就多出一串空行。
	parts := make([]string, 0, len(sections))
	for _, section := range sections {
		if section != "" {
			parts = append(parts, section)
		}
	}
	return strings.Join(parts, "\n\n")
}

// identitySection 说明"你是谁、按什么规矩做事"。
//
// 用中文写：本产品的界面、错误与工具描述都是中文，提示词跟着统一，
// 模型输出才不会一半中文一半英文。要换语言只改这一个函数。
func identitySection() string {
	return strings.Join([]string{
		"你是 SyCode，运行在用户终端里的编程代理。",
		"",
		"工作方式：",
		"- 回答用中文；代码、路径、命令、标识符保持原文。",
		"- 先看清再动手：结论要基于实际读到的文件与命令输出，不靠猜测。",
		"- 不确定就说不确定，并给出验证方式；不要编造命令输出或测试结果。",
		"- 改动尽量小且可回退，只改与当前任务相关的部分。",
	}, "\n")
}

// envSection 给出"在哪儿干活"，缺值不写该行。
func envSection(env Env) string {
	lines := []string{"当前环境："}

	if goos := strings.TrimSpace(env.OS); goos != "" {
		lines = append(lines, "- 操作系统："+goos+archSuffix(env.Arch))
	}
	if workDir := strings.TrimSpace(env.WorkDir); workDir != "" {
		lines = append(lines, "- 工作目录："+workDir)
	}
	if shell := strings.TrimSpace(env.Shell); shell != "" {
		lines = append(lines, "- Current shell: "+shell)
	}
	if model := strings.TrimSpace(env.Model); model != "" {
		lines = append(lines, "- 模型："+model)
	}
	if !env.Today.IsZero() {
		lines = append(lines, "- 今天："+env.Today.Format("2006-01-02"))
	}

	// 只有标题没有任何事实时，整段都不要。
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// archSuffix 拼出 "/amd64"，架构为空时返回空串。
func archSuffix(arch string) string {
	if strings.TrimSpace(arch) == "" {
		return ""
	}
	return "/" + arch
}

// projectSection 注入工作目录下的 AGENTS.md。
//
// 读不到（不存在、无权限、是目录）一律当作"没有项目指令"：项目指令是增强，
// 不该让一次对话起不来；模型发现信息不足时会自己去读文件。
func projectSection(workDir string) string {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(workDir, projectInstructionsFile))
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}

	content, truncated := truncateUTF8(content, maxProjectInstructions)
	if truncated {
		content += fmt.Sprintf("\n\n（已截断：%s 超过 %d 字节，后续内容未注入，需要时可自行读取该文件）",
			projectInstructionsFile, maxProjectInstructions)
	}
	return "项目指令（来自 " + projectInstructionsFile + "）：\n" + content
}

// truncateUTF8 按字节上限截断，且不切断多字节字符。
//
// 中文按 3 字节算，直接 s[:limit] 会把最后一个字切成半个，
// 变成 U+FFFD 替换符——模型读到的没意义，日志里也难看。
func truncateUTF8(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut], true
}
