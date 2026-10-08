# Changelog

Changes to the `drivers/gemini` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/gemini/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

## [0.4.0] - 2026-10-08

Released with the framework's v0.4.0.

### Changed
- Indirect dependencies updated (Dependabot); the Google API client
  stays below v0.299.0, which requires gRPC 1.84 (GO-2026-6443).

## [0.3.0] - 2026-10-07

The first release, with the framework's v0.3.0.

### Added
- `Provider.Embed`: embeddings with `RETRIEVAL_DOCUMENT` and
  `RETRIEVAL_QUERY` task types and `outputDimensionality`; the usage is
  estimated (S2).
- The Gemini provider of package ai (`AI_PROVIDER=gemini`) on Google's
  Gen AI SDK: the Gemini API with text, streaming, function calls,
  structured output (`responseJsonSchema`) and usage; thought signatures
  kept as `ai.Reasoning`; `Options.ThinkingBudget`, `Options.Config`;
  retries on rate limits and server errors; `GEMINI_API_KEY`,
  `GEMINI_BASE_URL`. It requires google.golang.org/grpc v1.83.2 or
  later, for fixes the SDK's minimum lacks (A2); not v1.84.0, which has
  GO-2026-6443 and no fixed release yet.
