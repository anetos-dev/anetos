---
title: Add full-text search
since: v0.3.0
group: "Features"
weight: 502
---

# Add full-text search

Give users a search box that finds what they mean: every word they type,
in any of the columns you choose, best matches first. Search runs on
your database's own full-text engine (PostgreSQL, MySQL/MariaDB or
SQLite), with an index built by a migration; there's no search server to
run. The complete example is [`examples/forms`](../../../examples/forms),
a notes app with a search box.

## Before you start

You have an app with a database (`db.Connect`) and migrations
(`migrate.New`), as `anetos new` makes it. The columns to search hold
text.

## Steps

### 1. Add a search index

In a migration, list the columns to search, most important first:
matches in earlier columns weigh more.

```go
Migrations.AddFunc("2026_10_02_120000_add_search_to_notes",
	func(s *migrate.Schema) error {
		return s.Alter("notes", func(t *migrate.Table) {
			t.SearchIndex("title", "body") // titles weigh more
		})
	},
	func(s *migrate.Schema) error {
		return s.Alter("notes", func(t *migrate.Table) { t.DropSearchIndex() })
	})
```

(Copied from [`examples/forms/models.go`](../../../examples/forms/models.go), region `search-index`.)

`t.SearchIndex` works in `Create` too. In `Alter`, the rows already in
the table are indexed. Run it:

```bash
go run . migrate
```

### 2. Search

`Search` keeps the rows that match every word of the text and orders them
by relevance, best first:

```go
func (Notes) Index(c *web.Ctx, in ListNotes) (web.Responder, error) {
	// Newest first; the ID breaks ties between notes created in the same
	// instant, so no note shows on two pages. With search words, the
	// best matches come first, then the newest. Search of no words
	// changes nothing.
	page, err := db.Query[Note](c).OrderBy(NoteCols.CreatedAt.Desc(), NoteCols.ID.Desc()).
		Search(in.Q).
		Paginate(in.Page, 10)
	if err != nil {
		return nil, err
	}
	return web.View(NotesPage(page, in.Q)), nil
}
```

(Copied from [`examples/forms/main.go`](../../../examples/forms/main.go), region `index`.)

Each word of three letters or more matches as a prefix ("tea" finds
"teas", "gree tea" finds "green tea"), in any of the indexed columns;
shorter words match whole words only ("go" finds "go", not "golang"),
and only the first ten words count (v0.3: so that a search box can't be
made to scan most of the index). Punctuation is ignored, and
text without words (an empty search box) leaves the query as it was.
`Search` combines with `Where`, soft deletes, `Paginate`, `Count`,
`Pluck` and the aggregates. Its ranking comes first; `OrderBy` terms,
before or after it, break ties. A `Distinct` or `GroupBy` query still
searches, but has no relevance order (its rows aren't the rows the
ranking scores), only its `OrderBy`.

### 3. Add the search box

A `GET` form with a `q` field is enough; the page links keep `q`
(`web.PageURL` keeps the query string):

```templ
// NotesPage lists one page of notes, with links to the pages around it
// (they keep the search words, q) and a search box.
templ NotesPage(page db.Page[Note], q string) {
	@Layout("All notes") {
		<h1>Notes</h1>
		<p><a href={ web.URL(ctx, "notes.new") }>New note</a></p>
		<form method="get" action={ web.URL(ctx, "notes.index") } role="search">
			<input type="search" name="q" value={ q } aria-label="Search notes"/>
			<button type="submit">Search</button>
		</form>
		if page.Total == 0 && q != "" {
			<p>No notes match “{ q }”.</p>
		} else if page.Total == 0 {
			<p>No notes yet.</p>
		}
		<ul class="notes">
			for _, n := range page.Data {
				@noteItem(n)
			}
		</ul>
		<nav class="pages">
			if page.HasPrev() {
				<a href={ web.PageURL(ctx, min(page.CurrentPage-1, page.LastPage)) } rel="prev">Newer</a>
			}
			if page.LastPage > 1 {
				<span>Page { page.CurrentPage } of { page.LastPage }</span>
			}
			if page.HasMore() {
				<a href={ web.PageURL(ctx, page.CurrentPage+1) } rel="next">Older</a>
			}
		</nav>
	}
}
```

(Copied from [`examples/forms/notes.templ`](../../../examples/forms/notes.templ), region `list`.)

The notes app writes its own markup. In a project made with
`anetos new`, build the box from the components of `views/ui`
(`ui.Form` with `"GET"`, `ui.Input`, `ui.Button`), as the
[tutorial's search page](../getting-started/tutorial/05-search.md) does,
or as [below](#search-a-list-made-with-makecrud).

Bound the input (`validate:"max:200"` on `Q`): a search box shouldn't
accept a novel.

### 4. Test it

```go
func TestSearch(t *testing.T) {
	app := anetostest.New(t, setup)
	anetostest.Create(app, NoteFactory.With(func(n *Note) { n.Title, n.Body = "Groceries", "Rice, lentils and green tea" }))
	anetostest.Create(app, NoteFactory.With(func(n *Note) { n.Title, n.Body = "Tea tasting", "Darjeeling first flush" }))
	anetostest.Create(app, NoteFactory.With(func(n *Note) { n.Title, n.Body = "Ideas", "A blog about Go" }))

	// Every word must match, as a prefix from 3 letters; title matches
	// rank first.
	app.Get("/notes?q=tea").AssertOK().
		AssertSee("<strong>Tea tasting</strong>", "<strong>Groceries</strong>", `value="tea"`).
		AssertDontSee("<strong>Ideas</strong>")
	if page := app.Get("/notes?q=tea").Text(); strings.Index(page, "Tea tasting") > strings.Index(page, "Groceries") {
		t.Error("the title match isn't first")
	}
	app.Get("/notes?q=green+lent").AssertSee("<strong>Groceries</strong>").AssertDontSee("<strong>Tea tasting</strong>")
	app.Get("/notes?q=coffee").AssertSee("No notes match “coffee”.")
}
```

(Copied from [`examples/forms/main_test.go`](../../../examples/forms/main_test.go), region `test-search`.)

Tests use an in-memory SQLite database by default. On MySQL and MariaDB,
full-text indexes only see committed rows, so a search test there needs
`anetostest.WithoutTransaction()`: its rows are committed, so the test
deletes them itself:

```go
// illustrative
app := anetostest.New(t, setup, anetostest.WithoutTransaction())
t.Cleanup(func() {
	if _, err := db.Query[models.Post](app.Context()).ForceDelete(); err != nil {
		t.Error(err)
	}
})
```

### Search a list made with make:crud

The pages `make:crud` writes take a search box in three changes, with
the components of `views/ui` and with their text in the locale file.
For `posts`, after the migration of step 1:

1. The list's input takes the words, in `app/handlers/posts.go`, and the
   query searches them:

   ```go
   // illustrative
   type PostList struct {
   	Page int    `query:"page"`
   	Q    string `query:"q" validate:"max:200"`
   }

   page, err := db.Query[models.Post](c).OrderBy(models.PostCols.ID.Desc()).
   	Search(in.Q).
   	Paginate(in.Page, 20)
   if err != nil {
   	return nil, err
   }
   return web.View(views.PostsPage(page, in.Q)), nil
   ```

2. The page shows the box under its header, in `views/posts.templ`
   (`PostsPage(page db.Page[models.Post], q string)`), and says when
   nothing matches. In place of the `if page.Total == 0 {` line and its
   `ui.Empty`:

   ```templ
   // illustrative
   @ui.Form(web.MustURL(ctx, "posts.index"), "GET", templ.Attributes{"role": "search"}) {
   	@ui.Cluster() {
   		@ui.Input("q", "search", q, templ.Attributes{"aria-label": i18n.T(ctx, "posts.search")})
   		@ui.Button(ui.Secondary, nil) {
   			{ i18n.T(ctx, "posts.search") }
   		}
   	}
   }
   if page.Total == 0 && q != "" {
   	@ui.Empty(i18n.T(ctx, "posts.no_match", "q", q))
   } else if page.Total == 0 {
   	@ui.Empty(i18n.T(ctx, "posts.empty"))
   } else {
   ```

   The `else` block, the table and the page links, stays as it was.

3. Its text goes in `locales/en/posts.yaml`, under `posts:`:

   ```yaml
   search: "Search"
   no_match: "No posts match “{q}”."
   ```

The page links keep `q` (`web.PageURL` keeps the query string).

## Choose the settings

Two settings, in `.env`, decide how every search index is built and
queried:

| Setting | Values | Default |
|---|---|---|
| `SEARCH_LANGUAGE` | `simple`: words as written, in any language. `english` (PostgreSQL, SQLite): also their other forms ("runs" finds "running"). On PostgreSQL, any text search configuration: `german`, `french`, `spanish`… | `simple` |
| `SEARCH_RANKING` | `default`: the database's own ranking. `bm25`: BM25, which weighs rare words more and long texts less | `default` |

`simple` with prefix matching is the default because it's right for
every language (Bangla, Hindi and mixed content included) and behaves
the same on every database; pick a language when all your content is in
it. Changing either setting needs the indexes rebuilt:

```bash
go run . search:reindex          # all of them; or name the tables
```

### What each database can do

| | PostgreSQL | SQLite | MySQL, MariaDB |
|---|---|---|---|
| `SEARCH_LANGUAGE` | `simple`, `english` and the server's other text search configurations | `simple`, `english` | `simple` only |
| `SEARCH_RANKING=bm25` | PostgreSQL 17+ with the [pg_textsearch](https://github.com/timescale/pg_textsearch) extension (in `shared_preload_libraries`, then `CREATE EXTENSION pg_textsearch`) | built in | not available |
| Column weights | yes | yes | no: columns count alike |
| Common words ("the", "and") | ignored with a language other than `simple`; a search of only those finds nothing | searched | not indexed (stop words), like words shorter than 3 characters (`innodb_ft_min_token_size`): they only find longer words they start, and are optional next to other words |
| Accents ("cafe" finds "Café") | no | yes | yes, with the default accent-insensitive collation |
| Scripts with combining marks (Bangla, Hindi…) | yes | yes | yes |

These are checked when the app starts: a setting the database can't
serve stops the app with a message saying which setting, which database,
and what to do:

```text
blog serve: anetos: provider "db.Connect(mysql)": boot: db: SEARCH_RANKING=bm25 needs BM25 ranking, which this MySQL/MariaDB database doesn't have: MySQL and MariaDB don't have it; use SQLite, or PostgreSQL with pg_textsearch
blog serve: anetos: provider "db.Connect(sqlite)": boot: db: the search indexes of notes (built for SEARCH_LANGUAGE=simple, SEARCH_RANKING=default) don't match SEARCH_LANGUAGE=english, SEARCH_RANKING=default: rebuild them with the search:reindex command, or set the settings back
```

`migrate` and `search:reindex` still run when only the indexes are out of
date (they're how you fix it). Your own features can declare what they
need from the database the same way: `d.Require("my feature", db.BM25)`,
checked at boot.

## How it works

`t.SearchIndex` builds the database's own index, and records it (columns,
language, ranking) in the `search_indexes` table:

- **PostgreSQL**: a generated `search_vector` column (`tsvector`, the
  columns weighted A, B, C, D) with a GIN index. `Search` matches
  `search_vector @@ to_tsquery(…)` and ranks with `ts_rank_cd`; with BM25,
  a generated `search_text` column with a `bm25` index ranks first.
- **MySQL, MariaDB**: a generated `search_text` column with a `FULLTEXT`
  index, matched in boolean mode with every word required. Words the
  index skips (short words, stop words) are optional prefixes instead,
  or required ones when the search has no other words.
- **SQLite**: an FTS5 table, `<table>_search`, kept in sync by triggers
  on the table, ranked with its `bm25()`.

The generated columns are the database's, not your model's: your structs
don't change. Search words are reduced to letters, digits and marks
before they reach the database, and travel as parameters.

The index follows inserts, updates and deletes in the same transaction
(on MySQL, at commit). Database search serves most apps: it is exact,
needs no extra service, and stays consistent with the data. For typo
tolerance, facets or very large collections, a search engine
(Meilisearch, Typesense, OpenSearch) is the next step; it's on the
roadmap behind the same `Search` method.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `… (does notes have a search index? …)` | No `t.SearchIndex` for the model's table, or the migration hasn't run | Add it in a migration; `go run . migrate` |
| The app doesn't start: `the search indexes of … don't match` | `SEARCH_LANGUAGE` or `SEARCH_RANKING` changed since the indexes were built | `go run . search:reindex` |
| The app doesn't start: `SEARCH_LANGUAGE=english isn't available on MySQL/MariaDB` | MySQL can't stem words | `SEARCH_LANGUAGE=simple`, or PostgreSQL or SQLite |
| The app doesn't start: `needs BM25 ranking` | The database has no BM25 | `SEARCH_RANKING=default`, or install pg_textsearch on PostgreSQL 17+ |
| "Go" or "the" doesn't find "Go" or "the" on MySQL | MySQL doesn't index words shorter than 3 characters, or stop words; they only find longer words they start | Expected; lower `innodb_ft_min_token_size` on the server and rebuild, or use PostgreSQL or SQLite |
| Results of a `Distinct` or `GroupBy` search aren't best first | Those queries have no relevance order | Order them with `OrderBy`, or drop `Distinct` |
| `CursorPaginate can't page through Search results` | Results are ordered by relevance, which has no cursor | Use `Paginate` |
| `Rename … has a search index` | Renaming a searchable table | `DropSearchIndex`, rename, `SearchIndex` again |
| `… is in the search index` when dropping or renaming a column | The index is built from it | In the same `Alter`: `DropSearchIndex`, the change, then `SearchIndex` with the new columns |
| `search:reindex`: `its indexed column … is gone` | The column was dropped outside a migration's `Alter` | `DropSearchIndex` and `SearchIndex` again in a migration |

## Next steps

- [Query data](queries.md): conditions, ordering and pagination that
  combine with `Search`.
- [Search by meaning](semantic-search.md): embeddings, and hybrid search
  that combines them with this index.
- [Migrations reference](../reference/migrations.md#search-indexes): the
  objects each database gets.
- [Configuration reference](../reference/configuration.md#search):
  `SEARCH_LANGUAGE`, `SEARCH_RANKING`.

> **Coming from Laravel?** This is Scout's database engine without the
> `Searchable` trait: the migration declares what's searchable, and the
> database's full-text index (not `LIKE`) does the matching and ranking.
> Scout's search engines (Meilisearch, Algolia, Typesense) are on the
> roadmap as drivers behind the same `Search`.
