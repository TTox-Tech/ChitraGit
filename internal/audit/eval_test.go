package audit

import (
	"bytes"
	"fmt"
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

// evalReported runs the head-file checks over one eval case and returns the
// rules they report, sorted.
func evalReported(pack *RulesPack, c evalCase) []string {
	fs := append(ScanActions(pack, "r", "me/app", "abc", c.files), ScanAgents(pack, "r", "me/app", "abc", c.files)...)
	var got []string
	for rule := range byRule(fs) {
		got = append(got, rule)
	}
	slices.Sort(got)
	return got
}

// TestEvalCorpus runs the head-file checks over every eval case: each case
// must report exactly the rules it expects.
func TestEvalCorpus(t *testing.T) {
	pack := mustRules(t)
	for _, c := range loadEvalCorpus(t) {
		got := evalReported(pack, c)
		want := slices.Sorted(slices.Values(c.expect))
		if !slices.Equal(got, want) {
			t.Errorf("%s: reported %v, want %v", c.name, got, want)
		}
	}
}

// Gates for TestEvalPrecision. Precision is the share of reported rules that
// the case expects; recall is the share of expected rules that are reported.
const (
	minEvalPrecision = 0.95
	minEvalRecall    = 0.90
)

// evalScore counts one (case, rule) pair at a time.
type evalScore struct{ tp, fp, fn int }

func (s *evalScore) add(got, want []string) {
	for _, r := range got {
		if slices.Contains(want, r) {
			s.tp++
		} else {
			s.fp++
		}
	}
	for _, r := range want {
		if !slices.Contains(got, r) {
			s.fn++
		}
	}
}

func (s evalScore) precision() float64 { return ratio(s.tp, s.tp+s.fp) }
func (s evalScore) recall() float64    { return ratio(s.tp, s.tp+s.fn) }

// ratio is 1 when nothing was counted: no reports means no false positives.
func ratio(n, d int) float64 {
	if d == 0 {
		return 1
	}
	return float64(n) / float64(d)
}

// TestEvalPrecision is the precision and recall gate that CI runs on every
// change to internal/audit. It writes the scores to the job summary.
func TestEvalPrecision(t *testing.T) {
	pack := mustRules(t)
	var s evalScore
	cases := loadEvalCorpus(t)
	for _, c := range cases {
		s.add(evalReported(pack, c), c.expect)
	}
	report := fmt.Sprintf("KodeKavach eval: %d cases, precision %.3f (gate %.2f), recall %.3f (gate %.2f), %d true, %d false positives, %d missed",
		len(cases), s.precision(), minEvalPrecision, s.recall(), minEvalRecall, s.tp, s.fp, s.fn)
	t.Log(report)
	if path := os.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
			fmt.Fprintln(f, report)
			_ = f.Close()
		}
	}
	if s.precision() < minEvalPrecision {
		t.Errorf("precision %.3f is below %.2f", s.precision(), minEvalPrecision)
	}
	if s.recall() < minEvalRecall {
		t.Errorf("recall %.3f is below %.2f", s.recall(), minEvalRecall)
	}
}

func TestEvalScore(t *testing.T) {
	var s evalScore
	s.add([]string{"a", "b"}, []string{"a", "c"}) // a found, b false, c missed
	s.add(nil, nil)                               // a clean case adds nothing
	s.add([]string{"d"}, []string{"d"})
	if s != (evalScore{tp: 2, fp: 1, fn: 1}) || s.precision() != 2.0/3 || s.recall() != 2.0/3 {
		t.Errorf("score = %+v, precision %v, recall %v", s, s.precision(), s.recall())
	}
	if (evalScore{}).precision() != 1 {
		t.Error("empty score should have precision 1")
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
