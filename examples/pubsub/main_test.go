// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/anetostest"
)

// listen collects the messages of topic, from a subscription of the test.
func listen(t *testing.T, ps *pubsub.PubSub, topic string) func() []pubsub.Message {
	t.Helper()
	var mu sync.Mutex
	var got []pubsub.Message
	s := pubsub.SubscriptionSpec{Topic: topic, Name: topic + ".test", Concurrency: 1, AckTimeout: time.Minute}
	if err := ps.Broker().Prepare(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ps.Broker().Subscribe(ctx, s, func(_ context.Context, m *pubsub.Message) pubsub.Outcome {
			mu.Lock()
			got = append(got, *m)
			mu.Unlock()
			return pubsub.Outcome{Ack: true}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return func() []pubsub.Message {
		mu.Lock()
		defer mu.Unlock()
		return append([]pubsub.Message(nil), got...)
	}
}

// region: test
// The listener runs with app.Run, as in production, with the memory
// broker (PUBSUB_DRIVER's default). The test publishes what the shop
// would, and checks what billing did.
func TestCreateInvoice(t *testing.T) {
	app := anetostest.New(t, setup)
	ps := anetos.MustResolve[*pubsub.PubSub](app.App)
	invoices := listen(t, ps, "invoices.created")
	dead := listen(t, ps, "orders.created.dlq")

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, "listeners") }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})

	order := OrderCreated{OrderID: 1, Customer: "Ada", Cents: 1500}
	for _, o := range []OrderCreated{order, order, {OrderID: 2}} { // a duplicate, and an invalid order
		if err := pubsub.Publish(app.Context(), "orders.created", o); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, func() bool { return len(invoices()) == 1 && len(dead()) == 1 })

	var created InvoiceCreated
	if err := json.Unmarshal(invoices()[0].Data, &created); err != nil || created.OrderID != 1 {
		t.Errorf("invoices.created: %+v, %v", created, err)
	}
	if got := dead()[0].Attributes["anetos.error"]; got != "invalid order: {OrderID:2 Customer: Cents:0}" {
		t.Errorf("dead letter error: %q", got)
	}
	app.Get("/invoices").AssertOK().AssertJSONPath("0.customer", "Ada")
	anetostest.AssertDatabaseCount[Invoice](app, 1)
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
