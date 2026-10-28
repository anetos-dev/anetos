---
title: Move htmx pages to Inertia
since: v0.6.0
group: "Frontends"
weight: 172
---

# Move htmx pages to Inertia

A project of the web stack renders its pages with templ and htmx. The
react, svelte and vue stacks keep the same Go side (routes, handlers,
sessions, validation, logins) and render the pages as components with
Inertia. Moving from one to the other changes the views, not the app:
the handlers send props instead of rendering templ components. This
guide moves a project page by page, with both kinds of pages working
side by side until the last one moves.

## Before you start

- A project made with `anetos new` (the web stack), with Anetos v0.6 or
  later, and Node.js 20.19 or 22.12 or later.
- Commit your work first: you'll copy files over the project's own.
- The guide uses React; for Svelte or Vue, use `--stack=svelte` or
  `--stack=vue`, and the files of that stack, where it says React's.

## Steps

### 1. Make a reference project

The files you need are the ones `anetos new` writes for the stack you
move to. In another folder, make a project of that stack with your
project's name and module path (`go.mod`'s first line), and run the
same generators your project ran:

```sh
cd /tmp
anetos new blog --stack=react --module=example.com/blog
cd blog
go tool anetos make:auth                                         # if your project has accounts
go tool anetos make:crud Post title:string body:text published:bool   # each make:crud you ran
```

This reference project is what you copy from. Diffing it with your
project shows what each step changes.

### 2. Add the frontend

In your project:

```sh
go tool anetos vite:install
```

It adds `frontend/` with Vite, `vite.New` in `main.go`, the `/build/`
route, a Node stage in the Dockerfile and the build in `.gitignore` and
`.dockerignore`, and installs the packages. Its frontend is a plain
script for the templ pages; replace it with the reference project's:

- Remove the line `@vite.Tags("src/main.ts")` it added to
  `views/layout.templ` (and the `vite` import, if nothing else uses it),
  and `frontend/src/main.ts` and `main.css`.
- Copy from the reference project: `frontend/package.json`,
  `package-lock.json`, `vite.config.ts` and `tsconfig.json`;
  `frontend/src/` without `pages/` and `components/SocialButtons.tsx`
  (the accounts' pages' parts: step 6), then `pages/Error.tsx`;
  `views/app.templ`, the root view; `app/handlers/pages.go`, which
  declares the pages and the props every page shares; and
  `public/favicon.svg`.
- Add the keys of the reference project's `locales/en/app.yaml` that
  yours lacks (`errors.request_id`).
- Run `npm install` in `frontend/`.

With accounts, `pages.go` gets the logged-in user from `authUser`, in
the reference project's `app/handlers/auth.go`. Until the accounts'
pages move (step 6), get it in `pages.go` itself (importing
`anetos.dev/anetos/auth` and your `app/models`):

```go
// illustrative: app/handlers/pages.go, until the accounts move
func currentUser(c *web.Ctx) (*SharedUser, error) {
	u, ok := auth.User[*models.User](c)
	if !ok {
		return nil, nil
	}
	return &SharedUser{ID: u.ID, Name: u.Name, Email: u.Email, EmailVerified: u.EmailVerifiedAt != nil}, nil
}
```

### 3. Set Inertia up

In `main.go`'s `setup`, add Inertia, with the root view, the shared
props and the pages, after the `vite.New` that `vite:install` wrote,
which takes `vite.WithReactRefresh()` for React's hot reload (Svelte and
Vue don't take it):

```go
// illustrative: main.go, in setup
if _, err := vite.New(app, public.Files, vite.WithReactRefresh()); err != nil {
	return nil, err
}
in, err := inertia.New(app, views.App(), inertia.WithShared(handlers.SharedProps), inertia.WithPages(handlers.Pages...))
if err != nil {
	return nil, err
}
```

`main.go` imports `anetos.dev/anetos/web/inertia`,
`anetos.dev/anetos/web/typescript`, and your `app/handlers` and `views`
packages. After the routes (before `ext.Load(plugins())`), the
command that writes the
frontend's types:

```go
// illustrative: main.go, at the end of setup
if err := typescript.Register(app, srv, typescript.Config{Inertia: in, Routes: true, ExcludeRoutes: []string{"admin."}}); err != nil {
	return nil, err
}
```

In `routes/web.go`, and in `routes/auth.go` with accounts, the pages'
groups get Inertia's middleware, and CSRF protection gives Inertia's
client its token in a cookie:

```go
// illustrative: routes/web.go
pages := r.Group("", sessions.Middleware, web.CSRF(web.WithXSRFCookie()), inertia.Middleware)
```

Both files import `anetos.dev/anetos/web/inertia`. The error pages
become the frontend's `Error` page, for the templ pages too (then
`routes/web.go` no longer uses `view` and `views`: remove their
imports):

```go
// illustrative: routes/web.go
r.ErrorPages(inertia.ErrorPages("Error"))
```

Then `go tool templ generate`, `go run . types`, and
`go tool anetos dev`: the templ pages work as before.

### 4. Move a page

Take one page, the posts' list, say. In Go, the handler renders a page
with props instead of a templ component:

```go
// illustrative: before, in app/handlers/posts.go
return web.Render(views.PostsPage(page)), nil

// after
return postIndexPage.Responder(PostsIndexProps{Page: db.MapPage(page, postItem)}), nil
```

with the page and its props declared beside it
(`postIndexPage = page[PostsIndexProps]("Posts/Index")`). For pages a
generator wrote, the reference project has the code, a file for a
model's pages: `make:crud`'s list, page and form move together. Copy
the handlers' file, the components (`frontend/src/pages/Posts/`) and
the test, which checks the component and the props; delete the templ
file (`views/posts.templ`); run `go tool templ generate` and
`go run . types`. Then make your own changes to those pages again, in
the components and their text in `locales/` (a column you added, a
cell the list shows differently). For a page of your own, see [Build
pages in React](react.md). The home page (`app/handlers/home.go`,
`views/home.templ`) moves the same way, with the reference project's
`frontend/src/pages/Home.tsx`, `main_test.go` and `home.edit` text in
`locales/en/app.yaml`.

The two kinds of pages link to each other as they are. A link in a
component to a page not moved yet works: Inertia's middleware answers
the client's visit to a templ page with `409` and its address, and the
client loads it whole (the browser's console notes the 409). A link in
a templ page to a moved one loads it whole too. A message flashed
before the redirect shows on either kind. Two kinds of links aren't
covered: to a route outside the pages' groups (the admin's), and with
Inertia's `prefetch`; make those plain `<a>` links.

### 5. Move the others

One by one, with the app working after each. The text stays in
`locales/`, which the components read with `useT()`. The tests check
props rather than HTML (`AssertInertia`, `AssertInertiaProp`). Once
`app/handlers/pages.go` and `frontend/package.json` are in, the tool
takes the project for a react one: `make:crud` writes React pages and
adds its link to `frontend/src/Layout.tsx` (add one to
`views/layout.templ` by hand while templ pages remain), and
`go tool anetos agent:update` updates the coding agents' files.

### 6. Move the accounts' pages together

`make:auth`'s pages share handlers, so they move at once: copy the
reference project's `app/handlers/auth.go` and `settings.go`,
`routes/auth.go`, `auth_test.go`, `frontend/src/pages/Auth/` and
`frontend/src/components/SocialButtons.tsx`, then put back the
reference project's `currentUser` in `pages.go`, which returns
`authUser(c)` (the copied `auth.go` has it). If you
changed `make:auth`'s handlers, make the same changes in the copies.

### 7. Remove what's left of templ's pages

When no page renders a templ view (the home page included):

- Delete `views/` but `app.templ` and `auth_mail.templ` (the emails
  stay templ), `views/ui/`, and `public/static/`.
- Take the reference project's `public/public.go` (no `Assets`) and
  `routes/web.go`'s first lines: no `web.MethodOverride` (Inertia sends
  PUT and DELETE as they are) and no `/assets/` route. Keep your
  routes.
- Run `go tool anetos agent:update`, then `go tool anetos check`.

## Complete example

A project of the web stack with `make:auth` and `make:crud`, moved this
way, ends with the reference project's code, but for comments, the
order of `setup`'s lines, the README (which still describes the web
stack: take the reference project's) and the text you changed.

## How it works

The handlers, routes, sessions and validation are the same in both
stacks: what differs is how a page is written. A templ page is HTML
from the server; an Inertia page is a component's name and its props,
which the client renders. On a first visit, the root view
(`views/app.templ`) holds the page; after that, links and forms are
requests the server answers with the next page's props as JSON.

When such a request reaches a handler in the group that writes
ordinary HTML (a templ page), `inertia.Middleware` doesn't send that HTML
to the client, which would show it in an error dialog: it answers
`409` with `X-Inertia-Location`, and the client loads the page whole.
The handler runs twice for that link: once for the client's request,
then for the page load.

## Testing it

After each page: `go test ./...` and a look in the browser. The
reference project's tests show how a page's test reads:

```go
// illustrative
app.Get("/posts").AssertOK().AssertInertia("Posts/Index").AssertInertiaProp("page.total", 0)
```

`go tool anetos check` runs the tests and the frontend's build, which
type-checks every component.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `vite: "src/main.ts" isn't an entry of build/manifest.json` on the templ pages | `vite:install`'s line is still in `views/layout.templ` | Remove `@vite.Tags("src/main.ts")` (step 2) |
| The type check fails on the reference project's pages | Components were copied for pages your project doesn't have yet | Copy a page's components when you move it |
| `undefined: authUser` | `pages.go` came from a project with accounts, before the accounts' handlers | Use step 2's `currentUser` until step 6 |
| A key shows instead of its text | The components read keys the templ pages didn't use | Copy the reference project's keys into `locales/` |
| A form posts but nothing happens | The group lacks `inertia.Middleware`, or CSRF lacks `web.WithXSRFCookie()` | Step 3, for each pages' group |

## Next steps

- [Build pages in React](react.md), [in Svelte](svelte.md), [in
  Vue](vue.md).
- [Pages in Vue, React or Svelte with Inertia](inertia.md): the
  protocol.
- [Move htmx pages to an API and an SPA](migrate-htmx-to-spa.md): the
  other way.
- [Frontends](../concepts/frontends.md).
