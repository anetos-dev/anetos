// SPDX-License-Identifier: Apache-2.0

package ai_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
)

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fake returns a context with a client of a fake with replies.
func fake(replies ...ai.FakeReply) (context.Context, *ai.Fake) {
	f := ai.NewFake(replies...)
	c := ai.New(f, ai.Model("test-model"), ai.MaxTokens(100))
	return ai.WithClient(context.Background(), c), f
}

type summary struct {
	Title string   `json:"title" validate:"required|max:20"`
	Tags  []string `json:"tags" description:"Topics" validate:"max:3|in:go,web,ai"`
	Score *int     `json:"score,omitempty" validate:"between:1,5"`
	Lang  string   `json:"lang" validate:"in:en,bn"`
	Notes string   `json:"-"`
	inner string   //nolint:unused // unexported fields aren't in the schema
}

// schemaJSON returns T's schema as JSON.
func schemaJSON[T any](t *testing.T) string {
	t.Helper()
	s, err := ai.SchemaFor[T]()
	check(t, err)
	data, err := json.Marshal(s)
	check(t, err)
	return string(data)
}

// level is written as text: "low" or "high".
type level int

func (l level) MarshalText() ([]byte, error) { return []byte([]string{"low", "high"}[l]), nil }

func (l *level) UnmarshalText(b []byte) error {
	*l = level(strings.Index("lowhigh", string(b)) / 3)
	return nil
}

// status is a string type written as text.
type status string

func (s status) MarshalText() ([]byte, error) { return []byte(s), nil }

// money has its own JSON form.
type money struct{ Cents int }

func (m money) MarshalJSON() ([]byte, error) { return json.Marshal(m.Cents) }

type base struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type base2 struct{ X int }

func TestSchemaFor(t *testing.T) {
	want := `{"type":"object","properties":{` +
		`"title":{"type":"string","minLength":1,"maxLength":20},` +
		`"tags":{"type":"array","description":"Topics","items":{"type":"string","enum":["go","web","ai"]},"maxItems":3},` +
		`"score":{"type":["integer","null"],"minimum":1,"maximum":5},` +
		`"lang":{"type":"string","enum":["en","bn"]}},` +
		`"required":["title"],"additionalProperties":false}`
	if got := schemaJSON[summary](t); got != want {
		t.Errorf("schema:\n%s\nwant\n%s", got, want)
	}

	type inner struct {
		When  time.Time         `json:"when" validate:"required"`
		Email string            `json:"email" validate:"email"`
		Meta  map[string]int    `json:"meta" validate:"max:3"`
		Any   any               `json:"any"`
		Raw   json.RawMessage   `json:"raw"`
		Bytes []byte            `json:"bytes"`
		Price float64           `json:"price,string" validate:"in:1.5,2"`
		Opt   *struct{ N bool } `json:"opt"`
	}
	type outer struct {
		inner
		ID int `json:"id" validate:"min:1"`
	}
	want = `{"type":"object","properties":{` +
		`"when":{"type":"string","format":"date-time"},"email":{"type":"string","format":"email"},` +
		`"meta":{"type":"object","properties":{},"additionalProperties":{"type":"integer"},"maxProperties":3},"any":{},"raw":{},` +
		`"bytes":{"type":"string"},"price":{"type":"string","enum":["1.5","2"]},` +
		`"opt":{"type":["object","null"],"properties":{"N":{"type":"boolean"}},"additionalProperties":false},` +
		`"id":{"type":"integer","minimum":1}},"required":["when"],"additionalProperties":false}`
	if got := schemaJSON[outer](t); got != want {
		t.Errorf("schema:\n%s\nwant\n%s", got, want)
	}

	// Fields as encoding/json resolves them: the shallower one wins, then
	// the tagged one; ambiguous ones and embedded pointers to unexported
	// structs are left out.
	type left struct{ A, B int }
	type right struct {
		A int
		B int `json:"B"`
	}
	type shadow struct {
		base
		*base2
		left
		right
		ID string `json:"id" description:"Ours"`
	}
	want = `{"type":"object","properties":{"name":{"type":"string"},"B":{"type":"integer"},"id":{"type":"string","description":"Ours"}},"additionalProperties":false}`
	if got := schemaJSON[shadow](t); got != want {
		t.Errorf("shadowing:\n%s\nwant\n%s", got, want)
	}
	data, _ := json.Marshal(shadow{right: right{B: 2}, ID: "x"})
	if string(data) != `{"name":"","B":2,"id":"x"}` {
		t.Errorf("encoding/json disagrees: %s", data)
	}

	// Rules: spaces as validate allows them; required means not blank; a
	// text type's rules are on its Go value, not its text.
	type rules struct {
		Lang   string            `json:"lang" validate:"required | in: en , bn"`
		Count  uint              `json:"count"`
		Big    int64             `json:"big" validate:"in:9007199254740993"`
		Items  []string          `json:"items" validate:"required|between:1,4|url"`
		Ptr    *int              `json:"ptr" validate:"required"`
		Level  level             `json:"level" validate:"in:0,1"`
		Num    json.Number       `json:"num"`
		ByID   map[int]string    `json:"by_id" validate:"required"`
		Nested map[string]*inner `json:"nested"`
	}
	want = `{"type":"object","properties":{` +
		`"lang":{"type":"string","enum":["en","bn"],"minLength":1},` +
		`"count":{"type":"integer","minimum":0},` +
		`"big":{"type":"integer","enum":[9007199254740993]},` +
		`"items":{"type":"array","items":{"type":"string","format":"uri"},"minItems":1,"maxItems":4},` +
		`"ptr":{"type":"integer"},` +
		`"level":{"type":"string"},` +
		`"num":{"type":"number"},` +
		`"by_id":{"type":"object","properties":{},"additionalProperties":{"type":"string"},"minProperties":1},`
	if got := schemaJSON[rules](t); !strings.HasPrefix(got, want) || !strings.Contains(got, `"required":["lang","items","ptr","by_id"]`) {
		t.Errorf("rules:\n%s\nwant prefix\n%s", got, want)
	}

	// Numbers in canonical form; a string type's rules apply to its text;
	// json.Number's rules count characters, which a number can't say.
	type more struct {
		Frac   float64     `json:"frac" validate:"in:.5,5.,+1,01"`
		Status status      `json:"status" validate:"in:open,closed"`
		Num    json.Number `json:"num" validate:"min:3"`
	}
	want = `{"type":"object","properties":{"frac":{"type":"number","enum":[0.5,5,1,1]},` +
		`"status":{"type":"string","enum":["open","closed"]},"num":{"type":"number"}},"additionalProperties":false}`
	if got := schemaJSON[more](t); got != want {
		t.Errorf("more:\n%s\nwant\n%s", got, want)
	}

	for name, f := range map[string]func() (*ai.Schema, error){
		"recursive":      func() (*ai.Schema, error) { type node struct{ Next *node }; return ai.SchemaFor[node]() },
		"a string":       ai.SchemaFor[string],
		"own JSON":       func() (*ai.Schema, error) { return ai.SchemaFor[struct{ M money }]() },
		"embedded time":  func() (*ai.Schema, error) { return ai.SchemaFor[struct{ time.Time }]() },
		"a text type":    ai.SchemaFor[struct{ level }],
		"bad map key":    func() (*ai.Schema, error) { return ai.SchemaFor[struct{ M map[bool]int }]() },
		"a channel type": func() (*ai.Schema, error) { return ai.SchemaFor[struct{ C chan int }]() },
	} {
		if _, err := f(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestGenerate(t *testing.T) {
	ctx, f := fake(ai.FakeText("Paris is the capital."))
	res, err := ai.Generate(ctx, "What's the capital of France?", ai.System("Be brief."), ai.System("Answer in English."), ai.Temperature(0.2))
	check(t, err)
	if res.Text() != "Paris is the capital." || len(res.Steps) != 1 || res.Response().Stop != ai.StopEnd {
		t.Errorf("result: %+v", res)
	}
	if res.Usage.OutputTokens != 4 || res.Usage.InputTokens == 0 {
		t.Errorf("usage: %+v", res.Usage)
	}
	if len(res.Messages) != 2 || res.Messages[0].Role != ai.RoleUser || res.Messages[1].Text() != "Paris is the capital." {
		t.Errorf("messages: %+v", res.Messages)
	}
	reqs := f.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}
	r := reqs[0]
	if r.Model != "test-model" || r.MaxTokens != 100 || r.System != "Be brief.\n\nAnswer in English." ||
		r.Temperature == nil || *r.Temperature != 0.2 || r.Prompt() != "What's the capital of France?" || r.Output != nil {
		t.Errorf("request: %+v", r)
	}

	// A conversation continues; the call's options override the client's.
	f.Add(ai.FakeText("About 2 million."))
	res2, err := ai.Generate(ctx, "And its population?", ai.Messages(res.Messages...), ai.Model("other"), ai.MaxTokens(5))
	check(t, err)
	r = f.Requests()[1]
	if len(r.Messages) != 3 || r.Model != "other" || r.MaxTokens != 5 || len(res2.Messages) != 4 {
		t.Errorf("continued: %+v; %d messages", r, len(res2.Messages))
	}

	// No reply left, nothing to send, no client.
	if _, err := ai.Generate(ctx, "Hello?"); err == nil || !strings.Contains(err.Error(), "ai: fake: no reply scripted for request 3") {
		t.Errorf("no reply: %v", err)
	}
	if _, err := ai.Generate(ctx, ""); err == nil || !strings.Contains(err.Error(), "nothing to send") {
		t.Errorf("nothing: %v", err)
	}
	if _, err := ai.Generate(context.Background(), "Hi"); !errors.Is(err, ai.ErrNoClient) {
		t.Errorf("no client: %v", err)
	}
	other := ai.NewFake(ai.FakeText("from the other client"))
	if res, err := ai.Generate(context.Background(), "Hi", ai.Using(ai.New(other))); err != nil || res.Text() != "from the other client" {
		t.Errorf("Using: %v, %v", res, err)
	}
	// A provider error is wrapped with the provider's name.
	boom := errors.New("overloaded")
	ctx, _ = fake(ai.FakeError(boom))
	if _, err := ai.Generate(ctx, "Hi"); !errors.Is(err, boom) || !strings.Contains(err.Error(), "ai: fake: overloaded") {
		t.Errorf("provider error: %v", err)
	}
	// A canceled context stops before any request.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := ai.Generate(cctx, "Hi"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled: %v", err)
	}
	// Messages are checked before they're sent.
	bad := ai.Message{Role: ai.RoleUser, Parts: []ai.Part{ai.ToolCall{ID: "x", Name: "t"}}}
	if _, err := ai.Generate(ctx, "", ai.Messages(bad)); err == nil || !strings.Contains(err.Error(), "can't hold") {
		t.Errorf("bad message: %v", err)
	}
}

type order struct {
	Number int    `json:"number"`
	Status string `json:"status"`
}

type findOrder struct {
	Number int `json:"number" description:"The order's number" validate:"required|min:1"`
}

type userKey struct{}

// orderTool finds orders of the user in the context: 7 is someone
// else's, 13 breaks the database.
var orderTool = ai.Func("find_order", "Look up one of the customer's orders",
	func(ctx context.Context, in findOrder) (order, error) {
		user, _ := ctx.Value(userKey{}).(string)
		switch {
		case user == "":
			return order{}, web.Error(http.StatusUnauthorized, "")
		case in.Number == 7:
			return order{}, web.Error(http.StatusForbidden, "That order belongs to someone else.").Wrap(errors.New("owner is bob"))
		case in.Number == 13:
			return order{}, errors.New("connection refused")
		}
		return order{Number: in.Number, Status: "shipped"}, nil
	})

func TestTools(t *testing.T) {
	agent := ai.Agent{Name: "support", Instructions: "You help with orders.", Tools: []ai.Tool{orderTool}}
	ctx, f := fake(
		ai.FakeToolCall("find_order", map[string]any{"number": 1042}),
		ai.FakeText("It shipped."),
	)
	ctx = context.WithValue(ctx, userKey{}, "ada")
	res, err := agent.Prompt(ctx, "Where is order 1042?")
	check(t, err)
	if res.Text() != "It shipped." || len(res.Steps) != 2 || len(res.Messages) != 4 {
		t.Fatalf("result: %q, %d steps, %d messages", res.Text(), len(res.Steps), len(res.Messages))
	}
	tr := res.Messages[2]
	if tr.Role != ai.RoleTool || len(tr.Parts) != 1 {
		t.Fatalf("tool message: %+v", tr)
	}
	r := tr.Parts[0].(ai.ToolResult)
	if r.Name != "find_order" || r.IsError || r.Content != `{"number":1042,"status":"shipped"}` || r.CallID != res.Messages[1].ToolCalls()[0].ID {
		t.Errorf("tool result: %+v", r)
	}
	reqs := f.Requests()
	if len(reqs[0].Tools) != 1 || reqs[0].Tools[0].Name != "find_order" || reqs[0].System != "You help with orders." {
		t.Errorf("first request: %+v", reqs[0])
	}
	if in, _ := json.Marshal(reqs[0].Tools[0].Input); !strings.Contains(string(in), `"description":"The order's number"`) {
		t.Errorf("tool schema: %s", in)
	}
	if len(reqs[1].Messages) != 3 {
		t.Errorf("second request: %d messages", len(reqs[1].Messages))
	}

	// What the model is told when a tool refuses, and when its input is
	// wrong: as a web client would see it, never the internal cause.
	for _, c := range []struct {
		name  string
		input any
		user  string
		want  string
	}{
		{"forbidden", map[string]any{"number": 7}, "ada", "Forbidden: That order belongs to someone else."},
		{"unauthenticated", map[string]any{"number": 1}, "", "Unauthorized"},
		{"invalid", map[string]any{"number": 0}, "ada", "Unprocessable Entity\n- number: The number field is required."},
		{"not JSON of the schema", map[string]any{"number": "one"}, "ada", "Unprocessable Entity: number must be a whole number"},
		{"unknown tool", nil, "ada", `There is no tool named "nope".`},
	} {
		name := "find_order"
		if c.name == "unknown tool" {
			name = "nope"
		}
		ctx, _ := fake(ai.FakeToolCall(name, c.input), ai.FakeText("Sorry."))
		ctx = context.WithValue(ctx, userKey{}, c.user)
		res, err := agent.Prompt(ctx, "Order?")
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		r := res.Messages[2].Parts[0].(ai.ToolResult)
		if !r.IsError || !strings.HasPrefix(r.Content, c.want) || strings.Contains(r.Content, "bob") {
			t.Errorf("%s: %+v", c.name, r)
		}
	}

	// Other errors stop the call.
	ctx, _ = fake(ai.FakeToolCall("find_order", map[string]any{"number": 13}), ai.FakeText("never"))
	ctx = context.WithValue(ctx, userKey{}, "ada")
	res, err = agent.Prompt(ctx, "Order 13?")
	if err == nil || !strings.Contains(err.Error(), "ai: tool find_order: connection refused") || len(res.Steps) != 1 {
		t.Errorf("tool failure: %v, %+v", err, res)
	}

	// The step limit.
	ctx, _ = fake(ai.FakeToolCall("find_order", map[string]any{"number": 1}), ai.FakeToolCall("find_order", map[string]any{"number": 2}))
	ctx = context.WithValue(ctx, userKey{}, "ada")
	res, err = agent.Prompt(ctx, "Loop", ai.MaxSteps(2))
	if !errors.Is(err, ai.ErrMaxSteps) || len(res.Steps) != 2 {
		t.Errorf("max steps: %v, %d steps", err, len(res.Steps))
	}

	// Two tools of one name.
	if _, err := agent.Prompt(ctx, "x", ai.Tools(orderTool)); err == nil || !strings.Contains(err.Error(), "two tools") {
		t.Errorf("duplicate tools: %v", err)
	}
}

func TestFuncPanics(t *testing.T) {
	for name, f := range map[string]func(){
		"bad name":   func() { ai.Func("find order", "", func(context.Context, findOrder) (int, error) { return 0, nil }) },
		"not struct": func() { ai.Func("x", "", func(context.Context, int) (int, error) { return 0, nil }) },
		"bad rule": func() {
			ai.Func("x", "", func(context.Context, struct {
				N int `validate:"nonesuch"`
			}) (int, error) {
				return 0, nil
			})
		},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			f()
		}()
	}
	// No input: an empty object, or nothing at all.
	tool := ai.Func("now", "The time", func(context.Context, struct{}) (string, error) { return "noon", nil })
	for _, in := range []string{"", "null", "{}"} {
		if out, err := tool.Call(context.Background(), json.RawMessage(in)); err != nil || out != "noon" {
			t.Errorf("input %q: %q, %v", in, out, err)
		}
	}
}

func TestGenerateObject(t *testing.T) {
	ctx, f := fake(ai.FakeObject(summary{Title: "Go", Tags: []string{"go"}, Lang: "en"}))
	sum, res, err := ai.GenerateObject[summary](ctx, "Summarize")
	check(t, err)
	if sum.Title != "Go" || len(res.Steps) != 1 {
		t.Errorf("object: %+v", sum)
	}
	r := f.Requests()[0]
	if r.Output == nil || r.Output.Name != "summary" || r.Output.Schema.Type != "object" {
		t.Errorf("output spec: %+v", r.Output)
	}

	// A wrong answer is sent back once, with what's wrong.
	ctx, f = fake(
		ai.FakeText("```json\n{\"title\": \"A title that is far too long\", \"lang\": \"fr\"}\n```"),
		ai.FakeObject(summary{Title: "Short", Lang: "bn"}),
	)
	sum, res, err = ai.GenerateObject[summary](ctx, "Summarize")
	check(t, err)
	if sum.Title != "Short" || len(res.Steps) != 2 || res.Usage.OutputTokens == 0 {
		t.Errorf("retried: %+v, %+v", sum, res)
	}
	retry := f.Requests()[1].Prompt()
	if !strings.Contains(retry, "title: The title field must not be greater than 20 characters") || !strings.Contains(retry, "lang:") {
		t.Errorf("retry prompt: %s", retry)
	}

	// Twice wrong: an OutputError, 502 for the web.
	ctx, _ = fake(ai.FakeText("not JSON"), ai.FakeText(`{"title": ""}`))
	_, res, err = ai.GenerateObject[summary](ctx, "Summarize")
	oe, ok := errors.AsType[*ai.OutputError](err)
	if !ok || web.StatusOf(err) != http.StatusBadGateway || oe.Text != `{"title": ""}` || len(res.Steps) != 2 {
		t.Fatalf("twice wrong: %v", err)
	}
	if _, ok := errors.AsType[*validate.Errors](err); !ok {
		t.Errorf("not a validation error: %v", err)
	}

	// Cut off: no retry.
	ctx, _ = fake(func(*ai.Request) (*ai.Response, error) {
		return &ai.Response{Message: ai.AssistantMessage(`{"title": "Go`), Stop: ai.StopMaxTokens}, nil
	})
	if _, _, err := ai.GenerateObject[summary](ctx, "Summarize"); err == nil || !strings.Contains(err.Error(), "cut off") {
		t.Errorf("cut off: %v", err)
	}
	ctx, f = fake(func(*ai.Request) (*ai.Response, error) {
		return &ai.Response{Message: ai.AssistantMessage("I can't help with that."), Stop: ai.StopRefusal}, nil
	}, ai.FakeText("never"))
	if _, _, err := ai.GenerateObject[summary](ctx, "Summarize"); err == nil || !strings.Contains(err.Error(), "declined") || f.Remaining() != 1 {
		t.Errorf("refusal: %v", err)
	}
	if _, _, err := ai.GenerateObject[[]string](ctx, "List"); err == nil {
		t.Error("a slice: no error")
	}
}

func TestStream(t *testing.T) {
	ctx, _ := fake(ai.FakeToolCall("find_order", map[string]any{"number": 5}), ai.FakeText("It shipped yesterday."))
	ctx = context.WithValue(ctx, userKey{}, "ada")
	var kinds []string
	var text strings.Builder
	var result *ai.Result
	for ev, err := range ai.Stream(ctx, "Order 5?", ai.Tools(orderTool)) {
		check(t, err)
		kinds = append(kinds, ev.Kind.String())
		switch ev.Kind {
		case ai.EventText:
			text.WriteString(ev.Text)
		case ai.EventDone:
			result = ev.Result
		}
	}
	want := []string{"tool_call", "response", "tool_result", "text", "text", "text", "response", "done"}
	if !slices.Equal(kinds, want) || text.String() != "It shipped yesterday." || result == nil || result.Text() != "It shipped yesterday." {
		t.Errorf("events %v, text %q, result %+v", kinds, text.String(), result)
	}

	// Breaking out stops the call: no further request.
	ctx, f := fake(ai.FakeText("one two three"), ai.FakeText("never"))
	for ev, err := range ai.Stream(ctx, "Count") {
		check(t, err)
		if ev.Kind == ai.EventText {
			break
		}
	}
	if len(f.Requests()) != 1 {
		t.Errorf("%d requests after break", len(f.Requests()))
	}
	// An error ends the stream.
	ctx, _ = fake(ai.FakeError(errors.New("down")))
	var errs []error
	for _, err := range ai.Stream(ctx, "Hi") {
		errs = append(errs, err)
	}
	if len(errs) != 1 || errs[0] == nil {
		t.Errorf("errors: %v", errs)
	}
}

func TestMessageJSON(t *testing.T) {
	msgs := []ai.Message{
		ai.UserMessage("Where is 1042?"),
		{Role: ai.RoleAssistant, Parts: []ai.Part{ai.Text("Looking."), ai.ToolCall{ID: "c1", Name: "find_order", Input: json.RawMessage(`{"number":1042}`)}}},
		{Role: ai.RoleTool, Parts: []ai.Part{ai.ToolResult{CallID: "c1", Name: "find_order", Content: "Not Found", IsError: true}}},
	}
	data, err := json.Marshal(msgs)
	check(t, err)
	var back []ai.Message
	check(t, json.Unmarshal(data, &back))
	again, err := json.Marshal(back)
	check(t, err)
	if !bytes.Equal(data, again) || back[1].Text() != "Looking." || back[2].Parts[0].(ai.ToolResult).Content != "Not Found" {
		t.Errorf("round trip:\n%s\n%s", data, again)
	}
	if err := json.Unmarshal([]byte(`{"role":"system","parts":[]}`), &back[0]); err == nil {
		t.Error("unknown role: no error")
	}
	if err := json.Unmarshal([]byte(`{"role":"user","parts":[{"type":"image"}]}`), &back[0]); err == nil {
		t.Error("unknown part: no error")
	}
}

func newApp(t *testing.T, env config.Map, logs io.Writer) *anetos.App {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}
	maps.Copy(src, env)
	if logs == nil {
		logs = io.Discard
	}
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(logs))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestForApp(t *testing.T) {
	if _, err := ai.ForApp(newApp(t, nil, nil)); err == nil || !strings.Contains(err.Error(), "AI_PROVIDER isn't set: set it to one of [fake]") {
		t.Errorf("unset: %v", err)
	}
	if _, err := ai.ForApp(newApp(t, config.Map{"AI_PROVIDER": "anthropic"}, nil)); err == nil || !strings.Contains(err.Error(), `AI_PROVIDER is "anthropic"`) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := ai.ForApp(newApp(t, config.Map{"AI_PROVIDER": "fake", "AI_MAX_TOKENS": "0"}, nil)); err == nil {
		t.Error("AI_MAX_TOKENS=0: no error")
	}

	var logs bytes.Buffer
	app := newApp(t, config.Map{"AI_PROVIDER": "fake", "AI_MODEL": "m1", "AI_MAX_TOKENS": "50"}, &logs)
	var units []anetos.Unit
	app.AroundUnits(func(ctx context.Context, u anetos.Unit) (context.Context, func()) {
		units = append(units, u)
		return ctx, nil
	})
	c, err := ai.ForApp(app)
	check(t, err)
	if _, err := ai.ForApp(app); err == nil {
		t.Error("ForApp twice: no error")
	}
	f := c.Fake(ai.FakeToolCall("find_order", findOrder{Number: 3}), ai.FakeText("Done."))
	if c.Provider() != f || c.Fake() != f {
		t.Error("Fake doesn't replace the provider once")
	}
	ctx := context.WithValue(app.Context(context.Background()), userKey{}, "ada")
	res, err := ai.Generate(ctx, "Order 3?", ai.Tools(orderTool))
	check(t, err)
	if res.Text() != "Done." {
		t.Errorf("text: %q", res.Text())
	}
	if r := f.Requests()[0]; r.Model != "m1" || r.MaxTokens != 50 {
		t.Errorf("defaults: %+v", r)
	}
	if !slices.Equal(units, []anetos.Unit{{Kind: "tool", Name: "find_order"}}) {
		t.Errorf("units: %+v", units)
	}
	out := logs.String()
	for _, want := range []string{"ai: response", "component=ai", "input_tokens=", "ai: tool", "tool=find_order"} {
		if !strings.Contains(out, want) {
			t.Errorf("logs lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Order 3?") {
		t.Errorf("the prompt was logged:\n%s", out)
	}
}

func TestOutputNames(t *testing.T) {
	type OrderSummary struct {
		A string `json:"a"`
	}
	type HTTPStatusReport struct {
		A string `json:"a"`
	}
	ctx, f := fake(ai.FakeObject(OrderSummary{A: "x"}), ai.FakeObject(HTTPStatusReport{A: "x"}), ai.FakeObject(struct {
		A string `json:"a"`
	}{"x"}))
	_, _, err := ai.GenerateObject[OrderSummary](ctx, "x")
	check(t, err)
	_, _, err = ai.GenerateObject[HTTPStatusReport](ctx, "x")
	check(t, err)
	_, _, err = ai.GenerateObject[struct {
		A string `json:"a"`
	}](ctx, "x")
	check(t, err)
	var names []string
	for _, r := range f.Requests() {
		names = append(names, r.Output.Name)
	}
	if !slices.Equal(names, []string{"order_summary", "http_status_report", "output"}) {
		t.Errorf("names: %q", names)
	}
}

// slowProvider answers when its context ends, with the context's error.
type slowProvider struct{ ai.Fake }

func (p *slowProvider) Generate(ctx context.Context, _ *ai.Request) (*ai.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestEdges(t *testing.T) {
	// An empty answer can be continued, and retried.
	empty := func(*ai.Request) (*ai.Response, error) {
		return &ai.Response{Message: ai.Message{Role: ai.RoleAssistant}}, nil
	}
	ctx, _ := fake(empty, ai.FakeText("Now with words."))
	res, err := ai.Generate(ctx, "Say nothing")
	check(t, err)
	if res, err = ai.Generate(ctx, "Say something", ai.Messages(res.Messages...)); err != nil || res.Text() != "Now with words." {
		t.Errorf("continued after an empty answer: %v", err)
	}
	ctx, _ = fake(empty, ai.FakeObject(summary{Title: "T", Lang: "en"}))
	if sum, res, err := ai.GenerateObject[summary](ctx, "Summarize"); err != nil || sum.Title != "T" || len(res.Steps) != 2 {
		t.Errorf("retried after an empty answer: %v", err)
	}

	// The retry continues the conversation given with Messages, and an
	// agent's tools work with typed answers.
	ctx, f := fake(
		ai.FakeToolCall("find_order", findOrder{Number: 1}),
		ai.FakeText(`{"title": ""}`),
		ai.FakeObject(summary{Title: "Order 1", Lang: "en"}),
	)
	ctx = context.WithValue(ctx, userKey{}, "ada")
	agent := ai.Agent{Tools: []ai.Tool{orderTool}}
	sum, res, err := ai.GenerateObject[summary](ctx, "Summarize order 1", agent, ai.Messages(ai.UserMessage("Hi"), ai.AssistantMessage("Hello.")))
	check(t, err)
	if sum.Title != "Order 1" || len(res.Steps) != 3 || len(res.Messages) != 8 {
		t.Errorf("object with tools: %+v, %d steps, %d messages", sum, len(res.Steps), len(res.Messages))
	}
	if r := f.Requests()[2]; len(r.Messages) != 7 || r.Messages[0].Text() != "Hi" || len(r.Tools) != 1 || r.Output == nil {
		t.Errorf("retry request: %d messages, %+v", len(r.Messages), r)
	}

	// Options: their order, the provider's own, Agent.Stream.
	type anthropicOptions struct{ Thinking bool }
	sysAgent := ai.Agent{Name: "a", Instructions: "agent"}
	ctx, f = fake(ai.FakeText("ok"), ai.FakeText("streamed"))
	_, err = ai.Generate(ctx, "x", ai.System("call"), sysAgent, ai.ProviderOptions(anthropicOptions{Thinking: true}))
	check(t, err)
	if r := f.Requests()[0]; r.System != "call\n\nagent" || r.Options != (anthropicOptions{Thinking: true}) {
		t.Errorf("options: %q, %+v", r.System, r.Options)
	}
	var text strings.Builder
	for ev, err := range sysAgent.Stream(ctx, "y") {
		check(t, err)
		text.WriteString(ev.Text)
	}
	if text.String() != "streamed" || f.Requests()[1].System != "agent" {
		t.Errorf("Agent.Stream: %q", text.String())
	}

	// Timeouts: each request's.
	c := ai.New(&slowProvider{}, ai.Timeout(10*time.Millisecond))
	_, err = ai.Generate(context.Background(), "Hi", ai.Using(c))
	if !errors.Is(err, context.DeadlineExceeded) || web.StatusOf(err) != http.StatusServiceUnavailable {
		t.Errorf("timeout: %v", err)
	}

	// A reply without a response.
	ctx, _ = fake(func(*ai.Request) (*ai.Response, error) { return nil, nil })
	if _, err := ai.Generate(ctx, "Hi"); err == nil || !strings.Contains(err.Error(), "returned no response") {
		t.Errorf("nil reply: %v", err)
	}

	// A model's broken tool input still marshals, as a string.
	broken := ai.Message{Role: ai.RoleAssistant, Parts: []ai.Part{ai.ToolCall{ID: "c", Name: "t", Input: json.RawMessage(`{"number":`)}}}
	if data, err := json.Marshal(broken); err != nil || !strings.Contains(string(data), `"input":"{\"number\":"`) {
		t.Errorf("broken input: %s, %v", data, err)
	}
}

type title string

// statusErr is a domain error with a status, wrapping a cause.
type statusErr struct {
	status int
	cause  error
}

func (e statusErr) Error() string   { return "lookup: " + e.cause.Error() }
func (e statusErr) Unwrap() error   { return e.cause }
func (e statusErr) HTTPStatus() int { return e.status }

func TestToolErrorsForTheModel(t *testing.T) {
	for _, c := range []struct {
		name string
		err  error
		want string
	}{
		{"status only", statusErr{http.StatusNotFound, web.Error(http.StatusInternalServerError, "db at 10.0.0.5 down")}, "Not Found"},
		{"input error made by hand", &ai.InputError{Err: errors.New("pq: password authentication failed")}, "Unprocessable Entity"},
		{"joined validation", errors.Join(errors.New("x"), validate.Fail("email", "The email is taken.")), "Unprocessable Entity\n- email: The email is taken."},
		{"HTTP error with a cause", web.Error(http.StatusConflict, "Already booked.").Wrap(errors.New("unique violation on bookings_pkey")), "Conflict: Already booked."},
		{"HTTP error with fields", &web.HTTPError{Status: http.StatusUnprocessableEntity, Message: "Check the dates.", Fields: map[string]string{"from": "The from field must be a date."}},
			"Unprocessable Entity: Check the dates.\n- from: The from field must be a date."},
		{"HTTP error around a validation error", web.Error(http.StatusUnprocessableEntity, "").Wrap(validate.Fail("email", "The email is taken.")),
			"Unprocessable Entity\n- email: The email is taken."},
	} {
		tool := ai.Func("t", "", func(context.Context, struct{}) (title, error) { return "", c.err })
		ctx, _ := fake(ai.FakeToolCall("t", struct{}{}), ai.FakeText("ok"))
		res, err := ai.Generate(ctx, "go", ai.Tools(tool))
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if r := res.Messages[2].Parts[0].(ai.ToolResult); !r.IsError || r.Content != c.want {
			t.Errorf("%s: %q, want %q", c.name, r.Content, c.want)
		}
	}
	// A string type's output is sent as it is.
	tool := ai.Func("t", "", func(context.Context, struct{}) (title, error) { return "Dune", nil })
	if out, err := tool.Call(context.Background(), nil); err != nil || out != "Dune" {
		t.Errorf("string type: %q, %v", out, err)
	}
}

func TestLogs(t *testing.T) {
	var logs bytes.Buffer
	app := newApp(t, config.Map{"AI_PROVIDER": "fake"}, &logs)
	c, err := ai.ForApp(app)
	check(t, err)
	c.Fake(ai.FakeToolCall("nope", nil), ai.FakeText("done"))
	ctx := app.Context(context.Background())
	_, err = ai.Agent{Name: "helper"}.Prompt(ctx, "my secret question")
	check(t, err)
	_, err = ai.Generate(ctx, "another secret")
	if err == nil {
		t.Fatal("no error without a reply")
	}
	out := logs.String()
	for _, want := range []string{"agent=helper", `msg="ai: the model called a tool that doesn't exist"`, "tool=nope", `msg="ai: request failed"`} {
		if !strings.Contains(out, want) {
			t.Errorf("logs lack %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret") {
		t.Errorf("a prompt was logged:\n%s", out)
	}
}
