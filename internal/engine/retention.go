package engine

import (
	"fmt"
	"sort"
	"time"

	"github.com/TTox-Tech/chitragit/internal/store"
)

// RetentionPolicy says which snapshots retention keeps in each repo. A
// snapshot is kept when any rule wants it. The newest snapshot of a repo
// and every named snapshot are always kept.
type RetentionPolicy struct {
	KeepLast   int // the newest N snapshots
	KeepDaily  int // the newest snapshot of each of the last N days that have one
	KeepWeekly int // the newest snapshot of each of the last N weeks that have one
}

// Why a snapshot is kept.
const (
	KeepNamed  = "named"
	KeepLast   = "last"
	KeepDaily  = "daily"
	KeepWeekly = "weekly"
)

// planRetention returns the snapshots the policy keeps, by id, with the
// first rule that wants each one. Days and weeks are counted in UTC, and
// only periods that have a snapshot count, so a repo nobody synced for a
// month keeps its last N days of history, not nothing.
func planRetention(snaps []store.Snapshot, p RetentionPolicy) map[int64]string {
	byRepo := map[string][]store.Snapshot{}
	for _, sn := range snaps {
		byRepo[sn.RepoID] = append(byRepo[sn.RepoID], sn)
	}
	keep := map[int64]string{}
	mark := func(id int64, why string) {
		if _, ok := keep[id]; !ok {
			keep[id] = why
		}
	}
	for _, list := range byRepo {
		sort.Slice(list, func(i, j int) bool { return list[i].ID > list[j].ID }) // newest first
		for _, sn := range list {
			if sn.Label != "" {
				mark(sn.ID, KeepNamed)
			}
		}
		for i := 0; i < len(list) && i < max(p.KeepLast, 1); i++ {
			mark(list[i].ID, KeepLast)
		}
		keepPeriods(list, p.KeepDaily, KeepDaily, func(t time.Time) string { return t.Format(time.DateOnly) }, mark)
		keepPeriods(list, p.KeepWeekly, KeepWeekly, func(t time.Time) string {
			y, w := t.ISOWeek()
			return fmt.Sprintf("%d-W%02d", y, w)
		}, mark)
	}
	return keep
}

// keepPeriods marks the newest snapshot of each of the first n periods in
// list (newest first). A snapshot with an unreadable time is kept, since
// retention cannot tell how old it is.
func keepPeriods(list []store.Snapshot, n int, why string, period func(time.Time) string, mark func(int64, string)) {
	if n <= 0 {
		return
	}
	seen := map[string]bool{}
	for _, sn := range list {
		t, err := time.Parse(time.RFC3339Nano, sn.At)
		if err != nil {
			mark(sn.ID, why)
			continue
		}
		key := period(t.UTC())
		if seen[key] || len(seen) == n {
			continue
		}
		seen[key] = true
		mark(sn.ID, why)
	}
}
