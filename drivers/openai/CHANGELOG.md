# Changelog

Changes to the `drivers/openai` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/openai/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

## [0.3.0] - 2026-10-07

The first release, with the framework's v0.3.0.

### Added
- `Provider.Embed`: embeddings with the Embeddings API, for OpenAI and
  compatible servers (`dimensions` when asked); `AI_EMBEDDING_PROVIDER=openai`
  uses them with another provider's chat (S2).
- The OpenAI provider of package ai (`AI_PROVIDER=openai`) on
  openai-go, and the provider for OpenAI-compatible servers
  (`AI_PROVIDER=openai-compatible`: Ollama, vLLM, LM Studio, OpenRouter,
  Groq…): Chat Completions with text, streaming, tools, structured
  output (strict when the schema allows) and usage;
  `Options.ReasoningEffort`, `Options.Params`; `OPENAI_API_KEY`,
  `OPENAI_BASE_URL`, `OPENAI_COMPATIBLE_URL`, `OPENAI_COMPATIBLE_KEY` (A2).
