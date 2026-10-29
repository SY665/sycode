//go:build windows

package tools

import (
	"os/exec"
	"strconv"
)

// prepareCommand 在 Windows 上不需要额外设置：
// 进程组与作业对象的差别在这里没有实际价值（见 killTree 的做法）。
func prepareCommand(*exec.Cmd) {}

// killTree 杀掉整棵进程树。
//
// cmd.Process.Kill() 只杀 shell 本身；`go build`、`npm test` 这类会派生子进程的命令，
// 子进程会变成孤儿继续占用文件与 CPU。taskkill /T 是 Windows 上最省事的整棵树方案。
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// 失败也不慌：至少把 shell 本身杀掉，超时语义仍然成立。
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
