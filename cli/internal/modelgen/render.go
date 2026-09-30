// SPDX-License-Identifier: Apache-2.0

package modelgen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// imports assigns file-local names to the packages the generated file
// uses, avoiding every package-level name of the model's package.
type imports struct {
	self   string
	taken  map[string]bool
	byPath map[string]string
}

func (im *imports) qualifier(p *types.Package) string {
	if p.Path() == im.self {
		return ""
	}
	if name, ok := im.byPath[p.Path()]; ok {
		return name
	}
	name := p.Name()
	for i := 2; im.taken[name]; i++ {
		name = p.Name() + strconv.Itoa(i)
	}
	im.taken[name] = true
	im.byPath[p.Path()] = name
	return name
}

func (im *imports) block() string {
	var std, other []string
	for path, name := range im.byPath {
		spec := strconv.Quote(path)
		if name != lastElem(path) {
			spec = name + " " + spec
		}
		if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
			other = append(other, spec)
		} else {
			std = append(std, spec)
		}
	}
	slices.Sort(std)
	slices.Sort(other)
	var b strings.Builder
	b.WriteString("import (\n")
	for _, s := range std {
		b.WriteString("\t" + s + "\n")
	}
	if len(std) > 0 && len(other) > 0 {
		b.WriteString("\n")
	}
	for _, s := range other {
		b.WriteString("\t" + s + "\n")
	}
	b.WriteString(")\n")
	return b.String()
}

func lastElem(path string) string { return path[strings.LastIndexByte(path, '/')+1:] }

// render returns the formatted models_gen.go for pkg.
func render(pkg *packages.Package, models []model) ([]byte, error) {
	scope := pkg.Types.Scope()
	im := &imports{self: pkg.PkgPath, taken: map[string]bool{}, byPath: map[string]string{}}
	for _, name := range scope.Names() {
		im.taken[name] = true
	}
	// The package's own _test.go files, and files built only on other
	// platforms, share its scope.
	testNames, err := otherFileNames(pkg)
	if err != nil {
		return nil, err
	}
	for name := range testNames {
		im.taken[name] = true
	}
	for _, m := range models {
		for _, v := range []string{varName(m.name), relsName(m.name)} {
			if v == relsName(m.name) && len(m.rels) == 0 {
				continue
			}
			if obj := scope.Lookup(v); obj != nil {
				return nil, fmt.Errorf("%s: %s is already declared, so anetos gen can't declare it for %s; rename it, or mark %s //anetos:skip",
					pkg.Fset.Position(obj.Pos()), v, m.name, m.name)
			}
			if pos, ok := testNames[v]; ok {
				return nil, fmt.Errorf("%s: %s is already declared, so anetos gen can't declare it for %s; rename it, or mark %s //anetos:skip",
					pos, v, m.name, m.name)
			}
			im.taken[v] = true
		}
	}
	dbName := im.qualifier(types.NewPackage(dbPath, "db"))

	var body bytes.Buffer
	for _, m := range models {
		v := varName(m.name)
		fmt.Fprintf(&body, "\n// %s are the columns of [%s].\nvar %s = struct {\n", v, m.name, v)
		typs := make([]string, len(m.cols))
		for i, c := range m.cols {
			typs[i] = types.TypeString(c.typ, im.qualifier)
			fmt.Fprintf(&body, "\t%s %s.Column[%s]\n", c.goName, dbName, typs[i])
		}
		body.WriteString("}{\n")
		for i, c := range m.cols {
			ctor := "Col"
			if c.json {
				ctor = "JSONCol"
			}
			fmt.Fprintf(&body, "\t%s: %s.%s[%s](%s),\n", c.goName, dbName, ctor, typs[i], strconv.Quote(c.name))
		}
		body.WriteString("}\n")
		if len(m.rels) == 0 {
			continue
		}
		r := relsName(m.name)
		fmt.Fprintf(&body, "\n// %s are the relations of [%s], for With, Load and WhereHas.\nvar %s = struct {\n", r, m.name, r)
		rtyps := make([]string, len(m.rels))
		for i, rel := range m.rels {
			rtyps[i] = types.TypeString(rel.related, im.qualifier)
			fmt.Fprintf(&body, "\t%s %s.Rel[%s, %s]\n", rel.goName, dbName, m.name, rtyps[i])
		}
		body.WriteString("}{\n")
		for i, rel := range m.rels {
			fmt.Fprintf(&body, "\t%s: %s.RelOf[%s, %s](%s),\n", rel.goName, dbName, m.name, rtyps[i], strconv.Quote(rel.goName))
		}
		body.WriteString("}\n")
	}

	var out bytes.Buffer
	out.WriteString(Header + "\n\npackage " + pkg.Name + "\n\n")
	out.WriteString(im.block())
	out.Write(body.Bytes())
	src, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("anetos gen: %s: formatting generated code: %w", pkg.PkgPath, err)
	}
	return src, nil
}

// otherFileNames returns the package-level names declared by the files
// of the package that weren't type-checked: in-package _test.go files, and
// files excluded by build constraints. The generated file's imports must
// not clash with them either.
func otherFileNames(pkg *packages.Package) (map[string]string, error) {
	names := map[string]string{}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(pkg.GoFiles[0]), "*_test.go"))
	if err != nil {
		return nil, err
	}
	for _, f := range pkg.IgnoredFiles {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") && filepath.Base(f) != FileName {
			files = append(files, f)
		}
	}
	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil || f.Name.Name != pkg.Name {
			continue // a syntax error is go test's to report; package x_test has its own scope
		}
		add := func(id *ast.Ident) {
			if id.Name != "_" && id.Name != "init" {
				names[id.Name] = fset.Position(id.Pos()).String()
			}
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					add(d.Name)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						add(spec.Name)
					case *ast.ValueSpec:
						for _, n := range spec.Names {
							add(n)
						}
					}
				}
			}
		}
	}
	return names, nil
}
