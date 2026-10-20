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
