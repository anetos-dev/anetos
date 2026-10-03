# Changelog

Changes to the `drivers/mysql` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/mysql/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added
- On MariaDB 11.7+, each session sets `mhnsw_ef_search` to 1000, so vector
  searches get their candidates under selective filters (MariaDB's
  default is 20); MySQL ignores it (S2, D182).

## [0.2.0] - 2026-10-02

Released with the framework's v0.2.0.

### Added
- Its conformance tests (`db/dbtest`) also cover the database cache,
  session and queue stores (B1, B2, B5).

## [0.1.0] - 2026-09-30

### Added
- MySQL and MariaDB through go-sql-driver/mysql: DSN from `DB_*` or `DB_URL` with `parseTime`, UTC and found-rows settings, named-lock migrations; tested on MySQL 8.0 and MariaDB 10.11 (F7).
- Runs the `db/dbtest` conformance suite, and an `anetostest` app test
  (F7, F8, F12).
