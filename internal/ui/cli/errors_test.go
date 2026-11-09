package cli

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/core"
)

var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/*.golden")

// TestErrorHints is a golden test of how errors print: every error ends
// with a "Try:" line, its own hint when it has one, else its category's.
func TestErrorHints(t *testing.T) {
	isolate(t)
	cases := []struct {
		name string
		err  error
	}{
		{"own hint", &core.Error{Category: core.CatAuth, Message: "GitHub rejected the token", Hint: "run `chitragit auth login github` again"}},
		{"usage", &usageError{errors.New("expected 1 argument(s), got 0")}},
		{"plain", errors.New("something odd happened")},
		{"wrapped", fmt.Errorf("sync me/app: %w", &core.Error{Category: core.CatDisk, Message: "the disk is full"})},
	}
	for _, cat := range []core.Category{core.CatNetwork, core.CatAuth, core.CatNotFound, core.CatRateLimited, core.CatTimeout,
		core.CatUnsafeState, core.CatConflict, core.CatCorrupt, core.CatDisk, core.CatPermission, core.CatConfig,
		core.CatEnvironment, core.CatInternal} {
		cases = append(cases, struct {
			name string
			err  error
		}{string(cat), &core.Error{Category: cat, Message: "a " + string(cat) + " error"}})
	}
	var b bytes.Buffer
	for _, c := range cases {
		var out bytes.Buffer
		(&app{out: &out, errOut: &out}).printError(c.err)
		if !strings.Contains(out.String(), "  Try: ") {
			t.Errorf("%s: no Try line:\n%s", c.name, out.String())
		}
		fmt.Fprintf(&b, "## %s\n%s\n", c.name, out.String())
	}
	golden := filepath.Join("testdata", "errors.golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run: go test ./internal/ui/cli -run TestErrorHints -update-golden)", err)
	}
	if got := b.String(); strings.ReplaceAll(string(want), "\r\n", "\n") != got {
		t.Errorf("error output changed; run: go test ./internal/ui/cli -run TestErrorHints -update-golden\n%s", got)
	}

	var js bytes.Buffer
	(&app{out: &js, errOut: &js, jsonOut: true}).printError(&core.Error{Category: core.CatDisk, Message: "full"})
	if !strings.Contains(js.String(), `"hint":"free some disk space`) {
		t.Errorf("json hint: %s", js.String())
	}
}
