---
title: UI components reference
since: v0.5.0
group: "Web"
weight: 205
---

# UI components reference

The components of `views/ui`, the package `anetos new` writes into a
web project (since v0.5), which the layout and the pages of `make:crud`
and `make:auth` call. The package is your app's: this page describes it
as `anetos new` writes it. See [Style your app](../guides/styling.md)
for changing it.

Every component exists in both kits, with the same name and arguments.
The tables show what the `anetos` kit (the starter theme) writes;
[the `none` kit](#the-none-kit) writes the same elements without
classes.

```templ
// illustrative
@ui.Card("Profile") {
	@ui.Form(web.MustURL(ctx, "settings.profile"), "POST", nil) {
		@ui.Field("name", "Name", "As others see it") {
			@ui.Input("name", "", view.Old(ctx, "name", user.Name), templ.Attributes{"autocomplete": "name"})
		}
		@ui.Actions() {
			@ui.Button(ui.Primary, nil) {
				Save
			}
		}
	}
}
```

A component marked *children* takes content in `{ }`. An `attrs`
argument (`templ.Attributes`, or `nil`) adds attributes to the element,
such as `templ.Attributes{"autocomplete": "email"}` or htmx's
`hx-post`; it shouldn't repeat one the component writes.

## The page's shell

`views/ui/shell.templ`. The layout (`views/layout.templ`) puts these
together.

| Component | Children | Writes |
|---|---|---|
| `Head()` | | `<link rel="stylesheet">` to `app.css` (`public.Assets.URL`), for the layout's `<head>` |
| `Header(brand, home string)` | the `Nav` and `NavEnd` | `<header class="topbar">` with the app's name, `brand`, linking to `home` |
| `Nav(label string)` | `NavLink`s | `<nav class="nav">`, named `label` for screen readers (`aria-label`) |
| `NavLink(href, label string, current bool)` | | A link of the header; `aria-current="page"` when `current` |
| `NavEnd()` | links and buttons | `<div class="nav-end">`: the end of the header, for the account's links (`make:auth`'s `AccountMenu`) |
| `Main()` | the page | `<main class="container">` |
| `Footer()` | its content | `<footer class="site-footer">` |
| `Flash(tone Tone)` | the message, and any buttons | `<div class="flash cluster …" role="status">` in the tone's color |

## A page's structure

`views/ui/page.templ`.

| Component | Children | Writes |
|---|---|---|
| `PageHeader(title, lead string)` | the page's actions | `<div class="page-header">`: the title in an `<h1>`, then `lead` in a paragraph when it isn't `""`, and the children at the end |
| `Narrow()` | content | `<div class="narrow">`: a column of a readable width (42rem), for forms and text |
| `Stack()` | content | `<div class="stack">`: its children one under the other, evenly spaced |
| `Cluster()` | content | `<div class="cluster">`: its children side by side, wrapping (buttons, a search field and its button) |
| `Card(title string)` | content | `<section class="card">`, with the title in an `<h2>` when it isn't `""` |
| `CardHeader(title string)` | the card's actions | `<div class="card-header">`: an `<h2>` with the children beside it, at the top of a `Card("")` |
| `AuthCard(title string)` | content | `<div class="card auth-card">`: a small centered card under an `<h1>`, as the sign-in pages are |
| `Empty(text string)` | | `<p class="empty">`: what a list shows when it has nothing |
| `Note()` | text | `<p class="muted">`: secondary text |
| `SROnly(text string)` | | `<span class="sr-only">`: text for screen readers only, such as a column's heading |

## Forms

`views/ui/form.templ`. A control (`Input`, `Textarea`, `Select`,
`Checkbox`) is named after its field, which is its `id` too (two
controls of one name on a page would share an id: give one form's
field another name). Pass `view.Old(ctx, name, current)` as its value,
so it shows what was typed when validation failed.

| Component | Children | Writes |
|---|---|---|
| `Form(action, method string, attrs templ.Attributes)` | the fields | For `"POST"`: `<form method="post">` with the CSRF token (`view.CSRFField`). For `"PUT"`, `"PATCH"` or `"DELETE"`: the same, with the method in a `_method` field (`view.MethodField`). For `"GET"`: `<form method="get">`, without either. Upper or lower case |
| `InlineForm(action, method string, attrs templ.Attributes)` | the controls | `<form method="post" class="inline">` with the CSRF token, and `_method` for PUT, PATCH or DELETE: controls in a line, such as a hidden field and its button; `attrs` adds others (htmx's). It always posts |
| `Field(name, label, hint string)` | a control | `<div class="field">`: a `<label for={ name }>` (none when `label` is `""`), the control, the hint (`<p class="hint">`) when it isn't `""`, and the field's message (`FieldError`) |
| `Input(name, typ, value string, attrs templ.Attributes)` | | `<input>`, of type `typ` (none, so text, when `""`), with `value` unless it is `""` |
| `Textarea(name, value string, attrs templ.Attributes)` | | `<textarea>` holding `value` |
| `Select(name string, options []Option, value string, attrs templ.Attributes)` | | `<select>` with an `<option>` per `Option`, the one whose `Value` is `value` selected |
| `Checkbox(name, label string, checked bool, attrs templ.Attributes)` | | `<label class="check">` around a checkbox sending `1`, then `label`. It has its own label: wrap it in `Field(name, "", "")` for the field's message. Pass `view.OldChecked(ctx, name, current)` as `checked` |
| `FieldError(name string)` | | `<p class="error" id="name-error">` with the field's validation message, or nothing; the control is described by it (`aria-describedby`) |
| `Actions()` | buttons and links | `<div class="form-actions">`: a form's buttons, after its fields |
| `Button(look Look, attrs templ.Attributes)` | its text | `<button type="submit">`, with the look's classes |
| `LinkButton(href string, look Look)` | its text | `<a class="button …">`: a link that looks like a button |
| `PostButton(action, method string, look Look)` | its text | A `Button` in an `InlineForm` of its own: sends `method` (`"POST"`, or `"PUT"`, `"PATCH"`, `"DELETE"`) to `action` with the CSRF token, to delete something or log out |

## Lists and values

`views/ui/data.templ`.

| Component | Children | Writes |
|---|---|---|
| `Table()` | `<thead>` and `<tbody>` | `<table>` in a `<div class="table-wrap">`, which scrolls sideways when the table is too wide |
| `ActionsCell()` | buttons | `<td class="actions">`: a row's cell of buttons, at its end |
| `Details()` | `Detail`s | `<dl class="details">`: terms and their values, in two columns |
| `Detail(term string)` | the value | `<dt>` with `term`, then `<dd>` with the children |
| `Multiline(text string)` | | `<span class="prewrap">`: text shown with its line breaks |
| `Badge(tone Tone)` | its text | `<span class="badge …">`: a short label (a status, a count) in the tone's color |
| `Pagination(p Pages)` | | `<nav class="pagination">` named `p.Label`: a link to `p.Prev` (`rel="prev"`) unless it is `""`, `p.Status` unless it is `""`, and a link to `p.Next` (`rel="next"`) unless it is `""` |

## Looks

A button's look (`ui.Look`) is one kind, with sizes added with `|`:
`ui.Secondary|ui.Small`. `Button`, `LinkButton` and `PostButton` take
it.

| Constant | For | Classes (`anetos` kit) |
|---|---|---|
| `ui.Primary` | The page's main action; the default (zero) look | `button` on a link; none on a `<button>`, which looks primary as it is |
| `ui.Secondary` | Another action | `secondary` |
| `ui.Danger` | An action that deletes, or can't be undone | `danger` |
| `ui.Ghost` | A quiet action, as in the header | `ghost` |
| `ui.Small` | Added: smaller, as in a table's row | `small` |
| `ui.Full` | Added: as wide as its container | `full` |

## Tones

What a color means (`ui.Tone`), for `Badge` and `Flash`.

| Constant | Classes (`anetos` kit) |
|---|---|
| `ui.Neutral` | none (gray) |
| `ui.Success` | `success` |
| `ui.Warning` | `warning` |
| `ui.Error` | `danger` (a `Flash`: `error`) |
| `ui.Info` | `info` |

## Types

In `views/ui/ui.go`, shared by every kit.

| Type | Holds |
|---|---|
| `ui.Option` | An option of a `Select`: `Value` (sent), `Label` (shown) |
| `ui.Pages` | What `Pagination` shows: `Label` (the links' name for screen readers), `Status` (`"Page 2 of 5"`), `Prev` and `Next` (the URLs of the pages around it, `""` at the ends), `PrevLabel` and `NextLabel` (their links' text) |
| `ui.PagesOf(ctx, page, label, status, prev, next)` | The `Pages` of a `db.Page[T]`, with those texts: `Prev` when `page.HasPrev()` (the last page when the page is past it), `Next` when `page.HasMore()`, built with `web.PageURL`, so the links keep the query's other parameters (a search, a filter) |

```templ
// illustrative
if page.LastPage > 1 {
	@ui.Pagination(ui.PagesOf(ctx, page, "Pages", fmt.Sprintf("Page %d of %d", page.CurrentPage, page.LastPage), "Previous", "Next"))
}
```

## What reads the request

Some components read the request's context (`ctx`), so they need a page
rendered for a request (`c.Render`, `web.View`):

| Component | Reads |
|---|---|
| `Form` (but GET), `InlineForm`, `PostButton` | The session's CSRF token (`view.CSRFField`): rendering fails with `view.ErrNoSession` on a route without the session middleware |
| `Field`, `FieldError` | The field's validation message (`view.Errors`) |
| `Input`, `Textarea`, `Select`, `Checkbox` | Whether their field failed validation: then `aria-invalid="true"`, which the theme shows with a red border, and `aria-describedby` naming the message |
| `ui.PagesOf` | The request's URL, for the page links (`web.PageURL`) |

The others write only their arguments. Arguments made with
`web.MustURL` need a request too: rendering a page outside one (with
`context.Background()` in a test) panics where `web.URL` returned an
error.

## Helpers for the components' arguments

These are the framework's, not `views/ui`'s; the
[views reference](views.md) has the rest.

| Function | Does |
|---|---|
| `web.MustURL(ctx, name, args...)` | The path of a named route, as `web.URL`, for a component's string arguments (`ui.LinkButton(web.MustURL(ctx, "posts.edit", post.ID), ui.Secondary)`). A name or arguments the route doesn't take is a panic, which the router answers with a 500 page (v0.5) |
| `web.RouteIs(ctx, names...)` | Whether the request's route has one of the names, for `NavLink`'s `current` (the layout's `navLink` uses it) |
| `view.Old(ctx, field, fallback...)` | A control's value: what was typed, after a failed post, else the fallback |
| `view.OldChecked(ctx, field, fallback)` | A `Checkbox`'s `checked`, the same way |

## The `none` kit

`anetos new --css=none` writes the same components, with plain HTML
and no classes. The differences:

| Component | In the `none` kit |
|---|---|
| `Header`, `Footer` | No inner `<div class="container">` |
| `Main` | `<main>` without its class |
| `Field` | A `<br/>` after the label, so the control is under it |
| `Button`, `LinkButton`, `NavLink`, `Badge` | Followed by a space, so side by side they don't touch |
| `AuthCard` | A `<section>` with its `<h1>` |
| `Table` | The `<table>` alone, without the scrolling box |
| `Button`, `LinkButton`, `PostButton` | The look changes nothing |
| `Badge`, `Flash` | The tone changes nothing |
| `SROnly`, `Multiline` | An inline `style` hides the text visually, or keeps its line breaks (a Content-Security-Policy without `'unsafe-inline'` for styles blocks it: give them classes then) |

Its `app.css` holds only a comment, and there is no `classes.go`. Kits
for CSS frameworks (Pico, Bootstrap, Bulma, Tailwind) are coming in
later releases.
