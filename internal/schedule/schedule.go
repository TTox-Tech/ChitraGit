// Package schedule installs, inspects and removes the OS job that runs
// `chitragit sync --headless` on a timer (PRD F14): cron on Linux and other
// Unixes, a launchd agent on macOS, and Task Scheduler on Windows.
//
// Each archive gets at most one job. Its ID is derived from the archive
// path, so installing again replaces the job instead of adding a second
// one. The job's settings are also saved in the archive (schedule.json) so
// that `schedule status` can show the next run without parsing every
// scheduler's own format.
package schedule

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/core"
)

// Frequencies a schedule can have. The wizard offers Off / Daily / Weekly.
const (
	Daily  = "daily"
	Weekly = "weekly"
)

// Spec describes one scheduled job.
type Spec struct {
	Archive   string       `json:"archive"`   // absolute archive root
	Binary    string       `json:"binary"`    // absolute path of the chitragit executable
	Frequency string       `json:"frequency"` // daily | weekly
	Hour      int          `json:"hour"`
	Minute    int          `json:"minute"`
	Weekday   time.Weekday `json:"weekday"`        // weekly only
	Path      string       `json:"path,omitempty"` // PATH for the job, so it finds git
	Installed string       `json:"installed_at,omitempty"`
}

// Validate checks the fields a backend relies on.
func (s Spec) Validate() error {
	var problems []string
	if !filepath.IsAbs(s.Archive) {
		problems = append(problems, "archive path must be absolute")
	}
	if !filepath.IsAbs(s.Binary) {
		problems = append(problems, "chitragit path must be absolute")
	}
	if strings.ContainsAny(s.Archive+s.Binary+s.Path, "\r\n\x00") {
		problems = append(problems, "paths must not contain line breaks")
	}
	if s.Frequency != Daily && s.Frequency != Weekly {
		problems = append(problems, fmt.Sprintf("frequency %q must be daily or weekly", s.Frequency))
	}
	if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 {
		problems = append(problems, fmt.Sprintf("time %02d:%02d is not a valid time of day", s.Hour, s.Minute))
	}
	if s.Weekday < time.Sunday || s.Weekday > time.Saturday {
		problems = append(problems, "weekday out of range")
	}
	if len(problems) == 0 {
		return nil
	}
	return &core.Error{Category: core.CatConfig, Message: "invalid schedule: " + strings.Join(problems, "; ")}
}

// ID names the job for this archive, e.g. "chitragit-3f9a1c2b7d".
func (s Spec) ID() string { return JobID(s.Archive) }

// Args are the arguments the job passes to chitragit.
func (s Spec) Args() []string {
	return []string{"sync", "--headless", "--archive", s.Archive}
}

// Describe renders "daily at 02:00" or "weekly on Sunday at 02:00".
func (s Spec) Describe() string {
	at := fmt.Sprintf("%02d:%02d", s.Hour, s.Minute)
	if s.Frequency == Weekly {
		return "weekly on " + s.Weekday.String() + " at " + at
	}
	return "daily at " + at
}

// Next returns the first run time strictly after now, in now's location.
func (s Spec) Next(now time.Time) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), s.Hour, s.Minute, 0, 0, now.Location())
	for !t.After(now) || (s.Frequency == Weekly && t.Weekday() != s.Weekday) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// JobID derives a stable job name from an archive path. Windows paths are
// case-insensitive, so they are folded first.
func JobID(archive string) string {
	p := filepath.Clean(archive)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	sum := sha256.Sum256([]byte(p))
	return "chitragit-" + hex.EncodeToString(sum[:5])
}

// ParseClock parses "02:00" or "2:30" into hour and minute.
func ParseClock(s string) (hour, minute int, err error) {
	h, m, ok := strings.Cut(strings.TrimSpace(s), ":")
	hour, err1 := strconv.Atoi(h)
	minute, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || len(m) != 2 || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid time %q, use 24-hour HH:MM such as 02:00", s)
	}
	return hour, minute, nil
}

// ParseWeekday accepts English day names or their first three letters.
func ParseWeekday(s string) (time.Weekday, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	for d := time.Sunday; d <= time.Saturday; d++ {
		name := strings.ToLower(d.String())
		if s == name || (len(s) >= 3 && strings.HasPrefix(name, s)) {
			return d, nil
		}
	}
	return 0, fmt.Errorf("invalid day %q, use a day name such as sun or monday", s)
}

// Backend talks to one OS scheduler.
type Backend interface {
	// Name is shown to the user, e.g. "cron" or "Task Scheduler".
	Name() string
	// Install creates or replaces the job for spec.ID().
	Install(ctx context.Context, spec Spec) error
	// Installed reports whether the scheduler still has the job.
	Installed(ctx context.Context, id string) (bool, error)
	// Remove deletes the job. Removing a job that does not exist is not an error.
	Remove(ctx context.Context, id string) error
}

// Runner runs a program and returns its stdout and stderr. Tests replace it.
type Runner func(ctx context.Context, stdin []byte, name string, args ...string) (stdout, stderr []byte, err error)

// Exec is the real Runner.
func Exec(ctx context.Context, stdin []byte, name string, args ...string) ([]byte, []byte, error) {
	var out, errOut bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.Bytes(), errOut.Bytes(), err
}

// Env holds what backends need from the machine, so tests can fake it.
type Env struct {
	GOOS      string
	Run       Runner
	Home      string // user home, for ~/Library/LaunchAgents
	UID       int    // for launchctl's gui/<uid> domain
	BackupDir string // where the previous crontab is saved before a change
	TempDir   string // where the Task Scheduler XML is written
}

// DefaultEnv describes the current machine.
func DefaultEnv(backupDir string) Env {
	home, _ := os.UserHomeDir()
	return Env{GOOS: runtime.GOOS, Run: Exec, Home: home, UID: os.Getuid(), BackupDir: backupDir, TempDir: os.TempDir()}
}

// For returns the backend for env.GOOS.
func For(env Env) Backend {
	switch env.GOOS {
	case "windows":
		return &schtasks{env: env}
	case "darwin":
		return &launchd{env: env}
	default:
		return &cron{env: env}
	}
}

// specFile is where the archive remembers its schedule.
func specFile(metaDir string) string { return filepath.Join(metaDir, "schedule.json") }

// Save records spec in the archive's data folder.
func Save(metaDir string, spec Spec) error {
	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(specFile(metaDir), append(b, '\n'), 0o600)
}

// Load reads the archive's saved schedule, or returns nil if there is none.
func Load(metaDir string) (*Spec, error) {
	b, err := os.ReadFile(specFile(metaDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, &core.Error{Category: core.CatConfig, Message: "cannot read " + specFile(metaDir), Err: err,
			Hint: "run `chitragit schedule install` again to rewrite it"}
	}
	return &s, nil
}

// Forget deletes the saved schedule.
func Forget(metaDir string) error {
	err := os.Remove(specFile(metaDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// writeAtomic writes via a temp file and rename, so a crash never leaves a
// half-written file behind.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// schedulerError wraps a failed scheduler command with its output.
func schedulerError(what string, stderr []byte, err error) error {
	return &core.Error{
		Category: core.CatEnvironment,
		Message:  what,
		Detail:   strings.TrimSpace(string(stderr)),
		Err:      err,
	}
}
