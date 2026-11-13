// Command benchreport turns `go test -bench` output into a Markdown table
// of the median time of each benchmark, for the perf workflow's summary.
//
//	go run ./internal/perf/benchreport bench.txt
//
// With -base it compares two runs and exits 1 when a benchmark's median
// is more than -max percent slower than on the base commit (the perf
// budget gate on pull requests):
//
//	go run ./internal/perf/benchreport -base base.txt head.txt
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

func main() {
	base := flag.String("base", "", "benchmark output of the base commit; compare and fail on a regression")
	maxPct := flag.Float64("max", 15, "largest allowed slowdown of a median, in percent")
	floor := flag.Duration("floor", 50*time.Millisecond, "slowdowns smaller than this never fail, however large in percent")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: benchreport [-base base.txt [-max 15] [-floor 50ms]] bench.txt")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	head, err := parseFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *base == "" {
		fmt.Print(Table(head))
		return
	}
	old, err := parseFile(*base)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	table, failed := Compare(old, head, *maxPct, *floor)
	fmt.Print(table)
	if failed {
		os.Exit(1)
	}
}

func parseFile(name string) (map[string][]float64, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	runs, err := Parse(f)
	if err == nil && len(runs) == 0 {
		err = fmt.Errorf("%s: no benchmark results", name)
	}
	return runs, err
}

// Parse reads the ns/op of every benchmark line, keyed by benchmark name
// without the -GOMAXPROCS suffix, in the order the runs appear.
func Parse(r io.Reader) (map[string][]float64, error) {
	runs := map[string][]float64{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") || f[3] != "ns/op" {
			continue
		}
		ns, err := strconv.ParseFloat(f[2], 64)
		if err != nil {
			return nil, fmt.Errorf("%s: bad ns/op %q", f[0], f[2])
		}
		name := f[0]
		if i := strings.LastIndexByte(name, '-'); i > 0 {
			if _, err := strconv.Atoi(name[i+1:]); err == nil {
				name = name[:i]
			}
		}
		runs[name] = append(runs[name], ns)
	}
	return runs, sc.Err()
}

// Median returns the middle value (the mean of the two middle ones for an
// even count). The median, not the mean, so one slow run on a busy CI
// machine does not move the result.
func Median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Table is a Markdown table of each benchmark's median, sorted by name.
func Table(runs map[string][]float64) string {
	var b strings.Builder
	b.WriteString("| Benchmark | Median | Runs |\n|---|---:|---:|\n")
	for _, name := range slices.Sorted(maps.Keys(runs)) {
		fmt.Fprintf(&b, "| %s | %s | %d |\n", strings.TrimPrefix(name, "Benchmark"), seconds(Median(runs[name])), len(runs[name]))
	}
	return b.String()
}

func seconds(ns float64) string {
	return strconv.FormatFloat(ns/1e9, 'f', 3, 64) + " s"
}

// Compare is a Markdown table of base and head medians. A benchmark fails
// when its head median is more than maxPct percent and more than floor
// slower than its base median. The floor keeps millisecond benchmarks,
// where a few milliseconds of CI noise is a large percentage, from
// failing on noise. A benchmark missing on either side is listed but
// never fails: it was added or removed by the change.
func Compare(base, head map[string][]float64, maxPct float64, floor time.Duration) (string, bool) {
	var b strings.Builder
	b.WriteString("| Benchmark | Base | Head | Change | |\n|---|---:|---:|---:|---|\n")
	names := slices.Sorted(maps.Keys(head))
	for name := range base {
		if _, ok := head[name]; !ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	failed := false
	for _, name := range names {
		short := strings.TrimPrefix(name, "Benchmark")
		was, okBase := base[name]
		now, okHead := head[name]
		switch {
		case !okBase:
			fmt.Fprintf(&b, "| %s | | %s | | new |\n", short, seconds(Median(now)))
			continue
		case !okHead:
			fmt.Fprintf(&b, "| %s | %s | | | removed |\n", short, seconds(Median(was)))
			continue
		}
		m0, m1 := Median(was), Median(now)
		pct := 0.0
		if m0 > 0 {
			pct = (m1 - m0) / m0 * 100
		}
		verdict := "ok"
		if pct > maxPct && m1-m0 > float64(floor) {
			verdict, failed = fmt.Sprintf("**slower than the %g%% budget**", maxPct), true
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %+.1f%% | %s |\n", short, seconds(m0), seconds(m1), pct, verdict)
	}
	return b.String(), failed
}
