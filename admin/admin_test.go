// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
)

type User struct {
	db.Model
	Name string `db:"name"`
}

func (u *User) AuthID() string       { return strconv.FormatInt(u.ID, 10) }
func (u *User) AuthPassword() string { return "" }
func (u *User) AdminName() string    { return u.Name }

type Post struct {
	db.Model
	db.SoftDeletes
	Title    string `db:"title"`
	Body     string `db:"body"`
	Status   string `db:"status"`
	Featured bool   `db:"featured"`
}

type PostForm struct {
	Title    string `json:"title" validate:"required|max:100"`
	Body     string `json:"body" admin:"textarea,help=Markdown."`
	Status   string `json:"status" admin:"select=draft|published" validate:"required|in:draft,published"`
	Featured bool   `json:"featured"`
}

type Tag struct {
	ID   string `db:"id,pk"`
	Name string `db:"name"`
}

var migrations = migrate.NewSet("app")

func init() {
	migrations.AddFunc("2026_10_06_000000_create_tables", func(s *migrate.Schema) error {
		return errors.Join(
			s.Create("users", func(t *migrate.Table) {
				t.ID()
				t.String("name", 100)
				t.Timestamps()
			}),
			s.Create("posts", func(t *migrate.Table) {
				t.ID()
				t.String("title", 100)
				t.Text("body")
				t.String("status", 20)
				t.Boolean("featured").Default(false)
				t.Timestamps()
				t.SoftDeletes()
			}),
			s.Create("notes", func(t *migrate.Table) {
				t.ID()
				t.String("title", 100)
				t.Text("body").Default("")
				t.String("status", 20)
				t.Boolean("featured").Default(false)
				t.Timestamp("created_at").Nullable()
				t.Timestamp("deleted_at").Nullable()
			}),
			s.Create("tags", func(t *migrate.Table) {
				t.String("id", 50).Primary()
				t.String("name", 100)
			}),
		)
	}, func(s *migrate.Schema) error { return nil })
}

var users = auth.Users[*User]{
	ByID: func(ctx context.Context, id string) (*User, error) {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			return nil, auth.ErrNoUser
		}
		u, err := db.Find[User](ctx, n)
		if errors.Is(err, db.ErrNotFound) {
			return nil, auth.ErrNoUser
		}
		return &u, err
	},
	ByLogin: func(context.Context, string) (*User, error) { return nil, auth.ErrNoUser },
}

func postsResource() Resource[Post, PostForm] {
	return Resource[Post, PostForm]{
		Name: "posts",
		Columns: []Column[Post]{
			Field[Post]("Title", "title"),
			Field[Post]("Status", "status"),
			{Title: "Featured", Value: func(p Post) any { return p.Featured }},
		},
		Search:  []string{"title", "body"},
		Filters: []Filter[Post]{Equals[Post]("status", "Status", Choices("draft", "published")...)},
		Label:   func(p Post) string { return p.Title },
		Edit: func(p Post) PostForm {
			return PostForm{Title: p.Title, Body: p.Body, Status: p.Status, Featured: p.Featured}
		},
		Apply: func(_ context.Context, in PostForm, p *Post) error {
			if in.Title == "Forbidden" {
				return validate.Fail("title", "That title is taken.")
			}
			p.Title, p.Body, p.Status, p.Featured = in.Title, in.Body, in.Status, in.Featured
			return nil
		},
		Actions: []Action[Post]{{
			Name: "publish", Title: "Publish", When: func(p Post) bool { return p.Status != "published" },
			Run: func(ctx context.Context, p *Post) error {
				if p.Title == "Unpublishable" {
					return validate.Fail("status", "This post can't be published.")
				}
				p.Status = "published"
				return db.Update(ctx, p)
			},
		}},
		BulkActions: []BulkAction[Post]{{
			Name: "feature", Title: "Feature",
			Run: func(_ context.Context, q *db.Q[Post]) (int64, error) {
				return q.Update(db.C("featured").Set(true))
			},
		}},
	}
}

func setupWith(opts func(p *Panel) error) func(app *anetos.App) (*web.Server, error) {
	return func(app *anetos.App) (*web.Server, error) {
		if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
			return nil, err
		}
		if _, err := migrate.ForApp(app, []*migrate.Set{migrations, auth.Migrations(), rbac.Migrations()}); err != nil {
			return nil, err
		}
		if _, err := cache.ForApp(app); err != nil {
			return nil, err
		}
		a, err := auth.ForApp(app, users)
		if err != nil {
			return nil, err
		}
		if _, err := rbac.ForApp(app, nil, rbac.Role{Name: "admin", Super: true}); err != nil {
			return nil, err
		}
		srv, err := web.NewServer(app)
		if err != nil {
			return nil, err
		}
		sessions, err := session.ForApp(app)
		if err != nil {
			return nil, err
		}
		r := srv.Router()
		mws := []web.Middleware{sessions.Middleware, web.CSRF(), a.Middleware}
		r.With(mws...).Post("/test/login/{id}", func(c *web.Ctx) error {
			u, err := users.ByID(c, c.Param("id"))
			if err != nil {
				return err
			}
			if err := a.Login(c, u, false); err != nil {
				return err
			}
			return c.NoContent()
		})
		p, err := New(app, a, Title("Back office"))
		if err != nil {
			return nil, err
		}
		if err := Add(p, postsResource()); err != nil {
			return nil, err
		}
		if err := Add(p, Resource[Tag, struct{}]{
			Name:    "tags",
			Columns: []Column[Tag]{Field[Tag]("Name", "name")},
		}); err != nil {
			return nil, err
		}
		if opts != nil {
			if err := opts(p); err != nil {
				return nil, err
			}
		}
		anetos.Provide(app, p)
		return srv, p.Mount(r, mws...)
	}
}

var setup = setupWith(nil)

// signIn creates a user with perms (or the admin role for "admin") and
// signs them in.
func signIn(t *testing.T, app *anetostest.App, name string, perms ...rbac.Permission) *User {
	t.Helper()
	u := &User{Name: name}
	if err := db.Create(app.Context(), u); err != nil {
		t.Fatal(err)
	}
	if len(perms) == 1 && perms[0] == "admin" {
		if err := rbac.Assign(app.Context(), u.AuthID(), rbac.Global, "admin"); err != nil {
			t.Fatal(err)
		}
	} else if len(perms) > 0 {
		if err := rbac.Grant(app.Context(), u.AuthID(), rbac.Global, perms...); err != nil {
			t.Fatal(err)
		}
	}
	app.PostForm("/test/login/"+u.AuthID(), nil).AssertNoContent()
	return u
}

func newPost(t *testing.T, app *anetostest.App, title, status string) Post {
	t.Helper()
	p := Post{Title: title, Body: "Body of " + title, Status: status}
	if err := db.Create(app.Context(), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func postURL(p Post, suffix string) string { return fmt.Sprintf("/admin/posts/%d%s", p.ID, suffix) }

func TestAccess(t *testing.T) {
	app := anetostest.New(t, setup)
	app.Get("/admin").AssertRedirect("/login")
	app.Get("/admin/posts").AssertRedirect("/login")
	signIn(t, app, "Mallory")
	app.Get("/admin").AssertForbidden()
	app.Get("/admin/posts").AssertForbidden()
}

func TestViewer(t *testing.T) {
	app := anetostest.New(t, setup)
	p := newPost(t, app, "Hello world", "draft")
	newPost(t, app, "Second post", "published")
	signIn(t, app, "Vera", Access, "admin.posts.view")

	res := app.Get("/admin").AssertOK().AssertSee("Back office", "Vera", "Posts").AssertDontSee(`href="/admin/tags"`)
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	app.Get("/admin/posts").AssertOK().AssertSee("Hello world", "Second post", "2 in all").
		AssertDontSee(`href="/admin/posts/new"`, `value="delete"`, "Trash")
	app.Get("/admin/posts?q=HELLO").AssertSee("Hello world").AssertDontSee("Second post")
	app.Get("/admin/posts?q=body+of+second").AssertSee("Second post").AssertDontSee("Hello world")
	app.Get("/admin/posts?q=%25").AssertSee("Nothing matches.") // LIKE wildcards are literal
	app.Get("/admin/posts?status=published").AssertSee("Second post").AssertDontSee("Hello world")
	app.Get("/admin/posts?status=bogus").AssertSee("Second post", "Hello world")
	asc := app.Get("/admin/posts?sort=title").Text()
	if strings.Index(asc, "Hello world") > strings.Index(asc, "Second post") {
		t.Error("sort=title isn't ascending")
	}
	desc := app.Get("/admin/posts?sort=-title").Text()
	if strings.Index(desc, "Hello world") < strings.Index(desc, "Second post") {
		t.Error("sort=-title isn't descending")
	}
	app.Get("/admin/posts?sort=body").AssertOK() // not sortable: the default order
	app.Get(postURL(p, "")).AssertOK().AssertSee("Hello world", "draft", "No").AssertDontSee("Edit", "Delete", "Publish")
	app.Get("/admin/posts/999").AssertNotFound()
	app.Get("/admin/posts/abc").AssertNotFound()

	// Everything else needs its own permission.
	app.Get("/admin/posts/new").AssertForbidden()
	app.PostForm("/admin/posts", url.Values{"title": {"x"}, "status": {"draft"}}).AssertForbidden()
	app.Get(postURL(p, "/edit")).AssertForbidden()
	app.PostForm(postURL(p, ""), url.Values{"title": {"x"}, "status": {"draft"}}).AssertForbidden()
	app.PostForm(postURL(p, "/delete"), nil).AssertForbidden()
	app.Get("/admin/posts/trash").AssertForbidden()
	app.PostForm(postURL(p, "/actions/publish"), nil).AssertForbidden()
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"delete"}, "ids": {strconv.FormatInt(p.ID, 10)}}).AssertForbidden()
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"feature"}, "ids": {strconv.FormatInt(p.ID, 10)}}).AssertForbidden()
	app.Get("/admin/tags").AssertForbidden()
}

func TestCreateAndEdit(t *testing.T) {
	app := anetostest.New(t, setup)
	signIn(t, app, "Ada", "admin")

	app.Get("/admin/posts/new").AssertOK().AssertSee(`name="title"`, `<textarea`, `<option value="published"`, "Markdown.", `type="checkbox"`)
	app.Get("/admin/posts/new")
	app.PostForm("/admin/posts", url.Values{"title": {""}, "status": {"nope"}}).AssertValidationErrors("title", "status")
	app.Get("/admin/posts/new").AssertSee("is required") // the errors, shown after the redirect back
	app.PostForm("/admin/posts", url.Values{"title": {"Forbidden"}, "status": {"draft"}}).AssertValidationErrors("title")

	res := app.PostForm("/admin/posts", url.Values{"title": {"Fresh"}, "body": {"Text"}, "status": {"draft"}, "featured": {"true"}})
	res.AssertStatus(http.StatusSeeOther)
	p, err := db.Query[Post](app.Context()).Where(db.C("title").Eq("Fresh")).First()
	if err != nil || !p.Featured || p.Body != "Text" {
		t.Fatalf("created %+v, %v", p, err)
	}
	res.AssertRedirect(postURL(p, ""))
	res.Follow().AssertSee("Post created.", "Fresh")

	app.Get(postURL(p, "/edit")).AssertOK().AssertSee(`value="Fresh"`, "checked")
	app.PostForm(postURL(p, ""), url.Values{"title": {"Renamed"}, "status": {"published"}}).AssertRedirect(postURL(p, "")).
		Follow().AssertSee("Saved.", "Renamed")
	p, _ = db.Find[Post](app.Context(), p.ID)
	if p.Title != "Renamed" || p.Featured || p.Body != "" {
		t.Errorf("updated %+v", p)
	}

	// A resource without a form is only listed and shown.
	if err := db.Create(app.Context(), &Tag{ID: "go", Name: "Go"}); err != nil {
		t.Fatal(err)
	}
	app.Get("/admin/tags").AssertOK().AssertSee("Go", `href="/admin/tags/go"`).AssertDontSee(`href="/admin/tags/new"`)
	app.Get("/admin/tags/go").AssertOK().AssertDontSee("Edit")
	app.Get("/admin/tags/new").AssertNotFound()
}

func TestDeleteTrashRestore(t *testing.T) {
	app := anetostest.New(t, setup)
	signIn(t, app, "Ada", "admin")
	p := newPost(t, app, "Doomed", "draft")

	app.Get(postURL(p, "")).AssertSee(`data-confirm="Delete Doomed?"`)
	app.PostForm(postURL(p, "/delete"), nil).AssertRedirect("/admin/posts").Follow().AssertSee("Doomed moved to the trash.")
	app.Get(postURL(p, "")).AssertNotFound()
	app.Get("/admin/posts/trash").AssertOK().AssertSee("Doomed", "Restore", "Delete forever")
	app.PostForm(postURL(p, "/restore"), nil).AssertRedirect("/admin/posts/trash")
	app.Get(postURL(p, "")).AssertOK()
	app.PostForm(postURL(p, "/restore"), nil).AssertNotFound() // not in the trash

	app.PostForm(postURL(p, "/force-delete"), nil).AssertNotFound() // only from the trash
	app.PostForm(postURL(p, "/delete"), nil)
	app.PostForm(postURL(p, "/force-delete"), nil).AssertRedirect("/admin/posts/trash")
	if n, _ := db.Query[Post](app.Context()).WithTrashed().Count(); n != 0 {
		t.Errorf("%d posts left", n)
	}

	if err := db.Create(app.Context(), &Tag{ID: "go", Name: "Go"}); err != nil {
		t.Fatal(err)
	}
	app.Get("/admin/tags/go").AssertSee("This can&#39;t be undone.")
	app.Get("/admin/tags/trash").AssertNotFound()
	app.PostForm("/admin/tags/go/delete", nil).AssertRedirect("/admin/tags")
	if _, err := db.Find[Tag](app.Context(), "go"); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("tag not deleted: %v", err)
	}
}

func TestActions(t *testing.T) {
	app := anetostest.New(t, setup)
	signIn(t, app, "Ada", "admin")
	p := newPost(t, app, "Draft", "draft")
	bad := newPost(t, app, "Unpublishable", "draft")

	app.Get(postURL(p, "")).AssertSee("Publish")
	app.PostForm(postURL(p, "/actions/publish"), nil).AssertRedirect(postURL(p, "")).Follow().
		AssertSee("Publish: done.").AssertDontSee(">Publish</button>")
	if p, _ = db.Find[Post](app.Context(), p.ID); p.Status != "published" {
		t.Errorf("status %q", p.Status)
	}
	app.PostForm(postURL(p, "/actions/publish"), nil).Follow().AssertSee("can&#39;t be done")
	app.PostForm(postURL(bad, "/actions/publish"), nil).AssertRedirect(postURL(bad, "")).Follow().
		AssertSee("This post can&#39;t be published.")
	app.PostForm(postURL(p, "/actions/nope"), nil).AssertNotFound()
}

func TestBulk(t *testing.T) {
	app := anetostest.New(t, setup)
	signIn(t, app, "Ada", "admin")
	a, b, c := newPost(t, app, "A", "draft"), newPost(t, app, "B", "draft"), newPost(t, app, "C", "draft")
	id := func(p Post) string { return strconv.FormatInt(p.ID, 10) }

	app.Get("/admin/posts").AssertSee(`name="ids"`, `value="feature"`, `value="delete"`)
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"feature"}, "ids": {id(a), id(b)}, "_back": {"/admin/posts?page=1"}}).
		AssertRedirect("/admin/posts?page=1").Follow().AssertSee("Feature: 2 of 2.")
	if n, _ := db.Query[Post](app.Context()).Where(db.C("featured").Eq(true)).Count(); n != 2 {
		t.Errorf("%d featured", n)
	}
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"delete"}, "ids": {id(c), "999"}, "_back": {"https://evil.example/"}}).
		AssertRedirect("/admin/posts").Follow().AssertSee("Deleted: 1 of 2.")
	if n, _ := db.Query[Post](app.Context()).OnlyTrashed().Count(); n != 1 {
		t.Errorf("%d trashed", n)
	}
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"feature"}}).AssertRedirect("/admin/posts").Follow().AssertSee("Select some posts first.")
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"nope"}, "ids": {id(a)}}).AssertNotFound()
	app.PostForm("/admin/posts/bulk", url.Values{"action": {"feature"}, "ids": {"x"}}).AssertStatus(http.StatusBadRequest)
}

func TestScope(t *testing.T) {
	app := anetostest.New(t, setupWith(func(p *Panel) error {
		r := postsResource()
		r.Name, r.Title = "drafts", "Drafts"
		r.Query = func(q *db.Q[Post]) *db.Q[Post] { return q.Where(db.C("status").Eq("draft")) }
		return Add(p, r)
	}))
	signIn(t, app, "Ada", "admin")
	d, pub := newPost(t, app, "Draft", "draft"), newPost(t, app, "Live", "published")
	app.Get("/admin/drafts").AssertSee("Draft", "1 in all").AssertDontSee("Live")
	app.Get(fmt.Sprintf("/admin/drafts/%d", pub.ID)).AssertNotFound()
	app.PostForm(fmt.Sprintf("/admin/drafts/%d/delete", pub.ID), nil).AssertNotFound()
	app.PostForm("/admin/drafts/bulk", url.Values{"action": {"delete"}, "ids": {strconv.FormatInt(pub.ID, 10), strconv.FormatInt(d.ID, 10)}}).
		Follow().AssertSee("Deleted: 1 of 2.")
	if _, err := db.Find[Post](app.Context(), pub.ID); err != nil {
		t.Errorf("a post outside the scope was deleted: %v", err)
	}
}

func TestPaginationAndAssets(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"ADMIN_PER_PAGE": "2"}))
	signIn(t, app, "Ada", "admin")
	for i := range 5 {
		newPost(t, app, fmt.Sprintf("Post %d", i), "draft")
	}
	app.Get("/admin/posts").AssertSee("Post 4", "Post 3", "page 1 of 3", `rel="next"`).AssertDontSee("Post 2", `rel="prev"`)
	app.Get("/admin/posts?page=3").AssertSee("Post 0", `rel="prev"`).AssertDontSee(`rel="next"`)

	page := app.Get("/admin").Text()
	for _, asset := range []string{"admin.css", "admin.js", "htmx.min.js"} {
		i := strings.Index(page, "/admin/_assets/"+asset)
		if i < 0 {
			t.Fatalf("no %s in the page", asset)
		}
		path := page[i:]
		path = path[:strings.IndexByte(path, '"')]
		app.Get(path).AssertOK()
	}
}

func TestHost(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"ADMIN_HOST": "admin.example.com"}))
	if got, want := anetos.MustResolve[*Panel](app.App).base, ""; got != want {
		t.Errorf("base %q", got)
	}
	app.Get("/admin").AssertNotFound()
	for _, rt := range app.Router().Routes() {
		if rt.Name == "admin.home" && (rt.Host != "admin.example.com" || rt.Pattern != "/") {
			t.Errorf("admin.home = %+v", rt)
		}
	}
}

func TestAddErrors(t *testing.T) {
	app := anetostest.New(t, setup)
	p := anetos.MustResolve[*Panel](app.App)
	for name, err := range map[string]error{
		"mounted":  Add(p, Resource[Tag, struct{}]{Name: "more"}),
		"no name":  Add(&Panel{}, Resource[Tag, struct{}]{}),
		"half":     Add(&Panel{}, Resource[Tag, struct{}]{Name: "x", Edit: func(Tag) struct{} { return struct{}{} }}),
		"column":   Add(&Panel{}, Resource[Tag, struct{}]{Name: "x", Columns: []Column[Tag]{Field[Tag]("Nope", "nope")}}),
		"search":   Add(&Panel{}, Resource[Tag, struct{}]{Name: "x", Search: []string{"nope"}}),
		"sort":     Add(&Panel{}, Resource[Tag, struct{}]{Name: "x", Sort: "-nope"}),
		"filter":   Add(&Panel{}, Resource[Tag, struct{}]{Name: "x", Filters: []Filter[Tag]{{Name: "q", Apply: func(q *db.Q[Tag], _ string) *db.Q[Tag] { return q }}}}),
		"action":   Add(&Panel{}, Resource[Tag, struct{}]{Name: "x", Actions: []Action[Tag]{{Name: "Bad Name", Run: func(context.Context, *Tag) error { return nil }}}}),
		"bulk":     Add(&Panel{}, Resource[Tag, struct{}]{Name: "x", BulkActions: []BulkAction[Tag]{{Name: "delete", Run: func(context.Context, *db.Q[Tag]) (int64, error) { return 0, nil }}}}),
		"no value": Add(&Panel{}, Resource[Tag, struct{}]{Name: "x", Columns: []Column[Tag]{{Title: "Empty"}}}),
		"form": Add(&Panel{}, Resource[Tag, struct{ M map[string]int }]{Name: "x",
			Edit:  func(Tag) struct{ M map[string]int } { return struct{ M map[string]int }{} },
			Apply: func(context.Context, struct{ M map[string]int }, *Tag) error { return nil }}),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	q := &Panel{byName: map[string]resource{}, reg: mustRegistry(t)}
	if err := Add(q, Resource[Tag, struct{}]{Name: "Tags"}); err == nil {
		t.Error("an invalid name was accepted")
	}
	if err := Add(q, Resource[Tag, struct{}]{Name: "tags"}); err != nil {
		t.Fatal(err)
	}
	if err := Add(q, Resource[Tag, struct{}]{Name: "tags"}); err == nil {
		t.Error("a name was accepted twice")
	}
	if got := q.Permissions(); len(got) != 5 || got[1] != "admin.tags.view" {
		t.Errorf("Permissions = %v", got)
	}
	if err := p.Mount(app.Router()); err == nil {
		t.Error("Mount twice")
	}
}

func mustRegistry(t *testing.T) *rbac.Registry {
	t.Helper()
	r, err := rbac.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewNeedsRBAC(t *testing.T) {
	anetostest.New(t, func(app *anetos.App) (*web.Server, error) {
		if _, err := db.Connect(context.Background(), app, sqlite.Driver()); err != nil {
			return nil, err
		}
		if _, err := cache.ForApp(app); err != nil {
			return nil, err
		}
		a, err := auth.ForApp(app, users)
		if err != nil {
			return nil, err
		}
		if _, err := New(app, a); err == nil || !strings.Contains(err.Error(), "rbac.ForApp") {
			t.Errorf("New without rbac: %v", err)
		}
		return web.NewServer(app)
	}, anetostest.WithoutMigrations())
}
