// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/web"
)

// named is a middleware that says it's name (as session's, auth's…).
func named(name string) web.Middleware {
	return func(next http.Handler) http.Handler {
		return web.Documented(next, web.MiddlewareDoc{Name: name})
	}
}

// The route rules find what they're for, and leave out what's fine.
func TestSecurityRouteRules(t *testing.T) {
	t.Chdir(t.TempDir())
	app := newApp(t, config.Map{})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	ok := func(*web.Ctx) error { return nil }
	r := srv.Router()
	pages := r.Group("", named("session.Manager.Middleware"))
	pages.Post("/no-csrf", ok)                                                // route.csrf
	pages.With(named("web.CSRF")).Post("/with-csrf", ok)                      // fine
	pages.Get("/read", ok)                                                    // fine: GET
	r.Get("/admin/users", ok).Name("admin.users")                             // route.login
	r.With(named("auth.Auth.Require")).Get("/admin/ok", ok)                   // fine
	r.Get("/admin/assets", ok).Public("the same for everyone")                // fine: public
	r.Get("/debug/pprof/heap", ok)                                            // route.debug
	r.Handle("", "/debug/{path...}", web.WrapHandler(http.NotFoundHandler())) // route.debug: pprof's usual mount
	r.Get("/debug/vars", ok).Public("public")                                 // route.debug: Public doesn't excuse it
	r.Get("/debugging", ok)                                                   // fine
	r.Post("/debug/pprof/x", ok)                                              // fine for route.debug: a POST
	r.Post("/register", ok)                                                   // route.rate-limit
	r.With(named("ratelimit.Middleware")).Post("/forgot-password", ok)        // fine
	api := r.Group("/api/v1", named("web.JSONErrors")).As("api.")
	api.Post("/orders", ok).Name("orders.create")                                       // route.api-auth (advanced)
	api.Post("/login", ok).Name("login")                                                // fine: make:auth's
	api.Post("/hooks", ok).Public("signed by the sender")                               // fine: public
	api.With(named("auth.Auth.Require")).Post("/orders/{id}", ok).Name("orders.update") // fine

	run := func(level string) map[string]string {
		t.Helper()
		var out, errOut strings.Builder
		app.ExecuteArgs(t.Context(), []string{"security:check", "--format=json", "--level=" + level}, &out, &errOut)
		var rep struct {
			Findings []struct {
				Rule, Where, File string
				Line              int
			}
		}
		if err := json.Unmarshal([]byte(out.String()), &rep); err != nil {
			t.Fatalf("%v\n%s\n%s", err, out.String(), errOut.String())
		}
		got := map[string]string{}
		for _, f := range rep.Findings {
			if !strings.HasPrefix(f.Rule, "route.") {
				continue
			}
			got[f.Where] = f.Rule
			if f.File != "security_test.go" && !strings.HasSuffix(f.File, "/security_test.go") || f.Line == 0 {
				t.Errorf("%s: at %s:%d, not where it was registered", f.Where, f.File, f.Line)
			}
		}
		return got
	}
	want := map[string]string{"POST /no-csrf": "route.csrf", "GET /admin/users": "route.login", "GET /debug/pprof/heap": "route.debug",
		"ANY /debug/{path...}": "route.debug", "GET /debug/vars": "route.debug", "POST /register": "route.rate-limit"}
	got := run("basic")
	if len(got) != len(want) {
		t.Errorf("basic: %v", got)
	}
	for where, rule := range want {
		if got[where] != rule {
			t.Errorf("basic: %s: %q, want %s", where, got[where], rule)
		}
	}
	app2 := newApp(t, config.Map{})
	srv2, err := web.NewServer(app2)
	if err != nil {
		t.Fatal(err)
	}
	srv2.Router().Group("/api/v1", named("web.JSONErrors")).Post("/orders", ok)
	app = app2
	if got := run("advanced"); got["POST /api/v1/orders"] != "route.api-auth" {
		t.Errorf("advanced: %v", got)
	}
}

// A route rule of an app's own sees every route, the global
// middleware first.
func TestRouteRule(t *testing.T) {
	t.Chdir(t.TempDir())
	app := newApp(t, config.Map{})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	srv.Router().UseGlobal(named("shop.Global"))
	srv.Router().Post("/webhooks/pay", func(*web.Ctx) error { return nil })
	app.AddSecurityRule(web.RouteRule(anetos.SecurityRule{ID: "shop.webhooks", Summary: "Webhooks without signatures"},
		func(rt web.RouteInfo) []anetos.SecurityFinding {
			if strings.HasPrefix(rt.Pattern, "/webhooks/") && rt.Uses("shop.Global") && !rt.Uses("shop.Verify") {
				return []anetos.SecurityFinding{{Severity: anetos.Problem, Message: "no signature check"}}
			}
			return nil
		}))
	var out, errOut strings.Builder
	if code := app.ExecuteArgs(t.Context(), []string{"security:check"}, &out, &errOut); code != 1 ||
		!strings.Contains(out.String(), "problem  shop.webhooks  POST /webhooks/pay  ") || !strings.Contains(out.String(), "security_test.go:") {
		t.Errorf("exit %d:\n%s\n%s", code, out.String(), errOut.String())
	}
}

// A route says where it was registered and why it's public, in
// route:list --json too; Public needs a reason.
func TestRoutePublicAndPlace(t *testing.T) {
	t.Chdir(t.TempDir())
	app := newApp(t, config.Map{})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	srv.Router().Post("/hooks", func(*web.Ctx) error { return nil }).Public("signed by the sender")
	var out, errOut strings.Builder
	if code := app.ExecuteArgs(t.Context(), []string{"route:list", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	var list struct {
		Routes []struct {
			Path, File, Public string
			Line               int
		}
	}
	if err := json.Unmarshal([]byte(out.String()), &list); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	i := slices.IndexFunc(list.Routes, func(rt struct {
		Path, File, Public string
		Line               int
	}) bool {
		return rt.Path == "/hooks"
	})
	if i < 0 {
		t.Fatalf("no /hooks:\n%s", out.String())
	}
	if rt := list.Routes[i]; !strings.HasSuffix(rt.File, "security_test.go") || rt.Line == 0 || rt.Public != "signed by the sender" {
		t.Errorf("route:list --json: %+v", rt)
	}
	defer func() {
		if recover() == nil {
			t.Error(`Public("") didn't panic`)
		}
	}()
	srv.Router().Get("/x", func(*web.Ctx) error { return nil }).Public(" ")
}
