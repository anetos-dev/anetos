// SPDX-License-Identifier: Apache-2.0

package anetos

import (
	"context"
	"sync/atomic"
	"time"
)

// clock is the app's source of the time: the system clock, or a test's.
type clock struct {
	now atomic.Pointer[func() time.Time]
	loc *time.Location // the app's zone; nil: time.Local
}

func (c *clock) read() time.Time {
	t := time.Now()
	if fn := c.now.Load(); fn != nil {
		t = (*fn)()
	}
	if c.loc != nil && t.Location() != c.loc {
		t = t.In(c.loc) // keeps time.Now's monotonic reading when the zones match
	}
	return t
}

type clockKey struct{}

// Now returns the time on the app's clock, in the app's zone
// ([App.Location]): the system's, unless a test set another with
// [App.SetClock] (anetostest's Freeze and Travel). The
// framework reads the time an app can observe from it: model
// timestamps, expiry of sessions, tokens, signed URLs and cache items in
// memory, dates in validation rules, emails' Date. Durations, timeouts
// and the time kept by database and Redis servers stay real.
func (a *App) Now() time.Time { return a.clock.read() }

// SetClock makes now the app's clock, for tests (anetostest's Freeze and
// Travel use it); nil restores the system clock. It applies at once,
// also to contexts made before.
func (a *App) SetClock(now func() time.Time) {
	if now == nil {
		a.clock.now.Store(nil)
		return
	}
	a.clock.now.Store(&now)
}

// Now returns the time on the clock of the app in ctx ([App.Now]), or
// time.Now when ctx has none. Use it, rather than time.Now, for times
// tests may want to control:
//
//	order.PaidAt = anetos.Now(ctx)
func Now(ctx context.Context) time.Time {
	if c, ok := ctx.Value(clockKey{}).(*clock); ok {
		return c.read()
	}
	return time.Now()
}

// WithClock returns ctx with now as its clock, for [Now], for code
// without an app (a package's own tests).
func WithClock(ctx context.Context, now func() time.Time) context.Context {
	c := &clock{}
	c.now.Store(&now)
	return context.WithValue(ctx, clockKey{}, c)
}
