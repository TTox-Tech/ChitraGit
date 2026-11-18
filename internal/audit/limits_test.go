package audit

import (
	"slices"
	"testing"
)

func TestLimitsCheck(t *testing.T) {
	l := Limits{MaxRepoBytes: 10 << 20, MaxFiles: 100, MaxBlobBytes: 1 << 20, MaxTreeDepth: 8, MaxRefs: 50}
	under := TreeStats{Files: 100, LargestBlob: 1 << 20, MaxDepth: 8}
	if fs := l.Check("r", "me/app", "c", under, Size{Bytes: 10 << 20, Refs: 50}); len(fs) != 0 {
		t.Fatalf("at the limits: %+v", fs)
	}
	over := TreeStats{Files: 101, LargestBlob: 2 << 20, LargestPath: "big.bin", MaxDepth: 9}
	fs := l.Check("r", "me/app", "c", over, Size{Bytes: 11 << 20, Refs: 51})
	var rules []string
	for _, f := range fs {
		rules = append(rules, f.Rule)
		if f.Severity != SevCritical || f.Check != CheckLimits {
			t.Errorf("%s: severity %s check %s", f.Rule, f.Severity, f.Check)
		}
	}
	want := []string{RuleLimitRepoSize, RuleLimitFiles, RuleLimitBlob, RuleLimitDepth, RuleLimitRefs}
	if !slices.Equal(rules, want) {
		t.Errorf("rules = %q, want %q", rules, want)
	}
	if Judge(fs, SevHigh) != VerdictBlock {
		t.Error("a limit does not block")
	}
	if fs := (Limits{}).Check("r", "p", "c", over, Size{Bytes: 1 << 40, Refs: 1 << 20}); len(fs) != 0 {
		t.Errorf("zero limits are not off: %+v", fs)
	}
	if fs := l.Check("r", "p", "c", TreeStats{}, Size{Bytes: -1, Refs: -1}); len(fs) != 0 {
		t.Errorf("unmeasured sizes were checked: %+v", fs)
	}
}
