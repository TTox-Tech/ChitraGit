// Package platform isolates OS-specific behaviour: standard directories,
// process liveness checks and terminal facts.
package platform

import (
	"os"
	"path/filepath"
)

const appName = "chitragit"

// ConfigDir returns the per-user configuration directory, honouring
// CHITRAGIT_CONFIG_DIR for tests and portable installs.
func ConfigDir() (string, error) {
	if d := os.Getenv("CHITRAGIT_CONFIG_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName), nil
}

// CacheDir returns the per-user cache directory, honouring
// CHITRAGIT_CACHE_DIR.
func CacheDir() (string, error) {
	if d := os.Getenv("CHITRAGIT_CACHE_DIR"); d != "" {
		return d, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appName), nil
}

// UserConfigFile is the path of the personal config.toml.
func UserConfigFile() (string, error) {
	d, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "config.toml"), nil
}

// EmptyHooksDir returns (creating if needed) an empty directory that is
// passed to git as core.hooksPath so that hooks inside archived repos
// never run.
func EmptyHooksDir() (string, error) {
	d, err := CacheDir()
	if err != nil {
		return "", err
	}
	p := filepath.Join(d, "empty-hooks")
	if err := os.MkdirAll(p, 0o700); err != nil {
		return "", err
	}
	return p, nil
}
