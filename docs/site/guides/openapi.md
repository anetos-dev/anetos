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
`TokenMiddleware` that a bad token is a 401, `rbac.Require` 401 and
403, `ratelimit.Middleware` 429. Your own:

```go
// illustrative
func fullAccess(next http.Handler) http.Handler {
	return web.Documented(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !auth.TokenCan(r.Context(), "*") {
			web.WriteError(w, r, auth.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}), web.MiddlewareDoc{Security: "bearer", Scopes: []string{"*"},
		Responses: map[int]string{http.StatusForbidden: "The token lacks the * ability."}})
}
```

`Security` names a scheme: `bearer` is built in; give others in
`Config.SecuritySchemes` (`{Type: "apiKey", In: "header", Name:
"X-API-Key"}`). `Scopes` are what the credentials must allow (a token's
abilities). Only the middleware of the route's groups and `With` is
seen, not `UseGlobal`'s.

## How it works

Each typed route under the prefix is an operation, named by the route's
name (`operationId`), grouped by its handler's type (`tags`: the
`Products` of `h.Index`).

**Inputs**, as `web.H` binds them: `path`, `query` and `header` fields
are parameters; the other fields are the JSON body (a
`multipart/form-data` body when the input has files). The `validate`
rules say what JSON Schema can:

| Rule | Schema |
|---|---|
| `required` | Listed in `required`; a string at least 1 character long, an array or map not empty; a number not 0 and a bool `true` (unless the field is a pointer), as `required` checks |
| `min`, `max`, `size`, `between` | `minLength`/`maxLength` (strings), `minimum`/`maximum` (numbers), `minItems`/`maxItems` (slices), `minProperties`/`maxProperties` (maps) |
| `in` | `enum` |
| `email`, `url`, `uuid`, `date`, `datetime`, `ipv4`, `ipv6` | `format` |
| `distinct` | `uniqueItems` |

The other rules skip an empty value, so a field that isn't `required`
may also be blank: its schema is `anyOf` the constrained one and a blank
string (or an empty array or map). Rules JSON Schema has no keyword for
(`unique`, `confirmed`, `after`…) are still checked; the schema doesn't
show them. A `time.Duration` parameter is text (`30s`), as the binding
reads it. GET and HEAD requests have no body.

**Results**, as `encoding/json` writes them: the route's status
(`Status`, 200 by default) with the result's schema; `web.Empty` and
204 without a body. Every field without `omitempty` is listed in
`required` (not those of an embedded pointer, left out when it's nil);
pointers are nullable (`["string", "null"]`), slices and maps aren't:
answer empty ones, not nil.

| Go | Schema |
|---|---|
| `string`, `bool` | `string`, `boolean` |
| integers, floats | `integer`, `number` (`int64`, `double`… as `format`) |
| `time.Time`, `anetos.Date` | `string`, `date-time`, `date` |
| `[]byte` | `string`, base64 |
| slices, maps | `array`, `object` with `additionalProperties` |
| a named struct | a component (`#/components/schemas/ProductResponse`); `db.Page[ProductResponse]` is `PageProductResponse` |
| an `encoding.TextMarshaler` | `string` |
| an interface, a `json.Marshaler` | any value |

A type used for a body and a result has two components, the body's
named `…Input`. Two types of one name in different packages get their
package's name first (`handlers.Order`); a type named `Problem` becomes
`Problem2`. Two routes OpenAPI can't tell apart (`GET /a/{id}` and
`DELETE /a/{key}`, or one path on two hosts) are an error.

**Errors** are problem details (`application/problem+json`, the
`Problem` schema): 400 when the input has values to read, 422 when it
has validate rules or a `Validate` method, the middleware's, and
`default` for any other.

**Security** comes from the middleware (step 5): an operation behind
`auth.Require` lists `bearer`; when some operations need credentials,
the others say they don't (`security: []`).

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

- [Handlers](handlers.md): typed handlers, `Status` and `web.Empty`.
- [Add accounts to an API](api-accounts.md): the bearer tokens the
  document describes.
- [Validation](validation.md): the rules the schemas come from.
