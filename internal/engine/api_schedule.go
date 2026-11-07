package engine

// Re-exports for scheduled syncs, so the UI packages reach
// internal/schedule only through engine.

import (
	"time"

	"github.com/TTox-Tech/chitragit/internal/schedule"
)

type (
	ScheduleBackend = schedule.Backend
	ScheduleEnv     = schedule.Env
	ScheduleSpec    = schedule.Spec
)

const (
	Daily  = schedule.Daily
	Weekly = schedule.Weekly
)

func DefaultScheduleEnv(backupDir string) schedule.Env { return schedule.DefaultEnv(backupDir) }

func ScheduleBackendFor(env schedule.Env) schedule.Backend { return schedule.For(env) }

func ForgetSchedule(metaDir string) error { return schedule.Forget(metaDir) }

func ScheduleJobID(archive string) string { return schedule.JobID(archive) }

func ScheduleJobPath() string { return schedule.JobPath() }

func LoadSchedule(metaDir string) (*schedule.Spec, error) { return schedule.Load(metaDir) }

func SaveSchedule(metaDir string, spec schedule.Spec) error { return schedule.Save(metaDir, spec) }

func ParseClock(s string) (hour int, minute int, err error) { return schedule.ParseClock(s) }

func ParseWeekday(s string) (time.Weekday, error) { return schedule.ParseWeekday(s) }
