package main

// region: imports
import (
	"net/url"
	"testing"

	"anetos.dev/anetos/anetostest"

	"tracker/app/mailers"
	"tracker/app/models"
	"tracker/database/factories"
)

// endregion

// region: test

// The author gets an email when someone else comments. In tests the
// queue runs jobs at once, and emails are kept, not sent.
func TestEmailAuthor(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, factories.Users)
	grace := anetostest.Create(app, factories.Users)
	anetostest.Create(app, factories.Issues.With(func(i *models.Issue) { i.Title, i.AuthorID = "Login fails", ada.ID }))

	anetostest.ActingAs(app, &ada)
	app.PostForm("/issues/1/comments", url.Values{"body": {"More details."}}).AssertRedirect("/issues/1")
	anetostest.AssertMailNotSent(app, func(m mailers.NewComment) bool { return true }) // her own

	anetostest.ActingAs(app, &grace)
	app.PostForm("/issues/1/comments", url.Values{"body": {"Same here."}}).AssertRedirect("/issues/1")
	anetostest.AssertMailSent(app, func(m mailers.NewComment) bool {
		return m.Email == ada.Email && m.By == grace.Name && m.URL == "http://example.test/issues/1"
	})
}

// endregion
