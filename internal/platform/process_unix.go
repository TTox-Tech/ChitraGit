//go:build !windows

package platform

import (
	"errors"
	"os/exec"
	"syscall"
)

// ProcessAlive reports whether a process with the given PID exists.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	// EPERM means the process exists but belongs to someone else.
	return err == nil || errors.Is(err, syscall.EPERM)
}

// KillTreeOnCancel makes cmd run in its own process group and, when its
// context is cancelled, kills the whole group. Without this, helpers that
// git spawns (ssh, git-remote-https) would outlive a timed-out git.
func KillTreeOnCancel(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
