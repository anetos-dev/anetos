// SPDX-License-Identifier: Apache-2.0

package modelgen

import (
	"fmt"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"anetos.dev/anetos/internal/naming"
)

const dbPath = "anetos.dev/anetos/db"

// column is one mapped struct field, as the db package sees it.
type column struct {
	goName string // field name in the generated struct
	name   string // column name
	typ    types.Type
	json   bool
	pos    token.Pos
}

// walker collects a model's columns with the rules of the db package's
// buildMeta and addFields (db/model.go). Keep the two in step: the
// fixture test compares them.
type walker struct {
	fset  *token.FileSet
	model *types.TypeName
	self  *types.Package
	// constrained reports whether a position is in a file with build
	// constraints.
	constrained func(token.Pos) bool
	cols        []column
	byName      map[string]bool
	pk          bool
}

func (w *walker) errorf(pos token.Pos, format string, args ...any) error {
	if !pos.IsValid() {
		pos = w.model.Pos()
	}
	return fmt.Errorf("%s: %s: %s", w.fset.Position(pos), w.model.Name(), fmt.Sprintf(format, args...))
}

// columns returns the columns of the model's struct type st.
func (w *walker) columns(st *types.Struct) ([]column, error) {
	w.byName = map[string]bool{}
	if err := w.fields(st, w.model.Type(), map[types.Type]bool{}); err != nil {
		return nil, err
	}
	goNames := map[string]string{}
	for _, c := range w.cols {
		if prev, dup := goNames[c.goName]; dup {
			return nil, w.errorf(c.pos, "columns %q and %q both come from fields named %s; rename one of the fields", prev, c.name, c.goName)
		}
		goNames[c.goName] = c.name
		if err := w.nameable(c.typ, c.pos, c.goName); err != nil {
			return nil, err
		}
	}
	return w.cols, nil
}

func (w *walker) fields(st *types.Struct, t types.Type, seen map[types.Type]bool) error {
	if seen[t] {
		return w.errorf(token.NoPos, "embeds itself")
	}
	seen[t] = true
	defer delete(seen, t)
	for i := range st.NumFields() {
		f := st.Field(i)
		tag, hasTag := reflect.StructTag(st.Tag(i)).Lookup("db")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		ft := f.Type()
		base := ft
		if p, ok := under(base).(*types.Pointer); ok {
			base = p.Elem()
		}
		if f.Embedded() && name == "" {
			if bst, ok := under(base).(*types.Struct); ok && !isValueType(base) {
				if _, isPtr := under(ft).(*types.Pointer); isPtr && !f.Exported() {
					continue // the runtime can't allocate it while scanning
				}
				if n, ok := types.Unalias(base).(*types.Named); ok && n.Obj().Pkg() == w.self && w.constrained(n.Obj().Pos()) {
					return w.errorf(f.Pos(), "embeds %s, declared in a file with build constraints; models and the structs they embed must be in files without them", n.Obj().Name())
				}
				if err := w.fields(bst, types.Unalias(base), seen); err != nil {
					return err
				}
				continue
			}
		}
		if !f.Exported() {
			continue
		}
		if !hasTag && !isColumnType(ft) {
			continue // a relation or other non-column field
		}
		if name == "" {
			name = naming.Snake(f.Name())
		}
		if w.byName[name] {
			return w.errorf(f.Pos(), "two fields map to column %q", name)
		}
		c := column{goName: f.Name(), name: name, typ: ft, pos: f.Pos()}
		for o := range strings.SplitSeq(opts, ",") {
			switch strings.TrimSpace(o) {
			case "":
			case "pk":
				if w.pk {
					return w.errorf(f.Pos(), "more than one pk field")
				}
				w.pk = true
			case "json":
				c.json = true
			case "readonly":
			default:
				return w.errorf(f.Pos(), "field %s: unknown db tag option %q (known: pk, json, readonly)", f.Name(), o)
			}
		}
		w.byName[name] = true
		w.cols = append(w.cols, c)
	}
	return nil
}

// nameable reports an error if t can't be written in the model's package:
// an unexported type of another package, reached through an embedded
// struct.
func (w *walker) nameable(t types.Type, pos token.Pos, field string) error {
	var bad string
	var visit func(t types.Type, depth int)
	visit = func(t types.Type, depth int) {
		if bad != "" || depth > 20 {
			return
		}
		switch t := t.(type) {
		case *types.Alias:
			w.checkObj(t.Obj(), &bad)
			if args := t.TypeArgs(); args != nil {
				for a := range args.Types() {
					visit(a, depth+1)
				}
			}
		case *types.Named:
			w.checkObj(t.Obj(), &bad)
			if args := t.TypeArgs(); args != nil {
				for a := range args.Types() {
					visit(a, depth+1)
				}
			}
		case *types.Pointer:
			visit(t.Elem(), depth+1)
		case *types.Slice:
			visit(t.Elem(), depth+1)
		case *types.Array:
			visit(t.Elem(), depth+1)
		case *types.Map:
			visit(t.Key(), depth+1)
			visit(t.Elem(), depth+1)
		case *types.Chan:
			visit(t.Elem(), depth+1)
		case *types.Struct:
			for f := range t.Fields() {
				if !f.Exported() && f.Pkg() != nil && f.Pkg().Path() != w.self.Path() {
					// Written in another package, the struct type would differ.
					bad = "a struct type with unexported fields of package " + f.Pkg().Path()
					return
				}
				visit(f.Type(), depth+1)
			}
		case *types.Signature:
			for v := range t.Params().Variables() {
				visit(v.Type(), depth+1)
			}
			for v := range t.Results().Variables() {
				visit(v.Type(), depth+1)
			}
		}
	}
	visit(t, 0)
	if bad != "" {
		return w.errorf(pos, "field %s has %s, which package %s can't name; embed the struct differently or tag the field db:\"-\"", field, bad, w.self.Path())
	}
	return nil
}

// checkObj records why obj can't be named in the model's package: it is
// unexported, or in an internal package the model's package can't import.
func (w *walker) checkObj(obj *types.TypeName, bad *string) {
	p := obj.Pkg()
	if p == nil || p.Path() == w.self.Path() {
		return
	}
	switch {
	case !obj.Exported():
		*bad = "type " + p.Name() + "." + obj.Name() + ", unexported in package " + p.Path()
	case !importable(p.Path(), w.self.Path()):
		*bad = "type " + p.Name() + "." + obj.Name() + " from internal package " + p.Path()
	}
}

// importable applies Go's internal-package rule: path can be imported by
// from if from is inside the tree rooted at the parent of path's last
// internal element.
func importable(path, from string) bool {
	i := strings.LastIndex(path, "/internal/")
	switch {
	case i >= 0:
	case strings.HasSuffix(path, "/internal"):
		i = len(path) - len("/internal")
	case path == "internal" || strings.HasPrefix(path, "internal/"):
		return true // standard library internals can't appear in exported API
	default:
		return true
	}
	parent := path[:i]
	return from == parent || strings.HasPrefix(from, parent+"/")
}

// isModelType reports whether a struct type is detected as a model: it
// embeds db.Model, db.Timestamps or db.SoftDeletes (possibly through
// other embedded structs), or it has a TableName method.
func isModelType(t types.Type, st *types.Struct) bool {
	if hasMethod(types.NewPointer(t), "TableName", 0, "string") {
		return true
	}
	return embedsDB(st, map[types.Type]bool{})
}

func embedsDB(st *types.Struct, seen map[types.Type]bool) bool {
	for i := range st.NumFields() {
		f := st.Field(i)
		tag := reflect.StructTag(st.Tag(i)).Get("db")
		if !f.Embedded() || tag == "-" || strings.Split(tag, ",")[0] != "" {
			continue
		}
		base := f.Type()
		if p, ok := under(base).(*types.Pointer); ok {
			base = p.Elem()
		}
		base = types.Unalias(base)
		for _, name := range []string{"Model", "Timestamps", "SoftDeletes"} {
			if isNamed(base, dbPath, name) {
				return true
			}
		}
		if bst, ok := under(base).(*types.Struct); ok && !seen[base] && !isValueType(base) {
			seen[base] = true
			if embedsDB(bst, seen) {
				return true
			}
		}
	}
	return false
}

// isValueType reports whether a struct type is a single value (time,
// sql.Null*, or anything that scans itself) rather than a set of columns.
func isValueType(t types.Type) bool {
	return isNamed(types.Unalias(t), "time", "Time") ||
		hasMethod(types.NewPointer(t), "Scan", 1, "error") ||
		hasValuer(t)
}

// isColumnType reports whether an untagged field is a column: anything
// but structs, pointers to structs and slices of structs. []byte and value
// types are columns.
func isColumnType(t types.Type) bool {
	if s, ok := under(t).(*types.Slice); ok && !isUint8(s.Elem()) {
		t = s.Elem()
	}
	if p, ok := under(t).(*types.Pointer); ok {
		t = p.Elem()
	}
	_, isStruct := under(t).(*types.Struct)
	return !isStruct || isValueType(t)
}

func under(t types.Type) types.Type { return types.Unalias(t).Underlying() }

func isUint8(t types.Type) bool {
	b, ok := under(t).(*types.Basic)
	return ok && b.Kind() == types.Uint8
}

func isNamed(t types.Type, pkg, name string) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkg && n.Obj().Name() == name
}

var (
	errorType = types.Universe.Lookup("error").Type()
	anyType   = types.NewInterfaceType(nil, nil)
)

// hasMethod reports whether t's method set has name with the given number
// of parameters (each of type any) and a single result: error, or string.
func hasMethod(t types.Type, name string, params int, result string) bool {
	sig := methodSig(t, name)
	if sig == nil || sig.Variadic() || sig.Params().Len() != params || sig.Results().Len() != 1 {
		return false
	}
	for v := range sig.Params().Variables() {
		if !types.Identical(v.Type(), anyType) {
			return false
		}
	}
	res := sig.Results().At(0).Type()
	switch result {
	case "error":
		return types.Identical(res, errorType)
	case "string":
		return types.Identical(res, types.Typ[types.String])
	}
	return false
}

// hasValuer reports whether t implements database/sql/driver.Valuer.
func hasValuer(t types.Type) bool {
	sig := methodSig(t, "Value")
	if sig == nil || sig.Params().Len() != 0 || sig.Results().Len() != 2 {
		return false
	}
	return isNamed(types.Unalias(sig.Results().At(0).Type()), "database/sql/driver", "Value") &&
		types.Identical(sig.Results().At(1).Type(), errorType)
}

func methodSig(t types.Type, name string) *types.Signature {
	sel := types.NewMethodSet(t).Lookup(nil, name)
	if sel == nil {
		return nil
	}
	fn, ok := sel.Obj().(*types.Func)
	if !ok {
		return nil
	}
	return fn.Signature()
}
