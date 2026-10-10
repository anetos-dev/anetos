// SPDX-License-Identifier: Apache-2.0

package gcppubsub_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/gcppubsub"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/pubsub/pubsubtest"
	"anetos.dev/anetos/web"
	gpubsub "cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// fake starts a fake Pub/Sub server for the test and returns a client
// option connecting to it.
func fake(t *testing.T) option.ClientOption {
	t.Helper()
	srv := pstest.NewServer()
	t.Cleanup(func() { _ = srv.Close() })
	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return option.WithGRPCConn(conn)
}

func newClient(t *testing.T, opt option.ClientOption) *gpubsub.Client {
	t.Helper()
	c, err := gpubsub.NewClient(context.Background(), "proj", opt)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestBroker(t *testing.T) {
	pubsubtest.Run(t, func(t *testing.T) pubsub.Broker {
		b := gcppubsub.NewBroker(newClient(t, fake(t)), "", true)
		t.Cleanup(func() { _ = b.Close() })
		return b
	}, pubsubtest.Features{})
}

func TestExistingSubscriptions(t *testing.T) {
	opt := fake(t)
	admin := newClient(t, opt)
	defer admin.Close()
	b := gcppubsub.NewBroker(newClient(t, opt), "", false)
	defer b.Close()
	ctx := context.Background()
	s := pubsub.SubscriptionSpec{Topic: "orders.created", Name: "orders.created.billing", Concurrency: 1, AckTimeout: 40 * time.Second}
	// Without create, Prepare checks nothing (so the service account
	// needs no pubsub.subscriptions.get); the listener finds out.
	if err := b.Prepare(ctx, s); err != nil {
		t.Errorf("Prepare without create = %v", err)
	}
	if err := b.Subscribe(ctx, s, func(context.Context, *pubsub.Message) pubsub.Outcome { return pubsub.Outcome{Ack: true} }); err == nil {
		t.Error("Subscribe to a missing subscription = nil")
	}
	if _, err := b.Publish(ctx, "orders.created", pubsub.Outgoing{Data: []byte("x")}); err == nil {
		t.Error("Publish to a missing topic without create = nil")
	}
	// A subscription with a dead-letter policy: Pub/Sub counts deliveries.
	for _, topic := range []string{"orders.created", "orders.dlq"} {
		if _, err := admin.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{Name: "projects/proj/topics/" + topic}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := admin.SubscriptionAdminClient.CreateSubscription(ctx, &pubsubpb.Subscription{
		Name: "projects/proj/subscriptions/orders.created.billing", Topic: "projects/proj/topics/orders.created",
		DeadLetterPolicy: &pubsubpb.DeadLetterPolicy{DeadLetterTopic: "projects/proj/topics/orders.dlq", MaxDeliveryAttempts: 5},
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Prepare(ctx, s); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Publish(ctx, "orders.created", pubsub.Outgoing{Data: []byte("x"), Attributes: map[string]string{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var attempts []int
	err := b.Subscribe(sctx, s, func(_ context.Context, m *pubsub.Message) pubsub.Outcome {
		attempts = append(attempts, m.Attempt)
		if m.Attributes["k"] != "v" || string(m.Data) != "x" || m.Topic != "orders.created" || m.PublishedAt.IsZero() {
			t.Errorf("message %+v", m)
		}
		if len(attempts) == 2 {
			cancel()
			return pubsub.Outcome{Ack: true}
		}
		return pubsub.Outcome{}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Errorf("attempts %v, want [1 2]", attempts)
	}
}

type orderCreated struct {
	ID int64 `json:"id"`
}

func TestDriver(t *testing.T) {
	opt := fake(t)
	var got atomic.Int64
	a := anetostest.New(t, func(app *anetos.App) (*web.Server, error) {
		ps, err := pubsub.New(app, gcppubsub.Driver(opt))
		if err != nil {
			return nil, err
		}
		return nil, pubsub.Listen(ps, "orders.created", func(_ context.Context, o orderCreated) error {
			got.Add(o.ID)
			return nil
		})
	}, anetostest.Env(map[string]string{"PUBSUB_DRIVER": "gcp", "PUBSUB_GCP_PROJECT": "proj", "PUBSUB_GCP_CREATE": "true"}))
	// The subscription was created when the app booted, under the
	// test's prefix: this message is kept for it.
	if err := pubsub.Publish(a.Context(), "orders.created", orderCreated{ID: 7}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, "listeners") }()
	deadline := time.Now().Add(10 * time.Second)
	for got.Load() != 7 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got.Load() != 7 {
		t.Errorf("the listener got %d, want 7", got.Load())
	}
}

func TestDriverConfig(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "PUBSUB_DRIVER": "gcp"}))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if _, err := pubsub.New(app, gcppubsub.Driver()); err == nil || !strings.Contains(err.Error(), "PUBSUB_GCP_PROJECT") {
		t.Errorf("New without a project = %v", err)
	}
}

func TestIDs(t *testing.T) {
	b := gcppubsub.NewBroker(newClient(t, fake(t)), "", true)
	defer b.Close()
	ctx := context.Background()
	// Names that differ get different IDs: ":" and "%" are escaped.
	got := map[string]int{}
	for _, topic := range []string{"a:b", "a-b", "a%3Ab"} {
		s := pubsub.SubscriptionSpec{Topic: topic, Name: topic + ".sub", Concurrency: 1, AckTimeout: time.Minute}
		if err := b.Prepare(ctx, s); err != nil {
			t.Fatal(err)
		}
		if _, err := b.Publish(ctx, topic, pubsub.Outgoing{Data: []byte(topic)}); err != nil {
			t.Fatal(err)
		}
		sctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = b.Subscribe(sctx, s, func(_ context.Context, m *pubsub.Message) pubsub.Outcome {
			got[topic+" got "+string(m.Data)]++
			cancel()
			return pubsub.Outcome{Ack: true}
		})
		cancel()
	}
	for _, topic := range []string{"a:b", "a-b", "a%3Ab"} {
		if got[topic+" got "+topic] != 1 || len(got) != 3 {
			t.Errorf("deliveries %v", got)
		}
	}
	for _, topic := range []string{"ab", "1abc", "goog-x", "Google"} {
		if _, err := b.Publish(ctx, topic, pubsub.Outgoing{Data: []byte("x")}); err == nil || !strings.Contains(err.Error(), "valid Pub/Sub ID") {
			t.Errorf("Publish to %q = %v", topic, err)
		}
	}
}
