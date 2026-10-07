---
title: "4. The issue page"
since: v0.3.0
weight: 4
---

# 4. The issue page

Each issue gets its page, with comments that post without reloading the
page, and a button to close it.

## Comments

```sh
go tool anetos make:model Comment --migration
```

The model, and an event the app emits when someone comments (part 6
sends an email on it):

```go
// Comment is a reply on an issue.
type Comment struct {
	db.Model
	IssueID  int64  `db:"issue_id"`
	AuthorID int64  `db:"author_id"`
	Body     string `db:"body"`

	Author *User `rel:"belongs_to"` // by AuthorID
}

// CommentAdded is an event, emitted when someone comments on an issue:
// other parts of the app can react to it (events.On).
type CommentAdded struct {
	CommentID int64
}
```

(Copied from [`examples/tutorial/app/models/comment.go`](../../../../examples/tutorial/app/models/comment.go), region `model`.)

Its table. A comment belongs to an issue: deleting the issue deletes its
comments (`CascadeOnDelete`):

```go
func(s *migrate.Schema) error {
	return s.Create("comments", func(t *migrate.Table) {
		t.ID()
		t.ForeignID("issue_id").Constrained().CascadeOnDelete()
		t.ForeignID("author_id").References("users")
		t.Text("body")
		t.Timestamps()
	})
},
```

(Copied from [`examples/tutorial/database/migrations/2026_10_06_142838_create_comments_table.go`](../../../../examples/tutorial/database/migrations/2026_10_06_142838_create_comments_table.go), region `migration`.)

```sh
go run . migrate
```

## The handlers

Create `app/handlers/issue_page.go`, in package `handlers`:

```go
import (
	"context"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/web"

	"tracker/app/models"
	"tracker/views"
)
```

(Copied from [`examples/tutorial/app/handlers/issue_page.go`](../../../../examples/tutorial/app/handlers/issue_page.go), region `imports`.)

The page:

```go
// Show is an issue's page: the issue and its comments, oldest first.
func (Issues) Show(c *web.Ctx, in IssueID) (web.Responder, error) {
	issue, err := db.Query[models.Issue](c).With(models.IssueRels.Author).Find(in.ID)
	if err != nil {
		return nil, err // db.ErrNotFound: 404
	}
	comments, err := db.Query[models.Comment](c).
		Where(models.CommentCols.IssueID.Eq(issue.ID)).
		With(models.CommentRels.Author).
		OrderBy(models.CommentCols.ID.Asc()).
		Get()
	if err != nil {
		return nil, err
	}
	return web.View(views.IssuePage(issue, comments)), nil
}
```

(Copied from [`examples/tutorial/app/handlers/issue_page.go`](../../../../examples/tutorial/app/handlers/issue_page.go), region `show`.)

Adding a comment:

```go
// CommentInput is a new comment on the issue {id}.
type CommentInput struct {
	IssueID
	Body string `json:"body" validate:"required|max:10000"`
}

// Comment adds a comment, and emits CommentAdded. With htmx, the comment
// is sent back to add to the page; otherwise the browser reloads it.
func (Issues) Comment(c *web.Ctx, in CommentInput) (web.Responder, error) {
	issue, err := db.Find[models.Issue](c, in.ID)
	if err != nil {
		return nil, err
	}
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return nil, err
	}
	comment := models.Comment{IssueID: issue.ID, AuthorID: u.ID, Body: in.Body}
	err = db.Tx(c, func(ctx context.Context) error { // the comment and its event, or neither
		if err := db.Create(ctx, &comment); err != nil {
			return err
		}
		return events.Emit(ctx, models.CommentAdded{CommentID: comment.ID})
	})
	if err != nil {
		return nil, err
	}
	if c.IsHTMX() {
		comment.Author = u
		return web.View(views.CommentItem(comment)), nil
	}
	return web.RedirectRoute("issues.show", issue.ID), nil
}
```

(Copied from [`examples/tutorial/app/handlers/issue_page.go`](../../../../examples/tutorial/app/handlers/issue_page.go), region `comment`.)

The comment and its event are written in one transaction (`db.Tx`):
listeners that send email wait for it to commit, so nobody is told about
a comment that wasn't saved. A request from htmx (`c.IsHTMX`) gets the
new comment alone, to add to the page.

Closing and reopening, which `own` (from part 3) keeps to the author:

```go
// StatusInput closes or reopens the issue {id}.
type StatusInput struct {
	IssueID
	Status string `json:"status" validate:"required|in:open,closed"`
}

// SetStatus closes or reopens an issue (its author only), and goes back
// to its page.
func (Issues) SetStatus(c *web.Ctx, in StatusInput) (web.Responder, error) {
	issue, err := own(c, in.ID)
	if err != nil {
		return nil, err
	}
	issue.Status = in.Status
	if err := db.Update(c, &issue); err != nil {
		return nil, err
	}
	return web.RedirectRoute("issues.show", issue.ID), nil
}
```

(Copied from [`examples/tutorial/app/handlers/issue_page.go`](../../../../examples/tutorial/app/handlers/issue_page.go), region `status`.)

## The page

Create `views/issue_page.templ`, in package `views`:

```templ
import (
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"

	"tracker/app/models"
)
```

(Copied from [`examples/tutorial/views/issue_page.templ`](../../../../examples/tutorial/views/issue_page.templ), region `imports`.)

```templ
// IssuePage shows an issue with its comments. Its author gets the
// buttons to edit, close and reopen it.
templ IssuePage(issue models.Issue, comments []models.Comment) {
	@Layout(issue.Title) {
		<p><a href={ web.URL(ctx, "issues.index") }>← Issues</a></p>
		<div class="page-header">
			<div>
				<h1>{ issue.Title } <small>#{ issue.ID }</small></h1>
				<p class="cluster">
					<span class={ "badge", templ.KV("success", issue.Status == "open") }>{ issue.Status }</span>
					<span class="muted">Opened by { issue.Author.Name }</span>
				</p>
			</div>
			if u, ok := auth.User[*models.User](ctx); ok && u.ID == issue.AuthorID {
				<div class="cluster">
					<a class="button secondary" href={ web.URL(ctx, "issues.edit", issue.ID) }>Edit</a>
					<form method="post" action={ web.URL(ctx, "issues.status", issue.ID) } class="inline">
						@view.CSRFField(ctx)
						if issue.Status == "open" {
							<input type="hidden" name="status" value="closed"/>
							<button type="submit">Close</button>
						} else {
							<input type="hidden" name="status" value="open"/>
							<button type="submit">Reopen</button>
						}
					</form>
				</div>
			}
		</div>
		<div class="card prewrap">{ issue.Body }</div>
		<h2>Comments</h2>
		<div id="comments">
			for _, c := range comments {
				@CommentItem(c)
			}
		</div>
		// With htmx, the form posts in the background and the new comment
		// is added to the list; without JavaScript, it posts as usual.
		<form method="post" action={ web.URL(ctx, "comments.store", issue.ID) } class="card"
			hx-post={ web.URL(ctx, "comments.store", issue.ID) } hx-target="#comments" hx-swap="beforeend"
			hx-on::after-request="if (event.detail.successful) this.reset()">
			@view.CSRFField(ctx)
			<div class="field">
				<label for="comment">Add a comment</label>
				<textarea id="comment" name="body" rows="3" required>{ view.Old(ctx, "body") }</textarea>
				@fieldError("body")
			</div>
			<button type="submit">Comment</button>
		</form>
	}
}

// CommentItem is a comment: in the list, and what htmx adds to it.
templ CommentItem(c models.Comment) {
	<article class="card comment">
		<strong>{ c.Author.Name }</strong>
		<div class="prewrap">{ c.Body }</div>
	</article>
}
```

(Copied from [`examples/tutorial/views/issue_page.templ`](../../../../examples/tutorial/views/issue_page.templ), region `page`.)

The comment form works without JavaScript. With htmx, which the layout
loads, `hx-post` sends it in the background, `hx-target` and `hx-swap`
add the answer to the list, and the form clears. The CSRF token goes
along in a header the layout sets. If the server refuses a comment (a
422 for a failed validation), htmx leaves the page as it is: the
textarea's `required` stops the common case, an empty comment, in the
browser.

## The routes

In `routes/auth.go`, below the routes of part 3:

```go
members.Get("/issues/{id}", web.H(issues.Show)).Name("issues.show")
members.Post("/issues/{id}/comments", web.H(issues.Comment)).Name("comments.store")
members.Post("/issues/{id}/status", web.H(issues.SetStatus)).Name("issues.status")
```

(Copied from [`examples/tutorial/routes/auth.go`](../../../../examples/tutorial/routes/auth.go), region `routes-issue-page`.)

Open an issue from the list, comment on it, close it.

## Test it

A factory for issues, in `database/factories/factories.go`: first these
imports, below the package line,

```go
import (
	"fmt"

	"anetos.dev/anetos/db/factory"

	"tracker/app/models"
)
```

(Copied from [`examples/tutorial/database/factories/factories.go`](../../../../examples/tutorial/database/factories/factories.go), region `imports`.)

then the factory:

```go
// Issues are open issues. Set AuthorID:
//
//	factories.Issues.With(func(i *models.Issue) { i.AuthorID = u.ID })
var Issues = factory.New(func(n int) models.Issue {
	return models.Issue{Title: fmt.Sprintf("Issue %d", n), Body: "Steps to reproduce.", Status: "open"}
})
```

(Copied from [`examples/tutorial/database/factories/factories.go`](../../../../examples/tutorial/database/factories/factories.go), region `issues`.)

And `issue_page_test.go`, in package `main`:

```go
import (
	"net/url"
	"testing"

	"anetos.dev/anetos/anetostest"

	"tracker/app/models"
	"tracker/database/factories"
)
```

(Copied from [`examples/tutorial/issue_page_test.go`](../../../../examples/tutorial/issue_page_test.go), region `imports`.)

```go
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
```

(Copied from [`examples/tutorial/issue_page_test.go`](../../../../examples/tutorial/issue_page_test.go), region `test`.)

Next: [5. Search](05-search.md).
