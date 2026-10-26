---
title: Log in from your SPA with a session cookie
since: v0.6.0
group: "Frontends"
weight: 180
---

# Log in from your SPA with a session cookie

An API project's accounts ([Add accounts to an API](api-accounts.md))
log in with tokens. Your own single-page app, built with Vue, React,
Svelte or anything else, can log in with a session cookie instead: the
browser keeps it out of reach of the page's scripts, so a script
injected into the page can't steal the login, as it could a token kept
in JavaScript. Tokens stay for everything else: mobile apps, programs,
other people's sites.

The same endpoints answer both: a request from an origin listed in
`HTTP_SPA_ORIGINS` gets a session, CSRF protection and CORS with
credentials; any other request, and any request with a token (from
whatever origin), is answered as before, with tokens.

## Before you start

- An API project with accounts: `go tool anetos make:auth`
  ([Add accounts to an API](api-accounts.md)).
- An SPA on the **same site** as the API: the same host
  (`example.com`, served by the app or behind the same proxy), or
  another subdomain of one domain (`app.example.com` calling
  `api.example.com`). Browsers don't send cookies to another site
  (`app.example.net` calling `api.example.com`): such an SPA, like a
  mobile app, logs in with a token.

## Steps

### 1. List the SPA's origins

In `.env` for development, where the SPA runs on Vite's server:

```sh
HTTP_SPA_ORIGINS=http://localhost:5173
```

And in production's settings, the address your users open:

```sh
HTTP_SPA_ORIGINS=https://app.example.com
# The SPA on another subdomain than the API: the cookies go to both.
SESSION_DOMAIN=example.com
```

An origin is a scheme, a host and a port, without a path
(`https://app.example.com`); the app refuses to start with anything
else. Several are separated by commas.

`SESSION_DOMAIN` lets the SPA's scripts read the `XSRF-TOKEN` cookie
the API sets, and has a cost: the session cookie loses its `__Host-`
prefix, so any other subdomain of the domain can set one, and log a
visitor into an account of its own choosing. Where you can, serve the
SPA and the API from one origin instead (the app serving the SPA, or
one proxy in front of both), and leave `SESSION_DOMAIN` empty.

### 2. The routes take sessions for the SPA

`make:auth` already wires it, in `routes/auth.go`: for the SPA's
requests, the session middleware and CSRF protection, with the
`XSRF-TOKEN` cookie the SPA sends back in the `X-XSRF-TOKEN` header;
other requests skip both.

```go
spa := web.FirstParty(sessions.Middleware, web.CSRF(web.WithXSRFCookie()))
api := r.Group("/api/v1", spa, a.TokenMiddleware).As("api.")
```

(Copied from [`examples/bookmarks/routes/auth.go`](../../../examples/bookmarks/routes/auth.go), region `spa`.)

The routes you add to that group, or its `loggedIn` group, work for
both: `auth.Current` is the session's user or the token's.

### 3. Log in from the SPA

The handlers log the SPA in with its session, in
`app/handlers/auth.go`:

```go
// Login checks the email and password (throttled: AUTH_THROTTLE) and
// answers with a token; or, for a user with two-factor authentication on, with
// a challenge for POST /login/two-factor. The SPA (an origin of
// HTTP_SPA_ORIGINS) is logged in with its session instead: the answer is
// the user, or that a code is needed.
func (h Accounts) Login(c *web.Ctx, in LoginInput) (LoginResponse, error) {
	if web.IsFirstParty(c) {
		u, err := h.Auth.Attempt(c, in.Email, in.Password, false)
		if errors.Is(err, auth.ErrTwoFactorRequired) {
			return LoginResponse{TwoFactor: true}, nil // the session waits for the code
		}
		if err != nil {
			return LoginResponse{}, loginFailed(c, err)
		}
		return sessionResponse(u), nil
	}
```

(Copied from [`examples/bookmarks/app/handlers/auth.go`](../../../examples/bookmarks/app/handlers/auth.go), region `spa-login`.)

From the SPA, send every request with its cookies, and, for those that
change something (POST, PUT, PATCH, DELETE), the `XSRF-TOKEN` cookie's
value in the `X-XSRF-TOKEN` header. The client that
[`typescript.Register`](typescript.md) generates does both; with
`fetch`:

```ts
// illustrative
const xsrf = () =>
  decodeURIComponent(document.cookie.match(/(?:^|; )XSRF-TOKEN=([^;]*)/)?.[1] ?? '')

// When the SPA starts: who is logged in, and the XSRF-TOKEN cookie.
const me = await fetch('/api/v1/me', { credentials: 'include' })

const res = await fetch('/api/v1/login', {
  method: 'POST',
  credentials: 'include',
  headers: { 'Content-Type': 'application/json', 'X-XSRF-TOKEN': xsrf() },
  body: JSON.stringify({ email, password }),
})
```

With the generated client, on another subdomain than the API (the
routes are named `api.me`, `api.login`…, so the functions are under
`api`):

```ts
// illustrative
const client = createApi({ baseUrl: 'https://api.example.com', credentials: 'include' })
try {
  const user = await client.api.me()            // sets XSRF-TOKEN either way
} catch (e) {
  if (!(e instanceof ApiError && e.status === 401)) throw e // 401: logged out
}
await client.api.login({ email, password })     // { user } or { two_factor: true }
await client.api.loginTwoFactor({ code })       // { user }
await client.api.logout()
```

The endpoints answer the SPA as follows:

| Endpoint | The SPA gets |
|---|---|
| `POST /api/v1/register` | 201 and `{"user": …}`, logged in |
| `POST /api/v1/login` | `{"user": …}`; or `{"two_factor": true}` for a user with two-factor authentication, whose login waits in the session |
| `POST /api/v1/login/two-factor` | `{"code": …}` (no challenge): `{"user": …}` |
| `GET /api/v1/me` | The user, or 401 |
| `POST /api/v1/logout` | 204: the session ends |

The account's other endpoints (the password, two-factor
authentication, tokens for programs) are the same for both. Changing
the password or turning on two-factor authentication logs the user out
of their other sessions and revokes their tokens, keeping the session
that did it.

## Complete example

[`examples/bookmarks`](../../../examples/bookmarks) is the API that
`make:auth` and `make:crud` write; its `auth_test.go` logs in as its SPA
would.

## How it works

- **First-party requests.** The server marks a request as the SPA's
  when its `Origin` header, or without one its `Referer`'s origin, is
  in `HTTP_SPA_ORIGINS`, or when the browser says it comes from the same
  origin (`Sec-Fetch-Site`) and the request's host is listed; never a
  request with an API token (`Authorization: Bearer`), from any origin
  (`web.FirstPartyOrigins`). `web.IsFirstParty` reports it, and
  `web.FirstParty(...)` runs middleware for those requests only. Browsers set `Origin` themselves, so a page elsewhere
  can't pass for the SPA; a program can claim the origin, which gets it
  the session and CSRF checks of the SPA, not a token's access.
- **CORS.** The SPA's origins are allowed with credentials and the
  `X-XSRF-TOKEN` header, whatever `HTTP_CORS_*` says for other origins,
  which keep their own rules (without credentials unless
  `HTTP_CORS_CREDENTIALS`).
- **CSRF.** `web.CSRF` accepts the SPA's origins as its own and checks
  the session's token, which `web.WithXSRFCookie()` writes into the
  `XSRF-TOKEN` cookie of the SPA's responses when the request's cookie
  doesn't hold it already (readable by the page's scripts, with the
  session cookie's domain, path and security).
- **Sessions.** `make:auth` adds `session.New`: an encrypted cookie by
  default (`SESSION_DRIVER=cookie`), so the API needs no sessions table.
  `SESSION_DRIVER=database` keeps them on the server, where logging out
  ends a session for good: add `session.Migrations("")` to the
  migrations in `main.go` for its table.

## Testing it

Send the SPA's `Origin` header from the test client, which keeps the
cookies and sends the CSRF token:

```go
app := anetostest.New(t, setup, anetostest.Env(map[string]string{"AUTH_CLIENT_URL": authClient, "HTTP_SPA_ORIGINS": authClient}))
app.Freeze(time.Time{})
app.WithHeader("Origin", authClient) // as the SPA's browser sends it
```

(Copied from [`examples/bookmarks/auth_test.go`](../../../examples/bookmarks/auth_test.go), region `spa-test`.)

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| The login answers a token | The request's origin isn't in `HTTP_SPA_ORIGINS` (another port, `http` for `https`), or the request sends a token | List the origin as the browser's address bar shows it; don't send a token from the SPA |
| `403` "The page has expired. Reload it and try again." | The request has no `X-XSRF-TOKEN` header: the SPA didn't read the cookie, or can't, because it is on another subdomain | Send the header; set `SESSION_DOMAIN` to the shared domain |
| The browser doesn't send the cookie | `fetch` without `credentials: 'include'` (another origin), or the SPA is on another site | Add `credentials: 'include'`; on another site, log in with a token |
| Logged in, then `401` on the next request | `SESSION_SECURE` cookies over plain HTTP outside development | Serve over HTTPS |

## Next steps

- [Add accounts to an API](api-accounts.md): the endpoints and tokens.
- [Generate TypeScript types for the frontend](typescript.md): the
  client.
- [Bundle JavaScript and CSS with Vite](vite.md).
- [Settings reference](../reference/configuration.md): `HTTP_SPA_ORIGINS`
  and the `SESSION_*` settings.
