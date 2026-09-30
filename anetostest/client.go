// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/session"
)

// Response is the app's response to a test request. Its assertion methods
// report failures with t.Errorf and return the response, so they chain:
//
//	app.Get("/posts").AssertOK().AssertSee("Hello")
type Response struct {
	StatusCode int           // the status code
	Header     http.Header   // the response headers
	Body       []byte        // the whole body
	Request    *http.Request // the request that was sent
	app        *App
}

// Get sends a GET request, as a browser following a link.
func (a *App) Get(path string) *Response {
	a.t.Helper()
	return a.send(http.MethodGet, path, nil, "", "text/html")
}

// Head sends a HEAD request.
func (a *App) Head(path string) *Response {
	a.t.Helper()
	return a.send(http.MethodHead, path, nil, "", "text/html")
}

// Delete sends a DELETE request without a body.
func (a *App) Delete(path string) *Response {
	a.t.Helper()
	return a.send(http.MethodDelete, path, nil, "", "text/html")
}

// PostForm sends a POST request with an URL-encoded form, as a browser
// submitting a form. The session's CSRF token is sent in the X-CSRF-Token
// header, unless form has a "_token" field or the header is set.
func (a *App) PostForm(path string, form url.Values) *Response {
	a.t.Helper()
	return a.sendForm(http.MethodPost, path, form)
}

// PutForm sends a PUT request with an URL-encoded form. Browsers send
// forms with POST only; to test a form using a "_method" field (see
// web.MethodOverride), use [App.PostForm] with the field.
func (a *App) PutForm(path string, form url.Values) *Response {
	a.t.Helper()
	return a.sendForm(http.MethodPut, path, form)
}

// PatchForm sends a PATCH request with an URL-encoded form.
func (a *App) PatchForm(path string, form url.Values) *Response {
	a.t.Helper()
	return a.sendForm(http.MethodPatch, path, form)
}

// DeleteForm sends a DELETE request with an URL-encoded form.
func (a *App) DeleteForm(path string, form url.Values) *Response {
	a.t.Helper()
	return a.sendForm(http.MethodDelete, path, form)
}

// GetJSON sends a GET request accepting JSON, as an API client.
func (a *App) GetJSON(path string) *Response {
	a.t.Helper()
	return a.send(http.MethodGet, path, nil, "", "application/json")
}

// PostJSON sends a POST request with body encoded as JSON, accepting JSON.
func (a *App) PostJSON(path string, body any) *Response {
	a.t.Helper()
	return a.sendJSON(http.MethodPost, path, body)
}

// PutJSON sends a PUT request with body encoded as JSON, accepting JSON.
func (a *App) PutJSON(path string, body any) *Response {
	a.t.Helper()
	return a.sendJSON(http.MethodPut, path, body)
}

// PatchJSON sends a PATCH request with body encoded as JSON, accepting
// JSON.
func (a *App) PatchJSON(path string, body any) *Response {
	a.t.Helper()
	return a.sendJSON(http.MethodPatch, path, body)
}

// DeleteJSON sends a DELETE request accepting JSON, without a body.
func (a *App) DeleteJSON(path string) *Response {
	a.t.Helper()
	return a.send(http.MethodDelete, path, nil, "", "application/json")
}

// Do sends req, adding what the other methods add when it lacks them: the
// jar's cookies (by name), the headers set with [App.WithHeader], the
// Referer, and (for methods other than GET, HEAD, OPTIONS and TRACE) the
// X-CSRF-Token header, which takes precedence over a "_token" form field.
// It runs with the test's context ([App.Context]). Build req with
// httptest.NewRequest and a path:
//
//	req := httptest.NewRequest(http.MethodDelete, "/notes/1", nil)
//	req.Header.Set("HX-Request", "true")
//	app.Do(req).AssertOK()
func (a *App) Do(req *http.Request) *Response {
	a.t.Helper()
	req = req.WithContext(a.ctx)
	if req.URL.Host == "" { // a path: send it to the test site
		req.URL = base.ResolveReference(req.URL)
		if req.Host == "" || req.Host == "example.com" { // httptest's default
			req.Host = base.Host
		}
	}
	return a.do(req, false, false)
}

func (a *App) sendForm(method, path string, form url.Values) *Response {
	a.t.Helper()
	return a.sendWith(method, path, strings.NewReader(form.Encode()), "application/x-www-form-urlencoded", "text/html", form.Has("_token"))
}

func (a *App) sendJSON(method, path string, body any) *Response {
	a.t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		a.t.Fatalf("anetostest: %s %s: encode the body: %v", method, path, err)
	}
	return a.send(method, path, bytes.NewReader(b), "application/json", "application/json")
}

func (a *App) send(method, path string, body io.Reader, contentType, accept string) *Response {
	a.t.Helper()
	return a.sendWith(method, path, body, contentType, accept, false)
}

func (a *App) sendWith(method, path string, body io.Reader, contentType, accept string, hasToken bool) *Response {
	a.t.Helper()
	u, err := base.Parse(path)
	if err != nil {
		a.t.Fatalf("anetostest: %s %q: %v", method, path, err)
	}
	if u.Host != base.Host {
		a.t.Fatalf("anetostest: %s %s: not the test site (%s); use a path", method, path, base.Host)
	}
	// Like httptest.NewRequest, without its panic on a malformed target.
	req, err := http.NewRequestWithContext(a.ctx, method, u.String(), body)
	if err != nil {
		a.t.Fatalf("anetostest: %s %q: %v", method, path, err)
	}
	req.RequestURI = u.RequestURI()
	req.RemoteAddr = "192.0.2.1:1234"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", accept)
	return a.do(req, hasToken, true)
}

// do sends req to the router. defaults: req's Accept and Content-Type
// are the method's defaults, which WithHeader replaces.
func (a *App) do(req *http.Request, hasToken, defaults bool) *Response {
	a.t.Helper()
	if a.router == nil {
		a.t.Fatalf("anetostest: %s %s: setup returned no server", req.Method, req.URL.Path)
	}
	for k, vs := range a.headers {
		if _, ok := req.Header[k]; !ok || (defaults && (k == "Accept" || k == "Content-Type")) {
			req.Header[k] = slices.Clone(vs)
		}
	}
	if req.Header.Get("Referer") == "" && a.referer != "" {
		req.Header.Set("Referer", a.referer)
	}
	if a.sessions != nil && !hasToken && unsafeMethod(req.Method) && req.Header.Get("X-CSRF-Token") == "" {
		var token string
		a.editSession(func(s *session.Session) { token = s.Token() })
		req.Header.Set("X-CSRF-Token", token)
	}
	for _, c := range a.jar.forPath(req.URL.Path) {
		if _, err := req.Cookie(c.Name); err != nil { // the request's own cookies win
			req.AddCookie(c)
		}
	}

	rec := httptest.NewRecorder()
	if a.tx {
		// Each request runs in a savepoint, so that a failed statement
		// (which aborts a PostgreSQL transaction) doesn't break the rest of
		// the test.
		a.exec(req, "SAVEPOINT anetostest_request")
		a.router.ServeHTTP(rec, req)
		if _, err := db.Exec(a.ctx, "RELEASE SAVEPOINT anetostest_request"); err != nil {
			// A statement failed (PostgreSQL): undo the request's writes.
			a.exec(req, "ROLLBACK TO SAVEPOINT anetostest_request")
			a.exec(req, "RELEASE SAVEPOINT anetostest_request")
		}
	} else {
		a.router.ServeHTTP(rec, req)
	}
	res := rec.Result()
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	a.jar.set(res.Cookies())
	if isPage(req, res) {
		a.referer = req.URL.String() // the page the "browser" is on
	}
	return &Response{StatusCode: res.StatusCode, Header: res.Header, Body: body, Request: req, app: a}
}

// isPage reports whether a response is a page the browser shows (so the
// Referer of later requests): a successful GET of HTML, not an htmx
// fragment.
func isPage(req *http.Request, res *http.Response) bool {
	ct := res.Header.Get("Content-Type")
	return req.Method == http.MethodGet && res.StatusCode == http.StatusOK &&
		(ct == "" || strings.HasPrefix(ct, "text/html")) &&
		(req.Header.Get("HX-Request") != "true" || req.Header.Get("HX-Boosted") == "true")
}

func (a *App) exec(req *http.Request, query string) {
	a.t.Helper()
	if _, err := db.Exec(a.ctx, query); err != nil {
		a.t.Fatalf("anetostest: %s %s: %s: %v\n"+
			"The request left the test's transaction unusable: a query canceled by a timeout closes its connection "+
			"(PostgreSQL, MySQL), and MySQL commits on schema changes. Test this with anetostest.WithoutTransaction().",
			req.Method, req.URL.Path, query, err)
	}
}

func unsafeMethod(m string) bool {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	}
	return true
}

// Follow sends a GET request to the redirect's Location, as a browser
// does. The response must be a redirect.
func (r *Response) Follow() *Response {
	r.app.t.Helper()
	loc := r.Header.Get("Location")
	if r.StatusCode < 300 || r.StatusCode > 399 || loc == "" {
		r.app.t.Fatalf("anetostest: Follow: %s %s responded %d, not a redirect%s", r.Request.Method, r.Request.URL.Path, r.StatusCode, r.excerpt())
	}
	u, err := r.Request.URL.Parse(loc)
	if err != nil {
		r.app.t.Fatalf("anetostest: Follow: Location %q: %v", loc, err)
	}
	if u.Host != base.Host {
		r.app.t.Fatalf("anetostest: Follow: %s is on another site; check it with AssertRedirect", loc)
	}
	accept := r.Request.Header.Get("Accept")
	if accept == "" {
		accept = "text/html"
	}
	return r.app.send(http.MethodGet, u.String(), nil, "", accept)
}

// Text returns the body as a string.
func (r *Response) Text() string { return string(r.Body) }

// JSON decodes the body into v, failing the test if it can't.
func (r *Response) JSON(v any) *Response {
	r.app.t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		r.app.t.Fatalf("anetostest: %s %s: decode the JSON body: %v%s", r.Request.Method, r.Request.URL.Path, err, r.excerpt())
	}
	return r
}

// excerpt is the start of the body, for failure messages.
func (r *Response) excerpt() string {
	const max = 2000
	b := bytes.TrimSpace(r.Body)
	if len(b) == 0 {
		return ""
	}
	s := string(b)
	if len(s) > max {
		s = s[:max] + "…"
	}
	return "\nbody:\n" + s
}
