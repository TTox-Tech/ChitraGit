package audit

import "testing"

func TestJudge(t *testing.T) {
	f := func(s Severity, st string) Finding { return Finding{Severity: s, Status: st} }
	cases := []struct {
		fs      []Finding
		blockAt Severity
		want    Verdict
	}{
		{nil, SevHigh, VerdictPass},
		{[]Finding{f(SevInfo, StatusOpen)}, SevHigh, VerdictPass},
		{[]Finding{f(SevLow, StatusOpen), f(SevMedium, StatusOpen)}, SevHigh, VerdictWarn},
		{[]Finding{f(SevLow, StatusOpen), f(SevHigh, StatusOpen)}, SevHigh, VerdictBlock},
		{[]Finding{f(SevCritical, StatusIgnored), f(SevHigh, StatusFixed)}, SevHigh, VerdictPass},
		{[]Finding{f(SevHigh, StatusOpen)}, SevCritical, VerdictWarn},
		{[]Finding{f(SevMedium, StatusOpen)}, SevMedium, VerdictBlock},
		{[]Finding{f(SevHigh, StatusOpen)}, Severity("nonsense"), VerdictBlock},
	}
	for i, c := range cases {
		if got := Judge(c.fs, c.blockAt); got != c.want {
			t.Errorf("case %d: Judge = %s, want %s", i, got, c.want)
		}
	}
}
