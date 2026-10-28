---
title: Build a single-page app
since: v0.6.0
group: "Frontends"
weight: 178
---

# Build a single-page app

Give an API project a single-page app (SPA): a frontend in React,
Svelte, Vue, or plain TypeScript or JavaScript, which Vite builds, the app's binary embeds
and serves at `/`, and which calls the API with a client generated from
the handlers' Go types. In development, Vite serves it with hot reload
and forwards `/api` to the app; the SPA logs in with a session cookie.

One binary then holds the API and the SPA: one origin, so no CORS and
no shared cookie domain, and one thing to deploy. For an SPA that lives
elsewhere (another repository, a CDN, a framework with its own server),
see [The SPA elsewhere](#the-spa-elsewhere).

## Before you start

- An API project (`anetos new shop --stack=api`; [Tutorial: build an
  API](../getting-started/build-an-api.md)), with accounts if the SPA
  logs in (`go tool anetos make:auth`; [Add accounts to an
  API](api-accounts.md)).
- [Node.js](https://nodejs.org) 20.19 or later in 20, or 22.12 or
  later, with npm (the packages you add may ask for more: React Router
  8, which `examples/spa` uses, needs 22.22 or later). Only the frontend
  needs it: the Go app doesn't, and neither do its tests.

## Steps

### 1. Add the SPA

In an API project, after `make:auth` if it has accounts:

```sh
go tool anetos vite:install             # TypeScript; --js for JavaScript
```

The SPA's page is written for the routes there are: run `make:auth`
first, and the page logs in; after it, the page shows the API's welcome
and stays so (write the login yourself, as `examples/spa` does). A new
project without accounts can have it from the start:

```sh
anetos new shop --stack=api --vite      # --vite=js for JavaScript
```

Either writes a working SPA without a framework, and wires it:

| File | What it holds |
|---|---|
| `frontend/package.json`, `tsconfig.json`, `go.mod` | Vite and TypeScript; the `go.mod` keeps the go command out of `node_modules` |
| `frontend/vite.config.ts` | Vite's address (`localhost:5173`), the forwarding of `/api` to the app, the build into `../public/spa` |
| `frontend/index.html`, `src/main.ts`, `src/style.css` | The page: with `make:auth`'s routes, a login (with two-factor codes), a registration form, a logout, and the `/verify-email` and `/reset-password` pages the account emails link to; without them, the API's welcome |
| `frontend/src/types/api.ts` | The API's client, written by `go run . types` (see [Generate TypeScript types](typescript.md)) |
| `public/public.go` | `public.Files`: the folder, with the build in `spa/`, embedded in the binary |
| `main.go` | `typescript.Register` (the client) and `vite.SPA` as the router's fallback (step 4) |
| `.env`, `.env.example` | Vite's address and `APP_URL` in `HTTP_SPA_ORIGINS`, so the SPA logs in with the session |
| `deploy/production.env.example` | `APP_URL` in `HTTP_SPA_ORIGINS` (and `AUTH_CLIENT_URL`: the account emails link to the SPA) |
| `.gitignore`, `.dockerignore`, `Dockerfile` | The build and `node_modules` left out; a Node stage that builds the SPA |

Then it installs the packages with npm (`--skip-install` skips it).

### 2. Run it

```sh
go tool anetos dev
```

Open **http://localhost:5173**, Vite's address: Vite serves the SPA from
`frontend/`, updating it as you edit, and forwards `/api` to `anetos
dev`'s address (`127.0.0.1:8080`, `HTTP_ADDR`), which serves the app
and rebuilds it as the Go code changes, writing the client again when a
route or a handler's types change. The address and the forwarding are in `vite.config.ts`:

```ts
server: {
  port: 5173,
  strictPort: true, // HTTP_SPA_ORIGINS and AUTH_CLIENT_URL in .env name it
  proxy: { '/api': 'http://127.0.0.1:8080' },
},
```

(Copied from [`examples/spa/frontend/vite.config.ts`](../../../examples/spa/frontend/vite.config.ts), region `server`.)

The page and the API are one origin for the browser (Vite's), as they
are in production (the app's).

### 3. Write the SPA

The SPA is yours: add a framework and a router with npm, in
`frontend/`. For React, with React Router:

```sh
cd frontend
npm install react react-dom react-router
npm install -D @vitejs/plugin-react @types/react @types/react-dom
```

then add `react()` to `plugins` in `vite.config.ts`, `"jsx":
"react-jsx"` to `tsconfig.json`, and point `index.html` at
`src/main.tsx`. Svelte (`@sveltejs/vite-plugin-svelte`) and Vue
(`@vitejs/plugin-vue`) take their plugin the same way; their components
need their own type checker in the build script (`svelte-check`,
`vue-tsc --noEmit`) in place of `tsc --noEmit`. [`examples/spa`](../../../examples/spa)
is a React SPA made like this: its pages are routes of the browser's
path, which the app answers with `index.html` (step 4):

```tsx
createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route element={<Layout />}>
            {/* Two keys: the lists don't share their state. */}
            <Route index element={<RequireUser><Bookmarks key="current" /></RequireUser>} />
            <Route path="archived" element={<RequireUser><Bookmarks key="archived" archived /></RequireUser>} />
            <Route path="login" element={<GuestOnly><Login /></GuestOnly>} />
            <Route path="register" element={<GuestOnly><Register /></GuestOnly>} />
            <Route path="forgot-password" element={<GuestOnly><ForgotPassword /></GuestOnly>} />
            {/* The account emails link to these two (AUTH_CLIENT_URL). */}
            <Route path="reset-password" element={<ResetPassword />} />
            <Route path="verify-email" element={<VerifyEmail />} />
            <Route path="*" element={<NotFound />} />
          </Route>
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  </StrictMode>,
)
```

(Copied from [`examples/spa/frontend/src/main.tsx`](../../../examples/spa/frontend/src/main.tsx), region `routes`.)

Call the API with the generated client, whose functions are named after
the routes (the route named `api.login` is `client.api.login`, which
`api.ts` below exports as `api.login`), with the handlers' input and
result types. A 422's problem details carry a message per field:

```ts
import { ApiError, createApi } from './types/api'

export const api = createApi().api

// errorMessages says what went wrong: a 422's message per field of the
// input ({ email: "…" }), otherwise the error's message, under "".
export function errorMessages(err: unknown): Record<string, string> {
  if (err instanceof ApiError) {
    return err.problem.errors ?? { '': err.message }
  }
  return { '': err instanceof Error ? err.message : String(err) }
}
```

(Copied from [`examples/spa/frontend/src/api.ts`](../../../examples/spa/frontend/src/api.ts), region `client`.)

The client sends the session cookie and the `XSRF-TOKEN` cookie back in
the `X-XSRF-TOKEN` header: the login is the session's, as [Log in from
your SPA](spa-login.md) describes. When the SPA starts, `api.me()` says
who is logged in (or 401). The account emails link to the SPA's
`/verify-email` and `/reset-password` pages (`AUTH_CLIENT_URL`), which
send the link's token to the API.

### 4. Serve it from the app

`vite:install` wrote this in `main.go`'s setup, after the routes:

```go
// `go run . types`: the client of the API in frontend/src/types, which
// the SPA calls it with (anetos dev updates it).
if err := typescript.Register(app, srv, typescript.Config{Prefix: routes.OpenAPI.Prefix}); err != nil {
	return nil, err
}
// The single-page app (frontend/), which Vite builds into public/spa:
// at /, for the paths the routes don't take, but /api/'s. In
// development, open Vite's address instead (anetos dev runs it).
spa, err := vite.SPA(app, public.Files, vite.Except("/api/"))
if err != nil {
	return nil, err
}
srv.Router().Fallback(spa)
```

(Copied from [`examples/spa/main.go`](../../../examples/spa/main.go), region `spa-serve`.)

The routes come first; for a request none of them takes, the SPA's
handler answers:

| Request | Answer |
|---|---|
| A GET or HEAD for a file of the build (`GET /assets/index-Bx1.js`) | The file: those under `assets/`, named by a hash of their content, cached for a year; the others revalidated |
| A GET or HEAD for `/`, or for a page's path from a browser (`GET /bookmarks/12`, `Accept: text/html`) | `index.html`, whose router shows the page |
| A path under `/api/` (`vite.Except`), from a browser too | 404, as problem details |
| Anything else (`POST /login`, a missing file, `Accept: application/json`, `/index.html` by its name from a program) | 404, as problem details |

### 5. Build for production

```sh
go tool anetos build
```

builds the SPA (`npm run build`: TypeScript's checks, in TypeScript,
then `vite build` into `public/spa`), then the binary, which embeds it. The `Dockerfile`
builds the SPA in a Node stage, then the binary with `--no-frontend`.
Where the app is deployed (`APP_ENV` production or staging), a binary
built without the SPA refuses to start, saying so.

In production's settings, the SPA's origin is `APP_URL`'s:

```sh
APP_URL=https://shop.example.com
HTTP_SPA_ORIGINS=https://shop.example.com
AUTH_CLIENT_URL=https://shop.example.com    # with make:auth
```

`vite:install` wrote them in `deploy/production.env.example`. The app's
`doctor` command, run with the production settings (`./shop doctor`),
warns when `HTTP_SPA_ORIGINS` doesn't list `APP_URL`'s origin. See [Deploy](deployment.md).

## The SPA elsewhere

The API serves other frontends too; `vite.SPA` is for the one it ships.

- **Another repository, or a CDN.** Build the SPA there, and list its
  origin in `HTTP_SPA_ORIGINS`. On another subdomain than the API
  (`app.example.com` calling `api.example.com`), set `SESSION_DOMAIN` and
  call the API with `createApi({ baseUrl: 'https://api.example.com',
  credentials: 'include' })`; on another site, the SPA logs in with a
  token ([Log in from your SPA](spa-login.md)). Write the client into
  the other repository with `typescript.Config{Dir: …}`, or copy
  `api.ts` there in your build.
- **A framework with its own server** (Next.js, Nuxt, SvelteKit). Run
  it as an app of its own, beside the API. Its pages in the browser
  call the API as an SPA elsewhere does; its server's code calls it
  with an API token, and can use the generated client too
  (`createApi({ baseUrl: 'https://api.example.com', token: process.env.API_TOKEN })`).

## Complete example

[`examples/spa`](../../../examples/spa) is the API of
[`examples/bookmarks`](../../../examples/bookmarks) with a React SPA:
login with two-factor codes, registration, password reset, email
verification, and the bookmarks, through the generated client. Its
tests run without Node.

## How it works

- **`vite.SPA(app, files, opts...)`** serves the build in `files`'
  `spa/` folder (`public.Files`): Vite writes it there (`outDir:
  '../public/spa'`, base `/`), naming its files without the leading `_`
  or `.` that `//go:embed` leaves out. A file's ETag is a hash of its
  content; text of 1 KiB or more goes gzipped to clients that take it.
  Only the files under `spa/` are served (not `public.go`), never dot
  files, and `index.html` only as the page. The page and the 404 depend
  on the `Accept` header, so they carry `Vary: Accept`.
- **`r.Fallback(h)`** is the router's handler for the requests no route
  takes, after `r.Static`'s files and before the 404. A path with routes
  for other methods still answers 405 ([Routing](routing.md#how-it-works)).
- **Without the build** (tests, `go run .` before `npm run build`), the
  SPA's paths answer 404 with a page that says to build it, outside
  production and staging, where the app refuses to start instead.
- **Development.** In an API project whose `main.go` calls `vite.SPA`,
  `anetos dev` runs Vite with its `vite.config`'s address (`node
  frontend/node_modules/vite/bin/vite.js --clearScreen false`), and
  `anetos build` and `anetos check` build the frontend; an API project's
  `frontend/` without it is an app of its own, which they leave alone.

## Testing it

The Go tests run without Node, with or without the build: test the API
as before,
check that the client is up to date (`typescript.Check`; see
[Generate TypeScript types](typescript.md#testing-it)), and that the
SPA's paths and the API's are apart:

```go
func TestSPAPages(t *testing.T) {
	app := anetostest.New(t, setup)
	app.WithHeader("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	for _, path := range []string{"/", "/archived", "/verify-email?token=x"} {
		app.Get(path).AssertHeader("Content-Type", "text/html; charset=utf-8")
	}
	app.Get("/api/v1/no-such-thing").AssertNotFound().
		AssertHeader("Content-Type", "application/problem+json")
	app.Get("/api/v1/bookmarks").AssertStatus(http.StatusUnauthorized).
		AssertHeader("Content-Type", "application/problem+json")
}
```

(Copied from [`examples/spa/spa_test.go`](../../../examples/spa/spa_test.go), region `spa-test`.)

Test the SPA itself with the tools of its framework (Vitest, Playwright),
against `anetos dev` or the built binary.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| http://localhost:8080 shows "The frontend isn't built" | In development, the SPA is Vite's | Open http://localhost:5173; or build it (`npm run build` in `frontend/`) for the app to serve it |
| Vite: "Port 5173 is already in use" | Another Vite runs there (`strictPort`) | Stop it, or change the port in `vite.config.ts`, `HTTP_SPA_ORIGINS` and `AUTH_CLIENT_URL` together |
| Vite: "http proxy error" for `/api/…` | The app isn't on `127.0.0.1:8080` (another `HTTP_ADDR` or `--addr`), or it is restarting | Change the proxy's target in `vite.config.ts`; wait for `anetos dev`'s "ready" |
| The login answers a token, and the next request is 401 | The page's origin isn't in `HTTP_SPA_ORIGINS` | List the address the browser shows: `http://localhost:5173` in development, `APP_URL`'s in production |
| An API path answers the SPA's page | `vite.SPA` without `vite.Except("/api/")` | Add it |
| The app refuses to start: "public/spa/index.html is missing" | A production binary built without the frontend | Build with `anetos build` (or the `Dockerfile`), not `go build` alone |
| `go vet ./...` reports packages in `frontend/node_modules` | No `frontend/go.mod` | Keep the `go.mod` `vite:install` wrote |

## Next steps

- [Log in from your SPA with a session cookie](spa-login.md): how the
  login works, and an SPA on another subdomain.
- [Generate TypeScript types for the frontend](typescript.md): the
  client and its options.
- [Bundle JavaScript and CSS with Vite](vite.md): a web project's
  frontend, for its pages.
- [Deploy](deployment.md).
- [Protect your app in the browser](browser-security.md): CORS and a
  Content-Security-Policy for the SPA.
