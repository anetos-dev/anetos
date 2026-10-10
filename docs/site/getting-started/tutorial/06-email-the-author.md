---
title: "6. Email the author"
since: v0.3.0
weight: 6
---

# 6. Email the author

When someone comments on an issue, its author gets an email. The comment
handler of part 4 already emits `CommentAdded`; you add the email and a
listener that sends it from a queue job, so a slow mail server never
slows the page down, and a failed send is retried.

## The email

Create `app/mailers/comment.go`, in package `mailers` (where
`make:auth` put the account emails):

```go
import (
	"context"

	"anetos.dev/anetos/mailer"

	"tracker/views"
)
```

(Copied from [`examples/tutorial/app/mailers/comment.go`](../../../../examples/tutorial/app/mailers/comment.go), region `imports`.)

```go
// NewComment tells an issue's author that someone commented on it.
type NewComment struct {
	Name, Email string // the author's
	Issue       string // the issue's title
	By, Body    string // the comment's author and text
	URL         string // the issue's page
}

// Build implements mailer.Mailable.
func (m NewComment) Build(context.Context) (*mailer.Message, error) {
	return &mailer.Message{
		To:      []mailer.Address{{Address: m.Email}},
		Subject: "New comment on " + m.Issue,
		HTML:    views.NewCommentMail(m.Name, m.Issue, m.By, m.Body, m.URL),
	}, nil
}
```

(Copied from [`examples/tutorial/app/mailers/comment.go`](../../../../examples/tutorial/app/mailers/comment.go), region `mailable`.)

And its HTML, `views/comment_mail.templ`, in the frame of the account
emails (`authMail`, from `make:auth`):

```templ
// NewCommentMail is the email about a new comment.
templ NewCommentMail(name, issue, by, body, url string) {
	@authMail() {
		<p>Hello { name },</p>
		<p>{ by } commented on “{ issue }”:</p>
		<blockquote>{ body }</blockquote>
		<p><a href={ templ.SafeURL(url) }>See the issue</a></p>
	}
}
```

(Copied from [`examples/tutorial/views/comment_mail.templ`](../../../../examples/tutorial/views/comment_mail.templ), region `mail`.)

## The listener

Create `app/listeners/comments.go`, a new package, `listeners`:

```go
import (
	"context"
	"errors"
	"fmt"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/mailer"

	"tracker/app/mailers"
	"tracker/app/models"
)
```

(Copied from [`examples/tutorial/app/listeners/comments.go`](../../../../examples/tutorial/app/listeners/comments.go), region `imports`.)

```go
// EmailAuthor emails an issue's author about a new comment, unless they
// wrote it. It runs as a queue job (events.OnQueued), after the comment
// is committed: retried if the mail server fails.
func EmailAuthor(ctx context.Context, e models.CommentAdded) error {
	comment, err := db.Query[models.Comment](ctx).With(models.CommentRels.Author).Find(e.CommentID)
	if errors.Is(err, db.ErrNotFound) {
		return nil // deleted since
	}
	if err != nil {
		return err
	}
	issue, err := db.Query[models.Issue](ctx).With(models.IssueRels.Author).Find(comment.IssueID)
	if err != nil {
		return err
	}
	if issue.AuthorID == comment.AuthorID {
		return nil
	}
	url, err := mailer.URL(ctx, fmt.Sprintf("/issues/%d", issue.ID)) // on APP_URL
	if err != nil {
		return err
	}
	return mailer.Send(ctx, mailers.NewComment{
		Name: issue.Author.Name, Email: issue.Author.Email, Issue: issue.Title,
		By: comment.Author.Name, Body: comment.Body, URL: url,
	})
}
```

(Copied from [`examples/tutorial/app/listeners/comments.go`](../../../../examples/tutorial/app/listeners/comments.go), region `listener`.)

The listener reads the comment again rather than trusting the event's
contents: it runs later, maybe after a retry. `mailer.URL` makes the
link absolute, on `APP_URL`, since an email leaves the app.

Add it in `main.go`: import `"tracker/app/listeners"`, and where
`setup` sets up the events, replace the `events.New` lines with:

```go
bus, err := events.New(app)
if err != nil {
	return nil, err
}
// The author's email about a new comment, sent by a queue worker.
if err := events.OnQueued(bus, listeners.EmailAuthor); err != nil {
	return nil, err
}
```

(Copied from [`examples/tutorial/main.go`](../../../../examples/tutorial/main.go), region `listeners`.)

`events.OnQueued` turns each `CommentAdded` into a job, once the
comment's transaction commits. The queue's workers run it: in
development they run in the app (`QUEUE_DRIVER=database` in `.env`); in
production, in the same binary or in processes of their own (`run
--only=workers`).

## Try it

Register a second user in another browser (or a private window),
comment on the first user's issue, and look in the `anetos dev`
terminal: the email is in the log, a moment later.

## Test it

`notify_test.go`, in package `main`:

```go
import (
	"net/url"
	"testing"

	"anetos.dev/anetos/anetostest"

	"tracker/app/mailers"
	"tracker/app/models"
	"tracker/database/factories"
)
```

(Copied from [`examples/tutorial/notify_test.go`](../../../../examples/tutorial/notify_test.go), region `imports`.)

```go
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
```

(Copied from [`examples/tutorial/notify_test.go`](../../../../examples/tutorial/notify_test.go), region `test`.)

The [events](../../guides/events.md), [queues](../../guides/queues.md)
and [mail](../../guides/mail.md) guides go further: delays, retries,
SMTP and Postmark.

Next: [7. Deploy](07-deploy.md).
