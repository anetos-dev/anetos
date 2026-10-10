// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"iter"
	"slices"
)

// Provider is a model provider's API: what a driver module implements,
// and the [Fake]. The package builds every [Request]; a provider
// translates it, calls the API, and translates the answer back.
// Retrying rate limits and server errors is the provider's.
type Provider interface {
	// Name is the provider's, for logs and errors: "anthropic", "fake".
	Name() string
	// Generate sends req and returns the model's complete answer.
	Generate(ctx context.Context, req *Request) (*Response, error)
	// Stream sends req and yields the answer as it's written: EventText
	// for each piece of text, EventToolCall for each complete tool call,
	// and last, one EventResponse with the complete answer, the same
	// Generate would return. An error ends the stream.
	Stream(ctx context.Context, req *Request) iter.Seq2[Event, error]
}

// Request is one call to a model. Providers must not modify it.
type Request struct {
	// Model is the model's name. The provider drivers refuse an empty
	// one; the Fake accepts it.
	Model string
	// System is the system instructions, empty for none.
	System string
	// Messages is the conversation so far, oldest first; the last is the
	// user's prompt or tool results.
	Messages []Message
	// Tools are the tools the model may call.
	Tools []ToolSpec
	// Output, when set, asks for a JSON object matching its schema
	// (the provider's structured output feature), instead of free text.
	Output *OutputSpec
	// MaxTokens bounds the answer's length, in tokens.
	MaxTokens int
	// Temperature, when set, is the sampling temperature.
	Temperature *float64
	// Options are providers' own request options ([ProviderOptions]):
	// values of types the drivers define, for features the common
	// request doesn't have, in the order given. A provider uses its own
	// types' values and ignores the others, so one call can carry the
	// options of every provider the app may be configured with.
	Options []any
}

// Prompt returns the text of the request's last user message.
func (r *Request) Prompt() string {
	for _, m := range slices.Backward(r.Messages) {
		if m.Role == RoleUser {
			return m.Text()
		}
	}
	return ""
}

// OutputSpec is the structured output a [Request] asks for.
type OutputSpec struct {
	// Name names the output ("summary"), for providers that need one:
	// letters, digits, _ and -.
	Name string
	// Schema is the JSON object's schema.
	Schema *Schema
}

// Response is a model's answer to one [Request].
type Response struct {
	// Message is the answer: text and tool calls, role assistant.
	Message Message
	// Stop says why the model stopped.
	Stop StopReason
	// Usage is what the request used.
	Usage Usage
	// Model is the model that answered, as the provider names it.
	Model string
	// Raw is the provider's own response value (a driver documents its
	// type), for what the common response doesn't have.
	Raw any
}

// Text returns the answer's text.
func (r *Response) Text() string { return r.Message.Text() }

// ToolCalls returns the tool calls in the answer.
func (r *Response) ToolCalls() []ToolCall { return r.Message.ToolCalls() }

// StopReason says why a model stopped.
type StopReason string

// The reasons a model stops.
const (
	StopEnd       StopReason = "end"        // the answer is complete
	StopToolCalls StopReason = "tool_calls" // it called tools, and waits for their results
	StopMaxTokens StopReason = "max_tokens" // the answer reached MaxTokens: it's cut off
	StopRefusal   StopReason = "refusal"    // it declined to answer (safety)
	StopOther     StopReason = "other"      // a provider-specific reason; see Raw
)

// Usage counts the tokens of one or more requests.
type Usage struct {
	// InputTokens counts the requests' input: instructions, messages,
	// tool definitions, including the tokens read from or written to
	// the provider's prompt cache.
	InputTokens int64
	// OutputTokens counts the answers.
	OutputTokens int64
	// CacheReadTokens is the part of InputTokens read from the
	// provider's prompt cache (usually cheaper).
	CacheReadTokens int64
	// CacheWriteTokens is the part of InputTokens written to it.
	CacheWriteTokens int64
}

// Add adds o's counts to u's.
func (u *Usage) Add(o Usage) {
	u.InputTokens += o.InputTokens
	u.OutputTokens += o.OutputTokens
	u.CacheReadTokens += o.CacheReadTokens
	u.CacheWriteTokens += o.CacheWriteTokens
}

// EventKind says what an [Event] carries.
type EventKind int

// The kinds of events. Providers yield EventText, EventToolCall and
// EventResponse; [Stream] adds EventToolResult and EventDone.
const (
	// EventText is a piece of the answer's text, in Event.Text.
	EventText EventKind = iota + 1
	// EventToolCall is a complete tool call, in Event.ToolCall.
	EventToolCall
	// EventToolResult is a tool call's result, in Event.ToolResult.
	EventToolResult
	// EventResponse is a model's complete answer, in Event.Response
	// (one per step).
	EventResponse
	// EventDone ends a successful [Stream], with Event.Result.
	EventDone
)

// String returns the kind's name: "text", "tool_call"…
func (k EventKind) String() string {
	switch k {
	case EventText:
		return "text"
	case EventToolCall:
		return "tool_call"
	case EventToolResult:
		return "tool_result"
	case EventResponse:
		return "response"
	case EventDone:
		return "done"
	}
	return "unknown"
}

// Event is one step of a streamed answer; Kind says which field is set.
type Event struct {
	// Kind says what the event carries.
	Kind EventKind
	// Text is a piece of text (EventText).
	Text string
	// ToolCall is a complete tool call (EventToolCall).
	ToolCall *ToolCall
	// ToolResult is a tool call's result (EventToolResult).
	ToolResult *ToolResult
	// Response is a complete model response (EventResponse).
	Response *Response
	// Result is the call's result (EventDone).
	Result *Result
}
