---
title: Validation rules reference
since: v0.1.0
---

# Validation rules reference

Rules for the `validate` struct tag, used by `validate.Struct` and `web.H`.
See the [validation guide](../guides/validation.md) for how to use them.

## Syntax

```go
// illustrative
Name  string `json:"name" validate:"required|max:100"`
Role  string `json:"role" validate:"in:reader,editor"`
Skip  Inner  `json:"skip" validate:"-"`
```

- Rules are separated by `|` and run left to right; the first failure is
  the field's message.
- Parameters follow `:` and are separated by commas. Spaces around rules and
  parameters are ignored; parameters can't contain `|` or `,`.
- A rule that names another field (`required_if:plan,team`, `same:email`)
  accepts its Go name or its request key. It must be a field of the same
  struct (including fields promoted from embedded structs).
- Rule names are letters, digits and underscores. A few Laravel rules that
  aren't needed here (`nullable`, `sometimes`, `bail`, `string`, …) give a
  startup error explaining what to do instead.
- `validate:"-"` skips the field and anything nested in it.
- Other tags: `label:"web site"` sets the name used in messages. The
  request key comes from `json`, then `form`, `query`, `path`, `header`, then
  the Go field name.
- Fields are resolved like `encoding/json`: embedded structs (or pointers to
  them) without a json name are flattened, a field shadowed by a shallower
  one with the same key is ignored, and a field inside a nil embedded
  pointer counts as empty. Startup errors: rules on an embedded struct
  itself, rules on a field `encoding/json` ignores as ambiguous (two
  embedded structs at the same depth providing the same name), and two
  fields with rules reporting under the same key (`path:"id"` and
  `json:"id"`).

**Empty fields.** Only the rules marked *implicit* run on empty fields; the
others pass. Empty means: nil pointer or interface, a string of only
whitespace, an empty slice or map, or a zero struct or array value (a zero
`time.Time`, `netip.Addr`, UUID or nested struct). Non-pointer numbers and
booleans are never empty. Struct-based numbers, like decimal types, count
as empty at zero; use a pointer if zero is a valid answer.

**Kinds.** Rules apply to kinds of field, following pointers:

| Kind | Go types |
|---|---|
| string | `string` and named string types |
| numeric | `int…`, `uint…`, `float…` |
| array | slices, arrays and maps, except types with an `UnmarshalText` method (UUIDs, `net.IP`), which are values |
| bool | `bool` |
| time | `time.Time` |
| file | `multipart.FileHeader` (usually `*multipart.FileHeader`) |

A rule on the wrong kind is a startup error. Rules marked *each* also accept
a slice of their kind and check every element; empty elements (nil, blank
strings) are skipped.

## Presence

| Rule | Applies to | Passes when | Default message |
|---|---|---|---|
| `required` | any, implicit | Not empty; for non-pointer numbers and booleans, not zero/`false` | The {label} field is required. |
| `required_if:field,v1,v2…` | any, implicit | `required`, but only when *field* equals one of the values | The {label} field is required when {0} is {1}. |
| `required_unless:field,v1,v2…` | any, implicit | `required`, unless *field* equals one of the values | The {label} field is required unless {0} is {1}. |
| `required_with:f1,f2…` | any, implicit | `required`, when any of the fields is not empty | The {label} field is required when {list} is present. |
| `required_without:f1,f2…` | any, implicit | `required`, when any of the fields is empty | The {label} field is required when {list} is not present. |
| `accepted` | string, numeric, bool; implicit | `true`, `1`, or a string `yes`, `on`, `1`, `true` (any case) | The {label} field must be accepted. |

`required_if` and `required_unless` compare against a string, number or
bool field: numbers compare by value, exactly (`3` matches `3.0`; large
integers are never rounded) and booleans accept
`true`/`false`/`1`/`0`. In their messages, `{0}` is the other field's label
and `{1}` the listed values; in `required_with`/`required_without`, `{list}`
is the other fields' labels.

## Size

| Rule | Applies to | Passes when |
|---|---|---|
| `min:n` | string, numeric, array | length / value / item count ≥ n |
| `max:n` | string, numeric, array | ≤ n |
| `size:n` | string, numeric, array | = n |
| `between:a,b` | string, numeric, array | a ≤ … ≤ b |

Strings are measured in characters (runes), not bytes. For strings and
arrays the parameters must be whole numbers; for numbers they can be
decimals, and integer fields are compared exactly. Messages depend on the kind, e.g. "must be at least 3
characters", "must be at least 3", "must have at least 3 items". For
uploads, `min`/`max` count files; use `max_size` for bytes.

## String formats

All of these apply to strings and are *each*.

| Rule | Passes when |
|---|---|
| `email` | A plain address like `name@example.com`: no display name, dotted domain. Non-ASCII letters allowed; quoted local parts and IP domains are not |
| `url` | An absolute `http` or `https` URL with a host |
| `url:https,ftp,…` | An absolute URL with one of these schemes. Other schemes (`javascript:`, `data:`) are never accepted by default, since URLs often end up in `href` attributes |
| `uuid` | `xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx` in hex, any case |
| `alpha` | Letters only (any script) |
| `alpha_num` | Letters and digits |
| `alpha_dash` | Letters, digits, `-` and `_` |
| `ascii` | ASCII characters only |
| `numeric` | A decimal number, e.g. `-1.5`, `1e3` |
| `integer` | A base-10 integer |
| `lowercase` / `uppercase` | Has no upper-case / lower-case letters |
| `starts_with:a,b…` / `ends_with:a,b…` | Starts / ends with one of the values |
| `ip` / `ipv4` / `ipv6` | An IP address (of that version), without a zone like `%eth0` |
| `json` | Valid JSON |
| `date` | `YYYY-MM-DD` and a real date |
| `datetime` | RFC 3339, e.g. `2026-09-30T10:00:00Z` |

For typed values, prefer typed fields: an `int`, a `time.Time`, a
`netip.Addr` or any type with `UnmarshalText` is converted during binding,
and bad input is rejected there with a 400.

## Choice

| Rule | Applies to | Passes when | Default message |
|---|---|---|---|
| `in:a,b…` | string, numeric, bool; each | The value is one of the parameters (numbers by value; a fractional parameter on an integer field is a startup error) | The selected {label} is invalid. |
| `not_in:a,b…` | string, numeric, bool; each | The value is none of them | The selected {label} is invalid. |
| `distinct` | slices and arrays whose elements contain no interfaces, slices, maps or funcs | No two elements are equal | The {label} field has a duplicate value. |

## Comparison

| Rule | Applies to | Passes when |
|---|---|---|
| `same:field` | any | Equal to *field* (same type required) |
| `different:field` | any | Not equal to *field* |
| `confirmed` | any | Equal to the field named `<Name>Confirmation` or keyed `<key>_confirmation` |
| `after:now` / `after:field` | `time.Time`, `anetos.Date` | Later than now / than *field* |
| `after_or_equal:…`, `before:…`, `before_or_equal:…` | `time.Time`, `anetos.Date` | As named |

`now` is the app's clock (`anetos.Now`); for an `anetos.Date` it is today
in the app's zone (`APP_TIMEZONE`). *field* must have the same type: a
date compares with a date, a time with a time. If the other field of a
date comparison is empty, the rule passes; give that field its own
`required` rule.

## Files

For `*multipart.FileHeader` fields, and *each* for
`[]*multipart.FileHeader`.

| Rule | Passes when |
|---|---|
| `max_size:2MB` / `min_size:1KB` | Size within the limit. Units as in config: `B`, `KB`, `MB`, `GB` (powers of 1024) |
| `mimetypes:image/png,application/pdf` | The type **detected from the content** matches; `image/*` matches any detectable image type |
| `extensions:jpg,png,tar.gz` | The file name ends with one of the extensions (any case). The name comes from the client, so pair it with `mimetypes` or `image` |
| `image` | Detected as PNG, JPEG, GIF or WebP. SVG is not accepted, since it can contain scripts |

Content is detected from the first 512 bytes with
`net/http.DetectContentType`; the client's `Content-Type` is ignored. Only
types it can detect are accepted in `mimetypes` (others are a startup
error): `application/pdf`, `application/zip` (also Office files),
`application/x-gzip`, `application/x-rar-compressed`, `application/wasm`,
`application/ogg`, `application/postscript`,
`application/vnd.ms-fontobject`, `application/octet-stream` (unknown),
`audio/aiff`, `audio/midi`, `audio/mpeg`, `audio/wave`, `font/collection`,
`font/otf`, `font/ttf`, `font/woff`, `font/woff2`, `image/bmp`,
`image/gif`, `image/jpeg`, `image/png`, `image/webp`, `image/x-icon`,
`image/vnd.microsoft.icon`, `text/html`, `text/plain`, `text/xml`,
`video/avi`, `video/mp4`, `video/webm`. CSV and JSON files are
`text/plain`; SVG is `text/xml` or `text/plain`.

## Database rules

Registered by the `db` package (importing it is enough); they query the
database in the validation context, which in `web.H` is the request's.

| Rule | Passes when | Default message |
|---|---|---|
| `unique:table[,column[,exceptField[,idColumn]]]` | No row in *table* has this value in *column* (default: the field's key). With *exceptField*, the row whose *idColumn* (default `id`) equals that field's value is ignored, for edit forms | The {label} has already been taken. |
| `exists:table[,column]` | A row in *table* has this value in *column* | The selected {label} is invalid. |

```go
// illustrative
type UpdateUser struct {
	ID    int64  `path:"id"` // from the URL: the body can't change it
	Email string `json:"email" validate:"required|email|unique:users,email,ID"`
	Owner int64  `json:"owner_id" validate:"required|exists:users,id"`
}
```

Take the *exceptField* from the path (or set it in code), never from a
body field: a client could otherwise name another user's ID and skip the
check.

Both count soft-deleted rows, as a unique index would. Without a database
in the context they fail with `db.ErrNoDB` (a 500). Like every custom rule,
they skip empty fields.

## Custom rules

```go
// illustrative
validate.Register("slug", "The {label} field must be a slug.", func(ctx context.Context, f validate.Field) (bool, error) {
	s, _ := f.Value.(string)
	return slugPattern.MatchString(s), nil
})
```

- Register from `init`, before routes are added. Names are ASCII letters,
  digits and underscores, and can't reuse a built-in or registered name
  (`Register` panics).
- Custom rules apply to any kind and skip empty fields.
- `f.Value` has pointers followed (nil for a nil pointer); `f.Params` holds
  the tag parameters; `f.Parent` is the struct holding the field.
- Returning an error aborts validation with that error (a 500 in `web.H`).

## Messages

Messages come from the translation catalogs (package `i18n`), in the
request's language: the framework's English ones unless a catalog defines
the key. See [Translations](../guides/translations.md#6-translate-the-frameworks-messages).

| Key | Is |
|---|---|
| `validation.<rule>` | A rule's message: `validation.required`, `validation.email` |
| `validation.<rule>.string`, `.numeric`, `.array` | A size rule's message (`min`, `max`, `size`, `between`) for the field's kind |
| `validation.<name>` | A custom rule's message, instead of the one given to `Register` |
| `validation.custom` | A custom rule registered without a message |
| `validation.attributes.<key>` | A field's label in messages (`email: "email address"`); else the `label` tag (translated if a catalog has it as a key), else the key humanized |
| `validation.values.<parameter>` | A rule parameter shown in messages (`now`) |

Templates can use `{label}`, `{0}`, `{1}`, … for parameters and `{list}` for
all parameters joined with ", ". For rules that name other fields, the
arguments are those fields' labels. Override per struct with:

```go
// illustrative
func (SignUp) ValidationMessages() map[string]string {
	return map[string]string{"email.required": "…", "min": "…"}
}
```

Keys are `key.rule` (one field) or `rule` (every field of this struct, not
nested ones). A value that is a catalog key is translated. A size rule's override key is the rule name (`min`), whatever
the field's kind. A key naming an unknown rule, or a field and rule that
don't exist on this struct, is a startup error.

## Result

`validate.Struct` returns `nil`, a `*validate.Errors`, or an error from a
custom rule (or for structs nested more than 10,000 levels deep, the same
limit `encoding/json` has, which in practice means cyclic data). `*validate.Errors` offers `Has`, `Get`, `Keys` (in field order),
`Len`, `FieldErrors()` (a map copy), `Add` (keeps the first message per
field), `Err()` (a copy, or nil when empty) and JSON encoding as an object
in field order. `validate.Fail(field, message)` builds one with a single
message. It reports status 422 to `web`, which renders it as problem JSON
with an `errors` object or as the HTML error page.
