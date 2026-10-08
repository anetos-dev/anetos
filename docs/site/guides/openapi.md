---
title: Describe an API with OpenAPI
since: v0.4.0
group: "Basics"
weight: 110
---

# Describe an API with OpenAPI

Package `web/openapi` writes an OpenAPI 3.1 document of your API from
its routes and its typed handlers: what each operation reads and
answers, which credentials it needs and how it fails. You commit the
document (`openapi.json`) beside the code, a test fails when it's out of
date, and the app serves it for clients and tools.

## Before you start

- Handlers routed with `web.H` (typed handlers: [Handlers](handlers.md)).
  The document describes only those: a plain `func(c *web.Ctx) error`
  says nothing about what it reads or writes.
- A project made with `anetos new --stack=api` has all of this already:
  `routes.OpenAPI` in `routes/api.go`, `openapi.ForApp` in `main.go`,
  `openapi.json` and its test. `make:auth` and `make:crud` update the
  file.

## Steps

### 1. Describe the API

A `Config` names the API and says which routes it covers. The tracker
example's:

```go
// OpenAPI describes the API from its typed handlers: `go run . openapi`
// writes openapi.json, which api_test.go checks is up to date, and the
// app serves it at /api/openapi.json.
var OpenAPI = openapi.Config{
	Title:       "Tracker",
	Version:     "1.0.0",
	Description: "Projects and issues, for clients with an API token: create one on the tokens page.",
	Prefix:      "/api",
	Path:        "/api/openapi.json",
}
```

(Copied from [`examples/tracker/routes/tracker.go`](../../../examples/tracker/routes/tracker.go), region `openapi`.)

| Field | Means |
|---|---|
| `Title` | The API's name (required) |
| `Version` | The document's version, `1.0.0` if empty: yours, not Anetos's |
| `Description` | An introduction, in Markdown |
| `Prefix` | Only the routes at or below this path (`/api/v1`); empty: every typed route |
| `File` | Where `openapi` writes and `Check` reads, `openapi.json` if empty |
| `Path` | Where the app serves the document; empty: not served |
| `Servers` | The API's base URLs; none: `/`, the document's own host |
| `SecuritySchemes` | Schemes your middleware names besides `bearer` (step 5) |

### 2. Add the command and the route

In `setup`, after the routes:

```go
// The API's description: `go run . openapi`, GET /api/openapi.json.
if err := openapi.ForApp(app, srv, routes.OpenAPI); err != nil {
	return nil, err
}
```

(Copied from [`examples/tracker/main.go`](../../../examples/tracker/main.go), region `openapi`.)

### 3. Write the document

```sh
go run . openapi            # writes openapi.json
go run . openapi --check    # exits 1 if openapi.json is out of date
go run . openapi --out=-    # prints it
```

The command builds the document from the routes alone: it doesn't boot
the app, so it needs no database. It warns about what it can't describe
(below). The same routes always give the same bytes, so the file's diff
shows how a change alters the API.

### 4. Check it in a test

```go
// openapi.json describes the API as it is: after changing a route or a
// handler's types, update it with `go run . openapi`.
func TestOpenAPI(t *testing.T) {
	app := anetostest.New(t, setup)
	if err := openapi.Check(app.Router(), routes.OpenAPI); err != nil {
		t.Fatal(err)
	}
	app.Get("/api/openapi.json").AssertOK().AssertJSONPath("info.title", "Tracker")
}
```

(Copied from [`examples/tracker/api_test.go`](../../../examples/tracker/api_test.go), region `openapi-test`.)

`Check` fails with the first line that differs and the command to run.
CI runs your tests, so a pull request can't change the API without the
document.

### 5. Say what your middleware asks for

The document can't look inside a middleware: a middleware tells it, by
returning its handler through `web.Documented`. Package `auth`'s
`Require` says its routes need a bearer token (and may answer 401),
`RequireAbilities("bookmarks:write")` that the token needs those
abilities (its scopes, and 403), `TokenMiddleware` that a bad token is a
401, `rbac.Require` 401 and 403, `ratelimit.Middleware` 429. Your own,
for a key partners send in a header:

```go
// illustrative
func partnerKey(next http.Handler) http.Handler {
	return web.Documented(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validPartnerKey(r.Header.Get("X-Partner-Key")) {
			web.WriteError(w, r, web.Error(http.StatusUnauthorized, "A partner key is needed."))
			return
		}
		next.ServeHTTP(w, r)
	}), web.MiddlewareDoc{Security: "partnerKey",
		Responses: map[int]string{http.StatusUnauthorized: "No valid partner key."}})
}
```

`Security` names a scheme: `bearer` is built in; give others in
`Config.SecuritySchemes`:

```go
// illustrative
var OpenAPI = openapi.Config{
	Title: "Shop", Prefix: "/api/v1",
	SecuritySchemes: map[string]openapi.SecurityScheme{
		"partnerKey": {Type: "apiKey", In: "header", Name: "X-Partner-Key"},
	},
}
```

`Scopes` are what the credentials must allow (a token's abilities).
Only the middleware of the route's groups and `With` is seen, not
`UseGlobal`'s.

## How it works

Each typed route under the prefix is an operation, named by the route's
name (`operationId`), grouped by its handler's type (`tags`: the
`Bookmarks` of `h.Index`).

- **Inputs**, as `web.H` binds them: `path`, `query` and `header`
  fields are parameters; the other fields are the JSON body (a
  `multipart/form-data` body when the input has files; none for GET).
  The `validate` rules become what JSON Schema can say: `required`,
  lengths and bounds, `enum`, formats. A field that isn't required may
  also be blank, as `validate` lets it be.
- **Results**, as `encoding/json` writes them: the route's status
  (`Status`, 200 by default) with the result's schema; `web.Empty` and
  204 without a body. Fields without `omitempty` are required, pointers
  nullable; slices and maps aren't, so answer empty ones, not nil.
- **Schemas** of named structs are components named after the Go type
  (`BookmarkResponse`; `PageBookmarkResponse` for
  `db.Page[BookmarkResponse]`).
- **Errors** are problem details (the `Problem` schema): 400 when the
  input has values to read, 422 when it has validate rules or a
  `Validate` method, the middleware's, and `default` for any other.
- **Security** comes from the middleware (step 5); when some operations
  need credentials, the others say they don't (`security: []`).

The [OpenAPI reference](../reference/openapi.md) has every rule: the
types' schemas, the validation rules' keywords, the names, the
warnings.

## Testing it

The test of step 4 is the check. To look at the document as clients
get it, `go tool anetos dev` and open `/api/openapi.json`, or load
`openapi.json` into an OpenAPI viewer or validator.

## Common problems

- **"not a typed handler: left out".** The route's handler is a plain
  `func(c *web.Ctx) error`, or `web.H`'s handler wrapped in another
  function before it was registered. Route `web.H(…)` as it returns it.
- **"its result is a web.Responder".** A handler returning
  `web.Responder` chooses its status and body as it runs: the document
  says only "2XX". Return a struct and put the status on the route
  (`.Status(http.StatusCreated)`).
- **"has its own MarshalJSON".** The type writes its own JSON: the
  document says "any value". Answer with a plain struct.
- **A list is `null` in responses.** The schema says array; a nil slice
  is written as `null`. Make empty slices (`[]T{}`).
- **"its result is an interface".** The result type is `any` or
  another interface: the document can't say what it is. Return a
  concrete type.
- **The test fails after a change.** Run `go run . openapi` and commit
  the file with the change.

## Next steps

- [OpenAPI reference](../reference/openapi.md): every rule of the
  document.
- [Tutorial: build an API](../getting-started/build-an-api.md): an API
  whose document grows step by step.
- [Handlers](handlers.md): typed handlers, `Status` and `web.Empty`.
- [Add accounts to an API](api-accounts.md): the bearer tokens the
  document describes.
- [Validation](validation.md): the rules the schemas come from.
