// SPDX-License-Identifier: Apache-2.0

package anetostest_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/storage"
	"anetos.dev/anetos/web"
)

// ship is a job.
type ship struct{ OrderID int }

var shipped atomic.Int32

func (j ship) Handle(context.Context) error { shipped.Add(1); return nil }

type placed struct{ OrderID int }
type canceled struct{ OrderID int }

// welcome is a mailable.
type welcome struct{ To string }

func (m welcome) Build(context.Context) (*mailer.Message, error) {
	return &mailer.Message{To: []mailer.Address{{Address: m.To}}, Subject: "Welcome", Text: "Hi"}, nil
}

type orderCreated struct{ ID int }

// shop dispatches, emits, mails, publishes and stores on POST /orders.
func shop(listened *atomic.Int32) func(app *anetos.App) (*web.Server, error) {
	return func(app *anetos.App) (*web.Server, error) {
		q, err := queue.New(app)
		if err != nil {
			return nil, err
		}
		if err := queue.Register[ship](q); err != nil {
			return nil, err
		}
		bus, err := events.New(app)
		if err != nil {
			return nil, err
		}
		count := func(context.Context, placed) error { listened.Add(1); return nil }
		if err := events.On(bus, count); err != nil {
			return nil, err
		}
		if err := events.On(bus, func(context.Context, canceled) error { listened.Add(10); return nil }); err != nil {
			return nil, err
		}
		if _, err := mailer.New(app); err != nil {
			return nil, err
		}
		if _, err := pubsub.New(app); err != nil {
			return nil, err
		}
		if _, err := storage.New(app); err != nil {
			return nil, err
		}
		srv, err := web.NewServer(app)
		if err != nil {
			return nil, err
		}
		srv.Router().Post("/orders", func(c *web.Ctx) error {
			for _, err := range []error{
				queue.Dispatch(c, ship{OrderID: 7}, queue.OnQueue("shipping")),
				events.Emit(c, placed{OrderID: 7}),
				events.Emit(c, canceled{OrderID: 8}),
				mailer.Send(c, welcome{To: "ada@example.com"}),
				mailer.Queue(c, welcome{To: "bob@example.com"}),
				pubsub.Publish(c, "orders.created", orderCreated{ID: 7}),
			} {
				if err != nil {
					return err
				}
			}
			d, err := storage.From(c)
			if err != nil {
				return err
			}
			if err := d.PutBytes(c, "invoices/7.txt", []byte("invoice 7")); err != nil {
				return err
			}
			return c.NoContent()
		})
		return srv, nil
	}
}

func TestRecording(t *testing.T) {
	shipped.Store(0)
	var listened atomic.Int32
	app := anetostest.New(t, shop(&listened)) // QUEUE_DRIVER=sync: jobs run
	app.AssertNothingDispatched().AssertNothingEmitted().AssertNoMail()
	app.PostJSON("/orders", nil).AssertNoContent()

	anetostest.AssertDispatched(app, func(j ship) bool { return j.OrderID == 7 })
	anetostest.AssertDispatched[ship](app, nil)
	anetostest.AssertNotDispatched(app, func(j ship) bool { return j.OrderID == 8 })
	if shipped.Load() != 1 {
		t.Errorf("the job ran %d times", shipped.Load())
	}
	if d := app.Dispatched(); len(d) != 2 || d[0].Queue != "shipping" || d[1].Job != "mail:send" {
		t.Errorf("Dispatched = %+v", d)
	}
	anetostest.AssertEmitted(app, func(e placed) bool { return e.OrderID == 7 })
	anetostest.AssertNotEmitted(app, func(e placed) bool { return e.OrderID == 9 })
	if listened.Load() != 11 {
		t.Errorf("listeners ran: %d", listened.Load())
	}
	anetostest.AssertMailSent(app, func(m welcome) bool { return m.To == "ada@example.com" })
	anetostest.AssertMailQueued(app, func(m welcome) bool { return m.To == "bob@example.com" })
	anetostest.AssertMailNotSent(app, func(m welcome) bool { return m.To == "eve@example.com" })
	if m := anetostest.Mailables[welcome](app); len(m) != 2 {
		t.Errorf("Mailables = %v", m)
	}
	// The sync queue ran the mail:send job: both reached the transport.
	if sent := anetos.MustResolve[*mailer.Mailer](app.App).Transport().(*mailer.MemoryTransport).Sent(); len(sent) != 2 {
		t.Errorf("%d emails sent", len(sent))
	}
	anetostest.AssertPublished(app, "orders.created", func(m orderCreated) bool { return m.ID == 7 })
	anetostest.AssertNotPublished[orderCreated](app, "orders.shipped", nil)
	if m := anetostest.Messages[orderCreated](app, "orders.created"); len(m) != 1 {
		t.Errorf("Messages = %v", m)
	}
	app.Disk().AssertExists("invoices/7.txt").AssertContent("invoices/7.txt", "invoice 7").AssertMissing("invoices/8.txt")
	if f := app.Disk().Files("invoices/"); len(f) != 1 || f[0] != "invoices/7.txt" {
		t.Errorf("Files = %v", f)
	}
}

func TestFakes(t *testing.T) {
	shipped.Store(0)
	var listened atomic.Int32
	app := anetostest.New(t, shop(&listened), anetostest.FakeQueue(), anetostest.FakeEvents(placed{}), anetostest.FakePubSub())
	app.PostJSON("/orders", nil).AssertNoContent()

	if jobs := anetostest.Jobs[ship](app); len(jobs) != 1 || jobs[0].OrderID != 7 || shipped.Load() != 0 {
		t.Errorf("jobs %v, ran %d", jobs, shipped.Load())
	}
	if listened.Load() != 10 { // placed is faked, canceled isn't
		t.Errorf("listeners ran: %d", listened.Load())
	}
	anetostest.AssertEmitted[placed](app, nil)
	anetostest.AssertMailQueued[welcome](app, nil)
	// The queued email's job didn't run: only the one sent now reached
	// the transport.
	if sent := anetos.MustResolve[*mailer.Mailer](app.App).Transport().(*mailer.MemoryTransport).Sent(); len(sent) != 1 {
		t.Errorf("%d emails sent", len(sent))
	}
	anetostest.AssertPublished[orderCreated](app, "orders.created", nil)
}

func TestFakeAllEvents(t *testing.T) {
	var listened atomic.Int32
	app := anetostest.New(t, shop(&listened), anetostest.FakeEvents())
	app.PostJSON("/orders", nil).AssertNoContent()
	if listened.Load() != 0 {
		t.Errorf("listeners ran: %d", listened.Load())
	}
	if e := app.Emitted(); len(e) != 2 {
		t.Errorf("Emitted = %v", e)
	}
}

func TestFakeFailures(t *testing.T) {
	ft := &fakeT{TB: t}
	var listened atomic.Int32
	app := anetostest.New(ft, shop(&listened), anetostest.FakeQueue())
	anetostest.AssertDispatched[ship](app, nil)
	anetostest.AssertEmitted(app, func(placed) bool { return true })
	anetostest.AssertMailSent[welcome](app, nil)
	anetostest.AssertPublished[orderCreated](app, "orders.created", nil)
	app.Disk().AssertExists("x.txt").AssertContent("x.txt", "x")
	app.PostJSON("/orders", nil).AssertNoContent()
	app.AssertNothingDispatched().AssertNothingEmitted().AssertNoMail()
	anetostest.AssertNotDispatched[ship](app, nil)
	anetostest.AssertNotEmitted[placed](app, nil)
	anetostest.AssertMailSent(app, func(m welcome) bool { return m.To == "bob@example.com" }) // queued, not sent
	anetostest.AssertMailQueued(app, func(m welcome) bool { return m.To == "ada@example.com" })
	anetostest.AssertMailNotSent[welcome](app, nil)
	anetostest.AssertNotPublished[orderCreated](app, "orders.created", nil)
	app.Disk().AssertMissing("invoices/7.txt").AssertContent("invoices/7.txt", "other")
	want := []string{
		"no anetostest_test.ship job was dispatched; dispatched: none",
		"no anetostest_test.placed event matching was emitted; emitted: none",
		"no anetostest_test.welcome email was sent; sent or queued: none",
		"no message was published to orders.created; published to: none",
		"disk default has no file x.txt; it has: none",
		"disk default has no file x.txt; it has: none",
		"jobs were dispatched: anetostest_test.ship, mail:send",
		"events were emitted: anetostest_test.placed, anetostest_test.canceled",
		"emails were sent or queued: anetostest_test.welcome, anetostest_test.welcome",
		"1 anetostest_test.ship job(s) were dispatched",
		"1 anetostest_test.placed event(s) were emitted",
		"no anetostest_test.welcome email matching was sent (one was queued with mailer.Queue: AssertMailQueued); sent or queued: anetostest_test.welcome, anetostest_test.welcome",
		"no anetostest_test.welcome email matching was queued (one was sent with mailer.Send)",
		"2 anetostest_test.welcome email(s) were sent or queued",
		"1 message(s) were published to orders.created",
		"disk default has the file invoices/7.txt",
		`invoices/7.txt on disk default is "invoice 7", want "other"`,
	}
	if len(ft.errs) != len(want) {
		t.Fatalf("errors:\n%s", strings.Join(ft.errs, "\n"))
	}
	for i, w := range want {
		if !strings.Contains(ft.errs[i], w) {
			t.Errorf("error %d = %q, want %q", i, ft.errs[i], w)
		}
	}
	if msg := fatalOf(func() { anetostest.Jobs[queue.Job](app) }); !strings.Contains(msg, "is an interface") {
		t.Errorf("Jobs of an interface: %q", msg)
	}
	if msg := fatalOf(func() { anetostest.New(&fakeT{TB: t}, shop(&listened), anetostest.FakeEvents(nil)) }); !strings.Contains(msg, "FakeEvents(nil)") {
		t.Errorf("FakeEvents(nil): %q", msg)
	}
	// A message that doesn't decode as T doesn't match, and doesn't stop
	// the test.
	ft2 := &fakeT{TB: t}
	app2 := anetostest.New(ft2, shop(&listened), anetostest.FakePubSub())
	if err := pubsub.Publish(app2.Context(), "orders.created", []byte("not JSON")); err != nil {
		t.Fatal(err)
	}
	anetostest.AssertPublished[orderCreated](app2, "orders.created", nil)
	anetostest.AssertNotPublished[orderCreated](app2, "orders.created", nil)
	if len(ft2.errs) != 1 || !strings.Contains(ft2.errs[0], "(1 didn't decode as anetostest_test.orderCreated)") {
		t.Errorf("errors: %q", ft2.errs)
	}
	if msg := fatalOf(func() { anetostest.Jobs[placedJob](app) }); !strings.Contains(msg, "isn't registered") {
		t.Errorf("Jobs of an unregistered type: %q", msg)
	}
	if msg := fatalOf(func() { app.Disk("nope") }); msg == "" {
		t.Error("Disk of an unknown disk: no failure")
	}
	if msg := fatalOf(func() { app.Disk("a", "b") }); !strings.Contains(msg, "one disk name") {
		t.Errorf("Disk with two names: %q", msg)
	}
	if msg := fatalOf(func() { anetostest.New(&fakeT{TB: t}, setup, anetostest.FakeQueue()) }); !strings.Contains(msg, "the app has no queue") {
		t.Errorf("FakeQueue without a queue: %q", msg)
	}
}

type placedJob struct{}

func (placedJob) Handle(context.Context) error { return nil }

// The app's clock: frozen, moved, and seen by the framework.
func TestClock(t *testing.T) {
	app := anetostest.New(t, setup)
	at := time.Date(2030, 1, 2, 3, 4, 5, 123456789, time.UTC)
	if got := app.Freeze(at); !got.Equal(at.Truncate(time.Microsecond)) || !app.Now().Equal(got) || !anetos.Now(app.Context()).Equal(got) {
		t.Errorf("Freeze = %v, Now = %v", got, app.Now())
	}
	app.Travel(time.Hour)
	if !app.Now().Equal(at.Truncate(time.Microsecond).Add(time.Hour)) {
		t.Errorf("after Travel: %v", app.Now())
	}
	if got := app.Freeze(time.Time{}); !got.Equal(at.Truncate(time.Microsecond).Add(time.Hour)) {
		t.Errorf("Freeze(zero) while frozen = %v", got)
	}
	app.Unfreeze()
	if d := time.Since(app.Now()); d < -time.Second || d > time.Second {
		t.Errorf("after Unfreeze: %v", app.Now())
	}
	app.Travel(-48 * time.Hour) // running, two days back
	if d := time.Since(app.Now()); d < 47*time.Hour || d > 49*time.Hour {
		t.Errorf("after Travel back: %v", app.Now())
	}
	if got := app.Freeze(time.Time{}); time.Since(got) < 47*time.Hour {
		t.Errorf("Freeze(zero) after Travel = %v", got)
	}

	// The session cookie expires on the app's clock.
	app.Unfreeze()
	app.WithSession(func(s *session.Session) { s.Put("user", "ada") })
	app.Get("/items").AssertSee("user ada")
	app.Travel(3 * time.Hour) // SESSION_TTL is 2h by default
	app.Get("/items").AssertDontSee("user ada")

	// The clock is in the app's zone.
	dhaka := anetostest.New(t, setup, anetostest.Env(map[string]string{"APP_TIMEZONE": "Asia/Dhaka"}))
	dhaka.Freeze(time.Date(2026, 3, 15, 20, 0, 0, 0, time.UTC))
	if got := anetos.Today(dhaka.Context()); got != anetos.NewDate(2026, 3, 16) {
		t.Errorf("Today in Dhaka = %v", got)
	}
	if loc := anetos.Now(dhaka.Context()).Location(); loc.String() != "Asia/Dhaka" {
		t.Errorf("Now's zone = %v", loc)
	}
}
