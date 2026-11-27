package gitx

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// DefaultMaxGrep is how many matches Grep keeps when GrepOptions sets none.
const DefaultMaxGrep = 1000

// maxGrepText is how much of a matching line Grep keeps: a minified file
// is one line of megabytes.
const maxGrepText = 1000

// GrepOptions shapes Grep.
type GrepOptions struct {
	Fixed      bool     // the pattern is a plain string, not a regular expression
	IgnoreCase bool     // match without regard to case
	Paths      []string // pathspecs to search in; none means the whole tree
	MaxMatches int      // matches kept; 0 means DefaultMaxGrep
}

// GrepMatch is one matching line.
type GrepMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// GrepResult lists matches in tree order. When Truncated, more were left.
type GrepResult struct {
	Matches   []GrepMatch `json:"matches"`
	Truncated bool        `json:"truncated,omitempty"`
}

// Grep searches the tree of rev for pattern, read from the object store, so
// it works on bare repos. Binary files are skipped. A pattern is an
// extended regular expression unless opts.Fixed.
func (r *Runner) Grep(ctx context.Context, dir, rev, pattern string, opts GrepOptions) (*GrepResult, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	if pattern == "" || strings.ContainsAny(pattern, "\x00\n") {
		return nil, &core.Error{Category: core.CatInternal, Message: "not a search pattern", Detail: pattern}
	}
	args := []string{"grep", "-z", "-n", "-I", "--no-color", "--full-name"}
	if opts.Fixed {
		args = append(args, "-F")
	} else {
		args = append(args, "-E")
	}
	if opts.IgnoreCase {
		args = append(args, "-i")
	}
	args = append(args, "-e", pattern, rev, "--")
	for _, p := range opts.Paths {
		if p == "" || strings.ContainsAny(p, "\x00\n") {
			return nil, &core.Error{Category: core.CatInternal, Message: "not a path in the repo", Detail: p}
		}
		args = append(args, p)
	}
	max := opts.MaxMatches
	if max <= 0 {
		max = DefaultMaxGrep
	}
	s, err := r.Stream(ctx, dir, "", args...)
	if err != nil {
		return nil, err
	}
	res, rerr := parseGrep(bufio.NewReader(s), rev+":", max)
	cerr := s.Close()
	if rerr != nil {
		return nil, rerr
	}
	// git grep exits 1, saying nothing, when nothing matched.
	if cerr != nil && (len(res.Matches) > 0 || s.ExitCode() != 1) {
		return nil, cerr
	}
	return res, nil
}

// parseGrep reads `git grep -z -n <rev>`: "<rev>:<path>\0<line>\0<text>\n".
func parseGrep(br *bufio.Reader, prefix string, max int) (*GrepResult, error) {
	res := &GrepResult{Matches: []GrepMatch{}}
	for {
		raw, err := br.ReadBytes('\n')
		if len(raw) > 0 {
			name, rest, ok1 := bytes.Cut(raw, []byte{0})
			num, text, ok2 := bytes.Cut(rest, []byte{0})
			n, nerr := strconv.Atoi(string(num))
			if !ok1 || !ok2 || nerr != nil {
				return nil, &core.Error{Category: core.CatInternal, Message: "unexpected git grep output", Detail: string(raw[:min(len(raw), 200)])}
			}
			if len(res.Matches) == max {
				res.Truncated = true
				return res, nil
			}
			text = bytes.TrimSuffix(text, []byte{'\n'})
			if len(text) > maxGrepText {
				text = text[:maxGrepText]
			}
			res.Matches = append(res.Matches, GrepMatch{Path: strings.TrimPrefix(string(name), prefix), Line: n, Text: strings.ToValidUTF8(string(text), "�")})
		}
		if err == io.EOF {
			return res, nil
		}
		if err != nil {
			return nil, err
		}
	}
}
