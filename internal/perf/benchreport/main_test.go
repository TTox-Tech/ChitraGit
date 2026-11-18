package main

import (
	"strings"
	"testing"
	"time"
)

const sample = `goos: linux
BenchmarkScan-4           	       1	  48780647 ns/op	       500.0 repos
BenchmarkScan-4           	       1	  36456549 ns/op	       500.0 repos
BenchmarkScan-4           	       1	  40000000 ns/op	       500.0 repos
BenchmarkSyncNoChange-16  	       1	2471365744 ns/op	       500.0 repos
BenchmarkSyncNoChange-16  	       1	2481795923 ns/op	       500.0 repos
BenchmarkName-with-dash   	       1	   1000000 ns/op
PASS
ok  	github.com/TTox-Tech/chitragit/internal/perf	24.835s
`

func TestParseAndTable(t *testing.T) {
	runs, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs["BenchmarkScan"]) != 3 || len(runs["BenchmarkSyncNoChange"]) != 2 || len(runs["BenchmarkName-with-dash"]) != 1 {
		t.Fatalf("runs = %v", runs)
	}
	want := "| Benchmark | Median | Runs |\n|---|---:|---:|\n" +
		"| Name-with-dash | 0.001 s | 1 |\n" +
		"| Scan | 0.040 s | 3 |\n" +
		"| SyncNoChange | 2.477 s | 2 |\n"
	if got := Table(runs); got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
}

func TestParseRejectsBadNumbers(t *testing.T) {
	if _, err := Parse(strings.NewReader("BenchmarkX-4 1 abc ns/op\n")); err == nil {
		t.Error("bad ns/op accepted")
	}
}

func TestMedian(t *testing.T) {
	for _, c := range []struct {
		in   []float64
		want float64
	}{{nil, 0}, {[]float64{5}, 5}, {[]float64{9, 1, 5}, 5}, {[]float64{4, 1, 3, 2}, 2.5}} {
		if got := Median(c.in); got != c.want {
			t.Errorf("Median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCompare(t *testing.T) {
	ms := func(v ...float64) []float64 {
		for i := range v {
			v[i] *= 1e6
		}
		return v
	}
	base := map[string][]float64{
		"BenchmarkSync":    ms(1000, 1010, 990),
		"BenchmarkScan":    ms(200, 210, 190),
		"BenchmarkStatus":  ms(10, 10, 10),
		"BenchmarkRemoved": ms(5),
	}
	head := map[string][]float64{
		"BenchmarkSync":   ms(1100, 1140, 5000), // +14%: within budget; the outlier does not count
		"BenchmarkScan":   ms(260, 250, 240),    // +25%, but 50 ms is not over the floor
		"BenchmarkStatus": ms(30, 30, 30),       // +200% but only 20 ms
		"BenchmarkNew":    ms(7),
	}
	table, failed := Compare(base, head, 15, 50*time.Millisecond)
	if failed {
		t.Errorf("failed within the budget:\n%s", table)
	}
	for _, want := range []string{"| Sync | 1.000 s | 1.140 s | +14.0% | ok |", "| New | | 0.007 s | | new |", "| Removed | 0.005 s | | | removed |", "| Status | 0.010 s | 0.030 s | +200.0% | ok |"} {
		if !strings.Contains(table, want) {
			t.Errorf("table lacks %q:\n%s", want, table)
		}
	}

	head["BenchmarkScan"] = ms(300, 290, 280) // +45%, 90 ms
	table, failed = Compare(base, head, 15, 50*time.Millisecond)
	if !failed || !strings.Contains(table, "| Scan | 0.200 s | 0.290 s | +45.0% | **slower than the 15% budget** |") {
		t.Errorf("regression not failed:\n%s", table)
	}
}
