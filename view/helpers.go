// SPDX-License-Identifier: Apache-2.0

package view

import (
	"context"
	"errors"
	"html"
	"io"
	"net/http"
	"strings"

	"anetos.dev/anetos/internal/convert"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/validate"
)

// ErrNoSession is returned when rendering a helper that needs the session
// on a route without the session middleware.
var ErrNoSession = errors.New("view: no session for this request; add the session middleware to the route")

// CSRFField renders the hidden "_token" input that web.CSRF checks. Put it
// in every form that posts:
//
//	<form method="post" action="/posts">
//		@view.CSRFField(ctx)
//		…
//	</form>
func CSRFField(ctx context.Context) Component {
	return ComponentFunc(func(_ context.Context, w io.Writer) error {
		s := session.From(ctx)
		if s == nil {
			return ErrNoSession
		}
		_, err := io.WriteString(w, `<input type="hidden" name="_token" value="`+html.EscapeString(s.Token())+`">`)
		return err
	})
}

// CSRFToken returns a CSRF token for the X-CSRF-Token header, for pages
// that post with JavaScript or htmx:
//
//	<body hx-headers={ `{"X-CSRF-Token": "` + view.CSRFToken(ctx) + `"}` }>
//
// It returns "" without a session.
func CSRFToken(ctx context.Context) string {
	if s := session.From(ctx); s != nil {
		return s.Token()
	}
	return ""
}

// MethodField renders the hidden "_method" input that web.MethodOverride
// turns into the request method, so an HTML form can send PUT, PATCH or
// DELETE.
func MethodField(method string) Component {
	return ComponentFunc(func(_ context.Context, w io.Writer) error {
		m := strings.ToUpper(method)
		switch m {
		case http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return errors.New("view: MethodField: method must be PUT, PATCH or DELETE, not " + method)
		}
		_, err := io.WriteString(w, `<input type="hidden" name="_method" value="`+m+`">`)
		return err
	})
}

// Errors returns the validation errors of the form post that redirected to
// this page, keyed by form field name. It is never nil, so templates can
// call its methods directly:
//
//	if view.Errors(ctx).Has("title") {
//		<p class="error">{ view.Errors(ctx).Get("title") }</p>
//	}
func Errors(ctx context.Context) *validate.Errors {
	errs := &validate.Errors{}
	if s := session.From(ctx); s != nil {
		for _, e := range s.Errors() {
			errs.Add(e.Field, e.Message)
		}
	}
	return errs
}

// Old returns the value submitted for a form field by the post that
// redirected to this page, so the form can be refilled. Without one, it
// returns fallback (the first, if given) or "":
//
//	<input name="title" value={ view.Old(ctx, "title", post.Title) }>
func Old(ctx context.Context, field string, fallback ...string) string {
	if s := session.From(ctx); s != nil {
		if v, ok := s.Old(field); ok {
			return v
		}
	}
	if len(fallback) > 0 {
		return fallback[0]
	}
	return ""
}

// OldChecked reports whether a checkbox should be checked: after a post
// that redirected back, whether it was submitted checked (a browser
// sends nothing for an unchecked box; its first value is read as binding
// reads it, so "0", "false", "off" and "no" are unchecked); otherwise
// fallback, the saved value:
//
//	<input type="checkbox" name="publish" checked?={ view.OldChecked(ctx, "publish", post.Published) }/>
//
// It can't tell which form was posted: on a page with two forms, a failed
// post of one makes the other's checkboxes unchecked. And a form whose
// other fields are all passwords or tokens (which aren't kept) looks like
// no post, so its checkboxes show fallback.
func OldChecked(ctx context.Context, field string, fallback bool) bool {
	if s := session.From(ctx); s != nil {
		if old := s.OldInput(); len(old) > 0 {
			v := old.Get(field)
			if v == "" {
				return false
			}
			b, err := convert.ParseBool(v)
			return err != nil || b // a value that isn't a boolean ("publish") is checked
		}
	}
	return fallback
}

// Flash returns the flashed string under key ("" if none):
//
//	if msg := view.Flash(ctx, "status"); msg != "" {
//		<div class="notice">{ msg }</div>
//	}
func Flash(ctx context.Context, key string) string {
	if s := session.From(ctx); s != nil {
		return s.GetString(key)
	}
	return ""
}
