// SPDX-License-Identifier: Apache-2.0

package events_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/events"
	"anetos.dev/anetos/queue"
)

type OrderPlaced struct {
	OrderID int64 `json:"order_id"`
}

type Other struct{}

// logBuffer collects logs, safely for concurrent writers.
type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newBus(t *testing.T, opts ...events.Option) (*events.Bus, *logBuffer) {
	t.Helper()
	logs := &logBuffer{}
	b := events.NewBus(append([]events.Option{events.WithLogger(slog.New(slog.NewTextHandler(logs, nil)))}, opts...)...)
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b, logs
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type ctxKey struct{}

func TestOn(t *testing.T) {
	b, _ := newBus(t)
	var got []string
	check(t, events.On(b, func(ctx context.Context, e OrderPlaced) error {
		got = append(got, fmt.Sprintf("first %d %v", e.OrderID, ctx.Value(ctxKey{})))
		return nil
	}))
	fail := errors.New("no stock")
	check(t, events.On(b, func(_ context.Context, e OrderPlaced) error {
		got = append(got, "second")
		if e.OrderID == 2 {
			return fail
		}
		return nil
	}, events.Name("stock")))
	check(t, events.On(b, func(context.Context, OrderPlaced) error { got = append(got, "third"); return nil }))
	ctx := events.WithBus(context.WithValue(context.Background(), ctxKey{}, "request"), b)

	check(t, events.Emit(ctx, OrderPlaced{OrderID: 1}))
	if strings.Join(got, ",") != "first 1 request,second,third" {
		t.Errorf("listeners ran: %v", got)
	}
	got = nil
	err := events.Emit(ctx, OrderPlaced{OrderID: 2})
	if !errors.Is(err, fail) || !strings.Contains(err.Error(), "events: stock: no stock") {
		t.Errorf("Emit = %v", err)
	}
	if strings.Join(got, ",") != "first 2 request,second" {
		t.Errorf("after an error, listeners ran: %v", got)
	}
	// Other types, pointers and nil.
	check(t, events.Emit(ctx, Other{}))
	got = nil
	check(t, events.Emit(ctx, &OrderPlaced{OrderID: 1}))
	if len(got) != 0 {
		t.Errorf("a *OrderPlaced reached OrderPlaced listeners: %v", got)
	}
	if err := events.Emit(ctx, nil); err == nil {
		t.Error("Emit(nil) = nil")
	}
	if err := events.Emit(context.Background(), OrderPlaced{}); !errors.Is(err, events.ErrNoBus) {
		t.Errorf("no bus: %v", err)
	}
}

func TestOptions(t *testing.T) {
	b, _ := newBus(t)
	fn := func(context.Context, OrderPlaced) error { return nil }
	for name, err := range map[string]error{
		"Concurrency on On":   events.On(b, fn, events.Concurrency(2)),
		"Buffer on On":        events.On(b, fn, events.Buffer(2)),
		"Timeout on On":       events.On(b, fn, events.Timeout(time.Second)),
		"Job on OnAsync":      events.OnAsync(b, fn, events.Job(queue.Tries(2))),
		"Dispatch on OnAsync": events.OnAsync(b, fn, events.Dispatch(queue.OnQueue("x"))),
		"Concurrency(0)":      events.OnAsync(b, fn, events.Concurrency(0)),
		"Buffer(0)":           events.OnAsync(b, fn, events.Buffer(0)),
		"Timeout(0)":          events.OnAsync(b, fn, events.Timeout(0)),
		"Name empty":          events.On(b, fn, events.Name("")),
		"nil":                 events.On[OrderPlaced](b, nil),
		"queued, no queue":    events.OnQueued(b, fn, events.Name("x")),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestOnAsync(t *testing.T) {
	b, logs := newBus(t)
	var handled atomic.Int64
	var sawRequest atomic.Bool
	check(t, events.OnAsync(b, func(ctx context.Context, e OrderPlaced) error {
		if ctx.Value(ctxKey{}) != nil {
			sawRequest.Store(true)
		}
		if _, err := events.From(ctx); err != nil {
			return err
		}
		handled.Add(e.OrderID)
		switch e.OrderID {
		case 100:
			return errors.New("analytics down")
		case 200:
			panic("kaboom")
		}
		return nil
	}, events.Name("analytics")))
	ctx := events.WithBus(context.WithValue(context.Background(), ctxKey{}, "request"), b)
	for _, id := range []int64{1, 2, 100, 200, 3} {
		check(t, events.Emit(ctx, OrderPlaced{OrderID: id}))
	}
	check(t, b.Wait(context.Background()))
	if n := handled.Load(); n != 306 {
		t.Errorf("handled %d, want 306", n)
	}
	if sawRequest.Load() {
		t.Error("the async listener saw the request's context values")
	}
	out := logs.String()
	if !strings.Contains(out, "analytics down") || !strings.Contains(out, "kaboom") || !strings.Contains(out, "listener=analytics") {
		t.Errorf("logs:\n%s", out)
	}
}

func TestAsyncConcurrencyAndBuffer(t *testing.T) {
	b, _ := newBus(t)
	release := make(chan struct{})
	var running, peak atomic.Int32
	check(t, events.OnAsync(b, func(context.Context, OrderPlaced) error {
		n := running.Add(1)
		defer running.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		<-release
		return nil
	}, events.Concurrency(3), events.Buffer(2)))
	ctx := events.WithBus(context.Background(), b)
	for i := range 5 { // 3 running, 2 waiting
		check(t, events.Emit(ctx, OrderPlaced{OrderID: int64(i)}))
	}
	deadline := time.Now().Add(5 * time.Second)
	for running.Load() != 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	// The buffer is full: Emit waits, until its context ends.
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := events.Emit(short, OrderPlaced{OrderID: 9}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Emit with a full buffer = %v", err)
	}
	close(release)
	check(t, b.Wait(context.Background()))
	if p := peak.Load(); p != 3 {
		t.Errorf("peak = %d, want 3", p)
	}
}

func TestAsyncTimeoutAndClose(t *testing.T) {
	b, logs := newBus(t)
	check(t, events.OnAsync(b, func(ctx context.Context, e OrderPlaced) error {
		<-ctx.Done()
		return ctx.Err()
	}, events.Timeout(20*time.Millisecond), events.Name("slow")))
	ctx := events.WithBus(context.Background(), b)
	check(t, events.Emit(ctx, OrderPlaced{}))
	check(t, b.Wait(context.Background()))
	if !strings.Contains(logs.String(), "deadline exceeded") {
		t.Errorf("logs:\n%s", logs.String())
	}

	// Close waits for pending events, then refuses new ones.
	c, _ := newBus(t)
	var done atomic.Int32
	check(t, events.OnAsync(c, func(context.Context, OrderPlaced) error {
		time.Sleep(20 * time.Millisecond)
		done.Add(1)
		return nil
	}))
	cctx := events.WithBus(context.Background(), c)
	check(t, events.Emit(cctx, OrderPlaced{}))
	check(t, events.Emit(cctx, OrderPlaced{}))
	check(t, c.Close(context.Background()))
	if done.Load() != 2 {
		t.Errorf("Close returned with %d of 2 events handled", done.Load())
	}
	if err := events.Emit(cctx, OrderPlaced{}); !errors.Is(err, events.ErrClosed) {
		t.Errorf("Emit after Close = %v", err)
	}

	// A Close that runs out of time cancels the listeners and says so.
	d, _ := newBus(t)
	var canceled atomic.Bool
	check(t, events.OnAsync(d, func(ctx context.Context, e OrderPlaced) error {
		<-ctx.Done()
		canceled.Store(true)
		return nil
	}))
	check(t, events.Emit(events.WithBus(context.Background(), d), OrderPlaced{}))
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := d.Close(short); err == nil || !strings.Contains(err.Error(), "0 async event(s) not handled, 1 canceled") {
		t.Errorf("Close = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !canceled.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !canceled.Load() {
		t.Error("the listener's context wasn't canceled")
	}
}

func notifyWarehouse(context.Context, OrderPlaced) error { return nil }

type Mailer struct{}

func (*Mailer) Send(context.Context, OrderPlaced) error { return nil }

func TestOnQueued(t *testing.T) {
	store := queue.NewMemoryStore()
	q := queue.NewWithStore(store, queue.Config{PollInterval: 5 * time.Millisecond}, queue.WithLogger(slog.New(slog.NewTextHandler(io.Discard, nil))))
	b, _ := newBus(t, events.WithQueue(q))
	var got atomic.Int64
	check(t, events.OnQueued(b, func(ctx context.Context, e OrderPlaced) error {
		info, _ := queue.Current(ctx)
		if info.Job != "event:warehouse" || info.Tries != 7 {
			return queue.Permanent(fmt.Errorf("info = %+v", info))
		}
		got.Add(e.OrderID)
		return nil
	}, events.Name("warehouse"), events.Job(queue.Tries(7)), events.Dispatch(queue.OnQueue("events"))))
	// Anonymous functions need a name; named ones don't.
	if err := events.OnQueued(b, func(context.Context, OrderPlaced) error { return nil }); err == nil || !strings.Contains(err.Error(), "events.Name") {
		t.Errorf("anonymous queued listener: %v", err)
	}
	check(t, events.OnQueued(b, notifyWarehouse))
	check(t, events.OnQueued(b, (&Mailer{}).Send))
	if err := events.OnQueued(b, notifyWarehouse); err == nil {
		t.Error("the same queued listener twice = nil")
	}
	ctx := events.WithBus(queue.WithQueue(context.Background(), q), b)
	check(t, events.Emit(ctx, OrderPlaced{OrderID: 5}))
	if n, _ := store.Size(ctx, "events"); n != 1 {
		t.Errorf("events queue: %d jobs", n)
	}
	if n, _ := store.Size(ctx, "default"); n != 2 {
		t.Errorf("default queue: %d jobs, want 2 (notifyWarehouse, Mailer.Send)", n)
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- q.Run(runCtx, queue.Queues("events", "default")) }()
	deadline := time.Now().Add(5 * time.Second)
	for got.Load() != 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	stop()
	check(t, <-done)
	if got.Load() != 5 {
		t.Errorf("the queued listener got %d", got.Load())
	}
	f, err := store.Failed(ctx, 0, 10)
	check(t, err)
	if len(f) != 0 {
		t.Errorf("failed: %+v", f)
	}
	for _, j := range []string{"event:events_test.notifyWarehouse", "event:events_test.(*Mailer).Send"} {
		if err := q.DispatchFunc(ctx, j, OrderPlaced{}); err != nil {
			t.Errorf("job %s: %v", j, err)
		}
	}
}

func TestAppNew(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "QUEUE_DRIVER": "sync"}),
		anetos.WithLogOutput(io.Discard))
	check(t, err)
	app.AddContextValue(ctxKey{}, "app")
	var unitMu sync.Mutex
	var units []string
	app.AroundOperations(func(ctx context.Context, u anetos.Operation) (context.Context, func()) {
		unitMu.Lock()
		defer unitMu.Unlock()
		units = append(units, u.Kind+" "+u.Name)
		return ctx, nil
	})
	_, err = queue.New(app)
	check(t, err)
	b, err := events.New(app)
	check(t, err)
	if _, err := events.New(app); err == nil {
		t.Error("New twice = nil")
	}
	if anetos.MustResolve[*events.Bus](app) != b {
		t.Error("the bus isn't provided")
	}
	var queued, async atomic.Value
	check(t, events.OnQueued(b, func(ctx context.Context, e OrderPlaced) error {
		queued.Store(ctx.Value(ctxKey{}))
		return nil
	}, events.Name("q")))
	release := make(chan struct{})
	check(t, events.OnAsync(b, func(ctx context.Context, e OrderPlaced) error {
		<-release
		async.Store(ctx.Value(ctxKey{}))
		return nil
	}))
	ctx := app.Context(context.Background())
	check(t, events.Emit(ctx, OrderPlaced{OrderID: 1}))
	if queued.Load() != "app" {
		t.Errorf("the queued listener (sync driver) got %v", queued.Load())
	}
	close(release)
	// Closing the app drains the async listener.
	check(t, app.Close())
	if async.Load() != "app" {
		t.Errorf("after Close, the async listener got %v", async.Load())
	}
	unitMu.Lock()
	defer unitMu.Unlock()
	if !slices.Contains(units, "job event:q") || !slices.ContainsFunc(units, func(u string) bool { return strings.HasPrefix(u, "listener ") }) {
		t.Errorf("units %v", units)
	}
}

func TestCloseTimeoutDropsBuffered(t *testing.T) {
	b, _ := newBus(t)
	check(t, events.OnAsync(b, func(ctx context.Context, e OrderPlaced) error {
		<-ctx.Done()
		return nil
	}, events.Buffer(5)))
	ctx := events.WithBus(context.Background(), b)
	for range 4 { // 1 running, 3 buffered
		check(t, events.Emit(ctx, OrderPlaced{}))
	}
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.Close(short); err == nil || !strings.Contains(err.Error(), "3 async event(s) not handled, 1 canceled") {
		t.Errorf("Close = %v", err)
	}
	// Nothing is left pending: Wait and a second Close return.
	wctx, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	check(t, b.Wait(wctx))
	check(t, b.Close(wctx))
}

type Chain struct{ N int }

func TestEmitWhileClosing(t *testing.T) {
	b, logs := newBus(t)
	var last atomic.Int64
	check(t, events.OnAsync(b, func(ctx context.Context, e Chain) error {
		time.Sleep(5 * time.Millisecond)
		last.Store(int64(e.N))
		if e.N < 3 {
			return events.Emit(ctx, Chain{N: e.N + 1}) // an async listener may still emit while the bus closes
		}
		return nil
	}))
	ctx := events.WithBus(context.Background(), b)
	check(t, events.Emit(ctx, Chain{N: 1}))
	check(t, b.Close(context.Background()))
	if last.Load() != 3 {
		t.Errorf("the chain stopped at %d; logs:\n%s", last.Load(), logs.String())
	}
	if err := events.Emit(ctx, Chain{N: 1}); !errors.Is(err, events.ErrClosed) {
		t.Errorf("Emit after Close = %v", err)
	}
}

type Stringer interface{ String() string }

func notifyAny[E any](context.Context, E) error { return nil }

func TestListenerTypesAndNames(t *testing.T) {
	q := queue.NewWithStore(queue.NewMemoryStore(), queue.Config{})
	b, _ := newBus(t, events.WithQueue(q))
	if err := events.On(b, func(context.Context, Stringer) error { return nil }); err == nil || !strings.Contains(err.Error(), "interface") {
		t.Errorf("an interface event type: %v", err)
	}
	if err := events.OnQueued(b, notifyAny[OrderPlaced]); err == nil || !strings.Contains(err.Error(), "generic") {
		t.Errorf("a generic queued listener without a name: %v", err)
	}
	check(t, events.OnQueued(b, notifyAny[OrderPlaced], events.Name("notify-order")))
	check(t, events.OnQueued(b, notifyAny[Chain], events.Name("notify-chain")))
	check(t, events.On(b, notifyAny[OrderPlaced])) // sync and async listeners may share names
	check(t, events.On(b, notifyAny[OrderPlaced]))
	if err := queue.RegisterFunc(q, "any", func(context.Context, any) error { return nil }); err == nil {
		t.Error("RegisterFunc with an interface payload = nil")
	}
}

// failingStore is a queue store whose pushes fail.
type failingStore struct{ queue.Store }

func (failingStore) Push(context.Context, queue.Message, time.Duration) error {
	return errors.New("redis down")
}

func TestQueuedDispatchError(t *testing.T) {
	q := queue.NewWithStore(failingStore{queue.NewMemoryStore()}, queue.Config{})
	b, _ := newBus(t, events.WithQueue(q))
	check(t, events.OnQueued(b, notifyWarehouse))
	var async atomic.Int32
	check(t, events.OnAsync(b, func(context.Context, OrderPlaced) error { async.Add(1); return nil }))
	ctx := events.WithBus(context.Background(), b)
	err := events.Emit(ctx, OrderPlaced{})
	if err == nil || !strings.Contains(err.Error(), "events_test.notifyWarehouse") || !strings.Contains(err.Error(), "redis down") {
		t.Errorf("Emit = %v", err)
	}
	check(t, b.Wait(ctx))
	if async.Load() != 1 {
		t.Error("the async listener didn't get the event")
	}
}

// Async listeners get the values of the app's carriers from the context
// that emitted the event.
func TestAsyncCarriedValues(t *testing.T) {
	app, err := anetos.New(anetos.WithSource(config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}),
		anetos.WithLogOutput(io.Discard))
	check(t, err)
	type who struct{}
	app.AddCarrier(anetos.Carrier{
		Name:    "test.who",
		Capture: func(ctx context.Context) string { s, _ := ctx.Value(who{}).(string); return s },
		Restore: func(ctx context.Context, v string) context.Context { return context.WithValue(ctx, who{}, v) },
	})
	b, err := events.New(app)
	check(t, err)
	got := make(chan string, 2)
	check(t, events.OnAsync(b, func(ctx context.Context, e OrderPlaced) error {
		s, _ := ctx.Value(who{}).(string)
		got <- s
		return nil
	}))
	ctx := app.Context(context.Background())
	check(t, events.Emit(context.WithValue(ctx, who{}, "user:7"), OrderPlaced{OrderID: 1}))
	check(t, events.Emit(ctx, OrderPlaced{OrderID: 2}))
	check(t, b.Wait(ctx))
	if a, b := <-got, <-got; a != "user:7" || b != "" {
		t.Errorf("the listener saw %q and %q", a, b)
	}
	check(t, app.Close())
}
