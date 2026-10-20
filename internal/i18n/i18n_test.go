package i18n

import (
	"regexp"
	"strings"
	"testing"
)

func TestT(t *testing.T) {
	SetLanguage("en")
	if got := T("init.created", "path", "/x"); got != "Archive created at /x" {
		t.Errorf("T = %q", got)
	}
	if got := T("no.such.key"); got != "no.such.key" {
		t.Errorf("missing key should return the key, got %q", got)
	}
}

func TestUnknownLanguageFallsBack(t *testing.T) {
	if SetLanguage("xx") {
		t.Error("unknown language should report false")
	}
	if T("doctor.ok") != "ok" {
		t.Error("should fall back to English")
	}
}

// Every placeholder in a translation must also exist in English, so a
// translator cannot introduce a {name} the code never fills in.
func TestCataloguesConsistent(t *testing.T) {
	en := mustLoad("en")
	ph := regexp.MustCompile(`\{[a-z_]+\}`)
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		lang := strings.TrimSuffix(e.Name(), ".toml")
		m := mustLoad(lang)
		for k, v := range m {
			base, ok := en[k]
			if !ok {
				t.Errorf("%s: key %q not in en.toml", lang, k)
				continue
			}
			want := map[string]bool{}
			for _, p := range ph.FindAllString(base, -1) {
				want[p] = true
			}
			for _, p := range ph.FindAllString(v, -1) {
				if !want[p] {
					t.Errorf("%s: %q uses unknown placeholder %s", lang, k, p)
				}
			}
		}
	}
}
