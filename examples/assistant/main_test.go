// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/url"
	"strings"
	"testing"

	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/anetostest"
)

// signIn creates a user and signs in as them.
func signIn(t *testing.T, app *anetostest.App, name string) *User {
	t.Helper()
	hash, err := password.Hash("password1")
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Name: name, Email: strings.ToLower(name) + "@example.com", Password: hash}
	if err := db.Create(app.Context(), u); err != nil {
		t.Fatal(err)
	}
	app.Get("/login")
	app.PostForm("/login", url.Values{"email": {u.Email}, "password": {"password1"}}).AssertRedirect("/")
	return u
}

func addArticles(t *testing.T, app *anetostest.App) {
	t.Helper()
	articles := append([]Article(nil), helpCenter...)
	if err := db.CreateMany(app.Context(), articles); err != nil {
		t.Fatal(err)
	}
}

// region: test
func TestAssistant(t *testing.T) {
	// The model's replies, scripted: search, read, answer.
	app := anetostest.New(t, setup, anetostest.FakeAI(
		ai.FakeToolCall("search_articles", SearchInput{Query: "export"}),
		ai.FakeToolCall("read_article", ReadInput{ID: 1}),
		ai.FakeText("Open Settings, then Data, and choose Export (Export your lists)."),
	))
	addArticles(t, app)
	signIn(t, app, "Ada")

	app.PostForm("/chat", url.Values{"prompt": {"How do I export my lists?"}}).Follow().
		AssertSee("How do I export my lists?", `sse-connect="/chat/1/reply"`)
	// The answer streams as server-sent events, and is stored.
	app.Get("/chat/1/reply").AssertOK().
		AssertSee("event: tool\ndata: search_articles", "event: tool\ndata: read_article",
			"event: text\ndata: Open ", "event: done")
	app.Get("/chat/1").AssertSee("Used: search_articles, read_article", "Open Settings, then Data, and choose Export").
		AssertDontSee("sse-connect")

	// The tools ran for real: the search found the article, which the
	// model read.
	reqs := app.AI().Requests()
	if got := reqs[1].Messages[2].Parts[0].(ai.ToolResult).Content; !strings.Contains(got, `"title":"Export your lists"`) {
		t.Errorf("search results: %s", got)
	}
	app.AssertPrompted(func(r ai.Request) bool { return strings.Contains(r.System, "help-center articles") })

	// A browser that reconnects gets no second answer.
	app.Get("/chat/1/reply").AssertOK().AssertDontSee("event: text")
}

// endregion

func TestFollowUpAndUsage(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(ai.FakeText("Hello!"), ai.FakeText("Four dollars a month.")))
	signIn(t, app, "Ada")
	app.PostForm("/chat", url.Values{"prompt": {"Hi"}})
	app.Get("/chat/1/reply").AssertSee("data: Hello!")

	// htmx posts the next question; the fragment streams its answer.
	app.PostForm("/chat/1", url.Values{"prompt": {"How much is Pro?"}}).
		AssertSee(`<div class="msg user"><p>How much is Pro?</p></div>`, `sse-connect="/chat/1/reply"`)
	app.Get("/chat/1/reply").AssertSee("data: Four ")
	if reqs := app.AI().Requests(); len(reqs[1].Messages) != 3 {
		t.Errorf("the follow-up sent %d messages", len(reqs[1].Messages))
	}
	app.Get("/").AssertSee(`<a href="/chat/1">Hi</a>`, "Today: ")
	if strings.Contains(app.Get("/").Text(), "Today: 0 tokens") {
		t.Error("no usage recorded")
	}
}

func TestBudget(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"ASSISTANT_DAILY_TOKENS": "3"}),
		anetostest.FakeAI(ai.FakeText("one two three four")))
	signIn(t, app, "Ada")
	app.PostForm("/chat", url.Values{"prompt": {"Hi"}})
	app.Get("/chat/1/reply").AssertSee("data: one ")
	app.PostForm("/chat/1", url.Values{"prompt": {"More?"}})
	app.Get("/chat/1/reply").AssertSee("event: error\ndata: You&#39;ve reached your AI usage limit. Try again in ", "event: done")
	app.AssertPrompted(func(r ai.Request) bool { return len(r.Messages) == 1 }) // only the first
}

// region: test-queue
func TestAnswerLater(t *testing.T) {
	// QUEUE_DRIVER is sync by default: the job runs at once.
	app := anetostest.New(t, setup, anetostest.FakeAI(ai.FakeText("Hello!"), ai.FakeText("Shared lists need a Team plan.")))
	signIn(t, app, "Ada")
	app.PostForm("/chat", url.Values{"prompt": {"Hi"}})
	app.Get("/chat/1/reply")

	app.PostForm("/chat/1/later", url.Values{"prompt": {"Can I share a list?"}}).
		AssertSee("Can I share a list?", `hx-get="/chat/1/status"`)
	app.Get("/chat/1/status").AssertSee("Shared lists need a Team plan.").AssertDontSee("Working on it")
}

// endregion

func TestOtherUsersConversations(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(ai.FakeText("Hello!")))
	signIn(t, app, "Ada")
	app.PostForm("/chat", url.Values{"prompt": {"Hi"}})
	app.PostForm("/logout", nil)
	signIn(t, app, "Bob")
	for _, path := range []string{"/chat/1", "/chat/1/reply", "/chat/1/status"} {
		app.Get(path).AssertNotFound()
	}
	app.PostForm("/chat/1", url.Values{"prompt": {"Mine now?"}}).AssertNotFound()
	app.AssertNotPrompted()
}
