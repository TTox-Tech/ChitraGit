//go:build windows

package platform

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

const stillActive = 259 // STILL_ACTIVE exit code

// ProcessAlive reports whether a process with the given PID exists.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied still means the process exists.
		return err == windows.ERROR_ACCESS_DENIED
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

// KillTreeOnCancel keeps Go's default cancellation on Windows.
// shortcut: only git itself is killed, not helpers it started; use a job
// object when long-running fetches land (M1).
func KillTreeOnCancel(cmd *exec.Cmd) {}
