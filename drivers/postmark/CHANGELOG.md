# Changelog

Changes to the `drivers/postmark` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/postmark/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added
- Postmark transport for the `mailer` package: `postmark.Driver()`
  (`MAIL_DRIVER=postmark`, `MAIL_POSTMARK_TOKEN`, `MAIL_POSTMARK_STREAM`)
  and `postmark.NewTransport`, on Postmark's HTTP API with the standard
  library only (B9).
