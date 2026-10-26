// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"anetos.dev/anetos/config"
	"anetos.dev/anetos/web"
)

// HTTP_SPA_ORIGINS: CORS with credentials and the XSRF header for those
// origins, first-party requests by Origin or Referer, and FirstParty's
// middleware for them only.
func TestFirstParty(t *testing.T) {
	app := newApp(t, config.Map{
		"HTTP_SPA_ORIGINS":  "https://app.example.com:443, http://localhost:5173/",
		"HTTP_CORS_ORIGINS": "https://partner.example.org",
	})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	mark := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Session", "yes")
			next.ServeHTTP(w, r)
		})
	}
	g := srv.Router().Group("/api", web.FirstParty(mark))
	g.Get("/who", func(c *web.Ctx) error {
		if web.IsFirstParty(c) {
			return c.Text(http.StatusOK, "first")
		}
		return c.Text(http.StatusOK, "other")
	})
	serve := func(method string, headers ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/who", nil)
		for i := 0; i+1 < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		return rec
	}
	for _, c := range []struct {
		headers []string
		want    string
	}{
		{[]string{"Origin", "https://app.example.com"}, "first"},
		{[]string{"Origin", "HTTP://LOCALHOST:5173"}, "first"},
		{[]string{"Referer", "https://app.example.com/notes?page=2"}, "first"},
		{[]string{"Origin", "https://partner.example.org"}, "other"},
		{[]string{"Origin", "https://app.example.com.evil.test"}, "other"},
		{[]string{"Origin", "null"}, "other"},
		{nil, "other"},
	} {
		rec := serve(http.MethodGet, c.headers...)
		if got := rec.Body.String(); got != c.want || (rec.Header().Get("X-Session") == "yes") != (c.want == "first") {
			t.Errorf("%v: %q %v", c.headers, got, rec.Header())
		}
	}
	// CORS: the SPA's origin with credentials and the XSRF header; the
	// partner's without credentials.
	pre := serve(http.MethodOptions, "Origin", "https://app.example.com", "Access-Control-Request-Method", "POST")
	if h := pre.Header(); h.Get("Access-Control-Allow-Origin") != "https://app.example.com" || h.Get("Access-Control-Allow-Credentials") != "true" ||
		!strings.Contains(h.Get("Access-Control-Allow-Headers"), "X-XSRF-TOKEN") {
		t.Errorf("the SPA's preflight: %d %v", pre.Code, h)
	}
	pre = serve(http.MethodOptions, "Origin", "https://partner.example.org", "Access-Control-Request-Method", "POST")
	if h := pre.Header(); h.Get("Access-Control-Allow-Origin") != "https://partner.example.org" || h.Get("Access-Control-Allow-Credentials") != "" ||
		strings.Contains(h.Get("Access-Control-Allow-Headers"), "X-XSRF-TOKEN") {
		t.Errorf("the partner's preflight: %d %v", pre.Code, h)
	}
	if h := serve(http.MethodGet, "Origin", "https://evil.test").Header(); h.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("another origin: %v", h)
	}

	// A request with an API token is a token's, from any origin.
	if rec := serve(http.MethodGet, "Origin", "https://app.example.com", "Authorization", "Bearer 1|x"); rec.Body.String() != "other" {
		t.Errorf("a token from the SPA's origin: %s", rec.Body)
	}
	// A same-origin GET without Origin or Referer (a no-referrer policy):
	// Sec-Fetch-Site and the request's host.
	same := httptest.NewRequest(http.MethodGet, "https://app.example.com/api/who", nil)
	same.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, same)
	if rec.Body.String() != "first" {
		t.Errorf("a same-origin GET: %s", rec.Body)
	}
	same.Host = "api.example.com"
	rec = httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, same)
	if rec.Body.String() != "other" {
		t.Errorf("a same-origin GET on an unlisted host: %s", rec.Body)
	}
	// Tools see what FirstParty runs.
	for _, rt := range srv.Router().Routes() {
		if rt.Pattern != "/api/who" {
			continue
		}
		i := slices.IndexFunc(rt.Middleware, func(d web.MiddlewareDoc) bool { return d.Name == "web.FirstParty" })
		if i < 0 || len(rt.Middleware[i].Inner) != 1 || !rt.Uses(rt.Middleware[i].Inner[0].Name) {
			t.Errorf("FirstParty's inner middleware: %+v", rt.Middleware)
		}
	}

	for v, want := range map[string]string{
		"https://app.example.com:":  "its port is empty",
		"https://bücher.example":    "in ASCII",
		"app.example.com":           "needs http:// or https://",
		"https://*.example.com":     "without wildcards",
		"https://app.example.com/x": "only the scheme",
		"*":                         "isn't an origin",
	} {
		if _, err := web.LoadConfig(config.Map{"HTTP_SPA_ORIGINS": v}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("HTTP_SPA_ORIGINS=%s: %v", v, err)
		}
	}
	defer func() {
		if recover() == nil {
			t.Error("FirstPartyOrigins with a bad origin didn't panic")
		}
	}()
	web.FirstPartyOrigins([]string{"nope"})
}

// Outside a server's router, FirstPartyOrigins keeps its mark in the
// context.
func TestFirstPartyOutsideRouter(t *testing.T) {
	var first bool
	h := web.FirstPartyOrigins([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first = web.IsFirstParty(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://app.example.com")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !first {
		t.Error("not first-party")
	}
}

// With HTTP_SPA_ORIGINS alone (no HTTP_CORS_ORIGINS), the SPA's origin
// gets CORS with credentials and others none.
func TestSPAOriginsAloneCORS(t *testing.T) {
	app := newApp(t, config.Map{"HTTP_SPA_ORIGINS": "https://app.example.com"})
	srv, err := web.NewServer(app)
	if err != nil {
		t.Fatal(err)
	}
	srv.Router().Get("/x", func(c *web.Ctx) error { return c.Text(http.StatusOK, "x") })
	for origin, want := range map[string]string{"https://app.example.com": "true", "https://other.example": ""} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != want || (want == "") != (rec.Header().Get("Access-Control-Allow-Origin") == "") {
			t.Errorf("%s: %v", origin, rec.Header())
		}
	}
}
