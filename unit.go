// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
)

// unitFuncs is the App's AroundUnits functions.
type unitFuncs struct {
	unitMu   sync.RWMutex
	unitFns  []UnitFunc
	hasUnits atomic.Bool
}

// Unit is a unit of work the app runs: an HTTP request, a queue job, an
// event or pub/sub listener's handling of a message, a scheduled task, a
// model's call of a tool (package ai), which runs inside another unit.
type Unit struct {
	// Kind is "request", "job", "listener", "message", "task" or "tool".
	Kind string
	// Name says which: "GET /posts/7", the job type's name, the
	// listener's, task's or tool's name.
	Name string
}

// UnitFunc wraps units of work ([App.AroundUnits]): it returns the
// context the unit runs with, and a function called when it ends.
type UnitFunc func(ctx context.Context, u Unit) (context.Context, func())

// AroundUnits adds fn, which wraps every unit of work from now on: the
// server's requests, the queue's jobs, async and queued event listeners,
// pub/sub listeners, scheduled tasks and AI tool calls call
// [App.StartUnit]. db.Connect uses it to detect repeated queries.
func (a *App) AroundUnits(fn UnitFunc) {
	a.unitMu.Lock()
	defer a.unitMu.Unlock()
	a.unitFns = append(a.unitFns, fn)
	a.hasUnits.Store(true)
}

// HasAroundUnits reports whether [App.AroundUnits] added a function, so
// that code starting many units can skip building their names.
func (a *App) HasAroundUnits() bool { return a.hasUnits.Load() }

// StartUnit runs the [App.AroundUnits] functions for u, in the order
// they were added, and returns the context for the unit and the function
// to call when it ends (which ends them in reverse order). Packages that
// run units of work call it; with no functions, it returns ctx and a
// no-op.
func (a *App) StartUnit(ctx context.Context, u Unit) (context.Context, func()) {
	a.unitMu.RLock()
	fns := a.unitFns
	a.unitMu.RUnlock()
	if len(fns) == 0 {
		return ctx, func() {}
	}
	ends := make([]func(), 0, len(fns))
	for _, fn := range fns {
		var end func()
		ctx, end = fn(ctx, u)
		if end != nil {
			ends = append(ends, end)
		}
	}
	return ctx, func() {
		for _, end := range slices.Backward(ends) {
			end()
		}
	}
}
