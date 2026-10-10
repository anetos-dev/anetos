---
title: Build an AI assistant
since: v0.3.0
group: "Working with AI"
weight: 701
---

# Build an AI assistant

Give your users a chat with an agent that answers from your app's data:
conversations stored in the database, answers streamed to the page as
they're written, slow questions answered in the background, and a usage
budget per user.

## Before you start

- [Add AI to your app](ai.md): the client, agents and tools.
- [Authentication](authentication.md): conversations belong to users.
- A [queue](queues.md), for answers in the background, and the
  [cache](cache.md), where budgets are counted.

The code here comes from [`examples/assistant`](../../../examples/assistant),
a help center whose assistant searches and reads its articles (a
[search by meaning](semantic-search.md), with the articles' full-text
index).

## Steps

### 1. Write the agent

The agent's tools query the app's data. They run as the logged-in user,
so a tool sees only what the user may see. Here, one searches the
articles by their embeddings and words (`articles.Tool`, from
[Search by meaning](semantic-search.md)), and one reads an article:

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

var readArticle = ai.NewTool("read_article", "Read a help-center article",
	func(ctx context.Context, in ReadInput) (Article, error) {
		return db.Find[Article](ctx, in.ID) // not found: 404, told to the model
	})
```

(Copied from [`examples/assistant/agent.go`](../../../examples/assistant/agent.go), region `agent`.)

`anetos make:agent Support` writes a starting point in `app/agents`.

### 2. Set up the client

Add `ai.Migrations()` to your migration sets: the tables of
conversations (`ai_conversations`, `ai_messages`) and of usage records
(`ai_usage`). Then, after the cache, auth and the queue:

```go
client, err := ai.New(app, anthropic.Driver(), openai.Driver(), openai.CompatibleDriver())
if err != nil {
	return nil, err
}
client.TrackUsage(ai.UsageConfig{ // the ai_usage table, and budgets
	Budget: func(context.Context, string) (ai.Budget, error) {
		return ai.Budget{Tokens: settings.DailyTokens, Per: 24 * time.Hour}, nil
	},
	// Prices: map[string]ai.Price{"model-name": {Input: …, Output: …}}, // per million tokens
})
articles, err := ai.EmbeddingsFor(app, articleEmbeddings) // articles_embeddings; ai:embed
if err != nil {
	return nil, err
}
anetos.Provide(app, articles)
helpdesk := newHelpdesk(articles)
if err := ai.QueueAgents(app, helpdesk); err != nil { // answers in the background
	return nil, err
}
```

(Copied from [`examples/assistant`](../../../examples/assistant/main.go), region `setup`.)

`TrackUsage` records each model response's tokens and cost for its user,
and refuses calls once the user's budget for the period is spent.
`EmbeddingsFor` keeps the articles' embeddings, for the search tool.
`QueueAgents` lets the agent answer from queue jobs.

### 3. Store conversations

`ai.StartConversation` stores a conversation of a user, and
`conv.Add` adds the user's question, without calling the model yet:

```go
// Start stores a new conversation with the question, and shows it: the
// page streams the answer.
func (Handlers) Start(c *web.Ctx, in NewChat) (web.Responder, error) {
	userID, err := auth.CurrentID(c)
	if err != nil {
		return nil, err
	}
	conv, err := ai.StartConversation(c, userID, truncate(in.Prompt, 60))
	if err != nil {
		return nil, err
	}
	if err := conv.Add(c, ai.UserMessage(in.Prompt)); err != nil {
		return nil, err
	}
	return web.Redirect(fmt.Sprintf("/chat/%d", conv.ID)), nil
}
```

(Copied from [`examples/assistant`](../../../examples/assistant/main.go), region `start`.)

`ai.FindConversation(ctx, userID, id)` finds one only if it's the
user's (else 404), and `ai.Conversations(ctx, userID)` lists them, the
latest first. `conv.Prompt(ctx, question, agent)` asks and stores the
answer in one call, for code that doesn't stream.

### 4. Stream the answer to the page

The page asks a question with an htmx form, and gets back the question
and a place for the answer, which connects to a stream of server-sent
events:

```go
// Send stores the question and returns it, with a place for the answer
// that streams it from Reply.
func (Handlers) Send(c *web.Ctx, in Question) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := conv.Add(c, ai.UserMessage(in.Prompt)); err != nil {
		return nil, err
	}
	return page("exchange", map[string]any{"Conversation": conv, "Prompt": in.Prompt}), nil
}

// Reply streams the answer to the conversation's last question, and
// stores it.
func (h Handlers) Reply(c *web.Ctx, in ChatPath) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	msgs, err := conv.Messages(c)
	if err != nil {
		return nil, err
	}
	answer := conv.StreamReply(c, h.helpdesk)
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != ai.RoleUser || conv.Status == ai.StatusQueued {
		answer = func(func(ai.Event, error) bool) {} // answered already: a browser that reconnected
	}
	return web.ResponderFunc(func(c *web.Ctx) error { return ai.SSE(c, answer) }), nil
}
```

(Copied from [`examples/assistant`](../../../examples/assistant/main.go), region `send`.)

`conv.StreamReply` sends the conversation to the model, and stores the
answer when it's complete. `ai.SSE` writes it as events: `text` for each
piece (HTML-escaped), `tool` when the model calls a tool, `error` with a
message for the user, and `done` at the end. htmx's SSE extension,
bundled with htmx in `view/htmx`, appends the pieces:

```html
<!-- illustrative -->
<script src="{{call .Asset "htmx.min.js"}}"></script>
<script src="{{call .Asset "htmx-ext-sse.min.js"}}"></script>

<div class="msg assistant" hx-ext="sse" sse-connect="/chat/7/reply" sse-close="done">
  <p class="tool" sse-swap="tool"></p>
  <p sse-swap="text" hx-swap="beforeend"></p>
  <p class="error" sse-swap="error"></p>
</div>
```

`msg`, `tool` and `error` are the example's own classes: its pages are
`html/template`, with their own styles. In a project made with
`anetos new`, make a chat message a component of your own in
`views/ui`, next to the others, so its class names stay in one place
([Style your app](styling.md#5-add-your-own-component)).

`sse-close="done"` matters: browsers reconnect to a stream that ends,
which would ask the model again. The reply handler also answers a
reconnecting browser with an empty stream when the question is answered
already.

An answer takes as long as it takes: `ai.SSE` (through `c.EventStream()`)
lifts `HTTP_REQUEST_TIMEOUT` and `HTTP_WRITE_TIMEOUT` for the stream,
which still stops when the browser goes away.

Only one call at a time can add to a conversation: a question asked while
an answer is written would change the conversation under it, and that
answer would fail with `ai.ErrConversationChanged` (409) rather than be
stored out of order. The example's page disables its form while a stream
is open.

### 5. Answer in the background

For slow questions, or answers that should survive the user leaving,
`conv.QueueReply` answers from a queue job, as the conversation's user;
the conversation's `Status` is `ai.StatusQueued` until the answer is
stored, or `ai.StatusFailed`, with `Error`, if it fails for good:

```go
// Later stores the question for a queue job to answer, and returns a
// placeholder that polls Status.
func (h Handlers) Later(c *web.Ctx, in Question) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := conv.Add(c, ai.UserMessage(in.Prompt)); err != nil {
		return nil, err
	}
	if err := conv.QueueReply(c, h.helpdesk); err != nil {
		return nil, err
	}
	return page("queued", map[string]any{"Conversation": conv, "Prompt": in.Prompt}), nil
}

// Status returns the queued answer when it's there, else the placeholder
// again. If the job left the question unanswered (a question asked in
// the meantime), the answer streams instead.
func (Handlers) Status(c *web.Ctx, in ChatPath) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	switch conv.Status {
	case ai.StatusQueued:
		return page("waiting", map[string]any{"Conversation": conv}), nil
	case ai.StatusFailed:
		return page("failed", map[string]any{"Error": conv.Error}), nil
	}
	msgs, err := conv.Messages(c)
	if err != nil {
		return nil, err
	}
	if n := len(msgs); n > 0 && msgs[n-1].Role == ai.RoleUser {
		return page("answer", map[string]any{"Conversation": conv}), nil
	}
	all := bubbles(msgs)
	return page("bubbles", all[max(len(all)-1, 0):]), nil
}
```

(Copied from [`examples/assistant`](../../../examples/assistant/main.go), region `later`.)

The job runs the agent as the user (`auth.WithUser`): its tools see them as
in a request. A job retried after it answered, or that finds a newer
question, does nothing, so each question gets one answer. A failed
attempt is retried from the start, tools included: make tools that
change things safe to repeat.

With the default `QUEUE_DRIVER=sync`, the job runs in the request that
queued it, once it commits, so it's bound by that request's timeout:
use the `database` or `redis` driver, with workers, for real background
work. A queued reply's own limit is `AI_QUEUE_TIMEOUT` (15 minutes).

### 6. Show the user their usage

Each model response is a row of `ai_usage` (`ai.UsageRecord`), with its
user, conversation, agent, model, tokens and cost. `ai.TotalUsage` adds
them up:

```go
today := anetos.Now(c).UTC().Truncate(24 * time.Hour)
usage, err := ai.TotalUsage(c, userID, today)
if err != nil {
	return err
}
```

(Copied from [`examples/assistant`](../../../examples/assistant/main.go), region `usage`.)

With `UsageConfig.Prices` (per million tokens, by model name), records
have a cost, and budgets can limit it (`ai.Budget{Cost: 2, Per: 24 *
time.Hour}`). A call over budget fails with an `*ai.BudgetError`, a 429
whose message tells the user when they can try again; `ai.SSE` shows it
as the `error` event.

## How it works

A call on a conversation loads its messages, sends them with the new
question, and, once the answer is complete, stores the question, the
model's messages and the tools' results in one transaction, checking
that no other call added messages since. A failed call stores nothing
(the question added with `Add` stays, for a retry). Messages are stored
as JSON, with the reasoning some models need back. See
[AI](../concepts/ai.md#conversations).

> **Coming from Laravel?** Prism leaves storage and streaming to the
> app; here conversations, budgets and queued replies are part of
> package `ai`, and `ai.SSE` replaces a streamed response with
> `echo`/`flush`.

## Testing it

`anetostest.FakeAI` scripts the model's replies, at the start or later
with `app.AI().Add`; the tools, the storage, the stream and the queue
run for real. `startChat` posts the first question and returns the
conversation's path, so the test doesn't assume IDs, which differ on
PostgreSQL and MySQL (their sequences aren't rolled back with a test):

```go
func TestAssistant(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI())
	export := addArticles(t, app)[0] // "Export your lists"
	// The model's replies, scripted: search, read, answer.
	app.AI().Add(
		ai.FakeToolCall("search_articles", map[string]string{"query": "How do I export my lists?"}),
		ai.FakeToolCall("read_article", ReadInput{ID: export.ID}),
		ai.FakeText("Open Settings, then Data, and choose Export (Export your lists)."),
	)
	login(t, app, "Ada")

	chat := startChat(t, app, "How do I export my lists?")
	app.Get(chat).AssertSee("How do I export my lists?", `sse-connect="`+chat+`/reply"`)
	// The answer streams as server-sent events, and is stored.
	app.Get(chat+"/reply").AssertOK().
		AssertSee("event: tool\ndata: search_articles", "event: tool\ndata: read_article",
			"event: text\ndata: Open ", "event: done")
	app.Get(chat).AssertSee("Used: search_articles, read_article", "Open Settings, then Data, and choose Export").
		AssertDontSee("sse-connect")

	// The tools ran for real: the search found the article, with its
	// passage, which the model read.
	reqs := app.AI().Requests()
	want := fmt.Sprintf(`[{"id":%d,"title":"Export your lists","text":"Export your lists\n\nOpen Settings`, export.ID)
	if got := reqs[1].Messages[2].Parts[0].(ai.ToolResult).Content; !strings.HasPrefix(got, want) {
		t.Errorf("search results: %s", got)
	}
	app.AssertPrompted(func(r ai.Request) bool { return strings.Contains(r.System, "help-center articles") })

	// A browser that reconnects gets no second answer.
	app.Get(chat + "/reply").AssertOK().AssertDontSee("event: text")
}
```

(Copied from [`examples/assistant/main_test.go`](../../../examples/assistant/main_test.go), region `test`.)

With the default `QUEUE_DRIVER=sync`, a queued reply runs as soon as
its transaction commits, so a test sees the answer at once:

```go
func TestAnswerLater(t *testing.T) {
	// QUEUE_DRIVER is sync by default: the job runs at once.
	app := anetostest.New(t, setup, anetostest.FakeAI(ai.FakeText("Hello!"), ai.FakeText("Shared lists need a Team plan.")))
	login(t, app, "Ada")
	chat := startChat(t, app, "Hi")
	app.Get(chat + "/reply")

	app.PostForm(chat+"/later", url.Values{"prompt": {"Can I share a list?"}}).
		AssertSee("Can I share a list?", `hx-get="`+chat+`/status"`)
	app.Get(chat + "/status").AssertSee("Shared lists need a Team plan.").AssertDontSee("Working on it")
}
```

(Copied from [`examples/assistant/main_test.go`](../../../examples/assistant/main_test.go), region `test-queue`.)

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| The same question is answered again and again | The page doesn't close the stream on `done`, and the browser reconnects | Add `sse-close="done"` (or close the `EventSource` on `done`) |
| A stream stops after 30 seconds | The stream doesn't go through `c.EventStream()` (or `ai.SSE`), so the request's timeout applies | Use `ai.SSE`, or `web.WithoutTimeout` for other streaming |
| `The conversation changed while the answer was written` | Two questions at once in one conversation | Let one answer finish before the next question |
| `ai: agent "…" can't answer queued replies` | The agent wasn't passed to `ai.QueueAgents`, or has no `Name` | Pass it at setup |
| A queued reply stays `queued` | No worker runs the queue (with `QUEUE_DRIVER=database` or `redis`) | Run `./app run --only=worker`, or everything |
| `You've reached your AI usage limit` | The user's budget is spent | Raise the budget, or wait for the period to start again |
| No cost in `ai_usage` | No price for the model's name | Add it to `Prices`, by the name responses report (`Model`) |
| Usage records vanish | The call ran inside a transaction that rolled back | Call models outside transactions |
| "The reply failed" for long background answers with the sync driver | The reply ran within the queuing request's `HTTP_REQUEST_TIMEOUT` | Use a real queue driver and workers |

## Next steps

- [AI reference](../reference/ai.md#conversations): every function and
  option.
- [Queues](queues.md): workers, retries and failed jobs.
- [Roles and permissions](roles-and-permissions.md): what the agent's
  tools may do, for whom.
