package engine

// Re-exports for per-run log files, so the UI packages reach
// internal/runlog only through engine.

import (
	"os"
	"time"

	"github.com/TTox-Tech/chitragit/internal/runlog"
)

const (
	RunLogsKept = runlog.Keep
)

func CreateRunLog(dir string, kind string, now time.Time) (*os.File, error) {
	return runlog.Create(dir, kind, now)
}

func LatestRunLog(dir string, kind string) string { return runlog.Latest(dir, kind) }

func PruneRunLogs(dir string, keep time.Duration, now time.Time) (int, error) {
	return runlog.Prune(dir, keep, now)
}
