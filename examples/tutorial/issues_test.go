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
func TestIssues(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, factories.Users)
	anetostest.ActingAs(app, &ada) // logged in for the requests that follow

	app.Get("/issues/new").AssertOK()
	// Invalid: back to the form, with the errors.
	app.PostForm("/issues", url.Values{"title": {""}}).
		AssertRedirect("/issues/new").
		AssertValidationErrors("title")
	// Valid: to the list, which shows it.
	app.PostForm("/issues", url.Values{"title": {"Login fails"}, "body": {"Since Tuesday."}}).
		AssertRedirect("/issues").
		Follow().
		AssertSee("Issue opened.", "Login fails", "by "+ada.Name)
	anetostest.AssertDatabaseHas[models.Issue](app, models.IssueCols.Title.Eq("Login fails"), models.IssueCols.AuthorID.Eq(ada.ID))

	// Only its author edits it.
	app.PutForm("/issues/1", url.Values{"title": {"Login fails on Safari"}}).AssertRedirect("/issues")
	grace := anetostest.Create(app, factories.Users)
	anetostest.ActingAs(app, &grace)
	app.Get("/issues/1/edit").AssertForbidden()
	app.Get("/issues/2/edit").AssertNotFound()

	// Guests go to the login page.
	app.PostForm("/logout", nil)
	app.Get("/issues").AssertRedirect("/login")
}

// endregion
