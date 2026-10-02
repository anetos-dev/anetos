// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strings"
	"sync"
)

// FakeReply answers one request to a [Fake].
type FakeReply func(req *Request) (*Response, error)

// FakeText replies with text.
func FakeText(text string) FakeReply {
	return func(*Request) (*Response, error) {
		return &Response{Message: AssistantMessage(text), Stop: StopEnd}, nil
	}
}

// FakeObject replies with v as JSON: the answer to [GenerateObject].
func FakeObject(v any) FakeReply {
	return func(*Request) (*Response, error) {
		data, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("encode the scripted object: %w", err)
		}
		return &Response{Message: AssistantMessage(string(data)), Stop: StopEnd}, nil
	}
}

// FakeToolCall replies with a call of the tool name, with input (a
// struct or map, encoded as JSON). The call's result goes to the next
// reply's request.
func FakeToolCall(name string, input any) FakeReply {
	return func(req *Request) (*Response, error) {
		data, err := json.Marshal(input)
		if err != nil {
			return nil, fmt.Errorf("encode the scripted input of %s: %w", name, err)
		}
		id := fmt.Sprintf("call_%d", len(req.Messages))
		return &Response{
			Message: Message{Role: RoleAssistant, Parts: []Part{ToolCall{ID: id, Name: name, Input: data}}},
			Stop:    StopToolCalls,
		}, nil
	}
}

// FakeError fails the request with err.
func FakeError(err error) FakeReply {
	return func(*Request) (*Response, error) { return nil, err }
}

// Fake is a [Provider] that answers with scripted replies, one per
// request, in order, and records the requests. It never calls a model.
// Usage counts words, as a stand-in for tokens. A request with no reply
// left fails. It makes embeddings too ([Fake.Embed]), with no replies. A Fake is safe for concurrent use; replies are taken in
// the order requests arrive.
//
//	f := ai.NewFake(ai.FakeToolCall("find_order", map[string]int{"number": 1042}), ai.FakeText("It shipped."))
//	ctx = ai.WithClient(ctx, ai.New(f))
type Fake struct {
	mu       sync.Mutex
	replies  []FakeReply
	requests []Request
	embeds   []EmbedRequest
}

// NewFake returns a Fake with replies.
func NewFake(replies ...FakeReply) *Fake { return &Fake{replies: slices.Clip(replies)} }

// Add adds replies, after the ones left.
func (f *Fake) Add(replies ...FakeReply) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies = append(f.replies, replies...)
}

// Requests returns the requests so far, oldest first.
func (f *Fake) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.requests)
}

// Embeddings returns the embedding requests so far, oldest first.
func (f *Fake) Embeddings() []EmbedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.embeds)
}

// Remaining returns the number of replies not used yet.
func (f *Fake) Remaining() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.replies)
}

// Name returns "fake".
func (f *Fake) Name() string { return "fake" }

// Generate answers req with the next reply.
func (f *Fake) Generate(ctx context.Context, req *Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	cp := *req
	cp.Messages = slices.Clone(req.Messages)
	cp.Tools = slices.Clone(req.Tools)
	f.requests = append(f.requests, cp)
	n := len(f.requests)
	if len(f.replies) == 0 {
		f.mu.Unlock()
		return nil, fmt.Errorf("no reply scripted for request %d: add one (anetostest.FakeAI, Fake.Add)", n)
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	f.mu.Unlock()

	resp, err := reply(&cp)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("the reply to request %d returned no response", n)
	}
	if resp.Model == "" {
		resp.Model = cmp.Or(req.Model, "fake")
	}
	if resp.Message.Role == "" {
		resp.Message.Role = RoleAssistant
	}
	if resp.Stop == "" {
		resp.Stop = StopEnd
	}
	if resp.Usage == (Usage{}) {
		in := words(req.System)
		for _, m := range req.Messages {
			in += words(m.Text())
		}
		resp.Usage = Usage{InputTokens: int64(in), OutputTokens: int64(words(resp.Text()))}
	}
	return resp, nil
}

// Stream answers req with the next reply, its text a word at a time.
func (f *Fake) Stream(ctx context.Context, req *Request) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		resp, err := f.Generate(ctx, req)
		if err != nil {
			yield(Event{}, err)
			return
		}
		for _, w := range strings.SplitAfter(resp.Text(), " ") {
			if w != "" && !yield(Event{Kind: EventText, Text: w}, nil) {
				return
			}
		}
		for _, tc := range resp.ToolCalls() {
			if !yield(Event{Kind: EventToolCall, ToolCall: &tc}, nil) {
				return
			}
		}
		yield(Event{Kind: EventResponse, Response: resp}, nil)
	}
}

func words(s string) int { return len(strings.Fields(s)) }
