package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// ArchiveTemplate is written to <archive>/.chitragit/config.toml by
// `chitragit init`. Every line is commented out so built-in defaults and
// the user config keep applying until a value is set here.
const ArchiveTemplate = `# ChitraGit archive settings.
# Values here override your personal config for this archive only.
# Run "chitragit config get <key>" to see the effective value.

# [sync]
# mode = "ff"                # fetch | ff | dry-run
# workers = 0                # 0 = auto
# timeout = "10m"
# prune = false              # false keeps refs deleted upstream, archived
# stash_local_changes = true

# [scan]
# exclude = ["node_modules", ".venv", "vendor", "target", ".cache"]

# [guardian]
# rescue_confirmations = 2
# auto_bundle_rescued = true

# [clone]
# layout = "{owner}/{repo}"
# type = "normal"            # normal | mirror

# [providers]
# hosts = { "git.example.com" = "gitea" }   # self-hosted github | gitlab | gitea
# smart_skip = true          # ask the GitHub API what changed; fetch only those
`

// SetInFile validates key=value against the schema and stores it in the TOML
// file at path, creating the file if needed. Other keys in the file are kept;
// comments are not preserved.
func SetInFile(path, key, value string) error {
	probe := Defaults()
	if err := probe.Set(key, value); err != nil {
		return &core.Error{Category: core.CatConfig, Message: err.Error()}
	}
	if err := probe.Validate(); err != nil {
		return err
	}

	doc := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	default:
		if _, err := toml.Decode(string(data), &doc); err != nil {
			return &core.Error{Category: core.CatConfig, Message: "invalid config file " + path, Err: err}
		}
	}

	section, field, _ := strings.Cut(key, ".")
	sec, _ := doc[section].(map[string]any)
	if sec == nil {
		sec = map[string]any{}
		doc[section] = sec
	}
	v, _, _ := lookupField(reflect.ValueOf(probe).Elem(), key)
	if v.Type() == durationType {
		sec[field] = Duration(v.Int()).String()
	} else {
		sec[field] = v.Interface()
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(doc); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, buf.Bytes(), 0o600)
}

// writeFileAtomic writes to a temp file in the same directory and renames it
// over the target so a crash never leaves a half-written config.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
