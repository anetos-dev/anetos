---
title: "5. Search"
since: v0.3.0
weight: 5
---

# 5. Search

You add a search box that finds issues by the words in their title and
description, best matches first, from a full-text index in the database.

## The index

```sh
go tool anetos make:migration add_search_to_issues_table
```

Fill in its two functions:

```go
func(s *migrate.Schema) error {
	return s.Alter("issues", func(t *migrate.Table) {
		t.SearchIndex("title", "body") // titles weigh more
	})
},
func(s *migrate.Schema) error {
	return s.Alter("issues", func(t *migrate.Table) {
		t.DropSearchIndex()
	})
},
```

(Copied from [`examples/tutorial/database/migrations/2026_10_06_142847_add_search_to_issues_table.go`](../../../../examples/tutorial/database/migrations/2026_10_06_142847_add_search_to_issues_table.go), region `migration`.)

```sh
go run . migrate
```

`SearchIndex` builds what each database has: an FTS5 table on SQLite, a
`tsvector` column on PostgreSQL, a `FULLTEXT` index on MySQL. The index
follows the table's changes by itself.

## The page

Create `app/handlers/search.go`, in package `handlers`:

```go
import (
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"

	"tracker/app/models"
	"tracker/views"
)
```

(Copied from [`examples/tutorial/app/handlers/search.go`](../../../../examples/tutorial/app/handlers/search.go), region `imports`.)

```go
// SearchInput is the search box: GET /search?q=words.
type SearchInput struct {
	Q string `query:"q" validate:"max:200"`
}

// Search finds the issues whose title or description has the words,
// best matches first.
func (Issues) Search(c *web.Ctx, in SearchInput) (web.Responder, error) {
	var found []models.Issue
	if in.Q != "" {
		var err error
		found, err = db.Query[models.Issue](c).With(models.IssueRels.Author).Search(in.Q).Limit(50).Get()
		if err != nil {
			return nil, err
		}
	}
	return web.View(views.SearchPage(in.Q, found)), nil
}
```

(Copied from [`examples/tutorial/app/handlers/search.go`](../../../../examples/tutorial/app/handlers/search.go), region `search`.)

`Search` keeps the rows that have every word (as a word or the start of
one: `pay` finds `payment`), and orders them by how well they match.

And `views/search.templ`, in package `views`:

```templ
import (
	"fmt"

	"tracker/app/models"
)
```

(Copied from [`examples/tutorial/views/search.templ`](../../../../examples/tutorial/views/search.templ), region `imports`.)

```templ
// SearchPage shows the issues that match q.
templ SearchPage(q string, found []models.Issue) {
	@Layout("Search") {
		<h1>Search</h1>
		<form method="get" role="search" class="cluster">
			<input type="search" name="q" value={ q } aria-label="Search issues"/>
			<button type="submit">Search</button>
		</form>
		if q != "" && len(found) == 0 {
			<p class="empty">No issues match “{ q }”.</p>
		}
		if len(found) > 0 {
			<div class="table-wrap">
				<table>
					<tbody>
						for _, issue := range found {
							<tr>
								<td><span class={ "badge", templ.KV("success", issue.Status == "open") }>{ issue.Status }</span></td>
								<td><a href={ templ.URL(fmt.Sprintf("/issues/%d", issue.ID)) }>{ issue.Title }</a></td>
								<td class="muted">#{ issue.ID } by { issue.Author.Name }</td>
							</tr>
						}
					</tbody>
				</table>
			</div>
		}
	}
}
```

(Copied from [`examples/tutorial/views/search.templ`](../../../../examples/tutorial/views/search.templ), region `page`.)

The route, in `routes/auth.go` below the others:

```go
members.Get("/search", web.H(issues.Search)).Name("search")
```

(Copied from [`examples/tutorial/routes/auth.go`](../../../../examples/tutorial/routes/auth.go), region `routes-search`.)

Last, a search box on every page. In `views/layout.templ`, add it to
the header, below the nav's closing `</nav>`:

```templ
<form method="get" action={ web.URL(ctx, "search") } role="search">
	<input type="search" name="q" placeholder="Search issues" aria-label="Search issues"/>
</form>
```

(Copied from [`examples/tutorial/views/layout.templ`](../../../../examples/tutorial/views/layout.templ), region `nav-search`.)

## Test it

`search_test.go`, in package `main`:

```go
import (
	"testing"

	"anetos.dev/anetos/anetostest"

	"tracker/app/models"
	"tracker/database/factories"
)
```

(Copied from [`examples/tutorial/search_test.go`](../../../../examples/tutorial/search_test.go), region `imports`.)

```go
func TestSearch(t *testing.T) {
	app := anetostest.New(t, setup)
	ada := anetostest.Create(app, factories.Users)
	issues := factories.Issues.With(func(i *models.Issue) { i.AuthorID = ada.ID })
	anetostest.Create(app, issues.With(func(i *models.Issue) { i.Title, i.Body = "Checkout fails", "The payment form spins." }))
	anetostest.Create(app, issues.With(func(i *models.Issue) { i.Title = "Typo in the footer" }))
	anetostest.ActingAs(app, &ada)

	app.Get("/search?q=payment").AssertOK().AssertSee("Checkout fails").AssertDontSee("Typo in the footer")
	app.Get("/search?q=zebra").AssertSee("No issues match “zebra”.")
}
```

(Copied from [`examples/tutorial/search_test.go`](../../../../examples/tutorial/search_test.go), region `test`.)

> **Note:** On MySQL and MariaDB, full-text indexes only see committed
> rows, and each test runs in a transaction: a search test there needs
> `anetostest.WithoutTransaction()`. The tutorial's SQLite has no such
> limit.

The [search guide](../../guides/search.md) covers languages, ranking and
the other databases.

Next: [6. Email the author](06-email-the-author.md).
