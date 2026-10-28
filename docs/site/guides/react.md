---
title: Build pages in React
since: v0.6.0
group: "Frontends"
weight: 165
---

# Build pages in React

In a project of the react stack, the Go side is the web stack's: routes,
handlers, sessions, validation, translations and logins stay in Go. The
pages are React components in TypeScript, which Inertia's client renders
in the browser with the props a handler sends. Each page's props are a Go
struct, and the TypeScript types the components use are generated from
it, so the type check catches a renamed field or route before the
browser does.

This guide adds a page to a project with posts: a list of the drafts,
with a filter, a count in the header, and a test.

## Before you start

- Go 1.26 and Node.js 20.19 or 22.12 or later.
- A project of the react stack with posts:

```sh
anetos new shop --stack=react
cd shop
go tool anetos make:crud Post title:string body:text published:bool
go run . migrate
go tool anetos dev   # http://localhost:8080
```

`anetos dev` runs the app and Vite, rebuilds the app when Go code
changes, and updates the frontend's types (`go run . types`).

## Steps

### 1. Know where things are

| File | Holds |
|---|---|
| `app/handlers/pages.go` | `page[P](component)`, which declares a page, and `Shared`, the props every page gets |
| `app/handlers/posts.go` | `make:crud`'s handlers, their pages and props |
| `routes/` | The routes: `web.go` has the `pages` group, `posts.go` the posts' routes |
| `frontend/src/pages/` | The components: `Home.tsx`, `Posts/Index.tsx`… A page named `Posts/Index` in Go is `pages/Posts/Index.tsx` |
| `frontend/src/Layout.tsx` | The header, nav and footer around every page |
| `frontend/src/components/ui.tsx` | The starter theme's components: `PageHeader`, `Card`, `Field`, `Input`, `Button`, `Table`, `Pagination`… |
| `frontend/src/i18n.ts` | `useT()` for the text of `locales/`, `useFormat()` for numbers and dates |
| `frontend/src/types/` | Written by `go run . types`: `pages.ts` (the props), `routes.ts` (`route()`) |

### 2. Declare the page in Go

A page is its component's name and the Go type of its props. Add
`app/handlers/drafts.go`:

```go
// illustrative: app/handlers/drafts.go
package handlers

import (
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"

	"shop/app/models"
)

// DraftsQuery is what the drafts page reads from the URL: ?q=, a part of
// the title.
type DraftsQuery struct {
	Q string `query:"q"`
}

// DraftsProps are the drafts page's props.
type DraftsProps struct {
	Q     string     `json:"q"`
	Posts []PostItem `json:"posts"`
}

var draftsPage = page[DraftsProps]("Posts/Drafts")

// Drafts lists the posts not published yet, newest first; with ?q=, those
// whose title contains it.
func (Posts) Drafts(c *web.Ctx, in DraftsQuery) (web.Responder, error) {
	q := db.Query[models.Post](c).Where(models.PostCols.Published.Eq(false)).
		OrderBy(models.PostCols.ID.Desc()).Limit(50)
	if in.Q != "" {
		q = q.Where(models.PostCols.Title.Contains(in.Q))
	}
	rows, err := q.Get()
	if err != nil {
		return nil, err
	}
	posts := make([]PostItem, len(rows))
	for i, row := range rows {
		posts[i] = postItem(row)
	}
	return draftsPage.Responder(DraftsProps{Q: in.Q, Posts: posts}), nil
}
```

`PostItem` is `make:crud`'s: the fields of a post the pages get, so a
new column never shows by accident. Route it in `routes/posts.go`,
with a name:

```go
// illustrative: routes/posts.go
r.Get("/drafts", web.H(h.Drafts)).Name("posts.drafts")
```

`page` adds the page to `handlers.Pages`, which `setup` lists:
`go run . types` (which `anetos dev` runs) writes `DraftsProps` into
`frontend/src/types/pages.ts`, and `posts.drafts` into `routes.ts`.

### 3. Write the component

Add `frontend/src/pages/Posts/Drafts.tsx`. It gets the props, with
their type from Go:

```tsx
// illustrative: frontend/src/pages/Posts/Drafts.tsx
import { Form, Head, Link } from '@inertiajs/react'
import { Button, Cluster, Empty, Input, PageHeader, Table } from '../../components/ui'
import { useT } from '../../i18n'
import type { PageProps } from '../../types/pages'
import { route } from '../../types/routes'

// Posts/Drafts lists the posts not published yet, filtered by title.
export default function Drafts({ q, posts }: PageProps<'Posts/Drafts'>) {
  const t = useT()
  return (
    <>
      <Head title={t('posts.drafts.title')} />
      <PageHeader title={t('posts.drafts.title')} />
      <Form action={route('posts.drafts')} method="get" options={{ preserveState: true }}>
        <Cluster>
          <Input name="q" defaultValue={q} aria-label={t('posts.drafts.search')} placeholder={t('posts.drafts.search')} />
          <Button variant="secondary">{t('posts.drafts.filter')}</Button>
        </Cluster>
      </Form>
      {posts.length === 0 ? (
        <Empty text={t('posts.drafts.none')} />
      ) : (
        <Table>
          <tbody>
            {posts.map((post) => (
              <tr key={post.id}>
                <td>
                  <Link href={route('posts.show', { id: post.id })}>{post.title}</Link>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </>
  )
}
```

- **The filter** is Inertia's `<Form>` with `method="get"`: it visits
  `/drafts?q=…` without a page load, and `preserveState` keeps what was
  typed. The handler reads `q` from the URL (`DraftsQuery`).
- **Links** name their routes: `route('posts.show', { id: post.id })`.
  A route renamed in Go, or a parameter left out, fails the type check.
  `Link` visits the page without reloading the app.
- **The text** comes from `locales/`. Add the keys to
  `locales/en/posts.yaml`, under `posts:`:

```yaml
  drafts:
    title: "Drafts"
    search: "Title contains"
    filter: "Filter"
    none: "No drafts."
    nav: "Drafts ({count})"
```

React escapes what it prints, so a title holding `<script>` shows as
text. Don't write user data with `dangerouslySetInnerHTML`
([Protect your app in the browser](browser-security.md#5-show-what-users-typed-as-text-xss)).

### 4. Share a prop with every page

The props of `Shared` in `app/handlers/pages.go` reach every page: the
app's name, the locale, the logged-in `user`, the flashed `notice`. Add
the number of drafts, for the header:

```go
// illustrative: app/handlers/pages.go, in Shared
	// Drafts is the number of posts not published yet.
	Drafts int64 `json:"drafts"`
```

```go
// illustrative: app/handlers/pages.go, in SharedProps, before the return
	drafts, err := db.Query[models.Post](c).Where(models.PostCols.Published.Eq(false)).Count()
	if err != nil {
		return Shared{}, err
	}
```

and `Drafts: drafts,` in the `Shared` it returns (`pages.go` then imports
`anetos.dev/anetos/db` and `shop/app/models`). That's a query on
every page: keep shared props cheap. A component reads them with
`usePage()`; in `frontend/src/Layout.tsx`:

```tsx
// illustrative: frontend/src/Layout.tsx
const { appName, routePrefix, notice, locale, dir, drafts } = usePage().props
// …
<NavLink route="posts.drafts" label={t('posts.drafts.nav', { count: drafts })} />
```

The layout stays mounted as pages change; the count follows each visit.

### 5. Take input with a form

`make:crud`'s `Posts/Form.tsx` is the pattern for a form. Inertia's
`<Form>` posts to the route, and the handler's input struct validates
it before the handler runs (`validate:"required|max:255"`). An invalid
form comes back to the page with `errors`, by field, and what was typed
stays:

```tsx
// illustrative: from frontend/src/pages/Posts/Form.tsx
<InertiaForm
  action={item ? route('posts.update', { id: item.id }) : route('posts.create')}
  method={item ? 'put' : 'post'}
  // The form's values are text: the handler's input wants numbers
  // and true or false.
  transform={(data) => ({ ...data, published: data.published === '1' })}
>
  {({ errors, processing }) => (
    <>
      <Field name="title" label={t('posts.fields.title')} error={errors.title}>
        <Input defaultValue={item?.title} />
      </Field>
      {/* … */}
      <Button disabled={processing}>{t('posts.save')}</Button>
    </>
  )}
</InertiaForm>
```

After saving, the handler flashes a message and redirects
(`c.Session().Flash("status", …)`, `web.RedirectRoute("posts.show",
row.ID)`); the layout shows it from the shared `notice`. The messages
of the rules are translated with the rest of the text
([Validation](validation.md)).

### 6. Let only logged-in users in

`go tool anetos make:auth` adds registration, login, two-factor
authentication and the account's pages, in React, and fills the shared
`user` prop (`usePage().props.user`, null for a guest); then
`go run . migrate` creates the accounts' tables. To keep the posts for
logged-in users, move their routes into its `loggedIn` group: remove
`Posts(pages)` from `routes/web.go`, and in `routes/auth.go`:

```go
// illustrative: routes/auth.go, after loggedIn := …
Posts(loggedIn) // the posts, for logged-in users only
```

Hiding a link from guests isn't enough: the route's group is what keeps
them out.

### 7. Test it

The tests are Go, and need neither Node nor a build: `AssertInertia`
checks the component, `AssertInertiaProp` a prop, by its path:

```go
// illustrative: drafts_test.go
package main

import (
	"testing"

	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/db"

	"shop/app/models"
)

func TestDrafts(t *testing.T) {
	app := anetostest.New(t, setup)
	for _, p := range []models.Post{
		{Title: "Alpha", Body: "a"},
		{Title: "Beta", Body: "b"},
		{Title: "Out", Body: "c", Published: true},
	} {
		if err := db.Create(app.Context(), &p); err != nil {
			t.Fatal(err)
		}
	}
	app.Get("/drafts").AssertOK().AssertInertia("Posts/Drafts").
		AssertInertiaProp("posts.0.title", "Beta").AssertInertiaProp("drafts", 2)
	app.Get("/drafts?q=Alp").AssertInertiaProp("posts.0.title", "Alpha").AssertInertiaProp("q", "Alp")
}
```

With the posts behind a login (step 6), log a user in first:
`ada := anetostest.Create(app, factories.Users)` and
`anetostest.ActingAs(app, &ada)`. `go tool anetos check` runs the tests
and the frontend's build, whose `tsc` type-checks every component.

### 8. Build it

```sh
go tool anetos build   # bin/shop
```

It type-checks and builds the frontend into `public/build`, then the
binary, which embeds it: one file to deploy ([Deploy](deployment.md)).

## Complete example

`anetos new --stack=react` with `make:auth` and `make:crud` is the
complete example: its pages, handlers and tests are the patterns above.
[`examples/inertia`](../../../examples/inertia) shows more of Inertia's
props: deferred, merged and flashed.

## How it works

The first visit gets the HTML of `views/app.templ`, holding the page's
component and props; Inertia's client renders it inside the layout.
Later links and forms are requests that answer the next page's props as
JSON, and the client swaps the component in place. The routes, the
session, CSRF protection (an `XSRF-TOKEN` cookie the client sends back)
and validation are the web stack's.
[Pages in Vue, React or Svelte with Inertia](inertia.md) covers the
protocol: partial reloads, deferred props, asset versions.

## Testing it

See step 7. The Go tests check what the handlers send; the type check
catches a component that reads a prop or route that isn't there. To
check a page in a browser, run the app with `go tool anetos dev`.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| The type check says a prop or a page doesn't exist | `frontend/src/types` is older than the Go code | `go run . types` (`anetos dev` does after each build) |
| `inertia: the page "Posts/Drafts" isn't in inertia.WithPages` | The page was made with `inertia.NewPage` instead of `page` | Declare it with `page[P]`, which lists it |
| `no page Posts/Drafts (frontend/src/pages/Posts/Drafts.tsx)` in the browser's console | The component's file is missing or named differently | The file's path under `pages/` is the name in Go, with `.tsx` |
| A key shows instead of its text (`posts.drafts.title`) | It isn't in `locales/en/` | Add it; the server sends the catalogs to the pages |

## Next steps

- [Pages in Vue, React or Svelte with Inertia](inertia.md): the
  protocol, and the props that load later or merge.
- [Generate TypeScript types for the frontend](typescript.md).
- [Move htmx pages to Inertia](migrate-htmx-to-inertia.md), for a web
  project.
- [Frontends](../concepts/frontends.md): the ways Anetos serves a
  frontend, and how to choose.
