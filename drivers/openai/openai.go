// SPDX-License-Identifier: Apache-2.0

// Package openai is the OpenAI provider of package ai, on OpenAI's
// official Go SDK, and the provider for servers that speak OpenAI's Chat
// Completions API (Ollama, vLLM, OpenRouter, Groq, LM Studio…):
//
//	client, err := ai.ForApp(app, openai.Driver(), openai.CompatibleDriver())
//
// AI_PROVIDER=openai reads OPENAI_API_KEY (required) and OPENAI_BASE_URL;
// AI_PROVIDER=openai-compatible reads OPENAI_COMPATIBLE_URL (required)
// and OPENAI_COMPATIBLE_KEY. Both need AI_MODEL. Both use Chat
// Completions, which every compatible server has. Structured output uses
// strict JSON schemas where the schema allows (no maps, no values of any
// type); constraints go into the fields' descriptions, and the answer is
// validated as usual. [Options] sets the reasoning effort, and reaches
// every request parameter; Response.Raw is the SDK's
// *openai.ChatCompletion (for a stream, its []openai.ChatCompletionChunk),
// and [Provider.Client] the SDK client. Both make embeddings
// ([Provider.Embed]), with the Embeddings API: AI_EMBEDDING_MODEL names
// the model (text-embedding-3-small), and AI_EMBEDDING_PROVIDER=openai
// uses them with another provider's chat.
package openai

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/config"
)

// The providers' names: AI_PROVIDER=openai, AI_PROVIDER=openai-compatible.
const (
	Name           = "openai"
	CompatibleName = "openai-compatible"
)

// Config holds the OPENAI_* settings.
type Config struct {
	// APIKey is OpenAI's API key. OPENAI_API_KEY, required for
	// AI_PROVIDER=openai.
	APIKey anetos.Secret `env:"OPENAI_API_KEY"`
	// BaseURL replaces OpenAI's API URL (a proxy, a gateway).
	// OPENAI_BASE_URL.
	BaseURL string `env:"OPENAI_BASE_URL"`
	// CompatibleURL is the API URL of an OpenAI-compatible server, up to
	// /v1 (http://localhost:11434/v1 for Ollama). OPENAI_COMPATIBLE_URL,
	// required for AI_PROVIDER=openai-compatible.
	CompatibleURL string `env:"OPENAI_COMPATIBLE_URL"`
	// CompatibleKey is its API key, if it needs one.
	// OPENAI_COMPATIBLE_KEY.
	CompatibleKey anetos.Secret `env:"OPENAI_COMPATIBLE_KEY"`
}

// Driver is OpenAI's driver for ai.ForApp: AI_PROVIDER=openai.
func Driver() ai.Driver {
	return ai.Driver{Name: Name, Open: func(app *anetos.App, cfg ai.Config) (ai.Provider, error) {
		c, err := config.Get[Config](app.Source())
		if err != nil {
			return nil, err
		}
		if c.APIKey == "" {
			return nil, errors.New("OPENAI_API_KEY isn't set: the API key, from OpenAI's dashboard")
		}
		if cfg.Model == "" {
			return nil, errors.New("AI_MODEL isn't set: name the OpenAI model to use")
		}
		var opts []option.RequestOption
		if c.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(c.BaseURL))
		}
		return New(string(c.APIKey), opts...), nil
	}}
}

// CompatibleDriver is the driver of OpenAI-compatible servers for
// ai.ForApp: AI_PROVIDER=openai-compatible.
func CompatibleDriver() ai.Driver {
	return ai.Driver{Name: CompatibleName, Open: func(app *anetos.App, cfg ai.Config) (ai.Provider, error) {
		c, err := config.Get[Config](app.Source())
		if err != nil {
			return nil, err
		}
		if c.CompatibleURL == "" {
			return nil, errors.New("OPENAI_COMPATIBLE_URL isn't set: the server's API URL, such as http://localhost:11434/v1")
		}
		if cfg.Model == "" {
			return nil, errors.New("AI_MODEL isn't set: name the server's model to use")
		}
		return NewCompatible(c.CompatibleURL, string(c.CompatibleKey)), nil
	}}
}

// Provider calls a Chat Completions API. It is safe for concurrent use.
type Provider struct {
	client     sdk.Client
	name       string
	compatible bool
}

// New returns OpenAI's provider with apiKey; opts are the SDK's (a base
// URL, an HTTP client, retries: the SDK retries rate limits and server
// errors twice by default).
func New(apiKey string, opts ...option.RequestOption) *Provider {
	return &Provider{client: sdk.NewClient(append([]option.RequestOption{option.WithAPIKey(apiKey)}, opts...)...), name: Name}
}

// NewCompatible returns the provider of an OpenAI-compatible server at
// baseURL (up to /v1), with apiKey if it needs one: then baseURL must be
// https, or on this machine (the SDK refuses to send a key in plain
// HTTP elsewhere, and sends one to this machine through its own
// connections, not an HTTP client in opts). Nothing the SDK reads from
// the OPENAI_* environment variables, which are OpenAI's (the key, the
// organization and project, custom headers), is sent. It sends
// max_tokens, which those servers read, where OpenAI's provider sends
// max_completion_tokens.
func NewCompatible(baseURL, apiKey string, opts ...option.RequestOption) *Provider {
	base := []option.RequestOption{
		option.WithBaseURL(baseURL),
		option.WithHeaderDel("OpenAI-Organization"), option.WithHeaderDel("OpenAI-Project"),
	}
	for _, name := range customHeaders() {
		base = append(base, option.WithHeaderDel(name))
	}
	if apiKey == "" {
		base = append(base, option.WithAPIKey(""), option.WithHeaderDel("Authorization"))
	} else {
		base = append(base, option.WithAPIKey(apiKey))
		if u, err := url.Parse(baseURL); err == nil && u.Scheme == "http" && isLoopback(u.Hostname()) {
			base = append(base, option.WithUnsafeAllowHTTP())
		}
	}
	return &Provider{client: sdk.NewClient(append(base, opts...)...), name: CompatibleName, compatible: true}
}

// customHeaders returns the names of the headers the SDK adds from
// OPENAI_CUSTOM_HEADERS ("Name: value" lines), but the SDK's own.
func customHeaders() []string {
	var names []string
	for line := range strings.SplitSeq(os.Getenv("OPENAI_CUSTOM_HEADERS"), "\n") {
		name, _, ok := strings.Cut(line, ":")
		name = http.CanonicalHeaderKey(strings.TrimSpace(name))
		switch {
		case !ok || name == "", name == "Content-Type", name == "Accept", name == "User-Agent",
			name == "Idempotency-Key", strings.HasPrefix(name, "X-Stainless-"):
			continue
		}
		names = append(names, name)
	}
	return names
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Client returns the SDK client, for the API's other features.
func (p *Provider) Client() *sdk.Client { return &p.client }

// Name returns "openai" or "openai-compatible".
func (p *Provider) Name() string { return p.name }

// Options are the provider's own request options, for ai.ProviderOptions.
type Options struct {
	// ReasoningEffort, for reasoning models: "minimal", "low",
	// "medium", "high".
	ReasoningEffort string
	// Params, if set, changes the request's parameters last, for what
	// the common request doesn't have.
	Params func(*sdk.ChatCompletionNewParams)
}

// toolSchema is the dialect for tool inputs: all of JSON Schema.
var toolSchema = ai.SchemaOptions{Keywords: ai.ConstraintKeywords}

// strictable reports whether s fits strict mode: every value of a known
// type, and objects with fixed properties.
func strictable(s *ai.Schema) bool {
	if s == nil {
		return true
	}
	if s.Type == "" || s.AdditionalProperties != nil {
		return false
	}
	for _, p := range s.Properties {
		if !strictable(p.Schema) {
			return false
		}
	}
	return strictable(s.Items)
}

func (p *Provider) params(req *ai.Request) (sdk.ChatCompletionNewParams, error) {
	if req.Model == "" {
		return sdk.ChatCompletionNewParams{}, errors.New("no model: set AI_MODEL, or ai.Model")
	}
	params := sdk.ChatCompletionNewParams{Model: req.Model}
	if req.MaxTokens > 0 {
		if p.compatible {
			params.MaxTokens = sdk.Int(int64(req.MaxTokens))
		} else {
			params.MaxCompletionTokens = sdk.Int(int64(req.MaxTokens))
		}
	}
	if req.Temperature != nil {
		params.Temperature = sdk.Float(*req.Temperature)
	}
	if req.System != "" {
		params.Messages = append(params.Messages, sdk.SystemMessage(req.System))
	}
	msgs, err := messages(req.Messages)
	if err != nil {
		return params, err
	}
	params.Messages = append(params.Messages, msgs...)
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, sdk.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name: t.Name, Description: sdk.String(t.Description), Parameters: shared.FunctionParameters(t.Input.Map(toolSchema)),
		}))
	}
	if req.Output != nil {
		strict := strictable(req.Output.Schema)
		params.ResponseFormat = sdk.ChatCompletionNewParamsResponseFormatUnion{OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
			JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
				Name: req.Output.Name, Strict: sdk.Bool(strict), Schema: req.Output.Schema.Map(ai.SchemaOptions{AllRequired: strict}),
			},
		}}
	}
	var o *Options
	switch v := req.Options.(type) {
	case Options:
		o = &v
	case *Options:
		o = v
	}
	if o != nil {
		if o.ReasoningEffort != "" {
			params.ReasoningEffort = shared.ReasoningEffort(o.ReasoningEffort)
		}
		if o.Params != nil {
			o.Params(&params)
		}
	}
	return params, nil
}

// messages translates the conversation: each tool result is a tool
// message. The API has no reasoning to send back, nor errors in tool
// results: an error's content says it is one.
func messages(msgs []ai.Message) ([]sdk.ChatCompletionMessageParamUnion, error) {
	var out []sdk.ChatCompletionMessageParamUnion
	for _, m := range msgs {
		switch m.Role {
		case ai.RoleUser:
			for _, part := range m.Parts {
				if t, ok := part.(ai.Text); ok {
					out = append(out, sdk.UserMessage(string(t)))
				}
			}
		case ai.RoleAssistant:
			a := sdk.ChatCompletionAssistantMessageParam{}
			if text := m.Text(); text != "" {
				a.Content.OfString = sdk.String(text)
			}
			for _, c := range m.ToolCalls() {
				args := string(c.Input)
				if args == "" {
					args = "{}"
				}
				a.ToolCalls = append(a.ToolCalls, sdk.ChatCompletionMessageToolCallUnionParam{OfFunction: &sdk.ChatCompletionMessageFunctionToolCallParam{
					ID: c.ID, Function: sdk.ChatCompletionMessageFunctionToolCallFunctionParam{Name: c.Name, Arguments: args},
				}})
			}
			if !a.Content.OfString.Valid() && len(a.ToolCalls) == 0 {
				continue // nothing to send
			}
			out = append(out, sdk.ChatCompletionMessageParamUnion{OfAssistant: &a})
		case ai.RoleTool:
			for _, part := range m.Parts {
				if r, ok := part.(ai.ToolResult); ok {
					content := r.Content
					if r.IsError {
						content = "Error: " + content
					}
					out = append(out, sdk.ToolMessage(content, r.CallID))
				}
			}
		default:
			return nil, fmt.Errorf("unknown message role %q", m.Role)
		}
	}
	return out, nil
}

// stopOf translates a finish reason; hasCalls says the answer called
// tools (some servers finish such an answer with "stop").
func stopOf(reason string, hasCalls bool) ai.StopReason {
	switch {
	case hasCalls && (reason == "stop" || reason == "tool_calls" || reason == "function_call"):
		return ai.StopToolCalls
	case reason == "stop":
		return ai.StopEnd
	case reason == "length":
		return ai.StopMaxTokens
	case reason == "content_filter":
		return ai.StopRefusal
	}
	return ai.StopOther
}

func usageOf(u sdk.CompletionUsage) ai.Usage {
	return ai.Usage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, CacheReadTokens: u.PromptTokensDetails.CachedTokens}
}

// answer is a response's message parts, and whether the model refused.
func answer(content, refusal string, calls []ai.ToolCall) ([]ai.Part, bool) {
	var parts []ai.Part
	switch {
	case refusal != "":
		parts = append(parts, ai.Text(refusal))
	case content != "":
		parts = append(parts, ai.Text(content))
	}
	for _, c := range calls {
		parts = append(parts, c)
	}
	return parts, refusal != ""
}

func response(c *sdk.ChatCompletion) (*ai.Response, error) {
	if len(c.Choices) == 0 {
		return nil, errors.New("the response has no choices")
	}
	choice := c.Choices[0]
	var calls []ai.ToolCall
	for _, tc := range choice.Message.ToolCalls {
		if tc.Type == "function" || tc.Type == "" {
			calls = append(calls, ai.ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: arguments(tc.Function.Arguments)})
		}
	}
	parts, refused := answer(choice.Message.Content, choice.Message.Refusal, calls)
	stop := stopOf(choice.FinishReason, len(calls) > 0)
	if refused {
		stop = ai.StopRefusal
	}
	return &ai.Response{
		Message: ai.Message{Role: ai.RoleAssistant, Parts: parts},
		Stop:    stop,
		Usage:   usageOf(c.Usage),
		Model:   c.Model,
		Raw:     c,
	}, nil
}

// arguments is a tool call's input: "{}" for none.
func arguments(args string) json.RawMessage {
	if strings.TrimSpace(args) == "" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(args)
}

// Generate sends req and returns the model's answer.
func (p *Provider) Generate(ctx context.Context, req *ai.Request) (*ai.Response, error) {
	params, err := p.params(req)
	if err != nil {
		return nil, err
	}
	c, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}
	return response(c)
}

// Embed implements ai.Embedder with the Embeddings API, which OpenAI and
// most compatible servers (Ollama, vLLM, LM Studio) have: the vectors of
// the inputs, req.Dimensions long if set (OpenAI's text-embedding-3
// models shorten theirs). The purpose doesn't change OpenAI's
// embeddings.
func (p *Provider) Embed(ctx context.Context, req *ai.EmbedRequest) (*ai.EmbedResponse, error) {
	params := sdk.EmbeddingNewParams{
		Model:          req.Model,
		Input:          sdk.EmbeddingNewParamsInputUnion{OfArrayOfStrings: req.Inputs},
		EncodingFormat: sdk.EmbeddingNewParamsEncodingFormatFloat,
	}
	if req.Dimensions > 0 {
		params.Dimensions = sdk.Int(int64(req.Dimensions))
	}
	res, err := p.client.Embeddings.New(ctx, params)
	if err != nil {
		return nil, err
	}
	out := &ai.EmbedResponse{Vectors: make([]ai.Vector, len(req.Inputs)), Model: res.Model, Usage: ai.Usage{InputTokens: res.Usage.PromptTokens}}
	for _, e := range res.Data {
		if e.Index < 0 || int(e.Index) >= len(out.Vectors) || out.Vectors[e.Index] != nil {
			return nil, fmt.Errorf("openai: an embedding at index %d for %d inputs", e.Index, len(req.Inputs))
		}
		v := make(ai.Vector, len(e.Embedding))
		for i, x := range e.Embedding {
			v[i] = float32(x)
		}
		out.Vectors[e.Index] = v
	}
	if len(res.Data) != len(req.Inputs) {
		return nil, fmt.Errorf("openai: %d embeddings for %d inputs", len(res.Data), len(req.Inputs))
	}
	return out, nil
}

// streamCall is a tool call being streamed.
type streamCall struct {
	id, name string
	args     strings.Builder
}

// Stream sends req and yields the model's answer as it's written. Tool
// calls are yielded when the answer finishes, complete.
func (p *Provider) Stream(ctx context.Context, req *ai.Request) iter.Seq2[ai.Event, error] {
	return func(yield func(ai.Event, error) bool) {
		params, err := p.params(req)
		if err != nil {
			yield(ai.Event{}, err)
			return
		}
		params.StreamOptions = sdk.ChatCompletionStreamOptionsParam{IncludeUsage: sdk.Bool(true)}
		stream := p.client.Chat.Completions.NewStreaming(ctx, params)
		defer func() { _ = stream.Close() }()
		var (
			chunks           []sdk.ChatCompletionChunk
			content, refusal strings.Builder
			calls            []*streamCall
			current          = map[int64]*streamCall{} // by the deltas' index
			finish, model    string
			usage            ai.Usage
		)
		for stream.Next() {
			chunk := stream.Current()
			chunks = append(chunks, chunk)
			model = cmp.Or(chunk.Model, model)
			if chunk.Usage.TotalTokens > 0 || chunk.Usage.PromptTokens > 0 {
				usage = usageOf(chunk.Usage)
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			ch := chunk.Choices[0]
			if ch.FinishReason != "" {
				finish = ch.FinishReason
			}
			refusal.WriteString(ch.Delta.Refusal)
			if d := ch.Delta.Content; d != "" {
				content.WriteString(d)
				if !yield(ai.Event{Kind: ai.EventText, Text: d}, nil) {
					return
				}
			}
			for _, tc := range ch.Delta.ToolCalls {
				c := current[tc.Index]
				// A new ID at an index is a new call: some servers leave
				// the index at 0.
				if c == nil || (tc.ID != "" && c.id != "" && tc.ID != c.id) {
					c = &streamCall{}
					calls = append(calls, c)
					current[tc.Index] = c
				}
				c.id = cmp.Or(c.id, tc.ID)
				c.name = cmp.Or(c.name, tc.Function.Name) // some servers repeat it
				c.args.WriteString(tc.Function.Arguments)
			}
		}
		if err := stream.Err(); err != nil {
			yield(ai.Event{}, err)
			return
		}
		var tcs []ai.ToolCall
		for _, c := range calls {
			tc := ai.ToolCall{ID: c.id, Name: c.name, Input: arguments(c.args.String())}
			tcs = append(tcs, tc)
			if !yield(ai.Event{Kind: ai.EventToolCall, ToolCall: &tc}, nil) {
				return
			}
		}
		parts, refused := answer(content.String(), refusal.String(), tcs)
		stop := stopOf(finish, len(tcs) > 0)
		if refused {
			stop = ai.StopRefusal
		}
		yield(ai.Event{Kind: ai.EventResponse, Response: &ai.Response{
			Message: ai.Message{Role: ai.RoleAssistant, Parts: parts},
			Stop:    stop, Usage: usage, Model: model, Raw: chunks,
		}}, nil)
	}
}
