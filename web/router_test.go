// SPDX-License-Identifier: Apache-2.0

package web

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newTestRouter(opts ...RouterOption) *Router {
	return NewRouter(append([]RouterOption{WithLogger(slog.New(slog.DiscardHandler))}, opts...)...)
}

type result struct {
	status int
	body   string
	header http.Header
}

func do(t *testing.T, h http.Handler, method, target string, body io.Reader, headers ...string) result {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return result{rec.Code, rec.Body.String(), rec.Header()}
}

func text(s string) HandlerFunc { return func(c *Ctx) error { return c.Text(http.StatusOK, s) } }

func TestRoutingBasics(t *testing.T) {
	r := newTestRouter()
	r.Get("/", text("home"))
	r.Get("/posts", text("index"))
	r.Get("/posts/{id}", func(c *Ctx) error { return c.Text(200, "show "+c.Param("id")) })
	r.Post("/posts", text("store"))
	r.Put("/posts/{id}", text("put"))
	r.Patch("/posts/{id}", text("patch"))
	r.Delete("/posts/{id}", text("delete"))
	r.Get("/files/{path...}", func(c *Ctx) error { return c.Text(200, c.Param("path")) })
	r.Get("/dir/", text("dir exact"))

	tests := []struct{ method, target, want string }{
		{"GET", "/", "home"},
		{"GET", "/posts", "index"},
		{"GET", "/posts/42", "show 42"},
		{"GET", "/posts/a%20b", "show a b"},
		{"POST", "/posts", "store"},
		{"PUT", "/posts/1", "put"},
		{"PATCH", "/posts/1", "patch"},
		{"DELETE", "/posts/1", "delete"},
		{"GET", "/files/a/b/c.txt", "a/b/c.txt"},
		{"GET", "/dir/", "dir exact"},
	}
	for _, tt := range tests {
		got := do(t, r, tt.method, tt.target, nil)
		if got.status != 200 || got.body != tt.want {
			t.Errorf("%s %s = %d %q, want 200 %q", tt.method, tt.target, got.status, got.body, tt.want)
		}
	}

	// "/" and "/dir/" are exact: they don't match everything below them.
	for _, target := range []string{"/nope", "/dir/sub"} {
		if got := do(t, r, "GET", target, nil); got.status != 404 {
			t.Errorf("GET %s = %d, want 404", target, got.status)
		}
	}
	// HEAD is served by GET routes, without a body.
	if got := do(t, r, "HEAD", "/posts", nil); got.status != 200 || got.body != "" {
		t.Errorf("HEAD = %d %q", got.status, got.body)
	}
}

func TestMethodNotAllowedAndOptions(t *testing.T) {
	r := newTestRouter()
	r.Get("/posts/{id}", text("show"))
	r.Delete("/posts/{id}", text("delete"))

	got := do(t, r, "POST", "/posts/1", nil, "Accept", "application/json")
	if got.status != 405 || got.header.Get("Allow") != "GET, HEAD, DELETE" {
		t.Errorf("405: %d Allow=%q", got.status, got.header.Get("Allow"))
	}
	if !strings.Contains(got.body, `"status": 405`) {
		t.Errorf("405 body = %s", got.body)
	}
	got = do(t, r, "OPTIONS", "/posts/1", nil)
	if got.status != 204 || got.header.Get("Allow") != "GET, HEAD, DELETE, OPTIONS" {
		t.Errorf("OPTIONS: %d Allow=%q", got.status, got.header.Get("Allow"))
	}
	if got := do(t, r, "OPTIONS", "/missing", nil); got.status != 404 {
		t.Errorf("OPTIONS on missing path = %d", got.status)
	}
}

func TestGroupsWithAndMiddlewareOrder(t *testing.T) {
	var order []string
	mw := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}
	r := newTestRouter()
	r.UseGlobal(mw("global"))
	r.Use(mw("root"))
	api := r.Group("/api", mw("api"))
	v1 := api.Group("/v1").As("v1.")
	v1.With(mw("route")).Get("/ping", text("pong")).Name("ping")
	api.Get("", text("api root"))

	got := do(t, r, "GET", "/api/v1/ping", nil)
	if got.body != "pong" || strings.Join(order, ",") != "global,root,api,route" {
		t.Errorf("body %q, order %v", got.body, order)
	}
	order = nil
	if got := do(t, r, "GET", "/api", nil); got.body != "api root" || strings.Join(order, ",") != "global,root,api" {
		t.Errorf("group root: %q %v", got.body, order)
	}
	order = nil
	do(t, r, "GET", "/missing", nil)
	if strings.Join(order, ",") != "global" {
		t.Errorf("404 should pass through global middleware only: %v", order)
	}
	if u := r.MustURL("v1.ping"); u != "/api/v1/ping" {
		t.Errorf("URL = %q", u)
	}
}

func TestUsePanicsAfterRoutes(t *testing.T) {
	r := newTestRouter()
	r.Get("/", text("x"))
	mustPanic(t, "Use after routes", func() { r.Use(func(h http.Handler) http.Handler { return h }) })

	do(t, r, "GET", "/", nil)
	mustPanic(t, "UseGlobal after serving", func() { r.UseGlobal(func(h http.Handler) http.Handler { return h }) })
}

func TestPatternValidation(t *testing.T) {
	r := newTestRouter()
	mustPanic(t, "no leading slash", func() { r.Get("posts", text("x")) })
	mustPanic(t, "method in pattern", func() { r.Get("GET /posts", text("x")) })
	mustPanic(t, "nil handler", func() { r.Get("/x", nil) })
	r.Get("/dup", text("x"))
	mustPanic(t, "conflicting pattern", func() { r.Get("/dup", text("y")) })
}

func TestURLGeneration(t *testing.T) {
	r := newTestRouter()
	r.Get("/posts/{id}/comments/{cid}", text("x")).Name("comments.show")
	r.Get("/files/{path...}", text("x")).Name("files")
	r.Get("/about/", text("x")).Name("about")

	if u, _ := r.URL("comments.show", 42, "a b/c"); u != "/posts/42/comments/a%20b%2Fc" {
		t.Errorf("escaped URL = %q", u)
	}
	if u, _ := r.URL("files", "docs/my file.txt"); u != "/files/docs/my%20file.txt" {
		t.Errorf("wildcard URL = %q", u)
	}
	if u, _ := r.URL("comments.show", 1, 2, url.Values{"sort": {"new"}, "q": {"a b&c"}}); u != "/posts/1/comments/2?q=a+b%26c&sort=new" {
		t.Errorf("URL with query = %q", u)
	}
	if _, err := r.URL("comments.show", url.Values{"a": {"1"}}, 2, url.Values{}); err == nil {
		t.Error("url.Values as a path value accepted")
	}
	if u, _ := r.URL("about", url.Values{}); u != "/about/" {
		t.Errorf("empty query URL = %q", u)
	}
	if u, _ := r.URL("about"); u != "/about/" {
		t.Errorf("trailing slash URL = %q", u)
	}
	if _, err := r.URL("nope"); !errors.Is(err, ErrUnknownRoute) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := r.URL("comments.show", 1); err == nil {
		t.Error("too few args accepted")
	}
	if _, err := r.URL("comments.show", 1, 2, 3); err == nil {
		t.Error("too many args accepted")
	}
	mustPanic(t, "duplicate name", func() { r.Get("/other", text("x")).Name("files") })
	mustPanic(t, "MustURL", func() { r.MustURL("nope") })
}

func TestRoutesListAndRouteContext(t *testing.T) {
	r := newTestRouter()
	var seen *Route
	r.Get("/a/{id}", func(c *Ctx) error {
		seen = c.Route()
		return c.NoContent()
	}).Name("a.show")
	r.HandleStd("", "/std", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if rt := RouteFromContext(req.Context()); rt == nil || rt.Pattern() != "/std" {
			t.Error("std handlers must be visible to outer middleware")
		}
		w.WriteHeader(202)
	}))

	do(t, r, "GET", "/a/1", nil)
	if seen == nil || seen.Pattern() != "/a/{id}" || seen.Method() != "GET" || seen.RouteName() != "a.show" {
		t.Errorf("route = %+v", seen)
	}
	if got := do(t, r, "PATCH", "/std", nil); got.status != 202 {
		t.Errorf("std any-method route = %d", got.status)
	}
	routes := r.Routes()
	if len(routes) != 2 || routes[0] != (RouteInfo{"GET", "/a/{id}", "a.show"}) || routes[1].Method != "" {
		t.Errorf("Routes = %+v", routes)
	}
}

func mustPanic(t *testing.T, what string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: expected panic", what)
		}
	}()
	fn()
}
