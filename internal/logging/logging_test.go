package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setHome 把用户目录指到临时路径，避免污染真实的 ~/.sycode。
// 同时设置 HOME 与 USERPROFILE：前者给 Unix，后者给 Windows 的 os.UserHomeDir。
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestParseLevel(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want slog.Level
	}{
		{"debug", "debug", slog.LevelDebug},
		{"大写 DEBUG", "DEBUG", slog.LevelDebug},
		{"前后空格", "  debug  ", slog.LevelDebug},
		{"info", "info", slog.LevelInfo},
		{"warn", "warn", slog.LevelWarn},
		{"warning 别名", "warning", slog.LevelWarn},
		{"error", "error", slog.LevelError},
		{"空值回退 info", "", slog.LevelInfo},
		{"乱填回退 info", "verbose", slog.LevelInfo},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseLevel(c.in); got != c.want {
				t.Fatalf("级别解析不符：输入 %q，期望 %v，实际 %v", c.in, c.want, got)
			}
		})
	}
}

func TestInitWritesJSONL(t *testing.T) {
	home := setHome(t)
	t.Setenv(levelEnv, "debug")

	cleanup, path, err := Init()
	if err != nil {
		t.Fatalf("初始化日志失败：%v", err)
	}

	if !strings.HasPrefix(path, filepath.Join(home, ".sycode", "logs")) {
		t.Errorf("日志路径不在临时家目录下：%q", path)
	}

	slog.Debug("调试信息", "k", "v")
	slog.Info("普通信息")
	cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败：%v", err)
	}
	got := string(raw)

	for _, want := range []string{`"level":"DEBUG"`, `"level":"INFO"`, `调试信息`, `"k":"v"`} {
		if !strings.Contains(got, want) {
			t.Errorf("日志内容缺少 %s，实际内容：\n%s", want, got)
		}
	}
}

func TestInitRespectsLevel(t *testing.T) {
	setHome(t)
	t.Setenv(levelEnv, "warn")

	cleanup, path, err := Init()
	if err != nil {
		t.Fatalf("初始化日志失败：%v", err)
	}
	slog.Debug("不应出现")
	slog.Warn("应该出现")
	cleanup()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志文件失败：%v", err)
	}
	got := string(raw)

	if strings.Contains(got, "不应出现") {
		t.Errorf("warn 级别下不应写入 debug 日志，实际内容：\n%s", got)
	}
	if !strings.Contains(got, "应该出现") {
		t.Errorf("warn 级别下应写入 warn 日志，实际内容：\n%s", got)
	}
}

func TestInitLogDirUnwritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上目录权限位不生效，无法构造不可写场景")
	}
	if os.Geteuid() == 0 {
		t.Skip("以 root 运行时权限位不生效")
	}

	home := t.TempDir()
	if err := os.Chmod(home, 0o500); err != nil {
		t.Fatalf("设置只读目录失败：%v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o700) })
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	if _, _, err := Init(); err == nil {
		t.Fatal("目录不可写时应返回错误，实际返回 nil")
	} else if !strings.Contains(err.Error(), "创建日志目录") {
		t.Errorf("错误信息应说明是创建目录失败，实际：%v", err)
	}
}

func TestInitCleanupTwice(t *testing.T) {
	setHome(t)

	cleanup1, path1, err := Init()
	if err != nil {
		t.Fatalf("第一次初始化失败：%v", err)
	}
	// 两次 Init 可能落在同一秒，文件名相同并追加写，因此只断言"不 panic 且路径合法"。
	cleanup2, path2, err := Init()
	if err != nil {
		t.Fatalf("第二次初始化失败：%v", err)
	}

	cleanup1()
	cleanup2()

	if path1 == "" || path2 == "" {
		t.Fatalf("路径不应为空：%q / %q", path1, path2)
	}
}
