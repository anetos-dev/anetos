# Changelog

Changes to the `drivers/s3` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/s3/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Changed
- `Option` no longer exposes the MinIO client's options (a function of
  `*minio.Options`): `WithTransport` (was `Transport`, deprecated until
  v0.6) is the option there is. `Backend.TemporaryURL` implements
  `storage.TemporaryURLBackend` (was `SignedURL`, deprecated until v0.6)
  (M8b-5).

### Security
- `golang.org/x/net` v0.60.0, an indirect dependency (GO-2026-6612,
  GO-2026-6617).

## [0.4.0] - 2026-10-08

Released with the framework's v0.4.0.

### Changed
- Indirect dependencies updated (Dependabot).

## [0.3.0] - 2026-10-07

No changes of its own; released with the framework's v0.3.0.

## [0.2.0] - 2026-10-02

The first release, with the framework's v0.2.0.

### Added
- S3 and S3-compatible backend for the `storage` package: `s3.Driver()`
  (`STORAGE_DRIVER=s3`, `STORAGE_S3_BUCKET`, `_REGION`, `_ENDPOINT`,
  `_ACCESS_KEY`, `_SECRET_KEY`, `_PATH_STYLE`, `_PREFIX`) and `s3.New`,
  on github.com/minio/minio-go/v7; presigned temporary URLs; runs the
  `storage/storagetest` conformance suite against an in-process fake and,
  with `ANETOS_TEST_S3_URL`, a real server (B10).
