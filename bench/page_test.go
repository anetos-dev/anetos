// SPDX-License-Identifier: Apache-2.0

package bench

import (
	"bytes"
	"context"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"testing/fstest"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/i18n"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"
)

// ---- A page as an app made with anetos new and make:auth serves it ----
//
// GET /posts for a signed-in user: the server's middleware with the
// access log on (JSON, to io.Discard), the request's language, the
// session in an encrypted cookie, CSRF, the signed-in user loaded from
// the database, 20 rows of a list, and HTML rendered as templ renders it
// (escaped strings written in order).

// User is the signed-in user of the page benchmarks.
type User struct {
	db.Model
	Name     string `db:"name"`
	Email    string `db:"email"`
	Password string `db:"password"`
}

// AuthID implements auth.Authenticatable.
func (u *User) AuthID() string { return strconv.FormatInt(u.ID, 10) }

// AuthPassword implements auth.Authenticatable.
func (u *User) AuthPassword() string { return u.Password }

var benchUsers = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrNoUser
		}
		u, err := db.Find[User](ctx, n)
		return &u, err
	},
	ByLogin: func(ctx context.Context, login string) (*User, error) {
		u, err := db.Query[User](ctx).Where(db.Col[string]("email").Eq(login)).First()
		return &u, err
	},
}

// benchKey is a fixed APP_KEY, for the session cookie.
const benchKey = "base64:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

var colPostID = db.Col[int64]("id")

// postsPage renders the list as a templ component would.
func postsPage(title, user, token string, posts []Post) view.Component {
	return view.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		write := func(s string) { _, _ = io.WriteString(w, s) }
		write(`<!doctype html><html lang="en"><head><meta charset="utf-8"><title>`)
		write(html.EscapeString(title))
		write(`</title><link rel="stylesheet" href="/assets/app.css"></head><body><header class="topbar"><span>`)
		write(html.EscapeString(user))
		write(`</span><form method="post" action="/logout"><input type="hidden" name="_token" value="`)
		write(html.EscapeString(token))
		write(`"><button>Log out</button></form></header><main class="container"><h1>`)
		write(html.EscapeString(title))
		write(`</h1><ul>`)
		for _, p := range posts {
			write(`<li><a href="/posts/`)
			write(strconv.FormatInt(p.ID, 10))
			write(`">`)
			write(html.EscapeString(p.Title))
			write(`</a> <time>`)
			write(p.CreatedAt.Format("2006-01-02"))
			write(`</time></li>`)
		}
		write(`</ul></main></body></html>`)
		return nil
	})
}

// pageApp returns the app, its router, and request makers for a path,
// carrying a signed-in user's session cookie.
func pageApp(b testing.TB, logger *slog.Logger) (*anetos.App, *web.Router, func(path string) func() *http.Request) {
	b.Helper()
	src := config.Map{"APP_ENV": "production", "APP_KEY": benchKey, "DB_DATABASE": ":memory:"}
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogger(logger))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = app.Close() })
	if _, err := i18n.New(app, fstest.MapFS{"en/app.yaml": {Data: []byte("posts:\n  title: \"Posts\"\n")}}); err != nil {
		b.Fatal(err)
	}
	if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
		b.Fatal(err)
	}
	if _, err := cache.New(app); err != nil { // memory: login throttling
		b.Fatal(err)
	}
	srv, err := web.NewServer(app)
	if err != nil {
		b.Fatal(err)
	}
	sessions, err := session.New(app)
	if err != nil {
		b.Fatal(err)
	}
	a, err := auth.New(app, benchUsers)
	if err != nil {
		b.Fatal(err)
	}
	sessions.Use(a.Middleware)
	r := srv.Router()
	pages := r.Group("", sessions.Middleware, web.CSRF())
	pages.Get("/_login", func(c *web.Ctx) error {
		u, err := benchUsers.ByID(c, "1")
		if err != nil {
			return err
		}
		if err := a.Login(c, u, false); err != nil {
			return err
		}
		c.Session().Token() // stored now, so later pages don't change the session
		return c.Text(http.StatusOK, "ok")
	})
	// The parts, for BenchmarkPage's breakdown.
	hello := func(c *web.Ctx) error { return c.Text(http.StatusOK, "Hello, world!") }
	r.Get("/hello", hello)
	r.Group("", sessions.Middleware).Get("/session", hello)
	pages.Get("/csrf", hello)
	members := pages.Group("", a.Require)
	members.Get("/signed-in", hello)
	members.Get("/posts", func(c *web.Ctx) error {
		posts, err := db.Query[Post](c).OrderBy(colPostID.Desc()).Limit(20).Get()
		if err != nil {
			return err
		}
		u, err := auth.Current[*User](c)
		if err != nil {
			return err
		}
		return c.Render(http.StatusOK, postsPage(i18n.T(c, "posts.title"), u.Name, c.Session().Token(), posts))
	})
	if err := app.Boot(context.Background()); err != nil {
		b.Fatal(err)
	}
	ctx := app.Context(context.Background())
	for _, stmt := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT NOT NULL, password TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT NOT NULL,
			created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`,
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			b.Fatal(err)
		}
	}
	if err := db.Create(ctx, &User{Name: "Ada", Email: "ada@example.com", Password: "x"}); err != nil {
		b.Fatal(err)
	}
	for i := range 50 {
		if err := db.Create(ctx, &Post{Title: "Post " + strconv.Itoa(i+1), Body: "A post on the list."}); err != nil {
			b.Fatal(err)
		}
	}
	// Sign in, as the login form would, and keep the cookie.
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/_login", nil))
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusOK || len(cookies) == 0 {
		b.Fatalf("sign in: %d %v %s", rec.Code, cookies, rec.Body)
	}
	return app, r, func(path string) func() *http.Request {
		return func() *http.Request {
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			return req
		}
	}
}

// BenchmarkPage serves the page ("Full"), and the hello route through
// more and more of its stack: the server's middleware with the access log
// ("Server"), plus the session ("Session"), plus CSRF ("CSRF"), plus
// the signed-in user ("SignedIn"). The rest of "Full" is the list query
// and the rendering.
func BenchmarkPage(b *testing.B) {
	_, r, req := pageApp(b, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, c := range []struct{ name, path string }{
		{"Server", "/hello"}, {"Session", "/session"}, {"CSRF", "/csrf"}, {"SignedIn", "/signed-in"}, {"Full", "/posts"},
	} {
		b.Run(c.name, func(b *testing.B) { serveNoCookie(b, r, req(c.path)) })
	}
}

// serveNoCookie is serve, also checking that the page doesn't set a
// cookie (an unchanged session isn't written again).
func serveNoCookie(b *testing.B, h http.Handler, newReq func() *http.Request) {
	b.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newReq())
	if rec.Code != http.StatusOK || rec.Header().Get("Set-Cookie") != "" {
		b.Fatalf("status %d, Set-Cookie %q: %s", rec.Code, rec.Header().Get("Set-Cookie"), rec.Body)
	}
	serve(b, h, newReq, http.StatusOK)
}

// BenchmarkPageRender writes the page's HTML for 20 rows, alone.
func BenchmarkPageRender(b *testing.B) {
	posts := make([]Post, 20)
	for i := range posts {
		posts[i].ID = int64(i + 1)
		posts[i].Title = "Post " + strconv.Itoa(i+1)
		posts[i].CreatedAt = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	}
	page := postsPage("Posts", "Ada", "a-masked-csrf-token-of-the-usual-length-aaaaaaaaaaaaaaaaaaaaa", posts)
	var buf bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		buf.Reset()
		if err := page.Render(context.Background(), &buf); err != nil {
			b.Fatal(err)
		}
	}
}

// ---- What each middleware costs ----
//
// Each sub-benchmark serves the hello route through a bare router with
// one middleware; compare with BenchmarkHelloRouter.

func BenchmarkMiddleware(b *testing.B) {
	hello := func(mw ...web.Middleware) http.Handler {
		r := web.NewRouter(web.WithLogger(slog.New(slog.DiscardHandler)))
		r.UseGlobal(mw...)
		helloRoutes(r)
		return r
	}
	b.Run("RequestIDs", func(b *testing.B) { serve(b, hello(web.RequestIDs), get("/"), http.StatusOK) })
	b.Run("RealIP", func(b *testing.B) { serve(b, hello(web.RealIP(nil)), get("/"), http.StatusOK) })
	b.Run("SecureHeaders", func(b *testing.B) { serve(b, hello(web.SecureHeaders(true)), get("/"), http.StatusOK) })
	b.Run("BodyLimit", func(b *testing.B) { serve(b, hello(web.BodyLimit(10<<20)), get("/"), http.StatusOK) })
	b.Run("Timeout", func(b *testing.B) { serve(b, hello(web.Timeout(30_000_000_000)), get("/"), http.StatusOK) })
	b.Run("AccessLog", func(b *testing.B) {
		serve(b, hello(web.AccessLog(slog.New(slog.NewJSONHandler(io.Discard, nil)))), get("/"), http.StatusOK)
	})
	b.Run("Recover", func(b *testing.B) {
		serve(b, hello(web.Recover(slog.New(slog.DiscardHandler))), get("/"), http.StatusOK)
	})
}

// ---- A list of 20 rows, without HTTP ----

func BenchmarkQueryListScan(b *testing.B) {
	app, _, d := dbApp(b)
	ctx := app.Context(context.Background())
	seedPosts(b, ctx, 50)
	b.ReportAllocs()
	for b.Loop() {
		rows, err := d.SQL().QueryContext(ctx, "SELECT id, created_at, updated_at, title, body FROM posts ORDER BY id DESC LIMIT 20")
		if err != nil {
			b.Fatal(err)
		}
		var out []Post
		for rows.Next() {
			var p Post
			if err := rows.Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt, &p.Title, &p.Body); err != nil {
				b.Fatal(err)
			}
			out = append(out, p)
		}
		if err := rows.Close(); err != nil || len(out) != 20 {
			b.Fatal(err, len(out))
		}
	}
}

func BenchmarkQueryList(b *testing.B) {
	app, _, _ := dbApp(b)
	ctx := app.Context(context.Background())
	seedPosts(b, ctx, 50)
	b.ReportAllocs()
	for b.Loop() {
		posts, err := db.Query[Post](ctx).OrderBy(colPostID.Desc()).Limit(20).Get()
		if err != nil || len(posts) != 20 {
			b.Fatal(err, len(posts))
		}
	}
}

func seedPosts(b testing.TB, ctx context.Context, n int) {
	b.Helper()
	for i := range n {
		if err := db.Create(ctx, &Post{Title: "Post " + strconv.Itoa(i+1), Body: "A post on the list."}); err != nil {
			b.Fatal(err)
		}
	}
}
