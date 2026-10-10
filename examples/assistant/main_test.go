// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/db"
)

// login creates a user and logs in as them.
func login(t *testing.T, app *anetostest.App, name string) *User {
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

// addArticles adds the help center's articles, embeds them (with the
// fake embedder: no model is called) and returns them with their IDs.
func addArticles(t *testing.T, app *anetostest.App) []Article {
	t.Helper()
	if err := db.CreateMany(app.Context(), append([]Article(nil), helpCenter...)); err != nil {
		t.Fatal(err)
	}
	// Read back: MySQL doesn't report the IDs of a multi-row insert.
	articles, err := db.Query[Article](app.Context()).OrderBy(db.Col[int64]("id").Asc()).Get()
	if err != nil {
		t.Fatal(err)
	}
	embeddings, err := anetos.Resolve[*ai.Embeddings[Article]](app.App)
	if err != nil {
		t.Fatal(err)
	}
	if err := embeddings.Sync(app.Context(), articles...); err != nil {
		t.Fatal(err)
	}
	return articles
}

// startChat posts the first question and returns the conversation's
// path, /chat/<id>: IDs depend on the database, whose sequences tests
// don't roll back.
func startChat(t *testing.T, app *anetostest.App, prompt string) string {
	t.Helper()
	res := app.PostForm("/chat", url.Values{"prompt": {prompt}})
	path := res.Header.Get("Location")
	if !strings.HasPrefix(path, "/chat/") {
		t.Fatalf("POST /chat: status %d, Location %q", res.StatusCode, path)
	}
	return path
}

// region: test
func TestAssistant(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI())
	export := addArticles(t, app)[0] // "Export your lists"
	// The model's replies, scripted: search, read, answer.
	app.AI().Add(
		ai.FakeToolCall("search_articles", map[string]string{"query": "How do I export my lists?"}),
		ai.FakeToolCall("read_article", ReadInput{ID: export.ID}),
		ai.FakeText("Open Settings, then Data, and choose Export (Export your lists)."),
	)
	login(t, app, "Ada")

	chat := startChat(t, app, "How do I export my lists?")
	app.Get(chat).AssertSee("How do I export my lists?", `sse-connect="`+chat+`/reply"`)
	// The answer streams as server-sent events, and is stored.
	app.Get(chat+"/reply").AssertOK().
		AssertSee("event: tool\ndata: search_articles", "event: tool\ndata: read_article",
			"event: text\ndata: Open ", "event: done")
	app.Get(chat).AssertSee("Used: search_articles, read_article", "Open Settings, then Data, and choose Export").
		AssertDontSee("sse-connect")

	// The tools ran for real: the search found the article, with its
	// passage, which the model read.
	reqs := app.AI().Requests()
	want := fmt.Sprintf(`[{"id":%d,"title":"Export your lists","text":"Export your lists\n\nOpen Settings`, export.ID)
	if got := reqs[1].Messages[2].Parts[0].(ai.ToolResult).Content; !strings.HasPrefix(got, want) {
		t.Errorf("search results: %s", got)
	}
	app.AssertPrompted(func(r ai.Request) bool { return strings.Contains(r.System, "help-center articles") })

	// A browser that reconnects gets no second answer.
	app.Get(chat + "/reply").AssertOK().AssertDontSee("event: text")
}

// endregion

func TestFollowUpAndUsage(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(ai.FakeText("Hello!"), ai.FakeText("Four dollars a month.")))
	login(t, app, "Ada")
	chat := startChat(t, app, "Hi")
	app.Get(chat + "/reply").AssertSee("data: Hello!")

	// htmx posts the next question; the fragment streams its answer.
	app.PostForm(chat, url.Values{"prompt": {"How much is Pro?"}}).
		AssertSee(`<div class="msg user"><p>How much is Pro?</p></div>`, `sse-connect="`+chat+`/reply"`)
	app.Get(chat + "/reply").AssertSee("data: Four ")
	if reqs := app.AI().Requests(); len(reqs[1].Messages) != 3 {
		t.Errorf("the follow-up sent %d messages", len(reqs[1].Messages))
	}
	app.Get("/").AssertSee(`<a href="`+chat+`">Hi</a>`, "Today: ")
	if strings.Contains(app.Get("/").Text(), "Today: 0 tokens") {
		t.Error("no usage recorded")
	}
}

func TestBudget(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"ASSISTANT_DAILY_TOKENS": "3"}),
		anetostest.FakeAI(ai.FakeText("one two three four")))
	login(t, app, "Ada")
	chat := startChat(t, app, "Hi")
	app.Get(chat + "/reply").AssertSee("data: one ")
	app.PostForm(chat, url.Values{"prompt": {"More?"}})
	app.Get(chat+"/reply").AssertSee("event: error\ndata: You&#39;ve reached your AI usage limit. Try again in ", "event: done")
	app.AssertPrompted(func(r ai.Request) bool { return len(r.Messages) == 1 }) // only the first
}

// region: test-queue
func TestAnswerLater(t *testing.T) {
	// QUEUE_DRIVER is sync by default: the job runs at once.
	app := anetostest.New(t, setup, anetostest.FakeAI(ai.FakeText("Hello!"), ai.FakeText("Shared lists need a Team plan.")))
	login(t, app, "Ada")
	chat := startChat(t, app, "Hi")
	app.Get(chat + "/reply")

	app.PostForm(chat+"/later", url.Values{"prompt": {"Can I share a list?"}}).
		AssertSee("Can I share a list?", `hx-get="`+chat+`/status"`)
	app.Get(chat + "/status").AssertSee("Shared lists need a Team plan.").AssertDontSee("Working on it")
}

// endregion

func TestOtherUsersConversations(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.FakeAI(ai.FakeText("Hello!")))
	login(t, app, "Ada")
	chat := startChat(t, app, "Hi")
	app.PostForm("/logout", nil)
	login(t, app, "Bob")
	for _, path := range []string{chat, chat + "/reply", chat + "/status"} {
		app.Get(path).AssertNotFound()
	}
	app.PostForm(chat, url.Values{"prompt": {"Mine now?"}}).AssertNotFound()
	app.AssertNotPrompted()
}

// region: test-search
func TestSearchArticles(t *testing.T) {
	app := anetostest.New(t, setup)
	addArticles(t, app)
	embeddings, err := anetos.Resolve[*ai.Embeddings[Article]](app.App)
	if err != nil {
		t.Fatal(err)
	}
	// Each article was embedded once, as a document.
	if reqs := app.AI().EmbedRequests(); len(reqs) != 1 || len(reqs[0].Inputs) != len(helpCenter) || reqs[0].Dimensions != embeddingDims {
		t.Fatalf("embedding requests: %+v", reqs)
	}
	found, err := embeddings.Search(app.Context(), "what does the team plan cost", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Record.Title != "Plans and billing" {
		t.Errorf("found %+v", found)
	}
}

// endregion
