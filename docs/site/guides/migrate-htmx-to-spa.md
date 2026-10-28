---
title: Move htmx pages to an API and an SPA
since: v0.6.0
group: "Frontends"
weight: 182
---

# Move htmx pages to an API and an SPA

A project of the web stack renders its pages on the server. With a
single-page app, the browser renders them, and the server answers JSON:
the handlers return data instead of pages, validation errors come back
as a `422` answer instead of a redirect, and the SPA has its own router,
text and forms. Since every page and most handlers change, this guide
builds the API as a new project of the api stack beside the old one, on
the same database, adds the SPA to it, and switches over when it's
ready. The old app keeps serving your users until then.

To keep the Go side and write the pages as components instead, without
an API, see [Move htmx pages to Inertia](migrate-htmx-to-inertia.md).

## Before you start

- A project made with `anetos new` (the web stack), with Anetos v0.6 or
  later, and Node.js 20.19 or 22.12 or later for the SPA.
- [Build a single-page app](spa.md) and [Log in from your SPA with a
  session cookie](spa-login.md): the SPA's side, which this guide
  doesn't repeat.

## Steps

### 1. Make the API project

In a new folder, an API project with your app's name and module path,
and the same generators your project ran: `make:auth`'s API accounts,
and `make:crud`'s endpoints for each model, with the same fields.

```sh
anetos new blog --stack=api --module=example.com/blog
cd blog
go tool anetos make:auth
go tool anetos make:crud Post title:string body:text published:bool
```

`make:crud` writes the model, a migration, the handlers (the list with
pages, sorting and filters; show, create, replace, delete), the routes
under `/api/v1` and a test.

### 2. Share the old app's database and key

In the new project's `.env`, set the `DB_*` settings and `APP_KEY` (and
`APP_PREVIOUS_KEYS`, if set) to the old app's. The key decrypts what the
old app encrypted, such as two-factor secrets.

The tables exist already, so the new project takes the old one's
migrations instead of its own: delete the migrations `make:auth` and
`make:crud` wrote for the same tables (`database/migrations/*_create_users_table.go`,
`*_create_posts_table.go`), and copy the old project's files from
`database/migrations/`. Then:

```sh
go run . migrate   # Nothing to migrate.
```

The new project's models can stay as `make:crud` and `make:auth` wrote
them: a column the API's model doesn't name (the web stack's
`remember_token`, the user's `locale`) is left as it is.

### 3. Move your code into the endpoints

The generated endpoints do what `make:crud`'s pages did. Move what you
added to the old handlers: validation rules, permission checks,
queries, events and jobs, emails. A handler of the API returns its
result instead of a page:

```go
// illustrative: a page in the web stack
func (Posts) Show(c *web.Ctx, in PostID) (web.Responder, error) {
	row, err := db.Find[models.Post](c, in.ID)
	if err != nil {
		return nil, err
	}
	return web.Render(views.PostPage(row)), nil
}

// the API's endpoint
func (Posts) Show(c *web.Ctx, in PostID) (PostResponse, error) {
	row, err := db.Find[models.Post](c, in.ID)
	if err != nil {
		return PostResponse{}, err
	}
	return postResponse(row), nil
}
```

The result's type is in the OpenAPI document (`go run . openapi`) and in
the SPA's client (`go run . types`). Check the API with its tests
(`go test ./...`), and with a user of the old app, which logs in:

```sh
curl -X POST localhost:8080/api/v1/login -H 'Content-Type: application/json' \
  -d '{"email": "ada@example.com", "password": "…", "device_name": "cli"}'
```

### 4. Add the SPA

```sh
go tool anetos vite:install
```

In an API project it adds the SPA, which the binary serves at the
app's own address; `go tool anetos dev` runs Vite at
`http://localhost:5173`, which forwards `/api` to the app. It lists
those origins in `.env`'s `HTTP_SPA_ORIGINS`, so the SPA logs in with
the session cookie ([Log in from your SPA with a session
cookie](spa-login.md)); put your production address in
`deploy/production.env.example`'s. Then write its pages: the steps are in [Build a
single-page app](spa.md), and
[`examples/spa`](../../../examples/spa) has an SPA in React with the
accounts' pages (login with two-factor codes, registration, password
reset, email verification) and a list of records with a form.

What each part of a page becomes:

| In the web stack | With the API and the SPA |
|---|---|
| A handler renders a templ page | The handler returns a typed result, as JSON; the SPA renders it |
| A link names a route (`web.MustURL`, `@navLink`) | The SPA's router moves between its pages; its calls name the API's routes, in the generated client (`client.api.posts.index()` for `api.posts.index`) |
| A form posts; an invalid one is redirected back, with its errors and input in the session | The SPA sends JSON; an invalid body is a `422` answer with the errors by field, which the client throws as `ApiError` (`examples/spa`'s `form.tsx` shows them by field) |
| A message flashed for the next page | The SPA shows its own message after a `200` or `201` |
| The `_token` field and htmx's header (CSRF) | The `XSRF-TOKEN` cookie, which the generated client sends back |
| `make:auth`'s pages, the session | The API's accounts: the session cookie for the SPA, tokens for other clients |
| The text, in `locales/` | The API's messages (validation, errors) stay in `locales/`; the SPA's own text is the SPA's |
| Tests: `AssertSee` on the page | Tests: `AssertJSON`, `AssertJSONPath` on the answer |

### 5. Switch over

When the SPA covers the old pages, deploy the new project in the old
one's place ([Deploy](deployment.md)), with the same settings, at the
same address, and stop the old app. Links to the old pages' paths
(`/posts/12`) reach the SPA, which the binary serves for every path but
`/api/`'s: give its router the same paths, so bookmarks and links in
emails keep working.

## Complete example

[`examples/spa`](../../../examples/spa) is the end state: an API made
with `make:auth` and `make:crud`, and its SPA in React, served by one
binary.

## How it works

The API project and the old app read and write the same tables, so
both work during the move: a post written in one shows in the other.
The API's model of a table can name fewer columns than the old one's.
The key encrypts sessions, two-factor secrets and signed links, so the
same `APP_KEY` makes the old app's encrypted data readable by the new
one.

## Testing it

Each endpoint has its test from `make:crud`; add the cases your old
handlers' tests had, as JSON:

```go
// illustrative
app.GetJSON("/api/v1/posts").AssertOK().AssertJSONPath("total", 0)
app.PostJSON("/api/v1/posts", map[string]any{}).AssertValidationErrors("title", "body")
```

The SPA's pages are tested with your frontend's tools; `go tool anetos
check` builds the SPA, type-checking it against the generated client.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `migrate` tries to create a table that exists | The new project's own migration for it is still there | Delete it; copy the old project's migrations instead (step 2) |
| Two-factor codes are refused for users who had them on | Another `APP_KEY` than the old app's, which encrypted their secrets | Copy the key (step 2) |
| The SPA's login answers a token | The SPA's origin isn't in `HTTP_SPA_ORIGINS` | List it ([Log in from your SPA](spa-login.md)) |
| An old link gives the SPA's not-found page | The SPA's router lacks the path | Add the route to the SPA's router |

## Next steps

- [Build a single-page app](spa.md).
- [Generate TypeScript types for the frontend](typescript.md): the
  client.
- [Add accounts to an API](api-accounts.md).
- [Frontends](../concepts/frontends.md): the ways Anetos serves a
  frontend.
