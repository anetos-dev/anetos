// SPDX-License-Identifier: Apache-2.0

// Command covermerge merges the coverage profiles of the repository's
// modules into one, for make cover and CI's coverage job (design D332).
//
// Each published module's tests write a profile that counts the code of
// every module they run (go test -coverpkg), so the drivers' tests count
// the core's db packages, which the core's own tests can't run. A block
// counts as covered when any profile covered it. The merged profile's
// paths are relative to the repository (anetos.dev/anetos/web/router.go
// is web/router.go, anetos.dev/anetos/cli/… is cli/…), as Codecov reads
// them; the repository's own tooling (internal/cmd, cli/internal/cmd,
// the release tool's cli/internal/release) and the core module's doc
// examples (examples/) are left out. It fails when two profiles disagree
// on a file's blocks (a module built against another copy of the core,
// say with GOWORK=off) or when nothing is left to report.
//
//	go run ./internal/cmd/covermerge -o coverage.out a.out b.out…
//
// It prints the total and, with -v, each package's share.
package main

import (
	"bufio"
	"cmp"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

// module is the import path the repository's paths are relative to.
const module = "anetos.dev/anetos"

// excluded are the repository paths left out of the total.
var excluded = []string{"internal/cmd/", "cli/internal/cmd/", "cli/internal/release/", "examples/"}

func main() {
	out := flag.String("o", "coverage.out", "the merged profile to write")
	verbose := flag.Bool("v", false, "print each package's coverage")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: covermerge [-o file] [-v] profile...")
		os.Exit(2)
	}
	p := profile{}
	for _, name := range flag.Args() {
		if err := p.readFile(name); err != nil {
			fmt.Fprintln(os.Stderr, "covermerge:", err)
			os.Exit(1)
		}
	}
	if err := p.check(); err != nil {
		fmt.Fprintln(os.Stderr, "covermerge:", err)
		os.Exit(1)
	}
	if err := p.writeFile(*out); err != nil {
		fmt.Fprintln(os.Stderr, "covermerge:", err)
		os.Exit(1)
	}
	p.summary(os.Stdout, *verbose)
}

// block is a profile's line without its count: "file:start,end statements".
type block struct {
	file, pos  string
	statements int
}

// profile is the merged profile: each block, and whether a test ran it.
type profile map[block]bool

func (p profile) readFile(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := p.read(f); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

// read adds a profile (go test -coverprofile's format) to p.
func (p profile) read(r io.Reader) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; s.Scan(); n++ {
		line := s.Text()
		if n == 1 {
			if !strings.HasPrefix(line, "mode: ") {
				return errors.New("not a coverage profile")
			}
			continue
		}
		if line == "" {
			continue
		}
		b, count, err := parse(line)
		if err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
		file, ok := relative(b.file)
		if !ok {
			continue
		}
		b.file = file
		p[b] = p[b] || count > 0
	}
	return s.Err()
}

// parse reads "anetos.dev/anetos/web/router.go:10.2,12.3 2 1".
func parse(line string) (block, int, error) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return block{}, 0, fmt.Errorf("want 3 fields: %q", line)
	}
	file, pos, ok := strings.Cut(fields[0], ":")
	if !ok {
		return block{}, 0, fmt.Errorf("no position: %q", line)
	}
	statements, err := strconv.Atoi(fields[1])
	if err != nil {
		return block{}, 0, err
	}
	count, err := strconv.Atoi(fields[2])
	if err != nil {
		return block{}, 0, err
	}
	return block{file, pos, statements}, count, nil
}

// relative is file's path in the repository, and false for files outside
// it or left out.
func relative(file string) (string, bool) {
	rel, ok := strings.CutPrefix(file, module+"/")
	if !ok {
		return "", false
	}
	for _, e := range excluded {
		if strings.HasPrefix(rel, e) {
			return "", false
		}
	}
	return rel, true
}

// check fails when p is empty, or when two of a file's blocks overlap:
// the profiles were made from different source.
func (p profile) check() error {
	if len(p) == 0 {
		return errors.New("no coverage blocks in the profiles")
	}
	type span struct{ start, end [2]int }
	files := map[string][]span{}
	for b := range p {
		s, err := parseSpan(b.pos)
		if err != nil {
			return fmt.Errorf("%s: %w", b.file, err)
		}
		files[b.file] = append(files[b.file], span{s[0], s[1]})
	}
	before := func(a, b [2]int) bool { return a[0] < b[0] || a[0] == b[0] && a[1] < b[1] }
	for file, spans := range files {
		slices.SortFunc(spans, func(a, b span) int {
			if before(a.start, b.start) {
				return -1
			}
			if before(b.start, a.start) {
				return 1
			}
			return 0
		})
		for i := 1; i < len(spans); i++ {
			if before(spans[i].start, spans[i-1].end) {
				return fmt.Errorf("%s: blocks overlap at line %d: the profiles come from different source", file, spans[i].start[0])
			}
		}
	}
	return nil
}

// parseSpan reads a block's position, "10.2,12.3", as its start and end
// (line, column).
func parseSpan(pos string) ([2][2]int, error) {
	var s [2][2]int
	from, to, ok := strings.Cut(pos, ",")
	if !ok {
		return s, fmt.Errorf("bad position %q", pos)
	}
	for i, part := range []string{from, to} {
		line, col, ok := strings.Cut(part, ".")
		if !ok {
			return s, fmt.Errorf("bad position %q", pos)
		}
		var err error
		if s[i][0], err = strconv.Atoi(line); err != nil {
			return s, fmt.Errorf("bad position %q", pos)
		}
		if s[i][1], err = strconv.Atoi(col); err != nil {
			return s, fmt.Errorf("bad position %q", pos)
		}
	}
	return s, nil
}

func (p profile) writeFile(name string) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	p.write(w)
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// write writes p in set mode, sorted.
func (p profile) write(w io.Writer) {
	blocks := make([]block, 0, len(p))
	for b := range p {
		blocks = append(blocks, b)
	}
	slices.SortFunc(blocks, func(a, b block) int {
		return cmp.Or(cmp.Compare(a.file, b.file), cmp.Compare(a.pos, b.pos))
	})
	fmt.Fprintln(w, "mode: set")
	for _, b := range blocks {
		covered := 0
		if p[b] {
			covered = 1
		}
		fmt.Fprintf(w, "%s:%s %d %d\n", b.file, b.pos, b.statements, covered)
	}
}

// summary prints the total and, with all, each package's coverage.
func (p profile) summary(w io.Writer, all bool) {
	type share struct{ covered, total int }
	pkgs := map[string]*share{}
	var sum share
	for b, covered := range p {
		dir := path.Dir(b.file)
		s := pkgs[dir]
		if s == nil {
			s = &share{}
			pkgs[dir] = s
		}
		s.total += b.statements
		sum.total += b.statements
		if covered {
			s.covered += b.statements
			sum.covered += b.statements
		}
	}
	percent := func(s share) float64 {
		if s.total == 0 {
			return 0
		}
		return 100 * float64(s.covered) / float64(s.total)
	}
	if all {
		dirs := make([]string, 0, len(pkgs))
		for d := range pkgs {
			dirs = append(dirs, d)
		}
		slices.Sort(dirs)
		for _, d := range dirs {
			fmt.Fprintf(w, "%5.1f%%  %6d statements  %s\n", percent(*pkgs[d]), pkgs[d].total, d)
		}
	}
	fmt.Fprintf(w, "total: %.1f%% of %d statements\n", percent(sum), sum.total)
}
