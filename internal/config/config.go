// Package config loads and validates ChitraGit configuration.
//
// Layering, later wins (PRD F13):
//
//	built-in defaults → user config → archive config → CHITRAGIT_* env vars → CLI flags
//
// CLI flags are applied by the caller after Load returns.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config is the full, merged configuration.
type Config struct {
	Sync      SyncConfig      `toml:"sync"`
	Scan      ScanConfig      `toml:"scan"`
	Guardian  GuardianConfig  `toml:"guardian"`
	Clone     CloneConfig     `toml:"clone"`
	Providers ProvidersConfig `toml:"providers"`
	UI        UIConfig        `toml:"ui"`
	Notify    NotifyConfig    `toml:"notify"`
}

type SyncConfig struct {
	Mode              string         `toml:"mode" doc:"fetch | ff | dry-run"`
	Workers           int            `toml:"workers" doc:"parallel repos (0 = auto: min(16, 2×CPU))"`
	PerHostLimit      map[string]int `toml:"per_host_limit" doc:"max parallel operations per host"`
	Timeout           Duration       `toml:"timeout" doc:"per-repo timeout"`
	Retries           int            `toml:"retries" doc:"retries for network errors"`
	Prune             bool           `toml:"prune" doc:"delete refs removed upstream (off keeps them archived)"`
	StashLocalChanges bool           `toml:"stash_local_changes" doc:"stash local changes, update, re-apply"`
	FetchAllRemotes   bool           `toml:"fetch_all_remotes" doc:"fetch every remote, not just origin"`
	LFS               string         `toml:"lfs" doc:"auto | always | never"`
	Submodules        bool           `toml:"submodules" doc:"also update submodules"`
	OnInterrupted     string         `toml:"on_interrupted" doc:"resume | fresh (used when ChitraGit cannot ask)"`
}

type ScanConfig struct {
	MaxDepth       int      `toml:"max_depth" doc:"0 = unlimited"`
	Exclude        []string `toml:"exclude" doc:"directory names or globs to skip"`
	FollowSymlinks bool     `toml:"follow_symlinks"`
}

type GuardianConfig struct {
	RescueConfirmations int      `toml:"rescue_confirmations" doc:"failed runs before a repo is marked Rescued"`
	AutoBundleRescued   bool     `toml:"auto_bundle_rescued" doc:"write a git bundle backup of rescued repos"`
	StaleAfter          Duration `toml:"stale_after" doc:"no upstream activity for this long = stale"`
}

type CloneConfig struct {
	Layout string `toml:"layout" doc:"folder layout, e.g. {owner}/{repo} or {host}/{owner}/{repo}"`
	Type   string `toml:"type" doc:"normal | mirror"`
}

type ProvidersConfig struct {
	Hosts          map[string]string `toml:"hosts" doc:"self-hosted servers, e.g. git.example.com=gitea (github | gitlab | gitea)"`
	SmartSkip      bool              `toml:"smart_skip" doc:"ask the GitHub API which repos changed and fetch only those (needs a login)"`
	GitHubClientID string            `toml:"github_client_id" doc:"OAuth app client ID for browser login with 'auth login github'"`
}

type UIConfig struct {
	Theme      string `toml:"theme" doc:"ledger | ledger-light | high-contrast"`
	Animations string `toml:"animations" doc:"auto | full | light | off"`
	Mouse      bool   `toml:"mouse"`
	Language   string `toml:"language" doc:"UI language code, e.g. en"`
}

type NotifyConfig struct {
	Desktop bool     `toml:"desktop"`
	On      []string `toml:"on" doc:"run_finished | rescued | conflict"`
}

// Defaults returns the built-in configuration.
func Defaults() *Config {
	return &Config{
		Sync: SyncConfig{
			Mode:              "ff",
			Workers:           0,
			PerHostLimit:      map[string]int{"github.com": 8},
			Timeout:           Duration(10 * time.Minute),
			Retries:           3,
			Prune:             false,
			StashLocalChanges: true,
			FetchAllRemotes:   true,
			LFS:               "auto",
			Submodules:        false,
			OnInterrupted:     "resume",
		},
		Scan: ScanConfig{
			MaxDepth: 0,
			Exclude:  []string{"node_modules", ".venv", "vendor", "target", ".cache"},
		},
		Guardian: GuardianConfig{
			RescueConfirmations: 2,
			AutoBundleRescued:   true,
			StaleAfter:          Duration(180 * 24 * time.Hour),
		},
		Clone: CloneConfig{
			Layout: "{owner}/{repo}",
			Type:   "normal",
		},
		Providers: ProvidersConfig{
			Hosts:     map[string]string{},
			SmartSkip: true,
		},
		UI: UIConfig{
			Theme:      "ledger",
			Animations: "auto",
			Mouse:      true,
			Language:   "en",
		},
		Notify: NotifyConfig{
			Desktop: true,
			On:      []string{"run_finished", "rescued", "conflict"},
		},
	}
}

// Duration is a time.Duration that also accepts a "d" (days) suffix,
// e.g. "180d", and marshals back to a readable string.
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

func (d Duration) String() string {
	td := time.Duration(d)
	day := 24 * time.Hour
	if td >= day && td%day == 0 {
		return strconv.FormatInt(int64(td/day), 10) + "d"
	}
	// time.Duration prints "10m0s" and "1h0m0s"; trim the zero units.
	out := td.String()
	if strings.HasSuffix(out, "m0s") {
		out = strings.TrimSuffix(out, "0s")
	}
	if strings.HasSuffix(out, "h0m") {
		out = strings.TrimSuffix(out, "0m")
	}
	return out
}

// ParseDuration parses Go durations plus whole days ("7d").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", s)
	}
	return v, nil
}
