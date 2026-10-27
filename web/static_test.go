// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"anetos.dev/anetos/web"
)

func TestStatic(t *testing.T) {
	files := fstest.MapFS{
		"robots.txt":               {Data: []byte("User-agent: *\n")},
		".well-known/security.txt": {Data: []byte("Contact: mailto:security@example.com\n")},
		".env":                     {Data: []byte("APP_KEY=secret")},
		"_private/notes.txt":       {Data: []byte("no")},
		"public.go":                {Data: []byte("package public")},
		"OLD.GO":                   {Data: []byte("package public")},
		"static/app.css":           {Data: []byte("body{}")},
		"docs/.hidden/x.txt":       {Data: []byte("no")},
		"about.txt":                {Data: []byte("routes win")},
		"logout":                   {Data: []byte("no: a POST route has this path")},
	}
	r := web.NewRouter()
	r.Get("/about.txt", func(c *web.Ctx) error { return c.Text(http.StatusOK, "the route") })
	r.Post("/logout", func(c *web.Ctx) error { return c.NoContent() })
	r.Static("/", files)
	docs := web.NewRouter()
	docs.Group("/docs").Static("/", fstest.MapFS{"a.txt": {Data: []byte("docs a")}})
	// A host's files win over the others, whatever the order.
	hosts := web.NewRouter()
	hosts.Static("/", fstest.MapFS{"robots.txt": {Data: []byte("any host")}})
	hosts.Host("admin.example.com").Static("/", fstest.MapFS{"robots.txt": {Data: []byte("admin host")}})

	for _, tc := range []struct {
		router       *web.Router
		method, path string
		status       int
		body         string
	}{
		{r, "GET", "/robots.txt", 200, "User-agent: *\n"},
		{r, "HEAD", "/robots.txt", 200, ""},
		{r, "GET", "/.well-known/security.txt", 200, "Contact: mailto:security@example.com\n"},
		{r, "GET", "/static/app.css", 200, "body{}"},
		{r, "GET", "/about.txt", 200, "the route"},
		{r, "GET", "/logout", 405, ""},
		{r, "POST", "/robots.txt", 404, ""},
		{r, "GET", "/.env", 404, ""},
		{r, "GET", "/_private/notes.txt", 404, ""},
		{r, "GET", "/public.go", 404, ""},
		{r, "GET", "/OLD.GO", 404, ""},
		{r, "GET", "/docs/.hidden/x.txt", 404, ""},
		{r, "GET", "/static", 404, ""},
		{r, "GET", "/static/", 404, ""},
		{r, "GET", "/missing.txt", 404, ""},
		{docs, "GET", "/docs/a.txt", 200, "docs a"},
		{docs, "GET", "/a.txt", 404, ""},
		{hosts, "GET", "http://admin.example.com/robots.txt", 200, "admin host"},
		{hosts, "GET", "http://www.example.com/robots.txt", 200, "any host"},
	} {
		rec := httptest.NewRecorder()
		tc.router.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != tc.status || tc.body != "" && string(body) != tc.body {
			t.Errorf("%s %s = %d %q, want %d %q", tc.method, tc.path, rec.Code, body, tc.status, tc.body)
		}
	}
}

// Fallback serves what no route and no Static file takes; a path with
// routes for other methods still answers 405.
func TestFallback(t *testing.T) {
	r := web.NewRouter()
	r.Static("/", fstest.MapFS{"robots.txt": {Data: []byte("User-agent: *")}})
	r.Post("/login", func(c *web.Ctx) error { return c.NoContent() })
	r.Fallback(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write([]byte("spa")) }))
	for target, want := range map[string]string{"/robots.txt": "User-agent", "/notes/1": "spa", "/": "spa"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %q", target, rec.Code, rec.Body)
		}
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /login: %d", rec.Code)
	}
	for name, fn := range map[string]func(){
		"twice": func() { r.Fallback(http.NotFoundHandler()) },
		"group": func() { web.NewRouter().Group("/x").Fallback(http.NotFoundHandler()) },
		"with":  func() { web.NewRouter().With(web.JSONErrors).Fallback(http.NotFoundHandler()) },
		"host":  func() { web.NewRouter().Host("a.example.com").Fallback(http.NotFoundHandler()) },
		"nil":   func() { web.NewRouter().Fallback(nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s didn't panic", name)
				}
			}()
			fn()
		}()
	}
}
