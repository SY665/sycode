// Package logging 统一管理 SyCode 的日志出口。
//
// 本包只负责：定位日志目录、创建日志文件、装配 slog handler、解析日志级别。
// 不负责：内容脱敏（P3 安全红线统一处理）、日志回收（internal/cleanup，P3）、
// 终端渲染（TUI 接管屏幕后写 stderr 会破坏渲染，故 v0.1 只写文件）。
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// 目录 0700 / 文件 0600：日志会带出工作目录、会话 ID 等线索，不给同机其他用户读。
	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600

	// 本地时间而非 UTC：排查时更方便把日志时间与"我几点操作的"对上；
	// 文件名已含日期，跨时区混淆的风险可以接受。
	timeFormat = "20060102-150405"

	levelEnv = "SYCODE_LOG_LEVEL"
)

// Init 创建日志文件并把文件 handler 设为全局默认 logger。
//
// 返回的 cleanup 只关闭文件句柄，不删除文件——历史日志由 internal/cleanup
// 按保留期回收，这里删会让「启动即清理」变成「退出即清理」，与设计不符。
//
// 不保证幂等：重复调用会新建文件并替换默认 logger，旧句柄需由前一次的 cleanup 关闭。
func Init() (cleanup func(), path string, err error) {
	level := parseLevel(os.Getenv(levelEnv))

	dir, err := logDir()
	if err != nil {
		return nil, "", err
	}

	file, path, err := createFile(dir)
	if err != nil {
		return nil, "", err
	}

	slog.SetDefault(slog.New(newHandler(file, level)))

	return func() { _ = file.Close() }, path, nil
}

// parseLevel 把 SYCODE_LOG_LEVEL 解析成 slog 级别。
//
// 未知值或空值一律回退 info 且不报错：环境变量拼错不该导致程序打不开，
// 用户排查"为什么没有 debug 日志"时看日志级别即可。
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// logDir 返回并创建日志目录 ~/.sycode/logs。
func logDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("无法确定用户目录：%w", err)
	}

	dir := filepath.Join(home, ".sycode", "logs")
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return "", fmt.Errorf("创建日志目录 %q 失败：%w。请确认该路径可写", dir, err)
	}
	return dir, nil
}

// createFile 在 dir 下按启动时间新建日志文件，返回文件与其路径。
func createFile(dir string) (*os.File, string, error) {
	path := filepath.Join(dir, "sycode-"+time.Now().Format(timeFormat)+".log")

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return nil, "", fmt.Errorf("创建日志文件 %q 失败：%w。请确认磁盘空间与目录权限", path, err)
	}
	return file, path, nil
}

// newHandler 产出文件用的 JSON handler。
//
// 只做 JSON：终端可读格式留给 P4 的 TUI 内面板（在界面里渲染），
// 不写 stderr —— Bubble Tea 接管屏幕后写 stderr 会破坏渲染。
func newHandler(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
}
