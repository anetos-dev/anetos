// SPDX-License-Identifier: Apache-2.0

// Command apisnap writes the exported API of each library module of the
// repository to api/<module>.txt, one line per identifier, sorted, in the
// style of Go's own api files:
//
//	pkg anetos.dev/anetos/web, func MustURL(context.Context, string, ...any) string
//	pkg anetos.dev/anetos/web, method (*Router) Get(string, HandlerFunc) *Route
//	pkg anetos.dev/anetos/web, const MaxMultipartMemory = 32 << 20
//
// Functions and methods have their parameter and result types; types
// their exported fields, embedded types and interface methods;
// constants their type and value (iota counted); variables their type,
// which must be written or plain from the value (a composite literal,
// errors.New…), else apisnap fails. Parameter names, struct tags and doc
// comments aren't part of the lines, and the layout of the source
// doesn't change them.
//
// With -check it writes nothing and fails when a file differs from the
// source, printing the lines added (+) and removed (-): an API change
// shows in review as a change of those files (design D308). It reads the
// source only, without type checking: a type's package is the name the
// file imports it as, and the exported members of an unexported
// embedded type aren't listed (its embedding is). It skips main,
// internal and testdata packages, test files, directories starting with
// "." or "_", files built only with the ignore tag, and the examples,
// bench and cli modules. Run it with make api-update and make api-check.
package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

func main() {
	check := flag.Bool("check", false, "compare api/*.txt with the source instead of writing them")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	apis, err := collect(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "apisnap:", err)
		os.Exit(2)
	}
	dir := filepath.Join(root, "api")
	if *check {
		if !compare(dir, apis) {
			fmt.Println("apisnap: the exported API changed (lines above). If that's intended, run make api-update and commit api/; a breaking change also needs the CHANGELOG and the upgrade guide")
			os.Exit(1)
		}
		fmt.Printf("apisnap: api/ matches the exported API of %d modules\n", len(apis))
		return
	}
	if err := write(dir, apis); err != nil {
		fmt.Fprintln(os.Stderr, "apisnap:", err)
		os.Exit(2)
	}
	fmt.Printf("apisnap: wrote the exported API of %d modules to api/\n", len(apis))
}

// module is a library module: its path, and the API file's name.
type module struct {
	path, file string
	lines      []string
}

// collect reads the exported API of every module under root that has
// one, keyed by the API file's name.
func collect(root string) (map[string]*module, error) {
	mods := map[string]*module{} // by directory, relative to root
	apis := map[string]*module{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			name := d.Name()
			if rel != "." && (name == "internal" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir // as the go command does, or not API
			}
			switch rel {
			case "examples", "bench", "cli", "api", "tmp":
				return filepath.SkipDir // not library modules
			}
			gomod := filepath.Join(p, "go.mod")
			if b, err := os.ReadFile(gomod); err == nil {
				mp := modulePath(b)
				if mp == "" {
					return fmt.Errorf("%s: no module line", gomod)
				}
				name := "anetos"
				if rel != "." {
					name = strings.ReplaceAll(rel, "/", "-")
				}
				mods[rel] = &module{path: mp, file: name + ".txt"}
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if f.Name.Name == "main" || ignored(f) {
			return nil
		}
		dir := path.Dir(rel)
		m, modDir := owner(mods, dir)
		if m == nil {
			return fmt.Errorf("%s: no go.mod above it", p)
		}
		pkg := m.path
		if sub := strings.TrimPrefix(strings.TrimPrefix(dir, modDir), "/"); sub != "" && sub != "." {
			pkg += "/" + sub
		}
		lines, err := declLines(fset, "pkg "+pkg+", ", f)
		if err != nil {
			return err
		}
		m.lines = append(m.lines, lines...)
		apis[m.file] = m
		return nil
	})
	for _, m := range apis {
		slices.Sort(m.lines)
		m.lines = slices.Compact(m.lines)
	}
	return apis, err
}

// owner finds the module of a directory: the nearest one above it.
func owner(mods map[string]*module, dir string) (*module, string) {
	for d := dir; ; d = path.Dir(d) {
		if m, ok := mods[d]; ok {
			return m, d
		}
		if d == "." || d == "/" {
			return nil, ""
		}
	}
}

func modulePath(gomod []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(gomod))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`)
		}
	}
	return ""
}

// ignored reports a file built only with the ignore tag
// (//go:build ignore).
func ignored(f *ast.File) bool {
	for _, g := range f.Comments {
		if g.Pos() > f.Package {
			break
		}
		for _, c := range g.List {
			if !constraint.IsGoBuild(c.Text) {
				continue
			}
			if x, err := constraint.Parse(c.Text); err == nil {
				if t, ok := x.(*constraint.TagExpr); ok && t.Tag == "ignore" {
					return true
				}
			}
		}
	}
	return false
}

// declLines returns the lines of a file's exported declarations.
func declLines(fset *token.FileSet, prefix string, f *ast.File) ([]string, error) {
	var out []string
	add := func(s string) { out = append(out, prefix+s) }
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			if d.Recv == nil {
				add("func " + d.Name.Name + typeParams(fset, d.Type.TypeParams) + signature(fset, d.Type))
				continue
			}
			recv := d.Recv.List[0].Type
			if !exportedRecv(recv) {
				continue
			}
			add("method (" + node(fset, recv) + ") " + d.Name.Name + signature(fset, d.Type))
		case *ast.GenDecl:
			// A const group repeats the last type and values given,
			// with iota counting the specs.
			var lastType ast.Expr
			var lastValues []ast.Expr
			for iota, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.IsExported() {
						out = append(out, typeLines(fset, prefix, s)...)
					}
				case *ast.ValueSpec:
					if d.Tok == token.CONST && (s.Type != nil || len(s.Values) > 0) {
						lastType, lastValues = s.Type, s.Values
					}
					for i, nm := range s.Names {
						if !nm.IsExported() {
							continue
						}
						if d.Tok == token.CONST {
							line := "const " + nm.Name
							if lastType != nil {
								line += " " + node(fset, lastType)
							}
							if i < len(lastValues) {
								line += " = " + iotaRE.ReplaceAllString(node(fset, lastValues[i]), strconv.Itoa(iota))
							}
							add(line)
							continue
						}
						typ := ""
						if s.Type != nil {
							typ = node(fset, s.Type)
						} else if i < len(s.Values) {
							typ = valueType(fset, s.Values[i])
						}
						if typ == "" {
							return nil, fmt.Errorf("%s: give var %s an explicit type, which apisnap can't tell from its value", fset.Position(nm.Pos()), nm.Name)
						}
						add("var " + nm.Name + " " + typ)
					}
				}
			}
		}
	}
	return out, nil
}

var iotaRE = regexp.MustCompile(`\biota\b`)

// valueType tells a variable's type from its value where the syntax
// alone says it: a composite literal, its address, a function literal,
// errors.New or fmt.Errorf, a basic literal. Else it returns "".
func valueType(fset *token.FileSet, v ast.Expr) string {
	switch x := v.(type) {
	case *ast.CompositeLit:
		if x.Type != nil {
			return node(fset, x.Type)
		}
	case *ast.UnaryExpr:
		if c, ok := x.X.(*ast.CompositeLit); ok && x.Op == token.AND && c.Type != nil {
			return "*" + node(fset, c.Type)
		}
	case *ast.FuncLit:
		return node(fset, x.Type)
	case *ast.CallExpr:
		switch node(fset, x.Fun) {
		case "errors.New", "fmt.Errorf":
			return "error"
		}
	case *ast.BasicLit:
		return map[token.Token]string{token.INT: "int", token.FLOAT: "float64", token.IMAG: "complex128", token.CHAR: "rune", token.STRING: "string"}[x.Kind]
	case *ast.Ident:
		if x.Name == "true" || x.Name == "false" {
			return "bool"
		}
	}
	return ""
}

// typeLines returns a type's line and those of its exported fields or
// methods.
func typeLines(fset *token.FileSet, prefix string, s *ast.TypeSpec) []string {
	head := prefix + "type " + s.Name.Name + typeParams(fset, s.TypeParams)
	if s.Assign.IsValid() {
		return []string{head + " = " + node(fset, s.Type)}
	}
	switch t := s.Type.(type) {
	case *ast.StructType:
		out := []string{head + " struct"}
		for _, fl := range t.Fields.List {
			if len(fl.Names) == 0 {
				// An unexported embedded type's exported fields and
				// methods are promoted: its line at least shows it.
				out = append(out, head+" struct, embedded "+node(fset, fl.Type))
				continue
			}
			for _, nm := range fl.Names {
				if nm.IsExported() {
					out = append(out, head+" struct, "+nm.Name+" "+node(fset, fl.Type))
				}
			}
		}
		return out
	case *ast.InterfaceType:
		out := []string{head + " interface"}
		for _, fl := range t.Methods.List {
			if len(fl.Names) == 0 {
				out = append(out, head+" interface, embedded "+node(fset, fl.Type))
				continue
			}
			for _, nm := range fl.Names {
				if !nm.IsExported() {
					out = append(out, head+" interface, unexported methods")
					continue
				}
				out = append(out, head+" interface, "+nm.Name+signature(fset, fl.Type.(*ast.FuncType)))
			}
		}
		return out
	}
	return []string{head + " " + node(fset, s.Type)}
}

// exportedRecv reports whether a receiver or embedded type is an
// exported type (or a pointer to, or instance of, one).
func exportedRecv(t ast.Expr) bool {
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
		case *ast.IndexExpr:
			t = x.X
		case *ast.IndexListExpr:
			t = x.X
		case *ast.SelectorExpr:
			return x.Sel.IsExported()
		case *ast.Ident:
			return x.IsExported()
		default:
			return false
		}
	}
}

// signature prints a function type's parameters and results without
// their names: "(string, ...any) error".
func signature(fset *token.FileSet, ft *ast.FuncType) string {
	params := fieldTypes(fset, ft.Params)
	s := "(" + strings.Join(params, ", ") + ")"
	if ft.Results == nil {
		return s
	}
	res := fieldTypes(fset, ft.Results)
	if len(res) == 1 {
		return s + " " + res[0]
	}
	return s + " (" + strings.Join(res, ", ") + ")"
}

func fieldTypes(fset *token.FileSet, fl *ast.FieldList) []string {
	var out []string
	if fl == nil {
		return out
	}
	for _, f := range fl.List {
		t := node(fset, f.Type)
		n := max(len(f.Names), 1)
		for range n {
			out = append(out, t)
		}
	}
	return out
}

func typeParams(fset *token.FileSet, fl *ast.FieldList) string {
	if fl == nil || len(fl.List) == 0 {
		return ""
	}
	var ps []string
	for _, f := range fl.List {
		var names []string
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
		ps = append(ps, strings.Join(names, ", ")+" "+node(fset, f.Type))
	}
	return "[" + strings.Join(ps, ", ") + "]"
}

// node prints an expression on one line, the same whatever its layout
// in the source, without comments, tags or parameter names.
func node(_ *token.FileSet, x ast.Expr) string {
	return types.ExprString(stripComments(x).(ast.Expr))
}

// stripComments drops the comments and tags of the fields of struct and
// interface types written inline, which the printer would otherwise
// keep, and the parameter names of function types.
func stripComments(n ast.Node) ast.Node {
	ast.Inspect(n, func(x ast.Node) bool {
		switch x := x.(type) {
		case *ast.Field:
			x.Doc, x.Comment, x.Tag = nil, nil, nil
		case *ast.FuncType:
			unname(x.Params)
			unname(x.Results)
		}
		return true
	})
	return n
}

// unname replaces the fields of a parameter list by one unnamed field
// per name.
func unname(fl *ast.FieldList) {
	if fl == nil {
		return
	}
	var list []*ast.Field
	for _, f := range fl.List {
		for range max(len(f.Names), 1) {
			list = append(list, &ast.Field{Type: f.Type})
		}
	}
	fl.List = list
}

func header(m *module) string {
	return "# The exported API of " + m.path + ": make api-update writes it, make api-check (CI) compares it with the source.\n"
}

func write(dir string, apis map[string]*module) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	for _, m := range apis {
		content := header(m) + strings.Join(m.lines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, m.file), []byte(content), 0o644); err != nil {
			return err
		}
	}
	var stale []string
	for _, o := range old {
		if apis[filepath.Base(o)] == nil {
			stale = append(stale, o)
		}
	}
	if len(stale) > 0 {
		return errors.New("no module has the API of " + strings.Join(stale, ", ") + ": remove it")
	}
	return nil
}

// compare prints the differences between api/ and the source, and
// reports whether there were none.
func compare(dir string, apis map[string]*module) bool {
	same := true
	names := make([]string, 0, len(apis))
	for n := range apis {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		m := apis[n]
		b, err := os.ReadFile(filepath.Join(dir, n))
		if err != nil {
			fmt.Printf("api/%s: missing (module %s)\n", n, m.path)
			same = false
			continue
		}
		var have []string
		for l := range strings.SplitSeq(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
			if l != "" && !strings.HasPrefix(l, "#") {
				have = append(have, l)
			}
		}
		slices.Sort(have)
		for _, l := range m.lines {
			if _, found := slices.BinarySearch(have, l); !found {
				fmt.Printf("api/%s: + %s\n", n, l)
				same = false
			}
		}
		for _, l := range have {
			if _, found := slices.BinarySearch(m.lines, l); !found {
				fmt.Printf("api/%s: - %s\n", n, l)
				same = false
			}
		}
	}
	old, _ := filepath.Glob(filepath.Join(dir, "*.txt"))
	for _, o := range old {
		if apis[filepath.Base(o)] == nil {
			fmt.Printf("api/%s: no module has this API: remove it\n", filepath.Base(o))
			same = false
		}
	}
	return same
}
