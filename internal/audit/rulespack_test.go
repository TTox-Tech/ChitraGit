package audit

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuiltinRulesPack(t *testing.T) {
	p := mustRules(t)
	if p.Schema != RulesSchema || p.Source != "built-in" || p.FileSpecCount() < 50 || p.ContentRuleCount() < 10 {
		t.Errorf("built-in pack = schema %d, %s, %d files, %d rules", p.Schema, p.Source, p.FileSpecCount(), p.ContentRuleCount())
	}
	for _, r := range p.compiled {
		if !strings.HasPrefix(r.ID, "agent-") {
			t.Errorf("content rule %s should be named agent-…", r.ID)
		}
	}
}

func TestParseRulesRefusesBrokenPacks(t *testing.T) {
	for name, pack := range map[string]string{
		"future schema": `{"schema":99,"version":"2030.01.01"}`,
		"bad version":   `{"schema":1,"version":"latest"}`,
		"bad regex":     `{"schema":1,"version":"2030.01.01","agents":{"content_rules":[{"id":"x","title":"x","hint":"x","severity":"low","regex":"("}]}}`,
		"bad severity":  `{"schema":1,"version":"2030.01.01","agents":{"content_rules":[{"id":"x","title":"x","hint":"x","severity":"huge","regex":"a"}]}}`,
		"bad kind":      `{"schema":1,"version":"2030.01.01","agents":{"files":[{"pattern":"x.md","kind":"magic"}]}}`,
		"duplicate id":  `{"schema":1,"version":"2030.01.01","agents":{"content_rules":[{"id":"x","title":"x","hint":"x","severity":"low","regex":"a"},{"id":"x","title":"x","hint":"x","severity":"low","regex":"b"}]}}`,
		"not json":      `{`,
	} {
		if _, err := ParseRules([]byte(pack)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestComparePackVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"2026.11.06", "2026.11.06", 0}, {"2026.11.06", "2026.11.07", -1}, {"2026.12.01", "2026.11.30", 1},
		{"2026.11.06.2", "2026.11.06", 1}, {"2026.11.06", "2026.11.06.1", -1}, {"bad", "2026.01.01", -1},
	} {
		if got := ComparePackVersions(c.a, c.b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// withKey installs a fresh signing key for the test and returns the
// private seed (base64).
func withKey(t *testing.T) string {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	old := RulesPublicKey
	RulesPublicKey = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() { RulesPublicKey = old })
	return base64.StdEncoding.EncodeToString(priv.Seed())
}

func newerPack(version string) []byte {
	s := strings.Replace(string(builtinPackJSON), `"version": "`+mustVersion()+`"`, `"version": "`+version+`"`, 1)
	return []byte(s)
}

func mustVersion() string { p, _ := BuiltinRules(); return p.Version }

func TestInstallAndLoadSignedRules(t *testing.T) {
	dir := t.TempDir()
	// Without a key in the build nothing can be installed.
	if _, err := InstallRules(dir, newerPack("2099.01.01"), []byte("x")); !errors.Is(err, ErrNoRulesKey) {
		t.Fatalf("no key: %v", err)
	}
	seed := withKey(t)
	pack := newerPack("2099.01.01")
	if string(pack) == string(builtinPackJSON) {
		t.Fatal("test pack has the built-in version")
	}
	sig, err := SignRules(seed, pack)
	if err != nil {
		t.Fatal(err)
	}

	// A wrong signature is refused and nothing is written.
	other := withKey(t)
	badSig, _ := SignRules(other, pack)
	RulesPublicKey = base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(mustB64(t, seed)).Public().(ed25519.PublicKey))
	if _, err := InstallRules(dir, pack, []byte(badSig)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("bad signature: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, rulesFile)); !os.IsNotExist(err) {
		t.Fatal("a refused pack was written")
	}
	// A tampered pack with the right signature is refused too.
	if _, err := InstallRules(dir, append([]byte(nil), append(pack, ' ')...), []byte(sig)); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("tampered pack: %v", err)
	}

	res, err := InstallRules(dir, pack, []byte(sig))
	if err != nil || !res.Updated || res.Current != "2099.01.01" || res.Previous != mustVersion() {
		t.Fatalf("install = %+v, %v", res, err)
	}
	p, warn := LoadRules(dir)
	if warn != nil || p.Version != "2099.01.01" || !strings.HasSuffix(p.Source, rulesFile) {
		t.Fatalf("load = %v %v", p.Version, warn)
	}
	if RulesLastChecked(dir).IsZero() || RulesUpdateDue(dir, time.Hour) {
		t.Error("check time not recorded")
	}

	// An older pack never replaces a newer one.
	older := newerPack("2098.01.01")
	oldSig, _ := SignRules(seed, older)
	if res, err := InstallRules(dir, older, []byte(oldSig)); err != nil || res.Updated || res.Current != "2099.01.01" {
		t.Errorf("downgrade = %+v, %v", res, err)
	}

	// If the file on disk is changed, it is no longer trusted.
	b, _ := os.ReadFile(filepath.Join(dir, rulesFile))
	b = []byte(strings.Replace(string(b), `"signature":"`, `"signature":"AAAA`, 1))
	if err := os.WriteFile(filepath.Join(dir, rulesFile), b, 0o600); err != nil {
		t.Fatal(err)
	}
	p, warn = LoadRules(dir)
	if warn == nil || p.Source != "built-in" {
		t.Errorf("tampered file: %v %v", p.Source, warn)
	}
}

func mustB64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUpdateRulesDownloads(t *testing.T) {
	seed := withKey(t)
	pack := newerPack("2099.02.03")
	sig, _ := SignRules(seed, pack)
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		switch r.URL.Path {
		case "/rel/" + RulesAsset:
			_, _ = w.Write(pack)
		case "/rel/" + RulesSigAsset:
			_, _ = w.Write([]byte(sig + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	res, err := UpdateRules(context.Background(), dir, srv.URL+"/rel/", srv.Client())
	if err != nil || !res.Updated || res.Current != "2099.02.03" {
		t.Fatalf("update = %+v, %v", res, err)
	}
	if len(hits) != 2 {
		t.Errorf("requests = %v", hits)
	}
	// Up to date: nothing changes.
	if res, err := UpdateRules(context.Background(), dir, srv.URL+"/rel/", srv.Client()); err != nil || res.Updated {
		t.Errorf("second update = %+v, %v", res, err)
	}
	// A missing release is an error, and the installed pack stays.
	if _, err := UpdateRules(context.Background(), dir, srv.URL+"/missing/", srv.Client()); err == nil {
		t.Error("missing release accepted")
	}
	if p, _ := LoadRules(dir); p.Version != "2099.02.03" {
		t.Errorf("pack after failed update = %s", p.Version)
	}
}
