package audit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Trimmed from real `zizmor --format sarif` output (v1.32).
const zizmorSARIF = `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"zizmor","rules":[
 {"id":"zizmor/template-injection","helpUri":"https://docs.zizmor.sh/audits/#template-injection","help":{"text":"code injection via template expansion"}},
 {"id":"zizmor/artipacked","helpUri":"https://docs.zizmor.sh/audits/#artipacked","help":{"text":"credential persistence through GitHub Actions artifacts"}}]}},
 "results":[
 {"ruleId":"zizmor/template-injection","level":"error","message":{"text":"code injection via template expansion: may expand into attacker-controllable code"},
  "properties":{"zizmor/confidence":"High","zizmor/severity":"High"},
  "locations":[{"physicalLocation":{"artifactLocation":{"uri":"repo/.github/workflows/ci.yml"},"region":{"startLine":11,"snippet":{"text":"run: echo \"${{ github.event.issue.title }}\""}}}}]},
 {"ruleId":"zizmor/artipacked","level":"warning","message":{"text":"credential persistence through GitHub Actions artifacts: does not set persist-credentials: false"},
  "properties":{"zizmor/confidence":"Low","zizmor/severity":"Medium"},
  "locations":[{"physicalLocation":{"artifactLocation":{"uri":"./repo/.github/workflows/ci.yml"},"region":{"startLine":8}}}]},
 {"ruleId":"plain","level":"note","message":{"text":"something"},"locations":[]}
]}]}`

func TestImportSARIF(t *testing.T) {
	fs, err := importSARIF([]byte(zizmorSARIF), CheckActions, "r1", "me/app", "abc", "repo/")
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 3 {
		t.Fatalf("findings = %+v", fs)
	}
	f := fs[0]
	if f.Rule != "zizmor/template-injection" || f.Severity != SevHigh || f.File != ".github/workflows/ci.yml" ||
		f.Line != 11 || f.Title != "Code injection via template expansion" || !strings.Contains(f.Hint, "docs.zizmor.sh") {
		t.Errorf("first = %+v", f)
	}
	if fs[1].Rule != "zizmor/artipacked" || fs[1].Severity != SevMedium || fs[1].Confidence != ConfidenceLow {
		t.Errorf("second = %+v", fs[1])
	}
	if fs[2].Rule != "zizmor/plain" || fs[2].Severity != SevLow {
		t.Errorf("third = %+v", fs[2])
	}
	if _, err := importSARIF([]byte("not json"), CheckActions, "r", "x", "c", ""); err == nil {
		t.Error("bad SARIF accepted")
	}
}

func TestToolEnvHasNoSecrets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_shouldnotleak")
	t.Setenv("OPENAI_API_KEY", "sk-shouldnotleak")
	t.Setenv("HTTPS_PROXY", "http://corp-proxy:8080")
	dir := t.TempDir()
	env := strings.Join(toolEnv(dir), "\n")
	if strings.Contains(env, "shouldnotleak") || strings.Contains(env, "corp-proxy") {
		t.Errorf("tool env leaks: %s", env)
	}
	for _, want := range []string{"HTTPS_PROXY=" + deadProxy, "HOME=" + filepath.Join(dir, ".home")} {
		if !strings.Contains(env, want) {
			t.Errorf("tool env lacks %s", want)
		}
	}
}

func TestExportFilesStaysInside(t *testing.T) {
	dir, err := exportFiles([]HeadFile{
		{Path: ".github/workflows/ci.yml", Data: []byte("on: push")},
		{Path: "../escape.txt", Data: []byte("x")},
		{Path: "/abs.txt", Data: []byte("x")},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if _, err := os.Stat(filepath.Join(dir, "repo", ".github", "workflows", "ci.yml")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); err == nil {
		t.Error("wrote outside the export folder")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.txt")); err == nil {
		t.Error("wrote outside the export folder")
	}
}

func TestFindToolModes(t *testing.T) {
	ctx := context.Background()
	if tool, err := FindTool(ctx, "chitragit-no-such-tool", ToolAuto); tool != nil || err != nil {
		t.Errorf("auto, missing = %v, %v", tool, err)
	}
	if _, err := FindTool(ctx, "chitragit-no-such-tool", ToolOn); err == nil {
		t.Error("on, missing: no error")
	}
	if tool, err := FindTool(ctx, "git", ToolOff); tool != nil || err != nil {
		t.Errorf("off = %v, %v", tool, err)
	}
}

// TestRunZizmor runs the real zizmor when it is installed.
func TestRunZizmor(t *testing.T) {
	if _, err := exec.LookPath(ToolZizmor); err != nil {
		t.Skip("zizmor not installed")
	}
	tool, err := FindTool(context.Background(), ToolZizmor, ToolOn)
	if err != nil {
		t.Fatal(err)
	}
	fs, err := RunZizmor(context.Background(), tool, "r1", "me/app", "abc", []HeadFile{
		{Path: ".github/workflows/ci.yml", Data: []byte(riskyWorkflow)},
		{Path: "README.md", Data: []byte("not a workflow")},
	})
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]bool{}
	for _, f := range fs {
		rules[f.Rule] = true
		if f.File != ".github/workflows/ci.yml" || f.Check != CheckActions {
			t.Errorf("finding = %+v", f)
		}
	}
	if !rules["zizmor/template-injection"] || !rules["zizmor/dangerous-triggers"] {
		t.Errorf("zizmor rules = %v", rules)
	}
}

func TestDropCovered(t *testing.T) {
	builtin := []Finding{
		{Rule: RuleUnpinnedAction, File: "a.yml"},
		{Rule: RuleUnpinnedAction, File: "b.yml"},
		{Rule: RuleWriteAll, File: "a.yml"},
	}
	tool := []Finding{{Rule: "zizmor/unpinned-uses", File: "a.yml"}}
	got := DropCovered(builtin, tool, mustRules(t).Actions.ZizmorEquivalents)
	if len(got) != 2 || got[0].File != "b.yml" || got[1].Rule != RuleWriteAll {
		t.Errorf("DropCovered = %+v", got)
	}
}
