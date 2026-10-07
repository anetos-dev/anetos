// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"anetos.dev/anetos/view"
)

func TestErrorPages(t *testing.T) {
	page := func(_ *Ctx, e ErrorPage) view.Component {
		return view.ComponentFunc(func(_ context.Context, w io.Writer) error {
			switch e.Status {
			case http.StatusTeapot:
				return errors.New("broken page")
			case http.StatusGone:
				panic("the page panicked")
			}
			_, err := fmt.Fprintf(w, "<main>app page %d %s|%s|%s</main>", e.Status, e.Title, e.Detail, e.RequestID)
			return err
		})
	}
	build := func(debug bool) *Router {
		r := NewRouter(WithDebug(debug))
		r.UseGlobal(RequestIDs)
		r.ErrorPages(page)
		r.Get("/fail", func(*Ctx) error { return errors.New("database down") })
		r.Get("/gone", func(*Ctx) error { return Error(http.StatusNotFound, "That page is gone.") })
		r.Get("/teapot", func(*Ctx) error { return Error(http.StatusTeapot, "short and stout") })
		r.Get("/410", func(*Ctx) error { return Error(http.StatusGone, "Gone for good.") })
		return r
	}
	get := func(r *Router, path, accept string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	r := build(false)
	if rec := get(r, "/nowhere", ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "app page 404 Not Found|") || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("unknown route: %d %q", rec.Code, rec.Body)
	}
	if rec := get(r, "/gone", ""); !strings.Contains(rec.Body.String(), "|That page is gone.|") {
		t.Errorf("detail: %q", rec.Body)
	}
	if rec := get(r, "/fail", ""); rec.Code != 500 || !strings.Contains(rec.Body.String(), "app page 500 Internal Server Error||") || strings.Contains(rec.Body.String(), "database down") {
		t.Errorf("500: %d %q", rec.Code, rec.Body)
	} else if id := rec.Header().Get("X-Request-ID"); id == "" || !strings.HasSuffix(strings.TrimSuffix(rec.Body.String(), "</main>"), "|"+id) {
		t.Errorf("request ID %q: %q", id, rec.Body)
	}
	if rec := get(r, "/gone", "application/json"); !strings.Contains(rec.Header().Get("Content-Type"), "problem+json") {
		t.Errorf("JSON client: %s %q", rec.Header().Get("Content-Type"), rec.Body)
	}
	if rec := get(r, "/teapot", ""); rec.Code != http.StatusTeapot || strings.Contains(rec.Body.String(), "app page") || !strings.Contains(rec.Body.String(), "short and stout") {
		t.Errorf("a page that fails to render: %d %q", rec.Code, rec.Body)
	}
	if rec := get(r, "/410", ""); rec.Code != http.StatusGone || strings.Contains(rec.Body.String(), "app page") || !strings.Contains(rec.Body.String(), "Gone for good.") {
		t.Errorf("a page that panics: %d %q", rec.Code, rec.Body)
	}

	debug := build(true)
	if rec := get(debug, "/fail", ""); strings.Contains(rec.Body.String(), "app page") || !strings.Contains(rec.Body.String(), "database down") {
		t.Errorf("debug 500: %q", rec.Body)
	}
	if rec := get(debug, "/gone", ""); !strings.Contains(rec.Body.String(), "app page 404") {
		t.Errorf("debug 404: %q", rec.Body)
	}
}
