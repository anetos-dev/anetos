// SPDX-License-Identifier: Apache-2.0

package fixture

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos/db"
)

// named is what every generated column has.
type named interface{ Name() string }

// check compares a generated columns struct with the db package's view
// of model M: same column names in the same order, and each column typed
// with its field's type, and JSON for `db:",json"` fields.
func check[M any](t *testing.T, cols any) {
	t.Helper()
	want, err := db.Columns[M]()
	if err != nil {
		t.Fatal(err)
	}
	mt := reflect.TypeFor[M]()
	v := reflect.ValueOf(cols)
	var got []string
	for i := range v.NumField() {
		f := v.Type().Field(i)
		got = append(got, v.Field(i).Interface().(named).Name())
		sf, ok := mt.FieldByName(f.Name)
		if !ok {
			t.Errorf("%s: no field %s", mt, f.Name)
			continue
		}
		_, opts, _ := strings.Cut(sf.Tag.Get("db"), ",")
		wantJSON := slices.Contains(strings.Split(opts, ","), "json")
		if isJSON := v.Field(i).FieldByName("json").Bool(); isJSON != wantJSON {
			t.Errorf("%s.%s: JSON column %v, want %v", mt, f.Name, isJSON, wantJSON)
		}
		// Column[T].Eq takes a T.
		if eq, _ := f.Type.MethodByName("Eq"); eq.Type.In(1) != sf.Type {
			t.Errorf("%s.%s: column of %s, field of %s", mt, f.Name, eq.Type.In(1), sf.Type)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: generated %v, db.Columns %v", mt, got, want)
	}
}

// checkRels compares generated relation handles with model M's rel-tagged
// fields: one handle per field, in order, each usable.
func checkRels[M any](t *testing.T, rels any) {
	t.Helper()
	mt := reflect.TypeFor[M]()
	var want []string
	for f := range mt.Fields() {
		if _, ok := f.Tag.Lookup("rel"); ok {
			want = append(want, f.Name)
		}
	}
	var got []string
	for _, fv := range reflect.ValueOf(rels).Fields() {
		h := fv.Interface().(interface {
			Name() string
			Err() error
		})
		got = append(got, h.Name())
		if err := h.Err(); err != nil {
			t.Errorf("%s: %v", h.Name(), err)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s: generated relations %v, rel-tagged fields %v", mt, got, want)
	}
}

func TestGeneratedMatchesRuntime(t *testing.T) {
	checkRels[Post](t, PostRels)
	checkRels[Comment](t, CommentRels)
	check[Author](t, AuthorCols)
	check[Post](t, PostCols)
	check[Comment](t, CommentCols)
	check[Invoice](t, InvoiceCols)
	check[Setting](t, SettingCols)
	check[auditEntry](t, auditEntryCols)
}
