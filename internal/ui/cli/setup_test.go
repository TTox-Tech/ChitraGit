package cli

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/schedule"
	"github.com/TTox-Tech/chitragit/internal/ui/tui"
)

func TestLocalBackendSetup(t *testing.T) {
	isolate(t)
	ctx := context.Background()
	tab, exists := "", false
	a := &app{schedEnv: &schedule.Env{GOOS: "linux", BackupDir: t.TempDir(),
		Run: func(_ context.Context, stdin []byte, _ string, args ...string) ([]byte, []byte, error) {
			if args[0] == "-l" {
				if !exists {
					return nil, []byte("no crontab for tester"), errors.New("exit 1")
				}
				return []byte(tab), nil, nil
			}
			tab, exists = string(stdin), true
			return nil, nil, nil
		}}}
	a.setupOutput()
	b := &localBackend{a: a}
	defer func() {
		if s := b.sess(); s != nil {
			s.close()
		}
	}()

	if b.ArchiveRoot() != "" {
		t.Fatal("no archive yet")
	}
	if repos, err := b.Repos(ctx); err != nil || repos != nil {
		t.Fatalf("Repos before an archive = %v %v", repos, err)
	}

	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", filepath.Join(root, "alice", "tool")).CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	got, err := b.InitArchive(ctx, root)
	if err != nil || got != root || b.ArchiveRoot() != root {
		t.Fatalf("InitArchive = %q %v", got, err)
	}
	sum, err := b.Scan(ctx)
	if err != nil || sum.Found != 1 || sum.Owners != 1 {
		t.Fatalf("Scan = %+v %v", sum, err)
	}

	file, err := b.SaveSetting("sync.workers", "6", false)
	if err != nil || !strings.HasPrefix(file, root) {
		t.Fatalf("SaveSetting = %q %v", file, err)
	}
	cfg, _, err := b.Settings()
	if err != nil || cfg.Sync.Workers != 6 || b.sess().cfg.Sync.Workers != 6 {
		t.Fatalf("Settings after save: %v %v", cfg, err)
	}
	_, err = b.SaveSetting("sync.mode", "yolo", false)
	if err == nil || !strings.Contains(err.Error(), `sync.mode = "yolo"`) || strings.Contains(err.Error(), "\n") {
		t.Fatalf("refusal should name the problem on one line: %v", err)
	}

	info, err := b.SetSchedule(ctx, tui.ScheduleChoice{Frequency: schedule.Daily, Hour: 2})
	// The test binary lives in a temporary build folder, which a schedule must refuse.
	if err == nil || !strings.Contains(err.Error(), "temporary build folder") {
		t.Fatalf("SetSchedule from go test = %+v %v", info, err)
	}
	if info, err := b.Schedule(ctx); err != nil || info.On {
		t.Fatalf("Schedule = %+v %v", info, err)
	}
	if _, err := b.SetSchedule(ctx, tui.ScheduleChoice{}); err != nil {
		t.Fatalf("turning off with nothing scheduled: %v", err)
	}
}
