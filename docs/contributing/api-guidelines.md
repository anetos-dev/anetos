# Anetos — API Guidelines

| | |
|---|---|
| **Status** | Active from v0.5 (M8a) |
| **Owner** | Samiul Hoque |
| **Last updated** | 2026-10-10 |
| **Related** | [Design document](../design/design.md) §2, §23 · [Documentation guide](documentation-guide.md) |

The **API** is every exported identifier of the library modules: the
core (`anetos.dev/anetos`), `admin`, the drivers and the first-party
plugins. `api/<module>.txt` lists it, one line per identifier (design
D308). The `cli` module, `internal/` packages, the examples and the
code `anetos new` and the generators write are not API: the generated
code belongs to the app.

These rules apply to new API and to changes. The design principles
(design §2) come first: stdlib-compatible, explicit, typed, no
per-request reflection, a small core. Where these rules don't say,
follow [Effective Go](https://go.dev/doc/effective_go) and
[Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).

---

## 1. Names

- A name reads well with its package: `web.Router`, `queue.Dispatch`,
  not `web.WebRouter` or `queue.QueueDispatch`. A package's main type
  may share its name when nothing better exists (`cache.Cache`).
- Initialisms keep one case: `URL`, `ID`, `HTTP`, `JSON`, `SQL`, `API`,
  `CSRF` (`RequestID`, `apiKey`).
- No `Get` on getters: `r.Name()`, not `r.GetName()`.
- The same idea has the same word everywhere: if one package says
  `Close`, another doesn't say `Shutdown` for the same thing; `New` makes
  a value, `Open` connects to something, `Load` reads configuration,
  `Register` adds to a registry at startup.
- No package name shadows a standard library package (design §5).

## 2. Constructors and options

- `New(…)` (or `NewThing` when a package makes several things) takes the
  required values as parameters and the rest as options.
- **A service the app shares** (the cache, the queue, the mailer…) has
  `ForApp(app, …) (*T, error)`, which builds it from the app's
  configuration once and returns the same one after; `WithT(ctx, *T)
  context.Context` carries it in a context and `From(ctx) (*T, error)`
  gets it back, with the package's `ErrNoT` when the context has none.
- **Options** are functional: `type Option func(*options)`, the function
  taking an unexported struct or the type it builds, so that adding an
  option never breaks a caller. A package with options for several
  things names them after the thing or the verb: `DispatchOption`,
  `ListenOption`. An option that can be invalid returns an `error`
  (`func(*options) error`) or records it for the constructor to report,
  never panics.
- An **options struct** (`Options`, `PutOptions`) is fine instead when
  every field is optional and its zero value is the default, as
  `slog.HandlerOptions`.
- **Config structs** are for values read from the environment:
  `type Config struct` and `LoadConfig(config.Source) (Config, error)`,
  with `DefaultConfig() Config` where apps often build one in code;
  their zero value is never silently used for a required setting.
- `MustX` panics instead of returning an error. It exists only beside an
  `X` that returns the error, for a mistake in the program rather than
  in its input (`web.MustURL` with a route name that doesn't exist,
  `anetos.MustResolve` with a service never provided).

## 3. Context, errors and panics

- A function that does I/O, waits or may be canceled takes
  `ctx context.Context` first. A type keeps a context only for its own
  lifetime (canceled when it stops) or to adapt to an interface without
  one (an `io.Reader`), never a request's beyond the request (`web.Ctx`
  is the request).
- `error` is the last result. Error strings are lower case, without a
  final period, prefixed with the package (`"queue: the worker is
  stopped"`), and wrap their cause with `%w`.
- A condition callers test is an exported sentinel (`var ErrNotFound`)
  or an error type (`*HTTPError`, `*ai.BudgetError`), documented on the
  function that returns it; callers use `errors.Is` and `errors.As`.
- Panics are for mistakes in the program (a duplicate or unknown route
  name, `Must…`), never for user input or I/O.

## 4. Types and interfaces

- Constructors return concrete types (`*Router`), so methods can be
  added; functions accept interfaces where callers bring their own.
- An interface is small and defined by the package that needs it. A
  contract that drivers implement (a cache store, a queue store, a
  broker, a storage backend) lives in the package that owns it, with a
  conformance suite in its `…test` package.
- An interface a user must not implement has an unexported method, so
  methods can be added to it.
- The zero value is useful or the type says it isn't (doc comment: "use
  New").
- Exported struct fields are for values a user sets or reads; anything
  else is unexported, with a method if it must be read.

## 5. What to export

- Export what an app or a driver needs, nothing more. A helper shared
  by two packages of the module, and not by apps, goes under
  `internal/`.
- No exported package variable that changes behaviour (`var
  DefaultTimeout = …`): use an option or a config value. Sentinel errors
  and embedded files (`htmx.FS`) are the exceptions.
- No exported identifier whose only purpose is a test: test helpers go
  in a `…test` package (`cachetest`, `queuetest`) or `anetostest`.
- Generics where they give type safety the caller sees
  (`anetos.Resolve[T]`, `web.H[In, Out]`, models), not to shorten the
  implementation.

## 6. Doc comments

Every exported identifier, struct field and interface method has a doc
comment that starts with its name (documentation guide §6; `make
api-docs` checks). Say what a function returns on failure, what it
does when called twice, and whether it's safe for concurrent use when
that isn't obvious.

## 7. Changing the API

- An API change changes `api/<module>.txt`: run `make api-update` and
  commit the file with the change. `make api-check` (CI) fails when it
  is out of date, so every API change shows in review.
- **Adding** to the API needs no more than that (and the docs).
- **Renaming or removing** (from v0.5): keep the old name for one minor
  release, marked deprecated, and remove it in the next (design §23,
  D309). When the old name can be written in terms of the new one, add
  `//go:fix inline`, so that `go fix ./...` (Go 1.26 and later) rewrites
  callers:

  ```go
  // illustrative
  // NewMux returns a router.
  //
  // Deprecated: Use NewRouter; NewMux is removed in v0.6.
  //
  //go:fix inline
  func NewMux(opts ...RouterOption) *Router { return NewRouter(opts...) }

  // Endpoint is a route.
  //
  // Deprecated: Use Route; Endpoint is removed in v0.6.
  //
  //go:fix inline
  type Endpoint = Route

  // ErrNoRoute is ErrUnknownRoute.
  //
  // Deprecated: Use ErrUnknownRoute; ErrNoRoute is removed in v0.6.
  var ErrNoRoute = ErrUnknownRoute // errors.Is matches either
  ```

  The `Deprecated:` paragraph names the replacement and the version that
  removes the old name. A variable can't be inlined: point the old one
  at the new value. A change `go fix` can't make (a method's signature,
  a removed field, a changed behaviour) is described in the upgrade
  guide with the code to write instead.
- Move the repository's own callers (examples, templates, docs) to the
  new name in the same change: the linters flag uses of deprecated
  names.
- Every rename, removal or behaviour change is listed in the CHANGELOG
  (`Deprecated`, `Removed` or `Changed`) and in the upgrade guide of the
  version that ships it (documentation guide §9).

## 8. Review checklist

- [ ] The name reads well with the package and matches the words used
      elsewhere (§1).
- [ ] Required values are parameters, the rest options; shared services
      have `ForApp`, `WithT` and `From` (§2).
- [ ] `ctx` first where it may block; errors testable with `errors.Is`
      or `errors.As` (§3).
- [ ] Concrete types returned; interfaces small, owned by the right
      package (§4).
- [ ] Nothing exported that apps don't need (§5).
- [ ] Doc comments (§6); `api/*.txt` updated; renames deprecated, not
      removed (§7).

---

## Document history

| Date | Change |
|---|---|
| 2026-10-10 | Initial guidelines (M8a, D308, D309) |
