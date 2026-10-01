// SPDX-License-Identifier: Apache-2.0

package anetostest

import (
	"sync"
	"time"
)

// testClock is an App's clock: the system's, moved by Travel, or frozen.
type testClock struct {
	mu     sync.Mutex
	frozen bool
	at     time.Time     // when frozen
	offset time.Duration // when not
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return c.at
	}
	return time.Now().Add(c.offset)
}

// Freeze stops the app's clock at t (at the current time, for the zero
// time), in UTC and to the microsecond (as databases store times), and
// returns it. Everything that reads the time from the app's
// clock (anetos.Now, app.Now) sees it: model timestamps, the expiry of
// sessions, cookies, tokens, signed URLs and cached items, dates in
// validation rules, emails' Date. Time kept by database and Redis
// servers, and timeouts, aren't affected. The clock stays frozen until
// [App.Unfreeze].
//
//	now := app.Freeze(time.Time{})
//	app.PostJSON("/posts", post)
//	anetostest.AssertDatabaseHas[models.Post](app, models.PostCols.CreatedAt.Eq(now))
func (a *App) Freeze(t time.Time) time.Time {
	a.clock.mu.Lock()
	defer a.clock.mu.Unlock()
	if t.IsZero() {
		t = time.Now().Add(a.clock.offset)
		if a.clock.frozen {
			t = a.clock.at
		}
	}
	// Microseconds, as databases store them, so times read back compare
	// equal.
	a.clock.frozen, a.clock.at = true, t.Round(0).Truncate(time.Microsecond).UTC()
	return a.clock.at
}

// Travel moves the app's clock by d (back, if d is negative): a frozen
// clock stays frozen at its new time; a running one keeps running, d
// ahead.
//
//	app.Travel(31 * time.Minute) // past the reset link's lifetime
func (a *App) Travel(d time.Duration) {
	a.clock.mu.Lock()
	defer a.clock.mu.Unlock()
	if a.clock.frozen {
		a.clock.at = a.clock.at.Add(d)
	} else {
		a.clock.offset += d
	}
}

// Unfreeze returns the app's clock to the system's time.
func (a *App) Unfreeze() {
	a.clock.mu.Lock()
	defer a.clock.mu.Unlock()
	a.clock.frozen, a.clock.offset = false, 0
}
