# Bookmarks, with its SPA

An [Anetos](https://github.com/anetos-dev/anetos) application: the JSON
API of [`bookmarks`](../bookmarks) (accounts with `make:auth`, bookmarks
with `make:crud`) and a single-page app in React that the binary serves
at `/`. See [Build a single-page app](../../docs/site/guides/spa.md).

```sh
go run . migrate        # create the database tables
go tool anetos dev      # the SPA at http://localhost:5173 (Vite), the API under /api/v1
go test ./...           # without Node: the API, the types and the serving
go tool anetos build    # bin/spa: the API and the SPA's build in one binary
```

The frontend needs Node.js 22.22 or later (React Router 8's).
`anetos dev` runs the app and Vite, which serves the SPA from
`frontend/` with hot reload, and forwards `/api` to the app. The app was
made with `anetos new spa --stack=api`, then `make:auth`, `make:crud`
and `vite:install`, whose plain TypeScript page became the React app:

| File | Holds |
|---|---|
| `main.go` | `typescript.Register` (the client) and `vite.SPA`, the router's fallback: the paths no route takes but `/api/`'s get the SPA |
| `public/public.go` | The build (`public/spa`), embedded in the binary |
| `frontend/vite.config.ts` | Vite's address (`localhost:5173`), the forwarding of `/api`, the build into `../public/spa` |
| `frontend/src/types/api.ts` | The API's client, written by `go run . types` from the handlers' Go types (`anetos dev` keeps it up to date; a test checks it) |
| `frontend/src/api.ts`, `auth.tsx`, `form.tsx` | The client, who is logged in, and the forms that show the API's errors by field |
| `frontend/src/pages/` | Login (with a two-factor code), registration, forgot and reset password, email verification, the bookmarks |

The SPA logs in with the session: its origin is in `HTTP_SPA_ORIGINS`
(Vite's address and `APP_URL` in `.env.example`; `APP_URL` in
`deploy/production.env.example`), so the account routes answer with a
session cookie, and the client sends the `XSRF-TOKEN` cookie back.
Other clients log in with API tokens, as in `bookmarks`. The emails'
links go to the SPA's `/verify-email` and `/reset-password` pages
(`AUTH_CLIENT_URL`).

## Deploy

```sh
go tool anetos build    # bin/spa: builds the frontend (Node.js), then the binary
```

Copy the binary to the server with its settings
(`deploy/production.env.example` lists them) and run `spa migrate`,
then `spa run`; `deploy/spa.service` does that with systemd. Or build
the image with `docker build -t spa .`: the `Dockerfile` builds the
frontend in a Node stage. See
[Deploy](https://github.com/anetos-dev/anetos/blob/main/docs/site/guides/deployment.md).
