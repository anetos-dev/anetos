<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset=".github/assets/anetos-logo-dark.svg">
    <img alt="Anetos" src=".github/assets/anetos-logo.svg" width="320">
  </picture>
</p>

> *Anetos* (Greek άνετος, "at ease, comfortable"; say **AH-neh-tos**).
> Module `anetos.dev/anetos`, command `anetos`.

A batteries-included Go web framework with the developer comfort of Laravel,
built the Go way: typed, `net/http`-compatible, code generation instead of
runtime magic, and a supervised runtime where HTTP, queue workers, pub/sub
listeners and the scheduler run together in **one binary**.

**Status:** pre-alpha. v0.4.0 is tagged; the first public release is
v0.5. APIs will change.

- **v0.1, the foundation:** the kernel, configuration, runtime
  supervisor, HTTP layer, validation, data layer with relations,
  migrations, model code generation, views, sessions, forms, the CLI and
  testing helpers.
- **v0.2, the batteries:** the cache, server-side sessions and rate
  limiting, authentication, social login, queues, events, pub/sub
  listeners, the scheduler, mail, file storage, plugins (`anetos add`),
  test fakes, N+1 detection and `anetos make:auth`;
  [`examples/saas`](examples/saas) runs them as one binary or split by
  role.
- **v0.3, search, AI and the starter experience:** full-text search and
  search by meaning with hybrid ranking (PostgreSQL with pgvector,
  MariaDB 11.7+ or SQLite); AI with Anthropic, OpenAI (and compatible
  servers) and Gemini: typed calls, tools that run as the user, agents,
  stored conversations, streaming, budgets and a test fake
  ([`examples/assistant`](examples/assistant)); roles and permissions,
  global and per team ([`examples/teams`](examples/teams));
  internationalization, with Bangla, French and Spanish translations of
  the framework ([`examples/i18n`](examples/i18n)); an audit log and
  soft deletes ([`examples/audit`](examples/audit)); the admin (module
  `anetos.dev/anetos/admin`, [`examples/admin`](examples/admin)) with
  two-factor authentication; account settings; Google Cloud Storage; `anetos
  build`, a Dockerfile and a systemd unit
  ([Deploy](docs/site/guides/deployment.md)); a starter theme,
  `make:crud` and error pages in the app's layout; a security review
  ([checklist](docs/security/checklist.md), [SECURITY.md](SECURITY.md))
  and a `doctor` command in every app; [benchmarks](docs/benchmarks/v0.3.md)
  against plain `net/http`, chi, Gin and Echo, with CI holding every
  change to allocation budgets.
- **v0.4, the API stack:** `anetos new --stack=api`, an app that serves
  JSON only; accounts that log in with API tokens, with two-factor
  codes and abilities; `make:crud` JSON endpoints with pages, sorting
  and filters; typed results with the route's status; an OpenAPI 3.1
  description generated from the handlers, served and checked in a
  test ([`examples/bookmarks`](examples/bookmarks)).

The [tutorial](docs/site/getting-started/tutorial/README.md) builds an
issue tracker step by step, and [`examples/tracker`](examples/tracker),
the reference app, is a bigger one; the
[API tutorial](docs/site/getting-started/build-an-api.md) builds
[`examples/bookmarks`](examples/bookmarks), a JSON API. The docs are at
[docs.anetos.dev](https://docs.anetos.dev).

## Documents

- [Planning & roadmap](docs/planning/roadmap.md): vision, milestones v0.1–v0.6, work packages, risks
- [Design document](docs/design/design.md): architecture, principles and decisions
- [Documentation guide](docs/contributing/documentation-guide.md): how docs are written alongside code
- [API guidelines](docs/contributing/api-guidelines.md): how the public API is named, shaped and changed; `api/` lists it
- [Benchmarks](docs/benchmarks/README.md): overhead compared with plain `net/http`

## Developer experience

```bash
anetos new blog && cd blog # templ views in a starter theme (light/dark), sessions, CSRF, SQLite
go tool anetos make:crud Post title:string body:text published:bool  # pages to list, show, create, edit, delete
go tool anetos make:auth   # accounts: password, Google, GitHub, API tokens
go tool anetos make:admin  # an admin at /admin; make:admin-resource Post adds posts
go run . migrate
go tool anetos css:use pico # restyle the app's pages: Pico, Bootstrap, Bulma, Tailwind CSS or none
go tool anetos dev         # rebuild and reload on every change

go tool anetos build       # bin/blog: one static binary, everything in it
./bin/blog                 # everything: web, queue workers and the scheduler
./bin/blog run --only=http # or split by role when you scale
./bin/blog run --only=workers
./bin/blog doctor          # unsafe settings, pending migrations
./bin/blog help            # migrate, route:list, your own commands, …
docker build -t blog .     # or the image, from the generated Dockerfile

anetos new shop --stack=api # or JSON only: routes under /api/v1, errors as problem details,
                            # an OpenAPI 3.1 description (openapi.json) a test keeps current;
                            # there, make:auth writes accounts that log in with API tokens,
                            # and make:crud JSON endpoints (pages, sorting, filters)
```

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
