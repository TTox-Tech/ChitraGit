package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// A made-up token in GitHub's format. It has never been valid.
const fakeToken = "ghp_R8pZ3kQ9vT2mX7wL4nB6cY1dF5gH0jK8sA2e"

func runner(t *testing.T) *gitx.Runner {
	t.Helper()
	r, err := gitx.New(gitx.Options{HooksDir: t.TempDir()})
	if err != nil {
		t.Skip("git not available:", err)
	}
	return r
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test Author", "GIT_AUTHOR_EMAIL=a@example.com",
		"GIT_COMMITTER_NAME=Test Author", "GIT_COMMITTER_EMAIL=a@example.com",
		"GIT_CONFIG_NOSYSTEM=1", "HOME="+dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, file, content, msg string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if content == "" {
		git(t, dir, "rm", "-q", file)
	} else {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", file)
	}
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	return dir
}

func TestSecretInDeletedFileIsFoundInHistoryOnly(t *testing.T) {
	r := runner(t)
	dir := newRepo(t)
	first := commit(t, dir, "config/settings.py", "GITHUB_TOKEN = \""+fakeToken+"\"\n", "add settings")
	commit(t, dir, "config/settings.py", "", "remove settings")
	commit(t, dir, "README.md", "hello\n", "readme")

	ctx := context.Background()
	res, err := ScanSecrets(ctx, r, SecretOptions{RepoID: "r1", RepoPath: "me/app", Dir: dir, History: HistoryFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	f := res.Findings[0]
	if f.File != "config/settings.py" || f.Line != 1 || f.Commit != first || f.Author != "Test Author" {
		t.Errorf("location = %s:%d %s %q", f.File, f.Line, f.Commit, f.Author)
	}
	if f.Severity != SevCritical || f.Confidence != ConfidenceHigh {
		t.Errorf("severity = %s/%s", f.Severity, f.Confidence)
	}
	if f.Preview != "ghp_****" {
		t.Errorf("preview = %q", f.Preview)
	}
	b, _ := json.Marshal(res.Findings)
	if bytes.Contains(b, []byte(fakeToken[4:])) {
		t.Fatal("the full secret leaked into the finding")
	}
	if res.Commits != 2 || len(res.Tips) == 0 { // the delete-only commit has nothing to read
		t.Errorf("commits = %d, tips = %v", res.Commits, res.Tips)
	}

	head, err := ScanSecrets(ctx, r, SecretOptions{RepoID: "r1", RepoPath: "me/app", Dir: dir, History: HistoryHead})
	if err != nil {
		t.Fatal(err)
	}
	if len(head.Findings) != 0 {
		t.Errorf("HEAD mode found a deleted secret: %+v", head.Findings)
	}
}

func TestSameSecretTwiceIsOneFinding(t *testing.T) {
	r := runner(t)
	dir := newRepo(t)
	first := commit(t, dir, "a.env", "TOKEN="+fakeToken+"\n", "one")
	commit(t, dir, "b.env", "TOKEN="+fakeToken+"\n", "two")
	res, err := ScanSecrets(context.Background(), r, SecretOptions{RepoID: "r", RepoPath: "x", Dir: dir, History: HistoryFull})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Findings[0].Occurrences != 2 {
		t.Fatalf("findings = %+v", res.Findings)
	}
	if res.Findings[0].Commit != first || res.Findings[0].File != "a.env" {
		t.Errorf("kept %s in %s, want the first sighting", res.Findings[0].File, res.Findings[0].Commit)
	}
}

func TestIncrementalReadsOnlyNewCommits(t *testing.T) {
	r := runner(t)
	dir := newRepo(t)
	commit(t, dir, "a.env", "TOKEN="+fakeToken+"\n", "one")
	ctx := context.Background()
	o := SecretOptions{RepoID: "r", RepoPath: "x", Dir: dir, History: HistoryIncremental}
	res, err := ScanSecrets(ctx, r, o)
	if err != nil || len(res.Findings) != 1 {
		t.Fatalf("first run: %v %+v", err, res)
	}

	o.Seen = res.Tips
	again, err := ScanSecrets(ctx, r, o)
	if err != nil || len(again.Findings) != 0 || again.Commits != 0 {
		t.Fatalf("unchanged repo: %v %+v", err, again)
	}

	commit(t, dir, "b.txt", "nothing here\n", "two")
	more, err := ScanSecrets(ctx, r, o)
	if err != nil {
		t.Fatal(err)
	}
	if more.Commits != 1 || len(more.Findings) != 0 {
		t.Fatalf("after one new commit: commits=%d findings=%+v", more.Commits, more.Findings)
	}

	// A tip that no longer exists (history rewritten and collected) is
	// ignored instead of failing the audit.
	o.Seen = []string{"0123456789abcdef0123456789abcdef01234567"}
	gone, err := ScanSecrets(ctx, r, o)
	if err != nil || len(gone.Findings) != 1 {
		t.Fatalf("stale cursor: %v %+v", err, gone)
	}
}

func TestBareRepo(t *testing.T) {
	r := runner(t)
	src := newRepo(t)
	commit(t, src, "deploy.sh", "export GH="+fakeToken+"\n", "deploy")
	bare := filepath.Join(t.TempDir(), "mirror.git")
	git(t, src, "clone", "-q", "--mirror", src, bare)

	for _, mode := range []string{HistoryFull, HistoryHead} {
		res, err := ScanSecrets(context.Background(), r, SecretOptions{RepoID: "r", RepoPath: "m", Dir: bare, History: mode})
		if err != nil || len(res.Findings) != 1 {
			t.Errorf("%s: %v %+v", mode, err, res)
		}
	}
}

// A repo's own config must not be able to run commands during an audit.
func TestRepoDiffDriversNeverRun(t *testing.T) {
	r := runner(t)
	dir := newRepo(t)
	marker := filepath.Join(t.TempDir(), "pwned")
	git(t, dir, "config", "diff.evil.textconv", "touch "+marker+" && cat")
	git(t, dir, "config", "diff.external", "touch "+marker)
	commit(t, dir, ".gitattributes", "*.txt diff=evil\n", "attrs")
	commit(t, dir, "x.txt", "TOKEN="+fakeToken+"\n", "x")

	res, err := ScanSecrets(context.Background(), r, SecretOptions{RepoID: "r", RepoPath: "x", Dir: dir, History: HistoryFull})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repo's diff driver ran")
	}
	if len(res.Findings) != 1 {
		t.Errorf("findings = %+v", res.Findings)
	}
}

func TestGitleaksAllowCommentIsNotTrusted(t *testing.T) {
	r := runner(t)
	dir := newRepo(t)
	commit(t, dir, "a.py", "TOKEN = \""+fakeToken+"\"  # gitleaks:allow\n", "hide")
	res, err := ScanSecrets(context.Background(), r, SecretOptions{RepoID: "r", RepoPath: "x", Dir: dir, History: HistoryFull})
	if err != nil || len(res.Findings) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
}

func TestLargeFilesAreSkipped(t *testing.T) {
	r := runner(t)
	dir := newRepo(t)
	commit(t, dir, "big.txt", strings.Repeat("x", 2048)+"\nTOKEN="+fakeToken+"\n", "big")
	for _, mode := range []string{HistoryFull, HistoryHead} {
		res, err := ScanSecrets(context.Background(), r, SecretOptions{RepoID: "r", RepoPath: "x", Dir: dir, History: mode, MaxFileSize: 1024})
		if err != nil || len(res.Findings) != 0 || res.Skipped != 1 {
			t.Errorf("%s: %v %+v", mode, err, res)
		}
	}
}

func TestEmptyRepo(t *testing.T) {
	r := runner(t)
	dir := newRepo(t)
	for _, mode := range []string{HistoryFull, HistoryHead, HistoryIncremental} {
		res, err := ScanSecrets(context.Background(), r, SecretOptions{RepoID: "r", RepoPath: "x", Dir: dir, History: mode})
		if err != nil || len(res.Findings) != 0 {
			t.Errorf("%s: %v %+v", mode, err, res)
		}
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{"short": "****", "": "****", "AKIA1234567890ABCD": "AKIA****"} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSARIF(t *testing.T) {
	var b bytes.Buffer
	fs := []Finding{
		{ID: "abc", RepoPath: "me/app", Check: CheckSecrets, Rule: "github-pat", Title: "GitHub token", Severity: SevCritical, File: "a.py", Line: 3, Preview: "ghp_****", Status: StatusOpen},
		{ID: "def", RepoPath: "me/app", Rule: "x", Status: StatusIgnored},
	}
	if err := WriteSARIF(&b, "1.1.0", fs); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []struct {
				RuleID string `json:"ruleId"`
				Level  string `json:"level"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(b.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != "2.1.0" || len(doc.Runs) != 1 || len(doc.Runs[0].Results) != 1 || doc.Runs[0].Results[0].Level != "error" {
		t.Errorf("sarif = %s", b.String())
	}
}
