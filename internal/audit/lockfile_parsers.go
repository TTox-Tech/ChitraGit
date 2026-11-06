package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

// Each parser reads one lockfile format and returns what it pins. They
// never fetch anything: a lockfile that needs resolving (a manifest with
// version ranges) is not read.

func parseGoMod(data []byte) ([]Package, error) {
	// The strict parser keeps replace directives; fall back to the lax one
	// for a go.mod with statements this build does not know.
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		if f, err = modfile.ParseLax("go.mod", data, nil); err != nil {
			return nil, err
		}
	}
	replaced := map[string]*modfile.Replace{}
	for _, r := range f.Replace {
		replaced[r.Old.Path+"@"+r.Old.Version] = r
		if r.Old.Version == "" {
			replaced[r.Old.Path] = r
		}
	}
	var out []Package
	for _, r := range f.Require {
		p, v := r.Mod.Path, r.Mod.Version
		rep := replaced[p+"@"+v]
		if rep == nil {
			rep = replaced[p]
		}
		if rep != nil {
			if rep.New.Version == "" {
				continue // replaced by a local directory
			}
			p, v = rep.New.Path, rep.New.Version
		}
		out = append(out, Package{Ecosystem: EcoGo, Name: p, Version: v})
	}
	return out, nil
}

// package-lock.json and npm-shrinkwrap.json, lockfile versions 1 to 3.
func parsePackageLock(data []byte) ([]Package, error) {
	var lock struct {
		Packages map[string]struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Link    bool   `json:"link"`
		} `json:"packages"`
		Dependencies map[string]npmV1Dep `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	var out []Package
	if len(lock.Packages) > 0 {
		for key, p := range lock.Packages {
			if key == "" || p.Link {
				continue
			}
			i := strings.LastIndex(key, "node_modules/")
			if i < 0 {
				continue // a workspace folder, not an installed package
			}
			name := key[i+len("node_modules/"):]
			if p.Name != "" {
				name = p.Name
			}
			out = append(out, Package{Ecosystem: EcoNpm, Name: name, Version: p.Version})
		}
		return out, nil
	}
	var walk func(map[string]npmV1Dep, int)
	walk = func(deps map[string]npmV1Dep, depth int) {
		if depth > 64 {
			return
		}
		for name, d := range deps {
			out = append(out, Package{Ecosystem: EcoNpm, Name: name, Version: d.Version})
			walk(d.Dependencies, depth+1)
		}
	}
	walk(lock.Dependencies, 0)
	return out, nil
}

type npmV1Dep struct {
	Version      string              `json:"version"`
	Dependencies map[string]npmV1Dep `json:"dependencies"`
}

// yarn.lock, both the classic v1 format and Berry (v2+).
func parseYarnLock(data []byte) ([]Package, error) {
	var out []Package
	var name string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line[0] != ' ' {
			name = yarnName(strings.TrimSuffix(line, ":"))
			continue
		}
		t := strings.TrimSpace(line)
		if name == "" || !strings.HasPrefix(t, "version") || strings.HasPrefix(line, "    ") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, "version"), ":"))
		v = strings.Trim(v, `"`)
		if strings.HasPrefix(v, "0.0.0-use.local") {
			name = ""
			continue
		}
		out = append(out, Package{Ecosystem: EcoNpm, Name: name, Version: v})
		name = ""
	}
	return out, sc.Err()
}

// yarnName takes the package name from a block header such as
// `"@babel/core@^7.0.0", "@babel/core@^7.1.0"` or `lodash@npm:^4.17.21`.
func yarnName(header string) string {
	if header == "__metadata" || strings.HasPrefix(header, `"__metadata`) {
		return ""
	}
	spec, _, _ := strings.Cut(header, ",")
	spec = strings.Trim(strings.TrimSpace(spec), `"`)
	at := strings.LastIndex(spec[min(1, len(spec)):], "@")
	if at < 0 {
		return ""
	}
	name := spec[:at+1]
	rest := spec[at+2:]
	if strings.HasPrefix(rest, "patch:") || strings.HasPrefix(rest, "workspace:") || strings.HasPrefix(rest, "link:") ||
		strings.HasPrefix(rest, "portal:") || strings.HasPrefix(rest, "file:") {
		return ""
	}
	return name
}

// pnpm-lock.yaml, lockfile versions 5 to 9.
func parsePnpmLock(data []byte) ([]Package, error) {
	var lock struct {
		Packages map[string]struct {
			Name    string `yaml:"name"`
			Version string `yaml:"version"`
		} `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	var out []Package
	for key, p := range lock.Packages {
		name, version := pnpmKey(key)
		if p.Name != "" {
			name = p.Name
		}
		if p.Version != "" && plausibleVersion(p.Version) {
			version = p.Version
		}
		out = append(out, Package{Ecosystem: EcoNpm, Name: name, Version: version})
	}
	return out, nil
}

// pnpmKey splits "/lodash/4.17.21" (v5), "/lodash@4.17.21" (v6) or
// "@scope/pkg@1.0.0(react@18.0.0)" (v9).
func pnpmKey(key string) (name, version string) {
	key = strings.TrimPrefix(key, "/")
	if i := strings.IndexByte(key, '('); i > 0 {
		key = key[:i]
	}
	// v5: the name is "pkg" or "@scope/pkg", then "/", then the version,
	// maybe followed by a peer suffix: 1.0.0_react@17.0.2.
	slashes := 0
	if strings.HasPrefix(key, "@") {
		slashes = 1
	}
	if i := nthIndex(key, '/', slashes); i > 0 && i+1 < len(key) && key[i+1] >= '0' && key[i+1] <= '9' {
		name, version = key[:i], key[i+1:]
		if j := strings.IndexByte(version, '_'); j > 0 {
			version = version[:j]
		}
		return name, version
	}
	if at := strings.LastIndex(key[min(1, len(key)):], "@"); at >= 0 {
		return key[:at+1], key[at+2:]
	}
	return "", ""
}

// nthIndex returns the index of the (n+1)th c in s, or -1.
func nthIndex(s string, c byte, n int) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			if n == 0 {
				return i
			}
			n--
		}
	}
	return -1
}

func parseCargoLock(data []byte) ([]Package, error) {
	var lock struct {
		Package []struct {
			Name    string `toml:"name"`
			Version string `toml:"version"`
		} `toml:"package"`
	}
	if _, err := toml.Decode(string(data), &lock); err != nil {
		return nil, err
	}
	out := make([]Package, 0, len(lock.Package))
	for _, p := range lock.Package {
		out = append(out, Package{Ecosystem: EcoCrates, Name: p.Name, Version: p.Version})
	}
	return out, nil
}

// poetry.lock, uv.lock and pdm.lock all list [[package]] tables. Packages
// from git, a local folder or a URL are not from PyPI and are skipped.
func parsePythonTOMLLock(data []byte) ([]Package, error) {
	var lock struct {
		Package []struct {
			Name    string         `toml:"name"`
			Version string         `toml:"version"`
			Source  map[string]any `toml:"source"`
		} `toml:"package"`
	}
	if _, err := toml.Decode(string(data), &lock); err != nil {
		return nil, err
	}
	var out []Package
	for _, p := range lock.Package {
		if notFromRegistry(p.Source) {
			continue
		}
		out = append(out, Package{Ecosystem: EcoPyPI, Name: p.Name, Version: p.Version})
	}
	return out, nil
}

func notFromRegistry(src map[string]any) bool {
	if len(src) == 0 {
		return false
	}
	if t, ok := src["type"].(string); ok { // poetry
		return t == "git" || t == "directory" || t == "file" || t == "url"
	}
	for _, k := range []string{"git", "path", "directory", "editable", "virtual", "url"} { // uv
		if _, ok := src[k]; ok {
			return true
		}
	}
	return false
}

func parsePipfileLock(data []byte) ([]Package, error) {
	var lock map[string]json.RawMessage
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	var out []Package
	for _, group := range []string{"default", "develop"} {
		raw, ok := lock[group]
		if !ok {
			continue
		}
		var deps map[string]struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(raw, &deps); err != nil {
			return nil, fmt.Errorf("%s: %w", group, err)
		}
		for name, d := range deps {
			out = append(out, Package{Ecosystem: EcoPyPI, Name: name, Version: strings.TrimPrefix(d.Version, "==")})
		}
	}
	return out, nil
}

// requirements*.txt: only exact pins (name==1.2.3) say which version is
// installed, so ranges are not reported.
func parseRequirements(data []byte) ([]Package, error) {
	var out []Package
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), "\\"))
		if line == "" || line[0] == '#' || line[0] == '-' {
			continue
		}
		if i := strings.IndexAny(line, ";"); i >= 0 {
			line = line[:i]
		}
		name, ver, ok := strings.Cut(line, "==")
		if !ok {
			continue
		}
		ver = strings.TrimPrefix(ver, "=") // ===
		if i := strings.IndexAny(ver, " ,\t"); i >= 0 {
			ver = ver[:i]
		}
		if i := strings.IndexByte(name, '['); i >= 0 {
			name = name[:i] // extras: requests[socks]==2.31.0
		}
		out = append(out, Package{Ecosystem: EcoPyPI, Name: strings.TrimSpace(name), Version: ver})
	}
	return out, sc.Err()
}

// Gemfile.lock: the "specs:" list under each GEM section, four spaces in.
func parseGemfileLock(data []byte) ([]Package, error) {
	var out []Package
	inGem, inSpecs := false, false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if line[0] != ' ' {
			inGem, inSpecs = line == "GEM", false
			continue
		}
		if !inGem {
			continue
		}
		if strings.TrimSpace(line) == "specs:" {
			inSpecs = true
			continue
		}
		if !inSpecs || !strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "     ") {
			continue
		}
		name, rest, ok := strings.Cut(strings.TrimSpace(line), " (")
		if !ok {
			continue
		}
		ver := strings.TrimSuffix(rest, ")")
		if i := strings.IndexByte(ver, '-'); i > 0 {
			ver = ver[:i] // platform gems: nokogiri (1.16.0-x86_64-linux)
		}
		out = append(out, Package{Ecosystem: EcoRubyGems, Name: name, Version: ver})
	}
	return out, sc.Err()
}

func parseComposerLock(data []byte) ([]Package, error) {
	var lock struct {
		Packages    []composerPkg `json:"packages"`
		PackagesDev []composerPkg `json:"packages-dev"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	var out []Package
	for _, p := range append(lock.Packages, lock.PackagesDev...) {
		out = append(out, Package{Ecosystem: EcoPackagist, Name: p.Name, Version: p.Version})
	}
	return out, nil
}

type composerPkg struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// gradle.lockfile: "group:artifact:version=configurations" per line.
func parseGradleLock(data []byte) ([]Package, error) {
	var out []Package
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || strings.HasPrefix(line, "empty=") {
			continue
		}
		coord, _, _ := strings.Cut(line, "=")
		parts := strings.Split(coord, ":")
		if len(parts) != 3 {
			continue
		}
		out = append(out, Package{Ecosystem: EcoMaven, Name: parts[0] + ":" + parts[1], Version: parts[2]})
	}
	return out, sc.Err()
}

// packages.lock.json (NuGet): resolved versions per target framework.
func parseNuGetLock(data []byte) ([]Package, error) {
	var lock struct {
		Dependencies map[string]map[string]struct {
			Type     string `json:"type"`
			Resolved string `json:"resolved"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	var out []Package
	for _, deps := range lock.Dependencies {
		for name, d := range deps {
			if strings.EqualFold(d.Type, "Project") {
				continue
			}
			out = append(out, Package{Ecosystem: EcoNuGet, Name: name, Version: d.Resolved})
		}
	}
	return out, nil
}

func parsePubspecLock(data []byte) ([]Package, error) {
	var lock struct {
		Packages map[string]struct {
			Version string `yaml:"version"`
			Source  string `yaml:"source"`
		} `yaml:"packages"`
	}
	if err := yaml.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	var out []Package
	for name, p := range lock.Packages {
		if p.Source != "" && p.Source != "hosted" {
			continue // git, path or sdk packages
		}
		out = append(out, Package{Ecosystem: EcoPub, Name: name, Version: p.Version})
	}
	return out, nil
}
