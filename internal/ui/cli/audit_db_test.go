package cli

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// fakeOSV serves an npm export with one lodash advisory.
func fakeOSV(t *testing.T) {
	t.Helper()
	rec := map[string]any{
		"id": "GHSA-p6mc-m468-83gw", "summary": "Prototype Pollution in lodash", "aliases": []string{"CVE-2020-8203"},
		"database_specific": map[string]any{"severity": "HIGH"},
		"affected": []map[string]any{{
			"package": map[string]string{"ecosystem": "npm", "name": "lodash"},
			"ranges":  []map[string]any{{"type": "SEMVER", "events": []map[string]string{{"introduced": "0"}, {"fixed": "4.17.19"}}}},
		}},
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("GHSA-p6mc-m468-83gw.json")
	_ = json.NewEncoder(w).Encode(rec)
	_ = zw.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/npm/all.zip" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(buf.Bytes())
	}))
	t.Cleanup(srv.Close)
	old := osvBaseURL
	osvBaseURL = srv.URL
	t.Cleanup(func() { osvBaseURL = old })
}

func TestAuditDepsEndToEnd(t *testing.T) {
	isolate(t)
	fakeOSV(t)
	root := t.TempDir()
	if code, _, errOut := run(t, "init", root); code != 0 {
		t.Fatalf("init: %s", errOut)
	}
	repo := filepath.Join(root, "me", "web")
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
	lock := func(v string) {
		t.Helper()
		b := `{"lockfileVersion":3,"packages":{"":{},"node_modules/lodash":{"version":"` + v + `"}}}`
		if err := os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte(b), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn("add", "package-lock.json")
		gitIn("commit", "-q", "-m", "lodash "+v)
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn("init", "-q", "-b", "main")
	lock("4.17.15")
	if code, _, errOut := run(t, "--archive", root, "scan"); code != 0 {
		t.Fatalf("scan: %s", errOut)
	}

	// No database yet: asking for deps is an error with a hint; the
	// default audit skips deps with a note and still runs secrets.
	if code, _, errOut := run(t, "--archive", root, "audit", "--checks", "deps"); code != core.ExitUsage || !strings.Contains(errOut, "audit db update") {
		t.Fatalf("deps without a database = %d\n%s", code, errOut)
	}
	if code, _, errOut := run(t, "--archive", root, "audit"); code != 0 || !strings.Contains(errOut, "Skipped the deps check") {
		t.Fatalf("default audit without a database = %d\n%s", code, errOut)
	}

	if code, out, errOut := run(t, "--archive", root, "audit", "db", "update", "--ecosystems", "npm"); code != 0 || !strings.Contains(out, "npm") {
		t.Fatalf("db update = %d\n%s\n%s", code, out, errOut)
	}
	if code, _, _ := run(t, "audit", "db", "update", "--ecosystems", "cobol"); code != core.ExitUsage {
		t.Errorf("unknown ecosystem = %d", code)
	}
	if code, out, _ := run(t, "audit", "db", "status"); code != 0 || !strings.Contains(out, "npm") || !strings.Contains(out, "not downloaded: Go") {
		t.Errorf("db status = %d\n%s", code, out)
	}

	code, out, errOut := run(t, "--archive", root, "--json", "audit", "--checks", "deps")
	var rep struct {
		Findings []struct{ ID, Check, Rule, File, Preview, Hint, Severity string } `json:"findings"`
		Deps     struct{ Lockfiles, Packages int }                                 `json:"deps"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("audit json: %v\n%s\n%s", err, out, errOut)
	}
	if code != core.ExitFindings || len(rep.Findings) != 1 || rep.Deps.Lockfiles != 1 || rep.Deps.Packages != 1 {
		t.Fatalf("deps audit = %d %+v\n%s", code, rep, errOut)
	}
	f := rep.Findings[0]
	if f.Check != "deps" || f.Rule != "CVE-2020-8203" || f.File != "package-lock.json" || f.Severity != "high" ||
		!strings.Contains(f.Hint, "4.17.19") {
		t.Errorf("finding = %+v", f)
	}

	// Upgrading fixes it: the audit passes and the finding is kept as fixed.
	lock("4.17.21")
	if code, out, _ := run(t, "--archive", root, "audit", "--checks", "deps"); code != 0 || !strings.Contains(out, "1 earlier finding fixed") {
		t.Errorf("audit after the upgrade = %d\n%s", code, out)
	}
	if code, out, _ := run(t, "--archive", root, "audit", "findings", "--check", "deps"); code != 0 || !strings.Contains(out, "No findings") {
		t.Errorf("findings after the upgrade = %d\n%s", code, out)
	}
	if code, out, _ := run(t, "--archive", root, "audit", "findings", "--fixed"); code != 0 || !strings.Contains(out, "[fixed]") {
		t.Errorf("findings --fixed = %d\n%s", code, out)
	}
}
