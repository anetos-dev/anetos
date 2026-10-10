// SPDX-License-Identifier: Apache-2.0

// Command assistant is a help center with an AI assistant: users log in
// and chat with an agent that searches and reads the help-center
// articles (a hybrid search: their embeddings, and their words).
// Conversations are stored, answers stream
// to the page as they're written (server-sent events, with htmx), a
// question can also be answered in the background by a queue job, and
// each user's usage counts against a daily budget.
//
//	anetos key:generate                  # APP_KEY, once
//	export APP_ENV=development HTTP_ADDR=:8080
//	export AI_PROVIDER=anthropic AI_MODEL=claude-sonnet-4-5 ANTHROPIC_API_KEY=…
//	export AI_EMBEDDING_PROVIDER=openai AI_EMBEDDING_MODEL=text-embedding-3-small OPENAI_API_KEY=…
//	go run . migrate
//	go run . seed                         # the articles, and ada@example.com (password: password)
//	go run .
//
// Then open http://localhost:8080. AI_PROVIDER=openai-compatible with
// OPENAI_COMPATIBLE_URL=http://localhost:11434/v1 uses a local model
// (Ollama).
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/drivers/anthropic"
	"anetos.dev/anetos/drivers/mysql"
	"anetos.dev/anetos/drivers/openai"
	"anetos.dev/anetos/drivers/postgres"
	"anetos.dev/anetos/drivers/sqlite"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/view"
	"anetos.dev/anetos/view/htmx"
	"anetos.dev/anetos/web"
)

// Settings are the app's own.
type Settings struct {
	// DailyTokens is each user's AI budget: input and output tokens a day.
	DailyTokens int `env:"ASSISTANT_DAILY_TOKENS" default:"200000"`
}

// setup connects the database and adds the cache, sessions, auth, the
// queue, the AI client, the migrations, the server and the routes. Tests
// call it too.
func setup(app *anetos.App) (*web.Server, error) {
	if _, err := db.Connect(context.Background(), app, sqlite.Driver(), postgres.Driver(), mysql.Driver()); err != nil {
		return nil, err
	}
	sets := []*migrate.Set{Migrations, ai.Migrations(), session.Migrations(""), cache.Migrations(""), queue.Migrations("jobs", "failed_jobs")}
	if _, err := migrate.New(app, sets); err != nil {
		return nil, err
	}
	if _, err := cache.New(app); err != nil { // budgets and login throttling count in the cache
		return nil, err
	}
	sessions, err := session.New(app)
	if err != nil {
		return nil, err
	}
	a, err := auth.New(app, users)
	if err != nil {
		return nil, err
	}
	if _, err := queue.New(app); err != nil { // QUEUE_DRIVER: sync by default
		return nil, err
	}
	settings, err := config.Get[Settings](app.Source())
	if err != nil {
		return nil, err
	}
	// region: setup
	client, err := ai.New(app, anthropic.Driver(), openai.Driver(), openai.CompatibleDriver())
	if err != nil {
		return nil, err
	}
	client.TrackUsage(ai.UsageConfig{ // the ai_usage table, and budgets
		Budget: func(context.Context, string) (ai.Budget, error) {
			return ai.Budget{Tokens: settings.DailyTokens, Per: 24 * time.Hour}, nil
		},
		// Prices: map[string]ai.Price{"model-name": {Input: …, Output: …}}, // per million tokens
	})
	articles, err := ai.EmbeddingsFor(app, articleEmbeddings) // articles_embeddings; ai:embed
	if err != nil {
		return nil, err
	}
	anetos.Provide(app, articles)
	helpdesk := newHelpdesk(articles)
	if err := ai.QueueAgents(app, helpdesk); err != nil { // answers in the background
		return nil, err
	}
	// endregion
	app.Command("seed", "Add the help-center articles and a user (ada@example.com, password: password)",
		func(ctx context.Context, args *cmd.Args) error { return seed(ctx, args, articles) })
	srv, err := web.NewServer(app)
	if err != nil {
		return nil, err
	}
	routes(srv.Router(), sessions, Handlers{auth: a, helpdesk: helpdesk})
	return srv, nil
}

func routes(r *web.Router, sessions *session.Manager, h Handlers) {
	a := h.auth
	r.Get("/assets/{path...}", web.WrapHandler(assets))
	// region: routes
	pages := r.Group("", sessions.Middleware, web.CSRF(), a.Middleware)
	pages.With(a.Guest).Get("/login", h.LoginPage)
	pages.With(a.Guest).Post("/login", web.H(h.Login))

	loggedIn := pages.Group("", a.Require)
	loggedIn.Post("/logout", h.Logout)
	loggedIn.Get("/", h.Index)
	loggedIn.Post("/chat", web.H(h.Start))
	loggedIn.Get("/chat/{id}", web.H(h.Show))
	loggedIn.Post("/chat/{id}", web.H(h.Send))         // htmx: the question, and a place for the answer
	loggedIn.Get("/chat/{id}/reply", web.H(h.Reply))   // the answer, streamed (server-sent events)
	loggedIn.Post("/chat/{id}/later", web.H(h.Later))  // answered by a queue job
	loggedIn.Get("/chat/{id}/status", web.H(h.Status)) // htmx polls it while the job runs
	// endregion
}

// assets serves the bundled htmx and its SSE extension at /assets.
var assets = must(view.NewAssets("/assets", htmx.FS))

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Handlers serves the pages.
type Handlers struct {
	auth     *auth.Auth[*User]
	helpdesk ai.Agent
}

// LoginInput is the login form.
type LoginInput struct {
	Email    string `json:"email" validate:"required|email"`
	Password string `json:"password" validate:"required"`
}

// LoginPage shows the login form.
func (Handlers) LoginPage(c *web.Ctx) error { return render(c, "login", nil) }

// Login logs a user in.
func (h Handlers) Login(c *web.Ctx, in LoginInput) (web.Responder, error) {
	_, err := h.auth.Attempt(c, in.Email, in.Password, false)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		return nil, validate.Fail("email", "These credentials don't match our records.")
	}
	if err != nil {
		return nil, err
	}
	return web.Redirect(auth.Intended(c, "/")), nil
}

// Logout logs the user out.
func (h Handlers) Logout(c *web.Ctx) error {
	if err := h.auth.Logout(c); err != nil {
		return err
	}
	return c.Redirect(http.StatusSeeOther, "/login")
}

// Index lists the user's conversations and their usage today.
func (Handlers) Index(c *web.Ctx) error {
	userID, err := auth.CurrentID(c)
	if err != nil {
		return err
	}
	convs, err := ai.Conversations(c, userID)
	if err != nil {
		return err
	}
	// region: usage
	today := anetos.Now(c).UTC().Truncate(24 * time.Hour)
	usage, err := ai.TotalUsage(c, userID, today)
	if err != nil {
		return err
	}
	// endregion
	return render(c, "index", map[string]any{"Conversations": convs, "Tokens": usage.InputTokens + usage.OutputTokens})
}

// NewChat starts a conversation with a question.
type NewChat struct {
	Prompt string `json:"prompt" validate:"required|max:4000"`
}

// region: start
// Start stores a new conversation with the question, and shows it: the
// page streams the answer.
func (Handlers) Start(c *web.Ctx, in NewChat) (web.Responder, error) {
	userID, err := auth.CurrentID(c)
	if err != nil {
		return nil, err
	}
	conv, err := ai.StartConversation(c, userID, truncate(in.Prompt, 60))
	if err != nil {
		return nil, err
	}
	if err := conv.Add(c, ai.UserMessage(in.Prompt)); err != nil {
		return nil, err
	}
	return web.Redirect(fmt.Sprintf("/chat/%d", conv.ID)), nil
}

// endregion

// ChatPath reads {id} from the path.
type ChatPath struct {
	ID int64 `path:"id"`
}

// conversation returns the logged-in user's conversation, or 404.
func conversation(c *web.Ctx, id int64) (*ai.Conversation, error) {
	userID, err := auth.CurrentID(c)
	if err != nil {
		return nil, err
	}
	return ai.FindConversation(c, userID, id) // another user's: db.ErrNotFound, 404
}

// Show shows a conversation. If its last message is the user's, the page
// streams the answer, or polls for a queued one.
func (Handlers) Show(c *web.Ctx, in ChatPath) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	msgs, err := conv.Messages(c)
	if err != nil {
		return nil, err
	}
	pending := len(msgs) > 0 && msgs[len(msgs)-1].Role == ai.RoleUser
	return page("chat", map[string]any{"Conversation": conv, "Messages": bubbles(msgs), "Pending": pending}), nil
}

// Question is a question in a conversation.
type Question struct {
	ID     int64  `path:"id"`
	Prompt string `json:"prompt" validate:"required|max:4000"`
}

// region: send
// Send stores the question and returns it, with a place for the answer
// that streams it from Reply.
func (Handlers) Send(c *web.Ctx, in Question) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := conv.Add(c, ai.UserMessage(in.Prompt)); err != nil {
		return nil, err
	}
	return page("exchange", map[string]any{"Conversation": conv, "Prompt": in.Prompt}), nil
}

// Reply streams the answer to the conversation's last question, and
// stores it.
func (h Handlers) Reply(c *web.Ctx, in ChatPath) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	msgs, err := conv.Messages(c)
	if err != nil {
		return nil, err
	}
	answer := conv.StreamReply(c, h.helpdesk)
	if len(msgs) == 0 || msgs[len(msgs)-1].Role != ai.RoleUser || conv.Status == ai.StatusQueued {
		answer = func(func(ai.Event, error) bool) {} // answered already: a browser that reconnected
	}
	return web.ResponderFunc(func(c *web.Ctx) error { return ai.SSE(c, answer) }), nil
}

// endregion

// region: later
// Later stores the question for a queue job to answer, and returns a
// placeholder that polls Status.
func (h Handlers) Later(c *web.Ctx, in Question) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	if err := conv.Add(c, ai.UserMessage(in.Prompt)); err != nil {
		return nil, err
	}
	if err := conv.QueueReply(c, h.helpdesk); err != nil {
		return nil, err
	}
	return page("queued", map[string]any{"Conversation": conv, "Prompt": in.Prompt}), nil
}

// Status returns the queued answer when it's there, else the placeholder
// again. If the job left the question unanswered (a question asked in
// the meantime), the answer streams instead.
func (Handlers) Status(c *web.Ctx, in ChatPath) (web.Responder, error) {
	conv, err := conversation(c, in.ID)
	if err != nil {
		return nil, err
	}
	switch conv.Status {
	case ai.StatusQueued:
		return page("waiting", map[string]any{"Conversation": conv}), nil
	case ai.StatusFailed:
		return page("failed", map[string]any{"Error": conv.Error}), nil
	}
	msgs, err := conv.Messages(c)
	if err != nil {
		return nil, err
	}
	if n := len(msgs); n > 0 && msgs[n-1].Role == ai.RoleUser {
		return page("answer", map[string]any{"Conversation": conv}), nil
	}
	all := bubbles(msgs)
	return page("bubbles", all[max(len(all)-1, 0):]), nil
}

// endregion

// bubble is a message as the page shows it.
type bubble struct {
	Role  ai.Role
	Text  string
	Tools []string // the tools the model called
}

// bubbles turns a conversation into what the page shows: the user's
// questions and the model's answers, with the tools it used before them.
func bubbles(msgs []ai.Message) []bubble {
	var out []bubble
	var tools []string
	for _, m := range msgs {
		switch m.Role {
		case ai.RoleUser:
			out = append(out, bubble{Role: m.Role, Text: m.Text()})
		case ai.RoleAssistant:
			for _, tc := range m.ToolCalls() {
				tools = append(tools, tc.Name)
			}
			if t := m.Text(); t != "" {
				out = append(out, bubble{Role: m.Role, Text: t, Tools: tools})
				tools = nil
			}
		}
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// region: seed
// seed adds the articles, embedded, and a user.
func seed(ctx context.Context, args *cmd.Args, embeddings *ai.Embeddings[Article]) error {
	articles := append([]Article(nil), helpCenter...)
	if err := db.CreateMany(ctx, articles); err != nil {
		return err
	}
	if err := embeddings.Sync(ctx, articles...); err != nil { // a queue job; with QUEUE_DRIVER=sync, now
		return err
	}
	// endregion
	hash, err := password.Hash("password")
	if err != nil {
		return err
	}
	if err := db.Create(ctx, &User{Name: "Ada", Email: "ada@example.com", Password: hash}); err != nil {
		return err
	}
	_, err = fmt.Fprintf(args.Stdout, "Added %d articles and ada@example.com (password: password).\n", len(articles))
	return err
}

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
