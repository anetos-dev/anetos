package main

// region: imports
import (
	"net/url"
	"testing"

	"anetos.dev/anetos/anetostest"

	"tracker/app/models"
	"tracker/database/factories"
)

// endregion

// region: test
func TestIssuePage(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, factories.Users)
	issue := anetostest.Create(app, factories.Issues.With(func(i *models.Issue) { i.AuthorID = ada.ID }))
	anetostest.ActingAs(app, &ada)

	app.Get("/issues/1").AssertOK().AssertSee(issue.Title, "Close")
	app.PostForm("/issues/1/comments", url.Values{"body": {"Same on Firefox."}}).
		AssertRedirect("/issues/1").
		Follow().
		AssertSee("Same on Firefox.")

	// With htmx, the comment comes back alone, to add to the list.
	app.WithHeader("HX-Request", "true")
	app.PostForm("/issues/1/comments", url.Values{"body": {"Fixed."}}).
		AssertOK().
		AssertSee(`<article class="card comment">`, "Fixed.").
		AssertDontSee("<html")
	app.WithHeader("HX-Request", "")

	app.PostForm("/issues/1/status", url.Values{"status": {"closed"}}).AssertRedirect("/issues/1")
	anetostest.AssertDatabaseHas[models.Issue](app, models.IssueCols.Status.Eq("closed"))
	app.Get("/issues/1").AssertSee("Reopen")
}

// endregion
