package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/i18n"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/runlog"
	"github.com/TTox-Tech/chitragit/internal/schedule"
	"github.com/TTox-Tech/chitragit/internal/store"
)

// scheduleEnv returns the scheduler environment; tests replace a.schedEnv.
func (a *app) scheduleEnv() (schedule.Backend, error) {
	if a.schedEnv != nil {
		return schedule.For(*a.schedEnv), nil
	}
	dir, err := platform.ConfigDir()
	if err != nil {
		return nil, err
	}
	return schedule.For(schedule.DefaultEnv(filepath.Join(dir, "backups"))), nil
}

func (a *app) scheduleCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schedule",
		Short: "Run sync automatically every day or week",
		Long: "Adds a job to your system's own scheduler: cron on Linux, launchd on macOS,\n" +
			"Task Scheduler on Windows. The job runs `chitragit sync --headless` for this\n" +
			"archive, never asks questions, writes a log to .chitragit/logs/ and shows a\n" +
			"desktop notification when it finishes. If a sync is already running when the\n" +
			"job starts, the job exits without touching anything.",
	}
	cmd.AddCommand(a.scheduleInstallCmd(), a.scheduleStatusCmd(), a.scheduleRemoveCmd())
	return cmd
}

func (a *app) scheduleInstallCmd() *cobra.Command {
	var daily, weekly, day, binary string
	cmd := &cobra.Command{
		Use:     "install (--daily HH:MM | --weekly HH:MM --day <day>)",
		Short:   "Set up the scheduled sync (replaces an existing one)",
		Example: "  chitragit schedule install --daily 02:00\n  chitragit schedule install --weekly 03:30 --day sun",
		Args:    argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			spec := schedule.Spec{Frequency: schedule.Daily}
			at := daily
			switch {
			case daily != "" && weekly != "":
				return &usageError{fmt.Errorf("use either --daily or --weekly, not both")}
			case daily == "" && weekly == "":
				return &usageError{fmt.Errorf("say when to run: --daily 02:00 or --weekly 02:00 --day sun")}
			case weekly != "":
				spec.Frequency, at = schedule.Weekly, weekly
				if day == "" {
					day = "sun"
				}
				d, err := schedule.ParseWeekday(day)
				if err != nil {
					return &usageError{err}
				}
				spec.Weekday = d
			case day != "":
				return &usageError{fmt.Errorf("--day only goes with --weekly")}
			}
			h, m, err := schedule.ParseClock(at)
			if err != nil {
				return &usageError{err}
			}
			spec.Hour, spec.Minute = h, m

			arc, err := a.findArchive()
			if err != nil {
				return err
			}
			backend, err := a.installSchedule(cmd.Context(), arc, &spec, binary)
			if err != nil {
				return err
			}
			next := spec.Next(time.Now())
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "installed": true, "backend": backend.Name(),
					"id": spec.ID(), "when": spec.Describe(), "next_run": next.Format(time.RFC3339), "command": spec.Args()})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(i18n.T("schedule.installed",
				"when", spec.Describe(), "backend", backend.Name(), "next", next.Format("Mon 2 Jan 15:04"))))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&daily, "daily", "", "run every day at this time (24-hour HH:MM)")
	f.StringVar(&weekly, "weekly", "", "run once a week at this time (24-hour HH:MM)")
	f.StringVar(&day, "day", "", "day of the week for --weekly, e.g. sun or monday (default sun)")
	f.StringVar(&binary, "binary", "", "chitragit executable the job runs (default: this one)")
	_ = f.MarkHidden("binary")
	return cmd
}

// installSchedule fills in the archive, binary and PATH of spec, adds the
// job to the OS scheduler and records it in the archive.
func (a *app) installSchedule(ctx context.Context, arc *archive.Archive, spec *schedule.Spec, binary string) (schedule.Backend, error) {
	var err error
	spec.Archive = arc.Root
	if spec.Binary, err = a.executable(binary); err != nil {
		return nil, err
	}
	spec.Path = schedule.JobPath()
	spec.Installed = time.Now().Format(time.RFC3339)
	backend, err := a.scheduleEnv()
	if err != nil {
		return nil, err
	}
	if err := backend.Install(ctx, *spec); err != nil {
		return nil, err
	}
	return backend, schedule.Save(arc.Meta(), *spec)
}

// removeSchedule deletes the archive's job and its record.
func (a *app) removeSchedule(ctx context.Context, arc *archive.Archive) (schedule.Backend, error) {
	backend, err := a.scheduleEnv()
	if err != nil {
		return nil, err
	}
	if err := backend.Remove(ctx, schedule.JobID(arc.Root)); err != nil {
		return nil, err
	}
	return backend, schedule.Forget(arc.Meta())
}

// executable resolves the chitragit binary the job will run. A binary from
// `go run` lives in a temporary folder that is deleted later, so it is
// refused.
func (a *app) executable(override string) (string, error) {
	p := override
	if p == "" {
		exe, err := os.Executable()
		if err != nil {
			return "", &core.Error{Category: core.CatEnvironment, Message: "cannot find the chitragit executable", Err: err}
		}
		p = exe
		if strings.Contains(filepath.ToSlash(p), "/go-build") {
			return "", &core.Error{Category: core.CatEnvironment, Message: i18n.T("schedule.go_run", "path", filepath.Dir(p)),
				Hint: "go install github.com/TTox-Tech/chitragit/cmd/chitragit@latest"}
		}
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Abs(p)
}

type scheduleStatus struct {
	Schema    string            `json:"schema"`
	Scheduled bool              `json:"scheduled"`
	Backend   string            `json:"backend"`
	Installed bool              `json:"installed"` // the OS scheduler still has the job
	When      string            `json:"when,omitempty"`
	NextRun   string            `json:"next_run,omitempty"`
	Binary    string            `json:"binary,omitempty"`
	LastRun   *store.RunSummary `json:"last_run,omitempty"`
	LogFile   string            `json:"log_file,omitempty"`
}

func (a *app) scheduleStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the schedule, the next run and how the last run went",
		Args:  argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			arc, err := a.findArchive()
			if err != nil {
				return err
			}
			backend, err := a.scheduleEnv()
			if err != nil {
				return err
			}
			spec, err := schedule.Load(arc.Meta())
			if err != nil {
				return err
			}
			st := scheduleStatus{Schema: jsonSchema, Backend: backend.Name(), LogFile: runlog.Latest(arc.LogsDir(), "sync")}
			if st.Installed, err = backend.Installed(ctx, schedule.JobID(arc.Root)); err != nil {
				return err
			}
			if spec != nil {
				st.Scheduled, st.When, st.Binary = true, spec.Describe(), spec.Binary
				st.NextRun = spec.Next(time.Now()).Format(time.RFC3339)
			}
			if st.LastRun, err = lastRun(ctx, arc); err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(st)
			}
			a.printScheduleStatus(st)
			return nil
		},
	}
}

func lastRun(ctx context.Context, arc *archive.Archive) (*store.RunSummary, error) {
	st, err := store.Open(ctx, arc.DBFile())
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return st.LastRun(ctx)
}

func (a *app) printScheduleStatus(st scheduleStatus) {
	row := func(label, value string) {
		fmt.Fprintf(a.out, "%s %s\n", a.styles.Label.Render(fmt.Sprintf("%-10s", label)), a.styles.Text.Render(value))
	}
	switch {
	case !st.Scheduled && !st.Installed:
		fmt.Fprintln(a.out, a.styles.Muted.Render(i18n.T("schedule.none")))
	case st.Scheduled && !st.Installed:
		a.warn(i18n.T("schedule.missing", "backend", st.Backend))
	default:
		when := st.When
		if when == "" {
			when = "unknown (installed outside this archive's settings)"
		}
		row("Schedule", when+" ("+st.Backend+")")
		if st.NextRun != "" {
			t, _ := time.Parse(time.RFC3339, st.NextRun)
			row("Next run", t.Format("Mon 2 Jan 15:04"))
		}
	}
	if r := st.LastRun; r != nil {
		t, _ := time.Parse(time.RFC3339Nano, r.StartedAt)
		row("Last run", fmt.Sprintf("%s · %s · %d updated · %d failed", t.Local().Format("Mon 2 Jan 15:04"), r.State, r.Updated, r.Failed))
	}
	if st.LogFile != "" {
		row("Log", st.LogFile)
	}
}

func (a *app) scheduleRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove",
		Short: "Stop the scheduled sync for this archive",
		Args:  argsN(0, 0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			arc, err := a.findArchive()
			if err != nil {
				return err
			}
			backend, err := a.removeSchedule(cmd.Context(), arc)
			if err != nil {
				return err
			}
			if a.jsonOut {
				return a.printJSON(map[string]any{"schema": jsonSchema, "removed": true, "backend": backend.Name()})
			}
			fmt.Fprintln(a.out, a.styles.OK.Render(a.icon(iconOK))+a.styles.Text.Render(i18n.T("schedule.removed", "backend", backend.Name())))
			return nil
		},
	}
}
