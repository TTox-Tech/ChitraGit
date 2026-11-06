package audit

import (
	"slices"
	"strings"
	"testing"
)

const riskyWorkflow = `name: ci
on:
  pull_request_target:
  issues:
env:
  ACTIONS_ALLOW_UNSECURE_COMMANDS: true
jobs:
  build:
    runs-on: [self-hosted, linux]
    permissions: write-all
    steps:
      - uses: actions/checkout@v4
        with:
          ref: ${{ github.event.pull_request.head.sha }}
      - uses: someone/setup-thing@main
      - uses: someone/pinned@0123456789abcdef0123456789abcdef01234567
      - uses: ./local-action
      - uses: docker://alpine:3
      - name: greet
        run: |
          echo "hello"
          echo "${{ github.event.issue.title }}"
      - uses: actions/github-script@60a0d83039c74a4aee543508d2ffcb1c3799cdea
        with:
          script: console.log("${{ github.event.comment.body }}")
  call:
    uses: other/repo/.github/workflows/x.yml@v1
    secrets: inherit
`

const safeWorkflow = `on: [push]
permissions:
  contents: read
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8
      - run: echo "${{ github.sha }}"
        env:
          TITLE: ${{ github.event.issue.title }}
`

func mustRules(t *testing.T) *RulesPack {
	t.Helper()
	p, err := BuiltinRules()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func byRule(fs []Finding) map[string][]Finding {
	m := map[string][]Finding{}
	for _, f := range fs {
		m[f.Rule] = append(m[f.Rule], f)
	}
	return m
}

func TestActionsRules(t *testing.T) {
	files := []HeadFile{
		{Path: ".github/workflows/ci.yml", Data: []byte(riskyWorkflow)},
		{Path: ".github/workflows/ok.yaml", Data: []byte(safeWorkflow)},
		{Path: "docs/not-a-workflow.yml", Data: []byte(riskyWorkflow)},
		{Path: ".github/workflows/broken.yml", Data: []byte("on: [push\njobs: {")},
	}
	fs := ScanActions(mustRules(t), "r1", "me/app", "abc", files)
	got := byRule(fs)
	for _, f := range fs {
		if f.File != ".github/workflows/ci.yml" {
			t.Errorf("finding outside the risky workflow: %+v", f)
		}
		if f.Check != CheckActions || f.Commit != "abc" || f.Hint == "" || f.Title == "" || f.Line == 0 {
			t.Errorf("incomplete finding: %+v", f)
		}
	}
	want := map[string]int{
		RulePRTargetCheckout: 1, RuleWriteAll: 1, RuleInsecureCommands: 1, RuleSecretsInherit: 1,
		RuleSelfHostedOnForks: 1, RuleScriptInjection: 2, RuleUnpinnedAction: 4,
	}
	for rule, n := range want {
		if len(got[rule]) != n {
			t.Errorf("%s: %d findings, want %d: %+v", rule, len(got[rule]), n, got[rule])
		}
	}
	if len(got[RuleDefaultPerms]) != 1 { // the "call" job has no permissions block
		t.Errorf("%s: %+v", RuleDefaultPerms, got[RuleDefaultPerms])
	}
	if f := got[RulePRTargetCheckout][0]; f.Severity != SevCritical || f.Line != 14 {
		t.Errorf("pr target checkout = %+v", f)
	}
	inj := got[RuleScriptInjection]
	lines := []int{inj[0].Line, inj[1].Line}
	slices.Sort(lines)
	if !slices.Equal(lines, []int{22, 25}) {
		t.Errorf("injection lines = %v", lines)
	}
	for _, f := range got[RuleUnpinnedAction] {
		switch {
		case strings.HasPrefix(f.Preview, "actions/"), strings.HasPrefix(f.Preview, "docker://"):
			if f.Severity != SevLow {
				t.Errorf("%s severity %s", f.Preview, f.Severity)
			}
		case f.Severity != SevMedium:
			t.Errorf("%s severity %s", f.Preview, f.Severity)
		}
		if strings.Contains(f.Preview, "pinned@") || strings.Contains(f.Preview, "./") {
			t.Errorf("pinned or local action reported: %s", f.Preview)
		}
	}

	// IDs are stable across runs and do not depend on line numbers.
	again := ScanActions(mustRules(t), "r1", "me/app", "def", []HeadFile{{Path: ".github/workflows/ci.yml", Data: []byte("\n\n" + riskyWorkflow)}})
	if len(again) != len(fs) || again[0].ID != fs[0].ID {
		t.Errorf("IDs changed when lines moved")
	}
}

func TestActionsDefaultPermissionsAndComposite(t *testing.T) {
	wf := "on: push\njobs:\n  a:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo hi\n"
	action := "runs:\n  using: composite\n  steps:\n    - uses: foo/bar@v2\n    - run: echo ${{ github.event.pull_request.title }}\n      shell: bash\n"
	fs := ScanActions(mustRules(t), "r", "me/app", "c", []HeadFile{
		{Path: ".github/workflows/a.yml", Data: []byte(wf)},
		{Path: "sub/action.yml", Data: []byte(action)},
	})
	got := byRule(fs)
	if len(got[RuleDefaultPerms]) != 1 || got[RuleDefaultPerms][0].Severity != SevLow {
		t.Errorf("default permissions: %+v", got[RuleDefaultPerms])
	}
	if len(got[RuleUnpinnedAction]) != 1 || len(got[RuleScriptInjection]) != 1 {
		t.Errorf("composite action: %+v", fs)
	}
}

func TestActionsPackOverrides(t *testing.T) {
	p, err := ParseRules([]byte(`{"schema":1,"version":"2030.01.01","actions":{"rules":{
		"gha-unpinned-action":{"disabled":true},"gha-write-all":{"severity":"critical"}},
		"untrusted_inputs":["github\\.event\\.issue\\.title"]},"agents":{"files":[],"content_rules":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	got := byRule(ScanActions(p, "r", "x", "c", []HeadFile{{Path: ".github/workflows/ci.yml", Data: []byte(riskyWorkflow)}}))
	if len(got[RuleUnpinnedAction]) != 0 {
		t.Error("disabled rule still reported")
	}
	if got[RuleWriteAll][0].Severity != SevCritical {
		t.Error("severity override ignored")
	}
	if len(got[RuleScriptInjection]) != 1 {
		t.Errorf("pack inputs list not used: %+v", got[RuleScriptInjection])
	}
}

func TestIsWorkflowFile(t *testing.T) {
	for p, want := range map[string]bool{
		".github/workflows/ci.yml": true, ".github/workflows/x.yaml": true, ".GitHub/Workflows/CI.YML": true,
		".github/workflows/sub/ci.yml": false, "action.yml": true, "a/b/action.yaml": true,
		"ci.yml": false, ".github/workflows/readme.md": false,
	} {
		if IsWorkflowFile(p) != want {
			t.Errorf("IsWorkflowFile(%q) = %v", p, !want)
		}
	}
}
