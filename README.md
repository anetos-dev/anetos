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

**Status:** pre-alpha. v0.2.0 is tagged (privately). v0.1 was the
foundation: the kernel, configuration, runtime supervisor, HTTP layer,
validation, data layer with relations, migrations, model code
generation, views, sessions, forms, the CLI and testing helpers. v0.2
added the batteries: the cache, server-side sessions and rate limiting,
authentication, social login, queues, events, pub/sub listeners, the
scheduler, mail, file storage, plugins (`anetos add`), test fakes, N+1
detection and `anetos make:auth`. [`examples/saas`](examples/saas)
shows them in one app, run as one binary or split by role. v0.3, the
first public release, is in progress: full-text search, AI (typed
LLM calls, tools that run as the user, streaming, a test fake;
Anthropic, OpenAI and compatible servers, and Gemini) and roles and
permissions (global and per team, [`examples/teams`](examples/teams))
and AI in the app (stored conversations, answers streamed to the page,
replies from queue jobs, usage budgets; [`examples/assistant`](examples/assistant))
and vector and hybrid search (embeddings kept next to the records, on
PostgreSQL with pgvector, MariaDB 11.7+ or SQLite, and a search tool for
agents) are done. So is internationalization: times stored in UTC with
a database check, calendar dates and the app's time zone, translations
(YAML catalogs, the visitor's language from the URL, the user, a cookie
or the browser, translated framework messages), numbers, prices, dates
and relative times in the user's language and zone, right-to-left
pages, and Bangla, French and Spanish translations of the framework
(`anetos lang:add`; [`examples/i18n`](examples/i18n)). So is an audit
log: who created, changed (field by field), deleted and restored the rows
of the models an app tracks, written in the change's transaction, with
bulk writes as one entry ([`examples/audit`](examples/audit)). An admin
interface (users, roles, activity) is next, then the release work
(deploy, docs). The docs are at [docs.anetos.dev](https://docs.anetos.dev).
APIs will change.

## Documents

- [Planning & roadmap](docs/planning/roadmap.md): vision, milestones v0.1–v0.4, work packages, risks
- [Design document](docs/design/design.md): architecture, principles and decisions
- [Documentation guide](docs/contributing/documentation-guide.md): how docs are written alongside code
- [Benchmarks](docs/benchmarks/README.md): overhead compared with plain `net/http`

## Developer experience

```bash
anetos new blog && cd blog # templ views, sessions, CSRF, SQLite by default
go tool anetos make:auth   # accounts: password, Google, GitHub, API tokens
go run . migrate
go tool anetos dev         # rebuild and reload on every change

go build -o blog .
./blog                     # everything: web, queue workers and the scheduler
./blog run --only=http     # or split by role when you scale
./blog run --only=workers
./blog help                # migrate, routes:list, your own commands, …
```

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
