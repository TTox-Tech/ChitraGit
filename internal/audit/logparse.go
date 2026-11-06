package audit

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// logFormat is the commit header logArgs asks git for: one line each, so
// nothing in a commit message can be mistaken for a diff.
const logFormat = "format:commit %H%nauthor %an%ndate %aI"

// fileChange is what one commit added to one file.
type fileChange struct {
	commit, author, date string
	path                 string // new path, '/' separated
	deleted, binary      bool
	hunks                []addedHunk
}

// addedHunk is a run of added lines; newStart is the 1-based line number
// of the first one in the new file.
type addedHunk struct {
	newStart int
	text     string // the added lines, each ending in "\n"
	size     int
}

func (f *fileChange) addedBytes() int64 {
	var n int64
	for _, h := range f.hunks {
		n += int64(h.size)
	}
	return n
}

// parseLog reads `git log -p -U0` output in logFormat and calls yield for
// every file a commit changed, in the order git printed them. It is a
// small, strict reader of the subset of the diff format logArgs produces.
func parseLog(r io.Reader, yield func(*fileChange) error) (commits int, err error) {
	br := bufio.NewReaderSize(r, 256*1024)
	var (
		commit, author, date string
		cur                  *fileChange
		hunk                 *addedHunk
		oldLeft, newLeft     int
		lineNo               int
	)
	flush := func() error {
		if cur == nil {
			return nil
		}
		f := cur
		cur, hunk = nil, nil
		return yield(f)
	}
	for {
		line, rerr := br.ReadString('\n')
		if line == "" && rerr != nil {
			break
		}
		body := strings.TrimSuffix(line, "\n")

		// Inside a hunk, the header's line counts say how many lines belong
		// to it, so a changed line can never be read as a header.
		if hunk != nil && (oldLeft > 0 || newLeft > 0) {
			switch {
			case strings.HasPrefix(body, "+"):
				hunk.text += body[1:] + "\n"
				hunk.size += len(body) - 1
				lineNo++
				newLeft--
			case strings.HasPrefix(body, "-"):
				oldLeft--
			case strings.HasPrefix(body, " "):
				oldLeft--
				newLeft--
				lineNo++
			case strings.HasPrefix(body, `\`): // "\ No newline at end of file"
			default:
				oldLeft, newLeft = 0, 0 // malformed: end the hunk and read the line as a header
				goto header
			}
			if rerr != nil {
				break
			}
			continue
		}
		if hunk != nil && strings.HasPrefix(body, `\`) {
			continue
		}
	header:
		switch {
		case strings.HasPrefix(body, "commit "):
			if err := flush(); err != nil {
				return commits, err
			}
			commit, author, date = strings.TrimPrefix(body, "commit "), "", ""
			commits++
		case strings.HasPrefix(body, "author ") && cur == nil:
			author = strings.TrimPrefix(body, "author ")
		case strings.HasPrefix(body, "date ") && cur == nil:
			date = strings.TrimPrefix(body, "date ")
		case strings.HasPrefix(body, "diff --git "):
			if err := flush(); err != nil {
				return commits, err
			}
			cur = &fileChange{commit: commit, author: author, date: date, path: pathFromDiffGit(body)}
		case cur != nil && strings.HasPrefix(body, "+++ "):
			p := strings.TrimPrefix(body, "+++ ")
			if p == "/dev/null" {
				cur.deleted = true
			} else if name, ok := diffPath(p, "b/"); ok {
				cur.path = name
			}
		case cur != nil && strings.HasPrefix(body, "deleted file mode"):
			cur.deleted = true
		case cur != nil && (strings.HasPrefix(body, "Binary files ") || body == "GIT binary patch"):
			cur.binary = true
		case cur != nil && strings.HasPrefix(body, "@@ "):
			start, oldN, newN, ok := parseHunkHeader(body)
			if !ok {
				break
			}
			cur.hunks = append(cur.hunks, addedHunk{newStart: start})
			hunk = &cur.hunks[len(cur.hunks)-1]
			oldLeft, newLeft, lineNo = oldN, newN, start
		}
		if rerr != nil {
			break
		}
	}
	return commits, flush()
}

// parseHunkHeader reads "@@ -a[,b] +c[,d] @@ ...".
func parseHunkHeader(s string) (newStart, oldN, newN int, ok bool) {
	f := strings.Fields(s)
	if len(f) < 3 || !strings.HasPrefix(f[1], "-") || !strings.HasPrefix(f[2], "+") {
		return 0, 0, 0, false
	}
	_, oldN, ok1 := parseRange(f[1][1:])
	newStart, newN, ok2 := parseRange(f[2][1:])
	return newStart, oldN, newN, ok1 && ok2
}

func parseRange(s string) (start, n int, ok bool) {
	a, b, has := strings.Cut(s, ",")
	start, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, false
	}
	n = 1
	if has {
		if n, err = strconv.Atoi(b); err != nil {
			return 0, 0, false
		}
	}
	return start, n, true
}

// pathFromDiffGit takes the new path from "diff --git a/x b/x". The +++
// line, when present, wins; this covers files with no content lines.
func pathFromDiffGit(s string) string {
	rest := strings.TrimPrefix(s, "diff --git ")
	if strings.HasPrefix(rest, `"`) {
		// Quoted old path: skip it, then read the new one.
		if end := closingQuote(rest); end > 0 {
			if name, ok := diffPath(strings.TrimSpace(rest[end+1:]), "b/"); ok {
				return name
			}
		}
		return ""
	}
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		if name, ok := diffPath(rest[i+1:], "b/"); ok {
			return name
		}
	}
	return ""
}

// diffPath strips git's prefix from a path, unquoting a C-style quoted one.
func diffPath(p, prefix string) (string, bool) {
	if strings.HasPrefix(p, `"`) {
		u, err := strconv.Unquote(p)
		if err != nil {
			return "", false
		}
		p = u
	}
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	return strings.TrimPrefix(p, prefix), true
}

func closingQuote(s string) int {
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '"':
			return i
		}
	}
	return -1
}
