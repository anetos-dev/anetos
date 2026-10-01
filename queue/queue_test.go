// SPDX-License-Identifier: Apache-2.0

package queue_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/queue/queuetest"
)

func TestMemoryStore(t *testing.T) {
	queuetest.Run(t, func(*testing.T) queue.Store { return queue.NewMemoryStore() })
}

// runs records what the test jobs did, by key.
var runs = struct {
	sync.Mutex
	calls  map[string][]queue.Info
	failed map[string][]string
	seq    []string // keys, in the order the jobs ran
}{calls: map[string][]queue.Info{}, failed: map[string][]string{}}

func record(key string, info queue.Info) int {
	runs.Lock()
	defer runs.Unlock()
	runs.seq = append(runs.seq, key)
	runs.calls[key] = append(runs.calls[key], info)
	return len(runs.calls[key])
}

func calls(key string) []queue.Info {
	runs.Lock()
	defer runs.Unlock()
	return append([]queue.Info(nil), runs.calls[key]...)
}

// sequence returns the keys starting with prefix, in the order their
// jobs ran.
func sequence(prefix string) []string {
	runs.Lock()
	defer runs.Unlock()
	var out []string
	for _, k := range runs.seq {
		if strings.HasPrefix(k, prefix+"-") {
			out = append(out, k)
		}
	}
	return out
}

func failures(key string) []string {
	runs.Lock()
	defer runs.Unlock()
	return append([]string(nil), runs.failed[key]...)
}

var keys atomic.Int64

func newKey() string { return fmt.Sprintf("k%d", keys.Add(1)) }

// Work fails until its attempt number reaches SucceedAt (0: never), or
// does what Mode says.
type Work struct {
	Key       string `json:"key"`
	SucceedAt int    `json:"succeed_at"`
	Mode      string `json:"mode,omitempty"`
}

type ctxKey struct{}

func (w Work) Handle(ctx context.Context) error {
	info, _ := queue.Current(ctx)
	n := record(w.Key, info)
	switch w.Mode {
	case "permanent":
		return queue.Permanent(errors.New("no such record"))
	case "panic":
		panic("kaboom")
	case "timeout":
		<-ctx.Done()
		return ctx.Err()
	case "block": // until the worker's grace period ends
		<-ctx.Done()
		return fmt.Errorf("stopped: %w", context.Cause(ctx))
	case "block-permanent":
		<-ctx.Done()
		return queue.Permanent(errors.New("gave up"))
	case "bytes":
		return queue.Permanent(errors.New("bad \x00 \xff bytes"))
	case "value":
		if ctx.Value(ctxKey{}) != "app" {
			return queue.Permanent(errors.New("the context lacks the app's value"))
		}
		return nil
	case "dispatch": // dispatch another job with the job's context
		return queue.Dispatch(ctx, Work{Key: w.Key + "-child", SucceedAt: 1})
	}
	if w.SucceedAt > 0 && n >= w.SucceedAt {
		return nil
	}
	return fmt.Errorf("attempt %d failed", n)
}

func (w Work) Failed(_ context.Context, err error) {
	runs.Lock()
	defer runs.Unlock()
	runs.failed[w.Key] = append(runs.failed[w.Key], err.Error())
}

// Ptr is a job registered as a pointer type.
type Ptr struct {
	Key string `json:"key"`
}

func (p *Ptr) Handle(ctx context.Context) error {
	info, _ := queue.Current(ctx)
	record(p.Key, info)
	return nil
}

func newQueue(t *testing.T, store queue.Store, opts ...queue.JobOption) *queue.Queue {
	t.Helper()
	q := queue.New(store, queue.Config{Poll: 10 * time.Millisecond, Backoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond},
		queue.WithLogger(testLogger(t)))
	if err := queue.Register[Work](q, opts...); err != nil {
		t.Fatal(err)
	}
	if err := queue.Register[*Ptr](q); err != nil {
		t.Fatal(err)
	}
	return q
}

// start runs workers until the test ends, and returns a function that
// stops them and waits.
func start(t *testing.T, q *queue.Queue, opts ...queue.WorkOption) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxKey{}, "app"))
	ctx = queue.WithQueue(ctx, q)
	done := make(chan error, 1)
	go func() { done <- q.Run(ctx, opts...) }()
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

// eventually waits for cond.
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

func failedJobs(t *testing.T, s queue.Store) []queue.FailedJob {
	t.Helper()
	f, err := s.Failed(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestRetriesThenSuccess(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s)
	start(t, q)
	k := newKey()
	if err := q.Dispatch(context.Background(), Work{Key: k, SucceedAt: 3}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "3 attempts", func() bool { return len(calls(k)) == 3 })
	eventually(t, "the job to be deleted", func() bool { n, _ := s.Size(context.Background(), "default"); return n == 0 })
	got := calls(k)
	for i, info := range got {
		if info.Attempt != i+1 || info.Tries != 3 || info.Job != "queue_test.Work" || info.Queue != "default" || info.ID != got[0].ID || info.ID == "" {
			t.Errorf("attempt %d: Info = %+v", i+1, info)
		}
	}
	if f := failedJobs(t, s); len(f) != 0 || len(failures(k)) != 0 {
		t.Errorf("failed: %v, %v", f, failures(k))
	}
}

func TestTriesExhausted(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s, queue.Tries(2), queue.Backoff(time.Millisecond))
	start(t, q)
	k := newKey()
	if err := q.Dispatch(context.Background(), Work{Key: k}, queue.OnQueue("default")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the failed job", func() bool { return len(failedJobs(t, s)) == 1 })
	f := failedJobs(t, s)[0]
	if f.Attempts != 2 || f.Error != "attempt 2 failed" || f.Job() != "queue_test.Work" || f.Queue != "default" {
		t.Errorf("failed job = %+v", f)
	}
	eventually(t, "Failed to run", func() bool { return len(failures(k)) == 1 })
	if got := failures(k); got[0] != "attempt 2 failed" {
		t.Errorf("Failed got %q", got)
	}
	if n := len(calls(k)); n != 2 {
		t.Errorf("%d attempts, want 2", n)
	}
}

func TestPermanentPanicTimeout(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s, queue.Timeout(50*time.Millisecond))
	start(t, q, queue.Concurrency(3))
	perm, pan, slow := newKey(), newKey(), newKey()
	for _, w := range []Work{{Key: perm, Mode: "permanent"}, {Key: pan, Mode: "panic"}, {Key: slow, Mode: "timeout"}} {
		if err := q.Dispatch(context.Background(), w); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "three failed jobs", func() bool { return len(failedJobs(t, s)) == 3 })
	errs := map[string]string{}
	for _, f := range failedJobs(t, s) {
		errs[strings.Split(string(f.Payload), `"key":"`)[1][:len(perm)]] = f.Error
	}
	if len(calls(perm)) != 1 || errs[perm] != "no such record" {
		t.Errorf("permanent: %d attempts, error %q", len(calls(perm)), errs[perm])
	}
	if len(calls(pan)) != 3 || errs[pan] != "panic: kaboom" {
		t.Errorf("panic: %d attempts, error %q", len(calls(pan)), errs[pan])
	}
	if len(calls(slow)) != 3 || !strings.HasPrefix(errs[slow], "timed out after 50ms") {
		t.Errorf("timeout: %d attempts, error %q", len(calls(slow)), errs[slow])
	}
}

func TestQueuesPriority(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s)
	ctx := context.Background()
	k := newKey()
	// Dispatched before the workers start: low first, but high goes first,
	// and each queue in order.
	for i := range 2 {
		if err := q.Dispatch(ctx, &Ptr{Key: fmt.Sprintf("%s-low-%d", k, i)}, queue.OnQueue("low")); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 2 {
		if err := q.Dispatch(ctx, &Ptr{Key: fmt.Sprintf("%s-high-%d", k, i)}, queue.OnQueue("high")); err != nil {
			t.Fatal(err)
		}
	}
	start(t, q, queue.Queues("high", "low"))
	want := []string{k + "-high-0", k + "-high-1", k + "-low-0", k + "-low-1"}
	eventually(t, "all four", func() bool { return len(sequence(k)) == 4 })
	if got := sequence(k); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("order = %v, want %v", got, want)
	}
	// Ptr is registered as *Ptr; its jobs carry its name.
	if c := calls(k + "-high-1"); len(c) != 1 || c[0].Job != "queue_test.Ptr" || c[0].Queue != "high" {
		t.Errorf("Info = %+v", c)
	}
}

func TestJobsSeeContextValues(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s)
	start(t, q)
	k := newKey()
	if err := q.Dispatch(context.Background(), Work{Key: k, Mode: "value"}); err != nil {
		t.Fatal(err)
	}
	d := newKey()
	if err := q.Dispatch(context.Background(), Work{Key: d, Mode: "dispatch"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the jobs", func() bool { return len(calls(k)) == 1 && len(calls(d+"-child")) == 1 })
	eventually(t, "the queue to empty", func() bool { n, _ := s.Size(context.Background(), "default"); return n == 0 })
	if f := failedJobs(t, s); len(f) != 0 {
		t.Errorf("failed: %+v", f)
	}
}

func TestConcurrency(t *testing.T) {
	s := queue.NewMemoryStore()
	q := queue.New(s, queue.Config{Poll: 5 * time.Millisecond}, queue.WithLogger(testLogger(t)))
	var running, peak atomic.Int32
	release := make(chan struct{})
	if err := queue.Register[*gate](q); err != nil {
		t.Fatal(err)
	}
	gates.Store(&gateState{running: &running, peak: &peak, release: release})
	for range 8 {
		if err := q.Dispatch(context.Background(), &gate{}); err != nil {
			t.Fatal(err)
		}
	}
	start(t, q, queue.Concurrency(4))
	eventually(t, "4 jobs at once", func() bool { return running.Load() == 4 })
	time.Sleep(30 * time.Millisecond)
	if p := peak.Load(); p != 4 {
		t.Errorf("peak = %d, want 4", p)
	}
	close(release)
	eventually(t, "all jobs", func() bool { n, _ := s.Size(context.Background(), "default"); return n == 0 })
}

type gateState struct {
	running, peak *atomic.Int32
	release       chan struct{}
}

var gates atomic.Pointer[gateState]

type gate struct{}

func (*gate) Handle(ctx context.Context) error {
	g := gates.Load()
	n := g.running.Add(1)
	defer g.running.Add(-1)
	for {
		p := g.peak.Load()
		if n <= p || g.peak.CompareAndSwap(p, n) {
			break
		}
	}
	select {
	case <-g.release:
	case <-ctx.Done():
	}
	return nil
}

func TestShutdown(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s)
	ctx := context.Background()
	blocked, quick := newKey(), newKey()
	if err := q.Dispatch(ctx, Work{Key: blocked, Mode: "block"}); err != nil {
		t.Fatal(err)
	}
	stop := start(t, q, queue.Concurrency(2), queue.ShutdownGrace(50*time.Millisecond))
	eventually(t, "the job to start", func() bool { return len(calls(blocked)) == 1 })
	if err := q.Dispatch(ctx, Work{Key: quick, SucceedAt: 1}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the quick job", func() bool { return len(calls(quick)) == 1 })
	began := time.Now()
	stop()
	if d := time.Since(began); d < 50*time.Millisecond || d > 2*time.Second {
		t.Errorf("stopping took %s, want the grace period", d)
	}
	// The stopped job is back, its attempt not counted.
	r, err := s.Reserve(ctx, "default", time.Minute)
	if err != nil || r == nil {
		t.Fatalf("Reserve = %v, %v; want the stopped job", r, err)
	}
	if r.Attempts != 1 || !strings.Contains(string(r.Payload), blocked) {
		t.Errorf("Reserve = %+v, want the blocked job at attempt 1", r)
	}
	if f := failedJobs(t, s); len(f) != 0 {
		t.Errorf("failed: %+v", f)
	}
}

func TestShutdownPermanent(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s)
	k := newKey()
	if err := q.Dispatch(context.Background(), Work{Key: k, Mode: "block-permanent"}); err != nil {
		t.Fatal(err)
	}
	stop := start(t, q, queue.ShutdownGrace(10*time.Millisecond))
	eventually(t, "the job to start", func() bool { return len(calls(k)) == 1 })
	stop()
	// A permanent error fails the job, shutdown or not.
	if f := failedJobs(t, s); len(f) != 1 || f[0].Error != "gave up" {
		t.Errorf("failed = %+v", f)
	}
	if n, _ := s.Size(context.Background(), "default"); n != 0 {
		t.Errorf("%d job(s) back on the queue", n)
	}
}

func TestOutOfTriesAfterCrash(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s, queue.Tries(2))
	ctx := context.Background()
	k := newKey()
	if err := q.Dispatch(ctx, Work{Key: k, SucceedAt: 1}); err != nil {
		t.Fatal(err)
	}
	// Two workers took the job and died without recording anything.
	for range 2 {
		if r, err := s.Reserve(ctx, "default", time.Millisecond); err != nil || r == nil {
			t.Fatal(r, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	start(t, q)
	eventually(t, "the failed job", func() bool { return len(failedJobs(t, s)) == 1 })
	f := failedJobs(t, s)[0]
	if f.Attempts != 3 || !strings.Contains(f.Error, "out of tries: attempt 3 of 2") {
		t.Errorf("failed job = %+v", f)
	}
	if len(calls(k)) != 0 {
		t.Errorf("the job ran %d time(s), want none", len(calls(k)))
	}
	eventually(t, "Failed", func() bool { return len(failures(k)) == 1 })
}

// Panicky panics when it is decoded.
type Panicky struct{}

func (*Panicky) UnmarshalJSON([]byte) error { panic("bad data") }

func (Panicky) Handle(context.Context) error { return nil }

func TestErrorTextAndDecodePanic(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s)
	if err := queue.Register[Panicky](q); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := q.Dispatch(ctx, Work{Key: newKey(), Mode: "bytes"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(ctx, queue.Message{ID: "panicky", Queue: "default", Payload: []byte(`{"id":"p","job":"queue_test.Panicky","data":{}}`)}, 0); err != nil {
		t.Fatal(err)
	}
	start(t, q)
	eventually(t, "two failed jobs", func() bool { return len(failedJobs(t, s)) == 2 })
	for _, f := range failedJobs(t, s) {
		switch {
		case f.ID == "panicky":
			if f.Attempts != 1 || f.Error != "decode queue_test.Panicky: panic: bad data" {
				t.Errorf("decode panic: %+v", f)
			}
		case f.Error != "bad \uFFFD \uFFFD bytes":
			t.Errorf("error text = %q, want it valid UTF-8 without NUL", f.Error)
		}
	}
}

func TestUnknownAndUndecodable(t *testing.T) {
	s := queue.NewMemoryStore()
	q := queue.New(s, queue.Config{Poll: 5 * time.Millisecond, Tries: 2, Backoff: time.Millisecond}, queue.WithLogger(testLogger(t)))
	ctx := context.Background()
	for _, p := range []string{`{"id":"u","job":"jobs.Gone","data":{}}`, `not json`} {
		if err := s.Push(ctx, queue.Message{ID: "x-" + p[:3], Queue: "default", Payload: []byte(p)}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := queue.Register[Work](q); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(ctx, queue.Message{ID: "bad-data", Queue: "default", Payload: []byte(`{"id":"b","job":"queue_test.Work","data":{"key":5}}`)}, 0); err != nil {
		t.Fatal(err)
	}
	start(t, q)
	eventually(t, "three failed jobs", func() bool { return len(failedJobs(t, s)) == 3 })
	for _, f := range failedJobs(t, s) {
		switch f.ID {
		case "x-{\"i":
			if f.Attempts != 2 || !strings.Contains(f.Error, `unknown job "jobs.Gone"`) {
				t.Errorf("unknown job: %+v", f)
			}
		case "x-not":
			if f.Attempts != 1 || !strings.Contains(f.Error, "decode the job") {
				t.Errorf("bad envelope: %+v", f)
			}
		case "bad-data":
			if f.Attempts != 1 || !strings.Contains(f.Error, "decode queue_test.Work") {
				t.Errorf("bad data: %+v", f)
			}
		default:
			t.Errorf("unexpected failed job %+v", f)
		}
	}
}

func TestSync(t *testing.T) {
	q := newQueue(t, nil)
	ctx := queue.WithQueue(context.WithValue(context.Background(), ctxKey{}, "app"), q)
	k := newKey()
	if err := queue.Dispatch(ctx, Work{Key: k, SucceedAt: 1}, queue.Delay(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c := calls(k); len(c) != 1 || c[0].Attempt != 1 || c[0].Tries != 1 {
		t.Errorf("calls = %+v", c)
	}
	f := newKey()
	err := queue.Dispatch(ctx, Work{Key: f})
	if err == nil || err.Error() != "queue: queue_test.Work: attempt 1 failed" {
		t.Errorf("Dispatch = %v", err)
	}
	if got := failures(f); len(got) != 1 {
		t.Errorf("Failed calls = %v", got)
	}
	v := newKey()
	if err := queue.Dispatch(ctx, Work{Key: v, Mode: "value"}); err != nil {
		t.Errorf("the job didn't get the caller's context: %v", err)
	}
	if q.Store() != nil {
		t.Error("Store() isn't nil")
	}
	if err := q.Run(ctx); err == nil {
		t.Error("Run with the sync driver = nil")
	}
	// A pointer to a registered value type is dispatched as it.
	p := newKey()
	if err := q.Dispatch(ctx, &Work{Key: p, SucceedAt: 1}); err != nil || len(calls(p)) != 1 {
		t.Errorf("Dispatch(&Work) = %v, %d calls", err, len(calls(p)))
	}
	// AfterCommit without a transaction: at once, returning the error.
	a := newKey()
	if err := q.Dispatch(ctx, Work{Key: a, SucceedAt: 1}, queue.AfterCommit()); err != nil || len(calls(a)) != 1 {
		t.Errorf("AfterCommit: %v, %d calls", err, len(calls(a)))
	}
	if err := q.Dispatch(ctx, Work{Key: newKey()}, queue.AfterCommit()); err == nil {
		t.Error("AfterCommit without a transaction lost the job's error")
	}
}

type notStruct int

func (notStruct) Handle(context.Context) error { return nil }

type Other struct{}

func (Other) Handle(context.Context) error { return nil }

func TestRegisterAndDispatchErrors(t *testing.T) {
	q := queue.New(queue.NewMemoryStore(), queue.Config{})
	ctx := queue.WithQueue(context.Background(), q)
	if err := queue.Dispatch(ctx, Work{}); err == nil || !strings.Contains(err.Error(), "isn't registered") {
		t.Errorf("unregistered: %v", err)
	}
	if err := queue.Register[notStruct](q); err == nil {
		t.Error("Register of a non-struct = nil")
	}
	for name, opt := range map[string]queue.JobOption{
		"Tries(0)": queue.Tries(0), "Timeout(0)": queue.Timeout(0), "Backoff()": queue.Backoff(), "Backoff(-1)": queue.Backoff(-1),
		"Name(\"\")": queue.Name(""),
	} {
		if err := queue.Register[Work](q, opt); err == nil {
			t.Errorf("Register with %s = nil", name)
		}
	}
	if err := queue.Register[Work](q, queue.Name("work")); err != nil {
		t.Fatal(err)
	}
	if err := queue.Register[Work](q); err == nil {
		t.Error("Register twice = nil")
	}
	if err := queue.Register[Other](q, queue.Name("work")); err == nil {
		t.Error("two types with one name = nil")
	}
	for _, name := range []string{"", "Emails", "with space", strings.Repeat("a", 101), "-x"} {
		if err := queue.Dispatch(ctx, Work{}, queue.OnQueue(name)); err == nil {
			t.Errorf("OnQueue(%q) = nil", name)
		}
	}
	if err := queue.Dispatch(ctx, nil); err == nil {
		t.Error("Dispatch(nil) = nil")
	}
	if err := queue.Dispatch(context.Background(), Work{}); !errors.Is(err, queue.ErrNoQueue) {
		t.Errorf("no queue in the context: %v", err)
	}
	for _, opt := range []queue.WorkOption{queue.Queues(), queue.Queues("Bad"), queue.Concurrency(0), queue.ShutdownGrace(-1)} {
		if err := q.Run(context.Background(), opt); err == nil {
			t.Error("Run with a bad option = nil")
		}
	}
	if err := q.Work(); err == nil {
		t.Error("Work without ForApp = nil")
	}
	if queue.Permanent(nil) != nil || queue.IsPermanent(errors.New("x")) || !queue.IsPermanent(fmt.Errorf("w: %w", queue.Permanent(io.EOF))) {
		t.Error("Permanent")
	}
	if !errors.Is(queue.Permanent(io.EOF), io.EOF) {
		t.Error("Permanent doesn't unwrap")
	}
}

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey(), "APP_NAME": "qt"}
	maps.Copy(src, env)
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogger(testLogger(t)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestForAppErrors(t *testing.T) {
	for env, want := range map[string]config.Map{
		`QUEUE_DRIVER is "nope"`:            {"QUEUE_DRIVER": "nope"},
		"QUEUE_DEFAULT":                     {"QUEUE_DEFAULT": "No Good"},
		"QUEUE_TRIES":                       {"QUEUE_TRIES": "0"},
		"QUEUE_POLL":                        {"QUEUE_POLL": "0s"},
		"the database driver needs the app": {"QUEUE_DRIVER": "database"},
	} {
		if _, err := queue.ForApp(newApp(t, want)); err == nil || !strings.Contains(err.Error(), env) {
			t.Errorf("%v: ForApp = %v, want %q", want, err, env)
		}
	}
}

func TestForAppWork(t *testing.T) {
	app := newApp(t, config.Map{"QUEUE_DRIVER": "memory", "QUEUE_POLL": "5ms", "QUEUE_BACKOFF": "1ms"})
	app.AddContextValue(ctxKey{}, "app")
	q, err := queue.ForApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if got := anetos.MustResolve[*queue.Queue](app); got != q {
		t.Error("the queue isn't provided")
	}
	if err := queue.Register[Work](q); err != nil {
		t.Fatal(err)
	}
	if err := q.Work(queue.Queues("default", "other"), queue.Concurrency(2)); err != nil {
		t.Fatal(err)
	}
	if err := q.Work(queue.Queues("default", "other")); err == nil {
		t.Error("the same workers twice = nil")
	}
	if got := app.Supervisor().Roles(); len(got) != 1 || got[0] != "workers" {
		t.Errorf("roles = %v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	k := newKey()
	if err := queue.Dispatch(app.Context(context.Background()), Work{Key: k, Mode: "value"}, queue.OnQueue("other")); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the job", func() bool { return len(calls(k)) == 1 })
	eventually(t, "the queue to empty", func() bool { n, _ := q.Store().Size(context.Background(), "other"); return n == 0 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if f := failedJobs(t, q.Store()); len(f) != 0 {
		t.Errorf("failed: %+v", f)
	}
}

func TestSyncDriverWork(t *testing.T) {
	app := newApp(t, nil)
	q, err := queue.ForApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if q.Config().Driver != "sync" || q.Store() != nil {
		t.Errorf("driver = %q", q.Config().Driver)
	}
	if err := q.Work(); err != nil {
		t.Fatal(err)
	}
	if got := app.Supervisor().Roles(); len(got) != 0 {
		t.Errorf("roles = %v, want none", got)
	}
	var out bytes.Buffer
	if code := app.ExecuteArgs(context.Background(), []string{"queue:failed"}, &out, &out); code != 1 || !strings.Contains(out.String(), "sync driver keeps no jobs") {
		t.Errorf("queue:failed = %d: %s", code, out.String())
	}
}

func TestCommands(t *testing.T) {
	for _, env := range []string{"testing", "production"} {
		t.Run(env, func(t *testing.T) {
			app := newApp(t, config.Map{"QUEUE_DRIVER": "memory", "APP_ENV": env})
			q, err := queue.ForApp(app)
			if err != nil {
				t.Fatal(err)
			}
			s := q.Store()
			ctx := context.Background()
			failJob := func(id, payload string) {
				t.Helper()
				if err := s.Push(ctx, queue.Message{ID: id, Queue: "mail", Payload: []byte(payload)}, 0); err != nil {
					t.Fatal(err)
				}
				r, err := s.Reserve(ctx, "mail", time.Minute)
				if err != nil || r == nil {
					t.Fatal(r, err)
				}
				if err := s.Fail(ctx, r, "smtp: 421 try later\nmore"); err != nil {
					t.Fatal(err)
				}
				time.Sleep(2 * time.Millisecond)
			}
			run := func(args ...string) (int, string) {
				var out bytes.Buffer
				for _, c := range app.Commands() {
					if c.Name == args[0] {
						err := c.Run(ctx, &cmd.Args{Name: c.Name, Args: args[1:], Stdout: &out, Stderr: &out})
						switch {
						case err == nil:
							return 0, out.String()
						case errors.Is(err, cmd.ErrUsage):
							return 2, out.String() + err.Error()
						default:
							return 1, out.String() + err.Error()
						}
					}
				}
				t.Fatalf("no command %s", args[0])
				return 0, ""
			}
			if code, out := run("queue:failed"); code != 0 || !strings.Contains(out, "No failed jobs.") {
				t.Errorf("queue:failed = %d: %s", code, out)
			}
			failJob("id-1", `{"id":"id-1","job":"jobs.SendWelcome","data":{}}`)
			failJob("id-2", `{"id":"id-2","job":"jobs.Report","data":{}}`)
			failJob("id-3", `{"id":"id-3","job":"jobs.Report","data":{}}`)
			code, out := run("queue:failed")
			if code != 0 || !strings.Contains(out, "id-1") || !strings.Contains(out, "jobs.SendWelcome") || !strings.Contains(out, "smtp: 421 try later") ||
				strings.Contains(out, "more") || strings.Index(out, "id-3") > strings.Index(out, "id-1") {
				t.Errorf("queue:failed = %d:\n%s", code, out)
			}
			if code, out := run("queue:failed", "--limit=1"); code != 0 || strings.Contains(out, "id-1") || !strings.Contains(out, "id-3") {
				t.Errorf("queue:failed --limit=1 = %d:\n%s", code, out)
			}
			if code, _ := run("queue:failed", "--limit=0"); code != 2 {
				t.Errorf("queue:failed --limit=0 = %d", code)
			}
			if code, out := run("queue:retry", "id-1", "nope"); code != 1 || !strings.Contains(out, "Put job id-1 back") || !strings.Contains(out, "no failed job with the ID nope") {
				t.Errorf("queue:retry = %d: %s", code, out)
			}
			if n, _ := s.Size(ctx, "mail"); n != 1 {
				t.Errorf("after retry, size = %d", n)
			}
			if code, out := run("queue:forget", "id-2"); code != 0 || !strings.Contains(out, "Deleted failed job id-2") {
				t.Errorf("queue:forget = %d: %s", code, out)
			}
			if code, _ := run("queue:forget", "id-2"); code != 1 {
				t.Errorf("queue:forget twice = %d", code)
			}
			if code, _ := run("queue:retry"); code != 2 {
				t.Errorf("queue:retry with no IDs = %d", code)
			}
			failJob("id-4", `{}`)
			if code, out := run("queue:retry", "all"); code != 0 || !strings.Contains(out, "Put 2 failed job(s)") {
				t.Errorf("queue:retry all = %d: %s", code, out)
			}
			failJob("id-5", `{}`)
			force := []string{}
			if env == "production" {
				if code, out := run("queue:flush"); code != 1 || !strings.Contains(out, "--force") {
					t.Errorf("queue:flush in production = %d: %s", code, out)
				}
				if code, out := run("queue:clear", "mail"); code != 1 || !strings.Contains(out, "--force") {
					t.Errorf("queue:clear in production = %d: %s", code, out)
				}
				force = []string{"--force"}
			}
			if code, out := run(append([]string{"queue:flush"}, force...)...); code != 0 || !strings.Contains(out, "Deleted 1 failed job(s)") {
				t.Errorf("queue:flush = %d: %s", code, out)
			}
			// id-1, id-3 and id-4 are back on the mail queue.
			if code, out := run(append(append([]string{"queue:clear"}, force...), "mail")...); code != 0 || !strings.Contains(out, "Deleted 3 job(s) from the mail queue") {
				t.Errorf("queue:clear = %d: %s", code, out)
			}
			if code, _ := run("queue:clear", "Bad Name"); code != 2 {
				t.Errorf("queue:clear with a bad name = %d", code)
			}
		})
	}
}

// Report is a function job's payload.
type Report struct {
	Key   string `json:"key"`
	Month string `json:"month"`
}

func TestRegisterFunc(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s, queue.Name("work"))
	var got atomic.Pointer[Report]
	err := queue.RegisterFunc(q, "reports.send", func(ctx context.Context, r Report) error {
		info, _ := queue.Current(ctx)
		if info.Job != "reports.send" || info.Tries != 2 {
			return queue.Permanent(fmt.Errorf("Info = %+v", info))
		}
		got.Store(&r)
		return nil
	}, queue.Tries(2))
	if err != nil {
		t.Fatal(err)
	}
	ctx := queue.WithQueue(context.Background(), q)
	if err := queue.DispatchFunc(ctx, "reports.send", Report{Key: "a", Month: "2026-09"}); err != nil {
		t.Fatal(err)
	}
	if err := queue.DispatchFunc(ctx, "reports.send", &Report{Key: "b"}); err != nil {
		t.Errorf("a pointer payload: %v", err)
	}
	for name, err := range map[string]error{
		"wrong type":  queue.DispatchFunc(ctx, "reports.send", "2026-09"),
		"nil":         queue.DispatchFunc(ctx, "reports.send", nil),
		"unknown":     queue.DispatchFunc(ctx, "reports.nope", Report{}),
		"struct job":  queue.DispatchFunc(ctx, "work", Work{}),
		"taken name":  queue.RegisterFunc(q, "work", func(context.Context, Report) error { return nil }),
		"taken twice": queue.RegisterFunc(q, "reports.send", func(context.Context, Report) error { return nil }),
		"nil func":    queue.RegisterFunc[Report](q, "reports.nil", nil),
		"empty name":  queue.RegisterFunc(q, "", func(context.Context, Report) error { return nil }),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	start(t, q)
	eventually(t, "the jobs", func() bool { n, _ := s.Size(context.Background(), "default"); return n == 0 })
	if r := got.Load(); r == nil || r.Key == "" {
		t.Errorf("payload = %+v", r)
	}
	if f := failedJobs(t, s); len(f) != 0 {
		t.Errorf("failed: %+v", f)
	}
	// With the sync driver too.
	sq := queue.New(nil, queue.Config{})
	var ran atomic.Bool
	if err := queue.RegisterFunc(sq, "f", func(_ context.Context, r Report) error { ran.Store(r.Month == "x"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := sq.DispatchFunc(context.Background(), "f", Report{Month: "x"}); err != nil || !ran.Load() {
		t.Errorf("sync: %v, ran %v", err, ran.Load())
	}
}

func TestFuncJobDecodeFailure(t *testing.T) {
	s := queue.NewMemoryStore()
	q := newQueue(t, s)
	if err := queue.RegisterFunc(q, "reports.bad", func(context.Context, Report) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.Push(context.Background(), queue.Message{ID: "bad", Queue: "default", Payload: []byte(`{"id":"bad","job":"reports.bad","data":{"month":5}}`)}, 0); err != nil {
		t.Fatal(err)
	}
	start(t, q)
	eventually(t, "the failed job", func() bool { return len(failedJobs(t, s)) == 1 })
	if f := failedJobs(t, s)[0]; f.Attempts != 1 || !strings.Contains(f.Error, "decode reports.bad") {
		t.Errorf("failed job = %+v", f)
	}
}
