---
title: Sessions and flash messages
since: v0.1.0
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
sessions, err := session.ForApp(app) // SESSION_* settings; needs APP_KEY
if err != nil {
	return nil, err
}
r := srv.Router()
r.UseGlobal(web.MethodOverride) // forms can send PUT and DELETE with _method
r.HandleStd(http.MethodGet, "/assets/{path...}", assets)

h := Notes{store: NewStore()}
pages := r.Group("", sessions.Middleware, web.CSRF())
pages.Get("/", func(c *web.Ctx) error { return c.RedirectRoute("notes.index") })
pages.Get("/notes", h.Index).Name("notes.index")
pages.Get("/notes/new", h.New).Name("notes.new")
pages.Post("/notes", web.H(h.Create)).Name("notes.store")
pages.Get("/notes/{id}/edit", web.H(h.Edit)).Name("notes.edit")
pages.Put("/notes/{id}", web.H(h.Update)).Name("notes.update")
pages.Delete("/notes/{id}", web.H(h.Delete)).Name("notes.delete")
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `routes`.)

`session.ForApp` reads the `SESSION_*` settings
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
func (h Notes) Create(c *web.Ctx, in NoteInput) (web.Responder, error) {
	// Invalid input never gets here: the browser is sent back to the form,
	// which shows the errors and the submitted values.
	h.store.Add(in.Title, in.Body)
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

Authentication (v0.2) calls these for you. `Invalidate` empties the
session in this browser. Because the session lives in the cookie, a copy
of an earlier cookie (stolen, or saved before logout) keeps working until
it expires: after `SESSION_LIFETIME` without use, and in any case
`SESSION_MAX_LIFETIME` (7 days by default) after it started or was last
regenerated. Server-side stores, which can revoke sessions, arrive in
v0.2.

### Rotate the key

Move the current key to `APP_PREVIOUS_KEYS` and set a new `APP_KEY`.
Sessions encrypted with the old key keep working and are re-encrypted with
the new one the next time their cookie is written; remove the old key after
`SESSION_LIFETIME`.

## How it works

The whole session is in the cookie, encrypted and authenticated with
AES-256-GCM under a key derived from `APP_KEY` for each cookie value, so there is
nothing to store on the server and a visitor can neither read nor change
it. The cookie is `HttpOnly`, `SameSite=Lax` and, outside development,
`Secure`; a Secure cookie is named `__Host-anetos_session`, so no other
site, subdomains included, can set it. The cookie holds the times the
session started and was last used: after `SESSION_LIFETIME` without a
request, or `SESSION_MAX_LIFETIME` in all, the session is empty, whatever
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
Server-side stores (database, Redis) arrive in v0.2.

> **Coming from Laravel?** `session()->put/get/flash/regenerate/invalidate`
> map to `Put`, `Get`/`Value`, `Flash`, `Regenerate` and `Invalidate`;
> this is Laravel's `cookie` session driver with an encrypted cookie.

## Testing it

Give a handler a session without the middleware:

```go
// illustrative
s := session.New()
ctx := session.NewContext(context.Background(), s)
```

To test across requests, send the cookie back, as
[`examples/forms`](../../../examples/forms/main_test.go) does with a
cookie jar (set `APP_KEY` in the test's configuration).

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `APP_KEY is not set` at startup | No key configured | `go tool anetos key:generate >> .env` |
| Everyone was logged out after a deploy | `APP_KEY` changed | Keep the old key in `APP_PREVIOUS_KEYS`; use the same key on every instance |
| The session is empty on every request in development | `SESSION_SECURE=true` over plain HTTP, or a custom dev hostname | Leave `SESSION_SECURE` unset in development, or use HTTPS |
| `session too large for its cookie` in the logs | More than about 4 KB stored | Store less: IDs instead of records |
| `web: no session for this request` | `c.Session()` on a route without the middleware | Add `sessions.Middleware` to the route's group |

## Next steps

- [Handle HTML forms](forms.md)
- [Views, sessions and forms reference](../reference/views.md)
