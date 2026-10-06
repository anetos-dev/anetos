---
title: Handle HTML forms
since: v0.1.0
group: "Basics"
weight: 104
---

# Handle HTML forms

Protect forms against cross-site request forgery, send PUT and DELETE
from plain HTML, and show validation errors next to the fields with the
submitted values kept.

## Before you start

[Sessions](sessions.md) and [views](views.md). Validation rules work as in
[Validation](validation.md).

## Steps

### 1. Protect the routes

Add `web.CSRF()` after the session middleware, and `web.MethodOverride`
globally:

```go
sessions, err := session.ForApp(app) // SESSION_* settings; needs APP_KEY
if err != nil {
	return nil, err
}
r := srv.Router()
r.UseGlobal(web.MethodOverride) // forms can send PUT and DELETE with _method
r.HandleStd(http.MethodGet, "/assets/{path...}", assets)

var h Notes
pages := r.Group("", sessions.Middleware, web.CSRF())
pages.Get("/", func(c *web.Ctx) error { return c.RedirectRoute("notes.index") })
pages.Get("/notes", web.H(h.Index)).Name("notes.index")
pages.Get("/notes/new", h.New).Name("notes.new")
pages.Post("/notes", web.H(h.Create)).Name("notes.store")
pages.Get("/notes/{id}/edit", web.H(h.Edit)).Name("notes.edit")
pages.Put("/notes/{id}", web.H(h.Update)).Name("notes.update")
pages.Delete("/notes/{id}", web.H(h.Delete)).Name("notes.delete")
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `routes`.)

For every POST, PUT, PATCH and DELETE (any method but GET, HEAD, OPTIONS
and TRACE), `web.CSRF` rejects requests from
other sites (using the browser's `Sec-Fetch-Site` and `Origin` headers)
and requires the session's token, from the `_token` field or the
`X-CSRF-Token` header. Failures are a **403** ("The page has expired.
Reload it and try again."). APIs that authenticate with tokens in headers
don't need it: keep them in a group without it.

### 2. Write the form

```templ
// NoteFormPage creates a note, or edits one when n.ID is set. After a
// failed post, view.Old refills the fields and view.Errors has the messages.
templ NoteFormPage(n Note) {
	@Layout(formTitle(n)) {
		<h1>{ formTitle(n) }</h1>
		if errs := view.Errors(ctx); errs.Len() > 0 {
			<div class="errors" role="alert">Please fix the errors below.</div>
		}
		<form method="post" action={ formAction(ctx, n) }>
			@view.CSRFField(ctx)
			if n.ID != 0 {
				@view.MethodField("PUT")
			}
			<label for="title">Title</label>
			<input type="text" id="title" name="title" value={ view.Old(ctx, "title", n.Title) }/>
			@fieldError("title")
			<label for="body">Body</label>
			<textarea id="body" name="body" rows="6">{ view.Old(ctx, "body", n.Body) }</textarea>
			@fieldError("body")
			<p><button type="submit">Save</button></p>
		</form>
	}
}

// fieldError shows the validation message for a field, if any.
templ fieldError(field string) {
	if msg := view.Errors(ctx).Get(field); msg != "" {
		<p class="error">{ msg }</p>
	}
}
```

(Copied from [`examples/forms/notes.templ`](../../../examples/forms/notes.templ), region `form`.)

- `view.CSRFField(ctx)` renders the hidden `_token` input.
- `view.MethodField("PUT")` renders `_method`, which `web.MethodOverride`
  turns into the request method, so the form reaches the `Put` route. It
  reads URL-encoded bodies only (parsed for binding, and restored for
  handlers that read the raw body); for a form with files
  (`multipart/form-data`), put `?_method=PUT` in the action URL instead.
- `view.Old(ctx, "title", n.Title)` is the value the visitor submitted if
  the last post failed, else the fallback.
- `view.Errors(ctx)` holds the messages of that post, keyed by field.

### 3. Handle the post

A typed handler with validation rules:

```go
func (Notes) Create(c *web.Ctx, in NoteInput) (web.Responder, error) {
	// Invalid input never gets here: the browser is sent back to the form,
	// which shows the errors and the submitted values.
	if err := db.Create(c, &Note{Title: in.Title, Body: in.Body}); err != nil {
		return nil, err
	}
	c.Session().Flash("status", "Note created.")
	return web.RedirectRoute("notes.index"), nil
}
```

(Copied from [`examples/forms`](../../../examples/forms/main.go), region `create`.)

When the rules fail on a form post from a browser, the handler doesn't
run: the errors and the submitted values (except passwords and the token)
are flashed, and the browser is redirected back (303) to the form, which
shows them. The same happens for a `validate.Fail` from a `Validate`
method or the handler, and for values that can't be converted (a letter
in a number field). JSON clients still get a 422 with the errors.

Errors are keyed by the form field name: a field with `form:"content"`
and `json:"body"` gets its message under `content` in a form post.

### 4. Redirect back yourself

`c.Back()` (or the `web.Back()` responder) redirects to the previous page:
the `Referer` if it is on your site (its host is the request's, or the
browser says the request is same-origin), else `/`.

## Complete example

[`examples/forms`](../../../examples/forms/main.go) creates, edits and
deletes notes with these forms.

## How it works

When a handler returns a validation error for a POST, PUT, PATCH or
DELETE from a browser (a navigation, or `Accept: text/html`),
`web.DefaultErrorHandler` flashes the field errors (in order) and
`r.PostForm`, and redirects to the page the form was on (its `Referer`).
On the next request `view.Errors` and `view.Old` read them from the
session; after that they're gone. A custom error handler
(`web.WithErrorHandler`) keeps this only if it passes validation errors
on to `web.DefaultErrorHandler`.

htmx requests are the exception: they get the 422, because htmx swaps
fragments rather than following the redirect to a page. Boosted forms
(`hx-boost`) are page loads, so they are redirected back like the others.

The CSRF token is random, stored in the session, and masked differently
every time it is rendered, so compressed pages don't leak it. Checking
`Sec-Fetch-Site` and `Origin` (Go's `http.CrossOriginProtection`) stops
cross-site posts even before the token is compared. `web.TrustedOrigins`
allows other origins of yours.

> **Coming from Laravel?** `@csrf` is `@view.CSRFField(ctx)`, `@method('PUT')`
> is `@view.MethodField("PUT")`, `old('title', $post->title)` is
> `view.Old(ctx, "title", post.Title)`, and `$errors` is `view.Errors(ctx)`.
> A token mismatch is a 403 instead of 419.

## Testing it

`anetostest` posts forms like a browser: it keeps the session cookie,
sends the CSRF token and the `Referer`, so a failed form redirects back:

```go
// illustrative
app.Get("/notes/new")
app.PostForm("/notes", url.Values{"title": {""}}).
	AssertRedirect("/notes/new").
	AssertValidationErrors("title")
```

See [Test your app](testing.md#2-test-a-form).

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| 403 "The page has expired" | No `_token` field, the session expired, or the session cookie isn't sent back | Add `@view.CSRFField(ctx)`; check `SESSION_SECURE` over plain HTTP |
| 403 "Cross-origin request rejected" | The form is on another origin (a different port counts) | `web.CSRF(web.TrustedOrigins("https://admin.example.com"))` |
| htmx requests get 403 | The token isn't sent | Put `view.CSRFToken(ctx)` in `hx-headers`, as the layout does |
| A 422 page instead of the form with errors | The route has no session middleware, or the client asked for JSON | Add the middleware; browsers send `Accept: text/html` |
| The form is empty after an error | `view.Old` not used, or the input was larger than the cookie allows | Use `view.Old(ctx, field, fallback)`; long text may not be kept (logged) |
| 405 for a form with `_method=PUT` | `web.MethodOverride` not added, or added with `Use`; or a form with files | `r.UseGlobal(web.MethodOverride)`; for files, `action="/posts/1?_method=PUT"` |
| Redirected to `/` after a failed post | The browser sent no `Referer` (a `Referrer-Policy` of `no-referrer`) | Keep the default policy (`strict-origin-when-cross-origin`) |
| An htmx form gets a 422 page | htmx requests aren't redirected back | Use a regular or boosted form, or render the errors yourself |
| curl or a script gets a 422 page instead of a redirect | It sends `Accept: */*`, so it isn't treated as a browser navigation | Send `Accept: text/html` to test the browser flow |
| A checkbox is checked again after a failed post | `view.Old` can't tell an unchecked box (not sent) from no post | `checked?={ view.OldChecked(ctx, "publish", post.Published) }` |

## Next steps

- [Validation rules reference](../reference/validation-rules.md)
- [Views, sessions and forms reference](../reference/views.md)
