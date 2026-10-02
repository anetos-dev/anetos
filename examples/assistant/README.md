# Assistant: an AI help desk

A help center for Tidy, a made-up to-do app, with an AI assistant that
answers from its articles. It shows what package `ai` adds to an app:

- **An agent with tools over the app's data** ([`agent.go`](agent.go)):
  full-text search over the articles, and reading one.
- **Stored conversations**: each user's, listed on the home page.
- **Answers streamed to the page** as they're written: server-sent
  events (`ai.SSE`) with htmx's SSE extension.
- **Answers in the background**: "Ask in the background" queues the
  reply (`conv.QueueReply`); the page polls until it's there.
- **A daily budget per user** (`ASSISTANT_DAILY_TOKENS`, 200,000 tokens
  by default) and today's usage on the home page.

## Run it

```sh
anetos key:generate >> .env           # APP_KEY, once
export APP_ENV=development HTTP_ADDR=:8080
export AI_PROVIDER=anthropic AI_MODEL=claude-sonnet-4-5 ANTHROPIC_API_KEY=…
go run . migrate
go run . seed                          # the articles, and ada@example.com (password: password)
go run .
```

Open http://localhost:8080 and sign in. For a local model, use
`AI_PROVIDER=openai-compatible`, `AI_MODEL` (say, `llama3.1`) and
`OPENAI_COMPATIBLE_URL=http://localhost:11434/v1` (Ollama); OpenAI works
with `AI_PROVIDER=openai` and `OPENAI_API_KEY`.

By default (`QUEUE_DRIVER=sync`), a background answer runs right after
the request that asked for it, within its timeout. With
`QUEUE_DRIVER=database`, it waits for a worker: `go run . run
--only=workers` in another terminal, or `go run .` runs everything.

The tests ([`main_test.go`](main_test.go)) script the model with
`anetostest.FakeAI`. See the guide
[Build an AI assistant](../../docs/site/guides/ai-assistant.md).
