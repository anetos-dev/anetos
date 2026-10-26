// SPDX-License-Identifier: Apache-2.0

package inertia

import (
	"context"
	"errors"
	"io"
	"reflect"

	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
)

// ErrorProps are the props of the error page of [ErrorPages].
type ErrorProps struct {
	Status    int    `json:"status"`    // the HTTP status, such as 404
	Title     string `json:"title"`     // the status's title, translated ("Not Found")
	Detail    string `json:"detail"`    // the error's message for the visitor, if any; never a 5xx's internals
	RequestID string `json:"requestId"` // the request's ID, to quote when asking for help
}

// ErrorPages makes the router's error pages the frontend's page
// component, with [ErrorProps] (and the error's field errors, if any, in
// the errors prop):
//
//	r.ErrorPages(inertia.ErrorPages("Error"))
//
// Visits of Inertia's client get the page object with the error's
// status, which the client renders as a page instead of its error
// dialog; others get the root view, as a first visit does. As
// [web.Router.ErrorPages] says, JSON clients, failed form posts and the
// debug mode's 5xx pages don't change. Without an Inertia in the context
// ([From]), the built-in page is sent. ErrorPages panics if component is
// "".
func ErrorPages(component string) func(c *web.Ctx, e web.ErrorPage) view.Component {
	p := NewPage[ErrorProps](component)
	return func(_ *web.Ctx, e web.ErrorPage) view.Component { return errorPage{p: p, e: e} }
}

// errorPage responds itself ([web.Responder]), with the error's status.
type errorPage struct {
	p *Page[ErrorProps]
	e web.ErrorPage
}

func (ep errorPage) Respond(c *web.Ctx) error {
	props := ErrorProps{Status: ep.e.Status, Title: ep.e.Title, Detail: ep.e.Detail, RequestID: ep.e.RequestID}
	errs := ep.e.Errors
	if errs == nil {
		errs = map[string]string{}
	}
	return render(c, ep.e.Status, ep.p.component, ep.p.plan, reflect.ValueOf(props), errs)
}

// Render implements view.Component; the router calls Respond instead.
func (ep errorPage) Render(context.Context, io.Writer) error {
	return errors.New("inertia: an error page responds; it doesn't render")
}
