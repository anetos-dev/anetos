// SPDX-License-Identifier: Apache-2.0

package dbtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/web"
)

func init() {
	extra = append(extra, test{"AIConversations", testAIConversations})
}

type stLookup struct {
	Number int `json:"number" validate:"required"`
}

// testAIConversations stores conversations, usage records and queued
// replies in the tables ai.Migrations creates.
func testAIConversations(t *testing.T, ctx context.Context) {
	r, err := migrate.NewRunner(d(ctx), []*migrate.Set{ai.Migrations()}, migrate.WithTable("st_ai_migrations"))
	check(t, err)
	_, err = r.Up(ctx)
	check(t, err)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		for _, table := range []string{"ai_usage", "ai_messages", "ai_conversations", "st_ai_migrations"} {
			_, _ = db.Exec(ctx, "DROP TABLE IF EXISTS "+table)
		}
	})
	app, err := anetos.New(anetos.WithSource(config.Map{"AI_PROVIDER": "fake", "APP_KEY": encryption.GenerateKey()}), anetos.WithLogOutput(io.Discard))
	check(t, err)
	_, err = cache.New(app)
	check(t, err)
	a, err := auth.New(app, auth.Users[stAuthUser]{
		ByID:    func(_ context.Context, id string) (stAuthUser, error) { return stAuthUser{id}, nil },
		ByLogin: func(context.Context, string) (stAuthUser, error) { return stAuthUser{}, auth.ErrNoUser },
	})
	check(t, err)
	_, err = queue.New(app) // sync: queued jobs run at once
	check(t, err)
	client, err := ai.New(app)
	check(t, err)
	client.TrackUsage(ai.UsageConfig{
		Prices: map[string]ai.Price{"fake-1": {Input: 2, Output: 10}},
		Budget: func(_ context.Context, userID string) (ai.Budget, error) {
			if userID == "frugal" {
				return ai.Budget{Tokens: 5, Per: 24 * time.Hour}, nil
			}
			return ai.Budget{}, nil
		},
	})
	var conv *ai.Conversation
	lookup := ai.Func("lookup", "Look up an order", func(ctx context.Context, in stLookup) (string, error) {
		if in.Number == 13 { // a tool adding to the conversation during the call
			return "", conv.Add(ctx, ai.UserMessage("interrupting"))
		}
		return "shipped", nil
	})
	var seen string // who the whoami tool ran as, and whether it could read orders
	whoami := ai.Func("whoami", "Who is asking", func(ctx context.Context, _ struct{}) (string, error) {
		id, err := auth.CurrentID(ctx)
		seen = fmt.Sprintf("%s %v", id, auth.TokenCan(ctx, "orders:read"))
		return id, err
	})
	support := ai.Agent{Name: "support", Instructions: "Answer about orders.", Tools: []ai.Tool{lookup, whoami}, Model: "fake-1"}
	check(t, ai.QueueAgents(app, support))
	ctx = app.Context(ctx)
	fake := client.Fake()

	conv, err = ai.StartConversation(ctx, "ada", "Orders")
	check(t, err)
	fake.Add(ai.FakeToolCall("lookup", stLookup{Number: 1042}), ai.FakeText("Order 1042 shipped."))
	res, err := conv.Prompt(ctx, "Where is order 1042?", support)
	check(t, err)
	if res.Text() != "Order 1042 shipped." {
		t.Errorf("answer %q", res.Text())
	}
	msgs, err := conv.Messages(ctx)
	check(t, err)
	if len(msgs) != 4 || msgs[0].Text() != "Where is order 1042?" || len(msgs[1].ToolCalls()) != 1 ||
		msgs[1].ToolCalls()[0].Name != "lookup" || msgs[2].Role != ai.RoleTool || msgs[3].Text() != "Order 1042 shipped." {
		t.Fatalf("stored messages: %+v", msgs)
	}

	// The next prompt carries the conversation.
	fake.Add(ai.FakeText("You're welcome."))
	_, err = conv.Prompt(ctx, "Thanks!", support)
	check(t, err)
	if reqs := fake.Requests(); len(reqs[len(reqs)-1].Messages) != 5 {
		t.Errorf("the model got %d messages", len(reqs[len(reqs)-1].Messages))
	}

	// Add, then a streamed reply.
	check(t, conv.Add(ctx, ai.UserMessage("One more thing.")))
	fake.Add(ai.FakeText("Go ahead."))
	var text strings.Builder
	var done bool
	for ev, err := range conv.StreamReply(ctx, support) {
		check(t, err)
		switch ev.Kind {
		case ai.EventText:
			text.WriteString(ev.Text)
		case ai.EventDone:
			done = true
		}
	}
	if text.String() != "Go ahead." || !done {
		t.Errorf("streamed %q, done %v", text.String(), done)
	}
	if _, err := conv.Reply(ctx, support); err == nil {
		t.Error("a reply to the model's own message")
	}
	msgs, _ = conv.Messages(ctx)
	if len(msgs) != 8 {
		t.Errorf("%d messages", len(msgs))
	}

	// A call during which the conversation changes stores nothing.
	fake.Add(ai.FakeToolCall("lookup", stLookup{Number: 13}), ai.FakeText("Hm."))
	if _, err := conv.Prompt(ctx, "Order 13?", support); !errors.Is(err, ai.ErrConversationChanged) || web.StatusOf(err) != http.StatusConflict {
		t.Errorf("a changed conversation: %v", err)
	}
	if msgs, _ := conv.Messages(ctx); len(msgs) != 9 || msgs[8].Text() != "interrupting" {
		t.Errorf("after the conflict: %d messages", len(msgs))
	}

	// Conversations are their user's.
	if _, err := ai.FindConversation(ctx, "bob", conv.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("bob found ada's conversation: %v", err)
	}
	if _, err := ai.FindConversation(ctx, "ADA", conv.ID); !errors.Is(err, db.ErrNotFound) {
		t.Errorf("ADA found ada's conversation: %v", err)
	}
	other, err := ai.StartConversation(ctx, "ada", "Billing")
	check(t, err)
	_, err = ai.StartConversation(ctx, "bob", "Bob's")
	check(t, err)
	list, err := ai.Conversations(ctx, "ada")
	check(t, err)
	if len(list) != 2 {
		t.Errorf("ada's conversations: %+v", list)
	}

	// Usage records, with the prices.
	total, err := ai.TotalUsage(ctx, "ada", time.Now().Add(-time.Hour))
	check(t, err)
	if total.Responses != 6 || total.InputTokens == 0 || total.OutputTokens == 0 || total.Cost <= 0 {
		t.Errorf("ada's usage: %+v", total)
	}
	recs, err := db.Query[ai.UsageRecord](ctx).Where(db.Col[string]("user_id").Eq("ada")).Get()
	check(t, err)
	if len(recs) != 6 || recs[0].ConversationID == nil || *recs[0].ConversationID != conv.ID || recs[0].Agent != "support" || recs[0].Provider != "fake" {
		t.Errorf("records: %+v", recs)
	}

	// A spent budget refuses calls (429), before asking the model.
	frugal, err := ai.StartConversation(ctx, "frugal", "")
	check(t, err)
	fake.Add(ai.FakeText("one two three four five six"), ai.FakeText("never"))
	_, err = frugal.Prompt(ctx, "Hello there", support)
	check(t, err)
	_, err = frugal.Prompt(ctx, "Again", support)
	be, ok := errors.AsType[*ai.BudgetError](err)
	if !ok || web.StatusOf(err) != http.StatusTooManyRequests || be.UserID != "frugal" || be.RetryAfter <= 0 {
		t.Errorf("over budget: %v", err)
	}
	if fake.Remaining() != 1 {
		t.Error("the model was asked over budget")
	}
	// Embeddings count too: for the signed-in user, against their budget.
	if _, err := ai.Embed(a.ActAs(ctx, "frugal"), 0, "spent"); web.StatusOf(err) != http.StatusTooManyRequests {
		t.Errorf("embedding over budget: %v", err)
	}
	if _, err := ai.Embed(a.ActAs(ctx, "embedder"), 0, "three little words", "and four more words"); err != nil {
		t.Error(err)
	}
	if recs, err := db.Query[ai.UsageRecord](ctx).Where(db.Col[string]("user_id").Eq("embedder")).Get(); err != nil || len(recs) != 1 ||
		recs[0].Model != "fake-embedding" || recs[0].Agent != "embed" || recs[0].InputTokens != 7 || recs[0].Provider != "fake" {
		t.Errorf("embedding usage: %+v, %v", recs, err)
	}
	fake = client.Fake() // drop the unused reply
	for fake.Remaining() > 0 {
		_, _ = ai.Generate(ctx, "drain", ai.ForUser("drain"))
	}

	// Queued replies run as queue jobs (here, at once: the sync driver).
	check(t, other.Add(ctx, ai.UserMessage("Is invoice 7 paid?")))
	fake.Add(ai.FakeText("Yes."))
	check(t, other.QueueReply(ctx, support))
	other, err = ai.FindConversation(ctx, "ada", other.ID)
	check(t, err)
	if msgs, _ := other.Messages(ctx); len(msgs) != 2 || msgs[1].Text() != "Yes." || other.Status != "" {
		t.Errorf("queued reply: %+v, status %q", msgs, other.Status)
	}
	// A failed one shows on the conversation.
	check(t, other.Add(ctx, ai.UserMessage("And invoice 8?")))
	fake.Add(ai.FakeError(web.Error(http.StatusTooManyRequests, "Slow down.")))
	check(t, other.QueueReply(ctx, support)) // the job runs once QueueReply's transaction commits
	other, _ = ai.FindConversation(ctx, "ada", other.ID)
	if other.Status != ai.StatusFailed || other.Error != "Slow down." {
		t.Errorf("failed reply: status %q, error %q", other.Status, other.Error)
	}
	if err := other.QueueReply(ctx, ai.Agent{Name: "stranger"}); err == nil {
		t.Error("an agent not passed to QueueAgents")
	}

	// The job's tools run as the conversation's user, with the abilities
	// of the token the reply was queued with.
	check(t, other.Add(ctx, ai.UserMessage("Who am I?")))
	fake.Add(ai.FakeToolCall("whoami", struct{}{}), ai.FakeText("Ada."))
	check(t, other.QueueReply(a.ActAs(ctx, "ada", auth.WithAbilities([]string{"chat:write"})), support))
	if seen != "ada false" {
		t.Errorf("the queued reply's tool ran as %q", seen)
	}
	check(t, other.Add(ctx, ai.UserMessage("And now?")))
	fake.Add(ai.FakeToolCall("whoami", struct{}{}), ai.FakeText("Ada."))
	check(t, other.QueueReply(a.ActAs(ctx, "ada"), support)) // a session: no token limits
	if seen != "ada true" {
		t.Errorf("the queued reply's tool ran as %q", seen)
	}

	// A job that finds a newer question does nothing, and leaves a newer
	// job's status alone: one answer, from the last job.
	fake.Add(ai.FakeText("Both answered."))
	n0 := len(fake.Requests())
	check(t, db.Tx(ctx, func(ctx context.Context) error { // the jobs run after the commit
		check(t, other.Add(ctx, ai.UserMessage("First?")))
		check(t, other.QueueReply(ctx, support))
		check(t, other.Add(ctx, ai.UserMessage("Second?")))
		return other.QueueReply(ctx, support)
	}))
	other, _ = ai.FindConversation(ctx, "ada", other.ID)
	msgs, _ = other.Messages(ctx)
	if other.Status != "" || msgs[len(msgs)-1].Text() != "Both answered." || len(fake.Requests()) != n0+1 {
		t.Errorf("stale job: status %q, last %q", other.Status, msgs[len(msgs)-1].Text())
	}

	// Deleting a conversation deletes its messages, not its usage.
	check(t, conv.Delete(ctx))
	if n, err := db.Query[ai.Conversation](ctx).Where(db.Col[int64]("id").Eq(conv.ID)).Count(); err != nil || n != 0 {
		t.Errorf("deleted conversation: %d, %v", n, err)
	}
	if msgs, _ := conv.Messages(ctx); len(msgs) != 0 {
		t.Errorf("deleted conversation's messages: %d", len(msgs))
	}
	if total, _ := ai.TotalUsage(ctx, "ada", time.Now().Add(-time.Hour)); total.Responses < 6 {
		t.Errorf("usage after the delete: %+v", total)
	}
}
