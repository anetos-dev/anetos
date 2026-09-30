---
title: Render HTML with templ
since: v0.1.0
---

# Render HTML with templ

Write pages as [templ](https://templ.guide) components (typed, compiled,
escaped by default), share a layout, link to routes by name, serve
static files with cache-busting URLs, and add interactivity with the
bundled htmx.

## Before you start

[Route requests to handlers](routing.md). Pages with forms also need
[sessions](sessions.md).

## Steps

### 1. Add templ to your module

```sh
go get -tool github.com/a-h/templ/cmd/templ@latest
```

Components live in `.templ` files; `go tool templ generate` turns each
into a `_templ.go` file that you commit. Add
`//go:generate go tool templ generate` to one Go file so
`go generate ./...` runs it, and `go tool anetos dev` runs it on every
change.

### 2. Write a layout

A layout is a component that renders its children inside the page shell:

```templ
// Layout is the page shell. Pages pass their content as children.
templ Layout(title string) {
	<!DOCTYPE html>
	<html lang="en">
		<head>
			<meta charset="utf-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1"/>
			<title>{ title } · Notes</title>
			<link rel="stylesheet" href={ assets.URL("app.css") }/>
			<script src={ assets.URL("htmx.min.js") } defer></script>
		</head>
		<!-- htmx sends the CSRF token in a header with every request. -->
		<body hx-headers={ `{"X-CSRF-Token": "` + view.CSRFToken(ctx) + `"}` }>
			<header><a href={ web.URL(ctx, "notes.index") }>Notes</a></header>
			if msg := view.Flash(ctx, "status"); msg != "" {
				<p class="flash" role="status">{ msg }</p>
			}
			<main>
				{ children... }
			</main>
		</body>
	</html>
}
```

(Copied from [`examples/forms/layout.templ`](../../../examples/forms/layout.templ), region `layout`.)

`ctx` is available in every component: it is the request's context, so
the helpers work inside templates. `web.URL(ctx, name, args...)` builds a
route's path; templ accepts its `(string, error)` result in attributes
and text, and a wrong route name fails the render with an error page.

### 3. Write pages and render them

A page wraps its content in the layout:

```templ
// illustrative
templ PostPage(p Post) {
	@Layout(p.Title) {
		<h1>{ p.Title }</h1>
		<p>{ p.Body }</p>
	}
}
```

Render it from a handler with `c.Render`, or return `web.View` from a
typed handler:

```go
// illustrative
func (h Posts) Show(c *web.Ctx, in PostID) (web.Responder, error) {
	p, err := db.Find[Post](c, in.ID)
	if err != nil {
		return nil, err
	}
	return web.View(PostPage(p)), nil
}
```

Rendering is buffered: if a component returns an error, the visitor gets
an error page, not half a page.

### 4. Serve static files

`view.NewAssets` serves the files of one or more file systems and builds
their URLs with a content hash:

```go
// public holds the files of the public directory.
//
//go:embed public
var public embed.FS

// assets serves public/ and the bundled htmx at /assets, with hashed URLs.
var assets = mustAssets()

func mustAssets() *view.Assets {
	files, err := fs.Sub(public, "public")
	if err != nil {
		panic(err)
	}
	a, err := view.NewAssets("/assets", files, htmx.FS)
	if err != nil {
		panic(err)
	}
	return a
}
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `assets`.)

`assets.URL("app.css")` is `/assets/app.css?v=3f2a9c01d4`. Requests
with the current hash may be cached for a year; a changed file gets a new
URL. Mount it with `r.HandleStd(http.MethodGet, "/assets/{path...}", assets)`.
`htmx.FS` holds the bundled htmx (version `htmx.Version`).

### 5. Update parts of a page with htmx

htmx requests carry `HX-Request`; `c.IsHTMX()` tells the handler to answer
with a fragment. Deleting a note in place:

```templ
// noteItem is one note. Its delete form works without JavaScript
// (POST with _method=DELETE); with htmx it sends DELETE and removes the
// item in place.
templ noteItem(n Note) {
	<li>
		<div>
			<strong>{ n.Title }</strong>
			<p>{ n.Body }</p>
		</div>
		<div>
			<a href={ web.URL(ctx, "notes.edit", n.ID) }>Edit</a>
			<form class="inline" method="post" action={ web.URL(ctx, "notes.delete", n.ID) }
				hx-delete={ web.URL(ctx, "notes.delete", n.ID) } hx-target="closest li" hx-swap="outerHTML"
				hx-confirm="Delete this note?">
				@view.CSRFField(ctx)
				@view.MethodField("DELETE")
				<button type="submit">Delete</button>
			</form>
		</div>
	</li>
}
```

(Copied from [`examples/forms/notes.templ`](../../../examples/forms/notes.templ), region `delete-button`.)

```go
func (h Notes) Delete(c *web.Ctx, in NoteID) (web.Responder, error) {
	h.store.Delete(in.ID)
	if c.IsHTMX() {
		// htmx replaces the note's list item with this empty response.
		return web.Text(http.StatusOK, ""), nil
	}
	c.Session().Flash("status", "Note deleted.")
	return web.RedirectRoute("notes.index"), nil
}
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `delete`.)

The layout puts the CSRF token in `hx-headers`, so every htmx request
passes [CSRF protection](forms.md). `c.HTMX()` returns the other htmx
headers (target, trigger, boosted).

### 6. Or use html/template

`view.Template(t, name, data)` turns an `html/template` template into a
component, so `c.Render` works the same way.

## Complete example

[`examples/forms`](../../../examples/forms/main.go) is a notes app with a
layout, forms, flash messages, assets and htmx.

## How it works

A component is anything with `Render(ctx context.Context, w io.Writer)
error`: that is templ's `templ.Component`, so the framework doesn't depend
on templ. `c.Render` renders into a pooled buffer with the `*web.Ctx` as
context and writes it with `Content-Type: text/html; charset=utf-8`.
`c.IsHTMX()` adds `Vary: HX-Request`, so caches keep fragments and full
pages apart.

> **Coming from Laravel?** templ components play the role of Blade views
> and components; `{ children... }` is `$slot`/`@yield`. `web.URL` is
> `route()`, and `assets.URL` replaces `asset()`/`mix()` for files without
> a build step (Vite arrives in v0.4).

## Testing it

Render a component to a string with `view.String(ctx, component)`, or
request the page with `anetostest` and check it with `AssertSee`, as
[`examples/forms`](../../../examples/forms/main_test.go) does.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `undefined: NotesPage` | The `.templ` file wasn't generated | Run `go generate ./...` (or `go tool templ generate`) |
| `web: unknown route name: "…"` when rendering | A typo in a route name | Use the name given with `.Name(…)` |
| `view: no session for this request` | A page uses `view.CSRFField` on a route without the session middleware | Add the middleware to the route's group |
| Browsers keep an old CSS file | A URL written by hand, without the hash | Use `assets.URL(name)` |

## Next steps

- [Handle HTML forms](forms.md)
- [Sessions and flash messages](sessions.md)
- [Views, sessions and forms reference](../reference/views.md)
