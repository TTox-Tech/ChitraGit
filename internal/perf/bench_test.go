package perf

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/TTox-Tech/chitragit/internal/ui/cli"
)

// The budget benchmarks run the real CLI in process against one synthetic
// archive, the way a user runs it, so startup, config, the database and
// git calls all count. They are slow: run them on purpose, e.g.
//
//	go test ./internal/perf -run '^$' -bench . -benchtime 3s -count 5
//
// CHITRAGIT_PERF_REPOS sets the archive size (default 5000).

var (
	benchOnce sync.Once
	benchDir  string
	benchArc  *Archive
	benchErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if benchDir != "" {
		_ = os.RemoveAll(benchDir)
	}
	os.Exit(code)
}

// benchArchive generates the archive once per process, with its own
// config and cache folders, and runs the first scan and sync so the
// benchmarks measure repeat runs.
func benchArchive(b *testing.B) *Archive {
	b.Helper()
	benchOnce.Do(func() {
		if _, err := exec.LookPath("git"); err != nil {
			benchErr = err
			return
		}
		n := 5000
		if s := os.Getenv("CHITRAGIT_PERF_REPOS"); s != "" {
			if n, benchErr = strconv.Atoi(s); benchErr != nil {
				return
			}
		}
		// Not b.TempDir: the archive outlives this benchmark for the others.
		dir, err := os.MkdirTemp("", "chitragit-perf-")
		if err != nil {
			benchErr = err
			return
		}
		benchDir = dir
		home := filepath.Join(dir, "home")
		for k, v := range map[string]string{
			"CHITRAGIT_CONFIG_DIR":     filepath.Join(home, "config"),
			"CHITRAGIT_CACHE_DIR":      filepath.Join(home, "cache"),
			"CHITRAGIT_NOTIFY_DESKTOP": "false",
			"NO_COLOR":                 "1",
		} {
			if err := os.Setenv(k, v); err != nil {
				benchErr = err
				return
			}
		}
		if benchArc, benchErr = Generate(context.Background(), filepath.Join(dir, "gen"), Options{Repos: n}); benchErr != nil {
			return
		}
		for _, args := range [][]string{{"init", benchArc.Root}, {"scan"}, {"sync", "--mode", "fetch"}} {
			if benchErr = runCLI(benchArc.Root, args...); benchErr != nil {
				return
			}
		}
	})
	if benchErr != nil {
		b.Fatal(benchErr)
	}
	return benchArc
}

type cliError struct {
	args []string
	code int
	out  string
}

func (e *cliError) Error() string {
	return "chitragit " + strings.Join(e.args, " ") + ": exit " + strconv.Itoa(e.code) + "\n" + e.out
}

// runCLI runs one headless chitragit command against the archive.
func runCLI(root string, args ...string) error {
	full := append([]string{"--headless", "--archive", root}, args...)
	if args[0] == "init" {
		full = append([]string{"--headless"}, args...)
	}
	var out bytes.Buffer
	if code := cli.Execute(context.Background(), full, &out, &out); code != 0 {
		return &cliError{args: args, code: code, out: out.String()}
	}
	return nil
}

func benchCLI(b *testing.B, args ...string) {
	a := benchArchive(b)
	b.ResetTimer()
	for range b.N {
		if err := runCLI(a.Root, args...); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(a.Paths)), "repos")
}

// BenchmarkScan is a scan of an archive that has not changed: the common
// case, budget < 3 s for 5,000 repos on a warm disk.
func BenchmarkScan(b *testing.B) { benchCLI(b, "scan") }

// BenchmarkScanNew is the first scan of a fresh database: every repo is
// opened with git once to read its ID and remotes.
func BenchmarkScanNew(b *testing.B) {
	a := benchArchive(b)
	db := filepath.Join(a.Root, ".chitragit")
	backup := db + ".perf-backup"
	if err := os.Rename(db, backup); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = os.RemoveAll(db)
		_ = os.Rename(backup, db)
	})
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		if err := os.RemoveAll(db); err != nil {
			b.Fatal(err)
		}
		if err := runCLI(a.Root, "init", a.Root); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := runCLI(a.Root, "scan"); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(len(a.Paths)), "repos")
}

// BenchmarkSyncNoChange fetches every repo when nothing changed upstream.
// Upstreams are local folders, so this is the engine's own cost per repo
// (git calls, snapshots, the database), not the network's.
func BenchmarkSyncNoChange(b *testing.B) { benchCLI(b, "sync", "--mode", "fetch") }

// BenchmarkStatus and BenchmarkLs read the last results from the database.
func BenchmarkStatus(b *testing.B) { benchCLI(b, "status", "--json") }

func BenchmarkLs(b *testing.B) { benchCLI(b, "ls", "--json") }
