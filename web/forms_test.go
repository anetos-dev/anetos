// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
)

// browser is an HTTP client with cookies that doesn't follow redirects.
type browser struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	last   string // last page loaded, sent as Referer like a browser
}

func newBrowser(t *testing.T, r *Router) *browser {
	t.Helper()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, srv: srv, client: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type page struct {
	status int
	body   string
	header http.Header
}

func (b *browser) send(method, path string, body io.Reader, headers ...string) page {
	b.t.Helper()
	req, err := http.NewRequest(method, b.srv.URL+path, body)
	if err != nil {
		b.t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html")
	if b.last != "" {
		req.Header.Set("Referer", b.last)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	if method == http.MethodGet {
		b.last = b.srv.URL + path
	}
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return page{res.StatusCode, string(out), res.Header}
}

func (b *browser) get(path string) page { return b.send(http.MethodGet, path, nil) }

func (b *browser) post(path string, form url.Values, headers ...string) page {
	return b.send(http.MethodPost, path, strings.NewReader(form.Encode()),
		append([]string{"Content-Type", "application/x-www-form-urlencoded"}, headers...)...)
}

var tokenRe = regexp.MustCompile(`name="_token" value="([^"]+)"`)

func (p page) token(t *testing.T) string {
	t.Helper()
	m := tokenRe.FindStringSubmatch(p.body)
	if m == nil {
		t.Fatalf("no CSRF field in %q", p.body)
	}
	return m[1]
}

func sessions(t *testing.T) *session.Manager {
	t.Helper()
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.NewEncrypter(k)
	no := false
	cfg := session.DefaultConfig()
	cfg.Secure = &no // httptest serves plain HTTP
	m, err := session.NewManager(cfg, enc)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

type postForm struct {
	Title string `json:"title" validate:"required|max:10"`
	Body  string `json:"body" form:"content" validate:"required"`
	Pass  string `json:"password"`
}

// formPage renders a form with the helpers of the view package.
func formPage(ctx context.Context, w io.Writer) error {
	errs := view.Errors(ctx)
	_, err := fmt.Fprintf(w, "<form>%s title=%q content=%q errors=%v titleErr=%q flash=%q</form>",
		mustString(ctx, view.CSRFField(ctx)), view.Old(ctx, "title", "default"), view.Old(ctx, "content"),
		errs.Keys(), errs.Get("title"), view.Flash(ctx, "status"))
	return err
}

func mustString(ctx context.Context, c view.Component) string {
	s, err := view.String(ctx, c)
	if err != nil {
		panic(err)
	}
	return s
}

func formRouter(t *testing.T) *Router {
	r := newTestRouter()
	r.UseGlobal(MethodOverride)
	g := r.Group("", sessions(t).Middleware, CSRF())
	g.Get("/posts/new", func(c *Ctx) error {
		return c.Render(http.StatusOK, view.ComponentFunc(formPage))
	}).Name("posts.new")
	g.Post("/posts", H(func(c *Ctx, in postForm) (Responder, error) {
		c.Session().Flash("status", "Saved "+in.Title)
		return RedirectRoute("posts.new"), nil
	}))
	g.Delete("/posts/{id}", func(c *Ctx) error { return c.Text(http.StatusOK, "deleted "+c.Param("id")) })
	g.Put("/posts/{id}", func(c *Ctx) error { return c.Text(http.StatusOK, "put "+c.Param("id")) })
	return r
}

func TestFormFlow(t *testing.T) {
	b := newBrowser(t, formRouter(t))
	p := b.get("/posts/new")
	if p.status != 200 || p.header.Get("Content-Type") != "text/html; charset=utf-8" || !strings.Contains(p.body, `title="default"`) {
		t.Fatalf("form page: %d %q", p.status, p.body)
	}
	tok := p.token(t)

	// Invalid: redirected back to the form with errors and input.
	res := b.post("/posts", url.Values{"_token": {tok}, "title": {"far too long title"}, "content": {""}, "password": {"secret"}})
	if res.status != http.StatusSeeOther || res.header.Get("Location") != "/posts/new" {
		t.Fatalf("invalid post: %d %q %q", res.status, res.header.Get("Location"), res.body)
	}
	p = b.get("/posts/new")
	for _, want := range []string{`title="far too long title"`, `errors=[title content]`, `titleErr="The title field must not be greater than 10 characters."`} {
		if !strings.Contains(p.body, want) {
			t.Errorf("missing %s in %q", want, p.body)
		}
	}
	if strings.Contains(p.body, "secret") {
		t.Error("password flashed")
	}
	p = b.get("/posts/new")
	if !strings.Contains(p.body, "errors=[]") || !strings.Contains(p.body, `title="default"`) {
		t.Errorf("errors lasted two requests: %q", p.body)
	}

	// Valid: flash message after the redirect.
	res = b.post("/posts", url.Values{"_token": {p.token(t)}, "title": {"Hi"}, "content": {"x"}})
	if res.status != http.StatusSeeOther {
		t.Fatalf("valid post: %d %q", res.status, res.body)
	}
	if p = b.get("/posts/new"); !strings.Contains(p.body, `flash="Saved Hi"`) {
		t.Errorf("no flash: %q", p.body)
	}

	// JSON clients still get 422.
	res = b.send(http.MethodPost, "/posts", strings.NewReader(`{"title":""}`),
		"Content-Type", "application/json", "Accept", "application/json", "X-CSRF-Token", p.token(t))
	if res.status != http.StatusUnprocessableEntity || !strings.Contains(res.body, `"body"`) {
		t.Errorf("JSON: %d %s", res.status, res.body)
	}
}

func TestCSRF(t *testing.T) {
	b := newBrowser(t, formRouter(t))
	tok := b.get("/posts/new").token(t)
	valid := url.Values{"title": {"Hi"}, "content": {"x"}}

	if res := b.post("/posts", valid); res.status != http.StatusForbidden || !strings.Contains(res.body, "expired") {
		t.Errorf("no token: %d %q", res.status, res.body)
	}
	if res := b.post("/posts", valid, "X-CSRF-Token", "bogus"); res.status != http.StatusForbidden {
		t.Errorf("bad token: %d", res.status)
	}
	if res := b.post("/posts", valid, "X-CSRF-Token", tok); res.status != http.StatusSeeOther {
		t.Errorf("header token: %d %q", res.status, res.body)
	}
	withTok := url.Values{"_token": {tok}, "title": {"Hi"}, "content": {"x"}}
	if res := b.post("/posts", withTok, "Sec-Fetch-Site", "cross-site"); res.status != http.StatusForbidden || !strings.Contains(res.body, "Cross-origin") {
		t.Errorf("cross-site: %d %q", res.status, res.body)
	}
	if res := b.post("/posts", withTok, "Origin", "https://evil.example"); res.status != http.StatusForbidden {
		t.Errorf("foreign origin: %d", res.status)
	}

	// Multipart forms carry the token too.
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range map[string]string{"_token": tok, "title": "Hi", "content": "x"} {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()
	if res := b.send(http.MethodPost, "/posts", &body, "Content-Type", mw.FormDataContentType()); res.status != http.StatusSeeOther {
		t.Errorf("multipart: %d %q", res.status, res.body)
	}

	// Another browser's token doesn't work.
	other := newBrowser(t, formRouter(t))
	other.srv = b.srv
	if res := other.post("/posts", withTok); res.status != http.StatusForbidden {
		t.Errorf("token of another session: %d", res.status)
	}

	// Without the session middleware, CSRF fails loudly.
	r := newTestRouter()
	r.With(CSRF()).Post("/x", text("x"))
	if res := do(t, r, http.MethodPost, "/x", nil); res.status != http.StatusInternalServerError {
		t.Errorf("no session: %d", res.status)
	}
	if res := do(t, r, http.MethodGet, "/x", nil); res.status != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", res.status)
	}
	mustPanic(t, "bad trusted origin", func() { CSRF(TrustedOrigins("not a url")) })
}

func TestMethodOverride(t *testing.T) {
	b := newBrowser(t, formRouter(t))
	tok := b.get("/posts/new").token(t)
	if res := b.post("/posts/7", url.Values{"_method": {"delete"}, "_token": {tok}}); res.body != "deleted 7" {
		t.Errorf("DELETE: %d %q", res.status, res.body)
	}
	if res := b.post("/posts/7", url.Values{"_method": {"PUT"}, "_token": {tok}}); res.body != "put 7" {
		t.Errorf("PUT: %d %q", res.status, res.body)
	}
	if res := b.post("/posts/7", url.Values{"_method": {"GET"}, "_token": {tok}}); res.status != http.StatusMethodNotAllowed {
		t.Errorf("_method=GET: %d %q", res.status, res.body)
	}
	if res := b.send(http.MethodPost, "/posts/7", strings.NewReader("%zz"), "Content-Type", "application/x-www-form-urlencoded"); res.status != http.StatusBadRequest {
		t.Errorf("malformed form: %d", res.status)
	}
	// _method in the query string, for multipart forms.
	for _, m := range []string{"PUT", "DELETE"} {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("_token", tok)
		_ = mw.Close()
		want := map[string]string{"PUT": "put 7", "DELETE": "deleted 7"}[m]
		if res := b.send(http.MethodPost, "/posts/7?_method="+m, &body, "Content-Type", mw.FormDataContentType()); res.body != want {
			t.Errorf("multipart %s: %d %q", m, res.status, res.body)
		}
	}
}

func TestRenderAndHTMX(t *testing.T) {
	r := newTestRouter()
	r.Get("/ok", H(func(c *Ctx, _ struct{}) (Responder, error) {
		return View(view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			u, err := URL(ctx, "fail")
			_, _ = fmt.Fprintf(w, "<p>%s %v</p>", u, err)
			return nil
		})), nil
	}))
	r.Get("/must", func(c *Ctx) error {
		name := c.Query("route")
		if name == "" {
			name = "fail"
		}
		return c.Render(http.StatusOK, view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, _ = fmt.Fprintf(w, "<p>%s</p>", MustURL(ctx, name))
			return nil
		}))
	})
	r.Get("/fail", func(c *Ctx) error {
		return c.Render(http.StatusOK, view.ComponentFunc(func(_ context.Context, w io.Writer) error {
			_, _ = io.WriteString(w, "<p>half")
			return errors.New("broken template")
		}))
	}).Name("fail")
	r.Get("/hx", func(c *Ctx) error {
		h := c.HTMX()
		return c.Text(http.StatusOK, fmt.Sprintf("%v %v %s", c.IsHTMX(), h.Boosted, h.Target))
	})
	if res := do(t, r, http.MethodGet, "/ok", nil); res.status != 200 || res.body != "<p>/fail <nil></p>" {
		t.Errorf("render: %d %q", res.status, res.body)
	}
	if res := do(t, r, http.MethodGet, "/must", nil); res.status != 200 || res.body != "<p>/fail</p>" {
		t.Errorf("MustURL: %d %q", res.status, res.body)
	}
	if res := do(t, r, http.MethodGet, "/must?route=nope", nil); res.status != 500 {
		t.Errorf("MustURL of a route that doesn't exist: %d %q", res.status, res.body)
	}
	if res := do(t, r, http.MethodGet, "/fail", nil); res.status != 500 || strings.Contains(res.body, "half") {
		t.Errorf("failed render: %d %q", res.status, res.body)
	}
	if res := do(t, r, http.MethodHead, "/ok", nil); res.status != 200 || res.body != "" {
		t.Errorf("HEAD: %d %q", res.status, res.body)
	}
	res := do(t, r, http.MethodGet, "/hx", nil, "HX-Request", "true", "HX-Boosted", "true", "HX-Target", "list")
	if res.body != "true true list" || res.header.Get("Vary") != "HX-Request" {
		t.Errorf("htmx: %q %v", res.body, res.header)
	}
	if _, err := URL(context.Background(), "fail"); err == nil {
		t.Error("URL outside a request")
	}
}

func TestBackAndWriteError(t *testing.T) {
	r := newTestRouter()
	r.Post("/back", func(c *Ctx) error { return c.Back() })
	r.With(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			WriteError(w, req, Error(http.StatusTeapot, "no coffee"))
		})
	}).Get("/mw", text("unreachable"))
	for ref, want := range map[string]string{
		"":                                 "/",
		"http://example.com/posts?page=2":  "/posts?page=2",
		"http://evil.example/x":            "/",
		"http://example.com//evil.example": "/",
	} {
		res := do(t, r, http.MethodPost, "/back", nil, "Referer", ref)
		if res.status != http.StatusSeeOther || res.header.Get("Location") != want {
			t.Errorf("Referer %q: %d %q", ref, res.status, res.header.Get("Location"))
		}
	}
	res := do(t, r, http.MethodGet, "/mw", nil, "Accept", "application/json")
	if res.status != http.StatusTeapot || !strings.Contains(res.body, "no coffee") {
		t.Errorf("WriteError: %d %q", res.status, res.body)
	}
	rec := httptest.NewRecorder()
	WriteError(rec, httptest.NewRequest(http.MethodGet, "/", nil), ErrCSRF)
	if rec.Code != http.StatusForbidden {
		t.Errorf("WriteError outside a router: %d", rec.Code)
	}
	mustPanic(t, "Session without middleware", func() {
		(&Ctx{r: httptest.NewRequest(http.MethodGet, "/", nil)}).Session()
	})
}

func TestNoSessionKeeps422(t *testing.T) {
	r := newTestRouter()
	r.Post("/posts", H(func(c *Ctx, in postForm) (Responder, error) { return NoContent(), nil }))
	res := do(t, r, http.MethodPost, "/posts", strings.NewReader("title="), "Content-Type", "application/x-www-form-urlencoded", "Accept", "text/html")
	if res.status != http.StatusUnprocessableEntity {
		t.Errorf("status %d", res.status)
	}
	// Form posts key errors by form field name (content), JSON by json name (body).
	if !strings.Contains(res.body, "content") {
		t.Errorf("form error key: %q", res.body)
	}
	res = do(t, r, http.MethodPost, "/posts", strings.NewReader(`{}`), "Content-Type", "application/json", "Accept", "application/json")
	if !strings.Contains(res.body, `"body"`) || strings.Contains(res.body, `"content"`) {
		t.Errorf("JSON error key: %q", res.body)
	}
}

func TestConversionErrorRedirectsBack(t *testing.T) {
	type qty struct {
		Count int `json:"count"`
	}
	r := newTestRouter()
	g := r.Group("", sessions(t).Middleware, CSRF())
	g.Get("/form", func(c *Ctx) error {
		return c.Render(http.StatusOK, view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
			_, err := fmt.Fprintf(w, "%s errors=%v old=%q", mustString(ctx, view.CSRFField(ctx)), view.Errors(ctx).FieldErrors(), view.Old(ctx, "count"))
			return err
		}))
	})
	g.Post("/form", H(func(c *Ctx, in qty) (Responder, error) { return NoContent(), nil }))
	b := newBrowser(t, r)
	tok := b.get("/form").token(t)
	res := b.post("/form", url.Values{"_token": {tok}, "count": {"many"}})
	if res.status != http.StatusSeeOther || res.header.Get("Location") != "/form" {
		t.Fatalf("status %d %q", res.status, res.body)
	}
	if p := b.get("/form"); !strings.Contains(p.body, "map[count:") || !strings.Contains(p.body, `old="many"`) {
		t.Errorf("page: %q", p.body)
	}
}

// MethodOverride restores URL-encoded bodies, so handlers can read them raw
// (webhook signatures).
func TestMethodOverrideKeepsBody(t *testing.T) {
	r := newTestRouter()
	r.UseGlobal(MethodOverride)
	r.Post("/hook", func(c *Ctx) error {
		b, _ := io.ReadAll(c.Request().Body)
		return c.Text(http.StatusOK, string(b))
	})
	res := do(t, r, http.MethodPost, "/hook", strings.NewReader("a=1&b=2"), "Content-Type", "application/x-www-form-urlencoded")
	if res.body != "a=1&b=2" {
		t.Errorf("body %q", res.body)
	}
}

func TestRedirectBackSkips(t *testing.T) {
	b := newBrowser(t, formRouter(t))
	tok := b.get("/posts/new").token(t)
	bad := url.Values{"_token": {tok}, "title": {""}, "content": {""}}
	// htmx requests get the 422 to render in place.
	if res := b.post("/posts", bad, "HX-Request", "true"); res.status != http.StatusUnprocessableEntity {
		t.Errorf("htmx: %d", res.status)
	}
	// Boosted forms (hx-boost) are navigations: redirected back.
	if res := b.post("/posts", bad, "HX-Request", "true", "HX-Boosted", "true"); res.status != http.StatusSeeOther {
		t.Errorf("boosted: %d", res.status)
	}
	// Clients that don't ask for HTML get the 422 too.
	if res := b.post("/posts", bad, "Accept", "*/*"); res.status != http.StatusUnprocessableEntity {
		t.Errorf("Accept */*: %d", res.status)
	}
	// A navigation without an Accept header is a browser.
	if res := b.post("/posts", bad, "Accept", "", "Sec-Fetch-Mode", "navigate"); res.status != http.StatusSeeOther {
		t.Errorf("navigation: %d", res.status)
	}
}

func TestOverriddenDeleteBindsBody(t *testing.T) {
	type reason struct {
		Reason string `json:"reason" validate:"required"`
	}
	r := newTestRouter()
	r.UseGlobal(MethodOverride)
	g := r.Group("", sessions(t).Middleware, CSRF())
	g.Get("/form", func(c *Ctx) error { return c.Render(http.StatusOK, view.CSRFField(c)) })
	g.Delete("/items/{id}", H(func(c *Ctx, in reason) (Responder, error) {
		return Text(http.StatusOK, "deleted for "+in.Reason+" "+c.Request().FormValue("reason")), nil
	}))
	b := newBrowser(t, r)
	tok := b.get("/form").token(t)
	res := b.post("/items/1", url.Values{"_method": {"DELETE"}, "_token": {tok}, "reason": {"spam"}})
	if res.body != "deleted for spam spam" {
		t.Errorf("%d %q", res.status, res.body)
	}
	// Bodies over 10 MB aren't read into memory.
	big := strings.NewReader("a=" + strings.Repeat("x", 10<<20))
	if res := do(t, r, http.MethodPost, "/items/1", big, "Content-Type", "application/x-www-form-urlencoded"); res.status != http.StatusRequestEntityTooLarge {
		t.Errorf("big body: %d", res.status)
	}
}

func TestBackBehindProxy(t *testing.T) {
	r := newTestRouter()
	r.Post("/back", func(c *Ctx) error { return c.Back() })
	res := do(t, r, http.MethodPost, "/back", nil, "Referer", "https://public.example/posts/new", "Sec-Fetch-Site", "same-origin")
	if res.header.Get("Location") != "/posts/new" {
		t.Errorf("same-origin Referer behind a proxy: %q", res.header.Get("Location"))
	}
	res = do(t, r, http.MethodPost, "/back", nil, "Referer", "https://public.example/posts/new", "Sec-Fetch-Site", "cross-site")
	if res.header.Get("Location") != "/" {
		t.Errorf("cross-site Referer: %q", res.header.Get("Location"))
	}
}

func TestPageURL(t *testing.T) {
	r := NewRouter()
	var got []string
	r.Get("/posts", func(c *Ctx) error {
		got = append(got, PageURL(c, 3), PageURL(c.Request().Context(), 1))
		return c.NoContent()
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/posts?q=go+tips&page=2&per_page=5&x=a;b&%70age=9", nil))
	if want := "?q=go+tips&per_page=5&x=a;b&page=3"; len(got) != 2 || got[0] != want || got[1] != "?q=go+tips&per_page=5&x=a;b&page=1" {
		t.Errorf("PageURL = %q", got)
	}
	got = nil
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/posts", nil))
	if len(got) != 2 || got[0] != "?page=3" {
		t.Errorf("PageURL without a query = %q", got)
	}
	got = nil
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/posts?q=a:b&page=2", nil))
	if len(got) != 2 || got[0] != "?q=a%3Ab&page=3" {
		t.Errorf("PageURL of a query with a colon = %q", got)
	}
	if u := PageURL(context.Background(), 2); u != "?page=2" {
		t.Errorf("outside a request: %q", u)
	}
}

func TestRouteIs(t *testing.T) {
	r := NewRouter()
	var got []bool
	check := func(c *Ctx) error {
		got = append(got, RouteIs(c, "issues.index"), RouteIs(c, "issues.*"), RouteIs(c, "home", "issues.show"), RouteIs(c, "issue*"), RouteIs(c))
		return c.NoContent()
	}
	r.Get("/issues", check).Name("issues.index")
	r.Get("/issues/{id}", check).Name("issues.show")
	r.Get("/unnamed", check)
	for path, want := range map[string][]bool{
		"/issues":   {true, true, false, false, false}, // issue* isn't a prefix: .* is
		"/issues/1": {false, true, true, false, false},
		"/unnamed":  {false, false, false, false, false},
	} {
		got = nil
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
		if !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
	if RouteIs(context.Background(), "issues.*") {
		t.Error("outside a request")
	}
}

// Only HTML forms from this site are overridden: a text/plain post (a
// cross-site "simple" request) or a cross-site form keeps its method.
func TestMethodOverrideOnlyForms(t *testing.T) {
	r := newTestRouter()
	r.UseGlobal(MethodOverride)
	r.Post("/account", func(c *Ctx) error { return c.Text(http.StatusOK, "post") })
	r.Delete("/account", func(c *Ctx) error { return c.Text(http.StatusOK, "deleted") })
	for _, tc := range []struct {
		ct, site, origin, want string
	}{
		{"application/x-www-form-urlencoded", "", "", "deleted"},
		{"application/x-www-form-urlencoded", "same-origin", "", "deleted"},
		{"text/plain", "", "", "post"},
		{"application/json", "", "", "post"},
		{"application/x-www-form-urlencoded", "cross-site", "", "post"},
		// Browsers without Sec-Fetch-Site: the Origin decides.
		{"application/x-www-form-urlencoded", "", "http://example.com", "deleted"},
		{"application/x-www-form-urlencoded", "", "https://evil.example", "post"},
		{"application/x-www-form-urlencoded", "", "null", "post"},
	} {
		headers := []string{"Content-Type", tc.ct}
		if tc.site != "" {
			headers = append(headers, "Sec-Fetch-Site", tc.site)
		}
		if tc.origin != "" {
			headers = append(headers, "Origin", tc.origin)
		}
		if res := do(t, r, http.MethodPost, "/account?_method=DELETE", strings.NewReader("x"), headers...); res.body != tc.want {
			t.Errorf("%s from %q %q: %q, want %q", tc.ct, tc.site, tc.origin, res.body, tc.want)
		}
	}
	for _, u := range []string{"/\t/evil.example", "/a\nb", "//evil.example", `/\evil.example`} {
		if localPath(u) {
			t.Errorf("localPath(%q) = true", u)
		}
	}
}
