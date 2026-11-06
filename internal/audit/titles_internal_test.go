package audit

import "testing"

func TestShortTitlesForAllRules(t *testing.T) {
	cfg, err := secretRules()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cfg.rules {
		id := r.id
		got := shortTitle(r.description, id)
		if len(got) > 70 || got == "" {
			t.Errorf("%s: %q", id, got)
		}
		if testing.Verbose() {
			t.Logf("%-40s %s", id, got)
		}
	}
}
