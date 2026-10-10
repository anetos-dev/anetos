---
title: Add pages for a model
since: v0.3.0
group: "Your first app"
weight: 22
---

# Add pages for a model

`make:crud` writes a model with its table, and the pages to list, show,
create, edit and delete its rows, made of your app's UI components, with
their routes and a test. You get working pages in a minute, and code
you can read and change.

## Generate them

```sh
go tool anetos make:crud Post title:string body:text published:bool
go run . migrate
```

Open http://localhost:8080/posts (the header links to it): an empty
list with a "New post" button. Save the form empty to see the
validation messages, then fill it in.

Each field is `name:type`, then `:optional` or `:unique` if you like:

| Type | Go | Column | Input |
|---|---|---|---|
| `string` | `string` | `VARCHAR(255)` | a text box |
| `text` | `string` | `TEXT` | a text area |
| `email` | `string` | `VARCHAR(255)` | an email box, checked with the `email` rule |
| `int` | `int64` | `BIGINT` | a number box |
| `float` | `float64` | double | a number box with decimals |
| `bool` | `bool` | boolean | a checkbox |
| `date` | `anetos.Date` | `DATE` | a date picker |

Strings, texts, emails and dates are required; `:optional` lets them be
empty. `:unique`, for required strings and emails and for dates, adds a
unique index and the `unique` rule, so the form says "The email has
already been taken." rather than failing. (An optional string is empty,
not NULL, when left blank, and a blank number is 0: only one row could
have either, so `make:crud` refuses them; add such an index by hand.)

```sh
go tool anetos make:crud Customer name:string email:email:unique notes:text:optional since:date
```

## What it writes

For `Post`:

| File | Holds |
|---|---|
| `app/models/post.go` | `Post`, with a field per column; `anetos generate` adds `PostCols` to `models_gen.go` |
| `database/migrations/…_create_posts_table.go` | The `posts` table |
| `app/handlers/posts.go` | `Posts`: `Index` (20 a page, newest first), `Show`, `New`, `Create`, `Edit`, `Update`, `Delete`; `PostInput`, the form, with its `validate` tags |
| `views/posts.templ` | The list, a post's page, and the form for new and existing posts |
| `routes/posts.go` | `routes.Posts`: the seven routes, named `posts.index`, `posts.show`… |
| `locales/en/posts.yaml` | The pages' text: titles, buttons, the fields' labels, the messages |
| `posts_test.go` | A test that creates, shows, edits and deletes a post through the pages |

It also adds `Posts(pages)` to `routes/web.go` (the page group, with
sessions and CSRF protection) and a link to the list in the layout's
header. It never overwrites a file: if one exists, it writes nothing.
In a project made before v0.5, which has no `views/ui`, it writes the
components first, and says so ([Style your
app](../guides/styling.md#projects-made-before-v05)).

| Route | URL | Does |
|---|---|---|
| `posts.index` | `GET /posts` | The list, with pages (`?page=2`) |
| `posts.new` | `GET /posts/new` | The empty form |
| `posts.create` | `POST /posts` | Creates the post, then shows it |
| `posts.show` | `GET /posts/{id}` | The post, with Edit and Delete |
| `posts.edit` | `GET /posts/{id}/edit` | The filled form |
| `posts.update` | `PUT /posts/{id}` | Saves the changes |
| `posts.delete` | `DELETE /posts/{id}` | Deletes the post, then lists the others |

HTML forms send `PUT` and `DELETE` with a hidden `_method` field, which
the project's `web.MethodOverride` reads.

Each route is named after its handler (`h.Create` is `posts.create`).

> **Coming from Laravel?** Its resource routes call `create` the empty
> form and `store` the post that saves it, and say `destroy` for delete.
> Here the form is `new` and saving is `create`, as in Rails and
> Phoenix, and deleting is `delete`, as in Phoenix.

## Make it yours

The code is plain Anetos code: change it as you would your own.

- **Only for logged-in users.** After [`make:auth`](add-accounts.md),
  move the `Posts(pages)` call from `routes/web.go` to the `loggedIn`
  group of `routes/auth.go` (`Posts(loggedIn)`): guests then go to the
  login page. Log a user in at the start of `TestPosts`, as its comment
  shows (`make:auth` wrote `factories.Users`), and show the header's
  link to logged-in users only, in `views/layout.templ` (importing
  `anetos.dev/anetos/auth` and the app's `app/models`):

  ```templ
  // illustrative
  if _, ok := auth.User[*models.User](ctx); ok {
  	@navLink("posts.index", i18n.T(ctx, "posts.title"))
  }
  ```
- **Another column.** Add a migration (`go tool anetos make:migration
  add_slug_to_posts_table`), the field to `Post` and `PostInput`, a
  line to `fill`, and the input to `postFields` in the view.
- **Other text.** Change `locales/en/posts.yaml`; add a language with
  its own folder ([Translations](../guides/translations.md)).
- **Look.** The pages call the components of `views/ui` (`ui.PageHeader`,
  `ui.Table`, `ui.Form`, `ui.Field`…) and have no class names. Change
  the colors in `public/static/app.css`, or a component's markup in
  `views/ui`, and every page follows ([Style your
  app](../guides/styling.md)).

`go test ./...` runs the test it wrote; keep it passing as you change
the pages.

## In an API project

In a project made with `anetos new --stack=api`, the same
command writes JSON endpoints instead of pages: the model and its
migration, `app/handlers/posts.go`, `routes/posts.go` and
`posts_test.go`, and `Posts(api)` at the end of `Register` in
`routes/api.go`. There are no views or catalog.

| Route | URL | Answers |
|---|---|---|
| `api.posts.index` | `GET /api/v1/posts` | A page of posts |
| `api.posts.create` | `POST /api/v1/posts` | `201` with the post, and its URL in `Location` |
| `api.posts.show` | `GET /api/v1/posts/{id}` | The post |
| `api.posts.update` | `PUT /api/v1/posts/{id}` | The post, every field replaced |
| `api.posts.delete` | `DELETE /api/v1/posts/{id}` | `204` |

A post is answered as `PostResponse`, a struct of the handlers' file
(the ID, the fields, `created_at`, `updated_at`), never as the model:
a column you add later shows only once you add it there. Optional dates
are `null` when empty. The body of `POST` and `PUT` is JSON with the
fields' names, checked with the same rules as the pages' form: a 422
lists the fields' errors.

The list is `{"data": [...], "current_page": 1, "per_page": 20,
"total": 1, "last_page": 1}`. Its query:

- `?page=2`, and `?per_page=50` (20 by default, at most 100).
- `?sort=title`, or `-title` for descending: `id`, `created_at`,
  `updated_at` or a field other than a text. Newest first (`-id`) by
  default; another value is a 422. Rows with the same value come newest
  first. Where an optional date's empty value (`NULL`) sorts depends on
  the database: first ascending in SQLite and MySQL, last in PostgreSQL.
- An exact filter per field of type string, email, int, bool or date:
  `?published=true`. An empty value (`?title=`) doesn't filter. Texts
  and floats aren't filters: search and ranges are yours to add to
  `Index`.

So fields can't be named `page`, `per_page` or `sort` in an API project.

The endpoints are open to every client. After
[`make:auth`](../guides/api-accounts.md), for clients with a token only:

1. Move the `Posts(api)` call from `routes/api.go` to `routes/auth.go`,
   in the `loggedIn` group: `Posts(loggedIn)`.
2. If tokens should need abilities, require them on the routes, in
   `routes/posts.go` (with `anetos.dev/anetos/auth` imported):

   ```go
   // illustrative
   read := r.With(auth.RequireAbilities("posts:read"))
   write := r.With(auth.RequireAbilities("posts:write"))
   read.Get("/posts", web.H(h.Index)).Name("posts.index")
   write.Post("/posts", web.H(h.Create)).Name("posts.create").Status(http.StatusCreated)
   ```

3. `posts_test.go` now gets 401: make its test sign up first, with
   `auth_test.go`'s helper, `app, _ := authRegister(t)` in place of
   `app := anetostest.New(t, setup)`.
4. `go run . openapi`, so `openapi.json` says they need a token.

[Tutorial: build an API](build-an-api.md) does it step by step, with
records that belong to their user.

`make:crud` updates `openapi.json`, the API's description, with the
five operations, `PostResponse`, `PostInput` and `PagePostResponse`
([Describe an API with OpenAPI](../guides/openapi.md)).

Number and boolean fields are never required: a request without
`price` saves 0, and a `PUT` without it sets it back to 0. Add a rule
(`validate:"required"` refuses 0, so a positive `min:1` or a pointer
field may suit better) where a value must be given.

## When to write pages by hand

`make:crud` makes the common case quick: a table, a form, plain
validation. Pages with relations, permissions or htmx start from its
code, or from scratch: the [tutorial](tutorial/README.md) builds issue
pages by hand, step by step, and the [guides](../guides/) cover
[forms](../guides/forms.md), [validation](../guides/validation.md),
[queries](../guides/queries.md) and [relations](../guides/relations.md).

Next: [Add accounts](add-accounts.md).
