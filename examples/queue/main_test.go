// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"strconv"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/queue"
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
	app.PostJSON("/orders", map[string]any{"item": item, "cents": cents}).
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
