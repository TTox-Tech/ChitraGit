// Package guardian protects repos whose upstream vanished (PRD F5): it asks
// the provider API why a fetch failed, and writes git bundle backups.
//
// It never deletes or rewrites anything in a repo. The worst a wrong
// verdict can do is add a badge and a backup file.
package guardian

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/provider"
)

// Reasons stored on a rescue.
const (
	ReasonGone     = "gone"      // API: 404, deleted or made private
	ReasonLegal    = "legal"     // API: 451 or blocked, a legal takedown
	ReasonNotFound = "not_found" // no API (plain Git URL): git said "not found"
)

// Diagnosis is what the guardian concluded about one failed fetch.
type Diagnosis struct {
	// Reason is set when the failure counts toward a rescue.
	Reason string
	// Moved is the repo's new place when upstream was renamed/transferred.
	Moved *provider.Repo
	// Repo is what the API said when the repo still exists (or moved).
	Repo   *provider.Repo
	Detail string
}

// Worth reports whether a failure category is worth asking the API about.
// Only "not found" and "access denied" can mean the upstream vanished.
func Worth(c string) bool {
	return c == string(core.CatNotFound) || c == string(core.CatAuth)
}

// Diagnose asks client (nil for a plain Git URL) why repo r could not be
// fetched. An error means the API could not answer, so nothing is
// concluded this run.
func Diagnose(ctx context.Context, client provider.Client, r core.Repo, category string) (Diagnosis, error) {
	if client == nil || r.Owner == "" || r.Name == "" {
		// Without an API, git's own "repository not found" is the only
		// evidence. An auth error alone proves nothing.
		if category == string(core.CatNotFound) {
			return Diagnosis{Reason: ReasonNotFound, Detail: "git: repository not found"}, nil
		}
		return Diagnosis{}, nil
	}
	l, err := client.Lookup(ctx, r.Owner, r.Name)
	if err != nil {
		return Diagnosis{}, err
	}
	switch l.Verdict {
	case provider.Gone:
		return Diagnosis{Reason: ReasonGone, Detail: client.Host() + " API: 404 not found"}, nil
	case provider.Legal:
		return Diagnosis{Reason: ReasonLegal, Detail: client.Host() + " API: unavailable for legal reasons"}, nil
	case provider.Moved:
		if l.Repo == nil || !strings.EqualFold(l.Repo.Host, r.Host) || l.Repo.CloneURL == "" {
			return Diagnosis{}, nil
		}
		return Diagnosis{Moved: l.Repo, Repo: l.Repo, Detail: "moved to " + l.Repo.FullName()}, nil
	}
	return Diagnosis{Repo: l.Repo}, nil
}

// NewURL picks the moved repo's URL in the same style as the old one: SSH
// stays SSH, HTTPS stays HTTPS, so the user's credentials keep working.
func NewURL(oldURL string, moved *provider.Repo) string {
	ssh := strings.HasPrefix(oldURL, "ssh://") || (!strings.Contains(oldURL, "://") && strings.Contains(oldURL, "@"))
	if ssh && moved.SSHURL != "" {
		return moved.SSHURL
	}
	return moved.CloneURL
}

// BundleFile is where a repo's bundle lives under the bundles folder: the
// repo's archive path plus ".bundle", which is unique per repo.
func BundleFile(bundlesDir, repoPath string) string {
	return filepath.Join(bundlesDir, filepath.FromSlash(strings.TrimSuffix(repoPath, ".git"))+".bundle")
}

// ErrEmpty means a repo has no commits, so there is nothing to bundle.
var ErrEmpty = errors.New("the repo has no commits yet; nothing to bundle")

// Bundle writes a single-file copy of every ref and its history (git
// bundle --all, which includes refs/chitragit/) to dest. The new bundle
// is verified before it replaces the old one, so a failed or interrupted
// run always leaves the last good backup in place.
func Bundle(ctx context.Context, git *gitx.Runner, dir, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return &core.Error{Category: core.CatPermission, Message: "cannot create the bundles folder", Err: err}
	}
	tmp := fmt.Sprintf("%s.tmp-%d", dest, os.Getpid())
	defer os.Remove(tmp) // no-op after the rename
	abs, err := filepath.Abs(tmp)
	if err != nil {
		return err
	}
	if res, err := git.Run(ctx, dir, "bundle", "create", abs, "--all"); err != nil {
		if res != nil && strings.Contains(strings.ToLower(string(res.Stderr)), "empty bundle") {
			return ErrEmpty
		}
		return err
	}
	if _, err := git.Run(ctx, dir, "bundle", "verify", abs); err != nil {
		return &core.Error{Category: core.CatCorrupt, Message: "the new bundle did not verify; the previous backup was kept", Err: err}
	}
	if err := os.Rename(tmp, dest); err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot save the bundle", Err: err}
	}
	return nil
}
