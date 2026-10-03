# Changelog

Changes to the `drivers/postgres` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/postgres/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added
- Each session sets pgvector's `hnsw.ef_search` to `db.SimilarCandidates`
  and, with pgvector 0.8+, `hnsw.iterative_scan` to `strict_order`, so
  vector searches get their 200 candidates after filters (by default the
  index stops at 40) (S2, D182).

## [0.2.0] - 2026-10-02

Released with the framework's v0.2.0.

### Added
- Its conformance tests (`db/dbtest`) also cover the database cache,
  session and queue stores (B1, B2, B5).

## [0.1.0] - 2026-09-30

### Added
- PostgreSQL through pgx (stdlib mode): DSN from `DB_*` or `DB_URL`, UTC session time zone, advisory-lock migrations (F7).
- Runs the `db/dbtest` conformance suite, and an `anetostest` app test
  (F7, F8, F12).
