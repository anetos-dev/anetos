// SPDX-License-Identifier: Apache-2.0

package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Baselines: the same routes on a plain http.ServeMux, so Anetos's overhead
// is visible (design §22).

func benchServe(b *testing.B, h http.Handler, method, target, body, ct string) {
	b.Helper()
	b.ReportAllocs()
	for b.Loop() {
		var rdr *strings.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		var req *http.Request
		if rdr != nil {
			req = httptest.NewRequest(method, target, rdr)
			req.Header.Set("Content-Type", ct)
		} else {
			req = httptest.NewRequest(method, target, nil)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}

func BenchmarkServeMuxParam(b *testing.B) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /posts/{id}", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.PathValue("id")))
	})
	benchServe(b, mux, "GET", "/posts/42", "", "")
}

func BenchmarkRouterParam(b *testing.B) {
	r := newTestRouter()
	r.Get("/posts/{id}", func(c *Ctx) error { return c.Text(200, c.Param("id")) })
	benchServe(b, r, "GET", "/posts/42", "", "")
}

type benchIn struct {
	ID    int    `path:"id"`
	Page  int    `query:"page"`
	Title string `json:"title"`
}

func BenchmarkRouterTypedJSON(b *testing.B) {
	r := newTestRouter()
	r.Post("/posts/{id}", H(func(c *Ctx, in benchIn) (benchIn, error) { return in, nil }))
	benchServe(b, r, "POST", "/posts/42?page=2", `{"title":"hello"}`, "application/json")
}

func BenchmarkRouterNotFound(b *testing.B) {
	r := newTestRouter()
	r.Get("/posts/{id}", func(c *Ctx) error { return c.NoContent() })
	benchServe(b, r, "GET", "/nope", "", "")
}
