package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TTox-Tech/chitragit/internal/archive"
	"github.com/TTox-Tech/chitragit/internal/config"
	"github.com/TTox-Tech/chitragit/internal/core"
	"github.com/TTox-Tech/chitragit/internal/engine"
	"github.com/TTox-Tech/chitragit/internal/platform"
	"github.com/TTox-Tech/chitragit/internal/schedule"
	"github.com/TTox-Tech/chitragit/internal/ui/tui"
)

// The Setup half of localBackend: what the wizard and Settings screen use.

func (b *localBackend) ArchiveRoot() string {
	if s := b.sess(); s != nil {
		return s.arc.Root
	}
	return ""
}

func (b *localBackend) InitArchive(ctx context.Context, path string) (string, error) {
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == '\\') {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, rest)
		}
	}
	arc, _, err := archive.Init(ctx, path)
	if err != nil {
		return "", err
	}
	b.a.archivePath = arc.Root
	s, err := b.a.open(ctx, "", false)
	if err != nil {
		return "", err
	}
	b.smu.Lock()
	old := b.s
	b.s = s
	b.smu.Unlock()
	if old != nil {
		old.close()
	}
	return arc.Root, nil
}

func (b *localBackend) Scan(ctx context.Context) (tui.ScanSummary, error) {
	s := b.sess()
	if s == nil {
		return tui.ScanSummary{}, archive.ErrNotFound
	}
	git, err := b.runner(ctx)
	if err != nil {
		return tui.ScanSummary{}, err
	}
	l, err := b.lock()
	if err != nil {
		return tui.ScanSummary{}, err
	}
	defer func() { _ = l.Release() }()
	rep, err := engine.Scan(ctx, s.arc.Root, s.scanOptions(), git, s.st)
	if err != nil {
		return tui.ScanSummary{}, err
	}
	repos, err := s.st.Repos(ctx)
	if err != nil {
		return tui.ScanSummary{}, err
	}
	owners := map[string]bool{}
	for _, r := range repos {
		if r.Missing {
			continue
		}
		owner := r.Owner
		if owner == "" {
			owner, _, _ = strings.Cut(r.Path, "/")
		}
		owners[owner] = true
	}
	return tui.ScanSummary{Found: rep.Found, Owners: len(owners)}, nil
}

func (b *localBackend) Settings() (*config.Config, []string, error) {
	var arc *archive.Archive
	s := b.sess()
	if s != nil {
		arc = s.arc
	}
	cfg, warnings, err := b.a.loadConfig(arc)
	if err != nil {
		return nil, nil, flatten(err)
	}
	if s != nil {
		s.cfg = cfg // the next sync uses what was just saved
	}
	out := make([]string, 0, len(warnings))
	for _, w := range warnings {
		out = append(out, w.String())
	}
	return cfg, out, nil
}

func (b *localBackend) SaveSetting(key, value string, user bool) (string, error) {
	file, err := platform.UserConfigFile()
	if s := b.sess(); s != nil && !user {
		file = s.arc.ConfigFile()
	}
	if err != nil {
		return "", err
	}
	if err := config.SetInFile(file, key, value); err != nil {
		return "", flatten(err)
	}
	return file, nil
}

func (b *localBackend) Schedule(ctx context.Context) (tui.ScheduleInfo, error) {
	s := b.sess()
	if s == nil {
		return tui.ScheduleInfo{}, nil
	}
	backend, err := b.a.scheduleEnv()
	if err != nil {
		return tui.ScheduleInfo{}, err
	}
	spec, err := schedule.Load(s.arc.Meta())
	if err != nil || spec == nil {
		return tui.ScheduleInfo{Backend: backend.Name()}, err
	}
	installed, err := backend.Installed(ctx, spec.ID())
	if err != nil {
		return tui.ScheduleInfo{}, err
	}
	return tui.ScheduleInfo{On: installed, Missing: !installed, When: spec.Describe(), Backend: backend.Name(),
		Next: spec.Next(time.Now())}, nil
}

func (b *localBackend) SetSchedule(ctx context.Context, c tui.ScheduleChoice) (tui.ScheduleInfo, error) {
	s := b.sess()
	if s == nil {
		return tui.ScheduleInfo{}, archive.ErrNotFound
	}
	if c.Frequency == "" {
		backend, err := b.a.removeSchedule(ctx, s.arc)
		if err != nil {
			return tui.ScheduleInfo{}, flatten(err)
		}
		return tui.ScheduleInfo{Backend: backend.Name()}, nil
	}
	spec := schedule.Spec{Frequency: c.Frequency, Hour: c.Hour, Minute: c.Minute, Weekday: c.Weekday}
	backend, err := b.a.installSchedule(ctx, s.arc, &spec, "")
	if err != nil {
		return tui.ScheduleInfo{}, flatten(err)
	}
	return tui.ScheduleInfo{On: true, When: spec.Describe(), Backend: backend.Name(), Next: spec.Next(time.Now())}, nil
}

// flatten puts a config error's list of problems and its hint into one
// line, since a screen has no room for the CLI's multi-line layout.
func flatten(err error) error {
	var ce *core.Error
	if !errors.As(err, &ce) {
		return err
	}
	msg := ce.Message
	if ce.Detail != "" && ce.Category == core.CatConfig {
		var parts []string
		for _, l := range strings.Split(ce.Detail, "\n") {
			if l = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "- ")); l != "" {
				parts = append(parts, l)
			}
		}
		msg = strings.Join(parts, "; ")
	}
	if ce.Err != nil && ce.Detail == "" {
		msg += ": " + ce.Err.Error()
	}
	if ce.Hint != "" {
		msg += " (" + ce.Hint + ")"
	}
	return errors.New(msg)
}
