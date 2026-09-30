// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"mime/multipart"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Regression tests for the F6 review.

type EmbBase struct {
	Name string `json:"name" validate:"required"`
}

type withEmbeddedPtr struct {
	*EmbBase
	Other string `json:"other"`
}

type withNamedEmbedded struct {
	EmbBase `json:"base"`
}

func TestEmbeddedPointerStructs(t *testing.T) {
	// A nil embedded pointer can't hide required fields, and keys are flat.
	wantError(t, &withEmbeddedPtr{Other: "o"}, "name", "The name field is required.")
	wantError(t, &withEmbeddedPtr{EmbBase: &EmbBase{}}, "name", "")
	wantValid(t, &withEmbeddedPtr{EmbBase: &EmbBase{Name: "x"}})
	// An embedded struct with a json name is nested, like encoding/json.
	wantError(t, &withNamedEmbedded{}, "base.name", "")
}

type ShadowInner struct {
	Name string `json:"name" validate:"required"`
	Age  int    `json:"age" validate:"min:18"`
}

type shadowOuter struct {
	ShadowInner
	Name string `json:"name" validate:"max:10"`
}

func TestShadowedFieldsAreIgnored(t *testing.T) {
	wantValid(t, &shadowOuter{Name: "bob", ShadowInner: ShadowInner{Age: 20}})
	wantError(t, &shadowOuter{Name: "bob"}, "age", "")
}

type ambiguousA struct {
	ID string `validate:"required"`
}
type ambiguousB struct {
	ID string `validate:"required"`
}
type ambiguous struct {
	ambiguousA
	ambiguousB
}

func TestAmbiguousFieldsWithRulesAreErrors(t *testing.T) {
	// encoding/json ignores both, so their rules could never pass.
	if _, err := Compile(reflect.TypeFor[ambiguous]()); err == nil || !strings.Contains(err.Error(), "ambiguous with") {
		t.Errorf("err = %v", err)
	}
	type Leaf struct {
		X string `json:"x" validate:"required"`
	}
	type P1 struct{ Leaf }
	type P2 struct{ Leaf }
	// Built with StructOf because go vet (rightly) rejects the literal type.
	twice := reflect.StructOf([]reflect.StructField{
		{Name: "P1", Type: reflect.TypeFor[P1](), Anonymous: true},
		{Name: "P2", Type: reflect.TypeFor[P2](), Anonymous: true},
	})
	if _, err := Compile(twice); err == nil || !strings.Contains(err.Error(), "embedded more than once") {
		t.Errorf("err = %v", err)
	}
	// Without rules, ambiguity is fine (and ignored, like encoding/json).
	type plainLeaf struct{ X string }
	type Q1 struct{ plainLeaf }
	type Q2 struct{ plainLeaf }
	type plainTwice struct {
		Q1
		Q2
		Y string `validate:"required"`
	}
	wantError(t, &plainTwice{}, "Y", "")
}

func TestJSONIgnoredEmbedded(t *testing.T) {
	type Hidden struct {
		Name string `json:"name" validate:"required"`
	}
	type in struct {
		Hidden `json:"-"`
		Other  string `json:"other"`
	}
	wantValid(t, &in{})
}

func TestSameKeyFromDifferentTags(t *testing.T) {
	_, err := Compile(reflect.TypeFor[struct {
		ID  int    `path:"id" validate:"min:1"`
		Ref string `json:"id" validate:"required|uuid"`
	}]())
	if err == nil || !strings.Contains(err.Error(), `both report errors as "id"`) {
		t.Errorf("err = %v", err)
	}
}

func TestDottedKeysInMessages(t *testing.T) {
	wantError(t, &dottedMessages{}, "user.name", "Name, please.")
}

type dottedMessages struct {
	Name string `json:"user.name" validate:"required"`
}

func (dottedMessages) ValidationMessages() map[string]string {
	return map[string]string{"user.name.required": "Name, please."}
}

func TestMapKeysAreNotIndexes(t *testing.T) {
	type item struct {
		B string `json:"b" validate:"required"`
	}
	type in struct {
		M map[string]item `json:"m"`
	}
	errs := fieldErrors(t, &in{M: map[string]item{"": {}, "0": {B: "x"}}})
	if _, ok := errs["m..b"]; !ok || len(errs) != 1 {
		t.Errorf("errors = %v", errs)
	}
}

type deepTree struct {
	Name string     `json:"name" validate:"required"`
	Kids []deepTree `json:"kids"`
}

func TestDeepButFiniteNesting(t *testing.T) {
	root := deepTree{Name: "0"}
	cur := &root
	for i := range 300 {
		cur.Kids = []deepTree{{Name: strconv.Itoa(i + 1)}}
		cur = &cur.Kids[0]
	}
	wantValid(t, &root)
	cur.Kids = []deepTree{{}}
	errs := fieldErrors(t, &root)
	want := strings.Repeat("kids.0.", 301) + "name"
	if _, ok := errs[want]; !ok || len(errs) != 1 {
		t.Errorf("errors = %v", errs)
	}
}

type selfEmbed struct {
	*selfEmbed
	A string `validate:"required"`
}

func TestSelfEmbeddingTerminates(t *testing.T) {
	wantError(t, &selfEmbed{selfEmbed: &selfEmbed{A: "inner"}}, "A", "")
}

func TestDistinctRejectsHiddenInterfaces(t *testing.T) {
	type anyV struct{ V any }
	for _, typ := range []reflect.Type{
		reflect.TypeFor[struct {
			Items []anyV `validate:"distinct"`
		}](),
		reflect.TypeFor[struct {
			Items [][2]any `validate:"distinct"`
		}](),
	} {
		if _, err := Compile(typ); err == nil || !strings.Contains(err.Error(), "can't be compared") {
			t.Errorf("%s: err = %v", typ, err)
		}
	}
	type point struct{ X, Y int }
	type ok struct {
		Points []point `validate:"distinct"`
	}
	wantError(t, &ok{Points: []point{{1, 2}, {1, 2}}}, "Points", "")
}

// uuid is a fixed-size array that parses itself, like github.com/google/uuid.
type uuid [16]byte

func (u *uuid) UnmarshalText([]byte) error { return nil }

func TestValueArrays(t *testing.T) {
	type in struct {
		ID uuid `json:"id" validate:"required"`
	}
	wantError(t, &in{}, "id", "The id field is required.")
	wantValid(t, &in{ID: uuid{1}})
	_, err := Compile(reflect.TypeFor[struct {
		ID uuid `validate:"max:5"`
	}]())
	if err == nil || !strings.Contains(err.Error(), "does not apply") {
		t.Errorf("max on a UUID: %v", err)
	}
	type counts struct {
		Grid [3]int `json:"grid" validate:"required"`
	}
	wantError(t, &counts{}, "grid", "")
	wantValid(t, &counts{Grid: [3]int{0, 1, 0}})
}

func TestNumericPrecision(t *testing.T) {
	type in struct {
		Ratio float32 `validate:"in:0.1,0.2"`
		Kind  float32
		Other string `validate:"required_if:Kind,0.1"`
		ID    int64  `validate:"in:9007199254740993"`
		U     uint64 `validate:"in:18446744073709551615"`
		Big   int64  `validate:"max:9007199254740992"`
		Neg   uint   `validate:"min:-5"`
	}
	base := in{Ratio: 0.1, Kind: 1, ID: 9007199254740993, U: math.MaxUint64, Big: 1}
	wantValid(t, &base)
	v := base
	v.Kind = 0.1
	wantError(t, &v, "Other", "")
	v = base
	v.ID = 9007199254740992
	wantError(t, &v, "ID", "")
	v = base
	v.Big = 9007199254740993
	wantError(t, &v, "Big", "")

	if _, err := Compile(reflect.TypeFor[struct {
		N int `validate:"in:1.5"`
	}]()); err == nil || !strings.Contains(err.Error(), "whole number") {
		t.Errorf("in:1.5 on int: %v", err)
	}
}

type noRules struct{ A, B string }

type recursiveNoRules struct {
	Children []*recursiveNoRules
	Name     string
}

type recursiveRules struct {
	Next *recursiveRules
	Name string `json:"name" validate:"required"`
}

func TestPlansWithoutRulesArePruned(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[struct {
			N noRules
			S []noRules
			M map[string]*noRules
		}](),
		reflect.TypeFor[recursiveNoRules](),
	} {
		if p := MustCompile(typ); !p.Empty() {
			t.Errorf("%s: plan not empty: %d fields", typ, len(p.fields))
		}
	}
	if MustCompile(reflect.TypeFor[recursiveRules]()).Empty() {
		t.Error("recursive plan with rules pruned")
	}
	wantError(t, &recursiveRules{Name: "a", Next: &recursiveRules{}}, "Next.name", "")
}

func TestCyclicDataIsAnError(t *testing.T) {
	n := &recursiveRules{Name: "loop"}
	n.Next = n
	err := Struct(context.Background(), n)
	if err == nil || !strings.Contains(err.Error(), "levels deep") {
		t.Fatalf("err = %v", err)
	}
	if _, ok := errors.AsType[*Errors](err); ok {
		t.Error("cycle reported as a validation failure")
	}
}

type allocInput struct {
	Email    string    `json:"email" validate:"required|email|max:100"`
	Password string    `json:"password" validate:"required|min:8|confirmed"`
	Confirm  string    `json:"password_confirmation"`
	Plan     string    `json:"plan" validate:"in:free,pro"`
	Seats    int       `json:"seats" validate:"in:100,200|between:1,500"`
	Kind     int       `json:"kind"`
	Company  string    `json:"company" validate:"required_if:kind,2"`
	Start    time.Time `json:"start" validate:"required|after:now"`
	End      time.Time `json:"end" validate:"after:start"`
	Tags     []string  `json:"tags" validate:"max:5|distinct|alpha_dash"`
	Address  struct {
		City string `json:"city" validate:"required"`
	} `json:"address"`
	Items []struct {
		Name string `json:"name" validate:"required"`
	} `json:"items"`
}

func TestValidInputDoesNotAllocate(t *testing.T) {
	in := &allocInput{
		Email: "a@b.co", Password: "12345678", Confirm: "12345678", Plan: "pro", Seats: 200,
		Kind: 2, Company: "Acme", Start: time.Now().Add(time.Hour), End: time.Now().Add(2 * time.Hour),
		Tags: []string{"a", "b"},
	}
	in.Address.City = "Dhaka"
	in.Items = append(in.Items, struct {
		Name string `json:"name" validate:"required"`
	}{Name: "x"})
	ctx := context.Background()
	if err := Struct(ctx, in); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(100, func() { _ = Struct(ctx, in) }); n != 0 {
		t.Errorf("valid input: %v allocations per run", n)
	}
}

func TestErrDeepCopies(t *testing.T) {
	var errs Errors
	errs.Add("a", "A")
	err := errs.Err()
	errs.Add("b", "B")
	var got *Errors
	if !errors.As(err, &got) || got.Len() != 1 || got.Has("b") || len(got.FieldErrors()) != 1 {
		t.Errorf("Err() changed by a later Add: %v", err)
	}
}

func TestSkipUnexportedWithDash(t *testing.T) {
	type in struct {
		secret string `validate:"-"`
		Name   string `validate:"required"`
	}
	_ = in{secret: ""}
	wantError(t, &in{}, "Name", "")
}

func TestMimetypesAndExtensionsParams(t *testing.T) {
	for _, tag := range []string{"mimetypes:image/svg+xml", "mimetypes:text/csv", "mimetypes:*/*", "mimetypes:model/*"} {
		typ := reflect.StructOf([]reflect.StructField{{
			Name: "F", Type: reflect.TypeFor[*multipart.FileHeader](), Tag: reflect.StructTag(`validate:"` + tag + `"`),
		}})
		if _, err := Compile(typ); err == nil || !strings.Contains(err.Error(), "can't be detected") {
			t.Errorf("%s: err = %v", tag, err)
		}
	}
	f := uploads(t, map[string][]byte{"backup.TAR.GZ": []byte("x"), ".gz": []byte("x")})
	type in struct {
		F *multipart.FileHeader `validate:"extensions:tar.gz"`
	}
	wantValid(t, &in{F: f["backup.TAR.GZ"]})
	wantError(t, &in{F: f[".gz"]}, "F", "")
}

func TestNilSafety(t *testing.T) {
	p := MustCompile(reflect.TypeFor[EmbBase]())
	if err := p.Validate(context.Background(), nil); err == nil {
		t.Error("Validate(nil) succeeded")
	}
	var e *Errors
	if e.Has("x") || e.Get("x") != "" || e.Len() != 0 || e.Keys() != nil || e.FieldErrors() != nil || e.Err() != nil {
		t.Error("nil *Errors")
	}
	for _, name := range []string{"a=b", "a\tb", "a-b", "a.b", "é"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q) did not panic", name)
				}
			}()
			Register(name, "", func(context.Context, Field) (bool, error) { return true, nil })
		}()
	}
}

func TestEachSkipsEmptyElements(t *testing.T) {
	type in struct {
		Emails []string `validate:"email"`
	}
	wantValid(t, &in{Emails: []string{"a@b.co", "", "  "}})
	wantError(t, &in{Emails: []string{"", "nope"}}, "Emails", "")
}

func TestLaravelHints(t *testing.T) {
	_, err := Compile(reflect.TypeFor[struct {
		A *string `validate:"nullable|email"`
	}]())
	if err == nil || !strings.Contains(err.Error(), "not needed") {
		t.Errorf("err = %v", err)
	}
}

type typoMessages struct {
	Email string `json:"email" validate:"required"`
}

func (typoMessages) ValidationMessages() map[string]string {
	return map[string]string{"email.reqired": "x"}
}

type unknownRuleMessage struct {
	Email string `json:"email" validate:"required"`
}

func (unknownRuleMessage) ValidationMessages() map[string]string {
	return map[string]string{"reqired": "x"}
}

type wrongFieldMessage struct {
	Email string `json:"email" validate:"required"`
}

func (wrongFieldMessage) ValidationMessages() map[string]string {
	return map[string]string{"mail.required": "x", "min": "fine: a known rule, even if unused"}
}

func TestMessageKeysAreChecked(t *testing.T) {
	for _, typ := range []reflect.Type{
		reflect.TypeFor[typoMessages](), reflect.TypeFor[unknownRuleMessage](), reflect.TypeFor[wrongFieldMessage](),
	} {
		if _, err := Compile(typ); err == nil || !strings.Contains(err.Error(), "ValidationMessages") {
			t.Errorf("%s: err = %v", typ, err)
		}
	}
}

func TestMarshalJSONAndFail(t *testing.T) {
	var errs Errors
	errs.Add("b", `say "hi"`)
	errs.Add("a", "A")
	b, err := json.Marshal(&errs)
	if err != nil || string(b) != `{"b":"say \"hi\"","a":"A"}` {
		t.Errorf("MarshalJSON = %s, %v", b, err)
	}
	var empty *Errors
	if b, _ := json.Marshal(empty); string(b) != "null" {
		t.Errorf("nil = %s", b)
	}
	err = Fail("email", "Taken.")
	var ve *Errors
	if !errors.As(err, &ve) || ve.Get("email") != "Taken." || err.Error() != "validation failed: email: Taken." {
		t.Errorf("Fail = %v", err)
	}
}

func TestNumericParameterChecks(t *testing.T) {
	for tag, want := range map[string]string{
		"size:1.5": "not a whole number",
		"between:9007199254740993,9007199254740992": "greater than",
		"in:18446744073709551616":                   "out of range",
		"max:18446744073709551616000":               "out of range",
	} {
		typ := reflect.StructOf([]reflect.StructField{{
			Name: "N", Type: reflect.TypeFor[int64](), Tag: reflect.StructTag(`validate:"` + tag + `"`),
		}})
		if _, err := Compile(typ); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", tag, err, want)
		}
	}
	type ok struct {
		N int64   `validate:"between:9007199254740992,9007199254740993"`
		F float64 `validate:"max:1e3|min:-1.5e2"`
	}
	wantValid(t, &ok{N: 9007199254740993, F: 1000})
}
