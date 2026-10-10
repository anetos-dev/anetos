// SPDX-License-Identifier: Apache-2.0

// Package view renders HTML. Pages are components: anything with a
// Render(ctx, w) method, which is what templ (https://templ.guide)
// generates, so templ components work as they are. [Template] adapts
// html/template for those who prefer it.
//
// Handlers render with web.Ctx.Render or the web.Render responder:
//
//	func (h Posts) Index(c *web.Ctx) error {
//		return c.Render(http.StatusOK, views.PostsIndex(posts))
//	}
//
// Inside components, the helpers of this package read the request's
// session: [CSRFField], [Errors], [Old], [Flash]. web.URL builds links
// from route names.
package view

import (
	"bytes"
	"context"
	"html/template"
	"io"
)

// Component is a renderable piece of HTML. templ components implement it.
// ctx is the request's context (a *web.Ctx when rendered by the web
// package).
type Component interface {
	// Render writes the component's HTML to w.
	Render(ctx context.Context, w io.Writer) error
}

// ComponentFunc adapts a function to a [Component].
type ComponentFunc func(ctx context.Context, w io.Writer) error

// Render implements [Component].
func (f ComponentFunc) Render(ctx context.Context, w io.Writer) error { return f(ctx, w) }

// Template returns a component that executes the named html/template
// template with data, for applications that prefer html/template to templ.
// Templates don't see the request context: put what they need from the
// helpers (a CSRF field, errors, old input) in data.
func Template(t *template.Template, name string, data any) Component {
	return ComponentFunc(func(_ context.Context, w io.Writer) error {
		return t.ExecuteTemplate(w, name, data)
	})
}

// String renders c to a string, for tests and emails.
func String(ctx context.Context, c Component) (string, error) {
	var b bytes.Buffer
	err := c.Render(ctx, &b)
	return b.String(), err
}
