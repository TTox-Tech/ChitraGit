package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// A made-up token in GitHub's format. It has never been valid. It is split so
// that secret scanners reading this file (gitleaks in CI) do not report it.
const auditFakeToken = "ghp_" + "R8pZ3kQ9vT2mX7wL4nB6cY1dF5gH0jK8sA2e"

func TestAuditFindsIgnoresAndNeverStoresSecrets(t *testing.T) {
	isolate(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	repo := filepath.Join(root, "me", "app")
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.com",
			"GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, ".env"), []byte("TOKEN="+auditFakeToken+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn("add", ".env")
	gitIn("commit", "-q", "-m", "add env")
	gitIn("rm", "-q", ".env")
	gitIn("commit", "-q", "-m", "remove env")
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}
	sarif := filepath.Join(t.TempDir(), "out.sarif")

	code, out, errOut := run(t, "--archive", root, "--json", "audit", "--sarif", sarif)
	var rep struct {
		Schema   string `json:"schema"`
		Kind     string `json:"kind"`
		Findings []struct {
			ID, Repo, File, Preview, Severity, Status string
			New                                       bool
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("audit json: %v\n%s\n%s", err, out, errOut)
	}
	if code != core.ExitFindings || rep.Schema != jsonSchema || rep.Kind != "audit" || len(rep.Findings) != 1 {
		t.Fatalf("audit = exit %d %+v\n%s", code, rep, errOut)
	}
	f := rep.Findings[0]
	if f.Repo != "me/app" || f.File != ".env" || f.Preview != "ghp_****" || f.Severity != "critical" || !f.New {
		t.Errorf("finding = %+v", f)
	}

	// Nothing ChitraGit wrote may contain the secret.
	tail := auditFakeToken[4:]
	for _, name := range []string{sarif} {
		if b, err := os.ReadFile(name); err != nil || bytes.Contains(b, []byte(tail)) {
			t.Errorf("%s: err=%v or it holds the secret", name, err)
		}
	}
	_ = filepath.Walk(filepath.Join(root, ".chitragit"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			if b, _ := os.ReadFile(p); bytes.Contains(b, []byte(tail)) {
				t.Errorf("%s holds the secret", p)
			}
		}
		return nil
	})

	// Nothing new: incremental audit reads nothing and passes.
	if code, out, _ := run(t, "--archive", root, "audit"); code != 0 || !strings.Contains(out, "0 new") {
		t.Errorf("second audit = %d\n%s", code, out)
	}

	// Stored findings are still listed.
	if code, out, _ := run(t, "--archive", root, "audit", "findings"); code != 0 || !strings.Contains(out, f.ID[:8]) {
		t.Errorf("findings = %d\n%s", code, out)
	}

	if code, _, _ := run(t, "--archive", root, "audit", "ignore", f.ID[:6]); code != core.ExitUsage {
		t.Errorf("ignore without --reason = %d", code)
	}
	if code, _, errOut := run(t, "--archive", root, "audit", "ignore", f.ID[:6], "--reason", "test token"); code != 0 {
		t.Fatalf("ignore: %d %s", code, errOut)
	}
	// A full re-read finds it again, but it stays ignored.
	if code, out, _ := run(t, "--archive", root, "audit", "--history", "full"); code != 0 || !strings.Contains(out, "1 ignored") {
		t.Errorf("audit after ignore = %d\n%s", code, out)
	}
	if code, out, _ := run(t, "--archive", root, "audit", "findings"); code != 0 || !strings.Contains(out, "No findings") {
		t.Errorf("findings after ignore = %d\n%s", code, out)
	}
	if code, _, _ := run(t, "--archive", root, "audit", "unignore", f.ID); code != 0 {
		t.Errorf("unignore = %d", code)
	}
	if code, _, _ := run(t, "--archive", root, "audit", "--history", "head"); code != 0 {
		t.Errorf("head audit (secret deleted at HEAD) = %d", code)
	}
	if code, _, _ := run(t, "--archive", root, "audit", "--history", "full", "--fail-on", "critical"); code != core.ExitFindings {
		t.Errorf("--fail-on critical = %d", code)
	}

	for _, bad := range [][]string{{"--history", "all"}, {"--fail-on", "nope"}, {"--checks", "magic"}} {
		if code, _, _ := run(t, append([]string{"--archive", root, "audit"}, bad...)...); code != core.ExitUsage {
			t.Errorf("audit %v = %d, want usage error", bad, code)
		}
	}
}
