// SPDX-License-Identifier: Apache-2.0

package ai_test

import (
	"context"
	"fmt"

	"anetos.dev/anetos/ai"
)

func ExampleGenerate() {
	// In an app, ai.ForApp puts the client in every context.
	ctx := ai.WithClient(context.Background(), ai.New(ai.NewFake(ai.FakeText("Paris."))))

	res, err := ai.Generate(ctx, "What's the capital of France?", ai.System("Answer in one word."))
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Text(), res.Usage.OutputTokens)
	// Output: Paris. 1
}

func ExampleGenerateObject() {
	type Capital struct {
		City    string `json:"city" validate:"required"`
		Country string `json:"country" validate:"required"`
	}
	ctx := ai.WithClient(context.Background(), ai.New(ai.NewFake(ai.FakeObject(Capital{City: "Paris", Country: "France"}))))

	c, _, err := ai.GenerateObject[Capital](ctx, "What's the capital of France?")
	if err != nil {
		panic(err)
	}
	fmt.Println(c.City, c.Country)
	// Output: Paris France
}

func ExampleAgent() {
	type Weather struct {
		City string `json:"city" validate:"required"`
	}
	weather := ai.Func("weather", "Today's weather in a city",
		func(ctx context.Context, in Weather) (string, error) { return "Sunny in " + in.City, nil })
	forecaster := ai.Agent{Instructions: "You answer questions about the weather.", Tools: []ai.Tool{weather}}

	ctx := ai.WithClient(context.Background(), ai.New(ai.NewFake(
		ai.FakeToolCall("weather", Weather{City: "Dhaka"}), // the model calls the tool,
		ai.FakeText("It's sunny in Dhaka today."),          // then answers with its result
	)))
	res, err := forecaster.Prompt(ctx, "Is it sunny in Dhaka?")
	if err != nil {
		panic(err)
	}
	for _, m := range res.Messages {
		for _, p := range m.Parts {
			switch p := p.(type) {
			case ai.Text:
				fmt.Printf("%s: %s\n", m.Role, p)
			case ai.ToolCall:
				fmt.Printf("%s calls %s %s\n", m.Role, p.Name, p.Input)
			case ai.ToolResult:
				fmt.Printf("%s: %s\n", p.Name, p.Content)
			}
		}
	}
	// Output:
	// user: Is it sunny in Dhaka?
	// assistant calls weather {"city":"Dhaka"}
	// weather: Sunny in Dhaka
	// assistant: It's sunny in Dhaka today.
}

func ExampleStream() {
	ctx := ai.WithClient(context.Background(), ai.New(ai.NewFake(ai.FakeText("One, two, three."))))

	for ev, err := range ai.Stream(ctx, "Count to three.") {
		if err != nil {
			panic(err)
		}
		switch ev.Kind {
		case ai.EventText:
			fmt.Printf("%q\n", ev.Text)
		case ai.EventDone:
			fmt.Println("done:", ev.Result.Usage.OutputTokens, "tokens")
		}
	}
	// Output:
	// "One, "
	// "two, "
	// "three."
	// done: 3 tokens
}
