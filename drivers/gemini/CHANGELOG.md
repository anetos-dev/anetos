# Changelog

Changes to the `drivers/gemini` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/gemini/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added
- The Gemini provider of package ai (`AI_PROVIDER=gemini`) on Google's
  Gen AI SDK: the Gemini API with text, streaming, function calls,
  structured output (`responseJsonSchema`) and usage; thought signatures
  kept as `ai.Reasoning`; `Options.ThinkingBudget`, `Options.Config`;
  retries on rate limits and server errors; `GEMINI_API_KEY`,
  `GEMINI_BASE_URL`. It requires google.golang.org/grpc v1.84.0 or
  later, for fixes the SDK's minimum lacks (A2).
