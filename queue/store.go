// SPDX-License-Identifier: Apache-2.0

package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Store keeps jobs for a [Queue]: the memory and database stores are
// built in, and driver modules provide others (redis.QueueDriver()).
//
// A reserved job is leased: it is hidden from other workers until the
// lease ends, then becomes available again (at-least-once delivery if a
// worker dies). Each reservation has its own token, and Delete, Release
// and Fail act only if the job is still reserved with it, so a worker
// whose lease ran out can't remove the job from under the worker that
// reserved it next.
//
// Every method must be safe for concurrent use. The queue/queuetest
// package is a conformance suite for stores.
type Store interface {
	// Push adds a job to m.Queue, available after delay (0 for now).
	Push(ctx context.Context, m Message, delay time.Duration) error
	// Reserve leases the next available job of queue for lease, adding
	// one to its attempts. It returns nil, nil when none is available.
	// Jobs become available in order of the time they became available,
	// as far as the store's clock allows.
	Reserve(ctx context.Context, queue string, lease time.Duration) (*Reservation, error)
	// Delete removes a reserved job, once it is done. It returns
	// [ErrLeaseLost] if the job isn't reserved with r's token anymore.
	Delete(ctx context.Context, r *Reservation) error
	// Release ends a reservation: the job becomes available again after
	// delay. With refund, the attempt doesn't count (the worker stopped
	// before the job could finish). It returns [ErrLeaseLost] if the job
	// isn't reserved with r's token anymore.
	Release(ctx context.Context, r *Reservation, delay time.Duration, refund bool) error
	// Fail removes a reserved job and keeps it as failed, with the error
	// message (replacing a failed job with the same ID). It returns
	// [ErrLeaseLost], and keeps nothing, if the job isn't reserved with
	// r's token anymore.
	Fail(ctx context.Context, r *Reservation, errMsg string) error
	// Size returns the number of jobs in queue, reserved ones included.
	Size(ctx context.Context, queue string) (int64, error)
	// Clear removes every job of queue, reserved ones included, and
	// returns how many there were.
	Clear(ctx context.Context, queue string) (int64, error)

	// Failed returns up to limit failed jobs, the latest first, after
	// skipping offset of them.
	Failed(ctx context.Context, offset, limit int) ([]FailedJob, error)
	// Retry pushes the failed job id back to its queue, with no attempts,
	// and stops keeping it as failed. It reports whether there was one.
	Retry(ctx context.Context, id string) (bool, error)
	// Forget removes the failed job id, and reports whether there was one.
	Forget(ctx context.Context, id string) (bool, error)
	// Flush removes every failed job, and returns how many there were.
	Flush(ctx context.Context) (int64, error)

	// Close releases the store's resources.
	Close() error
}

// Message is a job to push.
type Message struct {
	// ID identifies the job, also once it has failed: a UUID.
	ID string
	// Queue is the name of the queue.
	Queue string
	// Payload is the job's encoding.
	Payload []byte
}

// Reservation is a job a worker has leased.
type Reservation struct {
	// ID is the job's ID.
	ID string
	// Queue is the name of its queue.
	Queue string
	// Payload is the job's encoding.
	Payload []byte
	// Attempts counts the reservations of the job, this one included.
	Attempts int
	// Token identifies this reservation to the store.
	Token string
}

// FailedJob is a job that failed for good: its last attempt failed, it
// used all its tries, or it returned a [Permanent] error.
type FailedJob struct {
	// ID is the job's ID.
	ID string
	// Queue is the queue it was on.
	Queue string
	// Payload is the job's encoding.
	Payload []byte
	// Error is the error of its last attempt.
	Error string
	// Attempts is the number of times it ran.
	Attempts int
	// FailedAt is when it failed, by the store's clock.
	FailedAt time.Time
}

// Job returns the name the job was registered with, from its payload.
func (f FailedJob) Job() string {
	var e envelope
	if json.Unmarshal(f.Payload, &e) != nil {
		return ""
	}
	return e.Job
}

// ErrLeaseLost is returned by a [Store] when a reservation has ended:
// its lease ran out (and another worker may have reserved the job), or
// the job was removed.
var ErrLeaseLost = errors.New("queue: the job's reservation has ended")
