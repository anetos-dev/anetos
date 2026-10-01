// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/mailer"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
	"anetos.dev/anetos/anetostest"
)

// fakeGateway makes setup use g.
func fakeGateway(t *testing.T, g *FakeGateway) {
	old := newGateway
	newGateway = func() Gateway { return g }
	t.Cleanup(func() { newGateway = old })
}

// placeOrder places an order and returns its ID.
func placeOrder(t *testing.T, app *anetostest.App, item string, cents int64) int64 {
	t.Helper()
	var o Order
	app.PostJSON("/orders", map[string]any{"item": item, "cents": cents, "email": "ada@example.com"}).
		AssertStatus(202).
		AssertJSONPath("status", "pending").
		JSON(&o)
	return o.ID
}

func orderStatus(t *testing.T, app *anetostest.App, id int64) string {
	t.Helper()
	o, err := db.Find[Order](app.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return o.Status
}

// region: test-sync
// With QUEUE_DRIVER=sync, jobs run when they are dispatched: a test sees
// their effects as soon as the request returns.
func TestChargeOrder(t *testing.T) {
	g := &FakeGateway{}
	fakeGateway(t, g)
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))

	paid := placeOrder(t, app, "Book", 1500)
	app.Get("/orders/"+strconv.FormatInt(paid, 10)).AssertOK().AssertJSONPath("status", "paid")

	declined := placeOrder(t, app, "Piano", 500_000)
	app.Get("/orders/"+strconv.FormatInt(declined, 10)).AssertOK().AssertJSONPath("status", "failed")

	if n := g.Charges(); n != 1 {
		t.Errorf("%d charges, want 1", n)
	}
}

// endregion

// region: test-workers
// With QUEUE_DRIVER=database, workers run the jobs: the test runs them
// with app.Run, and waits for the outcome.
func TestWorkersRetry(t *testing.T) {
	g := &FakeGateway{Flaky: true} // each charge fails once
	fakeGateway(t, g)
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{
		"QUEUE_DRIVER": "database", "QUEUE_POLL": "10ms", "QUEUE_BACKOFF": "10ms",
	}))
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, "workers") }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	paid := placeOrder(t, app, "Book", 1500)
	declined := placeOrder(t, app, "Piano", 500_000)
	waitFor(t, func() bool { return orderStatus(t, app, paid) == "paid" && orderStatus(t, app, declined) == "failed" })

	q := anetos.MustResolve[*queue.Queue](app.App)
	failed, err := q.Store().Failed(app.Context(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 1 || failed[0].Error != "card declined" || failed[0].Attempts != 1 {
		t.Errorf("failed jobs = %+v", failed)
	}
	if n := g.Charges(); n != 1 {
		t.Errorf("%d charges, want 1", n)
	}
}

// endregion

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMissingOrder(t *testing.T) {
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))
	err := queue.Dispatch(app.Context(), ChargeOrder{OrderID: 999})
	if err == nil || !queue.IsPermanent(err) {
		t.Errorf("Dispatch = %v, want a permanent error", err)
	}
	app.Get("/orders/999").AssertNotFound()
	app.PostJSON("/orders", map[string]any{"item": ""}).AssertUnprocessable()
}

// region: test-events
// Each kind of listener: the audit entry is written with the order, the
// receipt sent by the queued listener (the sync driver runs it at once),
// and the sales counted in the background: bus.Wait waits for that.
func TestOrderEvents(t *testing.T) {
	fakeGateway(t, &FakeGateway{})
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))
	ordersBefore, _ := sales.Snapshot()

	id := placeOrder(t, app, "Lamp", 4250)

	anetostest.AssertDatabaseHas[AuditEntry](app, db.Col[int64]("order_id").Eq(id))
	if sent := sentMail(app); len(sent) != 1 || sent[0].Subject != fmt.Sprintf("Your receipt for order %d", id) {
		t.Errorf("emails: %+v", sent)
	}
	bus := anetos.MustResolve[*events.Bus](app.App)
	if err := bus.Wait(app.Context()); err != nil {
		t.Fatal(err)
	}
	if orders, _ := sales.Snapshot(); orders != ordersBefore+1 {
		t.Errorf("sales: %d orders, want %d", orders, ordersBefore+1)
	}
	app.Get("/stats").AssertOK()
}

// endregion

// region: test-schedule
// RunTask runs a task now, as `go run . schedule:run <task>` does.
func TestScheduledTasks(t *testing.T) {
	fakeGateway(t, &FakeGateway{})
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))
	old := &AuditEntry{OrderID: 1, Message: "placed: Kettle"}
	if err := db.Create(app.Context(), old); err != nil {
		t.Fatal(err)
	}
	_, err := db.Query[AuditEntry](app.Context()).Where(colID.Eq(old.ID)).Update(colCreatedAt.Set(time.Now().AddDate(0, 0, -100)))
	if err != nil {
		t.Fatal(err)
	}
	recent := placeOrder(t, app, "Mug", 900)

	s := anetos.MustResolve[*schedule.Scheduler](app.App)
	if err := s.RunTask(app.Context(), "prune-audit-log"); err != nil {
		t.Fatal(err)
	}
	anetostest.AssertDatabaseMissing[AuditEntry](app, colID.Eq(old.ID))
	anetostest.AssertDatabaseHas[AuditEntry](app, db.Col[int64]("order_id").Eq(recent))

	if err := s.RunTask(app.Context(), "sales-report"); err != nil { // the sync queue runs the job at once
		t.Fatal(err)
	}
	if r := salesReports.Sent(); len(r) == 0 || r[len(r)-1] != "Sales report: orders paid in the last hour: 1" {
		t.Errorf("reports: %q", r)
	}
}

// endregion

// sentMail returns the emails the app sent: anetostest sets
// MAIL_DRIVER=memory, which keeps them.
func sentMail(app *anetostest.App) []*mailer.Outgoing {
	return anetos.MustResolve[*mailer.Mailer](app.App).Transport().(*mailer.MemoryTransport).Sent()
}

// region: test-mail
// The receipt is queued (the sync driver sends it at once) and kept by
// the memory transport: the test checks its recipient, subject and body.
func TestResendReceipt(t *testing.T) {
	fakeGateway(t, &FakeGateway{})
	app := anetostest.New(t, setup, anetostest.Env(map[string]string{"QUEUE_DRIVER": "sync"}))
	id := placeOrder(t, app, "Lamp", 4250)

	app.PostJSON(fmt.Sprintf("/orders/%d/receipt", id), nil).AssertStatus(http.StatusAccepted)

	sent := sentMail(app)
	if len(sent) != 2 { // when placed, and again
		t.Fatalf("%d emails", len(sent))
	}
	m := sent[1]
	if m.To[0].Address != "ada@example.com" || m.Subject != fmt.Sprintf("Your receipt for order %d", id) {
		t.Errorf("email %+v", m)
	}
	for _, want := range []string{"Lamp, $42.50", fmt.Sprintf("See your order (http://example.test/orders/%d)", id)} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("the text lacks %q:\n%s", want, m.Text)
		}
	}
	app.PostJSON("/orders/999/receipt", nil).AssertNotFound()
}

// endregion
