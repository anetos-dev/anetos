// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"anetos.dev/anetos/anetostest"
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
