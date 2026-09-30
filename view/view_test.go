// SPDX-License-Identifier: Apache-2.0

package view_test

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/view/htmx"
)

func render(t *testing.T, ctx context.Context, c view.Component) (string, error) {
	t.Helper()
	return view.String(ctx, c)
}

func TestHelpersWithoutSession(t *testing.T) {
	ctx := context.Background()
	if _, err := render(t, ctx, view.CSRFField(ctx)); !errors.Is(err, view.ErrNoSession) {
		t.Errorf("CSRFField without session: %v", err)
	}
	if view.CSRFToken(ctx) != "" || view.Flash(ctx, "x") != "" || view.Errors(ctx).Len() != 0 {
		t.Error("helpers without a session should be empty")
	}
	if view.Old(ctx, "title") != "" || view.Old(ctx, "title", "fallback") != "fallback" {
		t.Error("Old fallback")
	}
}

func TestHelpersWithSession(t *testing.T) {
	s := session.New()
	ctx := session.NewContext(context.Background(), s)
	out, err := render(t, ctx, view.CSRFField(ctx))
	if err != nil || !strings.HasPrefix(out, `<input type="hidden" name="_token" value="`) {
		t.Fatalf("CSRFField = %q, %v", out, err)
	}
	tok := strings.TrimSuffix(strings.TrimPrefix(out, `<input type="hidden" name="_token" value="`), `">`)
	if !s.VerifyToken(tok) || !s.VerifyToken(view.CSRFToken(ctx)) {
		t.Error("rendered token doesn't verify")
	}
	s.Flash("status", "Saved.")
	if view.Flash(ctx, "status") != "Saved." {
		t.Error("Flash")
	}
	for m, want := range map[string]string{"put": `value="PUT"`, "DELETE": `value="DELETE"`} {
		if out, err := render(t, ctx, view.MethodField(m)); err != nil || !strings.Contains(out, want) {
			t.Errorf("MethodField(%s) = %q %v", m, out, err)
		}
	}
	if _, err := render(t, ctx, view.MethodField("GET")); err == nil {
		t.Error("MethodField(GET) accepted")
	}
}

// Errors and Old read what the previous request flashed; the session
// middleware carries them over.
func TestErrorsAndOldAfterRedirect(t *testing.T) {
	m := testManager(t)
	var cookie *http.Cookie
	run := func(fn func(ctx context.Context)) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fn(r.Context()) })).ServeHTTP(rec, req)
		for _, c := range rec.Result().Cookies() {
			cookie = c
		}
	}
	run(func(ctx context.Context) {
		s := session.From(ctx)
		s.FlashErrors(session.FieldError{Field: "title", Message: "Required."}, session.FieldError{Field: "body", Message: "Short."})
		s.FlashInput(url.Values{"title": {"x"}})
	})
	run(func(ctx context.Context) {
		errs := view.Errors(ctx)
		if strings.Join(errs.Keys(), ",") != "title,body" || errs.Get("body") != "Short." {
			t.Errorf("errors = %v", errs.FieldErrors())
		}
		if view.Old(ctx, "title", "fallback") != "x" || view.Old(ctx, "body", "fallback") != "fallback" {
			t.Error("Old")
		}
	})
}

func TestTemplate(t *testing.T) {
	tmpl := template.Must(template.New("page").Parse(`<h1>{{.}}</h1>`))
	out, err := render(t, context.Background(), view.Template(tmpl, "page", "<b>"))
	if err != nil || out != "<h1>&lt;b&gt;</h1>" {
		t.Errorf("Template = %q, %v", out, err)
	}
	if _, err := render(t, context.Background(), view.Template(tmpl, "missing", nil)); err == nil {
		t.Error("missing template")
	}
}

func TestAssets(t *testing.T) {
	files := fstest.MapFS{
		"app.css":         {Data: []byte("body{}")},
		"js/app.js":       {Data: []byte("1")},
		".env":            {Data: []byte("SECRET=1")},
		".git/config":     {Data: []byte("x")},
		"htmx.min.js":     {Data: []byte("overridden")},
		"img/.hidden.png": {Data: []byte("x")},
	}
	a, err := view.NewAssets("/assets/", files, htmx.FS)
	if err != nil {
		t.Fatal(err)
	}
	u := a.URL("app.css")
	if !strings.HasPrefix(u, "/assets/app.css?v=") || len(u) != len("/assets/app.css?v=")+10 {
		t.Errorf("URL = %q", u)
	}
	if a.URL("/js/app.js") == "/assets/js/app.js" || a.URL("missing.css") != "/assets/missing.css" {
		t.Errorf("URLs: %q %q", a.URL("/js/app.js"), a.URL("missing.css"))
	}
	if got := a.URL("my file#1.css"); got != "/assets/my%20file%231.css" {
		t.Errorf("escaping: %q", got)
	}
	if !a.Has("htmx.min.js") || a.Has(".env") || a.Has("img/.hidden.png") {
		t.Error("Has")
	}

	get := func(target string, headers ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, req)
		return rec
	}
	rec := get(u)
	if rec.Code != 200 || rec.Body.String() != "body{}" || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Errorf("versioned: %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	rec = get("/assets/app.css")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("unversioned: %d %v", rec.Code, rec.Header())
	}
	if rec := get("/assets/app.css", "If-None-Match", rec.Header().Get("ETag")); rec.Code != http.StatusNotModified {
		t.Errorf("revalidation: %d", rec.Code)
	}
	if rec := get("/assets/htmx.min.js"); rec.Body.String() != "overridden" {
		t.Error("first file system should win")
	}
	for _, p := range []string{"/assets/.env", "/assets/.git/config", "/assets/missing", "/assets/js/../app.css", "/other/app.css", "/assets/"} {
		if rec := get(p); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, u, nil)
	rec = httptest.NewRecorder()
	a.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
	if _, err := view.NewAssets("assets", files); err == nil {
		t.Error("relative prefix accepted")
	}
}

func TestBundledHTMX(t *testing.T) {
	a, err := view.NewAssets("/assets", htmx.FS)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, a.URL("htmx.min.js"), nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "htmx") || rec.Body.Len() < 40000 {
		t.Errorf("htmx: %d, %d bytes", rec.Code, rec.Body.Len())
	}
}

func testManager(t *testing.T) *session.Manager {
	t.Helper()
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.New(k)
	m, err := session.NewManager(session.DefaultConfig(), enc)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
