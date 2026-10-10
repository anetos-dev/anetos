// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"anetos.dev/anetos/internal/httperr"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/validate"
)

// Session returns the request's session. It panics if the session
// middleware doesn't run for the route, which is a wiring mistake; use
// session.From to check.
func (c *Ctx) Session() *session.Session {
	s := session.From(c)
	if s == nil {
		panic("web: no session for this request; add the session middleware (session.New(app) … .Middleware) to the route's group")
	}
	return s
}

// URL returns the path of the named route, like [Router.URL], for any
// context of a request served by a [Router], in the request's locale
// (with APP_LOCALE_STRATEGY=prefix, "/bn/posts/1"; see [LocalePath]). Components
// use it to link to routes:
//
//	<a href={ web.URL(ctx, "posts.show", post.ID) }>
func URL(ctx context.Context, name string, args ...any) (string, error) {
	st := stateFrom(ctx)
	if st == nil || st.core == nil {
		return "", fmt.Errorf("web: URL(%q) needs the context of a request served by a Router", name)
	}
	path, err := (&Router{core: st.core}).URL(name, args...)
	if err != nil {
		return "", err
	}
	return LocalePath(ctx, path), nil
}

// MustURL is [URL] for a component's arguments, where a call returning
// an error doesn't fit: the path of the named route, or a panic (a
// route that doesn't exist or arguments it doesn't take is a mistake in
// the code; the router answers the handler's panic as a 500). Attributes
// take URL itself, whose error templ returns:
//
//	@ui.LinkButton(web.MustURL(ctx, "posts.edit", post.ID), ui.Secondary) { Edit }
func MustURL(ctx context.Context, name string, args ...any) string {
	u, err := URL(ctx, name, args...)
	if err != nil {
		panic(err)
	}
	return u
}

// RouteIs reports whether the request was routed to one of the named
// routes. A name ending in ".*" matches the names that start with what
// comes before the star: "issues.*" matches issues.index and
// issues.show. Layouts use it to mark the current page's link:
//
//	<a href={ web.URL(ctx, "issues.index") } if web.RouteIs(ctx, "issues.*") { aria-current="page" }>
//
// Outside a request served by a [Router], or for a route without a name,
// it returns false.
func RouteIs(ctx context.Context, names ...string) bool {
	st := stateFrom(ctx)
	if st == nil || st.route == nil {
		return false
	}
	name := st.route.RouteName()
	if name == "" {
		return false
	}
	for _, n := range names {
		if prefix, ok := strings.CutSuffix(n, ".*"); ok {
			if strings.HasPrefix(name, prefix+".") {
				return true
			}
		} else if n == name {
			return true
		}
	}
	return false
}

// PageURL returns a link to page n of the current list: the current
// page's query string with its "page" parameter set to n, the other
// parameters (a search, a filter, per_page) kept as they were. It is
// relative ("?q=go&page=2"), so it works behind a path prefix or a proxy;
// a colon in the query is encoded (%3A), so the link can't be read as a
// scheme.
//
//	if posts.HasPrev() {
//		<a href={ web.PageURL(ctx, posts.CurrentPage-1) }>Newer</a>
//	}
//
// Outside a request served by a [Router], it returns "?page=N".
func PageURL(ctx context.Context, page int) string {
	var parts []string
	if st := stateFrom(ctx); st != nil && st.url != nil && st.url.RawQuery != "" {
		for p := range strings.SplitSeq(st.url.RawQuery, "&") {
			k, _, _ := strings.Cut(p, "=")
			if uk, err := url.QueryUnescape(k); err == nil {
				k = uk
			}
			if k != "page" && p != "" {
				// A colon, which a link may carry unencoded (?q=a:b), would
				// make the relative link read as a URL's scheme.
				parts = append(parts, strings.ReplaceAll(p, ":", "%3A"))
			}
		}
	}
	return "?" + strings.Join(append(parts, "page="+strconv.Itoa(page)), "&")
}

// Middleware of packages web imports (session) fails requests through
// WriteError too.
func init() { httperr.Write = WriteError }

// WriteError sends err through the router's error handler, as if a
// handler had returned it. Middleware uses it to fail a request with the
// application's error pages. Outside a Router, it writes a plain-text
// error.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	st := stateFrom(r.Context())
	if st == nil || st.core == nil {
		status := StatusOf(err)
		http.Error(w, http.StatusText(status), status)
		return
	}
	c := &Ctx{w: wrapWriter(w), r: r, router: &Router{core: st.core}}
	st.core.errorHandler(c, err)
}

// Back redirects (303 See Other) to the previous page: the Referer if it
// is on this site, else "/". Browsers send the Referer unless the page's
// Referrer-Policy is no-referrer.
func (c *Ctx) Back() error { return c.Redirect(http.StatusSeeOther, backURL(c.r)) }

// Back responds with a redirect to the previous page (see [Ctx.Back]).
func Back() Responder { return ResponderFunc(func(c *Ctx) error { return c.Back() }) }

func backURL(r *http.Request) string {
	ref, err := url.Parse(r.Referer())
	// The Referer is on this site if its host is the request's, or if the
	// browser says the request came from this origin (behind a proxy that
	// rewrites Host).
	if err == nil && ref.Host != "" && (strings.EqualFold(ref.Host, r.Host) || r.Header.Get("Sec-Fetch-Site") == "same-origin") {
		if u := ref.RequestURI(); localPath(u) {
			return u
		}
	}
	return "/"
}

// localPath reports whether u is a path on this site: it must not be read
// as another host ("//evil.example", "/\evil.example", "/\t/evil.example").
func localPath(u string) bool {
	return strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && !strings.HasPrefix(u, `/\`) &&
		!strings.ContainsFunc(u, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// ---- CSRF ----

// ErrCSRF is the error [CSRF] reports for a request without a valid token:
// 403 with a message asking to reload the page.
var ErrCSRF = &HTTPError{Status: http.StatusForbidden, Message: "The page has expired. Reload it and try again.", Key: "http.csrf"}

// ErrCrossOrigin is the error [CSRF] reports for a request sent by a page
// of another site.
var ErrCrossOrigin = &HTTPError{Status: http.StatusForbidden, Message: "Cross-origin request rejected.", Key: "http.cross_origin"}

// CSRFOption configures [CSRF].
type CSRFOption func(*http.CrossOriginProtection) error

// TrustedOrigins lets pages on the given origins ("https://admin.example.com")
// send cross-origin requests.
func TrustedOrigins(origins ...string) CSRFOption {
	return func(p *http.CrossOriginProtection) error {
		for _, o := range origins {
			if err := p.AddTrustedOrigin(o); err != nil {
				return err
			}
		}
		return nil
	}
}

// CSRF protects the routes it wraps from cross-site request forgery. For
// every request that may change something (any method but GET, HEAD,
// OPTIONS and TRACE: POST, PUT, PATCH, DELETE, …) it
//
//   - rejects requests the browser marks as coming from another site
//     (Sec-Fetch-Site, or an Origin that isn't this host), with
//     [ErrCrossOrigin], and
//   - requires the session's token, from the "_token" form field
//     (view.CSRFField) or the X-CSRF-Token header (view.CSRFToken), with
//     [ErrCSRF].
//
// It needs the session middleware to run first. Use it for routes that
// serve browsers; APIs authenticated by tokens in headers don't need it.
// CSRF panics if an option is invalid.
func CSRF(opts ...CSRFOption) Middleware {
	cop := http.NewCrossOriginProtection()
	for _, opt := range opts {
		if err := opt(cop); err != nil {
			panic(fmt.Sprintf("web: CSRF: %v", err))
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				next.ServeHTTP(w, r)
				return
			}
			if err := cop.Check(r); err != nil {
				WriteError(w, r, ErrCrossOrigin)
				return
			}
			s := session.From(r.Context())
			if s == nil {
				WriteError(w, r, errors.New("web: CSRF needs the session middleware to run before it"))
				return
			}
			token := r.Header.Get("X-CSRF-Token")
			if token == "" && isForm(r) {
				var err error
				if token, err = formToken(r); err != nil {
					WriteError(w, r, err)
					return
				}
				defer removeFiles(r)
			}
			if !s.VerifyToken(token) {
				WriteError(w, r, ErrCSRF)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MethodOverride lets HTML forms send PUT, PATCH and DELETE: a POST whose
// "_method" field (view.MethodField) or query parameter says so is routed
// with that method. Add it with UseGlobal, so it runs before routing:
//
//	r.UseGlobal(web.MethodOverride)
//
// URL-encoded bodies are read to find the field and then restored, so
// handlers still see the raw body. Multipart bodies (forms with files) are
// not read: put _method in the form's action URL instead
// (action="/posts/1?_method=PUT").
//
// Only HTML forms are overridden: posts whose body is URL-encoded or
// multipart, and not sent by another site (Sec-Fetch-Site: cross-site;
// for browsers without it, an Origin other than the request's host).
// Other posts keep their method, so a cross-site "simple" request (a
// text/plain post) can't reach a DELETE route that relies on CORS rather
// than CSRF tokens.
func MethodOverride(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !isForm(r) || crossSite(r) {
			next.ServeHTTP(w, r)
			return
		}
		method := r.URL.Query().Get("_method")
		if method == "" && isURLEncoded(r) {
			if err := readURLEncoded(r); err != nil {
				WriteError(w, r, err)
				return
			}
			method = r.PostForm.Get("_method")
		}
		switch m := strings.ToUpper(method); m {
		case http.MethodPut, http.MethodPatch, http.MethodDelete:
			r = r.WithContext(r.Context())
			r.Method = m
		}
		next.ServeHTTP(w, r)
	})
}

// crossSite reports whether another site's page sent r: Sec-Fetch-Site
// says so or, for browsers that don't send it, the Origin isn't the
// request's host ("null" included).
func crossSite(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "cross-site":
		return true
	case "":
		origin := r.Header.Get("Origin")
		if origin == "" {
			return false // not a browser, or a same-origin GET-like request
		}
		u, err := url.Parse(origin)
		return err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host)
	}
	return false
}

// maxURLEncoded is the most of a URL-encoded body read into memory, as in
// net/http's ParseForm.
const maxURLEncoded = 10 << 20

// readURLEncoded parses a URL-encoded body into r.PostForm and r.Form, for
// any method (net/http's ParseForm ignores DELETE bodies), and puts the
// body back so handlers can still read it raw.
func readURLEncoded(r *http.Request) error {
	if r.PostForm != nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxURLEncoded+1))
	if err != nil {
		return bodyError(err)
	}
	if len(body) > maxURLEncoded {
		return Errorf(http.StatusRequestEntityTooLarge, "form body is larger than %d bytes", maxURLEncoded)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	vals, err := url.ParseQuery(string(body))
	if err != nil {
		return bodyError(err)
	}
	r.PostForm = vals
	form := url.Values{}
	for k, vs := range vals {
		form[k] = append(form[k], vs...)
	}
	for k, vs := range r.URL.Query() {
		form[k] = append(form[k], vs...)
	}
	r.Form = form
	return nil
}

// formToken reads the "_token" field of a form body.
func formToken(r *http.Request) (string, error) {
	if isURLEncoded(r) {
		if err := readURLEncoded(r); err != nil {
			return "", err
		}
		return r.PostForm.Get("_token"), nil
	}
	if err := r.ParseMultipartForm(MaxMultipartMemory); err != nil {
		return "", bodyError(err)
	}
	if v := r.MultipartForm.Value["_token"]; len(v) > 0 {
		return v[0], nil
	}
	return "", nil
}

func isURLEncoded(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "application/x-www-form-urlencoded" && r.Body != nil
}

func isForm(r *http.Request) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return mt == "application/x-www-form-urlencoded" || mt == "multipart/form-data"
}

func bodyError(err error) error {
	if mbe, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return Errorf(http.StatusRequestEntityTooLarge, "request body is larger than %d bytes", mbe.Limit)
	}
	return Error(http.StatusBadRequest, "malformed form data").Wrap(err)
}

// removeFiles deletes the temporary files of a parsed multipart form; the
// server only removes those of the request it created.
func removeFiles(r *http.Request) {
	if r.MultipartForm != nil {
		_ = r.MultipartForm.RemoveAll()
	}
}

// ---- Redirecting back with errors ----

// redirectBack handles a failed form post from a browser: with a session,
// the field errors and the submitted input are flashed and the browser is
// sent back to the form. It reports whether it responded. htmx requests
// get the 422, except boosted ones (hx-boost), which are page navigations.
func redirectBack(c *Ctx, err error, status int) bool {
	switch {
	case status != http.StatusUnprocessableEntity && status != http.StatusBadRequest,
		!acceptsHTML(c.r), c.WantsJSON(), c.w.started(),
		c.r.Header.Get("HX-Request") != "" && c.r.Header.Get("HX-Boosted") != "true",
		c.r.Method == http.MethodGet, c.r.Method == http.MethodHead:
		return false
	}
	s := session.From(c)
	if s == nil {
		return false
	}
	fields := orderedFieldErrors(err)
	if len(fields) == 0 {
		return false
	}
	s.FlashErrors(fields...)
	if c.r.PostForm != nil {
		s.FlashInput(c.r.PostForm)
	}
	c.Logger().Debug("form invalid; redirecting back", "path", c.r.URL.Path, "fields", len(fields))
	return c.Back() == nil
}

// orderedFieldErrors returns the field messages of err in order: the order
// of a *validate.Errors, else sorted by field.
func orderedFieldErrors(err error) []session.FieldError {
	var msgs map[string]string
	if he, ok := errors.AsType[*HTTPError](err); ok && len(he.Fields) > 0 {
		msgs = he.Fields
	} else if fe := FieldErrorer(nil); errors.As(err, &fe) {
		msgs = fe.FieldErrors()
	}
	if len(msgs) == 0 {
		return nil
	}
	var keys []string
	if ve, ok := errors.AsType[*validate.Errors](err); ok && sameKeys(ve, msgs) {
		keys = ve.Keys()
	} else {
		for k := range msgs {
			keys = append(keys, k)
		}
		slices.Sort(keys)
	}
	out := make([]session.FieldError, 0, len(keys))
	for _, k := range keys {
		out = append(out, session.FieldError{Field: k, Message: msgs[k]})
	}
	return out
}

func sameKeys(ve *validate.Errors, msgs map[string]string) bool {
	if ve.Len() != len(msgs) {
		return false
	}
	for k := range msgs {
		if !ve.Has(k) {
			return false
		}
	}
	return true
}

// acceptsHTML reports whether the client is a browser expecting a page: a
// navigation, or an Accept header listing text/html.
func acceptsHTML(r *http.Request) bool {
	return r.Header.Get("Sec-Fetch-Mode") == "navigate" || strings.Contains(r.Header.Get("Accept"), "text/html")
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
