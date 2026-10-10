// SPDX-License-Identifier: Apache-2.0

// Package anthropic is the Anthropic provider of package ai (Claude
// models), on Anthropic's official Go SDK:
//
//	client, err := ai.New(app, anthropic.Driver()) // AI_PROVIDER=anthropic
//
// It reads ANTHROPIC_API_KEY (required) and ANTHROPIC_BASE_URL, and
// needs AI_MODEL. Structured output uses Claude's structured outputs;
// constraints its JSON Schema doesn't take (lengths, ranges) go into
// the fields' descriptions, and the answer is validated as usual.
// [Options] turns on extended thinking, and reaches every request
// parameter; Response.Raw is the SDK's *anthropic.Message, and
// [Provider.Client] the SDK client.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/config"
)

// Name is the provider's name: AI_PROVIDER=anthropic.
const Name = "anthropic"

// Config holds the ANTHROPIC_* settings.
type Config struct {
	// APIKey is the API key. ANTHROPIC_API_KEY, required.
	APIKey anetos.Secret `env:"ANTHROPIC_API_KEY"`
	// BaseURL replaces the API's URL (a proxy, a gateway).
	// ANTHROPIC_BASE_URL.
	BaseURL string `env:"ANTHROPIC_BASE_URL"`
}

// Driver is the provider's driver for ai.New: AI_PROVIDER=anthropic.
func Driver() ai.Driver {
	return ai.Driver{Name: Name, Open: func(app *anetos.App, cfg ai.Config) (ai.Provider, error) {
		c, err := config.Get[Config](app.Source())
		if err != nil {
			return nil, err
		}
		if c.APIKey == "" {
			return nil, errors.New("ANTHROPIC_API_KEY isn't set: the API key, from the Anthropic Console")
		}
		if cfg.Model == "" {
			return nil, errors.New("AI_MODEL isn't set: name the Claude model to use")
		}
		var opts []option.RequestOption
		if c.BaseURL != "" {
			opts = append(opts, option.WithBaseURL(c.BaseURL))
		}
		return New(string(c.APIKey), opts...), nil
	}}
}

// Provider calls Anthropic's Messages API. It is safe for concurrent
// use.
type Provider struct {
	client sdk.Client
}

// New returns a provider with apiKey; opts are the SDK's (a base URL,
// an HTTP client, retries: the SDK retries rate limits and server
// errors twice by default). The SDK's own environment variables
// (ANTHROPIC_AUTH_TOKEN, profiles, custom headers) aren't read: the
// provider sends apiKey and nothing else of yours.
func New(apiKey string, opts ...option.RequestOption) *Provider {
	base := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(apiKey)}
	return &Provider{client: sdk.NewClient(append(base, opts...)...)}
}

// Client returns the SDK client, for the API's other features.
func (p *Provider) Client() *sdk.Client { return &p.client }

// Name returns "anthropic".
func (p *Provider) Name() string { return Name }

// Options are the provider's own request options, for ai.ProviderOptions.
type Options struct {
	// ThinkingBudget, if positive, turns on extended thinking with that
	// many tokens (at least 1024, and less than the request's
	// MaxTokens; Claude doesn't take a temperature with it). The
	// thinking is kept in the conversation as ai.Reasoning, which Claude
	// needs to continue after tool calls. Models with adaptive thinking
	// take it through Params instead.
	ThinkingBudget int64
	// Params, if set, changes the request's parameters last, for what
	// the common request doesn't have.
	Params func(*sdk.MessageNewParams)
}

// defaultMaxTokens is the answer's length when a request doesn't set
// one: the API requires it.
const defaultMaxTokens = 4096

// redacted marks the Data of an ai.Reasoning that is a redacted
// thinking block's.
const redacted = "redacted:"

// outputSchema is Claude's dialect of JSON Schema for structured output.
var outputSchema = ai.SchemaOptions{Keywords: []string{"format"}, NullableAnyOf: true}

// toolSchema is the dialect for tool inputs: all of JSON Schema.
var toolSchema = ai.SchemaOptions{Keywords: ai.ConstraintKeywords}

func (p *Provider) params(req *ai.Request) (sdk.MessageNewParams, error) {
	if req.Model == "" {
		return sdk.MessageNewParams{}, errors.New("no model: set AI_MODEL, or ai.Model")
	}
	params := sdk.MessageNewParams{Model: req.Model, MaxTokens: int64(req.MaxTokens)}
	if params.MaxTokens <= 0 {
		params.MaxTokens = defaultMaxTokens
	}
	if req.System != "" {
		params.System = []sdk.TextBlockParam{{Text: req.System}}
	}
	if req.Temperature != nil {
		params.Temperature = sdk.Float(*req.Temperature)
	}
	msgs, err := messages(req.Messages)
	if err != nil {
		return params, err
	}
	params.Messages = msgs
	for _, t := range req.Tools {
		params.Tools = append(params.Tools, sdk.ToolUnionParam{OfTool: &sdk.ToolParam{
			Name: t.Name, Description: sdk.String(t.Description), InputSchema: inputSchema(t.Input),
		}})
	}
	if req.Output != nil {
		if err := checkOutput(req.Output.Schema, req.Output.Name); err != nil {
			return params, err
		}
		params.OutputConfig = sdk.OutputConfigParam{Format: sdk.JSONOutputFormatParam{Schema: req.Output.Schema.Map(outputSchema)}}
	}
	var o *Options
	switch v := req.Options.(type) {
	case Options:
		o = &v
	case *Options:
		o = v
	}
	if o != nil {
		if o.ThinkingBudget > 0 {
			params.Thinking = sdk.ThinkingConfigParamOfEnabled(o.ThinkingBudget)
		}
		if o.Params != nil {
			o.Params(&params)
		}
	}
	return params, nil
}

// checkOutput refuses what Claude's structured output can't take: maps
// (objects must list their properties) and values of any type.
func checkOutput(s *ai.Schema, path string) error {
	switch {
	case s == nil:
		return nil
	case s.Type == "":
		return fmt.Errorf("structured output: %s can be any value, which Claude's structured output can't express: give it a type", path)
	case s.AdditionalProperties != nil:
		return fmt.Errorf("structured output: %s is a map, which Claude's structured output can't express: use a struct, or a list of key-value structs", path)
	}
	for _, p := range s.Properties {
		if err := checkOutput(p.Schema, path+"."+p.Name); err != nil {
			return err
		}
	}
	return checkOutput(s.Items, path+"[]")
}

// inputSchema is a tool's input schema in the SDK's form.
func inputSchema(s *ai.Schema) sdk.ToolInputSchemaParam {
	m := s.Map(toolSchema)
	out := sdk.ToolInputSchemaParam{Properties: m["properties"], ExtraFields: map[string]any{}}
	if r, ok := m["required"].([]string); ok {
		out.Required = r
	}
	for k, v := range m {
		if k != "type" && k != "properties" && k != "required" {
			out.ExtraFields[k] = v
		}
	}
	return out
}

// messages translates the conversation: tool results are user messages,
// and consecutive messages of one role are merged.
func messages(msgs []ai.Message) ([]sdk.MessageParam, error) {
	var out []sdk.MessageParam
	add := func(role sdk.MessageParamRole, blocks []sdk.ContentBlockParamUnion) {
		if len(blocks) == 0 {
			return
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content = append(out[n-1].Content, blocks...)
			return
		}
		out = append(out, sdk.MessageParam{Role: role, Content: blocks})
	}
	for _, m := range msgs {
		var blocks []sdk.ContentBlockParamUnion
		for _, part := range m.Parts {
			switch part := part.(type) {
			case ai.Text:
				if part != "" { // the API refuses empty text blocks
					blocks = append(blocks, sdk.NewTextBlock(string(part)))
				}
			case ai.ToolCall:
				input := part.Input
				if !isObject(input) { // the API takes only an object
					input = json.RawMessage("{}")
				}
				blocks = append(blocks, sdk.NewToolUseBlock(part.ID, input, part.Name))
			case ai.ToolResult:
				if part.Content == "" { // no empty text block: the API refuses those
					blocks = append(blocks, sdk.ContentBlockParamUnion{OfToolResult: &sdk.ToolResultBlockParam{
						ToolUseID: part.CallID, IsError: sdk.Bool(part.IsError),
					}})
					continue
				}
				blocks = append(blocks, sdk.NewToolResultBlock(part.CallID, part.Content, part.IsError))
			case ai.Reasoning:
				if part.Provider != Name {
					continue // another provider's
				}
				if data, ok := strings.CutPrefix(part.Data, redacted); ok {
					blocks = append(blocks, sdk.NewRedactedThinkingBlock(data))
				} else {
					blocks = append(blocks, sdk.NewThinkingBlock(part.Data, part.Text))
				}
			default:
				return nil, fmt.Errorf("unknown message part %T", part)
			}
		}
		switch m.Role {
		case ai.RoleAssistant:
			add(sdk.MessageParamRoleAssistant, blocks)
		case ai.RoleUser, ai.RoleTool:
			add(sdk.MessageParamRoleUser, blocks)
		default:
			return nil, fmt.Errorf("unknown message role %q", m.Role)
		}
	}
	return out, nil
}

// isObject reports whether data is a JSON object.
func isObject(data json.RawMessage) bool {
	t := bytes.TrimSpace(data)
	return len(t) > 0 && t[0] == '{' && json.Valid(t)
}

// response translates the API's answer.
func response(msg *sdk.Message) *ai.Response {
	parts := make([]ai.Part, 0, len(msg.Content))
	for _, c := range msg.Content {
		switch c.Type {
		case "text":
			parts = append(parts, ai.Text(c.Text))
		case "tool_use":
			input := c.Input
			if len(input) == 0 {
				input = json.RawMessage("{}")
			}
			parts = append(parts, ai.ToolCall{ID: c.ID, Name: c.Name, Input: slices.Clone(input)})
		case "thinking":
			parts = append(parts, ai.Reasoning{Provider: Name, Text: c.Thinking, Data: c.Signature})
		case "redacted_thinking":
			parts = append(parts, ai.Reasoning{Provider: Name, Data: redacted + c.Data})
		}
	}
	u := msg.Usage
	return &ai.Response{
		Message: ai.Message{Role: ai.RoleAssistant, Parts: parts},
		Stop:    stopOf(msg.StopReason),
		Usage: ai.Usage{
			InputTokens:      u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens,
			OutputTokens:     u.OutputTokens,
			CacheReadTokens:  u.CacheReadInputTokens,
			CacheWriteTokens: u.CacheCreationInputTokens,
		},
		Model: msg.Model,
		Raw:   msg,
	}
}

func stopOf(r sdk.StopReason) ai.StopReason {
	switch r {
	case sdk.StopReasonEndTurn, sdk.StopReasonStopSequence:
		return ai.StopEnd
	case sdk.StopReasonToolUse:
		return ai.StopToolCalls
	case sdk.StopReasonMaxTokens, sdk.StopReasonModelContextWindowExceeded:
		return ai.StopMaxTokens
	case sdk.StopReasonRefusal:
		return ai.StopRefusal
	}
	return ai.StopOther
}

// Generate sends req and returns Claude's answer.
func (p *Provider) Generate(ctx context.Context, req *ai.Request) (*ai.Response, error) {
	params, err := p.params(req)
	if err != nil {
		return nil, err
	}
	// The SDK refuses long answers (large MaxTokens) without a timeout of
	// the caller's: the context's deadline (AI_TIMEOUT), else an hour.
	timeout := time.Hour
	if d, ok := ctx.Deadline(); ok {
		timeout = max(time.Until(d), time.Second)
	}
	msg, err := p.client.Messages.New(ctx, params, option.WithRequestTimeout(timeout))
	if err != nil {
		return nil, err
	}
	return response(msg), nil
}

// Stream sends req and yields Claude's answer as it's written.
func (p *Provider) Stream(ctx context.Context, req *ai.Request) iter.Seq2[ai.Event, error] {
	return func(yield func(ai.Event, error) bool) {
		params, err := p.params(req)
		if err != nil {
			yield(ai.Event{}, err)
			return
		}
		stream := p.client.Messages.NewStreaming(ctx, params)
		defer func() { _ = stream.Close() }()
		var msg sdk.Message
		for stream.Next() {
			ev := stream.Current()
			if err := msg.Accumulate(ev); err != nil {
				yield(ai.Event{}, err)
				return
			}
			switch ev.Type {
			case "content_block_delta":
				if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
					if !yield(ai.Event{Kind: ai.EventText, Text: ev.Delta.Text}, nil) {
						return
					}
				}
			case "content_block_stop":
				if c := msg.Content[ev.Index]; c.Type == "tool_use" {
					input := slices.Clone(c.Input)
					if len(input) == 0 {
						input = json.RawMessage("{}")
					}
					if !yield(ai.Event{Kind: ai.EventToolCall, ToolCall: &ai.ToolCall{ID: c.ID, Name: c.Name, Input: input}}, nil) {
						return
					}
				}
			}
		}
		if err := stream.Err(); err != nil {
			yield(ai.Event{}, err)
			return
		}
		yield(ai.Event{Kind: ai.EventResponse, Response: response(&msg)}, nil)
	}
}
