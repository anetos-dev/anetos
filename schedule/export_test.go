// SPDX-License-Identifier: Apache-2.0

package schedule

import "time"

// SetClock replaces the scheduler's clock.
func SetClock(s *Scheduler, c interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}) {
	s.clock = c
}

// SetOverlapLease sets how long WithoutOverlapping's lock lasts unless
// extended, until the test ends.
func SetOverlapLease(t interface{ Cleanup(func()) }, d time.Duration) {
	old := overlapLease
	overlapLease = d
	t.Cleanup(func() { overlapLease = old })
}

// Grace returns the scheduler's shutdown grace.
func Grace(s *Scheduler) time.Duration { return s.grace }
