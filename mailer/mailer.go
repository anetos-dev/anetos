// SPDX-License-Identifier: Apache-2.0

package mailer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"anetos.dev/anetos/queue"
)

// Transport sends rendered emails: SMTP, an API, the log. Drivers
// provide them; [MemoryTransport] keeps them, for tests.
type Transport interface {
	// Send sends m, which [Outgoing.Validate] accepted. An error that
	// retrying won't fix (a rejected address, a refused message) should
	// be queue.Permanent, so a queued email fails at once.
	Send(ctx context.Context, m *Outgoing) error
}

// Mailer sends emails with a transport. Create it with [ForApp] (or
// [New]); send with [Send] or [Queue], which find it in the context.
type Mailer struct {
	transport Transport
	from      Address
	url       string
	log       *slog.Logger
	now       func() time.Time
	queued    atomic.Bool // the queue has the mail:send job

	obsMu     sync.RWMutex
	observers []func(context.Context, Record)
}

// Record is an email the mailer sent or queued, for [Mailer.Observe].
type Record struct {
	// Mailable is the mailable given to Send or Queue.
	Mailable Mailable
	// Message is the email it rendered.
	Message *Outgoing
	// Queued says it went through Queue: a job sends it.
	Queued bool
}

// Observe calls fn with each email the mailer sends with [Send] (once
// the transport took it) or queues with [Queue] (once the job is
// dispatched: with queue.AfterCommit, after the commit; not if the
// transaction rolls back; with the sync driver, once the job sent it)
// from now on, for tests and instrumentation. fn must be
// quick and safe for concurrent use. anetostest uses it to record mail.
func (m *Mailer) Observe(fn func(ctx context.Context, r Record)) {
	m.obsMu.Lock()
	defer m.obsMu.Unlock()
	m.observers = append(m.observers, fn)
}

func (m *Mailer) observe(ctx context.Context, r Record) {
	m.obsMu.RLock()
	obs := m.observers
	m.obsMu.RUnlock()
	for _, fn := range obs {
		fn(ctx, r)
	}
}

// Option configures a [Mailer] made with [New].
type Option func(*Mailer)

// DefaultFrom sets the sender of messages without one.
func DefaultFrom(a Address) Option { return func(m *Mailer) { m.from = a } }

// BaseURL sets the app's public URL, for [URL].
func BaseURL(url string) Option { return func(m *Mailer) { m.url = strings.TrimSuffix(url, "/") } }

// WithLogger sets the mailer's logger. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(m *Mailer) { m.log = l } }

// New returns a mailer that sends with t.
func New(t Transport, opts ...Option) *Mailer {
	m := &Mailer{transport: t, log: slog.Default(), now: time.Now}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Transport returns the mailer's transport: in tests, a
// *[MemoryTransport] to check what was sent.
func (m *Mailer) Transport() Transport { return m.transport }

type mailerKey struct{}

// WithMailer returns ctx with m, for [Send] and [Queue]. [ForApp] makes
// the mailer available in every context the app creates.
func WithMailer(ctx context.Context, m *Mailer) context.Context {
	return context.WithValue(ctx, mailerKey{}, m)
}

// ErrNoMailer is returned by [From] (and [Send], [Queue]) when the
// context has no mailer.
var ErrNoMailer = errors.New("mailer: no mailer in the context: call mailer.ForApp at startup, or mailer.WithMailer")

// From returns the mailer in ctx.
func From(ctx context.Context) (*Mailer, error) {
	if m, ok := ctx.Value(mailerKey{}).(*Mailer); ok {
		return m, nil
	}
	return nil, ErrNoMailer
}

// Send builds, renders and sends mailable now, with the mailer in ctx,
// and returns the transport's error:
//
//	err := mailer.Send(ctx, mails.Welcome{User: u})
func Send(ctx context.Context, mailable Mailable) error {
	m, err := From(ctx)
	if err != nil {
		return err
	}
	return m.Send(ctx, mailable)
}

// Send builds, renders and sends mailable now.
func (m *Mailer) Send(ctx context.Context, mailable Mailable) error {
	o, err := m.render(ctx, mailable)
	if err != nil {
		return err
	}
	if err := m.send(ctx, o); err != nil {
		return err
	}
	m.observe(ctx, Record{Mailable: mailable, Message: o})
	return nil
}

// send sends o with the transport.
func (m *Mailer) send(ctx context.Context, o *Outgoing) error {
	if err := m.transport.Send(ctx, o); err != nil {
		return fmt.Errorf("mailer: send %q to %s: %w", o.Subject, strings.Join(o.Recipients(), ", "), err)
	}
	m.log.DebugContext(ctx, "mail sent", "subject", o.Subject, "to", strings.Join(o.Recipients(), ", "), "message_id", o.MessageID)
	return nil
}

// Render builds and renders mailable without sending it: for previews
// and tests.
func (m *Mailer) Render(ctx context.Context, mailable Mailable) (*Outgoing, error) {
	return m.render(ctx, mailable)
}

// sendJob is the queue job of queued emails.
const sendJob = "mail:send"

// Queue builds and renders mailable now, with ctx (a request's, say),
// and dispatches a queue job that sends it, with the queue's retries:
// opts are the dispatch's (queue.OnQueue, queue.Delay, queue.AfterCommit).
// It needs the app's queue (queue.ForApp). The job carries the rendered
// email, attachments included (and a failed job keeps it): send big
// files from a job of your own, with [Send]. The email's Date is when
// the job sends it.
//
//	err := mailer.Queue(c, mails.Receipt{Order: o}, queue.OnQueue("emails"))
func Queue(ctx context.Context, mailable Mailable, opts ...queue.DispatchOption) error {
	m, err := From(ctx)
	if err != nil {
		return err
	}
	return m.Queue(ctx, mailable, opts...)
}

// Queue is the function [Queue] with this mailer.
func (m *Mailer) Queue(ctx context.Context, mailable Mailable, opts ...queue.DispatchOption) error {
	if !m.queued.Load() {
		return errors.New("mailer: Queue needs the app's queue, set up before the app boots (queue.ForApp)")
	}
	o, err := m.render(ctx, mailable)
	if err != nil {
		return err
	}
	r := Record{Mailable: mailable, Message: o, Queued: true}
	// Recorded once dispatched: with the sync driver, during DispatchFunc,
	// then only if the email was sent; otherwise when the store has it
	// (after the commit, with queue.AfterCommit).
	var (
		mu      sync.Mutex
		inline  = true
		pending []context.Context
	)
	record := queue.OnDispatched(func(ctx context.Context, _ queue.Dispatched) {
		mu.Lock()
		if inline {
			pending = append(pending, ctx)
			mu.Unlock()
			return
		}
		mu.Unlock()
		m.observe(ctx, r)
	})
	err = queue.DispatchFunc(ctx, sendJob, o, append(slices.Clone(opts), record)...)
	mu.Lock()
	inline = false
	ctxs := pending
	mu.Unlock()
	if err != nil {
		return err
	}
	for _, ctx := range ctxs {
		m.observe(ctx, r)
	}
	return nil
}

// register adds the job that sends queued emails to q.
func (m *Mailer) register(q *queue.Queue) error {
	if err := queue.RegisterFunc(q, sendJob, func(ctx context.Context, o Outgoing) error {
		o.Date = m.now().UTC().Truncate(time.Second) // sent now, perhaps after a delay or retries
		if err := o.Validate(); err != nil {
			return queue.Permanent(err)
		}
		return m.send(ctx, &o)
	}); err != nil {
		return err
	}
	m.queued.Store(true)
	return nil
}

// URL returns the absolute URL of path (an absolute path, "/orders/1")
// on the app's public URL (APP_URL), for links in emails, which leave
// the app:
//
//	<a href={ mailer.URL(ctx, "/orders/"+id) }>Your order</a>
func URL(ctx context.Context, path string) (string, error) {
	m, err := From(ctx)
	if err != nil {
		return "", err
	}
	if m.url == "" {
		return "", errors.New("mailer: URL needs the app's public URL: set APP_URL")
	}
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return "", fmt.Errorf("mailer: URL(%q): the path must start with a single /", path)
	}
	return m.url + path, nil
}

// Preview returns a handler that shows the HTML body of the mailable
// newMailable returns, rendered with the request's context, for viewing
// emails in a browser during development. Don't serve it in production:
// it shows whatever the mailable puts in the email.
//
//	if app.Config().Env.IsDevelopment() {
//		r.HandleStd("GET", "/dev/mail/receipt", mailer.Preview(func(r *http.Request) mailer.Mailable { return mails.Receipt{…} }))
//	}
func Preview(newMailable func(r *http.Request) Mailable) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m, err := From(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		o, err := m.render(r.Context(), newMailable(r))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if o.HTML == "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte(o.Text))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(o.HTML))
	})
}
