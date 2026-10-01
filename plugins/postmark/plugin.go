// SPDX-License-Identifier: Apache-2.0

package postmark

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
	"anetos.dev/anetos/ext"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/web"
)

// Plugin returns the Postmark plugin: an endpoint for Postmark's
// webhooks, which records the addresses Postmark stops sending to (hard
// bounces, spam complaints, unsubscribes) in a suppression list, so the
// app can stop emailing them too ([Suppressed]). Install it with
//
//	anetos add anetos.dev/anetos/plugins/postmark
//
// It adds:
//   - settings POSTMARK_WEBHOOK_USER and POSTMARK_WEBHOOK_PASSWORD, the
//     basic auth credentials of the webhook URL you set in Postmark;
//   - the route POST /postmark/webhook (postmark.webhook), which queues
//     each event;
//   - the job postmark:webhook, which updates the list;
//   - the table postmark_suppressions (migration set postmark);
//   - the commands postmark:suppressions and postmark:unsuppress.
//
// In Postmark, add a webhook to
// https://USER:PASSWORD@example.com/postmark/webhook for the Bounce, Spam
// Complaint and Subscription Change events.
func Plugin() ext.Plugin { return &plugin{} }

// region: plugin

// Settings are the plugin's settings.
type Settings struct {
	// WebhookUser is the user name of the basic auth Postmark sends with
	// webhooks (set in the webhook's URL). Without it and the password,
	// the webhook endpoint refuses every request.
	WebhookUser string `env:"POSTMARK_WEBHOOK_USER"`
	// WebhookPassword is the webhook's password.
	WebhookPassword anetos.Secret `env:"POSTMARK_WEBHOOK_PASSWORD"`
}

type plugin struct{ cfg Settings }

func (p *plugin) Name() string     { return "postmark" }
func (p *plugin) Requires() string { return ">= v0.2.0, < v0.3.0" }
func (p *plugin) Config() any      { return &p.cfg }

// endregion

// Suppression is an address Postmark stopped sending to.
type Suppression struct {
	db.Model
	// Email is the address, in lower case.
	Email string `db:"email" json:"email"`
	// RecordType is the webhook event: Bounce, SpamComplaint or
	// SubscriptionChange.
	RecordType string `db:"record_type" json:"record_type"`
	// Reason is Postmark's description: the bounce type, the
	// unsubscribe's origin.
	Reason string `db:"reason" json:"reason"`
	// MessageID is the Postmark ID of the email the event is about.
	MessageID string `db:"message_id" json:"message_id"`
}

// TableName implements db.Tabler.
func (Suppression) TableName() string { return "postmark_suppressions" }

var colEmail = db.Col[string]("email")

// region: migrations

// Migrations implements ext.HasMigrations.
func (p *plugin) Migrations() *migrate.Set {
	s := migrate.NewSet("postmark")
	s.AddFunc("2026_10_02_000000_create_postmark_suppressions",
		func(s *migrate.Schema) error {
			return s.Create("postmark_suppressions", func(t *migrate.Table) {
				t.ID()
				t.String("email", 254)
				t.String("record_type", 40)
				t.String("reason", 255)
				t.String("message_id", 64)
				t.Timestamps()
				t.Unique("email")
			})
		},
		func(s *migrate.Schema) error { return s.Drop("postmark_suppressions") })
	return s
}

// endregion

// Event is the part of a Postmark webhook the plugin uses.
type Event struct {
	// RecordType is Bounce, SpamComplaint or SubscriptionChange.
	RecordType string `json:"RecordType"`
	// Email is a bounce's or complaint's address.
	Email string `json:"Email"`
	// Recipient is a subscription change's address.
	Recipient string `json:"Recipient"`
	// Inactive says Postmark stopped sending to a bounce's or
	// complaint's address.
	Inactive bool `json:"Inactive"`
	// SuppressSending says whether a subscription change stops or
	// resumes sending.
	SuppressSending bool `json:"SuppressSending"`
	// Type is a bounce's type (HardBounce, …).
	Type string `json:"Type"`
	// SuppressionReason is a subscription change's reason.
	SuppressionReason string `json:"SuppressionReason"`
	// MessageID is the email's Postmark ID.
	MessageID string `json:"MessageID"`
}

// region: routes

// Routes implements ext.HasRoutes: POST /webhook under the plugin's
// prefix.
func (p *plugin) Routes(r *web.Router) error {
	r.Post("/webhook", p.webhook).Name("webhook")
	return nil
}

// webhook checks the credentials and queues the event: Postmark retries
// a webhook that doesn't get a 2xx quickly.
func (p *plugin) webhook(c *web.Ctx) error {
	if !p.authorized(c.Request()) {
		c.Writer().Header().Set("WWW-Authenticate", `Basic realm="postmark"`)
		return web.Error(http.StatusUnauthorized, "unauthorized")
	}
	var e Event
	body := http.MaxBytesReader(c.Writer(), c.Request().Body, 1<<20) // events are a few KB
	if err := json.NewDecoder(body).Decode(&e); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return web.Error(http.StatusRequestEntityTooLarge, "too large")
		}
		return web.Error(http.StatusBadRequest, "invalid JSON")
	}
	switch e.RecordType {
	case "Bounce", "SpamComplaint", "SubscriptionChange":
		if a := e.address(); a != "" && len(a) <= 254 {
			if err := queue.DispatchFunc(c, "postmark:webhook", e); err != nil {
				return err
			}
		}
	}
	return c.NoContent() // other events, and events without an address: acknowledged, ignored
}

// endregion

// authorized checks the request's basic auth, in constant time.
func (p *plugin) authorized(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	if !ok || p.cfg.WebhookUser == "" || p.cfg.WebhookPassword == "" {
		return false
	}
	hash := func(s string) []byte { h := sha256.Sum256([]byte(s)); return h[:] }
	userOK := subtle.ConstantTimeCompare(hash(user), hash(p.cfg.WebhookUser))
	passOK := subtle.ConstantTimeCompare(hash(pass), hash(string(p.cfg.WebhookPassword)))
	return userOK&passOK == 1
}

// region: jobs

// Jobs implements ext.HasJobs: the job postmark:webhook, run by the
// app's workers.
func (p *plugin) Jobs(q *queue.Queue) error {
	return queue.RegisterFunc(q, "postmark:webhook", record)
}

// endregion

// record updates the suppression list for an event. Only a
// subscription change resumes sending: a bounce or complaint that
// didn't deactivate the address (a soft bounce, possibly late) leaves
// the list as it is.
func record(ctx context.Context, e Event) error {
	email, reason, suppress := e.address(), e.Type, e.Inactive
	if e.RecordType == "SubscriptionChange" {
		reason, suppress = e.SuppressionReason, e.SuppressSending
	}
	if email == "" || len(email) > 254 {
		return queue.Permanent(fmt.Errorf("postmark: an event without a valid address (%d bytes)", len(email)))
	}
	if !suppress {
		if e.RecordType != "SubscriptionChange" {
			return nil
		}
		_, err := db.Query[Suppression](ctx).Where(colEmail.Eq(email)).Delete()
		return err
	}
	return db.Upsert(ctx, []Suppression{{Email: email, RecordType: e.RecordType, Reason: truncate(reason, 255), MessageID: truncate(e.MessageID, 64)}},
		[]string{"email"}, "record_type", "reason", "message_id")
}

// address is the event's address, normalized.
func (e Event) address() string {
	if e.RecordType == "SubscriptionChange" {
		return normalize(e.Recipient)
	}
	return normalize(e.Email)
}

// normalize is the form addresses are kept in.
func normalize(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// truncate cuts s to at most n bytes, at a character boundary.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// Suppressed reports whether Postmark stopped sending to email (its
// webhooks said so), for apps that check before sending:
//
//	if bad, err := postmark.Suppressed(ctx, user.Email); err != nil || bad { … }
func Suppressed(ctx context.Context, email string) (bool, error) {
	return db.Query[Suppression](ctx).Where(colEmail.Eq(normalize(email))).Exists()
}

// Commands implements ext.HasCommands.
func (p *plugin) Commands() []cmd.Command {
	return []cmd.Command{{
		Name:        "postmark:suppressions",
		Description: "List the addresses Postmark stopped sending to",
		Run: func(ctx context.Context, args *cmd.Args) error {
			if len(args.Args) > 0 {
				return cmd.Usagef("postmark:suppressions takes no arguments")
			}
			rows, err := db.Query[Suppression](ctx).OrderBy(colEmail.Asc()).Get()
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				_, err := fmt.Fprintln(args.Stdout, "No suppressed addresses.")
				return err
			}
			tw := tabwriter.NewWriter(args.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "EMAIL\tEVENT\tREASON\tSINCE")
			for _, s := range rows {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", s.Email, s.RecordType, s.Reason, s.UpdatedAt.Format(time.DateTime))
			}
			return tw.Flush()
		},
	}, {
		Name:        "postmark:unsuppress",
		Usage:       "<email>",
		Description: "Remove an address from the suppression list (reactivate it in Postmark too)",
		Run: func(ctx context.Context, args *cmd.Args) error {
			if len(args.Args) != 1 {
				return cmd.Usagef("postmark:unsuppress takes an address")
			}
			n, err := db.Query[Suppression](ctx).Where(colEmail.Eq(normalize(args.Args[0]))).Delete()
			if err != nil {
				return err
			}
			if n == 0 {
				return fmt.Errorf("%s isn't suppressed", args.Args[0])
			}
			_, err = fmt.Fprintf(args.Stdout, "Removed %s. Reactivate it in Postmark too, or Postmark won't send to it.\n", args.Args[0])
			return err
		},
	}}
}
