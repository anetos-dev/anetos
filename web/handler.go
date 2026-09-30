// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"net/http"
	"reflect"

	"anetos.dev/anetos/validate"
)

// Validator is implemented by input types that check themselves after
// binding and after their `validate` tag rules pass. Report problems with
// [validate.Fail] or a *[validate.Errors], which become a 422 response with
// a message per field, or with an [HTTPError]. Any other error is treated
// like a handler error: a 500 whose details stay out of the response, so a
// failing database check doesn't leak its error text.
type Validator interface {
	// Validate checks the bound input; ctx is the request's.
	Validate(ctx context.Context) error
}

// H adapts a typed handler into a [HandlerFunc]:
//
//	type StorePost struct {
//		Title string `json:"title"`
//		Body  string `json:"body"`
//	}
//
//	func (h *Posts) Store(c *web.Ctx, in StorePost) (web.Responder, error) {
//		post, err := h.repo.Create(c, in.Title, in.Body)
//		if err != nil {
//			return nil, err
//		}
//		return web.Created(post), nil
//	}
//
//	r.Post("/posts", web.H(h.Store))
//
// Before fn runs, the request is bound into a new In value:
//
//   - The body, for JSON (`json` tags) or forms (`form` tags, falling back to
//     the JSON name); multipart file fields are *multipart.FileHeader or
//     []*multipart.FileHeader.
//   - Then `query`, `header` and `path` tags, which the body can never set.
//
// Conversion failures produce a 400 response listing each bad field. Then
// the `validate` tag rules run (see the validate package); failures produce a
// 422 response with a message per field. If they pass and In implements
// [Validator], Validate runs last.
//
// The result is written by its [Responder] method if Out implements it, and
// as JSON with status 200 otherwise; a nil Responder writes 204 No Content.
// If fn wrote the response itself, the result is ignored.
//
// H inspects In once, when it is called; it panics if In is not a struct,
// has a field type it can't bind, or has an invalid validate tag, so
// mistakes show up at startup.
func H[In, Out any](fn func(c *Ctx, in In) (Out, error)) HandlerFunc {
	plan, err := newBindPlan(reflect.TypeFor[In]())
	if err != nil {
		panic(err)
	}
	rules, err := validate.Compile(reflect.TypeFor[In]())
	if err != nil {
		panic(err)
	}
	if rules.Empty() {
		rules = nil
	}
	return func(c *Ctx) error {
		var in In
		if err := plan.bind(c, reflect.ValueOf(&in)); err != nil {
			return err
		}
		if rules != nil {
			if err := rules.Validate(c, &in); err != nil {
				return plan.formErrors(c, err)
			}
		}
		if v, ok := any(&in).(Validator); ok {
			if err := v.Validate(c); err != nil {
				return plan.formErrors(c, err)
			}
		}
		out, err := fn(c, in)
		if err != nil {
			return err
		}
		return respond(c, out)
	}
}

func respond(c *Ctx, out any) error {
	if c.w.started() {
		return nil
	}
	switch o := out.(type) {
	case Responder:
		if isNil(o) {
			return c.NoContent()
		}
		return o.Respond(c)
	case nil:
		return c.NoContent()
	}
	return c.JSON(http.StatusOK, out)
}

func isNil(v any) bool {
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return rv.IsNil()
	}
	return false
}

// Responder writes a response. Return one from a typed handler to choose the
// status, format or redirect.
type Responder interface {
	// Respond writes the response to c.
	Respond(c *Ctx) error
}

// ResponderFunc adapts a function to a [Responder].
type ResponderFunc func(c *Ctx) error

// Respond implements [Responder].
func (f ResponderFunc) Respond(c *Ctx) error { return f(c) }

// JSON responds with v encoded as JSON and the given status.
func JSON(status int, v any) Responder {
	return ResponderFunc(func(c *Ctx) error { return c.JSON(status, v) })
}

// Created responds 201 Created with v as JSON.
func Created(v any) Responder { return JSON(http.StatusCreated, v) }

// NoContent responds 204 No Content.
func NoContent() Responder { return ResponderFunc(func(c *Ctx) error { return c.NoContent() }) }

// Text responds with plain text.
func Text(status int, s string) Responder {
	return ResponderFunc(func(c *Ctx) error { return c.Text(status, s) })
}

// Redirect responds 303 See Other to url, the right status after a
// successful form submission.
func Redirect(url string) Responder {
	return ResponderFunc(func(c *Ctx) error { return c.Redirect(http.StatusSeeOther, url) })
}

// RedirectRoute responds 303 See Other to the named route.
func RedirectRoute(name string, args ...any) Responder {
	return ResponderFunc(func(c *Ctx) error { return c.RedirectRoute(name, args...) })
}
