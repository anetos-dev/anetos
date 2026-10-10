// SPDX-License-Identifier: Apache-2.0

package anthropic_test

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/anthropic"
	"anetos.dev/anetos/encryption"
)

func TestDriver(t *testing.T) {
	for _, c := range []struct {
		env  config.Map
		want string
	}{
		{config.Map{"AI_MODEL": "m"}, "ANTHROPIC_API_KEY isn't set"},
		{config.Map{"ANTHROPIC_API_KEY": "k"}, "AI_MODEL isn't set"},
		{config.Map{"ANTHROPIC_API_KEY": "k", "AI_MODEL": "m"}, ""},
	} {
		src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "AI_PROVIDER": "anthropic"}
		maps.Copy(src, c.env)
		app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		client, err := ai.New(app, anthropic.Driver())
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: %v", c.env, err)
		case c.want == "" && client.Provider().Name() != "anthropic":
			t.Errorf("provider %s", client.Provider().Name())
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%v: %v, want %q", c.env, err, c.want)
		}
		_ = app.Close()
	}
}

// server answers each request with the next reply, and records the
// requests' bodies and headers.
func server(t *testing.T, replies ...string) (*anthropic.Provider, *[]map[string]any) {
	t.Helper()
	p, bodies, _ := serverWithHeaders(t, replies...)
	return p, bodies
}

func serverWithHeaders(t *testing.T, replies ...string) (*anthropic.Provider, *[]map[string]any, *[]http.Header) {
	t.Helper()
	var bodies []map[string]any
	var headers []http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		headers = append(headers, r.Header.Clone())
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replies[len(bodies)-1])
	}))
	t.Cleanup(srv.Close)
	return anthropic.New("k", option.WithBaseURL(srv.URL), option.WithMaxRetries(0)), &bodies, &headers
}

const okAnswer = `{"id":"m","type":"message","role":"assistant","model":"claude-x","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"text","text":"ok"}]}`

func TestEdges(t *testing.T) {
	// The SDK's own environment isn't sent.
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "oauth-from-env")
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS", "X-Secret: s3cret")
	p, bodies, headers := serverWithHeaders(t, okAnswer, okAnswer)
	msgs := []ai.Message{
		ai.UserMessage("x"),
		{Role: ai.RoleAssistant, Parts: []ai.Part{ai.ToolCall{ID: "t1", Name: "f", Input: json.RawMessage(`"{\"q\": \"cut"`)}}},
		{Role: ai.RoleTool, Parts: []ai.Part{ai.ToolResult{CallID: "t1", Name: "f"}}},
	}
	// A long answer: the SDK wants a timeout of the caller's.
	if _, err := p.Generate(context.Background(), &ai.Request{Model: "claude-x", MaxTokens: 64000, Messages: msgs}); err != nil {
		t.Fatal(err)
	}
	h := (*headers)[0]
	if h.Get("Authorization") != "" || h.Get("X-Secret") != "" || h.Get("X-Api-Key") != "k" {
		t.Errorf("headers: %v", h)
	}
	data, _ := json.Marshal((*bodies)[0]["messages"])
	if !strings.Contains(string(data), `"input":{}`) || !strings.Contains(string(data), `{"is_error":false,"tool_use_id":"t1","type":"tool_result"}`) {
		t.Errorf("messages: %s", data)
	}
	// Structured output without fixed properties is refused before sending.
	type tags struct {
		Tags map[string]int `json:"tags"`
	}
	s, _ := ai.SchemaFor[tags]()
	_, err := p.Generate(context.Background(), &ai.Request{Model: "claude-x", Messages: msgs, Output: &ai.OutputSpec{Name: "tags", Schema: s}})
	if err == nil || !strings.Contains(err.Error(), "tags.tags is a map") || len(*bodies) != 1 {
		t.Errorf("map output: %v", err)
	}
}

func TestThinking(t *testing.T) {
	answer := `{"id":"m1","type":"message","role":"assistant","model":"claude-x","stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":5,"cache_creation_input_tokens":2},
		"content":[{"type":"thinking","thinking":"Weather needs the tool.","signature":"sig1"},{"type":"redacted_thinking","data":"opaque"},
		{"type":"tool_use","id":"t1","name":"get_weather","input":{"city":"Paris"}}]}`
	final := `{"id":"m2","type":"message","role":"assistant","model":"claude-x","stop_reason":"refusal","usage":{"input_tokens":1,"output_tokens":1},"content":[]}`
	p, bodies := server(t, answer, final)
	req := &ai.Request{Model: "claude-x", MaxTokens: 4096, Messages: []ai.Message{ai.UserMessage("Weather in Paris?")},
		Options: []any{anthropic.Options{ThinkingBudget: 2048, Params: func(p *sdk.MessageNewParams) { p.TopK = sdk.Int(5) }}}}
	resp, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Message.Parts) != 3 || resp.Stop != ai.StopToolCalls || resp.Usage != (ai.Usage{InputTokens: 17, OutputTokens: 20, CacheReadTokens: 5, CacheWriteTokens: 2}) {
		t.Fatalf("response %+v", resp)
	}
	if r, ok := resp.Message.Parts[0].(ai.Reasoning); !ok || r.Text != "Weather needs the tool." || r.Data != "sig1" || r.Provider != "anthropic" {
		t.Errorf("reasoning %+v", resp.Message.Parts[0])
	}
	b := (*bodies)[0]
	if th, _ := b["thinking"].(map[string]any); th["budget_tokens"] != float64(2048) || b["top_k"] != float64(5) {
		t.Errorf("options: %v", b)
	}

	// The reasoning goes back as it came; a tool result after a user
	// message merges into one user turn; other providers' reasoning is
	// left out.
	resp.Message.Parts = append([]ai.Part{ai.Reasoning{Provider: "gemini", Data: "x"}}, resp.Message.Parts...)
	req.Messages = append(req.Messages, resp.Message,
		ai.Message{Role: ai.RoleTool, Parts: []ai.Part{ai.ToolResult{CallID: "t1", Name: "get_weather", Content: "boom", IsError: true}}},
		ai.UserMessage("Well?"))
	resp, err = p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Stop != ai.StopRefusal {
		t.Errorf("stop %s", resp.Stop)
	}
	data, _ := json.Marshal((*bodies)[1]["messages"])
	want := `[{"content":[{"text":"Weather in Paris?","type":"text"}],"role":"user"},` +
		`{"content":[{"signature":"sig1","thinking":"Weather needs the tool.","type":"thinking"},{"data":"opaque","type":"redacted_thinking"},{"id":"t1","input":{"city":"Paris"},"name":"get_weather","type":"tool_use"}],"role":"assistant"},` +
		`{"content":[{"content":[{"text":"boom","type":"text"}],"is_error":true,"tool_use_id":"t1","type":"tool_result"},{"text":"Well?","type":"text"}],"role":"user"}]`
	if string(data) != want {
		t.Errorf("messages:\n%s\nwant\n%s", data, want)
	}
	if _, err := p.Generate(context.Background(), &ai.Request{Messages: req.Messages}); err == nil || !strings.Contains(err.Error(), "no model") {
		t.Errorf("no model: %v", err)
	}
}
