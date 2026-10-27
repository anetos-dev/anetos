// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/web/inertia"
)

// AsInertia makes later requests visits of Inertia's client (web/inertia):
// they carry X-Inertia, the app's asset version and the client's Accept
// header, and get the page object as JSON; a failed post (PostJSON) is
// redirected back with its errors, as the client's is. Without it, a GET is a first visit, whose HTML holds
// the page object; both work with [Response.AssertInertia].
//
//	app.AsInertia().Get("/posts").AssertInertia("Posts/Index")
func (a *App) AsInertia() *App {
	a.t.Helper()
	in, err := anetos.Resolve[*inertia.Inertia](a.App)
	if err != nil {
		a.t.Fatalf("anetostest: AsInertia needs Inertia (inertia.New in setup): %v", err)
	}
	a.headers.Set("X-Inertia", "true")
	a.headers.Set("X-Requested-With", "XMLHttpRequest")
	a.headers.Set("Accept", "text/html, application/xhtml+xml")
	a.headers.Set("X-Inertia-Version", in.Version())
	return a
}

// AssertInertia checks that the response is the Inertia page component
// (a visit's page object, or a first visit's HTML holding it).
func (r *Response) AssertInertia(component string) *Response {
	r.app.t.Helper()
	p, err := inertia.ReadPage(r.Body)
	switch {
	case err != nil:
		r.errorf("%v (status %d): %s", err, r.StatusCode, r.excerpt())
	case p.Component != component:
		r.errorf("got the Inertia page %q, want %q", p.Component, component)
	}
	return r
}

// AssertInertiaProp checks that the Inertia page's prop name equals want,
// compared as JSON (want can be a struct, a map or a plain value). A name
// with dots is a path into the prop: "user.name", "tokens.0.id" (since
// v0.6).
//
//	res.AssertInertiaProp("errors", map[string]string{"title": "The title is required."})
func (r *Response) AssertInertiaProp(name string, want any) *Response {
	r.app.t.Helper()
	p, err := inertia.ReadPage(r.Body)
	if err != nil {
		r.errorf("%v (status %d)", err, r.StatusCode)
		return r
	}
	raw, ok := propAt(p.Props, name)
	if !ok {
		r.errorf("the Inertia page %q has no prop %q", p.Component, name)
		return r
	}
	var got, exp any
	b, err := json.Marshal(want)
	if err == nil {
		err = json.Unmarshal(b, &exp)
	}
	if err != nil {
		r.errorf("AssertInertiaProp: %v", err)
		return r
	}
	_ = json.Unmarshal(raw, &got)
	if !reflect.DeepEqual(got, exp) {
		r.errorf("Inertia prop %q is %s, want %s", name, raw, b)
	}
	return r
}

// InertiaPage returns the response's Inertia page object, for the checks
// the assertions don't make; it fails the test if there is none.
func (r *Response) InertiaPage() *inertia.PageObject {
	r.app.t.Helper()
	p, err := inertia.ReadPage(r.Body)
	if err != nil {
		r.app.t.Fatalf("%s %s: %v (status %d)", r.Request.Method, r.Request.URL.RequestURI(), err, r.StatusCode)
	}
	return p
}

// propAt returns the JSON at path in props: a prop's name, then the keys
// of objects and the indexes of arrays inside it, separated by dots. A
// prop whose own name has dots is found first.
func propAt(props map[string]json.RawMessage, path string) (json.RawMessage, bool) {
	if raw, ok := props[path]; ok {
		return raw, true
	}
	name, rest, ok := strings.Cut(path, ".")
	raw, found := props[name]
	if !ok || !found {
		return nil, false
	}
	for part := range strings.SplitSeq(rest, ".") {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			if raw, ok = obj[part]; !ok {
				return nil, false
			}
			continue
		}
		var arr []json.RawMessage
		i, err := strconv.Atoi(part)
		if json.Unmarshal(raw, &arr) != nil || err != nil || i < 0 || i >= len(arr) {
			return nil, false
		}
		raw = arr[i]
	}
	return raw, true
}
