package audit

import (
	"path"
	"regexp"
	"strings"
)

// CheckAutorun covers files that make an editor or a shell run something
// when a folder is opened or entered (KodeKavach Shield threat T14).
const CheckAutorun = "autorun"

// Rules of the autorun check.
const (
	RuleVSCodeFolderOpenTask = "autorun-vscode-folder-open"
	RuleVSCodeExecPath       = "autorun-vscode-exec-path"
	RuleDirenv               = "autorun-direnv"
	RuleDevcontainerHost     = "autorun-devcontainer-host"
	RuleNpmrcExec            = "autorun-npmrc-exec"
	RuleYarnPath             = "autorun-yarn-path"
)

// IsAutorunFile reports the files the autorun check reads.
// shortcut: JetBrains .idea run configurations are not read; they only run
// when the user starts them. Add them if a startup-task plugin becomes common.
func IsAutorunFile(p string) bool {
	lp := strings.ToLower(p)
	base := path.Base(lp)
	switch {
	case lp == ".vscode/tasks.json", lp == ".vscode/settings.json":
		return true
	case base == ".envrc", base == ".npmrc", base == ".yarnrc.yml":
		return true
	case lp == ".devcontainer.json", base == "devcontainer.json" && strings.HasPrefix(lp, ".devcontainer/"):
		return true
	}
	return false
}

var (
	folderOpenRe = regexp.MustCompile(`"runOn"\s*:\s*"folderOpen"`)
	// Settings that name a program VS Code or an extension starts on its own.
	vscodeExecRe = regexp.MustCompile(`"(python\.defaultInterpreterPath|python\.pythonPath|[\w.-]+\.(executablePath|serverPath|binPath|toolsPath|runtime)|terminal\.integrated\.(shell|automationProfile|profiles)[\w.-]*|go\.alternateTools|git\.path)"\s*:\s*("[^"]*"|\{)`)
	devInitRe    = regexp.MustCompile(`"initializeCommand"\s*:`)
	npmrcExecRe  = regexp.MustCompile(`(?m)^\s*(script-shell|node-options|git|shell)\s*=\s*(.*)$`)
	yarnPathRe   = regexp.MustCompile(`(?m)^\s*yarnPath\s*:\s*["']?([^"'\s#]+)`)
)

// ScanAutorun checks the autorun files among files (read from a commit by
// the caller).
func ScanAutorun(repoID, repoPath, commit string, files []HeadFile) []Finding {
	seen := map[string]bool{}
	var out []Finding
	add := func(rule string, sev Severity, file string, data []byte, off int, key, title, preview, hint string) {
		id := FindingID(repoID, CheckAutorun, rule, file+"\x00"+key)
		if seen[id] {
			return
		}
		seen[id] = true
		out = append(out, Finding{
			ID: id, RepoID: repoID, RepoPath: repoPath, Check: CheckAutorun, Rule: rule, Title: title,
			Severity: sev, Confidence: ConfidenceHigh, File: file, Line: lineAt(data, off), Commit: commit,
			Preview: snippet(preview, 80), Occurrences: 1, Hint: hint, Status: StatusOpen,
		})
	}
	for _, f := range files {
		lp := strings.ToLower(f.Path)
		base := path.Base(lp)
		d := f.Data
		switch {
		case lp == ".vscode/tasks.json":
			if loc := folderOpenRe.FindIndex(d); loc != nil {
				add(RuleVSCodeFolderOpenTask, SevHigh, f.Path, d, loc[0], "", "A VS Code task that runs when the folder is opened",
					string(d[loc[0]:loc[1]]), "VS Code runs this task as soon as the folder is opened in a trusted window. Read the task before trusting the folder.")
			}
		case lp == ".vscode/settings.json":
			for _, m := range vscodeExecRe.FindAllSubmatchIndex(d, -1) {
				key := string(d[m[2]:m[3]])
				add(RuleVSCodeExecPath, SevMedium, f.Path, d, m[0], key, "A VS Code setting that names a program to run ("+key+")",
					string(d[m[0]:m[1]]), "VS Code or an extension starts this program on its own when the folder is trusted. Check that it points where you expect.")
			}
		case base == ".envrc":
			add(RuleDirenv, SevMedium, f.Path, d, 0, "", "A direnv .envrc that runs when you enter the folder",
				firstLine(string(d)), "direnv runs this shell file on every cd into the folder once it is allowed. Read it before `direnv allow`.")
		case base == ".npmrc":
			for _, m := range npmrcExecRe.FindAllSubmatchIndex(d, -1) {
				key := string(d[m[2]:m[3]])
				add(RuleNpmrcExec, SevMedium, f.Path, d, m[0], key, "An .npmrc setting that changes what npm runs ("+key+")",
					string(d[m[0]:m[1]]), "npm uses this for every install and script in the folder.")
			}
		case base == ".yarnrc.yml":
			if m := yarnPathRe.FindSubmatchIndex(d); m != nil {
				target := path.Clean(path.Join(path.Dir(f.Path), string(d[m[2]:m[3]]))) // nosemgrep: use-filepath-join -- repo paths always use "/"
				if !strings.HasPrefix(target, ".yarn/releases/") {
					add(RuleYarnPath, SevHigh, f.Path, d, m[0], target, "yarnPath runs a file from outside .yarn/releases",
						string(d[m[0]:m[1]]), "Every yarn command in the folder runs this file instead of yarn.")
				}
			}
		case IsAutorunFile(f.Path): // devcontainer.json
			if loc := devInitRe.FindIndex(d); loc != nil {
				add(RuleDevcontainerHost, SevHigh, f.Path, d, loc[0], "", "A dev container command that runs on your computer, not in the container",
					string(d[loc[0]:min(len(d), loc[1]+60)]), "initializeCommand runs on the host before the container starts. Read it before reopening in a container.")
			}
		}
	}
	sortFindings(out)
	return out
}
