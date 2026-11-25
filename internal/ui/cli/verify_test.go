package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
)

func TestVerifyLedger(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if out, err := exec.Command("git", "init", "-q", filepath.Join(root, "app")).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}

	code, out, errOut := run(t, "--archive", root, "verify")
	if code != 0 || !strings.Contains(out, "hash chain intact") || !strings.Contains(out, "every check passed") {
		t.Fatalf("verify: %d %q %q", code, out, errOut)
	}
	if code, out, errOut := run(t, "--archive", root, "verify", "--full"); code != 0 || !strings.Contains(out, "pass git fsck (every object)") {
		t.Fatalf("verify --full: %d %q %q", code, out, errOut)
	}
	// `fsck` is the git word for it.
	code, out, _ = run(t, "--archive", root, "--json", "fsck")
	var rep struct {
		OK     bool
		Checks []struct {
			Name string
			OK   bool
		}
	}
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || !rep.OK || len(rep.Checks) == 0 || rep.Checks[0].Name != engine.CheckLedger {
		t.Fatalf("fsck --json: %d %q", code, out)
	}

	arc, err := engine.OpenArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := engine.OpenStore(context.Background(), arc.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB.Exec(`UPDATE events SET payload = '{"message":"forged"}' WHERE seq = 1`)
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	code, out, _ = run(t, "--archive", root, "verify")
	if code != core.ExitPartial || !strings.Contains(out, "broken at event 1") || !strings.Contains(out, "Some checks failed") {
		t.Errorf("verify after tampering: %d %q", code, out)
	}
}

func TestSyncVerify(t *testing.T) {
	isolate(t)
	t.Setenv("CHITRAGIT_NOTIFY_DESKTOP", "true")
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	if out, err := exec.Command("git", "init", "-q", filepath.Join(root, "solo")).CombinedOutput(); err != nil {
		t.Fatalf("git init: %s", out)
	}
	code, out, errOut := run(t, "--archive", root, "sync", "--verify")
	if code != 0 || !strings.Contains(out, "every check passed") {
		t.Fatalf("sync --verify: %d %q %q", code, out, errOut)
	}
	// Without the flag nothing is checked.
	if _, out, _ := run(t, "--archive", root, "sync"); strings.Contains(out, "every check passed") {
		t.Errorf("plain sync ran verify: %q", out)
	}

	arc, err := engine.OpenArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	st, err := engine.OpenStore(context.Background(), arc.DBFile())
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.DB.Exec(`UPDATE events SET payload = '{"message":"forged"}' WHERE seq = 1`)
	st.Close()
	if err != nil {
		t.Fatal(err)
	}
	notes := &fakeSender{}
	code, out, _ = runWith(&app{notifier: notes}, "--archive", root, "--json", "sync", "--verify")
	var rep struct{ Verify *struct{ OK bool } }
	if code != core.ExitPartial || json.Unmarshal([]byte(out), &rep) != nil || rep.Verify == nil || rep.Verify.OK {
		t.Fatalf("sync --verify after tampering: %d %q", code, out)
	}
	if len(notes.sent) != 2 || !strings.Contains(notes.sent[1].body, "found problems") {
		t.Errorf("notifications = %+v", notes.sent)
	}
}
