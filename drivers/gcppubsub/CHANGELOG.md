# Changelog

Changes to the `drivers/gcppubsub` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/gcppubsub/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added
- Google Cloud Pub/Sub broker for the `pubsub` package: `gcppubsub.Driver()`
  (`PUBSUB_DRIVER=gcp`, `PUBSUB_GCP_PROJECT`, `PUBSUB_GCP_CREATE`) and
  `gcppubsub.NewBroker`, on `cloud.google.com/go/pubsub/v2`; runs the
  `pubsub/pubsubtest` conformance suite against the `pstest` fake server
  (B7).
