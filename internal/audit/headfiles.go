package audit

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// HeadFile is one file at HEAD, read straight from the object store.
type HeadFile struct {
	Path string // slash-separated, relative to the repo root
	Data []byte
}

// HeadCommitOf returns the commit HEAD points to, or "" for an empty repo or
// an unborn branch.
func HeadCommitOf(ctx context.Context, git *gitx.Runner, dir string) string {
	head, err := git.Output(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(head)
}

// ReadHeadFiles reads the files at commit whose path match accepts. It
// never touches a work tree, so it works the same on bare and mirror
// repos. Symlinks, submodules, binary files and files larger than maxSize
// bytes (0 = no limit) are left out; skipped counts the too-large ones.
func ReadHeadFiles(ctx context.Context, git *gitx.Runner, dir, commit string, match func(string) bool, maxSize int64) (files []HeadFile, skipped int, err error) {
	tree, err := git.Tree(ctx, dir, commit, gitx.TreeOptions{})
	if err != nil {
		return nil, 0, err
	}
	var blobs []gitx.TreeEntry
	for _, e := range tree {
		if !e.Regular() || !match(e.Path) {
			continue
		}
		if maxSize > 0 && e.Size > maxSize {
			skipped++
			continue
		}
		blobs = append(blobs, e)
	}
	err = git.Blobs(ctx, dir, shasOf(blobs), 0, func(b gitx.Blob) error {
		if !b.Binary {
			files = append(files, HeadFile{Path: blobs[b.Index].Path, Data: b.Data})
		}
		return nil
	})
	if err != nil {
		return nil, skipped, err
	}
	return files, skipped, nil
}

func shasOf(entries []gitx.TreeEntry) []string {
	shas := make([]string, len(entries))
	for i, e := range entries {
		shas[i] = e.SHA
	}
	return shas
}

// lineAt returns the 1-based line number of byte offset off in data.
func lineAt(data []byte, off int) int {
	if off > len(data) {
		off = len(data)
	}
	return bytes.Count(data[:off], []byte{'\n'}) + 1
}

// snippet shortens matched text for a finding preview: one line, at most
// n runes, control characters shown as U+XXXX.
func snippet(s string, n int) string {
	var b strings.Builder
	count := 0
	for _, r := range s {
		if count >= n {
			b.WriteString("…")
			break
		}
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteRune(' ')
		case invisible(r):
			fmt.Fprintf(&b, "<U+%04X>", r)
		default:
			b.WriteRune(r)
		}
		count++
	}
	return strings.TrimSpace(b.String())
}
