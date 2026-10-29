package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/schedule"
)

// runWith executes the CLI with a prepared app, so tests can fake the
// scheduler and notifications.
func runWith(a *app, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	a.out, a.errOut = &out, &errOut
	code := a.execute(context.Background(), args)
	return code, out.String(), errOut.String()
}

type sentNote struct{ title, body string }

type fakeSender struct{ sent []sentNote }

func (f *fakeSender) Send(_ context.Context, title, body string) error {
	f.sent = append(f.sent, sentNote{title, body})
	return nil
}

func TestScheduleInstallStatusRemove(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, e := run(t, "init", root); code != 0 {
		t.Fatal(e)
	}
	tab, exists := "", false
	env := &schedule.Env{GOOS: "linux", BackupDir: t.TempDir(),
		Run: func(_ context.Context, stdin []byte, _ string, args ...string) ([]byte, []byte, error) {
			if args[0] == "-l" {
				if !exists {
					return nil, []byte("no crontab for tester"), errors.New("exit 1")
				}
				return []byte(tab), nil, nil
			}
			tab, exists = string(stdin), true
			return nil, nil, nil
		}}
	bin := filepath.Join(t.TempDir(), "chitragit")
	newApp := func() *app { return &app{schedEnv: env} }

	code, out, errOut := runWith(newApp(), "--archive", root, "schedule", "status")
	if code != 0 || !strings.Contains(out, "No scheduled sync") {
		t.Fatalf("status before install: %d %q %q", code, out, errOut)
	}

	code, out, errOut = runWith(newApp(), "--archive", root, "schedule", "install", "--weekly", "03:30", "--day", "fri", "--binary", bin)
	if code != 0 || !strings.Contains(out, "weekly on Friday at 03:30 with cron") {
		t.Fatalf("install: %d %q %q", code, out, errOut)
	}
	if !strings.Contains(tab, "30 3 * * 5 ") || !strings.Contains(tab, bin+" sync --headless --archive "+root) {
		t.Fatalf("crontab:\n%s", tab)
	}

	code, out, _ = runWith(newApp(), "--archive", root, "--json", "schedule", "status")
	var st scheduleStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil || code != 0 {
		t.Fatalf("status json: %v %d\n%s", err, code, out)
	}
	if !st.Scheduled || !st.Installed || st.Backend != "cron" || st.NextRun == "" || st.Binary != bin {
		t.Errorf("status = %+v", st)
	}

	// If someone deletes the job by hand, status says so.
	tab = ""
	_, _, errOut = runWith(newApp(), "--archive", root, "schedule", "status")
	if !strings.Contains(errOut, "no longer has the job") {
		t.Errorf("missing job not reported: %q", errOut)
	}

	if code, _, errOut := runWith(newApp(), "--archive", root, "schedule", "remove"); code != 0 {
		t.Fatalf("remove: %d %s", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(root, ".chitragit", "schedule.json")); !os.IsNotExist(err) {
		t.Error("schedule.json should be gone")
	}
}

func TestScheduleInstallUsage(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, e := run(t, "init", root); code != 0 {
		t.Fatal(e)
	}
	for _, args := range [][]string{
		{"schedule", "install"},
		{"schedule", "install", "--daily", "02:00", "--weekly", "02:00"},
		{"schedule", "install", "--daily", "25:00"},
		{"schedule", "install", "--daily", "02:00", "--day", "mon"},
		{"schedule", "install", "--weekly", "02:00", "--day", "someday"},
	} {
		code, _, _ := runWith(&app{schedEnv: &schedule.Env{GOOS: "linux"}}, append([]string{"--archive", root}, args...)...)
		if code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestHeadlessSyncLogsAndNotifies(t *testing.T) {
	isolate(t)
	t.Setenv("CHITRAGIT_NOTIFY_DESKTOP", "true")
	root := t.TempDir()
	if code, _, e := run(t, "init", root); code != 0 {
		t.Fatal(e)
	}
	if out, err := exec.Command("git", "init", "-q", filepath.Join(root, "solo")).CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	notes := &fakeSender{}
	code, out, errOut := runWith(&app{notifier: notes}, "--archive", root, "--headless", "sync")
	if code != 0 {
		t.Fatalf("sync: %d %s %s", code, out, errOut)
	}
	if len(notes.sent) != 1 || notes.sent[0].title != "ChitraGit" || !strings.Contains(notes.sent[0].body, "Synced 1 repos") {
		t.Errorf("notifications = %+v", notes.sent)
	}
	logs, _ := filepath.Glob(filepath.Join(root, ".chitragit", "logs", "sync-*.log"))
	if len(logs) != 1 {
		t.Fatalf("want one run log, got %v", logs)
	}
	b, _ := os.ReadFile(logs[0])
	if !strings.Contains(string(b), "1 skipped") || strings.Contains(string(b), "\x1b[") {
		t.Errorf("log should hold the plain summary:\n%s", b)
	}

	// notify.on without run_finished keeps quiet.
	t.Setenv("CHITRAGIT_NOTIFY_ON", "conflict")
	notes.sent = nil
	runWith(&app{notifier: notes}, "--archive", root, "sync")
	if len(notes.sent) != 0 {
		t.Errorf("should not notify: %+v", notes.sent)
	}
}

func TestAccessibleOutput(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	_, out, _ := run(t, "--accessible", "init", root)
	if !strings.HasPrefix(out, "OK: Archive created") {
		t.Errorf("accessible init = %q", out)
	}
	t.Setenv("CHITRAGIT_UI_ACCESSIBLE", "true")
	_, out, _ = run(t, "--archive", root, "config", "validate")
	if !strings.HasPrefix(out, "OK: ") || strings.Contains(out, "✓") {
		t.Errorf("ui.accessible should switch to words: %q", out)
	}
}
