// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/internal/naming"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
)

// DateTime is a date and time in a form, as a datetime-local input sends
// it ("2026-10-06T14:30"), in the app's time zone (APP_TIMEZONE). Use it
// for time fields of form structs; Time is the value.
type DateTime struct{ time.Time }

// UnmarshalText reads a datetime-local value, with or without seconds,
// or RFC 3339; empty text is the zero DateTime.
func (d *DateTime) UnmarshalText(b []byte) error {
	s := string(b)
	if s == "" {
		*d = DateTime{}
		return nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			*d = DateTime{t}
			return nil
		}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return fmt.Errorf("invalid date and time %q", s)
	}
	*d = DateTime{t}
	return nil
}

// MarshalText writes the value as a datetime-local input shows it.
func (d DateTime) MarshalText() ([]byte, error) {
	if d.IsZero() {
		return nil, nil
	}
	return []byte(d.In(time.Local).Format("2006-01-02T15:04")), nil
}

// formSpec is a field of a form struct.
type formSpec struct {
	Name     string // the JSON name, which the form posts
	Label    string
	Kind     string // text, textarea, password, email, number, checkbox, select, date, datetime
	Step     string // number inputs: "1" or "any"
	Index    []int
	Required bool
	Choices  []Choice // fixed choices of a select
	Help     string
}

var (
	dateType     = reflect.TypeFor[anetos.Date]()
	dateTimeType = reflect.TypeFor[DateTime]()
	textType     = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// formSpecs reads the fields of form struct t: exported fields with a
// JSON name, in order, through embedded structs.
func formSpecs(t reflect.Type) ([]formSpec, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("the form %s is not a struct", t)
	}
	var out []formSpec
	var walk func(t reflect.Type, prefix []int) error
	walk = func(t reflect.Type, prefix []int) error {
		for i := range t.NumField() {
			sf := t.Field(i)
			idx := append(append([]int(nil), prefix...), i)
			if sf.Anonymous && sf.Type.Kind() == reflect.Struct && !hasBindTag(sf) {
				if err := walk(sf.Type, idx); err != nil {
					return err
				}
				continue
			}
			if !sf.IsExported() || sf.Tag.Get("admin") == "-" || hasNonBodyTag(sf) {
				continue
			}
			// The name the form posts, as package web binds it: the form
			// tag, else the JSON name.
			name, ok := sf.Tag.Lookup("form")
			if !ok {
				tag, hasJSON := sf.Tag.Lookup("json")
				if !hasJSON {
					return fmt.Errorf("form field %s needs a json tag: its name in the form", sf.Name)
				}
				name, _, _ = strings.Cut(tag, ",")
				if name == "" {
					name = sf.Name
				}
			}
			if name == "-" || name == "" {
				continue
			}
			f := formSpec{Name: name, Label: sf.Tag.Get("label"), Index: idx}
			if f.Label == "" {
				f.Label = humanize(naming.Snake(sf.Name))
			}
			rules := "|" + sf.Tag.Get("validate") + "|"
			f.Required = strings.Contains(rules, "|required|")
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			switch {
			case ft == dateTimeType:
				f.Kind = "datetime"
			case ft == dateType:
				f.Kind = "date"
			case ft.Kind() == reflect.Bool:
				f.Kind = "checkbox"
			case ft.Kind() >= reflect.Int && ft.Kind() <= reflect.Uint64:
				f.Kind, f.Step = "number", "1"
			case ft.Kind() == reflect.Float32 || ft.Kind() == reflect.Float64:
				f.Kind, f.Step = "number", "any"
			case ft.Kind() == reflect.String || reflect.PointerTo(ft).Implements(textType):
				f.Kind = "text"
				if strings.Contains(rules, "|email|") {
					f.Kind = "email"
				}
			default:
				return fmt.Errorf("form field %s: a %s can't be edited in a form (strings, numbers, bools, admin.DateTime, anetos.Date)", sf.Name, sf.Type)
			}
			for opt := range strings.SplitSeq(sf.Tag.Get("admin"), ",") {
				k, v, _ := strings.Cut(strings.TrimSpace(opt), "=")
				switch k {
				case "":
				case "textarea", "password":
					f.Kind = k
				case "select":
					f.Kind = "select"
					if v != "" {
						f.Choices = Choices(strings.Split(v, "|")...)
					}
				case "help":
					f.Help = v
				default:
					return fmt.Errorf("form field %s: unknown admin tag option %q (textarea, password, select=a|b, help=…, -)", sf.Name, k)
				}
			}
			out = append(out, f)
		}
		return nil
	}
	return out, walk(t, nil)
}

// hasBindTag reports whether a field says where it is bound from, as
// package web reads it (an embedded struct with one isn't walked).
func hasBindTag(sf reflect.StructField) bool {
	for _, k := range []string{"path", "query", "header", "form", "json"} {
		if _, ok := sf.Tag.Lookup(k); ok {
			return true
		}
	}
	return false
}

// hasNonBodyTag reports fields bound from the path, query or headers.
func hasNonBodyTag(sf reflect.StructField) bool {
	for _, k := range []string{"path", "query", "header"} {
		if _, ok := sf.Tag.Lookup(k); ok {
			return true
		}
	}
	return false
}

// inputField is a form field as the page shows it.
type inputField struct {
	formSpec
	Value   string
	Checked bool
	Choices []choiceView
	Error   string
}

type choiceView struct {
	Value, Label string
	Selected     bool
}

// inputs returns form's fields with their values (the submitted ones
// after a failed post) and errors.
func (r *res[T, F]) inputs(ctx context.Context, form F) ([]inputField, error) {
	v := reflect.ValueOf(form)
	errs := view.Errors(ctx)
	out := make([]inputField, len(r.form))
	for i, spec := range r.form {
		f := inputField{formSpec: spec, Error: errs.Get(spec.Name)}
		fv := v.FieldByIndex(spec.Index)
		saved := inputValue(fv)
		if spec.Kind == "checkbox" {
			b := fv.Kind() == reflect.Bool && fv.Bool() || fv.Kind() == reflect.Pointer && !fv.IsNil() && fv.Elem().Bool()
			f.Checked = view.OldChecked(ctx, spec.Name, b)
		} else if spec.Kind != "password" {
			f.Value = view.Old(ctx, spec.Name, saved)
		}
		choices := spec.Choices
		if spec.Kind == "select" {
			if load := r.Choices[spec.Name]; load != nil {
				c, err := load(ctx)
				if err != nil {
					return nil, err
				}
				choices = c
			}
			for _, c := range choices {
				f.Choices = append(f.Choices, choiceView{c.Value, c.Label, c.Value == f.Value})
			}
		}
		out[i] = f
	}
	return out, nil
}

// inputValue formats a form field's value for its input.
func inputValue(v reflect.Value) string {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if tm, ok := reflect.TypeAssert[encoding.TextMarshaler](v); ok {
		b, err := tm.MarshalText()
		if err != nil {
			return ""
		}
		return string(b)
	}
	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, v.Type().Bits())
	}
	return fmt.Sprint(v.Interface())
}

// formPage is a create or edit page.
type formPage struct {
	Action   string
	Cancel   string
	Submit   string
	Fields   []inputField
	Multiple bool
}

func (r *res[T, F]) renderForm(c *web.Ctx, title, action, cancel, submit string, form F, crumbs []navItem) error {
	fields, err := r.inputs(c, form)
	if err != nil {
		return err
	}
	return r.p.render(c, "form", page{Title: title, Crumbs: crumbs, Data: formPage{Action: action, Cancel: cancel, Submit: submit, Fields: fields}})
}

func (r *res[T, F]) newPage(c *web.Ctx) error {
	var zero T
	return r.renderForm(c, "New "+strings.ToLower(r.Label), r.url(""), r.url(""), "Create", r.Edit(zero),
		r.crumbs(navItem{Title: "New"}))
}

// errOutOfScope is a record that Apply put outside the resource's Query.
var errOutOfScope = errors.New("admin: out of scope")

// save applies the submitted form to row, checks it, and saves it: the
// form's fields from in, the others (admin:"-", path or query fields)
// from base, whatever the request sent; select values among their
// choices; and a row the resource still sees, in one transaction.
func (r *res[T, F]) save(c *web.Ctx, base, in F, row *T, create bool) error {
	b := reflect.ValueOf(&base).Elem()
	iv := reflect.ValueOf(in)
	for _, f := range r.form {
		b.FieldByIndex(f.Index).Set(iv.FieldByIndex(f.Index))
	}
	if err := r.checkChoices(c, b); err != nil {
		return err
	}
	return db.Tx(c, func(ctx context.Context) error {
		if err := r.Apply(ctx, base, row); err != nil {
			return err
		}
		var err error
		if create {
			err = db.Create(ctx, row)
		} else {
			err = db.Update(ctx, row)
		}
		if err != nil || r.Query == nil {
			return err
		}
		_, k, err := db.KeyOf(row)
		if err != nil {
			return err
		}
		if _, err := r.query(ctx).Find(k); errors.Is(err, db.ErrNotFound) {
			return errOutOfScope
		} else if err != nil {
			return err
		}
		return nil
	})
}

// checkChoices fails select fields whose value isn't one of their
// choices.
func (r *res[T, F]) checkChoices(ctx context.Context, form reflect.Value) error {
	var errs validate.Errors
	for _, spec := range r.form {
		fv := form.FieldByIndex(spec.Index)
		if spec.Kind != "select" || fv.IsZero() {
			continue
		}
		choices := spec.Choices
		if load := r.Choices[spec.Name]; load != nil {
			var err error
			if choices, err = load(ctx); err != nil {
				return err
			}
		}
		v := inputValue(fv)
		if !slices.ContainsFunc(choices, func(ch Choice) bool { return ch.Value == v }) {
			errs.Add(spec.Name, strings.ReplaceAll(i18n.T(ctx, "validation.in"), "{label}", strings.ToLower(spec.Label)))
		}
	}
	if errs.Len() > 0 {
		return &errs
	}
	return nil
}

// outOfScope tells the user the record would leave the resource.
func (r *res[T, F]) outOfScope(back string) web.Responder {
	return web.ResponderFunc(func(c *web.Ctx) error {
		return failed(c, "Not saved: the "+strings.ToLower(r.Label)+" would be outside this list.", back)
	})
}

func (r *res[T, F]) create(c *web.Ctx, in F) (web.Responder, error) {
	var row T
	if err := r.save(c, r.Edit(row), in, &row, true); errors.Is(err, errOutOfScope) {
		return r.outOfScope(r.url("/new")), nil
	} else if err != nil {
		return nil, err
	}
	to := r.url("/" + r.keyText(row))
	return web.ResponderFunc(func(c *web.Ctx) error { return done(c, r.Label+" created.", to) }), nil
}

func (r *res[T, F]) edit(c *web.Ctx) error {
	row, err := r.find(c, false)
	if err != nil {
		return err
	}
	show := r.url("/" + r.keyText(row))
	if err := r.check(c, row, "update"); err != nil {
		return refused(c, err, show)
	}
	return r.renderForm(c, "Edit "+r.label(row), show, show, "Save", r.Edit(row),
		r.crumbs(navItem{Title: r.label(row), URL: show}, navItem{Title: "Edit"}))
}

func (r *res[T, F]) update(c *web.Ctx, in F) (web.Responder, error) {
	row, err := r.find(c, false)
	if err != nil {
		return nil, err
	}
	show := r.url("/" + r.keyText(row))
	if err := r.check(c, row, "update"); err != nil {
		if _, ok := userError(err); ok {
			return web.ResponderFunc(func(c *web.Ctx) error { return refused(c, err, show) }), nil
		}
		return nil, err
	}
	if err := r.save(c, r.Edit(row), in, &row, false); errors.Is(err, errOutOfScope) {
		return r.outOfScope(show + "/edit"), nil
	} else if err != nil {
		return nil, err
	}
	return web.ResponderFunc(func(c *web.Ctx) error { return done(c, "Saved.", show) }), nil
}
