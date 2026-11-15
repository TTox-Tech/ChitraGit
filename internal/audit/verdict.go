package audit

// Verdict is what Shield decides about a commit from its findings
// (KodeKavach Shield, CG-42.6).
type Verdict string

// Verdicts, from least to most severe.
const (
	VerdictPass  Verdict = "pass"  // nothing above info
	VerdictWarn  Verdict = "warn"  // findings below blockAt: check out, but say so
	VerdictBlock Verdict = "block" // a finding at or above blockAt: never check out
)

// Judge turns a commit's findings into a verdict. Ignored and fixed
// findings do not count. An unknown blockAt blocks at high.
func Judge(fs []Finding, blockAt Severity) Verdict {
	if blockAt.Rank() < 0 {
		blockAt = SevHigh
	}
	v := VerdictPass
	for _, f := range fs {
		if f.Status == StatusIgnored || f.Status == StatusFixed {
			continue
		}
		switch {
		case f.Severity.Rank() >= blockAt.Rank():
			return VerdictBlock
		case f.Severity.Rank() > SevInfo.Rank():
			v = VerdictWarn
		}
	}
	return v
}
