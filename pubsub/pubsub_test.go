// SPDX-License-Identifier: Apache-2.0

package pubsub_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/pubsub"
	"anetos.dev/anetos/pubsub/pubsubtest"
	"anetos.dev/anetos/queue"
)

func TestMemoryBroker(t *testing.T) {
	pubsubtest.Run(t, func(t *testing.T) pubsub.Broker {
		b := pubsub.NewMemoryBroker()
		t.Cleanup(func() { _ = b.Close() })
		return b
	}, pubsubtest.Features{Attempts: true, Delays: true, AckTimeout: true, Ordered: true})
}

type OrderCreated struct {
	ID    int64  `json:"id"`
	Mode  string `json:"mode,omitempty"`
	Key   string `json:"key"`
	Fails int    `json:"fails,omitempty"`
}

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

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// run runs p's listeners until the test ends; stop stops them and waits.
func run(t *testing.T, p *pubsub.PubSub) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(pubsub.WithPubSub(context.Background(), p))
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("Run = %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

type recorder struct {
	mu   sync.Mutex
	msgs []OrderCreated
	meta []pubsub.Message
}

func (r *recorder) handle(ctx context.Context, o OrderCreated) error {
	m, _ := pubsub.Current(ctx)
	r.mu.Lock()
	r.msgs = append(r.msgs, o)
	r.meta = append(r.meta, *m)
	n := 0
	for _, x := range r.msgs {
		if x.Key == o.Key {
			n++
		}
	}
	r.mu.Unlock()
	switch o.Mode {
	case "permanent":
		return pubsub.Permanent(errors.New("unknown customer"))
	case "panic":
		panic("kaboom")
	case "timeout":
		<-ctx.Done()
		return ctx.Err()
	case "block":
		<-ctx.Done()
		return context.Cause(ctx)
	}
	if n <= o.Fails {
		return fmt.Errorf("attempt %d failed", n)
	}
	return nil
}

func (r *recorder) count(key string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, x := range r.msgs {
		if x.Key == key {
			n++
		}
	}
	return n
}

func newPS(t *testing.T) (*pubsub.PubSub, *pubsub.MemoryBroker, *logBuffer) {
	t.Helper()
	b := pubsub.NewMemoryBroker()
	logs := &logBuffer{}
	return pubsub.New(b, pubsub.WithLogger(slog.New(slog.NewTextHandler(logs, nil))), pubsub.WithName("billing")), b, logs
}

// deadLetters subscribes to topic and collects its messages.
func deadLetters(t *testing.T, b pubsub.Broker, topic string) func() []pubsub.Message {
	t.Helper()
	var mu sync.Mutex
	var got []pubsub.Message
	s := pubsub.SubscriptionSpec{Topic: topic, Name: topic + ".test", Concurrency: 1, AckTimeout: time.Minute}
	check(t, b.Prepare(context.Background(), s))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = b.Subscribe(ctx, s, func(_ context.Context, m *pubsub.Message) pubsub.Outcome {
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

func TestListen(t *testing.T) {
	p, b, logs := newPS(t)
	r := &recorder{}
	check(t, pubsub.Listen(p, "orders.created", r.handle, pubsub.Concurrency(4), pubsub.MaxAttempts(3),
		pubsub.Backoff(5*time.Millisecond), pubsub.DeadLetter("orders.dlq"), pubsub.Timeout(50*time.Millisecond)))
	dlq := deadLetters(t, b, "orders.dlq")
	run(t, p)
	time.Sleep(20 * time.Millisecond) // Run prepares the subscription
	ctx := pubsub.WithPubSub(context.Background(), p)
	msgs := []OrderCreated{
		{ID: 1, Key: "ok"},
		{ID: 2, Key: "retried", Fails: 2},
		{ID: 3, Key: "exhausted", Fails: 5},
		{ID: 4, Key: "permanent", Mode: "permanent"},
		{ID: 5, Key: "panic", Mode: "panic"},
		{ID: 6, Key: "timeout", Mode: "timeout"},
	}
	for _, m := range msgs {
		check(t, pubsub.Publish(ctx, "orders.created", m, pubsub.Attributes(map[string]string{"source": "shop"})))
	}
	check(t, pubsub.Publish(ctx, "orders.created", []byte("not json")))
	eventually(t, "the dead letters", func() bool { return len(dlq()) == 5 })
	for key, want := range map[string]int{"ok": 1, "retried": 3, "exhausted": 3, "permanent": 1, "panic": 3, "timeout": 3} {
		if n := r.count(key); n != want {
			t.Errorf("%s: %d deliveries, want %d", key, n, want)
		}
	}
	errs := map[string]string{}
	for _, m := range dlq() {
		if m.Attributes["anetos.topic"] != "orders.created" || m.Attributes["anetos.subscription"] != "orders.created.billing" {
			t.Errorf("dead letter attributes: %v", m.Attributes)
		}
		errs[string(m.Data)] = m.Attributes["anetos.error"] + " / " + m.Attributes["anetos.attempts"] + " / " + m.Attributes["source"]
	}
	for data, want := range map[string]string{
		`{"id":3,"key":"exhausted","fails":5}`:          "attempt 3 failed / 3 / shop",
		`{"id":4,"mode":"permanent","key":"permanent"}`: "unknown customer / 1 / shop",
		`{"id":5,"mode":"panic","key":"panic"}`:         "panic: kaboom / 3 / shop",
		"not json":                                      "decode the message: invalid character 'o' in literal null (expecting 'u') / 1 / ",
	} {
		if errs[data] != want {
			t.Errorf("dead letter %s: %q, want %q", data, errs[data], want)
		}
	}
	if got := errs[`{"id":6,"mode":"timeout","key":"timeout"}`]; !strings.HasPrefix(got, "timed out after 50ms") {
		t.Errorf("timeout dead letter: %q", got)
	}
	r.mu.Lock()
	for _, m := range r.meta {
		if m.Topic != "orders.created" || m.ID == "" || m.Attempt < 1 || m.Attributes["source"] != "shop" {
			t.Errorf("Current = %+v", m)
		}
	}
	r.mu.Unlock()
	if !strings.Contains(logs.String(), "sent to the dead-letter topic") {
		t.Errorf("logs:\n%s", logs.String())
	}
}

func TestDropWithoutDeadLetter(t *testing.T) {
	p, _, logs := newPS(t)
	r := &recorder{}
	check(t, pubsub.Listen(p, "orders.created", r.handle, pubsub.MaxAttempts(2), pubsub.Backoff(time.Millisecond)))
	run(t, p)
	time.Sleep(20 * time.Millisecond)
	check(t, p.Publish(context.Background(), "orders.created", OrderCreated{Key: "x", Fails: 9}))
	eventually(t, "the drop", func() bool { return strings.Contains(logs.String(), "dropped (no dead-letter topic)") })
	if n := r.count("x"); n != 2 {
		t.Errorf("%d deliveries, want 2", n)
	}
}

func TestRawAndUnlimited(t *testing.T) {
	p, _, _ := newPS(t)
	var got atomic.Value
	var n atomic.Int32
	check(t, pubsub.Listen(p, "raw", func(ctx context.Context, b []byte) error {
		if n.Add(1) < 4 { // no MaxAttempts: retried until it works
			return errors.New("not yet")
		}
		got.Store(string(b))
		return nil
	}, pubsub.Backoff(time.Millisecond)))
	run(t, p)
	time.Sleep(20 * time.Millisecond)
	check(t, p.Publish(context.Background(), "raw", []byte("\x00 bytes")))
	eventually(t, "the message", func() bool { return got.Load() != nil })
	if got.Load() != "\x00 bytes" {
		t.Errorf("got %q", got.Load())
	}
}

func TestShutdownGrace(t *testing.T) {
	p, b, _ := newPS(t)
	r := &recorder{}
	check(t, pubsub.Listen(p, "orders.created", r.handle, pubsub.ShutdownGrace(20*time.Millisecond), pubsub.MaxAttempts(1)))
	stop := run(t, p)
	time.Sleep(20 * time.Millisecond)
	check(t, p.Publish(context.Background(), "orders.created", OrderCreated{Key: "slow", Mode: "block"}))
	eventually(t, "the message", func() bool { return r.count("slow") == 1 })
	stop()
	// Stopped by the shutdown: not given up on (despite MaxAttempts 1),
	// so another listener gets it.
	s := pubsub.SubscriptionSpec{Topic: "orders.created", Name: "orders.created.billing", Concurrency: 1, AckTimeout: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var again atomic.Bool
	_ = b.Subscribe(ctx, s, func(context.Context, *pubsub.Message) pubsub.Outcome {
		again.Store(true)
		cancel()
		return pubsub.Outcome{Ack: true}
	})
	if !again.Load() {
		t.Error("the stopped message wasn't delivered again")
	}
}

func TestPublish(t *testing.T) {
	p, b, _ := newPS(t)
	s := pubsub.SubscriptionSpec{Topic: "t", Name: "t.x", Concurrency: 1, AckTimeout: time.Minute}
	check(t, b.Prepare(context.Background(), s))
	ctx := pubsub.WithPubSub(context.Background(), p)
	check(t, pubsub.Publish(ctx, "t", map[string]int{"a": 1}))
	check(t, pubsub.Publish(ctx, "t", []byte("raw")))
	check(t, pubsub.Publish(ctx, "t", OrderCreated{ID: 9}, pubsub.AfterCommit())) // no transaction: at once
	var got []string
	sctx, cancel := context.WithCancel(context.Background())
	_ = b.Subscribe(sctx, s, func(_ context.Context, m *pubsub.Message) pubsub.Outcome {
		got = append(got, string(m.Data))
		if len(got) == 3 {
			cancel()
		}
		return pubsub.Outcome{Ack: true}
	})
	if strings.Join(got, " ") != `{"a":1} raw {"id":9,"key":""}` {
		t.Errorf("published %q", got)
	}
	for name, err := range map[string]error{
		"bad topic":    pubsub.Publish(ctx, "bad topic", 1),
		"unencodable":  pubsub.Publish(ctx, "t", func() {}),
		"no pubsub":    pubsub.Publish(context.Background(), "t", 1),
		"closed":       func() error { _ = b.Close(); return pubsub.Publish(ctx, "t", 1) }(),
		"after commit": pubsub.Publish(ctx, "t", 1, pubsub.AfterCommit()), // closed broker, no transaction: returned
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestListenErrors(t *testing.T) {
	p, _, _ := newPS(t)
	fn := func(context.Context, OrderCreated) error { return nil }
	for name, err := range map[string]error{
		"bad topic":       pubsub.Listen(p, "", fn),
		"nil":             pubsub.Listen[OrderCreated](p, "t", nil),
		"interface":       pubsub.Listen(p, "t", func(context.Context, any) error { return nil }),
		"Concurrency(0)":  pubsub.Listen(p, "t", fn, pubsub.Concurrency(0)),
		"Timeout(0)":      pubsub.Listen(p, "t", fn, pubsub.Timeout(0)),
		"MaxAttempts(-1)": pubsub.Listen(p, "t", fn, pubsub.MaxAttempts(-1)),
		"Backoff()":       pubsub.Listen(p, "t", fn, pubsub.Backoff()),
		"Backoff(-1)":     pubsub.Listen(p, "t", fn, pubsub.Backoff(-1)),
		"bad dead letter": pubsub.Listen(p, "t", fn, pubsub.DeadLetter("bad topic")),
		"bad name":        pubsub.Listen(p, "t", fn, pubsub.Subscription("")),
		"ShutdownGrace":   pubsub.Listen(p, "t", fn, pubsub.ShutdownGrace(-1)),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	check(t, pubsub.Listen(p, "t", fn))
	if err := pubsub.Listen(p, "t", fn); err == nil {
		t.Error("two listeners with one subscription = nil")
	}
	check(t, pubsub.Listen(p, "t", fn, pubsub.Subscription("t.other")))
	if err := pubsub.New(pubsub.NewMemoryBroker()).Run(context.Background()); err == nil {
		t.Error("Run without listeners = nil")
	}
}

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "APP_NAME": "billing"}
	maps.Copy(src, env)
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestForApp(t *testing.T) {
	if _, err := pubsub.ForApp(newApp(t, config.Map{"PUBSUB_DRIVER": "kafka"})); err == nil || !strings.Contains(err.Error(), `PUBSUB_DRIVER is "kafka"`) {
		t.Errorf("unknown driver: %v", err)
	}
	app := newApp(t, nil)
	var unitMu sync.Mutex
	var units []string
	app.AroundUnits(func(ctx context.Context, u anetos.Unit) (context.Context, func()) {
		unitMu.Lock()
		defer unitMu.Unlock()
		units = append(units, u.Kind+" "+u.Name)
		return ctx, nil
	})
	p, err := pubsub.ForApp(app)
	check(t, err)
	if anetos.MustResolve[*pubsub.PubSub](app) != p {
		t.Error("not provided")
	}
	r := &recorder{}
	check(t, pubsub.Listen(p, "orders.created", r.handle))
	if got := app.Supervisor().Roles(); len(got) != 1 || got[0] != "listeners" {
		t.Errorf("roles = %v", got)
	}
	// A message published after boot, before the listener runs, is kept:
	// the subscription is prepared when the app boots.
	check(t, app.Boot(context.Background()))
	check(t, pubsub.Publish(app.Context(context.Background()), "orders.created", OrderCreated{Key: "early"}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, "listeners") }()
	eventually(t, "the message", func() bool { return r.count("early") == 1 })
	// A listener added while the app runs prepares its subscription and
	// starts.
	r2 := &recorder{}
	check(t, pubsub.Listen(p, "late.topic", r2.handle))
	check(t, pubsub.Publish(app.Context(context.Background()), "late.topic", OrderCreated{Key: "late"}))
	eventually(t, "the late listener", func() bool { return r2.count("late") == 1 })
	cancel()
	check(t, <-done)
	unitMu.Lock()
	if !slices.ContainsFunc(units, func(u string) bool { return strings.HasPrefix(u, "message orders.created (") }) {
		t.Errorf("units %v", units)
	}
	unitMu.Unlock()

	// ForApp after boot prepares each listener's subscription itself.
	app3 := newApp(t, nil)
	check(t, app3.Boot(context.Background()))
	p3, err := pubsub.ForApp(app3)
	check(t, err)
	r3 := &recorder{}
	check(t, pubsub.Listen(p3, "after.boot", r3.handle))
	check(t, pubsub.Publish(app3.Context(context.Background()), "after.boot", OrderCreated{Key: "kept"}))
	ctx3, cancel3 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel3()
	go func() { _ = p3.Run(ctx3) }()
	eventually(t, "the message published before the listener ran", func() bool { return r3.count("kept") == 1 })
	cancel3()

	// pubsub:publish refuses the memory broker, which is per process.
	app2 := newApp(t, config.Map{"PUBSUB_DRIVER": "custom"})
	p2, err := pubsub.ForApp(app2, pubsub.Driver{Name: "custom", Open: func(*anetos.App, pubsub.Config) (pubsub.Broker, error) {
		return pubsub.NewMemoryBroker(), nil
	}})
	check(t, err)
	s := pubsub.SubscriptionSpec{Topic: "t", Name: "t.cli", Concurrency: 1, AckTimeout: time.Minute}
	check(t, p2.Broker().Prepare(context.Background(), s))
	var c cmd.Command
	for _, x := range app2.Commands() {
		if x.Name == "pubsub:publish" {
			c = x
		}
	}
	var out bytes.Buffer
	check(t, c.Run(context.Background(), &cmd.Args{Args: []string{"t", `{"id":1}`}, Stdout: &out}))
	if !strings.Contains(out.String(), "Published message 1 to t.") {
		t.Errorf("output: %q", out.String())
	}
	for _, args := range [][]string{{"t"}, {"bad topic", "x"}} {
		if err := c.Run(context.Background(), &cmd.Args{Args: args, Stdout: &out}); !errors.Is(err, cmd.ErrUsage) {
			t.Errorf("pubsub:publish %q = %v", args, err)
		}
	}
	for _, x := range app.Commands() {
		if x.Name == "pubsub:publish" {
			if err := x.Run(context.Background(), &cmd.Args{Args: []string{"t", "{}"}, Stdout: &out}); err == nil || !strings.Contains(err.Error(), "memory broker") {
				t.Errorf("pubsub:publish with the memory broker = %v", err)
			}
		}
	}
}

// flaky fails its first Subscribe calls, and publishes to dead-letter
// topics fail its first times.
type flaky struct {
	*pubsub.MemoryBroker
	subscribeFails atomic.Int32
	publishFails   atomic.Int32
}

func (f *flaky) Subscribe(ctx context.Context, s pubsub.SubscriptionSpec, h func(context.Context, *pubsub.Message) pubsub.Outcome) error {
	if f.subscribeFails.Add(-1) >= 0 {
		return errors.New("connection reset")
	}
	return f.MemoryBroker.Subscribe(ctx, s, h)
}

func (f *flaky) Publish(ctx context.Context, topic string, m pubsub.Outgoing) (string, error) {
	if strings.HasSuffix(topic, ".dlq") && f.publishFails.Add(-1) >= 0 {
		return "", errors.New("broker busy")
	}
	return f.MemoryBroker.Publish(ctx, topic, m)
}

func TestRunRestartsAndDeadLetterRetries(t *testing.T) {
	f := &flaky{MemoryBroker: pubsub.NewMemoryBroker()}
	f.subscribeFails.Store(1)
	f.publishFails.Store(2)
	logs := &logBuffer{}
	p := pubsub.New(f, pubsub.WithLogger(slog.New(slog.NewTextHandler(logs, nil))), pubsub.WithName("billing"))
	r := &recorder{}
	check(t, pubsub.Listen(p, "orders.created", r.handle, pubsub.DeadLetter("orders.dlq")))
	dlq := deadLetters(t, f.MemoryBroker, "orders.dlq")
	run(t, p)
	time.Sleep(20 * time.Millisecond)
	check(t, p.Publish(context.Background(), "orders.created", OrderCreated{Key: "bad", Mode: "permanent"}))
	// The listener started again after its broker failed, and the dead
	// letter got through on the third try, without running the listener
	// again.
	eventually(t, "the dead letter", func() bool { return len(dlq()) == 1 })
	if n := r.count("bad"); n != 1 {
		t.Errorf("the listener ran %d times, want 1", n)
	}
	if !strings.Contains(logs.String(), "connection reset") {
		t.Errorf("logs:\n%s", logs.String())
	}
}

// noCount is a broker that doesn't count deliveries.
type noCount struct{ *pubsub.MemoryBroker }

func (n noCount) Subscribe(ctx context.Context, s pubsub.SubscriptionSpec, h func(context.Context, *pubsub.Message) pubsub.Outcome) error {
	return n.MemoryBroker.Subscribe(ctx, s, func(ctx context.Context, m *pubsub.Message) pubsub.Outcome {
		m.Attempt = 0
		return h(ctx, m)
	})
}

func TestMaxAttemptsWithoutCounts(t *testing.T) {
	logs := &logBuffer{}
	p := pubsub.New(noCount{pubsub.NewMemoryBroker()}, pubsub.WithLogger(slog.New(slog.NewTextHandler(logs, nil))))
	r := &recorder{}
	check(t, pubsub.Listen(p, "t", r.handle, pubsub.MaxAttempts(2), pubsub.Backoff(time.Millisecond)))
	run(t, p)
	time.Sleep(20 * time.Millisecond)
	check(t, p.Publish(context.Background(), "t", OrderCreated{Key: "k", Fails: 4}))
	eventually(t, "the fifth delivery", func() bool { return r.count("k") == 5 }) // MaxAttempts can't apply
	if n := strings.Count(logs.String(), "doesn't count this subscription's deliveries"); n != 1 {
		t.Errorf("warned %d times, want once", n)
	}
}

func TestPermanentAcrossPackages(t *testing.T) {
	if !pubsub.IsPermanent(fmt.Errorf("wrapped: %w", queue.Permanent(io.EOF))) || !queue.IsPermanent(pubsub.Permanent(io.EOF)) {
		t.Error("queue and pubsub don't recognize each other's permanent errors")
	}
	if pubsub.IsPermanent(io.EOF) || pubsub.Permanent(nil) != nil {
		t.Error("IsPermanent")
	}
}

func TestRawMessage(t *testing.T) {
	p, _, _ := newPS(t)
	var got atomic.Value
	check(t, pubsub.Listen(p, "raw", func(_ context.Context, b json.RawMessage) error { got.Store(string(b)); return nil }))
	run(t, p)
	time.Sleep(20 * time.Millisecond)
	check(t, p.Publish(context.Background(), "raw", []byte("not json")))
	eventually(t, "the message", func() bool { return got.Load() == "not json" })
}

// Blob is a named byte slice.
type Blob []byte

func TestByteSliceRoundTrip(t *testing.T) {
	p, _, _ := newPS(t)
	var got atomic.Value
	check(t, pubsub.Listen(p, "blob", func(_ context.Context, b Blob) error { got.Store(string(b)); return nil }))
	run(t, p)
	time.Sleep(20 * time.Millisecond)
	check(t, p.Publish(context.Background(), "blob", Blob("hello")))
	eventually(t, "the message", func() bool { return got.Load() == "hello" })
}
