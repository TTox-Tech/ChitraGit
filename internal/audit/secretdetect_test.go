package audit

import "testing"

// The global allowlist skips placeholder values such as `false` or `null`, but
// must not skip a real secret that only contains those words. The tokens are
// made up and split so that secret scanners reading this file do not report them.
func TestGlobalAllowlistSkipsOnlyWholePlaceholders(t *testing.T) {
	set, err := secretRules()
	if err != nil {
		t.Fatal(err)
	}
	secrets := map[string]string{
		"plain":          fakeToken,
		"contains false": "ghp_" + "R8pZ3kQ9vT2mX7wL4nFalsedF5gH0jK8sA2e",
		"ends in null":   "ghp_" + "R8pZ3kQ9vT2mX7wL4nB6cY1dF5gH0jK8null",
	}
	for name, secret := range secrets {
		if got := set.detect("token = \""+secret+"\"\n", "config.py", ""); len(got) != 1 {
			t.Errorf("%s: %d findings, want 1", name, len(got))
		}
	}
	for _, placeholder := range []string{"false", "FALSE", "null", "true"} {
		if got := set.detect("api_key = \""+placeholder+"\"\n", "config.py", ""); len(got) != 0 {
			t.Errorf("placeholder %q reported: %+v", placeholder, got)
		}
	}
}
