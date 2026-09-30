// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"math/big"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/factory"
)

// ---- Status ----

// AssertStatus checks the status code.
func (r *Response) AssertStatus(want int) *Response {
	r.app.t.Helper()
	if r.StatusCode != want {
		r.errorf("status %d, want %d%s", r.StatusCode, want, r.excerpt())
	}
	return r
}

// AssertOK checks for 200 OK.
func (r *Response) AssertOK() *Response {
	r.app.t.Helper()
	return r.AssertStatus(http.StatusOK)
}

// AssertCreated checks for 201 Created.
func (r *Response) AssertCreated() *Response {
	r.app.t.Helper()
	return r.AssertStatus(http.StatusCreated)
}

// AssertNoContent checks for 204 No Content.
func (r *Response) AssertNoContent() *Response {
	r.app.t.Helper()
	return r.AssertStatus(http.StatusNoContent)
}

// AssertNotFound checks for 404 Not Found.
func (r *Response) AssertNotFound() *Response {
	r.app.t.Helper()
	return r.AssertStatus(http.StatusNotFound)
}

// AssertForbidden checks for 403 Forbidden.
func (r *Response) AssertForbidden() *Response {
	r.app.t.Helper()
	return r.AssertStatus(http.StatusForbidden)
}

// AssertUnprocessable checks for 422 Unprocessable Content, the status of
// failed validation for API clients.
func (r *Response) AssertUnprocessable() *Response {
	r.app.t.Helper()
	return r.AssertStatus(http.StatusUnprocessableEntity)
}

// AssertRedirect checks for a redirect (3xx) to path, such as "/posts" or
// "/posts?page=2".
func (r *Response) AssertRedirect(path string) *Response {
	r.app.t.Helper()
	loc := r.Header.Get("Location")
	if r.StatusCode < 300 || r.StatusCode > 399 {
		r.errorf("status %d, want a redirect to %s%s", r.StatusCode, path, r.excerpt())
		return r
	}
	if local(loc) != local(path) {
		r.errorf("redirect to %s, want %s", loc, path)
	}
	return r
}

// AssertRedirectRoute checks for a redirect to the named route, with args
// as for web.Router.URL.
func (r *Response) AssertRedirectRoute(name string, args ...any) *Response {
	r.app.t.Helper()
	path, err := r.app.router.URL(name, args...)
	if err != nil {
		r.app.t.Fatalf("anetostest: AssertRedirectRoute: %v", err)
	}
	return r.AssertRedirect(path)
}

// local drops the test site's scheme and host from an URL.
func local(u string) string {
	if rest, ok := strings.CutPrefix(u, "http://example.test"); ok && (rest == "" || strings.HasPrefix(rest, "/") || strings.HasPrefix(rest, "?")) {
		if rest == "" || rest[0] == '?' {
			rest = "/" + rest
		}
		return rest
	}
	return u
}

// ---- Headers and body ----

// AssertHeader checks that the response has the header with the value.
func (r *Response) AssertHeader(name, want string) *Response {
	r.app.t.Helper()
	vs, ok := r.Header[http.CanonicalHeaderKey(name)]
	switch {
	case !ok:
		r.errorf("no %s header, want %q", name, want)
	case !slices.Contains(vs, want):
		r.errorf("%s header %q, want %q", name, vs, want)
	}
	return r
}

// AssertSee checks that the body contains each text, as it is or
// HTML-escaped as templates write it: AssertSee(`"Go" & you`) also finds
// "&#34;Go&#34; &amp; you". For JSON, use [Response.AssertJSONPath].
func (r *Response) AssertSee(texts ...string) *Response {
	r.app.t.Helper()
	for _, text := range texts {
		if !r.contains(text) {
			r.errorf("body doesn't contain %q%s", text, r.excerpt())
		}
	}
	return r
}

// AssertDontSee checks that the body contains none of texts, as they are
// or HTML-escaped.
func (r *Response) AssertDontSee(texts ...string) *Response {
	r.app.t.Helper()
	for _, text := range texts {
		if r.contains(text) {
			r.errorf("body contains %q%s", text, r.excerpt())
		}
	}
	return r
}

func (r *Response) contains(text string) bool {
	for _, form := range []string{text, html.EscapeString(text), templateEscape.Replace(text)} {
		if bytes.Contains(r.Body, []byte(form)) {
			return true
		}
	}
	return false
}

// templateEscape escapes like html/template in text, which also escapes +.
var templateEscape = strings.NewReplacer(`&`, "&amp;", `'`, "&#39;", `<`, "&lt;", `>`, "&gt;", `"`, "&#34;", `+`, "&#43;")

// AssertJSON checks that the body is JSON equal to want (encoded as JSON
// to compare): a struct, a map or a slice.
//
//	res.AssertJSON(map[string]any{"id": 1, "title": "Hello"})
func (r *Response) AssertJSON(want any) *Response {
	r.app.t.Helper()
	got, ok := r.decoded()
	if !ok {
		return r
	}
	w, err := normalize(want)
	if err != nil {
		r.app.t.Fatalf("anetostest: AssertJSON: encode want: %v", err)
	}
	if !jsonEqual(got, w) {
		r.errorf("JSON body\n  %s\nwant\n  %s", compact(got), compact(w))
	}
	return r
}

// AssertJSONPath checks the value at path in the JSON body: keys and array
// indexes separated by dots, as in "data.0.title". want is compared after
// encoding it as JSON, so 1 matches 1.0 and a struct matches an object.
func (r *Response) AssertJSONPath(path string, want any) *Response {
	r.app.t.Helper()
	got, ok := r.decoded()
	if !ok {
		return r
	}
	v, err := lookup(got, path)
	if err != nil {
		r.errorf("JSON %s: %v%s", path, err, r.excerpt())
		return r
	}
	w, err := normalize(want)
	if err != nil {
		r.app.t.Fatalf("anetostest: AssertJSONPath: encode want: %v", err)
	}
	if !jsonEqual(v, w) {
		r.errorf("JSON %s = %s, want %s", path, compact(v), compact(w))
	}
	return r
}

func (r *Response) decoded() (any, bool) {
	r.app.t.Helper()
	v, err := decodeJSON(r.Body)
	if err != nil {
		r.errorf("body isn't JSON: %v%s", err, r.excerpt())
		return nil, false
	}
	return v, true
}

// normalize encodes v as JSON and decodes it, with numbers as json.Number
// so they compare exactly.
func normalize(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return decodeJSON(b)
}

func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("invalid JSON: data after the value")
	}
	return out, nil
}

// jsonEqual compares decoded JSON; numbers by value, so 2 equals 2.0.
func jsonEqual(a, b any) bool {
	switch a := a.(type) {
	case json.Number:
		b, ok := b.(json.Number)
		if !ok {
			return false
		}
		x, okx := new(big.Rat).SetString(a.String())
		y, oky := new(big.Rat).SetString(b.String())
		return okx && oky && x.Cmp(y) == 0
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for k, v := range a {
			w, ok := b[k]
			if !ok || !jsonEqual(v, w) {
				return false
			}
		}
		return true
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !jsonEqual(a[i], b[i]) {
				return false
			}
		}
		return true
	default:
		return a == b // string, bool, nil
	}
}

func compact(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func lookup(v any, path string) (any, error) {
	if path == "" {
		return v, nil
	}
	for key := range strings.SplitSeq(path, ".") {
		switch c := v.(type) {
		case map[string]any:
			next, ok := c[key]
			if !ok {
				return nil, fmt.Errorf("no key %q", key)
			}
			v = next
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(c) {
				return nil, fmt.Errorf("no index %q in an array of %d", key, len(c))
			}
			v = c[i]
		default:
			return nil, fmt.Errorf("%q: %s isn't an object or array", key, compact(v))
		}
	}
	return v, nil
}

// ---- Validation and session ----

// AssertValidationErrors checks that validation failed with an error for
// each of fields (the form's or the JSON body's field names; none: any
// error). For an API client, the response must be a 422 (or 400) problem
// with the fields in its "errors" member; for a browser's form, a redirect
// back with the errors flashed to the session.
func (r *Response) AssertValidationErrors(fields ...string) *Response {
	r.app.t.Helper()
	have, ok := r.validationErrors()
	if !ok {
		return r
	}
	if len(have) == 0 {
		r.errorf("no validation errors, want errors for %s", strings.Join(fields, ", "))
		return r
	}
	for _, f := range fields {
		if _, ok := have[f]; !ok {
			r.errorf("no validation error for %q; errors: %v", f, have)
		}
	}
	return r
}

// AssertNoValidationErrors checks that the request didn't fail validation:
// not a 422, no 400 with field errors, and no errors flashed to the
// session.
func (r *Response) AssertNoValidationErrors() *Response {
	r.app.t.Helper()
	if r.StatusCode == http.StatusUnprocessableEntity {
		r.errorf("status 422%s", r.excerpt())
		return r
	}
	if r.StatusCode == http.StatusBadRequest {
		if errs, _ := r.problemErrors(); len(errs) > 0 {
			r.errorf("status 400 with field errors %v", errs)
			return r
		}
	}
	if r.app.sessions != nil {
		if errs := r.app.Session().Errors(); len(errs) > 0 {
			r.errorf("validation errors in the session: %v", errs)
		}
	}
	return r
}

// problemErrors returns the "errors" member of a problem response.
func (r *Response) problemErrors() (map[string]string, error) {
	var p struct {
		Errors map[string]string `json:"errors"`
	}
	err := json.Unmarshal(r.Body, &p)
	return p.Errors, err
}

// validationErrors returns the field errors of a problem response, or
// those flashed to the session by a redirect.
func (r *Response) validationErrors() (map[string]string, bool) {
	r.app.t.Helper()
	switch {
	case r.StatusCode == http.StatusUnprocessableEntity || r.StatusCode == http.StatusBadRequest:
		p, err := r.problemErrors()
		if err != nil {
			r.errorf("status %d, and the body isn't problem JSON (%v); request JSON (GetJSON, PostJSON, …) to check the errors%s", r.StatusCode, err, r.excerpt())
			return nil, false
		}
		return p, true
	case r.StatusCode >= 300 && r.StatusCode <= 399:
		if r.app.sessions == nil {
			r.errorf("a redirect, and the app has no sessions to hold validation errors")
			return nil, false
		}
		have := map[string]string{}
		for _, e := range r.app.Session().Errors() {
			if _, ok := have[e.Field]; !ok {
				have[e.Field] = e.Message
			}
		}
		return have, true
	}
	r.errorf("status %d, want 422 (API) or a redirect back (forms)%s", r.StatusCode, r.excerpt())
	return nil, false
}

// AssertSessionHas checks that the session the next request will carry
// has key and, if a value is given, that it holds that value (compared as
// JSON).
//
//	res.AssertSessionHas("status", "Post created.")
func (r *Response) AssertSessionHas(key string, value ...any) *Response {
	r.app.t.Helper()
	if len(value) > 1 {
		r.app.t.Fatalf("anetostest: AssertSessionHas: one value, got %d", len(value))
	}
	s := r.app.Session()
	if !s.Has(key) {
		r.errorf("session has no %q", key)
		return r
	}
	if len(value) == 1 {
		var raw json.RawMessage
		s.Get(key, &raw)
		got, _ := decodeJSON(raw)
		want, err := normalize(value[0])
		if err != nil {
			r.app.t.Fatalf("anetostest: AssertSessionHas: encode the value: %v", err)
		}
		if !jsonEqual(got, want) {
			r.errorf("session %q = %s, want %s", key, compact(got), compact(want))
		}
	}
	return r
}

// AssertSessionMissing checks that the session the next request will
// carry doesn't have key.
func (r *Response) AssertSessionMissing(key string) *Response {
	r.app.t.Helper()
	if r.app.Session().Has(key) {
		r.errorf("session has %q", key)
	}
	return r
}

func (r *Response) errorf(format string, args ...any) {
	r.app.t.Helper()
	r.app.t.Errorf("%s %s: "+format, append([]any{r.Request.Method, r.Request.URL.RequestURI()}, args...)...)
}

// ---- Database ----

// AssertDatabaseHas checks that a T row matches conds, as db.Query[T]
// sees it (soft-deleted rows don't count; see [AssertSoftDeleted]).
//
//	anetostest.AssertDatabaseHas[models.Post](app, models.PostCols.Title.Eq("Hello"))
func AssertDatabaseHas[T any](a *App, conds ...db.Expr) {
	a.t.Helper()
	if n := count[T](a, conds); n == 0 {
		a.t.Errorf("anetostest: no %s row matches", typeName[T]())
	}
}

// AssertDatabaseMissing checks that no T row matches conds.
func AssertDatabaseMissing[T any](a *App, conds ...db.Expr) {
	a.t.Helper()
	if n := count[T](a, conds); n != 0 {
		a.t.Errorf("anetostest: %d %s rows match, want none", n, typeName[T]())
	}
}

// AssertDatabaseCount checks the number of T rows matching conds.
func AssertDatabaseCount[T any](a *App, want int64, conds ...db.Expr) {
	a.t.Helper()
	if n := count[T](a, conds); n != want {
		a.t.Errorf("anetostest: %d %s rows match, want %d", n, typeName[T](), want)
	}
}

// AssertSoftDeleted checks that a soft-deleted T row matches conds. T must
// embed db.SoftDeletes.
func AssertSoftDeleted[T any](a *App, conds ...db.Expr) {
	a.t.Helper()
	ok, err := db.Query[T](a.ctx).OnlyTrashed().Where(conds...).Exists()
	if err != nil {
		a.t.Fatalf("anetostest: AssertSoftDeleted[%s]: %v", typeName[T](), err)
	}
	if !ok {
		a.t.Errorf("anetostest: no soft-deleted %s row matches", typeName[T]())
	}
}

func count[T any](a *App, conds []db.Expr) int64 {
	a.t.Helper()
	n, err := db.Query[T](a.ctx).Where(conds...).Count()
	if err != nil {
		a.t.Fatalf("anetostest: count %s rows: %v", typeName[T](), err)
	}
	return n
}

func typeName[T any]() string { return reflect.TypeFor[T]().String() }

// Create inserts a row made by f, in the test's database and transaction.
//
//	post := anetostest.Create(app, factories.Post.With(func(p *models.Post) { p.Draft = true }))
func Create[T any](a *App, f *factory.Factory[T]) T {
	a.t.Helper()
	row, err := f.Create(a.ctx)
	if err != nil {
		a.t.Fatalf("anetostest: create a %s: %v", typeName[T](), err)
	}
	return row
}

// CreateMany inserts n rows made by f.
func CreateMany[T any](a *App, f *factory.Factory[T], n int) []T {
	a.t.Helper()
	rows, err := f.CreateMany(a.ctx, n)
	if err != nil {
		a.t.Fatalf("anetostest: create %d %s rows: %v", n, typeName[T](), err)
	}
	return rows
}
