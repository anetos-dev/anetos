---
title: Views, sessions and forms reference
since: v0.1.0
group: "Web"
weight: 201
---

# Views, sessions and forms reference

The APIs of packages `view`, `session` and `encryption`, and the HTML
helpers of package `web`. See [Render HTML with templ](../guides/views.md),
[Sessions and flash messages](../guides/sessions.md) and
[Handle HTML forms](../guides/forms.md) for walkthroughs. The components
of a project's `views/ui` are in the [UI components reference](ui.md).

## Rendering (`web`)

| API | Does |
|---|---|
| `c.Render(status, component)` | Renders a component (templ or `view.Component`) into a buffer and writes it as `text/html; charset=utf-8`; a render error becomes an error response |
| `web.Render(component)` | Responder rendering the component with 200 |
| `web.URL(ctx, name, args...)` | `(string, error)`: path of a named route, from any request context (use in components); a trailing `url.Values` argument becomes the query string |
| `web.MustURL(ctx, name, args...)` | The path of `web.URL` alone, for a component's string argument (`@ui.LinkButton(web.MustURL(ctx, "posts.edit", post.ID), ui.Secondary)`); panics on an unknown route name or arguments the route doesn't take, which the router answers with a 500 (v0.5) |
| `web.RouteIs(ctx, names...)` | Whether the request's route has one of the names; `"issues.*"` matches the names starting with `issues.`. False outside a request or for an unnamed route (v0.3). The layout of `anetos new` marks the current page's link with it |
| `web.PageURL(ctx, page)` | A relative link (`?…&page=N`) to page N of the current list, keeping the request's other query parameters as written; `?page=N` outside a request |
| `c.IsHTMX()` | Whether `HX-Request: true`; adds `Vary: HX-Request` |
| `c.HTMX()` | `web.HTMX`: `Request`, `Boosted`, `HistoryRestore`, `Target`, `Trigger`, `TriggerName`, `CurrentURL`; adds `Vary: HX-Request` |
| `c.Back()`, `web.Back()` | 303 to the `Referer` if it is on this site, else `/` |
| `c.Session()` | The request's `*session.Session`; panics without the session middleware |
| `web.WriteError(w, r, err)` | Sends err through the router's error handler, for middleware |

## Form protection (`web`)

| API | Does |
|---|---|
| `web.CSRF(opts...)` | Middleware: for every method but GET, HEAD, OPTIONS and TRACE, rejects cross-site requests (`web.ErrCrossOrigin`, 403) and requires the session token from `_token` or `X-CSRF-Token` (`web.ErrCSRF`, 403). Needs the session middleware first |
| `web.WithTrustedOrigins(origins...)` | CSRF option: allow these origins (`https://admin.example.com`) |
| `web.MethodOverride` | Global middleware: a POST with `_method` of PUT, PATCH or DELETE (in a URL-encoded body of at most 10 MB, which is parsed into `r.PostForm` and restored, or in the query string) is routed with that method |

Validation failures (422, or 400 with field errors) of a non-GET request
from a browser (a navigation or `Accept: text/html`; not htmx unless
boosted), on a route with a session, are redirected back to the `Referer`
by `web.DefaultErrorHandler` with the errors and `r.PostForm` flashed.
Form posts key errors by `form` name where it differs from the `json` name.

## View helpers (`view`)

| Helper | Returns |
|---|---|
| `view.CSRFField(ctx)` | Component: `<input type="hidden" name="_token" value="…">`; render error `view.ErrNoSession` without a session |
| `view.CSRFToken(ctx)` | A token for the `X-CSRF-Token` header (`""` without a session) |
| `view.MethodField(method)` | Component: `<input type="hidden" name="_method" value="PUT">`; PUT, PATCH or DELETE |
| `view.Errors(ctx)` | `*validate.Errors` flashed by the previous request (never nil): `Has`, `Get`, `Keys`, `Len` |
| `view.Old(ctx, field, fallback...)` | The value submitted for field by the previous request, else the fallback or `""` |
| `view.OldChecked(ctx, field, fallback)` | For a checkbox: after a post that redirected back, whether field was sent (and not `0`, `false`, `off`); else fallback |
| `view.Flash(ctx, key)` | The flashed string under key, or `""` |
| `view.String(ctx, component)` | The rendered HTML, for tests and emails |
| `view.Template(t, name, data)` | An `html/template` template as a component (without the request context: pass what it needs in data) |
| `view.ComponentFunc` | A function as a component |

## Static files (`view.Assets`)

| API | Does |
|---|---|
| `view.NewAssets(prefix, fsys...)` | Hashes every file (first file system wins; dot files skipped) |
| `a.URL(name)` | `prefix/name?v=<hash>`; no hash for a missing file |
| `a.Has(name)` | Whether the file exists |
| `a` as `http.Handler` | GET/HEAD; `Cache-Control: public, max-age=31536000, immutable` with the current hash, else `no-cache`; `ETag`. A text file of 1 KiB or more (`text/*` such as CSS, JavaScript, JSON, SVG, XML, wasm) is gzipped once, the first time it's requested, and served gzipped to a client that accepts it (`Content-Encoding: gzip`, `Vary: Accept-Encoding`, its own `ETag`), unless gzip saves less than a tenth (v0.5) |
| `htmx.FS`, `htmx.Version` | The bundled `htmx.min.js` (package `view/htmx`) |

## Sessions (`session`)

| API | Does |
|---|---|
| `session.New(app, drivers...)` | Manager from `SESSION_*` and `APP_KEY` ([settings](configuration.md#sessions)); `SESSION_DRIVER` picks cookie, database or a passed driver (`redis.SessionDriver()`) |
| `session.Migrations(table)` | The database driver's table, for `migrate.New` |
| `session.NewManager(cfg, enc, opts...)` | Manager from a `session.Config` and an `*encryption.Encrypter`; `session.WithLogger`, `session.WithStore(store, prefix)` (any `cache.Store`) |
| `m.Middleware` | Loads the session into the request context and saves it when the response starts; adds `Cache-Control: private` (if unset) and `Vary: Cookie` for requests with a session. Does nothing if the same manager already runs for the request |
| `m.Use(mw...)` | Middleware that run inside `m.Middleware`, wherever it runs (every group with sessions), after the session is loaded and before the group's other middleware, in order; for routes registered before and after. Call it at setup. `make:auth`'s `setupAuth` calls `sessions.Use(a.Middleware)`, so every page knows the logged-in user (v0.3) |
| `m.CookieName()` | `SESSION_COOKIE`, with the `__Host-` prefix when Secure, without Domain, with Path `/` |
| `session.From(ctx)` | The session, or nil |
| `session.NewSession()`, `session.WithSession(ctx, s)` | A session for tests |
| `s.Put(key, v)`, `s.Get(key, &dst)`, `session.Value[T](s, key)`, `s.GetString(key)` | Store (as JSON) and read values |
| `s.Has`, `s.Delete`, `s.Pull`, `s.Clear` | Check, remove, read-and-remove, remove all |
| `s.Flash(key, v)`, `s.Keep(keys...)`, `s.Reflash()` | Values for the next request only |
| `s.Regenerate()`, `s.Invalidate()` | New ID and token, restarting the maximum lifetime (login); remove everything (logout, in this browser only) |
| `s.Token()`, `s.VerifyToken(t)`, `s.RegenerateToken()` | Masked CSRF token |
| `s.FlashErrors(...)`, `s.Errors()`, `s.FlashInput(values)`, `s.OldInput()`, `s.Old(field)` | Form errors and input for the next request; `FlashInput` leaves out `_method` and fields whose name contains `password`, `secret` or `token` |
| `s.ID()` | Random session ID |

`Put` panics for values `encoding/json` can't encode. Session methods are
safe for concurrent use.

## Encryption (`encryption`)

| API | Does |
|---|---|
| `encryption.New(app)` | Encrypter from `APP_KEY` and `APP_PREVIOUS_KEYS`; the error for a missing key suggests one |
| `encryption.NewEncrypter(current, previous...)` | Encrypter from 32-byte keys |
| `e.Encrypt(plain, context)`, `e.Decrypt(ct, context)` | AES-256-GCM with a per-message key (HKDF-SHA256, random salt); context is authenticated. `encryption.ErrInvalid` for tampered, foreign-context or unknown-key messages |
| `e.EncryptString`, `e.DecryptString` | Same, as URL-safe base64 |
| `encryption.GenerateKey()`, `encryption.ParseKey(s)` | `base64:…` keys |
| `go tool anetos key:generate` | Sets a new `APP_KEY` in `.env` (`--show` prints one) |
