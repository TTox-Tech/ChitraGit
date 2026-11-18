package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/TTox-Tech/chitragit/internal/audit"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/gitx"
	"github.com/TTox-Tech/chitragit/internal/provider"
	"github.com/TTox-Tech/chitragit/internal/syncer"
)

// ShieldOptions turns on the KodeKavach Shield clone gate (CG-42.6): a new
// repo is cloned without a checkout, its HEAD is read from git objects
// alone, and its files are written only when the verdict lets them.
type ShieldOptions struct {
	Pack       *audit.RulesPack // actions and agents rules
	BlockAt    audit.Severity   // findings at or above this block (default high)
	Quarantine string           // where blocked clones are kept, as bare repos
	Limits     audit.Limits     // size caps (CG-42.8); zero values are off
}

// Shield verdicts and the ledger event of a blocked clone.
const (
	VerdictPass        = audit.VerdictPass
	VerdictWarn        = audit.VerdictWarn
	VerdictBlock       = audit.VerdictBlock
	EventShieldBlocked = "shield_blocked"
	shieldReportFile   = "kodekavach-report.json"
)

// ShieldReport is what Shield found in a new clone. A blocked clone keeps
// it next to its objects in the quarantine.
type ShieldReport struct {
	Repo       string        `json:"repo"`
	URL        string        `json:"url"`
	Commit     string        `json:"commit,omitempty"`
	Verdict    audit.Verdict `json:"verdict"`
	Quarantine string        `json:"quarantine,omitempty"`
	Findings   []Finding     `json:"findings"`
}

// BlockedError says Shield blocked a clone. Its category maps to exit code 6.
type BlockedError struct{ Report *ShieldReport }

func (e *BlockedError) Error() string {
	top := ""
	if len(e.Report.Findings) > 0 {
		top = ": " + e.Report.Findings[0].Title
	}
	return fmt.Sprintf("KodeKavach blocked %s (%d findings%s)", e.Report.Repo, len(e.Report.Findings), top)
}

// AsCoreError gives the plain message, the quarantine path and the exit code.
func (e *BlockedError) AsCoreError() *core.Error {
	hint := "nothing was checked out; every object is kept in " + e.Report.Quarantine + " with the report in " + shieldReportFile
	if e.Report.Quarantine == "" {
		hint = "the download was stopped and deleted, so it cannot fill the disk; the block is in the ledger"
	}
	return &core.Error{Category: core.CatBlocked, Message: e.Error(), Err: e, Hint: hint}
}

// overLimit reports whether a block comes from a size limit. Such a clone
// is not quarantined: keeping a pack bomb would fill the disk it guards,
// and nothing of the archive's own is lost by dropping a download.
func overLimit(rep *ShieldReport) bool {
	for _, f := range rep.Findings {
		if f.Check == audit.CheckLimits {
			return true
		}
	}
	return false
}

// dropOversize logs a clone blocked by a limit and returns its error. The
// caller deletes the temporary clone.
func (c *cloner) dropOversize(ctx context.Context, r provider.Repo, rep *ShieldReport) error {
	rep.Verdict = audit.VerdictBlock
	if c.st != nil {
		_ = c.st.AddEvent(context.WithoutCancel(ctx), "", EventShieldBlocked, map[string]any{
			"message": fmt.Sprintf("KodeKavach blocked %s: %s; the download was deleted", r.FullName(), rep.Findings[0].Title),
			"repo":    r.FullName(), "url": r.CloneURL, "commit": rep.Commit, "findings": len(rep.Findings), "rules": rules(rep.Findings),
		})
	}
	be := &BlockedError{Report: rep}
	return be.AsCoreError()
}

func rules(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rule
	}
	return out
}

// repoSize is the bytes of a git dir's objects, packs being written included.
func repoSize(gitDir string) int64 {
	var n int64
	_ = filepath.WalkDir(filepath.Join(gitDir, "objects"), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// watchSize stops a clone whose objects in dir grow past limit: every tick
// it measures the folder and, once over, records the size and calls stop.
// It returns when ctx is done.
func watchSize(ctx context.Context, dir string, limit int64, tick time.Duration, over *atomic.Int64, stop func()) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var n int64
		_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if info, err := d.Info(); err == nil {
					n += info.Size()
				}
			}
			return nil
		})
		if n > limit {
			over.Store(n)
			stop()
			return
		}
	}
}

// gate runs Shield on a fresh clone in tmp. On pass or warn it checks the
// files out (a mirror has none); on block it moves the objects to the
// quarantine and returns a *BlockedError, so nothing reaches the archive.
// A mirror is never held back: it has no files, so the block is only
// logged and reported.
func (c *cloner) gate(ctx context.Context, tmp string, r provider.Repo) error {
	dir := tmp
	if !c.opts.Mirror {
		dir = filepath.Join(tmp, ".git")
	}
	rep, err := c.inspectClone(ctx, dir, r)
	if err != nil {
		return err
	}
	switch {
	case rep.Verdict == audit.VerdictBlock && overLimit(rep):
		return c.dropOversize(ctx, r, rep)
	case rep.Verdict == audit.VerdictBlock && c.opts.Mirror:
		c.warnings = append(c.warnings, fmt.Sprintf("KodeKavach: %s has %d findings; it is a mirror, so no files were written. Run `chitragit audit`.",
			r.FullName(), len(rep.Findings)))
		return nil
	case rep.Verdict == audit.VerdictBlock:
		if err := c.quarantine(ctx, tmp, r, rep); err != nil {
			return err
		}
		be := &BlockedError{Report: rep}
		return be.AsCoreError()
	case rep.Verdict == audit.VerdictWarn:
		c.warnings = append(c.warnings, fmt.Sprintf("KodeKavach: %s has %d findings below the block level. Run `chitragit audit`.",
			r.FullName(), len(rep.Findings)))
	}
	if c.opts.Mirror || rep.Commit == "" {
		return nil
	}
	return checkout(ctx, c.git, tmp)
}

// inspectClone judges the HEAD of a fresh clone in dir. A repo with no
// commits has nothing to check out and passes.
func (c *cloner) inspectClone(ctx context.Context, dir string, r provider.Repo) (*ShieldReport, error) {
	rep := &ShieldReport{Repo: r.FullName(), URL: r.CloneURL, Verdict: audit.VerdictPass, Findings: []Finding{}}
	res, err := c.git.Run(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		if res != nil && res.ExitCode == 1 {
			return rep, nil // unborn HEAD: an empty repo
		}
		return nil, err
	}
	rep.Commit = strings.TrimSpace(string(res.Stdout))
	cr, err := audit.ScanCommit(ctx, c.git, audit.CommitOptions{
		RepoID: "", RepoPath: r.FullName(), Dir: dir, Commit: rep.Commit, Pack: c.opts.Shield.Pack,
	})
	if err != nil {
		return nil, err
	}
	refs, err := c.git.Output(ctx, dir, "for-each-ref", "--format=x")
	if err != nil {
		return nil, err
	}
	size := audit.Size{Bytes: repoSize(dir), Refs: strings.Count(refs, "x")}
	rep.Findings = append(c.opts.Shield.Limits.Check("", r.FullName(), rep.Commit, cr.Stats, size), cr.Findings...)
	rep.Verdict = audit.Judge(rep.Findings, c.opts.Shield.BlockAt)
	return rep, nil
}

// quarantine keeps the objects of a blocked clone in tmp as a bare repo in
// the quarantine folder, with the report beside them, and logs the block
// in the ledger. Nothing is thrown away (PRD G2).
func (c *cloner) quarantine(ctx context.Context, tmp string, r provider.Repo, rep *ShieldReport) error {
	dest, err := quarantinePath(c.opts.Shield.Quarantine, r)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return &core.Error{Category: core.CatPermission, Message: "cannot create " + filepath.Dir(dest), Err: err}
	}
	if err := moveDir(filepath.Join(tmp, ".git"), dest); err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot move a blocked clone into the quarantine", Err: err}
	}
	if _, err := c.git.Run(ctx, dest, "config", "--file", "config", "core.bare", "true"); err != nil {
		return err
	}
	rep.Quarantine = dest
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dest, shieldReportFile), append(b, '\n'), 0o600); err != nil {
		return &core.Error{Category: core.CatDisk, Message: "cannot write the KodeKavach report", Err: err}
	}
	if c.st != nil {
		msg := fmt.Sprintf("KodeKavach blocked %s: %d findings, quarantined", r.FullName(), len(rep.Findings))
		_ = c.st.AddEvent(context.WithoutCancel(ctx), "", EventShieldBlocked, map[string]any{
			"message": msg, "repo": r.FullName(), "url": r.CloneURL, "commit": rep.Commit,
			"findings": len(rep.Findings), "quarantine": dest,
		})
	}
	return nil
}

// quarantinePath is <dir>/<host>/<owner>/<name>.git, with a number added
// when that is taken. Names a provider sent that are not safe as folder
// names are replaced.
func quarantinePath(dir string, r provider.Repo) (string, error) {
	if dir == "" {
		return "", &core.Error{Category: core.CatConfig, Message: "no quarantine folder for KodeKavach Shield"}
	}
	parts := []string{r.Host, r.Owner, r.Name}
	for i, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, `/\:*?"<>|`) ||
			strings.TrimRight(p, ". ") != p || strings.ContainsFunc(p, func(c rune) bool { return c < 0x20 || c == 0x7f }) {
			parts[i] = "unnamed-" + uuid.NewString()[:8]
		}
	}
	base := filepath.Join(dir, parts[0], parts[1], parts[2])
	dest := base + ".git"
	for n := 2; ; n++ {
		if _, err := os.Lstat(dest); errors.Is(err, fs.ErrNotExist) {
			return dest, nil
		}
		dest = fmt.Sprintf("%s-%d.git", base, n)
	}
}

// moveDir renames src to dst, or copies it when they are on different
// disks. A git folder holds only folders and regular files.
func moveDir(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(to, 0o700)
		case d.Type().IsRegular():
			return copyFile(p, to)
		}
		return fmt.Errorf("unexpected file %s in a fresh clone", p)
	})
	if err != nil {
		_ = os.RemoveAll(dst)
		return err
	}
	return nil // the caller removes src with the rest of the temporary clone
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// checkout writes the files of a clone made with --no-checkout.
func checkout(ctx context.Context, git *gitx.Runner, dir string) error {
	_, err := git.Run(ctx, dir, "checkout", "--quiet")
	return err
}

// SyncShield is the sync gate (CG-42.7): the syncer calls it on every
// fetched upstream tip before a branch moves there. It reads objects only.
func SyncShield(git *gitx.Runner, o *ShieldOptions) syncer.ShieldFunc {
	return func(ctx context.Context, dir, commit string) (syncer.Inspection, error) {
		cr, err := audit.ScanCommit(ctx, git, audit.CommitOptions{RepoPath: dir, Dir: dir, Commit: commit, Pack: o.Pack})
		if err != nil {
			return syncer.Inspection{}, err
		}
		// Repo size and refs are not judged here: an archive copy that grew
		// over years is not a bomb, and the fetch is already on disk.
		fs := append(o.Limits.Check("", dir, commit, cr.Stats, audit.Size{Bytes: -1, Refs: -1}), cr.Findings...)
		in := syncer.Inspection{Block: audit.Judge(fs, o.BlockAt) == audit.VerdictBlock, Findings: len(fs)}
		if len(fs) > 0 {
			in.Worst = fs[0].Title // limits first, then findings worst first
		}
		return in, nil
	}
}

// DefaultLimits are Shield's size caps until [shield] config lands (CG-42.9).
func DefaultLimits() audit.Limits { return audit.DefaultLimits() }
