---
title: Pages in Vue, React or Svelte with Inertia
since: v0.6.0
group: "Frontends"
weight: 170
---

# Pages in Vue, React or Svelte with Inertia

Write the pages as Vue, React or Svelte components while the routes,
handlers, sessions, validation and redirects stay in Go. A handler
renders a page component with its props, which [Inertia](https://inertiajs.com)'s
client shows; links and forms then visit the server without reloading
the page. There is no JSON API to design: the props are the page's
data.

Package `web/inertia` is the server side of Inertia's protocol (v3). The
client is Inertia's own: `@inertiajs/react`, `@inertiajs/vue3` or
`@inertiajs/svelte`. This guide sets it up in a web project with React;
the Go side is the same for the three.

## Before you start

- A web project with a frontend that Vite builds
  ([Bundle JavaScript and CSS with Vite](vite.md)), and Node.js.
- [Sessions](sessions.md) and [forms](forms.md): Inertia's forms use the
  same redirect back with errors.

## Steps

### 1. Install the client

In `frontend/`:

```sh
npm install @inertiajs/react react react-dom
npm install -D @vitejs/plugin-react @types/react @types/react-dom
```

Add React's plugin to `vite.config.ts` (`plugins: [react()]`), and
`"jsx": "react-jsx"` to `tsconfig.json`'s `compilerOptions`. Make the
entry `src/app.tsx`, which starts Inertia's client and finds each page
in `src/pages`:

```tsx
// illustrative: frontend/src/app.tsx
import { createInertiaApp } from '@inertiajs/react'
import { createRoot } from 'react-dom/client'
import type { ComponentType } from 'react'

const pages = import.meta.glob<{ default: ComponentType }>('./pages/**/*.tsx')

createInertiaApp({
  resolve: (name) => {
    const page = pages[`./pages/${name}.tsx`]
    if (!page) throw new Error(`no page ${name}`)
    return page().then((m) => m.default)
  },
  setup({ el, App, props }) {
    if (el) createRoot(el).render(<App {...props} />)
  },
})
```

List it in `vite.config.ts`'s inputs (`input: ['src/app.tsx']`). The
page `Notes/Index` is then `src/pages/Notes/Index.tsx`.

### 2. Write the root view

The root view is the HTML of a first visit: it loads the frontend and
holds the page for the client. `@inertia.App()` writes the page object
and the element the client renders in:

```templ
// root is the root view: the HTML of a first visit, which loads the
// frontend and holds the page for Inertia's client.
templ root() {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="utf-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1"/>
			<title>Notes</title>
			@vite.Tags("src/app.tsx")
		</head>
		<body>
			@inertia.App()
		</body>
	</html>
}
```

(Copied from [`examples/inertia/app.templ`](../../../examples/inertia/app.templ), region `root`.)

### 3. Set it up

In `main.go`'s `setup`, `inertia.New` takes the root view. Its options
add shared props, which every page gets (the app's name, the logged-in
user), and list the pages, whose props get TypeScript types
(`typescript.Register` adds the command that writes them: see
[Generate TypeScript types for the frontend](typescript.md)). In the routes, `inertia.Middleware` runs after the session and
CSRF middleware; `web.WithXSRFCookie` gives Inertia's client the CSRF
token in the cookie it reads. `inertia.ErrorPages` makes the error pages
the frontend's `Error` component:

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

`vite.WithReactRefresh()` adds what React's hot reload needs in
development; leave it out for Vue and Svelte.

### 4. Declare the pages

A page is declared once, at package level, with the Go type of its
props. Its fields are the props, as `encoding/json` writes them (their
`json` tags, `omitempty`, embedded structs):

```go
// NotesIndex are the props of the list: a page of notes, which the
// client appends to the ones it shows when it loads more, and the
// statistics, which it asks for after the page shows.
type NotesIndex struct {
	Notes inertia.Prop[[]Note] `json:"notes"`
	Next  int                  `json:"next"` // the next page, 0 after the last
	Stats inertia.Prop[Stats]  `json:"stats"`
}

// Stats are the list's statistics, slow to compute.
type Stats struct {
	Count int `json:"count"`
	Words int `json:"words"`
}

// NotesCreate are the props of the form.
type NotesCreate struct {
	MaxTitle int `json:"maxTitle"`
}

var (
	notesIndex  = inertia.NewPage[NotesIndex]("Notes/Index")
	notesCreate = inertia.NewPage[NotesCreate]("Notes/Create")
)
```

(Copied from [`examples/inertia`](../../../examples/inertia/main.go), region `pages`.)

The props' types are the page's contract with the frontend. `NewPage`
reads them once, when the program starts, and panics on a mistake: two
props of one name, or a prop named `errors`, which holds the validation
errors.

### 5. Render them

A handler renders the page with its props:

```go
func (h notes) index(c *web.Ctx) error {
	p, _ := strconv.Atoi(c.Query("page"))
	list, next := h.store.page(max(p, 1), 10)
	return notesIndex.Render(c, NotesIndex{
		Notes: inertia.Merge(list).MatchOn("id"),
		Next:  next,
		Stats: inertia.Defer(func(ctx context.Context) (Stats, error) {
			time.Sleep(300 * time.Millisecond) // a slow query
			return h.store.stats(), nil
		}),
	})
}

func (h notes) create(c *web.Ctx) error {
	return notesCreate.Render(c, NotesCreate{MaxTitle: 60})
}

// save saves a note; an invalid one is sent back to the form with its
// errors, which the page gets in its errors prop.
func (h notes) save(c *web.Ctx, in NoteInput) (web.Responder, error) {
	n := h.store.add(in)
	inertia.Flash(c, "toast", "Saved “"+n.Title+"”.")
	return web.RedirectRoute("notes.index"), nil
}
```

(Copied from [`examples/inertia`](../../../examples/inertia/main.go), region `handlers`.)

The component gets the props, typed from the Go types
(`frontend/src/types/pages.ts`, which `go run . types` writes):

```tsx
// illustrative: frontend/src/pages/Notes/Index.tsx
import type { PageProps } from '../../types/pages'

export default function Index({ notes = [], next, stats }: PageProps<'Notes/Index'>) {
  // …
}
```

A failed post is sent back to the page it came from, as with templ
pages, and the page gets the field errors in its `errors` prop
(`{"title": "The title field is required."}`). Inertia's `<Form>` and
`useForm` show them:

```tsx
// illustrative: frontend/src/pages/Notes/Create.tsx
<Form action="/notes" method="post">
  {({ errors }) => (
    <>
      <input name="title" />
      {errors.title && <p>{errors.title}</p>}
      <button type="submit">Save</button>
    </>
  )}
</Form>
```

A form that names an error bag (`errorBag: 'createNote'`) gets them
under it (`errors.createNote.title`).

### 6. Choose how each prop loads

A plain field is sent with every visit of the page. An `inertia.Prop[T]`
field loads as you say:

| Set it to | The prop |
|---|---|
| `inertia.Func(fn)` | Is computed by `fn` only when the response includes it: a partial reload that leaves it out doesn't run `fn` |
| `inertia.Defer(fn)` | Is left out of the visit and loaded by the client right after the page shows, so a slow query doesn't hold the page. `.Group("charts")` loads the props of a group together; `.Rescue()` leaves a failed one out instead of failing the response |
| `inertia.Optional(fn)` | Loads only when the client asks for it (`router.reload({ only: ['users'] })`) |
| `inertia.Merge(v)` | Is appended to the value the page holds on a partial reload: "load more". `.Append("data")` and `.Prepend(…)` merge arrays inside it; `.MatchOn("id")` replaces the items with the same id instead of adding them again; `.DeepMerge()` merges at every depth instead of appending |
| `inertia.Once(fn)` | Is loaded once and kept by the client across pages, until it expires (`.ExpiresIn(time.Hour)`) |
| `inertia.Always(v)` | Is sent even when a partial reload asks for other props |

Their methods combine: `inertia.Defer(fn).Append()` is a deferred list
that grows. A `Prop` field you don't set is left out of the page.

### 7. Messages, redirects and history

- `inertia.Flash(c, "toast", "Saved.")` gives the next page Inertia
  renders, here or after a redirect, data in `page.flash`
  (`usePage().flash` in React). It, `ClearHistory` and
  `EncryptHistory` need `inertia.Middleware` on the route.
- `c.Redirect` and `c.RedirectRoute` work as usual; the middleware turns
  a 302 after PUT, PATCH or DELETE into a 303, so the browser follows it
  with a GET.
- `inertia.Location(c, url)` sends the browser to another site, or to a
  page that isn't Inertia's (a download), with a full page load.
- `inertia.ClearHistory(c)` at logout makes the pages kept in the
  browser's history unreadable, with history encryption
  (`inertia.WithEncryptHistory()`, HTTPS only).

## Complete example

[`examples/inertia`](../../../examples/inertia) is a notes app in React:
a list that loads more notes and its statistics after the page shows, a
form with errors from the server, a message after saving, deleting, and
the error page. Its tests run without Node; its frontend runs with
`go tool anetos dev`.

## How it works

The first visit is an ordinary GET: the handler's `Render` answers with
the root view, whose `<script data-page="app" type="application/json">`
holds the page object (the component, the props, the URL and the asset
version). The client renders it, then takes over the links and forms:
each later visit is a request with `X-Inertia: true`, answered with the
page object as JSON, and the client swaps the component in place.

- **Partial reloads** ask for some props (`X-Inertia-Partial-Data`); the
  others aren't computed when they are `Func`, `Defer` or `Optional`
  props.
- **Asset versions:** the page carries the frontend build's version (a
  hash of Vite's manifest). After a deploy, a client with the old one
  gets a 409, and loads the page whole with the new scripts; flashed
  messages and errors are kept for that load.
- **CSRF:** the `XSRF-TOKEN` cookie holds the session's token, which the
  client sends back in `X-XSRF-TOKEN`. `web.CSRF` checks it as it checks
  a form's `_token`.
- **Errors:** a 404, a 403 or a 500 is the `Error` component with
  `status`, `title`, `detail` and `requestId`, in the page, instead of
  the client's error dialog. In debug mode (`APP_DEBUG`), a 500 is still
  the debug page, which the client shows in its dialog.

Server-side rendering, infinite scroll and Precognition (validating as
the visitor types) aren't supported yet.

> **Coming from another framework?** This is the Inertia protocol that
> the adapters of other frameworks implement (Django, Laravel, Rails).
> The props are a Go struct per page rather than a dictionary, so the
> frontend's TypeScript types can come from it.

## Testing it

The tests need neither Node nor a build. `app.AsInertia()` makes the
requests the client's; `AssertInertia` and `AssertInertiaProp` check the
page, and `InertiaPage()` returns the whole page object:

```go
func TestNotes(t *testing.T) {
	app := anetostest.New(t, setup)

	// The first visit: the root view's HTML holding the page.
	app.Get("/notes").AssertOK().AssertInertia("Notes/Index").AssertInertiaProp("next", 2)

	// The client's visits after it get the page object.
	app.AsInertia()
	page := app.Get("/notes").AssertOK().InertiaPage()
	if page.DeferredProps["default"][0] != "stats" || page.MergeProps[0] != "notes" {
		t.Errorf("deferred %v, merged %v", page.DeferredProps, page.MergeProps)
	}

	// An invalid note goes back to the form with its errors.
	app.Get("/notes/new").AssertInertia("Notes/Create")
	app.PostJSON("/notes", NoteInput{Title: ""}).AssertRedirect("/notes/new")
	app.Get("/notes/new").AssertInertiaProp("errors", map[string]string{"title": "The title field is required."})

	// A valid one is saved, with a message for the next page.
	app.PostJSON("/notes", NoteInput{Title: "Groceries"}).AssertRedirect("/notes")
	page = app.Get("/notes").InertiaPage()
	if string(page.Flash["toast"]) != `"Saved “Groceries”."` {
		t.Errorf("flash %s", page.Flash["toast"])
	}
}
```

(Copied from [`examples/inertia/main_test.go`](../../../examples/inertia/main_test.go), region `test`.)

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| A post answers `403` "The page has expired" | The client has no `XSRF-TOKEN` cookie | Add `web.WithXSRFCookie()` to `web.CSRF` in the pages' group |
| `inertia: no Inertia in the context` | `inertia.New` wasn't called in `setup` | Call it in `setup` |
| The client shows a dialog with an HTML page | The response isn't Inertia's: a route outside the group with `inertia.Middleware`, or an error without `inertia.ErrorPages` | Put the page's route in the group, and set the error pages |
| Every visit reloads the whole page | The asset version changes on each request, or the client sends another one | Leave the version to Vite's build (the default), or give `inertia.WithVersion` a value that changes only with the assets |
| `inertia: NewPage("…"): … the prop name errors is Inertia's` | A prop named `errors` | Rename it: the page gets the validation errors there |

## Next steps

- [Bundle JavaScript and CSS with Vite](vite.md): the frontend's build
  and development server.
- [Generate TypeScript types for the frontend](typescript.md): the
  props' types, kept in step with the Go code.
- [Views, sessions and forms reference](../reference/views.md#inertia-pages-webinertia):
  every function of `web/inertia`.
