# Assistant: an AI help desk

A help center for Tidy, a made-up to-do app, with an AI assistant that
answers from its articles. It shows what package `ai` adds to an app:

- **An agent with tools over the app's data** ([`agent.go`](agent.go)):
  a hybrid search of the articles (`ai.Embeddings`: by meaning, with
  their embeddings, and by their words, with the full-text index), and
  reading one.
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
export AI_EMBEDDING_PROVIDER=openai AI_EMBEDDING_MODEL=text-embedding-3-small OPENAI_API_KEY=…
go run . migrate
go run . seed                          # the articles, embedded, and ada@example.com (password: password)
go run .
```

Anthropic has no embeddings model, so `AI_EMBEDDING_PROVIDER` sends the
embeddings to OpenAI. (For Gemini's `gemini-embedding-001`, add
`gemini.Driver()` from `drivers/gemini` to `ai.New` in `main.go`.)
The vectors have 1536 dimensions
(`embeddingDims` in [`agent.go`](agent.go), and the migration); for a
model that makes others, change it. After changing the model, `go run .
ai:embed` embeds the articles again.

Open http://localhost:8080 and sign in. For a local model, use
`AI_PROVIDER=openai-compatible`, `AI_MODEL` (say, `llama3.1`) and
`OPENAI_COMPATIBLE_URL=http://localhost:11434/v1` (Ollama), with an
embedding model of the same server (`AI_EMBEDDING_MODEL`; set
`embeddingDims` to its size, and `FixedSize: true` in
`articleEmbeddings`: such models make one size); OpenAI works with `AI_PROVIDER=openai` and
`OPENAI_API_KEY`, for both.

It runs on SQLite by default, and on a database
that searches vectors: PostgreSQL with pgvector (`DB_DRIVER=postgres`
and `DB_URL`, or the `DB_*` settings) or MariaDB 11.7+
(`DB_DRIVER=mysql`). On MySQL or an older MariaDB it refuses to
start, saying why. The tests run on any of them the same way:
`DB_DRIVER=postgres DB_URL=postgres://…/assistant_test go test ./...`.

By default (`QUEUE_DRIVER=sync`), a background answer runs right after
the request that asked for it, within its timeout. With
`QUEUE_DRIVER=database`, it waits for a worker: `go run . run
--only=workers` in another terminal, or `go run .` runs everything.

The tests ([`main_test.go`](main_test.go)) script the model with
`anetostest.FakeAI`; the fake makes embeddings too (from the texts'
words), so search works without a model. See the guide
[Build an AI assistant](../../docs/site/guides/ai-assistant.md).
