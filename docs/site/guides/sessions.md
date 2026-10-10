---
title: Sessions and flash messages
since: v0.1.0
group: "Basics"
weight: 106
---

# Sessions and flash messages

Remember things about a visitor between requests, and show a message
once after a redirect.

## Before you start

Set `APP_KEY`: sessions are encrypted with it.

```sh
go tool anetos key:generate >> .env
```

(`go get -tool anetos.dev/anetos/cli/cmd/anetos@latest` adds
the tool.) Keep the key secret and the same across instances and
restarts; see [Rotate the key](#rotate-the-key).

## Steps

### 1. Add the session middleware

Create the manager from the app's configuration and add its middleware
to the routes that serve pages:

```go
sessions, err := session.New(app) // SESSION_* settings; needs APP_KEY
if err != nil {
	return nil, err
}
r := srv.Router()
r.UseGlobal(web.MethodOverride) // forms can send PUT and DELETE with _method
r.HandleStd(http.MethodGet, "/assets/{path...}", assets)

var h Notes
pages := r.Group("", sessions.Middleware, web.CSRF())
pages.Get("/", func(c *web.Ctx) error { return c.RedirectRoute("notes.index") })
pages.Get("/notes", web.H(h.Index)).Name("notes.index")
pages.Get("/notes/new", h.New).Name("notes.new")
pages.Post("/notes", web.H(h.Create)).Name("notes.store")
pages.Get("/notes/{id}/edit", web.H(h.Edit)).Name("notes.edit")
pages.Put("/notes/{id}", web.H(h.Update)).Name("notes.update")
pages.Delete("/notes/{id}", web.H(h.Delete)).Name("notes.delete")
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `routes`.)

`session.New` reads the `SESSION_*` settings
([configuration reference](../reference/configuration.md#sessions)) and
fails at startup, suggesting a key, when `APP_KEY` is missing.

### 2. Store and read values

```go
// illustrative
s := c.Session() // panics if the route has no session middleware

s.Put("theme", "dark")          // any value encoding/json can encode
theme := s.String("theme")      // "dark"
cart, ok := session.Value[[]int64](s, "cart")
s.Delete("theme")
```

`session.From(ctx)` returns the session (or nil) from any request
context, for code outside handlers.

### 3. Flash a message for the next page

```go
func (Notes) Create(c *web.Ctx, in NoteInput) (web.Responder, error) {
	// Invalid input never gets here: the browser is sent back to the form,
	// which shows the errors and the submitted values.
	if err := db.Create(c, &Note{Title: in.Title, Body: in.Body}); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Note created.")
	return web.RedirectRoute("notes.index"), nil
}
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `create`.)

A flashed value is there for this request and the next one, then gone.
The layout shows it with `view.Flash(ctx, "status")`. `s.Keep(keys...)`
and `s.Reflash()` keep flashed values for one more request.

### 4. Log in and out safely

```go
// illustrative
s.Regenerate()  // after login: new session ID and CSRF token
s.Invalidate()  // on logout: everything removed, new ID
```

[Authentication](authentication.md) calls these for you (`a.Login` and
`a.Logout`); call them yourself if you sign users in another way. With a
server-side driver it matters more: a session cookie planted in a
victim's browser before they sign in would otherwise share their
signed-in session. With the default cookie
driver, `Invalidate` empties the session in this browser only: the session
lives in the cookie, so a copy of an earlier cookie (stolen, or saved
before logout) keeps working until it expires, after `SESSION_TTL`
without use and in any case `SESSION_MAX_TTL` (7 days by default)
after it started or was last regenerated. With a server-side driver (next
step), `Invalidate` and `Regenerate` remove the old session from the
store, so every copy of the old cookie stops working, and a request that
was still running with the old session (a slow form post with a stolen
cookie) doesn't bring it back when it ends.

### 5. Keep sessions on the server

Set `SESSION_DRIVER` to keep sessions in the database or Redis instead of
the cookie. The cookie then holds only the encrypted session ID: sessions
can be revoked, and hold more than 4 KB.

For the database, add the sessions table to the migrations and run
`migrate`:

```go
// session.Migrations creates the sessions table, for SESSION_DRIVER=database.
if _, err := migrate.New(app, []*migrate.Set{Migrations, session.Migrations("")}, migrate.WithSeeders(Seeders...)); err != nil {
	return nil, err
}
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `runner`.)

```env
SESSION_DRIVER=database
```

For Redis, add the `drivers/redis` module and pass its driver
(`REDIS_URL` says where the server is):

```go
// illustrative
sessions, err := session.New(app, redis.SessionDriver()) // SESSION_DRIVER=redis
```

Changing the driver ends every current session: visitors sign in again.

### Rotate the key

Move the current key to `APP_PREVIOUS_KEYS` and set a new `APP_KEY`.
Sessions encrypted with the old key keep working and are re-encrypted with
the new one the next time their cookie is written; remove the old key after
`SESSION_TTL`.

## How it works

The whole session is in the cookie, encrypted and authenticated with
AES-256-GCM under a key derived from `APP_KEY` for each cookie value, so there is
nothing to store on the server and a visitor can neither read nor change
it. The cookie is `HttpOnly`, `SameSite=Lax` and, outside development,
`Secure`; a Secure cookie is named `__Host-anetos_session`, so no other
site, subdomains included, can set it. The cookie holds the times the
session started and was last used: after `SESSION_TTL` without a
request, or `SESSION_MAX_TTL` in all, the session is empty, whatever
the browser sends. Responses to requests with a session get
`Cache-Control: private` (unless the handler sets `Cache-Control`) and
`Vary: Cookie`, so shared caches don't store them.

A new visitor gets no cookie until something is stored. After that the
cookie is written when the session changes, and at most every tenth of
the lifetime to keep it alive. Changes made after the response has
started (streaming) aren't saved.

Browsers limit cookies to about 4 KB. Store IDs, not records. If a failed
form's input doesn't fit, it is dropped (the errors are kept) and a warning
is logged; a session that doesn't fit isn't saved and an error is logged.

With a server-side driver, the cookie holds the session ID, encrypted
like a cookie session. The store holds the session encrypted with
`APP_KEY`, under a hash of the ID and without the ID itself, so reading
the store (or a database backup) gives no one a usable session or its
contents. It expires after `SESSION_TTL` without use. Keys start
with `SESSION_PREFIX` (default `APP_NAME:session:`), which `cache:clear`
leaves alone unless `CACHE_PREFIX` is set to a start of it. A saved
session is only replaced while it exists: a session ended meanwhile (by a
logout) stays ended.

A server-side session keeps a failed form's input up to 64 KB (larger
input is dropped, the errors are kept) and holds at most 1 MB; a larger
session isn't saved and an error is logged. If the store can't be read,
the request gets 503 Service Unavailable (through the app's error pages)
rather than an empty session, which would log the visitor out; if it
can't be written, the response goes out and an error is logged. If
removing a session at logout fails, it is logged, and copies of its cookie
keep working until it expires. Two requests of one visitor running at the
same time each save their own changes: the last one wins.

> **Coming from Laravel?** `session()->put/get/flash/regenerate/invalidate`
> map to `Put`, `Get`/`Value`, `Flash`, `Regenerate` and `Invalidate`;
> The default is Laravel's `cookie` session driver with an encrypted
> cookie; `database` and `redis` match Laravel's drivers of those names.

## Testing it

Give a handler a session without the middleware:

```go
// illustrative
s := session.NewSession()
ctx := session.NewContext(context.Background(), s)
```

Across requests, an `anetostest` app keeps the session cookie: check the
session after a response with `res.AssertSessionHas("status", "Saved.")`,
and set values before a request with `app.WithSession(func(s
*session.Session) { … })`. See [Test your app](testing.md#2-test-a-form).

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `APP_KEY is not set` at startup | No key configured | `go tool anetos key:generate >> .env` |
| Everyone was logged out after a deploy | `APP_KEY` changed; or `SESSION_COOKIE` or `SESSION_SECURE` changed, which renames the cookie | Keep the old key in `APP_PREVIOUS_KEYS`; use the same key and session settings on every instance |
| The session is empty on every request in development | `SESSION_SECURE=true` over plain HTTP, or a custom dev hostname | Leave `SESSION_SECURE` unset in development, or use HTTPS |
| `session too large for its cookie` in the logs | More than about 4 KB stored | Store less: IDs instead of records |
| `web: no session for this request` | `c.Session()` on a route without the middleware | Add `sessions.Middleware` to the route's group |
| Every page answers 503, with `the session store failed` in the logs | The database or Redis server isn't reachable | Check the server; `SESSION_DRIVER=cookie` needs none |
| `SESSION_DRIVER is "redis", but the drivers are [cookie, database]` | The Redis driver wasn't passed | `session.New(app, redis.SessionDriver())` |
| `no such table: sessions` | The sessions migration didn't run | Add `session.Migrations("")` to the runner and run `migrate` |

## Next steps

- [Authentication](authentication.md)
- [Handle HTML forms](forms.md)
- [Views, sessions and forms reference](../reference/views.md)
