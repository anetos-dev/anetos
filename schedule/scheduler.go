// SPDX-License-Identifier: Apache-2.0

package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/queue"
	"anetos.dev/anetos/supervisor"
)

// Config configures the app's scheduler.
type Config struct {
	// Timezone is the time zone of schedules without [Schedule.In]: an
	// IANA name. SCHEDULE_TIMEZONE, default the app's (APP_TIMEZONE,
	// itself UTC by default).
	Timezone string `env:"SCHEDULE_TIMEZONE"`
}

// LoadConfig reads the SCHEDULE_* settings.
func LoadConfig(src config.Source) (Config, error) {
	return config.Get[Config](src)
}

// clock is time, replaceable in tests.
type clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Scheduler runs tasks on schedules. Create it with [New] (or [NewScheduler])
// and add tasks with [Scheduler.Add].
type Scheduler struct {
	app   *anetos.App
	log   *slog.Logger
	loc   *time.Location
	clock clock
	grace time.Duration

	mu        sync.Mutex
	tasks     []*task
	checked   bool          // check ran: the app's boot reached the scheduler
	component bool          // added to the app
	wake      chan struct{} // a task was added
}

// task is a scheduled task.
type task struct {
	name      string
	when      Schedule
	fn        func(ctx context.Context) error
	overlap   bool // WithoutOverlapping
	oneServer bool
	timeout   time.Duration
}

// Option configures a [Scheduler] made with [NewScheduler].
type Option func(*Scheduler)

// WithLogger sets the scheduler's logger. Default slog.Default().
func WithLogger(l *slog.Logger) Option { return func(s *Scheduler) { s.log = l } }

// WithLocation sets the time zone of schedules without [Schedule.In].
// Default UTC.
func WithLocation(loc *time.Location) Option {
	return func(s *Scheduler) {
		if loc != nil {
			s.loc = loc
		}
	}
}

// WithShutdownGrace sets how long Run lets runs still going finish once
// its context is canceled, before canceling theirs. Default 15 seconds;
// with [New], half of APP_SHUTDOWN_TIMEOUT.
func WithShutdownGrace(d time.Duration) Option { return func(s *Scheduler) { s.grace = d } }

// NewScheduler returns a scheduler without an app: run it with [Scheduler.Run].
// Tasks with [WithoutOverlapping] or [OnOneServer] need a cache in Run's
// context (cache.WithCache).
func NewScheduler(opts ...Option) *Scheduler {
	s := &Scheduler{log: slog.Default(), loc: time.UTC, clock: realClock{}, grace: 15 * time.Second, wake: make(chan struct{}, 1)}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// New returns the app's scheduler, configured from SCHEDULE_TIMEZONE
// (default the app's zone, APP_TIMEZONE):
// it runs as a component with the role "scheduler" (so `run
// --only=scheduler` runs only it), stopping first after the HTTP server,
// and adds the schedule:list and schedule:run commands. Tasks with
// [WithoutOverlapping] or [OnOneServer] need cache.New, with a store
// every instance shares (database or Redis) for OnOneServer.
//
//	s, err := schedule.New(app)
//	err = s.Add(schedule.DailyAt("02:00"), "prune-sessions", tasks.PruneSessions, schedule.OnOneServer())
func New(app *anetos.App) (*Scheduler, error) {
	cfg, err := LoadConfig(app.Source())
	if err != nil {
		return nil, err
	}
	loc := app.Location()
	if cfg.Timezone != "" {
		if loc, err = time.LoadLocation(cfg.Timezone); err != nil {
			return nil, fmt.Errorf("schedule: SCHEDULE_TIMEZONE: %w", err)
		}
	}
	if _, err := anetos.Resolve[*Scheduler](app); err == nil {
		return nil, errors.New("schedule: New called twice for one app")
	}
	s := NewScheduler(WithLogger(app.Logger().With("component", "scheduler")), WithLocation(loc))
	s.app = app
	// run --only=scheduler works before the first task (the component
	// comes with it).
	app.Supervisor().Declare("scheduler")
	for _, c := range s.commands() {
		if err := app.AddCommand(c); err != nil {
			return nil, err
		}
	}
	anetos.Provide(app, s)
	if app.Booted() {
		return s, s.check(app)
	}
	app.Use(booter{s})
	return s, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp(app *anetos.App) (*Scheduler, error) {
	return New(app)
}

// booter checks the tasks and adds the scheduler's component when the app
// boots.
type booter struct{ s *Scheduler }

func (booter) Name() string               { return "schedule" }
func (booter) Register(*anetos.App) error { return nil }
func (b booter) Boot(_ context.Context, app *anetos.App) error {
	return b.s.check(app)
}

// check verifies that tasks needing locks have a cache, and adds the
// component if there are tasks.
func (s *Scheduler) check(app *anetos.App) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checked = true
	if _, err := anetos.Resolve[*cache.Cache](app); err != nil {
		for _, t := range s.tasks {
			if t.overlap || t.oneServer {
				return fmt.Errorf("schedule: the task %s uses a lock (WithoutOverlapping, OnOneServer): call cache.New", t.name)
			}
		}
	}
	return s.addComponent()
}

// addComponent adds the scheduler to the app once it has tasks; s.mu is
// held.
func (s *Scheduler) addComponent() error {
	if s.app == nil || s.component || len(s.tasks) == 0 {
		return nil
	}
	if err := s.app.Component(s, anetos.Roles("scheduler"), anetos.Stage(supervisor.StageScheduler), anetos.Restart(supervisor.RestartOnFailure)); err != nil {
		return err
	}
	s.component = true
	return nil
}

// TaskOption configures a task.
type TaskOption func(*task) error

// WithoutOverlapping skips a run while the previous one is still going,
// on any instance sharing the cache: the run holds a cache lock, extended
// while it goes, so a run whose process died holds it for a few minutes
// at most.
func WithoutOverlapping() TaskOption {
	return func(t *task) error { t.overlap = true; return nil }
}

// OnOneServer runs each run of the task on one instance only, when
// several run the scheduler: the first to take the run's cache lock runs
// it. It needs a cache store the instances share (database or Redis).
func OnOneServer() TaskOption {
	return func(t *task) error { t.oneServer = true; return nil }
}

// Timeout bounds a run: then its context is canceled.
func Timeout(d time.Duration) TaskOption {
	return func(t *task) error {
		if d <= 0 {
			return fmt.Errorf("schedule: Timeout(%s) must be positive", d)
		}
		t.timeout = d
		return nil
	}
}

var taskName = regexp.MustCompile(`^[a-z0-9][a-z0-9._:-]{0,99}$`)

// Add adds a task: fn runs on schedule when, with a context that has the
// app's values, canceled at shutdown (after a grace period) or after
// [Timeout]. name identifies it in logs, locks and commands: lower-case
// letters, digits and . _ : -.
//
//	err := s.Add(schedule.Every(5*time.Minute), "sync-inventory", inventory.Sync)
//
// Runs that should have happened while no scheduler ran are skipped, as
// with cron. A run that fails is logged; it isn't retried (dispatch a
// queue job, with [Dispatch], for retries).
func (s *Scheduler) Add(when Schedule, name string, fn func(ctx context.Context) error, opts ...TaskOption) error {
	if !taskName.MatchString(name) {
		return fmt.Errorf("schedule: invalid task name %q: use up to 100 lower-case letters, digits and . _ : -", name)
	}
	if when.err != nil {
		return fmt.Errorf("schedule: task %s: %w", name, when.err)
	}
	if fn == nil {
		return fmt.Errorf("schedule: task %s has no function", name)
	}
	if when.next(time.Now(), s.loc).IsZero() {
		return fmt.Errorf("schedule: task %s: %s never runs", name, when)
	}
	t := &task{name: name, when: when, fn: fn}
	for _, opt := range opts {
		if err := opt(t); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.ContainsFunc(s.tasks, func(o *task) bool { return o.name == name }) {
		return fmt.Errorf("schedule: two tasks are named %s", name)
	}
	if s.checked && (t.overlap || t.oneServer) { // added after the boot check
		if _, err := anetos.Resolve[*cache.Cache](s.app); err != nil {
			return fmt.Errorf("schedule: the task %s uses a lock (WithoutOverlapping, OnOneServer): call cache.New", t.name)
		}
	}
	s.tasks = append(s.tasks, t)
	if s.checked {
		if err := s.addComponent(); err != nil {
			s.tasks = s.tasks[:len(s.tasks)-1]
			return err
		}
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return nil
}

// Dispatch returns a task function that dispatches job to the queue, for
// work that needs the queue's retries:
//
//	err := s.Add(schedule.DailyAt("06:00"), "daily-report", schedule.Dispatch(jobs.DailyReport{}))
func Dispatch(job queue.Job, opts ...queue.DispatchOption) func(ctx context.Context) error {
	return func(ctx context.Context) error { return queue.Dispatch(ctx, job, opts...) }
}

// TaskInfo describes a task.
type TaskInfo struct {
	// Name is the task's name.
	Name string
	// Schedule is when it runs.
	Schedule Schedule
	// WithoutOverlapping and OnOneServer are its options.
	WithoutOverlapping, OnOneServer bool
	// Timeout bounds each run; 0 for none.
	Timeout time.Duration

	loc *time.Location
}

// Next returns the first time after t the task runs, in its schedule's
// time zone.
func (i TaskInfo) Next(t time.Time) time.Time { return i.Schedule.next(t, i.loc) }

// Tasks returns the tasks, in the order they were added.
func (s *Scheduler) Tasks() []TaskInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TaskInfo, len(s.tasks))
	for i, t := range s.tasks {
		out[i] = TaskInfo{Name: t.name, Schedule: t.when, WithoutOverlapping: t.overlap, OnOneServer: t.oneServer, Timeout: t.timeout, loc: s.loc}
	}
	return out
}

// ErrOverlap is returned by [Scheduler.RunTask] when a task with
// [WithoutOverlapping] is already running.
var ErrOverlap = errors.New("schedule: the task is already running")

// RunTask runs the task name now, whatever its schedule, and returns its
// error: for the schedule:run command and tests. WithoutOverlapping
// applies (across processes only with a shared cache store); OnOneServer
// doesn't.
func (s *Scheduler) RunTask(ctx context.Context, name string) error {
	s.mu.Lock()
	i := slices.IndexFunc(s.tasks, func(t *task) bool { return t.name == name })
	var t *task
	if i >= 0 {
		t = s.tasks[i]
	}
	s.mu.Unlock()
	if t == nil {
		return fmt.Errorf("schedule: no task named %s", name)
	}
	if s.app != nil {
		ctx = s.app.Context(ctx)
	}
	start := time.Now()
	err := s.runTask(ctx, t)
	s.recordRun(ctx, t.name, start, err)
	if p := (*panicError)(nil); errors.As(err, &p) {
		s.log.ErrorContext(ctx, "schedule: task panicked", "task", t.name, "panic", p.value, "stack", string(p.stack))
	}
	return err
}

// runTask runs t once: with its overlap lock and timeout, recovering a
// panic.
func (s *Scheduler) runTask(ctx context.Context, t *task) (err error) {
	if t.overlap {
		lock := cache.NewLock(ctx, "schedule:"+t.name+":running", overlapLease)
		ok, err := lock.TryAcquire(ctx)
		if err != nil {
			return fmt.Errorf("schedule: task %s: take its lock: %w", t.name, err)
		}
		if !ok {
			return ErrOverlap
		}
		stop := s.keep(ctx, t, lock)
		defer func() {
			stop()
			lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockCallTimeout)
			defer cancel()
			_, _ = lock.Release(lctx)
		}()
	}
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	if s.app != nil {
		var end func()
		ctx, end = s.app.StartUnit(ctx, anetos.Unit{Kind: "task", Name: t.name})
		defer end()
	}
	defer func() {
		if v := recover(); v != nil {
			err = &panicError{value: v, stack: debug.Stack()}
		}
	}()
	return t.fn(ctx)
}

// overlapLease is how long WithoutOverlapping's lock lasts unless the
// run, while it goes, extends it (every third of it): a run whose process
// died holds it for at most this long.
var overlapLease = 3 * time.Minute

// lockCallTimeout bounds extending and releasing the overlap lock.
const lockCallTimeout = 10 * time.Second

// keep extends lock while the run goes, until stop is called.
func (s *Scheduler) keep(ctx context.Context, t *task, lock *cache.Lock) (stop func()) {
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(overlapLease / 3)
		defer tick.Stop()
		warned := false
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockCallTimeout)
				ok, err := lock.Extend(lctx, overlapLease)
				cancel()
				switch {
				case err != nil && !warned:
					warned = true
					s.log.WarnContext(ctx, "schedule: couldn't extend the task's lock: another run may start", "task", t.name, "error", err)
				case err == nil && !ok:
					s.log.WarnContext(ctx, "schedule: the task's lock expired: another run may start", "task", t.name)
					return
				}
			}
		}
	})
	return func() { close(done); wg.Wait() }
}

// panicError is a task's panic.
type panicError struct {
	value any
	stack []byte
}

func (e *panicError) Error() string { return fmt.Sprintf("panic: %v", e.value) }

// Name implements supervisor.Component.
func (s *Scheduler) Name() string { return "scheduler" }

// errShutdown cancels the runs still going at the end of the grace
// period.
var errShutdown = errors.New("schedule: the scheduler is stopping")

// releaseMargin is kept before the shutdown deadline.
const releaseMargin = 2 * time.Second

// Run runs the tasks on their schedules until ctx is canceled, then gives
// runs still going half of APP_SHUTDOWN_TIMEOUT (without an app, 15
// seconds or [WithShutdownGrace]) and cancels them. With [New], the scheduler runs as a component of the
// app: don't call Run.
func (s *Scheduler) Run(ctx context.Context) error {
	grace := s.grace
	var deadline func() time.Time
	if s.app != nil {
		grace = s.app.Config().ShutdownTimeout / 2
		deadline = s.app.Supervisor().ShutdownDeadline
	}
	if err := s.checkLocks(ctx); err != nil {
		return err
	}
	s.log.Info("schedule: scheduler started", "tasks", len(s.Tasks()))
	base := context.WithoutCancel(ctx) // runs outlive ctx by the grace period
	runCtx, stop := context.WithCancelCause(base)
	defer stop(nil)
	var wg sync.WaitGroup
	next := map[*task]time.Time{}
loop:
	for {
		s.mu.Lock()
		tasks := slices.Clone(s.tasks)
		s.mu.Unlock()
		now := s.clock.Now()
		var earliest time.Time
		for _, t := range tasks {
			at, seen := next[t]
			if !seen {
				at = s.nextRun(t, now)
				next[t] = at
			}
			if !at.IsZero() && (earliest.IsZero() || at.Before(earliest)) {
				earliest = at
			}
		}
		var timer <-chan time.Time
		if !earliest.IsZero() {
			timer = s.clock.After(earliest.Sub(now))
		}
		select {
		case <-ctx.Done():
			break loop
		case <-s.wake:
			continue
		case <-timer:
		}
		now = s.clock.Now()
		for _, t := range tasks {
			if at := next[t]; !at.IsZero() && !at.After(now) {
				wg.Go(func() { s.fire(runCtx, t, at) })
				next[t] = s.nextRun(t, now)
			}
		}
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	if deadline != nil {
		if d := deadline(); !d.IsZero() {
			grace = max(min(grace, time.Until(d)-releaseMargin), 0)
		}
	}
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		s.log.Info("schedule: stopping the runs still going", "grace", grace)
		stop(errShutdown)
		<-done
	}
	s.log.Info("schedule: scheduler stopped")
	return nil
}

// nextRun returns t's first run after now, or the zero time, logged, if
// it has none.
func (s *Scheduler) nextRun(t *task, now time.Time) time.Time {
	at := t.when.next(now, s.loc)
	if at.IsZero() {
		s.log.Warn("schedule: the task has no more runs", "task", t.name, "schedule", t.when.String())
	}
	return at
}

// checkLocks verifies that ctx has the cache that tasks with locks need,
// and warns if OnOneServer can't hold across instances.
func (s *Scheduler) checkLocks(ctx context.Context) error {
	c, cacheErr := cache.From(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tasks {
		if !t.overlap && !t.oneServer {
			continue
		}
		if cacheErr != nil {
			return fmt.Errorf("schedule: the task %s uses a lock (WithoutOverlapping, OnOneServer): Run's context needs a cache", t.name)
		}
		if _, inMemory := c.Store().(*cache.MemoryStore); inMemory && t.oneServer {
			s.log.WarnContext(ctx, "schedule: OnOneServer with the memory cache store holds only within this process: use a store the instances share (CACHE_STORE=database or redis) if several run the scheduler", "task", t.name)
		}
	}
	return nil
}

// Run is a task's run, as [Scheduler.LastRun] returns it.
type Run struct {
	// At is when it started.
	At time.Time `json:"at"`
	// Duration is how long it took.
	Duration time.Duration `json:"duration"`
	// Error is its error, "" if it succeeded.
	Error string `json:"error,omitempty"`
}

// lastRunTTL is how long the cache keeps a task's last run.
const lastRunTTL = 90 * 24 * time.Hour

// recordRun keeps a task's last run in the cache, if there is one. A run
// skipped because the previous one was still going isn't one: that one's
// stays.
func (s *Scheduler) recordRun(ctx context.Context, name string, start time.Time, err error) {
	if errors.Is(err, ErrOverlap) {
		return
	}
	run := Run{At: start.UTC(), Duration: time.Since(start).Round(time.Millisecond)}
	if err != nil {
		run.Error = err.Error()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lockCallTimeout)
	defer cancel()
	if err := cache.Set(ctx, "schedule:last:"+name, run, lastRunTTL); err != nil {
		s.log.DebugContext(ctx, "schedule: keeping the last run", "task", name, "error", err)
	}
}

// LastRun returns the last run of the task name, from the cache: kept by
// whichever process ran it, so processes share it with a shared cache
// store (CACHE_STORE=database or redis). It reports false if none is
// known.
func (s *Scheduler) LastRun(ctx context.Context, name string) (Run, bool, error) {
	return cache.Get[Run](ctx, "schedule:last:"+name)
}

// fire runs t for its run at, unless another instance took the run
// (OnOneServer) or the previous run is still going (WithoutOverlapping).
func (s *Scheduler) fire(ctx context.Context, t *task, at time.Time) {
	log := s.log.With("task", t.name)
	if t.oneServer {
		run := cache.NewLock(ctx, "schedule:"+t.name+":"+at.UTC().Format("200601021504"), time.Hour)
		ok, err := run.TryAcquire(ctx)
		if err != nil {
			log.Error("schedule: take the run's lock", "error", err)
			return
		}
		if !ok {
			log.Debug("schedule: another instance runs it")
			return
		}
	}
	start := time.Now()
	log.Debug("schedule: task started")
	var p *panicError
	err := s.runTask(ctx, t)
	s.recordRun(ctx, t.name, start, err)
	switch {
	case errors.Is(err, ErrOverlap):
		log.Warn("schedule: skipped: the previous run is still going")
	case errors.As(err, &p):
		log.Error("schedule: task panicked", "panic", p.value, "stack", string(p.stack), "duration", time.Since(start).Round(time.Millisecond))
	case err != nil:
		log.Error("schedule: task failed", "error", err, "duration", time.Since(start).Round(time.Millisecond))
	default:
		log.Info("schedule: task done", "duration", time.Since(start).Round(time.Millisecond))
	}
}

// commands are schedule:list and schedule:run.
func (s *Scheduler) commands() []cmd.Command {
	return []cmd.Command{{
		Name:        "schedule:list",
		Description: "List the scheduled tasks and when they run next",
		Run: func(ctx context.Context, args *cmd.Args) error {
			if len(args.Args) > 0 {
				return cmd.Usagef("schedule:list takes no arguments")
			}
			tasks := s.Tasks()
			if len(tasks) == 0 {
				_, err := fmt.Fprintln(args.Stdout, "No scheduled tasks.")
				return err
			}
			now := s.clock.Now()
			tw := tabwriter.NewWriter(args.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "TASK\tSCHEDULE\tNEXT RUN\tOPTIONS")
			for _, t := range tasks {
				var o []string
				if t.WithoutOverlapping {
					o = append(o, "without overlapping")
				}
				if t.OnOneServer {
					o = append(o, "on one server")
				}
				if t.Timeout > 0 {
					o = append(o, "timeout "+short(t.Timeout))
				}
				n := t.Next(now)
				fmt.Fprintf(tw, "%s\t%s\t%s (in %s)\t%s\n", t.Name, t.Schedule, n.Format("2006-01-02 15:04 MST"),
					until(n.Sub(now)), strings.Join(o, ", "))
			}
			return tw.Flush()
		},
	}, {
		Name:        "schedule:run",
		Usage:       "<task>",
		Description: "Run a scheduled task now",
		Run: func(ctx context.Context, args *cmd.Args) error {
			if len(args.Args) != 1 {
				return cmd.Usagef("schedule:run takes a task's name")
			}
			start := time.Now()
			if err := s.RunTask(ctx, args.Args[0]); err != nil {
				return err
			}
			_, err := fmt.Fprintf(args.Stdout, "Ran %s in %s.\n", args.Args[0], time.Since(start).Round(time.Millisecond))
			return err
		},
	}}
}

// until formats d to the minute: "17h1m", "1m".
func until(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "under a minute"
	}
	return short(d)
}

// short formats d without zero units at its end: "10m", "2h", "1m30s".
func short(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
