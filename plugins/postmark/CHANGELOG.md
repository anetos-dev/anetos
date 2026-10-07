# Changelog

Changes to the `plugins/postmark` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `plugins/postmark/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

## [0.3.0] - 2026-10-07

### Changed
- Works with Anetos v0.3 (`Requires`: `>= v0.2.0, < v0.4.0`).

## [0.2.0] - 2026-10-02

The first release, with the framework's v0.2.0.

### Added
- Postmark transport for the `mailer` package: `postmark.Driver()`
  (`MAIL_DRIVER=postmark`, `MAIL_POSTMARK_TOKEN`, `MAIL_POSTMARK_STREAM`)
  and `postmark.NewTransport`, on Postmark's HTTP API with the standard
  library only (B9).
- The plugin, `postmark.Plugin()`, for `anetos add`: the webhook
  `POST /postmark/webhook` (basic auth from `POSTMARK_WEBHOOK_USER` and
  `POSTMARK_WEBHOOK_PASSWORD`), the job `postmark:webhook`, the table
  `postmark_suppressions`, `postmark.Suppressed`, and the commands
  `postmark:suppressions` and `postmark:unsuppress` (B11).
- A README: what the plugin adds, sending through Postmark, the webhook
  (v0.2 checks).

### Changed
- The module moved from `drivers/postmark` (B11).
