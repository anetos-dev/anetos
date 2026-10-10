// SPDX-License-Identifier: Apache-2.0

// Package gemini is the Gemini provider of package ai (Google's Gemini
// API), on Google's official Gen AI SDK for Go:
//
//	client, err := ai.New(app, gemini.Driver()) // AI_PROVIDER=gemini
//
// It reads GEMINI_API_KEY (required) and GEMINI_BASE_URL, and needs
// AI_MODEL. Structured output uses Gemini's JSON schema response format.
// Gemini's thought signatures, which it needs back to continue after
// tool calls, are kept in the conversation as ai.Reasoning. [Options]
// sets the thinking budget, and reaches every request parameter;
// Response.Raw is the SDK's *genai.GenerateContentResponse (for a
// stream, its []*genai.GenerateContentResponse), and [Provider.Client]
// the SDK client. Requests are retried twice on rate limits and server
// errors. It makes embeddings ([Provider.Embed]): AI_EMBEDDING_MODEL
// names the model (gemini-embedding-001), and AI_EMBEDDING_PROVIDER=gemini
// uses them with another provider's chat. Vertex AI isn't supported yet.
package gemini

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"

	"google.golang.org/genai"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/config"
)

// Name is the provider's name: AI_PROVIDER=gemini.
const Name = "gemini"

// Config holds the GEMINI_* settings.
type Config struct {
	// APIKey is the Gemini API key, from Google AI Studio.
	// GEMINI_API_KEY, required.
	APIKey anetos.Secret `env:"GEMINI_API_KEY"`
	// BaseURL replaces the API's URL (a proxy, a gateway).
	// GEMINI_BASE_URL.
	BaseURL string `env:"GEMINI_BASE_URL"`
}

// Driver is the provider's driver for ai.New: AI_PROVIDER=gemini.
func Driver() ai.Driver {
	return ai.Driver{Name: Name, Open: func(app *anetos.App, cfg ai.Config) (ai.Provider, error) {
		c, err := config.Get[Config](app.Source())
		if err != nil {
			return nil, err
		}
		if c.APIKey == "" {
			return nil, errors.New("GEMINI_API_KEY isn't set: the API key, from Google AI Studio")
		}
		if cfg.Model == "" {
			return nil, errors.New("AI_MODEL isn't set: name the Gemini model to use")
		}
		cc := genai.ClientConfig{APIKey: string(c.APIKey)}
		cc.HTTPOptions.BaseURL = c.BaseURL
		return New(context.Background(), cc)
	}}
}

// Provider calls the Gemini API. It is safe for concurrent use.
type Provider struct {
	client *genai.Client
}

// New returns a provider for the SDK's client configuration: the API
// key, and optionally an HTTP client, a base URL and retries. The
// backend is the Gemini API; retries default to two, on rate limits and
// server errors (the SDK retries only when told).
func New(ctx context.Context, cc genai.ClientConfig) (*Provider, error) {
	if cc.Backend == genai.BackendUnspecified {
		cc.Backend = genai.BackendGeminiAPI
	}
	if cc.HTTPOptions.RetryOptions == nil {
		attempts := int32(3)
		cc.HTTPOptions.RetryOptions = &genai.HTTPRetryOptions{Attempts: &attempts}
	}
	client, err := genai.NewClient(ctx, &cc)
	if err != nil {
		return nil, err
	}
	return &Provider{client: client}, nil
}

// Client returns the SDK client, for the API's other features.
func (p *Provider) Client() *genai.Client { return p.client }

// Name returns "gemini".
func (p *Provider) Name() string { return Name }

// Embed implements ai.Embedder with the Gemini API's embeddings
// (gemini-embedding-001): documents are embedded for retrieval
// (RETRIEVAL_DOCUMENT), queries as queries (RETRIEVAL_QUERY), and
// req.Dimensions shortens the vectors. Gemini doesn't count the tokens:
// the usage is estimated, at four bytes a token.
func (p *Provider) Embed(ctx context.Context, req *ai.EmbedRequest) (*ai.EmbedResponse, error) {
	cfg := &genai.EmbedContentConfig{TaskType: "RETRIEVAL_DOCUMENT"}
	if req.Purpose == ai.EmbedForQuery {
		cfg.TaskType = "RETRIEVAL_QUERY"
	}
	if req.Dimensions > 0 {
		dims := int32(req.Dimensions)
		cfg.OutputDimensionality = &dims
	}
	contents := make([]*genai.Content, len(req.Inputs))
	size := 0
	for i, text := range req.Inputs {
		contents[i] = genai.NewContentFromText(text, genai.RoleUser)
		size += len(text)
	}
	res, err := p.client.Models.EmbedContent(ctx, req.Model, contents, cfg)
	if err != nil {
		return nil, err
	}
	if len(res.Embeddings) != len(req.Inputs) {
		return nil, fmt.Errorf("gemini: %d embeddings for %d inputs", len(res.Embeddings), len(req.Inputs))
	}
	out := &ai.EmbedResponse{Model: req.Model, Usage: ai.Usage{InputTokens: int64((size + 3) / 4)}, Estimated: true}
	for _, e := range res.Embeddings {
		if e == nil {
			return nil, errors.New("gemini: an empty embedding")
		}
		out.Vectors = append(out.Vectors, ai.Vector(e.Values))
	}
	return out, nil
}

// Options are the provider's own request options, for ai.ProviderOptions.
type Options struct {
	// ThinkingBudget, if set, bounds the tokens the model thinks with
	// (0 turns thinking off where the model allows it).
	ThinkingBudget *int32
	// Params, if set, changes the request's configuration last, for
	// what the common request doesn't have (as the anthropic and openai
	// drivers' Params).
	Params func(*genai.GenerateContentConfig)
	// Config is Params.
	//
	// Deprecated: Use Params; Config is removed in v0.6.
	Config func(*genai.GenerateContentConfig)
}

// schemaOptions is Gemini's dialect of JSON Schema.
var schemaOptions = ai.SchemaOptions{
	Keywords:      []string{"format", "minimum", "maximum", "minItems", "maxItems"},
	Formats:       []string{"date-time", "date", "time"},
	NullableAnyOf: true,
}

// syntheticID starts the IDs given to function calls without one.
const syntheticID = "anetos_call_"

// skipSignature is the placeholder signature Gemini takes on function
// calls it didn't make (another provider's, a written history): Gemini 3
// rejects a step's first call without a signature. Calls this driver
// gave an ID (Gemini's own, from models that sign none) don't get it.
var skipSignature, _ = base64.URLEncoding.DecodeString("skip_thought_signature_validator")

func (p *Provider) request(req *ai.Request) ([]*genai.Content, *genai.GenerateContentConfig, error) {
	if req.Model == "" {
		return nil, nil, errors.New("no model: set AI_MODEL, or ai.Model")
	}
	cfg := &genai.GenerateContentConfig{}
	if req.System != "" {
		cfg.SystemInstruction = &genai.Content{Parts: []*genai.Part{{Text: req.System}}}
	}
	if req.MaxTokens > 0 {
		cfg.MaxOutputTokens = int32(min(req.MaxTokens, 1<<31-1))
	}
	if req.Temperature != nil {
		t := float32(*req.Temperature)
		cfg.Temperature = &t
	}
	if len(req.Tools) > 0 {
		decls := make([]*genai.FunctionDeclaration, len(req.Tools))
		for i, t := range req.Tools {
			decls[i] = &genai.FunctionDeclaration{Name: t.Name, Description: t.Description, ParametersJsonSchema: t.Input.Map(schemaOptions)}
		}
		cfg.Tools = []*genai.Tool{{FunctionDeclarations: decls}}
	}
	if req.Output != nil {
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseJsonSchema = req.Output.Schema.Map(schemaOptions)
	}
	for _, v := range req.Options {
		var o *Options
		switch v := v.(type) {
		case Options:
			o = &v
		case *Options:
			o = v
		}
		if o == nil {
			continue
		}
		if o.ThinkingBudget != nil {
			cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingBudget: o.ThinkingBudget}
		}
		if o.Config != nil {
			o.Config(cfg)
		}
		if o.Params != nil {
			o.Params(cfg)
		}
	}
	contents, err := contentsOf(req.Messages)
	return contents, cfg, err
}

// contentsOf translates the conversation: tool results are user
// content, and a Gemini reasoning signature goes back on the part it
// came with.
func contentsOf(msgs []ai.Message) ([]*genai.Content, error) {
	var out []*genai.Content
	for _, m := range msgs {
		var parts []*genai.Part
		var signature []byte
		firstCall := true
		gemini := false // the call being added is one this driver read from Gemini
		add := func(p *genai.Part) {
			p.ThoughtSignature, signature = signature, nil
			if p.FunctionCall != nil && m.Role == ai.RoleAssistant {
				if firstCall && len(p.ThoughtSignature) == 0 && !gemini {
					p.ThoughtSignature = skipSignature
				}
				firstCall = false
			}
			parts = append(parts, p)
		}
		for _, part := range m.Parts {
			switch part := part.(type) {
			case ai.Text:
				if part != "" {
					add(&genai.Part{Text: string(part)})
				}
			case ai.ToolCall:
				gemini = strings.HasPrefix(part.ID, syntheticID)
				args := map[string]any{}
				if len(part.Input) > 0 {
					if err := json.Unmarshal(part.Input, &args); err != nil {
						args = map[string]any{} // the model's broken JSON: nothing to send back
					}
				}
				id := part.ID
				if strings.HasPrefix(id, syntheticID) {
					id = ""
				}
				add(&genai.Part{FunctionCall: &genai.FunctionCall{ID: id, Name: part.Name, Args: args}})
			case ai.ToolResult:
				id := part.CallID
				if strings.HasPrefix(id, syntheticID) {
					id = ""
				}
				add(&genai.Part{FunctionResponse: &genai.FunctionResponse{ID: id, Name: part.Name, Response: resultOf(part)}})
			case ai.Reasoning:
				if part.Provider != Name {
					continue
				}
				sig, err := base64.StdEncoding.DecodeString(part.Data)
				if err != nil {
					return nil, fmt.Errorf("a reasoning signature isn't base64: %w", err)
				}
				if part.Text != "" {
					parts = append(parts, &genai.Part{Text: part.Text, Thought: true, ThoughtSignature: sig})
					continue
				}
				if len(sig) > 0 {
					signature = sig
				}
			default:
				return nil, fmt.Errorf("unknown message part %T", part)
			}
		}
		if signature != nil {
			// A signature at the end came on an empty text part (a stream's
			// last): it goes on the last text, or nowhere (text signatures
			// aren't required).
			for _, p := range slices.Backward(parts) {
				if p.Text != "" && !p.Thought && len(p.ThoughtSignature) == 0 {
					p.ThoughtSignature = signature
					break
				}
			}
		}
		if len(parts) == 0 {
			continue
		}
		role := genai.RoleUser
		switch m.Role {
		case ai.RoleAssistant:
			role = genai.RoleModel
		case ai.RoleUser, ai.RoleTool:
		default:
			return nil, fmt.Errorf("unknown message role %q", m.Role)
		}
		out = append(out, &genai.Content{Role: role, Parts: parts})
	}
	return out, nil
}

// resultOf is a tool result as a function response: the output under
// "output", an error under "error", as the API documents them.
func resultOf(r ai.ToolResult) map[string]any {
	if r.IsError {
		return map[string]any{"error": r.Content}
	}
	var v any
	if json.Unmarshal([]byte(r.Content), &v) == nil {
		return map[string]any{"output": v}
	}
	return map[string]any{"output": r.Content}
}

// partsOf translates a response's parts. A thought signature becomes an
// ai.Reasoning before the part it came with. Function calls without an
// ID get one, from prefix (the response's own) and calls (counted).
func partsOf(parts []*genai.Part, prefix string, calls *int) ([]ai.Part, error) {
	var out []ai.Part
	for _, p := range parts {
		if p == nil {
			continue
		}
		if p.Thought {
			out = append(out, ai.Reasoning{Provider: Name, Text: p.Text, Data: base64.StdEncoding.EncodeToString(p.ThoughtSignature)})
			continue
		}
		if len(p.ThoughtSignature) > 0 {
			out = append(out, ai.Reasoning{Provider: Name, Data: base64.StdEncoding.EncodeToString(p.ThoughtSignature)})
		}
		switch {
		case p.FunctionCall != nil:
			args := p.FunctionCall.Args
			if args == nil {
				args = map[string]any{}
			}
			input, err := json.Marshal(args)
			if err != nil {
				return nil, err
			}
			id := p.FunctionCall.ID
			if id == "" {
				id = fmt.Sprintf("%s%s_%d", syntheticID, prefix, *calls)
			}
			*calls++
			out = append(out, ai.ToolCall{ID: id, Name: p.FunctionCall.Name, Input: input})
		case p.Text != "":
			out = append(out, ai.Text(p.Text))
		}
	}
	return out, nil
}

// callPrefix returns a random prefix for a response's made-up call IDs,
// so they don't repeat in a conversation.
func callPrefix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// merge joins consecutive text parts, as a stream splits them.
func merge(parts []ai.Part) []ai.Part {
	var out []ai.Part
	for _, p := range parts {
		if t, ok := p.(ai.Text); ok && len(out) > 0 {
			if prev, ok := out[len(out)-1].(ai.Text); ok {
				out[len(out)-1] = prev + t
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

func stopOf(r genai.FinishReason, hasCalls bool) ai.StopReason {
	switch r {
	case genai.FinishReasonStop:
		if hasCalls {
			return ai.StopToolCalls
		}
		return ai.StopEnd
	case genai.FinishReasonMaxTokens:
		return ai.StopMaxTokens
	case genai.FinishReasonSafety, genai.FinishReasonRecitation, genai.FinishReasonBlocklist,
		genai.FinishReasonProhibitedContent, genai.FinishReasonSPII, genai.FinishReasonImageSafety:
		return ai.StopRefusal
	}
	return ai.StopOther
}

func usageOf(u *genai.GenerateContentResponseUsageMetadata) ai.Usage {
	if u == nil {
		return ai.Usage{}
	}
	return ai.Usage{
		InputTokens:     int64(u.PromptTokenCount) + int64(u.ToolUsePromptTokenCount),
		OutputTokens:    int64(u.CandidatesTokenCount) + int64(u.ThoughtsTokenCount),
		CacheReadTokens: int64(u.CachedContentTokenCount),
	}
}

// response builds the answer from parts, the finish reason and usage.
func response(parts []ai.Part, finish genai.FinishReason, blocked bool, usage ai.Usage, model string, raw any) *ai.Response {
	hasCalls := false
	for _, p := range parts {
		if _, ok := p.(ai.ToolCall); ok {
			hasCalls = true
		}
	}
	stop := stopOf(finish, hasCalls)
	if blocked {
		stop = ai.StopRefusal
	}
	return &ai.Response{Message: ai.Message{Role: ai.RoleAssistant, Parts: parts}, Stop: stop, Usage: usage, Model: model, Raw: raw}
}

// Generate sends req and returns Gemini's answer.
func (p *Provider) Generate(ctx context.Context, req *ai.Request) (*ai.Response, error) {
	contents, cfg, err := p.request(req)
	if err != nil {
		return nil, err
	}
	r, err := p.client.Models.GenerateContent(ctx, req.Model, contents, cfg)
	if err != nil {
		return nil, err
	}
	var parts []ai.Part
	var finish genai.FinishReason
	if len(r.Candidates) > 0 && r.Candidates[0] != nil {
		c := r.Candidates[0]
		finish = c.FinishReason
		if c.Content != nil {
			calls := 0
			if parts, err = partsOf(c.Content.Parts, callPrefix(), &calls); err != nil {
				return nil, err
			}
		}
	}
	blocked := len(r.Candidates) == 0 && r.PromptFeedback != nil && r.PromptFeedback.BlockReason != ""
	return response(merge(parts), finish, blocked, usageOf(r.UsageMetadata), cmp.Or(r.ModelVersion, req.Model), r), nil
}

// Stream sends req and yields Gemini's answer as it's written.
func (p *Provider) Stream(ctx context.Context, req *ai.Request) iter.Seq2[ai.Event, error] {
	return func(yield func(ai.Event, error) bool) {
		contents, cfg, err := p.request(req)
		if err != nil {
			yield(ai.Event{}, err)
			return
		}
		var (
			chunks  []*genai.GenerateContentResponse
			parts   []ai.Part
			finish  genai.FinishReason
			usage   ai.Usage
			model   string
			blocked bool
			calls   int
			prefix  = callPrefix()
		)
		for r, err := range p.client.Models.GenerateContentStream(ctx, req.Model, contents, cfg) {
			if err != nil {
				yield(ai.Event{}, err)
				return
			}
			chunks = append(chunks, r)
			model = cmp.Or(r.ModelVersion, model)
			if r.UsageMetadata != nil {
				usage = usageOf(r.UsageMetadata)
			}
			if r.PromptFeedback != nil && r.PromptFeedback.BlockReason != "" {
				blocked = true
			}
			if len(r.Candidates) == 0 || r.Candidates[0] == nil {
				continue
			}
			c := r.Candidates[0]
			if c.FinishReason != "" {
				finish = c.FinishReason
			}
			if c.Content == nil {
				continue
			}
			ps, err := partsOf(c.Content.Parts, prefix, &calls)
			if err != nil {
				yield(ai.Event{}, err)
				return
			}
			for _, part := range ps {
				switch part := part.(type) {
				case ai.Text:
					if !yield(ai.Event{Kind: ai.EventText, Text: string(part)}, nil) {
						return
					}
				case ai.ToolCall:
					if !yield(ai.Event{Kind: ai.EventToolCall, ToolCall: &part}, nil) {
						return
					}
				}
			}
			parts = append(parts, ps...)
		}
		yield(ai.Event{Kind: ai.EventResponse, Response: response(merge(parts), finish, blocked, usage, cmp.Or(model, req.Model), chunks)}, nil)
	}
}
