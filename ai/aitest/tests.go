// SPDX-License-Identifier: Apache-2.0

package aitest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/ai"
)

// test is one conformance test.
type test struct {
	name string
	run  func(t *testing.T, p ai.Provider, model string)
}

// tests are the conformance tests, in order. Their names are the
// cassettes' file names: renaming one needs a new recording.
var tests = []test{
	{"Text", testText},
	{"Stream", testStream},
	{"Conversation", testConversation},
	{"Tools", testTools},
	{"ToolsStream", testToolsStream},
	{"Output", testOutput},
	{"MaxTokens", testMaxTokens},
	{"Error", testError},
}

func ctxOf(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// collect runs a stream and returns its events, and its error.
func collect(t *testing.T, p ai.Provider, req *ai.Request) ([]ai.Event, error) {
	t.Helper()
	var evs []ai.Event
	for ev, err := range p.Stream(ctxOf(t), req) {
		if err != nil {
			return evs, err
		}
		evs = append(evs, ev)
	}
	return evs, nil
}

// checkResponse checks what every response has.
func checkResponse(t *testing.T, resp *ai.Response, stop ai.StopReason) {
	t.Helper()
	if resp == nil {
		t.Fatal("no response")
	}
	if resp.Message.Role != ai.RoleAssistant {
		t.Errorf("role %q, want assistant", resp.Message.Role)
	}
	if resp.Stop != stop {
		t.Errorf("stop %q, want %q", resp.Stop, stop)
	}
	if resp.Usage.InputTokens <= 0 || resp.Usage.OutputTokens <= 0 {
		t.Errorf("usage %+v: no tokens counted", resp.Usage)
	}
	if resp.Model == "" {
		t.Error("no model in the response")
	}
	if resp.Raw == nil {
		t.Error("no Raw response")
	}
}

func textRequest(model string) *ai.Request {
	return &ai.Request{
		Model:     model,
		System:    "You answer with one word.",
		Messages:  []ai.Message{ai.UserMessage("Reply with the word pong.")},
		MaxTokens: 1024,
	}
}

func testText(t *testing.T, p ai.Provider, model string) {
	resp, err := p.Generate(ctxOf(t), textRequest(model))
	if err != nil {
		t.Fatal(err)
	}
	checkResponse(t, resp, ai.StopEnd)
	if !strings.Contains(strings.ToLower(resp.Text()), "pong") {
		t.Errorf("text %q: no pong", resp.Text())
	}
	if len(resp.ToolCalls()) != 0 {
		t.Errorf("tool calls: %+v", resp.ToolCalls())
	}
}

func testStream(t *testing.T, p ai.Provider, model string) {
	evs, err := collect(t, p, textRequest(model))
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var resp *ai.Response
	for i, ev := range evs {
		switch ev.Kind {
		case ai.EventText:
			text.WriteString(ev.Text)
		case ai.EventResponse:
			if i != len(evs)-1 {
				t.Errorf("EventResponse is event %d of %d: it must be last", i+1, len(evs))
			}
			resp = ev.Response
		default:
			t.Errorf("event %d: unexpected %s", i+1, ev.Kind)
		}
	}
	checkResponse(t, resp, ai.StopEnd)
	if text.Len() == 0 || text.String() != resp.Text() {
		t.Errorf("streamed text %q, response text %q", text.String(), resp.Text())
	}
	if !strings.Contains(strings.ToLower(resp.Text()), "pong") {
		t.Errorf("text %q: no pong", resp.Text())
	}
}

func testConversation(t *testing.T, p ai.Provider, model string) {
	resp, err := p.Generate(ctxOf(t), &ai.Request{
		Model: model,
		Messages: []ai.Message{
			ai.UserMessage("My name is Ada."),
			ai.AssistantMessage("Nice to meet you, Ada."),
			ai.UserMessage("What's my name? Answer with the name only."),
		},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkResponse(t, resp, ai.StopEnd)
	if !strings.Contains(resp.Text(), "Ada") {
		t.Errorf("text %q: no Ada", resp.Text())
	}
}

type weatherInput struct {
	City string `json:"city" description:"The city's name" validate:"required"`
}

func toolsRequest(t *testing.T, model string) *ai.Request {
	s, err := ai.SchemaFor[weatherInput]()
	if err != nil {
		t.Fatal(err)
	}
	return &ai.Request{
		Model:     model,
		System:    "You use the tools you're given to answer.",
		Messages:  []ai.Message{ai.UserMessage("What's the weather in Paris? Use the get_weather tool.")},
		Tools:     []ai.ToolSpec{{Name: "get_weather", Description: "Today's weather in a city", Input: s}},
		MaxTokens: 1024,
	}
}

// checkWeatherCall checks the response calls get_weather for Paris, and
// returns the call.
func checkWeatherCall(t *testing.T, resp *ai.Response) ai.ToolCall {
	t.Helper()
	checkResponse(t, resp, ai.StopToolCalls)
	calls := resp.ToolCalls()
	if len(calls) != 1 {
		t.Fatalf("%d tool calls, want 1: %+v", len(calls), resp.Message)
	}
	c := calls[0]
	var in weatherInput
	if err := json.Unmarshal(c.Input, &in); err != nil {
		t.Fatalf("tool input %s: %v", c.Input, err)
	}
	if c.Name != "get_weather" || c.ID == "" || !strings.Contains(in.City, "Paris") {
		t.Errorf("tool call %+v", c)
	}
	return c
}

func testTools(t *testing.T, p ai.Provider, model string) {
	req := toolsRequest(t, model)
	resp, err := p.Generate(ctxOf(t), req)
	if err != nil {
		t.Fatal(err)
	}
	call := checkWeatherCall(t, resp)

	// The result goes back, with the model's message as it was.
	req.Messages = append(req.Messages, resp.Message, ai.Message{Role: ai.RoleTool, Parts: []ai.Part{
		ai.ToolResult{CallID: call.ID, Name: call.Name, Content: `{"city":"Paris","sky":"sunny","celsius":21}`},
	}})
	resp, err = p.Generate(ctxOf(t), req)
	if err != nil {
		t.Fatal(err)
	}
	checkResponse(t, resp, ai.StopEnd)
	if !strings.Contains(strings.ToLower(resp.Text()), "sunny") {
		t.Errorf("text %q: the tool's result isn't in it", resp.Text())
	}
}

func testToolsStream(t *testing.T, p ai.Provider, model string) {
	evs, err := collect(t, p, toolsRequest(t, model))
	if err != nil {
		t.Fatal(err)
	}
	var calls []ai.ToolCall
	var resp *ai.Response
	for _, ev := range evs {
		switch ev.Kind {
		case ai.EventToolCall:
			calls = append(calls, *ev.ToolCall)
		case ai.EventResponse:
			resp = ev.Response
		}
	}
	call := checkWeatherCall(t, resp)
	if len(calls) != 1 || calls[0].ID != call.ID || string(calls[0].Input) != string(call.Input) {
		t.Errorf("streamed tool calls %+v, response's %+v", calls, call)
	}
}

type capital struct {
	City       string `json:"city" validate:"required"`
	Country    string `json:"country" validate:"required"`
	Population *int   `json:"population" description:"Inhabitants of the city proper, if known"`
}

func testOutput(t *testing.T, p ai.Provider, model string) {
	s, err := ai.SchemaFor[capital]()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Generate(ctxOf(t), &ai.Request{
		Model:     model,
		Messages:  []ai.Message{ai.UserMessage("What is the capital of France?")},
		Output:    &ai.OutputSpec{Name: "capital", Schema: s},
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkResponse(t, resp, ai.StopEnd)
	var c capital
	if err := json.Unmarshal([]byte(resp.Text()), &c); err != nil {
		t.Fatalf("answer %q isn't the JSON object: %v", resp.Text(), err)
	}
	if c.City != "Paris" || c.Country == "" {
		t.Errorf("answer %+v", c)
	}
}

func testMaxTokens(t *testing.T, p ai.Provider, model string) {
	resp, err := p.Generate(ctxOf(t), &ai.Request{
		Model:     model,
		Messages:  []ai.Message{ai.UserMessage("Count from 1 to 200, separated by commas.")},
		MaxTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	checkResponse(t, resp, ai.StopMaxTokens)
}

func testError(t *testing.T, p ai.Provider, _ string) {
	req := textRequest("anetos-no-such-model")
	if _, err := p.Generate(ctxOf(t), req); err == nil {
		t.Error("Generate with an unknown model: no error")
	}
	if _, err := collect(t, p, req); err == nil {
		t.Error("Stream with an unknown model: no error")
	}
}
