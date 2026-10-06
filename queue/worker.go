// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/supervisor"
)

// leaseMargin is how much longer than its timeout a job stays reserved.
const leaseMargin = 30 * time.Second

// storeTimeout bounds a worker's calls to the store.
const storeTimeout = 10 * time.Second

// WorkOption configures workers.
type WorkOption func(*workOptions) error

type workOptions struct {
	queues      []string
	concurrency int
	grace       time.Duration
	deadline    func() time.Time // the shutdown deadline, if known
}

// releaseMargin is the time kept before the shutdown deadline to put
// stopped jobs back.
const releaseMargin = 2 * time.Second

// Queues sets the queues the workers take jobs from, in order of
// priority: a job on the first is taken before any on the second, and so
// on. Default QUEUE_DEFAULT.
func Queues(names ...string) WorkOption {
	return func(o *workOptions) error {
		if len(names) == 0 {
			return errors.New("queue: Queues needs at least one name")
		}
		for _, n := range names {
			if err := checkQueue(n); err != nil {
				return err
			}
		}
		o.queues = names
		return nil
	}
}

// Concurrency sets how many jobs the workers run at once. Default 1.
func Concurrency(n int) WorkOption {
	return func(o *workOptions) error {
		if n < 1 {
			return fmt.Errorf("queue: Concurrency(%d) must be at least 1", n)
		}
		o.concurrency = n
		return nil
	}
}

// ShutdownGrace sets how long running jobs may take to finish once the
// workers stop: then their contexts are canceled, and the jobs that
// return because of it go back on the queue, without the attempt
// counting (unless they return a [Permanent] error). Default: half of
// APP_SHUTDOWN_TIMEOUT with [Queue.Work], and never more than the app's
// shutdown budget leaves (minus 2s to put the jobs back; with a budget
// of a few seconds, running jobs are stopped at once); 15s with
// [Queue.Run].
func ShutdownGrace(d time.Duration) WorkOption {
	return func(o *workOptions) error {
		if d < 0 {
			return fmt.Errorf("queue: ShutdownGrace(%s) can't be negative", d)
		}
		o.grace = d
		return nil
	}
}

func (q *Queue) workOptions(grace time.Duration, opts []WorkOption) (workOptions, error) {
	o := workOptions{queues: []string{q.cfg.Default}, concurrency: 1, grace: grace}
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return o, err
		}
	}
	return o, nil
}

// Work adds workers to the app, as a component with the role "workers"
// (so `run --only=workers` runs only them) that stops after the HTTP
// server and listeners, so it finishes the jobs they dispatched:
//
//	err := q.Work(queue.Queues("emails", "default"), queue.Concurrency(10))
//
// With the sync driver it does nothing. It needs a queue made with
// [ForApp].
func (q *Queue) Work(opts ...WorkOption) error {
	if q.app == nil {
		return errors.New("queue: Work needs a queue made with queue.ForApp; run Queue.Run in a component of your own")
	}
	o, err := q.workOptions(q.app.Config().ShutdownTimeout/2, opts)
	if err != nil {
		return err
	}
	if q.sync {
		q.log.Debug("queue: the sync driver runs jobs when they are dispatched: no workers", "queues", o.queues)
		return nil
	}
	o.deadline = q.app.Supervisor().ShutdownDeadline
	name := "queue-worker[" + strings.Join(o.queues, ",") + "]"
	return q.app.Component(supervisor.Func(name, func(ctx context.Context) error { return q.run(ctx, o) }),
		anetos.Roles("workers"), anetos.Stage(supervisor.StageWorkers), anetos.Restart(supervisor.RestartOnFailure))
}

// Run runs workers until ctx is canceled, then lets running jobs finish
// (see [ShutdownGrace]) and returns. Jobs get ctx's values, such as what
// an app adds to its contexts. [Queue.Work] runs it in the app.
func (q *Queue) Run(ctx context.Context, opts ...WorkOption) error {
	o, err := q.workOptions(15*time.Second, opts)
	if err != nil {
		return err
	}
	if q.sync {
		return errors.New("queue: the sync driver runs jobs when they are dispatched: there is nothing to work on")
	}
	return q.run(ctx, o)
}

// errShutdown is the cause of the cancellation of jobs at shutdown.
var errShutdown = errors.New("queue: the worker is stopping")

func (q *Queue) run(ctx context.Context, o workOptions) error {
	q.log.Info("queue: worker started", "queues", o.queues, "concurrency", o.concurrency)
	base := context.WithoutCancel(ctx) // jobs outlive ctx by the grace period
	jobCtx, stopJobs := context.WithCancelCause(base)
	defer stopJobs(nil)
	slots := make(chan struct{}, o.concurrency)
	var wg sync.WaitGroup
	failures := 0
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case slots <- struct{}{}:
		}
		if ctx.Err() != nil {
			<-slots
			break loop
		}
		r, err := q.reserve(base, o.queues)
		if err != nil {
			<-slots
			failures++
			wait := min(q.cfg.Poll<<min(failures, 5), 30*time.Second)
			q.log.Error("queue: reserve a job", "error", err, "retry_in", wait)
			if !sleep(ctx, jitter(wait)) {
				break loop
			}
			continue
		}
		failures = 0
		if r == nil {
			<-slots
			if !sleep(ctx, jitter(q.cfg.Poll)) {
				break loop
			}
			continue
		}
		wg.Go(func() {
			defer func() { <-slots }()
			q.process(jobCtx, base, r)
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	grace := o.grace
	if o.deadline != nil {
		if d := o.deadline(); !d.IsZero() {
			// Leave time to put the stopped jobs back before the app
			// closes its connections.
			grace = max(min(grace, time.Until(d)-releaseMargin), 0)
		}
	}
	t := time.NewTimer(grace)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		q.log.Info("queue: stopping the jobs still running", "grace", grace)
		stopJobs(errShutdown)
		<-done
	}
	q.log.Info("queue: worker stopped", "queues", o.queues)
	return nil
}

// sleep waits for d, and reports false if ctx ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// lease is how long a reserved job stays hidden: the longest timeout of
// the job types (the worker only learns the type once it has the job),
// plus a margin.
func (q *Queue) lease() time.Duration {
	q.mu.RLock()
	defer q.mu.RUnlock()
	d := q.cfg.Timeout
	for _, jt := range q.byName {
		d = max(d, jt.timeout)
	}
	return d + leaseMargin
}

// reserve reserves the next job of the first queue that has one.
func (q *Queue) reserve(ctx context.Context, queues []string) (*Reservation, error) {
	ctx, cancel := context.WithTimeout(ctx, storeTimeout)
	defer cancel()
	lease := q.lease()
	for _, name := range queues {
		r, err := q.store.Reserve(ctx, name, lease)
		if err != nil || r != nil {
			return r, err
		}
	}
	return nil, nil
}

// process runs a reserved job and records the outcome: deleted when
// done, released for a retry, released without counting the attempt at
// shutdown, or failed. ctx is canceled at the end of the grace period;
// base is not.
func (q *Queue) process(ctx, base context.Context, r *Reservation) {
	env, jt, job, err := q.decode(r.Payload)
	ctx, base = env.inLocale(ctx), env.inLocale(base) // the dispatcher's language and time zone
	if q.app != nil && len(env.Carried) > 0 {
		ctx, base = q.app.WithCarried(ctx, env.Carried), q.app.WithCarried(base, env.Carried)
	}
	info := Info{ID: r.ID, Job: env.Job, Queue: r.Queue, Attempt: r.Attempts, Tries: q.tries(jt)}
	log := q.log.With("job", info.Job, "id", info.ID, "queue", info.Queue, "attempt", info.Attempt)
	timeout := q.timeout(jt)
	if err == nil && r.Attempts > info.Tries {
		// The last attempt never recorded its outcome (its worker died,
		// maybe because of this job, or it ran past its lease): don't run
		// it again. Its work may have been done.
		err = Permanent(fmt.Errorf("out of tries: attempt %d of %d, after an attempt that didn't record its outcome (its worker stopped, or it ran past its lease)", r.Attempts, info.Tries))
	}
	if err == nil {
		start := time.Now()
		lease := q.lease()
		late := time.AfterFunc(lease, func() {
			log.Warn("queue: job still running after its lease ended: another worker may run it too", "lease", lease)
		})
		err = q.call(ctx, job, info, timeout)
		late.Stop()
		if err == nil {
			sctx, cancel := context.WithTimeout(base, storeTimeout)
			defer cancel()
			if err := q.store.Delete(sctx, r); err != nil {
				q.storeError(log, "delete the finished job", err)
				return
			}
			log.Info("queue: job done", "duration", time.Since(start).Round(time.Millisecond))
			return
		}
	}
	sctx, cancel := context.WithTimeout(base, storeTimeout)
	defer cancel()
	switch {
	case ctx.Err() != nil && errors.Is(context.Cause(ctx), errShutdown) && !IsPermanent(err):
		if err := q.store.Release(sctx, r, 0, true); err != nil {
			q.storeError(log, "release the stopped job", err)
			return
		}
		log.Info("queue: job stopped by the shutdown, back on the queue", "error", err)
	case IsPermanent(err) || r.Attempts >= info.Tries:
		if err := q.store.Fail(sctx, r, errorText(err)); err != nil {
			q.storeError(log, "record the failed job", err)
			return
		}
		log.Error("queue: job failed", "error", err, "tries", info.Tries)
		if job != nil {
			q.failed(base, job, info, timeout, err)
		}
	default:
		d := q.backoff(jt, r.Attempts)
		if err := q.store.Release(sctx, r, d, false); err != nil {
			q.storeError(log, "release the job for a retry", err)
			return
		}
		log.Warn("queue: job failed, will retry", "error", err, "retry_in", d.Round(time.Millisecond))
	}
}

// decode decodes a payload into its job. A payload that can't be decoded
// (or whose decoding panics) is a permanent error; a job type the app
// doesn't register is an ordinary one, since a newer worker may know it.
func (q *Queue) decode(payload []byte) (env envelope, jt *jobType, job Job, err error) {
	if err = json.Unmarshal(payload, &env); err != nil {
		return env, nil, nil, Permanent(fmt.Errorf("decode the job: %w", err))
	}
	if jt = q.lookup(env.Job); jt == nil {
		return env, nil, nil, fmt.Errorf("unknown job %q: the worker's app must register it with queue.Register", env.Job)
	}
	defer func() {
		if v := recover(); v != nil {
			job, err = nil, Permanent(fmt.Errorf("decode %s: panic: %v", env.Job, v))
		}
	}()
	if job, err = jt.decode(env.Data); err != nil {
		return env, jt, nil, Permanent(fmt.Errorf("decode %s: %w", env.Job, err))
	}
	return env, jt, job, nil
}

// maxErrorText caps the error kept with a failed job.
const maxErrorText = 64 << 10

// errorText is err's message as stores can keep it: valid UTF-8, without
// NUL bytes (which PostgreSQL rejects), at most maxErrorText bytes.
func errorText(err error) string {
	s := strings.ToValidUTF8(err.Error(), "\uFFFD")
	s = strings.ReplaceAll(s, "\x00", "\uFFFD")
	if len(s) > maxErrorText {
		s = strings.ToValidUTF8(s[:maxErrorText], "") + "…"
	}
	return s
}

// storeError logs a failure to record a job's outcome.
func (q *Queue) storeError(log *slog.Logger, what string, err error) {
	if errors.Is(err, ErrLeaseLost) {
		log.Warn("queue: couldn't "+what+": its reservation had ended, so another worker may run it again", "error", err)
		return
	}
	log.Error("queue: couldn't "+what+": it will run again when its reservation ends", "error", err)
}
