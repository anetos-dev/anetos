// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
)

// FakeAI scripts the model's answers: each request to the app's AI
// client gets the next reply, in order ([ai.FakeText], [ai.FakeObject],
// [ai.FakeToolCall], [ai.FakeError]), and no model is called: New sets
// AI_PROVIDER=fake, and FakeAI puts the fake in place of a provider an
// [Env] option chose. A request with no reply left fails. Several FakeAI
// options add up; [App.AI] adds more during the test.
//
//	app := anetostest.New(t, setup, anetostest.FakeAI(
//		ai.FakeToolCall("find_order", map[string]int{"number": 1042}),
//		ai.FakeText("Order 1042 shipped yesterday."),
//	))
func FakeAI(replies ...ai.FakeReply) Option {
	return func(o *options) {
		o.fakeAI = true
		o.aiReplies = append(o.aiReplies, replies...)
	}
}

// recordAI finds the app's AI client and makes sure it's the fake.
func (a *App) recordAI(o *options) {
	c, err := anetos.Resolve[*ai.Client](a.App)
	if err != nil {
		if o.fakeAI {
			a.t.Fatalf("anetostest: FakeAI: the app has no AI client (ai.ForApp in setup)")
		}
		return
	}
	if f, ok := c.Provider().(*ai.Fake); ok || o.fakeAI {
		if !ok {
			f = c.Fake()
		}
		f.Add(o.aiReplies...)
		a.ai = f
	}
}

// AI returns the fake provider of the app's AI client: its requests so
// far (Requests), and more replies (Add). The test fails if the app has
// no AI client, or AI_PROVIDER isn't fake and FakeAI wasn't used.
func (a *App) AI() *ai.Fake {
	a.t.Helper()
	if a.ai == nil {
		a.t.Fatalf("anetostest: the app's AI client isn't the fake: ai.ForApp in setup, and AI_PROVIDER=fake or anetostest.FakeAI")
	}
	return a.ai
}

// AssertPrompted checks that a request to the model matched match (nil
// matches any request).
//
//	app.AssertPrompted(func(r ai.Request) bool { return strings.Contains(r.Prompt(), "1042") })
func (a *App) AssertPrompted(match func(ai.Request) bool) *App {
	a.t.Helper()
	reqs := a.AI().Requests()
	for _, r := range reqs {
		if match == nil || match(r) {
			return a
		}
	}
	if len(reqs) == 0 {
		a.t.Errorf("anetostest: the model wasn't prompted")
	} else {
		a.t.Errorf("anetostest: none of the %d requests to the model matched; the last prompt was %q", len(reqs), reqs[len(reqs)-1].Prompt())
	}
	return a
}

// AssertNotPrompted checks that the app made no request to the model.
func (a *App) AssertNotPrompted() *App {
	a.t.Helper()
	if reqs := a.AI().Requests(); len(reqs) > 0 {
		a.t.Errorf("anetostest: the model was prompted %d times; the first prompt was %q", len(reqs), reqs[0].Prompt())
	}
	return a
}
