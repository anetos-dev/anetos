---
title: Add AI to your app
since: v0.3.0
group: "Working with AI"
weight: 700
---

# Add AI to your app

Use a language model in your app: summarize text into a typed struct,
answer questions with tools that look up the user's own data, and stream
the answer as it's written. Package `ai` does the work around the model:
schemas, validation, the tool loop, logging and tests that never call a
model. The complete example is [`examples/ai`](../../../examples/ai), a
small support desk API.

## Before you start

You have an app created with `anetos.New()`. Choose the provider with
`AI_PROVIDER`, and the model with `AI_MODEL`. Each provider is a driver
module, on the provider's official Go SDK, so your app depends only on
the SDKs it uses:

| `AI_PROVIDER` | Driver | Settings |
|---|---|---|
| `anthropic` | `anetos.dev/anetos/drivers/anthropic` | `ANTHROPIC_API_KEY` |
| `openai` | `anetos.dev/anetos/drivers/openai` | `OPENAI_API_KEY` |
| `openai-compatible` | the same, `openai.CompatibleDriver()`: Ollama, vLLM, LM Studio, OpenRouter, Groq… | `OPENAI_COMPATIBLE_URL`, and `OPENAI_COMPATIBLE_KEY` if the server needs one |
| `gemini` | `anetos.dev/anetos/drivers/gemini` | `GEMINI_API_KEY` |
| `fake` | built in: no model is called, requests get scripted replies | Tests (`anetostest` sets it) |

```env
AI_PROVIDER=anthropic
AI_MODEL=claude-haiku-4-5
ANTHROPIC_API_KEY=${ANTHROPIC_API_KEY}
```

For a local model, run [Ollama](https://ollama.com) and point the
compatible driver at it: `AI_PROVIDER=openai-compatible`,
`OPENAI_COMPATIBLE_URL=http://localhost:11434/v1`, `AI_MODEL=llama3.2`.

`AI_MAX_TOKENS` (default 4096) bounds each answer's length, and
`AI_TIMEOUT` (default 10 minutes) each request's time. See
[Configuration](../reference/configuration.md#ai). The app doesn't
start if the provider's key or `AI_MODEL` is missing.

## Steps

### 1. Set up the client

In your setup function, after `anetos.New()`, call `ai.New` with
the drivers of the providers your app may use (`AI_PROVIDER` picks one):

```go
// AI_PROVIDER picks one of these, AI_MODEL the model.
if _, err := ai.New(app, anthropic.Driver(), openai.Driver(), openai.CompatibleDriver(), gemini.Driver()); err != nil {
	return nil, err
}
```

(Copied from [`examples/ai`](../../../examples/ai), region `setup`.)

`ai.New` puts the client in every context the app creates: requests,
jobs, listeners, commands. Calls find it there.

### 2. Generate text

`ai.Generate` sends a prompt and returns the answer, with the tokens it
used:

```go
// illustrative
res, err := ai.Generate(ctx, "Summarize in one sentence: "+post.Body,
	ai.System("You write for a general audience."))
if err != nil {
	return err
}
fmt.Println(res.Text(), res.Usage.InputTokens, res.Usage.OutputTokens)
```

In a request handler, a call runs within the server's limits:
`HTTP_REQUEST_TIMEOUT` (30 seconds by default) cancels the request's
context, and with it the call (a 503). An agent that calls a few tools,
or a long answer, can take longer: raise it for your app (`0` disables
it), or keep model calls short.

Options shape the call: `ai.System` (instructions), `ai.Model`,
`ai.MaxTokens`, `ai.Temperature`, `ai.Timeout`. To continue a
conversation, pass the previous result's messages:
`ai.Generate(ctx, "And in French?", ai.Messages(res.Messages...))`.
Messages marshal to JSON, so you can store a conversation and continue it
in a later request. Continue only from a successful call: after an error,
the conversation can end with tool calls that have no results, which
providers refuse. (An answer cut off at the token limit doesn't run its
tool calls: they get error results, so the conversation stays valid.)

### 3. Get a typed answer

Describe the answer as a struct. `ai.GenerateObject` gives the model its
JSON schema, built from the `json`, `description` and `validate` tags,
and checks the answer with the `validate` rules:

```go
// Summary is a customer message, summarized. The model is given its
// schema, built from the json, description and validate tags, and its
// answer is checked with the validate rules.
type Summary struct {
	Topic     string   `json:"topic" description:"What the message is about, in a few words" validate:"required|max:60"`
	Sentiment string   `json:"sentiment" validate:"required|in:positive,neutral,negative"`
	Urgent    bool     `json:"urgent" description:"Whether the customer needs an answer today"`
	Tags      []string `json:"tags" description:"Lower-case keywords" validate:"max:5"`
}

// SummarizeInput is a customer's message.
type SummarizeInput struct {
	Text string `json:"text" validate:"required|max:5000"`
}

// Summarize summarizes a customer's message.
func Summarize(c *web.Ctx, in SummarizeInput) (Summary, error) {
	sum, _, err := ai.GenerateObject[Summary](c, "Summarize this customer message:\n\n"+in.Text,
		ai.System("You triage a support inbox."))
	return sum, err
}
```

(Copied from [`examples/ai`](../../../examples/ai), region `summary`.)

If the answer isn't valid JSON of that shape, or breaks a rule
(`sentiment` isn't one of the three), it goes back to the model once,
with what's wrong: a second call, with the same options and its own
step limit. A second failure returns an `*ai.OutputError`, which a
handler turns into a 502 response. An answer cut off at the token limit,
or a refusal, isn't retried.

### 4. Give the model tools

A tool is a Go function the model can call. Its input is a struct, with
the same tags; the model's input is checked before the function runs:

```go
// FindOrderInput is the find_order tool's input, as the model writes it.
type FindOrderInput struct {
	Number int `json:"number" description:"The order's number" validate:"required|min:1"`
}

// findOrder looks up an order of the current customer. It runs with the
// request's context: another customer's order is "not found", as it would
// be in the customer's own browser, and the model is told so.
var findOrder = ai.NewTool("find_order", "Look up one of the customer's orders by its number",
	func(ctx context.Context, in FindOrderInput) (Order, error) {
		i := slices.IndexFunc(orders, func(o Order) bool { return o.Number == in.Number && o.Customer == customer(ctx) })
		if i < 0 {
			return Order{}, web.Error(http.StatusNotFound, "The customer has no such order.")
		}
		return orders[i], nil
	})

// support answers customers' questions about their orders.
var support = ai.Agent{
	Name:         "support",
	Instructions: "You answer the customer's questions about their orders, briefly. Look orders up; never guess.",
	Tools:        []ai.Tool{findOrder},
	MaxSteps:     5,
}
```

(Copied from [`examples/ai`](../../../examples/ai), region `tool`.)

An `ai.Agent` bundles instructions, tools and options to reuse. Prompt it
from a handler:

```go
// Question is a customer's question; the header stands in for
// authentication.
type Question struct {
	Customer string `header:"X-Customer" validate:"required"`
	Question string `json:"question" validate:"required|max:500"`
}

// Answer is the agent's answer, with the tokens it took.
type Answer struct {
	Answer       string `json:"answer"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// Ask answers a question with the support agent.
func Ask(c *web.Ctx, in Question) (Answer, error) {
	ctx := context.WithValue(c, customerKey{}, in.Customer) // what the tool sees
	res, err := support.Prompt(ctx, in.Question)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Answer: res.Text(), InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens}, nil
}
```

(Copied from [`examples/ai`](../../../examples/ai), region `ask`.)

The model calls tools, they run, the model gets their results and goes
on, until it answers or reaches `MaxSteps` (10 by default; then
`ai.ErrMaxSteps`). `res.Usage` adds up every step.

Tools run with the context of the call, so they act as the current
user: in your app, `auth.Current`, your policies and permissions
(`rbac.Authorize`) apply inside them.
How a tool's error reaches the model:

| The tool returns | The model is told | The call |
|---|---|---|
| Invalid input (`validate` rules, a field of the wrong JSON type) | Each field's message, or which field has the wrong type | Goes on: the model can correct it |
| An error with a 4xx status (`web.Error`, `auth.ErrForbidden`, `db.ErrNotFound`) | What a web client would see: the status text and the error's message, never its internal cause | Goes on |
| Any other error | Nothing | Stops, and returns the error |

A panicking tool isn't recovered: the panic ends the call, as it would
a handler (the web layer answers 500). A tool's output goes to the model
whole, so return what it needs, not whole tables.

> **Warning:** Text the model reads can contain instructions (a
> customer's message, a web page, a document): "ignore your instructions
> and…". Don't rely on instructions for security. Give tools only what
> the user may do, check permissions inside them, and keep actions that
> can't be undone behind a confirmation the user makes, not the model.

### 5. Stream the answer

`Stream` yields the answer as it's written. Write each piece and flush:

```go
// StreamQuestion is a question in the query string.
type StreamQuestion struct {
	Customer string `header:"X-Customer" validate:"required"`
	Question string `query:"question" validate:"required|max:500"`
}

// AskStream answers a question as plain text, sent as the model writes
// it.
func AskStream(c *web.Ctx, in StreamQuestion) (web.Responder, error) {
	// A long answer outlasts HTTP_REQUEST_TIMEOUT and HTTP_WRITE_TIMEOUT:
	// lift both for this response. (Leaving the page still stops it.)
	ctx := context.WithValue(web.WithoutTimeout(c), customerKey{}, in.Customer)
	w := c.Writer()
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, err
	}
	for ev, err := range support.Stream(ctx, in.Question) {
		if err != nil {
			return nil, err // before any text, an error response; after, only logged
		}
		if ev.Kind != ai.EventText {
			continue
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		if _, err := io.WriteString(w, ev.Text); err != nil {
			return nil, err // the client left: breaking out stops the model
		}
		if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return nil, err
		}
	}
	return nil, nil
}
```

(Copied from [`examples/ai`](../../../examples/ai), region `stream`.)

Besides text, the stream has `ai.EventToolCall` and `ai.EventToolResult`
for each tool call, `ai.EventResponse` when a model response is complete,
and last `ai.EventDone`, whose `Result` holds the conversation and usage.
Leaving the loop early stops the generation. A request's timeout
(`AI_TIMEOUT`) counts the time your loop takes too, so a slow reader
makes the request last longer. A stream also outlasts the server's
limits: the example lifts `HTTP_REQUEST_TIMEOUT` for its request
(`web.WithoutTimeout`, keeping the cancellation when the client leaves)
and `HTTP_WRITE_TIMEOUT` for its response (`SetWriteDeadline`). For
browsers, `ai.SSE` sends the answer as server-sent events and does both:
see [Build an AI assistant](ai-assistant.md).

### 6. Test it

Tests don't call a model: `anetostest` sets `AI_PROVIDER=fake`, and
`anetostest.FakeAI` scripts the answers, one per request:

```go
func TestAsk(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(
		ai.FakeToolCall("find_order", FindOrderInput{Number: 1042}), // the model looks the order up
		ai.FakeText("Your kettle shipped yesterday."),               // then answers
	))
	app.WithHeader("X-Customer", "ada") // a logged-in customer, in a real app
	app.PostJSON("/questions", map[string]string{"question": "Where is order 1042?"}).
		AssertOK().AssertJSONPath("answer", "Your kettle shipped yesterday.")

	// The model got the tool's result, and the prompt.
	reqs := app.AI().Requests()
	result := reqs[1].Messages[2].Parts[0].(ai.ToolResult)
	if result.IsError || !strings.Contains(result.Content, `"status":"shipped yesterday"`) {
		t.Errorf("tool result: %+v", result)
	}
	app.AssertPrompted(func(r ai.Request) bool { return r.Prompt() == "Where is order 1042?" })
}
```

(Copied from [`examples/ai/main_test.go`](../../../examples/ai/main_test.go), region `test-ask`.)

The tool really runs: the fake only plays the model. `app.AI().Requests()`
shows what the model was sent: prompts, instructions, tools, tool
results. `ai.FakeObject(v)` answers `GenerateObject`, and `ai.FakeError`
makes a request fail.

## How it works

Each call builds a request (instructions, messages, tool definitions,
the output schema) and sends it to the provider through its driver.
Every request is logged with the provider, model, tokens and time, never
the prompt or the answer. Each tool call is an operation, like a
request or a job, so [N+1 detection](n-plus-one.md) covers it. See
[AI](../concepts/ai.md) for the design, and the
[AI reference](../reference/ai.md) for every option.

Providers differ, and the drivers smooth what they can:

- **Typed answers** use each provider's structured output. Each accepts
  a different part of JSON Schema: rules it doesn't take (lengths,
  ranges) are written into the fields' descriptions for the model, and
  the answer is validated either way. OpenAI's strict mode is used when
  the struct allows it (no maps, no `any` fields). Claude's structured
  output takes no maps or `any` fields at all (the call fails before
  sending), and at most 24 optional and 16 nullable fields.
- **Reasoning:** models that think (Claude with extended thinking,
  Gemini's thinking models) need their earlier reasoning back when they
  get tool results. It's kept in the conversation as `ai.Reasoning`
  parts, which marshal to JSON with the rest, and other providers
  leave out. Claude with extended thinking can't continue a tool call
  another provider made.
- **A provider's own features** go through `ai.ProviderOptions` with the
  driver's `Options`: `anthropic.Options{ThinkingBudget: 4096}`,
  `openai.Options{ReasoningEffort: "low"}`, `gemini.Options{ThinkingBudget: &budget}`;
  each also has a function to change the SDK's request parameters
  directly. `Response.Raw` holds the SDK's response.

## Common problems

| Problem | Cause | Fix |
|---|---|---|
| `ai: AI_PROVIDER isn't set` | No provider chosen | Set `AI_PROVIDER`, and pass its driver to `ai.New` |
| `ai: open the anthropic provider: ANTHROPIC_API_KEY isn't set` (or `AI_MODEL isn't set`) | The provider's key, or the model, is missing | Set it; the provider's names for its models are in its documentation |
| `openai: authenticated requests require HTTPS` | `OPENAI_COMPATIBLE_KEY` is set and `OPENAI_COMPATIBLE_URL` is plain HTTP to another machine | Use https, or no key; on your own machine, HTTP works |
| A local model's typed answers fail twice | Small models follow schemas poorly | Use a larger model, or simpler structs |
| `ai: no AI client in the context` | `ai.New` wasn't called, or the context isn't the app's | Call it in setup; use the request's or job's context |
| `ai: fake: no reply scripted for request N` in a test | The app made more requests than `FakeAI` scripted | Add replies (`FakeAI`, `app.AI().Add`); `app.AI().Requests()` shows them |
| `ai: the model's answer isn't a valid …` (502) | The answer broke the struct's shape or rules twice | Loosen the rules, describe fields better (`description` tags), or use a stronger model |
| `… was cut off at the token limit` | The answer reached `AI_MAX_TOKENS` | Raise it, or `ai.MaxTokens` on the call |
| `ai.ErrMaxSteps` | The model kept calling tools | Raise `MaxSteps`, or make tools return what the model needs in fewer calls |
| A panic: `ai: tool name …` or `isn't a struct` at startup | `ai.NewTool` checks its name and input type when it's made | Use letters, digits, `_` and `-`; make the input a struct (`struct{}` for none) |

## Next steps

- [Build an AI assistant](ai-assistant.md): stored conversations,
  answers streamed to the page, replies from queue jobs, usage budgets.
- [Search by meaning](semantic-search.md): embeddings, vector and hybrid
  search, and a search tool for agents.
- [AI concepts](../concepts/ai.md): what the package does, and doesn't.
- [AI reference](../reference/ai.md): options, events, errors, schemas.
- [Authorization](authorization.md) and [Roles and
  permissions](roles-and-permissions.md): what tools should check.

> **Coming from Laravel?** This covers what Prism (and Laravel's AI
> packages) do: text, structured output and tools, with a fake for
> tests. Tools are plain Go functions with typed inputs, and they run as
> the logged-in user.
