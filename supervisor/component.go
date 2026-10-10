// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"time"
)

// Component is a long-running task managed by a [Supervisor]:
// an HTTP server, a queue worker pool, a pub/sub listener, the scheduler, or
// an ad-hoc background task.
//
// Run must block until the work is finished or ctx is canceled. When ctx is
// canceled, Run should stop taking new work, finish or hand back in-flight
// work, and return promptly. Returning nil means the component completed
// normally; returning an error (or panicking) is a failure handled according
// to the component's [Restart] policy.
type Component interface {
	// Name identifies the component in logs and status; unique per app.
	Name() string
	// Run does the work until ctx is canceled or the work is done.
	Run(ctx context.Context) error
}

// Readier is optionally implemented by components that need time to become
// ready (for example, an HTTP server that must bind its port). Until Ready
// returns true, [Supervisor.Ready] reports false.
type Readier interface {
	// Ready reports whether the component can take work.
	Ready() bool
}

// Func adapts a function to a [Component].
func Func(name string, run func(ctx context.Context) error) Component {
	return funcComponent{name: name, run: run}
}

type funcComponent struct {
	name string
	run  func(ctx context.Context) error
}

func (f funcComponent) Name() string                  { return f.name }
func (f funcComponent) Run(ctx context.Context) error { return f.run(ctx) }

// Restart decides what happens when a component fails (returns an error or
// panics) while the supervisor is running.
type Restart int

const (
	// RestartNever logs the failure and leaves the component stopped. The
	// rest of the application keeps running.
	RestartNever Restart = iota

	// RestartOnFailure restarts the component after an exponential backoff.
	// If Backoff.MaxRestarts is exceeded, the failure escalates as if the
	// policy were StopOnFailure.
	RestartOnFailure

	// StopOnFailure shuts the whole application down; [Supervisor.Run]
	// returns the component's error. Use it for components the app cannot
	// work without, such as the HTTP server.
	StopOnFailure
)

// String returns the policy's name, as in logs.
func (r Restart) String() string {
	switch r {
	case RestartNever:
		return "never"
	case RestartOnFailure:
		return "on-failure"
	case StopOnFailure:
		return "stop-on-failure"
	}
	return fmt.Sprintf("Restart(%d)", int(r))
}

// Stage orders graceful shutdown. On shutdown the supervisor cancels stages
// from lowest to highest, waiting for every component in a stage to return
// before canceling the next. Producers of work stop before its consumers, so
// consumers can finish what was already produced.
type Stage int

// Built-in stages, in shutdown order. Custom components may use any value;
// equal values stop together.
const (
	StageIngress    Stage = 0  // HTTP servers: stop accepting requests first
	StageScheduler  Stage = 10 // scheduler: stop starting new runs
	StageListeners  Stage = 20 // pub/sub listeners: stop pulling messages
	StageWorkers    Stage = 30 // queue workers: finish in-flight jobs
	StageBackground Stage = 40 // ad-hoc background tasks (app.Go)
)

// Backoff configures restarts for [RestartOnFailure].
type Backoff struct {
	// Initial is the delay before the first restart. Default 1s.
	Initial time.Duration
	// Max caps the delay. Default 30s. A component that ran for longer than
	// Max before failing is considered healthy again, and the delay resets.
	Max time.Duration
	// MaxRestarts limits consecutive restarts; 0 means unlimited.
	MaxRestarts int
}

func (b Backoff) withDefaults() Backoff {
	if b.Initial <= 0 {
		b.Initial = time.Second
	}
	if b.Max <= 0 {
		b.Max = 30 * time.Second
	}
	if b.Max < b.Initial {
		b.Max = b.Initial
	}
	return b
}

// delay returns the wait before restart number n (1-based), with ±20% jitter.
func (b Backoff) delay(n int) time.Duration {
	d := b.Initial
	for i := 1; i < n && d < b.Max; i++ {
		if d > b.Max/2 { // also prevents overflow for huge Max values
			d = b.Max
			break
		}
		d *= 2
	}
	d = min(d, b.Max)
	f := float64(d) * (0.8 + 0.4*rand.Float64())
	if f >= math.MaxInt64 {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(f)
}

// Spec describes how a component is supervised.
type Spec struct {
	// Component is what runs.
	Component Component
	// ProcessTypes lists the process types this component runs in (e.g.
	// "web", "worker"). A component with none runs in every process.
	ProcessTypes []string
	// Stage orders shutdown: lower stages stop first.
	Stage Stage
	// Restart says what happens when Run fails (returns an error or
	// panics); a nil return is always done.
	Restart Restart
	// Backoff spaces restarts; the zero value uses the defaults.
	Backoff Backoff
}
