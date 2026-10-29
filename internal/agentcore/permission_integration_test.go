package agentcore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sy665/sycode/internal/filesystem"
	"github.com/sy665/sycode/internal/tools"
)

// TestWriteToolAsksThenWrites 验的是**接线**，不是通路本身：
// 工具构造时拿到的 perm 必须真的是界面那条通路，否则弹窗永远弹不出来。
// 光测 PermissionPrompt 测不出这一点——之前的 bug 正是工具全都拿到 nil。
func TestWriteToolAsksThenWrites(t *testing.T) {
	cases := []struct {
		name     string
		decision Decision
		wantFile bool
	}{
		{"允许一次后落盘", DecisionAllowOnce, true},
		{"拒绝后磁盘零副作用", DecisionDeny, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "a.txt")

			p := NewPermissionPrompt()
			// 顺序照 main.buildTools：guard 与 perm 一起交给工具。
			tool := tools.NewWriteFileTool(filesystem.NewGuard(dir, nil), p)

			execErr := make(chan error, 1)
			go func() {
				_, err := tool.Execute(context.Background(),
					tools.Input{"path": target, "content": "hello"})
				execErr <- err
			}()

			select {
			case req := <-p.Requests():
				if req.Tool != tools.WriteFileToolName || req.Op != string(filesystem.OpWrite) {
					t.Errorf("询问内容不符：%+v", req)
				}
				if req.Path != target {
					t.Errorf("询问路径应是被写的文件，实际 %q", req.Path)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("超时：工具没有发起权限询问（perm 可能没接上）")
			}

			p.Answer(tc.decision)

			select {
			case err := <-execErr:
				if tc.wantFile && err != nil {
					t.Fatalf("允许后不该失败：%v", err)
				}
				if !tc.wantFile && err == nil {
					t.Fatal("拒绝后应返回错误，好让模型知道这是决定而不是故障")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("超时：工具没有返回")
			}

			content, err := os.ReadFile(target)
			if tc.wantFile {
				if err != nil {
					t.Fatalf("允许后文件应存在：%v", err)
				}
				if string(content) != "hello" {
					t.Errorf("写入内容不符：%q", content)
				}
				return
			}
			if !os.IsNotExist(err) {
				t.Errorf("拒绝后不该碰磁盘，实际读到 %q（err=%v）", content, err)
			}
		})
	}
}
