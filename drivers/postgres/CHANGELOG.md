# Changelog

Changes to the `drivers/postgres` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/postgres/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

## [0.1.0] - 2026-09-30

### Added
- PostgreSQL through pgx (stdlib mode): DSN from `DB_*` or `DB_URL`, UTC session time zone, advisory-lock migrations (F7).
- Runs the `db/dbtest` conformance suite, and a `anetostest` app test
  (F7, F8, F12).
