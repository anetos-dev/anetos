// SPDX-License-Identifier: Apache-2.0

// Command benchcmp compares two runs of the benchmarks (go test -bench
// output, several samples of each) and fails on a regression: the CI's
// gate (design §22), and make bench-compare's.
//
//	benchcmp [-threshold 0.2] old.txt new.txt
//
// It prints a Markdown table of the median time, memory and allocations
// of each benchmark in both runs. A benchmark regresses when its median
// allocations grow, or when its median time grows by more than the
// threshold and every new sample is slower than every old one (with five
// samples each, chance alone puts them in that order once in 252).
// Benchmarks in one run only are listed, not judged. It exits 1 on a
// regression, 2 on bad input.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// sample is one line of go test -bench output.
type sample struct{ ns, bytes, allocs float64 }

// procs is the "-8" (GOMAXPROCS) that go test adds to names when it
// isn't 1.
var procs = regexp.MustCompile(`-\d+$`)

// parse reads lines like "BenchmarkName-8  12345  678.9 ns/op  10 B/op
// 2 allocs/op": a name, an iteration count, then value and unit pairs in
// any order (MB/s and custom metrics are skipped).
func parse(r io.Reader) (map[string][]sample, []string, error) {
	out := map[string][]sample{}
	var order []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || !strings.HasPrefix(f[0], "Benchmark") {
			continue
		}
		if _, err := strconv.Atoi(f[1]); err != nil {
			continue
		}
		var s sample
		found := false
		for i := 2; i+1 < len(f); i += 2 {
			v, err := strconv.ParseFloat(f[i], 64)
			if err != nil {
				break
			}
			switch f[i+1] {
			case "ns/op":
				s.ns, found = v, true
			case "B/op":
				s.bytes = v
			case "allocs/op":
				s.allocs = v
			}
		}
		if !found {
			continue
		}
		// The same names in both runs, whatever their GOMAXPROCS; a
		// sub-benchmark named like "size-64" loses its "-64" when
		// GOMAXPROCS is 1, in both runs alike.
		name := strings.TrimPrefix(procs.ReplaceAllString(f[0], ""), "Benchmark")
		if _, ok := out[name]; !ok {
			order = append(order, name)
		}
		out[name] = append(out[name], s)
	}
	return out, order, sc.Err()
}

func median(xs []float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func field(ss []sample, f func(sample) float64) []float64 {
	out := make([]float64, len(ss))
	for i, s := range ss {
		out[i] = f(s)
	}
	return out
}

// verdict judges one benchmark.
type verdict struct {
	name                   string
	oldNS, newNS           float64
	oldB, newB             float64
	oldAllocs, newAllocs   float64
	timeWorse, allocsWorse bool
}

func compare(name string, old, new []sample, threshold float64) verdict {
	ns := func(s sample) float64 { return s.ns }
	v := verdict{name: name,
		oldNS: median(field(old, ns)), newNS: median(field(new, ns)),
		oldB: median(field(old, func(s sample) float64 { return s.bytes })), newB: median(field(new, func(s sample) float64 { return s.bytes })),
		oldAllocs: median(field(old, func(s sample) float64 { return s.allocs })), newAllocs: median(field(new, func(s sample) float64 { return s.allocs })),
	}
	v.allocsWorse = v.newAllocs >= v.oldAllocs+1
	slower := v.newNS > v.oldNS*(1+threshold)
	separated := slices.Min(field(new, ns)) > slices.Max(field(old, ns))
	v.timeWorse = slower && separated && len(old) >= 3 && len(new) >= 3
	return v
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("benchcmp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	threshold := fs.Float64("threshold", 0.2, "the slowdown of the median time that fails, 0.2 for 20%")
	if err := fs.Parse(args); err != nil || fs.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: benchcmp [-threshold 0.2] old.txt new.txt")
		return 2
	}
	read := func(path string) (map[string][]sample, []string, bool) {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintln(stderr, "benchcmp:", err)
			return nil, nil, false
		}
		defer f.Close()
		m, order, err := parse(f)
		if err != nil {
			fmt.Fprintln(stderr, "benchcmp:", err)
			return nil, nil, false
		}
		return m, order, true
	}
	old, oldOrder, ok1 := read(fs.Arg(0))
	cur, order, ok2 := read(fs.Arg(1))
	if !ok1 || !ok2 {
		return 2
	}
	if len(cur) == 0 {
		fmt.Fprintln(stderr, "benchcmp: no benchmark results in", fs.Arg(1))
		return 2
	}
	fmt.Fprintf(stdout, "| Benchmark | Time (old → new) | Change | Memory | Allocations | |\n|---|---|---|---|---|---|\n")
	failed := 0
	for _, name := range order {
		o, ok := old[name]
		if !ok {
			n := cur[name]
			fmt.Fprintf(stdout, "| %s | new: %s | | %s | %.0f | new |\n", name, dur(median(field(n, func(s sample) float64 { return s.ns }))),
				bytes(median(field(n, func(s sample) float64 { return s.bytes }))), median(field(n, func(s sample) float64 { return s.allocs })))
			continue
		}
		v := compare(name, o, cur[name], *threshold)
		mark := ""
		switch {
		case v.allocsWorse && v.timeWorse:
			mark = "❌ slower, more allocations"
		case v.allocsWorse:
			mark = "❌ more allocations"
		case v.timeWorse:
			mark = "❌ slower"
		}
		if mark != "" {
			failed++
		}
		fmt.Fprintf(stdout, "| %s | %s → %s | %+.1f%% | %s → %s | %.0f → %.0f | %s |\n", name, dur(v.oldNS), dur(v.newNS),
			100*(v.newNS/v.oldNS-1), bytes(v.oldB), bytes(v.newB), v.oldAllocs, v.newAllocs, mark)
	}
	for _, name := range oldOrder {
		if _, ok := cur[name]; !ok {
			fmt.Fprintf(stdout, "| %s | old: %s | | | | gone |\n", name, dur(median(field(old[name], func(s sample) float64 { return s.ns }))))
		}
	}
	if failed > 0 {
		fmt.Fprintf(stdout, "\n%d benchmark(s) regressed: more allocations, or more than %.0f%% slower in every sample.\n", failed, 100**threshold)
		return 1
	}
	fmt.Fprintln(stdout, "\nNo regression.")
	return 0
}

func dur(ns float64) string {
	switch {
	case ns >= 1e6:
		return fmt.Sprintf("%.2fms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.2fµs", ns/1e3)
	}
	return fmt.Sprintf("%.0fns", ns)
}

func bytes(b float64) string {
	if b >= 1024 {
		return fmt.Sprintf("%.1f KiB", b/1024)
	}
	return fmt.Sprintf("%.0f B", b)
}
