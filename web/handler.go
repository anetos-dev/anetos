// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"unsafe"

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
//	func (h *Posts) Store(c *web.Ctx, in StorePost) (PostResponse, error) {
//		post, err := h.repo.Create(c, in.Title, in.Body)
//		if err != nil {
//			return PostResponse{}, err
//		}
//		return postResponse(post), nil
//	}
//
//	r.Post("/posts", web.H(h.Store)).Status(http.StatusCreated)
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
// The result is written by its [Responder] method if Out implements it (a
// nil Responder writes 204 No Content). Otherwise it is written as JSON
// with the route's status ([Route.Status]: 200 OK unless set), and an
// [Empty] result answers 204 No Content without a body. Typed results
// (Out a struct, a slice, Empty) say what the route answers in its
// signature, which tools can read (package web/openapi); a
// Responder chooses at run time. If fn wrote the response itself, the
// result is ignored.
//
// H inspects In once, when it is called; it panics if In is not a struct,
// has a field type it can't bind, or has an invalid validate tag, so
// mistakes show up at startup. Call it when registering routes, not per
// request: each call records the handler's types for [Router.Routes],
// keeping fn alive for the program's life. Register its result as it is:
// a HandlerFunc wrapping it has no types to report.
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
	_, empty := any(*new(Out)).(Empty)
	h := HandlerFunc(func(c *Ctx) error {
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
		return respond(c, out, empty)
	})
	typedHandlers.Store(funcKey(h), handlerTypes{in: reflect.TypeFor[In](), out: reflect.TypeFor[Out](), name: funcName(fn)})
	return h
}

// typedHandlers are the input and result types of the handlers H made,
// by funcKey: a route registering one of them knows its types
// ([RouteInfo]), for tools that describe the routes (the OpenAPI spec).
// An entry lives as long as the program, as routes do.
var typedHandlers sync.Map // unsafe.Pointer → handlerTypes

type handlerTypes struct {
	in, out reflect.Type
	name    string // the function's, as RouteInfo.Handler
}

// funcName is fn's name without its package path: "handlers.Posts.Store"
// for a method value (h.Store, with a value or pointer receiver),
// "main.main.func1" for a function literal.
func funcName(fn any) string {
	f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer())
	if f == nil {
		return ""
	}
	name := strings.TrimSuffix(f.Name(), "-fm") // a method value's wrapper
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return strings.NewReplacer("(*", "", ")", "").Replace(name)
}

// funcKey identifies a func value: the address of its closure, which
// each call of H allocates anew. reflect.Value.Pointer can't: it is the
// code's address, shared by the closures of one H[In, Out] and by
// instantiations with the same GC shape.
// TestTypedHandlerTypes checks the two closures of one H[In, Out] get
// keys of their own.
func funcKey(h HandlerFunc) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&h))
}

// Empty is a typed handler's result without a body: [H] answers it 204
// No Content (or the route's [Route.Status]), for an action such as a
// deletion or a logout.
//
//	func (h Orders) Delete(c *web.Ctx, in OrderID) (web.Empty, error)
type Empty struct{}

func respond(c *Ctx, out any, empty bool) error {
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
	status := http.StatusOK
	if c.route != nil {
		status = c.route.successStatus(empty)
	} else if empty {
		status = http.StatusNoContent
	}
	if empty || status == http.StatusNoContent || status == http.StatusResetContent {
		return c.Status(status)
	}
	return c.JSON(status, out)
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
