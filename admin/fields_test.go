// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"database/sql"
	"html/template"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/view"
)

func TestNames(t *testing.T) {
	for in, want := range map[string]string{"blog-posts": "Blog posts", "api_tokens": "Api tokens", "": ""} {
		if got := humanize(in); got != want {
			t.Errorf("humanize(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{"Posts": "Post", "Categories": "Category", "Boxes": "Box", "Classes": "Class", "Glass": "Glass"} {
		if got := singular(in); got != want {
			t.Errorf("singular(%q) = %q", in, got)
		}
	}
}

func TestColumnFields(t *testing.T) {
	mf, err := columnFields(reflect.TypeFor[Post]())
	if err != nil || mf.pk != "id" {
		t.Fatalf("pk %q, %v", mf.pk, err)
	}
	p := Post{Title: "x"}
	p.ID = 7
	v := reflect.ValueOf(p)
	if mf.value(v, "title") != "x" || mf.value(v, "id") != int64(7) || mf.value(v, "nope") != nil || mf.value(v, "deleted_at") != (*time.Time)(nil) {
		t.Error("value")
	}
	if mf, _ := columnFields(reflect.TypeFor[Tag]()); mf.pk != "id" {
		t.Errorf("Tag pk %q", mf.pk)
	}
	if _, err := columnFields(reflect.TypeFor[int]()); err == nil {
		t.Error("columnFields(int)")
	}
}

type stringer struct{}

func (stringer) String() string { return "<s>" }

func TestCell(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 10, 6, 14, 30, 0, 0, time.UTC)
	n := 5
	for _, tt := range []struct {
		v    any
		want template.HTML
	}{
		{nil, ""},
		{"<b>", "&lt;b&gt;"},
		{template.HTML("<b>ok</b>"), "<b>ok</b>"},
		{true, "Yes"},
		{false, "No"},
		{at, "2026-10-06 14:30"},
		{time.Time{}, ""},
		{DateTime{at}, "2026-10-06 14:30"},
		{anetos.Date{}, ""},
		{stringer{}, "&lt;s&gt;"},
		{&n, "5"},
		{(*int)(nil), ""},
		{(*time.Time)(nil), ""},
		{(*anetos.Date)(nil), ""},
		{&at, "2026-10-06 14:30"},
		{sql.NullString{String: "x", Valid: true}, "x"},
		{sql.NullString{}, ""},
		{[]byte("<b>"), "&lt;b&gt;"},
		{view.ComponentFunc(func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, "<i>x</i>"); return err }), "<i>x</i>"},
	} {
		if got := cell(ctx, tt.v); got != tt.want {
			t.Errorf("cell(%#v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

type everyField struct {
	Name     string      `json:"name" validate:"required"`
	Email    string      `json:"email" validate:"email"`
	Bio      *string     `json:"bio" admin:"textarea"`
	Secret   string      `json:"secret" admin:"password"`
	Age      int         `json:"age" label:"Age in years"`
	Price    float64     `json:"price"`
	Admin    bool        `json:"admin"`
	Born     anetos.Date `json:"born"`
	At       DateTime    `json:"at"`
	Role     string      `json:"role" admin:"select,help=Pick one."`
	Hidden   string      `json:"hidden" admin:"-"`
	Skip     string      `json:"-"`
	ID       int64       `path:"id"`
	internal string
	Embedded
}

type Embedded struct {
	Extra string `json:"extra"`
}

func TestFormSpecs(t *testing.T) {
	specs, err := formSpecs(reflect.TypeFor[everyField]())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range specs {
		got = append(got, s.Name+":"+s.Kind)
	}
	want := "name:text email:email bio:textarea secret:password age:number price:number admin:checkbox born:date at:datetime role:select extra:text"
	if strings.Join(got, " ") != want {
		t.Errorf("specs\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	if !specs[0].Required || specs[1].Required || specs[4].Label != "Age in years" || specs[4].Step != "1" || specs[5].Step != "any" || specs[9].Help != "Pick one." {
		t.Errorf("specs %+v", specs)
	}
	_ = everyField{}.internal
	for _, bad := range []any{struct {
		M map[string]int `json:"m"`
	}{}, struct {
		S string `json:"s" admin:"fancy"`
	}{}, 1} {
		if _, err := formSpecs(reflect.TypeOf(bad)); err == nil {
			t.Errorf("formSpecs(%T) accepted", bad)
		}
	}
}

func TestDateTime(t *testing.T) {
	var d DateTime
	for _, s := range []string{"2026-10-06T14:30", "2026-10-06T14:30:00", "2026-10-06T14:30:00Z"} {
		if err := d.UnmarshalText([]byte(s)); err != nil || d.Minute() != 30 {
			t.Errorf("%s: %v %v", s, d, err)
		}
	}
	if err := d.UnmarshalText([]byte("")); err != nil || !d.IsZero() {
		t.Error("empty")
	}
	if err := d.UnmarshalText([]byte("tomorrow")); err == nil {
		t.Error("tomorrow accepted")
	}
	if b, _ := (DateTime{}).MarshalText(); b != nil {
		t.Error("zero MarshalText")
	}
	d = DateTime{time.Date(2026, 10, 6, 14, 30, 0, 0, time.Local)}
	if b, _ := d.MarshalText(); string(b) != "2026-10-06T14:30" {
		t.Errorf("MarshalText %s", b)
	}
	s := "x"
	for _, tt := range []struct {
		v    any
		want string
	}{{"a", "a"}, {7, "7"}, {uint8(3), "3"}, {1.5, "1.5"}, {float32(0.1), "0.1"}, {&s, "x"}, {(*string)(nil), ""}, {d, "2026-10-06T14:30"}} {
		if got := inputValue(reflect.ValueOf(tt.v)); got != tt.want {
			t.Errorf("inputValue(%v) = %q", tt.v, got)
		}
	}
}

func TestConfigValidate(t *testing.T) {
	cfg, err := config.Get[Config](config.Map{})
	if err != nil || cfg.PerPage != 25 || cfg.Path != "" {
		t.Errorf("defaults %+v, %v", cfg, err)
	}
	for _, bad := range []config.Map{{"ADMIN_PATH": "admin"}, {"ADMIN_PATH": "/a/{x}"}, {"ADMIN_HOST": "http://x"}, {"ADMIN_PER_PAGE": "0"}, {"ADMIN_PER_PAGE": "501"}} {
		if _, err := config.Get[Config](bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
