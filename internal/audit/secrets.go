package audit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/gitx"
)

// SecretOptions controls one repo's secrets check.
type SecretOptions struct {
	RepoID   string
	RepoPath string // relative to the archive, for reports
	Dir      string // absolute path git runs in (works for bare repos too)
	History  string // HistoryHead | HistoryIncremental | HistoryFull
	// Seen are ref tips an earlier audit already covered (incremental mode).
	Seen []string
	// MaxFileSize skips a file (or one file's change in one commit) larger
	// than this many bytes. 0 = no limit.
	MaxFileSize int64
}

// SecretResult is what one repo's secrets check found.
type SecretResult struct {
	Findings []Finding
	// Tips are the ref tips this check covered; store them for the next
	// incremental run.
	Tips    []string
	Skipped int // files skipped because they were too large
	Commits int // commits read (history modes)
}

// ScanSecrets looks for leaked secrets in one repo. It only reads: git
// runs with external diff drivers and textconv filters disabled, so a
// repo's own config cannot make the audit run a command.
func ScanSecrets(ctx context.Context, git *gitx.Runner, o SecretOptions) (*SecretResult, error) {
	det, err := secretRules()
	if err != nil {
		return nil, err
	}
	acc := newAccumulator(o)

	res := &SecretResult{}
	if o.History == HistoryHead {
		if err := scanHead(ctx, git, det, o, acc, res); err != nil {
			return nil, err
		}
		res.Findings = acc.findings()
		return res, nil
	}

	tips, err := refTips(ctx, git, o.Dir)
	if err != nil {
		return nil, err
	}
	res.Tips = tips
	if len(tips) == 0 {
		return res, nil // empty repo
	}
	var exclude []string
	if o.History == HistoryIncremental && len(o.Seen) > 0 {
		if exclude, err = existingCommits(ctx, git, o.Dir, o.Seen); err != nil {
			return nil, err
		}
		if sameSet(exclude, tips) {
			return res, nil // nothing new since the last audit
		}
	}
	if err := scanLog(ctx, git, det, o, exclude, acc, res); err != nil {
		return nil, err
	}
	res.Findings = acc.findings()
	return res, nil
}

// logArgs is `git log -p` made safe and parseable whatever the user's or
// the repo's git config says.
func logArgs(stdin bool) []string {
	args := []string{
		"-c", "log.showSignature=false",
		"-c", "diff.noprefix=false",
		"-c", "diff.mnemonicPrefix=false",
		"-c", "core.quotePath=true",
		"log", "-p", "-U0",
		"--no-color", "--no-ext-diff", "--no-textconv", "--no-decorate", "--no-mailmap", "--no-renames",
		"--pretty=" + logFormat,
		"--src-prefix=a/", "--dst-prefix=b/",
		"--full-history", "--diff-filter=tuxdb",
		"--all",
	}
	if stdin {
		args = append(args, "--stdin")
	}
	return args
}

func scanLog(ctx context.Context, git *gitx.Runner, det *secretRuleSet, o SecretOptions, exclude []string, acc *accumulator, res *SecretResult) error {
	var stdin strings.Builder
	for _, sha := range exclude {
		stdin.WriteString("^" + sha + "\n")
	}
	s, err := git.Stream(ctx, o.Dir, stdin.String(), logArgs(len(exclude) > 0)...)
	if err != nil {
		return err
	}
	commits, perr := parseLog(s, func(fc *fileChange) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if fc.deleted || fc.binary || len(fc.hunks) == 0 || fc.path == "" {
			return nil
		}
		if o.MaxFileSize > 0 && fc.addedBytes() > o.MaxFileSize {
			res.Skipped++
			return nil
		}
		where := location{file: fc.path, commit: fc.commit, author: fc.author}
		if t, err := time.Parse(time.RFC3339, fc.date); err == nil {
			where.date = t.UTC().Format(time.RFC3339)
		}
		for _, h := range fc.hunks {
			if h.text == "" {
				continue
			}
			for _, m := range det.detect(h.text, fc.path, fc.commit) {
				acc.add(m.rule, m.description, m.secret, h.newStart+m.line, where)
			}
		}
		return nil
	})
	res.Commits += commits
	cerr := s.Close()
	if perr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read history of %s: %w", o.RepoPath, perr)
	}
	if cerr != nil {
		return cerr
	}
	return ctx.Err()
}

// scanHead checks the files at HEAD only, read straight from the object
// store, so it works the same on bare repos and never touches a work tree.
func scanHead(ctx context.Context, git *gitx.Runner, det *secretRuleSet, o SecretOptions, acc *accumulator, res *SecretResult) error {
	head, err := git.Output(ctx, o.Dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil || head == "" {
		return nil // no commits yet, or a detached/unborn HEAD: nothing at HEAD to read
	}
	res.Tips = []string{head}
	tree, err := git.Run(ctx, o.Dir, "ls-tree", "-r", "-z", "-l", "--full-tree", head)
	if err != nil {
		return err
	}
	type blob struct{ sha, path string }
	var blobs []blob
	for _, ent := range bytes.Split(tree.Stdout, []byte{0}) {
		meta, path, ok := bytes.Cut(ent, []byte{'\t'})
		if !ok {
			continue
		}
		fields := strings.Fields(string(meta)) // mode type sha size
		if len(fields) != 4 || fields[1] != "blob" {
			continue
		}
		if size, err := strconv.ParseInt(fields[3], 10, 64); err == nil && o.MaxFileSize > 0 && size > o.MaxFileSize {
			res.Skipped++
			continue
		}
		blobs = append(blobs, blob{fields[2], string(path)})
	}
	if len(blobs) == 0 {
		return nil
	}
	var in strings.Builder
	for _, b := range blobs {
		in.WriteString(b.sha + "\n")
	}
	s, err := git.Stream(ctx, o.Dir, in.String(), "cat-file", "--batch")
	if err != nil {
		return err
	}
	defer s.Close()
	r := bufio.NewReaderSize(s, 64*1024)
	for _, b := range blobs {
		header, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read %s in %s: %w", b.path, o.RepoPath, err)
		}
		f := strings.Fields(header) // sha type size | sha missing
		if len(f) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(f[2], 10, 64)
		content := make([]byte, size+1) // +1 for the newline after each object
		if _, err := io.ReadFull(r, content); err != nil {
			return fmt.Errorf("read %s in %s: %w", b.path, o.RepoPath, err)
		}
		content = content[:size]
		if looksBinary(content) {
			continue
		}
		where := location{file: b.path, commit: head}
		for _, m := range det.detect(string(content), b.path, head) {
			acc.add(m.rule, m.description, m.secret, m.line+1, where)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return s.Close()
}

func looksBinary(b []byte) bool {
	return bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0
}

// refTips lists the commit (or tag) every ref points to, sorted and
// unique. Together they are "everything this audit covered".
func refTips(ctx context.Context, git *gitx.Runner, dir string) ([]string, error) {
	out, err := git.Output(ctx, dir, "for-each-ref", "--format=%(objectname)")
	if err != nil {
		return nil, err
	}
	tips := strings.Fields(out)
	slices.Sort(tips)
	return slices.Compact(tips), nil
}

// existingCommits keeps the shas that still exist; a force-push followed
// by gc can remove a tip an earlier audit saw.
func existingCommits(ctx context.Context, git *gitx.Runner, dir string, shas []string) ([]string, error) {
	res, err := git.RunInput(ctx, dir, strings.Join(shas, "\n")+"\n", "cat-file", "--batch-check")
	if err != nil {
		return nil, err
	}
	var keep []string
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[1] != "missing" {
			keep = append(keep, f[0])
		}
	}
	slices.Sort(keep)
	return slices.Compact(keep), nil
}

func sameSet(a, b []string) bool { return slices.Equal(a, b) }

type location struct {
	file, commit, author, date string
}

// accumulator merges every sighting of the same secret into one finding,
// keeping the oldest sighting as where it was introduced.
type accumulator struct {
	o    SecretOptions
	byID map[string]*Finding
}

func newAccumulator(o SecretOptions) *accumulator {
	return &accumulator{o: o, byID: map[string]*Finding{}}
}

func (a *accumulator) add(rule, title, secret string, line int, at location) {
	if strings.TrimSpace(secret) == "" {
		return
	}
	id := FindingID(a.o.RepoID, CheckSecrets, rule, Fingerprint(secret))
	f, ok := a.byID[id]
	if !ok {
		sev, conf := secretSeverity(rule)
		f = &Finding{
			ID: id, RepoID: a.o.RepoID, RepoPath: a.o.RepoPath, Check: CheckSecrets,
			Rule: rule, Title: shortTitle(title, rule), Severity: sev, Confidence: conf,
			Preview: Redact(secret), Status: StatusOpen,
		}
		a.byID[id] = f
	}
	f.Occurrences++
	// git log lists newest first, so a later sighting is older. An empty
	// date (HEAD mode) never replaces a dated one.
	if !ok || (at.date != "" && (f.Date == "" || at.date <= f.Date)) {
		f.File, f.Line, f.Commit, f.Author, f.Date = at.file, line, at.commit, at.author, at.date
	}
}

func (a *accumulator) findings() []Finding {
	out := make([]Finding, 0, len(a.byID))
	for _, f := range a.byID {
		f.Hint = secretHint(f.Date)
		out = append(out, *f)
	}
	slices.SortFunc(out, func(x, y Finding) int {
		if c := y.Severity.Rank() - x.Severity.Rank(); c != 0 {
			return c
		}
		if c := strings.Compare(x.File, y.File); c != 0 {
			return c
		}
		return strings.Compare(x.ID, y.ID)
	})
	return out
}

// criticalRules are key types that give direct access to cloud accounts,
// code hosting, payments or private keys. Matched as substrings of the
// rule ID.
var criticalRules = []string{
	"private-key", "aws-", "gcp-", "azure", "github-", "gitlab-", "bitbucket",
	"stripe", "digitalocean", "heroku", "npm-access-token", "pypi", "rubygems",
	"shopify", "slack-bot", "slack-user", "twilio", "sendgrid", "openai", "anthropic",
	"hashicorp", "vault", "doppler", "databricks", "cloudflare", "alibaba",
}

// lowConfidenceRules are generic patterns that often match test data.
var lowConfidenceRules = []string{"generic-api-key", "generic"}

func secretSeverity(rule string) (Severity, string) {
	r := strings.ToLower(rule)
	for _, g := range lowConfidenceRules {
		if strings.Contains(r, g) {
			return SevMedium, ConfidenceLow
		}
	}
	for _, c := range criticalRules {
		if strings.Contains(r, c) {
			return SevCritical, ConfidenceHigh
		}
	}
	return SevHigh, ConfidenceHigh
}

// ErrNoRepo is returned when a repo folder is gone.
var ErrNoRepo = errors.New("repository folder not found")
