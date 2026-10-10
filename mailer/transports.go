// SPDX-License-Identifier: Apache-2.0

package mailer

import (
	"context"
	"log/slog"
	"strings"
	"sync"
)

// LogTransport writes emails to a log instead of sending them: the
// sender, recipients, subject and text body, at Info.
type LogTransport struct {
	log    *slog.Logger
	noBody bool
}

// NewLogTransport returns a transport that writes emails to log.
func NewLogTransport(log *slog.Logger) *LogTransport { return &LogTransport{log: log} }

// WithoutBodies returns the transport logging everything but the emails'
// bodies, which may hold login or reset links: what MAIL_DRIVER=log
// does in production, where logs are read by more people.
func (t *LogTransport) WithoutBodies() *LogTransport { return &LogTransport{log: t.log, noBody: true} }

// Send implements [Transport].
func (t *LogTransport) Send(ctx context.Context, m *Outgoing) error {
	attrs := []any{"from", m.From.String(), "to", list(m.To), "subject", m.Subject, "message_id", m.MessageID}
	if len(m.Cc) > 0 {
		attrs = append(attrs, "cc", list(m.Cc))
	}
	if len(m.Bcc) > 0 {
		attrs = append(attrs, "bcc", list(m.Bcc))
	}
	if len(m.Attachments) > 0 {
		names := make([]string, len(m.Attachments))
		for i, a := range m.Attachments {
			names[i] = a.Filename
		}
		attrs = append(attrs, "attachments", strings.Join(names, ", "))
	}
	if t.noBody {
		attrs = append(attrs, "text", "(not logged in production)")
	} else {
		attrs = append(attrs, "text", m.Text)
	}
	t.log.InfoContext(ctx, "mail (log driver: not sent)", attrs...)
	return nil
}

func list(as []Address) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

// MemoryTransport keeps the emails it is given, for tests:
//
//	sent := anetos.MustResolve[*mailer.Mailer](app).Transport().(*mailer.MemoryTransport).Sent()
type MemoryTransport struct {
	mu   sync.Mutex
	sent []*Outgoing
}

// NewMemoryTransport returns an empty memory transport.
func NewMemoryTransport() *MemoryTransport { return &MemoryTransport{} }

// Send implements [Transport].
func (t *MemoryTransport) Send(ctx context.Context, m *Outgoing) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c := *m
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = append(t.sent, &c)
	return nil
}

// Sent returns the emails sent, oldest first.
func (t *MemoryTransport) Sent() []*Outgoing {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*Outgoing(nil), t.sent...)
}

// Reset forgets the emails sent.
func (t *MemoryTransport) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = nil
}
