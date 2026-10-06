// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func write(t *testing.T, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "b.txt")
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func lines(name string, ns []int, allocs int) string {
	var b strings.Builder
	for _, n := range ns {
		b.WriteString(name + "-2   \t  100000\t  " + itoa(n) + " ns/op\t  6000 B/op\t  " + itoa(allocs) + " allocs/op\n")
	}
	return b.String()
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestCompare(t *testing.T) {
	old := lines("BenchmarkHello", []int{1000, 1010, 990, 1005, 995}, 24) +
		lines("BenchmarkJSON", []int{2000, 2100, 1900, 2050, 1950}, 41) +
		lines("BenchmarkPage/Full", []int{5000, 5100, 4900, 5050, 4950}, 600) +
		"goos: linux\nPASS\n"
	tests := []struct {
		name, new string
		code      int
		want      []string
	}{
		{"same", old, 0, []string{"No regression.", "| Hello | 1.00µs → 1.00µs | +0.0% |"}},
		{"noise", lines("BenchmarkHello", []int{1300, 990, 1000, 1010, 1300}, 24), 0, []string{"No regression."}},
		{"slower", lines("BenchmarkHello", []int{1300, 1310, 1290, 1305, 1295}, 24), 1, []string{"❌ slower", "1 benchmark(s) regressed"}},
		{"a little slower", lines("BenchmarkHello", []int{1100, 1110, 1090, 1105, 1095}, 24), 0, []string{"No regression."}},
		{"allocations", lines("BenchmarkJSON", []int{2000, 2100, 1900, 2050, 1950}, 42), 1, []string{"❌ more allocations"}},
		{"faster", lines("BenchmarkPage/Full", []int{3000, 3100, 2900, 3050, 2950}, 500), 0, []string{"| Page/Full | 5.00µs → 3.00µs | -40.0% |"}},
		{"new benchmark", lines("BenchmarkNew", []int{100, 100, 100}, 1), 0, []string{"| New | new: 100ns |"}},
		{"gone", lines("BenchmarkHello", []int{1000, 1000, 1000}, 24), 0, []string{"| JSON | old: 2.00µs | | | | gone |"}},
		{"MB/s and no GOMAXPROCS suffix", "BenchmarkJSON 100 2000 ns/op 20.00 MB/s 6000 B/op 45 allocs/op\n" +
			"BenchmarkJSON 100 2000 ns/op 20.00 MB/s 6000 B/op 45 allocs/op\nBenchmarkJSON 100 2000 ns/op 20.00 MB/s 6000 B/op 45 allocs/op\n",
			1, []string{"| JSON | 2.00µs → 2.00µs | +0.0% | 5.9 KiB → 5.9 KiB | 41 → 45 | ❌ more allocations |"}},
	}
	for _, tt := range tests {
		var out, errOut strings.Builder
		code := run([]string{write(t, old), write(t, tt.new)}, &out, &errOut)
		if code != tt.code {
			t.Errorf("%s: exit %d, want %d\n%s%s", tt.name, code, tt.code, out.String(), errOut.String())
		}
		for _, w := range tt.want {
			if !strings.Contains(out.String(), w) {
				t.Errorf("%s: no %q in\n%s", tt.name, w, out.String())
			}
		}
	}
	var out, errOut strings.Builder
	if code := run([]string{write(t, old), write(t, "PASS\n")}, &out, &errOut); code != 2 {
		t.Errorf("no results: exit %d", code)
	}
	if code := run([]string{"one"}, &out, &errOut); code != 2 {
		t.Errorf("one argument: exit %d", code)
	}
}
