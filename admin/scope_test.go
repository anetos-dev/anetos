// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/db"
)

// Note is a post seen differently: a nullable time, a hidden field, a
// select limited to some choices, a deleted_at column without
// SoftDeletes.
type Note struct {
	ID        int64      `db:"id,pk"`
	Title     string     `db:"title"`
	Body      string     `db:"body"`
	Status    string     `db:"status"`
	Featured  bool       `db:"featured"`
	CreatedAt *time.Time `db:"created_at"`
	DeletedAt *time.Time `db:"deleted_at"`
}

type NoteForm struct {
	Title    string `json:"title" validate:"required"`
	Status   string `json:"status" admin:"select"`
	Featured bool   `json:"featured" admin:"-"`
}

func notes(scoped bool) Resource[Note, NoteForm] {
	r := Resource[Note, NoteForm]{
		Name: "notes",
		Columns: []Column[Note]{
			Field[Note]("Title", "title"),
			Field[Note]("Created", "created_at"),
		},
		Search: []string{"title"},
		Edit:   func(n Note) NoteForm { return NoteForm{Title: n.Title, Status: n.Status, Featured: n.Featured} },
		Apply: func(_ context.Context, in NoteForm, n *Note) error {
			n.Title, n.Status, n.Featured = in.Title, in.Status, in.Featured
			return nil
		},
		Choices: map[string]func(context.Context) ([]Choice, error){
			"status": func(context.Context) ([]Choice, error) { return Choices("draft", "published"), nil },
		},
	}
	if scoped {
		r.Query = func(q *db.Q[Note]) *db.Q[Note] { return q.Where(db.C("status").Eq("draft")) }
	}
	return r
}

func notesApp(t *testing.T, scoped bool, env ...string) *anetostest.App {
	t.Helper()
	opts := []anetostest.Option{}
	if len(env) > 0 {
		m := map[string]string{}
		for i := 0; i+1 < len(env); i += 2 {
			m[env[i]] = env[i+1]
		}
		opts = append(opts, anetostest.Env(m))
	}
	return anetostest.New(t, setupWith(func(p *Panel) error { return Add(p, notes(scoped)) }), opts...)
}

func TestNullableTimesAndHiddenFields(t *testing.T) {
	app := notesApp(t, false)
	signIn(t, app, "Ada", "admin")
	// A NULL created_at (a nil *time.Time) shows as nothing.
	n := Note{Title: "Undated", Status: "draft", Featured: true}
	if err := db.Create(app.Context(), &n); err != nil {
		t.Fatal(err)
	}
	app.Get("/admin/notes").AssertOK().AssertSee("Undated")
	app.Get(fmt.Sprintf("/admin/notes/%d", n.ID)).AssertOK()

	// admin:"-" isn't in the form, and can't be posted: it keeps its value.
	app.Get(fmt.Sprintf("/admin/notes/%d/edit", n.ID)).AssertOK().AssertDontSee(`name="featured"`)
	app.PostForm(fmt.Sprintf("/admin/notes/%d", n.ID), url.Values{"title": {"Renamed"}, "status": {"draft"}, "featured": {"false"}}).
		AssertStatus(http.StatusSeeOther)
	got, _ := db.Find[Note](app.Context(), n.ID)
	if got.Title != "Renamed" || !got.Featured {
		t.Errorf("updated %+v", got)
	}
	app.PostForm("/admin/notes", url.Values{"title": {"New"}, "status": {"draft"}, "featured": {"true"}})
	if created, err := db.Query[Note](app.Context()).Where(db.C("title").Eq("New")).First(); err != nil || created.Featured {
		t.Errorf("created %+v, %v", created, err)
	}

	// A select value that isn't a choice is refused.
	app.Get("/admin/notes/new")
	app.PostForm("/admin/notes", url.Values{"title": {"Sneaky"}, "status": {"archived"}}).AssertValidationErrors("status")
	app.Get("/admin/notes/new").AssertSee("The selected status is invalid.")

	// A deleted_at column without db.SoftDeletes: no trash; deleting is
	// for good, and says so.
	app.Get("/admin/notes/trash").AssertNotFound()
	app.Get(fmt.Sprintf("/admin/notes/%d", n.ID)).AssertSee("This can&#39;t be undone.")
	app.PostForm(fmt.Sprintf("/admin/notes/%d/delete", n.ID), nil).Follow().AssertSee(fmt.Sprintf("Note #%d deleted.", n.ID))
}

func TestScopeOnSave(t *testing.T) {
	app := notesApp(t, true)
	signIn(t, app, "Ada", "admin")
	// A record Apply puts outside Query isn't created, nor moved out.
	app.Get("/admin/notes/new")
	app.PostForm("/admin/notes", url.Values{"title": {"Out"}, "status": {"published"}}).
		AssertRedirect("/admin/notes/new").Follow().AssertSee("would be outside this list")
	if n, _ := db.Query[Note](app.Context()).Count(); n != 0 {
		t.Errorf("%d notes created", n)
	}
	n := Note{Title: "In", Status: "draft"}
	if err := db.Create(app.Context(), &n); err != nil {
		t.Fatal(err)
	}
	path := "/admin/notes/" + strconv.FormatInt(n.ID, 10)
	app.PostForm(path, url.Values{"title": {"In"}, "status": {"published"}}).AssertRedirect(path + "/edit")
	if got, _ := db.Find[Note](app.Context(), n.ID); got.Status != "draft" {
		t.Errorf("moved out of scope: %+v", got)
	}
	app.PostForm(path, url.Values{"title": {"Still in"}, "status": {"draft"}}).AssertRedirect(path)
}

func TestSearchFragmentAndHostWithPort(t *testing.T) {
	app := notesApp(t, false)
	signIn(t, app, "Ada", "admin")
	for _, title := range []string{"100% sure", "100 percent"} {
		if err := db.Create(app.Context(), &Note{Title: title, Status: "draft"}); err != nil {
			t.Fatal(err)
		}
	}
	// What htmx's live search fetches: the page, whose #results it takes.
	app.WithHeader("HX-Request", "true").Get("/admin/notes?q=100%25").AssertOK().
		AssertSee(`<div id="results">`, "100% sure").AssertDontSee("100 percent")

	host := notesApp(t, false, "ADMIN_HOST", "admin.localhost:8080")
	signIn(t, host, "Ada", "admin")
	req, err := http.NewRequest(http.MethodGet, "/notes", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "admin.localhost:8080"
	host.Do(req).AssertOK().AssertSee(`href="/_assets/admin.css`, `href="/"`)
	if u, _ := host.Router().URL("admin.notes.index"); !strings.HasSuffix(u, "://admin.localhost:8080/notes") {
		t.Errorf("URL %q", u)
	}
}

func TestFormNamesAndPermissions(t *testing.T) {
	type noJSON struct{ Title string }
	if _, err := formSpecs(reflect.TypeFor[noJSON]()); err == nil || !strings.Contains(err.Error(), "needs a json tag") {
		t.Errorf("a field without a json tag: %v", err)
	}
	type named struct {
		Title string `json:"title" form:"heading"`
		Skip  string `json:"skip" form:"-"`
		Gone  string `json:"-"`
		ID    int64  `query:"id"`
	}
	specs, err := formSpecs(reflect.TypeFor[named]())
	if err != nil || len(specs) != 1 || specs[0].Name != "heading" {
		t.Errorf("specs %+v, %v", specs, err)
	}

	app := anetostest.New(t, setup)
	p := anetos.MustResolve[*Panel](app.App)
	q := &Panel{byName: map[string]resource{}, reg: p.reg}
	run := func(context.Context, *Tag) error { return nil }
	for perm, ok := range map[string]bool{"": true, "delete": true, "admin.access": true, "Update": false, "publish": false, "posts.publish": false} {
		err := Add(q, Resource[Tag, struct{}]{Name: "t" + strconv.Itoa(len(q.res)), Actions: []Action[Tag]{{Name: "go", Run: run, Permission: perm}}})
		if (err == nil) != ok {
			t.Errorf("permission %q: %v", perm, err)
		}
	}
	if err := (Config{Path: "/", PerPage: 25}).Validate(); err == nil {
		t.Error("ADMIN_PATH=/ without a host accepted")
	}
	if err := (Config{Path: "/", Host: "admin.example.com", PerPage: 25}).Validate(); err != nil {
		t.Errorf("ADMIN_PATH=/ with a host: %v", err)
	}
}
