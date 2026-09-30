// SPDX-License-Identifier: Apache-2.0

// Command doccheck lists exported identifiers of the public packages that
// have no doc comment: declarations, methods, struct fields and interface
// methods (the v0.1 exit criterion). It skips main and internal packages,
// examples, test and generated files. Run it with make api-docs.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	fset := token.NewFileSet()
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "examples" || name == "testdata" || (strings.Contains(p, "/internal/") && name == "fixture") || name == "templates" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if ast.IsGenerated(f) || f.Name.Name == "main" || strings.Contains("/"+filepath.ToSlash(p), "/internal/") {
			return nil
		}
		report := func(pos token.Pos, what string) {
			n++
			fmt.Printf("%s: %s\n", fset.Position(pos), what)
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if !d.Name.IsExported() {
					continue
				}
				if d.Recv != nil {
					t := d.Recv.List[0].Type
					if s, ok := t.(*ast.StarExpr); ok {
						t = s.X
					}
					if ix, ok := t.(*ast.IndexExpr); ok {
						t = ix.X
					}
					if ix, ok := t.(*ast.IndexListExpr); ok {
						t = ix.X
					}
					if id, ok := t.(*ast.Ident); ok && !id.IsExported() {
						continue
					}
				}
				if d.Doc == nil {
					report(d.Pos(), "func "+d.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if !s.Name.IsExported() {
							continue
						}
						if s.Doc == nil && d.Doc == nil {
							report(s.Pos(), "type "+s.Name.Name)
						}
						switch t := s.Type.(type) {
						case *ast.StructType:
							for _, fl := range t.Fields.List {
								for _, nm := range fl.Names {
									if nm.IsExported() && fl.Doc == nil && fl.Comment == nil {
										report(nm.Pos(), "field "+s.Name.Name+"."+nm.Name)
									}
								}
							}
						case *ast.InterfaceType:
							for _, fl := range t.Methods.List {
								for _, nm := range fl.Names {
									if nm.IsExported() && fl.Doc == nil && fl.Comment == nil {
										report(nm.Pos(), "method "+s.Name.Name+"."+nm.Name)
									}
								}
							}
						}
					case *ast.ValueSpec:
						for _, nm := range s.Names {
							if nm.IsExported() && s.Doc == nil && s.Comment == nil && d.Doc == nil {
								report(nm.Pos(), "value "+nm.Name)
							}
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "doccheck:", err)
		os.Exit(2)
	}
	if n > 0 {
		fmt.Printf("doccheck: %d exported identifiers without a doc comment\n", n)
		os.Exit(1)
	}
	fmt.Println("doccheck: every exported identifier has a doc comment")
}
