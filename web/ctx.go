// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
)

// Ctx is the per-request context passed to handlers. It wraps the
// http.ResponseWriter and *http.Request, gives access to the application and
// route, and provides response helpers.
//
// Ctx implements context.Context by delegating to the request's context, so
// it can be passed directly to database, queue and other calls:
//
//	post, err := posts.Find(c, id) // c is a context.Context
//
// A Ctx belongs to one request. Don't use it after the handler returns or
// from goroutines that outlive the request; use app.Go with its own context
// for background work.
type Ctx struct {
	w      *responseWriter
	r      *http.Request
	router *Router
	route  *Route
}

var _ context.Context = (*Ctx)(nil)

// Deadline implements context.Context.
func (c *Ctx) Deadline() (time.Time, bool) { return c.r.Context().Deadline() }

// Done implements context.Context.
func (c *Ctx) Done() <-chan struct{} { return c.r.Context().Done() }

// Err implements context.Context.
func (c *Ctx) Err() error { return c.r.Context().Err() }

// Value implements context.Context.
func (c *Ctx) Value(key any) any { return c.r.Context().Value(key) }

// Request returns the underlying request.
func (c *Ctx) Request() *http.Request { return c.r }

// Writer returns the response writer. Prefer the response helpers; if you
// write directly, errors returned afterwards can only be logged.
func (c *Ctx) Writer() http.ResponseWriter { return c.w }

// App returns the application, or nil for a router created without one.
func (c *Ctx) App() *anetos.App { return c.router.core.app }

// Route returns the matched route, or nil for requests no route matched.
func (c *Ctx) Route() *Route { return c.route }

// Logger returns the application logger annotated with the request ID and
// route name.
func (c *Ctx) Logger() *slog.Logger {
	l := c.router.core.logger
	if id := RequestID(c.r.Context()); id != "" {
		l = l.With("request_id", id)
	}
	if c.route != nil {
		if name := c.route.RouteName(); name != "" {
			l = l.With("route", name)
		}
	}
	return l
}

// Param returns the value of the path wildcard name, e.g. "id" for the
// pattern "/posts/{id}". It returns "" if there is no such wildcard.
func (c *Ctx) Param(name string) string { return c.r.PathValue(name) }

// Query returns the first value of the query parameter name.
func (c *Ctx) Query(name string) string { return c.r.URL.Query().Get(name) }

// Header returns the first value of the request header name.
func (c *Ctx) Header(name string) string { return c.r.Header.Get(name) }

// SetHeader sets a response header. It has no effect once the response has
// started.
func (c *Ctx) SetHeader(name, value string) { c.w.Header().Set(name, value) }

// URL builds the path of the named route. See [Router.URL].
func (c *Ctx) URL(name string, args ...any) (string, error) { return c.router.URL(name, args...) }

// WantsJSON reports whether the client prefers a JSON response: it accepts
// JSON but not HTML, sent a JSON body, or made an XMLHttpRequest. Error
// responses use it to choose between JSON and an HTML page.
func (c *Ctx) WantsJSON() bool { return wantsJSON(c.r) }

func wantsJSON(r *http.Request) bool {
	if accept := r.Header.Get("Accept"); accept != "" {
		jsonQ, htmlQ := acceptQuality(accept)
		if jsonQ > htmlQ {
			return true
		}
		if htmlQ > jsonQ {
			return false
		}
	}
	if r.Header.Get("X-Requested-With") == "XMLHttpRequest" {
		return true
	}
	return isJSONContentType(r.Header.Get("Content-Type"))
}

// acceptQuality returns the highest q-value the Accept header gives to JSON
// types and to HTML. Wildcards are ignored: they express no preference.
func acceptQuality(accept string) (jsonQ, htmlQ float64) {
	for part := range strings.SplitSeq(accept, ",") {
		mt, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		q := 1.0
		if v, ok := params["q"]; ok {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				q = f
			}
		}
		switch {
		case mt == "application/json" || strings.HasSuffix(mt, "+json"):
			jsonQ = max(jsonQ, q)
		case mt == "text/html" || mt == "application/xhtml+xml":
			htmlQ = max(htmlQ, q)
		}
	}
	return jsonQ, htmlQ
}

func isJSONContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	return err == nil && (mt == "application/json" || strings.HasSuffix(mt, "+json"))
}

// Status sets the response status without a body.
func (c *Ctx) Status(code int) error {
	c.w.WriteHeader(code)
	return nil
}

// NoContent responds with 204 No Content.
func (c *Ctx) NoContent() error { return c.Status(http.StatusNoContent) }

// JSON writes v as JSON with the given status.
func (c *Ctx) JSON(status int, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Blob(status, "application/json; charset=utf-8", append(body, '\n'))
}

// Text writes a plain-text response.
func (c *Ctx) Text(status int, s string) error {
	return c.Blob(status, "text/plain; charset=utf-8", []byte(s))
}

// HTML writes an HTML string. The string is sent as-is: escape any user
// content yourself, or use views (roadmap F10), which escape by default.
func (c *Ctx) HTML(status int, html string) error {
	return c.Blob(status, "text/html; charset=utf-8", []byte(html))
}

// Blob writes body with the given status and content type.
func (c *Ctx) Blob(status int, contentType string, body []byte) error {
	h := c.w.Header()
	h.Set("Content-Type", contentType)
	c.w.WriteHeader(status)
	if c.r.Method == http.MethodHead {
		return nil
	}
	_, err := c.w.Write(body)
	return err
}

// Redirect redirects to url with the given status. Use 303 See Other after
// a successful form POST, and 307/308 to preserve the method.
func (c *Ctx) Redirect(status int, url string) error {
	http.Redirect(c.w, c.r, url, status)
	return nil
}

// RedirectRoute redirects with 303 See Other to the named route.
func (c *Ctx) RedirectRoute(name string, args ...any) error {
	u, err := c.URL(name, args...)
	if err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, u)
}
