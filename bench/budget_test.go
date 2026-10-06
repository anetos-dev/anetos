// SPDX-License-Identifier: Apache-2.0

//go:build !race

package bench

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
)

// TestBudgets holds the framework to its allocation budgets (design
// §22): the allocations a request or query makes beyond the same work
// written by hand with net/http and database/sql, exactly; and, for the
// page, in all, with headroom for the standard library's own
// allocations, which differ between Go releases (the pull-request
// comparison catches growth within it). Allocations don't vary from run to run as times do, so a budget
// fails only when the code changed; a change that needs more must raise
// the budget, and say why. (The race detector changes allocations: the
// test is built without it; make bench-check runs it.)
func TestBudgets(t *testing.T) {
	allocs := func(h http.Handler, newReq func() *http.Request) int {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, newReq())
		if rec.Code >= 400 {
			t.Fatalf("status %d: %s", rec.Code, rec.Body)
		}
		return int(testing.AllocsPerRun(200, func() { h.ServeHTTP(httptest.NewRecorder(), newReq()) }))
	}
	check := func(name string, got, budget int) {
		t.Helper()
		if got > budget {
			t.Errorf("%s: %d allocations, over the budget of %d", name, got, budget)
		} else {
			t.Logf("%s: %d allocations (budget %d)", name, got, budget)
		}
	}

	// Hello and JSON: the router and typed handlers, then the server's
	// middleware, over plain net/http.
	mux := helloMux()
	router := web.NewRouter(web.WithLogger(slog.New(slog.DiscardHandler)))
	helloRoutes(router)
	baseHello := allocs(mux, get("/"))
	check("hello, router over net/http", allocs(router, get("/"))-baseHello, 6)
	app := newApp(t, nil)
	server := newServer(t, app)
	helloRoutes(server)
	if err := app.Boot(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("hello, server over net/http", allocs(server, get("/"))-baseHello, 23)

	jmux := jsonMux()
	jrouter := web.NewRouter(web.WithLogger(slog.New(slog.DiscardHandler)))
	jsonRoutes(jrouter)
	baseJSON := allocs(jmux, postReq)
	check("JSON, router over net/http", allocs(jrouter, postReq)-baseJSON, 9)

	// A row by key: db.Find over database/sql's QueryRow and Scan.
	dapp, _, d := dbApp(t)
	ctx := dapp.Context(context.Background())
	scan := int(testing.AllocsPerRun(200, func() {
		var p Post
		_ = d.SQL().QueryRowContext(ctx, "SELECT id, created_at, updated_at, title, body FROM posts WHERE id = ?", 1).
			Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Body)
	}))
	find := int(testing.AllocsPerRun(200, func() { _, _ = db.Find[Post](ctx, int64(1)) }))
	check("db.Find over QueryRow and Scan", find-scan, 22)

	// The page of an app made with anetos new and make:auth, in all
	// (the request built by the test included).
	_, r, req := pageApp(t, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	check("page: server and session", allocs(r, req("/session")), 125)
	check("page: signed in", allocs(r, req("/signed-in")), 235)
	check("page: list of 20 rows, rendered", allocs(r, req("/posts")), 640)
}
