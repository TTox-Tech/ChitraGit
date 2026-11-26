package engine

import (
	"maps"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/store"
)

func TestPlanRetention(t *testing.T) {
	snaps := []store.Snapshot{
		// r1: three on Mon 2 Nov, one on Tue 3 Nov, one a week later, one named long ago.
		{ID: 1, RepoID: "r1", At: "2026-10-01T09:00:00Z", Label: "before-move"},
		{ID: 2, RepoID: "r1", At: "2026-10-05T09:00:00Z"},
		{ID: 3, RepoID: "r1", At: "2026-11-02T08:00:00Z"},
		{ID: 4, RepoID: "r1", At: "2026-11-02T12:00:00Z"},
		{ID: 5, RepoID: "r1", At: "2026-11-02T18:00:00Z"},
		{ID: 6, RepoID: "r1", At: "2026-11-03T09:00:00Z"},
		{ID: 7, RepoID: "r1", At: "2026-11-10T09:00:00Z"},
		// r2: only one snapshot, kept whatever the policy says.
		{ID: 8, RepoID: "r2", At: "2026-01-01T00:00:00Z"},
		// r3: a time retention cannot read is kept.
		{ID: 9, RepoID: "r3", At: "yesterday"},
		{ID: 10, RepoID: "r3", At: "2026-11-10T09:00:00Z"},
	}
	cases := []struct {
		name string
		p    RetentionPolicy
		want map[int64]string
	}{
		{"nothing asked still keeps newest and named", RetentionPolicy{},
			map[int64]string{1: KeepNamed, 7: KeepLast, 8: KeepLast, 10: KeepLast}},
		{"last two", RetentionPolicy{KeepLast: 2},
			map[int64]string{1: KeepNamed, 6: KeepLast, 7: KeepLast, 8: KeepLast, 9: KeepLast, 10: KeepLast}},
		{"three days", RetentionPolicy{KeepLast: 1, KeepDaily: 3},
			map[int64]string{1: KeepNamed, 5: KeepDaily, 6: KeepDaily, 7: KeepLast, 8: KeepLast, 9: KeepDaily, 10: KeepLast}},
		{"three weeks", RetentionPolicy{KeepLast: 1, KeepWeekly: 3},
			map[int64]string{1: KeepNamed, 2: KeepWeekly, 6: KeepWeekly, 7: KeepLast, 8: KeepLast, 9: KeepWeekly, 10: KeepLast}},
	}
	for _, c := range cases {
		if got := planRetention(snaps, c.p); !maps.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
