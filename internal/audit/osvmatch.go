package audit

import (
	"context"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Vuln is one advisory that affects one package version.
type Vuln struct {
	ID       string
	Summary  string
	Aliases  []string
	Severity Severity
	Score    float64
	Fixed    []string // versions that fix it, lowest first
	// Key names the vulnerability across databases: its CVE when it has
	// one, else its smallest ID. Set by mergeAliases.
	Key string
}

func (v Vuln) key() string {
	if v.Key != "" {
		return v.Key
	}
	return v.ID
}

// affects reports whether version is affected by a, following the OSV
// schema's evaluation rules, and which later versions fix it.
func (a *advisory) affects(eco, version string) (bool, []string) {
	if _, err := compareVersions(eco, version, version); err != nil {
		// A version the ecosystem's rules cannot read can still be
		// listed exactly.
		return slices.Contains(a.Versions, version), nil
	}
	hit := slices.Contains(a.Versions, version)
	var fixes []string
	for _, rg := range a.Ranges {
		in, fixed := inRange(eco, version, rg)
		hit = hit || in
		fixes = append(fixes, fixed...)
	}
	if !hit {
		return false, nil
	}
	// Only fixes above the version in use help.
	var later []string
	for _, f := range fixes {
		if c, err := compareVersions(eco, version, f); err == nil && c < 0 && !slices.Contains(later, f) {
			later = append(later, f)
		}
	}
	slices.SortFunc(later, func(x, y string) int {
		c, err := compareVersions(eco, x, y)
		if err != nil {
			return strings.Compare(x, y)
		}
		return c
	})
	return true, later
}

// inRange evaluates one range: the version is affected after an
// "introduced" at or below it, until a "fixed" or "limit" at or below it,
// or a "last_affected" below it.
func inRange(eco, ver string, rg osvRange) (bool, []string) {
	type point struct {
		kind, v string
	}
	var pts []point
	var fixes []string
	for _, e := range rg.Events {
		switch {
		case e.Introduced != "":
			pts = append(pts, point{"introduced", e.Introduced})
		case e.Fixed != "":
			pts = append(pts, point{"fixed", e.Fixed})
			fixes = append(fixes, e.Fixed)
		case e.LastAffected != "":
			pts = append(pts, point{"last_affected", e.LastAffected})
		case e.Limit != "":
			pts = append(pts, point{"limit", e.Limit})
		}
	}
	// cmp returns how the version in use compares with an event version;
	// "0" is below everything.
	cmp := func(v string) (int, bool) {
		if v == "0" {
			return 1, true
		}
		c, err := compareVersions(eco, ver, v)
		return c, err == nil
	}
	// Sort events by version so each one opens or closes the range in turn.
	slices.SortStableFunc(pts, func(x, y point) int {
		if x.v == y.v {
			return 0
		}
		if x.v == "0" {
			return -1
		}
		if y.v == "0" {
			return 1
		}
		c, err := compareVersions(eco, x.v, y.v)
		if err != nil {
			return 0
		}
		return c
	})
	affected := false
	for _, p := range pts {
		c, ok := cmp(p.v)
		if !ok {
			continue
		}
		switch p.kind {
		case "introduced":
			if c >= 0 {
				affected = true
			}
		case "fixed", "limit":
			if c >= 0 {
				affected = false
			}
		case "last_affected":
			if c > 0 {
				affected = false
			}
		}
	}
	return affected, fixes
}

// Vulns lists the advisories that affect one package version.
func (v *VulnDB) Vulns(ctx context.Context, p Package) ([]Vuln, error) {
	advs, err := v.lookup(ctx, p.Ecosystem, p.Name)
	if err != nil {
		return nil, err
	}
	var out []Vuln
	for i := range advs {
		a := &advs[i]
		hit, fixes := a.affects(p.Ecosystem, p.Version)
		if !hit {
			continue
		}
		out = append(out, Vuln{ID: a.ID, Summary: a.Summary, Aliases: a.Aliases, Severity: a.Severity, Score: a.Score, Fixed: fixes})
	}
	return out, nil
}

// recordSeverity picks a severity for an OSV record: malware is critical;
// then the database's own rating (GitHub advisories carry one); then the
// CVSS v3 base score; otherwise medium, so an unrated advisory is still
// reported at the default --fail-on high without being hidden.
func recordSeverity(r *osvRecord) (Severity, float64) {
	if strings.HasPrefix(r.ID, "MAL-") {
		return SevCritical, 0
	}
	var score float64
	for _, s := range r.Severity {
		if strings.HasPrefix(s.Type, "CVSS_V3") {
			if v, ok := cvss3Score(s.Score); ok {
				score = v
			}
		}
	}
	if label, ok := r.DatabaseSpecific["severity"].(string); ok {
		switch strings.ToUpper(label) {
		case "CRITICAL":
			return SevCritical, score
		case "HIGH":
			return SevHigh, score
		case "MODERATE", "MEDIUM":
			return SevMedium, score
		case "LOW":
			return SevLow, score
		}
	}
	if score > 0 {
		return scoreSeverity(score), score
	}
	return SevMedium, 0
}

// scoreSeverity maps a CVSS score to its qualitative rating.
func scoreSeverity(s float64) Severity {
	switch {
	case s >= 9:
		return SevCritical
	case s >= 7:
		return SevHigh
	case s >= 4:
		return SevMedium
	case s > 0:
		return SevLow
	}
	return SevInfo
}

// cvss3Score computes the CVSS v3.0/v3.1 base score from a vector such as
// "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H".
func cvss3Score(vector string) (float64, bool) {
	parts := strings.Split(vector, "/")
	if len(parts) < 2 || !strings.HasPrefix(parts[0], "CVSS:3") {
		return 0, false
	}
	m := map[string]string{}
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, ":")
		if ok {
			m[k] = v
		}
	}
	weights := map[string]map[string]float64{
		"AV": {"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2},
		"AC": {"L": 0.77, "H": 0.44},
		"UI": {"N": 0.85, "R": 0.62},
		"C":  {"H": 0.56, "L": 0.22, "N": 0},
		"I":  {"H": 0.56, "L": 0.22, "N": 0},
		"A":  {"H": 0.56, "L": 0.22, "N": 0},
	}
	val := map[string]float64{}
	for k, w := range weights {
		x, ok := w[m[k]]
		if !ok {
			return 0, false
		}
		val[k] = x
	}
	changed := m["S"] == "C"
	if m["S"] != "C" && m["S"] != "U" {
		return 0, false
	}
	pr := map[string]float64{"N": 0.85, "L": 0.62, "H": 0.27}
	if changed {
		pr = map[string]float64{"N": 0.85, "L": 0.68, "H": 0.5}
	}
	prv, ok := pr[m["PR"]]
	if !ok {
		return 0, false
	}
	iss := 1 - (1-val["C"])*(1-val["I"])*(1-val["A"])
	var impact float64
	if changed {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	} else {
		impact = 6.42 * iss
	}
	if impact <= 0 {
		return 0, true
	}
	expl := 8.22 * val["AV"] * val["AC"] * prv * val["UI"]
	if changed {
		return roundUp(math.Min(1.08*(impact+expl), 10)), true
	}
	return roundUp(math.Min(impact+expl, 10)), true
}

// roundUp is CVSS v3.1's Roundup: the smallest one-decimal number not
// below x, computed in integers to avoid float noise.
func roundUp(x float64) float64 {
	i := int64(math.Round(x * 100000))
	if i%10000 == 0 {
		return float64(i) / 100000
	}
	return (math.Floor(float64(i)/10000) + 1) / 10
}

// formatScore prints a CVSS score such as 9.8.
func formatScore(s float64) string { return strconv.FormatFloat(s, 'f', 1, 64) }
