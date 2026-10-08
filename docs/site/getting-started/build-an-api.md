---
title: "Tutorial: build an API"
since: v0.4.0
group: "Your first API"
weight: 35
---

# Tutorial: build an API

Build a JSON API for bookmarks: people sign up, get a token, and save
links that only they see; a token made for a script can read them but
not change them; and the API describes itself in OpenAPI for the apps
that call it. About thirty minutes. The finished project is
[`examples/bookmarks`](../../../examples/bookmarks).

You need [Go and the `anetos` tool](install.md). The
[issue tracker tutorial](tutorial/README.md) builds an app with pages;
this one has none.

## 1. Create the project

```sh
anetos new bookmarks --stack=api
cd bookmarks
go run . migrate
go tool anetos dev
```

`--stack=api` writes an app that serves JSON only: no views, sessions
or CSRF protection, every error as JSON problem details, the routes
under `/api/v1` in `routes/api.go`. In another terminal:

```sh
curl http://localhost:8080/api/v1
```

```json
{"name":"bookmarks","message":"Welcome to the Bookmarks API."}
```

The project also has `openapi.json`, the API's description, which the
app serves at `/api/v1/openapi.json`. It grows with each step.
[Create a project](create-a-project.md#an-api-instead) lists the files.

## 2. Accounts and tokens

```sh
go tool anetos make:auth
go run . migrate
```

`make:auth` writes registration, login, logout, email verification,
password reset, two-factor sign-in and token management, as JSON
endpoints that sign in with API tokens. Sign up:

```sh
curl -X POST http://localhost:8080/api/v1/register \
  -H 'Content-Type: application/json' \
  -d '{"name":"Ada","email":"ada@example.com","password":"correct horse","password_confirmation":"correct horse"}'
```

The answer has a `token`. Send it with every request:

```sh
curl http://localhost:8080/api/v1/me -H 'Authorization: Bearer 1|q7Xw…'
```

The emails (verification, password reset) go to the log in
development, and link to your client app at `AUTH_CLIENT_URL`.
[Add accounts to an API](../guides/api-accounts.md) lists every
endpoint.

## 3. Bookmarks

```sh
go tool anetos make:crud Bookmark url:string title:string notes:text:optional archived:bool
go run . migrate
```

`make:crud` writes the model and its migration, five endpoints in
`app/handlers/bookmarks.go` (`GET` and `POST /api/v1/bookmarks`, `GET`,
`PUT` and `DELETE /api/v1/bookmarks/{id}`), their routes in
`routes/bookmarks.go`, and a test. The list is paged, sorted with
`?sort=` and filtered by field (`?archived=true`); each bookmark is
answered as `BookmarkResponse`, a struct of the handlers' file, never
the model. [Add pages for a model](crud.md#in-an-api-project) has the
details.

The endpoints are open to every client, and every bookmark is
everyone's. The next steps change that.

## 4. Each user's bookmarks

A bookmark belongs to the user who saved it: it needs a `user_id`
column. Undo the bookmarks' migration (`go run . migrate:rollback`
drops the table, with any bookmark you saved), and add the column to
it, after `t.ID()`:

```go
t.ForeignID("user_id").Constrained().CascadeOnDelete() // whose bookmark
```

(Copied from [`examples/bookmarks/database/migrations/2026_10_08_140750_create_bookmarks_table.go`](../../../examples/bookmarks/database/migrations/2026_10_08_140750_create_bookmarks_table.go), region `user-id`.)

and the field to the model, `app/models/bookmark.go`:

```go
// Bookmark is a row of the bookmarks table (anetos make:crud). `go tool anetos
// gen` writes its typed columns (BookmarkCols) to models_gen.go.
type Bookmark struct {
	db.Model        // id, created_at, updated_at
	UserID   int64  `db:"user_id"` // whose bookmark
	URL      string `db:"url"`
	Title    string `db:"title"`
	Notes    string `db:"notes"`
	Archived bool   `db:"archived"`
}
```

(Copied from [`examples/bookmarks/app/models/bookmark.go`](../../../examples/bookmarks/app/models/bookmark.go), region `model`.)

Then `go tool anetos gen` (the typed column `BookmarkCols.UserID`) and
`go run . migrate` again.

In `app/handlers/bookmarks.go`, two helpers (import
`anetos.dev/anetos/auth`): the signed-in user, and a bookmark of
theirs:

```go
// owner is the signed-in user's ID: a request sees their bookmarks only.
func owner(c *web.Ctx) (int64, error) {
	u, err := auth.Current[*models.User](c)
	if err != nil {
		return 0, err
	}
	return u.ID, nil
}

// find returns the user's bookmark id. Another user's is a 404, like
// one that doesn't exist (db.ErrNotFound): it isn't theirs to know of.
func find(c *web.Ctx, id int64) (models.Bookmark, error) {
	userID, err := owner(c)
	if err != nil {
		return models.Bookmark{}, err
	}
	return db.Query[models.Bookmark](c).Where(models.BookmarkCols.UserID.Eq(userID)).Find(id)
}
```

(Copied from [`examples/bookmarks/app/handlers/bookmarks.go`](../../../examples/bookmarks/app/handlers/bookmarks.go), region `owner`.)

`Index` lists the user's bookmarks only; its query starts with:

```go
userID, err := owner(c)
if err != nil {
	return db.Page[BookmarkResponse]{}, err
}
q := db.Query[models.Bookmark](c).Where(models.BookmarkCols.UserID.Eq(userID))
```

(Copied from [`examples/bookmarks/app/handlers/bookmarks.go`](../../../examples/bookmarks/app/handlers/bookmarks.go), region `index`.)

`Create` gives the new bookmark its owner:

```go
userID, err := owner(c)
if err != nil {
	return BookmarkResponse{}, err
}
row := models.Bookmark{UserID: userID}
```

(Copied from [`examples/bookmarks/app/handlers/bookmarks.go`](../../../examples/bookmarks/app/handlers/bookmarks.go), region `create`.)

and `Show`, `Update` and `Delete` load the row with `find(c, in.ID)`
in place of `db.Find[models.Bookmark](c, in.ID)`. While you're in the
file, check that links are links: the input's `url` field gets the
`url` rule, `validate:"required|url|max:255"`.

Last, only signed-in clients may reach the bookmarks. Remove the
`Bookmarks(api)` line from `routes/api.go`, and call it from
`routes/auth.go`, in the group whose routes need a token:

```go
Bookmarks(me) // the user's bookmarks, with their token
```

(Copied from [`examples/bookmarks/routes/auth.go`](../../../examples/bookmarks/routes/auth.go), region `me`.)

A request without a token now gets 401, and Ada's bookmarks are hers
alone: Bob gets a 404 for them, as for a bookmark that doesn't exist,
so he can't even learn which IDs are taken.

## 5. An action of your own

Archiving a bookmark is one request with nothing to answer. The
handler returns `web.Empty`, which answers 204:

```go
// Archive marks a bookmark archived: 204, nothing to answer.
func (Bookmarks) Archive(c *web.Ctx, in BookmarkID) (web.Empty, error) {
	row, err := find(c, in.ID)
	if err != nil {
		return web.Empty{}, err
	}
	row.Archived = true
	return web.Empty{}, db.Update(c, &row)
}
```

(Copied from [`examples/bookmarks/app/handlers/bookmarks.go`](../../../examples/bookmarks/app/handlers/bookmarks.go), region `archive`.)

Route it in `routes/bookmarks.go`, after the other routes:

```go
// illustrative
r.Post("/bookmarks/{id}/archive", web.H(h.Archive)).Name("bookmarks.archive")
```

`web.H` binds `{id}` into `BookmarkID` (a bad one is a 400). A
handler's signature and its route say what the request takes and
answers, which is what the API's description is made from.

## 6. Tokens for scripts

A login's token may do everything (its ability is `*`). A script that
only backs the bookmarks up shouldn't be able to delete them: give it a
token that can only read. Require abilities on the routes,
`routes/bookmarks.go` becoming (with `anetos.dev/anetos/auth` among its
imports):

```go
// Bookmarks adds the bookmarks API: routes/auth.go calls it with its me
// group, so every request has a token. Reading needs the token's
// bookmarks:read ability, changing bookmarks bookmarks:write; a login's
// token has every ability.
func Bookmarks(r *web.Router) {
	var h handlers.Bookmarks
	read := r.With(auth.RequireAbilities("bookmarks:read"))
	write := r.With(auth.RequireAbilities("bookmarks:write"))
	read.Get("/bookmarks", web.H(h.Index)).Name("bookmarks.index")
	write.Post("/bookmarks", web.H(h.Create)).Name("bookmarks.store").Status(http.StatusCreated)
	read.Get("/bookmarks/{id}", web.H(h.Show)).Name("bookmarks.show")
	write.Put("/bookmarks/{id}", web.H(h.Update)).Name("bookmarks.update")
	write.Delete("/bookmarks/{id}", web.H(h.Delete)).Name("bookmarks.destroy")
	write.Post("/bookmarks/{id}/archive", web.H(h.Archive)).Name("bookmarks.archive")
}
```

(Copied from [`examples/bookmarks/routes/bookmarks.go`](../../../examples/bookmarks/routes/bookmarks.go), region `routes`.)

The user makes the script's token with their password:

```sh
curl -X POST http://localhost:8080/api/v1/tokens -H 'Authorization: Bearer 1|q7Xw…' \
  -H 'Content-Type: application/json' \
  -d '{"name":"backup","abilities":["bookmarks:read"],"password":"correct horse"}'
```

With that token, `GET /api/v1/bookmarks` works, and `POST`, `PUT`,
`DELETE` and the archive answer 403.

## 7. Test it

`make:crud`'s test, `bookmarks_test.go`, calls the endpoints without a
token, and saves `"Example url"`, which the `url` rule now refuses.
Replace its `TestBookmarks` with one that signs up first
(`auth_test.go`'s `authRegister` signs Ada up and sends her token with
the requests that follow):

```go
// TestBookmarks creates, lists, shows, replaces, archives and deletes a
// bookmark through the API, with a login's token (auth_test.go's
// authRegister).
func TestBookmarks(t *testing.T) {
	app, _ := authRegister(t)
	app.GetJSON("/api/v1/bookmarks").AssertOK().AssertJSONPath("total", 0).AssertJSONPath("data", []any{})
	app.PostJSON("/api/v1/bookmarks", map[string]any{}).AssertValidationErrors("url", "title")
	app.PostJSON("/api/v1/bookmarks", map[string]any{"url": "not a link", "title": "x"}).AssertValidationErrors("url")

	body := map[string]any{"url": "https://go.dev/doc/effective_go", "title": "Effective Go", "notes": "Read again"}
	var created handlers.BookmarkResponse
	res := app.PostJSON("/api/v1/bookmarks", body).AssertCreated().JSON(&created)
	path := fmt.Sprintf("/api/v1/bookmarks/%d", created.ID)
	res.AssertHeader("Location", path).AssertJSONPath("title", "Effective Go").AssertJSONPath("archived", false)

	app.GetJSON(path).AssertOK().AssertJSONPath("id", created.ID)
	app.GetJSON("/api/v1/bookmarks?sort=title").AssertOK().AssertJSONPath("total", 1).AssertJSONPath("data.0.id", created.ID)
	app.GetJSON("/api/v1/bookmarks?sort=nonsense").AssertValidationErrors("sort")
	app.PutJSON(path, map[string]any{"url": "https://go.dev/doc/effective_go", "title": "Effective Go, again"}).
		AssertOK().AssertJSONPath("title", "Effective Go, again").AssertJSONPath("notes", "")

	// Archiving answers 204; the list filters on it.
	app.PostJSON(path+"/archive", nil).AssertNoContent()
	app.GetJSON("/api/v1/bookmarks?archived=true").AssertOK().AssertJSONPath("total", 1)
	app.GetJSON("/api/v1/bookmarks?archived=false").AssertOK().AssertJSONPath("total", 0)

	app.DeleteJSON(path).AssertNoContent()
	app.GetJSON(path).AssertNotFound()
	app.DeleteJSON(path).AssertStatus(http.StatusNotFound)
}
```

(Copied from [`examples/bookmarks/bookmarks_test.go`](../../../examples/bookmarks/bookmarks_test.go), region `test-crud`.)

Add a test that one user doesn't see another's bookmarks:

```go
// A user sees their own bookmarks only: another's is a 404.
func TestBookmarksAreTheUsers(t *testing.T) {
	app, _ := authRegister(t)
	var ada handlers.BookmarkResponse
	app.PostJSON("/api/v1/bookmarks", map[string]any{"url": "https://ada.example.com", "title": "Ada's"}).
		AssertCreated().JSON(&ada)

	var bob handlers.SignInResponse
	app.PostJSON("/api/v1/register", map[string]any{
		"name": "Bob", "email": "bob@example.com", "password": "correct horse", "password_confirmation": "correct horse",
	}).AssertCreated().JSON(&bob)
	app.WithHeader("Authorization", "Bearer "+bob.Token)
	app.GetJSON("/api/v1/bookmarks").AssertOK().AssertJSONPath("total", 0)
	path := fmt.Sprintf("/api/v1/bookmarks/%d", ada.ID)
	app.GetJSON(path).AssertNotFound()
	app.PutJSON(path, map[string]any{"url": "https://bob.example.com", "title": "Bob's"}).AssertNotFound()
	app.PostJSON(path+"/archive", nil).AssertNotFound()
	app.DeleteJSON(path).AssertNotFound()
}
```

(Copied from [`examples/bookmarks/bookmarks_test.go`](../../../examples/bookmarks/bookmarks_test.go), region `test-owner`.)

and that a reading token can't change them:

```go
// A token made for reading reads, and can't change bookmarks: 403.
// Without a token, 401.
func TestBookmarkAbilities(t *testing.T) {
	app, _ := authRegister(t)
	var mine handlers.BookmarkResponse
	app.PostJSON("/api/v1/bookmarks", map[string]any{"url": "https://go.dev", "title": "Go"}).AssertCreated().JSON(&mine)
	path := fmt.Sprintf("/api/v1/bookmarks/%d", mine.ID)
	var reader handlers.NewTokenResponse
	app.PostJSON("/api/v1/tokens", map[string]any{
		"name": "reader", "abilities": []string{"bookmarks:read"}, "password": "correct horse",
	}).AssertCreated().JSON(&reader)

	app.WithHeader("Authorization", "Bearer "+reader.Token)
	app.GetJSON("/api/v1/bookmarks").AssertOK()
	app.GetJSON(path).AssertOK()
	app.PostJSON("/api/v1/bookmarks", map[string]any{"url": "https://go.dev", "title": "Go"}).AssertForbidden()
	app.PutJSON(path, map[string]any{"url": "https://go.dev", "title": "Go!"}).AssertForbidden()
	app.PostJSON(path+"/archive", nil).AssertForbidden()
	app.DeleteJSON(path).AssertForbidden()

	app.WithHeader("Authorization", "")
	app.GetJSON("/api/v1/bookmarks").AssertStatus(http.StatusUnauthorized)
}
```

(Copied from [`examples/bookmarks/bookmarks_test.go`](../../../examples/bookmarks/bookmarks_test.go), region `test-abilities`.)

Run them:

```sh
go test ./...
```

One test fails until the next step: `TestOpenAPI`, which checks that
`openapi.json` describes the routes as they are now.

## 8. The API's description

```sh
go run . openapi
```

`openapi.json` now has the archive operation, the `url` field's
`format: uri`, and on every bookmark operation the token it needs:

```json
"security": [
  {
    "bearer": [
      "bookmarks:write"
    ]
  }
]
```

with the 401 and 403 those routes may answer. Commit it with the code:
its diff shows reviewers how a change alters the API, and client
developers load it (from the file, or from `/api/v1/openapi.json`)
into an OpenAPI viewer or a client generator.
[Describe an API with OpenAPI](../guides/openapi.md) explains what goes
in it.

## 9. Ship it

- **Browsers on other origins** (a front end at
  `https://app.example.com`) may call the API once `HTTP_CORS_ORIGINS`
  lists them.
- **The client app's address**, `AUTH_CLIENT_URL`, is where emails link
  to; production refuses to start without it.
- **Build and deploy** as any Anetos app: `go tool anetos build`, the
  `Dockerfile` or the systemd unit in `deploy/`
  ([Deploy](../guides/deployment.md)).

## Next steps

- [Add accounts to an API](../guides/api-accounts.md): every account
  endpoint, two-factor sign-in, throttling.
- [Describe an API with OpenAPI](../guides/openapi.md) and its
  [reference](../reference/openapi.md).
- [Roles and permissions](../guides/roles-and-permissions.md): what
  users may do, with tokens' abilities as a ceiling.
- [Rate limiting](../guides/rate-limiting.md).
