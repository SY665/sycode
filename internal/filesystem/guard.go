package filesystem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Permission 是权限三态。零值是 Denied：新增分支忘记赋值时倾向于"拒绝"。
type Permission int

const (
	Denied Permission = iota
	Pending
	Granted
)

// String 给日志与弹窗用。
func (p Permission) String() string {
	switch p {
	case Granted:
		return "允许"
	case Pending:
		return "需要确认"
	default:
		return "拒绝"
	}
}

// Op 是要做的动作。
//
// 用类型而不是字符串：常见实现的 CheckPath(path, "write") 写错一个字母就是静默放行，
// 这里拼错编译不过（ADR-005 的配套改动）。
type Op string

const (
	OpRead  Op = "read"
	OpWrite Op = "write"
	OpExec  Op = "exec"
)

// ReasonCode 是判定依据的机器可读标签，供 UI 分支（如"因忽略而拒绝"要不要提示 --no-ignore）。
type ReasonCode string

const (
	ReasonOK        ReasonCode = "ok"
	ReasonInWorkdir ReasonCode = "in_workdir"
	ReasonOutside   ReasonCode = "outside"
	ReasonIgnored   ReasonCode = "ignored"
	ReasonSystem    ReasonCode = "system"
	ReasonSensitive ReasonCode = "sensitive"
	ReasonArtifact  ReasonCode = "artifact"
	ReasonVCS       ReasonCode = "vcs"
	ReasonBadPath   ReasonCode = "bad_path"
	ReasonBadOp     ReasonCode = "bad_op"
)

// Reason 说明"为什么"，Message 直接给用户看（中文，带下一步）。
type Reason struct {
	Code    ReasonCode
	Message string
}

// Guard 回答"这个路径能不能读/写/执行"。
//
// 全域只有它做权限判定（ADR-005）：工具只查询，不自己判断；
// Check 是纯判定，不写盘、不建目录、不改状态，因此可以随便调、随便测。
type Guard struct {
	workDir     string
	homeDir     string
	blocked     []string
	artifactDir string
	git         *GitAwareness
}

// NewGuard 预计算工作目录、家目录、系统黑名单与工具输出目录。
//
// 不返回 error：构造失败（如工作目录取不到绝对路径）退化为"工作目录为空"，
// 此时所有路径都按"工作目录外"处理——宁可多问一次，也不放行。
//
// 三个目录都要**解析符号链接**再存，与 `Resolve` 的出口保持同一形态。
// 否则自己的工作目录会被判成"工作目录外"：
//   - macOS 的临时目录是 `/var/...`，EvalSymlinks 后变成 `/private/var/...`；
//   - Windows 的 `C:\Users\RUNNER~1\...`（8.3 短名）解析后变成长名。
//
// 两边一边解析一边不解析，比较就永远不相等；这个 bug 在 Linux 上看不出来（`/tmp` 通常不是链接）。
func NewGuard(workingDir string, git *GitAwareness) *Guard {
	blocked := defaultBlockedPaths()
	// 名单里写的是 /etc、/usr 这类"看起来"的路径，而 IsBlocked 收到的是解析后的路径；
	// macOS 上它们是 /private/etc 这样的链接目标，不解析就一条都拦不住。
	for i, path := range blocked {
		blocked[i] = resolveSymlinks(path)
	}

	guard := &Guard{
		workDir: resolveSymlinks(absClean(workingDir)),
		blocked: blocked,
		git:     git,
	}
	if home, err := os.UserHomeDir(); err == nil {
		guard.homeDir = resolveSymlinks(absClean(home))
	}
	if dir, err := BashOutputDir(); err == nil {
		guard.artifactDir = resolveSymlinks(absClean(dir))
	}
	return guard
}

// WorkDir 返回已绝对化的工作目录。
func (g *Guard) WorkDir() string { return g.workDir }

// Resolve 把用户/模型给的路径变成可判定的绝对路径。
//
// 顺序：展开 ~ → 相对工作目录绝对化 → Clean → 尽量解析符号链接。
// 符号链接必须解析，否则"工作目录里放一个指向 /etc 的链接"就能绕过全部规则。
func (g *Guard) Resolve(path string) (string, error) {
	raw := strings.TrimSpace(path)
	if raw == "" {
		return "", errors.New("路径为空：请提供要访问的文件或目录路径")
	}

	expanded := expandHome(raw, g.homeDir)
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(g.workDir, expanded)
	}

	abs, err := filepath.Abs(expanded)
	if err != nil {
		return "", fmt.Errorf("解析路径 %q 失败：%w", path, err)
	}
	return resolveSymlinks(filepath.Clean(abs)), nil
}

// Check 判定一次访问。这是本包的核心，也是唯一允许决定"能不能"的地方。
func (g *Guard) Check(path string, op Op) (Permission, Reason) {
	if op != OpRead && op != OpWrite && op != OpExec {
		return Denied, Reason{Code: ReasonBadOp,
			Message: fmt.Sprintf("未知操作 %q：只支持 read/write/exec", op)}
	}

	resolved, err := g.Resolve(path)
	if err != nil {
		return Denied, Reason{Code: ReasonBadPath, Message: err.Error()}
	}

	// 工具输出目录先判：它在 ~/.sycode 里，会被下面的"家目录隐藏项"误伤。
	if g.isArtifact(resolved) {
		if op == OpRead {
			return Granted, Reason{Code: ReasonArtifact, Message: "SyCode 自己的工具输出目录"}
		}
		// 写进去等于让模型改自己的证据；执行更不可能需要。
		return Denied, Reason{Code: ReasonArtifact, Message: "这是 SyCode 的工具输出目录，只能读"}
	}

	if blocked := g.blockedPrefix(resolved); blocked != "" {
		return Denied, Reason{Code: ReasonSystem,
			Message: fmt.Sprintf("路径落在受保护位置 %q 内，不允许访问", blocked)}
	}

	if name, hit := g.sensitiveHomeEntry(resolved); hit {
		return Denied, Reason{Code: ReasonSensitive,
			Message: fmt.Sprintf("家目录下的隐藏项 %q 通常存放凭证，不允许访问", name)}
	}

	if g.insideWorkDir(resolved) {
		// .git 里存着远端地址（可能带 token）与钩子脚本：读它没价值，写它等于改版本库。
		if vcs := g.vcsEntry(resolved); vcs != "" {
			return Denied, Reason{Code: ReasonVCS,
				Message: fmt.Sprintf("路径在版本库内部目录 %q 里：可能含远端凭证与钩子脚本，不允许访问", vcs)}
		}
		if g.git != nil && g.git.IsIgnored(resolved) {
			return Denied, Reason{Code: ReasonIgnored,
				Message: "该路径被 .gitignore 忽略，通常是可重新生成的产物；需要时请让用户把它移出忽略列表"}
		}
		if op == OpRead {
			return Granted, Reason{Code: ReasonOK, Message: "工作目录内"}
		}
		return Pending, Reason{Code: ReasonInWorkdir,
			Message: "会改动工作目录内的文件，需要你确认"}
	}

	// 工作目录外：读也要问（邻居目录可能有别人的代码或凭证），执行一律拒绝。
	if op == OpExec {
		return Denied, Reason{Code: ReasonOutside,
			Message: "工作目录外不允许执行命令；请把脚本放进工作目录再运行"}
	}
	return Pending, Reason{Code: ReasonOutside,
		Message: "路径在工作目录外，需要你确认"}
}

// IsBlocked 判断（已解析的）路径是否落在禁区：系统目录、家目录隐藏项。
// 工具输出目录不算禁区（它可读），因此单独排除。
func (g *Guard) IsBlocked(resolved string) bool {
	if g.isArtifact(resolved) {
		return false
	}
	if g.blockedPrefix(resolved) != "" {
		return true
	}
	_, hit := g.sensitiveHomeEntry(resolved)
	return hit
}

// IsIgnored 判断路径是否被 .gitignore 忽略（工作目录外的路径按"不忽略"处理）。
//
// 单独开这个入口是给遍历型工具剪枝用的：它们要在走进子目录之前决定跳不跳，
// 逐个别调 Check 会做符号链接解析与整张黑名单比对，代价高且答非所问。
// 判定本身仍走同一份 GitAwareness，所以与 Check 里的忽略规则必然一致。
func (g *Guard) IsIgnored(path string) bool {
	if g.git == nil {
		return false
	}
	resolved, err := g.Resolve(path)
	if err != nil {
		return false
	}
	return g.git.IsIgnored(resolved)
}

// insideWorkDir 判断是否在工作目录内（含工作目录本身）。
func (g *Guard) insideWorkDir(resolved string) bool {
	return g.workDir != "" && isUnder(g.workDir, resolved)
}

// vcsEntry 判断是否落在工作目录的版本库内部目录里，返回命中的目录名。
func (g *Guard) vcsEntry(resolved string) string {
	if g.workDir == "" {
		return ""
	}
	rel, err := filepath.Rel(normalizeCase(g.workDir), normalizeCase(resolved))
	if err != nil || rel == "." {
		return ""
	}
	first := strings.Split(rel, string(filepath.Separator))[0]
	if first == ".git" || first == ".svn" || first == ".hg" {
		return first
	}
	return ""
}

// isArtifact 判断是否落在工具输出目录内。
func (g *Guard) isArtifact(resolved string) bool {
	return g.artifactDir != "" && isUnder(g.artifactDir, resolved)
}

// blockedPrefix 返回命中的系统黑名单项；没命中返回空串。
func (g *Guard) blockedPrefix(resolved string) string {
	for _, blocked := range g.blocked {
		if isUnder(blocked, resolved) {
			return blocked
		}
	}
	return ""
}

// sensitiveHomeEntry 判断是否落在家目录的隐藏项里（.ssh/.aws/.gnupg/.config…）。
//
// 规则故意用"隐藏即敏感"这种粗粒度：逐个列凭证文件永远漏，而家目录下的隐藏项
// 几乎全是凭证与配置。代价是读 ~/.gitconfig 之类也要被拦住——这类需求可以让用户主动提供内容。
func (g *Guard) sensitiveHomeEntry(resolved string) (string, bool) {
	if g.homeDir == "" || !isUnder(g.homeDir, resolved) {
		return "", false
	}
	rel, err := filepath.Rel(g.homeDir, resolved)
	if err != nil || rel == "." {
		return "", false
	}
	first := strings.Split(rel, string(filepath.Separator))[0]
	if !strings.HasPrefix(first, ".") {
		return "", false
	}
	// ~/.sycode 自身也算隐藏项，但它的 bash 子目录已在常见实现单独处理；
	// 其余部分（配置、日志）不给读：日志里可能有用户粘贴过的密钥。
	return first, true
}

// BashOutputDir 返回工具输出（如 bash 超长输出）的落盘目录。
//
// 它同时是"可读不可写"的授权例外：模型能回头读自己的输出，但不能改。
// 导出是必要的：落盘方（tools）必须与 Guard 用同一个路径，否则会出现
// "写进去了但 Guard 不允许读"的死角。
func BashOutputDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户目录：%w", err)
	}
	return filepath.Join(home, ".sycode", "bash"), nil
}

// defaultBlockedPaths 返回与平台相关的系统目录黑名单。
//
// 只列"改一下就完蛋"的位置；工作目录外还有一层 Pending 兜底，不必把整棵树都列出来。
func defaultBlockedPaths() []string {
	if runtime.GOOS == "windows" {
		// 从环境变量取实际盘符：不同机器上 Windows 可能不在 C:，硬编码会漏。
		candidates := []string{
			os.Getenv("SystemRoot"),
			os.Getenv("SystemDrive") + `\Windows`,
			os.Getenv("ProgramFiles"),
			os.Getenv("ProgramFiles(x86)"),
			os.Getenv("ProgramData"),
		}
		paths := make([]string, 0, len(candidates))
		for _, c := range candidates {
			if p := absClean(c); p != "" {
				paths = append(paths, p)
			}
		}
		return paths
	}

	// /var 只封 lib 与 log：/var/tmp 是正常临时空间，封了会误伤工具输出。
	return []string{"/etc", "/usr", "/bin", "/sbin", "/boot", "/dev", "/proc", "/sys", "/var/lib", "/var/log"}
}

// expandHome 展开开头的 ~。支持 "~"、"~/x"、"~\x"；不支持 "~user"（跨平台语义不一致）。
func expandHome(path, homeDir string) string {
	if homeDir == "" || !strings.HasPrefix(path, "~") {
		return path
	}
	if len(path) == 1 {
		return homeDir
	}
	if path[1] != '/' && path[1] != '\\' {
		return path
	}
	return filepath.Join(homeDir, path[2:])
}

// absClean 绝对化并 Clean；失败返回空串（调用方按"未知"处理，倾向于更严格的判定）。
func absClean(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	return filepath.Clean(abs)
}

// isUnder 判断 child 是否在 parent 之下（含相等）。
//
// 用 filepath.Rel 而不是字符串前缀：前缀匹配会把 /tmp/work2 当成 /tmp/work 的子目录，
// 那就是一个"换个目录名就能越权"的漏洞。
func isUnder(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	parent, child = normalizeCase(parent), normalizeCase(child)
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// normalizeCase 在 Windows 上折成小写：那里路径不分大小写，
// 不折会让 C:\Work 与 c:\work\x 被判成"不同目录"。
func normalizeCase(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

// evalSymlinks 是 filepath.EvalSymlinks 的包内别名。
//
// 单独列出只为可测：Windows 默认不允许普通用户建符号链接（需要 SeCreateSymbolicLinkPrivilege），
// 而这恰恰是最该测的攻击面。测试里替换成假的映射，就能在没有权限的机器上验证穿越判定。
// 生产代码不要改它。
var evalSymlinks = filepath.EvalSymlinks

// resolveSymlinks 尽量解析符号链接。
//
// 目标存在就整体解析；不存在（write_file 新建文件的常态）则解析最近的已存在祖先，
// 再把剩余段接回去——"父目录是符号链接"同样可能把写入带到工作目录之外。
func resolveSymlinks(abs string) string {
	if resolved, err := evalSymlinks(abs); err == nil {
		return resolved
	}

	dir, base := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if dir == abs || dir == "." {
		// 已经到根，或路径本身不可再分：保持原样。
		return abs
	}
	parent := resolveSymlinks(dir)
	if base == "" {
		return parent
	}
	return filepath.Join(parent, base)
}
