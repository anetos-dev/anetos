// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"anetos.dev/anetos"
	"anetos.dev/anetos/validate"
)

// Option configures a call ([Generate], [Stream], [GenerateObject], an
// [Agent]'s). An [Agent] is an Option too: its settings, then the
// call's own.
type Option interface{ apply(*call) }

type optionFunc func(*call)

func (f optionFunc) apply(c *call) { f(c) }

// call is a call's settings.
type call struct {
	client      *Client
	name        string // the agent's, for logs
	model       string
	system      []string
	history     []Message
	tools       []Tool
	maxSteps    int
	maxTokens   int
	temperature *float64
	timeout     time.Duration
	options     any
}

// Model sets the model: its name at the provider ("claude-sonnet-4-5",
// "gpt-5"…). Without it, the AI_MODEL setting, else the provider's
// default.
func Model(name string) Option { return optionFunc(func(c *call) { c.model = name }) }

// System adds system instructions: who the model is, what it may do,
// how to answer. Several are joined by blank lines, in the order the
// options come (an agent's Instructions where the agent is).
func System(instructions string) Option {
	return optionFunc(func(c *call) {
		if instructions != "" {
			c.system = append(c.system, instructions)
		}
	})
}

// Messages adds a conversation before the prompt, oldest first: a
// previous [Result]'s Messages, to continue it. The prompt may then be
// empty.
func Messages(history ...Message) Option {
	return optionFunc(func(c *call) { c.history = append(c.history, history...) })
}

// Tools adds tools the model may call ([Func]). With tools, a call is
// a loop: the model calls tools, they run, the model gets their results
// and goes on, until it answers without calling any or [MaxSteps] is
// reached.
func Tools(tools ...Tool) Option {
	return optionFunc(func(c *call) { c.tools = append(c.tools, tools...) })
}

// DefaultMaxSteps is the number of model requests a call makes at most,
// unless [MaxSteps] sets another.
const DefaultMaxSteps = 10

// MaxSteps bounds the number of requests to the model in a call with
// tools (default [DefaultMaxSteps]). A model that still calls tools at
// the last step ends the call with [ErrMaxSteps].
func MaxSteps(n int) Option { return optionFunc(func(c *call) { c.maxSteps = n }) }

// MaxTokens bounds each answer's length, in tokens (AI_MAX_TOKENS by
// default). An answer that reaches it is cut off ([StopMaxTokens]).
func MaxTokens(n int) Option { return optionFunc(func(c *call) { c.maxTokens = n }) }

// Temperature sets the sampling temperature: lower is more predictable.
// Without it, the provider's default.
func Temperature(t float64) Option { return optionFunc(func(c *call) { c.temperature = &t }) }

// Timeout bounds each request to the model (AI_TIMEOUT by default). In
// a stream, the time the reader's loop takes counts too. The call's
// context bounds the whole call.
func Timeout(d time.Duration) Option { return optionFunc(func(c *call) { c.timeout = d }) }

// ProviderOptions sets the provider's own request options (a value of a
// type its driver defines), for features the common request doesn't
// have. Providers ignore types that aren't theirs.
func ProviderOptions(v any) Option { return optionFunc(func(c *call) { c.options = v }) }

// Using makes the call use client c instead of the context's.
func Using(c *Client) Option { return optionFunc(func(cl *call) { cl.client = c }) }

// Agent is a model with instructions and tools, reused for many calls:
//
//	var support = ai.Agent{
//		Name:         "support",
//		Instructions: "You answer the customer's questions about their orders.",
//		Tools:        []ai.Tool{findOrder},
//	}
//
//	res, err := support.Prompt(ctx, "Where is order 1042?")
//
// Its tools run with the call's context, as the current user. An Agent
// is also an [Option], for [GenerateObject] and the other calls.
type Agent struct {
	// Name names the agent in logs.
	Name string
	// Instructions are its system instructions.
	Instructions string
	// Tools are the tools it may call.
	Tools []Tool
	// Model is its model; empty means the app's default (AI_MODEL).
	Model string
	// MaxSteps bounds its requests per call; 0 means [DefaultMaxSteps].
	MaxSteps int
	// Options are more options for its calls ([MaxTokens],
	// [Temperature]…), before each call's own.
	Options []Option
}

func (a Agent) apply(c *call) {
	if a.Name != "" {
		c.name = a.Name
	}
	if a.Model != "" {
		c.model = a.Model
	}
	if a.MaxSteps != 0 {
		c.maxSteps = a.MaxSteps
	}
	System(a.Instructions).apply(c)
	c.tools = append(c.tools, a.Tools...)
	for _, o := range a.Options {
		o.apply(c)
	}
}

// Prompt sends prompt to the agent and returns its answer, after any
// tool calls: [Generate] with the agent's settings.
func (a Agent) Prompt(ctx context.Context, prompt string, opts ...Option) (*Result, error) {
	return Generate(ctx, prompt, append([]Option{a}, opts...)...)
}

// Stream is [Stream] with the agent's settings.
func (a Agent) Stream(ctx context.Context, prompt string, opts ...Option) iter.Seq2[Event, error] {
	return Stream(ctx, prompt, append([]Option{a}, opts...)...)
}

// Result is the outcome of a call: its answer, every model response on
// the way, and the conversation, to continue it.
type Result struct {
	// Messages is the whole conversation: the [Messages] given, the
	// prompt, then the model's answers and tool results, in order. Pass
	// it to [Messages] to continue. It marshals to JSON. After an error,
	// it can end with tool calls that have no results, which providers
	// refuse to continue from.
	Messages []Message
	// Steps are the model's responses, one per request.
	Steps []*Response
	// Usage is the total of the steps'.
	Usage Usage
}

// Response returns the last model response (nil if there was none).
func (r *Result) Response() *Response {
	if len(r.Steps) == 0 {
		return nil
	}
	return r.Steps[len(r.Steps)-1]
}

// Text returns the answer: the text of the last response.
func (r *Result) Text() string {
	if resp := r.Response(); resp != nil {
		return resp.Text()
	}
	return ""
}

// ErrMaxSteps ends a call whose model still called tools at its last
// step ([MaxSteps]).
var ErrMaxSteps = errors.New("ai: the model was still calling tools at the step limit (ai.MaxSteps)")

// Generate sends prompt to the model and returns its answer:
//
//	res, err := ai.Generate(ctx, "Summarize in one sentence: "+post.Body)
//	summary := res.Text()
//
// With [Tools], tool calls run until the model answers ([MaxSteps]). The
// client is the context's ([ForApp]), or [Using]'s. Each request to the
// model is logged (provider, model, tokens, time; never the content). An
// answer cut off at [MaxTokens] isn't an error: check
// res.Response().Stop.
func Generate(ctx context.Context, prompt string, opts ...Option) (*Result, error) {
	return run(ctx, runInput{prompt: prompt, opts: opts})
}

// Stream is [Generate] yielding the answer as it's written: EventText for
// each piece of text, EventToolCall and EventToolResult for each tool
// call and its result, EventResponse when a model response is complete,
// and last, EventDone with the [Result]. An error ends the stream.
//
//	for ev, err := range ai.Stream(ctx, question) {
//		if err != nil {
//			return err
//		}
//		if ev.Kind == ai.EventText {
//			fmt.Fprint(w, ev.Text)
//		}
//	}
//
// Breaking out of the loop stops the generation.
func Stream(ctx context.Context, prompt string, opts ...Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		stopped := false
		res, err := run(ctx, runInput{prompt: prompt, opts: opts, yield: func(e Event) bool {
			if !yield(e, nil) {
				stopped = true
			}
			return !stopped
		}})
		switch {
		case stopped:
		case err != nil:
			yield(Event{}, err)
		default:
			yield(Event{Kind: EventDone, Result: res}, nil)
		}
	}
}

// GenerateObject asks the model for a T, a struct, and returns it
// decoded and checked:
//
//	type Summary struct {
//		Title string   `json:"title" validate:"required|max:80"`
//		Tags  []string `json:"tags" description:"Lower-case topics" validate:"max:5"`
//	}
//
//	sum, res, err := ai.GenerateObject[Summary](ctx, "Summarize this post: "+post.Body)
//
// The model is given T's JSON schema ([SchemaFor]) through the
// provider's structured output; its answer is decoded into a T and
// checked with T's validate tags. An answer that fails is sent back
// once, with what's wrong, for the model to correct (a second call, with
// the same options and its own step limit); a second failure returns an
// [*OutputError]. An answer cut off at [MaxTokens], or a refusal, isn't
// retried. The [Result] has both calls' steps and usage.
func GenerateObject[T any](ctx context.Context, prompt string, opts ...Option) (T, *Result, error) {
	var zero T
	out, err := outputOf(reflect.TypeFor[T]())
	if err != nil {
		return zero, nil, err
	}
	res, err := run(ctx, runInput{prompt: prompt, opts: opts, output: out.spec})
	if err != nil {
		return zero, res, err
	}
	v, problem := decodeOutput[T](ctx, out, res)
	if problem == nil {
		return v, res, nil
	}
	oe, ok := errors.AsType[*OutputError](problem)
	if !ok || oe.Stop == StopMaxTokens || oe.Stop == StopRefusal {
		return zero, res, problem // a rule that couldn't run, no room to answer, or a refusal
	}
	retry, err := run(ctx, runInput{
		prompt: fmt.Sprintf("Your answer isn't a valid %s: %s. Answer again with only the JSON object, matching the schema.",
			out.spec.Name, problemText(oe.Err)),
		opts: opts, output: out.spec, conversation: res.Messages,
	})
	if retry != nil {
		retry.Steps = append(slices.Clip(res.Steps), retry.Steps...)
		retry.Usage.Add(res.Usage)
		res = retry
	}
	if err != nil {
		return zero, res, err
	}
	v, problem = decodeOutput[T](ctx, out, res)
	if problem != nil {
		return zero, res, problem
	}
	return v, res, nil
}

// OutputError is a structured answer that isn't a valid T: not JSON of
// T's shape, failing T's validate rules (Err is then a
// *validate.Errors), cut off at [MaxTokens], or a refusal. It reports
// status 502 to the web package: the model, upstream, failed.
type OutputError struct {
	// Type is T's name.
	Type string
	// Text is the model's answer.
	Text string
	// Stop is why the model stopped: StopMaxTokens (the answer was cut
	// off) and StopRefusal (it declined) aren't retried.
	Stop StopReason
	// Err says what's wrong.
	Err error
}

// Error says what's wrong with the answer.
func (e *OutputError) Error() string {
	switch e.Stop {
	case StopMaxTokens:
		return fmt.Sprintf("ai: the model's %s was cut off at the token limit (raise ai.MaxTokens)", e.Type)
	case StopRefusal:
		return fmt.Sprintf("ai: the model declined to write the %s", e.Type)
	}
	return fmt.Sprintf("ai: the model's answer isn't a valid %s: %v", e.Type, e.Err)
}

// Unwrap returns Err.
func (e *OutputError) Unwrap() error { return e.Err }

// HTTPStatus is 502 (Bad Gateway).
func (e *OutputError) HTTPStatus() int { return http.StatusBadGateway }

// problemText is a decoding or validation error, for the model.
func problemText(err error) string {
	if errs, ok := errors.AsType[*validate.Errors](err); ok {
		fields := errs.FieldErrors()
		parts := make([]string, 0, len(fields))
		for _, k := range errs.Keys() {
			parts = append(parts, k+": "+fields[k])
		}
		return strings.Join(parts, "; ")
	}
	return err.Error()
}

// output is a structured output type's request spec and validation.
type output struct {
	typ  reflect.Type
	spec *OutputSpec
	plan *validate.Plan
}

var outputs sync.Map // reflect.Type → *output or error

func outputOf(t reflect.Type) (*output, error) {
	if v, ok := outputs.Load(t); ok {
		if err, isErr := v.(error); isErr {
			return nil, err
		}
		return v.(*output), nil
	}
	o, err := newOutput(t)
	if err != nil {
		outputs.Store(t, err)
		return nil, err
	}
	v, _ := outputs.LoadOrStore(t, o)
	return v.(*output), nil
}

func newOutput(t reflect.Type) (*output, error) {
	s, err := schemaOf(t)
	if err != nil {
		return nil, err
	}
	plan, err := validate.Compile(t)
	if err != nil {
		return nil, fmt.Errorf("ai: %s: %w", t, err)
	}
	return &output{typ: t, spec: &OutputSpec{Name: outputName(t), Schema: s}, plan: plan}, nil
}

// outputName is t's name in snake case ("order_summary"), or "output".
func outputName(t reflect.Type) string {
	name := t.Name()
	if i := strings.IndexByte(name, '['); i >= 0 {
		name = name[:i] // a generic type's arguments
	}
	if name == "" {
		return "output"
	}
	var b strings.Builder
	rs := []rune(name)
	for i, r := range rs {
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(rs[i-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))) {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "output"
	}
	return b.String()
}

// decodeOutput decodes and checks res's answer as a T: an *OutputError
// if it isn't one.
func decodeOutput[T any](ctx context.Context, o *output, res *Result) (T, error) {
	var v T
	text := res.Text()
	var stop StopReason
	if resp := res.Response(); resp != nil {
		stop = resp.Stop
	}
	fail := func(err error) (T, error) {
		var zero T
		return zero, &OutputError{Type: o.typ.String(), Text: text, Stop: stop, Err: err}
	}
	switch stop {
	case StopMaxTokens:
		return fail(errors.New("cut off at the token limit"))
	case StopRefusal:
		return fail(errors.New("refused"))
	}
	if err := json.Unmarshal([]byte(jsonOf(text)), &v); err != nil {
		return fail(err)
	}
	if err := o.plan.Validate(ctx, &v); err != nil {
		if _, ok := errors.AsType[*validate.Errors](err); ok {
			return fail(err)
		}
		return v, err
	}
	return v, nil
}

// jsonOf returns the JSON object in a model's answer: the answer, less
// a Markdown code fence some models add.
func jsonOf(text string) string {
	t := strings.TrimSpace(text)
	if rest, ok := strings.CutPrefix(t, "```"); ok {
		// The fence's language (```json), up to the line's end or the
		// object.
		rest = strings.TrimLeftFunc(rest, func(r rune) bool { return r != '\n' && r != '{' && r != '[' })
		t = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "```"))
	}
	return t
}

// runInput is what run needs.
type runInput struct {
	prompt string
	opts   []Option
	output *OutputSpec
	// conversation, when set, replaces the history of Messages options
	// (a retry continues a result's conversation).
	conversation []Message
	// yield, when set, streams: it gets every event, and false stops.
	yield func(Event) bool
}

// errStopped ends a run whose stream's consumer stopped.
var errStopped = errors.New("ai: stopped")

// run makes a call: requests to the model, and tool calls between them.
func run(ctx context.Context, in runInput) (*Result, error) {
	var c call
	for _, o := range in.opts { // the client first: its defaults come before the options
		o.apply(&c)
	}
	client := c.client
	if client == nil {
		var err error
		if client, err = From(ctx); err != nil {
			return nil, err
		}
	}
	c = call{}
	for _, o := range client.defaults {
		o.apply(&c)
	}
	for _, o := range in.opts {
		o.apply(&c)
	}
	if in.conversation != nil {
		c.history = in.conversation
	}

	msgs := slices.Clone(c.history)
	if in.prompt != "" {
		msgs = append(msgs, UserMessage(in.prompt))
	}
	if len(msgs) == 0 {
		return nil, errNothingToSend
	}
	if err := checkMessages(msgs); err != nil {
		return nil, err
	}
	tools := map[string]Tool{}
	specs := make([]ToolSpec, 0, len(c.tools))
	for _, t := range c.tools {
		spec := t.Definition()
		if _, dup := tools[spec.Name]; dup {
			return nil, fmt.Errorf("ai: two tools are named %s", spec.Name)
		}
		tools[spec.Name] = t
		specs = append(specs, spec)
	}
	maxSteps := c.maxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	provider := client.Provider()
	res := &Result{}
	for step := 1; ; step++ {
		req := &Request{
			Model:       c.model,
			System:      strings.Join(c.system, "\n\n"),
			Messages:    slices.Clip(msgs),
			Tools:       slices.Clip(specs),
			Output:      in.output,
			MaxTokens:   c.maxTokens,
			Temperature: c.temperature,
			Options:     c.options,
		}
		resp, err := client.request(ctx, provider, &c, step, req, in.yield)
		if err != nil {
			res.Messages = msgs
			if errors.Is(err, errStopped) {
				return res, err
			}
			return res, fmt.Errorf("ai: %s: %w", provider.Name(), err)
		}
		res.Steps = append(res.Steps, resp)
		res.Usage.Add(resp.Usage)
		msgs = append(msgs, resp.Message)
		res.Messages = msgs
		if in.yield != nil && !in.yield(Event{Kind: EventResponse, Response: resp}) {
			return res, errStopped
		}
		calls := resp.ToolCalls()
		if len(calls) == 0 {
			return res, nil
		}
		if step >= maxSteps {
			return res, ErrMaxSteps
		}
		results := make([]Part, 0, len(calls))
		for _, tc := range calls {
			r, err := client.runTool(ctx, &c, tools, tc)
			if err != nil {
				return res, err
			}
			results = append(results, r)
			if in.yield != nil && !in.yield(Event{Kind: EventToolResult, ToolResult: &r}) {
				return res, errStopped
			}
		}
		msgs = append(msgs, Message{Role: RoleTool, Parts: results})
		res.Messages = msgs
	}
}

// request sends one request, streaming its events to yield if set.
func (cl *Client) request(ctx context.Context, p Provider, c *call, step int, req *Request, yield func(Event) bool) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rctx := ctx
	if c.timeout > 0 {
		var cancel context.CancelFunc
		rctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	start := time.Now()
	var resp *Response
	var err error
	if yield == nil {
		resp, err = p.Generate(rctx, req)
	} else {
		for ev, serr := range p.Stream(rctx, req) {
			if serr != nil {
				err = serr
				break
			}
			switch ev.Kind {
			case EventText, EventToolCall:
				if !yield(ev) {
					err = errStopped
				}
			case EventResponse:
				resp = ev.Response
			}
			if err != nil {
				break
			}
		}
		if err == nil && resp == nil {
			err = errors.New("the stream ended without a response")
		}
	}
	if err == nil && resp == nil {
		err = errors.New("no response")
	}
	if err == nil && resp.Message.Role != RoleAssistant {
		err = fmt.Errorf("the response's role is %q, not assistant", resp.Message.Role)
	}
	attrs := []slog.Attr{
		slog.String("provider", p.Name()), slog.Int("step", step), slog.Duration("duration", time.Since(start)),
	}
	if c.name != "" {
		attrs = append(attrs, slog.String("agent", c.name))
	}
	switch {
	case errors.Is(err, errStopped):
		// The tokens are spent all the same.
		attrs = append(attrs, slog.String("model", req.Model))
		cl.logger().LogAttrs(ctx, slog.LevelInfo, "ai: stream stopped by its reader", attrs...)
		return nil, err
	case err != nil:
		attrs = append(attrs, slog.String("model", req.Model), slog.Any("error", err))
		cl.logger().LogAttrs(ctx, slog.LevelWarn, "ai: request failed", attrs...)
		return nil, err
	}
	attrs = append(attrs, slog.String("model", resp.Model), slog.String("stop", string(resp.Stop)),
		slog.Int64("input_tokens", resp.Usage.InputTokens), slog.Int64("output_tokens", resp.Usage.OutputTokens))
	cl.logger().LogAttrs(ctx, slog.LevelInfo, "ai: response", attrs...)
	return resp, nil
}

// logger returns the client's logger: the app's, or slog's default.
func (cl *Client) logger() *slog.Logger {
	if cl.log != nil {
		return cl.log
	}
	return slog.Default()
}

// runTool runs one tool call as a unit of work, and returns its result
// for the model, or the error that stops the call.
func (cl *Client) runTool(ctx context.Context, c *call, tools map[string]Tool, tc ToolCall) (ToolResult, error) {
	r := ToolResult{CallID: tc.ID, Name: tc.Name}
	attrs := []slog.Attr{slog.String("tool", tc.Name)}
	if c.name != "" {
		attrs = append(attrs, slog.String("agent", c.name))
	}
	t, ok := tools[tc.Name]
	if !ok {
		cl.logger().LogAttrs(ctx, slog.LevelWarn, "ai: the model called a tool that doesn't exist", attrs...)
		r.Content, r.IsError = fmt.Sprintf("There is no tool named %q.", tc.Name), true
		return r, nil
	}
	uctx, end := ctx, func() {}
	if cl.units != nil {
		uctx, end = cl.units(ctx, anetos.Unit{Kind: "tool", Name: tc.Name})
	}
	start := time.Now()
	out, err := func() (string, error) {
		defer end()
		return t.Call(uctx, tc.Input)
	}()
	attrs = append(attrs, slog.Duration("duration", time.Since(start)))
	if err != nil {
		msg, tell := modelError(err)
		attrs = append(attrs, slog.Any("error", err))
		if !tell {
			cl.logger().LogAttrs(ctx, slog.LevelError, "ai: tool failed", attrs...)
			return r, fmt.Errorf("ai: tool %s: %w", tc.Name, err)
		}
		cl.logger().LogAttrs(ctx, slog.LevelInfo, "ai: tool refused", attrs...)
		r.Content, r.IsError = msg, true
		return r, nil
	}
	cl.logger().LogAttrs(ctx, slog.LevelInfo, "ai: tool", attrs...)
	r.Content = out
	return r, nil
}
