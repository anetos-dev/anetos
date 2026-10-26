// SPDX-License-Identifier: Apache-2.0

package inertia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"anetos.dev/anetos"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/view/vite"
	"anetos.dev/anetos/web"
)

// Inertia renders the app's pages for Inertia's client: the first visit
// as the root view's HTML, with the page object in it, and the visits
// after as the page object alone. Make it with [New]; it is safe for
// concurrent use.
type Inertia struct {
	root    view.Component
	version string
	encrypt bool
	shared  []shared
}

// shared is a WithShared function and the plan of its result.
type shared struct {
	plan *plan
	get  func(c *web.Ctx) (reflect.Value, error)
}

// Option configures [New].
type Option func(*Inertia) error

// WithVersion sets the asset version: when the version a client's page
// was built with differs, its next visit reloads the page whole, so the
// browser gets the new scripts. The default is the frontend build's
// ([vite.Vite.Version]) when the app has one.
func WithVersion(version string) Option {
	return func(in *Inertia) error {
		in.version = version
		return nil
	}
}

// WithEncryptHistory encrypts the pages' state in the browser's history,
// so the back button can't show a page after logging out ([ClearHistory]
// drops the key). Browsers allow it on HTTPS (and localhost) only.
// [EncryptHistory] does it for one response.
func WithEncryptHistory() Option {
	return func(in *Inertia) error {
		in.encrypt = true
		return nil
	}
}

// WithShared adds props to every page: what fn returns for the request
// (the logged-in user, the app's name), a struct as a page's props are
// or [Props]. A page's prop of the same name wins. fn runs for each page
// response, partial reloads included; its [Prop] fields work as a page's.
func WithShared[S any](fn func(c *web.Ctx) (S, error)) Option {
	return func(in *Inertia) error {
		if fn == nil {
			return errors.New("inertia: WithShared needs a function")
		}
		pl, err := planFor(reflect.TypeFor[S]())
		if err != nil {
			return fmt.Errorf("inertia: WithShared: %w", err)
		}
		in.shared = append(in.shared, shared{plan: pl, get: func(c *web.Ctx) (reflect.Value, error) {
			v, err := fn(c)
			return reflect.ValueOf(v), err
		}})
		return nil
	}
}

type ctxKey struct{}

// ErrNoInertia is [From]'s error for a context without an Inertia: New
// wasn't called for the app.
var ErrNoInertia = errors.New("inertia: no Inertia in the context (call inertia.New in setup)")

// New sets up the app's Inertia pages and makes the Inertia available in
// every context the app creates (for pages' Render, [Middleware] and
// [ErrorPages]) and to [anetos.Resolve]. root is the view of the first
// visit, the HTML page that loads the frontend and holds the page
// ([App]):
//
//	in, err := inertia.New(app, views.Root(), inertia.WithShared(shared))
//
// The asset version is the frontend build's when the app has one: call
// vite.New first. Calling New a second time for one app is an error.
func New(app *anetos.App, root view.Component, opts ...Option) (*Inertia, error) {
	if _, ok := anetos.Lookup[*Inertia](app); ok {
		return nil, errors.New("inertia: New called twice for one app")
	}
	if v, ok := anetos.Lookup[*vite.Vite](app); ok {
		opts = append([]Option{WithVersion(v.Version())}, opts...)
	}
	in, err := NewWithRoot(root, opts...)
	if err != nil {
		return nil, err
	}
	anetos.Provide(app, in)
	app.AddContextValue(ctxKey{}, in)
	return in, nil
}

// NewWithRoot returns an Inertia for root without an app, for tests and
// tools; [WithInertia] puts it in a context.
func NewWithRoot(root view.Component, opts ...Option) (*Inertia, error) {
	if root == nil {
		return nil, errors.New("inertia: the root view is nil")
	}
	in := &Inertia{root: root}
	for _, opt := range opts {
		if err := opt(in); err != nil {
			return nil, err
		}
	}
	return in, nil
}

// WithInertia returns ctx carrying in.
func WithInertia(ctx context.Context, in *Inertia) context.Context {
	return context.WithValue(ctx, ctxKey{}, in)
}

// From returns the Inertia in ctx, or [ErrNoInertia].
func From(ctx context.Context) (*Inertia, error) {
	if in, ok := ctx.Value(ctxKey{}).(*Inertia); ok && in != nil {
		return in, nil
	}
	return nil, ErrNoInertia
}

// Version returns the asset version ([WithVersion]).
func (in *Inertia) Version() string { return in.version }

// ---- The request ----

// The session keys of what one request leaves for the next page.
const (
	flashKey    = "_inertia.flash"
	errorBagKey = "_inertia.error_bag"
	clearKey    = "_inertia.clear_history"
	fragmentKey = "_inertia.preserve_fragment"
)

// state is a request's: what the previous request left in the session
// (read before the handler changes it), and what this one sets.
type state struct {
	prevFlash    map[string]json.RawMessage
	prevBag      string
	prevClear    bool
	prevFragment bool
	flash        map[string]any
	clear        bool
	fragment     bool
	encrypt      *bool
}

type stateKey struct{}

func newState(ctx context.Context) *state {
	st := &state{}
	if s := session.From(ctx); s != nil {
		s.Get(flashKey, &st.prevFlash)
		st.prevBag = s.GetString(errorBagKey)
		st.prevClear = s.Has(clearKey)
		st.prevFragment = s.Has(fragmentKey)
	}
	return st
}

func stateFrom(ctx context.Context) *state {
	st, _ := ctx.Value(stateKey{}).(*state)
	return st
}

// isInertia reports whether r is a visit of Inertia's client.
func isInertia(r *http.Request) bool { return r.Header.Get("X-Inertia") == "true" }

// Middleware speaks Inertia's protocol around the pages' handlers. Put
// it on the routes of the pages, after the session's middleware and
// CSRF (with [web.WithXSRFCookie], which Inertia's client reads):
//
//	g := r.Group("", sessions.Middleware, web.CSRF(web.WithXSRFCookie()), inertia.Middleware)
//
// For the client's visits it
//
//   - answers 409 with X-Inertia-Location to a GET whose page was built
//     with other assets ([WithVersion]): the client reloads it whole, and
//     the session's flashed data is kept for that reload;
//   - turns a 302 after PUT, PATCH or DELETE into 303, so the browser
//     follows it with a GET;
//   - turns a redirect to a URL with a fragment (#comments) into 409 with
//     X-Inertia-Redirect, which the client follows keeping the fragment;
//   - keeps the error bag a form names (X-Inertia-Error-Bag) for the
//     page the failed post redirects back to.
//
// It adds "Vary: X-Inertia" to every response, and keeps what [Flash],
// [ClearHistory] and [PreserveFragment] leave for the next page. The
// Inertia is the context's ([From]).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in, err := From(r.Context())
		if err != nil {
			web.WriteError(w, r, err)
			return
		}
		addVary(w.Header(), "X-Inertia")
		st := newState(r.Context())
		if isInertia(r) {
			if r.Method == http.MethodGet && r.Header.Get("X-Inertia-Version") != in.version {
				if s := session.From(r.Context()); s != nil {
					s.Reflash()
				}
				w.Header().Set("X-Inertia-Location", r.URL.RequestURI())
				w.Header().Set("X-Inertia-Version", in.version)
				w.WriteHeader(http.StatusConflict)
				return
			}
			if bag := r.Header.Get("X-Inertia-Error-Bag"); bag != "" && r.Method != http.MethodGet && r.Method != http.MethodHead {
				if s := session.From(r.Context()); s != nil {
					s.Flash(errorBagKey, bag)
				}
			}
			w = &writer{ResponseWriter: w, r: r}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, st)))
	})
}

// writer changes the redirects of an Inertia visit (see [Middleware]).
type writer struct {
	http.ResponseWriter
	r           *http.Request
	wroteHeader bool
	discard     bool // the body of a redirect turned into 409
}

func (w *writer) WriteHeader(code int) {
	if code < 200 && code != http.StatusSwitchingProtocols { // informational: the response is still to come
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if !w.wroteHeader {
		w.wroteHeader = true
		switch w.r.Method {
		case http.MethodPut, http.MethodPatch, http.MethodDelete:
			if code == http.StatusFound {
				code = http.StatusSeeOther
			}
		}
		if code >= 300 && code < 400 && w.r.Header.Get("Purpose") != "prefetch" {
			if loc := w.Header().Get("Location"); strings.Contains(loc, "#") {
				h := w.Header()
				h.Del("Location")
				h.Del("Content-Type")
				h.Set("X-Inertia-Redirect", loc)
				code, w.discard = http.StatusConflict, true
			}
		}
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *writer) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.discard {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap gives http.ResponseController the writer underneath.
func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush sends what is buffered.
func (w *writer) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// Location sends the browser to url with a full page load: another site,
// or a page of the app that isn't Inertia's (a file download, OAuth). For
// a visit of Inertia's client it answers 409 with X-Inertia-Location,
// which the client follows; otherwise it redirects (303).
func Location(c *web.Ctx, url string) error {
	if isInertia(c.Request()) {
		c.SetHeader("X-Inertia-Location", url)
		return c.Status(http.StatusConflict)
	}
	return c.Redirect(http.StatusSeeOther, url)
}

// Flash adds data for the next page Inertia renders, in the page object's
// flash (the client's flash event and page.flash): a toast after a save,
// whether this response renders the page or redirects to it. It needs
// [Middleware] (and the session's), and does nothing without it.
func Flash(ctx context.Context, key string, value any) {
	st, s := stateFrom(ctx), session.From(ctx)
	if st == nil || s == nil {
		return
	}
	if st.flash == nil {
		st.flash = map[string]any{}
	}
	st.flash[key] = value
	s.Flash(flashKey, st.flash)
}

// EncryptHistory encrypts the history state of this response's page (see
// [WithEncryptHistory]). It needs [Middleware], and does nothing without
// it.
func EncryptHistory(ctx context.Context) {
	if st := stateFrom(ctx); st != nil {
		on := true
		st.encrypt = &on
	}
}

// ClearHistory drops the client's history encryption key on the next
// page Inertia renders, here or after a redirect, which makes the
// encrypted pages in the history unreadable: after logging out. It needs
// [Middleware] (and the session's), and does nothing without it.
func ClearHistory(ctx context.Context) {
	st, s := stateFrom(ctx), session.From(ctx)
	if st == nil || s == nil {
		return
	}
	st.clear = true
	s.Flash(clearKey, true)
}

// PreserveFragment keeps the URL's fragment (#comments) of the visit that
// redirects to the next page Inertia renders. It needs [Middleware] (and
// the session's), and does nothing without it.
func PreserveFragment(ctx context.Context) {
	st, s := stateFrom(ctx), session.From(ctx)
	if st == nil || s == nil {
		return
	}
	st.fragment = true
	s.Flash(fragmentKey, true)
}

// addVary adds value to the Vary header unless it is there.
func addVary(h http.Header, value string) {
	for _, v := range h.Values("Vary") {
		for f := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(f), value) {
				return
			}
		}
	}
	h.Add("Vary", value)
}
