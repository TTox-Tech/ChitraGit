// Package audit holds KodeKavach, ChitraGit's read-only security audit of
// archived repos: what a finding is, how severe it is, and the checks that
// produce findings. It never writes to a repo.
//
// Orchestration (which repos, the lock, storing results) lives in the
// engine package; this package only reads a repo through the hardened git
// runner and reports what it found.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Checks. M9 ships secrets; the others follow in later milestones.
const (
	CheckSecrets = "secrets"
)

// AllChecks lists every check this build can run, in display order.
var AllChecks = []string{CheckSecrets}

// History modes for checks that read git history.
const (
	HistoryHead        = "head"        // only the files at HEAD
	HistoryIncremental = "incremental" // commits added since the last audit (all history the first time)
	HistoryFull        = "full"        // every commit, ignoring what earlier audits covered
)

// Severity of a finding.
type Severity string

const (
	SevInfo     Severity = "info"
	SevLow      Severity = "low"
	SevMedium   Severity = "medium"
	SevHigh     Severity = "high"
	SevCritical Severity = "critical"
)

// Severities lists every severity, lowest first.
var Severities = []Severity{SevInfo, SevLow, SevMedium, SevHigh, SevCritical}

// Rank orders severities; an unknown severity ranks lowest.
func (s Severity) Rank() int {
	for i, v := range Severities {
		if v == s {
			return i
		}
	}
	return -1
}

// ParseSeverity accepts a severity name in any case.
func ParseSeverity(s string) (Severity, bool) {
	v := Severity(strings.ToLower(strings.TrimSpace(s)))
	return v, v.Rank() >= 0
}

// Confidence says how sure a check is. Pattern rules for a specific
// provider's key format are "high"; generic patterns are "low" and worth a
// human look before acting.
const (
	ConfidenceHigh = "high"
	ConfidenceLow  = "low"
)

// Finding is one problem in one repo. A secret that appears in many
// commits is one finding: the ID depends on the repo, the rule and the
// secret itself, never on where it was seen.
type Finding struct {
	ID         string   `json:"id"`
	RepoID     string   `json:"repo_id"`
	RepoPath   string   `json:"repo"`
	Check      string   `json:"check"`
	Rule       string   `json:"rule"`
	Title      string   `json:"title"`
	Severity   Severity `json:"severity"`
	Confidence string   `json:"confidence"`

	// Where it was first introduced (the oldest commit seen so far).
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Commit string `json:"commit,omitempty"`
	Author string `json:"author,omitempty"` // name only; emails are not stored
	Date   string `json:"date,omitempty"`   // author date, RFC 3339

	// Preview is the redacted value, e.g. "AKIA****". The full secret is
	// never stored, logged or printed.
	Preview     string `json:"preview,omitempty"`
	Occurrences int    `json:"occurrences"` // commits or files it was seen in
	Hint        string `json:"hint"`

	Status      string `json:"status"` // open | ignored
	IgnoreNote  string `json:"ignore_reason,omitempty"`
	FirstSeenAt string `json:"first_seen_at,omitempty"`
	LastSeenAt  string `json:"last_seen_at,omitempty"`
	New         bool   `json:"new,omitempty"` // first found by this audit
}

// Finding statuses.
const (
	StatusOpen    = "open"
	StatusIgnored = "ignored"
)

// FindingID derives the stable ID of a finding from its repo, rule and a
// fingerprint of the value (for secrets: a hash of the secret).
func FindingID(repoID, check, rule, valueFingerprint string) string {
	h := sha256.Sum256([]byte(repoID + "\x00" + check + "\x00" + rule + "\x00" + valueFingerprint))
	return hex.EncodeToString(h[:8])
}

// Fingerprint hashes a secret so it can be compared without being stored.
func Fingerprint(secret string) string {
	h := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(h[:])
}

// Redact shows at most the first four characters of a secret, and nothing
// at all of a short one, so a preview can never reveal a usable value.
func Redact(secret string) string {
	secret = strings.TrimSpace(secret)
	if utf8.RuneCountInString(secret) < 12 {
		return "****"
	}
	r := []rune(secret)
	return string(r[:4]) + "****"
}

// Summary counts open findings by severity.
type Summary struct {
	Total      int              `json:"total"`
	New        int              `json:"new"`
	Ignored    int              `json:"ignored"`
	BySeverity map[Severity]int `json:"by_severity"`
}

// Summarise counts findings. Ignored findings count only as Ignored.
func Summarise(fs []Finding) Summary {
	s := Summary{BySeverity: map[Severity]int{}}
	for _, f := range fs {
		if f.Status == StatusIgnored {
			s.Ignored++
			continue
		}
		s.Total++
		s.BySeverity[f.Severity]++
		if f.New {
			s.New++
		}
	}
	return s
}

// AtLeast reports whether any open finding is at or above min.
func AtLeast(fs []Finding, min Severity) bool {
	for _, f := range fs {
		if f.Status != StatusIgnored && f.Severity.Rank() >= min.Rank() {
			return true
		}
	}
	return false
}

func secretHint(date string) string {
	since := ""
	if len(date) >= 10 {
		since = " since " + date[:10]
	}
	return fmt.Sprintf("Treat it as leaked and rotate it. It is in the repo's history%s; deleting the file does not remove it.", since)
}

// shortTitle turns a Gitleaks rule description such as "Uncovered a GitHub
// Personal Access Token, potentially leading to ..." into "GitHub Personal
// Access Token".
func shortTitle(desc, rule string) string {
	t := strings.TrimSpace(desc)
	for _, p := range []string{"Uncovered a ", "Uncovered an ", "Uncovered ", "Detected a ", "Detected an ", "Detected ",
		"Identified a ", "Identified an ", "Identified ", "Found a ", "Found an ", "Found ", "Discovered a ", "Discovered an ", "Discovered "} {
		if strings.HasPrefix(t, p) {
			t = strings.TrimPrefix(t, p)
			break
		}
	}
	for _, p := range []string{"pattern that resembles ", "pattern resembling ", "pattern that may indicate ",
		"a potential ", "potential ", "a possible ", "possible ", "an ", "a "} {
		t = strings.TrimPrefix(t, p)
	}
	if i := strings.IndexAny(t, ",;("); i > 0 {
		t = t[:i]
	}
	for _, sep := range []string{" potentially ", " which ", " that ", " posing ", " risking ", " provided in "} {
		if i := strings.Index(t, sep); i > 0 {
			t = t[:i]
		}
	}
	t = strings.TrimSuffix(strings.TrimSpace(t), ".")
	if t == "" {
		return rule
	}
	return t
}
