// Package conflict resolves the clashes between a repo's local changes and
// an upstream update (PRD F4).
//
// A sync never leaves a conflict behind: it rolls the repo back and marks
// it "conflict". A resolve session, started on request, applies the update
// again and lays the clashes out in the working tree for the user to
// settle, file by file or hunk by hunk. Finishing keeps the result as
// uncommitted changes; aborting puts the repo back exactly as it was. The
// session reuses the sync's backup ref, stash and marker (syncer.Flow), so
// a crash at any point loses nothing.
package conflict

import (
	"bytes"
	"errors"
	"strings"
)

// Choice says which side wins.
type Choice string

const (
	Mine   Choice = "mine"   // your local changes
	Theirs Choice = "theirs" // the upstream update
	Both   Choice = "both"   // upstream's lines, then yours (text only)
)

// ParseChoice accepts mine, theirs or both.
func ParseChoice(s string) (Choice, bool) {
	switch c := Choice(strings.ToLower(s)); c {
	case Mine, Theirs, Both:
		return c, true
	}
	return "", false
}

// Kinds of conflicted file.
const (
	KindContent       = "content"        // both sides changed the same lines
	KindBinary        = "binary"         // both sides changed a binary file: pick one whole side
	KindDeletedMine   = "deleted_mine"   // you deleted it, upstream changed it
	KindDeletedTheirs = "deleted_theirs" // upstream deleted it, you changed it
	KindAddedBoth     = "added_both"     // your new (untracked) file and an upstream file share a path
)

// File is one conflicted file of a session.
type File struct {
	Path     string `json:"path"` // relative to the repo, '/' separated
	Kind     string `json:"kind"`
	Hunks    int    `json:"hunks"` // unresolved hunks still in the file (text files)
	Resolved bool   `json:"resolved"`
}

// Hunk is one conflicting region of a text file.
type Hunk struct {
	Line   int    `json:"line"`   // 1-based line of its <<<<<<< marker
	Theirs string `json:"theirs"` // upstream's lines
	Base   string `json:"base"`   // the lines both started from (may be empty)
	Mine   string `json:"mine"`   // your lines
	raw    string // the hunk with its markers, as in the file
}

// Segment is a run of a conflicted file: either text both sides agree on,
// or a hunk.
type Segment struct {
	Text string
	Hunk *Hunk
}

var errMalformed = errors.New("conflict markers are incomplete")

// Parse splits a file with conflict markers (diff3 or merge style) into
// segments. Line endings are kept as they are.
func Parse(content []byte) ([]Segment, error) {
	const (
		outside = iota
		inTheirs
		inBase
		inMine
	)
	var segs []Segment
	var text, raw strings.Builder
	var h *Hunk
	var side *strings.Builder
	var theirs, base, mine strings.Builder
	state, line := outside, 0
	for _, l := range strings.SplitAfter(string(content), "\n") {
		if l == "" {
			continue
		}
		line++
		switch m := markerOf(l); {
		case state == outside && m == '<':
			if text.Len() > 0 {
				segs = append(segs, Segment{Text: text.String()})
				text.Reset()
			}
			h, state, side = &Hunk{Line: line}, inTheirs, &theirs
			raw.Reset()
			theirs.Reset()
			base.Reset()
			mine.Reset()
			raw.WriteString(l)
		case state == outside:
			text.WriteString(l)
		case m == '|' && state == inTheirs:
			state, side = inBase, &base
			raw.WriteString(l)
		case m == '=' && (state == inTheirs || state == inBase):
			state, side = inMine, &mine
			raw.WriteString(l)
		case m == '>' && state == inMine:
			raw.WriteString(l)
			h.Theirs, h.Base, h.Mine, h.raw = theirs.String(), base.String(), mine.String(), raw.String()
			segs = append(segs, Segment{Hunk: h})
			state = outside
		case m != 0:
			return nil, errMalformed
		default:
			side.WriteString(l)
			raw.WriteString(l)
		}
	}
	if state != outside {
		return nil, errMalformed
	}
	if text.Len() > 0 {
		segs = append(segs, Segment{Text: text.String()})
	}
	return segs, nil
}

// markerOf returns '<', '|', '=' or '>' when line is a conflict marker.
func markerOf(line string) byte {
	l := strings.TrimRight(line, "\r\n")
	if len(l) < 7 {
		return 0
	}
	c := l[0]
	if c != '<' && c != '|' && c != '=' && c != '>' || strings.Count(l[:7], string(c)) != 7 {
		return 0
	}
	if c == '=' {
		if l == "=======" {
			return c
		}
		return 0
	}
	if len(l) == 7 || l[7] == ' ' {
		return c
	}
	return 0
}

// Hunks returns the hunks of parsed segments.
func Hunks(segs []Segment) []Hunk {
	var hs []Hunk
	for _, s := range segs {
		if s.Hunk != nil {
			hs = append(hs, *s.Hunk)
		}
	}
	return hs
}

// Resolve replaces hunk i (0-based, counting hunks only) with the chosen
// side. Other hunks keep their markers.
func Resolve(segs []Segment, i int, c Choice) bool {
	n := 0
	for k, s := range segs {
		if s.Hunk == nil {
			continue
		}
		if n == i {
			segs[k] = Segment{Text: pick(s.Hunk, c)}
			return true
		}
		n++
	}
	return false
}

func pick(h *Hunk, c Choice) string {
	switch c {
	case Mine:
		return h.Mine
	case Theirs:
		return h.Theirs
	}
	return h.Theirs + h.Mine
}

// Render joins segments back into file content.
func Render(segs []Segment) []byte {
	var b bytes.Buffer
	for _, s := range segs {
		if s.Hunk != nil {
			b.WriteString(s.Hunk.raw)
		} else {
			b.WriteString(s.Text)
		}
	}
	return b.Bytes()
}

// isBinary uses git's own test: a NUL byte in the first 8000 bytes.
func isBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}
