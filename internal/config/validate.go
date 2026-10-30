package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// Validate checks every value and returns one *core.Error listing all
// problems, so users can fix them in a single pass.
func (c *Config) Validate() error {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	oneOf := func(key, val string, allowed ...string) {
		if !slices.Contains(allowed, val) {
			add("%s = %q, must be one of: %s", key, val, strings.Join(allowed, ", "))
		}
	}

	oneOf("sync.mode", c.Sync.Mode, "fetch", "ff", "dry-run")
	if c.Sync.Workers < 0 || c.Sync.Workers > 256 {
		add("sync.workers = %d, must be between 0 (auto) and 256", c.Sync.Workers)
	}
	for host, n := range c.Sync.PerHostLimit {
		if n < 1 {
			add("sync.per_host_limit[%q] = %d, must be at least 1", host, n)
		}
	}
	if c.Sync.Timeout <= 0 {
		add("sync.timeout must be greater than zero")
	}
	if c.Sync.Retries < 0 || c.Sync.Retries > 10 {
		add("sync.retries = %d, must be between 0 and 10", c.Sync.Retries)
	}
	oneOf("sync.lfs", c.Sync.LFS, "auto", "always", "never")
	oneOf("sync.on_interrupted", c.Sync.OnInterrupted, "resume", "fresh")

	if c.Scan.MaxDepth < 0 {
		add("scan.max_depth = %d, must be 0 (unlimited) or more", c.Scan.MaxDepth)
	}

	if c.Guardian.RescueConfirmations < 1 {
		add("guardian.rescue_confirmations must be at least 1")
	}
	if c.Guardian.StaleAfter <= 0 {
		add("guardian.stale_after must be greater than zero")
	}

	if !strings.Contains(c.Clone.Layout, "{repo}") {
		add("clone.layout = %q, must contain {repo}", c.Clone.Layout)
	}
	if strings.Contains(c.Clone.Layout, "..") {
		add("clone.layout must not contain \"..\"")
	}
	oneOf("clone.type", c.Clone.Type, "normal", "mirror")

	for host, kind := range c.Providers.Hosts {
		if host == "" || strings.ContainsAny(host, "/: ") {
			add("providers.hosts: %q is not a host name", host)
		}
		oneOf("providers.hosts["+host+"]", kind, "github", "gitlab", "gitea")
	}

	oneOf("ui.theme", c.UI.Theme, "ledger", "ledger-light", "high-contrast")
	oneOf("ui.animations", c.UI.Animations, "auto", "full", "light", "off")
	if c.UI.Language == "" {
		add("ui.language must not be empty")
	}

	for _, ev := range c.Notify.On {
		oneOf("notify.on", ev, "run_finished", "rescued", "conflict")
	}

	if len(problems) == 0 {
		return nil
	}
	return &core.Error{
		Category: core.CatConfig,
		Message:  fmt.Sprintf("configuration has %d problem(s)", len(problems)),
		Detail:   "  - " + strings.Join(problems, "\n  - "),
		Hint:     "edit the file with `chitragit config edit` or fix a value with `chitragit config set <key> <value>`",
	}
}
