package audit

import (
	"path"
	"regexp"
	"strings"
)

// OSV ecosystem names, as the OSV database spells them.
const (
	EcoGo        = "Go"
	EcoNpm       = "npm"
	EcoPyPI      = "PyPI"
	EcoCrates    = "crates.io"
	EcoRubyGems  = "RubyGems"
	EcoPackagist = "Packagist"
	EcoMaven     = "Maven"
	EcoNuGet     = "NuGet"
	EcoPub       = "Pub"
)

// Ecosystems lists every ecosystem the dependency check understands, in
// the order the vulnerability database downloads them.
var Ecosystems = []string{EcoGo, EcoNpm, EcoPyPI, EcoCrates, EcoRubyGems, EcoPackagist, EcoMaven, EcoNuGet, EcoPub}

// Package is one resolved dependency read from a lockfile.
type Package struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
	File      string `json:"file"` // the lockfile, relative to the repo root
}

type lockParser func(data []byte) ([]Package, error)

// lockfiles maps a file's base name to its parser.
var lockfiles = map[string]lockParser{
	"go.mod":              parseGoMod,
	"package-lock.json":   parsePackageLock,
	"npm-shrinkwrap.json": parsePackageLock,
	"yarn.lock":           parseYarnLock,
	"pnpm-lock.yaml":      parsePnpmLock,
	"Cargo.lock":          parseCargoLock,
	"poetry.lock":         parsePythonTOMLLock,
	"uv.lock":             parsePythonTOMLLock,
	"pdm.lock":            parsePythonTOMLLock,
	"Pipfile.lock":        parsePipfileLock,
	"Gemfile.lock":        parseGemfileLock,
	"gems.locked":         parseGemfileLock,
	"composer.lock":       parseComposerLock,
	"gradle.lockfile":     parseGradleLock,
	"packages.lock.json":  parseNuGetLock,
	"pubspec.lock":        parsePubspecLock,
}

var requirementsName = regexp.MustCompile(`^requirements.*\.txt$`)

// skippedDirs hold copies of other projects' files (installed packages,
// vendored code), not this repo's own dependency choices.
var skippedDirs = []string{"node_modules", "vendor", "bower_components", ".git", "site-packages", ".venv", "venv"}

// IsLockfile reports whether a repo path is a lockfile the dependency
// check reads.
func IsLockfile(p string) bool {
	for _, dir := range strings.Split(path.Dir(p), "/") {
		for _, s := range skippedDirs {
			if dir == s {
				return false
			}
		}
	}
	return parserFor(path.Base(p)) != nil
}

func parserFor(base string) lockParser {
	if p, ok := lockfiles[base]; ok {
		return p
	}
	if requirementsName.MatchString(base) {
		return parseRequirements
	}
	return nil
}

// ParseLockfile reads the packages a lockfile pins. A file it does not
// recognise yields nothing. Malformed content is an error, which the
// caller reports without failing the whole repo.
func ParseLockfile(file string, data []byte) ([]Package, error) {
	p := parserFor(path.Base(file))
	if p == nil {
		return nil, nil
	}
	pkgs, err := p(data)
	if err != nil {
		return nil, err
	}
	out := pkgs[:0]
	seen := map[string]bool{}
	for _, pk := range pkgs {
		pk.Name, pk.Version = strings.TrimSpace(pk.Name), normalizeVersion(pk.Ecosystem, strings.TrimSpace(pk.Version))
		if pk.Name == "" || !plausibleVersion(pk.Version) {
			continue
		}
		pk.File = file
		key := pk.Ecosystem + "\x00" + pk.Name + "\x00" + pk.Version
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, pk)
	}
	return out, nil
}

// plausibleVersion drops things lockfiles put where a version usually is,
// such as git URLs, local paths and workspace links, which no
// vulnerability database can match.
func plausibleVersion(v string) bool {
	if v == "" || v == "*" {
		return false
	}
	if strings.ContainsAny(v, " /\\") || strings.Contains(v, ":") {
		return false
	}
	c := v[0]
	return c >= '0' && c <= '9'
}

// normalizeName folds a package name the way its ecosystem compares names,
// so lockfile spellings match the database's.
func normalizeName(eco, name string) string {
	switch eco {
	case EcoPyPI:
		// PEP 503: case-insensitive, runs of "-", "_" and "." are equal.
		var b strings.Builder
		dash := false
		for _, r := range strings.ToLower(name) {
			if r == '-' || r == '_' || r == '.' {
				if !dash {
					b.WriteByte('-')
				}
				dash = true
				continue
			}
			dash = false
			b.WriteRune(r)
		}
		return b.String()
	case EcoNuGet, EcoPackagist, EcoRubyGems:
		return strings.ToLower(name)
	}
	return name
}

// normalizeVersion strips spellings that the database does not use.
func normalizeVersion(eco, v string) string {
	switch eco {
	case EcoGo:
		v = strings.TrimPrefix(v, "v")
		v = strings.TrimSuffix(v, "+incompatible")
	case EcoPackagist:
		v = strings.TrimPrefix(v, "v")
	}
	return v
}
