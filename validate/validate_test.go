// SPDX-License-Identifier: Apache-2.0

package validate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
)

// fieldErrors validates v and returns its messages, failing the test on
// other errors.
func fieldErrors(t *testing.T, v any) map[string]string {
	t.Helper()
	err := Struct(context.Background(), v)
	if err == nil {
		return nil
	}
	var ve *Errors
	if !errors.As(err, &ve) {
		t.Fatalf("Struct: %v", err)
	}
	return ve.FieldErrors()
}

func wantValid(t *testing.T, v any) {
	t.Helper()
	if errs := fieldErrors(t, v); errs != nil {
		t.Errorf("%+v: unexpected errors %v", v, errs)
	}
}

func wantError(t *testing.T, v any, key, msg string) {
	t.Helper()
	errs := fieldErrors(t, v)
	got, ok := errs[key]
	switch {
	case !ok:
		t.Errorf("%+v: no error for %q (got %v)", v, key, errs)
	case msg != "" && got != msg:
		t.Errorf("%+v: %s = %q, want %q", v, key, got, msg)
	}
}

func TestRequired(t *testing.T) {
	type in struct {
		Name string    `json:"name" validate:"required"`
		Age  int       `json:"age" validate:"required"`
		Opt  *int      `json:"opt" validate:"required"`
		Tags []int     `json:"tags" validate:"required"`
		When time.Time `json:"when" validate:"required"`
	}
	zero := 0
	wantValid(t, &in{Name: "a", Age: 1, Opt: &zero, Tags: []int{1}, When: time.Now()})

	errs := fieldErrors(t, &in{Name: "  "})
	for _, k := range []string{"name", "age", "opt", "tags", "when"} {
		if _, ok := errs[k]; !ok {
			t.Errorf("no error for %s: %v", k, errs)
		}
	}
	if errs["name"] != "The name field is required." {
		t.Errorf("message = %q", errs["name"])
	}
}

func TestOptionalFieldsSkipRules(t *testing.T) {
	type in struct {
		Email string   `validate:"email"`
		URL   *string  `validate:"url|min:100"`
		Tags  []string `validate:"min:2"`
	}
	wantValid(t, &in{})
	bad := "x"
	wantError(t, &in{URL: &bad}, "URL", "The url field must be a valid URL.")
}

func TestRequiredIfUnless(t *testing.T) {
	type in struct {
		Type    string `json:"type"`
		Company string `json:"company" validate:"required_if:type,business,partner"`
		Reason  string `json:"reason" validate:"required_unless:Type,personal"`
		Admin   bool   `json:"admin"`
		Code    string `json:"code" validate:"required_if:Admin,1"`
		Level   *int   `json:"level"`
		Note    string `json:"note" validate:"required_if:level,3.0"`
	}
	wantValid(t, &in{Type: "personal"})
	wantError(t, &in{Type: "business", Reason: "x"}, "company", "The company field is required when type is business, partner.")
	wantError(t, &in{Type: "other", Company: ""}, "reason", "The reason field is required unless type is personal.")
	wantError(t, &in{Type: "personal", Admin: true}, "code", "")
	three := 3
	wantError(t, &in{Type: "personal", Level: &three}, "note", "")
	wantValid(t, &in{Type: "personal", Level: nil})
}

func TestRequiredWithWithout(t *testing.T) {
	type in struct {
		Street string `json:"street"`
		City   string `json:"city" validate:"required_with:street"`
		Phone  string `json:"phone" validate:"required_without:Email"`
		Email  string `json:"email"`
	}
	wantValid(t, &in{Email: "a@b.co"})
	wantError(t, &in{Street: "Main", Email: "a@b.co"}, "city", "The city field is required when street is present.")
	wantError(t, &in{}, "phone", "The phone field is required when email is not present.")
}

func TestAccepted(t *testing.T) {
	type in struct {
		Terms bool   `validate:"accepted"`
		S     string `validate:"accepted"`
		N     int    `validate:"accepted"`
	}
	wantValid(t, &in{Terms: true, S: "Yes", N: 1})
	wantError(t, &in{S: "on", N: 1}, "Terms", "The terms field must be accepted.")
	wantError(t, &in{Terms: true, S: "nope", N: 1}, "S", "")
	wantError(t, &in{Terms: true, S: "1", N: 2}, "N", "")
}

func TestSizeRules(t *testing.T) {
	type in struct {
		Name  string   `json:"name" validate:"min:2|max:4"`
		Age   int      `json:"age" validate:"between:18,65"`
		Score float64  `json:"score" validate:"max:9.5"`
		Tags  []string `json:"tags" validate:"size:2"`
		Code  string   `json:"code" validate:"size:3"`
		Count uint     `json:"count" validate:"min:1"`
	}
	ok := in{Name: "日本語", Age: 18, Score: 9.5, Tags: []string{"a", "b"}, Code: "abc", Count: 1}
	wantValid(t, &ok)

	cases := []struct {
		mod      func(*in)
		key, msg string
	}{
		{func(v *in) { v.Name = "a" }, "name", "The name field must be at least 2 characters."},
		{func(v *in) { v.Name = "abcde" }, "name", "The name field must not be greater than 4 characters."},
		{func(v *in) { v.Age = 66 }, "age", "The age field must be between 18 and 65."},
		{func(v *in) { v.Age = 0 }, "age", ""},
		{func(v *in) { v.Score = 9.6 }, "score", "The score field must not be greater than 9.5."},
		{func(v *in) { v.Tags = []string{"a"} }, "tags", "The tags field must contain 2 items."},
		{func(v *in) { v.Code = "ab" }, "code", "The code field must be 3 characters."},
		{func(v *in) { v.Count = 0 }, "count", "The count field must be at least 1."},
	}
	for _, c := range cases {
		v := ok
		c.mod(&v)
		wantError(t, &v, c.key, c.msg)
	}
}

func TestStringFormats(t *testing.T) {
	cases := []struct {
		rule      string
		good, bad []string
	}{
		{"email", []string{"a@b.co", "first.last+tag@sub.example.org", "o'brien@x-y.io", "josé@exämple.de"}, []string{"a", "a@b", "@b.co", "A <a@b.co>", "a@b..co", "a b@c.co", "a@.co", ".a@b.co", "a.@b.co", "a..b@c.co", "a@b@c.co", "a@-b.co", "a@b_c.co", `"a"@b.co`, "a@[127.0.0.1]", "a@b.co\n", "\xffa@b.co"}},
		{"url", []string{"https://example.com/x?y=1", "http://h"}, []string{"example.com", "/path", "javascript:alert(1)", "javascript://x/%0Aalert(1)", "data://x/text/html,<script>", "ftp://h", "http:// x"}},
		{"url:ftp,HTTPS", []string{"ftp://h", "https://h"}, []string{"http://h"}},
		{"url:https", []string{"https://x.io"}, []string{"http://x.io"}},
		{"uuid", []string{"123e4567-e89b-12d3-a456-426614174000", "123E4567-E89B-12D3-A456-426614174000"}, []string{"123e4567e89b12d3a456426614174000", "123e4567-e89b-12d3-a456-42661417400g"}},
		{"alpha", []string{"abcÉé", "日本"}, []string{"ab1", "a b"}},
		{"alpha_num", []string{"abc123", "é٣"}, []string{"a-b", "a_b"}},
		{"alpha_dash", []string{"a-b_c1"}, []string{"a b", "a.b"}},
		{"ascii", []string{"Hello, world!"}, []string{"héllo"}},
		{"numeric", []string{"12", "-1.5", "1e3"}, []string{"1,000", "abc", "NaN", "Inf", "0x10", "1_000"}},
		{"integer", []string{"12", "-3"}, []string{"1.5", "abc", "1_000"}},
		{"lowercase", []string{"abc 1"}, []string{"aBc"}},
		{"uppercase", []string{"ABC 1"}, []string{"aBC"}},
		{"starts_with:foo, bar", []string{"foobar", "bar"}, []string{"baz"}},
		{"ends_with:.go", []string{"main.go"}, []string{"main.rs"}},
		{"ip", []string{"127.0.0.1", "::1"}, []string{"256.0.0.1", "host", "fe80::1%<script>", "fe80::1%eth0"}},
		{"ipv4", []string{"10.0.0.1"}, []string{"::1"}},
		{"ipv6", []string{"2001:db8::1"}, []string{"10.0.0.1"}},
		{"json", []string{`{"a":1}`, `[1]`, `"s"`}, []string{`{a:1}`}},
		{"date", []string{"2026-02-28"}, []string{"2026-02-30", "28/02/2026"}},
		{"datetime", []string{"2026-02-28T10:00:00Z", "2026-02-28T10:00:00+06:00"}, []string{"2026-02-28 10:00:00"}},
	}
	for _, c := range cases {
		typ := reflect.StructOf([]reflect.StructField{{
			Name: "V", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(`validate:"` + c.rule + `"`),
		}})
		plan, err := Compile(typ)
		if err != nil {
			t.Fatalf("%s: %v", c.rule, err)
		}
		for _, s := range c.good {
			v := reflect.New(typ)
			v.Elem().Field(0).SetString(s)
			if err := plan.Validate(context.Background(), v.Interface()); err != nil {
				t.Errorf("%s(%q): %v", c.rule, s, err)
			}
		}
		for _, s := range c.bad {
			v := reflect.New(typ)
			v.Elem().Field(0).SetString(s)
			if err := plan.Validate(context.Background(), v.Interface()); err == nil {
				t.Errorf("%s(%q) passed", c.rule, s)
			}
		}
	}
}

func TestFormatsOnSlices(t *testing.T) {
	type in struct {
		Emails []string  `json:"emails" validate:"required|email"`
		Opt    []*string `json:"opt" validate:"uuid"`
	}
	wantValid(t, &in{Emails: []string{"a@b.co", "c@d.io"}, Opt: []*string{nil}})
	wantError(t, &in{Emails: []string{"a@b.co", "nope"}}, "emails", "The emails field must be a valid email address.")
}

func TestChoiceRules(t *testing.T) {
	type in struct {
		Role   string   `json:"role" validate:"in:reader,editor"`
		Level  int      `json:"level" validate:"in:1,2,3.0"`
		Flag   bool     `json:"flag" validate:"in:true"`
		Colors []string `json:"colors" validate:"in:red,green|distinct"`
		Name   string   `json:"name" validate:"not_in:admin,root"`
		IDs    []int    `json:"ids" validate:"distinct"`
	}
	wantValid(t, &in{Role: "editor", Level: 3, Flag: true, Colors: []string{"red", "green"}, Name: "sam", IDs: []int{1, 2}})
	base := in{Role: "reader", Level: 1, Flag: true, Name: "x"}
	cases := []struct {
		mod      func(*in)
		key, msg string
	}{
		{func(v *in) { v.Role = "owner" }, "role", "The selected role is invalid."},
		{func(v *in) { v.Level = 4 }, "level", ""},
		{func(v *in) { v.Flag = false }, "flag", ""},
		{func(v *in) { v.Colors = []string{"red", "blue"} }, "colors", "The selected colors is invalid."},
		{func(v *in) { v.Colors = []string{"red", "red"} }, "colors", "The colors field has a duplicate value."},
		{func(v *in) { v.Name = "root" }, "name", ""},
		{func(v *in) { v.IDs = []int{1, 1} }, "ids", ""},
	}
	for _, c := range cases {
		v := base
		c.mod(&v)
		wantError(t, &v, c.key, c.msg)
	}
}

func TestComparisonRules(t *testing.T) {
	type in struct {
		Password             string  `json:"password" validate:"confirmed"`
		PasswordConfirmation string  `json:"password_confirmation"`
		Email                string  `json:"email"`
		Backup               string  `json:"backup_email" validate:"different:email"`
		Repeat               *string `json:"repeat" validate:"same:Email"`
	}
	same := "a@b.co"
	wantValid(t, &in{Password: "x", PasswordConfirmation: "x", Email: "a@b.co", Backup: "c@d.co", Repeat: &same})
	wantError(t, &in{Password: "x", PasswordConfirmation: "y"}, "password", "The password field confirmation does not match.")
	wantError(t, &in{Email: "a@b.co", Backup: "a@b.co"}, "backup_email", "The backup email field and email must be different.")
	other := "z"
	wantError(t, &in{Email: "a@b.co", Repeat: &other}, "repeat", "The repeat field must match email.")

	type byKey struct {
		Secret string `json:"secret" validate:"confirmed"`
		Again  string `json:"secret_confirmation"`
	}
	wantValid(t, &byKey{Secret: "s", Again: "s"})
	wantError(t, &byKey{Secret: "s", Again: "t"}, "secret", "")
}

func TestTimeRules(t *testing.T) {
	type in struct {
		Start time.Time  `json:"start" validate:"after:now"`
		End   *time.Time `json:"end" validate:"after:Start"`
		Due   time.Time  `json:"due" validate:"before_or_equal:end"`
	}
	now := time.Now()
	later := now.Add(2 * time.Hour)
	wantValid(t, &in{Start: now.Add(time.Hour), End: &later, Due: later})
	wantError(t, &in{Start: now.Add(-time.Hour)}, "start", "The start field must be a date after now.")
	early := now
	wantError(t, &in{Start: now.Add(time.Hour), End: &early}, "end", "The end field must be a date after start.")
	wantError(t, &in{Start: now.Add(time.Hour), End: &later, Due: later.Add(time.Second)}, "due", "")
	// Nothing to compare against: the other field's own rules report it.
	wantValid(t, &in{Start: now.Add(time.Hour), Due: later})
	// "now" is the app's clock.
	then := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	ctx := anetos.WithClock(context.Background(), func() time.Time { return then })
	if err := Struct(ctx, &in{Start: then.Add(time.Hour)}); err != nil {
		t.Errorf("after:now on the app's clock: %v", err)
	}
	if err := Struct(ctx, &in{Start: then.Add(-time.Hour)}); err == nil {
		t.Error("before the app's clock: no error")
	}
}

func TestNested(t *testing.T) {
	type address struct {
		City string `json:"city" validate:"required"`
	}
	type item struct {
		Name string `json:"name" validate:"required"`
		Qty  int    `json:"qty" validate:"min:1"`
	}
	type in struct {
		Address address          `json:"address"`
		Billing *address         `json:"billing"`
		Items   []item           `json:"items" validate:"required|max:3"`
		ByName  map[string]*item `json:"by_name"`
		Skipped address          `json:"skipped" validate:"-"`
		Tags    []string         `json:"tags"`
	}
	errs := fieldErrors(t, &in{
		Items:  []item{{Name: "a", Qty: 1}, {Qty: 0}},
		ByName: map[string]*item{"b": {Name: "b"}, "a": nil},
	})
	want := map[string]string{
		"address.city":  "The city field is required.",
		"items.1.name":  "The name field is required.",
		"items.1.qty":   "The qty field must be at least 1.",
		"by_name.b.qty": "The qty field must be at least 1.",
	}
	if !reflect.DeepEqual(errs, want) {
		t.Errorf("errors = %v\nwant %v", errs, want)
	}
	errs = fieldErrors(t, &in{Address: address{City: "x"}, Billing: &address{}})
	if _, ok := errs["billing.city"]; !ok {
		t.Errorf("no billing.city: %v", errs)
	}
	if _, ok := errs["items"]; !ok {
		t.Errorf("no items: %v", errs)
	}
}

type tree struct {
	Name     string  `json:"name" validate:"required"`
	Children []*tree `json:"children"`
}

func TestRecursiveTypes(t *testing.T) {
	errs := fieldErrors(t, &tree{Name: "root", Children: []*tree{{Name: "a", Children: []*tree{{}}}}})
	if _, ok := errs["children.0.children.0.name"]; !ok {
		t.Errorf("errors = %v", errs)
	}
}

type Embedded struct {
	ID string `json:"id" validate:"required|uuid"`
}

type withEmbedded struct {
	Embedded
	Other string `json:"other" validate:"required_with:id"`
}

func TestEmbedded(t *testing.T) {
	errs := fieldErrors(t, &withEmbedded{})
	if errs["id"] == "" || len(errs) != 1 {
		t.Errorf("errors = %v", errs)
	}
	wantError(t, &withEmbedded{Embedded: Embedded{ID: "123e4567-e89b-12d3-a456-426614174000"}}, "other", "")
}

func TestOrderAndFirstFailure(t *testing.T) {
	type in struct {
		B string `json:"b" validate:"required|email"`
		A string `json:"a" validate:"min:3|email"`
	}
	err := Struct(context.Background(), &in{A: "x"})
	var ve *Errors
	if !errors.As(err, &ve) {
		t.Fatal(err)
	}
	if got := ve.Keys(); !reflect.DeepEqual(got, []string{"b", "a"}) {
		t.Errorf("keys = %v", got)
	}
	if got := ve.Get("a"); !strings.Contains(got, "at least 3") {
		t.Errorf("a = %q, want the first failing rule", got)
	}
	if !strings.HasPrefix(err.Error(), "validation failed: b: The b field is required.; a: ") {
		t.Errorf("Error() = %q", err.Error())
	}
	if ve.HTTPStatus() != 422 {
		t.Errorf("status = %d", ve.HTTPStatus())
	}
}

type labeledInput struct {
	FirstName string `json:"first_name" validate:"required"`
	Email     string `json:"email" label:"email address" validate:"required|email"`
	Code      string `json:"code" validate:"min:3"`
	CamelCase string `validate:"required"`
	HTTPProxy string `validate:"required"`
}

func (labeledInput) ValidationMessages() map[string]string {
	return map[string]string{
		"email.required": "We need your {label}.",
		"min":            "{label}: at least {0}, got too few.",
	}
}

func TestLabelsAndMessages(t *testing.T) {
	errs := fieldErrors(t, &labeledInput{Code: "ab"})
	want := map[string]string{
		"first_name": "The first name field is required.",
		"email":      "We need your email address.",
		"code":       "code: at least 3, got too few.",
		"CamelCase":  "The camel case field is required.",
		"HTTPProxy":  "The http proxy field is required.",
	}
	if !reflect.DeepEqual(errs, want) {
		t.Errorf("errors = %v\nwant %v", errs, want)
	}
}

func TestFieldKeys(t *testing.T) {
	type in struct {
		A string `json:"a_json" form:"a_form" validate:"required"`
		B string `form:"b_form" validate:"required"`
		C string `query:"c" validate:"required"`
		D string `path:"d" validate:"required"`
		E string `header:"X-E" validate:"required"`
		F string `json:"-" query:"f" validate:"required"`
		G string `json:",omitempty" validate:"required"`
	}
	errs := fieldErrors(t, &in{})
	for _, k := range []string{"a_json", "b_form", "c", "d", "X-E", "f", "G"} {
		if _, ok := errs[k]; !ok {
			t.Errorf("missing %q in %v", k, errs)
		}
	}
}

func TestCustomRules(t *testing.T) {
	Register("test_even", "The {label} field must be even.", func(_ context.Context, f Field) (bool, error) {
		n, _ := f.Value.(int)
		return n%2 == 0, nil
	})
	Register("test_prefix", "", func(_ context.Context, f Field) (bool, error) {
		s, _ := f.Value.(string)
		return strings.HasPrefix(s, f.Params[0]), nil
	})
	boom := errors.New("db down")
	Register("test_fail", "", func(context.Context, Field) (bool, error) { return false, boom })
	var seen Field
	Register("test_spy", "", func(_ context.Context, f Field) (bool, error) { seen = f; return true, nil })

	type in struct {
		N int     `json:"n" validate:"test_even"`
		S string  `json:"s" validate:"test_prefix:ab"`
		P *string `json:"p" validate:"test_spy"`
	}
	wantValid(t, &in{N: 2, S: "abc"})
	wantError(t, &in{N: 3, S: "abc"}, "n", "The n field must be even.")
	wantError(t, &in{S: "x"}, "s", "The s field is invalid.")

	p := "val"
	wantValid(t, &in{S: "ab", P: &p})
	if seen.Key != "p" || seen.Label != "p" || seen.Value != "val" {
		t.Errorf("Field = %+v", seen)
	}
	if _, ok := seen.Parent.(in); !ok {
		t.Errorf("Parent = %T", seen.Parent)
	}

	type failing struct {
		S string `validate:"test_fail"`
	}
	if err := Struct(context.Background(), &failing{S: "x"}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the rule's error", err)
	}
	wantValid(t, &failing{}) // empty: skipped

	for _, bad := range []struct {
		name string
		fn   Rule
	}{{"", func(context.Context, Field) (bool, error) { return true, nil }}, {"a,b", nil}, {"required", func(context.Context, Field) (bool, error) { return true, nil }}, {"test_even", func(context.Context, Field) (bool, error) { return true, nil }}, {"ok_name", nil}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Register(%q) did not panic", bad.name)
				}
			}()
			Register(bad.name, "", bad.fn)
		}()
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		typ  any
		want string
	}{
		{struct {
			A string `validate:"nope"`
		}{}, `unknown rule "nope"`},
		{struct {
			A int `validate:"email"`
		}{}, `does not apply to int`},
		{struct {
			A string `validate:"min"`
		}{}, `needs 1 parameter`},
		{struct {
			A string `validate:"min:1,2"`
		}{}, `takes 1 parameter`},
		{struct {
			A string `validate:"min:-1"`
		}{}, `non-negative whole number`},
		{struct {
			A int `validate:"max:abc"`
		}{}, `"abc" is not a number`},
		{struct {
			A int `validate:"between:5,1"`
		}{}, `greater than`},
		{struct {
			A int `validate:"in:a"`
		}{}, `not a number`},
		{struct {
			A string `validate:"required_if:Missing,x"`
		}{}, `no field "Missing"`},
		{struct {
			A string `validate:"confirmed"`
		}{}, `needs a field named AConfirmation`},
		{struct {
			A string `validate:"same:B"`
			B int
		}{}, `not a string`},
		{struct {
			A time.Time `validate:"after:B"`
			B string
		}{}, `not a time.Time`},
		{struct {
			A string `validate:"max_size:1MB"`
		}{}, `does not apply`},
		{struct {
			a string `validate:"required"`
		}{}, `unexported`},
		{struct {
			A []map[string]int `validate:"distinct"`
		}{}, `can't be compared`},
		{struct {
			A bool `validate:"required_if:B,x"`
			B bool
		}{}, `not a boolean`},
		{struct {
			A string `validate:"url|accepted:1"`
		}{}, `takes no parameters`},
		{struct {
			A string `validate:"min:"`
		}{}, `empty parameter`},
		{struct {
			A string `validate:"between:1,,3"`
		}{}, `empty parameter`},
		{struct {
			A string `validate:"required,email"`
		}{}, `malformed rule "required,email"`},
		{struct {
			A string `validate:"max=3"`
		}{}, `malformed rule`},
		{struct {
			A string `validate:":3"`
		}{}, `malformed rule`},
	}
	for _, c := range cases {
		_, err := Compile(reflect.TypeOf(c.typ))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%T: err = %v, want %q", c.typ, err, c.want)
		}
	}
	if _, err := Compile(reflect.TypeFor[int]()); err == nil {
		t.Error("Compile(int) succeeded")
	}
	defer func() {
		if recover() == nil {
			t.Error("MustCompile did not panic")
		}
	}()
	MustCompile(reflect.TypeFor[struct {
		A string `validate:"nope"`
	}]())
}

func TestStructArguments(t *testing.T) {
	type in struct {
		A string `validate:"required"`
	}
	if err := Struct(context.Background(), (*in)(nil)); err == nil {
		t.Error("nil pointer accepted")
	}
	if err := Struct(context.Background(), 3); err == nil {
		t.Error("int accepted")
	}
	if errs := fieldErrors(t, in{}); errs == nil {
		t.Error("value not validated")
	}
	p := MustCompile(reflect.TypeFor[in]())
	if err := p.Validate(context.Background(), &struct{}{}); err == nil {
		t.Error("wrong type accepted")
	}
	if p.Empty() || !MustCompile(reflect.TypeFor[struct{ A string }]()).Empty() {
		t.Error("Empty wrong")
	}
	if p != MustCompile(reflect.TypeFor[in]()) {
		t.Error("plan not cached")
	}
}

func TestErrorsByHand(t *testing.T) {
	var errs Errors
	if errs.Err() != nil || (*Errors)(nil).Err() != nil {
		t.Error("empty Errors is not nil")
	}
	errs.Add("email", "taken")
	errs.Add("email", "second")
	errs.Add("name", "short")
	if errs.Len() != 2 || errs.Get("email") != "taken" || !errs.Has("name") || errs.Has("x") {
		t.Errorf("errors = %v", errs.FieldErrors())
	}
	m := errs.FieldErrors()
	m["email"] = "changed"
	if errs.Get("email") != "taken" {
		t.Error("FieldErrors is not a copy")
	}
	if errs.Err() == nil {
		t.Error("Err() = nil")
	}
}

func TestHumanize(t *testing.T) {
	for in, want := range map[string]string{
		"first_name": "first name", "firstName": "first name", "first-name": "first name",
		"ID": "id", "UserID": "user id", "address.city": "city", "URLPath": "url path", "X-Request-Id": "x request id", "a__b": "a b", "Page2Title": "page2 title",
	} {
		if got := humanize(in); got != want {
			t.Errorf("humanize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContextPassedToRules(t *testing.T) {
	type key struct{}
	Register("test_ctx", "", func(ctx context.Context, _ Field) (bool, error) {
		return ctx.Value(key{}) == "yes", nil
	})
	type in struct {
		A string `validate:"test_ctx"`
	}
	ctx := context.WithValue(context.Background(), key{}, "yes")
	if err := Struct(ctx, &in{A: "x"}); err != nil {
		t.Error(err)
	}
}

func TestTagSpacing(t *testing.T) {
	type in struct {
		Role string `validate:" required | in: reader , editor || "`
	}
	wantValid(t, &in{Role: "editor"})
	wantError(t, &in{Role: "x"}, "Role", "The selected role is invalid.")
}

func TestFormat(t *testing.T) {
	for _, c := range []struct{ msg, want string }{
		{"plain", "plain"},
		{"The {label} field", "The name field"},
		{"{0}-{1} of {list}", "a-b of a, b"},
		{"{2} {x} {} {label", "{2} {x} {} {label"},
		{"{{label}}", "{name}"},
	} {
		if got := format(c.msg, "name", []string{"a", "b"}); got != c.want {
			t.Errorf("format(%q) = %q, want %q", c.msg, got, c.want)
		}
	}
}

func TestDistinctLongLists(t *testing.T) {
	type in struct {
		IDs  []int     `validate:"distinct"`
		Ptrs []*string `validate:"distinct"`
	}
	long := make([]int, 40)
	for i := range long {
		long[i] = i
	}
	wantValid(t, &in{IDs: long})
	long[39] = 3
	wantError(t, &in{IDs: long}, "IDs", "")
	a, b, a2 := "a", "b", "a"
	wantValid(t, &in{Ptrs: []*string{&a, nil, &b, nil}})
	wantError(t, &in{Ptrs: []*string{&a, &b, &a2}}, "Ptrs", "")
}

func TestRequiredStructValues(t *testing.T) {
	type inner struct{ A string }
	type in struct {
		Nested inner `json:"nested" validate:"required"`
	}
	wantError(t, &in{}, "nested", "The nested field is required.")
	wantValid(t, &in{Nested: inner{A: "x"}})
}
