---
title: Views, sessions and forms reference
since: v0.1.0
---

# Views, sessions and forms reference

The APIs of packages `view`, `session` and `encryption`, and the HTML
helpers of package `web`. See [Render HTML with templ](../guides/views.md),
[Sessions and flash messages](../guides/sessions.md) and
[Handle HTML forms](../guides/forms.md) for walkthroughs.

## Rendering (`web`)

| API | Does |
|---|---|
| `c.Render(status, component)` | Renders a component (templ or `view.Component`) into a buffer and writes it as `text/html; charset=utf-8`; a render error becomes an error response |
| `web.View(component)` | Responder rendering the component with 200 |
| `web.URL(ctx, name, args...)` | `(string, error)`: path of a named route, from any request context (use in components) |
| `c.IsHTMX()` | Whether `HX-Request: true`; adds `Vary: HX-Request` |
| `c.HTMX()` | `web.HTMX`: `Request`, `Boosted`, `HistoryRestore`, `Target`, `Trigger`, `TriggerName`, `CurrentURL`; adds `Vary: HX-Request` |
| `c.Back()`, `web.Back()` | 303 to the `Referer` if it is on this site, else `/` |
| `c.Session()` | The request's `*session.Session`; panics without the session middleware |
| `web.WriteError(w, r, err)` | Sends err through the router's error handler, for middleware |

## Form protection (`web`)

| API | Does |
|---|---|
| `web.CSRF(opts...)` | Middleware: for POST, PUT, PATCH, DELETE, rejects cross-site requests (`web.ErrCrossOrigin`, 403) and requires the session token from `_token` or `X-CSRF-Token` (`web.ErrCSRF`, 403). Needs the session middleware first |
| `web.TrustedOrigins(origins...)` | CSRF option: allow these origins (`https://admin.example.com`) |
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
| `a` as `http.Handler` | GET/HEAD; `Cache-Control: public, max-age=31536000, immutable` with the current hash, else `no-cache`; `ETag` |
| `htmx.FS`, `htmx.Version` | The bundled `htmx.min.js` (package `view/htmx`) |

## Sessions (`session`)

| API | Does |
|---|---|
| `session.ForApp(app)` | Manager from `SESSION_*` and `APP_KEY` ([settings](configuration.md#sessions)) |
| `session.NewManager(cfg, enc, opts...)` | Manager from a `session.Config` and an `*encryption.Encrypter`; `session.WithLogger` |
| `m.Middleware` | Loads the session into the request context and saves it when the response starts; adds `Cache-Control: private` (if unset) and `Vary: Cookie` for requests with a session. Does nothing if the same manager already runs for the request |
| `m.CookieName()` | `SESSION_COOKIE`, with the `__Host-` prefix when Secure, without Domain, with Path `/` |
| `session.From(ctx)` | The session, or nil |
| `session.New()`, `session.NewContext(ctx, s)` | A session for tests |
| `s.Put(key, v)`, `s.Get(key, &dst)`, `session.Value[T](s, key)`, `s.String(key)` | Store (as JSON) and read values |
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
| `encryption.ForApp(app)` | Encrypter from `APP_KEY` and `APP_PREVIOUS_KEYS`; the error for a missing key suggests one |
| `encryption.New(current, previous...)` | Encrypter from 32-byte keys |
| `e.Encrypt(plain, context)`, `e.Decrypt(ct, context)` | AES-256-GCM with a per-message key (HKDF-SHA256, random salt); context is authenticated. `encryption.ErrInvalid` for tampered, foreign-context or unknown-key messages |
| `e.EncryptString`, `e.DecryptString` | Same, as URL-safe base64 |
| `encryption.GenerateKey()`, `encryption.ParseKey(s)` | `base64:…` keys |
| `go tool anetos key:generate` | Prints `APP_KEY=base64:…` |
