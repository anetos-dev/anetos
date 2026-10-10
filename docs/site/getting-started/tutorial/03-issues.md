---
title: "3. Issues"
since: v0.3.0
weight: 3
---

# 3. Issues

You add the issues: a model and its table, pages to list, open and edit
them, and a test.

> **Tip:** `go tool anetos make:crud Issue title:string body:text`
> writes a model, its table, and pages to list, show, create, edit and
> delete its rows, in one command ([Add pages for a
> model](../crud.md)). Here you write them by hand, to see each piece
> and make issues your own: they have an author and a status.

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

// Create opens an issue by the logged-in user.
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
run. `auth.Current` is the logged-in user.

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

// own returns the issue if the logged-in user wrote it: others get a
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
	"tracker/views/ui"
)
```

(Copied from [`examples/tutorial/views/issues.templ`](../../../../examples/tutorial/views/issues.templ), region `imports`.)

The list:

```templ
// IssuesPage lists a page of issues, with links to the pages around it.
templ IssuesPage(page db.Page[models.Issue]) {
	@Layout("Issues") {
		@ui.PageHeader("Issues", "") {
			@ui.LinkButton(web.MustURL(ctx, "issues.new"), ui.Primary) {
				New issue
			}
		}
		if page.Total == 0 {
			@ui.Empty("No issues yet.")
		} else {
			@ui.Table() {
				<tbody>
					for _, issue := range page.Data {
						<tr>
							<td>
								@statusBadge(issue.Status)
							</td>
							<td><a href={ templ.URL(fmt.Sprintf("/issues/%d", issue.ID)) }>{ issue.Title }</a></td>
							<td><small>#{ issue.ID } by { issue.Author.Name }</small></td>
						</tr>
					}
				</tbody>
			}
		}
		if page.LastPage > 1 {
			@ui.Pagination(ui.PagesOf(ctx, page, "Pages", "", "Newer", "Older"))
		}
	}
}

// statusBadge shows an issue's status: green while it's open.
templ statusBadge(status string) {
	if status == "open" {
		@ui.Badge(ui.Success) {
			{ status }
		}
	} else {
		@ui.Badge(ui.Neutral) {
			{ status }
		}
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
		@ui.Narrow() {
			if issue.ID == 0 {
				<h1>New issue</h1>
				@ui.Card("") {
					@ui.Form(web.MustURL(ctx, "issues.store"), "POST", nil) {
						@issueFields(issue)
					}
				}
			} else {
				<h1>Edit #{ issue.ID }</h1>
				@ui.Card("") {
					@ui.Form(web.MustURL(ctx, "issues.update", issue.ID), "PUT", nil) {
						@issueFields(issue)
					}
				}
			}
		}
	}
}

templ issueFields(issue models.Issue) {
	@ui.Field("title", "Title", "") {
		@ui.Input("title", "", view.Old(ctx, "title", issue.Title), nil)
	}
	@ui.Field("body", "Description", "") {
		@ui.Textarea("body", view.Old(ctx, "body", issue.Body), templ.Attributes{"rows": "6"})
	}
	@ui.Button(ui.Primary, nil) {
		Save
	}
}
```

(Copied from [`examples/tutorial/views/issues.templ`](../../../../examples/tutorial/views/issues.templ), region `form`.)

The pages are made of the components in `views/ui`, which `anetos new`
wrote: `ui.PageHeader`, `ui.Table`, `ui.Badge`, `ui.Card`, `ui.Field`…
They carry the markup and the starter theme's classes (in
`public/static/app.css`), so the pages need none, and another design
kit restyles them ([Style your app](../../guides/styling.md)).
`ui.Form` adds the token that protects forms from other sites, and,
since HTML forms can't send PUT, says the method in a field.
`ui.Field` shows the field's message when validation fails, and
`view.Old` puts back what was typed.

## The routes

The pages are for logged-in users: in `routes/auth.go`, add the routes
at the end of the `members` group, after the `/confirm-password` ones:

```go
// The tracker's pages, for logged-in users.
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

Link to the list from the header: in `views/layout.templ`, add a line
to the nav, below the home page's:

```templ
@navLink("issues.index", "Issues")
```

(Copied from [`examples/tutorial/views/layout.templ`](../../../../examples/tutorial/views/layout.templ), region `nav-issues`.)

`navLink` (in the layout) marks the link as the current page on the
issue pages, whose route names start with `issues.`.

## Try it

Open http://localhost:8080/issues. Open an issue with an empty title
(the form says what's wrong), then with one. Edit it.

> **Tip:** Logging in leads to `/dashboard`. To land on the issues
> instead, add `AUTH_HOME_URL=/issues` to `.env`, or change the default
> in `auth.go`: `auth.DefaultHomeURL("/issues")`.

## Test it

Tests need users: `make:auth` wrote a factory for them,
`factories.Users` in `database/factories/users.go`, which makes
verified users (`User 1`, `user1@example.com`…).

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
```

(Copied from [`examples/tutorial/issues_test.go`](../../../../examples/tutorial/issues_test.go), region `test`.)

`anetostest.New` boots the app with an in-memory database, migrated, and
`ActingAs` logs a user in. The requests go through the whole app,
middleware and CSRF protection included, as a browser's would.

```sh
go test ./...
```

The [testing guide](../../guides/testing.md) has the other assertions.

Next: [4. The issue page](04-the-issue-page.md).
