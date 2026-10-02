---
title: Search by meaning
since: v0.3.0
---

# Search by meaning

Find records by what they mean, not only by the words they share: "how
much is the team plan" finds the article on plans and billing, which
never says "how much". Each record's text is split into chunks, turned
into vectors (embeddings) by an embedding model, and stored next to the
record; a search embeds the question and returns the nearest records,
with the passage that matched. With a full-text index too, the search is
*hybrid*: records that its words find rank high as well, so names, codes
and rare words aren't missed. An agent can use the search as a tool, to
answer from your data (retrieval-augmented generation).

The code here comes from [`examples/assistant`](../../../examples/assistant),
a help center whose assistant searches its articles.

## Before you start

- [Add AI to your app](ai.md): `ai.ForApp`, and a provider with an
  embedding model (OpenAI, Gemini, or an OpenAI-compatible server).
  Anthropic has none: use another provider for the embeddings.
- A database that stores vectors: SQLite, PostgreSQL with
  [pgvector](https://github.com/pgvector/pgvector), or MariaDB 11.7+
  (see [what each database can do](#what-each-database-can-do)).
- A [queue](queues.md), to embed in the background (optional).

## Steps

### 1. Choose the embedding model

Two settings name the model, and the provider if it isn't your chat
provider's:

```bash
AI_EMBEDDING_MODEL=text-embedding-3-small   # OpenAI; gemini-embedding-001 for Gemini
AI_EMBEDDING_PROVIDER=openai                # when AI_PROVIDER has no embeddings (anthropic)
OPENAI_API_KEY=…
```

Pass the provider's driver to `ai.ForApp`, as for chat. Tests use the
fake, which embeds without a model.

### 2. Add an embeddings table

In a migration, `CreateEmbeddings` creates `<table>_embeddings`: a row
per chunk of a record, with its text and vector, and an index for the
search where the database has one. The size of the vectors is the
model's:

```go
Migrations.AddFunc("2026_10_02_140000_create_articles_embeddings",
	func(s *migrate.Schema) error {
		return s.CreateEmbeddings("articles", embeddingDims) // articles_embeddings
	},
	func(s *migrate.Schema) error { return s.DropEmbeddings("articles") })
```

(Copied from [`examples/assistant/models.go`](../../../examples/assistant/models.go), region `migration`.)

OpenAI's `text-embedding-3` models and Gemini's `gemini-embedding-001`
can shorten their vectors to the size you ask for; other models make one
size (768 for `nomic-embed-text`, 1536 for `text-embedding-ada-002`) and
refuse to be asked: set `FixedSize` in the next step. The table's size
must be the vectors'.

On PostgreSQL, the migration creates the `vector` extension if it isn't
there, which takes a superuser: if the app's user isn't one, have one run
`CREATE EXTENSION vector` once.

### 3. Say what to embed

An `ai.EmbeddingsConfig` gives the record's text, the vectors' size and,
for the agent's tool, its title:

```go
// embeddingDims is the size of the articles' vectors: the embeddings
// table's (the migration), and what the embedding model makes. 1536 suits
// OpenAI's text-embedding-3-small and Gemini's gemini-embedding-001; for
// another model, change both (and set FixedSize if it can't shorten its
// vectors).
const embeddingDims = 1536

// articleEmbeddings is how the articles are embedded: their title and
// body, split into chunks.
var articleEmbeddings = ai.EmbeddingsConfig[Article]{
	Text:       func(a Article) string { return a.Title + "\n\n" + a.Body },
	Title:      func(a Article) string { return a.Title },
	Dimensions: embeddingDims,
}
```

(Copied from [`examples/assistant/agent.go`](../../../examples/assistant/agent.go), region `embeddings`.)

`ai.EmbeddingsFor(app, articleEmbeddings)`, at setup after `ai.ForApp`
(and `queue.ForApp`), returns the `*ai.Embeddings[Article]` that keeps
and searches them. `FixedSize: true` is for a model that makes one size
of vector: the size isn't asked for, only checked. `Scope` limits every
search to what the user may see:

```go
// illustrative
notes, err := ai.EmbeddingsFor(app, ai.EmbeddingsConfig[Note]{
	Text:       func(n Note) string { return n.Body },
	Dimensions: 1536,
	Scope: func(ctx context.Context, q *db.Q[Note]) *db.Q[Note] {
		user, _ := auth.Current[*User](ctx)
		return q.Where(NoteCols.TeamID.Eq(user.TeamID))
	},
})
```

### 4. Keep the embeddings up to date

Call `Sync` after creating or changing records:

```go
// seed adds the articles, embedded, and a user.
func seed(ctx context.Context, args *cmd.Args, embeddings *ai.Embeddings[Article]) error {
	articles := append([]Article(nil), helpCenter...)
	if err := db.CreateMany(ctx, articles); err != nil {
		return err
	}
	if err := embeddings.Sync(ctx, articles...); err != nil { // a queue job; with QUEUE_DRIVER=sync, now
		return err
	}
```

(Copied from [`examples/assistant`](../../../examples/assistant/main.go), region `seed`.)

With the app's queue, `Sync` dispatches a job (`ai.embed:articles`) when
the transaction commits, so a request doesn't wait for the model;
without one, it embeds right away. Only chunks whose text changed are
embedded again. Deleting a record deletes its chunks (on MariaDB, a
record deleted by another table's cascading foreign key leaves them
until `ai:embed`; searches skip them).

For records that exist already, and after changing the model or `Text`,
run the command, which also deletes chunks left by deleted records:

```bash
go run . ai:embed            # every table's records; or name the tables
```

### 5. Search

`Search` embeds the question and returns the nearest records, best
first, with the chunk nearest the question:

```go
// illustrative
found, err := articles.Search(c, in.Q, 5)
for _, p := range found {
	fmt.Println(p.Record.Title, p.Text, p.Distance) // the passage, and how far it is (0: same meaning)
}
```

Extra scopes narrow one search: `articles.Search(ctx, q, 5,
func(q *db.Q[Article]) *db.Q[Article] { return q.Where(…) })`.

For an agent, `Tool` makes the search a tool that returns the best
records' IDs, titles and passages:

```go
// newHelpdesk returns the agent that answers questions about Tidy from
// the help center, with two tools over the articles: a hybrid search
// (their meaning and their words), and reading one.
func newHelpdesk(articles *ai.Embeddings[Article]) ai.Agent {
	return ai.Agent{
		Name: "helpdesk",
		Instructions: "You answer questions about Tidy, a to-do app, from its help-center articles. " +
			"Search the articles, read the best match if its passage isn't enough, and answer briefly, naming the article. " +
			"If the articles don't say, say you don't know.",
		Tools: []ai.Tool{
			articles.Tool("search_articles", "Search the help center by meaning and words; returns the best articles' IDs, titles and passages", 3),
			readArticle,
		},
		MaxSteps: 6,
	}
}

// ReadInput is what the model sends to read_article.
type ReadInput struct {
	ID int64 `json:"id" description:"The article's ID, from search_articles" validate:"required"`
}

var readArticle = ai.Func("read_article", "Read a help-center article",
	func(ctx context.Context, in ReadInput) (Article, error) {
		return db.Find[Article](ctx, in.ID) // not found: 404, told to the model
	})
```

(Copied from [`examples/assistant/agent.go`](../../../examples/assistant/agent.go), region `agent`.)

The tool runs with the context of the conversation, so `Scope` sees the
user the agent answers.

### 6. Test it

The fake embeds texts by their words, so texts sharing words are near,
and no model is called:

```go
func TestSearchArticles(t *testing.T) {
	app := anetostest.New(t, setup)
	addArticles(t, app)
	embeddings, err := anetos.Resolve[*ai.Embeddings[Article]](app.App)
	if err != nil {
		t.Fatal(err)
	}
	// Each article was embedded once, as a document.
	if reqs := app.AI().Embeddings(); len(reqs) != 1 || len(reqs[0].Inputs) != len(helpCenter) || reqs[0].Dimensions != embeddingDims {
		t.Fatalf("embedding requests: %+v", reqs)
	}
	found, err := embeddings.Search(app.Context(), "what does the team plan cost", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Record.Title != "Plans and billing" {
		t.Errorf("found %+v", found)
	}
}
```

(Copied from [`examples/assistant/main_test.go`](../../../examples/assistant/main_test.go), region `test-search`.)

## Search without package ai

The query builder searches vectors you made yourself:
`db.Query[Article](ctx).Similar(model, v)` orders the records by their
nearest chunk of that model, and `Hybrid(text, model, v)` merges that
order with full-text search's by reciprocal rank fusion. Both combine
with `Where`, `Limit`, `Count` and `Paginate`. `db.ReplaceChunks`,
`db.Chunks` and `db.NearestChunks` read and write the chunks; `ai.Embed`
and `ai.EmbedQuery` make vectors with the app's model. See the
[query builder reference](../reference/query-builder.md#vector-search).

## What each database can do

| | SQLite | PostgreSQL | MariaDB | MySQL |
|---|---|---|---|---|
| Vector search | built in: the driver compares every chunk | with [pgvector](https://github.com/pgvector/pgvector) (`CREATE EXTENSION vector`: the migration runs it, as a superuser) | 11.7 and later | not supported |
| Index | none: fine for thousands of chunks | HNSW, up to 2000 dimensions; above, every chunk is compared | its vector index (HNSW) | |
| Hybrid search | yes | yes | yes | |

Without vector search, `CreateEmbeddings` stops with a message naming
the database and what it needs, and `Similar`'s error says which
databases have it. Indexes find the nearest
chunks approximately: the best 200 chunks are the candidates of each
search, so a record with many chunks near the question takes more of
them. The PostgreSQL driver sets pgvector's `hnsw.ef_search` to 200
and, with pgvector 0.8+, `hnsw.iterative_scan` for each session, so the
index returns that many after the query's conditions, in order (by
default it stops at 40). The MySQL driver sets MariaDB's
`mhnsw_ef_search` to 1000 (its default is 20) for the same reason. They
apply to the app's own vector queries too; a connection pooler that
refuses them keeps the defaults.

## How it works

`CreateEmbeddings` makes `<table>_embeddings`: `record_id` (the
record's ID, deleted with it: by a foreign key, or on MariaDB by a
trigger), `chunk` (the chunk's position), `content`, `content_hash`,
`model`, `embedding` and timestamps. `Sync` splits the text into chunks
of at most 2000 characters (`ChunkSize`): whole paragraphs while they
fit, then sentences (after `.`, `!` or `?` and a space, or a full-width
`。！？`), then words. It embeds the chunks whose text (`content_hash`)
or model changed, and replaces the record's chunks in one transaction
that locks the record, so two workers take turns. With the queue, `Sync`
dispatches a job per hundred records.

A search embeds the question as a query (Gemini embeds queries and
documents differently), takes the 200 chunks nearest it by cosine
distance, of the current model only, and ranks each record by its
nearest chunk. A hybrid search ranks the full-text matches too, and
orders the records by the sum of `1/(60 + rank)` over the two lists:
records both find come first, without comparing their scores. The
embedding requests count in `ai_usage` (agent `embed`) and against
budgets, as model calls do.

Chunks of another model are ignored, so changing `AI_EMBEDDING_MODEL`
doesn't mix vectors that can't be compared; until `ai:embed` has run,
searches find only what was embedded since. A model with another vector
size needs a new migration (`DropEmbeddings`, then `CreateEmbeddings`).

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `ai: the AI provider has no embeddings` | `AI_PROVIDER` is `anthropic`, and `AI_EMBEDDING_PROVIDER` isn't set | Set `AI_EMBEDDING_PROVIDER` and `AI_EMBEDDING_MODEL`, and pass its driver to `ai.ForApp` |
| `the model made vectors of 768 dimensions, not the 1536 asked for` | The model can't shorten its vectors to the table's size | Choose a model that can, or recreate the table with its size and set `FixedSize` |
| The provider refuses `dimensions` | The model makes one size (`text-embedding-ada-002`, models of compatible servers) | `FixedSize: true`, with `Dimensions` its size |
| `permission denied to create extension "vector"` | pgvector isn't a trusted extension | Have a superuser run `CREATE EXTENSION vector` |
| `… needs vector search, which this … database doesn't have` | MySQL, MariaDB before 11.7, or PostgreSQL without pgvector | Install pgvector, upgrade MariaDB, or use SQLite |
| A search finds nothing after changing the model | The chunks are the old model's | `go run . ai:embed` |
| New records aren't found | `Sync` wasn't called, or the queue's workers aren't running | Call `Sync` after saving; run the workers |
| `a vector of 768 dimensions for articles_embeddings, whose vectors have 1536` | A query vector from another model | Embed with the app's model (`ai.EmbedQuery`) |
| `CREATE TRIGGER` is refused on MariaDB | The migration's trigger needs the `TRIGGER` privilege (and, with binary logging, `SUPER` or `log_bin_trust_function_creators`) | Grant it to the migrating user |

## Next steps

- [Build an AI assistant](ai-assistant.md): the agent that uses the
  search, in a chat.
- [Add full-text search](search.md): the index a hybrid search uses.
- [AI reference](../reference/ai.md#embeddings): every function and
  setting.

> **Coming from Laravel?** Where a Laravel app pairs Prism's embeddings
> with a vector store of its choosing, here the migration declares the
> table, `Sync` plays the part of Scout's `searchable()`, and the
> database (pgvector, MariaDB or SQLite) does the search, with no search
> server to run.
