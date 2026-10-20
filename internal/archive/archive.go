// Package archive locates, initialises and locks a ChitraGit archive.
//
// An archive is any directory containing a ".chitragit" folder. Everything
// ChitraGit knows about the archive lives there, so the archive stays
// portable across disks and machines.
package archive

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// MetaDir is the name of the per-archive data folder.
const MetaDir = ".chitragit"

// Archive is an initialised archive on disk.
type Archive struct {
	Root string // absolute path of the archive root
}

func (a *Archive) Meta() string       { return filepath.Join(a.Root, MetaDir) }
func (a *Archive) ConfigFile() string { return filepath.Join(a.Meta(), "config.toml") }
func (a *Archive) DBFile() string     { return filepath.Join(a.Meta(), "chitragit.db") }
func (a *Archive) LogsDir() string    { return filepath.Join(a.Meta(), "logs") }
func (a *Archive) BundlesDir() string { return filepath.Join(a.Meta(), "bundles") }
func (a *Archive) lockFile() string   { return filepath.Join(a.Meta(), "lock") }

// ErrNotFound is returned by Find when no archive contains the start path.
var ErrNotFound = &core.Error{
	Category: core.CatConfig,
	Message:  "this folder is not inside a ChitraGit archive",
	Hint:     "run `chitragit init` in your archive's root folder, or pass --archive <path>",
}

// Find walks up from start until it finds a directory with a .chitragit
// folder.
func Find(start string) (*Archive, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return nil, err
	}
	for {
		info, err := os.Stat(filepath.Join(dir, MetaDir))
		if err == nil && info.IsDir() {
			return &Archive{Root: dir}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, ErrNotFound
		}
		dir = parent
	}
}

// Open returns the archive rooted exactly at root.
func Open(root string) (*Archive, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Join(abs, MetaDir))
	if err != nil || !info.IsDir() {
		e := *ErrNotFound
		e.Message = abs + " is not a ChitraGit archive"
		return nil, &e
	}
	return &Archive{Root: abs}, nil
}

// Init creates the archive layout at root. It is idempotent: running it on
// an existing archive only fills in anything missing.
func Init(ctx context.Context, root string) (*Archive, bool, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, false, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, false, &core.Error{Category: core.CatConfig, Message: abs + " is not a folder", Err: err}
	}
	if outer, err := Find(filepath.Dir(abs)); err == nil {
		return nil, false, &core.Error{
			Category: core.CatConfig,
			Message:  fmt.Sprintf("%s is already inside the archive at %s", abs, outer.Root),
			Hint:     "nested archives are not supported; use the outer archive",
		}
	}

	a := &Archive{Root: abs}
	_, statErr := os.Stat(a.Meta())
	created := errors.Is(statErr, fs.ErrNotExist)

	for _, d := range []string{a.Meta(), a.LogsDir(), a.BundlesDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, false, &core.Error{Category: core.CatPermission, Message: "cannot create " + d, Err: err}
		}
	}
	if _, err := os.Stat(a.ConfigFile()); errors.Is(err, fs.ErrNotExist) {
		if err := os.WriteFile(a.ConfigFile(), []byte(config.ArchiveTemplate), 0o600); err != nil {
			return nil, false, err
		}
	}
	// Keep ChitraGit's data out of the archive if the archive itself is a repo.
	gi := filepath.Join(a.Meta(), ".gitignore")
	if _, err := os.Stat(gi); errors.Is(err, fs.ErrNotExist) {
		_ = os.WriteFile(gi, []byte("*\n"), 0o600)
	}

	s, err := store.Open(ctx, a.DBFile(), store.Options{})
	if err != nil {
		return nil, false, err
	}
	return a, created, s.Close()
}
