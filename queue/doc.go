// SPDX-License-Identifier: Apache-2.0

// Package queue runs jobs in the background: typed structs, dispatched
// from a request (or anywhere), kept in a store and run by workers, with
// retries, backoff, timeouts and a record of the jobs that failed.
//
// Set it up at startup, register the job types, and start the workers:
//
//	q, err := queue.ForApp(app, redis.QueueDriver()) // QUEUE_DRIVER picks the store
//	err = queue.Register[jobs.SendWelcome](q, queue.Tries(5))
//	err = q.Work(queue.Queues("emails", "default"), queue.Concurrency(10))
//
// Then dispatch jobs with the context of a request or another job:
//
//	err := queue.Dispatch(ctx, jobs.SendWelcome{UserID: u.ID}, queue.OnQueue("emails"))
//
// The drivers: sync (runs jobs at once, for development and tests),
// memory (in the process), database (the app's database; dispatches join
// its transaction), and redis (in drivers/redis).
//
// Delivery is at-least-once: a job may run more than once, so jobs must
// be idempotent. A failed attempt is retried after a backoff until the
// job has used its tries; then it is kept as failed, and the
// queue:failed, queue:retry, queue:forget and queue:flush commands list,
// retry and delete failed jobs.
package queue
