package audit

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // matches the server's published checksum
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func pkgSet(ps []Package) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Ecosystem+" "+p.Name+"@"+p.Version)
	}
	slices.Sort(out)
	return out
}

func TestParseLockfiles(t *testing.T) {
	cases := []struct {
		file, data string
		want       []string
	}{
		{"go.mod", "module x\ngo 1.21\nrequire (\n\tgolang.org/x/net v0.17.0\n\tgithub.com/a/b v1.2.3+incompatible // indirect\n\tgithub.com/local/c v1.0.0\n)\nreplace github.com/local/c => ../c\nreplace golang.org/x/net => golang.org/x/net v0.23.0\n",
			[]string{"Go github.com/a/b@1.2.3", "Go golang.org/x/net@0.23.0"}},
		{"web/package-lock.json", `{"lockfileVersion":3,"packages":{"":{"name":"app"},"node_modules/lodash":{"version":"4.17.15"},
			"node_modules/a/node_modules/@scope/b":{"version":"1.0.0"},"node_modules/linked":{"link":true},"packages/ws":{"version":"0.1.0"},
			"node_modules/git-dep":{"version":"git+https://github.com/x/y.git#abc"}}}`,
			[]string{"npm @scope/b@1.0.0", "npm lodash@4.17.15"}},
		{"package-lock.json", `{"lockfileVersion":1,"dependencies":{"a":{"version":"1.0.0","dependencies":{"b":{"version":"2.0.0"}}}}}`,
			[]string{"npm a@1.0.0", "npm b@2.0.0"}},
		{"yarn.lock", "# yarn lockfile v1\n\n\"@babel/core@^7.0.0\", \"@babel/core@^7.1.0\":\n  version \"7.12.3\"\n  dependencies:\n    debug \"^4.1.0\"\n\nlodash@^4.17.0:\n  version \"4.17.20\"\n",
			[]string{"npm @babel/core@7.12.3", "npm lodash@4.17.20"}},
		{"yarn.lock", "__metadata:\n  version: 6\n\n\"lodash@npm:^4.17.21\":\n  version: 4.17.21\n  resolution: \"lodash@npm:4.17.21\"\n\n\"app@workspace:.\":\n  version: 0.0.0-use.local\n",
			[]string{"npm lodash@4.17.21"}},
		{"pnpm-lock.yaml", "lockfileVersion: 5.4\npackages:\n  /lodash/4.17.21:\n    resolution: {integrity: x}\n  /@types/node/18.0.0_react@17.0.2:\n    dev: true\n",
			[]string{"npm @types/node@18.0.0", "npm lodash@4.17.21"}},
		{"pnpm-lock.yaml", "lockfileVersion: '9.0'\npackages:\n  lodash@4.17.21:\n    resolution: {integrity: x}\n  '@scope/pkg@1.2.3(react@18.0.0)':\n    resolution: {integrity: y}\n",
			[]string{"npm @scope/pkg@1.2.3", "npm lodash@4.17.21"}},
		{"Cargo.lock", "[[package]]\nname = \"smallvec\"\nversion = \"1.6.0\"\n", []string{"crates.io smallvec@1.6.0"}},
		{"poetry.lock", "[[package]]\nname = \"Django\"\nversion = \"3.2.0\"\n\n[[package]]\nname = \"mine\"\nversion = \"0.1.0\"\n[package.source]\ntype = \"directory\"\nurl = \"../mine\"\n",
			[]string{"PyPI Django@3.2.0"}},
		{"uv.lock", "[[package]]\nname = \"requests\"\nversion = \"2.19.0\"\nsource = { registry = \"https://pypi.org/simple\" }\n\n[[package]]\nname = \"app\"\nversion = \"0.1.0\"\nsource = { editable = \".\" }\n",
			[]string{"PyPI requests@2.19.0"}},
		{"Pipfile.lock", `{"default":{"flask":{"version":"==1.0"}},"develop":{"pytest":{"version":"==7.0.0"}}}`,
			[]string{"PyPI flask@1.0", "PyPI pytest@7.0.0"}},
		{"requirements-dev.txt", "# pinned\nDjango==3.2.0 \\\n    --hash=sha256:abc\nrequests[socks]==2.19.0 ; python_version > \"3\"\nflask>=2\n-r other.txt\n",
			[]string{"PyPI Django@3.2.0", "PyPI requests@2.19.0"}},
		{"Gemfile.lock", "GEM\n  remote: https://rubygems.org/\n  specs:\n    nokogiri (1.16.0-x86_64-linux)\n      racc (~> 1.4)\n    rails (6.0.0)\n\nPLATFORMS\n  ruby\n",
			[]string{"RubyGems nokogiri@1.16.0", "RubyGems rails@6.0.0"}},
		{"composer.lock", `{"packages":[{"name":"guzzlehttp/guzzle","version":"v7.4.0"}],"packages-dev":[{"name":"x/y","version":"dev-main"}]}`,
			[]string{"Packagist guzzlehttp/guzzle@7.4.0"}},
		{"gradle.lockfile", "# comment\norg.apache.logging.log4j:log4j-core:2.14.1=compileClasspath\nempty=\n",
			[]string{"Maven org.apache.logging.log4j:log4j-core@2.14.1"}},
		{"packages.lock.json", `{"version":1,"dependencies":{"net6.0":{"Newtonsoft.Json":{"type":"Direct","resolved":"12.0.1"},"Mine":{"type":"Project"}}}}`,
			[]string{"NuGet Newtonsoft.Json@12.0.1"}},
		{"pubspec.lock", "packages:\n  http:\n    source: hosted\n    version: \"0.13.0\"\n  mine:\n    source: path\n    version: \"1.0.0\"\n",
			[]string{"Pub http@0.13.0"}},
	}
	for _, c := range cases {
		got, err := ParseLockfile(c.file, []byte(c.data))
		if err != nil {
			t.Errorf("%s: %v", c.file, err)
			continue
		}
		if g := pkgSet(got); !slices.Equal(g, c.want) {
			t.Errorf("%s:\n got %v\nwant %v", c.file, g, c.want)
		}
	}
	if _, err := ParseLockfile("package-lock.json", []byte("{not json")); err == nil {
		t.Error("a malformed lockfile should be an error")
	}
}

func TestIsLockfile(t *testing.T) {
	for p, want := range map[string]bool{
		"go.mod": true, "web/yarn.lock": true, "requirements.txt": true, "reqs/requirements-prod.txt": true,
		"node_modules/x/package-lock.json": false, "vendor/github.com/a/go.mod": false, "README.md": false,
		"package.json": false, "a/.venv/lib/requirements.txt": false,
	} {
		if IsLockfile(p) != want {
			t.Errorf("IsLockfile(%q) = %v", p, !want)
		}
	}
}

func TestAdvisoryMatching(t *testing.T) {
	a := advisory{ID: "GHSA-1", Ranges: []osvRange{{Type: "ECOSYSTEM", Events: []osvEvent{
		{Introduced: "0"}, {Fixed: "3.2.5"}, {Introduced: "4.0.0"}, {Fixed: "4.1.2"},
	}}}}
	for v, want := range map[string]bool{"3.2.0": true, "3.2.5": false, "3.10": false, "4.0.0": true, "4.1.1": true, "4.1.2": false} {
		got, fixes := a.affects(EcoPyPI, v)
		if got != want {
			t.Errorf("%s affected = %v, want %v", v, got, want)
		}
		if v == "3.2.0" && (len(fixes) != 2 || fixes[0] != "3.2.5") {
			t.Errorf("fixes for 3.2.0 = %v", fixes)
		}
	}
	last := advisory{ID: "X", Ranges: []osvRange{{Type: "SEMVER", Events: []osvEvent{{Introduced: "1.0.0"}, {LastAffected: "1.4.0"}}}}}
	if ok, _ := last.affects(EcoNpm, "1.4.0"); !ok {
		t.Error("last_affected is inclusive")
	}
	if ok, _ := last.affects(EcoNpm, "1.4.1"); ok {
		t.Error("versions after last_affected are not affected")
	}
	listed := advisory{ID: "MAL-1", Versions: []string{"1.0.1"}}
	if ok, _ := listed.affects(EcoNpm, "1.0.1"); !ok {
		t.Error("an exactly listed version is affected")
	}
	if ok, _ := listed.affects(EcoNpm, "1.0.2"); ok {
		t.Error("an unlisted version is not affected")
	}
}

func TestCVSS3Score(t *testing.T) {
	for vec, want := range map[string]float64{
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H": 9.8,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H": 10.0,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N": 6.1,
		"CVSS:3.0/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:N/A:N": 5.5,
		"CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:N/I:N/A:N": 0,
	} {
		got, ok := cvss3Score(vec)
		if !ok || got != want {
			t.Errorf("%s = %v (%v), want %v", vec, got, ok, want)
		}
	}
	if _, ok := cvss3Score("CVSS:4.0/AV:N"); ok {
		t.Error("v4 vectors are not v3")
	}
}

func TestRecordSeverity(t *testing.T) {
	r := &osvRecord{ID: "MAL-2024-1"}
	if s, _ := recordSeverity(r); s != SevCritical {
		t.Errorf("malware = %s", s)
	}
	r = &osvRecord{ID: "GHSA-x", DatabaseSpecific: map[string]any{"severity": "MODERATE"}}
	if s, _ := recordSeverity(r); s != SevMedium {
		t.Errorf("MODERATE = %s", s)
	}
	r = &osvRecord{ID: "PYSEC-1"}
	r.Severity = append(r.Severity, struct {
		Type  string `json:"type"`
		Score string `json:"score"`
	}{"CVSS_V3", "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"})
	if s, score := recordSeverity(r); s != SevCritical || score != 9.8 {
		t.Errorf("CVSS 9.8 = %s %v", s, score)
	}
	if s, _ := recordSeverity(&osvRecord{ID: "GO-1"}); s != SevMedium {
		t.Errorf("unrated = %s", s)
	}
}

func TestMergeAliases(t *testing.T) {
	vs := []Vuln{
		{ID: "PYSEC-1", Aliases: []string{"CVE-2021-1"}, Severity: SevHigh},
		{ID: "GHSA-a", Aliases: []string{"CVE-2021-1", "PYSEC-1"}, Severity: SevCritical, Fixed: []string{"1.2"}},
		{ID: "GHSA-b", Severity: SevLow},
	}
	got := mergeAliases(vs)
	if len(got) != 2 {
		t.Fatalf("want 2 groups, got %+v", got)
	}
	if got[0].ID != "GHSA-a" || got[0].key() != "CVE-2021-1" || !slices.Contains(got[0].Aliases, "PYSEC-1") {
		t.Errorf("merged = %+v", got[0])
	}
	if got[1].key() != "GHSA-b" {
		t.Errorf("single = %+v", got[1])
	}
}

// osvServer serves a fake OSV bucket with one advisory per ecosystem.
func osvServer(t *testing.T, records map[string][]map[string]any, badSum bool) *httptest.Server {
	t.Helper()
	zips := map[string][]byte{}
	for eco, recs := range records {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, r := range recs {
			w, _ := zw.Create(r["id"].(string) + ".json")
			_ = json.NewEncoder(w).Encode(r)
		}
		_ = zw.Close()
		zips[eco] = buf.Bytes()
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eco := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/"), "/all.zip")
		b, ok := zips[eco]
		if !ok {
			http.NotFound(w, r)
			return
		}
		sum := md5.Sum(b) //nolint:gosec // see import
		s := base64.StdEncoding.EncodeToString(sum[:])
		if badSum {
			s = "AAAAAAAAAAAAAAAAAAAAAA=="
		}
		w.Header().Set("x-goog-hash", "crc32c=xx==,md5="+s)
		_, _ = w.Write(b)
	}))
}

func lodashAdvisory() map[string]any {
	return map[string]any{
		"id": "GHSA-p6mc-m468-83gw", "summary": "Prototype Pollution in lodash", "aliases": []string{"CVE-2020-8203"},
		"database_specific": map[string]any{"severity": "HIGH"},
		"affected": []map[string]any{{
			"package": map[string]string{"ecosystem": "npm", "name": "lodash"},
			"ranges":  []map[string]any{{"type": "SEMVER", "events": []map[string]string{{"introduced": "3.7.0"}, {"fixed": "4.17.19"}}}},
		}},
	}
}

func TestVulnDBUpdateKeepsOldDataOnFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := OpenVulnDB(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	good := osvServer(t, map[string][]map[string]any{"npm": {lodashAdvisory(),
		{"id": "OLD-1", "withdrawn": "2021-01-01T00:00:00Z", "affected": []map[string]any{{"package": map[string]string{"ecosystem": "npm", "name": "lodash"}, "versions": []string{"4.17.15"}}}},
	}}, false)
	defer good.Close()
	res, err := db.Update(ctx, good.Client(), good.URL, "test", []string{EcoNpm}, nil)
	if err != nil || len(res) != 1 || res[0].Advisories != 1 || res[0].Error != "" {
		t.Fatalf("update = %+v, %v", res, err)
	}
	// Same export again: nothing to re-index.
	res, _ = db.Update(ctx, good.Client(), good.URL, "test", []string{EcoNpm}, nil)
	if !res[0].Unchanged {
		t.Errorf("an unchanged export should be detected: %+v", res[0])
	}

	vs, err := db.Vulns(ctx, Package{Ecosystem: EcoNpm, Name: "lodash", Version: "4.17.15"})
	if err != nil || len(vs) != 1 || vs[0].ID != "GHSA-p6mc-m468-83gw" || vs[0].Severity != SevHigh || vs[0].Fixed[0] != "4.17.19" {
		t.Fatalf("vulns = %+v, %v (the withdrawn record must not count)", vs, err)
	}

	// A damaged download must leave the existing data alone.
	bad := osvServer(t, map[string][]map[string]any{"npm": {}}, true)
	defer bad.Close()
	res, err = db.Update(ctx, bad.Client(), bad.URL, "test", []string{EcoNpm}, nil)
	if err == nil || res[0].Error == "" || !strings.Contains(res[0].Error, "checksum") {
		t.Fatalf("a bad checksum should fail: %+v %v", res, err)
	}
	if vs, _ := db.Vulns(ctx, Package{Ecosystem: EcoNpm, Name: "lodash", Version: "4.17.15"}); len(vs) != 1 {
		t.Fatal("a failed update lost the previous data")
	}
	st, _ := db.Status(ctx)
	if len(st) != 1 || st[0].Ecosystem != EcoNpm || st[0].Advisories != 1 {
		t.Fatalf("status = %+v", st)
	}
}

func TestScanDepsReadsHeadOnly(t *testing.T) {
	ctx := context.Background()
	g := runner(t)
	db, err := OpenVulnDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	srv := osvServer(t, map[string][]map[string]any{"npm": {lodashAdvisory()}}, false)
	defer srv.Close()
	if _, err := db.Update(ctx, srv.Client(), srv.URL, "test", []string{EcoNpm}, nil); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	lock := func(v string) string {
		return `{"lockfileVersion":3,"packages":{"node_modules/lodash":{"version":"` + v + `"}}}`
	}
	commit(t, dir, "web/package-lock.json", lock("4.17.15"), "add lock")
	commit(t, dir, "node_modules/x/package-lock.json", lock("4.17.1"), "vendored copy")
	commit(t, dir, "broken/package-lock.json", "{oops", "broken lock")
	o := DepsOptions{RepoID: "r1", RepoPath: "me/app", Dir: dir}
	res, err := ScanDeps(ctx, g, db, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 1 || res.Lockfiles != 1 || len(res.Problems) != 1 {
		t.Fatalf("want one finding from web/package-lock.json and one unreadable lockfile: %+v", res)
	}
	f := res.Findings[0]
	if f.File != "web/package-lock.json" || f.Rule != "CVE-2020-8203" || f.Severity != SevHigh || f.Check != CheckDeps ||
		!strings.Contains(f.Hint, "4.17.19") || !strings.Contains(f.Preview, "lodash@4.17.15") {
		t.Fatalf("finding = %+v", f)
	}

	// Another affected version keeps the same finding ID.
	commit(t, dir, "web/package-lock.json", lock("4.17.16"), "bump")
	res2, _ := ScanDeps(ctx, g, db, o)
	if len(res2.Findings) != 1 || res2.Findings[0].ID != f.ID {
		t.Fatalf("the finding ID should not depend on the version: %+v", res2.Findings)
	}

	// The audit reads only: the work tree and index are untouched.
	if st := git(t, dir, "status", "--porcelain"); st != "" {
		t.Fatalf("the audit changed the repo: %s", st)
	}

	// A bare clone at a fixed version has nothing to report.
	commit(t, dir, "web/package-lock.json", lock("4.17.21"), "fix")
	bare := t.TempDir() + "/app.git"
	git(t, dir, "clone", "-q", "--bare", dir, bare)
	res3, err := ScanDeps(ctx, g, db, DepsOptions{RepoID: "r1", RepoPath: "me/app.git", Dir: bare})
	if err != nil || len(res3.Findings) != 0 || res3.Lockfiles != 1 {
		t.Fatalf("bare repo after the fix: %+v %v", res3, err)
	}

	// A lockfile from an ecosystem that was never downloaded is an error,
	// not a silent "no findings".
	commit(t, dir, "Cargo.lock", "[[package]]\nname = \"smallvec\"\nversion = \"1.6.0\"\n", "rust")
	if _, err := ScanDeps(ctx, g, db, o); err == nil || !strings.Contains(err.Error(), "crates.io") {
		t.Fatalf("missing ecosystem should fail: %v", err)
	}
}
