// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/web"
)

// Each benchmark serves requests in-process (ServeHTTP with a recorder),
// so it measures the framework, not the network. "Mux" is the standard
// library's http.ServeMux with hand-written code doing the same work;
// "Router" is a bare web.Router; "Server" is the router of web.NewServer,
// with its default middleware (recover, request IDs, real IP, security
// headers, body limit, timeout; no access log; the units and locale steps
// do nothing without their setup).

func serve(b *testing.B, h http.Handler, newReq func() *http.Request, want int) {
	b.Helper()
	b.ReportAllocs()
	if rec := httptest.NewRecorder(); true {
		h.ServeHTTP(rec, newReq())
		if rec.Code != want {
			b.Fatalf("status %d, want %d: %s", rec.Code, want, rec.Body)
		}
	}
	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), newReq())
	}
}

func get(target string) func() *http.Request {
	return func() *http.Request { return httptest.NewRequest(http.MethodGet, target, nil) }
}

func newApp(b testing.TB, env config.Map) *anetos.App {
	b.Helper()
	src := config.Map{"APP_ENV": "production", "HTTP_ACCESS_LOG": "false"}
	maps.Copy(src, env)
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogger(slog.New(slog.DiscardHandler)))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = app.Close() })
	return app
}

func newServer(b testing.TB, app *anetos.App) *web.Router {
	b.Helper()
	srv, err := web.NewServer(app)
	if err != nil {
		b.Fatal(err)
	}
	return srv.Router()
}

func boot(b testing.TB, app *anetos.App) {
	b.Helper()
	if err := app.Boot(context.Background()); err != nil {
		b.Fatal(err)
	}
}

// ---- Hello world ----

func helloMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "Hello, world!")
	})
	return mux
}

func BenchmarkHelloMux(b *testing.B) { serve(b, helloMux(), get("/"), http.StatusOK) }

func helloRoutes(r *web.Router) {
	r.Get("/", func(c *web.Ctx) error { return c.Text(http.StatusOK, "Hello, world!") })
}

func BenchmarkHelloRouter(b *testing.B) {
	r := web.NewRouter(web.WithLogger(slog.New(slog.DiscardHandler)))
	helloRoutes(r)
	serve(b, r, get("/"), http.StatusOK)
}

func BenchmarkHelloServer(b *testing.B) {
	app := newApp(b, nil)
	r := newServer(b, app)
	helloRoutes(r)
	boot(b, app)
	serve(b, r, get("/"), http.StatusOK)
}

// ---- Typed JSON handler: path and body binding, validation, JSON out ----

type createPost struct {
	AuthorID int64  `path:"id"`
	Title    string `json:"title" validate:"required|max:200"`
	Body     string `json:"body" validate:"required"`
}

type post struct {
	ID       int64  `json:"id"`
	AuthorID int64  `json:"author_id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
}

const postJSON = `{"title":"Hello, Anetos","body":"A typed handler binds, validates and encodes."}`

func postReq() *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/authors/7/posts", strings.NewReader(postJSON))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func BenchmarkJSONMux(b *testing.B) { serve(b, jsonMux(), postReq, http.StatusCreated) }

func jsonMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /authors/{id}/posts", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var in createPost
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if in.Title == "" || utf8.RuneCountInString(in.Title) > 200 || in.Body == "" {
			http.Error(w, "invalid", http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(post{ID: 1, AuthorID: id, Title: in.Title, Body: in.Body})
	})
	return mux
}

func jsonRoutes(r *web.Router) {
	r.Post("/authors/{id}/posts", web.H(func(c *web.Ctx, in createPost) (web.Responder, error) {
		return web.Created(post{ID: 1, AuthorID: in.AuthorID, Title: in.Title, Body: in.Body}), nil
	}))
}

func BenchmarkJSONRouter(b *testing.B) {
	r := web.NewRouter(web.WithLogger(slog.New(slog.DiscardHandler)))
	jsonRoutes(r)
	serve(b, r, postReq, http.StatusCreated)
}

func BenchmarkJSONServer(b *testing.B) {
	app := newApp(b, nil)
	r := newServer(b, app)
	jsonRoutes(r)
	boot(b, app)
	serve(b, r, postReq, http.StatusCreated)
}

// ---- Single-row database read (SQLite in memory) ----

type Post struct {
	db.Model
	Title string `db:"title" json:"title"`
	Body  string `db:"body" json:"body"`
}

type postID struct {
	ID int64 `path:"id"`
}

// dbApp returns an app connected to an in-memory SQLite database with one
// post, and its router.
func dbApp(b testing.TB) (*anetos.App, *web.Router, *db.DB) {
	b.Helper()
	app := newApp(b, config.Map{"DB_NAME": ":memory:"})
	d, err := db.Connect(context.Background(), app, sqlite.Driver())
	if err != nil {
		b.Fatal(err)
	}
	r := newServer(b, app)
	boot(b, app)
	ctx := app.Context(context.Background())
	if _, err := db.Exec(ctx, `CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL,
		created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`); err != nil {
		b.Fatal(err)
	}
	if err := db.Create(ctx, &Post{Title: "Hello", Body: "One row, read by id."}); err != nil {
		b.Fatal(err)
	}
	return app, r, d
}

func BenchmarkDBReadMux(b *testing.B) {
	_, _, d := dbApp(b)
	sqlDB := d.SQL()
	type row struct {
		ID        int64     `json:"id"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
		Title     string    `json:"title"`
		Body      string    `json:"body"`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /posts/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		var p row
		err = sqlDB.QueryRowContext(r.Context(), "SELECT id, created_at, updated_at, title, body FROM posts WHERE id = ?", id).
			Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Body)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
			return
		case err != nil:
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p)
	})
	serve(b, mux, get("/posts/1"), http.StatusOK)
}

func BenchmarkDBReadServer(b *testing.B) {
	app, r, _ := dbApp(b)
	r.Get("/posts/{id}", web.H(func(c *web.Ctx, in postID) (Post, error) {
		return db.Find[Post](c, in.ID) // db.ErrNotFound becomes a 404
	}))
	// The HTTP server gives requests the app's context (with the
	// database) as their base context; in-process requests get it here.
	appCtx := app.Context(context.Background())
	serve(b, r, func() *http.Request {
		return httptest.NewRequestWithContext(appCtx, http.MethodGet, "/posts/1", nil)
	}, http.StatusOK)
}

func BenchmarkDBReadRouter(b *testing.B) {
	app, _, _ := dbApp(b)
	r := web.NewRouter(web.WithLogger(slog.New(slog.DiscardHandler)))
	r.Get("/posts/{id}", web.H(func(c *web.Ctx, in postID) (Post, error) {
		return db.Find[Post](c, in.ID)
	}))
	appCtx := app.Context(context.Background())
	serve(b, r, func() *http.Request {
		return httptest.NewRequestWithContext(appCtx, http.MethodGet, "/posts/1", nil)
	}, http.StatusOK)
}

// The query alone, without HTTP: where the database read's overhead is.

func BenchmarkQueryRowScan(b *testing.B) {
	app, _, d := dbApp(b)
	ctx := app.Context(context.Background())
	b.ReportAllocs()
	for b.Loop() {
		var p Post
		if err := d.SQL().QueryRowContext(ctx, "SELECT id, created_at, updated_at, title, body FROM posts WHERE id = ?", 1).
			Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Body); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQueryFind(b *testing.B) {
	app, _, _ := dbApp(b)
	ctx := app.Context(context.Background())
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Find[Post](ctx, int64(1)); err != nil {
			b.Fatal(err)
		}
	}
}
