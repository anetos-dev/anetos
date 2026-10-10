// SPDX-License-Identifier: Apache-2.0

package mailer_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"
)

func TestObserve(t *testing.T) {
	app := newApp(t, config.Map{"MAIL_FROM_ADDRESS": "shop@example.com", "QUEUE_DRIVER": "sync"}, nil)
	_, err := queue.New(app)
	check(t, err)
	m, err := mailer.New(app)
	check(t, err)
	var seen []mailer.Record
	m.Observe(func(_ context.Context, r mailer.Record) { seen = append(seen, r) })
	ctx := app.Context(context.Background())
	check(t, app.Boot(ctx))
	now := &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "Now", Text: "x"}
	later := &mailer.Message{To: []mailer.Address{{Address: "b@example.com"}}, Subject: "Later", Text: "x"}
	check(t, mailer.Send(ctx, now))
	check(t, mailer.Queue(ctx, later))
	if len(seen) != 2 || seen[0].Mailable != now || seen[0].Queued || seen[0].Message.Subject != "Now" ||
		seen[1].Mailable != later || !seen[1].Queued || seen[1].Message.Subject != "Later" {
		t.Errorf("seen %+v", seen)
	}
	// Not sent: not recorded.
	f := mailer.NewWithTransport(failing{errors.New("down")}, mailer.WithDefaultFrom(mailer.Address{Address: "shop@example.com"}))
	f.Observe(func(context.Context, mailer.Record) { t.Error("a failed send was observed") })
	if err := f.Send(context.Background(), now); err == nil {
		t.Error("no error")
	}
}

// A caller's own OnDispatched still runs; a queued email the sync driver
// failed to send isn't recorded.
func TestObserveQueueOptions(t *testing.T) {
	app := newApp(t, config.Map{"MAIL_DRIVER": "bad", "MAIL_FROM_ADDRESS": "shop@example.com", "QUEUE_DRIVER": "sync"}, nil)
	_, err := queue.New(app)
	check(t, err)
	var fail atomic.Bool
	m, err := mailer.New(app, mailer.Driver{Name: "bad", Open: func(*anetos.App, mailer.Config) (mailer.Transport, error) {
		return transportFunc(func(context.Context, *mailer.Outgoing) error {
			if fail.Load() {
				return errors.New("down")
			}
			return nil
		}), nil
	}})
	check(t, err)
	var records, mine atomic.Int32
	m.Observe(func(context.Context, mailer.Record) { records.Add(1) })
	ctx := app.Context(context.Background())
	check(t, app.Boot(ctx))
	msg := &mailer.Message{To: []mailer.Address{{Address: "a@example.com"}}, Subject: "S", Text: "x"}
	check(t, mailer.Queue(ctx, msg, queue.OnDispatched(func(context.Context, queue.Dispatched) { mine.Add(1) })))
	if records.Load() != 1 || mine.Load() != 1 {
		t.Errorf("records %d, own callback %d", records.Load(), mine.Load())
	}
	fail.Store(true)
	if err := mailer.Queue(ctx, msg); err == nil {
		t.Error("no error")
	}
	if records.Load() != 1 {
		t.Errorf("a failed queued email was recorded: %d", records.Load())
	}
}

type transportFunc func(context.Context, *mailer.Outgoing) error

func (f transportFunc) Send(ctx context.Context, o *mailer.Outgoing) error { return f(ctx, o) }
