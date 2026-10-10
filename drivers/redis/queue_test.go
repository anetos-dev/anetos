// SPDX-License-Identifier: Apache-2.0

package redis_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/anetostest"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/drivers/redis"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/queue/queuetest"
	"anetos.dev/anetos/web"
	goredis "github.com/redis/go-redis/v9"
)

func randomPrefix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "test-queue-" + hex.EncodeToString(b) + ":"
}

func TestQueueStore(t *testing.T) {
	opts, err := goredis.ParseURL(redisURL(t))
	if err != nil {
		t.Fatal(err)
	}
	queuetest.Run(t, func(t *testing.T) queue.Store {
		s := redis.NewQueueStore(goredis.NewClient(opts), randomPrefix())
		t.Cleanup(func() {
			if err := s.Purge(context.Background()); err != nil {
				t.Error(err)
			}
			_ = s.Close()
		})
		return s
	})
	s := redis.NewQueueStore(goredis.NewClient(opts), "")
	defer s.Close()
	if err := s.Purge(t.Context()); err == nil {
		t.Error("Purge without a prefix = nil")
	}
}

var queueRuns atomic.Int64

type countJob struct {
	Fail bool `json:"fail"`
}

func (j countJob) Handle(context.Context) error {
	queueRuns.Add(1)
	if j.Fail {
		return queue.Permanent(errors.New("no"))
	}
	return nil
}

func TestQueueDriver(t *testing.T) {
	url := redisURL(t)
	prefix := randomPrefix()
	app := newApp(t, config.Map{"APP_NAME": "redistest", "QUEUE_DRIVER": "redis", "REDIS_URL": url, "QUEUE_PREFIX": prefix,
		"QUEUE_POLL_INTERVAL": "10ms", "APP_SHUTDOWN_TIMEOUT": "5s"})
	q, err := queue.New(app, redis.QueueDriver())
	if err != nil {
		t.Fatal(err)
	}
	store := q.Store().(*redis.QueueStore)
	opts, err := goredis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // the app's client is closed by then
		s := redis.NewQueueStore(goredis.NewClient(opts), prefix)
		defer s.Close()
		if err := s.Purge(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := queue.Register[countJob](q); err != nil {
		t.Fatal(err)
	}
	if err := q.Work(queue.Concurrency(2)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	before := queueRuns.Load()
	actx := app.Context(t.Context())
	for range 3 {
		if err := queue.Dispatch(actx, countJob{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.Dispatch(actx, countJob{Fail: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		failed, err := store.Failed(actx, 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		n, err := store.Size(actx, "default")
		if err != nil {
			t.Fatal(err)
		}
		if queueRuns.Load()-before == 4 && n == 0 && len(failed) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after 5s: %d runs, %d jobs left, %d failed", queueRuns.Load()-before, n, len(failed))
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// The app's client is closed with the app, not by the store.
	if err := store.Close(); err != nil {
		t.Error(err)
	}
}

// TestAnetostestQueuePrefix checks that anetostest gives each test app
// its own QUEUE_PREFIX, and removes its keys at the end.
func TestAnetostestQueuePrefix(t *testing.T) {
	url := redisURL(t)
	var store *redis.QueueStore
	var prefix string
	t.Run("app", func(t *testing.T) {
		var q *queue.Queue
		a := anetostest.New(t, func(app *anetos.App) (*web.Server, error) {
			var err error
			if q, err = queue.New(app, redis.QueueDriver()); err != nil {
				return nil, err
			}
			return nil, queue.Register[countJob](q)
		}, anetostest.Env(map[string]string{"QUEUE_DRIVER": "redis", "REDIS_URL": url}))
		prefix = q.Config().Prefix
		if prefix == "redistest:queue:" || prefix == "anetos:queue:" {
			t.Errorf("QUEUE_PREFIX = %q, not the test's own", prefix)
		}
		store = q.Store().(*redis.QueueStore)
		if err := q.Dispatch(a.Context(), countJob{}); err != nil {
			t.Fatal(err)
		}
	})
	opts, err := goredis.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	client := goredis.NewClient(opts)
	defer client.Close()
	keys, err := client.Keys(t.Context(), prefix+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 || store == nil {
		t.Errorf("after the test, keys %v remain", keys)
	}
}
