package audit

import (
	"path"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Rules of the actions check. Severities are defaults; the rules pack can
// change or disable them.
const (
	RuleUnpinnedAction    = "gha-unpinned-action"
	RuleScriptInjection   = "gha-script-injection"
	RulePRTargetCheckout  = "gha-pr-target-checkout"
	RuleWriteAll          = "gha-write-all"
	RuleDefaultPerms      = "gha-default-permissions"
	RuleInsecureCommands  = "gha-insecure-commands"
	RuleSecretsInherit    = "gha-secrets-inherit"
	RuleSelfHostedOnForks = "gha-self-hosted-pr"
)

// IsWorkflowFile reports whether p is a GitHub Actions workflow (directly
// in .github/workflows) or an action definition (action.yml anywhere).
func IsWorkflowFile(p string) bool {
	lp := strings.ToLower(p)
	ext := path.Ext(lp)
	if ext != ".yml" && ext != ".yaml" {
		return false
	}
	if path.Dir(lp) == ".github/workflows" {
		return true
	}
	base := path.Base(lp)
	return base == "action.yml" || base == "action.yaml"
}

var (
	exprRe   = regexp.MustCompile(`\$\{\{([^}]|\}[^}])*\}\}`)
	shaRef   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	inputsRe = regexp.MustCompile(`^\$\{\{\s*(github\.event\.)?inputs\.`)
	prHeadRe = regexp.MustCompile(`github\.event\.pull_request\.head\.|github\.head_ref|github\.event\.workflow_run\.head_|refs/pull/`)
)

// actionsScanner checks one repo's workflow files.
type actionsScanner struct {
	pack   *RulesPack
	repoID string
	repo   string
	commit string
	out    []Finding
	seen   map[string]bool
}

// ScanActions runs the built-in GitHub Actions rules over files (as read
// from HEAD by the caller). Files that are not workflows are ignored.
func ScanActions(pack *RulesPack, repoID, repoPath, commit string, files []HeadFile) []Finding {
	s := &actionsScanner{pack: pack, repoID: repoID, repo: repoPath, commit: commit, seen: map[string]bool{}}
	for _, f := range files {
		if !IsWorkflowFile(f.Path) {
			continue
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(f.Data, &doc); err != nil || len(doc.Content) == 0 {
			continue // not valid YAML: GitHub will not run it either
		}
		root := doc.Content[0]
		if root.Kind != yaml.MappingNode {
			continue
		}
		if path.Dir(strings.ToLower(f.Path)) == ".github/workflows" {
			s.workflow(f.Path, root)
		} else {
			s.action(f.Path, root)
		}
	}
	sortFindings(s.out)
	return s.out
}

func (s *actionsScanner) add(rule string, def Severity, file string, line int, key, title, preview, hint string) {
	sev, ok := apply(s.pack.Actions.Rules, rule, def)
	if !ok {
		return
	}
	id := FindingID(s.repoID, CheckActions, rule, file+"\x00"+key)
	if s.seen[id] {
		return
	}
	s.seen[id] = true
	s.out = append(s.out, Finding{
		ID: id, RepoID: s.repoID, RepoPath: s.repo, Check: CheckActions, Rule: rule, Title: title,
		Severity: sev, Confidence: ConfidenceHigh, File: file, Line: line, Commit: s.commit,
		Preview: snippet(preview, 80), Occurrences: 1, Hint: hint, Status: StatusOpen,
	})
}

func (s *actionsScanner) workflow(file string, root *yaml.Node) {
	triggers := workflowTriggers(get(root, "on"))
	privileged := triggers["pull_request_target"] || triggers["workflow_run"]
	forkPR := triggers["pull_request"] || triggers["pull_request_target"]

	topPerms := get(root, "permissions")
	s.permissions(file, topPerms, "workflow")
	s.insecureEnv(file, get(root, "env"), "workflow")

	jobs := get(root, "jobs")
	jobsWithoutPerms := 0
	eachPair(jobs, func(name string, _, job *yaml.Node) {
		if job.Kind != yaml.MappingNode {
			return
		}
		perms := get(job, "permissions")
		if perms == nil {
			jobsWithoutPerms++
		}
		s.permissions(file, perms, "job "+name)
		s.insecureEnv(file, get(job, "env"), "job "+name)
		if u := get(job, "uses"); u != nil && u.Kind == yaml.ScalarNode {
			s.uses(file, u, "job "+name)
			if sec := get(job, "secrets"); sec != nil && sec.Value == "inherit" && !strings.HasPrefix(u.Value, "./") {
				s.add(RuleSecretsInherit, SevMedium, file, sec.Line, name+"\x00"+u.Value,
					"Passes every secret to another repo's workflow", "secrets: inherit → "+u.Value,
					"`secrets: inherit` hands all of this repo's secrets to a workflow you do not control. Pass only the secrets it needs.")
			}
		}
		if forkPR && selfHosted(get(job, "runs-on")) {
			ro := get(job, "runs-on")
			s.add(RuleSelfHostedOnForks, SevMedium, file, ro.Line, name,
				"Runs pull request code on a self-hosted runner", "runs-on: self-hosted",
				"Anyone who opens a pull request can run code on this machine. Use GitHub-hosted runners for pull requests, or require approval for outside contributors.")
		}
		eachItem(get(job, "steps"), func(step *yaml.Node) { s.step(file, name, step, privileged) })
	})
	if topPerms == nil && jobsWithoutPerms > 0 {
		line := 1
		if jobs != nil {
			line = max(jobs.Line-1, 1)
		}
		s.add(RuleDefaultPerms, SevLow, file, line, "", "No permissions block, so the token gets the default scopes",
			"no top-level permissions:",
			"Without `permissions:` the job token can have write access to the repo. Add `permissions: contents: read` at the top and grant more per job.")
	}
}

func (s *actionsScanner) action(file string, root *yaml.Node) {
	runs := get(root, "runs")
	if runs == nil {
		return
	}
	if u := get(runs, "image"); u != nil && strings.HasPrefix(u.Value, "docker://") {
		s.uses(file, u, "action")
	}
	eachItem(get(runs, "steps"), func(step *yaml.Node) { s.step(file, "action", step, false) })
}

func (s *actionsScanner) step(file, job string, step *yaml.Node, privileged bool) {
	if step.Kind != yaml.MappingNode {
		return
	}
	s.insecureEnv(file, get(step, "env"), "step in "+job)
	uses := get(step, "uses")
	if uses != nil && uses.Kind == yaml.ScalarNode {
		s.uses(file, uses, job)
		name, _, _ := strings.Cut(strings.ToLower(uses.Value), "@")
		if privileged && name == "actions/checkout" {
			if ref := get(get(step, "with"), "ref"); ref != nil && prHeadRe.MatchString(ref.Value) {
				s.add(RulePRTargetCheckout, SevCritical, file, ref.Line, job+"\x00"+ref.Value,
					"Checks out pull request code in a privileged workflow", "ref: "+ref.Value,
					"pull_request_target and workflow_run jobs have secrets and a write token. Running a fork's code there lets anyone take them. Use the pull_request trigger for building PR code.")
			}
		}
		if name == "actions/github-script" {
			if sc := get(get(step, "with"), "script"); sc != nil {
				s.injection(file, job, sc)
			}
		}
	}
	if run := get(step, "run"); run != nil && run.Kind == yaml.ScalarNode {
		s.injection(file, job, run)
		if privileged && prHeadRe.MatchString(run.Value) &&
			(strings.Contains(run.Value, "git checkout") || strings.Contains(run.Value, "git fetch") ||
				strings.Contains(run.Value, "gh pr checkout")) {
			s.add(RulePRTargetCheckout, SevCritical, file, run.Line, job+"\x00run",
				"Checks out pull request code in a privileged workflow", firstLine(run.Value),
				"pull_request_target and workflow_run jobs have secrets and a write token. Running a fork's code there lets anyone take them. Use the pull_request trigger for building PR code.")
		}
	}
}

// injection reports ${{ }} expressions with attacker-controlled values
// inside a shell script or github-script body.
func (s *actionsScanner) injection(file, job string, n *yaml.Node) {
	for _, loc := range exprRe.FindAllStringIndex(n.Value, -1) {
		expr := n.Value[loc[0]:loc[1]]
		matched := false
		for _, re := range s.pack.Actions.untrusted {
			if re.MatchString(expr) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		sev := SevHigh
		if inputsRe.MatchString(expr) {
			sev = SevMedium // only people who can run the workflow set inputs
		}
		line := n.Line + strings.Count(n.Value[:loc[0]], "\n")
		if n.Style&(yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			line++ // the value starts on the line after `run: |`
		}
		s.add(RuleScriptInjection, sev, file, line, job+"\x00"+expr,
			"Puts untrusted input straight into a script", expr,
			"Whoever writes this value (an issue title, a branch name) can run commands in the job. Pass it through `env:` and use \"$VAR\" in the script instead.")
	}
}

func (s *actionsScanner) uses(file string, n *yaml.Node, where string) {
	v := strings.TrimSpace(n.Value)
	switch {
	case v == "" || strings.HasPrefix(v, "./") || strings.HasPrefix(v, "${{"):
		return
	case strings.HasPrefix(v, "docker://"):
		if !strings.Contains(v, "@sha256:") {
			s.add(RuleUnpinnedAction, SevLow, file, n.Line, v, "Container image is not pinned to a digest", v,
				"A tag can be moved to a different image. Pin it as image@sha256:<digest>.")
		}
		return
	}
	name, ref, ok := strings.Cut(v, "@")
	if !ok {
		return
	}
	if shaRef.MatchString(ref) {
		return
	}
	owner, _, _ := strings.Cut(strings.ToLower(name), "/")
	sev := SevMedium
	for _, o := range s.pack.Actions.FirstPartyOwners {
		if owner == strings.ToLower(o) {
			sev = SevLow
		}
	}
	s.add(RuleUnpinnedAction, sev, file, n.Line, v, "Action is not pinned to a commit", v,
		"A tag or branch can be moved to new code at any time. Pin the action to a full commit SHA (keep the version in a comment).")
}

func (s *actionsScanner) permissions(file string, n *yaml.Node, where string) {
	if n != nil && n.Kind == yaml.ScalarNode && strings.EqualFold(n.Value, "write-all") {
		s.add(RuleWriteAll, SevHigh, file, n.Line, where, "Grants the token write access to everything", "permissions: write-all",
			"`write-all` lets any step push code, change releases and more. List only the permissions the job needs.")
	}
}

func (s *actionsScanner) insecureEnv(file string, env *yaml.Node, where string) {
	eachPair(env, func(k string, key, v *yaml.Node) {
		if strings.EqualFold(k, "ACTIONS_ALLOW_UNSECURE_COMMANDS") && v.Kind == yaml.ScalarNode &&
			(strings.EqualFold(v.Value, "true") || v.Value == "1") {
			s.add(RuleInsecureCommands, SevHigh, file, key.Line, where, "Turns the old set-env commands back on",
				"ACTIONS_ALLOW_UNSECURE_COMMANDS: "+v.Value,
				"set-env and add-path were disabled because any log line could set variables. Remove this and use $GITHUB_ENV.")
		}
	})
}

// workflowTriggers lists the events in `on:`.
func workflowTriggers(on *yaml.Node) map[string]bool {
	out := map[string]bool{}
	if on == nil {
		return out
	}
	switch on.Kind {
	case yaml.ScalarNode:
		out[on.Value] = true
	case yaml.SequenceNode:
		for _, c := range on.Content {
			out[c.Value] = true
		}
	case yaml.MappingNode:
		eachPair(on, func(k string, _, _ *yaml.Node) { out[k] = true })
	}
	return out
}

func selfHosted(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return strings.Contains(strings.ToLower(n.Value), "self-hosted")
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if selfHosted(c) {
				return true
			}
		}
	case yaml.MappingNode:
		return selfHosted(get(n, "labels"))
	}
	return false
}

// get returns the value of key in a YAML mapping, or nil.
func get(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func eachPair(m *yaml.Node, fn func(key string, k, v *yaml.Node)) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		fn(m.Content[i].Value, m.Content[i], m.Content[i+1])
	}
}

func eachItem(seq *yaml.Node, fn func(*yaml.Node)) {
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return
	}
	for _, c := range seq.Content {
		fn(c)
	}
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
