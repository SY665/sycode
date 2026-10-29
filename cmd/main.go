// SyCode 的进程入口。
//
// 本文件只做进程级动作：初始化日志、装配命令树、决定退出码。
// 业务逻辑一律不写在这里，顺序见 docs/CODE-DESIGN.md §1。
package main

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/sy665/sycode/internal/agentcore"
	"github.com/sy665/sycode/internal/cleanup"
	"github.com/sy665/sycode/internal/cli/cmd"
	"github.com/sy665/sycode/internal/config"
	"github.com/sy665/sycode/internal/filesystem"
	"github.com/sy665/sycode/internal/llm"
	"github.com/sy665/sycode/internal/logging"
	"github.com/sy665/sycode/internal/providers"
	"github.com/sy665/sycode/internal/session"
	"github.com/sy665/sycode/internal/tools"
)

// version 与 commit 由构建注入：
// go build -ldflags "-X main.version=0.1.0 -X main.commit=$(git rev-parse --short HEAD)"
//
// 注意：必须是 var 而不是 const —— 链接器的 -X 只能改写字符串变量，
// 对 const 会静默失效（实测仍输出默认值），构建看起来成功但版本号是错的。
var (
	version = "dev"
	commit  = "none"
)

func main() {
	os.Exit(run())
}

// run 承担全部组装并返回退出码：0 成功，1 一般错误，2 用法错误。
//
// 启动顺序见 docs/CODE-DESIGN.md §1：日志必须最先就绪，后续步骤才有地方报错。
func run() int {
	closeLog, logPath, err := logging.Init()
	if err != nil {
		// 日志没起来时只能用 stderr；这条错误本身必须可读（CODING-STANDARDS §13）。
		fmt.Fprintf(os.Stderr, "错误：初始化日志失败：%v\n", err)
		return 1
	}
	defer closeLog()

	slog.Debug("日志已初始化", "path", logPath)

	// 回收过期工具输出：放在日志就绪之后（这样失败有地方记），装配之前（不占用户等待时间）。
	// 失败只记一行：回收是尽力而为，不能因为删不掉旧文件就起不来。
	if err := cleanup.Run(); err != nil {
		slog.Warn("清理过期工具输出失败，本次启动继续", "err", err)
	}

	root := cmd.NewRootCommand(cmd.Options{
		Version:  version,
		Commit:   commit,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		BuildEnv: buildEnv,
	})

	err = root.Execute()
	if err == nil {
		return 0
	}

	code := cmd.ExitCode(err)
	// 用法错误已经带"参数错误"语义，再套一层"错误："会显得重复。
	if code == 2 {
		fmt.Fprintf(os.Stderr, "参数错误：%v\n用法见 sycode --help\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "错误：%v\n", err)
	}
	slog.Debug("命令执行失败", "err", err, "exit_code", code)
	return code
}

// buildEnv 按 docs/CODE-DESIGN.md §1 的顺序准备运行环境：
// 注册表 → 用户配置 → 合并 → 密钥校验 → 客户端 → 系统提示 → 回合引擎。
//
// 为什么是一个"按需调用"的函数，而不是在 run() 里提前执行完再塞进 Options：
// 版本与帮助不需要密钥。提前解析会让新用户敲的第一条 `sycode --version`
// 就撞上"缺少密钥"，恰好卡在他还没机会配置的时候。
//
// 错误原样返回：config/providers 的文案已是中文且带修改指引（CODING-STANDARDS §13），
// 这里再包一层只会得到"错误：无法准备运行环境：缺少密钥：…"这样的叠词。
func buildEnv(build cmd.BuildOptions) (cmd.Env, error) {
	registry, err := providers.Load()
	if err != nil {
		// 内嵌注册表损坏属于打包错误：用户改不了，但也不能静默退回默认值。
		return cmd.Env{}, fmt.Errorf("内置模型注册表不可用（属打包问题，请反馈）：%w", err)
	}

	path, err := config.DefaultPath()
	if err != nil {
		return cmd.Env{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return cmd.Env{}, err
	}

	// 两个空串表示"命令行没有指定"，此时优先级为 配置文件 active_* > 注册表 defaults。
	resolved, err := cfg.Resolve(registry, "", "")
	if err != nil {
		return cmd.Env{}, err
	}

	// 密钥在这里从"校验有没有"变成"真的用上"：交给客户端，且任何情况下都不写日志。
	apiKey, err := config.ResolveKey(resolved)
	if err != nil {
		return cmd.Env{}, err
	}

	client, err := llm.NewOpenAI(llm.OpenAIConfig{
		BaseURL: resolved.BaseURL,
		APIKey:  apiKey,
		Model:   resolved.ModelID,
	})
	if err != nil {
		return cmd.Env{}, err
	}

	// 只记供应商、模型与端点；密钥永不记录。
	slog.Debug("模型已就绪",
		"provider", resolved.ProviderID,
		"model", resolved.ModelID,
		"base_url", resolved.BaseURL,
	)

	workDir, err := os.Getwd()
	if err != nil {
		slog.Debug("取工作目录失败，工具将无法判断工作目录范围", "err", err)
		workDir = ""
	}
	// .gitignore 读不到不算致命：Guard 还有系统路径、家目录、版本库目录三层规则兜着。
	git, gitErr := filesystem.NewGitAwareness(workDir)
	if gitErr != nil {
		slog.Debug("载入 .gitignore 失败，忽略规则将不生效", "err", gitErr)
	}
	guard := filesystem.NewGuard(workDir, git)

	// 权限通路两侧共用同一个对象：工具侧（经 perm）放请求，界面侧（经 Env）取出来弹窗。
	// 它不能挂在 Core 上——那样引擎得反过来持有界面，装配顺序会成环。
	//
	// 无界面模式下没有人能确认，两条路：默认不给通道（写与执行被拒），
	// 或 `--allow-write` 显式给一个一律放行的通道。放行也只放开"本来要问用户"的那一类，
	// 系统路径、`.git/`、家目录隐藏项在 Guard 那一层就被拒了。
	var (
		perm   tools.PermissionRequester
		prompt *agentcore.PermissionPrompt
	)
	if build.Headless {
		if build.AllowWrite {
			perm = agentcore.AutoApprove{}
			slog.Warn("非交互模式已开启写权限（--allow-write）")
		}
	} else {
		prompt = agentcore.NewPermissionPrompt()
		perm = prompt
	}

	writeRegistry, err := buildTools(guard, perm)
	if err != nil {
		return cmd.Env{}, err
	}

	// 会话：交互模式下默认续上次，--new 新建；无界面模式不落盘（跑一次就结束）。
	var (
		store   *session.Store
		sess    *session.Session
		history []session.Turn
		note    string
	)
	if !build.Headless {
		store, sess, history, note = openSession(workDir, build.NewSession)
	}

	// 系统提示只拼一次：它同时要交给引擎、还要用来估算固定开销。
	system := llm.Build(promptEnv(resolved.ModelID, workDir))

	core := agentcore.NewWithSession(client, writeRegistry, guard, perm, system, store, sess)
	if len(history) > 0 {
		// 上下文由引擎自己恢复；界面拿到的 history 只用来把屏幕填上。
		core.Restore(history)
	}

	// 窗口未知就给 0，界面显示"上限未知"而不是 0%——注册表里没填的模型是存在的。
	contextWindow, ok := registry.GetModelContextWindow(resolved.ProviderID, resolved.ModelID)
	base := baseContextTokens(system, writeRegistry)
	slog.Debug("上下文占用基线", "base_tokens", base, "window", contextWindow, "window_known", ok)

	return cmd.Env{
		Model: resolved.ModelID,
		Core:  core,
		// 无界面模式没有弹窗对象可交，界面侧本来就拿不到它。
		Permission:    prompt,
		History:       history,
		RestoredNote:  note,
		ContextBase:   base,
		ContextWindow: contextWindow,
	}, nil
}

// baseContextTokens 估算"每轮都在、但不属于任何一条消息"的那部分开销：
// 系统提示 + 每个工具的说明与 schema。
//
// 算一次传下去，界面不重算：这部分在一次运行里不变，而它可能是几千 token。
func baseContextTokens(system string, registry *tools.Registry) int {
	total := llm.EstimateTokens(system)
	if registry == nil {
		return total
	}
	for _, schema := range registry.Schemas() {
		total += llm.EstimateTokens(schema.Name) + llm.EstimateTokens(schema.Description) +
			llm.EstimateJSON(schema.Input)
	}
	return total
}

// openSession 打开会话库并决定"接着上次"还是"新建"。
//
// 任何一步失败都降级为"本次不落盘"：丢的是历史，不该是这次对话。
func openSession(workDir string, forceNew bool) (*session.Store, *session.Session, []session.Turn, string) {
	store, err := session.NewStore(workDir)
	if err != nil {
		slog.Warn("会话目录不可用，本次对话不会落盘", "err", err)
		return nil, nil, nil, ""
	}

	if !forceNew {
		history, sess, note := resume(store)
		if sess != nil {
			return store, sess, history, note
		}
	}

	sess, err := store.Create()
	if err != nil {
		slog.Warn("无法新建会话，本次对话不会落盘", "err", err)
		return nil, nil, nil, ""
	}
	slog.Debug("已新建会话", "id", sess.ID)
	return store, sess, nil, ""
}

// resume 尝试续上最近一次会话；没有可续的（或读不出来）时返回 nil。
func resume(store *session.Store) ([]session.Turn, *session.Session, string) {
	summary, ok, err := store.Latest()
	if err != nil {
		slog.Warn("读取会话列表失败，本次不恢复", "err", err)
		return nil, nil, ""
	}
	if !ok {
		return nil, nil, ""
	}

	loaded, err := store.Load(summary.ID)
	if err != nil {
		slog.Warn("恢复上次会话失败，改为新建", "id", summary.ID, "err", err)
		return nil, nil, ""
	}
	sess, err := store.Open(summary.ID)
	if err != nil {
		slog.Warn("无法重开上次会话，改为新建", "id", summary.ID, "err", err)
		return nil, nil, ""
	}

	turns := session.BuildTurns(loaded.Events)
	slog.Debug("已恢复会话", "id", summary.ID, "turns", len(turns))
	return turns, sess, fmt.Sprintf("已恢复 %s（%d 轮）", summary.ID, len(turns))
}

// buildTools 注册全部工具。顺序固定：模型看到 schema 的顺序会影响它的选择倾向。
//
// perm 必须传给每个工具：真正弹窗的是工具里的 askAccess，而不是回合循环。
// 传 nil 等于只读模式——写与执行会被拒绝并说明原因。
func buildTools(guard *filesystem.Guard, perm tools.PermissionRequester) (*tools.Registry, error) {
	registry := tools.NewRegistry()

	// bash 可能装不上（机器上没有可用 shell）。那种情况下其余工具照常可用，
	// 不该因为一条命令跑不了就让整个程序起不来。
	bash, err := tools.NewBashTool(guard, perm)
	if err != nil {
		slog.Warn("bash 工具不可用，其余工具不受影响", "err", err)
	}

	available := []tools.Tool{
		tools.NewReadFileTool(guard, perm),
		tools.NewGlobTool(guard, perm),
		tools.NewGrepTool(guard, perm),
		tools.NewWriteFileTool(guard, perm),
		tools.NewEditFileTool(guard, perm),
	}
	if bash != nil {
		available = append(available, bash)
	}

	for _, tool := range available {
		if err := registry.Register(tool); err != nil {
			// 注册失败只可能是内部 bug（重名/空名），直接终止比带着残缺工具表跑更安全。
			return nil, fmt.Errorf("装配工具失败：%w", err)
		}
	}
	slog.Debug("工具已注册", "count", registry.Len())
	return registry, nil
}

// promptEnv 收集系统提示要用的环境事实。workDir 由调用方给（取不到时为空串）。
func promptEnv(model, workDir string) llm.Env {
	return llm.Env{
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		WorkDir: workDir,
		Shell:   detectShell(),
		Model:   model,
		Today:   time.Now(),
	}
}

// detectShell 取命令执行工具会用的解释器名，写进系统提示（ADR-006 要求）。
//
// 与真正执行命令用的是同一个来源（tools.ResolveShell）：两处结论一旦分叉，
// 模型就会按一种语法写命令、而实际在另一种 shell 里跑。
func detectShell() string {
	shell, err := tools.ResolveShell()
	if err != nil {
		// 拿不到就省略这一行：提示里少一行，比写一个假名字强。
		slog.Debug("未能确定 shell，系统提示将省略该行", "err", err)
		return ""
	}
	return shell.Name
}
