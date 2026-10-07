# Changelog

Changes to the `drivers/gcs` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/gcs/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

## [0.3.0] - 2026-10-07

The first release, with the framework's v0.3.0.

### Added
- Google Cloud Storage backend for the `storage` package: `gcs.Driver()`
  (`STORAGE_DRIVER=gcs`, `STORAGE_GCS_BUCKET`, `_PREFIX`,
  `_CREDENTIALS_FILE`, `_CREDENTIALS`, `_SIGNER`) and `gcs.New`, on
  cloud.google.com/go/storage; Application Default Credentials by
  default; V4 signed temporary URLs, signed by a service account key or
  the IAM API; objects stored gzip-compressed read decompressed;
  `STORAGE_EMULATOR_HOST` for an emulator; runs the
  `storage/storagetest` conformance suite against fake-gcs-server in
  process and, with `ANETOS_TEST_GCS_BUCKET`, a real bucket (G1).
