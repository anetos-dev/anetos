---
title: Protect your app in the browser
since: v0.6.0
group: "Production"
weight: 804
---

# Protect your app in the browser

A browser runs your pages' scripts, sends your cookies with every
request to your site, and lets other sites' pages send requests to it
too. Four things keep that safe: CSRF protection (other sites' pages
can't act as your visitors), the CORS settings (which other sites'
scripts may read your answers; by default, none), a
Content-Security-Policy (scripts injected into a page don't run), and
escaping (what users typed is shown as text, not run). This guide covers what each stack does by default and what you
add, for pages in templ with htmx, in React, Vue or Svelte with
Inertia, and for a single-page app that calls your API.

## Before you start

An app made with `anetos new`, of any stack. For the checklist before
you go live (HTTPS, secrets, the database's connection), see [Secure
your app](security.md).

## Steps

### 1. Keep other sites' requests out (CSRF)

A page on another site can send a form to yours, and the browser adds
your visitor's session cookie. `web.CSRF` stops that: a request that
can change something (any method but `GET`, `HEAD`, `OPTIONS` and
`TRACE`) is refused when the browser says it comes from another origin
(`Sec-Fetch-Site`, `Origin`), and needs the session's token. Each stack
sends the token its own way, and the generated code wires it (in an
API project, `make:auth` adds the SPA's part):

| Stack | The pages' group | How the token comes back |
|---|---|---|
| web (templ, htmx) | `web.CSRF()` | Forms have a `_token` field (`view.CSRFField`, in the `ui` form components); htmx sends the `X-CSRF-Token` header, from `hx-headers` on the layout's `<body>` |
| react, vue, svelte | `web.CSRF(web.WithXSRFCookie())` | The app sets an `XSRF-TOKEN` cookie, and Inertia's client sends it back in the `X-XSRF-TOKEN` header |
| api | none: tokens | A browser never adds an `Authorization` header on its own, so another site's page can't send a request with your user's token |
| api, with your SPA | `web.FirstParty(sessions.Middleware, web.CSRF(web.WithXSRFCookie()))` | Step 2 |

What you add:

- **Routes of your own** go in the group that has `web.CSRF`, after
  the session middleware: `route.csrf` in the [security
  check](security-check.md#routecsrf) finds a route that changes data
  with the session and no CSRF protection. In an API project, that is
  `routes/auth.go`'s `api` group, or its `loggedIn` group for routes
  that need a login; `make:crud` registers its routes in
  `routes/api.go`, open to every client and without sessions: move the
  call to `routes/auth.go`, as its comment says, for your SPA's
  logged-in user to reach them.
- **A script of your own** in a templ page sends the `X-CSRF-Token`
  header. The layout writes the token only into `hx-headers`; give
  your scripts a tag to read it from, such as `<meta name="csrf-token"
  content={ view.CSRFToken(ctx) }>` in the layout's `<head>`. In a
  React, Vue or Svelte page, use Inertia's router or forms, which send
  it.
- **A webhook** (a payment provider posting to you) goes in a group
  without sessions, so it needs no token: check its signature instead.
- **Another origin of yours** that posts to the app (an admin on
  `admin.example.com`): `web.CSRF(web.WithTrustedOrigins("https://admin.example.com"))`.

### 2. Log your SPA in with a cookie, not a token

A single-page app that keeps an API token in JavaScript loses it to any
script injected into the page. An SPA on your own site can log in with
the session cookie instead, which scripts can't read: list its origins
in `HTTP_SPA_ORIGINS`.

```sh
HTTP_SPA_ORIGINS=https://app.example.com
```

Its requests then get a session, CSRF protection with the
`XSRF-TOKEN` cookie, and CORS with credentials; `make:auth`'s API
accounts answer its login with the session instead of a token. Other
clients (mobile apps, programs, other sites) keep their tokens. List
the origins of production in production's settings: `setting.spa-origins`
warns of an `http://` or `localhost` origin where the app is deployed.

A cookie only reaches the API from the same site: the same host, or
another subdomain of one domain. On another subdomain,
`SESSION_DOMAIN` shares the cookies with it, and has a cost: any other
subdomain of the domain can then set a session cookie. Where you can,
serve the SPA and the API from one origin (the app serving the SPA, or
one proxy in front of both). [Log in from your SPA with a session
cookie](spa-login.md) has the steps and the code.

### 3. Allow other sites' scripts only where you mean to (CORS)

By default the app sends no CORS headers, so a script on another site
can send a request but can't read the answer. Your SPA's origins are
allowed already (step 2). For a public API that other sites' pages call
with tokens, list those sites:

```sh
HTTP_CORS_ORIGINS=https://partner.example.com,https://*.example.org
```

They are allowed without cookies (`HTTP_CORS_CREDENTIALS=false`, the
default), which is what token clients need: the token goes in the
`Authorization` header, which `HTTP_CORS_HEADERS` allows. Keep it that
way: with credentials, a listed site's scripts read your pages as your
logged-in visitors see them. The app refuses to start with
`HTTP_CORS_ORIGINS="*"` and credentials together; `*` alone is a
warning in the security check (`setting.cors`), and `--url` with
`--level=advanced` finds a site that echoes any origin back
(`live.cors`). The [settings reference](../reference/configuration.md#http-server)
lists the `HTTP_CORS_*` settings.

### 4. Add a Content-Security-Policy

A Content-Security-Policy tells the browser where scripts may come
from, so a `<script>` injected into a page doesn't run. Anetos doesn't
set one for your pages (the admin has its own): a policy has to fit
your scripts. This one fits the pages that `anetos new` and the
generators write, in every stack:

```go
// illustrative: in main.go, which imports "net/http" for it
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data:; object-src 'none'; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'self'"

// csp sends the policy with every response.
func csp(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}
```

And in `setup`, right after `web.NewServer`, before the routes:

```go
// illustrative
if !app.Config().Env.IsDevelopment() {
	srv.Router().UseGlobal(csp)
}
```

What it allows, stack by stack:

- **Scripts** come from the app only: the web stack's htmx from
  `/assets/`, the React, Vue and Svelte stacks' build from `/build/`,
  an SPA the app serves from its own paths. Inertia's page data is a
  `<script type="application/json">` element, which the browser reads
  as data and never runs, so the policy doesn't need to allow it.
- **Inline styles**, `'unsafe-inline'`: htmx adds a `<style>` element
  for its indicators, Inertia's client one for its progress bar, and
  `admin.Banner` (the bar on your pages while an admin acts as a user)
  has a `style` attribute. Inline scripts stay blocked. An SPA without
  Inertia or htmx works with `style-src 'self'`.
- **Images** from the app, and `data:` URLs: the icons Pico's and
  Bootstrap's CSS draw inline, and the SPA's empty icon.
- **Development** gets no policy: there, the scripts come from Vite's
  server at another address, React's refresh needs an inline script,
  and `anetos dev` adds an inline script that reloads the page after a
  change. Tests (`APP_ENV=testing`), staging and production get it.

Add the hosts of anything else your pages load
(`script-src 'self' https://cdn.example.com`, `connect-src 'self'
https://api.example.com` for requests to another host). Before you
ship, run the build with the policy (`go tool anetos build`, then the
binary with `APP_ENV=staging`) and open the browser's console: it names
each resource the policy blocks. To try a policy on the live site
without breaking pages, send it as
`Content-Security-Policy-Report-Only` first: the browser blocks
nothing, and writes what it would block in its console (each visitor's
own; a `report-to` directive and an endpoint of yours collect them).

The admin's pages send a stricter policy of their own, and the files
the storage handler serves that a browser could run (HTML, SVG) get
`Content-Security-Policy: sandbox` and a download: both replace yours.
An SPA served by a CDN or another server gets its policy from that
server.

### 5. Show what users typed as text (XSS)

Each stack escapes what it prints, so `<script>` typed into a form
shows as text. Each also has a way to write HTML as it is, for HTML
you trust:

| Stack | Escaped | Written as it is |
|---|---|---|
| web | `{ post.Title }` (templ) | `@templ.Raw(html)` |
| react | `{post.title}` | `dangerouslySetInnerHTML` |
| vue | `{{ post.title }}` | `v-html` |
| svelte | `{post.title}` | `{@html html}` |

Never write a user's text as it is. To keep some HTML from users (a
rich text editor's), sanitize it on the server, with an allow-list
sanitizer, before saving it. The security check's
[`source.xss`](security-check.md#sourcexss) finds request data given to
`templ.Raw`, `c.HTML` or `template.HTML` in Go code; it doesn't read
the frontend's code, so review `dangerouslySetInnerHTML`,
`v-html` and `{@html}` yourself (in a react, vue or svelte project,
the `anetos-security` skill reminds coding agents of its framework's).

Links need a check of their own: escaping a URL doesn't stop a
`javascript:` URL from running when a link to it is clicked. templ
replaces such a URL in `href` and `src` with a harmless one, unless you
pass it through `templ.SafeURL`; a link built in a JavaScript framework
may keep it. In every stack, validate a URL users give with the `url`
rule (`validate:"url"`), which accepts `http` and `https` only, before
saving it.

### 6. Keep the session cookie yours

The session cookie is `HttpOnly` (scripts can't read it), `Secure`
outside development and testing, `SameSite=Lax` (other sites' forms
and requests don't carry it; following a link to your site does),
and, when `Secure` and without `SESSION_DOMAIN`, named with the
`__Host-` prefix, so no other site, subdomains included, can set it.
Leave these as they are:

- `SESSION_SAME_SITE=strict` keeps the cookie off a link from another
  site too: a visitor who follows a link in an email arrives logged
  out. `none` sends it with other sites' requests: the security check
  warns of it (`setting.session-same-site`).
- `SESSION_DOMAIN` shares the cookie with every subdomain: set it
  only for an SPA on another subdomain (step 2).

The `XSRF-TOKEN` cookie isn't `HttpOnly`: the page's scripts read it,
by design. It holds the CSRF token, not a login.

Pages can't be framed by other sites either: `X-Frame-Options:
SAMEORIGIN`, from `web.NewServer`, and `frame-ancestors 'self'` in the
policy above.

## How it works

What each part of the app sends the browser:

| Header or cookie | Sent by | When |
|---|---|---|
| `X-Content-Type-Options`, `X-Frame-Options: SAMEORIGIN`, `Referrer-Policy`, `Cross-Origin-Opener-Policy` | `web.NewServer` (`web.SecureHeaders`) | Every response |
| `Strict-Transport-Security` | `web.NewServer` | In production |
| `Access-Control-Allow-*` | `web.NewServer` | With `HTTP_CORS_ORIGINS` or `HTTP_SPA_ORIGINS`, to those origins |
| The session cookie | `session.New`'s middleware | Pages and first-party SPAs |
| `XSRF-TOKEN` | `web.CSRF(web.WithXSRFCookie())` | When the request's cookie doesn't hold the session's token: the first page, and after a login |
| `Content-Security-Policy` | Your middleware (step 4); the admin's; the storage handler's `sandbox` | As you set it |
| `Cache-Control: no-store` | `auth.Require` | Logged-in pages |

`web.CSRF` checks the origin with Go's `http.CrossOriginProtection`,
then the token, from the `_token` field, the `X-CSRF-Token` header or
the `X-XSRF-TOKEN` header. A request from a first-party SPA passes the
origin check (`web.IsFirstParty`) and still needs the token. A request
without `Origin` or `Sec-Fetch-Site` (a program's, not a browser's)
needs the token too.

## Testing it

Tests run with `APP_ENV=testing`, so they get the policy:

```go
// illustrative
app := anetostest.New(t, setup)
app.Get("/").AssertOK().AssertHeader("Content-Security-Policy", contentSecurityPolicy)
```

The test client sends the session's token by itself, in the
`X-CSRF-Token` header, which `web.CSRF` reads first; to check that a
route is protected, send another one there, whatever the stack (a
forged `X-XSRF-TOKEN` would be ignored for the client's own):

```go
// illustrative
app.WithHeader("X-CSRF-Token", "forged").
	PostForm("/posts", url.Values{"title": {"Hello"}}).AssertForbidden()
```

In the browser, open the console on each kind of page:
a policy that blocks something says so there. After a deploy,
`go tool anetos security:check --url=https://…` checks the live site's
headers and cookies ([Check the running site](security-check.md#6-check-the-running-site)).

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `403` "Cross-origin request rejected." | The request came from a page on another origin | List your SPA in `HTTP_SPA_ORIGINS`, or another origin of yours with `web.WithTrustedOrigins` |
| `401` for an API's request from another origin, with the session cookie | An API project's routes take sessions only for `HTTP_SPA_ORIGINS`: another origin's request is a guest's, or a token client's | List the origin, if it's your SPA's |
| `403` "The page has expired. Reload it and try again." | No token, or not the session's: a page opened before a login in another tab, an expired session, a `fetch` without the header | Reload the page; send the `X-CSRF-Token` (templ pages) or `X-XSRF-TOKEN` (cookie) header from your scripts |
| The console says a script, style or image was refused | The policy doesn't allow its host | Add the host to the policy's directive the console names |
| The console reports a CORS error | The page's origin isn't listed, or the request sends cookies to an origin listed without credentials | List it in `HTTP_CORS_ORIGINS`; for your own SPA with cookies, in `HTTP_SPA_ORIGINS` |
| The SPA can't read `XSRF-TOKEN` | The SPA is on another subdomain than the API | `SESSION_DOMAIN` with the shared domain ([Log in from your SPA](spa-login.md#1-list-the-spas-origins)) |
| A webhook gets `403` | Its route has sessions and CSRF protection | Move it to a group without them, and check the sender's signature |

## Next steps

- [Secure your app](security.md): what Anetos protects, the tools, the
  checklist before going live.
- [Check your app's security](security-check.md): the rules, CI and
  the live site.
- [Log in from your SPA with a session cookie](spa-login.md).
- [Pages in Vue, React or Svelte with Inertia](inertia.md).
- [Settings reference](../reference/configuration.md): `HTTP_CORS_*`,
  `HTTP_SPA_ORIGINS`, `SESSION_*`.
