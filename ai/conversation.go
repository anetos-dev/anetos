// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"slices"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// Conversation is a conversation stored in the database: a row of
// ai_conversations, its messages in ai_messages ([Migrations]). It
// belongs to a user. Its methods send it to a model and store the
// answers:
//
//	conv, err := ai.StartConversation(ctx, user.AuthID(), "Orders")
//	res, err := conv.Prompt(ctx, "Where is order 1042?", support)
//
// Only one call at a time may add to a conversation: a call that
// finishes after another has added messages fails with
// [ErrConversationChanged].
type Conversation struct {
	db.Model
	// UserID is the AuthID of the user it belongs to.
	UserID string `db:"user_id" json:"user_id"`
	// Title names it in lists of the user's conversations.
	Title string `db:"title" json:"title"`
	// Status is "" when nothing is pending, [StatusQueued] while a queued
	// reply ([Conversation.QueueReply]) waits or runs, [StatusFailed] if
	// it failed for good.
	Status string `db:"status" json:"status"`
	// Error says why the queued reply failed, for the user: a message of
	// the error's (for 4xx errors, such as a spent budget) or a general
	// one.
	Error string `db:"error" json:"error"`
	// QueuedAfter is the number of messages the queued reply answers, to
	// tell its job from an older one's.
	QueuedAfter int `db:"queued_after" json:"-"`
}

// TableName implements db.Tabler.
func (Conversation) TableName() string { return "ai_conversations" }

// The statuses of a [Conversation].
const (
	StatusQueued = "queued"
	StatusFailed = "failed"
)

// storedMessage is a row of ai_messages.
type storedMessage struct {
	db.Model
	ConversationID int64   `db:"conversation_id"`
	Position       int     `db:"position"`
	Message        Message `db:"message,json"`
}

func (storedMessage) TableName() string { return "ai_messages" }

var (
	colID             = db.Col[int64]("id")
	colUserID         = db.Col[string]("user_id")
	colConversationID = db.Col[int64]("conversation_id")
	colPosition       = db.Col[int]("position")
	colStatus         = db.Col[string]("status")
	colError          = db.Col[string]("error")
	colQueuedAfter    = db.Col[int]("queued_after")
)

// ErrConversationChanged is returned by a call that would store its
// answer in a conversation another call has added to since it began
// (409). Nothing is stored; the tokens are spent.
var ErrConversationChanged error = &conflictError{"ai: the conversation changed during the call (another call added to it)"}

type conflictError struct{ msg string }

func (e *conflictError) Error() string   { return e.msg }
func (e *conflictError) HTTPStatus() int { return http.StatusConflict }

// ClientMessage is what users see (ai.SSE, a failed queued reply).
func (e *conflictError) ClientMessage() string {
	return "The conversation changed while the answer was written. Ask again."
}

// ClientFields has no field messages.
func (e *conflictError) ClientFields() map[string]string { return nil }

// errNothingToReply is the error of a reply to a conversation whose last
// message is the model's.
var errNothingToReply = errors.New("ai: the conversation's last message is the model's: there is nothing to reply to")

// Migrations returns the migrations creating the tables of stored
// conversations (ai_conversations, ai_messages) and of usage records
// (ai_usage, [Client.TrackUsage]), for migrate.ForApp.
func Migrations() *migrate.Set {
	s := migrate.NewSet("ai")
	s.AddFunc("2026_10_02_000400_create_ai_tables",
		func(s *migrate.Schema) error {
			if err := s.Create("ai_conversations", func(t *migrate.Table) {
				t.ID()
				t.String("user_id", 100)
				t.String("title", 255)
				t.String("status", 20)
				t.String("error", 255)
				t.Integer("queued_after").Default(0)
				t.Timestamps()
				t.Index("user_id", "updated_at")
			}); err != nil {
				return err
			}
			if err := s.Create("ai_messages", func(t *migrate.Table) {
				t.ID()
				t.ForeignID("conversation_id").References("ai_conversations").CascadeOnDelete()
				t.Integer("position")
				t.JSON("message")
				t.Timestamps()
				t.Unique("conversation_id", "position")
			}); err != nil {
				return err
			}
			if err := s.Create("ai_usage", func(t *migrate.Table) {
				t.ID()
				t.String("user_id", 100)
				t.BigInteger("conversation_id").Nullable() // records outlive conversations
				t.String("agent", 100)
				t.String("provider", 50)
				t.String("model", 255)
				t.BigInteger("input_tokens")
				t.BigInteger("output_tokens")
				t.BigInteger("cache_read_tokens")
				t.BigInteger("cache_write_tokens")
				t.Float("cost")
				t.Boolean("estimated")
				t.Timestamps()
				t.Index("user_id", "created_at")
			}); err != nil {
				return err
			}
			if s.Dialect() == "mysql" {
				// User IDs compare byte for byte: MySQL's and MariaDB's text
				// collations ignore case, accents and trailing spaces.
				for _, table := range []string{"ai_conversations", "ai_usage"} {
					if err := s.Exec("ALTER TABLE " + table + " MODIFY user_id VARBINARY(400) NOT NULL"); err != nil {
						return err
					}
				}
			}
			return nil
		},
		func(s *migrate.Schema) error {
			return errors.Join(s.Drop("ai_usage"), s.Drop("ai_messages"), s.Drop("ai_conversations"))
		})
	return s
}

// StartConversation stores a new conversation of the user with userID
// (an AuthID), with no messages. A title longer than 255 bytes is cut.
func StartConversation(ctx context.Context, userID, title string) (*Conversation, error) {
	if userID == "" || len(userID) > 100 {
		return nil, fmt.Errorf("ai: invalid user ID %q (1 to 100 bytes)", userID)
	}
	conv := &Conversation{UserID: userID, Title: truncate(title, 255)}
	if err := db.Create(ctx, conv); err != nil {
		return nil, err
	}
	return conv, nil
}

// FindConversation returns the conversation with the ID if it's the
// user's, else db.ErrNotFound (404): a user can't reach another's
// conversation by changing an ID in a URL.
func FindConversation(ctx context.Context, userID string, id int64) (*Conversation, error) {
	if userID == "" {
		return nil, db.ErrNotFound
	}
	conv, err := db.Query[Conversation](ctx).Where(colID.Eq(id), colUserID.Eq(userID)).First()
	if err != nil {
		return nil, err
	}
	if conv.UserID != userID {
		return nil, db.ErrNotFound
	}
	return &conv, nil
}

// Conversations returns the user's conversations, the most recently
// changed first, without their messages.
func Conversations(ctx context.Context, userID string) ([]Conversation, error) {
	if userID == "" {
		return nil, nil
	}
	all, err := db.Query[Conversation](ctx).Where(colUserID.Eq(userID)).
		OrderBy(db.Col[any]("updated_at").Desc(), colID.Desc()).Get()
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(c Conversation) bool { return c.UserID != userID }), nil
}

// Messages returns the conversation's messages, oldest first.
func (conv *Conversation) Messages(ctx context.Context) ([]Message, error) {
	rows, err := db.Query[storedMessage](ctx).Where(colConversationID.Eq(conv.ID)).OrderBy(colPosition.Asc()).Get()
	if err != nil {
		return nil, err
	}
	msgs := make([]Message, len(rows))
	for i, r := range rows {
		msgs[i] = r.Message
	}
	return msgs, nil
}

// Add stores messages at the end of the conversation, without calling a
// model: the user's prompt, before [Conversation.StreamReply] answers it
// in another request.
func (conv *Conversation) Add(ctx context.Context, msgs ...Message) error {
	if err := checkMessages(msgs); err != nil {
		return err
	}
	return conv.save(ctx, -1, msgs)
}

// Delete deletes the conversation and its messages. Its usage records
// stay.
func (conv *Conversation) Delete(ctx context.Context) error {
	return db.Tx(ctx, func(ctx context.Context) error {
		if _, err := db.Query[storedMessage](ctx).Where(colConversationID.Eq(conv.ID)).Delete(); err != nil {
			return err
		}
		return db.ForceDelete(ctx, conv)
	})
}

// Prompt sends prompt, after the conversation's messages, to the model
// ([Generate]) and stores the prompt and the answers (with any tool calls
// and results) in the conversation, or nothing if the call fails. The
// call is for the conversation's user ([ForUser]), and its usage records
// name the conversation. [Messages] options are ignored.
func (conv *Conversation) Prompt(ctx context.Context, prompt string, opts ...Option) (*Result, error) {
	if prompt == "" {
		return nil, errNothingToSend
	}
	return conv.call(ctx, prompt, opts, nil)
}

// Stream is [Conversation.Prompt] yielding the answer as it's written
// ([Stream]). The messages are stored before EventDone; a failure to
// store them ends the stream with its error instead.
func (conv *Conversation) Stream(ctx context.Context, prompt string, opts ...Option) iter.Seq2[Event, error] {
	if prompt == "" {
		return func(yield func(Event, error) bool) { yield(Event{}, errNothingToSend) }
	}
	return conv.stream(ctx, prompt, opts)
}

// Reply sends the conversation to the model to answer its last message,
// the user's (stored with [Conversation.Add]), and stores the answers.
func (conv *Conversation) Reply(ctx context.Context, opts ...Option) (*Result, error) {
	return conv.call(ctx, "", opts, nil)
}

// StreamReply is [Conversation.Reply] yielding the answer as it's
// written, as [Conversation.Stream] does.
func (conv *Conversation) StreamReply(ctx context.Context, opts ...Option) iter.Seq2[Event, error] {
	return conv.stream(ctx, "", opts)
}

// history returns the messages, checking that a reply (no prompt) has
// something to answer.
func (conv *Conversation) history(ctx context.Context, prompt string) ([]Message, error) {
	history, err := conv.Messages(ctx)
	if err != nil {
		return nil, err
	}
	if prompt == "" && (len(history) == 0 || history[len(history)-1].Role == RoleAssistant) {
		if len(history) == 0 {
			return nil, errNothingToSend
		}
		return nil, errNothingToReply
	}
	if history == nil {
		history = []Message{}
	}
	return history, nil
}

// options puts the conversation's options before the call's.
func (conv *Conversation) options(opts []Option) []Option {
	id := conv.ID
	return append([]Option{optionFunc(func(c *call) {
		c.user, c.userSet, c.conversation = conv.UserID, true, &id
	})}, opts...)
}

func (conv *Conversation) call(ctx context.Context, prompt string, opts []Option, yield func(Event) bool) (*Result, error) {
	history, err := conv.history(ctx, prompt)
	if err != nil {
		return nil, err
	}
	res, err := run(ctx, runInput{prompt: prompt, opts: conv.options(opts), conversation: history, yield: yield})
	if err != nil {
		return res, err
	}
	if err := conv.save(ctx, len(history), res.Messages[len(history):]); err != nil {
		return res, err
	}
	return res, nil
}

func (conv *Conversation) stream(ctx context.Context, prompt string, opts []Option) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		stopped := false
		res, err := conv.call(ctx, prompt, opts, func(e Event) bool {
			if !yield(e, nil) {
				stopped = true
			}
			return !stopped
		})
		switch {
		case stopped:
		case err != nil:
			yield(Event{}, err)
		default:
			yield(Event{Kind: EventDone, Result: res}, nil)
		}
	}
}

// save stores msgs after the conversation's first after messages (-1:
// after all of them), clearing its status, or returns
// [ErrConversationChanged].
func (conv *Conversation) save(ctx context.Context, after int, msgs []Message) error {
	return db.Tx(ctx, func(ctx context.Context) error {
		// Lock the conversation, so two saves take turns.
		if _, err := db.Query[Conversation](ctx).Where(colID.Eq(conv.ID)).ForUpdate().First(); err != nil {
			return err
		}
		n, err := db.Query[storedMessage](ctx).Where(colConversationID.Eq(conv.ID)).Count()
		if err != nil {
			return err
		}
		if after >= 0 && int(n) != after {
			return ErrConversationChanged
		}
		rows := make([]storedMessage, len(msgs))
		for i, m := range msgs {
			rows[i] = storedMessage{ConversationID: conv.ID, Position: int(n) + i, Message: m}
		}
		if err := db.CreateMany(ctx, rows); err != nil {
			return err
		}
		if _, err := db.Query[Conversation](ctx).Where(colID.Eq(conv.ID)).Update(colStatus.Set(""), colError.Set("")); err != nil {
			return err
		}
		conv.Status, conv.Error = "", ""
		return nil
	})
}
