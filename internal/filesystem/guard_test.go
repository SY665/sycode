package filesystem

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// newWorkDir 造一个带 .gitignore 的工作目录。
func newWorkDir(t *testing.T, gitignore string) string {
	t.Helper()
	dir := t.TempDir()
	if gitignore != "" {
		writeFile(t, filepath.Join(dir, ".gitignore"), gitignore)
	}
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("写文件失败：%v", err)
	}
}

// newGuard 构造带 gitignore 的 Guard。
func newGuard(t *testing.T, workDir string) *Guard {
	t.Helper()
	git, err := NewGitAwareness(workDir)
	if err != nil {
		t.Fatalf("构造 GitAwareness 失败：%v", err)
	}
	return NewGuard(workDir, git)
}

// require 断言判定结果。
func require(t *testing.T, got Permission, reason Reason, want Permission, wantCode ReasonCode) {
	t.Helper()
	if got != want {
		t.Fatalf("权限不符：期望 %v(%s)，实际 %v(%s)：%s", want, wantCode, got, reason.Code, reason.Message)
	}
	if reason.Code != wantCode {
		t.Errorf("判定依据不符：期望 %s，实际 %s（%s）", wantCode, reason.Code, reason.Message)
	}
	if reason.Message == "" {
		t.Error("必须给出可读理由：权限弹窗要显示它")
	}
}

// TestCheckInsideWorkdirCoverPolicy 覆盖策略表第 1 行：工作目录内、未被忽略。
func TestCheckInsideWorkdirCoverPolicy(t *testing.T) {
	guard := newGuard(t, newWorkDir(t, "dist/\n*.log\n"))

	perm, reason := guard.Check("main.go", OpRead)
	require(t, perm, reason, Granted, ReasonOK)

	perm, reason = guard.Check("main.go", OpWrite)
	require(t, perm, reason, Pending, ReasonInWorkdir)

	perm, reason = guard.Check("main.go", OpExec)
	require(t, perm, reason, Pending, ReasonInWorkdir)
}

// TestCheckIgnoredByGitignore 覆盖第 2 行：被 .gitignore 忽略的一律拒绝。
func TestCheckIgnoredByGitignore(t *testing.T) {
	guard := newGuard(t, newWorkDir(t, "dist/\n*.log\n"))

	for _, path := range []string{filepath.Join("dist", "bundle.js"), "app.log"} {
		for _, op := range []Op{OpRead, OpWrite, OpExec} {
			perm, reason := guard.Check(path, op)
			require(t, perm, reason, Denied, ReasonIgnored)
		}
	}
}

// TestCheckOutsideWorkdir 覆盖第 3 行：工作目录外读/写要问、执行拒绝。
func TestCheckOutsideWorkdir(t *testing.T) {
	workDir := newWorkDir(t, "")
	outside := t.TempDir()
	target := filepath.Join(outside, "other.txt")
	if guardBlockedByHome(outside) {
		t.Skipf("临时目录落在受保护位置（%s），这条规则无法验证", outside)
	}

	guard := newGuard(t, workDir)
	perm, reason := guard.Check(target, OpRead)
	require(t, perm, reason, Pending, ReasonOutside)

	perm, reason = guard.Check(target, OpWrite)
	require(t, perm, reason, Pending, ReasonOutside)

	perm, reason = guard.Check(target, OpExec)
	require(t, perm, reason, Denied, ReasonOutside)
}

// TestCheckSystemPath 覆盖第 4 行：系统路径全拒绝。
func TestCheckSystemPath(t *testing.T) {
	blocked := defaultBlockedPaths()
	if len(blocked) == 0 {
		t.Skip("当前平台没有系统黑名单")
	}

	guard := newGuard(t, newWorkDir(t, ""))
	target := filepath.Join(blocked[0], "sycode-should-never-touch")

	for _, op := range []Op{OpRead, OpWrite, OpExec} {
		perm, reason := guard.Check(target, op)
		require(t, perm, reason, Denied, ReasonSystem)
	}
}

// TestCheckArtifactDir 覆盖第 5 行：工具输出目录可读、不可写不可执行。
func TestCheckArtifactDir(t *testing.T) {
	artifact, err := BashOutputDir()
	if err != nil {
		t.Skipf("取不到工具输出目录：%v", err)
	}
	guard := newGuard(t, newWorkDir(t, ""))
	target := filepath.Join(artifact, "2026-10-29", "out.txt")

	perm, reason := guard.Check(target, OpRead)
	require(t, perm, reason, Granted, ReasonArtifact)

	perm, reason = guard.Check(target, OpWrite)
	require(t, perm, reason, Denied, ReasonArtifact)

	perm, reason = guard.Check(target, OpExec)
	require(t, perm, reason, Denied, ReasonArtifact)

	// IsBlocked 不该把输出目录算成禁区，否则日志里会出现自相矛盾的记录。
	if guard.IsBlocked(target) {
		t.Error("工具输出目录不应被 IsBlocked 判为禁区")
	}
}

// TestCheckSymlinkEscape 覆盖第 6 行：符号链接按真实路径判定。
func TestCheckSymlinkEscape(t *testing.T) {
	workDir := newWorkDir(t, "")
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	writeFile(t, secret, "不该被读到")
	if guardBlockedByHome(outside) {
		t.Skipf("临时目录落在受保护位置（%s）", outside)
	}

	link := filepath.Join(workDir, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("当前系统不允许建符号链接（%v），跳过穿越用例", err)
	}

	guard := newGuard(t, workDir)
	perm, reason := guard.Check(filepath.Join("escape", "secret.txt"), OpRead)
	// 关键：不能因为"路径字符串在工作目录内"就放行。
	require(t, perm, reason, Pending, ReasonOutside)

	perm, reason = guard.Check("escape/secret.txt", OpExec)
	require(t, perm, reason, Denied, ReasonOutside)
}

// TestCheckSymlinkToSystemPath 符号链接指向系统目录时，要按系统路径拒绝而不是"问一下"。
func TestCheckSymlinkToSystemPath(t *testing.T) {
	blocked := defaultBlockedPaths()
	if len(blocked) == 0 {
		t.Skip("当前平台没有系统黑名单")
	}

	workDir := newWorkDir(t, "")
	link := filepath.Join(workDir, "syslink")
	if err := os.Symlink(blocked[0], link); err != nil {
		t.Skipf("当前系统不允许建符号链接（%v）", err)
	}

	guard := newGuard(t, workDir)
	perm, reason := guard.Check("syslink/passwd", OpRead)
	require(t, perm, reason, Denied, ReasonSystem)
}

// TestCheckNewFileUnderSymlinkedParent 新建文件的目标不存在，但父目录是符号链接：
// 仍然要解析出真实位置，否则 write_file 会写到工作目录外而不问。
func TestCheckNewFileUnderSymlinkedParent(t *testing.T) {
	workDir := newWorkDir(t, "")
	outside := t.TempDir()
	if guardBlockedByHome(outside) {
		t.Skipf("临时目录落在受保护位置（%s）", outside)
	}

	link := filepath.Join(workDir, "outdir")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("当前系统不允许建符号链接（%v）", err)
	}

	guard := newGuard(t, workDir)
	perm, reason := guard.Check(filepath.Join("outdir", "新建.txt"), OpWrite)
	require(t, perm, reason, Pending, ReasonOutside)
}

// TestCheckVCSEntry .git 里的远端地址可能带 token，默认拒绝。
func TestCheckVCSEntry(t *testing.T) {
	workDir := newWorkDir(t, "")
	writeFile(t, filepath.Join(workDir, ".git", "config"), "[remote]\n")

	guard := newGuard(t, workDir)
	perm, reason := guard.Check(filepath.Join(".git", "config"), OpRead)
	require(t, perm, reason, Denied, ReasonVCS)
}

// TestCheckSensitiveHomeEntry 家目录隐藏项一律拒绝（凭证集中地）。
func TestCheckSensitiveHomeEntry(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("取不到家目录：%v", err)
	}
	guard := newGuard(t, newWorkDir(t, ""))
	if guard.IsBlocked(home) {
		t.Skip("家目录本身在受保护位置内，这条规则无法单独验证")
	}

	perm, reason := guard.Check(filepath.Join("~", ".ssh", "id_rsa"), OpRead)
	require(t, perm, reason, Denied, ReasonSensitive)

	// ~/.sycode 下的配置与日志同样不给读：日志里可能有用户粘贴过的密钥。
	perm, reason = guard.Check(filepath.Join("~", ".sycode", "configs.json"), OpRead)
	require(t, perm, reason, Denied, ReasonSensitive)
}

// TestCheckTildeOutsideWorkdir 家目录下非隐藏路径仍按"工作目录外"处理：问一下即可。
func TestCheckTildeOutsideWorkdir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("取不到家目录：%v", err)
	}
	guard := newGuard(t, newWorkDir(t, ""))
	if guard.IsBlocked(home) {
		t.Skip("家目录本身在受保护位置内")
	}

	perm, reason := guard.Check(filepath.Join("~", "notes.txt"), OpRead)
	require(t, perm, reason, Pending, ReasonOutside)
}

// TestCheckPrefixTrap 工作目录名是另一个目录的前缀时，不能误判成"在里面"。
func TestCheckPrefixTrap(t *testing.T) {
	base := t.TempDir()
	workDir := filepath.Join(base, "work")
	sibling := filepath.Join(base, "work2")
	for _, dir := range []string{workDir, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("建目录失败：%v", err)
		}
	}
	if guardBlockedByHome(sibling) {
		t.Skipf("临时目录落在受保护位置（%s）", sibling)
	}

	guard := newGuard(t, workDir)
	perm, reason := guard.Check(filepath.Join(sibling, "x.txt"), OpRead)
	// 字符串前缀匹配会在这里放行；必须是"工作目录外"。
	require(t, perm, reason, Pending, ReasonOutside)
}

func TestCheckUnknownOp(t *testing.T) {
	guard := newGuard(t, newWorkDir(t, ""))
	perm, reason := guard.Check("main.go", Op("delete"))
	require(t, perm, reason, Denied, ReasonBadOp)
}

func TestCheckEmptyPath(t *testing.T) {
	guard := newGuard(t, newWorkDir(t, ""))
	perm, reason := guard.Check("   ", OpRead)
	require(t, perm, reason, Denied, ReasonBadPath)
}

func TestCheckWithoutGit(t *testing.T) {
	// 没有 .gitignore 解析器时仍要能判定：忽略规则只是附加条件。
	guard := NewGuard(newWorkDir(t, ""), nil)
	perm, reason := guard.Check("main.go", OpRead)
	require(t, perm, reason, Granted, ReasonOK)
}

func TestNewGuardWithoutWorkDir(t *testing.T) {
	// 工作目录取不到：所有路径都落到"工作目录外"，多问一次也不放行。
	guard := NewGuard("", nil)
	perm, reason := guard.Check("main.go", OpRead)
	require(t, perm, reason, Pending, ReasonOutside)
}

func TestResolveJoinsWorkDir(t *testing.T) {
	workDir := newWorkDir(t, "")
	guard := NewGuard(workDir, nil)

	got, err := guard.Resolve("internal/../main.go")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	// 期望值也要走同一次解析：Resolve 的出口是解析后的路径，
	// 而 macOS 的临时目录 /var/... 会变成 /private/var/...。
	want := filepath.Join(resolveSymlinks(workDir), "main.go")
	if normalizeCase(got) != normalizeCase(want) {
		t.Errorf("相对路径应基于工作目录：期望 %q，实际 %q", want, got)
	}
}

func TestResolveExpandsHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("取不到家目录")
	}
	guard := NewGuard(t.TempDir(), nil)

	got, err := guard.Resolve("~/a.txt")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !strings.HasPrefix(normalizeCase(got), normalizeCase(home)) {
		t.Errorf("~ 应展开到家目录：%q 不在 %q 下", got, home)
	}
}

func TestIsUnder(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		name   string
		parent string
		child  string
		want   bool
	}{
		{"相等", "/a/b", "/a/b", true},
		{"子目录", "/a/b", "/a/b/c", true},
		{"父目录", "/a/b", "/a", false},
		{"同名前缀不是子目录", "/a/b", "/a/bc", false},
		{"空父目录", "", "/a", false},
		{"空子路径", "/a", "", false},
		{"带分隔符的兄弟目录", "/a/b", "/a/b" + sep + ".." + sep + "d", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isUnder(c.parent, c.child); got != c.want {
				t.Errorf("期望 %v，实际 %v", c.want, got)
			}
		})
	}
}

func TestIsUnderIsCaseInsensitiveOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("只在 Windows 上验证大小写不敏感")
	}
	if !isUnder(`C:\Work`, `c:\work\main.go`) {
		t.Error("Windows 路径不分大小写，应判为子路径")
	}
}

func TestPermissionString(t *testing.T) {
	for perm, want := range map[Permission]string{Denied: "拒绝", Pending: "需要确认", Granted: "允许"} {
		if got := perm.String(); got != want {
			t.Errorf("%d 的文案应为 %q，实际 %q", int(perm), want, got)
		}
	}
}

// guardBlockedByHome 判断某个临时目录是否会被"家目录隐藏项"或系统路径规则拦住——
// 那种情况下"工作目录外→Pending"的用例无法成立，只能跳过。
func guardBlockedByHome(dir string) bool {
	guard := NewGuard(dir, nil)
	return guard.IsBlocked(absClean(dir))
}

// stubSymlinks 把符号链接解析替换成"假映射"，用完自动还原。
//
// 真符号链接在 Windows 默认建不了（需要特权），但穿越判定是最该压住的攻击面，
// 所以这里替换解析器来模拟：full 路径解析失败（目标不存在）、父目录解析到工作目录外。
func stubSymlinks(t *testing.T, mapping map[string]string) {
	t.Helper()
	original := evalSymlinks
	evalSymlinks = func(path string) (string, error) {
		if resolved, ok := mapping[normalizeCase(path)]; ok {
			return resolved, nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { evalSymlinks = original })
}

func TestCheckSymlinkTraversalViaStub(t *testing.T) {
	workDir := newWorkDir(t, "")
	outside := t.TempDir()
	if guardBlockedByHome(outside) {
		t.Skipf("临时目录落在受保护位置（%s）", outside)
	}

	link := filepath.Join(workDir, "escape")
	stubSymlinks(t, map[string]string{normalizeCase(link): outside})

	guard := NewGuard(workDir, nil)

	// 目标文件不存在：解析器只能解析到父目录（那个符号链接），剩余段接回去。
	perm, reason := guard.Check(filepath.Join("escape", "secret.txt"), OpRead)
	require(t, perm, reason, Pending, ReasonOutside)

	// 写一个还不存在的新文件，同样要落到"工作目录外"。
	perm, reason = guard.Check(filepath.Join("escape", "新建.txt"), OpWrite)
	require(t, perm, reason, Pending, ReasonOutside)

	perm, reason = guard.Check(filepath.Join("escape", "x"), OpExec)
	require(t, perm, reason, Denied, ReasonOutside)
}

func TestCheckSymlinkToBlockedPathViaStub(t *testing.T) {
	blocked := defaultBlockedPaths()
	if len(blocked) == 0 {
		t.Skip("当前平台没有系统黑名单")
	}

	workDir := newWorkDir(t, "")
	link := filepath.Join(workDir, "syslink")
	stubSymlinks(t, map[string]string{normalizeCase(link): blocked[0]})

	guard := NewGuard(workDir, nil)
	perm, reason := guard.Check(filepath.Join("syslink", "passwd"), OpRead)
	require(t, perm, reason, Denied, ReasonSystem)
}

func TestResolveSymlinkKeepsMissingTail(t *testing.T) {
	workDir := newWorkDir(t, "")
	outside := t.TempDir()
	link := filepath.Join(workDir, "escape")
	stubSymlinks(t, map[string]string{normalizeCase(link): outside})

	guard := NewGuard(workDir, nil)
	got, err := guard.Resolve(filepath.Join("escape", "deep", "nested.txt"))
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	want := filepath.Join(outside, "deep", "nested.txt")
	if normalizeCase(got) != normalizeCase(want) {
		t.Errorf("应把未存在部分接在解析后的父目录上：期望 %q，实际 %q", want, got)
	}
}

// TestNewGuardResolvesWorkDir 复现 CI 上的失败：工作目录一边解析、比较另一边不解析。
//
// 用替换 evalSymlinks 的方式模拟 macOS（`/var` → `/private/var`）与 Windows（8.3 短名）：
// 这两种平台上"传进来的工作目录"与"Resolve 的出口"形态不同，修复前会被判成"工作目录外"。
func TestNewGuardResolvesWorkDir(t *testing.T) {
	base := t.TempDir()
	old := evalSymlinks
	evalSymlinks = func(path string) (string, error) {
		if strings.HasPrefix(path, base) {
			return "/private" + path, nil
		}
		return old(path)
	}
	defer func() { evalSymlinks = old }()

	guard := NewGuard(filepath.Join(base, "work"), nil)

	_, reason := guard.Check(filepath.Join(base, "work", "a.txt"), OpWrite)
	if reason.Code != ReasonInWorkdir {
		t.Errorf("工作目录内的写应判为 %q，实际 %q（%s）——工作目录没有和 Resolve 走同一次解析",
			ReasonInWorkdir, reason.Code, reason.Message)
	}
}

// TestNewGuardResolvesSymlinkedWorkDir 用真符号链接再验一次。
//
// Windows 默认不允许普通用户建链接（需要特权），那种情况跳过；CI 的 macOS/Linux 会真跑，
// 而这正是当初把"工作目录在链接之下"漏掉的地方。
func TestNewGuardResolvesSymlinkedWorkDir(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("当前平台不允许建符号链接（%v）", err)
	}

	// 从链接进去启动：工作目录是 link，但所有文件路径都会解析成 real 之下。
	guard := NewGuard(link, nil)

	_, reason := guard.Check(filepath.Join(real, "a.txt"), OpWrite)
	if reason.Code != ReasonInWorkdir {
		t.Errorf("链接之下的工作目录应视为目录内，实际 %q（%s）", reason.Code, reason.Message)
	}
}

// TestGuardRulesSurviveSymlinkedRoot 用真符号链接验证"三处解析必须一致"。
//
// 场景等价于 macOS 的 /var → /private/var：工作目录本身是链接，而所有文件路径
// 解析后都落在链接目标之下。三件事必须同时成立，否则用户会觉得"目录里的文件全要授权"：
//  1. .gitignore 仍然生效（根与路径同形态）
//  2. 工作目录内仍是"目录内"
//  3. 系统路径仍被拒（黑名单条目也要解析）
//
// Windows 默认不允许普通用户建链接，跳过；CI 的 macOS 与 Linux 会真跑。
func TestGuardRulesSurviveSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("当前平台不允许建符号链接（%v）", err)
	}
	if err := os.WriteFile(filepath.Join(real, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatalf("写 .gitignore 失败：%v", err)
	}

	git, err := NewGitAwareness(link)
	if err != nil {
		t.Fatalf("构造 GitAwareness 失败：%v", err)
	}
	guard := NewGuard(link, git)

	if perm, reason := guard.Check(filepath.Join(link, "app.log"), OpRead); perm != Denied || reason.Code != ReasonIgnored {
		t.Errorf("被忽略的文件应拒绝（ignored），实际 %v / %q（%s）", perm, reason.Code, reason.Message)
	}
	if _, reason := guard.Check(filepath.Join(link, "main.go"), OpWrite); reason.Code != ReasonInWorkdir {
		t.Errorf("工作目录内的写应判为 %q，实际 %q（%s）", ReasonInWorkdir, reason.Code, reason.Message)
	}
}

// TestGuardRulesSurviveResolvedRoot 与上一个测试等价，但不依赖建链接的能力。
//
// 做法是把"链接"映射到一个真实存在的目录：解析后的路径能读到文件，walk 也正常，
// 因此在 Windows 上同样能跑（真链接那条在那里只能跳过）。
func TestGuardRulesSurviveResolvedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	link := filepath.Join(base, "link")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(real, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatalf("写 .gitignore 失败：%v", err)
	}
	if err := os.MkdirAll(link, 0o755); err != nil {
		t.Fatalf("建占位目录失败：%v", err)
	}

	old := evalSymlinks
	evalSymlinks = func(path string) (string, error) {
		if path == link || strings.HasPrefix(path, link+string(filepath.Separator)) {
			return real + path[len(link):], nil
		}
		return old(path)
	}
	defer func() { evalSymlinks = old }()

	git, err := NewGitAwareness(link)
	if err != nil {
		t.Fatalf("构造 GitAwareness 失败：%v", err)
	}
	guard := NewGuard(link, git)

	if perm, reason := guard.Check(filepath.Join(link, "app.log"), OpRead); perm != Denied || reason.Code != ReasonIgnored {
		t.Errorf("被忽略的文件应拒绝（ignored），实际 %v / %q（%s）", perm, reason.Code, reason.Message)
	}
	if _, reason := guard.Check(filepath.Join(link, "main.go"), OpWrite); reason.Code != ReasonInWorkdir {
		t.Errorf("工作目录内的写应判为 %q，实际 %q（%s）", ReasonInWorkdir, reason.Code, reason.Message)
	}

}
