// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"anetos.dev/anetos"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/queue"
)

// queuedAgents are the agents that may answer queued replies, by name.
type queuedAgents map[string]Agent

type queuedAgentsKey struct{}

// QueueAgents lets the agents answer conversations from queue jobs
// ([Conversation.QueueReply]): it registers the job type "ai.reply" on
// the app's queue (queue.New, called first), with AI_QUEUE_TIMEOUT
// (default 15m). Agents are found by name, so each needs a unique Name;
// a worker must run the same code. The job's timeout is also how long
// the queue's workers wait before taking back a job of any type whose
// worker died.
//
//	if err := ai.QueueAgents(app, agents.Support, agents.Research); err != nil {
func QueueAgents(app *anetos.App, agents ...Agent) error {
	q, err := anetos.Resolve[*queue.Queue](app)
	if err != nil {
		return errors.New("ai: QueueAgents needs the app's queue: call queue.New first")
	}
	if _, ok := anetos.Lookup[queuedAgents](app); ok {
		return errors.New("ai: QueueAgents called twice for one app")
	}
	byName := queuedAgents{}
	for _, a := range agents {
		if a.Name == "" {
			return errors.New("ai: QueueAgents: an agent has no Name")
		}
		if _, dup := byName[a.Name]; dup {
			return fmt.Errorf("ai: QueueAgents: two agents are named %q", a.Name)
		}
		byName[a.Name] = a
	}
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return err
	}
	if err := queue.Register[replyJob](q, queue.Name("ai.reply"), queue.Timeout(cfg.QueueTimeout)); err != nil {
		return err
	}
	anetos.Provide(app, byName)
	app.AddContextValue(queuedAgentsKey{}, byName)
	return nil
}

// QueueReply answers the conversation's last message, the user's (stored
// with [Conversation.Add]), from a queue job: [Conversation.Reply] with
// the agent passed to [QueueAgents] under agent.Name (the registered
// agent's settings: the job carries only the name), as the
// conversation's user (auth.WithUser, when the app has auth: its tools see
// the user as in a request, with the abilities of the API token ctx was
// logged in with, if any). The conversation's Status is [StatusQueued]
// until the reply is stored, or [StatusFailed] (with Error) if it fails
// for good; poll it to show the answer. The job is dispatched when the
// transaction in ctx, if any, commits; with the sync queue driver, it
// runs then, in the same request.
//
// A failed attempt is retried as the queue's settings say, from the
// start: its tool calls run again, so tools that change things must be
// safe to repeat. A queued reply does nothing if the conversation has
// changed by the time it runs (a message added), nor does a retry once
// the reply is stored, so a question gets one answer.
func (conv *Conversation) QueueReply(ctx context.Context, agent Agent) error {
	agents, _ := ctx.Value(queuedAgentsKey{}).(queuedAgents)
	if _, ok := agents[agent.Name]; !ok || agent.Name == "" {
		return fmt.Errorf("ai: agent %q can't answer queued replies: pass it to ai.QueueAgents", agent.Name)
	}
	if _, err := conv.history(ctx, ""); err != nil {
		return err
	}
	job := replyJob{Conversation: conv.ID, User: conv.UserID, Agent: agent.Name}
	if tok, ok := auth.CurrentToken(ctx); ok {
		job.Abilities = slices.Clone(tok.Abilities)
		if job.Abilities == nil {
			job.Abilities = []string{}
		}
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		n, err := db.Query[storedMessage](ctx).Where(colConversationID.Eq(conv.ID)).Count()
		if err != nil {
			return err
		}
		job.After = int(n)
		if _, err := db.Query[Conversation](ctx).Where(colID.Eq(conv.ID)).
			Update(colStatus.Set(StatusQueued), colError.Set(""), colQueuedAfter.Set(job.After)); err != nil {
			return err
		}
		conv.Status, conv.Error, conv.QueuedAfter = StatusQueued, "", job.After
		return queue.Dispatch(ctx, job, queue.AfterCommit())
	})
}

// replyJob is a queued reply.
type replyJob struct {
	Conversation int64  `json:"conversation"`
	User         string `json:"user"`
	Agent        string `json:"agent"`
	After        int    `json:"after"` // the messages it answers
	// Abilities are those of the API token the reply was queued with;
	// nil for none (a session).
	Abilities []string `json:"abilities,omitempty"`
}

// mine selects the conversation while this job is its queued reply.
func (j replyJob) mine() db.Expr {
	return db.And(colID.Eq(j.Conversation), colStatus.Eq(StatusQueued), colQueuedAfter.Eq(j.After))
}

// Handle implements queue.Job.
func (j replyJob) Handle(ctx context.Context) error {
	agents, _ := ctx.Value(queuedAgentsKey{}).(queuedAgents)
	agent, ok := agents[j.Agent]
	if !ok {
		return queue.Permanent(fmt.Errorf("ai: no agent %q for queued replies (ai.QueueAgents)", j.Agent))
	}
	conv, err := FindConversation(ctx, j.User, j.Conversation)
	if errors.Is(err, db.ErrNotFound) {
		return nil // deleted since
	}
	if err != nil {
		return err
	}
	n, err := db.Query[storedMessage](ctx).Where(colConversationID.Eq(conv.ID)).Count()
	if err != nil {
		return err
	}
	if int(n) != j.After {
		// Answered already, or changed since: the reply is no longer the
		// job's to give.
		_, err := db.Query[Conversation](ctx).Where(j.mine()).Update(colStatus.Set(""))
		return err
	}
	if j.User != "" {
		var opts []auth.UserOption
		if j.Abilities != nil {
			opts = append(opts, auth.WithAbilities(j.Abilities))
		}
		if acting, err := auth.WithUser(ctx, j.User, opts...); err == nil {
			ctx = acting
		}
	}
	_, err = conv.Reply(ctx, agent)
	switch {
	case errors.Is(err, ErrConversationChanged):
		return nil
	case clientStatus(err) != 0:
		return queue.Permanent(err) // 4xx: a spent budget, a tool refused…; retrying won't help
	}
	return err
}

// Failed implements queue.Failer: the conversation shows the failure.
func (j replyJob) Failed(ctx context.Context, err error) {
	msg := "The reply failed. Try again."
	if s := clientStatus(err); s != 0 {
		msg = clientMessage(err)
	}
	if _, uerr := db.Query[Conversation](ctx).Where(j.mine()).
		Update(colStatus.Set(StatusFailed), colError.Set(truncate(msg, 255))); uerr != nil {
		anetos.Logger(ctx).ErrorContext(ctx, "ai: marking a failed queued reply failed", "conversation", j.Conversation, "error", uerr)
	}
}

// clientStatus returns err's status if it's a 4xx (the request's, not the
// server's fault), else 0.
func clientStatus(err error) int {
	var sc statusCoder
	if err == nil || !errors.As(err, &sc) {
		return 0
	}
	if s := sc.HTTPStatus(); s >= 400 && s <= 499 {
		return s
	}
	return 0
}

// clientMessage returns what a user may see of a 4xx error: the message
// of a *web.HTTPError, else the status text.
func clientMessage(err error) string {
	var sc statusCoder
	if !errors.As(err, &sc) {
		return http.StatusText(http.StatusInternalServerError)
	}
	if ce, ok := sc.(clientError); ok {
		return ce.ClientMessage()
	}
	return http.StatusText(sc.HTTPStatus())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 { // not in the middle of a character
		n--
	}
	return s[:n]
}
