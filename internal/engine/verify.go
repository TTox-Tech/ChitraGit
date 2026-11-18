package engine

import (
	"context"
	"fmt"

	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// Verify checks that the archive is whole (CG-07): `chitragit verify`.
// Every check only reads, so verify is safe to run at any time, and a
// failed check says what is wrong without trying to repair it.

// Names of the verify checks.
const (
	CheckLedger = "ledger" // the event log's hash chain
)

// VerifyCheck is the outcome of one check.
type VerifyCheck struct {
	Name     string   `json:"name"`
	OK       bool     `json:"ok"`
	Summary  string   `json:"summary"`            // one line, plain words
	Problems []string `json:"problems,omitempty"` // what is wrong, one item each
}

// VerifyReport is what verify found.
type VerifyReport struct {
	OK     bool          `json:"ok"`
	Checks []VerifyCheck `json:"checks"`
}

// Verify runs every check on the archive at root.
func Verify(ctx context.Context, root string, git *gitx.Runner, st *store.Store) (*VerifyReport, error) {
	rep := &VerifyReport{OK: true}
	ledger, err := verifyLedger(ctx, st)
	if err != nil {
		return nil, err
	}
	rep.add(ledger)
	pinned, err := verifyPinned(ctx, root, git, st)
	if err != nil {
		return nil, err
	}
	rep.add(pinned)
	return rep, nil
}

func (r *VerifyReport) add(c VerifyCheck) {
	r.Checks = append(r.Checks, c)
	r.OK = r.OK && c.OK
}

// verifyLedger recomputes the event log's hash chain.
func verifyLedger(ctx context.Context, st *store.Store) (VerifyCheck, error) {
	c := VerifyCheck{Name: CheckLedger}
	lc, err := st.VerifyLedger(ctx)
	if err != nil {
		return c, err
	}
	c.OK = lc.OK
	if lc.OK {
		c.Summary = fmt.Sprintf("%d events, hash chain intact", lc.Events)
		return c, nil
	}
	c.Summary = fmt.Sprintf("hash chain broken at event %d", lc.Seq)
	c.Problems = []string{fmt.Sprintf("event %d: %s", lc.Seq, lc.Reason)}
	return c, nil
}
