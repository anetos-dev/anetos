// SPDX-License-Identifier: Apache-2.0

package vite_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos/config"
	"anetos.dev/anetos/view/vite"
	"anetos.dev/anetos/web"
)

// A built SPA: its files, index.html for pages' paths and the root, 404
// for the rest; problem details with web.JSONErrors.
func TestSPA(t *testing.T) {
	files := fstest.MapFS{
		"spa/index.html":           {Data: []byte("<!doctype html><div id=app></div>")},
		"spa/favicon.svg":          {Data: []byte("<svg/>")},
		"spa/assets/main-abc12.js": {Data: []byte(strings.Repeat("console.log(1);\n", 200))},
		"spa/.hidden":              {Data: []byte("secret")},
		"public.go":                {Data: []byte("package public")},
	}
	app := newApp(t, config.Map{"APP_ENV": "production", "APP_URL": "https://app.example.com"})
	h, err := vite.SPA(app, files, vite.Except("/api/"))
	if err != nil {
		t.Fatal(err)
	}
	r := web.NewRouter()
	r.UseGlobal(web.JSONErrors)
	r.Get("/api/v1/me", func(c *web.Ctx) error { return c.JSON(http.StatusOK, map[string]string{"me": "ada"}) })
	r.Fallback(h)
	serve := func(method, target string, headers ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, nil)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	html := "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8"
	for _, c := range []struct {
		method, target, accept string
		status                 int
		body, cache            string
	}{
		{"GET", "/", "", 200, "<div id=app>", "no-cache"},
		{"GET", "/bookmarks/12", html, 200, "<div id=app>", "no-cache"},
		{"GET", "/index.html", html, 200, "<div id=app>", "no-cache"},
		{"GET", "/favicon.svg", "", 200, "<svg/>", "no-cache"},
		{"GET", "/assets/main-abc12.js", "", 200, "console.log", "public, max-age=31536000, immutable"},
		{"HEAD", "/login", html, 200, "", "no-cache"},
		{"GET", "/api/v1/me", html, 200, `"ada"`, ""},
		{"GET", "/api/v1/nope", "application/json", 404, `"status": 404`, ""},
		{"GET", "/api/v1/nope", html, 404, `"status": 404`, ""}, // Except: never the page
		{"GET", "/api", html, 404, `"status": 404`, ""},
		{"GET", "/apis", html, 200, "<div id=app>", "no-cache"},
		{"GET", "/assets/missing.js", "*/*", 404, `"status": 404`, ""},
		{"POST", "/login", html, 404, `"status": 404`, ""},
		{"GET", "/.hidden", "", 404, `"status": 404`, ""},
		{"GET", "/public.go", "", 404, `"status": 404`, ""},
	} {
		rec := serve(c.method, c.target, "Accept", c.accept)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.body) || c.cache != "" && rec.Header().Get("Cache-Control") != c.cache {
			t.Errorf("%s %s (%s): %d %q %v", c.method, c.target, c.accept, rec.Code, rec.Body, rec.Header())
		}
	}
	// The page and its 404 depend on Accept: caches must keep them apart.
	// Files don't.
	if rec := serve("GET", "/bookmarks/12", "Accept", html); rec.Header().Get("Vary") != "Accept" {
		t.Errorf("page Vary: %v", rec.Header())
	}
	if rec := serve("GET", "/bookmarks/12", "Accept", "application/json"); rec.Code != 404 || rec.Header().Get("Vary") != "Accept" {
		t.Errorf("404 Vary: %d %v", rec.Code, rec.Header())
	}
	if rec := serve("GET", "/favicon.svg"); slices.Contains(rec.Header().Values("Vary"), "Accept") {
		t.Errorf("file Vary: %v", rec.Header())
	}
	// text/html refused (q=0) isn't a browser's page.
	if rec := serve("GET", "/bookmarks/12", "Accept", "application/json, text/html;q=0"); rec.Code != 404 {
		t.Errorf("text/html;q=0: %d", rec.Code)
	}
	if rec := serve("GET", "/bookmarks/12", "Accept", "application/json;q=0.9, TEXT/HTML ; q=0.5"); rec.Code != 200 {
		t.Errorf("TEXT/HTML;q=0.5: %d", rec.Code)
	}
	// gzipped for clients that take it
	if rec := serve("GET", "/assets/main-abc12.js", "Accept-Encoding", "gzip"); rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("gzip: %v", rec.Header())
	}

	// Doctor: deployed, APP_URL's origin must be first-party.
	var out bytes.Buffer
	app.ExecuteArgs(context.Background(), []string{"doctor"}, &out, &out)
	if !strings.Contains(out.String(), "HTTP_SPA_ORIGINS doesn't list https://app.example.com") {
		t.Errorf("doctor:\n%s", out.String())
	}
	// The scheme's default port is the same origin.
	listed := newApp(t, config.Map{"APP_ENV": "production", "APP_URL": "https://App.example.com:443/", "HTTP_SPA_ORIGINS": "https://app.example.com"})
	if _, err := vite.SPA(listed, files); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	listed.ExecuteArgs(context.Background(), []string{"doctor"}, &out, &out)
	if strings.Contains(out.String(), "HTTP_SPA_ORIGINS doesn't list") {
		t.Errorf("doctor with the origin listed:\n%s", out.String())
	}
}

// Without a build: an error where the app is deployed; a page saying so
// elsewhere.
func TestSPAWithoutBuild(t *testing.T) {
	files := fstest.MapFS{"public.go": {Data: []byte("package public")}}
	if _, err := vite.SPA(newApp(t, config.Map{"APP_ENV": "production"}), files); err == nil || !strings.Contains(err.Error(), "public/spa/index.html is missing") {
		t.Errorf("production: %v", err)
	}
	if _, err := vite.SPA(newApp(t, config.Map{"APP_ENV": "development"}), nil); err == nil {
		t.Error("nil files")
	}
	h, err := vite.SPA(newApp(t, config.Map{"APP_ENV": "development"}), files)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "isn't built") {
		t.Errorf("without a build: %d %s", rec.Code, rec.Body)
	}
}
