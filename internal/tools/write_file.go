package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sy665/sycode/internal/filesystem"
)

// WriteFileToolName 是工具名。
const WriteFileToolName = "write_file"

// tempFilePattern 是原子写用的临时文件名前缀。
//
// 用点开头 + 固定前缀，既方便排查（出问题时一眼看出是谁留下的），
// 又能在 GlobTool 的隐藏文件遍历里自然被忽略。
const tempFilePattern = ".sycode-*.tmp"

// WriteFileTool 整文件写入（覆盖）。
type WriteFileTool struct {
	guard *filesystem.Guard
	perm  PermissionRequester
}

// NewWriteFileTool 注入依赖。
func NewWriteFileTool(guard *filesystem.Guard, perm PermissionRequester) *WriteFileTool {
	return &WriteFileTool{guard: guard, perm: perm}
}

func (t *WriteFileTool) Name() string { return WriteFileToolName }

func (t *WriteFileTool) Description() string {
	return "把内容整体写入一个文件（覆盖原内容，不存在则新建，父目录会自动创建）。" +
		"改动已有的少量内容请用 edit_file：write_file 会整文件替换，容易把别处的手改覆盖掉。" +
		"写入是原子的（先写临时文件再替换），不会留下半截文件。"
}

func (t *WriteFileTool) InputSchema() map[string]any {
	return map[string]any{
		"path": map[string]any{
			"type":        "string",
			"description": "目标文件路径，相对工作目录或绝对路径",
		},
		"content": map[string]any{
			"type":        "string",
			"description": "要写入的完整内容（可以包含换行；空串表示清空文件）",
		},
	}
}

func (t *WriteFileTool) ValidateInput(_ context.Context, in Input) error {
	if _, err := requireString(in, "path"); err != nil {
		return err
	}
	_, err := requireText(in, "content")
	return err
}

// Execute 先问权限再落盘。
//
// 顺序不能反：被拒绝时文件必须**一个字节都没动**，否则"拒绝"就是句空话。
func (t *WriteFileTool) Execute(ctx context.Context, in Input) (Result, error) {
	if err := t.ValidateInput(ctx, in); err != nil {
		return Result{}, err
	}
	path, _ := requireString(in, "path")
	content, _ := requireText(in, "content")

	if err := askAccess(ctx, access{
		guard:  t.guard,
		perm:   t.perm,
		tool:   WriteFileToolName,
		path:   path,
		op:     filesystem.OpWrite,
		detail: "写入文件内容（覆盖原有内容）",
	}); err != nil {
		return Result{}, err
	}

	resolved, err := t.guard.Resolve(path)
	if err != nil {
		return Result{}, fmt.Errorf("参数错误：%w", err)
	}

	oldContent, existed, err := readExisting(resolved)
	if err != nil {
		return Result{}, err
	}
	if existed && oldContent == content {
		// 内容没变就别碰文件：改一次 mtime 会让 watch/构建重新跑一遍。
		return Result{
			Content: fmt.Sprintf("文件 %q 的内容没有变化，未写入。", path),
			Display: &Display{Kind: DisplayDiff, Title: path + "（无变化）", Lines: []string{"（内容没有变化）"}},
		}, nil
	}

	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return Result{}, fmt.Errorf("执行失败：无法创建父目录：%w", err)
	}
	if err := writeAndVerify(resolved, content, path); err != nil {
		return Result{}, err
	}

	diff := buildDiff(oldContent, content)
	return Result{
		Content: renderWriteSummary(path, existed, content, diff),
		Display: &Display{
			Kind:  DisplayDiff,
			Title: fmt.Sprintf("%s（%s）", path, diffSummary(diff)),
			Lines: formatDiff(diff),
		},
	}, nil
}

// readExisting 读取目标文件现状；不存在返回 existed=false。
func readExisting(path string) (content string, existed bool, err error) {
	info, statErr := os.Stat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		return "", false, nil
	}
	if statErr != nil {
		return "", false, fmt.Errorf("执行失败：无法访问 %q：%w", path, statErr)
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("参数错误：%q 是目录，不能当作文件写入。请给出文件路径", path)
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return "", false, fmt.Errorf("执行失败：读取 %q 失败：%w", path, readErr)
	}
	if isBinary(data) {
		// 覆盖二进制文件几乎一定是误操作，且回读校验对文本没有意义。
		return "", false, fmt.Errorf("不支持：%q 看起来是二进制文件，拒绝覆盖。如确需替换请先删除它", path)
	}
	return string(data), true, nil
}

// writeAndVerify 原子写入并回读校验。displayPath 是报错时给人看的路径（可能是相对路径）。
//
// 回读不是多余动作：磁盘满、被别的进程截断这类问题不会从 Write 返回，
// 用户却会拿到一个"写着成功、其实是坏的"文件。
func writeAndVerify(path, content, displayPath string) error {
	if err := writeFileAtomic(path, []byte(content)); err != nil {
		return err
	}
	written, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("执行失败：写入 %q 后回读失败：%w", displayPath, err)
	}
	if !bytes.Equal(written, []byte(content)) {
		return fmt.Errorf("执行失败：写入 %q 后回读校验不一致，文件可能不完整。请重试或检查磁盘空间", displayPath)
	}
	return nil
}

// writeFileAtomic 原子写：临时文件 + rename。
//
// 直接 os.WriteFile 在写到一半失败（磁盘满、进程被杀）时会留下半截源码，
// 而半截源码比"没改"危险得多——它还能编译通过一部分。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	// 临时文件必须与目标同目录：跨文件系统的 rename 不是原子操作。
	// 用剩余权限极可能保持原样；新建文件用 0644（与常见 umask 下的文本文件一致）。
	temp, err := os.CreateTemp(dir, tempFilePattern)
	if err != nil {
		return fmt.Errorf("执行失败：无法在 %q 创建临时文件：%w", dir, err)
	}
	tempName := temp.Name()
	// 失败路径要清掉临时文件，否则会在用户目录里堆垃圾。
	cleanup := func() {
		temp.Close()
		os.Remove(tempName)
	}

	if _, err := temp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("执行失败：写入临时文件失败：%w", err)
	}
	// Sync 之后再 rename：否则断电可能得到一个"存在但内容为空"的文件。
	if err := temp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("执行失败：刷盘失败：%w", err)
	}
	if err := temp.Close(); err != nil {
		os.Remove(tempName)
		return fmt.Errorf("执行失败：关闭临时文件失败：%w", err)
	}

	if err := os.Chmod(tempName, 0o644); err != nil {
		os.Remove(tempName)
		return fmt.Errorf("执行失败：设置文件权限失败：%w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		os.Remove(tempName)
		return fmt.Errorf("执行失败：替换目标文件失败：%w", err)
	}
	return nil
}

// renderWriteSummary 给模型看的一句话总结。
func renderWriteSummary(path string, existed bool, content string, diff []EditDiffLine) string {
	action := "新建"
	if existed {
		action = "覆盖"
	}
	return fmt.Sprintf("已%s %q（%d 字节，%d 行；%s）", action, path,
		len(content), len(splitLines(content)), diffSummary(diff))
}
