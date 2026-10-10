// SPDX-License-Identifier: Apache-2.0

package gemini_test

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/genai"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/gemini"
	"anetos.dev/anetos/encryption"
)

func TestDriver(t *testing.T) {
	for _, c := range []struct {
		env  config.Map
		want string
	}{
		{config.Map{"AI_MODEL": "m"}, "GEMINI_API_KEY isn't set"},
		{config.Map{"GEMINI_API_KEY": "k"}, "AI_MODEL isn't set"},
		{config.Map{"GEMINI_API_KEY": "k", "AI_MODEL": "m"}, ""},
	} {
		src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "AI_PROVIDER": "gemini"}
		maps.Copy(src, c.env)
		app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
		if err != nil {
			t.Fatal(err)
		}
		client, err := ai.New(app, gemini.Driver())
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: %v", c.env, err)
		case c.want == "" && client.Provider().Name() != "gemini":
			t.Errorf("provider %s", client.Provider().Name())
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%v: %v, want %q", c.env, err, c.want)
		}
		_ = app.Close()
	}
}

func TestRequests(t *testing.T) {
	replies := []string{
		// Two calls without IDs, a thought, and a signature.
		`{"candidates":[{"content":{"role":"model","parts":[{"text":"Checking.","thought":true,"thoughtSignature":"c2ln"},
			{"functionCall":{"name":"lookup","args":{"q":"a"}}},{"functionCall":{"name":"lookup","args":{"q":"b"}}}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":10,"toolUsePromptTokenCount":2,"candidatesTokenCount":3,"thoughtsTokenCount":4,"cachedContentTokenCount":5},"modelVersion":"gemini-x"}`,
		`{"promptFeedback":{"blockReason":"SAFETY"},"usageMetadata":{"promptTokenCount":10}}`,
	}
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, replies[len(bodies)-1])
	}))
	defer srv.Close()
	p, err := gemini.New(context.Background(), genai.ClientConfig{APIKey: "k", HTTPClient: srv.Client(), HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	budget := int32(0)
	req := &ai.Request{Model: "gemini-x", Messages: []ai.Message{ai.UserMessage("Look up a and b.")},
		// Another driver's options are ignored; each of the driver's own
		// applies, the deprecated Config too.
		Options: []any{struct{ Other bool }{true}, gemini.Options{ThinkingBudget: &budget},
			&gemini.Options{Config: func(c *genai.GenerateContentConfig) { c.CandidateCount = 1 }}, //nolint:staticcheck // the deprecated field still applies
			gemini.Options{Params: func(c *genai.GenerateContentConfig) { c.MaxOutputTokens = 99 }}}}
	resp, err := p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	calls := resp.ToolCalls()
	if len(calls) != 2 || calls[0].ID == calls[1].ID || resp.Stop != ai.StopToolCalls ||
		resp.Usage != (ai.Usage{InputTokens: 12, OutputTokens: 7, CacheReadTokens: 5}) || resp.Model != "gemini-x" {
		t.Fatalf("response %+v", resp)
	}
	if r, ok := resp.Message.Parts[0].(ai.Reasoning); !ok || r.Text != "Checking." || r.Data != "c2ln" {
		t.Errorf("reasoning %+v", resp.Message.Parts[0])
	}
	gc, _ := bodies[0]["generationConfig"].(map[string]any)
	if tc, _ := gc["thinkingConfig"].(map[string]any); tc["thinkingBudget"] != float64(0) || gc["candidateCount"] != float64(1) || gc["maxOutputTokens"] != float64(99) {
		t.Errorf("config: %v", gc)
	}

	// Back: the thought with its signature, calls without the made-up
	// IDs, results under "output".
	req.Messages = append(req.Messages, resp.Message, ai.Message{Role: ai.RoleTool, Parts: []ai.Part{
		ai.ToolResult{CallID: calls[0].ID, Name: "lookup", Content: `[1,2]`},
		ai.ToolResult{CallID: calls[1].ID, Name: "lookup", Content: "Not Found", IsError: true},
	}})
	resp, err = p.Generate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Stop != ai.StopRefusal {
		t.Errorf("blocked prompt: stop %s", resp.Stop)
	}
	data, _ := json.Marshal(bodies[1]["contents"])
	want := `[{"parts":[{"text":"Look up a and b."}],"role":"user"},` +
		`{"parts":[{"text":"Checking.","thought":true,"thoughtSignature":"c2ln"},{"functionCall":{"args":{"q":"a"},"name":"lookup"}},{"functionCall":{"args":{"q":"b"},"name":"lookup"}}],"role":"model"},` +
		`{"parts":[{"functionResponse":{"name":"lookup","response":{"output":[1,2]}}},{"functionResponse":{"name":"lookup","response":{"error":"Not Found"}}}],"role":"user"}]`
	if string(data) != want {
		t.Errorf("contents:\n%s\nwant\n%s", data, want)
	}
}

func TestStreamSignatures(t *testing.T) {
	// A stream's last, empty part carries the signature: it goes back on
	// the text.
	body := `data: {"candidates":[{"content":{"role":"model","parts":[{"text":"Hel"}]}}]}` + "\r\n\r\n" +
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"lo"}]}}]}` + "\r\n\r\n" +
		`data: {"candidates":[{"content":{"role":"model","parts":[{"text":"","thoughtSignature":"c2ln"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":3,"candidatesTokenCount":2}}` + "\r\n\r\n"
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	p, err := gemini.New(context.Background(), genai.ClientConfig{APIKey: "k", HTTPClient: srv.Client(), HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	req := &ai.Request{Model: "g", Messages: []ai.Message{ai.UserMessage("Hi")}}
	var resp *ai.Response
	for ev, err := range p.Stream(context.Background(), req) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind == ai.EventResponse {
			resp = ev.Response
		}
	}
	if resp.Text() != "Hello" || resp.Stop != ai.StopEnd {
		t.Fatalf("response %+v", resp)
	}
	req.Messages = append(req.Messages, resp.Message, ai.UserMessage("And?"))
	for _, err := range p.Stream(context.Background(), req) {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, _ := json.Marshal(bodies[1]["contents"].([]any)[1])
	if string(data) != `{"parts":[{"text":"Hello","thoughtSignature":"c2ln"}],"role":"model"}` {
		t.Errorf("model content: %s", data)
	}
}

func TestOtherProvidersCalls(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()
	p, err := gemini.New(context.Background(), genai.ClientConfig{APIKey: "k", HTTPClient: srv.Client(), HTTPOptions: genai.HTTPOptions{BaseURL: srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	// Claude's call: the first gets Gemini's placeholder signature.
	_, err = p.Generate(context.Background(), &ai.Request{Model: "g", Messages: []ai.Message{
		ai.UserMessage("x"),
		{Role: ai.RoleAssistant, Parts: []ai.Part{ai.Reasoning{Provider: "anthropic", Data: "s"}, ai.ToolCall{ID: "toolu_1", Name: "f"}, ai.ToolCall{ID: "toolu_2", Name: "f"}}},
		{Role: ai.RoleTool, Parts: []ai.Part{ai.ToolResult{CallID: "toolu_1", Name: "f", Content: "1"}, ai.ToolResult{CallID: "toolu_2", Name: "f", Content: "2"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(bodies[0]["contents"].([]any)[1])
	if string(data) != `{"parts":[{"functionCall":{"id":"toolu_1","name":"f"},"thoughtSignature":"skip/thought/signature/validator"},{"functionCall":{"id":"toolu_2","name":"f"}}],"role":"model"}` {
		t.Errorf("model content: %s", data)
	}
}
