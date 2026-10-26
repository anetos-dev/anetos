---
title: Views, sessions and forms reference
since: v0.1.0
group: "Web"
weight: 201
---

# Views, sessions and forms reference

The APIs of packages `view`, `view/vite`, `web/inertia`, `session` and `encryption`, and the HTML
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
| `web.MustURL(ctx, name, args...)` | The path of `web.URL` alone, for a component's string argument (`@ui.LinkButton(web.MustURL(ctx, "posts.edit", post.ID), ui.Secondary)`); panics on an unknown route name or arguments the route doesn't take, which the router answers with a 500 |
| `web.RouteIs(ctx, names...)` | Whether the request's route has one of the names; `"issues.*"` matches the names starting with `issues.`. False outside a request or for an unnamed route. The layout of `anetos new` marks the current page's link with it |
| `web.PageURL(ctx, page)` | A relative link (`?…&page=N`) to page N of the current list, keeping the request's other query parameters as written; `?page=N` outside a request |
| `c.IsHTMX()` | Whether `HX-Request: true`; adds `Vary: HX-Request` |
| `c.HTMX()` | `web.HTMX`: `Request`, `Boosted`, `HistoryRestore`, `Target`, `Trigger`, `TriggerName`, `CurrentURL`; adds `Vary: HX-Request` |
| `c.Back()`, `web.Back()` | 303 to the `Referer` if it is on this site, else `/` |
| `c.Session()` | The request's `*session.Session`; panics without the session middleware |
| `web.WriteError(w, r, err)` | Sends err through the router's error handler, for middleware |

## Form protection (`web`)

| API | Does |
|---|---|
| `web.CSRF(opts...)` | Middleware: for every method but GET, HEAD, OPTIONS and TRACE, rejects cross-site requests (`web.ErrCrossOrigin`, 403) and requires the session token from `_token`, `X-CSRF-Token` or `X-XSRF-TOKEN` (`web.ErrCSRF`, 403). Needs the session middleware first |
| `web.WithTrustedOrigins(origins...)` | CSRF option: allow these origins (`https://admin.example.com`) |
| `web.WithXSRFCookie()` | CSRF option (since v0.6): when the response starts, set an `XSRF-TOKEN` cookie holding the session's masked token, readable by the page's scripts, unless the request's holds that token (so a login's response carries the new one); scripts send it back in `X-XSRF-TOKEN` (Inertia's client, Axios and Angular do). The header is accepted with or without the option |
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
| `a` as `http.Handler` | GET/HEAD; `Cache-Control: public, max-age=31536000, immutable` with the current hash, else `no-cache`; `ETag`. A text file of 1 KiB or more (`text/*` such as CSS, JavaScript, JSON, SVG, XML, wasm) is gzipped once, the first time it's requested, and served gzipped to a client that accepts it (`Content-Encoding: gzip`, `Vary: Accept-Encoding`, its own `ETag`), unless gzip saves less than a tenth |
| `htmx.FS`, `htmx.Version` | The bundled `htmx.min.js` (package `view/htmx`) |

## Frontend build (`view/vite`)

Since v0.6. See [Bundle JavaScript and CSS with Vite](../guides/vite.md).

| API | Does |
|---|---|
| `vite.New(app, files, opts...)` | `(*Vite, error)`: reads `build/manifest.json` from the web root (`public.Files`) and puts the Vite in the app's contexts and container. Errors: nil files, a second call for the app, a manifest that isn't JSON, and, where the app is deployed, a missing manifest or `VITE_DEV_URL` (see [configuration](configuration.md#vite)) |
| `vite.WithReactRefresh()` | `New` option: in development, the tags start with the preamble `@vitejs/plugin-react` needs for a React frontend's hot reload |
| `vite.Tags(entries...)` | Component: from the build, the entries' stylesheets and those of the chunks they import, their module scripts (a stylesheet link for a CSS entry) and `modulepreload` links for the chunks they import, each once; with `VITE_DEV_URL`, `@vite/client` and the entries' sources there; without either, an HTML comment. Render error for an entry the manifest lacks, and `vite.ErrNoVite` without a Vite in the context |
| `vite.URL(ctx, src)` | A frontend file's URL by its source path (`src/logo.svg`): the built file's, or the development server's; `""` when the build has no such file |
| `vite.Handler()` | Serves the build under `/build/` (`vite.Base`): GET and HEAD; files under `assets/` (named by their content's hash) `Cache-Control: public, max-age=31536000, immutable`, others `no-cache`; `ETag`; text of 1 KiB or more gzipped as `view.Assets` does. Not the manifest or dot files; 404 without a Vite in the request's context |
| `vite.From(ctx)`, `vite.WithVite(ctx, v)` | The context's Vite (or `vite.ErrNoVite`); a context carrying v |
| `vite.NewWithFiles(files, devURL, opts...)` | A Vite without an app, for tests and tools; `v.Tags`, `v.URL`, `v.Dev()` and `v` as an `http.Handler` are the package's functions with it |
| `v.Version()` | A hash of the build's manifest, `""` in development and without a build: Inertia's asset version |
| `vite.LoadConfig(src)`, `vite.Config` | The `VITE_*` settings |

## Inertia pages (`web/inertia`)

Since v0.6. See [Pages in Vue, React or Svelte with Inertia](../guides/inertia.md).

| API | Does |
|---|---|
| `inertia.New(app, root, opts...)` | `(*Inertia, error)`: the app's Inertia, with the root view (the first visit's HTML, which places `@inertia.App()`), in the app's contexts and container. The asset version is the Vite build's when the app has one (call `vite.New` first). Errors: a nil root, an invalid option, a second call |
| `inertia.WithVersion(v)` | Option: the asset version; a client's GET visit with another one gets 409 and reloads the page whole |
| `inertia.WithShared(fn)` | Option: props for every page, `fn(c)`'s result (a struct, as a page's props, or `inertia.Props`); a page's prop of the same name wins. Listed in the page's `sharedProps` |
| `inertia.WithEncryptHistory()` | Option: encrypt the pages' history state (HTTPS only) |
| `inertia.WithPages(pages...)` | Option: the app's pages, whose props get TypeScript types (`web/typescript`); in development and tests, once the app lists pages, rendering a page it doesn't list is an error. `inertia.AnyPage` is any `*inertia.Page[P]` |
| `inertia.NewPage[P](component)` | A page: its component name and props type, read once. Props are the fields as `encoding/json` writes them (`json` names, `omitempty`, `omitzero`, embedded structs, the shallowest field of a name). Panics on an empty name, a `P` that isn't a struct or `inertia.Props`, two fields tagged with one name at one depth, or a prop named `errors` |
| `p.Render(c, props)` | Responds 200 with the page: JSON for a visit of the client (`X-Inertia: true`), the root view's HTML otherwise. Props are the fields, as `encoding/json` names them, the shared props, and `errors` |
| `inertia.Render(c, component, props)` | The same with untyped `inertia.Props` |
| `inertia.Prop[T]` | A prop field loaded specially; unset, it is left out (a shared prop of its name stays). Set by `inertia.Value(v)`, `Func(fn)` (computed when included), `Always(v)`, `Optional(fn)` (only when asked for), `Defer(fn)` (after the page shows), `Merge(v)`, `Once(fn)` |
| `.Group(name)`, `.Rescue()` | A deferred prop's group (`default`), and leaving it out (logged, in `rescuedProps`) when `fn` fails |
| `.Append(paths...)`, `.Prepend(paths...)`, `.DeepMerge()`, `.MatchOn(paths...)` | How the client merges the value on a partial reload; paths inside the prop (`"data"`, `"data.id"`), none for the prop itself. `DeepMerge` replaces appending and prepending |
| `.Once()`, `.OnceAs(key)`, `.ExpiresIn(d)`, `.Until(t)`, `.Fresh()` | Kept by the client once loaded, under key (the prop's name by default), until it expires; `Fresh` sends it anyway |
| `inertia.Middleware` | For the client's visits: 409 with `X-Inertia-Location` on an old asset version (GET; flashed data kept), 303 for a 302 after PUT, PATCH or DELETE, 409 with `X-Inertia-Redirect` for a redirect to a URL with a fragment (not for prefetches), the error bag of `X-Inertia-Error-Bag`; `Vary: X-Inertia` on every response |
| `inertia.Location(c, url)` | A full page load of `url`: 409 with `X-Inertia-Location` for the client, 303 otherwise |
| `inertia.Flash(ctx, key, v)` | Data in `flash` of the next page rendered, here or after a redirect |
| `inertia.ClearHistory(ctx)`, `inertia.PreserveFragment(ctx)` | Flags of the next page rendered: drop the history encryption key; keep the URL's fragment across the redirect |
| `inertia.EncryptHistory(ctx)` | Encrypt this response's history state. It, `Flash`, `ClearHistory` and `PreserveFragment` need `inertia.Middleware`, and do nothing without it |
| `inertia.ErrorPages(component)` | For `r.ErrorPages`: the error pages as the component with `inertia.ErrorProps` (`status`, `title`, `detail`, `requestId`) and the error's field errors in `errors`, with the error's status |
| `inertia.App()` | Component for the root view's body: `<script data-page="app" type="application/json">` with the page object (`/` escaped), and `<div id="app">` |
| `inertia.Component(ctx)` | The root view's page component, `""` elsewhere |
| `inertia.ReadPage(body)` | A response's `*inertia.PageObject` (JSON or the first visit's HTML), for tests |
| `inertia.From(ctx)`, `inertia.WithInertia(ctx, in)`, `inertia.NewWithRoot(root, opts...)` | The context's Inertia (or `inertia.ErrNoInertia`); a context carrying one; one without an app (refusing unlisted pages, as in development) |
| `in.Pages()`, `in.SharedTypes()`, `p.PropsType()`, `inertia.PropType(t)` | For tools such as `web/typescript`: the listed pages, the shared props' types, a page's props type, and `T` for an `inertia.Prop[T]` |

The `errors` prop is always there: the field errors the previous request
flashed, one message per field (`{"title": "…"}`), under the error bag
when the form named one. Server-side rendering, infinite scroll and
Precognition aren't supported.

In tests, `app.AsInertia()` makes later requests the client's visits,
and `res.AssertInertia(component)`, `res.AssertInertiaProp(name, want)`
and `res.InertiaPage()` read the page (package `anetostest`).

## Sessions (`session`)

| API | Does |
|---|---|
| `session.New(app, drivers...)` | Manager from `SESSION_*` and `APP_KEY` ([settings](configuration.md#sessions)); `SESSION_DRIVER` picks cookie, database or a passed driver (`redis.SessionDriver()`) |
| `session.Migrations(table)` | The database driver's table, for `migrate.New` |
| `session.NewManager(cfg, enc, opts...)` | Manager from a `session.Config` and an `*encryption.Encrypter`; `session.WithLogger`, `session.WithStore(store, prefix)` (any `cache.Store`) |
| `m.Middleware` | Loads the session into the request context and saves it when the response starts; adds `Cache-Control: private` (if unset) and `Vary: Cookie` for requests with a session. Does nothing if the same manager already runs for the request |
| `m.Use(mw...)` | Middleware that run inside `m.Middleware`, wherever it runs (every group with sessions), after the session is loaded and before the group's other middleware, in order; for routes registered before and after. Call it at setup. `make:auth`'s `setupAuth` calls `sessions.Use(a.Middleware)`, so every page knows the logged-in user |
| `m.CookieName()` | `SESSION_COOKIE`, with the `__Host-` prefix when Secure, without Domain, with Path `/` |
| `m.NewCookie(name, value)` | A cookie that goes with the session's: its Path, Domain, Secure and SameSite, without an expiry, not HttpOnly (since v0.6; `web.WithXSRFCookie`'s) |
| `session.From(ctx)` | The session, or nil |
| `session.ManagerFrom(ctx)` | The manager whose middleware runs for the request, or nil (since v0.6) |
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
