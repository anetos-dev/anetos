// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/session"
)

// region: test-forms
func TestCreateNote(t *testing.T) {
	app := anetostest.New(t, setup)

	app.Get("/").AssertRedirect("/notes")
	app.Get("/notes/new").
		AssertOK().
		AssertSee("<title>New note · Notes</title>", "/assets/htmx.min.js?v=")

	// Invalid: back to the form, with the errors and the input kept.
	app.PostForm("/notes", url.Values{"title": {"<Groceries>"}, "body": {""}}).
		AssertRedirect("/notes/new").
		AssertValidationErrors("body").
		Follow().
		AssertSee(`value="&lt;Groceries&gt;"`, "The body field is required.", "Please fix the errors below.")

	// Valid: to the list, with a flash message shown once.
	app.PostForm("/notes", url.Values{"title": {"Groceries"}, "body": {"Milk"}}).
		AssertRedirectRoute("notes.index").
		AssertSessionHas("status", "Note created.").
		Follow().
		AssertSee("Note created.", "<strong>Groceries</strong>")
	app.Get("/notes").AssertDontSee("Note created.")
}

// endregion

func TestEditAndDeleteNote(t *testing.T) {
	app := anetostest.New(t, setup)
	app.Get("/notes/new")
	app.PostForm("/notes", url.Values{"title": {"Groceries"}, "body": {"Milk"}}).AssertRedirect("/notes")

	// Edit with an HTML form (PUT through _method).
	app.Get("/notes/1/edit").AssertSee(`value="Groceries"`, `name="_method" value="PUT"`)
	app.PostForm("/notes/1", url.Values{"_method": {"PUT"}, "title": {"Shopping"}, "body": {"Milk"}}).
		AssertRedirect("/notes").
		Follow().
		AssertSee("Shopping", "Note saved.")

	// Delete with htmx: DELETE with the token in a header, empty response.
	req := httptest.NewRequest(http.MethodDelete, "/notes/1", nil)
	req.Header.Set("HX-Request", "true")
	app.Do(req).AssertOK().AssertDontSee("Shopping")
	app.Get("/notes").AssertDontSee("Shopping")

	// Without a valid token, nothing changes.
	app.PostForm("/notes", url.Values{"_token": {"forged"}, "title": {"x"}, "body": {"y"}}).AssertForbidden()
	app.Get("/notes/99/edit").AssertNotFound()
}

func TestAssets(t *testing.T) {
	app := anetostest.New(t, setup)
	for _, name := range []string{"app.css", "htmx.min.js"} {
		if res := app.Get(assets.URL(name)).AssertOK(); len(res.Body) == 0 {
			t.Errorf("%s: empty", name)
		}
	}
}

// TestDatabaseSessions runs a form post with sessions in the database: in
// memory, and in a file, where the test runs in a transaction.
func TestDatabaseSessions(t *testing.T) {
	for name, file := range map[string]string{"memory": "", "file": filepath.Join(t.TempDir(), "app.db")} {
		t.Run(name, func(t *testing.T) {
			app := anetostest.New(t, setup, anetostest.Env(map[string]string{"SESSION_DRIVER": "database", "DB_NAME": file}))
			app.WithSession(func(s *session.Session) { s.Put("theme", "dark") })
			app.Get("/notes/new").AssertOK()
			app.PostForm("/notes", url.Values{"title": {"Groceries"}, "body": {""}}).
				AssertRedirect("/notes/new").
				AssertValidationErrors("body").
				Follow().
				AssertSee(`value="Groceries"`, "The body field is required.")
			app.PostForm("/notes", url.Values{"title": {"Groceries"}, "body": {"Milk"}}).
				AssertSessionHas("status", "Note created.").
				AssertSessionHas("theme", "dark").
				Follow().
				AssertSee("Note created.")
			n, err := db.RawFirst[int64](app.Context(), "SELECT COUNT(*) FROM sessions")
			if err != nil || n != 1 {
				t.Errorf("stored sessions: %d, %v", n, err)
			}
		})
	}
}

// region: test-pagination
func TestPagination(t *testing.T) {
	app := anetostest.New(t, setup)
	notes := anetostest.CreateMany(app, NoteFactory, 12) // 10 per page, newest first
	item := func(i int) string { return "<strong>" + notes[i].Title + "</strong>" }

	app.Get("/notes").
		AssertOK().
		AssertSee(item(11), item(2), "Page 1 of 2", `href="?page=2"`).
		AssertDontSee(item(1), `rel="prev"`)
	app.Get("/notes?page=2").
		AssertSee(item(1), item(0), `href="?page=1"`).
		AssertDontSee(item(2), `rel="next"`)
	app.Get("/notes?page=9").AssertSee(`href="?page=2"`) // past the end: back to the last page
}

// endregion

// region: test-search
func TestSearch(t *testing.T) {
	app := anetostest.New(t, setup)
	anetostest.Create(app, NoteFactory.With(func(n *Note) { n.Title, n.Body = "Groceries", "Rice, lentils and green tea" }))
	anetostest.Create(app, NoteFactory.With(func(n *Note) { n.Title, n.Body = "Tea tasting", "Darjeeling first flush" }))
	anetostest.Create(app, NoteFactory.With(func(n *Note) { n.Title, n.Body = "Ideas", "A blog about Go" }))

	// Every word must match, as a prefix from 3 letters; title matches
	// rank first.
	app.Get("/notes?q=tea").AssertOK().
		AssertSee("<strong>Tea tasting</strong>", "<strong>Groceries</strong>", `value="tea"`).
		AssertDontSee("<strong>Ideas</strong>")
	if page := app.Get("/notes?q=tea").Text(); strings.Index(page, "Tea tasting") > strings.Index(page, "Groceries") {
		t.Error("the title match isn't first")
	}
	app.Get("/notes?q=green+lent").AssertSee("<strong>Groceries</strong>").AssertDontSee("<strong>Tea tasting</strong>")
	app.Get("/notes?q=coffee").AssertSee("No notes match “coffee”.")
}

// endregion
