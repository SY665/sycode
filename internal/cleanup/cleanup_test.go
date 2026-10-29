package cleanup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeFileWithAge 造一个 mtime 是 age 之前的文件。
//
// 必须用 os.Chtimes 改时间：文件系统上新建的文件永远是"现在"，
// 而"过期"这件事只能靠时间证明。
func writeFileWithAge(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("输出"), 0o600); err != nil {
		t.Fatalf("建测试文件失败：%v", err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatalf("改文件时间失败：%v", err)
	}
	return path
}

func TestPruneExpiredRemovesOnlyOldFiles(t *testing.T) {
	dir := t.TempDir()
	old := writeFileWithAge(t, dir, "old.txt", 8*24*time.Hour)
	fresh := writeFileWithAge(t, dir, "fresh.txt", time.Hour)

	removed, err := pruneExpired(dir, time.Now().Add(-keepDays*24*time.Hour))
	if err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	if removed != 1 {
		t.Fatalf("应删 1 个，实际 %d", removed)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("过期文件应被删除，实际仍在（err=%v）", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("未过期文件不该被删：%v", err)
	}
}

func TestPruneExpiredIncludesBoundary(t *testing.T) {
	dir := t.TempDir()
	path := writeFileWithAge(t, dir, "boundary.txt", keepDays*24*time.Hour)

	// 直接把文件的 mtime 当保留线：两条 time.Now() 之间会有微秒差，
	// 用推算出来的 cutoff 测不到"恰好等于"这个边界。
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读文件信息失败：%v", err)
	}

	removed, pruneErr := pruneExpired(dir, info.ModTime())
	if pruneErr != nil {
		t.Fatalf("不该出错：%v", pruneErr)
	}
	if removed != 1 {
		t.Errorf("边界文件应被删除（保留期是「不晚于即清」），实际删除 %d 个", removed)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("边界文件应已不存在（err=%v）", err)
	}
}

func TestPruneExpiredMissingDirIsNoop(t *testing.T) {
	removed, err := pruneExpired(filepath.Join(t.TempDir(), "不存在"), time.Now())
	if err != nil {
		t.Fatalf("目录不存在不该报错：%v", err)
	}
	if removed != 0 {
		t.Errorf("目录不存在时不该删任何东西，实际 %d", removed)
	}
}

func TestPruneExpiredIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "子目录")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatalf("建子目录失败：%v", err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(sub, old, old); err != nil {
		t.Fatalf("改目录时间失败：%v", err)
	}
	// 子目录里的文件也不该被动：不递归是刻意的，避免误删别人的树。
	inner := writeFileWithAge(t, sub, "inner.txt", 30*24*time.Hour)

	removed, err := pruneExpired(dir, time.Now())
	if err != nil {
		t.Fatalf("不该出错：%v", err)
	}
	if removed != 0 {
		t.Errorf("不该删目录，实际删除 %d 个", removed)
	}
	if _, err := os.Stat(inner); err != nil {
		t.Errorf("子目录里的文件不该被碰：%v", err)
	}
}

func TestRunAtUsesBashOutputDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	dir := filepath.Join(home, ".sycode", "bash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("建 artifact 目录失败：%v", err)
	}
	old := writeFileWithAge(t, dir, "cmd-1.txt", 9*24*time.Hour)
	fresh := writeFileWithAge(t, dir, "cmd-2.txt", time.Minute)

	if err := runAt(time.Now()); err != nil {
		t.Fatalf("回收不该失败：%v", err)
	}

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("9 天前的输出应被清掉（err=%v）", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("刚生成的输出不该被清：%v", err)
	}
}
