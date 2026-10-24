// SPDX-License-Identifier: Apache-2.0

package modelgen

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"path/filepath"
	"sort"

	"golang.org/x/tools/go/packages"

	"anetos.dev/anetos/internal/naming"
)

// ModelInfo describes a model, as anetos model:list prints it.
type ModelInfo struct {
	// Package is the model's package path; Name its type's name.
	Package string `json:"package"`
	Name    string `json:"name"`
	// Table is its table: its TableName method's (its own, or an
	// embedded struct's), else the plural of its name in snake case.
	// TableMethod says a TableName method gives it; Table is then "" when
	// the method does more than return a constant, or is another
	// package's.
	Table       string         `json:"table"`
	TableMethod bool           `json:"table_method,omitempty"`
	File        string         `json:"file"` // relative to the directory listed
	Columns     []ColumnInfo   `json:"columns"`
	Relations   []RelationInfo `json:"relations"`
}

// ColumnInfo is a column of a [ModelInfo].
type ColumnInfo struct {
	Name  string `json:"name"`  // the column's: "author_id"
	Field string `json:"field"` // the typed column's in <Model>Cols: "AuthorID"
	Type  string `json:"type"`  // the Go type: "int64", "*time.Time"
	JSON  bool   `json:"json,omitempty"`
}

// RelationInfo is a relation of a [ModelInfo] (a field with a rel tag).
type RelationInfo struct {
	Field string `json:"field"` // in <Model>Rels too
	Kind  string `json:"kind"`  // belongs_to, has_one, has_many, many_to_many
	Model string `json:"model"` // the related model's type
}

// List finds the models of the packages matching patterns (relative to
// dir), in the files' order.
func List(dir string, patterns ...string) ([]ModelInfo, error) {
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	roots, overlay, err := candidates(dir, patterns)
	if err != nil || len(roots) == 0 {
		return []ModelInfo{}, err
	}
	cfg := &packages.Config{
		Mode:    packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:     dir,
		Overlay: overlay,
	}
	pkgs, err := packages.Load(cfg, roots...)
	if err != nil {
		return nil, fmt.Errorf("anetos model:list: %w", err)
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].PkgPath < pkgs[j].PkgPath })
	out := []ModelInfo{}
	var errs []error
	for _, pkg := range pkgs {
		if pkg.Types == nil || len(pkg.GoFiles) == 0 {
			continue
		}
		gen := filepath.Join(filepath.Dir(pkg.GoFiles[0]), FileName)
		models, err := findModels(pkg, gen)
		if err != nil {
			errs = append(errs, err)
		}
		// Types are named as code in the package writes them: Status,
		// sql.NullString, anetos.Date.
		self := pkg.Types
		qual := func(p *types.Package) string {
			if p == self {
				return ""
			}
			return p.Name()
		}
		for _, m := range models {
			obj := pkg.Types.Scope().Lookup(m.name)
			info := ModelInfo{Package: pkg.PkgPath, Name: m.name, Columns: []ColumnInfo{}, Relations: []RelationInfo{}}
			if obj != nil {
				if rel, err := filepath.Rel(dir, pkg.Fset.Position(obj.Pos()).Filename); err == nil {
					info.File = filepath.ToSlash(rel)
				}
			}
			if t, ok := tableMethod(pkg, obj); obj != nil && ok {
				info.Table, info.TableMethod = t, true
			} else {
				info.Table = naming.Plural(naming.Snake(m.name))
			}
			for _, c := range m.cols {
				info.Columns = append(info.Columns, ColumnInfo{Name: c.name, Field: c.goName, Type: types.TypeString(c.typ, qual), JSON: c.json})
			}
			for _, r := range m.rels {
				info.Relations = append(info.Relations, RelationInfo{Field: r.goName, Kind: r.kind, Model: types.TypeString(r.related, qual)})
			}
			out = append(out, info)
		}
	}
	return out, errors.Join(errs...)
}

// tableMethod finds the model's TableName method, its own or promoted
// from an embedded struct: ok, with the string it returns when it
// returns only a constant (a literal or a const).
func tableMethod(pkg *packages.Package, obj types.Object) (table string, ok bool) {
	sel := types.NewMethodSet(types.NewPointer(obj.Type())).Lookup(nil, "TableName")
	if sel == nil {
		return "", false
	}
	fn := sel.Obj()
	for _, f := range pkg.Syntax {
		for _, d := range f.Decls {
			fd, isFunc := d.(*ast.FuncDecl)
			if !isFunc || fd.Name.Pos() != fn.Pos() || fd.Body == nil || len(fd.Body.List) != 1 {
				continue
			}
			if ret, isRet := fd.Body.List[0].(*ast.ReturnStmt); isRet && len(ret.Results) == 1 && pkg.TypesInfo != nil {
				if tv, has := pkg.TypesInfo.Types[ret.Results[0]]; has && tv.Value != nil && tv.Value.Kind() == constant.String {
					return constant.StringVal(tv.Value), true
				}
			}
		}
	}
	return "", true
}
