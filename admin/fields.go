// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"database/sql/driver"
	"fmt"
	"html/template"
	"log/slog"
	"reflect"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/internal/naming"
	"anetos.dev/anetos/view"
)

// modelFields maps a model's columns to its fields, as package db does:
// the db tag's name, else the field's name in snake case, through
// embedded structs (db.Model…).
type modelFields struct {
	index map[string][]int
	pk    string // the primary key's column
}

func columnFields(t reflect.Type) (modelFields, error) {
	mf := modelFields{index: map[string][]int{}}
	if t.Kind() != reflect.Struct {
		return mf, fmt.Errorf("admin: %s is not a struct", t)
	}
	var walk func(t reflect.Type, prefix []int)
	walk = func(t reflect.Type, prefix []int) {
		for i := range t.NumField() {
			sf := t.Field(i)
			idx := append(append([]int(nil), prefix...), i)
			tag, hasTag := sf.Tag.Lookup("db")
			name, opts, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if sf.Anonymous && !hasTag && sf.Type.Kind() == reflect.Struct {
				walk(sf.Type, idx)
				continue
			}
			if !sf.IsExported() {
				continue
			}
			if name == "" {
				name = naming.Snake(sf.Name)
			}
			mf.index[name] = idx
			if strings.Contains(","+opts+",", ",pk,") || mf.pk == "" && name == "id" {
				mf.pk = name
			}
		}
	}
	walk(t, nil)
	return mf, nil
}

// value returns the field of row for column, nil if there is none.
func (mf modelFields) value(row reflect.Value, column string) any {
	idx, ok := mf.index[column]
	if !ok {
		return nil
	}
	f, err := row.FieldByIndexErr(idx)
	if err != nil {
		return nil
	}
	return f.Interface()
}

// humanize turns a name into a title: "blog-posts", "blog_posts" →
// "Blog posts".
func humanize(s string) string {
	s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// singular guesses a plural English title's singular.
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return strings.TrimSuffix(s, "ies") + "y"
	case strings.HasSuffix(s, "sses"), strings.HasSuffix(s, "ches"), strings.HasSuffix(s, "shes"), strings.HasSuffix(s, "xes"):
		return strings.TrimSuffix(s, "es")
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return strings.TrimSuffix(s, "s")
	}
	return s
}

// cell renders a value for a list or a record's page.
func cell(ctx context.Context, v any) template.HTML {
	switch x := v.(type) {
	case nil:
		return ""
	case template.HTML:
		return x
	case view.Component:
		s, err := view.String(ctx, x)
		if err != nil {
			slog.ErrorContext(ctx, "admin: rendering a cell", "error", err)
			return "(error)"
		}
		return template.HTML(s) //nolint:gosec // the component's own markup
	}
	// Pointers first: a nil *time.Time is a Stringer whose String panics.
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return ""
		}
		return cell(ctx, rv.Elem().Interface())
	}
	switch x := v.(type) {
	case bool:
		if x {
			return "Yes"
		}
		return "No"
	case time.Time:
		if x.IsZero() {
			return ""
		}
		return template.HTML(template.HTMLEscapeString(x.In(anetos.Location(ctx)).Format("2006-01-02 15:04")))
	case DateTime:
		return cell(ctx, x.Time)
	case anetos.Date:
		if x.IsZero() {
			return ""
		}
		return template.HTML(template.HTMLEscapeString(x.String()))
	case fmt.Stringer:
		return template.HTML(template.HTMLEscapeString(x.String()))
	case driver.Valuer: // sql.NullString and the like
		dv, err := x.Value()
		if err != nil || dv == nil {
			return ""
		}
		return cell(ctx, dv)
	case []byte:
		return template.HTML(template.HTMLEscapeString(string(x)))
	}
	return template.HTML(template.HTMLEscapeString(fmt.Sprint(v)))
}
