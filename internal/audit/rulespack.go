package audit

import (
	"bytes"
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// RulesSchema is the rules pack format this build understands. A pack with
// a higher schema is refused, so an old binary never misreads new rules.
const RulesSchema = 1

// The built-in rules pack. It is also what CI signs and publishes, so the
// built-in pack and the published one come from the same file.
//
//go:embed rules/pack.json
var builtinPackJSON []byte

// RulesPack holds the data-driven part of the actions and agents checks:
// which files are agent files, the content patterns to look for, and
// per-rule overrides for the rules written in Go. It is updated from
// ChitraGit's own GitHub Releases with `chitragit audit rules update`.
type RulesPack struct {
	Schema  int          `json:"schema"`
	Version string       `json:"version"` // YYYY.MM.DD or YYYY.MM.DD.N
	Actions ActionsRules `json:"actions"`
	Agents  AgentsRules  `json:"agents"`

	// Source says where the active pack came from: "built-in" or the path
	// of the downloaded file. Not part of the JSON.
	Source string `json:"-"`

	compiled []compiledRule
	files    []compiledGlob
}

// RuleOverride changes a rule written in Go without a new binary.
type RuleOverride struct {
	Severity Severity `json:"severity,omitempty"`
	Disabled bool     `json:"disabled,omitempty"`
}

// ActionsRules configures the GitHub Actions check.
type ActionsRules struct {
	Rules map[string]RuleOverride `json:"rules,omitempty"`
	// FirstPartyOwners are action owners whose unpinned use is reported
	// with a lower severity (for example "actions").
	FirstPartyOwners []string `json:"first_party_owners"`
	// UntrustedInputs are regexes over a `${{ }}` expression that mark
	// values an outside contributor controls, such as an issue title.
	UntrustedInputs []string `json:"untrusted_inputs"`
	// ZizmorEquivalents maps a built-in rule to the zizmor audits that
	// cover it. When zizmor reports one of them in a file, the built-in
	// finding for that file is dropped so the same problem is not listed
	// twice.
	ZizmorEquivalents map[string][]string `json:"zizmor_equivalents,omitempty"`

	untrusted []*regexp.Regexp
}

// AgentsRules configures the agent files check.
type AgentsRules struct {
	Rules        map[string]RuleOverride `json:"rules,omitempty"`
	Files        []AgentFileSpec         `json:"files"`
	ContentRules []ContentRule           `json:"content_rules"`
}

// AgentFileSpec says which paths are agent files and what kind they are.
type AgentFileSpec struct {
	Pattern string `json:"pattern"` // glob; ** matches any number of folders; case-insensitive
	Kind    string `json:"kind"`    // instructions | skill | command | mcp | settings
	Tool    string `json:"tool,omitempty"`
}

// Agent file kinds.
const (
	KindInstructions = "instructions"
	KindSkill        = "skill"
	KindCommand      = "command"
	KindMCP          = "mcp"
	KindSettings     = "settings"
)

var agentKinds = []string{KindInstructions, KindSkill, KindCommand, KindMCP, KindSettings}

// ContentRule is a regex looked for in agent files.
type ContentRule struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Severity   Severity `json:"severity"`
	Confidence string   `json:"confidence,omitempty"` // high (default) | low
	Kinds      []string `json:"kinds,omitempty"`      // empty = every kind
	Regex      string   `json:"regex"`
	Hint       string   `json:"hint"`
	// CitedSeverity, when set, is used for a match in a Markdown agent file
	// that sits inside a code span, a code block or quotes: the way a guide
	// or a security tool cites an attack rather than makes one. Such a
	// match is also reported with low confidence.
	CitedSeverity Severity `json:"cited_severity,omitempty"`
	// KnownURLs matches URLs of well-known, expected targets, such as the
	// official rustup installer. A match on a line whose URLs all match it
	// is reported with KnownSeverity instead of Severity.
	KnownURLs     string   `json:"known_urls,omitempty"`
	KnownSeverity Severity `json:"known_severity,omitempty"`
}

type compiledRule struct {
	ContentRule
	re    *regexp.Regexp
	known *regexp.Regexp
}

var urlRe = regexp.MustCompile(`https?://[^\s'"|)<>]+`)

// severity is the rule's severity for a match on line: KnownSeverity when
// the line has URLs and every one is a known one, Severity otherwise.
func (r compiledRule) severity(line string) Severity {
	if r.known == nil {
		return r.Severity
	}
	urls := urlRe.FindAllString(line, -1)
	for _, u := range urls {
		if !r.known.MatchString(u) {
			return r.Severity
		}
	}
	if len(urls) == 0 {
		return r.Severity
	}
	return r.KnownSeverity
}

type compiledGlob struct {
	parts []string
	spec  AgentFileSpec
}

var (
	builtinOnce sync.Once
	builtinPack *RulesPack
	builtinErr  error
)

// BuiltinRules returns the rules pack compiled into this binary.
func BuiltinRules() (*RulesPack, error) {
	builtinOnce.Do(func() {
		builtinPack, builtinErr = ParseRules(builtinPackJSON)
		if builtinPack != nil {
			builtinPack.Source = "built-in"
		}
	})
	return builtinPack, builtinErr
}

// ParseRules reads and checks a rules pack. Every regex must compile and
// every rule must be complete, so a broken pack is refused as a whole.
func ParseRules(data []byte) (*RulesPack, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	var p RulesPack
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("rules pack: %w", err)
	}
	if p.Schema < 1 || p.Schema > RulesSchema {
		return nil, fmt.Errorf("rules pack schema %d is not supported by this version (it reads schema %d); update ChitraGit", p.Schema, RulesSchema)
	}
	if _, err := parsePackVersion(p.Version); err != nil {
		return nil, err
	}
	for _, s := range p.Actions.UntrustedInputs {
		re, err := regexp.Compile(s)
		if err != nil {
			return nil, fmt.Errorf("rules pack: actions untrusted input %q: %w", s, err)
		}
		p.Actions.untrusted = append(p.Actions.untrusted, re)
	}
	for _, f := range p.Agents.Files {
		if f.Pattern == "" || !oneOf(f.Kind, agentKinds) {
			return nil, fmt.Errorf("rules pack: agent file %q has unknown kind %q", f.Pattern, f.Kind)
		}
		parts := strings.Split(strings.ToLower(f.Pattern), "/")
		for _, part := range parts {
			if _, err := path.Match(part, ""); err != nil {
				return nil, fmt.Errorf("rules pack: agent file pattern %q: %w", f.Pattern, err)
			}
		}
		p.files = append(p.files, compiledGlob{parts: parts, spec: f})
	}
	seen := map[string]bool{}
	for _, r := range p.Agents.ContentRules {
		if r.ID == "" || r.Title == "" || r.Hint == "" || seen[r.ID] {
			return nil, fmt.Errorf("rules pack: content rule %q is incomplete or repeated", r.ID)
		}
		seen[r.ID] = true
		if r.Severity.Rank() < 0 {
			return nil, fmt.Errorf("rules pack: content rule %s has unknown severity %q", r.ID, r.Severity)
		}
		for _, k := range r.Kinds {
			if !oneOf(k, agentKinds) {
				return nil, fmt.Errorf("rules pack: content rule %s has unknown kind %q", r.ID, k)
			}
		}
		re, err := regexp.Compile(r.Regex)
		if err != nil {
			return nil, fmt.Errorf("rules pack: content rule %s: %w", r.ID, err)
		}
		if r.Confidence == "" {
			r.Confidence = ConfidenceHigh
		}
		if r.CitedSeverity != "" && r.CitedSeverity.Rank() < 0 {
			return nil, fmt.Errorf("rules pack: content rule %s has unknown cited_severity %q", r.ID, r.CitedSeverity)
		}
		c := compiledRule{ContentRule: r, re: re}
		if r.KnownURLs != "" {
			if r.KnownSeverity.Rank() < 0 {
				return nil, fmt.Errorf("rules pack: content rule %s has unknown known_severity %q", r.ID, r.KnownSeverity)
			}
			if c.known, err = regexp.Compile(r.KnownURLs); err != nil {
				return nil, fmt.Errorf("rules pack: content rule %s known_urls: %w", r.ID, err)
			}
		}
		p.compiled = append(p.compiled, c)
	}
	for _, o := range []map[string]RuleOverride{p.Actions.Rules, p.Agents.Rules} {
		for id, v := range o {
			if v.Severity != "" && v.Severity.Rank() < 0 {
				return nil, fmt.Errorf("rules pack: override for %s has unknown severity %q", id, v.Severity)
			}
		}
	}
	return &p, nil
}

// AgentFile reports whether p (a slash-separated repo path) is an agent
// file, and its kind.
func (p *RulesPack) AgentFile(file string) (AgentFileSpec, bool) {
	segs := strings.Split(strings.ToLower(file), "/")
	for _, g := range p.files {
		if globMatch(g.parts, segs) {
			return g.spec, true
		}
	}
	return AgentFileSpec{}, false
}

// ContentRuleCount is the number of data-driven agent content rules.
func (p *RulesPack) ContentRuleCount() int { return len(p.compiled) }

// FileSpecCount is the number of agent file patterns.
func (p *RulesPack) FileSpecCount() int { return len(p.files) }

// globMatch matches path segments against pattern segments, where "**"
// matches zero or more segments.
func globMatch(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for i := 0; i <= len(segs); i++ {
				if globMatch(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}

// apply returns a Go rule's severity after the pack's override, and
// whether the rule is enabled.
func apply(overrides map[string]RuleOverride, rule string, def Severity) (Severity, bool) {
	o, ok := overrides[rule]
	if !ok {
		return def, true
	}
	if o.Disabled {
		return def, false
	}
	if o.Severity != "" {
		return o.Severity, true
	}
	return def, true
}

// parsePackVersion splits YYYY.MM.DD[.N] into numbers for comparison.
func parsePackVersion(v string) ([]int, error) {
	parts := strings.Split(v, ".")
	if len(parts) < 3 || len(parts) > 4 {
		return nil, fmt.Errorf("rules pack version %q is not YYYY.MM.DD or YYYY.MM.DD.N", v)
	}
	out := make([]int, 4)
	for i, s := range parts {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("rules pack version %q is not YYYY.MM.DD or YYYY.MM.DD.N", v)
		}
		out[i] = n
	}
	return out, nil
}

// ComparePackVersions returns -1, 0 or 1. Invalid versions sort first.
func ComparePackVersions(a, b string) int {
	x, errA := parsePackVersion(a)
	y, errB := parsePackVersion(b)
	switch {
	case errA != nil && errB != nil:
		return 0
	case errA != nil:
		return -1
	case errB != nil:
		return 1
	}
	for i := range x {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// RulesPublicKey is the base64 Ed25519 public key that signs published
// rules packs. Release builds set it with -ldflags from the repository
// variable KODEKAVACH_RULES_PUBLIC_KEY; a build without it never accepts a
// downloaded pack and uses the built-in one.
var RulesPublicKey = ""

// ErrNoRulesKey means this build cannot verify downloaded rules packs.
var ErrNoRulesKey = errors.New("this build has no rules signing key, so it only uses its built-in rules pack")

// ErrBadSignature means a pack's signature did not verify.
var ErrBadSignature = errors.New("rules pack signature does not match; the pack was refused")

// VerifyRules checks a pack's detached signature (base64 Ed25519 over the
// exact bytes of the pack) against RulesPublicKey.
func VerifyRules(data, sig []byte) error {
	return verifyWith(RulesPublicKey, data, sig)
}

func verifyWith(pubB64 string, data, sig []byte) error {
	pubB64 = strings.TrimSpace(pubB64)
	if pubB64 == "" {
		return ErrNoRulesKey
	}
	pub, err := base64.StdEncoding.DecodeString(pubB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("the rules signing key in this build is malformed")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("%w (the signature is malformed)", ErrBadSignature)
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), data, raw) {
		return ErrBadSignature
	}
	return nil
}

// SignRules signs a pack with a base64 Ed25519 private key (seed or full
// key) and returns the base64 signature. Used by CI, not by the CLI.
func SignRules(privB64 string, data []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privB64))
	if err != nil {
		return "", errors.New("signing key is not base64")
	}
	var key ed25519.PrivateKey
	switch len(raw) {
	case ed25519.SeedSize:
		key = ed25519.NewKeyFromSeed(raw)
	case ed25519.PrivateKeySize:
		key = ed25519.PrivateKey(raw)
	default:
		return "", errors.New("signing key has the wrong length")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)), nil
}

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}
