---
title: OpenAPI reference
since: v0.4.0
group: "Web"
weight: 204
---

# OpenAPI reference

Package `web/openapi` (v0.4) and what it writes. How to use it:
[Describe an API with OpenAPI](../guides/openapi.md).

## API

| Name | Does |
|---|---|
| `openapi.Spec(r, cfg)` | The OpenAPI 3.1.0 document of `r`'s typed routes, as indented JSON (the same routes and `Config` give the same bytes), and warnings about what it left out or couldn't describe |
| `openapi.Check(r, cfg)` | An error unless `cfg.File` holds `Spec`'s document (Windows line ends allowed); the error names the first line that differs and says to run `go run . openapi` |
| `openapi.ForApp(app, srv, cfg)` | Adds the `openapi` command, and serves the document at `cfg.Path` if set (built on the first request). Call it after adding the routes. An error without `cfg.Title` |
| `web.Documented(h, web.MiddlewareDoc{…})` | The handler a middleware returns, with what the middleware asks for and may answer, which the routes it wraps report |
| `web.RouteInfo.Input`, `.Output`, `.Status` | A typed route's input and result types and its `Status` (0 unless set) |
| `web.RouteInfo.Handler` | A typed route's function, without its package path: `handlers.Bookmarks.Index` |
| `web.RouteInfo.Middleware` | The `MiddlewareDoc`s of the route's middleware, outermost first (read-only) |
| `auth.RequireAbilities(abilities…)` | Middleware: a token needs the abilities (403), a guest is refused (401, `WWW-Authenticate: Bearer`); documents them as the bearer token's scopes. Put it after `Require` |

## Config

| Field | Default | Meaning |
|---|---|---|
| `Title` | (required) | `info.title` |
| `Version` | `1.0.0` | `info.version`: the API's version, not Anetos's |
| `Description` | | `info.description`, Markdown |
| `Prefix` | every typed route | Only the routes at or below this path (a trailing `/` is ignored) |
| `File` | `openapi.json` | Where the command writes and `Check` reads, relative to the working directory |
| `Path` | not served | The route serving the document; it isn't described in it |
| `Servers` | `/` | `servers`' URLs |
| `SecuritySchemes` | `bearer` only | Schemes middleware may name: `openapi.SecurityScheme{Type, Scheme, BearerFormat, In, Name, Description}`, `Type` `http` or `apiKey` |

## The command

`openapi [--check] [--out=FILE]`, added by `openapi.ForApp`.

| Flag | Default | Meaning |
|---|---|---|
| `--out` | `Config.File` | The file to write or check; `-` writes to the standard output |
| `--check` | `false` | Exit 1 if the file isn't the document of the routes, instead of writing it |

It prints a warning per route it leaves out or can't fully describe.
It doesn't boot the app, so it needs no database.

## Operations

| What | From |
|---|---|
| Which routes | Typed routes (`web.H`, registered as it returns them) at or below `Prefix`; other routes and any-method routes are left out with a warning |
| `operationId` | The route's name; else the handler's (`Bookmarks.Index`, a function's `welcome`); else the method and path. Unique (a number is added) |
| `tags` | The handler's receiver type (`Bookmarks`); none for functions |
| `parameters` | The path's wildcards (`{path...}` is `{path}`), then the input's `query` and `header` fields |
| `requestBody` | The input's other fields as `application/json`, or `multipart/form-data` (the fields a form binds, by their form names, and the files) when it has files; none for GET and HEAD. Required when a field is |
| Success | The route's `Status` (200 by default) with the result's schema; `web.Empty`, 204 and 205 without content; a `web.Responder` result: `2XX` without content, with a warning |
| Errors | 400 (`InvalidValues`) when there are values to bind; 422 (`InvalidInput`) with `validate` rules or a `Validate` method; each documented middleware's statuses; `default` (`Error`). All `application/problem+json`, the `Problem` schema |
| `security` | The documented middleware's schemes, with their scopes merged; `[]` on the others when some operations have any |

Two routes OpenAPI can't tell apart (`GET /a/{id}` and `DELETE
/a/{key}`; one method and path on two hosts) are an error.

## Schemas

Types are described as `encoding/json` writes (results) and reads
(bodies) them: names from `json` tags, embedded structs' fields
promoted, `json:",string"` numbers as strings.

| Go | Schema |
|---|---|
| `string`, `bool` | `string`, `boolean` |
| `int`, `int8`, `int16` / `int32`, `int64` | `integer` / with `format` `int32`, `int64` |
| unsigned integers | `integer`, `minimum` 0 |
| `float32`, `float64` | `number`, `format` `float`, `double` |
| `time.Time`, `anetos.Date` | `string`, `format` `date-time`, `date` |
| `[]byte` | `string`, `contentEncoding` `base64` |
| `*multipart.FileHeader` | `string`, `format` `binary` |
| `time.Duration` parameter | `string` (`30s`, as the binding reads it); in a body, an `integer` (nanoseconds) |
| slices, arrays | `array` with `items` |
| maps | `object` with `additionalProperties` |
| a pointer | its type, nullable (`["string", "null"]`; `anyOf` a `$ref` and `null`) |
| a named struct | a component, `$ref: "#/components/schemas/Name"` |
| an anonymous struct | an inline `object` |
| an `encoding.TextMarshaler` | `string` |
| an interface, `json.RawMessage`, a `json.Marshaler` | any value (`{}`); a `json.Marshaler` with a warning |

| In a result | In a body |
|---|---|
| Every field without `omitempty` or `omitzero` is `required`, except those promoted through an embedded pointer | `required` comes from the `validate` rule |
| Slices and maps aren't nullable: answer empty ones, not nil | `path`, `query` and `header` fields aren't in the body |

**Components' names:** the Go type's (`BookmarkResponse`); a generic
type's with its type arguments' names (`db.Page[BookmarkResponse]` is
`PageBookmarkResponse`); the package's name first when two types share
a name (`handlers.Order`); `…Input` for a type's body schema when it's
a result too; a number after a name still taken. `Problem` is the
problem details'.

## Validation rules

What a `validate` tag says that JSON Schema can, in bodies and
parameters:

| Rule | Schema |
|---|---|
| `required` | `required`; a string `minLength` 1, an array `minItems` 1, a map `minProperties` 1; in a non-pointer field, a number `not: {const: 0}`, a bool `const: true` |
| `min`, `max`, `size`, `between` | `minLength`/`maxLength` (strings), `minimum`/`maximum` (numbers), `minItems`/`maxItems` (slices), `minProperties`/`maxProperties` (maps) |
| `in` | `enum` (with `null` for a pointer) |
| `email`, `url`, `uuid`, `date`, `datetime`, `ipv4`, `ipv6` | `format` `email`, `uri`, `uuid`, `date`, `date-time`, `ipv4`, `ipv6`; `url` also a `pattern` of its schemes (`http` and `https` unless it names others), as `uri` allows any |
| `distinct` | `uniqueItems` |
| others (`unique`, `confirmed`, `after`, custom rules…) | not shown; still checked |

A field that isn't `required` may be blank whatever its other rules (as
`validate` skips them): its schema is `anyOf` the constrained schema and
a blank one (`{"type": "string", "pattern": "^\\s*$"}`, an empty array
or map).

## Middleware

| Middleware | Documents |
|---|---|
| `auth.Auth.Require` | `bearer`; 401 |
| `auth.Auth.TokenMiddleware` | 401 (a bad token) |
| `auth.RequireAbilities(abilities…)` | `bearer` with the abilities as scopes; 401, 403 |
| `rbac.Require`, `rbac.RequireIn` | 401, 403 |
| `ratelimit.Middleware` | 429 |
| your own | `web.Documented(h, web.MiddlewareDoc{Security, Scopes, Responses})` |

Global middleware (`UseGlobal`) isn't seen.

## Warnings

| Warning | Means |
|---|---|
| `not a typed handler (web.H, registered as web.H returns it): left out` | A plain handler, or `web.H`'s handler wrapped in another function |
| `a route for every method isn't described` | `Handle("", …)` |
| `its result is a web.Responder…: described as any 2XX` | The handler chooses its status and body as it runs |
| `its result is an interface…: described as any value` | `any` or another interface type |
| `… has its own MarshalJSON: described as any value` | The type writes its own JSON |
| `… can't be written in JSON` | A channel, a function… |
