package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// A host with no API cannot be judged before cloning: shield check says so
// and exits 1, never 0, so a script cannot read it as "safe".
func TestShieldCheckWithoutAPI(t *testing.T) {
	isolate(t)
	code, _, errOut := run(t, "shield", "check", "https://git.example.org/acme/tool.git")
	if code != core.ExitPartial || !strings.Contains(errOut, "Could not check before cloning") {
		t.Fatalf("shield check: %d %q", code, errOut)
	}
	code, out, _ := run(t, "--json", "shield", "check", "https://git.example.org/acme/tool.git")
	var rep struct{ Verdict, Reason string }
	if code != core.ExitPartial || json.Unmarshal([]byte(out), &rep) != nil || rep.Verdict != "unknown" || rep.Reason == "" {
		t.Fatalf("json: %d %q", code, out)
	}
}
