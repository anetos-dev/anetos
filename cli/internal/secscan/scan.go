// SPDX-License-Identifier: Apache-2.0

package secscan

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"
)

// Options says what Scan checks.
type Options struct {
	// Dir is the project's folder (its go.mod's).
	Dir string
	// Level is basic or advanced.
	Level Level
	// Vuln runs govulncheck.
	Vuln bool
}

// Report is what Scan found, as the app's --findings file has it.
type Report struct {
	Rules    []Rule    `json:"rules"`
	Findings []Finding `json:"findings"`
}

// Scan runs the source rules, the search for secrets and (with
// opts.Vuln) govulncheck on the project. A project that doesn't build is
// an error.
func Scan(ctx context.Context, opts Options) (Report, error) {
	dir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Rules: ruleList(opts.Level), Findings: []Finding{}}
	code, err := source(ctx, dir)
	if err != nil {
		return Report{}, err
	}
	rep.Findings = append(rep.Findings, code...)
	sec, err := secrets(dir)
	if err != nil {
		return Report{}, err
	}
	rep.Findings = append(rep.Findings, sec...)
	if opts.Vuln {
		rep.Findings = append(rep.Findings, vulns(ctx, dir)...)
	}
	rep.Findings = slices.DeleteFunc(rep.Findings, func(f Finding) bool { return rules[f.Rule].level > opts.Level })
	slices.SortStableFunc(rep.Findings, func(a, b Finding) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Rule, b.Rule))
	})
	return rep, nil
}

// source runs the source rules on the project's packages (not their
// tests).
func source(ctx context.Context, dir string) ([]Finding, error) {
	cfg := &packages.Config{
		Context: ctx,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
			packages.NeedTypes | packages.NeedTypesSizes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("the project's packages don't load: %w", err)
	}
	var errs []error
	for _, p := range pkgs {
		for _, e := range p.Errors {
			errs = append(errs, errors.New(e.Error()))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("the project doesn't build: %w", errors.Join(errs...))
	}
	graph, err := checker.Analyze([]*analysis.Analyzer{Analyzer}, pkgs, nil)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, act := range graph.Roots {
		if act.Err != nil {
			return nil, fmt.Errorf("%s: %w", act.Package.PkgPath, act.Err)
		}
		found, _ := act.Result.([]found)
		label := pkgLabel(act.Package)
		for _, f := range found {
			where := label
			if f.where != "" {
				where += "." + f.where
			}
			pos := act.Package.Fset.Position(f.pos)
			end := act.Package.Fset.Position(f.end)
			file, line := pos.Filename, pos.Line
			if strings.HasSuffix(file, "_templ.go") {
				file, line = templLine(file, pos.Offset, end.Offset, line)
			}
			if rel, err := filepath.Rel(dir, file); err == nil && !strings.HasPrefix(rel, "..") {
				file = filepath.ToSlash(rel)
			}
			out = append(out, Finding{Rule: f.rule, Severity: f.severity, Where: where, File: file, Line: line, Message: f.message})
		}
	}
	return out, nil
}

// pkgLabel names a package in reports: its path in the module
// (app/handlers), or its name at the module's root (main).
func pkgLabel(p *packages.Package) string {
	if p.Module != nil {
		if rel, ok := strings.CutPrefix(p.PkgPath, p.Module.Path+"/"); ok {
			return rel
		}
	}
	return p.Name
}

// templErrorLine is the .templ line templ writes in generated code
// (templ.Error{…, Line: 35, …}).
var templErrorLine = regexp.MustCompile(`templ\.Error\{.*\bLine: (\d+)`)

// templLine maps a position in a _templ.go file to its .templ file: the
// line, at or after the last one templ recorded before it, that has the
// finding's code (templ.Raw(p.QR)); else that recorded line.
func templLine(gen string, start, end, genLine int) (string, int) {
	src := strings.TrimSuffix(gen, "_templ.go") + ".templ"
	g, err1 := os.ReadFile(gen)
	t, err2 := os.ReadFile(src)
	if err1 != nil || err2 != nil || start < 0 || end > len(g) || start > end {
		return gen, genLine
	}
	after := 1
	for _, m := range templErrorLine.FindAllSubmatchIndex(g[:start], -1) {
		if n, err := strconv.Atoi(string(g[m[2]:m[3]])); err == nil {
			after = n
		}
	}
	code := strings.TrimSpace(string(g[start:end]))
	lines := strings.Split(string(t), "\n")
	first := 0
	for i, l := range lines {
		if code == "" || !strings.Contains(l, code) {
			continue
		}
		if first == 0 {
			first = i + 1
		}
		if i+1 >= after {
			return src, i + 1
		}
	}
	if first > 0 {
		return src, first
	}
	return src, after
}
