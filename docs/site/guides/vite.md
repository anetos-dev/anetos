---
title: Bundle JavaScript and CSS with Vite
since: v0.6.0
group: "Frontends"
weight: 160
---

# Bundle JavaScript and CSS with Vite

Add a frontend that [Vite](https://vite.dev) builds: TypeScript or
JavaScript modules, CSS, and npm packages. Its pages update as you edit
them in development, and its build is embedded in the app's binary for
production.

The templ pages and htmx of a web project need no build step. Use Vite
when you want more: a charting library from npm, TypeScript, an
interactive widget in one of your pages.

## Before you start

- A web project ([Create a project](../getting-started/create-a-project.md)).
  An API project's frontend is a single-page app: see [Build a
  single-page app](spa.md).
- [Node.js](https://nodejs.org) 20.19 or later in 20, or 22.12 or
  later, with npm. Only the frontend needs it: the Go app doesn't, and
  neither do its tests.

## Steps

### 1. Add the frontend

In a new project:

```sh
anetos new shop --vite          # TypeScript; --vite=js for JavaScript
```

In an existing web project:

```sh
go tool anetos vite:install     # --js for JavaScript
go tool anetos agent:update     # so coding agents know about frontend/
```

Both write `frontend/`:

| File | Holds |
|---|---|
| `frontend/package.json` | The packages (Vite; TypeScript with `--vite`) and the scripts: `dev` runs Vite, `build` type-checks and runs `vite build` |
| `frontend/vite.config.ts` | Where the build goes (`../public/build`, served under `/build/`), the manifest's name, the entries (`src/main.ts`), and the names of the built files (below) |
| `frontend/src/main.ts` | The entry the layout loads; it imports `main.css` |
| `frontend/tsconfig.json` | TypeScript's settings (TypeScript only) |
| `frontend/go.mod` | A module of its own, which keeps `go build ./...` and `go vet ./...` out of `node_modules`, where some npm packages ship Go files |

Then they connect the app to it:

- In `main.go`'s `setup`, `vite.New(app, public.Files)`, before
  `web.NewServer`.
- In `routes/web.go`, the build's route:
  `r.Get("/build/{path...}", web.WrapHandler(vite.Handler()))`.
- In `views/layout.templ`'s `<head>`, after `@ui.Head()`:
  `@vite.Tags("src/main.ts")`, and they run `templ generate`.
- In `.gitignore` and `.dockerignore`, `frontend/node_modules/` and
  `public/build/`.
- In the `Dockerfile`, a Node stage (step 5).

They install the packages with npm (`--skip-install` skips it). If you
changed one of those files so that `vite:install` can't find its place
in it, it leaves the file alone and prints what to add. It leaves the
layout alone too when it couldn't change `main.go`: the tags need
`vite.New`.

### 2. Run it

```sh
go tool anetos dev
```

`anetos dev` starts Vite beside the app, on `127.0.0.1` and a port of
its own (flags that win over `vite.config`'s `server` settings), and
forwards `/build/` to it, the hot reload's WebSocket included. The
browser sees one address, `http://localhost:8080`. It also sets `VITE_DEV_URL=/build/` for the
app, so `@vite.Tags` loads the sources from Vite:

```html
<script type="module" src="/build/@vite/client"></script>
<script type="module" src="/build/src/main.ts"></script>
```

Edit `frontend/src/main.css` and the page changes without reloading.
Edit a module and Vite updates it, or reloads the page when it can't.
Changes to Go and templ files rebuild the app, as before.

### 3. Write the frontend

Import modules and stylesheets from the entry:

```ts
// illustrative: frontend/src/main.ts
import './main.css'
import { formatPrice } from './prices'

document.querySelectorAll<HTMLElement>('[data-price]').forEach((el) => {
  el.textContent = formatPrice(Number(el.dataset.price))
})
```

Add packages in `frontend/`:

```sh
cd frontend && npm install chart.js
```

Styles in `main.css` load after the CSS framework's stylesheet
(`public/static/app.css`), so they win where both set a property. Files
that need no build, such as a favicon, stay in `public/static`, served
under `/assets/`.

### 4. Add entries

A page that needs its own script gets its own entry. List it in
`vite.config.ts`'s inputs:

```ts
input: ['src/main.ts', 'src/charts.ts'],
```

and load it where it's needed, in a templ page:

```templ
// illustrative
templ Dashboard(stats []Stat) {
	@vite.Tags("src/charts.ts")
	…
}
```

Each `@vite.Tags` call on a page, such as the layout's and this one,
writes the tags of its own entries, so what two entries share comes
twice, and a stylesheet linked twice applies again where it's linked
the second time. Load an entry on its own, as above, when it imports
nothing the layout's entry imports. Otherwise load both with one call:
`@vite.Tags` takes several entries, so the layout can take the page's
entries as a parameter.

For a file the build hashes, such as an image the frontend imports,
`vite.URL(ctx, "src/logo.svg")` gives its URL.

### 5. Build for production

```sh
go tool anetos build
```

`anetos build` installs the packages if they need it, runs the
frontend's `build` script (TypeScript's checks, then `vite build` into
`public/build`), and builds the binary, which embeds `public/build`.
The server needs neither Node nor `frontend/`.

The `Dockerfile` builds the frontend in a Node stage, with npm:

```dockerfile
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY frontend/ ./
RUN npm run build
```

The Go stage copies `public/build` from it after `COPY . .` and runs
`go tool anetos build --no-frontend`, since the frontend is built
already. The stage needs `frontend/package-lock.json`, which installing
the packages writes: commit it. With pnpm, yarn or bun, copy that
manager's lockfile instead, and install and build with it.

## Complete example

`anetos new shop --vite` writes the files of step 1. The guide's
[test](https://github.com/anetos-dev/anetos/blob/main/cli/cmd/anetos/vite_test.go)
makes such a project, builds it with Vite, and checks that the app
embeds every file of the build.

## How it works

`vite build` writes the files with a hash of their content in their
names (`assets/main-B7PI925R.js`), and a manifest,
`public/build/manifest.json`, saying which files each entry needs. The
manifest gets that name because Vite's default, `.vite/manifest.json`,
is in a dot folder, which `//go:embed` leaves out. For the same reason,
`vite.config` names the built files without a leading `_` or `.`
(`src/_chart.ts` builds into `assets/chart-….js`). Files of
`frontend/public`, which Vite copies as they are, keep their names:
don't start them with `_` or `.`.

`vite.New` reads the manifest when the app starts. For an entry,
`@vite.Tags` writes:

1. a stylesheet link for each of its CSS files and those of the
   chunks it imports;
2. the entry's `<script type="module">`, or its stylesheet link for a
   CSS entry;
3. a `modulepreload` link for each chunk it imports.

`vite.Handler` serves the files under `/build/`. Those under
`assets/`, whose names change with their content, may be cached for a
year. Text files of 1 KiB or more are sent gzipped to clients that
accept it. The manifest itself isn't served.

> **Warning:** where the app is deployed (`APP_ENV=production` or
> `staging`), `vite.New` refuses `VITE_DEV_URL`, which would make the
> pages load their scripts from a development server. It also refuses
> to start without the manifest, which means the frontend wasn't
> built. Elsewhere, without a manifest, `@vite.Tags` writes an HTML
> comment saying so, and `go run . doctor` warns in development. A
> manifest that isn't valid JSON stops the app everywhere.

The [concept page on the frontend](../concepts/server-rendered-html.md#a-frontend-built-by-vite)
places Vite among the other ways to write pages.

## Testing it

The app's tests need no build: without a manifest, the tags are a
comment, and the pages render. To test the frontend itself, use the
JavaScript tools you know (Vitest, Playwright) in `frontend/`.

`go tool anetos check` runs the frontend's build as a step of its own,
before the Go build, so a type error in `frontend/src` fails it.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `anetos dev: the frontend (frontend/) needs Node.js 20.19 or 22.12 or later` | Node.js isn't installed, or is older | Install Node.js from nodejs.org |
| The app doesn't start in production: `vite: read build/manifest.json` | The binary was built without the frontend's build | Build with `go tool anetos build`, not `go build`, or run `npm run build` in `frontend/` first |
| `vite: VITE_DEV_URL is set with APP_ENV=production` | A development setting reached the server | Remove `VITE_DEV_URL` from its environment |
| The page has `<!-- vite: no build/manifest.json -->` and no scripts | The app runs without `anetos dev` and without a build | Use `go tool anetos dev`, or run `npm run build` in `frontend/` |
| `vite: "src/charts.ts" isn't an entry of build/manifest.json` | The entry isn't in `vite.config.ts`'s inputs, or the build is older | Add it to the inputs, and build again |
| `anetos dev: the frontend's Vite stopped before it served (exit status 1); its output is above` | Vite failed to start: an error in `vite.config`, or a plugin missing from `node_modules` | Read Vite's message above it |
| A file under `/build/` that the manifest names answers 404 from the binary, not in development | Its name starts with `_` or `.`, which `//go:embed` leaves out: a file of `frontend/public`, or `vite.config` lost its output names | Rename the file, or restore the names from a new project's `vite.config` |
| `go vet ./...` reports packages under `frontend/node_modules` | `frontend/go.mod` was removed | Restore it |

## Next steps

- [Style your app](styling.md): the CSS frameworks, which work beside
  the frontend.
- [Deploy](deployment.md): the Docker image with the frontend's stage.
- [Pages in Vue, React or Svelte with Inertia](inertia.md): whole pages
  written in a JavaScript framework.
- [Generate TypeScript types for the frontend](typescript.md): the Go
  types, in the frontend.
- [Protect your app in the browser](browser-security.md): a
  Content-Security-Policy that fits the build.
