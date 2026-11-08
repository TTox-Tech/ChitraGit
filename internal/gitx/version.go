package gitx

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// MinVersion is the oldest git ChitraGit supports.
var MinVersion = Version{2, 30, 0}

// Version is a git release number.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// ParseVersion understands the output of `git version` on every platform:
//
//	git version 2.43.0
//	git version 2.39.3 (Apple Git-146)
//	git version 2.45.1.windows.1
func ParseVersion(out string) (Version, error) {
	s := strings.TrimSpace(out)
	s = strings.TrimPrefix(s, "git version ")
	if i := strings.IndexByte(s, ' '); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return Version{}, fmt.Errorf("cannot parse git version %q", out)
	}
	nums := [3]int{}
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			if i < 2 {
				return Version{}, fmt.Errorf("cannot parse git version %q", out)
			}
			break // e.g. "2.45.rc1"
		}
		nums[i] = n
	}
	return Version{nums[0], nums[1], nums[2]}, nil
}

// GitVersion runs `git version` and parses it.
func (r *Runner) GitVersion(ctx context.Context) (Version, error) {
	out, err := r.Output(ctx, "", "version")
	if err != nil {
		return Version{}, err
	}
	return ParseVersion(out)
}

// CheckVersion returns an environment error if git is older than MinVersion.
func (r *Runner) CheckVersion(ctx context.Context) (Version, error) {
	v, err := r.GitVersion(ctx)
	if err != nil {
		return v, err
	}
	if v.Less(MinVersion) {
		return v, &core.Error{
			Category: core.CatEnvironment,
			Message:  fmt.Sprintf("git %s is too old; ChitraGit needs %s or newer", v, MinVersion),
			Hint:     installHint(),
		}
	}
	return v, nil
}

// cve is a git flaw that a hostile repo can use, with the first fixed
// release on each maintained line. Lines older than the first entry never
// got the fix; lines newer than the last one shipped with it.
type cve struct {
	id    string
	fixed []Version
}

// shortcut: Apple Git and distro packages backport fixes without a new
// version number, so they can be reported by mistake; doctor says to update.
var cves = []cve{
	{"CVE-2024-32002", []Version{{2, 39, 4}, {2, 40, 2}, {2, 41, 1}, {2, 42, 2}, {2, 43, 4}, {2, 44, 1}, {2, 45, 1}}},
	{"CVE-2025-48384", []Version{{2, 43, 7}, {2, 44, 4}, {2, 45, 4}, {2, 46, 4}, {2, 47, 3}, {2, 48, 2}, {2, 49, 1}, {2, 50, 1}}},
}

// KnownCVEs lists the flaws above that v does not have the fix for.
func (v Version) KnownCVEs() []string {
	var ids []string
	for _, c := range cves {
		if !c.fixedIn(v) {
			ids = append(ids, c.id)
		}
	}
	return ids
}

func (c cve) fixedIn(v Version) bool {
	last := c.fixed[len(c.fixed)-1]
	if v.Major != last.Major {
		return v.Major > last.Major
	}
	if v.Minor > last.Minor {
		return true
	}
	for _, f := range c.fixed {
		if f.Minor == v.Minor {
			return !v.Less(f)
		}
	}
	return false // a line older than any fixed release
}
