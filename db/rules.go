// SPDX-License-Identifier: Apache-2.0

package db

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"anetos.dev/anetos/validate"
)

// The db package registers two validation rules that query the database in
// the request's context:
//
//	unique:table[,column[,exceptField[,idColumn]]]
//	exists:table[,column]
//
// column defaults to the field's key. For unique, exceptField names a
// field of the same struct holding the ID of a row to ignore (the one being
// edited), compared with idColumn (default id):
//
//	ID    int64  `path:"id"` // not from the body, so clients can't pick it
//	Email string `json:"email" validate:"required|email|unique:users,email,ID"`
//
// Both rules count soft-deleted rows, like a unique index would.
func init() {
	validate.Register("unique", "The {label} has already been taken.", uniqueRule)
	validate.Register("exists", "The selected {label} is invalid.", existsRule)
}

func uniqueRule(ctx context.Context, f validate.Field) (bool, error) {
	if len(f.Params) < 1 || len(f.Params) > 4 {
		return false, fmt.Errorf("db: unique needs table[,column[,exceptField[,idColumn]]]")
	}
	table, col := f.Params[0], ruleColumn(f)
	conds := []Expr{cmpExpr{col, "=", f.Value}}
	if len(f.Params) >= 3 {
		except, err := fieldByName(f.Parent, f.Params[2])
		if err != nil {
			return false, err
		}
		if except != nil && !reflect.ValueOf(except).IsZero() {
			idCol := "id"
			if len(f.Params) == 4 {
				idCol = f.Params[3]
			}
			conds = append(conds, cmpExpr{idCol, "<>", except})
		}
	}
	found, err := rowExists(ctx, table, conds)
	return !found, err
}

func existsRule(ctx context.Context, f validate.Field) (bool, error) {
	if len(f.Params) < 1 || len(f.Params) > 2 {
		return false, fmt.Errorf("db: exists needs table[,column]")
	}
	return rowExists(ctx, f.Params[0], []Expr{cmpExpr{ruleColumn(f), "=", f.Value}})
}

func ruleColumn(f validate.Field) string {
	if len(f.Params) >= 2 {
		return f.Params[1]
	}
	key := f.Key
	if i := strings.LastIndexByte(key, '.'); i >= 0 {
		key = key[i+1:]
	}
	return key
}

func rowExists(ctx context.Context, table string, conds []Expr) (bool, error) {
	d, c, err := handle(ctx)
	if err != nil {
		return false, err
	}
	b := &sqlBuilder{d: d.dialect}
	b.write("SELECT 1 FROM ")
	b.name(table)
	b.write(" WHERE ")
	And(conds...).build(b)
	b.write(" LIMIT 1")
	if b.err != nil {
		return false, b.err
	}
	rows, err := d.query(ctx, c, b.String(), b.args)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	found := rows.Next()
	if err := rows.Err(); err != nil {
		return false, err
	}
	return found, rows.Close()
}

var fieldIndexes sync.Map // struct{reflect.Type; string} → []int

// fieldByName reads a field of the struct parent, by Go name or key.
func fieldByName(parent any, name string) (any, error) {
	v := reflect.ValueOf(parent)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	type key struct {
		t    reflect.Type
		name string
	}
	k := key{v.Type(), name}
	idx, ok := fieldIndexes.Load(k)
	if !ok {
		sf, found := v.Type().FieldByName(name)
		if !found || !sf.IsExported() {
			return nil, fmt.Errorf("db: unique: %s has no field %s", v.Type(), name)
		}
		idx, _ = fieldIndexes.LoadOrStore(k, sf.Index)
	}
	f, ok := fieldAt(v, idx.([]int))
	if !ok {
		return nil, nil // behind a nil embedded pointer: nothing to exclude
	}
	for f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return 0, nil
		}
		f = f.Elem()
	}
	return f.Interface(), nil
}

func fieldAt(v reflect.Value, index []int) (reflect.Value, bool) {
	f := fieldOf(v, index)
	return f, f.IsValid()
}
