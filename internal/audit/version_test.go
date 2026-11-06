package audit

import "testing"

// Each list is in ascending order; every pair must compare that way.
func TestVersionOrdering(t *testing.T) {
	cases := map[string][]string{
		EcoNpm: {"0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta", "1.0.0-beta.2",
			"1.0.0-beta.11", "1.0.0-rc.1", "1.0.0", "1.0.1", "1.2.0", "1.10.0", "4.17.15", "4.17.19"},
		EcoGo:     {"0.0.0-20210405180319-a5a99cb37ef4", "0.1.0", "0.17.0", "0.23.0", "1.2.3", "v1.10.0"},
		EcoCrates: {"1.6.0", "1.6.1", "1.13.2"},
		EcoNuGet:  {"12.0.1-Beta1", "12.0.1", "12.0.1.1", "13.0.1"},
		EcoPyPI: {"1.0.dev1", "1.0a1", "1.0a2.dev1", "1.0a2", "1.0b1", "1.0rc1", "1.0", "1.0+local.1",
			"1.0.post1", "1.0.post2", "1.1", "2.19.0", "3.2.0", "3.2.5", "3.10", "1!0.1"},
		EcoRubyGems:  {"1.0.0.a", "1.0.0.pre1", "1.0.0.pre2", "1.0.0.rc1", "1.0.0", "1.0.1", "1.10", "6.0.0"},
		EcoMaven:     {"2.0-alpha1", "2.0-beta1", "2.0-m1", "2.0-rc1", "2.0-SNAPSHOT", "2.0", "2.0-sp1", "2.0.1", "2.14.1", "2.15.0"},
		EcoPackagist: {"1.0.0-dev", "1.0.0-alpha1", "1.0.0-beta2", "1.0.0-RC1", "1.0.0", "1.0.0-pl1", "1.0.1", "7.4.0", "v7.4.5"},
	}
	for eco, list := range cases {
		for i := range list {
			for j := range list {
				want := cmpInt(i, j)
				got, err := compareVersions(eco, list[i], list[j])
				if err != nil {
					t.Errorf("%s: %s vs %s: %v", eco, list[i], list[j], err)
					continue
				}
				if got != want {
					t.Errorf("%s: compare(%s, %s) = %d, want %d", eco, list[i], list[j], got, want)
				}
			}
		}
	}
}

func TestVersionEquivalents(t *testing.T) {
	for _, c := range []struct{ eco, a, b string }{
		{EcoPyPI, "1.0", "1.0.0"}, {EcoPyPI, "1.0-1", "1.0.post1"}, {EcoPyPI, "1.0ALPHA1", "1.0a1"}, {EcoPyPI, "v2.0", "2.0"},
		{EcoNpm, "1.0.0+build.5", "1.0.0"}, {EcoMaven, "1.0", "1.0.0"}, {EcoMaven, "1.0-final", "1.0"},
		{EcoRubyGems, "1.0", "1.0.0"}, {EcoPackagist, "v1.2", "1.2"}, {EcoNuGet, "1.0.0-RC1", "1.0.0-rc1"},
	} {
		if got, err := compareVersions(c.eco, c.a, c.b); err != nil || got != 0 {
			t.Errorf("%s: %s and %s should be equal, got %d %v", c.eco, c.a, c.b, got, err)
		}
	}
	for _, c := range []struct{ eco, v string }{{EcoNpm, "latest"}, {EcoPyPI, "1.0-what"}, {EcoGo, "1.x"}} {
		if _, err := compareVersions(c.eco, c.v, "1.0.0"); err == nil {
			t.Errorf("%s %q should not parse", c.eco, c.v)
		}
	}
}
