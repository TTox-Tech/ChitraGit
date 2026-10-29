package schedule

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cron manages one marked block per archive in the user's crontab. Every
// other line of the crontab is kept exactly as it was, and the previous
// crontab is saved to BackupDir before each change.
type cron struct{ env Env }

func (c *cron) Name() string { return "cron" }

func blockStart(id string) string { return "# >>> " + id + " (managed by chitragit; do not edit) >>>" }
func blockEnd(id string) string   { return "# <<< " + id + " <<<" }

func (c *cron) read(ctx context.Context) (string, error) {
	out, errOut, err := c.env.Run(ctx, nil, "crontab", "-l")
	if err != nil {
		// An empty crontab is reported as an error by every cron flavour.
		if strings.Contains(strings.ToLower(string(errOut)), "no crontab") {
			return "", nil
		}
		// Never write a crontab we could not read: it would wipe the user's jobs.
		return "", schedulerError("cannot read your crontab", errOut, err)
	}
	return string(out), nil
}

func (c *cron) write(ctx context.Context, old, updated string) error {
	if old == updated {
		return nil
	}
	if strings.TrimSpace(old) != "" && c.env.BackupDir != "" {
		name := "crontab-" + time.Now().Format("20060102-150405") + ".bak"
		if err := writeAtomic(filepath.Join(c.env.BackupDir, name), []byte(old), 0o600); err != nil {
			return fmt.Errorf("cannot back up your crontab before changing it: %w", err)
		}
	}
	if _, errOut, err := c.env.Run(ctx, []byte(updated), "crontab", "-"); err != nil {
		return schedulerError("cannot update your crontab", errOut, err)
	}
	return nil
}

func (c *cron) Install(ctx context.Context, spec Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	old, err := c.read(ctx)
	if err != nil {
		return err
	}
	id := spec.ID()
	kept, _ := cutBlock(old, id)
	if kept != "" && !strings.HasSuffix(kept, "\n") {
		kept += "\n"
	}
	block := blockStart(id) + "\n" + "# archive: " + cronComment(spec.Archive) + "\n" + cronLine(spec) + "\n" + blockEnd(id) + "\n"
	return c.write(ctx, old, kept+block)
}

func (c *cron) Installed(ctx context.Context, id string) (bool, error) {
	cur, err := c.read(ctx)
	if err != nil {
		return false, err
	}
	_, found := cutBlock(cur, id)
	return found, nil
}

func (c *cron) Remove(ctx context.Context, id string) error {
	old, err := c.read(ctx)
	if err != nil {
		return err
	}
	kept, found := cutBlock(old, id)
	if !found {
		return nil
	}
	return c.write(ctx, old, kept)
}

// cutBlock removes the lines from id's start marker through its end marker.
// A start marker with no end marker removes only the marker line, so a
// hand-edited crontab never loses the user's own lines below it.
func cutBlock(tab, id string) (string, bool) {
	lines := strings.SplitAfter(tab, "\n")
	start, end := -1, -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if start < 0 && t == blockStart(id) {
			start = i
		} else if start >= 0 && t == blockEnd(id) {
			end = i
			break
		}
	}
	if start < 0 {
		return tab, false
	}
	if end < 0 {
		end = start
	}
	return strings.Join(lines[:start], "") + strings.Join(lines[end+1:], ""), true
}

// cronLine is "MM HH * * DOW command". cron runs the command with /bin/sh,
// where % means newline unless escaped.
func cronLine(spec Spec) string {
	dow := "*"
	if spec.Frequency == Weekly {
		dow = fmt.Sprint(int(spec.Weekday))
	}
	var cmd []string
	if spec.Path != "" {
		cmd = append(cmd, "PATH="+shQuote(spec.Path))
	}
	cmd = append(cmd, shQuote(spec.Binary))
	for _, a := range spec.Args() {
		cmd = append(cmd, shQuote(a))
	}
	// The headless run writes its own log; only crashes reach stderr.
	stderrLog := filepath.Join(spec.Archive, ".chitragit", "logs", "schedule-errors.log")
	cmd = append(cmd, ">/dev/null", "2>>"+shQuote(stderrLog))
	line := strings.Join(cmd, " ")
	return fmt.Sprintf("%d %d * * %s %s", spec.Minute, spec.Hour, dow, strings.ReplaceAll(line, "%", `\%`))
}

// shQuote wraps s in single quotes for /bin/sh.
func shQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/._-+:=,@", r))
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// cronComment keeps a path on one comment line.
func cronComment(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}

// JobPath returns a PATH for scheduled jobs: the directories holding git
// and git-lfs first, then the current PATH. Schedulers start jobs with a
// minimal PATH that often misses Homebrew or custom installs.
func JobPath() string {
	var parts []string
	seen := map[string]bool{}
	for _, name := range []string{"git", "git-lfs"} {
		if p, err := exec.LookPath(name); err == nil && !seen[filepath.Dir(p)] {
			seen[filepath.Dir(p)] = true
			parts = append(parts, filepath.Dir(p))
		}
	}
	for _, p := range filepath.SplitList(os.Getenv("PATH")) {
		if p != "" && !seen[p] {
			seen[p] = true
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, string(os.PathListSeparator))
}
