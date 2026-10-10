// SPDX-License-Identifier: Apache-2.0

package redis_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/redis"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/pubsub/pubsubtest"
	"anetos.dev/anetos/web"
	goredis "github.com/redis/go-redis/v9"
)

func TestStreamsBroker(t *testing.T) {
	opts, err := goredis.ParseURL(redisURL(t))
	if err != nil {
		t.Fatal(err)
	}
	pubsubtest.Run(t, func(t *testing.T) pubsub.Broker {
		b := redis.NewStreamsBroker(goredis.NewClient(opts), randomPrefix(), 1000)
		t.Cleanup(func() {
			if err := b.Purge(context.Background()); err != nil {
				t.Error(err)
			}
			_ = b.Close()
		})
		return b
	}, pubsubtest.Features{Attempts: true, Delays: true, AckTimeout: true, Ordered: true})
	b := redis.NewStreamsBroker(goredis.NewClient(opts), "", 0)
	defer b.Close()
	if err := b.Purge(t.Context()); err == nil {
		t.Error("Purge without a prefix = nil")
	}
}

type orderCreated struct {
	ID int64 `json:"id"`
}

func TestPubSubDriver(t *testing.T) {
	url := redisURL(t)
	var got atomic.Int64
	a := anetostest.New(t, func(app *anetos.App) (*web.Server, error) {
		ps, err := pubsub.New(app, redis.PubSubDriver())
		if err != nil {
			return nil, err
		}
		return nil, pubsub.Listen(ps, "orders.created", func(_ context.Context, o orderCreated) error {
			got.Add(o.ID)
			return nil
		})
	}, anetostest.Env(map[string]string{"PUBSUB_DRIVER": "redis", "REDIS_URL": url, "PUBSUB_REDIS_MAXLEN": "10"}))
	ps := anetos.MustResolve[*pubsub.PubSub](a.App)
	if err := pubsub.Publish(a.Context(), "orders.created", orderCreated{ID: 7}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, "listeners") }()
	deadline := time.Now().Add(5 * time.Second)
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
	if _, ok := ps.Broker().(*redis.StreamsBroker); !ok {
		t.Errorf("broker %T", ps.Broker())
	}
	bad := newApp(t, config.Map{"PUBSUB_DRIVER": "redis", "REDIS_URL": url, "PUBSUB_REDIS_MAXLEN": "-1"})
	if _, err := pubsub.New(bad, redis.PubSubDriver()); err == nil {
		t.Error("PUBSUB_REDIS_MAXLEN=-1 = nil")
	}
}

func streamsBroker(t *testing.T) (*redis.StreamsBroker, *goredis.Client, string) {
	t.Helper()
	opts, err := goredis.ParseURL(redisURL(t))
	if err != nil {
		t.Fatal(err)
	}
	prefix := randomPrefix()
	b := redis.NewStreamsBroker(goredis.NewClient(opts), prefix, 0)
	client := goredis.NewClient(opts)
	t.Cleanup(func() {
		_ = b.Purge(context.Background())
		_ = b.Close()
		_ = client.Close()
	})
	return b, client, prefix
}

// TestStreamDeleted checks that a listener carries on when its stream is
// deleted, even while it waits for messages (UNBLOCKED) or between reads
// (NOGROUP).
func TestStreamDeleted(t *testing.T) {
	b, client, prefix := streamsBroker(t)
	s := pubsub.SubscriptionSpec{Topic: "t", Name: "t.g", Concurrency: 1, AckTimeout: time.Minute}
	if err := b.Prepare(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	var got atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- b.Subscribe(ctx, s, func(context.Context, *pubsub.Message) pubsub.Outcome {
			got.Add(1)
			return pubsub.Outcome{Ack: true}
		})
	}()
	for i := range 3 {
		time.Sleep(200 * time.Millisecond) // the listener is waiting in XREADGROUP
		if err := client.Del(t.Context(), prefix+"t").Err(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(700 * time.Millisecond) // it recreated its group
		if _, err := b.Publish(t.Context(), "t", pubsub.Outgoing{Data: []byte("x")}); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for got.Load() != int32(i+1) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if got.Load() != int32(i+1) {
			t.Fatalf("after deleting the stream %d time(s), the listener got %d messages", i+1, got.Load())
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Subscribe = %v", err)
	}
}

// TestConsumersTidied checks that empty consumers of stopped listeners
// are removed.
func TestConsumersTidied(t *testing.T) {
	defer redis.SetIdleConsumer(50 * time.Millisecond)()
	b, client, prefix := streamsBroker(t)
	s := pubsub.SubscriptionSpec{Topic: "t", Name: "t.g", Concurrency: 1, AckTimeout: time.Minute}
	if err := b.Prepare(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	// A listener leaves a consumer with a pending (failed) message.
	if _, err := b.Publish(t.Context(), "t", pubsub.Outgoing{Data: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	_ = b.Subscribe(ctx, s, func(context.Context, *pubsub.Message) pubsub.Outcome {
		cancel()
		return pubsub.Outcome{RetryAfter: time.Millisecond}
	})
	consumers := func() []goredis.XInfoConsumer {
		cs, err := client.XInfoConsumers(t.Context(), prefix+"t", "t.g").Result()
		if err != nil {
			t.Fatal(err)
		}
		return cs
	}
	if cs := consumers(); len(cs) != 1 || cs[0].Pending != 1 {
		t.Fatalf("consumers %+v, want one with the pending message", cs)
	}
	// The next listener claims the message; once the old consumer is
	// empty and idle, a later start removes it.
	for range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 600*time.Millisecond)
		_ = b.Subscribe(ctx, s, func(context.Context, *pubsub.Message) pubsub.Outcome { return pubsub.Outcome{Ack: true} })
		cancel()
		time.Sleep(100 * time.Millisecond)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 600*time.Millisecond)
	defer cancel()
	_ = b.Subscribe(ctx, s, func(context.Context, *pubsub.Message) pubsub.Outcome { return pubsub.Outcome{Ack: true} })
	if cs := consumers(); len(cs) != 0 {
		t.Errorf("consumers left: %+v", cs)
	}
}
