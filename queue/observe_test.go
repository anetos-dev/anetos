// SPDX-License-Identifier: Apache-2.0

package queue_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/queue"
)

type noted struct{ N int }

var notedRuns atomic.Int32

func (noted) Handle(context.Context) error { notedRuns.Add(1); return nil }

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestObserveAndFake(t *testing.T) {
	ctx := context.Background()
	store := queue.NewMemoryStore()
	for _, isSync := range []bool{true, false} {
		var s queue.Store
		if !isSync {
			s = store
		}
		q := queue.New(s, queue.Config{})
		if err := queue.Register[noted](q, queue.Name("noted")); err != nil {
			t.Fatal(err)
		}
		if err := queue.RegisterFunc(q, "ping", func(context.Context, string) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if name, err := q.NameOf(&noted{}); err != nil || name != "noted" {
			t.Errorf("NameOf = %q, %v", name, err)
		}
		if _, err := q.NameOf(struct{ queue.Job }{}); err == nil {
			t.Error("NameOf of an unregistered type: no error")
		}
		var mu sync.Mutex
		var seen []queue.Dispatched
		q.Observe(func(_ context.Context, d queue.Dispatched) {
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, d)
		})
		notedRuns.Store(0)
		check(t, q.Dispatch(ctx, noted{N: 1}, queue.OnQueue("slow"), queue.Delay(time.Minute)))
		check(t, q.DispatchFunc(ctx, "ping", "hello"))
		var got noted
		if len(seen) != 2 || seen[0].Job != "noted" || seen[0].Queue != "slow" || seen[0].Delay != time.Minute ||
			seen[0].Decode(&got) != nil || got.N != 1 || seen[1].Job != "ping" || string(seen[1].Data) != `"hello"` {
			t.Errorf("sync %v: seen %+v", isSync, seen)
		}
		size, _ := store.Size(ctx, "slow")
		if isSync && notedRuns.Load() != 1 || !isSync && size != 1 {
			t.Errorf("sync %v: ran %d, stored %d", isSync, notedRuns.Load(), size)
		}

		// Fake: observed only.
		q.Fake()
		check(t, q.Dispatch(ctx, noted{N: 2}, queue.OnQueue("slow")))
		size2, _ := store.Size(ctx, "slow")
		if len(seen) != 3 || notedRuns.Load() != map[bool]int32{true: 1, false: 0}[isSync] || size2 != size {
			t.Errorf("sync %v, fake: seen %d, ran %d, stored %d", isSync, len(seen), notedRuns.Load(), size2)
		}
	}
}

// failingStore refuses jobs.
type failingStore struct{ queue.Store }

func (failingStore) Push(context.Context, queue.Message, time.Duration) error {
	return errors.New("store down")
}

// A dispatch that fails isn't observed, nor passed to OnDispatched.
func TestObserveFailedDispatch(t *testing.T) {
	q := queue.New(failingStore{queue.NewMemoryStore()}, queue.Config{})
	check(t, queue.Register[noted](q))
	q.Observe(func(context.Context, queue.Dispatched) { t.Error("a failed dispatch was observed") })
	err := q.Dispatch(context.Background(), noted{}, queue.OnDispatched(func(context.Context, queue.Dispatched) {
		t.Error("OnDispatched ran for a failed dispatch")
	}))
	if err == nil {
		t.Error("no error")
	}
	// OnDispatched runs for one that works.
	q2 := queue.New(queue.NewMemoryStore(), queue.Config{})
	check(t, queue.Register[noted](q2))
	var got queue.Dispatched
	check(t, q2.Dispatch(context.Background(), noted{N: 5}, queue.OnDispatched(func(_ context.Context, d queue.Dispatched) { got = d })))
	if got.Job == "" || string(got.Data) != `{"N":5}` {
		t.Errorf("OnDispatched got %+v", got)
	}
}

// Each job run is a unit of work.
func TestJobUnits(t *testing.T) {
	app := newApp(t, nil) // QUEUE_DRIVER defaults to sync
	var units []anetos.Unit
	app.AroundUnits(func(ctx context.Context, u anetos.Unit) (context.Context, func()) {
		units = append(units, u)
		return ctx, nil
	})
	q, err := queue.ForApp(app)
	check(t, err)
	check(t, queue.Register[noted](q, queue.Name("noted")))
	check(t, queue.Dispatch(app.Context(context.Background()), noted{}))
	if len(units) != 1 || units[0] != (anetos.Unit{Kind: "job", Name: "noted"}) {
		t.Errorf("units %+v", units)
	}
}
