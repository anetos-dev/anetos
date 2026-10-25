// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"
)

func TestMerge(t *testing.T) {
	p := profile{}
	// The core's tests: the router covered, the db package not.
	core := `mode: set
anetos.dev/anetos/web/router.go:10.2,12.3 2 1
anetos.dev/anetos/db/query.go:5.1,6.2 3 0
anetos.dev/anetos/internal/cmd/apisnap/main.go:1.1,2.2 4 0
anetos.dev/anetos/examples/notes/main.go:1.1,2.2 1 0
`
	// A driver's tests: the db package covered, with count mode.
	driver := `mode: count
anetos.dev/anetos/db/query.go:5.1,6.2 3 7
anetos.dev/anetos/drivers/sqlite/sqlite.go:3.1,4.2 1 0
modernc.org/sqlite/lib.go:1.1,2.2 5 1
`
	for _, s := range []string{core, driver} {
		if err := p.read(strings.NewReader(s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.check(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	p.write(&out)
	want := `mode: set
db/query.go:5.1,6.2 3 1
drivers/sqlite/sqlite.go:3.1,4.2 1 0
web/router.go:10.2,12.3 2 1
`
	if out.String() != want {
		t.Errorf("merged profile:\n%s\nwant:\n%s", out.String(), want)
	}
	var sum strings.Builder
	p.summary(&sum, true)
	for _, line := range []string{
		"100.0%       3 statements  db",
		"  0.0%       1 statements  drivers/sqlite",
		"total: 83.3% of 6 statements",
	} {
		if !strings.Contains(sum.String(), line) {
			t.Errorf("summary lacks %q:\n%s", line, sum.String())
		}
	}
}

func TestReadErrors(t *testing.T) {
	for _, s := range []string{
		"not a profile\n",
		"mode: set\nweb/router.go 2 1\n",
		"mode: set\nanetos.dev/anetos/web/router.go:1.1,2.2 two 1\n",
		"mode: set\nanetos.dev/anetos/web/router.go:1.1,2.2 2\n",
	} {
		if err := (profile{}).read(strings.NewReader(s)); err == nil {
			t.Errorf("read(%q) = nil, want an error", s)
		}
	}
}

func TestCheck(t *testing.T) {
	if err := (profile{}).check(); err == nil {
		t.Error("check of an empty profile = nil, want an error")
	}
	// The same file instrumented from two copies of the source.
	p := profile{}
	for _, s := range []string{
		"mode: set\nanetos.dev/anetos/db/query.go:5.1,9.2 3 1\n",
		"mode: set\nanetos.dev/anetos/db/query.go:7.1,8.2 2 0\n",
	} {
		if err := p.read(strings.NewReader(s)); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.check(); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Errorf("check = %v, want blocks overlapping", err)
	}
	// Blocks that touch (one ends where the next starts) don't overlap.
	p = profile{}
	if err := p.read(strings.NewReader("mode: set\nanetos.dev/anetos/db/query.go:5.1,7.2 3 1\nanetos.dev/anetos/db/query.go:7.2,8.2 2 0\n")); err != nil {
		t.Fatal(err)
	}
	if err := p.check(); err != nil {
		t.Errorf("check = %v, want nil", err)
	}
}
