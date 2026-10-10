# Changelog

Changes to the `drivers/gcs` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/gcs/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Changed
- `WithClientOptions` (was `ClientOptions`), and
  `Backend.TemporaryURL`, which implements `storage.TemporaryURLBackend`
  (was `SignedURL`); the old names are deprecated until v0.6 (M8b-5).

### Security
- `golang.org/x/net` v0.60.0, an indirect dependency (GO-2026-6612,
  GO-2026-6617).

## [0.4.0] - 2026-10-08

Released with the framework's v0.4.0.

### Changed
- `google.golang.org/api` v0.298.0 (was v0.293.0),
  `cloud.google.com/go/auth` v0.24.0 and their dependencies
  (Dependabot); the API client stays below v0.299.0, which requires
  gRPC 1.84 (GO-2026-6443).

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
