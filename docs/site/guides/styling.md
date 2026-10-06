---
title: Style your app
since: v0.3.0
group: "Basics"
weight: 103
---

# Style your app

A new project looks finished from the first page: `anetos new` writes a
small stylesheet, the starter theme, that the generators' pages use.
Change it, replace it, or start without it.

## Before you start

A project made with `anetos new` (v0.3 or later). The theme is one file,
`public/static/app.css`: no build step, no CDN, no JavaScript. The
layout links it; `anetos dev` reloads the page when you save it.

## Steps

### 1. Use what's there

Plain HTML looks right as it is: headings, links, forms, buttons,
tables, code. A few classes do the rest, and the pages of `make:crud`
and `make:auth` use them:

| Class | For |
|---|---|
| `container` | The page's width (64rem), with side margins |
| `topbar`, `brand`, `nav`, `nav-end` | The layout's header: the app's name, its links, the account links |
| `page-header` | A page's title with its buttons, side by side |
| `narrow` | A column for forms and settings (42rem) |
| `card`, `card-header`, `auth-card` | A box; with a title and buttons; a small centered box (login) |
| `field`, `hint`, `error`, `check`, `form-actions` | A label with its input; help text; a validation message; a checkbox with its label; the buttons under a form |
| `button` (on a link), `secondary`, `danger`, `ghost`, `small`, `full` | Buttons: a link that looks like one; the other kinds; sizes |
| `table-wrap` | A table in a box that scrolls sideways on phones |
| `badge` (+ `success`, `warning`, `danger`, `info`) | A short status: "Published", "Open" |
| `flash` (+ `warning`, `error`) | A message at the top of the page |
| `details` | A `<dl>` of labels and values, in two columns |
| `pagination`, `empty` | Links to other pages of a list; what a list shows when it is empty |
| `stack`, `cluster`, `grid`, `muted`, `prewrap`, `inline`, `sr-only` | Spacing and small helpers |

For example, a page with a title, a button and a box:

```templ
// illustrative
templ ProjectsPage(projects []models.Project) {
	@Layout("Projects") {
		<div class="page-header">
			<h1>Projects</h1>
			<a class="button" href={ web.URL(ctx, "projects.new") }>New project</a>
		</div>
		for _, p := range projects {
			<div class="card">
				<h2>{ p.Name }</h2>
				<span class="badge success">Active</span>
			</div>
		}
	}
}
```

### 2. Change the colors

The colors, radius, fonts and width are variables at the top of
`app.css`:

```css
:root {
	--primary: #3056d3;   /* links and buttons */
	--radius: 8px;
	--font: system-ui, …;
	--width: 64rem;       /* the container */
}
```

Change `--primary` and `--primary-hover` for your brand. Dark mode
follows the visitor's system and has its own values below; set
`data-theme="dark"` (or `"light"`) on `<html>` in `views/layout.templ`
to force one.

### 3. Add your own styles

Add rules at the end of `app.css`, or another file in `public/static/`
linked from the layout:

```templ
// illustrative
<link rel="stylesheet" href={ public.Assets.URL("app.css") }/>
<link rel="stylesheet" href={ public.Assets.URL("mine.css") }/>
```

`public.Assets.URL` adds a version to the URL, so browsers fetch the new
file after a deploy; the binary embeds `public/`.

### 4. Or start without it

```sh
anetos new blog --css=none
```

writes an almost empty `app.css`. The pages' markup is the same, with
the class names above, so you can style them yourself or put a CSS
framework's classes in their place: link Bootstrap or Bulma's
stylesheet from the layout, or set up Tailwind's command-line tool to
write `public/static/app.css`. Ready-made starter kits for those, and
for front-end stacks such as Vue or React, are planned for v0.4.

## How it works

The theme is about 270 lines of plain CSS: variables, then base
styles for elements, then the classes. Forms and tables need no class;
inputs get a red border when the field failed validation
(`aria-invalid="true"`, which `make:crud`'s forms set). The layout's
`navLink` marks the link of the current page with
`aria-current="page"` (`web.RouteIs`), which the theme highlights.

It is yours: nothing in the framework reads it, and updating Anetos
never changes it. A newer version's theme is in a new project's
`public/static/app.css`.

## Next steps

- [Add pages for a model](../getting-started/crud.md) with `make:crud`
- [Render HTML with templ](views.md)
- [Views, sessions and forms reference](../reference/views.md)
