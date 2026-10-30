// Package version exposes build information injected at link time.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Set via -ldflags "-X github.com/TTox-Tech/chitragit/internal/version.Version=..."
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// A binary built with `go install module@version` has no ldflags, so take
// the version from the module's build info instead.
func init() {
	if Version != "dev" {
		return
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := moduleVersion(info); v != "" {
			Version = v
		}
	}
}

// moduleVersion returns the main module's version without its "v" prefix,
// or "" for a local build, which Go reports as "(devel)".
func moduleVersion(info *debug.BuildInfo) string {
	v := info.Main.Version
	if v == "" || v == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

// String returns a one-line human-readable version description.
func String() string {
	commit := Commit
	if commit == "" {
		commit = vcsRevision()
	}
	if len(commit) > 12 {
		commit = commit[:12]
	}
	s := "chitragit " + Version
	if commit != "" {
		s += " (" + commit + ")"
	}
	if Date != "" {
		s += " built " + Date
	}
	return fmt.Sprintf("%s %s/%s %s", s, runtime.GOOS, runtime.GOARCH, runtime.Version())
}

func vcsRevision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return s.Value
		}
	}
	return ""
}
