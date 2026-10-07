---
title: Routing
since: v0.1.0
group: "Basics"
weight: 100
---

# Routing

Map URLs to handlers, group routes under shared prefixes and middleware,
name them, and generate URLs from names.

## Before you start

You have an app from `anetos.New()`. See
[Configure your application](configuration.md).

## Steps

### 1. Create the HTTP server

```go
func run() error {
	app, err := anetos.New()
	if err != nil {
		return err
	}
	srv, err := web.NewServer(app) // reads HTTP_*, registers the "http" component
	if err != nil {
		return err
	}
	routes(srv.Router(), &Notes{})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.Run(ctx)
}
```

(Copied from [`examples/notes`](../../../examples/notes/main.go), region
`main`.)

`web.NewServer` reads the `HTTP_*` settings (address, timeouts, body limit,
CORS…), installs the default middleware and adds the server to the app as
the `http` component. `app.Run` starts it, and SIGTERM shuts it down
gracefully, letting in-flight requests finish.

### 2. Register routes

```go
func routes(r *web.Router, notes *Notes) {
	r.Get("/", func(c *web.Ctx) error {
		return c.RedirectRoute("notes.index")
	}).Name("home")

	api := r.Group("/notes").As("notes.")
	api.Get("", web.H(notes.List)).Name("index")
	api.Post("", web.H(notes.Store)).Name("store")
	api.Get("/{id}", web.H(notes.Show)).Name("show")
	api.Delete("/{id}", web.H(notes.Delete)).Name("delete")
}
```

(Region `routes`.)

- `Get`, `Post`, `Put`, `Patch`, `Delete` and `Options` register a handler
  for one method. `GET` routes also answer `HEAD`. `Handle("", pattern, h)`
  matches every method.
- Handlers are `func(c *web.Ctx) error`, or typed handlers wrapped with
  `web.H`. See [Handlers and requests](handlers.md).

### 3. Use path parameters

Patterns use Go's `net/http` syntax:

| Pattern | Matches | Read with |
|---|---|---|
| `/posts/{id}` | `/posts/42` | `c.Param("id")` or a `path:"id"` field |
| `/files/{path...}` | `/files/a/b.txt` (the rest of the path) | `c.Param("path")` → `a/b.txt` |
| `/about/` | only `/about/`; `/about` is redirected to it | |

> **Note:** Unlike raw `http.ServeMux`, a pattern ending in `/` matches
> **only** that path, not everything below it. Use a `{name...}` wildcard
> for a subtree.

### 4. Group routes

`r.Group(prefix, middleware...)` returns a router whose routes share a path
prefix and middleware. `As("prefix.")` adds a prefix to their names. Groups
nest. Inside a group, `""` and `"/"` both mean the group's own path
(`/posts`, without a trailing slash).

```go
// illustrative
admin := r.Group("/admin", requireAdmin).As("admin.")
admin.Get("/users", h.Users).Name("users") // GET /admin/users, name "admin.users"
```

Routes for one host only (an admin at `admin.example.com`) go on
`r.Host(host)`, which works like a group. A host's routes win for that
host; routes without a host answer every host. Requests match on the
host name, whatever their port. The routes' URLs are absolute, with
`APP_URL`'s scheme (`https` without one) and the port given to `Host`, if
any, since another host can't be reached with a path:

```go
// illustrative
admin := r.Host("admin.example.com").Group("", requireAdmin)
admin.Get("/", h.Dashboard).Name("admin.home") // c.URL("admin.home"): "https://admin.example.com/"
```

### 5. Name routes and generate URLs

Give routes names and build URLs from them, so paths live in one place:

```go
// illustrative
url, err := c.URL("notes.show", note.ID) // "/notes/7"
return c.RedirectRoute("notes.index")    // 303 See Other
return web.RedirectRoute("notes.show", note.ID), nil // from a typed handler
```

Arguments fill the wildcards in order and are path-escaped; a `url.Values`
after them becomes the query string
(`c.URL("notes.index", url.Values{"page": {"2"}})` is `"/notes?page=2"`).
A wrong name or argument count is an error. `r.MustURL` panics instead, which is useful for
fixed links at startup.

### 6. Add middleware

Middleware is plain `func(http.Handler) http.Handler`, so any net/http
middleware works.

| Call | Applies to |
|---|---|
| `r.UseGlobal(mw...)` | Every request, including 404/405, before routing. Call it before the server starts |
| `r.Use(mw...)` | Routes registered on `r` (and groups created from it) **afterwards**. It panics if `r` or any of its groups already has routes, so middleware can't silently miss routes |
| `r.Group(prefix, mw...)` | Routes in the group |
| `r.With(mw...).Get(...)` | A single route |

`web.NewServer` already installs these global middleware, outermost first:
`Recover`, `RequestIDs`, `RealIP`, `AccessLog`, `SecureHeaders`, `CORS`
(when configured), the request's locale (with
[translations](translations.md)), `BodyLimit` and `Timeout`. See the
[configuration reference](../reference/configuration.md#http-server).
`web.ClientIP(r)` returns the client's address `RealIP` found;
`web.ClientIPFrom(ctx)` the same from a request's context.
To limit how often clients call a group of routes, add
`ratelimit.Middleware` to it: see [Rate limiting](rate-limiting.md).

## How it works

The router registers your patterns on a standard `http.ServeMux`, adding
the method and making trailing-slash patterns exact. A catch-all route
handles everything else:

- A path that exists for other methods gets **405 Method Not Allowed** with
  an `Allow` header listing them (including custom methods such as
  `PURGE`), and `OPTIONS` gets **204** with `Allow`.
- Anything else gets **404**, as JSON problem details or an HTML page (see
  [Return errors](handlers.md#4-return-errors)); always JSON when
  `web.JSONErrors` is global middleware, as in an API project.

> **Coming from Laravel?** `Route::get(...)->name('x')`, `Route::prefix()`
> groups and `route('x', $id)` map to `r.Get(...).Name("x")`, `r.Group()`
> and `c.URL("x", id)`.

## Testing it

The router is an `http.Handler`, so `httptest` works directly:

```go
// illustrative
r := web.NewRouter()
routes(r, &Notes{})
rec := httptest.NewRecorder()
r.ServeHTTP(rec, httptest.NewRequest("GET", "/notes/1", nil))
```

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| Panic: `Use called after routes were added` | `r.Use` after `r.Get(...)` | Call `Use` first, or use `r.With(mw).Get(...)` |
| Panic: `pattern "posts" must start with /` | Missing slash | Write `/posts` |
| Panic: `… conflicts with …` (from net/http) | Two routes match the same requests | Remove the duplicate or make one more specific |
| `/about` redirects to `/about/` | The route is `/about/` | Register `/about` instead |
| Panic: `duplicate route name` | Same name used twice | Names are global; use group prefixes (`As`) |

## Next steps

- [Handlers and requests](handlers.md): typed handlers, binding, responses,
  errors
- [HTTP request lifecycle](../concepts/http-request-lifecycle.md)
