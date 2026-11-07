package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A cancelled command must not leave the processes it started running.
func TestKillTreeOnCancel(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command",
		"$p = Start-Process ping -ArgumentList '-n','60','127.0.0.1' -PassThru -NoNewWindow; "+
			"Set-Content -Path '"+pidFile+"' -Value $p.Id; Start-Sleep 60")
	KillTreeOnCancel(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var child int
	for deadline := time.Now().Add(30 * time.Second); child == 0; time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("child never started")
		}
		if b, err := os.ReadFile(pidFile); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
	}
	cancel()
	_ = cmd.Wait()
	for deadline := time.Now().Add(10 * time.Second); ProcessAlive(child); time.Sleep(100 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("child %d still running after cancel", child)
		}
	}
}
