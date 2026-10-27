---
title: TypeScript reference
since: v0.6.0
group: "Web"
weight: 206
---

# TypeScript reference

Package `web/typescript` and the files it writes. How to use it:
[Generate TypeScript types for the frontend](../guides/typescript.md).

## API

| Name | Does |
|---|---|
| `typescript.Register(app, srv, cfg)` | Adds the `types` command: writes the files that changed, or with `--check` fails if one is out of date. Call it after adding the routes. An error the second time |
| `typescript.Generate(r, cfg)` | The files of `r`'s typed routes and `cfg.Inertia`'s pages, by path, and warnings about what it couldn't describe (a result with its own `MarshalJSON`). The same routes, pages and `Config` give the same bytes |
| `typescript.Check(r, cfg)` | An error unless the files hold what `Generate` writes (Windows line ends allowed), saying to run `go run . types` |
| `inertia.WithPages(pages...)` | The pages `pages.ts` describes; in development and tests, rendering a page the list doesn't have is an error (since the app lists pages). Two pages of one component are an error |
| `inertia.AnyPage` | What `*inertia.Page[P]` is: `Component()`, `PropsType()` |
| `in.Pages()`, `in.SharedTypes()`, `inertia.PropType(t)` | For tools: the listed pages, the shared props' types, and `T` for an `inertia.Prop[T]` |

## Config

| Field | Default | Meaning |
|---|---|---|
| `Dir` | `frontend/src/types` | Where the files go, relative to the working directory |
| `Prefix` | no client | The API's routes (`/api/v1`): `api.ts`'s client has a function for each typed handler at or below it |
| `Inertia` | no `pages.ts` | The app's Inertia: its listed pages and shared props |
| `Routes` | `false` | Also write `routes.ts`, the named routes for the frontend's links (since v0.6) |
| `ExcludeRoutes` | none | Prefixes of route names `routes.ts` leaves out (`"admin."`): routes the frontend doesn't link to, whose paths would otherwise be in its bundle (since v0.6) |

## api.ts

- An interface for each named struct of the typed handlers' inputs and
  results, by the names the OpenAPI document gives them (`Post`,
  `PostInput` when a type is both, `PagePost` for `db.Page[Post]`), made
  identifiers (`HandlersOrder` for `handlers.Order`, when two packages
  have an `Order`); a name TypeScript or the files have (`Record`,
  `Promise`, `Pages`, `ApiError`) gets a number (`Record2`), as does a
  second input of one name.
- `Problem`: the problem details of an error response.
- With `Prefix`: `<Operation>Input` for each route (the path's
  wildcards, a `string` when the input has no field for one; its query
  and header parameters; its body's fields), `ApiOptions`, `ApiError`
  (`status`, `problem`), and `createApi(options)`, whose functions are
  grouped by the dots of the operation's name, as the OpenAPI document
  names it: the route's name (`posts.show`), else the handler's method
  (`Posts.Index`) or function, else the method and path
  (`getApiV1Posts`). The functions under a name that is also a
  function's are joined with it (`edge`, `edgeSub`), whatever the
  routes' order. A function's input may be left out when nothing in it
  is required.

| Go | TypeScript |
|---|---|
| `string`, `time.Time`, `anetos.Date`, `[]byte`, a `TextMarshaler` | `string` |
| numbers | `number` (`string` with `json:",string"`) |
| `bool` | `boolean` |
| `[]T` | `T[]` |
| `map[string]T` | `Record<string, T>` |
| `*T` | `T \| null` |
| `any`, `json.RawMessage`, a type with its own `MarshalJSON` | `unknown` |
| a field with `omitempty` (or through an embedded pointer) | optional (`?`) |
| an input field without `required` | optional; `in:a,b` gives `"a" \| "b" \| ""` |
| an uploaded file | `Blob` |

## pages.ts

- A type for each listed page's props (named after its Go type) and
  each shared props type; an `inertia.Prop[T]` field is `T`, optional;
  `inertia.Props` is `Record<string, unknown>`.
- `ErrorProps`, the error pages' props.
- `SharedProps`, `Errors`, `Pages` (component to props),
  `PageProps<C>` (a page's props, the shared ones and `errors`).
- The augmentation of `@inertiajs/core`'s `InertiaConfig`
  (`sharedPageProps`), which types `usePage()`.

## routes.ts

With `Routes`, the router's named routes (not those of a host, a
subdomain's, nor those `ExcludeRoutes` leaves out); a pattern's `{$}`
is no parameter, and no part of the path:

- `RouteParams`: each route's name and its path's parameters, each a
  `string | number` (`'posts.show': { id: string | number }`);
  `RouteName`, the names, and `PlainRouteName`, those without
  parameters.
- `route(name, params, query)`: the route's path with its parameters
  encoded (a rest parameter, `{path...}`, keeps its slashes), and the
  query (`Query`: strings, numbers or booleans, a list of them repeating
  the name; `null` and `undefined` leave the name out). `params` is required when the route
  has parameters, so a missing one, or a renamed route, fails the type
  check.
- `setRoutePrefix(prefix)`: a path put before every path `route` makes,
  such as the locale's (`/bn`) with `APP_LOCALE_STRATEGY=prefix`.
