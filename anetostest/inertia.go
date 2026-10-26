// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"encoding/json"
	"reflect"

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
// compared as JSON (want can be a struct, a map or a plain value).
//
//	res.AssertInertiaProp("errors", map[string]string{"title": "The title is required."})
func (r *Response) AssertInertiaProp(name string, want any) *Response {
	r.app.t.Helper()
	p, err := inertia.ReadPage(r.Body)
	if err != nil {
		r.errorf("%v (status %d)", err, r.StatusCode)
		return r
	}
	raw, ok := p.Props[name]
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
