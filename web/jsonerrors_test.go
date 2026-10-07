// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/view"
)

// problemOf decodes a problem details response, failing unless it is one.
func problemOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type %q, body %q", ct, rec.Body)
	}
	var p map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("%v: %q", err, rec.Body)
	}
	return p
}

func TestJSONErrorsGlobal(t *testing.T) {
	build := func(debug bool) *Router {
		r := NewRouter(WithDebug(debug))
		r.UseGlobal(Recover(slog.New(slog.DiscardHandler)), RequestIDs, JSONErrors, Timeout(50*time.Millisecond))
		// An app's error page is never used under JSONErrors.
		r.ErrorPages(func(*Ctx, ErrorPage) view.Component {
			return view.ComponentFunc(func(_ context.Context, w io.Writer) error {
				_, err := io.WriteString(w, "<main>app page</main>")
				return err
			})
		})
		r.Get("/fail", func(*Ctx) error { return errors.New("database down") })
		r.Get("/panic", func(*Ctx) error { panic("boom") })
		r.Group("", func(http.Handler) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("middleware boom") })
		}).Get("/mw-panic", func(*Ctx) error { return nil })
		r.Get("/slow", func(c *Ctx) error {
			<-c.Done()
			return c.Err()
		})
		r.Get("/missing", func(*Ctx) error { return Error(http.StatusNotFound, "No such order.") })
		r.Post("/items", func(c *Ctx) error {
			return &HTTPError{Status: http.StatusUnprocessableEntity, Message: "Check the fields.", Fields: map[string]string{"name": "required"}}
		})
		r.Get("/wants", func(c *Ctx) error {
			if c.WantsJSON() {
				return c.Text(http.StatusOK, "json")
			}
			return c.Text(http.StatusOK, "html")
		})
		return r
	}
	get := func(r *Router, method, path, accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	r := build(false)
	for _, accept := range []string{"", "*/*", "text/html", "text/html,application/xhtml+xml,*/*;q=0.8"} {
		rec := get(r, http.MethodGet, "/nowhere", accept)
		if p := problemOf(t, rec); rec.Code != 404 || p["status"] != 404.0 || p["title"] != "Not Found" || p["request_id"] == "" {
			t.Errorf("404 with Accept %q: %d %v", accept, rec.Code, p)
		}
	}
	if rec := get(r, http.MethodDelete, "/fail", "text/html"); rec.Code != 405 || rec.Header().Get("Allow") == "" {
		t.Errorf("405: %d %v", rec.Code, rec.Header())
	} else {
		problemOf(t, rec)
	}
	if rec := get(r, http.MethodGet, "/missing", "text/html"); problemOf(t, rec)["detail"] != "No such order." {
		t.Errorf("detail: %q", rec.Body)
	}
	// A 5xx's message stays hidden outside debug mode.
	if rec := get(r, http.MethodGet, "/fail", "text/html"); rec.Code != 500 || strings.Contains(rec.Body.String(), "database down") {
		t.Errorf("500: %d %q", rec.Code, rec.Body)
	} else if p := problemOf(t, rec); p["debug"] != nil || p["detail"] != nil {
		t.Errorf("500 problem: %v", p)
	}
	// A handler's panic, a middleware's (answered by Recover, outside
	// JSONErrors; HEAD without a body), and Timeout's 503.
	for _, path := range []string{"/panic", "/mw-panic"} {
		if rec := get(r, http.MethodGet, path, "text/html"); rec.Code != 500 {
			t.Errorf("%s: %d", path, rec.Code)
		} else if p := problemOf(t, rec); p["status"] != 500.0 || p["request_id"] == "" || strings.Contains(rec.Body.String(), "boom") {
			t.Errorf("%s: %q", path, rec.Body)
		}
	}
	if rec := get(r, http.MethodHead, "/mw-panic", ""); rec.Code != 500 || rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("HEAD middleware panic: %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	if rec := get(r, http.MethodGet, "/slow", "text/html"); rec.Code != 503 {
		t.Errorf("timeout: %d %q", rec.Code, rec.Body)
	} else {
		problemOf(t, rec)
	}
	// A form post's errors aren't redirected back, even from a browser.
	req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(url.Values{"name": {""}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Referer", "http://example.com/items/new")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if p := problemOf(t, rec); rec.Code != 422 || p["detail"] != "Check the fields." || p["errors"].(map[string]any)["name"] != "required" {
		t.Errorf("422: %d %v", rec.Code, p)
	}
	// The request is an API's, whatever the client sent.
	if rec := get(r, http.MethodGet, "/wants", "text/html"); rec.Body.String() != "json" {
		t.Errorf("WantsJSON: %q", rec.Body)
	}

	// Debug mode: the details are in the problem, not on an HTML page.
	r = build(true)
	rec = get(r, http.MethodGet, "/fail", "text/html")
	p := problemOf(t, rec)
	if d, ok := p["debug"].(map[string]any); !ok || d["error"] != "database down" {
		t.Errorf("debug: %v", p)
	}
	rec = get(r, http.MethodGet, "/panic", "text/html")
	if d, ok := problemOf(t, rec)["debug"].(map[string]any); !ok || !strings.Contains(d["stack"].(string), "goroutine") {
		t.Errorf("panic in debug mode: %q", rec.Body)
	}
}

// In a group, JSONErrors leaves the other routes' errors as they were.
func TestJSONErrorsGroup(t *testing.T) {
	r := NewRouter()
	r.Get("/page", func(*Ctx) error { return Error(http.StatusNotFound, "") })
	api := r.Group("/api/v1", JSONErrors)
	api.Get("/orders/{id}", func(*Ctx) error { return Error(http.StatusNotFound, "No such order.") })
	// A sub-request made by an API handler keeps its JSON errors.
	api.Get("/inner", func(c *Ctx) error {
		req := c.Request().Clone(c)
		req.URL.Path = "/page"
		r.ServeHTTP(c.Writer(), req)
		return nil
	})

	// A URL under the prefix that no route matches isn't the group's.
	for path, wantJSON := range map[string]bool{"/page": false, "/api/v1/orders/1": true, "/api/v1/inner": true, "/api/v1/nope": false} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		isJSON := rec.Header().Get("Content-Type") == "application/problem+json"
		if rec.Code != 404 || isJSON != wantJSON {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// Outside a router, JSONErrors passes the request on.
func TestJSONErrorsOutsideRouter(t *testing.T) {
	h := JSONErrors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("%d", rec.Code)
	}
}
