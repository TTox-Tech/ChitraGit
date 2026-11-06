package store

import (
	"context"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/audit"
)

func TestMarkFixedKeepsHistoryAndReopens(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO repos(id, path, kind, first_seen_at) VALUES ('r1', 'me/app', 'normal', ?)`, Now()); err != nil {
		t.Fatal(err)
	}
	run, err := s.BeginAudit(ctx, []string{"deps"}, "incremental")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id, check string) audit.Finding {
		return audit.Finding{ID: id, RepoID: "r1", Check: check, Rule: "CVE-1", Title: "t", Severity: audit.SevHigh,
			Confidence: audit.ConfidenceHigh, File: "go.mod", Occurrences: 1, Status: audit.StatusOpen}
	}
	if _, err := s.SaveRepoAudit(ctx, run, "r1", "deps", []audit.Finding{mk("a", "deps"), mk("b", "deps")}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveRepoAudit(ctx, run, "r1", "secrets", []audit.Finding{mk("s", "secrets")}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.IgnoreFinding(ctx, "b", "dev only"); err != nil {
		t.Fatal(err)
	}

	// The next audit sees only "a": "b" is fixed, the secret is untouched.
	n, err := s.MarkFixed(ctx, "r1", "deps", []string{"a"})
	if err != nil || n != 1 {
		t.Fatalf("MarkFixed = %d, %v", n, err)
	}
	open, _ := s.Findings(ctx, FindingFilter{IncludeIgnored: true})
	if len(open) != 2 {
		t.Fatalf("fixed findings should be hidden by default: %+v", open)
	}
	all, _ := s.Findings(ctx, FindingFilter{IncludeIgnored: true, IncludeFixed: true})
	if len(all) != 3 {
		t.Fatalf("fixed findings must be kept: %+v", all)
	}
	for _, f := range all {
		if f.ID == "b" && (f.Status != audit.StatusFixed || f.FixedAt == "") {
			t.Fatalf("b should be fixed: %+v", f)
		}
	}

	// It comes back: open again, and the ignore still applies.
	if _, err := s.MarkFixed(ctx, "r1", "deps", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Findings(ctx, FindingFilter{RepoID: "r1", Check: "deps", IncludeIgnored: true})
	if len(got) != 2 {
		t.Fatalf("b should be back: %+v", got)
	}
	for _, f := range got {
		if f.ID == "b" && f.Status != audit.StatusIgnored {
			t.Fatalf("the ignore should survive a fix and a regression: %+v", f)
		}
	}
}
