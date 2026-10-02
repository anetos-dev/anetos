// SPDX-License-Identifier: Apache-2.0

// Package ai connects language models to the app: text and typed
// answers, streaming, and tools that run as the current user.
//
//	client, err := ai.ForApp(app) // AI_PROVIDER, AI_MODEL; pass providers' drivers here
//
//	res, err := ai.Generate(ctx, "Summarize in one sentence: "+post.Body)
//	fmt.Println(res.Text(), res.Usage.OutputTokens)
//
//	sum, _, err := ai.GenerateObject[Summary](ctx, "Summarize: "+post.Body) // decoded and validated
//
//	support := ai.Agent{
//		Instructions: "You answer the customer's questions about their orders.",
//		Tools:        []ai.Tool{ai.Func("find_order", "Look up an order by its number", findOrder)},
//	}
//	res, err = support.Prompt(ctx, "Where is order 1042?")
//
//	for ev, err := range ai.Stream(ctx, question) { … } // the answer as it's written
//
// The package holds the [Provider] contract, messages, the tool loop and
// a [Fake]; providers are driver modules that wrap the official SDKs
// (Anthropic, OpenAI and OpenAI-compatible servers, and Gemini are
// planned for v0.3).
// [Request.Options], [Response.Raw] and [Client.Provider] reach what the
// common contract doesn't cover. Typed outputs and tool inputs are
// structs: their JSON schema comes from their fields' json, description
// and validate tags ([SchemaFor]), and what the model sends is checked
// with the validate rules. Tools run with the caller's context, so the
// current user's permissions apply to them; each tool call is a unit of
// work, and each request to the model is logged with its token usage.
//
// Tests use the [Fake] (anetostest sets AI_PROVIDER=fake, and
// anetostest.FakeAI scripts its replies): no network, no cost, the same
// answers every run.
package ai
