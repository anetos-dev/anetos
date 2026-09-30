---
title: Getting started
since: v0.1.0
---

# Getting started

Create a project, run it with live reload, and add a model, a migration
and a page. About fifteen minutes.

## Before you start

Go 1.26 or later. Install the `anetos` tool:

```sh
go install anetos.dev/anetos/cli/cmd/anetos@latest
```

## Steps

### 1. Create the project

```sh
anetos new blog
cd blog
```

`anetos new` writes the project (routes, a handler, templ views, sessions
and CSRF protection, migrations, static files with htmx, a test) and a
`.env` with a fresh `APP_KEY`, then downloads the dependencies. SQLite is
the default database; `--db=postgres` or `--db=mysql` picks another (create
the database and set the `DB_*` settings in `.env` before migrating). The
[tool reference](../reference/cli.md#anetos-new-directory) lists every
file.

### 2. Run it

```sh
go run . migrate        # create the database (database/app.db)
go tool anetos dev      # http://localhost:8080
```

`anetos dev` rebuilds and restarts the app when you save a file and
reloads the page in your browser. Edit `views/home.templ` to see it.

### 3. Add a model and its table

```sh
go tool anetos make:model Post --migration
```

This writes `app/models/post.go`, its typed columns in
`app/models/models_gen.go`, and a migration in `database/migrations`.
Add fields to the model and columns to the migration:

```go
// illustrative
type Post struct {
	db.Model
	Title string `db:"title" json:"title"`
	Body  string `db:"body" json:"body"`
}
```

```go
// illustrative
return s.Create("posts", func(t *migrate.Table) {
	t.ID()
	t.String("title", 200)
	t.Text("body")
	t.Timestamps()
})
```

Then regenerate the typed columns (`anetos dev` does it on save) and
create the table:

```sh
go tool anetos gen ./app/models   # PostCols.Title, PostCols.Body, …
go run . migrate
```

See [Models](../guides/models.md) and [Migrations](../guides/migrations.md).

### 4. Add a page

```sh
go tool anetos make:handler Posts
```

Make `Index` list the posts with a view (`views/posts.templ`), and route
it in `routes/web.go`:

```go
// illustrative
func (h Posts) Index(c *web.Ctx) error {
	posts, err := db.Query[models.Post](c).OrderBy(models.PostCols.CreatedAt.Desc()).Get()
	if err != nil {
		return err
	}
	return c.Render(http.StatusOK, views.PostsIndex(posts))
}
```

```templ
// illustrative (views/posts.templ)
templ PostsIndex(posts []models.Post) {
	@Layout("Posts") {
		<h1>Posts</h1>
		for _, p := range posts {
			<article><h2>{ p.Title }</h2><p>{ p.Body }</p></article>
		}
	}
}
```

```go
// illustrative
posts := handlers.Posts{}
pages.Get("/posts", posts.Index).Name("posts.index")
```

Pages of posts (`Paginate` and `web.PageURL`) are in
[Query the database](../guides/queries.md#3-paginate).

[Render HTML with templ](../guides/views.md) and
[Handle HTML forms](../guides/forms.md) take it from there.

### 5. Test and build

```sh
go test ./...
go build -o bin/blog .
./bin/blog              # runs the app; ./bin/blog help lists the commands
```

`main_test.go` requests the home page with
[`anetostest`](../guides/testing.md), which boots the app with an
in-memory database, like a browser would. Add a test for each page and
form as you go.

The binary embeds the views and static files. Set `APP_ENV=production`
and the `APP_KEY` from your secrets in production, and run
`./bin/blog migrate` before starting the new version.

## Next steps

- [Commands](../guides/commands.md)
- [Configuration](../guides/configuration.md)
- [`anetos` tool and app commands reference](../reference/cli.md)
