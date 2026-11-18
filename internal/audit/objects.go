package audit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// Objects is where a commit scan reads one commit from: the local object
// store, or a provider's API before anything is downloaded (Shield L0,
// CG-42.16). Every check of ScanObjects reads through it, so both places
// give the same findings for the same tree.
type Objects interface {
	// Tree lists every entry of the commit's tree, recursively. Paths that
	// could leave the repo are left out, as gitx.Tree does.
	Tree(ctx context.Context) ([]gitx.TreeEntry, error)
	// Blobs reads shas in order and calls fn for each. Missing objects and
	// objects larger than maxSize (0 = no limit) are skipped.
	Blobs(ctx context.Context, shas []string, maxSize int64, fn func(gitx.Blob) error) error
	// Submodules reads the .gitmodules blob sha as "submodule.<name>.<key>"
	// and value pairs, in file order.
	Submodules(ctx context.Context, sha string) ([][2]string, error)
}

// GitObjects reads commit from the git dir at dir.
func GitObjects(git *gitx.Runner, dir, commit string) Objects {
	return gitObjects{git: git, dir: dir, commit: commit}
}

type gitObjects struct {
	git         *gitx.Runner
	dir, commit string
}

func (g gitObjects) Tree(ctx context.Context) ([]gitx.TreeEntry, error) {
	return g.git.Tree(ctx, g.dir, g.commit, gitx.TreeOptions{})
}

func (g gitObjects) Blobs(ctx context.Context, shas []string, maxSize int64, fn func(gitx.Blob) error) error {
	return g.git.Blobs(ctx, g.dir, shas, maxSize, fn)
}

// Submodules uses git's own config parser.
func (g gitObjects) Submodules(ctx context.Context, sha string) ([][2]string, error) {
	res, err := g.git.Run(ctx, g.dir, "config", "--blob", sha, "--list", "-z")
	if err != nil {
		return nil, err
	}
	var out [][2]string
	for item := range bytes.SplitSeq(res.Stdout, []byte{0}) {
		key, value, _ := strings.Cut(string(item), "\n")
		if key != "" {
			out = append(out, [2]string{key, value})
		}
	}
	return out, nil
}

// ErrIncomplete is an Objects that cannot read what a scan needs (an API
// out of budget or offline). The scan stops with it instead of reporting
// the file as unreadable.
var ErrIncomplete = errors.New("objects incomplete")

// errConfig is a .gitmodules ParseGitConfig cannot read.
var errConfig = errors.New("not a git config file")

// ParseGitConfig reads a git config file (such as .gitmodules) without git,
// as "section.subsection.key" and value pairs like `git config --list`
// gives them. It is for reading a file from a provider API, where there is
// no git to ask; anything it is unsure about is an error, so the caller
// treats the file as unreadable rather than as harmless.
func ParseGitConfig(data []byte) ([][2]string, error) {
	var out [][2]string
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	pending := ""
	for sc.Scan() {
		line := pending + sc.Text()
		pending = ""
		if strings.HasSuffix(line, "\\") && !strings.HasSuffix(line, "\\\\") {
			pending = strings.TrimSuffix(line, "\\")
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			s, rest, err := configSection(line)
			if err != nil {
				return nil, err
			}
			section = s
			if line = strings.TrimSpace(rest); line == "" || line[0] == '#' || line[0] == ';' {
				continue
			}
		}
		if section == "" {
			return nil, errConfig
		}
		name, value, hasValue := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !configName(name) {
			return nil, errConfig
		}
		v := "" // a bare key (a true boolean) has no value, as git lists it
		if hasValue {
			var err error
			if v, err = configValue(value); err != nil {
				return nil, err
			}
		}
		out = append(out, [2]string{section + "." + strings.ToLower(name), v})
	}
	if err := sc.Err(); err != nil || pending != "" {
		return nil, errConfig
	}
	return out, nil
}

// configSection reads `[name]` or `[name "subsection"]` and returns the
// key prefix and what follows the closing bracket.
func configSection(line string) (string, string, error) {
	end := -1
	inQuote := false
	for i := 1; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && inQuote:
			i++
		case c == '"':
			inQuote = !inQuote
		case c == ']' && !inQuote:
			end = i
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", "", errConfig
	}
	head := strings.TrimSpace(line[1:end])
	name, sub, hasSub := strings.Cut(head, " ")
	if name == "" || strings.ContainsFunc(name, func(r rune) bool {
		return !(r < 0x80 && (isLetter(byte(r)) || r >= '0' && r <= '9' || r == '-' || r == '.'))
	}) {
		return "", "", errConfig
	}
	key := strings.ToLower(name)
	if hasSub {
		sub = strings.TrimSpace(sub)
		if len(sub) < 2 || sub[0] != '"' || sub[len(sub)-1] != '"' {
			return "", "", errConfig
		}
		var b strings.Builder
		for i := 1; i < len(sub)-1; i++ {
			c := sub[i]
			if c == '\\' && i+1 < len(sub)-1 {
				i++
				c = sub[i]
			} else if c == '"' || c == '\n' || c == 0 {
				return "", "", errConfig
			}
			b.WriteByte(c)
		}
		key += "." + b.String()
	}
	return key, line[end+1:], nil
}

// configName is a variable name git accepts: a letter, then letters,
// digits and "-".
func configName(s string) bool {
	if s == "" || !isLetter(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if c := s[i]; !isLetter(c) && !(c >= '0' && c <= '9') && c != '-' {
			return false
		}
	}
	return true
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// configValue unquotes a value, drops a trailing comment and handles the
// escapes git knows. An unknown escape or an open quote is an error.
func configValue(s string) (string, error) {
	var b strings.Builder
	inQuote := false
	space := 0 // spaces seen outside quotes, kept only if more text follows
	s = strings.TrimLeft(s, " \t")
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case !inQuote && (c == '#' || c == ';'):
			i = len(s)
			continue
		case c == '"':
			inQuote = !inQuote
			continue
		case c == '\\':
			if i+1 >= len(s) {
				return "", errConfig
			}
			i++
			switch s[i] {
			case '\\', '"':
				c = s[i]
			case 'n':
				c = '\n'
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			default:
				return "", errConfig
			}
		case !inQuote && (c == ' ' || c == '\t'):
			space++
			continue
		case c == 0:
			return "", errConfig
		}
		if space > 0 {
			b.WriteString(strings.Repeat(" ", space))
			space = 0
		}
		b.WriteByte(c)
	}
	if inQuote {
		return "", errConfig
	}
	return b.String(), nil
}
