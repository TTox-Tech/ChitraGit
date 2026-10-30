package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// SyncOptions turns the [sync] settings into syncer options.
func SyncOptions(c config.SyncConfig) syncer.Options {
	return syncer.Options{
		Mode: c.Mode, Workers: c.Workers, PerHostLimit: c.PerHostLimit, Timeout: time.Duration(c.Timeout),
		Retries: c.Retries, Prune: c.Prune, StashLocalChanges: c.StashLocalChanges,
		FetchAllRemotes: c.FetchAllRemotes, Submodules: c.Submodules,
	}
}

// Describe turns ref changes into "3 new commits, 1 new tag". New commits
// are counted on remote-tracking refs, or on branches for bare repos, which
// have no remote-tracking refs.
func Describe(changes []core.RefChange) string {
	prefix := "refs/heads/"
	for _, c := range changes {
		if strings.HasPrefix(c.Ref, "refs/remotes/") {
			prefix = "refs/remotes/"
			break
		}
	}
	commits, branches, tags := 0, 0, 0
	for _, c := range changes {
		switch {
		case strings.HasPrefix(c.Ref, "refs/tags/") && c.Kind == "create":
			tags++
		case strings.HasPrefix(c.Ref, prefix) && c.Kind == "create":
			branches++
		case strings.HasPrefix(c.Ref, prefix):
			commits += c.Commits
		}
	}
	var parts []string
	for _, p := range []struct {
		n    int
		noun string
	}{{commits, "new commit"}, {branches, "new branch"}, {tags, "new tag"}} {
		if p.n == 1 {
			parts = append(parts, "1 "+p.noun)
		} else if p.n > 1 {
			parts = append(parts, fmt.Sprintf("%d %ss", p.n, p.noun))
		}
	}
	return strings.Join(parts, ", ")
}

// DescribeActivity is Describe for the activity feed: it also says what
// the archive kept after upstream deleted or rewrote history.
func DescribeActivity(changes []core.RefChange) string {
	msg := Describe(changes)
	kept, rewritten := 0, 0
	for _, c := range changes {
		switch {
		case strings.HasPrefix(c.Ref, "refs/chitragit/archived/"):
			kept++
		case strings.HasPrefix(c.Ref, "refs/chitragit/rewritten/") || c.Kind == "rewrite":
			rewritten++
		}
	}
	var extra []string
	if rewritten > 0 {
		extra = append(extra, i18n.T("activity.rewritten"))
	}
	if kept > 0 {
		extra = append(extra, i18n.T("activity.kept", "count", kept))
	}
	if len(extra) > 0 {
		if msg != "" {
			msg += " · "
		}
		msg += strings.Join(extra, " · ")
	}
	if msg == "" {
		msg = i18n.T("activity.refs_moved", "count", len(changes))
	}
	return msg
}
