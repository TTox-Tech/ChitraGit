package audit

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// evalCase is one file of the KodeKavach eval corpus (testdata/eval). The
// format is a small txtar: a "# note" line, an "expect:" line listing the
// rules that must be reported, then the repo files, each after a
// "-- path --" line. Any rule reported but not listed is a false positive.
type evalCase struct {
	name   string
	expect []string
	files  []HeadFile
}

func loadEvalCorpus(t *testing.T) []evalCase {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "eval", "*.txt"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no eval cases: %v", err)
	}
	var cases []evalCase
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		c, err := parseEvalCase(strings.TrimSuffix(filepath.Base(p), ".txt"), data)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		cases = append(cases, c)
	}
	return cases
}

func parseEvalCase(name string, data []byte) (evalCase, error) {
	c := evalCase{name: name}
	sawExpect := false
	var cur *HeadFile
	for _, line := range bytes.SplitAfter(data, []byte("\n")) {
		s := strings.TrimRight(string(line), "\r\n")
		if p, ok := strings.CutPrefix(s, "-- "); ok && strings.HasSuffix(p, " --") {
			c.files = append(c.files, HeadFile{Path: strings.TrimSuffix(p, " --")})
			cur = &c.files[len(c.files)-1]
			continue
		}
		if cur != nil {
			cur.Data = append(cur.Data, line...)
			continue
		}
		if rules, ok := strings.CutPrefix(s, "expect:"); ok {
			c.expect, sawExpect = strings.Fields(rules), true
		}
	}
	if !sawExpect || len(c.files) == 0 {
		return c, os.ErrInvalid
	}
	return c, nil
}

// TestEvalCorpus runs the head-file checks over every eval case: each case
// must report exactly the rules it expects.
func TestEvalCorpus(t *testing.T) {
	pack := mustRules(t)
	for _, c := range loadEvalCorpus(t) {
		fs := append(ScanActions(pack, "r", "me/app", "abc", c.files), ScanAgents(pack, "r", "me/app", "abc", c.files)...)
		var got []string
		for rule := range byRule(fs) {
			got = append(got, rule)
		}
		slices.Sort(got)
		want := slices.Sorted(slices.Values(c.expect))
		if !slices.Equal(got, want) {
			t.Errorf("%s: reported %v, want %v", c.name, got, want)
		}
	}
}

// TestEvalCorpusCoversEveryRule keeps the corpus complete: a new rule needs
// at least one case that reports it.
func TestEvalCorpusCoversEveryRule(t *testing.T) {
	covered := map[string]bool{}
	clean := 0
	for _, c := range loadEvalCorpus(t) {
		for _, r := range c.expect {
			covered[r] = true
		}
		if len(c.expect) == 0 {
			clean++
		}
	}
	rules := []string{RuleUnpinnedAction, RuleScriptInjection, RulePRTargetCheckout, RuleWriteAll, RuleDefaultPerms,
		RuleInsecureCommands, RuleSecretsInherit, RuleSelfHostedOnForks, RuleMCPUnpinned, RuleMCPPlainHTTP, RuleMCPInlineCode}
	for _, r := range mustRules(t).Agents.ContentRules {
		rules = append(rules, r.ID)
	}
	for _, r := range rules {
		if !covered[r] {
			t.Errorf("no eval case reports %s", r)
		}
	}
	if clean == 0 {
		t.Error("no clean eval cases: false positives are not measured")
	}
}
