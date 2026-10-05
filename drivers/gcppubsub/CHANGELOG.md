# Changelog

Changes to the `drivers/gcppubsub` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/gcppubsub/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Security
- OpenTelemetry (an indirect dependency of the Pub/Sub client) to v1.45.0,
  for GHSA-8wmf-6v46-5gfg / CVE-2026-81870: exporter endpoints written to
  OpenTelemetry's own Info logs. The driver doesn't configure OpenTelemetry,
  so apps were only exposed if they enabled its verbose logging themselves.

## [0.2.0] - 2026-10-02

The first release, with the framework's v0.2.0.

### Added
- Google Cloud Pub/Sub broker for the `pubsub` package: `gcppubsub.Driver()`
  (`PUBSUB_DRIVER=gcp`, `PUBSUB_GCP_PROJECT`, `PUBSUB_GCP_CREATE`) and
  `gcppubsub.NewBroker`, on `cloud.google.com/go/pubsub/v2`; runs the
  `pubsub/pubsubtest` conformance suite against the `pstest` fake server
  (B7).
