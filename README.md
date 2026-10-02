# Anetos

> **Working codename.** The final name will be chosen before the first public release (v0.3).

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
first public release, is in progress: full-text search and AI (typed
LLM calls, tools that run as the user, streaming, a test fake;
Anthropic, OpenAI and compatible servers, and Gemini) are done; AI in
the app (stored conversations, budgets, queued generation) and vector
search are next. APIs will change.

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
