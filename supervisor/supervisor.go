// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrAlreadyRun is returned by [Supervisor.Run] on a second call.
	ErrAlreadyRun = errors.New("supervisor: Run called more than once")
	// ErrStopping is returned by [Supervisor.Add] once shutdown has begun.
	ErrStopping = errors.New("supervisor: shutting down")
	// ErrShutdownTimeout is wrapped by the error [Supervisor.Run] returns when
	// components are still running after the shutdown timeout.
	ErrShutdownTimeout = errors.New("supervisor: shutdown timed out")
)

// PanicError is the failure recorded when a component panics.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// Options configures a [Supervisor].
type Options struct {
	// Logger receives lifecycle, failure and restart events.
	// Default slog.Default().
	Logger *slog.Logger
	// ShutdownTimeout bounds graceful shutdown across all stages. Default 30s.
	ShutdownTimeout time.Duration
}

// Supervisor runs components as goroutines, restarts or escalates failures,
// and shuts everything down in stage order.
//
// A Supervisor is safe for concurrent use. Components may be added before or
// during Run (for example, a background task started from a request handler).
type Supervisor struct {
	opts Options
	log  *slog.Logger

	mu       sync.Mutex
	entries  []*entry
	names    map[string]bool
	state    runState
	selected map[string]bool // roles chosen for this process; nil = all
	base     context.Context
	stages   map[Stage]*stageCtl
	fatal    chan error
	done     chan struct{} // closed when all started components have exited
	doneOnce bool
	running  int
	stopAt   time.Time // when shutdown began
}

type runState int

const (
	stateNew runState = iota
	stateRunning
	stateStopping
	stateStopped
)

type stageCtl struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type entry struct {
	spec Spec

	mu       sync.Mutex
	state    State
	restarts int
	lastErr  error
	since    time.Time
}

// New returns a Supervisor with the given options.
func New(opts Options) *Supervisor {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = 30 * time.Second
	}
	return &Supervisor{
		opts:  opts,
		log:   opts.Logger,
		names: map[string]bool{},
		fatal: make(chan error, 1),
		done:  make(chan struct{}),
	}
}

// Add registers a component. Names must be unique. If the supervisor is
// already running and the component's roles are selected, it starts
// immediately. Add returns [ErrStopping] once shutdown has begun, including
// when Run is returning because every component has finished.
func (s *Supervisor) Add(spec Spec) error {
	if spec.Component == nil {
		return errors.New("supervisor: nil component")
	}
	name := spec.Component.Name()
	if name == "" {
		return errors.New("supervisor: component name must not be empty")
	}
	spec.Backoff = spec.Backoff.withDefaults()
	spec.Roles = slices.Clone(spec.Roles)

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state >= stateStopping {
		return ErrStopping
	}
	if s.names[name] {
		return fmt.Errorf("supervisor: duplicate component name %q", name)
	}
	s.names[name] = true
	e := &entry{spec: spec, state: StatePending, since: time.Now()}
	s.entries = append(s.entries, e)
	if s.state == stateRunning && s.isSelected(spec) {
		s.startLocked(e)
	}
	return nil
}

// Roles returns the sorted set of roles declared by registered components.
func (s *Supervisor) Roles() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rolesLocked()
}

func (s *Supervisor) rolesLocked() []string {
	set := map[string]bool{}
	for _, e := range s.entries {
		for _, r := range e.spec.Roles {
			set[r] = true
		}
	}
	roles := make([]string, 0, len(set))
	for r := range set {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	return roles
}

// Run starts the components selected by roles (all components if roles is
// empty; components without roles always run) and blocks until ctx is
// canceled, a [StopOnFailure] component fails, or every component has
// returned. It then shuts down stage by stage within Options.ShutdownTimeout.
//
// Run returns nil after a clean shutdown, the failing component's error for
// an escalated failure, and an error wrapping [ErrShutdownTimeout] if
// components did not stop in time. Requesting a role that no component
// declares is an error, which catches typos in --only flags.
//
// Context values from ctx are visible to components; its cancellation only
// triggers shutdown, which the supervisor then performs in stage order.
func (s *Supervisor) Run(ctx context.Context, roles ...string) error {
	s.mu.Lock()
	if s.state != stateNew {
		s.mu.Unlock()
		return ErrAlreadyRun
	}
	if len(roles) > 0 {
		known := s.rolesLocked()
		s.selected = map[string]bool{}
		for _, r := range roles {
			if !slices.Contains(known, r) {
				s.mu.Unlock()
				return fmt.Errorf("supervisor: unknown role %q (known roles: %s)", r, strings.Join(known, ", "))
			}
			s.selected[r] = true
		}
	}
	s.state = stateRunning
	s.base = context.WithoutCancel(ctx)
	s.stages = map[Stage]*stageCtl{}
	for _, e := range s.entries {
		if s.isSelected(e.spec) {
			s.startLocked(e)
		}
	}
	if s.running == 0 {
		s.closeDoneLocked()
	}
	s.mu.Unlock()

	s.log.Info("supervisor started", "roles", rolesAttr(roles))

	var runErr error
	select {
	case <-ctx.Done():
		s.log.Info("shutdown requested", "cause", context.Cause(ctx))
	case runErr = <-s.fatal:
	case <-s.done:
		// The last component may have escalated just before exiting; if so,
		// both channels are ready and select may have picked this one.
		runErr = s.takeFatal()
		if runErr == nil {
			s.log.Info("all components finished")
		}
	}
	if runErr != nil {
		s.log.Error("shutting down after component failure", "error", runErr)
	}

	shutErr := s.shutdown()
	if runErr == nil {
		// A StopOnFailure component in a later stage may fail while earlier
		// stages drain.
		runErr = s.takeFatal()
	}
	return errors.Join(runErr, shutErr)
}

func (s *Supervisor) takeFatal() error {
	select {
	case err := <-s.fatal:
		return err
	default:
		return nil
	}
}

// ShutdownStarted returns when shutdown began, or the zero time if it hasn't.
func (s *Supervisor) ShutdownStarted() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopAt
}

func rolesAttr(roles []string) string {
	if len(roles) == 0 {
		return "all"
	}
	return strings.Join(roles, ",")
}

func (s *Supervisor) isSelected(spec Spec) bool {
	if s.selected == nil || len(spec.Roles) == 0 {
		return true
	}
	for _, r := range spec.Roles {
		if s.selected[r] {
			return true
		}
	}
	return false
}

// startLocked launches e's goroutine. s.mu must be held.
func (s *Supervisor) startLocked(e *entry) {
	st := s.stages[e.spec.Stage]
	if st == nil {
		ctx, cancel := context.WithCancel(s.base)
		st = &stageCtl{ctx: ctx, cancel: cancel}
		s.stages[e.spec.Stage] = st
	}
	st.wg.Add(1)
	s.running++
	e.setState(StateStarting, nil)
	go func() {
		defer st.wg.Done()
		defer s.exited()
		s.supervise(st.ctx, e)
	}()
}

func (s *Supervisor) exited() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running--
	if s.running == 0 && s.state == stateRunning {
		s.closeDoneLocked()
	}
}

// closeDoneLocked signals that every started component has exited. From
// then on the supervisor is stopping: Add returns ErrStopping, so no work is
// accepted only to be canceled immediately.
func (s *Supervisor) closeDoneLocked() {
	if !s.doneOnce {
		s.doneOnce = true
		s.state = stateStopping
		s.stopAt = time.Now()
		close(s.done)
	}
}

// supervise runs e until it completes, its stage is canceled, or its
// restart policy gives up.
func (s *Supervisor) supervise(ctx context.Context, e *entry) {
	name := e.spec.Component.Name()
	log := s.log.With("component", name)
	consecutive := 0

	for {
		e.setState(StateRunning, nil)
		started := time.Now()
		err := runSafely(ctx, e.spec.Component)

		if ctx.Err() != nil { // stopped by shutdown
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("component returned an error while stopping", "error", err)
			}
			e.setState(StateStopped, err)
			return
		}
		if err == nil {
			log.Info("component finished")
			e.setState(StateDone, nil)
			return
		}

		attrs := []any{"error", err}
		if pe, ok := errors.AsType[*PanicError](err); ok {
			attrs = append(attrs, "stack", string(pe.Stack))
		}
		log.Error("component failed", attrs...)

		switch e.spec.Restart {
		case RestartNever:
			e.setState(StateFailed, err)
			return
		case StopOnFailure:
			e.setState(StateFailed, err)
			s.escalate(fmt.Errorf("component %q: %w", name, err))
			return
		}

		// RestartOnFailure
		if time.Since(started) > e.spec.Backoff.Max {
			consecutive = 0
		}
		consecutive++
		if limit := e.spec.Backoff.MaxRestarts; limit > 0 && consecutive > limit {
			e.setState(StateFailed, err)
			s.escalate(fmt.Errorf("component %q failed %d times in a row: %w", name, consecutive, err))
			return
		}
		wait := e.spec.Backoff.delay(consecutive)
		e.recordRestart(err)
		log.Warn("restarting component", "in", wait, "attempt", consecutive)

		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			e.setState(StateStopped, err)
			return
		case <-t.C:
		}
	}
}

func (s *Supervisor) escalate(err error) {
	select {
	case s.fatal <- err:
	default: // another failure already triggered shutdown
	}
}

func runSafely(ctx context.Context, c Component) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &PanicError{Value: v, Stack: debug.Stack()}
		}
	}()
	return c.Run(ctx)
}

// shutdown cancels stages in ascending order, waiting for each to drain.
func (s *Supervisor) shutdown() error {
	s.mu.Lock()
	s.state = stateStopping
	if s.stopAt.IsZero() {
		s.stopAt = time.Now()
	}
	order := make([]Stage, 0, len(s.stages))
	for st := range s.stages {
		order = append(order, st)
	}
	s.mu.Unlock()
	slices.Sort(order)

	deadline := time.NewTimer(s.opts.ShutdownTimeout)
	defer deadline.Stop()

	var timedOut bool
	for _, stage := range order {
		ctl := s.stages[stage]
		ctl.cancel()
		if !waitGroupWithin(&ctl.wg, deadline.C) {
			timedOut = true
			break
		}
	}
	if timedOut {
		// Cancel everything left and give well-behaved components in later
		// stages a short grace period, so only truly stuck ones are reported.
		for _, stage := range order {
			s.stages[stage].cancel()
		}
		grace := time.NewTimer(min(s.opts.ShutdownTimeout/10, time.Second))
		for _, stage := range order {
			if !waitGroupWithin(&s.stages[stage].wg, grace.C) {
				break
			}
		}
		grace.Stop()
	}

	s.mu.Lock()
	s.state = stateStopped
	s.mu.Unlock()

	if timedOut {
		var stuck []string
		for _, st := range s.Status() {
			if st.State == StateRunning || st.State == StateStarting || st.State == StateBackoff {
				stuck = append(stuck, st.Name)
			}
		}
		s.log.Error("shutdown timed out", "timeout", s.opts.ShutdownTimeout, "still_running", stuck)
		return fmt.Errorf("%w after %s; still running: %s", ErrShutdownTimeout, s.opts.ShutdownTimeout, strings.Join(stuck, ", "))
	}
	s.log.Info("supervisor stopped")
	return nil
}

// waitGroupWithin waits for wg until timeout fires. On timeout the helper
// goroutine stays blocked until the stuck components return.
func waitGroupWithin(wg *sync.WaitGroup, timeout <-chan time.Time) bool {
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
		return true
	case <-timeout:
		return false
	}
}

// Ready reports whether the supervisor is running, not shutting down, and
// every started component implementing [Readier] is ready. A Readier
// component that is waiting to restart or has failed counts as not ready.
// Use Ready for readiness probes.
func (s *Supervisor) Ready() bool {
	s.mu.Lock()
	if s.state != stateRunning {
		s.mu.Unlock()
		return false
	}
	entries := slices.Clone(s.entries)
	s.mu.Unlock()

	for _, e := range entries {
		r, ok := e.spec.Component.(Readier)
		if !ok {
			continue
		}
		e.mu.Lock()
		st := e.state
		e.mu.Unlock()
		switch st {
		case StateBackoff, StateFailed:
			return false
		case StateStarting, StateRunning:
			if !r.Ready() {
				return false
			}
		}
	}
	return true
}

func (e *entry) setState(st State, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = st
	if err != nil {
		e.lastErr = err
	}
	e.since = time.Now()
}

func (e *entry) recordRestart(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.state = StateBackoff
	e.restarts++
	e.lastErr = err
	e.since = time.Now()
}
