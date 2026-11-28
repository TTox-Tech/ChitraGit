package scan

import (
	"bufio"
	"encoding/gob"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cacheVersion changes whenever listing changes shape; an older file is
// then ignored and the next walk reads every folder again.
const cacheVersion = 1

// racyWindow is how close to the walk a folder may have changed and still
// be trusted next time. File systems with coarse timestamps (FAT keeps 2 s)
// can change a folder twice within one tick, so a listing taken in that
// window is read again on the next walk, as git does for its index.
const racyWindow = 2 * time.Second

// listing is what the walker needs from one folder: its sub-folders and
// whether it is a repo. A folder's modification time changes whenever an
// entry is added, removed or renamed in it, so an unchanged time means an
// unchanged listing.
type listing struct {
	MTime  int64    // nanoseconds; 0 = never trust (racy)
	Dirs   []string // sub-folders, symlinks left out, ".git" left out
	Links  []string // symlinked entries, only looked at when following symlinks
	Git    byte     // gitNone, gitDir or gitFile
	GitDir string   // what a ".git" file points to
	GitErr string   // why the ".git" file could not be read
	Bare   bool
}

const (
	gitNone byte = iota
	gitDir
	gitFile
)

type cacheFile struct {
	Version int
	Dirs    map[string]listing // by path from the root, "." for the root
}

// loadCache reads the listings a previous walk saved. Any problem (no
// file, an old version, a damaged file) gives an empty cache, which only
// means this walk reads every folder.
func loadCache(file string) map[string]listing {
	f, err := os.Open(file)
	if err != nil {
		return nil
	}
	defer f.Close()
	var c cacheFile
	if gob.NewDecoder(bufio.NewReader(f)).Decode(&c) != nil || c.Version != cacheVersion {
		return nil
	}
	return c.Dirs
}

// saveCache writes the listings through a temporary file and a rename, so
// a crash leaves either the old cache or the new one.
func saveCache(file string, dirs map[string]listing) error {
	tmp, err := os.CreateTemp(filepath.Dir(file), filepath.Base(file)+".*")
	if err != nil {
		return err
	}
	w := bufio.NewWriter(tmp)
	err = gob.NewEncoder(w).Encode(cacheFile{Version: cacheVersion, Dirs: dirs})
	if err == nil {
		err = w.Flush()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), file)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

// readListing lists folder abs. mtime is the folder's time taken before the
// listing, so a change made while it was read shows up as a newer time
// next walk.
func readListing(abs string, mtime, start time.Time) (listing, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		return listing{}, err
	}
	l := listing{MTime: mtime.UnixNano()}
	if !mtime.Before(start.Add(-racyWindow)) {
		l.MTime = 0
	}
	var head, objects, refs bool
	for _, e := range entries {
		name := e.Name()
		head = head || name == "HEAD" && !e.IsDir()
		switch {
		case name == ".git" && e.IsDir():
			l.Git = gitDir
		case name == ".git":
			l.Git = gitFile
			data, err := os.ReadFile(filepath.Join(abs, name))
			if err != nil {
				l.GitErr, l.MTime = plainFSError(err), 0
				continue
			}
			l.GitDir = filepath.ToSlash(strings.TrimSpace(strings.TrimPrefix(string(data), "gitdir:")))
		case e.Type()&fs.ModeSymlink != 0:
			l.Links = append(l.Links, name)
		case e.IsDir():
			l.Dirs = append(l.Dirs, name)
			objects = objects || name == "objects"
			refs = refs || name == "refs"
		}
	}
	l.Bare = head && objects && refs
	return l, nil
}
