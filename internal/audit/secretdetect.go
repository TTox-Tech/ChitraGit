package audit

import (
	_ "embed"
	"fmt"
	"math"
	"regexp"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// secretRulesTOML is the secret rule set: patterns, keywords, entropy
// limits and allowlists. Its format follows the Gitleaks rule file it was
// converted from, so upstream rule updates can be taken as they are.
//
//go:embed secret_rules.toml
var secretRulesTOML string

// secretRule is one compiled rule.
type secretRule struct {
	id          string
	description string
	re          *regexp.Regexp // nil = path-only rule
	path        *regexp.Regexp // nil = any path
	secretGroup int
	entropy     float64
	keywords    []string // lower case; empty = always run
	allowlists  []*allowlist
}

// allowlist says when a match is not a secret.
type allowlist struct {
	and         bool // every criterion that is set must hold (default: any one)
	commits     map[string]bool
	paths       []*regexp.Regexp
	regexes     []*regexp.Regexp
	regexTarget string // "" (the secret) | "match" | "line"
	stopwords   []string
}

// secretRuleSet is the whole compiled rule file.
type secretRuleSet struct {
	rules  []*secretRule
	global []*allowlist
}

// rawSecret is one match before it becomes a finding.
type rawSecret struct {
	rule, description, secret, match string
	line                             int // 0-based line within the fragment
}

var (
	rulesOnce sync.Once
	rulesSet  *secretRuleSet
	rulesErr  error
)

// secretRules compiles the embedded rule file once.
func secretRules() (*secretRuleSet, error) {
	rulesOnce.Do(func() { rulesSet, rulesErr = parseSecretRules(secretRulesTOML) })
	return rulesSet, rulesErr
}

type allowlistTOML struct {
	Condition   string   `toml:"condition"`
	Commits     []string `toml:"commits"`
	Paths       []string `toml:"paths"`
	Regexes     []string `toml:"regexes"`
	RegexTarget string   `toml:"regexTarget"`
	Stopwords   []string `toml:"stopwords"`
}

func parseSecretRules(text string) (*secretRuleSet, error) {
	var file struct {
		Allowlist  *allowlistTOML  `toml:"allowlist"`
		Allowlists []allowlistTOML `toml:"allowlists"`
		Rules      []struct {
			ID          string          `toml:"id"`
			Description string          `toml:"description"`
			Regex       string          `toml:"regex"`
			Path        string          `toml:"path"`
			SecretGroup int             `toml:"secretGroup"`
			Entropy     float64         `toml:"entropy"`
			Keywords    []string        `toml:"keywords"`
			Allowlists  []allowlistTOML `toml:"allowlists"`
		} `toml:"rules"`
	}
	if _, err := toml.Decode(text, &file); err != nil {
		return nil, fmt.Errorf("secret rules: %w", err)
	}
	set := &secretRuleSet{}
	globals := file.Allowlists
	if file.Allowlist != nil {
		globals = append([]allowlistTOML{*file.Allowlist}, globals...)
	}
	for _, a := range globals {
		al, err := compileAllowlist(a)
		if err != nil {
			return nil, fmt.Errorf("secret rules: global allowlist: %w", err)
		}
		set.global = append(set.global, al)
	}
	for _, r := range file.Rules {
		if r.ID == "" || (r.Regex == "" && r.Path == "") {
			return nil, fmt.Errorf("secret rules: rule %q needs an id and a regex or path", r.ID)
		}
		sr := &secretRule{id: r.ID, description: r.Description, secretGroup: r.SecretGroup, entropy: r.Entropy}
		var err error
		if r.Regex != "" {
			if sr.re, err = regexp.Compile(r.Regex); err != nil {
				return nil, fmt.Errorf("secret rules: %s: %w", r.ID, err)
			}
			if sr.secretGroup > sr.re.NumSubexp() {
				return nil, fmt.Errorf("secret rules: %s: secretGroup %d but the regex has %d groups", r.ID, sr.secretGroup, sr.re.NumSubexp())
			}
		}
		if r.Path != "" {
			if sr.path, err = regexp.Compile(r.Path); err != nil {
				return nil, fmt.Errorf("secret rules: %s: path: %w", r.ID, err)
			}
		}
		for _, k := range r.Keywords {
			sr.keywords = append(sr.keywords, strings.ToLower(k))
		}
		for _, a := range r.Allowlists {
			al, err := compileAllowlist(a)
			if err != nil {
				return nil, fmt.Errorf("secret rules: %s: %w", r.ID, err)
			}
			sr.allowlists = append(sr.allowlists, al)
		}
		set.rules = append(set.rules, sr)
	}
	return set, nil
}

func compileAllowlist(a allowlistTOML) (*allowlist, error) {
	al := &allowlist{and: strings.EqualFold(a.Condition, "AND"), regexTarget: a.RegexTarget}
	switch a.RegexTarget {
	case "", "secret", "match", "line":
	default:
		return nil, fmt.Errorf("unknown regexTarget %q", a.RegexTarget)
	}
	if len(a.Commits) > 0 {
		al.commits = map[string]bool{}
		for _, c := range a.Commits {
			al.commits[strings.ToLower(c)] = true
		}
	}
	for _, p := range a.Paths {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("allowlist path: %w", err)
		}
		al.paths = append(al.paths, re)
	}
	for _, p := range a.Regexes {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("allowlist regex: %w", err)
		}
		al.regexes = append(al.regexes, re)
	}
	for _, w := range a.Stopwords {
		al.stopwords = append(al.stopwords, strings.ToLower(w))
	}
	return al, nil
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	if s == "" {
		return false
	}
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func (a *allowlist) commitAllowed(c string) bool { return c != "" && a.commits[strings.ToLower(c)] }

func (a *allowlist) hasStopword(secret string) bool {
	s := strings.ToLower(secret)
	for _, w := range a.stopwords {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// skipsFile reports whether a whole file (or commit) is allowed, which
// only commit and path criteria can decide.
func (a *allowlist) skipsFile(path, commit string) bool {
	commitOK, pathOK := a.commitAllowed(commit), anyMatch(a.paths, path)
	if !a.and {
		return commitOK || pathOK
	}
	if len(a.regexes) > 0 || len(a.stopwords) > 0 {
		return false // decided per match
	}
	var checks []bool
	if len(a.commits) > 0 {
		checks = append(checks, commitOK)
	}
	if len(a.paths) > 0 {
		checks = append(checks, pathOK)
	}
	return allTrue(checks)
}

// skipsMatch reports whether one match is allowed.
func (a *allowlist) skipsMatch(m rawSecret, line, path, commit string) bool {
	target := m.secret
	switch a.regexTarget {
	case "match":
		target = m.match
	case "line":
		target = line
	}
	regexOK, stopOK := anyMatch(a.regexes, target), a.hasStopword(m.secret)
	if !a.and {
		return regexOK || stopOK
	}
	var checks []bool
	if len(a.commits) > 0 {
		checks = append(checks, a.commitAllowed(commit))
	}
	if len(a.paths) > 0 {
		checks = append(checks, anyMatch(a.paths, path))
	}
	if len(a.regexes) > 0 {
		checks = append(checks, regexOK)
	}
	if len(a.stopwords) > 0 {
		checks = append(checks, stopOK)
	}
	return allTrue(checks)
}

func allTrue(bs []bool) bool {
	if len(bs) == 0 {
		return false
	}
	for _, b := range bs {
		if !b {
			return false
		}
	}
	return true
}

// detect finds secrets in raw, the text of one file or of the lines one
// commit added to it. path is '/' separated. Allow comments in the text
// are deliberately not honoured: an archived repo must not be able to
// hide its own secrets.
func (set *secretRuleSet) detect(raw, path, commit string) []rawSecret {
	for _, a := range set.global {
		if a.skipsFile(path, commit) {
			return nil
		}
	}
	lower := strings.ToLower(raw)
	var out []rawSecret
	var newlines []int // computed on first match
	for _, r := range set.rules {
		if !r.wants(lower) {
			continue
		}
		out = append(out, r.detect(set, raw, path, commit, &newlines)...)
	}
	return dropGenericDuplicates(out)
}

// wants is the keyword prefilter: a rule with keywords only runs when one
// of them appears in the text.
func (r *secretRule) wants(lower string) bool {
	if len(r.keywords) == 0 {
		return true
	}
	for _, k := range r.keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

func (r *secretRule) detect(set *secretRuleSet, raw, path, commit string, newlines *[]int) []rawSecret {
	for _, a := range r.allowlists {
		if a.skipsFile(path, commit) {
			return nil
		}
	}
	if r.path != nil {
		if !r.path.MatchString(path) {
			return nil
		}
		if r.re == nil { // the file itself is the secret, e.g. a keystore
			return []rawSecret{{rule: r.id, description: r.description, secret: "file:" + path, match: path}}
		}
	}
	if r.re == nil {
		return nil
	}
	idx := r.re.FindAllStringIndex(raw, -1)
	if len(idx) == 0 {
		return nil
	}
	if *newlines == nil {
		*newlines = lineStarts(raw)
	}
	var out []rawSecret
	for _, m := range idx {
		match := strings.Trim(raw[m[0]:m[1]], "\n")
		start := m[0] + strings.Index(raw[m[0]:m[1]], match)
		lineNo, lineStart, lineEnd := lineOf(*newlines, raw, start, start+len(match))
		found := rawSecret{rule: r.id, description: r.description, secret: match, match: match, line: lineNo}
		if groups := r.re.FindStringSubmatch(match); len(groups) >= 2 {
			if r.secretGroup > 0 {
				found.secret = groups[r.secretGroup]
			} else {
				for _, g := range groups[1:] {
					if g != "" {
						found.secret = g
						break
					}
				}
			}
		}
		if r.entropy != 0 && shannon(found.secret) <= r.entropy {
			continue
		}
		line := raw[lineStart:lineEnd]
		allowed := false
		for _, a := range set.global {
			if a.skipsMatch(found, line, path, commit) {
				allowed = true
				break
			}
		}
		for _, a := range r.allowlists {
			if allowed {
				break
			}
			allowed = a.skipsMatch(found, line, path, commit)
		}
		if !allowed {
			out = append(out, found)
		}
	}
	return out
}

// lineStarts lists the offset where each line after the first begins.
func lineStarts(s string) []int {
	starts := []int{}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineOf returns the 0-based line of offset start, and the bounds of the
// text from that line's start to the end of the line where end falls.
func lineOf(starts []int, s string, start, end int) (line, lineStart, lineEnd int) {
	for _, st := range starts {
		if st > start {
			break
		}
		line++
		lineStart = st
	}
	lineEnd = len(s)
	for _, st := range starts {
		if st > end {
			lineEnd = st - 1
			break
		}
	}
	return line, lineStart, lineEnd
}

// dropGenericDuplicates removes a generic-pattern match when a specific
// rule found the same secret on the same line.
func dropGenericDuplicates(ms []rawSecret) []rawSecret {
	out := ms[:0:0]
	for _, m := range ms {
		keep := true
		if strings.Contains(strings.ToLower(m.rule), "generic") {
			for _, o := range ms {
				if o.line == m.line && o.rule != m.rule && strings.Contains(o.secret, m.secret) &&
					!strings.Contains(strings.ToLower(o.rule), "generic") {
					keep = false
					break
				}
			}
		}
		if keep {
			out = append(out, m)
		}
	}
	return out
}

// shannon is the Shannon entropy of s in bits per character.
func shannon(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]int{}
	for _, c := range s {
		counts[c]++
	}
	inv := 1 / float64(len(s))
	var e float64
	for _, n := range counts {
		f := float64(n) * inv
		e -= f * math.Log2(f)
	}
	return e
}
