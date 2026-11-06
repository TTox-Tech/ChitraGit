package audit

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

// Rules of the agents check written in Go. The content rules come from
// the rules pack.
const (
	RuleMCPUnpinned   = "agent-mcp-unpinned-package"
	RuleMCPPlainHTTP  = "agent-mcp-plain-http"
	RuleMCPInlineCode = "agent-mcp-inline-script"
)

// AgentFiles keeps only the agent files among files, as the pack defines
// them, with their kinds.
func (p *RulesPack) AgentFiles(files []HeadFile) []AgentHit {
	var out []AgentHit
	for _, f := range files {
		if spec, ok := p.AgentFile(f.Path); ok {
			out = append(out, AgentHit{File: f, Spec: spec})
		}
	}
	return out
}

// AgentHit is an agent file and what the pack says it is.
type AgentHit struct {
	File HeadFile
	Spec AgentFileSpec
}

// ScanAgents runs the pack's content rules and the MCP config rules over
// the agent files among files.
func ScanAgents(pack *RulesPack, repoID, repoPath, commit string, files []HeadFile) []Finding {
	seen := map[string]bool{}
	var out []Finding
	add := func(rule, title string, sev Severity, conf, file string, line int, key, preview, hint string) {
		sev, ok := apply(pack.Agents.Rules, rule, sev)
		if !ok {
			return
		}
		id := FindingID(repoID, CheckAgents, rule, file+"\x00"+key)
		if seen[id] {
			for i := range out {
				if out[i].ID == id {
					out[i].Occurrences++
				}
			}
			return
		}
		seen[id] = true
		out = append(out, Finding{
			ID: id, RepoID: repoID, RepoPath: repoPath, Check: CheckAgents, Rule: rule, Title: title,
			Severity: sev, Confidence: conf, File: file, Line: line, Commit: commit,
			Preview: preview, Occurrences: 1, Hint: hint, Status: StatusOpen,
		})
	}
	for _, h := range pack.AgentFiles(files) {
		data := h.File.Data
		text := string(data)
		for _, r := range pack.compiled {
			if len(r.Kinds) > 0 && !slices.Contains(r.Kinds, h.Spec.Kind) {
				continue
			}
			for _, loc := range r.re.FindAllStringIndex(text, 20) {
				m := text[loc[0]:loc[1]]
				preview := snippet(m, 60)
				key := strings.ToLower(m)
				if len(m) > 120 {
					key = Fingerprint(m) // long blobs: keep the ID short and stable
					preview = snippet(m, 24)
				}
				add(r.ID, r.Title, r.Severity, r.Confidence, h.File.Path, lineAt(data, loc[0]), key, preview, r.Hint)
			}
		}
		if h.Spec.Kind == KindMCP || h.Spec.Kind == KindSettings {
			for _, srv := range mcpServers(data) {
				line := lineOf(data, srv.name)
				if pkg := unpinnedPackage(srv.command, srv.args); pkg != "" {
					add(RuleMCPUnpinned, "MCP server installs an unpinned package each run", SevLow, ConfidenceHigh,
						h.File.Path, line, srv.name+"\x00"+pkg, srv.name+": "+pkg,
						"The package is fetched fresh whenever the agent starts, so a hijacked release runs on your machine. Pin an exact version.")
				}
				if u, err := url.Parse(srv.url); err == nil && u.Scheme == "http" && !localHost(u.Hostname()) {
					add(RuleMCPPlainHTTP, "MCP server is reached over plain HTTP", SevMedium, ConfidenceHigh,
						h.File.Path, line, srv.name+"\x00"+srv.url, srv.name+": "+snippet(srv.url, 60),
						"Anyone on the network can read or change what the agent sends and receives. Use https.")
				}
				if inlineScript(srv.command, srv.args) {
					add(RuleMCPInlineCode, "MCP server runs an inline script", SevMedium, ConfidenceLow,
						h.File.Path, line, srv.name, srv.name+": "+snippet(srv.command+" "+strings.Join(srv.args, " "), 60),
						"The server is a command line instead of a package or binary, which is a common way to hide what it does. Read the whole command.")
				}
			}
		}
	}
	sortFindings(out)
	return out
}

type mcpServer struct {
	name, command, url string
	args               []string
}

// mcpServers reads MCP server entries from the config shapes agents use:
// mcpServers / servers / mcp / context_servers, each a map of name to
// {command, args, url}. Anything it cannot parse is skipped.
func mcpServers(data []byte) []mcpServer {
	var doc map[string]json.RawMessage
	if json.Unmarshal(stripJSONComments(data), &doc) != nil {
		return nil
	}
	var out []mcpServer
	for _, key := range []string{"mcpServers", "servers", "mcp", "context_servers"} {
		raw, ok := doc[key]
		if !ok {
			continue
		}
		var m map[string]map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		for name, v := range m {
			s := mcpServer{name: name}
			switch c := v["command"].(type) {
			case string:
				s.command = c
			case []any: // {"command": ["npx", "-y", "pkg"]}
				for i, x := range c {
					if str, ok := x.(string); ok {
						if i == 0 {
							s.command = str
						} else {
							s.args = append(s.args, str)
						}
					}
				}
			case map[string]any: // {"command": {"path": "...", "args": [...]}}
				s.command, _ = c["path"].(string)
				s.args = append(s.args, strs(c["args"])...)
			}
			s.args = append(s.args, strs(v["args"])...)
			for _, k := range []string{"url", "serverUrl", "httpUrl"} {
				if u, ok := v[k].(string); ok {
					s.url = u
				}
			}
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b mcpServer) int { return strings.Compare(a.name, b.name) })
	return out
}

func strs(v any) []string {
	a, _ := v.([]any)
	var out []string
	for _, x := range a {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

var (
	npmVersioned = regexp.MustCompile(`^(@[^/]+/)?[^@]+@[0-9]`)
	pyVersioned  = regexp.MustCompile(`(==|@)[0-9]`)
)

// unpinnedPackage returns the package a launcher such as npx or uvx would
// fetch without an exact version, or "".
func unpinnedPackage(command string, args []string) string {
	cmd := strings.ToLower(baseName(command))
	cmd = strings.TrimSuffix(cmd, ".cmd")
	cmd = strings.TrimSuffix(cmd, ".exe")
	rest := args
	switch cmd {
	case "npx", "bunx", "uvx", "pipx":
	case "pnpm", "yarn", "bun", "uv":
		if len(rest) == 0 || !oneOf(rest[0], []string{"dlx", "x", "tool"}) {
			return ""
		}
		rest = rest[1:]
		if cmd == "uv" && len(rest) > 0 && rest[0] == "run" {
			rest = rest[1:]
		}
	default:
		return ""
	}
	if cmd == "pipx" {
		if len(rest) == 0 || rest[0] != "run" {
			return ""
		}
		rest = rest[1:]
	}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if strings.HasPrefix(a, "-") {
			if (a == "--from" || a == "--package" || a == "-p" || a == "--with") && i+1 < len(rest) {
				i++
				if !versioned(cmd, rest[i]) {
					return rest[i]
				}
				return ""
			}
			continue
		}
		if strings.ContainsAny(a, "/\\") && !strings.HasPrefix(a, "@") {
			return "" // a local path or URL
		}
		if versioned(cmd, a) {
			return ""
		}
		return a
	}
	return ""
}

func versioned(cmd, pkg string) bool {
	if strings.HasSuffix(pkg, "@latest") {
		return false
	}
	switch cmd {
	case "uvx", "uv", "pipx":
		return pyVersioned.MatchString(pkg)
	}
	return npmVersioned.MatchString(pkg)
}

// inlineScript reports a server started as `sh -c "…"`, `node -e "…"` and
// similar.
func inlineScript(command string, args []string) bool {
	cmd := strings.ToLower(strings.TrimSuffix(baseName(command), ".exe"))
	flags := map[string][]string{
		"sh": {"-c"}, "bash": {"-c"}, "zsh": {"-c"}, "cmd": {"/c", "/k"},
		"powershell": {"-command", "-c", "-encodedcommand", "-e"}, "pwsh": {"-command", "-c", "-encodedcommand", "-e"},
		"node": {"-e", "--eval"}, "python": {"-c"}, "python3": {"-c"}, "deno": {"eval"}, "bun": {"-e", "--eval"},
	}
	want, ok := flags[cmd]
	if !ok {
		return false
	}
	for _, a := range args {
		if oneOf(strings.ToLower(a), want) {
			return true
		}
	}
	return false
}

func baseName(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func localHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1" || strings.HasSuffix(h, ".localhost")
}

// lineOf finds the line where "name" first appears as a JSON key.
func lineOf(data []byte, name string) int {
	key, _ := json.Marshal(name)
	if i := strings.Index(string(data), string(key)); i >= 0 {
		return lineAt(data, i)
	}
	return 0
}

// stripJSONComments removes // and /* */ comments outside strings, so
// JSONC configs (VS Code, Zed) parse.
func stripJSONComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inStr {
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '/' {
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, '\n')
			}
			continue
		}
		if c == '/' && i+1 < len(b) && b[i+1] == '*' {
			i += 2
			for i+1 < len(b) && (b[i] != '*' || b[i+1] != '/') {
				if b[i] == '\n' {
					out = append(out, '\n')
				}
				i++
			}
			i++
			continue
		}
		out = append(out, c)
	}
	return trailingCommas.ReplaceAll(out, []byte("$1"))
}

var trailingCommas = regexp.MustCompile(`,(\s*[}\]])`)

// invisible reports characters that render as nothing or reorder text.
func invisible(r rune) bool {
	switch {
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069,
		r >= 0x200B && r <= 0x200D, r >= 0x2060 && r <= 0x2064,
		r >= 0xE0000 && r <= 0xE007F, r == 0xFEFF:
		return true
	}
	return false
}

// sortFindings orders findings most severe first, then by file and line.
func sortFindings(fs []Finding) {
	slices.SortStableFunc(fs, func(a, b Finding) int {
		if c := b.Severity.Rank() - a.Severity.Rank(); c != 0 {
			return c
		}
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.ID, b.ID)
	})
}
