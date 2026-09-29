// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestSupervisor(timeout time.Duration) *Supervisor {
	return New(Options{Logger: slog.New(slog.DiscardHandler), ShutdownTimeout: timeout})
}

var fastBackoff = Backoff{Initial: time.Millisecond, Max: 5 * time.Millisecond}

// blocker runs until canceled and records when it stops.
func blocker(name string, stopped func(string)) Component {
	return Func(name, func(ctx context.Context) error {
		<-ctx.Done()
		if stopped != nil {
			stopped(name)
		}
		return ctx.Err()
	})
}

func runAsync(s *Supervisor, ctx context.Context, roles ...string) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- s.Run(ctx, roles...) }()
	return ch
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func statusOf(s *Supervisor, name string) ComponentStatus {
	for _, st := range s.Status() {
		if st.Name == name {
			return st
		}
	}
	return ComponentStatus{}
}

func mustAdd(t *testing.T, s *Supervisor, spec Spec) {
	t.Helper()
	if err := s.Add(spec); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func TestCleanShutdown(t *testing.T) {
	s := newTestSupervisor(time.Second)
	mustAdd(t, s, Spec{Component: blocker("a", nil)})
	mustAdd(t, s, Spec{Component: blocker("b", nil)})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "running", func() bool { return statusOf(s, "b").State == StateRunning })
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	for _, st := range s.Status() {
		if st.State != StateStopped {
			t.Errorf("%s state = %v, want stopped", st.Name, st.State)
		}
	}
}

func TestStagedShutdownOrder(t *testing.T) {
	s := newTestSupervisor(2 * time.Second)
	var mu sync.Mutex
	var order []string
	record := func(n string) { mu.Lock(); order = append(order, n); mu.Unlock() }

	workerCanceled := make(chan struct{})
	// The HTTP component takes a moment to drain; the worker must still be
	// running (its context not canceled) while that happens.
	mustAdd(t, s, Spec{Stage: StageIngress, Component: Func("http", func(ctx context.Context) error {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		select {
		case <-workerCanceled:
			t.Error("worker canceled before http finished draining")
		default:
		}
		record("http")
		return nil
	})})
	mustAdd(t, s, Spec{Stage: StageWorkers, Component: Func("worker", func(ctx context.Context) error {
		<-ctx.Done()
		close(workerCanceled)
		record("worker")
		return nil
	})})
	mustAdd(t, s, Spec{Stage: StageBackground, Component: blocker("bg", record)})
	mustAdd(t, s, Spec{Stage: StageScheduler, Component: blocker("scheduler", record)})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "running", func() bool { return s.Ready() })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
	if got := strings.Join(order, ","); got != "http,scheduler,worker,bg" {
		t.Errorf("stop order = %s", got)
	}
}

func TestRestartOnFailure(t *testing.T) {
	s := newTestSupervisor(time.Second)
	var calls atomic.Int32
	mustAdd(t, s, Spec{
		Restart: RestartOnFailure,
		Backoff: fastBackoff,
		Component: Func("flaky", func(ctx context.Context) error {
			if calls.Add(1) <= 2 {
				return errors.New("boom")
			}
			<-ctx.Done()
			return nil
		}),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "third run", func() bool { return calls.Load() == 3 && statusOf(s, "flaky").State == StateRunning })
	st := statusOf(s, "flaky")
	if st.Restarts != 2 || st.LastError == nil || st.LastError.Error() != "boom" {
		t.Errorf("status = %+v", st)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestMaxRestartsEscalates(t *testing.T) {
	s := newTestSupervisor(time.Second)
	b := fastBackoff
	b.MaxRestarts = 2
	mustAdd(t, s, Spec{Restart: RestartOnFailure, Backoff: b,
		Component: Func("crashloop", func(context.Context) error { return errors.New("nope") })})
	mustAdd(t, s, Spec{Component: blocker("other", nil)})

	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `component "crashloop" failed 3 times in a row: nope`) {
		t.Fatalf("Run = %v", err)
	}
	if st := statusOf(s, "other"); st.State != StateStopped {
		t.Errorf("other state = %v", st.State)
	}
}

func TestStopOnFailure(t *testing.T) {
	s := newTestSupervisor(time.Second)
	boom := errors.New("listen tcp :80: bind: permission denied")
	mustAdd(t, s, Spec{Restart: StopOnFailure, Component: Func("http", func(context.Context) error { return boom })})
	mustAdd(t, s, Spec{Component: blocker("worker", nil)})

	err := s.Run(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Run = %v, want wrapping %v", err, boom)
	}
	if statusOf(s, "http").State != StateFailed || statusOf(s, "worker").State != StateStopped {
		t.Errorf("status = %+v", s.Status())
	}
}

func TestRestartNeverKeepsAppRunning(t *testing.T) {
	s := newTestSupervisor(time.Second)
	mustAdd(t, s, Spec{Component: Func("once", func(context.Context) error { return errors.New("bad") })})
	mustAdd(t, s, Spec{Component: blocker("server", nil)})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "failure", func() bool { return statusOf(s, "once").State == StateFailed })
	select {
	case err := <-done:
		t.Fatalf("Run returned early: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	s := newTestSupervisor(time.Second)
	mustAdd(t, s, Spec{Component: Func("panicky", func(context.Context) error { panic("kaboom") })})

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run = %v", err)
	}
	st := statusOf(s, "panicky")
	var pe *PanicError
	if st.State != StateFailed || !errors.As(st.LastError, &pe) || pe.Value != "kaboom" || len(pe.Stack) == 0 {
		t.Errorf("status = %+v", st)
	}
}

func TestRoles(t *testing.T) {
	s := newTestSupervisor(time.Second)
	var started sync.Map
	mk := func(name string) Component {
		return Func(name, func(ctx context.Context) error {
			started.Store(name, true)
			<-ctx.Done()
			return nil
		})
	}
	mustAdd(t, s, Spec{Component: mk("http"), Roles: []string{"http"}})
	mustAdd(t, s, Spec{Component: mk("worker"), Roles: []string{"workers"}})
	mustAdd(t, s, Spec{Component: mk("both"), Roles: []string{"workers", "listeners"}})
	mustAdd(t, s, Spec{Component: mk("roleless")})

	if got := strings.Join(s.Roles(), ","); got != "http,listeners,workers" {
		t.Errorf("Roles = %s", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx, "workers")
	waitFor(t, "start", func() bool {
		_, a := started.Load("worker")
		_, b := started.Load("both")
		_, c := started.Load("roleless")
		return a && b && c
	})
	if _, ok := started.Load("http"); ok {
		t.Error("http started although not selected")
	}
	if statusOf(s, "http").State != StatePending {
		t.Errorf("http state = %v, want pending", statusOf(s, "http").State)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestUnknownRole(t *testing.T) {
	s := newTestSupervisor(time.Second)
	mustAdd(t, s, Spec{Component: blocker("http", nil), Roles: []string{"http"}})
	err := s.Run(context.Background(), "htp")
	if err == nil || err.Error() != `supervisor: unknown role "htp" (known roles: http)` {
		t.Fatalf("Run = %v", err)
	}
}

func TestShutdownTimeout(t *testing.T) {
	s := newTestSupervisor(30 * time.Millisecond)
	release := make(chan struct{})
	defer close(release)
	mustAdd(t, s, Spec{Component: Func("stubborn", func(context.Context) error { <-release; return nil })})
	mustAdd(t, s, Spec{Stage: StageBackground, Component: blocker("later", nil)})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "running", func() bool { return statusOf(s, "stubborn").State == StateRunning })
	cancel()
	err := <-done
	if !errors.Is(err, ErrShutdownTimeout) || !strings.Contains(err.Error(), "still running: stubborn") {
		t.Fatalf("Run = %v", err)
	}
}

func TestAllComponentsFinish(t *testing.T) {
	s := newTestSupervisor(time.Second)
	mustAdd(t, s, Spec{Component: Func("job", func(context.Context) error { return nil })})
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run = %v", err)
	}
	if st := statusOf(s, "job"); st.State != StateDone {
		t.Errorf("state = %v", st.State)
	}
}

func TestNoComponents(t *testing.T) {
	if err := newTestSupervisor(time.Second).Run(context.Background()); err != nil {
		t.Fatalf("Run = %v", err)
	}
}

func TestDynamicAdd(t *testing.T) {
	s := newTestSupervisor(time.Second)
	mustAdd(t, s, Spec{Component: blocker("server", nil)})
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "server", func() bool { return statusOf(s, "server").State == StateRunning })

	ran := make(chan struct{})
	mustAdd(t, s, Spec{Stage: StageBackground, Component: Func("task", func(context.Context) error {
		close(ran)
		return nil
	})})
	<-ran
	waitFor(t, "task done", func() bool { return statusOf(s, "task").State == StateDone })

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Spec{Component: blocker("late", nil)}); !errors.Is(err, ErrStopping) {
		t.Errorf("Add after stop = %v, want ErrStopping", err)
	}
}

// Components finishing and new ones being added concurrently must never
// close the internal done channel twice.
func TestDynamicAddRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		s := newTestSupervisor(time.Second)
		mustAdd(t, s, Spec{Component: Func("first", func(context.Context) error { return nil })})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = s.Add(Spec{Component: Func("dyn"+string(rune('a'+j)), func(context.Context) error { return nil })})
			}
		}()
		if err := s.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		wg.Wait()
	}
}

func TestAddValidation(t *testing.T) {
	s := newTestSupervisor(time.Second)
	if err := s.Add(Spec{}); err == nil {
		t.Error("nil component accepted")
	}
	if err := s.Add(Spec{Component: Func("", nil)}); err == nil {
		t.Error("empty name accepted")
	}
	mustAdd(t, s, Spec{Component: blocker("x", nil)})
	if err := s.Add(Spec{Component: blocker("x", nil)}); err == nil || !strings.Contains(err.Error(), `duplicate component name "x"`) {
		t.Errorf("duplicate: %v", err)
	}
}

type slowStart struct {
	ready atomic.Bool
}

func (c *slowStart) Name() string { return "slow" }
func (c *slowStart) Ready() bool  { return c.ready.Load() }
func (c *slowStart) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func TestReady(t *testing.T) {
	s := newTestSupervisor(time.Second)
	c := &slowStart{}
	mustAdd(t, s, Spec{Component: c})
	if s.Ready() {
		t.Error("ready before Run")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "running", func() bool { return statusOf(s, "slow").State == StateRunning })
	if s.Ready() {
		t.Error("ready while component not ready")
	}
	c.ready.Store(true)
	if !s.Ready() {
		t.Error("not ready after component became ready")
	}
	cancel()
	<-done
	if s.Ready() {
		t.Error("ready after shutdown")
	}
}

func TestRunTwice(t *testing.T) {
	s := newTestSupervisor(time.Second)
	_ = s.Run(context.Background())
	if err := s.Run(context.Background()); !errors.Is(err, ErrAlreadyRun) {
		t.Errorf("second Run = %v", err)
	}
}

type ctxKey struct{}

func TestContextValuesPropagate(t *testing.T) {
	s := newTestSupervisor(time.Second)
	got := make(chan any, 1)
	mustAdd(t, s, Spec{Component: Func("v", func(ctx context.Context) error {
		got <- ctx.Value(ctxKey{})
		return nil
	})})
	ctx := context.WithValue(context.Background(), ctxKey{}, "hello")
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if v := <-got; v != "hello" {
		t.Errorf("value = %v", v)
	}
}

func TestBackoffDelay(t *testing.T) {
	b := Backoff{Initial: 100 * time.Millisecond, Max: time.Second}.withDefaults()
	within := func(d, want time.Duration) bool {
		return d >= want*8/10 && d <= want*12/10
	}
	for n, want := range map[int]time.Duration{1: 100 * time.Millisecond, 2: 200 * time.Millisecond, 3: 400 * time.Millisecond, 5: time.Second, 50: time.Second} {
		for range 20 {
			if d := b.delay(n); !within(d, want) {
				t.Fatalf("delay(%d) = %v, want about %v", n, d, want)
			}
		}
	}
	if d := (Backoff{}).withDefaults(); d.Initial != time.Second || d.Max != 30*time.Second {
		t.Errorf("defaults = %+v", d)
	}
}

func TestStrings(t *testing.T) {
	if StateBackoff.String() != "backoff" || State(99).String() != "unknown" {
		t.Error("State.String")
	}
	if RestartOnFailure.String() != "on-failure" || Restart(9).String() != "Restart(9)" {
		t.Error("Restart.String")
	}
	if (&PanicError{Value: 1}).Error() != "panic: 1" {
		t.Error("PanicError.Error")
	}
}

// --- regression tests from the F2–F4 code review ---

// slowStartHandler delays the "supervisor started" log line, widening the
// window in which the only component can fail before Run starts waiting.
type slowStartHandler struct{ slog.Handler }

func (h slowStartHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Message == "supervisor started" {
		time.Sleep(time.Millisecond)
	}
	return h.Handler.Handle(ctx, r)
}

func TestFatalNotLostWhenLastComponentFails(t *testing.T) {
	for i := range 200 {
		s := New(Options{Logger: slog.New(slowStartHandler{slog.DiscardHandler}), ShutdownTimeout: time.Second})
		mustAdd(t, s, Spec{Restart: StopOnFailure, Component: Func("http", func(context.Context) error {
			return errors.New("bind failed")
		})})
		if err := s.Run(context.Background()); err == nil {
			t.Fatalf("iteration %d: Run returned nil after StopOnFailure component failed", i)
		}
	}
}

func TestFatalDuringShutdownIsReported(t *testing.T) {
	s := newTestSupervisor(2 * time.Second)
	draining := make(chan struct{})
	mustAdd(t, s, Spec{Stage: StageIngress, Component: Func("http", func(ctx context.Context) error {
		<-ctx.Done()
		close(draining)
		time.Sleep(20 * time.Millisecond)
		return nil
	})})
	mustAdd(t, s, Spec{Stage: StageWorkers, Restart: StopOnFailure, Component: Func("worker", func(ctx context.Context) error {
		<-draining // fails while the earlier stage drains; its own ctx is still live
		return errors.New("lost connection")
	})})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "running", func() bool { return statusOf(s, "worker").State == StateRunning })
	cancel()
	if err := <-done; err == nil || !strings.Contains(err.Error(), "lost connection") {
		t.Fatalf("Run = %v, want the worker failure", err)
	}
}

func TestAddNeverAcceptsWorkThatIsCanceledBeforeStarting(t *testing.T) {
	for range 100 {
		s := newTestSupervisor(time.Second)
		mustAdd(t, s, Spec{Component: Func("quick", func(context.Context) error { return nil })})
		var canceledAtStart atomic.Int32
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.Add(Spec{Component: Func(fmt.Sprintf("dyn-%d", i), func(ctx context.Context) error {
					if ctx.Err() != nil {
						canceledAtStart.Add(1)
					}
					return nil
				})})
			}
		}()
		if err := s.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		close(stop)
		wg.Wait()
		if n := canceledAtStart.Load(); n > 0 {
			t.Fatalf("%d accepted components were canceled before they started", n)
		}
	}
}

type crashingReadier struct{}

func (crashingReadier) Name() string              { return "http" }
func (crashingReadier) Ready() bool               { return true }
func (crashingReadier) Run(context.Context) error { return errors.New("crash") }

func TestNotReadyWhileReadierInBackoff(t *testing.T) {
	s := newTestSupervisor(time.Second)
	mustAdd(t, s, Spec{Component: crashingReadier{}, Restart: RestartOnFailure,
		Backoff: Backoff{Initial: time.Minute, Max: time.Minute}})
	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "backoff", func() bool { return statusOf(s, "http").State == StateBackoff })
	if s.Ready() {
		t.Error("Ready() = true while a Readier component is in backoff")
	}
	cancel()
	<-done
}

func TestShutdownTimeoutListsOnlyStuckComponents(t *testing.T) {
	s := newTestSupervisor(50 * time.Millisecond)
	release := make(chan struct{})
	defer close(release)
	mustAdd(t, s, Spec{Stage: StageIngress, Component: Func("stuck-ingress", func(context.Context) error { <-release; return nil })})
	mustAdd(t, s, Spec{Stage: StageWorkers, Component: blocker("well-behaved-worker", nil)})

	ctx, cancel := context.WithCancel(context.Background())
	done := runAsync(s, ctx)
	waitFor(t, "running", func() bool { return statusOf(s, "well-behaved-worker").State == StateRunning })
	cancel()
	err := <-done
	if !errors.Is(err, ErrShutdownTimeout) {
		t.Fatalf("Run = %v", err)
	}
	if !strings.HasSuffix(err.Error(), "still running: stuck-ingress") {
		t.Errorf("err = %v, want only stuck-ingress listed", err)
	}
	if st := s.ShutdownStarted(); st.IsZero() {
		t.Error("ShutdownStarted is zero after shutdown")
	}
}

func TestBackoffHugeMaxDoesNotOverflow(t *testing.T) {
	b := Backoff{Initial: time.Second, Max: time.Duration(math.MaxInt64)}.withDefaults()
	for _, n := range []int{1, 40, 100, 1000} {
		if d := b.delay(n); d <= 0 {
			t.Fatalf("delay(%d) = %v", n, d)
		}
	}
}
