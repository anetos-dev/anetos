---
title: Handlers and requests
since: v0.1.0
---

# Handlers and requests

Write handlers that receive typed input, return typed output, and turn
errors into proper HTTP responses.

## Before you start

Read [Routing](routing.md) to see how handlers are registered.

## Steps

### 1. Describe the input

An input struct says where each value comes from:

```go
// Note is what the API returns.
type Note struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateNote is the input for POST /notes. It binds from JSON or a form,
// and the validate rules run before the handler.
type CreateNote struct {
	Title string   `json:"title" validate:"required|max:200"`
	Body  string   `json:"body" validate:"max:10000"`
	Tags  []string `json:"tags" validate:"max:5|distinct|alpha_dash"`
}

// Validate runs after the tag rules pass, for checks tags can't express.
func (in CreateNote) Validate(context.Context) error {
	if strings.EqualFold(strings.TrimSpace(in.Title), "untitled") {
		return validate.Fail("title", "Please give the note a real title.")
	}
	return nil
}

// ListNotes reads its input from the query string.
type ListNotes struct {
	Limit int `query:"limit" validate:"min:0|max:100"`
}

// NoteID reads the {id} path parameter.
type NoteID struct {
	ID int `path:"id"`
}
```

(Copied from [`examples/notes`](../../../examples/notes/main.go), region
`types`.)

| Tag | Source |
|---|---|
| `json:"title"` | JSON body. Also used as the form field name when there's no `form` tag |
| `form:"title"` | URL-encoded or multipart form field |
| `query:"page"` | Query string (`[]T` collects repeated values) |
| `path:"id"` | Path wildcard `{id}` |
| `header:"X-Token"` | Request header |
| `validate:"required\|max:200"` | Not a source: [validation rules](validation.md) checked after binding |

Values are converted to the field's type: strings, numbers, booleans,
`time.Duration`, `time.Time` (RFC 3339), pointers, and any type with an
`UnmarshalText` method. Uploaded files are `*multipart.FileHeader` or
`[]*multipart.FileHeader` with a `form` tag. See the
[binding reference](../reference/binding.md).

### 2. Write a typed handler

```go
// illustrative
func (h *Notes) Store(c *web.Ctx, in CreateNote) (web.Responder, error) {
	note := h.create(in.Title, in.Body)
	return web.Created(note), nil
}

func (h *Notes) Show(c *web.Ctx, in NoteID) (Note, error) {
	note, ok := h.find(in.ID)
	if !ok {
		return Note{}, web.Error(http.StatusNotFound, "note not found")
	}
	return note, nil
}
```

Register it with `web.H`:

```go
// illustrative
api.Post("", web.H(notes.Store)).Name("store")
```

`web.H` inspects the input type **once, at startup**. It panics then if the
type can't be bound or a `validate` tag is wrong (an unknown rule, a bad
parameter), so a mistake never waits for the first request.

### 3. Choose the response

| Return | Response |
|---|---|
| Any value (struct, slice, map…) | `200` with JSON |
| `web.Created(v)` | `201` with JSON |
| `web.JSON(status, v)` | JSON with any status |
| `web.NoContent()` or a nil `web.Responder` | `204` |
| `web.Redirect(url)` / `web.RedirectRoute(name, args...)` | `303 See Other` |
| `web.Text(status, s)` | Plain text |
| Your own type with `Respond(c *web.Ctx) error` | Whatever it writes |

Use `web.Responder` as the output type when one handler returns different
kinds of responses.

Handlers can also be plain `func(c *web.Ctx) error` and write with
`c.JSON`, `c.Text`, `c.HTML`, `c.Blob`, `c.Redirect`, `c.RedirectRoute` or
`c.NoContent`.

### 4. Return errors

Return an error and let the framework respond:

- `web.Error(status, "message")` gives that status with your message, which
  is shown to the client.
- Your own errors can implement `HTTPStatus() int` to pick a status (for
  example a repository's "not found" error returning 404).
- Anything else is a **500**, and its message is **not** shown to clients
  unless `APP_DEBUG=true`.

<a id="errors"></a>Error responses are chosen per client:

- **API clients** (sending `Accept: application/json`, a JSON body, or
  `X-Requested-With: XMLHttpRequest`) get RFC 9457 problem details:

  ```json
  {
    "type": "about:blank",
    "title": "Unprocessable Entity",
    "status": 422,
    "detail": "The given data was invalid.",
    "errors": {
      "title": "The title field is required."
    },
    "request_id": "dikxmvlw5ml243cr"
  }
  ```

  Binding and validation errors fill the `errors` object, keyed by field
  name. Your own errors can do the same by implementing
  `FieldErrors() map[string]string` (`web.FieldErrorer`).
- **Browsers** get an HTML error page. With `APP_DEBUG=true` it shows the
  error chain, the stack trace for panics, and request details (with
  `Authorization` and `Cookie` headers redacted).

Panics in handlers are recovered and handled like errors. Every 5xx is
logged with the request ID.

Two edge cases:

- **The response already started.** If a handler writes part of a response
  and then fails or panics, the error is logged and the connection is
  aborted, so the client sees a failed response rather than a truncated one
  that looks complete.
- **Cancellation.** If the client went away, nothing is written (logged as
  499). If a handler returns `context.Canceled` from some internal context
  while the client is still waiting, it's a 500.

## How binding works

1. The **body** is decoded: JSON (`Content-Type: application/json` or
   `+json`) into the whole struct, or a form into `form`/`json`-named fields.
   Other content types get **415**, and bodies over `HTTP_MAX_BODY` get **413**.
2. Then **query**, **header** and **path** values are applied. Fields with
   these tags can **never** be set from the body, so a client can't override
   `{id}` by sending `"ID": 999` in JSON.
3. Conversion failures give **400** with each bad field listed.
4. The `validate` tag rules run. Failures give **422** with a message per
   field. See [Validation](validation.md).
5. If they pass and the input has `Validate(ctx) error`, it runs.
   `validate.Fail(field, message)` (or a `*validate.Errors`) gives **422**,
   an `HTTPError` keeps its own status, and any other error is a **500**.

> **Coming from Laravel?** The input struct plays the role of a Form Request:
> it declares the input, its rules (`required|email`) and extra checks, and
> is validated before your handler runs.

## Testing it

Send requests to the app's router with `anetostest`:

```go
// illustrative
app := anetostest.New(t, setup)
app.PostJSON("/notes", map[string]string{"title": "Hi"}).
	AssertCreated().
	AssertJSONPath("title", "Hi")
```

See [Test your app](testing.md#3-test-a-json-api). A router alone works
with `httptest` too: `r.ServeHTTP(rec, req)`.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| Panic at startup: `input type … must be a struct` | `web.H` handler takes a non-struct | Wrap inputs in a struct |
| Panic at startup: `unsupported field type` | e.g. a `map` field with a `query` tag | Use supported types or read `c.Request()` yourself |
| Panic at startup: `validate: … unknown rule` | A typo in a `validate` tag, or a custom rule registered after the route | Check the [rules reference](../reference/validation-rules.md); register custom rules in `init` |
| Field stays empty for JSON | The field has a `path`/`query`/`header` tag, or its JSON name differs | Check tags; body can't fill those fields |
| `415 Unsupported Media Type` | Missing or wrong `Content-Type` | Send `application/json` or a form content type |
| 500 with no details | Production mode hides internal errors | Check the log line with the same `request_id`, or set `APP_DEBUG=true` locally |

## Next steps

- [Validation](validation.md)
- [Binding reference](../reference/binding.md)
- [HTTP request lifecycle](../concepts/http-request-lifecycle.md)
