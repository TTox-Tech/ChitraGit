package main

import (
	"strings"
	"testing"
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
