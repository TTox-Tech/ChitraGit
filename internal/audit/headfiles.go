package audit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
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
	tree, err := git.Run(ctx, dir, "ls-tree", "-r", "-z", "-l", "--full-tree", commit)
	if err != nil {
		return nil, 0, err
	}
	type blob struct{ sha, path string }
	var blobs []blob
	for _, ent := range bytes.Split(tree.Stdout, []byte{0}) {
		meta, p, ok := bytes.Cut(ent, []byte{'\t'})
		if !ok {
			continue
		}
		fields := strings.Fields(string(meta)) // mode type sha size
		if len(fields) != 4 || fields[1] != "blob" || fields[0] == "120000" {
			continue
		}
		if !match(string(p)) {
			continue
		}
		if size, err := strconv.ParseInt(fields[3], 10, 64); err == nil && maxSize > 0 && size > maxSize {
			skipped++
			continue
		}
		blobs = append(blobs, blob{fields[2], string(p)})
	}
	if len(blobs) == 0 {
		return nil, skipped, nil
	}
	var in strings.Builder
	for _, b := range blobs {
		in.WriteString(b.sha + "\n")
	}
	s, err := git.Stream(ctx, dir, in.String(), "cat-file", "--batch")
	if err != nil {
		return nil, skipped, err
	}
	defer s.Close()
	r := bufio.NewReaderSize(s, 64*1024)
	for _, b := range blobs {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, skipped, fmt.Errorf("read %s: %w", b.path, err)
		}
		f := strings.Fields(header) // sha type size | sha missing
		if len(f) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		content := make([]byte, size+1) // +1 for the newline after each object
		if _, err := io.ReadFull(r, content); err != nil {
			return nil, skipped, fmt.Errorf("read %s: %w", b.path, err)
		}
		content = content[:size]
		if looksBinary(content) {
			continue
		}
		files = append(files, HeadFile{Path: b.path, Data: content})
		if ctx.Err() != nil {
			return nil, skipped, ctx.Err()
		}
	}
	return files, skipped, s.Close()
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
