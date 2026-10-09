---
title: Style your app
since: v0.3.0
group: "Basics"
weight: 103
---

# Style your app

A new project looks finished from the first page. Its pages are made of
components in `views/ui`, a package of your app, styled by one
stylesheet, `public/static/app.css`. Change the colors, change a
component, add your own, or start without styles.

## Before you start

A project made with `anetos new` v0.5 or later. (A project made before
v0.5 has no `views/ui` yet: see [below](#projects-made-before-v05).)
There is no build step, no CDN and no JavaScript: `anetos dev` reloads
the page when you save a file.

## Steps

### 1. Know what's there

The look of your app is two parts, which together are a *design kit*:

| File | Holds |
|---|---|
| `views/ui/shell.templ` | The page's shell, which the layout puts together: `Head`, `Header`, `Nav`, `NavLink`, `NavEnd`, `Main`, `Footer`, `Flash` |
| `views/ui/page.templ` | A page's structure: `PageHeader`, `Card`, `Narrow`, `Stack`, `Cluster`, `Empty`… |
| `views/ui/form.templ` | Forms: `Form`, `Field`, `Input`, `Textarea`, `Select`, `Checkbox`, `Button`, `LinkButton`, `PostButton`… |
| `views/ui/data.templ` | Lists and values: `Table`, `Details`, `Badge`, `Pagination`… |
| `views/ui/ui.go` | The types the components take: `Look`, `Tone`, `Option`, `Pages` |
| `views/ui/classes.go` | The starter theme's classes for each look and tone (not in the `none` kit) |
| `public/static/app.css` | The starter theme: light and dark, about 270 lines of plain CSS |

Only `views/ui` has class names. The layout (`views/layout.templ`), the
home page and the pages of `make:crud` and `make:auth` call the
components, and use plain HTML elements, without classes, for the rest:
headings, paragraphs, links, table rows. The
[UI components reference](../reference/ui.md) lists every component.

### 2. Build pages with the components

A page calls the components for its structure. The tutorial's list of
issues:

```templ
// IssuesPage lists a page of issues, with links to the pages around it.
templ IssuesPage(page db.Page[models.Issue]) {
	@Layout("Issues") {
		@ui.PageHeader("Issues", "") {
			@ui.LinkButton(web.MustURL(ctx, "issues.new"), ui.Primary) {
				New issue
			}
		}
		if page.Total == 0 {
			@ui.Empty("No issues yet.")
		} else {
			@ui.Table() {
				<tbody>
					for _, issue := range page.Data {
						<tr>
							<td>
								@statusBadge(issue.Status)
							</td>
							<td><a href={ templ.URL(fmt.Sprintf("/issues/%d", issue.ID)) }>{ issue.Title }</a></td>
							<td><small>#{ issue.ID } by { issue.Author.Name }</small></td>
						</tr>
					}
				</tbody>
			}
		}
		if page.LastPage > 1 {
			@ui.Pagination(ui.PagesOf(ctx, page, "Pages", "", "Newer", "Older"))
		}
	}
}

// statusBadge shows an issue's status: green while it's open.
templ statusBadge(status string) {
	if status == "open" {
		@ui.Badge(ui.Success) {
			{ status }
		}
	} else {
		@ui.Badge(ui.Neutral) {
			{ status }
		}
	}
}
```

(Copied from [`examples/tutorial/views/issues.templ`](../../../examples/tutorial/views/issues.templ), region `list`.)

- A component with content takes it as children, in `{ }`:
  `ui.PageHeader` puts its children (the buttons) beside the title.
- A button's look is one of `ui.Primary` (the default), `ui.Secondary`,
  `ui.Danger` and `ui.Ghost`, with `ui.Small` or `ui.Full` added:
  `ui.Secondary|ui.Small`.
- A badge's or a message's tone is `ui.Neutral`, `ui.Success`,
  `ui.Warning`, `ui.Error` or `ui.Info`.
- Components take URLs as strings. `web.MustURL(ctx, name, args...)` is
  the path of a named route, as `web.URL` is, without the error: a name
  that doesn't exist is a panic, which the router answers with a 500
  page ([Render HTML with templ](views.md#2-write-a-layout)).

### 3. Change the colors

The colors, radius, fonts and width are variables at the top of
`app.css`:

```css
/* illustrative */
:root {
	--primary: #3056d3;       /* links and buttons */
	--primary-hover: #2445b4;
	--radius: 8px;
	--font: system-ui, sans-serif;
	--width: 64rem;           /* the page's width */
}
```

Change `--primary` and `--primary-hover` for your brand. Dark mode
follows the visitor's system and has its own values below; set
`data-theme="dark"` (or `"light"`) on `<html>` in `views/layout.templ`
to force one.

### 4. Change a component

The components are yours: change the markup they write, and every page
that calls them follows. Keep each component's name and arguments:
the pages of `make:crud` and `make:auth` call them as they are. For
example, an empty list with a picture, in `views/ui/page.templ`
(which then imports your module's `public` package, as `shell.templ`
does):

```templ
// illustrative
// Empty says a list has nothing in it yet.
templ Empty(text string) {
	<div class="empty">
		<img src={ public.Assets.URL("empty.svg") } alt=""/>
		<p>{ text }</p>
	</div>
}
```

Then add the rules for it at the end of `app.css`. If a component needs
another argument, add a new component rather than change the old one's
signature; if you do change it, `go build` lists every call to update.

### 5. Add your own component

Markup you repeat belongs in a component of its own. Put it in
`views/ui`, in a file of your own, so the class names stay in one
package:

```templ
// illustrative
package ui

// Stat is a number with its label, as on a dashboard.
templ Stat(label, value string) {
	<div class="card stat">
		<p class="muted">{ label }</p>
		<p class="stat-value">{ value }</p>
	</div>
}
```

A page calls it like the others: `@ui.Stat("Open issues",
strconv.Itoa(open))`. The theme has a few classes that no component
uses yet, such as `grid` (cards side by side, wrapping), for components
of your own.

### 6. Add your own stylesheets

Add rules at the end of `app.css`, or put another file in
`public/static/` and link it from `ui.Head`, in `views/ui/shell.templ`:

```templ
// illustrative
templ Head() {
	<link rel="stylesheet" href={ public.Assets.URL("app.css") }/>
	<link rel="stylesheet" href={ public.Assets.URL("mine.css") }/>
}
```

`public.Assets.URL` adds a version to the URL, so browsers fetch the new
file after a deploy; the binary embeds `public/`.

### 7. Or start without styles

```sh
anetos new blog --css=none
```

writes the same components, with the same names and arguments, but
their markup is plain HTML without classes, and `app.css` is a comment.
A table has no scrolling box, a badge is a plain `<span>`, and a
button's look and a message's tone change nothing. Style the elements
in `app.css`, or put a CSS framework's classes in the components and
link its stylesheet from `ui.Head`. Before v0.5, `--css=none` kept the
starter theme's class names in the pages.

Ready-made design kits for Pico, Bootstrap, Bulma and Tailwind, and a
command to switch a project's kit, are coming; they aren't available
yet. A kit is `views/ui` with its `app.css`, so another kit restyles
every page that calls the components.

### Pages with classes of your own

A page can still use classes of its own (`<div class="hero">`), styled
by your rules in `app.css`. They keep working, but they are outside the
kit: a future kit switch restyles the components, not your pages'
classes. When the markup repeats, make it a component (step 5).

## Projects made before v0.5

A project made before v0.5 has no `views/ui`: its layout and pages
carry the starter theme's classes themselves. Nothing changes until
you run a generator. The first `make:crud` or `make:auth` writes
`views/ui` before the pages that call it, and says so:

```text
views/ui is the anetos kit's components, which the new pages call: the project had none (made before v0.5).
The layout keeps its markup; the upgrade guide shows how to use them there too.
```

It picks the kit from your stylesheet: `anetos` when
`public/static/app.css` has the starter theme's `.card` rule, else
`none` (classless markup, for a stylesheet of your own). It doesn't
change `app.css`, the layout or your other pages. Both kinds of layout
work with the generators: `make:crud` adds its link at the end of the
header's nav, and `make:auth` adds `@AccountMenu()` after it, whether
the nav is a `<nav>` or a `@ui.Nav` block.

To build the layout from the components too, compare it with the
`views/layout.templ` of a new project (`anetos new` in another folder):
`@ui.Head()` in `<head>`, then `ui.Header`, `ui.Nav`, `ui.Main`,
`ui.Footer` and `ui.Flash` in place of the markup with classes.

## How it works

`views/ui` is an ordinary package of your app. Nothing in the framework
imports it, and updating Anetos never changes it, or `app.css`. A newer
version's kit is in a new project.

Some components read the request's context (`ctx`): `ui.Form` adds the
CSRF token, `ui.Field` shows its field's validation message, and
`ui.Input`, `ui.Textarea`, `ui.Select` and `ui.Checkbox` are marked
`aria-invalid="true"` when their field failed validation, which the
theme shows with a red border. `ui.NavLink` marks the current page with
`aria-current="page"` (the layout's `navLink` asks `web.RouteIs`), which
the theme highlights.

The theme is variables, then base styles for the elements, then the
classes. Forms and tables need no class: plain HTML looks right as it
is.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| `undefined: ui.Stat` (or another component) | `views/ui` has no such component: it was renamed, deleted, or never there | Add it to `views/ui`, or copy it from a new project's |
| A generated page doesn't build after you changed a component | The pages of `make:crud` and `make:auth` call the component's old arguments | Keep the signature; add a new component for the new arguments |
| A 500 page and `web: unknown route name` in the log | `web.MustURL` was given a route name that doesn't exist | Fix the name; `go run . routes:list` lists them |
| The new pages of a project made before v0.5 have no style | `app.css` has no `.card` rule, so the classless kit was written | Style the elements, or give the components your classes |

## Next steps

- [UI components reference](../reference/ui.md): every component and
  its arguments
- [Add pages for a model](../getting-started/crud.md) with `make:crud`
- [Render HTML with templ](views.md)
