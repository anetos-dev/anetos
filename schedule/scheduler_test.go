// SPDX-License-Identifier: Apache-2.0

package schedule_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/schedule"
)

// fakeClock is a clock the test moves.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
	calls   int // After calls
}

type waiter struct {
	at time.Time
	ch chan time.Time
}

func newClock(t time.Time) *fakeClock { return &fakeClock{now: t} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, waiter{c.now.Add(d), ch})
	return ch
}

// advance moves the clock, once the scheduler waits on it.
func (c *fakeClock) advance(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		n := len(c.waiters)
		c.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the scheduler never waited on the clock")
		}
		time.Sleep(time.Millisecond)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	kept := c.waiters[:0]
	for _, w := range c.waiters {
		if !w.at.After(c.now) {
			w.ch <- c.now
		} else {
			kept = append(kept, w)
		}
	}
	c.waiters = kept
}

// afterCalls returns how many times the scheduler waited on the clock.
func (c *fakeClock) afterCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

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

// start runs s until the test ends; stop stops it and waits.
func start(t *testing.T, s *schedule.Scheduler, ctx context.Context) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
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

var t0 = time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC)

func newScheduler(t *testing.T, opts ...schedule.Option) (*schedule.Scheduler, *fakeClock, *logBuffer) {
	t.Helper()
	logs := &logBuffer{}
	opts = append([]schedule.Option{schedule.WithLogger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})))}, opts...)
	s := schedule.NewScheduler(opts...)
	c := newClock(t0)
	schedule.SetClock(s, c)
	return s, c, logs
}

func TestRunsOnSchedule(t *testing.T) {
	s, c, logs := newScheduler(t)
	var mu sync.Mutex
	var ran []string
	record := func(name string) func(context.Context) error {
		return func(ctx context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			ran = append(ran, name)
			if name == "failing" {
				return errors.New("upstream down")
			}
			return nil
		}
	}
	check(t, s.Add(schedule.EveryMinute(), "minutely", record("minutely")))
	check(t, s.Add(schedule.Every(5*time.Minute), "five", record("five")))
	check(t, s.Add(schedule.Every(2*time.Minute), "failing", record("failing")))
	check(t, s.Add(schedule.EveryMinute(), "panicky", func(context.Context) error { panic("kaboom") }))
	count := func(name string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, r := range ran {
			if r == name {
				n++
			}
		}
		return n
	}
	start(t, s, context.Background())
	for i := range 5 { // 00:01 … 00:05
		c.advance(t, time.Minute)
		eventually(t, "the minutely run", func() bool { return count("minutely") == i+1 })
	}
	eventually(t, "the others", func() bool { return count("five") == 1 && count("failing") == 2 })
	if !strings.Contains(logs.String(), "upstream down") || !strings.Contains(logs.String(), "kaboom") {
		t.Errorf("logs:\n%s", logs.String())
	}
	// A late wake-up runs a missed run once, not every one missed.
	c.advance(t, 10*time.Minute) // 00:15
	eventually(t, "the late run", func() bool { return count("minutely") == 6 })
	time.Sleep(20 * time.Millisecond)
	if n := count("minutely"); n != 6 {
		t.Errorf("after a 10-minute gap, %d runs, want 6", n)
	}
	// A task added while running is scheduled at once.
	calls := c.afterCalls()
	check(t, s.Add(schedule.EveryMinute(), "late", record("late")))
	eventually(t, "the scheduler to wake", func() bool { return c.afterCalls() > calls })
	c.advance(t, time.Minute)
	eventually(t, "the late task", func() bool { return count("late") == 1 })
}

func TestTaskOptions(t *testing.T) {
	s, c, logs := newScheduler(t)
	ctx := cache.WithCache(context.Background(), cache.NewWithStore(cache.NewMemoryStore(), "t:"))
	release := make(chan struct{})
	var started, timedOut atomic.Int32
	check(t, s.Add(schedule.EveryMinute(), "slow", func(ctx context.Context) error {
		started.Add(1)
		<-release
		return nil
	}, schedule.WithoutOverlapping()))
	check(t, s.Add(schedule.EveryMinute(), "bounded", func(ctx context.Context) error {
		<-ctx.Done()
		timedOut.Add(1)
		return ctx.Err()
	}, schedule.Timeout(10*time.Millisecond)))
	start(t, s, ctx)
	c.advance(t, time.Minute)
	eventually(t, "the first run", func() bool { return started.Load() == 1 })
	c.advance(t, time.Minute) // the first run still goes: skipped
	eventually(t, "the skip", func() bool { return strings.Contains(logs.String(), "previous run is still going") })
	if started.Load() != 1 {
		t.Errorf("%d runs started, want 1", started.Load())
	}
	if err := s.RunTask(ctx, "slow"); !errors.Is(err, schedule.ErrOverlap) {
		t.Errorf("RunTask while running = %v", err)
	}
	close(release)
	eventually(t, "the timeouts", func() bool { return timedOut.Load() == 2 })
	if !strings.Contains(logs.String(), "deadline exceeded") {
		t.Errorf("logs:\n%s", logs.String())
	}
	// The lock is released: the next run starts.
	time.Sleep(10 * time.Millisecond)
	c.advance(t, time.Minute)
	eventually(t, "the next run", func() bool { return started.Load() == 2 })
}

func TestOnOneServer(t *testing.T) {
	shared := cache.NewWithStore(cache.NewMemoryStore(), "t:")
	ctx := cache.WithCache(context.Background(), shared)
	var runs atomic.Int32
	var clocks []*fakeClock
	for range 3 { // three instances sharing a cache
		s, c, _ := newScheduler(t)
		check(t, s.Add(schedule.EveryMinute(), "report", func(context.Context) error { runs.Add(1); return nil }, schedule.OnOneServer()))
		clocks = append(clocks, c)
		start(t, s, ctx)
	}
	for i := range 3 {
		for _, c := range clocks {
			c.advance(t, time.Minute)
		}
		eventually(t, "one run per minute", func() bool { return runs.Load() == int32(i+1) })
	}
	time.Sleep(20 * time.Millisecond)
	if n := runs.Load(); n != 3 {
		t.Errorf("%d runs in 3 minutes on 3 instances, want 3", n)
	}
}

func TestShutdownGrace(t *testing.T) {
	s, c, logs := newScheduler(t, schedule.WithShutdownGrace(200*time.Millisecond))
	var canceled atomic.Bool
	started := make(chan struct{})
	check(t, s.Add(schedule.EveryMinute(), "long", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		canceled.Store(true)
		return nil
	}))
	stop := start(t, s, context.Background())
	c.advance(t, time.Minute)
	<-started
	began := time.Now()
	stop()
	if took := time.Since(began); !canceled.Load() || took < 200*time.Millisecond || took > 5*time.Second {
		t.Errorf("stopped after %s, canceled %v", took, canceled.Load())
	}
	if !strings.Contains(logs.String(), "stopping the runs still going") {
		t.Errorf("logs:\n%s", logs.String())
	}

	// A run that ends within the grace period isn't canceled.
	s, c, _ = newScheduler(t, schedule.WithShutdownGrace(5*time.Second))
	var finished atomic.Bool
	started = make(chan struct{})
	check(t, s.Add(schedule.EveryMinute(), "short", func(ctx context.Context) error {
		close(started)
		time.Sleep(50 * time.Millisecond)
		finished.Store(ctx.Err() == nil)
		return nil
	}))
	stop = start(t, s, context.Background())
	c.advance(t, time.Minute)
	<-started
	stop()
	if !finished.Load() {
		t.Error("a short run was canceled at shutdown")
	}
}

func TestAddErrors(t *testing.T) {
	s, _, _ := newScheduler(t)
	fn := func(context.Context) error { return nil }
	for name, err := range map[string]error{
		"bad name":     s.Add(schedule.Daily(), "Bad Name", fn),
		"bad schedule": s.Add(schedule.Cron("nope"), "x", fn),
		"never":        s.Add(schedule.Cron("0 0 30 2 *"), "x", fn),
		"nil":          s.Add(schedule.Daily(), "x", nil),
		"Timeout(0)":   s.Add(schedule.Daily(), "x", fn, schedule.Timeout(0)),
	} {
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	check(t, s.Add(schedule.Daily(), "x", fn))
	if err := s.Add(schedule.Daily(), "x", fn); err == nil {
		t.Error("two tasks named x = nil")
	}
	if err := s.RunTask(context.Background(), "nope"); err == nil {
		t.Error("RunTask of an unknown task = nil")
	}
	// Locks need a cache in Run's context.
	l, _, _ := newScheduler(t)
	check(t, l.Add(schedule.Daily(), "locked", fn, schedule.OnOneServer()))
	if err := l.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "needs a cache") {
		t.Errorf("Run without a cache = %v", err)
	}
}

type report struct{}

var reports atomic.Int32

func (report) Handle(context.Context) error { reports.Add(1); return nil }

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	src := config.Map{"APP_ENV": "testing", "APP_KEY": encryption.GenerateKey()}
	maps.Copy(src, env)
	app, err := anetos.New(anetos.WithSource(src), anetos.WithLogOutput(io.Discard))
	check(t, err)
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestAppNew(t *testing.T) {
	if _, err := schedule.New(newApp(t, config.Map{"SCHEDULE_TIMEZONE": "Mars/Olympus"})); err == nil {
		t.Error("a bad SCHEDULE_TIMEZONE = nil")
	}
	// No tasks: no component.
	empty := newApp(t, nil)
	_, err := schedule.New(empty)
	check(t, err)
	check(t, empty.Boot(context.Background()))
	if len(empty.Supervisor().Status()) != 0 {
		t.Error("a scheduler without tasks added a component")
	}
	// Locks need cache.New.
	nocache := newApp(t, nil)
	s, err := schedule.New(nocache)
	check(t, err)
	// The process type is known before the first task: run --only=scheduler works.
	if got := nocache.Supervisor().ProcessTypes(); len(got) != 1 || got[0] != "scheduler" {
		t.Errorf("process types without tasks = %v", got)
	}
	check(t, s.Add(schedule.Daily(), "x", func(context.Context) error { return nil }, schedule.WithoutOverlapping()))
	if err := nocache.Boot(context.Background()); err == nil || !strings.Contains(err.Error(), "cache.New") {
		t.Errorf("Boot = %v", err)
	}

	app := newApp(t, config.Map{"SCHEDULE_TIMEZONE": "Asia/Dhaka", "QUEUE_DRIVER": "sync"})
	_, err = cache.New(app)
	check(t, err)
	q, err := queue.New(app)
	check(t, err)
	check(t, queue.Register[report](q))
	s, err = schedule.New(app)
	check(t, err)
	if _, err := schedule.New(app); err == nil {
		t.Error("New twice = nil")
	}
	check(t, s.Add(schedule.DailyAt("02:00"), "daily-report", schedule.Dispatch(report{}), schedule.OnOneServer(), schedule.Timeout(time.Minute)))
	check(t, s.Add(schedule.Hourly().In("UTC"), "hourly", func(context.Context) error { return nil }, schedule.WithoutOverlapping()))
	check(t, app.Boot(context.Background()))
	if got := app.Supervisor().ProcessTypes(); len(got) != 1 || got[0] != "scheduler" {
		t.Errorf("process types = %v", got)
	}
	tasks := s.Tasks()
	if len(tasks) != 2 || tasks[0].Name != "daily-report" || !tasks[0].OnOneServer || tasks[0].Timeout != time.Minute {
		t.Errorf("Tasks = %+v", tasks)
	}
	// SCHEDULE_TIMEZONE applies to schedules without In.
	if next := tasks[0].Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); next.Format("15:04 MST") != "02:00 +06" || !next.Equal(time.Date(2025, 12, 31, 20, 0, 0, 0, time.UTC).AddDate(0, 0, 1)) {
		t.Errorf("Next = %s", next)
	}

	// Without SCHEDULE_TIMEZONE, schedules are in the app's zone.
	zoned := newApp(t, config.Map{"APP_TIMEZONE": "Asia/Dhaka"})
	zs, err := schedule.New(zoned)
	check(t, err)
	check(t, zs.Add(schedule.DailyAt("02:00"), "nightly", func(context.Context) error { return nil }))
	if next := zs.Tasks()[0].Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); next.Format("15:04 MST") != "02:00 +06" {
		t.Errorf("Next in the app's zone = %s", next)
	}

	run := func(name string, args ...string) (string, error) {
		var out bytes.Buffer
		for _, c := range app.Commands() {
			if c.Name == name {
				err := c.Run(context.Background(), &cmd.Args{Name: name, Args: args, Stdout: &out, Stderr: &out})
				return out.String(), err
			}
		}
		t.Fatalf("no command %s", name)
		return "", nil
	}
	out, err := run("schedule:list")
	check(t, err)
	for _, want := range []string{"daily-report", "0 2 * * *", "on one server, timeout 1m", "hourly", "0 * * * * (UTC)", "without overlapping"} {
		if !strings.Contains(out, want) {
			t.Errorf("schedule:list lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "m0s") {
		t.Errorf("schedule:list shows seconds:\n%s", out)
	}
	var (
		unitsMu sync.Mutex // schedule:run runs due tasks side by side
		units   []string
	)
	app.AroundOperations(func(ctx context.Context, u anetos.Operation) (context.Context, func()) {
		unitsMu.Lock()
		defer unitsMu.Unlock()
		units = append(units, u.Kind+" "+u.Name)
		return ctx, nil
	})
	before := reports.Load()
	out, err = run("schedule:test", "daily-report")
	unitsMu.Lock()
	first := ""
	if len(units) > 0 {
		first = units[0]
	}
	unitsMu.Unlock()
	if first != "task daily-report" { // then its job (sync driver)
		t.Errorf("first unit %q", first)
	}
	if err != nil || !strings.HasPrefix(out, "Ran daily-report in") || reports.Load() != before+1 {
		t.Errorf("schedule:test = %q, %v; %d reports", out, err, reports.Load()-before)
	}
	if _, err := run("schedule:test"); !errors.Is(err, cmd.ErrUsage) {
		t.Errorf("schedule:test without a task = %v", err)
	}
	if _, err := run("schedule:test", "nope"); err == nil {
		t.Error("schedule:test nope = nil")
	}
	// v0.4's schedule:run <task> runs the task, with a warning.
	if out, err := run("schedule:run", "daily-report"); err != nil || !strings.Contains(out, "schedule:test daily-report") || !strings.Contains(out, "Ran daily-report in") || reports.Load() != before+2 {
		t.Errorf("schedule:run daily-report = %q, %v", out, err)
	}
	// Without a task, schedule:run runs those due this minute: 02:00 in
	// Dhaka is 20:00 UTC, when the hourly task runs too.
	clock := newClock(time.Date(2026, 1, 1, 20, 0, 40, 0, time.UTC))
	schedule.SetClock(s, clock)
	if out, err := run("schedule:run"); err != nil || !strings.HasPrefix(out, "Ran daily-report, hourly in") || reports.Load() != before+3 {
		t.Errorf("schedule:run at 20:00 UTC = %q, %v; %d reports", out, err, reports.Load()-before)
	}
	clock.now = clock.now.Add(time.Minute)
	if out, err := run("schedule:run"); err != nil || out != "No tasks are due.\n" {
		t.Errorf("schedule:run at 20:01 UTC = %q, %v", out, err)
	}
	if _, err := run("schedule:run", "a", "b"); !errors.Is(err, cmd.ErrUsage) {
		t.Errorf("schedule:run a b = %v", err)
	}
	if _, err := run("schedule:list", "x"); !errors.Is(err, cmd.ErrUsage) {
		t.Errorf("schedule:list x = %v", err)
	}
}

func TestAppRun(t *testing.T) {
	app := newApp(t, nil)
	_, err := cache.New(app)
	check(t, err)
	s, err := schedule.New(app)
	check(t, err)
	c := newClock(t0)
	schedule.SetClock(s, c)
	type key struct{}
	app.AddContextValue(key{}, "app value")
	got := make(chan string, 1)
	check(t, s.Add(schedule.EveryMinute(), "tick", func(ctx context.Context) error {
		v, _ := ctx.Value(key{}).(string)
		select {
		case got <- v:
		default:
		}
		return nil
	}, schedule.WithoutOverlapping(), schedule.OnOneServer()))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx, "scheduler") }()
	c.advance(t, time.Minute)
	select {
	case v := <-got:
		if v != "app value" {
			t.Errorf("the task's context has %q, want the app's values", v)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the task didn't run")
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run = %v", err)
	}
}

// The overlap lock is extended while the run goes, past its lease, and
// holds for another scheduler sharing the cache; when the run ends, it is
// released.
func TestOverlapLease(t *testing.T) {
	schedule.SetOverlapLease(t, 60*time.Millisecond)
	ctx := cache.WithCache(context.Background(), cache.NewWithStore(cache.NewMemoryStore(), "t:"))
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	slow := func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	}
	a, _, _ := newScheduler(t)
	b, _, _ := newScheduler(t)
	for _, s := range []*schedule.Scheduler{a, b} {
		check(t, s.Add(schedule.Hourly(), "slow", slow, schedule.WithoutOverlapping()))
	}
	done := make(chan error, 1)
	go func() { done <- a.RunTask(ctx, "slow") }()
	<-started
	time.Sleep(200 * time.Millisecond) // three leases
	if err := b.RunTask(ctx, "slow"); !errors.Is(err, schedule.ErrOverlap) {
		t.Errorf("RunTask on another scheduler during the run = %v", err)
	}
	close(release)
	check(t, <-done)
	go func() { <-started }()
	if err := b.RunTask(ctx, "slow"); err != nil {
		t.Errorf("RunTask after the run = %v", err)
	}
}

func TestAddAfterBoot(t *testing.T) {
	fn := func(context.Context) error { return nil }
	// Locks need cache.New, for tasks added after boot too.
	app := newApp(t, nil)
	check(t, app.Boot(context.Background()))
	s, err := schedule.New(app)
	check(t, err)
	if err := s.Add(schedule.Daily(), "locked", fn, schedule.OnOneServer()); err == nil || !strings.Contains(err.Error(), "cache.New") {
		t.Errorf("Add with a lock, without a cache, after boot = %v", err)
	}
	check(t, s.Add(schedule.Daily(), "free", fn))
	if st := app.Supervisor().Status(); len(st) != 1 || st[0].Name != "scheduler" {
		t.Errorf("components = %+v", st)
	}

	// A task the scheduler couldn't be added for isn't kept.
	clash := newApp(t, nil)
	check(t, clash.Go("scheduler", func(ctx context.Context) error { <-ctx.Done(); return nil }))
	check(t, clash.Boot(context.Background()))
	s, err = schedule.New(clash)
	check(t, err)
	if err := s.Add(schedule.Daily(), "a", fn); err == nil {
		t.Error("Add with the component's name taken = nil")
	}
	if n := len(s.Tasks()); n != 0 {
		t.Errorf("%d tasks after a failed Add", n)
	}
}

func TestNilLocation(t *testing.T) {
	s := schedule.NewScheduler(schedule.WithLocation(nil))
	check(t, s.Add(schedule.Daily(), "x", func(context.Context) error { return nil }))
	if next := (schedule.TaskInfo{Schedule: schedule.Daily()}).Next(t0); !next.Equal(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Next = %s", next)
	}
}

// Each run is kept in the cache, the last one per task.
func TestLastRun(t *testing.T) {
	ctx := cache.WithCache(context.Background(), cache.NewWithStore(cache.NewMemoryStore(), "t:"))
	s, _, _ := newScheduler(t)
	fail := errors.New("upstream down")
	var calls atomic.Int32
	check(t, s.Add(schedule.Hourly(), "sync", func(context.Context) error {
		if calls.Add(1) == 1 {
			return fail
		}
		return nil
	}))
	if _, ok, err := s.LastRun(ctx, "sync"); ok || err != nil {
		t.Fatalf("LastRun before a run: %v, %v", ok, err)
	}
	if err := s.RunTask(ctx, "sync"); !errors.Is(err, fail) {
		t.Fatal(err)
	}
	run, ok, err := s.LastRun(ctx, "sync")
	if !ok || err != nil || run.Error != "upstream down" || run.At.IsZero() {
		t.Errorf("after a failure: %+v, %v, %v", run, ok, err)
	}
	check(t, s.RunTask(ctx, "sync"))
	if run, _, _ := s.LastRun(ctx, "sync"); run.Error != "" {
		t.Errorf("after a success: %+v", run)
	}
	// Without a cache, runs aren't kept, and nothing fails.
	check(t, s.RunTask(context.Background(), "sync"))
}

// A negative grace is none.
func TestNegativeShutdownGrace(t *testing.T) {
	if g := schedule.Grace(schedule.NewScheduler(schedule.WithShutdownGrace(-time.Second))); g != 0 {
		t.Errorf("grace = %v, want 0", g)
	}
}
