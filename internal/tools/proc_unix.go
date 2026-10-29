//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

// prepareCommand 让命令自成一个进程组，便于整组杀掉。
func prepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killTree 向整个进程组发 SIGKILL：负号 PID 表示"这个组里的所有进程"。
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
