// Command benchreport turns `go test -bench` output into a Markdown table
// of the median time of each benchmark, for the perf workflow's summary.
//
//	go run ./internal/perf/benchreport bench.txt
package main

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: benchreport <bench.txt>")
		os.Exit(2)
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	runs, err := Parse(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(Table(runs))
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
