# Anetos

> **Working codename.** The final name will be chosen before the first public release (v0.3).

A batteries-included Go web framework with the developer comfort of Laravel,
built the Go way: typed, `net/http`-compatible, code generation instead of
runtime magic, and a supervised runtime where HTTP, queue workers, pub/sub
listeners and the scheduler run together in **one binary**.

**Status:** pre-alpha, building v0.1. The kernel, configuration, runtime
supervisor, HTTP layer, validation, data layer, migrations and model code
generation exist; views, the CLI and testing helpers are next. APIs will
change.

## Documents

- [Planning & roadmap](docs/planning/roadmap.md): vision, milestones v0.1–v0.4, work packages, risks
- [Design document](docs/design/design.md): architecture, principles and decisions
- [Documentation guide](docs/contributing/documentation-guide.md): how docs are written alongside code

## Planned developer experience

```bash
anetos new blog && cd blog
anetos dev                 # hot reload, SQLite by default

anetos build
./blog run                 # web + workers + listeners + scheduler
./blog run --only=http     # or split by role when you scale
```

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
