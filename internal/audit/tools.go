package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/platform"
)

// External scanners KodeKavach can run when the user has installed them.
const (
	ToolZizmor       = "zizmor"
	ToolSkillSpector = "skillspector"
)

// Tool modes, from [audit.tools] in the config.
const (
	ToolAuto = "auto" // run it if it is installed
	ToolOn   = "on"   // run it; a missing tool is an error
	ToolOff  = "off"
)

// Tool is an installed external scanner.
type Tool struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Version string `json:"version,omitempty"`
}

// ErrToolMissing means a tool set to "on" is not installed.
var ErrToolMissing = errors.New("not installed")

// FindTool looks for a scanner on PATH according to mode. It returns nil
// (and no error) when the tool is off, or set to auto and not installed.
func FindTool(ctx context.Context, name, mode string) (*Tool, error) {
	if mode == ToolOff {
		return nil, nil
	}
	p, err := exec.LookPath(name)
	if err != nil {
		if mode == ToolOn {
			return nil, fmt.Errorf("%s: %w (audit.tools.%s = \"on\")", name, ErrToolMissing, name)
		}
		return nil, nil
	}
	t := &Tool{Name: name, Path: p}
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if out, err := toolCommand(vctx, p, "", "--version").Output(); err == nil {
		t.Version = strings.TrimSpace(firstLine(string(out)))
		t.Version = strings.TrimSpace(strings.TrimPrefix(t.Version, name))
	}
	return t, nil
}

// toolTimeout bounds one external scan of one repo.
const toolTimeout = 3 * time.Minute

// deadProxy is a local address nothing listens on. Pointing the tools'
// proxy variables at it stops any lookup they would make over the network
// (SkillSpector's OSV query, for one), so repo data stays on the machine.
const deadProxy = "http://127.0.0.1:9"

// toolCommand builds a command with no shell, a scrubbed environment
// (no provider tokens or API keys) and network access pointed at a dead
// proxy. dir is the working directory and the tool's HOME.
func toolCommand(ctx context.Context, bin, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	platform.KillTreeOnCancel(cmd)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = toolEnv(dir)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd
}

func toolEnv(dir string) []string {
	keep := []string{"PATH", "LANG", "LC_ALL", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "SYSTEMDRIVE", "PROGRAMDATA", "APPDATA", "LOCALAPPDATA"}
	var env []string
	for _, k := range keep {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	if dir != "" {
		home := filepath.Join(dir, ".home")
		_ = os.MkdirAll(home, 0o700)
		env = append(env, "HOME="+home, "USERPROFILE="+home, "TMPDIR="+dir, "TEMP="+dir, "TMP="+dir, "XDG_CACHE_HOME="+filepath.Join(home, ".cache"))
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		env = append(env, k+"="+deadProxy)
	}
	return append(env, "NO_PROXY=", "no_proxy=", "NO_COLOR=1", "ZIZMOR_OFFLINE=true", "PYTHONDONTWRITEBYTECODE=1")
}

// exportFiles writes files into a new temporary folder, keeping their
// repo paths, so a tool can read them. The repo itself is never touched.
func exportFiles(files []HeadFile) (string, error) {
	dir, err := os.MkdirTemp("", "chitragit-kodekavach-")
	if err != nil {
		return "", err
	}
	work := filepath.Join(dir, "repo")
	for _, f := range files {
		clean := path.Clean("/" + f.Path)[1:]
		if clean == "" || clean != f.Path {
			continue // never write outside the folder
		}
		p := filepath.Join(work, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		if err := os.WriteFile(p, f.Data, 0o600); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}
	return dir, nil
}

// RunZizmor runs zizmor offline over the repo's workflow files and turns
// its SARIF output into findings of the actions check.
func RunZizmor(ctx context.Context, t *Tool, repoID, repoPath, commit string, files []HeadFile) ([]Finding, error) {
	var wf []HeadFile
	for _, f := range files {
		if IsWorkflowFile(f.Path) {
			wf = append(wf, f)
		}
	}
	if len(wf) == 0 {
		return nil, nil
	}
	dir, err := exportFiles(wf)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	// --no-config and --no-ignores: an archived repo must not be able to
	// switch rules off for itself.
	cmd := toolCommand(ctx, t.Path, dir, "--offline", "--no-config", "--no-ignores", "--no-exit-codes",
		"--no-progress", "--color", "never", "--format", "sarif", "repo")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, toolError(t, ctx, err, stderr.Bytes())
	}
	return importSARIF(out, CheckActions, repoID, repoPath, commit, "repo/")
}

// RunSkillSpector runs SkillSpector's static analysis (no LLM) over the
// repo's skill folders.
func RunSkillSpector(ctx context.Context, t *Tool, repoID, repoPath, commit string, files []HeadFile) ([]Finding, error) {
	if len(files) == 0 {
		return nil, nil
	}
	dir, err := exportFiles(files)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	report := filepath.Join(dir, "report.sarif")
	cmd := toolCommand(ctx, t.Path, dir, "scan", "repo", "--recursive", "--no-llm", "--format", "sarif", "--output", report)
	cmd.Env = append(cmd.Env, "SKILLSPECTOR_MAX_WORKFLOW_SECONDS="+strconv.Itoa(int(toolTimeout/time.Second)-10))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	out, readErr := os.ReadFile(report)
	if readErr != nil || len(out) == 0 {
		// It exits non-zero when it finds something, so only a missing
		// report means it failed.
		if runErr == nil {
			runErr = errors.New("no report written")
		}
		return nil, toolError(t, ctx, runErr, stderr.Bytes())
	}
	return importSARIF(out, CheckAgents, repoID, repoPath, commit, "repo/")
}

func toolError(t *Tool, ctx context.Context, err error, stderr []byte) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s took longer than %s and was stopped", t.Name, toolTimeout)
	}
	msg := strings.TrimSpace(lastLine(string(stderr)))
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Errorf("%s failed: %s", t.Name, snippet(msg, 200))
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// importSARIF turns another tool's SARIF results into findings. Rule IDs
// keep the tool's own prefix (zizmor/…); a bare ID gets the tool's name.
// prefix is stripped from file URIs so paths are relative to the repo.
func importSARIF(data []byte, check, repoID, repoPath, commit, prefix string) ([]Finding, error) {
	var log struct {
		Runs []struct {
			Tool struct {
				Driver struct {
					Name  string `json:"name"`
					Rules []struct {
						ID               string                `json:"id"`
						ShortDescription struct{ Text string } `json:"shortDescription"`
						Help             struct{ Text string } `json:"help"`
						HelpURI          string                `json:"helpUri"`
						Properties       map[string]any        `json:"properties"`
					} `json:"rules"`
				} `json:"driver"`
			} `json:"tool"`
			Results []struct {
				RuleID     string                `json:"ruleId"`
				Level      string                `json:"level"`
				Message    struct{ Text string } `json:"message"`
				Properties map[string]any        `json:"properties"`
				Locations  []struct {
					PhysicalLocation struct {
						ArtifactLocation struct {
							URI string `json:"uri"`
						} `json:"artifactLocation"`
						Region struct {
							StartLine int                   `json:"startLine"`
							Snippet   struct{ Text string } `json:"snippet"`
						} `json:"region"`
					} `json:"physicalLocation"`
				} `json:"locations"`
			} `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &log); err != nil {
		return nil, fmt.Errorf("cannot read the tool's SARIF report: %w", err)
	}
	seen := map[string]int{}
	var out []Finding
	for _, run := range log.Runs {
		tool := strings.ToLower(strings.Fields(run.Tool.Driver.Name + " tool")[0])
		type ruleInfo struct {
			title, help string
			props       map[string]any
		}
		rules := map[string]ruleInfo{}
		for _, r := range run.Tool.Driver.Rules {
			title := r.ShortDescription.Text
			if title == "" {
				title = r.Help.Text
			}
			help := r.HelpURI
			rules[r.ID] = ruleInfo{title: title, help: help, props: r.Properties}
		}
		for _, res := range run.Results {
			rid := res.RuleID
			if rid == "" {
				continue
			}
			if !strings.Contains(rid, "/") {
				rid = tool + "/" + rid
			}
			ri := rules[res.RuleID]
			file, line, snip := "", 0, ""
			if len(res.Locations) > 0 {
				pl := res.Locations[0].PhysicalLocation
				file = strings.TrimPrefix(strings.TrimPrefix(pl.ArtifactLocation.URI, "file://"), "./")
				if i := strings.Index(file, prefix); i >= 0 {
					file = file[i+len(prefix):]
				}
				line, snip = pl.Region.StartLine, pl.Region.Snippet.Text
			}
			title := ri.title
			msg := res.Message.Text
			if title == "" {
				title = msg
			}
			if t, _, ok := strings.Cut(title, ": "); ok && len(t) > 8 {
				title = t
			}
			title = upperFirst(snippet(title, 70))
			sev, conf := sarifSeverity(res.Level, res.Properties, ri.props)
			hint := upperFirst(strings.TrimSuffix(msg, "."))
			if hint != "" {
				hint += "."
			}
			if ri.help != "" {
				hint += " See " + ri.help
			}
			key := file + "\x00" + strconv.Itoa(line) + "\x00" + msg
			id := FindingID(repoID, check, rid, key)
			if i, ok := seen[id]; ok {
				out[i].Occurrences++
				continue
			}
			seen[id] = len(out)
			out = append(out, Finding{
				ID: id, RepoID: repoID, RepoPath: repoPath, Check: check, Rule: rid, Title: title,
				Severity: sev, Confidence: conf, File: file, Line: line, Commit: commit,
				Preview: snippet(firstLine(snip), 80), Occurrences: 1, Hint: strings.TrimSpace(hint), Status: StatusOpen,
			})
		}
	}
	sortFindings(out)
	return out, nil
}

// sarifSeverity reads a tool's own severity property ("zizmor/severity"),
// then a numeric security-severity, then the SARIF level.
func sarifSeverity(level string, props, ruleProps map[string]any) (Severity, string) {
	conf := ConfidenceHigh
	var sev Severity
	for _, m := range []map[string]any{props, ruleProps} {
		for k, v := range m {
			s, _ := v.(string)
			lk := strings.ToLower(k)
			switch {
			case sev == "" && (lk == "severity" || strings.HasSuffix(lk, "/severity")):
				switch strings.ToLower(s) {
				case "critical":
					sev = SevCritical
				case "high", "error":
					sev = SevHigh
				case "medium", "moderate", "warning":
					sev = SevMedium
				case "low":
					sev = SevLow
				case "informational", "info", "note", "unknown":
					sev = SevInfo
				}
			case sev == "" && lk == "security-severity":
				if f, err := strconv.ParseFloat(s, 64); err == nil {
					sev = cvssSeverity(f)
				} else if f, ok := v.(float64); ok {
					sev = cvssSeverity(f)
				}
			case lk == "confidence" || strings.HasSuffix(lk, "/confidence"):
				if strings.EqualFold(s, "low") {
					conf = ConfidenceLow
				}
			}
		}
	}
	if sev == "" {
		switch level {
		case "error":
			sev = SevHigh
		case "warning":
			sev = SevMedium
		case "note":
			sev = SevLow
		default:
			sev = SevInfo
		}
	}
	return sev, conf
}

func cvssSeverity(f float64) Severity {
	switch {
	case f >= 9:
		return SevCritical
	case f >= 7:
		return SevHigh
	case f >= 4:
		return SevMedium
	case f > 0:
		return SevLow
	}
	return SevInfo
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] -= 'a' - 'A'
	}
	return string(r)
}

// SkillDirs returns the folders that hold a SKILL.md among the agent
// files, for SkillSpector.
func SkillDirs(hits []AgentHit) []string {
	var dirs []string
	for _, h := range hits {
		if h.Spec.Kind == KindSkill && strings.EqualFold(path.Base(h.File.Path), "skill.md") {
			dirs = append(dirs, path.Dir(h.File.Path))
		}
	}
	return dirs
}

// DropCovered removes built-in findings that a tool reported too, as the
// pack's equivalents map says: same file, equivalent rule.
func DropCovered(builtin, tool []Finding, equivalents map[string][]string) []Finding {
	have := map[string]bool{}
	for _, f := range tool {
		have[f.Rule+"\x00"+f.File] = true
	}
	out := builtin[:0]
	for _, f := range builtin {
		covered := false
		for _, rule := range equivalents[f.Rule] {
			if have[rule+"\x00"+f.File] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, f)
		}
	}
	return out
}
