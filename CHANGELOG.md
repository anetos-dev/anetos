# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/) (see the roadmap's versioning rules).

## [Unreleased]

### Added
- Planning & roadmap, design document, documentation guide, ADR template.
- Apache-2.0 `LICENSE` and `NOTICE`.
- Repository tooling: Go module, Makefile (`make check`), golangci-lint config,
  SPDX header check, CI workflow, docs site skeleton, `examples/` (F1).
- Configuration (`config` package): `.env` parser with quoting, escapes and
  `${VAR}` expansion; layered loading (environment > `.env.<APP_ENV>` >
  `.env`); typed binding with `env`, `default` and `prefix` tags; every
  missing or invalid key reported at once; `Validate()` hook (F3).
- Runtime supervisor (`supervisor` package): components, roles, restart
  policies with exponential backoff and jitter, panic recovery, staged
  graceful shutdown with a deadline, readiness and status (F4).
- App kernel (`anetos` package): `anetos.New`, `AppConfig` (`APP_*`,
  `LOG_*`), structured logging with `log/slog`, typed service container
  (`Provide`, `Resolve`), providers with Register/Boot phases, `app.Go` and
  `app.Component`, shutdown hooks within one total shutdown budget,
  `app.Run` with roles, `app.Close` for boot-only programs (F2).
- Docs: configuration guide and reference, background tasks guide,
  application lifecycle and runtime supervisor concepts, `examples/lifecycle`.

### Changed
- Minimum Go version is now 1.26 (the older of the two supported releases);
  code modernized for it (`errors.AsType`, `slices.Backward`,
  `sync.WaitGroup.Go`, …) and the `modernize` linter enabled (design D18).
