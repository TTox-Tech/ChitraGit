package audit

import (
	"errors"
	"strings"
)

// Version ordering, per ecosystem, the way OSV ranges are meant to be
// read (https://ossf.github.io/osv-schema/#affectedrangestype-field):
// SemVer 2.0 for npm, crates.io, Go, Pub and NuGet, PEP 440 for PyPI,
// Gem::Version for RubyGems, Maven's ComparableVersion and PHP's
// version_compare for Packagist. These are ChitraGit's own
// implementations; they cover the forms real lockfiles use and fall back
// to treating an unusual version as "cannot compare", which never turns
// a mismatch into a false "affected".

var errBadVersion = errors.New("unparsable version")

// compareVersions returns -1, 0 or +1 as a is older than, equal to or
// newer than b in ecosystem eco.
func compareVersions(eco, a, b string) (int, error) {
	switch eco {
	case EcoPyPI:
		return comparePEP440(a, b)
	case EcoRubyGems:
		return compareGem(a, b)
	case EcoMaven:
		return compareMaven(a, b), nil
	case EcoPackagist:
		return comparePHP(a, b), nil
	case EcoNuGet:
		return compareSemver(strings.ToLower(a), strings.ToLower(b))
	case EcoPub:
		return comparePub(a, b)
	}
	return compareSemver(a, b)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpNum compares two digit strings of any length.
func cmpNum(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return cmpInt(len(a), len(b))
	}
	return strings.Compare(a, b)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ---- SemVer 2.0 (lenient: any number of release parts, missing = 0) ----

type semver struct {
	release []string
	pre     []string
}

func parseSemver(s string) (semver, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	s, _, _ = strings.Cut(s, "+") // build metadata does not order
	core, pre, hasPre := strings.Cut(s, "-")
	var v semver
	for _, p := range strings.Split(core, ".") {
		if !isDigits(p) {
			return v, errBadVersion
		}
		v.release = append(v.release, p)
	}
	if hasPre {
		if pre == "" {
			return v, errBadVersion
		}
		v.pre = strings.Split(pre, ".")
	}
	return v, nil
}

func compareSemver(a, b string) (int, error) {
	va, err := parseSemver(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseSemver(b)
	if err != nil {
		return 0, err
	}
	for i := 0; i < max(len(va.release), len(vb.release)); i++ {
		x, y := "0", "0"
		if i < len(va.release) {
			x = va.release[i]
		}
		if i < len(vb.release) {
			y = vb.release[i]
		}
		if c := cmpNum(x, y); c != 0 {
			return c, nil
		}
	}
	// A pre-release is older than its release.
	switch {
	case len(va.pre) == 0 && len(vb.pre) == 0:
		return 0, nil
	case len(va.pre) == 0:
		return 1, nil
	case len(vb.pre) == 0:
		return -1, nil
	}
	for i := 0; i < min(len(va.pre), len(vb.pre)); i++ {
		x, y := va.pre[i], vb.pre[i]
		nx, ny := isDigits(x), isDigits(y)
		var c int
		switch {
		case nx && ny:
			c = cmpNum(x, y)
		case nx:
			c = -1 // numeric identifiers sort before alphanumeric ones
		case ny:
			c = 1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c, nil
		}
	}
	return cmpInt(len(va.pre), len(vb.pre)), nil
}

// comparePub is SemVer, except that pub orders build metadata: a build
// sorts after the plain release, and builds compare like pre-releases.
func comparePub(a, b string) (int, error) {
	if c, err := compareSemver(a, b); err != nil || c != 0 {
		return c, err
	}
	_, ba, okA := strings.Cut(a, "+")
	_, bb, okB := strings.Cut(b, "+")
	switch {
	case !okA && !okB:
		return 0, nil
	case !okA:
		return -1, nil
	case !okB:
		return 1, nil
	}
	return compareSemver("0-"+ba, "0-"+bb)
}

// ---- PEP 440 ----

type pep440 struct {
	epoch   string
	release []string
	// phase orders the stages around one release: a bare dev release,
	// then pre-releases, then the release, then post-releases.
	preKind int    // 0 = dev-only release, 1 = a, 2 = b, 3 = rc, 4 = none (the release)
	preNum  string // number after a/b/rc
	post    string // "" = no post-release
	dev     string // "" = not a dev release
	local   []string
}

func parsePEP440(s string) (pep440, error) {
	v := pep440{epoch: "0", preKind: 4}
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "v")
	s, local, hasLocal := strings.Cut(s, "+")
	if hasLocal {
		v.local = strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '-' || r == '_' })
	}
	if e, rest, ok := strings.Cut(s, "!"); ok {
		if !isDigits(e) {
			return v, errBadVersion
		}
		v.epoch, s = e, rest
	}
	// Release: digits and dots.
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	rel := strings.TrimSuffix(s[:i], ".")
	if rel == "" {
		return v, errBadVersion
	}
	for _, p := range strings.Split(rel, ".") {
		if !isDigits(p) {
			return v, errBadVersion
		}
		v.release = append(v.release, p)
	}
	for len(v.release) > 1 && strings.Trim(v.release[len(v.release)-1], "0") == "" {
		v.release = v.release[:len(v.release)-1] // 1.0.0 == 1
	}
	rest := s[i:]
	// Pre, post and dev segments, each optionally separated by . - or _.
	sep := func() { rest = strings.TrimLeft(rest, ".-_") }
	num := func() string {
		sep()
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		n := rest[:j]
		rest = rest[j:]
		if n == "" {
			n = "0"
		}
		return n
	}
	if strings.HasPrefix(rest, "-") && len(rest) > 1 && isDigits(rest[1:2]) { // 1.0-1 is a post-release
		rest = rest[1:]
		v.post = num()
	}
	sep()
	for _, p := range []struct {
		word string
		kind int
	}{{"alpha", 1}, {"beta", 2}, {"preview", 3}, {"pre", 3}, {"rc", 3}, {"a", 1}, {"b", 2}, {"c", 3}} {
		if strings.HasPrefix(rest, p.word) {
			rest = rest[len(p.word):]
			v.preKind, v.preNum = p.kind, num()
			break
		}
	}
	sep()
	for _, w := range []string{"post", "rev", "r"} {
		if v.post != "" {
			break
		}
		if strings.HasPrefix(rest, w) {
			rest = rest[len(w):]
			v.post = num()
			break
		}
	}
	sep()
	if strings.HasPrefix(rest, "dev") {
		rest = rest[3:]
		v.dev = num()
	}
	if rest != "" {
		return v, errBadVersion
	}
	if v.dev != "" && v.preKind == 4 && v.post == "" {
		v.preKind = 0 // 1.0.dev1 sorts before 1.0a1
	}
	return v, nil
}

func comparePEP440(a, b string) (int, error) {
	va, err := parsePEP440(a)
	if err != nil {
		return 0, err
	}
	vb, err := parsePEP440(b)
	if err != nil {
		return 0, err
	}
	if c := cmpNum(va.epoch, vb.epoch); c != 0 {
		return c, nil
	}
	for i := 0; i < max(len(va.release), len(vb.release)); i++ {
		x, y := "0", "0"
		if i < len(va.release) {
			x = va.release[i]
		}
		if i < len(vb.release) {
			y = vb.release[i]
		}
		if c := cmpNum(x, y); c != 0 {
			return c, nil
		}
	}
	if c := cmpInt(va.preKind, vb.preKind); c != 0 {
		return c, nil
	}
	if va.preNum != "" && vb.preNum != "" { // same kind, so both or neither have one
		if c := cmpNum(va.preNum, vb.preNum); c != 0 {
			return c, nil
		}
	}
	// No post-release sorts before any post-release.
	switch {
	case va.post == "" && vb.post != "":
		return -1, nil
	case va.post != "" && vb.post == "":
		return 1, nil
	case va.post != "":
		if c := cmpNum(va.post, vb.post); c != 0 {
			return c, nil
		}
	}
	// A dev release sorts before the same version without one.
	switch {
	case va.dev != "" && vb.dev == "":
		return -1, nil
	case va.dev == "" && vb.dev != "":
		return 1, nil
	case va.dev != "":
		if c := cmpNum(va.dev, vb.dev); c != 0 {
			return c, nil
		}
	}
	// A local version sorts after the public one.
	for i := 0; i < max(len(va.local), len(vb.local)); i++ {
		if i >= len(va.local) {
			return -1, nil
		}
		if i >= len(vb.local) {
			return 1, nil
		}
		x, y := va.local[i], vb.local[i]
		nx, ny := isDigits(x), isDigits(y)
		var c int
		switch {
		case nx && ny:
			c = cmpNum(x, y)
		case nx:
			c = 1 // numbers sort after strings in local segments
		case ny:
			c = -1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c, nil
		}
	}
	return 0, nil
}

// ---- RubyGems (Gem::Version) ----

// splitAlnum splits "1.0.0.pre2" into 1 0 0 pre 2, breaking on dots and
// on every switch between digits and letters.
func splitAlnum(s string, seps string) []string {
	var out []string
	cur := ""
	kind := 0 // 1 digit, 2 other
	for _, r := range s {
		if strings.ContainsRune(seps, r) {
			if cur != "" {
				out = append(out, cur)
			}
			cur, kind = "", 0
			continue
		}
		k := 2
		if r >= '0' && r <= '9' {
			k = 1
		}
		if kind != 0 && k != kind {
			out = append(out, cur)
			cur = ""
		}
		cur += string(r)
		kind = k
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func compareGem(a, b string) (int, error) {
	sa, sb := gemSegments(a), gemSegments(b)
	if sa == nil || sb == nil {
		return 0, errBadVersion
	}
	for i := 0; i < max(len(sa), len(sb)); i++ {
		x, y := "0", "0"
		if i < len(sa) {
			x = sa[i]
		}
		if i < len(sb) {
			y = sb[i]
		}
		nx, ny := isDigits(x), isDigits(y)
		var c int
		switch {
		case nx && ny:
			c = cmpNum(x, y)
		case nx:
			c = 1 // a letter segment marks a pre-release
		case ny:
			c = -1
		default:
			c = strings.Compare(x, y)
		}
		if c != 0 {
			return c, nil
		}
	}
	return 0, nil
}

// gemSegments is Gem::Version#canonical_segments: zeros at the end of the
// release part and at the end of the pre-release part do not count, so
// 1.0.0.alpha.1 and 1.alpha.1 are the same version.
func gemSegments(v string) []string {
	segs := splitAlnum(strings.TrimSpace(v), ".-")
	if len(segs) == 0 || !isDigits(segs[0]) {
		return nil
	}
	i := 0
	for i < len(segs) && isDigits(segs[i]) {
		i++
	}
	trim := func(p []string) []string {
		for len(p) > 0 && isDigits(p[len(p)-1]) && strings.Trim(p[len(p)-1], "0") == "" {
			p = p[:len(p)-1]
		}
		return p
	}
	out := append([]string{}, trim(segs[:i])...)
	out = append(out, trim(segs[i:])...)
	if len(out) == 0 {
		out = []string{"0"}
	}
	return out
}

// ---- Maven (ComparableVersion) ----

// A Maven version is a list of items: numbers, qualifiers and sub-lists.
// "-" and every switch between digits and letters start a sub-list, and
// null items (0, "", ga, final, release) at the end of a list are dropped,
// so 1.0.0-alpha is [1 [alpha]].
type mavenItem struct {
	kind int // mavenInt, mavenStr, mavenList
	s    string
	list []*mavenItem
}

const (
	mavenInt = iota
	mavenStr
	mavenList
)

var mavenOrder = []string{"alpha", "beta", "milestone", "rc", "snapshot", "", "sp"}

var mavenAlias = map[string]string{"a": "alpha", "b": "beta", "m": "milestone", "cr": "rc", "ga": "", "final": "", "release": ""}

func newMavenItem(digits bool, s string) *mavenItem {
	if digits {
		return &mavenItem{kind: mavenInt, s: strings.TrimLeft(s, "0")}
	}
	if a, ok := mavenAlias[s]; ok {
		s = a
	}
	return &mavenItem{kind: mavenStr, s: s}
}

func (m *mavenItem) isNull() bool {
	switch m.kind {
	case mavenInt:
		return m.s == ""
	case mavenStr:
		return m.s == ""
	}
	return len(m.list) == 0
}

func (m *mavenItem) normalize() {
	for i := len(m.list) - 1; i >= 0; i-- {
		it := m.list[i]
		if it.isNull() {
			m.list = append(m.list[:i], m.list[i+1:]...)
		} else if it.kind != mavenList {
			break
		}
	}
}

func parseMaven(v string) *mavenItem {
	v = strings.ToLower(strings.TrimSpace(v))
	root := &mavenItem{kind: mavenList}
	cur := root
	lists := []*mavenItem{root}
	sub := func() {
		l := &mavenItem{kind: mavenList}
		cur.list = append(cur.list, l)
		cur = l
		lists = append(lists, l)
	}
	start, digits := 0, false
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '.':
			if i == start {
				cur.list = append(cur.list, &mavenItem{kind: mavenInt})
			} else {
				cur.list = append(cur.list, newMavenItem(digits, v[start:i]))
			}
			start = i + 1
		case c == '-':
			if i == start {
				cur.list = append(cur.list, &mavenItem{kind: mavenInt})
			} else {
				cur.list = append(cur.list, newMavenItem(digits, v[start:i]))
			}
			start = i + 1
			sub()
		case c >= '0' && c <= '9':
			if !digits && i > start {
				cur.list = append(cur.list, newMavenItem(false, v[start:i]))
				start = i
				sub()
			}
			digits = true
		default:
			if digits && i > start {
				cur.list = append(cur.list, newMavenItem(true, v[start:i]))
				start = i
				sub()
			}
			digits = false
		}
	}
	if len(v) > start {
		cur.list = append(cur.list, newMavenItem(digits, v[start:]))
	}
	for i := len(lists) - 1; i >= 0; i-- {
		lists[i].normalize()
	}
	return root
}

func mavenQualifierKey(s string) string {
	for i, q := range mavenOrder {
		if q == s {
			return string(rune('0' + i))
		}
	}
	return string(rune('0'+len(mavenOrder))) + "-" + s
}

// mavenCmp compares x with y; a nil y means "nothing there".
func mavenCmp(x, y *mavenItem) int {
	if x == nil && y == nil {
		return 0
	}
	if x == nil {
		return -mavenCmp(y, nil)
	}
	switch x.kind {
	case mavenInt:
		if y == nil {
			return cmpNum(x.s, "")
		}
		if y.kind == mavenInt {
			return cmpNum(x.s, y.s)
		}
		return 1 // a number is newer than a qualifier or a sub-list
	case mavenStr:
		if y == nil {
			return strings.Compare(mavenQualifierKey(x.s), mavenQualifierKey(""))
		}
		switch y.kind {
		case mavenInt, mavenList:
			return -1
		}
		return strings.Compare(mavenQualifierKey(x.s), mavenQualifierKey(y.s))
	}
	if y == nil {
		if len(x.list) == 0 {
			return 0
		}
		return mavenCmp(x.list[0], nil)
	}
	switch y.kind {
	case mavenInt:
		return -1
	case mavenStr:
		return 1
	}
	for i := 0; i < max(len(x.list), len(y.list)); i++ {
		var a, b *mavenItem
		if i < len(x.list) {
			a = x.list[i]
		}
		if i < len(y.list) {
			b = y.list[i]
		}
		if c := mavenCmp(a, b); c != 0 {
			return c
		}
	}
	return 0
}

func compareMaven(a, b string) int { return mavenCmp(parseMaven(a), parseMaven(b)) }

// ---- Packagist (PHP version_compare) ----

func phpRank(s string) int {
	switch {
	case isDigits(s):
		return 10
	case s == "#":
		return 9
	}
	switch strings.ToLower(s) {
	case "dev":
		return 1
	case "alpha", "a":
		return 2
	case "beta", "b":
		return 3
	case "rc":
		return 4
	case "pl", "p":
		return 6
	}
	return 0 // any other word sorts first
}

func comparePHP(a, b string) int {
	sa := splitAlnum(strings.TrimLeft(strings.TrimSpace(a), "vV"), ".-_+")
	sb := splitAlnum(strings.TrimLeft(strings.TrimSpace(b), "vV"), ".-_+")
	for i := 0; i < max(len(sa), len(sb)); i++ {
		x, y := "#", "#"
		if i < len(sa) {
			x = sa[i]
		}
		if i < len(sb) {
			y = sb[i]
		}
		// A longer version with an extra number is newer.
		if i >= len(sa) && isDigits(y) {
			return -1
		}
		if i >= len(sb) && isDigits(x) {
			return 1
		}
		if isDigits(x) && isDigits(y) {
			if c := cmpNum(x, y); c != 0 {
				return c
			}
			continue
		}
		rx, ry := phpRank(x), phpRank(y)
		if x == "#" {
			rx = 5 // "nothing more" sits between rc and pl
		}
		if y == "#" {
			ry = 5
		}
		if c := cmpInt(rx, ry); c != 0 {
			return c
		}
	}
	return 0
}
