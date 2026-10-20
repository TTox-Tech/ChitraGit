// Package version exposes build information injected at link time.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

// Set via -ldflags "-X github.com/TTox-Tech/chitragit/internal/version.Version=..."
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

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
