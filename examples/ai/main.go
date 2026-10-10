// SPDX-License-Identifier: Apache-2.0

// Command ai is a small support desk API built on package ai: a typed
// summary of a customer's message, an agent that answers questions with a
// tool over the customer's orders, and an answer streamed as it's
// written. Orders are kept in memory; the X-Customer header stands in for
// authentication.
//
// It runs with any provider: Anthropic, OpenAI, Gemini, or a local
// model through an OpenAI-compatible server such as Ollama:
//
//	export APP_ENV=development HTTP_ADDR=:8080
//	export AI_PROVIDER=anthropic AI_MODEL=claude-haiku-4-5 ANTHROPIC_API_KEY=…
//	# or AI_PROVIDER=openai-compatible OPENAI_COMPATIBLE_URL=http://localhost:11434/v1 AI_MODEL=llama3.2
//	go run .
//	curl -s localhost:8080/summaries -d '{"text":"My parcel is late and the tracking page is broken."}'
//	curl -s localhost:8080/questions -H 'X-Customer: ada' -d '{"question":"Where is order 1042?"}'
//	curl -sN 'localhost:8080/questions/stream?question=Where+is+order+1042%3F' -H 'X-Customer: ada'
//
// Its tests script the model's answers with anetostest.FakeAI, so they
// run without a provider.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/drivers/anthropic"
	"anetos.dev/anetos/drivers/gemini"
	"anetos.dev/anetos/drivers/openai"
	"anetos.dev/anetos/web"
)

func main() {
	app, err := anetos.New()
	if err != nil {
		log.Fatal(err)
	}
	if _, err := setup(app); err != nil {
		log.Fatal(err)
	}
	app.Execute()
}

func setup(app *anetos.App) (*web.Server, error) {
	// region: setup
	// AI_PROVIDER picks one of these, AI_MODEL the model.
	if _, err := ai.New(app, anthropic.Driver(), openai.Driver(), openai.CompatibleDriver(), gemini.Driver()); err != nil {
		return nil, err
	}
	// endregion
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	r := srv.Router()
	r.Post("/summaries", web.H(Summarize))
	r.Post("/questions", web.H(Ask))
	r.Get("/questions/stream", web.H(AskStream))
	return srv, nil
}

// region: summary
// Summary is a customer message, summarized. The model is given its
// schema, built from the json, description and validate tags, and its
// answer is checked with the validate rules.
type Summary struct {
	Topic     string   `json:"topic" description:"What the message is about, in a few words" validate:"required|max:60"`
	Sentiment string   `json:"sentiment" validate:"required|in:positive,neutral,negative"`
	Urgent    bool     `json:"urgent" description:"Whether the customer needs an answer today"`
	Tags      []string `json:"tags" description:"Lower-case keywords" validate:"max:5"`
}

// SummarizeInput is a customer's message.
type SummarizeInput struct {
	Text string `json:"text" validate:"required|max:5000"`
}

// Summarize summarizes a customer's message.
func Summarize(c *web.Ctx, in SummarizeInput) (Summary, error) {
	sum, _, err := ai.GenerateObject[Summary](c, "Summarize this customer message:\n\n"+in.Text,
		ai.System("You triage a support inbox."))
	return sum, err
}

// endregion

// Order is a customer's order.
type Order struct {
	Number   int    `json:"number"`
	Customer string `json:"-"`
	Item     string `json:"item"`
	Status   string `json:"status"`
}

// orders are the shop's orders.
var orders = []Order{
	{Number: 1042, Customer: "ada", Item: "Kettle", Status: "shipped yesterday"},
	{Number: 1043, Customer: "bob", Item: "Teapot", Status: "being packed"},
}

type customerKey struct{}

// customer returns the customer of the request ctx belongs to.
func customer(ctx context.Context) string {
	name, _ := ctx.Value(customerKey{}).(string)
	return name
}

// region: tool
// FindOrderInput is the find_order tool's input, as the model writes it.
type FindOrderInput struct {
	Number int `json:"number" description:"The order's number" validate:"required|min:1"`
}

// findOrder looks up an order of the current customer. It runs with the
// request's context: another customer's order is "not found", as it would
// be in the customer's own browser, and the model is told so.
var findOrder = ai.NewTool("find_order", "Look up one of the customer's orders by its number",
	func(ctx context.Context, in FindOrderInput) (Order, error) {
		i := slices.IndexFunc(orders, func(o Order) bool { return o.Number == in.Number && o.Customer == customer(ctx) })
		if i < 0 {
			return Order{}, web.Error(http.StatusNotFound, "The customer has no such order.")
		}
		return orders[i], nil
	})

// support answers customers' questions about their orders.
var support = ai.Agent{
	Name:         "support",
	Instructions: "You answer the customer's questions about their orders, briefly. Look orders up; never guess.",
	Tools:        []ai.Tool{findOrder},
	MaxSteps:     5,
}

// endregion

// region: ask
// Question is a customer's question; the header stands in for
// authentication.
type Question struct {
	Customer string `header:"X-Customer" validate:"required"`
	Question string `json:"question" validate:"required|max:500"`
}

// Answer is the agent's answer, with the tokens it took.
type Answer struct {
	Answer       string `json:"answer"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// Ask answers a question with the support agent.
func Ask(c *web.Ctx, in Question) (Answer, error) {
	ctx := context.WithValue(c, customerKey{}, in.Customer) // what the tool sees
	res, err := support.Prompt(ctx, in.Question)
	if err != nil {
		return Answer{}, err
	}
	return Answer{Answer: res.Text(), InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens}, nil
}

// endregion

// region: stream
// StreamQuestion is a question in the query string.
type StreamQuestion struct {
	Customer string `header:"X-Customer" validate:"required"`
	Question string `query:"question" validate:"required|max:500"`
}

// AskStream answers a question as plain text, sent as the model writes
// it.
func AskStream(c *web.Ctx, in StreamQuestion) (web.Responder, error) {
	// A long answer outlasts HTTP_REQUEST_TIMEOUT and HTTP_WRITE_TIMEOUT:
	// lift both for this response. (Leaving the page still stops it.)
	ctx := context.WithValue(web.WithoutTimeout(c), customerKey{}, in.Customer)
	w := c.Writer()
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, err
	}
	for ev, err := range support.Stream(ctx, in.Question) {
		if err != nil {
			return nil, err // before any text, an error response; after, only logged
		}
		if ev.Kind != ai.EventText {
			continue
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}
		if _, err := io.WriteString(w, ev.Text); err != nil {
			return nil, err // the client left: breaking out stops the model
		}
		if err := rc.Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return nil, err
		}
	}
	return nil, nil
}

// endregion
