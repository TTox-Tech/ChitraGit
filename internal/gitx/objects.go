package gitx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// TreeEntry is one entry of a tree, as `git ls-tree --long` lists it.
type TreeEntry struct {
	Path string // slash-separated, relative to the repo root
	Mode string // e.g. 100644, 100755, 120000 (symlink), 160000 (submodule), 040000
	Type string // blob, tree or commit
	SHA  string
	Size int64 // -1 for trees and submodules
}

// Regular reports whether the entry is an ordinary file: not a symlink, a
// submodule or a directory.
func (e TreeEntry) Regular() bool { return e.Type == "blob" && e.Mode != "120000" }

// TreeOptions shapes Tree.
type TreeOptions struct {
	// TopLevel lists only the root of the tree instead of every file in it.
	TopLevel bool
	// MaxEntries refuses a tree with more entries than this; 0 means no limit.
	MaxEntries int
}

// ErrTreeTooLarge is returned by Tree when a tree has more than
// TreeOptions.MaxEntries entries.
var ErrTreeTooLarge = errors.New("tree has too many entries")

// Tree lists the tree of rev, read from the object store, so it works the
// same on bare repos and never touches a work tree. Entries whose path
// could leave the repo (absolute, empty or ".." parts) are dropped: git
// refuses to create them, and no caller should ever write or open one.
func (r *Runner) Tree(ctx context.Context, dir, rev string, opts TreeOptions) ([]TreeEntry, error) {
	if err := checkRev(rev); err != nil {
		return nil, err
	}
	args := []string{"ls-tree", "-z", "--long", "--full-tree"}
	if !opts.TopLevel {
		args = append(args, "-r")
	}
	res, err := r.Run(ctx, dir, append(args, "--end-of-options", rev)...)
	if err != nil {
		return nil, err
	}
	var entries []TreeEntry
	for _, raw := range bytes.Split(res.Stdout, []byte{0}) {
		meta, p, ok := bytes.Cut(raw, []byte{'\t'})
		if !ok {
			continue
		}
		f := strings.Fields(string(meta)) // mode type sha size
		if len(f) != 4 || !safeTreePath(string(p)) {
			continue
		}
		size, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil {
			size = -1 // "-" for trees and submodules
		}
		if opts.MaxEntries > 0 && len(entries) == opts.MaxEntries {
			return nil, ErrTreeTooLarge
		}
		entries = append(entries, TreeEntry{Path: string(p), Mode: f[0], Type: f[1], SHA: f[2], Size: size})
	}
	return entries, nil
}

// Blob is one object read by Blobs.
type Blob struct {
	Index  int // position of the object in the list given to Blobs
	SHA    string
	Data   []byte
	Binary bool // a NUL byte in the first 8000 bytes, as git decides
}

// Blobs reads the objects named by shas through one `git cat-file --batch`
// stream and calls fn for each, in order. Objects that are missing or larger
// than maxSize bytes (0 = no limit) are skipped without being kept in
// memory. An error from fn, or a cancelled ctx, stops git and is returned.
func (r *Runner) Blobs(ctx context.Context, dir string, shas []string, maxSize int64, fn func(Blob) error) error {
	if len(shas) == 0 {
		return nil
	}
	var in strings.Builder
	for _, sha := range shas {
		if !isHex(sha) {
			return &core.Error{Category: core.CatInternal, Message: "not an object name", Detail: sha}
		}
		in.WriteString(sha + "\n")
	}
	s, err := r.Stream(ctx, dir, in.String(), "cat-file", "--batch")
	if err != nil {
		return err
	}
	defer s.Close()
	br := bufio.NewReaderSize(s, 64*1024)
	for i, sha := range shas {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read object %s: %w", sha, err)
		}
		f := strings.Fields(header) // sha type size | name missing
		if len(f) != 3 {
			continue
		}
		size, err := strconv.ParseInt(f[2], 10, 64)
		if err != nil || size < 0 {
			return fmt.Errorf("read object %s: bad header %q", sha, strings.TrimSpace(header))
		}
		if maxSize > 0 && size > maxSize {
			if _, err := io.CopyN(io.Discard, br, size+1); err != nil {
				return fmt.Errorf("read object %s: %w", sha, err)
			}
			continue
		}
		data := make([]byte, size+1) // +1 for the newline after each object
		if _, err := io.ReadFull(br, data); err != nil {
			return fmt.Errorf("read object %s: %w", sha, err)
		}
		data = data[:size]
		if err := fn(Blob{Index: i, SHA: f[0], Data: data, Binary: bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0}); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.Close()
}

// checkRev refuses a revision git could read as an option.
func checkRev(rev string) error {
	if rev == "" || strings.HasPrefix(rev, "-") || strings.ContainsAny(rev, "\x00\n") {
		return &core.Error{Category: core.CatInternal, Message: "not a revision", Detail: rev}
	}
	return nil
}

// safeTreePath reports whether p stays inside the repo: relative, with no
// empty, "." or ".." parts, splitting on "\" too as Windows does.
func safeTreePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	for part := range strings.SplitSeq(strings.ReplaceAll(p, "\\", "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func isHex(s string) bool {
	if len(s) < 4 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
