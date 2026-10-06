---
title: "3. Issues"
since: v0.3.0
weight: 3
---

# 3. Issues

You add the issues: a model and its table, pages to list, open and edit
them, and a test.

## The model and its table

```sh
go tool anetos make:model Issue --migration
```

This writes `app/models/issue.go` and a migration,
`database/migrations/<date>_create_issues_table.go`. Give the model its
fields:

```go
// Issue is a bug or a task. `go tool anetos gen` writes its typed columns
// (IssueCols) and relations (IssueRels) to models_gen.go.
type Issue struct {
	db.Model        // id, created_at, updated_at
	Title    string `db:"title"`
	Body     string `db:"body"`
	Status   string `db:"status"` // "open" or "closed"
	AuthorID int64  `db:"author_id"`

	Author *User `rel:"belongs_to"` // by AuthorID
}
```

(Copied from [`examples/tutorial/app/models/issue.go`](../../../../examples/tutorial/app/models/issue.go), region `model`.)

`db.Model` adds the `id`, `created_at` and `updated_at` columns. Each
field's `db` tag names its column; `Author` is a relation, loaded when
you ask for it, by the `author_id` column (the [relations
guide](../../guides/relations.md) has the others).

Then the table, in the migration's first function (the second one undoes
it):

```go
func(s *migrate.Schema) error {
	return s.Create("issues", func(t *migrate.Table) {
		t.ID()
		t.String("title", 200)
		t.Text("body")
		t.String("status", 10).Default("open")
		t.ForeignID("author_id").References("users")
		t.Timestamps()
	})
},
```

(Copied from [`examples/tutorial/database/migrations/2026_10_06_142537_create_issues_table.go`](../../../../examples/tutorial/database/migrations/2026_10_06_142537_create_issues_table.go), region `migration`.)

Create the table, and the model's typed columns:

```sh
go run . migrate
go tool anetos gen
```

`anetos gen` (which `anetos dev` also runs when you save) writes the
model's typed columns and relations to `app/models/models_gen.go`, so
queries say `models.IssueCols.Title` and `models.IssueRels.Author`, and
the compiler catches a misspelled column.

## The handlers

```sh
go tool anetos make:handler Issues
```

It wrote `app/handlers/issues.go`, with the `Issues` type and an
`Index` method. The handlers below need these imports:

```go
import (
	"net/http"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"

	"tracker/app/models"
	"tracker/views"
)
```

(Copied from [`examples/tutorial/app/handlers/issues.go`](../../../../examples/tutorial/app/handlers/issues.go), region `imports`.)

Replace `Index` with the list:

```go
// IssueList is the input of GET /issues: the page number, from ?page=.
type IssueList struct {
	Page int `query:"page"`
}

// Index lists the issues, newest first, 20 a page.
func (Issues) Index(c *web.Ctx, in IssueList) (web.Responder, error) {
	page, err := db.Query[models.Issue](c).
		With(models.IssueRels.Author).
		OrderBy(models.IssueCols.ID.Desc()).
		Paginate(in.Page, 20)
	if err != nil {
		return nil, err
	}
	return web.View(views.IssuesPage(page)), nil
}
```

(Copied from [`examples/tutorial/app/handlers/issues.go`](../../../../examples/tutorial/app/handlers/issues.go), region `index`.)

`web.H` (in the routes below) turns a function of an input struct into a
handler: it fills `IssueList` from the request (`query:"page"`), and
renders what the function returns. `Paginate` counts the issues and
returns one page, with `With` loading their authors in one more query.

Below it, opening an issue:

```go
// IssueInput is the issue form. Its validate tags are checked before the
// handler runs: an invalid form goes back with its errors.
type IssueInput struct {
	Title string `json:"title" validate:"required|max:200"`
	Body  string `json:"body" validate:"max:10000"`
}

// New shows the form for a new issue.
func (Issues) New(c *web.Ctx) error {
	return c.Render(http.StatusOK, views.IssueForm(models.Issue{}))
}

// Create opens an issue by the signed-in user.
func (Issues) Create(c *web.Ctx, in IssueInput) (web.Responder, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return nil, err
	}
	issue := models.Issue{Title: in.Title, Body: in.Body, Status: "open", AuthorID: u.ID}
	if err := db.Create(c, &issue); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Issue opened.")
	return web.RedirectRoute("issues.index"), nil
}
```

(Copied from [`examples/tutorial/app/handlers/issues.go`](../../../../examples/tutorial/app/handlers/issues.go), region `create`.)

The form's fields fill `IssueInput` by their `json` names, and its
`validate` tags are checked first: when they fail, the browser goes back
to the form with the messages and what was typed, and `Create` doesn't
run. `auth.Current` is the signed-in user.

And editing one, which only its author may:

```go
// IssueID reads {id} from the path.
type IssueID struct {
	ID int64 `path:"id"`
}

// UpdateIssue is the input of PUT /issues/{id}: the path and the form.
type UpdateIssue struct {
	IssueID
	IssueInput
}

// own returns the issue if the signed-in user wrote it: others get a
// 403, and an issue that doesn't exist is a 404 (db.ErrNotFound).
func own(c *web.Ctx, id int64) (models.Issue, error) {
	issue, err := db.Find[models.Issue](c, id)
	if err != nil {
		return issue, err
	}
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return issue, err
	}
	if issue.AuthorID != u.ID {
		return issue, web.Error(http.StatusForbidden, "Only the issue's author can change it.")
	}
	return issue, nil
}

// Edit shows the form to edit an issue.
func (Issues) Edit(c *web.Ctx, in IssueID) (web.Responder, error) {
	issue, err := own(c, in.ID)
	if err != nil {
		return nil, err
	}
	return web.View(views.IssueForm(issue)), nil
}

// Update saves the issue.
func (Issues) Update(c *web.Ctx, in UpdateIssue) (web.Responder, error) {
	issue, err := own(c, in.ID)
	if err != nil {
		return nil, err
	}
	issue.Title, issue.Body = in.Title, in.Body
	if err := db.Update(c, &issue); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Issue saved.")
	return web.RedirectRoute("issues.index"), nil
}
```

(Copied from [`examples/tutorial/app/handlers/issues.go`](../../../../examples/tutorial/app/handlers/issues.go), region `edit`.)

`db.Find` returns `db.ErrNotFound` for an issue that doesn't exist, which
becomes a 404; `web.Error` makes a 403.

## The pages

Create `views/issues.templ`, in package `views`, with these imports:

```templ
import (
	"fmt"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/web"

	"tracker/app/models"
)
```

(Copied from [`examples/tutorial/views/issues.templ`](../../../../examples/tutorial/views/issues.templ), region `imports`.)

The list:

```templ
// IssuesPage lists a page of issues, with links to the pages around it.
templ IssuesPage(page db.Page[models.Issue]) {
	@Layout("Issues") {
		<h1>Issues</h1>
		<p><a href={ web.URL(ctx, "issues.new") }>New issue</a></p>
		if page.Total == 0 {
			<p>No issues yet.</p>
		}
		<ul class="issues">
			for _, issue := range page.Data {
				<li>
					<span class={ "status", issue.Status }>{ issue.Status }</span>
					<a href={ templ.URL(fmt.Sprintf("/issues/%d", issue.ID)) }>{ issue.Title }</a>
					<small>#{ issue.ID } by { issue.Author.Name }</small>
				</li>
			}
		</ul>
		<nav>
			if page.HasPrev() {
				<a href={ web.PageURL(ctx, page.CurrentPage-1) }>Newer</a>
			}
			if page.HasMore() {
				<a href={ web.PageURL(ctx, page.CurrentPage+1) }>Older</a>
			}
		</nav>
	}
}
```

(Copied from [`examples/tutorial/views/issues.templ`](../../../../examples/tutorial/views/issues.templ), region `list`.)

The titles link to each issue's page, which you add in part 4. The form,
for new issues and edits:

```templ
// IssueForm opens an issue, or edits one when issue.ID is set. After a
// failed post, view.Old refills the fields and view.Errors has the
// messages.
templ IssueForm(issue models.Issue) {
	@Layout("Issue") {
		if issue.ID == 0 {
			<h1>New issue</h1>
			<form method="post" action={ web.URL(ctx, "issues.store") }>
				@issueFields(issue)
			</form>
		} else {
			<h1>Edit #{ issue.ID }</h1>
			<form method="post" action={ web.URL(ctx, "issues.update", issue.ID) }>
				@view.MethodField("PUT")
				@issueFields(issue)
			</form>
		}
	}
}

templ issueFields(issue models.Issue) {
	@view.CSRFField(ctx)
	<label for="title">Title</label>
	<input id="title" name="title" value={ view.Old(ctx, "title", issue.Title) }/>
	@fieldError("title")
	<label for="body">Description</label>
	<textarea id="body" name="body" rows="6">{ view.Old(ctx, "body", issue.Body) }</textarea>
	@fieldError("body")
	<p><button type="submit">Save</button></p>
}

// fieldError shows the message of a field that failed validation.
templ fieldError(field string) {
	if msg := view.Errors(ctx).Get(field); msg != "" {
		<p class="error">{ msg }</p>
	}
}
```

(Copied from [`examples/tutorial/views/issues.templ`](../../../../examples/tutorial/views/issues.templ), region `form`.)

`view.CSRFField` adds the token that protects forms from other sites;
HTML forms can't send PUT, so `view.MethodField` says it.

## The routes

The pages are for signed-in users: in `routes/auth.go`, add the routes
at the end of the `members` group, after the `/confirm-password` ones:

```go
// The tracker's pages, for signed-in users.
var issues handlers.Issues
members.Get("/issues", web.H(issues.Index)).Name("issues.index")
members.Get("/issues/new", issues.New).Name("issues.new")
members.Post("/issues", web.H(issues.Create)).Name("issues.store")
members.Get("/issues/{id}/edit", web.H(issues.Edit)).Name("issues.edit")
members.Put("/issues/{id}", web.H(issues.Update)).Name("issues.update")
```

(Copied from [`examples/tutorial/routes/auth.go`](../../../../examples/tutorial/routes/auth.go), region `routes-issues`.)

Each route has a name, which `web.URL` and `web.RedirectRoute` use, so
URLs are written once. Guests who open these pages go to the login page.

## Try it

Open http://localhost:8080/issues. Open an issue with an empty title
(the form says what's wrong), then with one. Edit it.

## Test it

Tests need users. In `database/factories/factories.go`, add these
imports below the package line:

```go
import (
	"fmt"

	"anetos.dev/anetos/db/factory"

	"tracker/app/models"
)
```

(Copied from [`examples/tutorial/database/factories/factories.go`](../../../../examples/tutorial/database/factories/factories.go), region `imports`.)

and a factory, which makes valid models:

```go
// Users are users named User 1, User 2…, with addresses to match.
var Users = factory.New(func(n int) models.User {
	return models.User{Name: fmt.Sprintf("User %d", n), Email: fmt.Sprintf("user%d@example.com", n)}
})
```

(Copied from [`examples/tutorial/database/factories/factories.go`](../../../../examples/tutorial/database/factories/factories.go), region `users`.)

Then create `issues_test.go` next to `main.go`, in package `main`:

```go
import (
	"net/url"
	"testing"

	"anetos.dev/anetos/anetostest"

	"tracker/app/models"
	"tracker/database/factories"
)
```

(Copied from [`examples/tutorial/issues_test.go`](../../../../examples/tutorial/issues_test.go), region `imports`.)

```go
func TestIssues(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, factories.Users)
	anetostest.ActingAs(app, &ada) // signed in for the requests that follow

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
```

(Copied from [`examples/tutorial/issues_test.go`](../../../../examples/tutorial/issues_test.go), region `test`.)

`anetostest.New` boots the app with an in-memory database, migrated, and
`ActingAs` signs a user in. The requests go through the whole app,
middleware and CSRF protection included, as a browser's would.

```sh
go test ./...
```

The [testing guide](../../guides/testing.md) has the other assertions.

Next: [4. The issue page](04-the-issue-page.md).
