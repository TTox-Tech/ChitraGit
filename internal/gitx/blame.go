package gitx

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// DefaultMaxBlameLines is how many lines Blame reads when BlameOptions sets
// no cap. A generated file can have millions; nobody reads that far.
const DefaultMaxBlameLines = 20000

// BlameOptions shapes Blame.
type BlameOptions struct {
	MaxLines int // lines kept; 0 means DefaultMaxBlameLines
}

// BlameLine is one line of a file and the commit that last changed it.
type BlameLine struct {
	Line     int    `json:"line"`      // 1-based, in the blamed revision
	OrigLine int    `json:"orig_line"` // in the commit that wrote it
	SHA      string `json:"sha"`
	Text     string `json:"text"`
}

// BlameCommit is what blame tells about a commit it names.
type BlameCommit struct {
	Author      string `json:"author"`
	AuthorEmail string `json:"author_email"`
	AuthorDate  string `json:"author_date"` // ISO 8601
	Summary     string `json:"summary"`
	Path        string `json:"path"` // the file's name in that commit, after renames
	Boundary    bool   `json:"boundary,omitempty"`
}

// BlameResult is a blamed file. When Truncated, the lines stopped at the cap.
type BlameResult struct {
	Lines     []BlameLine            `json:"lines"`
	Commits   map[string]BlameCommit `json:"commits"`
	Truncated bool                   `json:"truncated,omitempty"`
}

// Blame tells, for each line of path at rev, the commit that last changed
// it, following renames. It reads the object store only, with no textconv.
func (r *Runner) Blame(ctx context.Context, dir, rev, path string, opts BlameOptions) (*BlameResult, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	if !safeTreePath(path) || strings.ContainsAny(path, "\x00\n") {
		return nil, &core.Error{Category: core.CatInternal, Message: "not a path in the repo", Detail: path}
	}
	max := opts.MaxLines
	if max <= 0 {
		max = DefaultMaxBlameLines
	}
	s, err := r.Stream(ctx, dir, "", "blame", "--porcelain", "--no-textconv", rev, "--", path)
	if err != nil {
		return nil, err
	}
	res, rerr := parseBlame(bufio.NewReader(s), max)
	if cerr := s.Close(); rerr == nil {
		rerr = cerr
	}
	if rerr != nil {
		return nil, rerr
	}
	return res, nil
}

// parseBlame reads `git blame --porcelain`: per line a "<sha> <orig> <final>
// [<count>]" header, the commit's headers the first time it appears, then
// the line itself after a tab.
func parseBlame(br *bufio.Reader, max int) (*BlameResult, error) {
	res := &BlameResult{Lines: []BlameLine{}, Commits: map[string]BlameCommit{}}
	bad := errors.New("unexpected git blame output")
	var cur BlameLine
	var c BlameCommit
	var authorTime int64
	var tz string
	for {
		raw, err := br.ReadString('\n')
		if err == io.EOF && raw == "" {
			return res, nil
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		line := strings.TrimSuffix(raw, "\n")
		if text, ok := strings.CutPrefix(line, "\t"); ok {
			if cur.SHA == "" {
				return nil, bad
			}
			if _, seen := res.Commits[cur.SHA]; !seen {
				c.AuthorDate = unixISO(authorTime, tz)
				res.Commits[cur.SHA] = c
			}
			if len(res.Lines) == max {
				res.Truncated = true
				return res, nil
			}
			cur.Text = text
			res.Lines = append(res.Lines, cur)
			cur = BlameLine{}
			continue
		}
		if cur.SHA == "" {
			f := strings.Fields(line)
			if len(f) < 3 || !isHex(f[0]) {
				return nil, bad
			}
			orig, err1 := strconv.Atoi(f[1])
			final, err2 := strconv.Atoi(f[2])
			if err1 != nil || err2 != nil {
				return nil, bad
			}
			cur = BlameLine{SHA: f[0], OrigLine: orig, Line: final}
			if _, seen := res.Commits[cur.SHA]; !seen {
				c, authorTime, tz = BlameCommit{}, 0, ""
			}
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		switch key {
		case "author":
			c.Author = val
		case "author-mail":
			c.AuthorEmail = strings.TrimSuffix(strings.TrimPrefix(val, "<"), ">")
		case "author-time":
			authorTime, _ = strconv.ParseInt(val, 10, 64)
		case "author-tz":
			tz = val
		case "summary":
			c.Summary = val
		case "filename":
			c.Path = val
		case "boundary":
			c.Boundary = true
		}
	}
}

// unixISO formats a git timestamp in the zone it was written in.
func unixISO(sec int64, tz string) string {
	t := time.Unix(sec, 0).UTC()
	if len(tz) == 5 && (tz[0] == '+' || tz[0] == '-') {
		h, err1 := strconv.Atoi(tz[1:3])
		m, err2 := strconv.Atoi(tz[3:5])
		if err1 == nil && err2 == nil {
			off := (h*60 + m) * 60
			if tz[0] == '-' {
				off = -off
			}
			t = t.In(time.FixedZone(tz, off))
		}
	}
	return t.Format(time.RFC3339)
}
