---
title: Request binding reference
since: v0.1.0
---

# Request binding reference

Rules used by `web.H` to fill a handler's input struct.

## Tags

| Tag | Source | Notes |
|---|---|---|
| `json:"name"` | JSON body | Standard `encoding/json` rules. Also the form field name when no `form` tag is present, if the type can come from a form value (structs, maps and `json.RawMessage` stay JSON-only) |
| `form:"name"` | Form body (`application/x-www-form-urlencoded`, `multipart/form-data`) | Query-string values are **not** read as form values |
| `query:"name"` | URL query | `[]T` receives every value of a repeated parameter |
| `path:"name"` | Path wildcard `{name}` or `{name...}` | Can't be a slice |
| `header:"Name"` | Request header | `[]T` receives every value |
| `form:"-"`, `query:"-"`, … | | Not bound from that source. `form:"-"` does **not** stop JSON; use `json:"-"` for that |

A field with a `path`, `query` or `header` tag (and no `form` tag) is
**never** set from the body. File fields are never set from JSON.

> **Warning:** Like any JSON binding, every exported field without
> `json:"-"` can be set by the client. Keep input structs to the fields a
> client may send (don't bind straight into database models with an
> `IsAdmin` field).

Embedded structs without tags are flattened, so a shared `Pagination`
struct can be embedded in many inputs. Embed them **by value**: an embedded
*pointer* whose struct has `path`, `query`, `header` or `form` tags is
rejected at startup, because JSON decoding could fill it from the body.

## Order

1. Body (JSON or form). Skipped for `GET` and `HEAD` requests and when the
   struct has no body fields. A JSON body must be a single value; trailing
   data is a 400.
2. Query, header and path values, overwriting the corresponding fields
   (also for a field tagged with both `form` and `path`: the path wins).
3. The `validate` tag rules ([rules reference](validation-rules.md)); failures
   stop here with a 422.
4. `Validate(ctx context.Context) error`, if the input type implements it.

## Field types

| Go type | Accepted text |
|---|---|
| `string` | Anything |
| `bool` | `true`/`false`, `1`/`0`, `t`/`f`, `yes`/`no`, `on`/`off` (checkboxes), any case |
| `int…`, `uint…`, `float…` | Decimal numbers within range |
| `time.Duration` | Go durations: `30s`, `5m` |
| `time.Time`, `netip.Addr`, any `encoding.TextUnmarshaler` | Whatever `UnmarshalText` accepts (RFC 3339 for `time.Time`) |
| `*T` of the above | Set only when a value is present |
| `[]T` of the above | Repeated query/header/form values |
| `*multipart.FileHeader`, `[]*multipart.FileHeader` | Uploaded files (`form` tag only) |

JSON bodies use `encoding/json`'s own conversions for `json` fields.

Empty form and query values (`age=`) count as absent for non-string fields,
so optional inputs stay unset instead of failing to parse.

## Error statuses

| Situation | Status |
|---|---|
| Value can't be converted (`page=abc`), JSON type mismatch | 400, with `errors` per field |
| Malformed JSON or form | 400 |
| Content type other than JSON or form, with a body | 415 |
| Body larger than `HTTP_MAX_BODY` | 413 |
| A `validate` tag rule fails | 422, with `errors` per field |
| `Validate` returns `validate.Fail(…)` or a `*validate.Errors` | 422, with `errors` per field |
| `Validate` returns a `*web.HTTPError` or an error with `HTTPStatus() int` | That status |
| `Validate` returns any other error | Handled like a handler error: 500 without details (503 for deadlines; nothing sent if the client left) |

## Startup checks

`web.H` panics when it is called (at route registration) if the input type
is not a struct, a tag is empty (`query:""`), a field type is unsupported,
a `path` field is a slice, a file field lacks a `form` tag, an embedded
pointer struct has source tags, or a `validate` tag is invalid (see the
[rules reference](validation-rules.md)).
