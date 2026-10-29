// Package cleanup 回收过期的工具输出落盘文件。
//
// 只做一件事：把 ~/.sycode 下"按时间就该消失"的普通文件删掉。
// 不碰会话与历史（那是 P4 才有格式的东西），也不做后台定时器——
// v0.1 只要求"下次启动时清一次"，定时回收是另一套复杂度。
//
// 失败必须只记日志：回收是尽力而为，不能让启动失败。
package cleanup

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/sy665/sycode/internal/filesystem"
)

// keepDays 是工具输出的保留期。
//
// 7 天够用户回头翻一次"刚才那条命令输出到底说了什么"；
// 再长下去，落盘目录会积累成没人看也删不掉的垃圾。
const keepDays = 7

// Run 回收过期的工具输出。调用方只需决定"什么时候调"，不必关心细节。
func Run() error {
	return runAt(time.Now())
}

// runAt 是 Run 的可注入时钟版本：保留期判定必须能在测试里控制时间。
func runAt(now time.Time) error {
	dir, err := filesystem.BashOutputDir()
	if err != nil {
		// 拿不到家目录：交给调用方记一行日志即可，不要在这里 panic。
		return err
	}

	removed, pruneErr := pruneExpired(dir, now.Add(-keepDays*24*time.Hour))
	if removed > 0 {
		slog.Info("已清理过期的工具输出", "dir", dir, "count", removed)
	}
	if pruneErr != nil {
		return fmt.Errorf("执行失败：清理 %q 时出错：%w", dir, pruneErr)
	}
	return nil
}

// pruneExpired 删掉 dir 下修改时间不晚于 cutoff 的普通文件，返回删除数量。
//
// 只处理平铺的普通文件：不递归（artifact 目录是平的），也不跟符号链接——
// 链接指向哪里不该由回收逻辑替用户决定（`entry.Info()` 本身就是 Lstat）。
// 目录不存在视为无事可做：第一次运行时它可能还没被创建过。
//
// 单个文件删不掉不中断整轮：其余仍该清掉，错误最后合并返回。
func pruneExpired(dir string, cutoff time.Time) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, err
	}

	removed := 0
	var failures []error
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !info.Mode().IsRegular() || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			failures = append(failures, err)
			continue
		}
		removed++
	}
	return removed, errors.Join(failures...)
}
