---
title: Generate TypeScript types for the frontend
since: v0.6.0
group: "Frontends"
weight: 175
---

# Generate TypeScript types for the frontend

Give the frontend the types of the app's Go code: the inputs and results
of the API's handlers, with a client that calls them, and the props of
the Inertia pages. The types are generated from the Go types, so when a
field changes in Go, TypeScript's type checks find what to change in the
frontend.

## Before you start

- A frontend in TypeScript ([Bundle JavaScript and CSS with Vite](vite.md),
  or a separate app).
- Typed handlers (`web.H`, see [Write handlers](handlers.md)) for the
  API's types and client, or Inertia pages
  ([Pages in Vue, React or Svelte with Inertia](inertia.md)) for their
  props.

## Steps

### 1. Register the types command

In `main.go`'s `setup`, after the routes:

```go
// illustrative
if err := typescript.Register(app, srv, typescript.Config{
	Prefix:  "/api/v1", // the API's routes: api.ts's client calls them
	Inertia: in,        // the Inertia pages' props: pages.ts
}); err != nil {
	return nil, err
}
```

`Config.Dir` is where the files go, `frontend/src/types` by default. A
frontend in another repository can take them from there, or you can
point `Dir` into it.

### 2. List the Inertia pages

With Inertia, list the pages in `inertia.New`, so the generator knows
them:

```go
func setup(app *anetos.App) (*web.Server, error) {
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	sessions, err := session.New(app)
	if err != nil {
		return nil, err
	}
	if _, err := vite.New(app, public.Files, vite.WithReactRefresh()); err != nil {
		return nil, err
	}
	shared := func(*web.Ctx) (Shared, error) { return Shared{AppName: app.Config().Name}, nil }
	in, err := inertia.New(app, root(), inertia.WithShared(shared), inertia.WithPages(notesIndex, notesCreate))
	if err != nil {
		return nil, err
	}

	r := srv.Router()
	r.ErrorPages(inertia.ErrorPages("Error"))
	r.Get("/build/{path...}", web.WrapHandler(vite.Handler()))
	h := notes{store: newStore()}
	g := r.Group("", sessions.Middleware, web.CSRF(web.WithXSRFCookie()), inertia.Middleware)
	g.Get("/", func(c *web.Ctx) error { return c.RedirectRoute("notes.index") })
	g.Get("/notes", h.index).Name("notes.index")
	g.Get("/notes/new", h.create).Name("notes.create")
	g.Post("/notes", web.H(h.save))
	g.Delete("/notes/{id}", h.delete)
	g.Get("/boom", func(*web.Ctx) error { return web.Error(http.StatusTeapot, "This page is a teapot.") })

	// go run . types writes the pages' props as TypeScript, in
	// frontend/src/types (anetos dev does after each build).
	if err := typescript.Register(app, srv, types(in)); err != nil {
		return nil, err
	}
	return srv, nil
}

// types is where the frontend's TypeScript types come from.
func types(in *inertia.Inertia) typescript.Config {
	return typescript.Config{Inertia: in}
}
```

(Copied from [`examples/inertia`](../../../examples/inertia/main.go), region `setup`.)

In development and tests, once the app lists pages, rendering a page
that isn't listed fails, saying to add it. A package of handlers can
export its pages for `setup` to list (`var Pages = []inertia.AnyPage{…}`).

### 3. Write the files

```sh
go run . types
```

writes `frontend/src/types/api.ts` and, with Inertia,
`frontend/src/types/pages.ts`. Commit them. `go tool anetos generate`
runs the command, and `go tool anetos dev` runs it after each build, so
the files follow the Go code as you edit; a file whose content didn't
change isn't written again.

### 4. Use the types

A page component takes its props' type from `pages.ts`:

```tsx
// illustrative: frontend/src/pages/Notes/Index.tsx
import type { PageProps } from '../../types/pages'

export default function Index({ notes = [], next, stats }: PageProps<'Notes/Index'>) {
  // notes: Note[], next: number, stats?: Stats
}
```

`PageProps<'Notes/Index'>` is the page's props, the shared props and
`errors`. `pages.ts` also tells Inertia's client the shared props'
type, so `usePage().props.appName` is typed in any component. A
`Prop` field is optional in TypeScript (`stats?`), since a deferred or
optional prop is left out of the first visit.

A form takes its input's type from `api.ts` (the typed handler's
input, `NoteInput`):

```tsx
// illustrative
import type { NoteInput } from '../../types/api'

const form = useForm<NoteInput>({ title: '', body: '' })
```

### 5. Call the API

`api.ts` has a function for each typed handler at or below
`Config.Prefix`, named after the route's name (`posts.show` is
`api.posts.show`); an unnamed route's is its handler's method or
function (`Posts.Index` is `api.posts.index`), else its method and path
(`getApiV1Posts`), so name the routes the frontend calls:

```ts
// illustrative
import { createApi, ApiError } from './types/api'

const api = createApi()
const post = await api.posts.show({ id: 1 }) // Post
try {
  await api.posts.create({ title: '' })
} catch (e) {
  if (e instanceof ApiError) console.log(e.status, e.problem.errors) // 422, { title: '…' }
}
```

A function takes one object: the path, query and header parameters by
their names (`id`, `page`), and the body's fields by their JSON names.
It sends JSON, or a multipart form when the input has files, and
resolves to the result's type (nothing for 204). An error status
rejects with an `ApiError` holding the problem details. `createApi`'s
options:

| Option | Does |
|---|---|
| `baseUrl` | The API's origin when it isn't the page's (`https://api.example.com`) |
| `token` | An API token, or a function returning it: sent as `Authorization: Bearer` |
| `credentials` | `same-origin` by default; `include` sends cookies to another origin too (the XSRF header goes when the page can read the `XSRF-TOKEN` cookie: a cookie domain both share) |
| `headers` | Headers for every request |
| `fetch` | The `fetch` to use |

On the page's origin, the session's cookie and the `XSRF-TOKEN`
cookie go with each call (`X-XSRF-TOKEN` for a post), as `web.CSRF`
expects with `web.WithXSRFCookie()`.

## Complete example

[`examples/inertia`](../../../examples/inertia) types its pages from
`frontend/src/types/pages.ts`, which its `setup` registers and a test
keeps up to date.

## How it works

The types are built from the Go types at run time, with the JSON
Schemas of the [OpenAPI document](openapi.md): a struct as
`encoding/json` writes it (its `json` names, `omitempty`, embedded
structs), a named struct as an interface of its name, and an input's
`validate` rules where TypeScript can say them (`required` makes a
field required, `in` makes a union of the values). `time.Time` is a
`string`, `[]byte` a base64 `string`, a pointer `T | null`.

> **Note:** a nil slice or map is written as `null` by
> `encoding/json`, while its type says it's an array or an object:
> return empty ones (`[]Post{}`) from handlers whose clients rely on it.

The command runs the app's `setup`, for its routes and pages, without
starting it: it needs the app's settings (`APP_KEY`), not its database.
So does `go tool anetos generate --check` in CI, which runs it.

`pages.ts` imports types from `@inertiajs/core`, which the Inertia
adapter (`@inertiajs/react`, `-vue` or `-svelte`) installs; with pnpm,
whose packages see only their own dependencies, add `@inertiajs/core`
to the frontend's.

## Testing it

A test fails when the files are out of date:

```go
// The frontend's TypeScript types are those of the Go types: a change to
// a page's props without go run . types fails here.
func TestTypes(t *testing.T) {
	app := anetostest.New(t, setup)
	in := anetos.MustResolve[*inertia.Inertia](app.App)
	if err := typescript.Check(app.Router(), types(in)); err != nil {
		t.Error(err)
	}
}
```

(Copied from [`examples/inertia/main_test.go`](../../../examples/inertia/main_test.go), region `types-test`.)

`go run . types --check` does the same, without a test.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `inertia: the page "…" isn't in inertia.WithPages` | A page rendered in development or a test isn't listed | Add it to `inertia.WithPages` |
| `typescript: frontend/src/types/api.ts is out of date` | A Go type changed without the files | Run `go run . types` (or `go tool anetos generate`), and commit the files |
| A route has no function in the client | It isn't a typed handler (`web.H`), or it's outside `Config.Prefix` | Route it with `web.H`, under the prefix |
| A result is `unknown` | The handler's result is a `web.Responder` or an interface | Return the type itself |
| A type is `Record2`, or `HandlersOrder` | Its Go name is one TypeScript or the files have (`Record`, `Pages`), or two packages have a type of that name | Use the name given, or rename the Go type |
| `warning: … the header X-Tags is a list` | The client sends a list header as one value, its items joined by commas | Read the list from the query instead |

## Next steps

- [Describe an API with OpenAPI](openapi.md): the same types, for other
  clients and tools.
- [TypeScript reference](../reference/typescript.md).
