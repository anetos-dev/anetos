// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
)

// operationFuncs is the App's AroundOperations functions.
type operationFuncs struct {
	opMu   sync.RWMutex
	opFns  []OperationFunc
	hasOps atomic.Bool
}

// Operation is one thing the app runs: an HTTP request, a queue job, an
// event or pub/sub listener's handling of a message, a scheduled task, a
// model's call of a tool (package ai), which runs inside another
// operation. OpenTelemetry and logging libraries use the word the same
// way.
type Operation struct {
	// Kind is "request", "job", "listener", "message", "task" or "tool".
	Kind string
	// Name says which: "GET /posts/7", the job type's name, the
	// listener's, task's or tool's name.
	Name string
}

// OperationFunc wraps operations ([App.AroundOperations]): it returns
// the context the operation runs with, and a function called when it
// ends.
type OperationFunc func(ctx context.Context, op Operation) (context.Context, func())

// AroundOperations adds fn, which wraps every operation from now on: the
// server's requests, the queue's jobs, async and queued event listeners,
// pub/sub listeners, scheduled tasks and AI tool calls call
// [App.StartOperation]. db.Connect uses it to detect repeated queries.
func (a *App) AroundOperations(fn OperationFunc) {
	a.opMu.Lock()
	defer a.opMu.Unlock()
	a.opFns = append(a.opFns, fn)
	a.hasOps.Store(true)
}

// HasAroundOperations reports whether [App.AroundOperations] added a
// function, so that code starting many operations can skip building
// their names.
func (a *App) HasAroundOperations() bool { return a.hasOps.Load() }

// StartOperation runs the [App.AroundOperations] functions for op, in
// the order they were added, and returns the context for the operation
// and the function to call when it ends (which ends them in reverse
// order). Packages that run operations call it; with no functions, it
// returns ctx and a no-op.
func (a *App) StartOperation(ctx context.Context, op Operation) (context.Context, func()) {
	a.opMu.RLock()
	fns := a.opFns
	a.opMu.RUnlock()
	if len(fns) == 0 {
		return ctx, func() {}
	}
	ends := make([]func(), 0, len(fns))
	for _, fn := range fns {
		var end func()
		ctx, end = fn(ctx, op)
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

// Unit is [Operation].
//
// Deprecated: Use Operation; Unit is removed in v0.6.
//
//go:fix inline
type Unit = Operation

// UnitFunc is [OperationFunc].
//
// Deprecated: Use OperationFunc; UnitFunc is removed in v0.6.
//
//go:fix inline
type UnitFunc = OperationFunc

// AroundUnits is [App.AroundOperations].
//
// Deprecated: Use AroundOperations; AroundUnits is removed in v0.6.
//
//go:fix inline
func (a *App) AroundUnits(fn OperationFunc) { a.AroundOperations(fn) }

// HasAroundUnits is [App.HasAroundOperations].
//
// Deprecated: Use HasAroundOperations; HasAroundUnits is removed in v0.6.
//
//go:fix inline
func (a *App) HasAroundUnits() bool { return a.HasAroundOperations() }

// StartUnit is [App.StartOperation].
//
// Deprecated: Use StartOperation; StartUnit is removed in v0.6.
//
//go:fix inline
func (a *App) StartUnit(ctx context.Context, u Operation) (context.Context, func()) {
	return a.StartOperation(ctx, u)
}
