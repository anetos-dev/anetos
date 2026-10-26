// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"testing"

	"anetos.dev/anetos/anetostest"
)

// region: test
func TestNotes(t *testing.T) {
	app := anetostest.New(t, setup)

	// The first visit: the root view's HTML holding the page.
	app.Get("/notes").AssertOK().AssertInertia("Notes/Index").AssertInertiaProp("next", 2)

	// The client's visits after it get the page object.
	app.AsInertia()
	page := app.Get("/notes").AssertOK().InertiaPage()
	if page.DeferredProps["default"][0] != "stats" || page.MergeProps[0] != "notes" {
		t.Errorf("deferred %v, merged %v", page.DeferredProps, page.MergeProps)
	}

	// An invalid note goes back to the form with its errors.
	app.Get("/notes/new").AssertInertia("Notes/Create")
	app.PostJSON("/notes", NoteInput{Title: ""}).AssertRedirect("/notes/new")
	app.Get("/notes/new").AssertInertiaProp("errors", map[string]string{"title": "The title field is required."})

	// A valid one is saved, with a message for the next page.
	app.PostJSON("/notes", NoteInput{Title: "Groceries"}).AssertRedirect("/notes")
	page = app.Get("/notes").InertiaPage()
	if string(page.Flash["toast"]) != `"Saved “Groceries”."` {
		t.Errorf("flash %s", page.Flash["toast"])
	}
}

// endregion

// The deferred statistics and the next page come in partial reloads.
func TestPartialReloads(t *testing.T) {
	app := anetostest.New(t, setup).AsInertia()
	app.WithHeader("X-Inertia-Partial-Component", "Notes/Index").WithHeader("X-Inertia-Partial-Data", "stats")
	app.Get("/notes").AssertInertiaProp("stats", Stats{Count: 23, Words: 115})
	app.WithHeader("X-Inertia-Partial-Data", "notes,next")
	page := app.Get("/notes?page=3").InertiaPage()
	if len(page.Props) != 3 || string(page.Props["next"]) != "0" || page.MatchPropsOn[0] != "notes.id" {
		t.Errorf("page 3: %+v", page)
	}
}

// A DELETE's redirect is a 303, and errors are the frontend's page.
func TestDeleteAndErrors(t *testing.T) {
	app := anetostest.New(t, setup).AsInertia()
	app.Get("/notes")
	app.Delete("/notes/1").AssertStatus(http.StatusSeeOther).AssertHeader("Location", "/notes")
	app.Get("/boom").AssertStatus(http.StatusTeapot).AssertInertia("Error").AssertInertiaProp("detail", "This page is a teapot.")
	app.Get("/nowhere").AssertNotFound().AssertInertia("Error")
}
