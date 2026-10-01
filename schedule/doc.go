// SPDX-License-Identifier: Apache-2.0

// Package schedule runs tasks on schedules (cron expressions, or helpers
// such as Daily and Every) inside the app, as a supervised component: no
// system cron, and a deploy of the binary deploys the schedule.
//
//	s, err := schedule.ForApp(app)
//	err = s.Add(schedule.Every(5*time.Minute), "sync-inventory", inventory.Sync)
//	err = s.Add(schedule.DailyAt("02:00").In("Asia/Dhaka"), "prune-sessions", sessions.Prune,
//		schedule.WithoutOverlapping(), schedule.OnOneServer())
//
// When several instances run the scheduler, OnOneServer runs each run on
// one of them, and WithoutOverlapping skips a run while the previous one
// goes on: both use locks in the app's cache, so they need a store the
// instances share (database or Redis).
package schedule
