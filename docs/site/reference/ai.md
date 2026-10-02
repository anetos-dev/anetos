---
title: AI reference
since: v0.3.0
---

# AI reference

The API of package `ai`. See [Add AI to your app](../guides/ai.md) for a
walkthrough, [AI](../concepts/ai.md) for the design, and
[Configuration](configuration.md#ai) for the `AI_*` settings.

## Setup

| API | Does | Since |
|---|---|---|
| `ai.ForApp(app, drivers...)` | `*ai.Client` from the `AI_*` settings, with the driver `AI_PROVIDER` names (`fake` is built in), in every context the app creates. Each tool call becomes a unit of work (`anetos.Unit{Kind: "tool"}`) | v0.3 |
| `ai.New(provider, defaults...)` | A client by hand; `defaults` (options) apply before each call's own. Logs to `slog.Default()` | v0.3 |
| `ai.WithClient(ctx, c)`, `ai.From(ctx)` | Put a client in a context; get it (`ai.ErrNoClient` if absent) | v0.3 |
| `c.Provider()` | The client's `ai.Provider` | v0.3 |
| `c.Fake(replies...)` | Replace the provider with an `*ai.Fake` (or add replies to it); for tests | v0.3 |
| `ai.Driver{Name, Open}` | A provider's driver: `Open(app, cfg) (ai.Provider, error)` reads its own settings; a provider that is an `io.Closer` is closed at shutdown | v0.3 |

## Calls

| API | Returns | Since |
|---|---|---|
| `ai.Generate(ctx, prompt, opts...)` | `*ai.Result`: the answer after any tool calls | v0.3 |
| `ai.GenerateObject[T](ctx, prompt, opts...)` | `T, *ai.Result`: the answer decoded into T (a struct) and checked with its `validate` tags; one retry with the problems, then `*ai.OutputError` | v0.3 |
| `ai.Stream(ctx, prompt, opts...)` | `iter.Seq2[ai.Event, error]`: the call's events; breaking out stops it | v0.3 |
| `agent.Prompt(ctx, prompt, opts...)`, `agent.Stream(…)` | `Generate` and `Stream` with the agent's settings | v0.3 |

The prompt may be empty when `ai.Messages` gives the conversation.

## Options

Applied in order: the client's defaults, an agent's, the call's.

| Option | Does | Default |
|---|---|---|
| `ai.System(text)` | Adds system instructions; several are joined by blank lines, in the order the options come (an agent's `Instructions` where the agent is) | none |
| `ai.Model(name)` | The model, by the provider's name for it | `AI_MODEL` |
| `ai.MaxTokens(n)` | The longest answer, in tokens | `AI_MAX_TOKENS` |
| `ai.Temperature(t)` | Sampling temperature | the provider's |
| `ai.Timeout(d)` | Each request's timeout (in a stream, including the reader's time) | `AI_TIMEOUT` |
| `ai.Messages(msgs...)` | Adds the conversation before the prompt (a `Result`'s `Messages`) | none |
| `ai.Tools(tools...)` | Adds tools | none |
| `ai.MaxSteps(n)` | The most requests in a call with tools; then `ai.ErrMaxSteps`. `GenerateObject`'s retry is a second call, with its own limit | `ai.DefaultMaxSteps` (10) |
| `ai.ProviderOptions(v)` | The driver's own request options (a type it defines) | none |
| `ai.ForUser(userID)` | The user the call is for: its usage records and budget (`TrackUsage`) | the signed-in user, if any; a conversation's user |
| `ai.Using(c)` | Use client c, not the context's | the context's |
| an `ai.Agent` | Its settings | |

## `ai.Agent`

| Field | Type | Does |
|---|---|---|
| `Name` | `string` | Names it in logs (`agent=`) |
| `Instructions` | `string` | Its system instructions |
| `Tools` | `[]ai.Tool` | Its tools |
| `Model` | `string` | Its model; empty: the default |
| `MaxSteps` | `int` | 0: `ai.DefaultMaxSteps` |
| `Options` | `[]ai.Option` | More options, before each call's |

## Results

| Type | Fields and methods |
|---|---|
| `ai.Result` | `Messages` (the whole conversation: given messages, prompt, answers, tool results; after an error it can end with tool calls without results, which providers refuse to continue from), `Steps` (`[]*ai.Response`), `Usage` (the total); `Text()` (the last response's), `Response()` (the last) |
| `ai.Response` | `Message`, `Stop`, `Usage`, `Model` (that answered), `Raw` (the provider's own response); `Text()`, `ToolCalls()` |
| `ai.Usage` | `InputTokens` (including cached), `OutputTokens`, `CacheReadTokens`, `CacheWriteTokens`; `Add(u)` |
| `ai.Message` | `Role` (`ai.RoleUser`, `RoleAssistant`, `RoleTool`), `Parts` (`ai.Text`, `ai.ToolCall{ID, Name, Input}`, `ai.ToolResult{CallID, Name, Content, IsError}`, `ai.Reasoning{Provider, Text, Data}`: a model's reasoning its provider needs back, before the part it belongs to; other providers leave it out); `Text()` (the text parts only), `ToolCalls()`; JSON `{"role", "parts": [{"type": "text" \| "tool_call" \| "tool_result" \| "reasoning", …}]}`. `ai.UserMessage(text)`, `ai.AssistantMessage(text)` |

| `Stop` | Means |
|---|---|
| `ai.StopEnd` | The answer is complete |
| `ai.StopToolCalls` | It called tools |
| `ai.StopMaxTokens` | Cut off at `MaxTokens` (not an error for `Generate`; an `OutputError` for `GenerateObject`) |
| `ai.StopRefusal` | The model declined (an `OutputError` for `GenerateObject`, not retried) |
| `ai.StopOther` | A provider-specific reason (see `Raw`) |

## Stream events

| `Kind` | Field | When |
|---|---|---|
| `ai.EventText` | `Text` | A piece of the answer |
| `ai.EventToolCall` | `ToolCall` | The model called a tool |
| `ai.EventToolResult` | `ToolResult` | The tool's result |
| `ai.EventResponse` | `Response` | A model response is complete (one per step) |
| `ai.EventDone` | `Result` | Last, after a successful call |

An error is yielded once, and ends the stream.

## Tools

| API | Does |
|---|---|
| `ai.Func(name, description, fn)` | A tool running `fn func(ctx, In) (Out, error)`. In is a struct: its schema is the tool's input; the model's input is decoded (`*ai.InputError` if it doesn't fit) and validated before fn runs. Out is sent as JSON (a string type as is). Panics on a bad name (1–64 letters, digits, `_`, `-`), a non-struct In or bad tags. A panic in fn isn't recovered |
| `ai.Tool` | The interface: `Definition() ai.ToolSpec{Name, Description, Input}`, `Call(ctx, json.RawMessage) (string, error)` |

| A tool's error | The model gets | The call |
|---|---|---|
| A 4xx status (`web.StatusCoder`): `*ai.InputError`, `web.Error`, `auth.ErrForbidden`, `db.ErrNotFound`… | `IsError` result, what a web client would see: the status text; the message and fields of the `HTTPError` that has the status, or which input field has the wrong JSON type; else the field messages of a validation error in the chain | Continues |
| Any other | Nothing | Stops; returns `ai: tool <name>: <error>` |
| A name no tool has | `IsError`: "There is no tool named …" | Continues |

## Schemas

`ai.SchemaFor[T]()` builds `*ai.Schema` (JSON Schema, properties in field
order) from struct T, once per type:

| Go | JSON Schema |
|---|---|
| struct | `object` of its exported fields by `json` name, resolved as encoding/json does (`json:"-"` skips; embedded structs flatten, a shallower or tagged field hides others of its name, ambiguous ones are left out); `additionalProperties: false` |
| `string`, `bool` | `string`, `boolean` |
| integers, floats | `integer` (unsigned: `minimum: 0`), `number`; with `json:",string"`, `string` |
| slice, array | `array` of the element; `[]byte`: `string` |
| `map[K]V` (string, integer or text keys) | `object` with `additionalProperties` V |
| pointer | the element's type, or `null` |
| `time.Time` | `string`, `date-time`; other `encoding.TextMarshaler`s: `string` (their rules aren't in the schema) |
| `json.Number` | `number` |
| interface, `json.RawMessage` | any value |
| a type with its own `MarshalJSON` or `UnmarshalJSON` | an error: its shape can't be known |

| Tag | Adds |
|---|---|
| `description:"…"` | `description` |
| `validate:"required"` | the field to `required`, not `null`, and at least one character, item or key |
| `min`, `max`, `size`, `between` | `minLength`/`maxLength` (strings), `minimum`/`maximum` (numbers), `minItems`/`maxItems` (slices), `minProperties`/`maxProperties` (maps) |
| `in:a,b` | `enum` (of each element, on slices) |
| `email`, `url`, `uuid`, `date`, `datetime`, `ipv4`, `ipv6` | `format` (`email`, `uri`, `uuid`, `date`, `date-time`, `ipv4`, `ipv6`) |

Other rules aren't in the schema, but are checked on the answer.
Recursive types are an error.

## Errors

| Error | When | Web status |
|---|---|---|
| `ai.ErrNoClient` | No client in the context and no `ai.Using` | 500 |
| `ai.ErrMaxSteps` | The model still called tools at the last step | 500 |
| `*ai.BudgetError` (`UserID`, `RetryAfter`) | The call's user spent their budget; wrapped in a `*web.HTTPError` whose message tells them when to try again | 429 |
| `ai.ErrConversationChanged` | Another call added to the conversation during this one; nothing was stored | 409 |
| `*ai.OutputError` (`Type`, `Text`, `Stop`, `Err`) | `GenerateObject`'s answer was invalid twice, cut off, or refused; `Err` is a `*validate.Errors` for broken rules | 502 |
| `ai: <provider>: <error>` | The provider failed (wraps its error) | 500; 503 on a timeout |

## Conversations

| API | Does | Since |
|---|---|---|
| `ai.Migrations()` | The `ai_conversations`, `ai_messages` and `ai_usage` tables, for `migrate.ForApp` | v0.3 |
| `ai.StartConversation(ctx, userID, title)` | Stores a new `*ai.Conversation` of the user (an `AuthID`, up to 100 bytes) | v0.3 |
| `ai.FindConversation(ctx, userID, id)` | The user's conversation, else `db.ErrNotFound` (404) | v0.3 |
| `ai.Conversations(ctx, userID)` | The user's conversations, the latest changed first, without messages | v0.3 |
| `conv.Messages(ctx)` | Its messages, oldest first | v0.3 |
| `conv.Add(ctx, msgs...)` | Stores messages at the end, without calling a model (the user's question) | v0.3 |
| `conv.Prompt(ctx, prompt, opts...)`, `conv.Stream(…)` | `Generate` and `Stream` after its messages; the prompt and answers are stored if the call succeeds (before `EventDone`) | v0.3 |
| `conv.Reply(ctx, opts...)`, `conv.StreamReply(…)` | Answer its last message (the user's); stored the same way | v0.3 |
| `conv.QueueReply(ctx, agent)` | Answers its last message from a queue job, with the agent registered under `agent.Name`, as its user (`auth.ActAs`, with the abilities of the API token ctx was signed in with, if any); `Status` is `ai.StatusQueued`, then `""`, or `ai.StatusFailed` with `Error`. A retry runs the tools again; a job that finds the conversation changed does nothing. With the sync queue driver, it runs in the calling request, after its commit | v0.3 |
| `conv.Delete(ctx)` | Deletes it and its messages (its usage records stay) | v0.3 |
| `ai.QueueAgents(app, agents...)` | Registers the `ai.reply` job type on the app's queue, with the agents queued replies may use (by `Name`); needs `queue.ForApp` first. Jobs time out after `AI_QUEUE_TIMEOUT` | v0.3 |

`ai.Conversation` fields: `ID`, `UserID`, `Title` (cut at 255 bytes), `Status`, `Error`,
`CreatedAt`, `UpdatedAt`. Calls on a conversation are for its user
(`ForUser`), and their usage records name it; `ai.Messages` options are
ignored.

## Streaming to the browser

| API | Does | Since |
|---|---|---|
| `ai.SSE(c, events)` | Writes a stream (`ai.Stream`, `conv.StreamReply`…) as server-sent events, through `c.Events()` (no request or write timeout): `text` (a piece, HTML-escaped), `tool` (a tool's name), `error` (a 4xx error's message, else a general one; others are logged), then `done`, always last; a comment every 15 seconds keeps proxies from closing a quiet stream | v0.3 |

## Usage and budgets

| API | Does | Since |
|---|---|---|
| `c.TrackUsage(ai.UsageConfig{Prices, Budget})` | Records each response in `ai_usage` and enforces budgets; call at startup. Budgets need the app's cache. Records are written with the call's context: inside a transaction that rolls back they go with it, so don't call models inside transactions | v0.3 |
| `ai.Price{Input, Output, CacheRead, CacheWrite}` | Per million tokens; cache prices of 0 charge `Input`. `p.Cost(usage)` | v0.3 |
| `UsageConfig.Prices` | `map[string]ai.Price` by model name: the response's model, else the requested one | v0.3 |
| `UsageConfig.Budget` | `func(ctx, userID) (ai.Budget, error)`, once per call with a user | v0.3 |
| `ai.Budget{Tokens, Cost, Per}` | Input and output tokens, or cost, per period (aligned to the clock: a day starts at midnight UTC); 0 for no limit. Checked before each request; a response can go past it. Changing a user's limit starts their count for the period over | v0.3 |
| `ai.UsageRecord` | A row of `ai_usage`: `UserID`, `ConversationID`, `Agent`, `Provider`, `Model`, the four token counts, `Cost`, `Estimated`, `CreatedAt`. A stream the reader left partway, or a request cut off by a timeout or a cancellation, is recorded and counted too, estimated at about four bytes a token for its input and the output it yielded (`Estimated`) | v0.3 |
| `ai.TotalUsage(ctx, userID, since)` | `ai.UsageTotal{Usage, Cost, Responses}` since a time | v0.3 |

## Providers

| Package | `AI_PROVIDER` | Constructor | `Options` (for `ai.ProviderOptions`) | `Response.Raw` |
|---|---|---|---|---|
| `drivers/anthropic` | `anthropic` | `anthropic.Driver()`, `anthropic.New(key, opts...)` | `ThinkingBudget` (extended thinking), `Params func(*anthropic.MessageNewParams)` | `*anthropic.Message` |
| `drivers/openai` | `openai` | `openai.Driver()`, `openai.New(key, opts...)` | `ReasoningEffort`, `Params func(*openai.ChatCompletionNewParams)` | `*openai.ChatCompletion` (streams: `[]openai.ChatCompletionChunk`) |
| `drivers/openai` | `openai-compatible` | `openai.CompatibleDriver()`, `openai.NewCompatible(url, key, opts...)` | the same | the same |
| `drivers/gemini` | `gemini` | `gemini.Driver()`, `gemini.New(ctx, genai.ClientConfig{APIKey: key, …})` | `ThinkingBudget *int32`, `Config func(*genai.GenerateContentConfig)` | `*genai.GenerateContentResponse` (streams: a slice of them) |

Each provider's `Client()` returns its SDK client. All use their API's
chat endpoint (OpenAI's: Chat Completions, which compatible servers
have) and the provider's structured output; the compatible provider
sends `max_tokens`, OpenAI's `max_completion_tokens`. Rate limits and
server errors are retried twice (by the Anthropic and OpenAI SDKs; the
Gemini driver turns the SDK's retries on). The Anthropic and compatible
providers send nothing from the SDKs' environment variables; OpenAI's
reads `OPENAI_ORG_ID`, `OPENAI_PROJECT_ID` and the like, which are
yours.

| Provider | JSON Schema it takes for structured output | Other rules |
|---|---|---|
| Anthropic (no maps or `any` fields: refused before sending; at most 24 optional fields and 16 nullable or union ones) | types, `enum`, `format`; nullable as `anyOf` | In the description |
| OpenAI (strict mode, when no maps or `any` fields) | types, `enum`; every property required, optional ones nullable | In the description |
| Gemini | types, `enum`, `format` (date-time, date, time), `minimum`, `maximum`, `minItems`, `maxItems`; nullable as `anyOf` | In the description |

Tool inputs get all of JSON Schema, except on Gemini (its dialect, as
above).

## Writing a provider

| API | Does |
|---|---|
| `ai.Provider` | `Name()`, `Generate(ctx, *ai.Request) (*ai.Response, error)`, `Stream(ctx, *ai.Request) iter.Seq2[ai.Event, error]` (`EventText`s and `EventToolCall`s, then one `EventResponse` with the whole response) |
| `schema.Map(ai.SchemaOptions{Keywords, Formats, AllRequired, NullableAnyOf})` | The schema as a `map[string]any` in the provider's dialect: the constraint keywords it takes (of `ai.ConstraintKeywords`) and the string formats (all, if `Formats` is empty), the others in words in the description; every property required (optional ones nullable); nullable as `anyOf`. Properties keep their order when marshaled (a provider may still reorder them: Anthropic puts required ones first) |
| `aitest.Run(t, aitest.Config{Name, Model, KeyEnv, New, Dir, Skip})` | The conformance suite (package `ai/aitest`): text, streams, a conversation, tool calls and their results (streamed too), structured output, the token limit, errors. It replays recorded HTTP exchanges (`testdata/aitest/<Test>.json`) and checks the requests match; `ANETOS_AI_RECORD=1` records them from the provider (its key in `KeyEnv`; headers are never saved), `ANETOS_AI_LIVE=1` calls it without recording, `ANETOS_AI_UPDATE_REQUESTS=1` rewrites the recorded requests. `ANETOS_TEST_<NAME>_MODEL` picks the model to record with |
| `ai.Request` | `Model` (the drivers refuse an empty one), `System`, `Messages`, `Tools` (`[]ai.ToolSpec`), `Output` (`*ai.OutputSpec{Name, Schema}`: structured output), `MaxTokens`, `Temperature` (`*float64`), `Options` (the driver's own type, or ignore); `Prompt()` |
| `ai.Fake`, `ai.NewFake(replies...)` | The scripted provider, safe for concurrent use: `Add`, `Requests`, `Remaining`; replies `ai.FakeText`, `FakeObject`, `FakeToolCall`, `FakeError`, or an `ai.FakeReply` function |
| `ai.FakeDriver()` | `AI_PROVIDER=fake` |
