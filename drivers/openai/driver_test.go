// SPDX-License-Identifier: Apache-2.0

package openai_test

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/openai"
	"anetos.dev/anetos/encryption"
)

func TestDrivers(t *testing.T) {
	for _, c := range []struct {
		env  config.Map
		want string
	}{
		{config.Map{"AI_PROVIDER": "openai", "AI_MODEL": "m"}, "OPENAI_API_KEY isn't set"},
		{config.Map{"AI_PROVIDER": "openai", "OPENAI_API_KEY": "k"}, "AI_MODEL isn't set"},
		{config.Map{"AI_PROVIDER": "openai", "OPENAI_API_KEY": "k", "AI_MODEL": "m"}, ""},
		{config.Map{"AI_PROVIDER": "openai-compatible", "AI_MODEL": "m"}, "OPENAI_COMPATIBLE_URL isn't set"},
		{config.Map{"AI_PROVIDER": "openai-compatible", "OPENAI_COMPATIBLE_URL": "http://localhost:11434/v1"}, "AI_MODEL isn't set"},
		{config.Map{"AI_PROVIDER": "openai-compatible", "OPENAI_COMPATIBLE_URL": "http://localhost:11434/v1", "AI_MODEL": "m"}, ""},
	} {
		src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}
		maps.Copy(src, c.env)
		app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		client, err := ai.ForApp(app, openai.Driver(), openai.CompatibleDriver())
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: %v", c.env, err)
		case c.want == "" && client.Provider().Name() != c.env["AI_PROVIDER"]:
			t.Errorf("provider %s", client.Provider().Name())
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%v: %v, want %q", c.env, err, c.want)
		}
		_ = app.Close()
	}
}

// server answers each request with the next reply, and records the
// requests.
func server(t *testing.T, replies ...string) (string, *[]*http.Request, *[]map[string]any) {
	t.Helper()
	var reqs []*http.Request
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		reqs, bodies = append(reqs, r), append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replies[len(bodies)-1])
	}))
	t.Cleanup(srv.Close)
	return srv.URL, &reqs, &bodies
}

const refusal = `{"id":"c1","object":"chat.completion","created":1,"model":"gpt-x","choices":[{"index":0,"finish_reason":"stop",
	"message":{"role":"assistant","content":null,"refusal":"I can't help with that."}}],"usage":{"prompt_tokens":9,"completion_tokens":6,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4}}}`

func TestRequests(t *testing.T) {
	url, _, bodies := server(t, refusal, refusal)
	p := openai.New("k", option.WithBaseURL(url), option.WithMaxRetries(0), option.WithUnsafeAllowHTTP())
	type withMap struct {
		Tags map[string]string `json:"tags"`
	}
	s, _ := ai.SchemaFor[withMap]()
	resp, err := p.Generate(context.Background(), &ai.Request{
		Model: "gpt-x", MaxTokens: 100, System: "Be brief.",
		Messages: []ai.Message{
			ai.UserMessage("Tag this."),
			{Role: ai.RoleAssistant, Parts: []ai.Part{ai.Reasoning{Provider: "anthropic", Data: "x"}, ai.ToolCall{ID: "c1", Name: "lookup"}}},
			{Role: ai.RoleTool, Parts: []ai.Part{ai.ToolResult{CallID: "c1", Name: "lookup", Content: "Not Found", IsError: true}}},
		},
		Output:  &ai.OutputSpec{Name: "tags", Schema: s},
		Options: openai.Options{ReasoningEffort: "low", Params: func(p *sdk.ChatCompletionNewParams) { p.Seed = sdk.Int(7) }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Stop != ai.StopRefusal || resp.Text() != "I can't help with that." || resp.Usage != (ai.Usage{InputTokens: 9, OutputTokens: 6, CacheReadTokens: 4}) {
		t.Errorf("response %+v", resp)
	}
	b := (*bodies)[0]
	data, _ := json.Marshal(b["messages"])
	want := `[{"content":"Be brief.","role":"system"},{"content":"Tag this.","role":"user"},` +
		`{"role":"assistant","tool_calls":[{"function":{"arguments":"{}","name":"lookup"},"id":"c1","type":"function"}]},` +
		`{"content":"Error: Not Found","role":"tool","tool_call_id":"c1"}]`
	if string(data) != want {
		t.Errorf("messages:\n%s\nwant\n%s", data, want)
	}
	rf, _ := b["response_format"].(map[string]any)
	js, _ := rf["json_schema"].(map[string]any)
	if js["strict"] != false || b["reasoning_effort"] != "low" || b["seed"] != float64(7) || b["max_completion_tokens"] != float64(100) {
		t.Errorf("request: %v", b)
	}

	// A compatible server gets max_tokens.
	cp := openai.NewCompatible(url, "", option.WithMaxRetries(0))
	if _, err := cp.Generate(context.Background(), &ai.Request{Model: "m", MaxTokens: 50, Messages: []ai.Message{ai.UserMessage("x")}}); err != nil {
		t.Fatal(err)
	}
	if b := (*bodies)[1]; b["max_tokens"] != float64(50) || b["max_completion_tokens"] != nil {
		t.Errorf("compatible request: %v", b)
	}
}

func TestCompatibleSendsNoOpenAIKey(t *testing.T) {
	for k, v := range map[string]string{
		"OPENAI_API_KEY": "sk-secret", "OPENAI_ADMIN_KEY": "sk-admin", "OPENAI_ORG_ID": "org-secret", "OPENAI_PROJECT_ID": "proj-secret",
		"OPENAI_CUSTOM_HEADERS": "Helicone-Auth: Bearer sk-helicone\nX-Team-Secret: s3cret", "OPENAI_WEBHOOK_SECRET": "whsec",
	} {
		t.Setenv(k, v)
	}
	url, reqs, _ := server(t, refusal, refusal)
	cp := openai.NewCompatible(url, "", option.WithMaxRetries(0))
	if _, err := cp.Generate(context.Background(), &ai.Request{Model: "m", Messages: []ai.Message{ai.UserMessage("x")}}); err != nil {
		t.Fatal(err)
	}
	r := (*reqs)[0]
	for name, vs := range r.Header {
		for _, v := range vs {
			if strings.Contains(v, "secret") || strings.Contains(v, "sk-") || strings.Contains(v, "whsec") {
				t.Errorf("header %s: %q", name, v)
			}
		}
	}
	// With its own key, on this machine, over HTTP.
	cp = openai.NewCompatible(url, "mine", option.WithMaxRetries(0))
	if _, err := cp.Generate(context.Background(), &ai.Request{Model: "m", Messages: []ai.Message{ai.UserMessage("x")}}); err != nil {
		t.Fatal(err)
	}
	if got := (*reqs)[1].Header.Get("Authorization"); got != "Bearer mine" {
		t.Errorf("Authorization %q", got)
	}
}

func TestStreamToolCallsWithoutIndexes(t *testing.T) {
	chunk := func(delta string) string {
		return `data: {"id":"s","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":` + delta + `,"finish_reason":null}]}` + "\n\n"
	}
	body := chunk(`{"role":"assistant","tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"a\"}"}}]}`) +
		chunk(`{"tool_calls":[{"index":0,"id":"call_b","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}`) +
		chunk(`{"tool_calls":[{"index":0,"function":{"arguments":"\"b\"}"}}]}`) +
		`data: {"id":"s","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	p := openai.NewCompatible(srv.URL, "", option.WithMaxRetries(0))
	var resp *ai.Response
	for ev, err := range p.Stream(context.Background(), &ai.Request{Model: "m", Messages: []ai.Message{ai.UserMessage("x")}}) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind == ai.EventResponse {
			resp = ev.Response
		}
	}
	calls := resp.ToolCalls()
	if len(calls) != 2 || calls[0].ID != "call_a" || string(calls[1].Input) != `{"q":"b"}` || resp.Stop != ai.StopToolCalls {
		t.Errorf("calls %+v", calls)
	}
}
