# Changelog

Changes to the `drivers/anthropic` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/anthropic/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

## [0.3.0] - 2026-10-07

The first release, with the framework's v0.3.0.

### Added
- The Anthropic provider of package ai (`AI_PROVIDER=anthropic`) on
  anthropic-sdk-go: the Messages API with text, streaming, tools,
  structured output (`output_config`) and usage; extended thinking
  (`Options.ThinkingBudget`) kept as `ai.Reasoning`; `Options.Params`
  for the rest; `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` (A2).
