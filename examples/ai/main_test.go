// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"
	"testing"

	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/anetostest"
)

func TestSummarize(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(
		// A first answer that breaks the rules is sent back once, with
		// what's wrong.
		ai.FakeObject(Summary{Topic: "Late parcel", Sentiment: "angry"}),
		ai.FakeObject(Summary{Topic: "Late parcel", Sentiment: "negative", Urgent: true, Tags: []string{"delivery"}}),
	))
	app.PostJSON("/summaries", map[string]string{"text": "My parcel is late!"}).AssertOK().
		AssertJSON(map[string]any{"topic": "Late parcel", "sentiment": "negative", "urgent": true, "tags": []any{"delivery"}})
	retry := app.AI().Requests()[1]
	if !strings.Contains(retry.Prompt(), "sentiment: The selected sentiment is invalid.") {
		t.Errorf("retry prompt: %q", retry.Prompt())
	}
	if retry.Output == nil || retry.Output.Name != "summary" {
		t.Errorf("output: %+v", retry.Output)
	}

	// Twice wrong: 502.
	app.AI().Add(ai.FakeText("not JSON"), ai.FakeText("still not"))
	app.PostJSON("/summaries", map[string]string{"text": "Hello"}).AssertStatus(http.StatusBadGateway)
}

// region: test-ask
func TestAsk(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(
		ai.FakeToolCall("find_order", FindOrderInput{Number: 1042}), // the model looks the order up
		ai.FakeText("Your kettle shipped yesterday."),               // then answers
	))
	app.WithHeader("X-Customer", "ada") // a signed-in customer, in a real app
	app.PostJSON("/questions", map[string]string{"question": "Where is order 1042?"}).
		AssertOK().AssertJSONPath("answer", "Your kettle shipped yesterday.")

	// The model got the tool's result, and the prompt.
	reqs := app.AI().Requests()
	result := reqs[1].Messages[2].Parts[0].(ai.ToolResult)
	if result.IsError || !strings.Contains(result.Content, `"status":"shipped yesterday"`) {
		t.Errorf("tool result: %+v", result)
	}
	app.AssertPrompted(func(r ai.Request) bool { return r.Prompt() == "Where is order 1042?" })
}

// endregion

func TestAskOthersOrder(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(
		ai.FakeToolCall("find_order", FindOrderInput{Number: 1043}), // bob's
		ai.FakeText("I can't find that order."),
	))
	app.WithHeader("X-Customer", "ada").PostJSON("/questions", map[string]string{"question": "Where is order 1043?"}).AssertOK()
	result := app.AI().Requests()[1].Messages[2].Parts[0].(ai.ToolResult)
	if !result.IsError || result.Content != "Not Found: The customer has no such order." {
		t.Errorf("tool result: %+v", result)
	}
	// Without a customer, nothing reaches the model.
	app.WithHeader("X-Customer", "").PostJSON("/questions", map[string]string{"question": "Hi"}).AssertUnprocessable()
	if n := len(app.AI().Requests()); n != 2 {
		t.Errorf("%d requests", n)
	}
}

func TestAskStream(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(
		ai.FakeToolCall("find_order", FindOrderInput{Number: 1042}),
		ai.FakeText("Your kettle shipped yesterday."),
	))
	app.WithHeader("X-Customer", "ada").Get("/questions/stream?question=Where+is+1042%3F").AssertOK().
		AssertHeader("Content-Type", "text/plain; charset=utf-8").
		AssertSee("Your kettle shipped yesterday.")
}
