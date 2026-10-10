// SPDX-License-Identifier: Apache-2.0

package anetostest_test

import (
	"fmt"
	"html/template"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
)

type itemInput struct {
	Title string `json:"title" validate:"required|max:10"`
}

type item struct {
	ID    int    `json:"id"`
	Title string `json:"title"`
}

func setup(app *anetos.App) (*web.Server, error) {
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	sessions, err := session.New(app)
	if err != nil {
		return nil, err
	}
	var items []item
	r := srv.Router()
	pages := r.Group("", sessions.Middleware, web.CSRF())
	pages.Get("/items", func(c *web.Ctx) error {
		var b strings.Builder
		if msg := c.Session().String("status"); msg != "" {
			fmt.Fprintf(&b, "<p>%s</p>", msg)
		}
		for _, it := range items {
			fmt.Fprintf(&b, "<li>%s</li>", it.Title)
		}
		fmt.Fprintf(&b, "<p>user %s</p>", c.Session().String("user"))
		b.WriteString(template.HTMLEscapeString("C++ ") + "a&#43;b")
		return c.HTML(http.StatusOK, b.String())
	}).Name("items.index")
	pages.Get("/items/new", func(c *web.Ctx) error { return c.HTML(http.StatusOK, "form") })
	pages.Post("/items", web.H(func(c *web.Ctx, in itemInput) (web.Responder, error) {
		items = append(items, item{len(items) + 1, in.Title})
		if c.WantsJSON() {
			return web.Created(items[len(items)-1]), nil
		}
		c.Session().Flash("status", "Item created.")
		return web.RedirectRoute("items.index"), nil
	}))
	pages.Get("/api/items", func(c *web.Ctx) error {
		return c.JSON(http.StatusOK, map[string]any{"data": items, "total": len(items)})
	})
	pages.Delete("/items/{id}", func(c *web.Ctx) error {
		if c.Request().Header.Get("HX-Request") == "true" {
			return c.Text(http.StatusOK, "")
		}
		return c.NoContent()
	})
	pages.Get("/big", func(c *web.Ctx) error { return c.JSON(http.StatusOK, int64(9007199254740993)) })
	pages.Get("/away", func(c *web.Ctx) error { return c.Redirect(http.StatusFound, "https://example.org/login") })
	pages.Get("/accept", func(c *web.Ctx) error { return c.Text(http.StatusOK, c.Request().Header.Get("Accept")) })
	pages.Get("/env", func(c *web.Ctx) error {
		return c.Text(http.StatusOK, string(app.Config().Env)+" "+c.Request().Header.Get("X-Test"))
	})
	return srv, nil
}

func TestForms(t *testing.T) {
	app := anetostest.New(t, setup)

	app.Get("/items/new").AssertOK().AssertSee("form")

	// Invalid: redirected back to the form, with the errors in the session.
	app.PostForm("/items", url.Values{"title": {"far too long a title"}}).
		AssertRedirect("/items/new").
		AssertValidationErrors("title")
	if old, _ := app.Session().Old("title"); old != "far too long a title" {
		t.Errorf("old input = %q", old)
	}

	app.PostForm("/items", url.Values{"title": {`<"Tea">`}}).
		AssertRedirectRoute("items.index").
		AssertNoValidationErrors().
		AssertSessionHas("status").
		AssertSessionHas("status", "Item created.").
		AssertSessionMissing("nope").
		Follow().
		AssertOK().
		AssertSee("Item created.", `<"Tea">`).
		AssertDontSee("<script>")

	// A flash message lasts one request.
	app.Get("/items").AssertDontSee("Item created.")

	// A wrong token is rejected.
	app.PostForm("/items", url.Values{"_token": {"wrong"}, "title": {"x"}}).AssertForbidden()
}

func TestJSON(t *testing.T) {
	app := anetostest.New(t, setup)

	app.PostJSON("/items", map[string]string{"title": ""}).
		AssertUnprocessable().
		AssertValidationErrors("title").
		AssertValidationErrors()
	app.PostJSON("/items", itemInput{Title: "Tea"}).
		AssertCreated().
		AssertJSON(item{1, "Tea"}).
		AssertHeader("Content-Type", "application/json; charset=utf-8")
	app.PostJSON("/items", itemInput{Title: "Milk"}).AssertCreated()

	res := app.GetJSON("/api/items").
		AssertOK().
		AssertJSONPath("total", 2).
		AssertJSONPath("data.1.title", "Milk").
		AssertJSONPath("data.0", item{1, "Tea"})
	var got struct{ Data []item }
	res.JSON(&got)
	if len(got.Data) != 2 {
		t.Errorf("decoded %+v", got)
	}
	app.DeleteJSON("/items/1").AssertNoContent()
	app.GetJSON("/nope").AssertNotFound()
}

func TestSessionAndHeaders(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"APP_NAME": "Test"}))
	if app.Config().Name != "Test" {
		t.Errorf("name = %q", app.Config().Name)
	}
	app.WithSession(func(s *session.Session) { s.Put("user", "ada") }).
		Get("/items").AssertSee("user ada")

	app.WithHeader("X-Test", "yes").Get("/env").AssertOK().AssertSee("testing yes")

	req := httptest.NewRequest(http.MethodDelete, "/items/1", nil)
	req.Header.Set("HX-Request", "true")
	app.Do(req).AssertOK()
	app.Delete("/items/1").AssertNoContent()
}

// fakeT records failures, to test that assertions fail.
type fakeT struct {
	testing.TB
	errs []string
}

func (f *fakeT) Helper() {}

func (f *fakeT) Errorf(format string, args ...any) {
	f.errs = append(f.errs, fmt.Sprintf(format, args...))
}

type fatal struct{ msg string }

func (f *fakeT) Fatalf(format string, args ...any) { panic(fatal{fmt.Sprintf(format, args...)}) }

// fatalOf runs fn and returns the message it passed to Fatalf.
func fatalOf(fn func()) (msg string) {
	defer func() {
		if v, ok := recover().(fatal); ok {
			msg = v.msg
		}
	}()
	fn()
	return ""
}

func TestFailures(t *testing.T) {
	ft := &fakeT{TB: t}
	app := anetostest.New(ft, setup)
	res := app.GetJSON("/api/items")
	res.AssertStatus(201).
		AssertRedirect("/x").
		AssertHeader("X-Nope", "1").
		AssertSee("nothing like this").
		AssertDontSee("total").
		AssertJSON(map[string]any{"total": 1}).
		AssertJSONPath("data.0.title", "x").
		AssertJSONPath("total", 3).
		AssertValidationErrors("title").
		AssertSessionHas("x")
	app.Get("/items").AssertJSON(nil)
	app.GetJSON("/big").AssertJSON(9007199254740992) // not rounded through float64
	app.PostForm("/items", url.Values{"title": {"ok"}}).AssertNoValidationErrors().Follow().AssertSee("C++", "a+b")
	want := []string{
		"status 200, want 201",
		"status 200, want a redirect to /x",
		"no X-Nope header",
		`body doesn't contain "nothing like this"`,
		`body contains "total"`,
		`JSON body`,
		`JSON data.0.title: "0": null isn't an object or array`,
		"JSON total = 0, want 3",
		"status 200, want 422 (API) or a redirect back",
		`session has no "x"`,
		"body isn't JSON",
		"JSON body\n  9007199254740993\nwant\n  9007199254740992",
	}
	if len(ft.errs) != len(want) {
		t.Fatalf("errors:\n%s", strings.Join(ft.errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(ft.errs[i], w) {
			t.Errorf("error %d = %q, want %q", i, ft.errs[i], w)
		}
		if !strings.HasPrefix(ft.errs[i], "GET /") && !strings.HasPrefix(ft.errs[i], "POST /") {
			t.Errorf("error %d = %q, want the request", i, ft.errs[i])
		}
	}
}

func TestTestingEnvFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module x\n")
	write(".env", "APP_NAME=FromDotEnv\n")
	write(".env.testing", "APP_NAME=FromTestingFile\nAPP_ENV=production\nHTTP_ACCESS_LOG=true\n")
	sub := filepath.Join(dir, "app", "handlers")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	app := anetostest.New(t, nil)
	cfg := app.Config()
	if cfg.Name != "FromTestingFile" || cfg.Env != "testing" {
		t.Errorf("config = %+v", cfg)
	}
	if v, _ := app.Source().Lookup("HTTP_ACCESS_LOG"); v != "true" {
		t.Errorf("HTTP_ACCESS_LOG = %q", v)
	}
	if app.Router() != nil {
		t.Error("router without a server")
	}
}

func TestClient(t *testing.T) {
	ft := &fakeT{TB: t}
	app := anetostest.New(ft, setup)

	// Paths are encoded, not a panic.
	app.Get("/env?q=hello world&x=<y>").AssertOK()
	if msg := fatalOf(func() { app.Get("https://example.org/") }); !strings.Contains(msg, "not the test site") {
		t.Errorf("another site: %q", msg)
	}

	// A redirect to another site isn't followed into the app.
	res := app.Get("/away").AssertRedirect("https://example.org/login")
	if msg := fatalOf(func() { res.Follow() }); !strings.Contains(msg, "another site") {
		t.Errorf("Follow: %q", msg)
	}

	// Only pages become the Referer: not JSON, not htmx fragments.
	app.Get("/items/new")
	app.GetJSON("/api/items")
	req := httptest.NewRequest(http.MethodGet, "/items", nil)
	req.Header.Set("HX-Request", "true")
	app.Do(req)
	app.PostForm("/items", url.Values{"title": {""}}).AssertRedirect("/items/new")

	// WithHeader replaces the method's Accept.
	app.WithHeader("Accept", "application/vnd.test+json").GetJSON("/accept").AssertSee("application/vnd.test+json")

	// Do keeps the request's own cookies and adds the jar's.
	app.WithSession(func(s *session.Session) { s.Put("user", "ada") })
	req = httptest.NewRequest(http.MethodGet, "/items", nil)
	req.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	app.Do(req).AssertSee("user ada")
	if len(ft.errs) > 0 {
		t.Errorf("errors:\n%s", strings.Join(ft.errs, "\n"))
	}
}

func TestSecureSessionCookies(t *testing.T) {
	for _, env := range []map[string]string{
		{"SESSION_SECURE": "true"}, // __Host- prefix
		{"SESSION_SECURE": "true", "SESSION_SAME_SITE": "none"},
		{"SESSION_DOMAIN": "example.com"},
		{"SESSION_PATH": "/items"},
	} {
		app := anetostest.New(t, setup, anetostest.Env(env))
		app.WithSession(func(s *session.Session) { s.Put("user", "ada") }).
			Get("/items").AssertSee("user ada")
		app.Get("/items/new")
		app.PostForm("/items", url.Values{"title": {"Tea"}}).AssertRedirect("/items").AssertSessionHas("status", "Item created.")
	}
}

func TestHalfSwitchedDatabase(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module x\n",
		".env":         "DB_CONNECTION=postgres\nDB_DATABASE=blog\n",
		".env.testing": "DB_DATABASE=blog_test\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	msg := fatalOf(func() { anetostest.New(&fakeT{TB: t}, nil) })
	if !strings.Contains(msg, "DB_DATABASE=blog_test but no DB_CONNECTION") || !strings.Contains(msg, "Set DB_CONNECTION=postgres in .env.testing") {
		t.Errorf("message = %q", msg)
	}
	msg = fatalOf(func() {
		anetostest.New(&fakeT{TB: t}, nil, anetostest.Env(map[string]string{"DB_DATABASE": "", "DB_URL": "postgres://db/blog_test"}))
	})
	if !strings.Contains(msg, "DB_URL but no DB_CONNECTION") {
		t.Errorf("DB_URL message = %q", msg)
	}
	// Without .env.testing, the tests would use an in-memory SQLite
	// database, while the app uses PostgreSQL.
	if err := os.Remove(filepath.Join(dir, ".env.testing")); err != nil {
		t.Fatal(err)
	}
	msg = fatalOf(func() { anetostest.New(&fakeT{TB: t}, nil) })
	if !strings.Contains(msg, "in-memory SQLite database, while .env uses postgres") || !strings.Contains(msg, "Add .env.testing") {
		t.Errorf("no .env.testing: %q", msg)
	}
}

func TestCachePrefix(t *testing.T) {
	var stores []*cache.Cache
	withCache := func(app *anetos.App) (*web.Server, error) {
		c, err := cache.New(app)
		stores = append(stores, c)
		return nil, err
	}
	a := anetostest.New(t, withCache)
	b := anetostest.New(t, withCache)
	pa, pb := stores[0].Prefix(), stores[1].Prefix()
	if !strings.HasPrefix(pa, "test-") || pa == pb {
		t.Errorf("prefixes %q and %q", pa, pb)
	}
	if err := cache.Set(a.Context(), "k", 1, cache.Forever); err != nil {
		t.Fatal(err)
	}
	if ok, _ := cache.Has(b.Context(), "k"); ok {
		t.Error("another App sees the item")
	}
	c := anetostest.New(t, withCache, anetostest.Env(map[string]string{"CACHE_PREFIX": "mine:"}))
	if p := stores[2].Prefix(); p != "mine:" || c == nil {
		t.Errorf("Env's CACHE_PREFIX: %q", p)
	}
}

// Emails are kept, not sent, unless a test says otherwise.
func TestMailDriver(t *testing.T) {
	t.Setenv("MAIL_DRIVER", "smtp")
	app := anetostest.New(t, nil)
	if v, _ := app.Source().Lookup("MAIL_DRIVER"); v != "memory" {
		t.Errorf("MAIL_DRIVER = %q", v)
	}
	if v, _ := app.Source().Lookup("STORAGE_DRIVER"); v != "memory" {
		t.Errorf("STORAGE_DRIVER = %q", v)
	}
	if app.Config().URL != "http://example.test" {
		t.Errorf("APP_URL = %q", app.Config().URL)
	}
	app = anetostest.New(t, nil, anetostest.Env(map[string]string{"MAIL_DRIVER": "log"}))
	if v, _ := app.Source().Lookup("MAIL_DRIVER"); v != "log" {
		t.Errorf("MAIL_DRIVER with Env = %q", v)
	}
}

type uploadInput struct {
	Title string                `form:"title"`
	File  *multipart.FileHeader `form:"file"`
}

func TestPostMultipart(t *testing.T) {
	app := anetostest.New(t, func(app *anetos.App) (*web.Server, error) {
		srv, err := web.NewServer(app)
		if err != nil {
			return nil, err
		}
		srv.Router().Post("/upload", web.H(func(c *web.Ctx, in uploadInput) (web.Responder, error) {
			f, err := in.File.Open()
			if err != nil {
				return nil, err
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			return web.Text(http.StatusOK, fmt.Sprintf("%s %s %s %s", in.Title, in.File.Filename, in.File.Header.Get("Content-Type"), b)), nil
		}))
		return srv, nil
	})
	app.PostMultipart("/upload", url.Values{"title": {"Report"}},
		anetostest.Upload{Field: "file", Filename: "r é.txt", Content: []byte("data"), ContentType: "text/plain"}).
		AssertOK().AssertSee("Report r é.txt text/plain data")
}
