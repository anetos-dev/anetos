# Changelog

Changes to the `drivers/sqlite` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/sqlite/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

## [0.2.0] - 2026-10-02

Released with the framework's v0.2.0.

### Added
- Its conformance tests (`db/dbtest`) also cover the database cache,
  session and queue stores (B1, B2, B5).

## [0.1.0] - 2026-09-30

### Added
- SQLite through modernc.org/sqlite (pure Go, no C compiler): file and in-memory databases (one connection for `:memory:`), WAL and foreign keys on, busy timeout, UTC text times (F7).
- Runs the `db/dbtest` conformance suite, and a `anetostest` app test
  (F7, F8, F12).
