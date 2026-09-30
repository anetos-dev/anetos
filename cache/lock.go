// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"
)

// ErrLockHeld is returned by [TryWithLock] when another holder has the
// lock.
var ErrLockHeld = errors.New("cache: lock is held")

// Lock is a named lock in the cache, held by one owner at a time across
// every process that shares the cache (with a memory store: within one
// process). It expires after its ttl, so a crashed holder can't keep it
// forever; extend it for longer work:
//
//	lock := cache.NewLock(ctx, "reports:monthly", 10*time.Minute)
//	if err := lock.Acquire(ctx); err != nil { // waits for it
//		return err
//	}
//	defer lock.Release(context.WithoutCancel(ctx)) // also when ctx is canceled
//
// A Lock value belongs to one owner; don't share it between goroutines
// that should exclude each other.
type Lock struct {
	c     *Cache
	err   error
	name  string
	key   string
	owner []byte
	ttl   time.Duration
}

// NewLock returns the lock called name, held for ttl (> 0) once acquired,
// in the cache of ctx.
func NewLock(ctx context.Context, name string, ttl time.Duration) *Lock {
	l := &Lock{name: name, ttl: ttl}
	l.c, l.err = From(ctx)
	if l.err == nil {
		l.key, l.err = l.c.key("lock:" + name)
	}
	if l.err == nil && ttl <= 0 {
		l.err = fmt.Errorf("cache: lock %s needs a positive ttl, got %s", name, ttl)
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	l.owner = []byte(base64.RawURLEncoding.EncodeToString(b))
	return l
}

// TryAcquire takes the lock if it is free, and reports whether it did.
func (l *Lock) TryAcquire(ctx context.Context) (bool, error) {
	if l.err != nil {
		return false, l.err
	}
	return l.c.store.Add(ctx, l.key, l.owner, l.ttl)
}

// Acquire waits for the lock and takes it, polling more slowly the longer
// it waits (up to a second). It returns ctx's error if ctx ends first:
//
//	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
//	defer cancel()
//	err := lock.Acquire(ctx) // context.DeadlineExceeded after 5s
func (l *Lock) Acquire(ctx context.Context) error {
	wait := 25 * time.Millisecond
	for {
		ok, err := l.TryAcquire(ctx)
		if err != nil || ok {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		wait = min(wait*2, time.Second)
	}
}

// Release frees the lock if this owner still holds it, and reports
// whether it did (false: it had expired, and may be someone else's now).
func (l *Lock) Release(ctx context.Context) (bool, error) {
	if l.err != nil {
		return false, l.err
	}
	return l.c.store.DeleteIf(ctx, l.key, l.owner)
}

// Extend sets the lock's remaining time to ttl if this owner still holds
// it, and reports whether it did.
func (l *Lock) Extend(ctx context.Context, ttl time.Duration) (bool, error) {
	if l.err != nil {
		return false, l.err
	}
	return l.c.store.ExpireIf(ctx, l.key, l.owner, ttl)
}

// Name returns the lock's name.
func (l *Lock) Name() string { return l.name }

// WithLock runs fn holding the lock called name, waiting for it as
// [Lock.Acquire] does, and releases it when fn returns.
func WithLock(ctx context.Context, name string, ttl time.Duration, fn func(ctx context.Context) error) error {
	l := NewLock(ctx, name, ttl)
	if err := l.Acquire(ctx); err != nil {
		return err
	}
	return runLocked(ctx, l, fn)
}

// TryWithLock runs fn holding the lock called name if it is free, and
// returns [ErrLockHeld] without running it otherwise: work that one
// instance at a time should do, such as a scheduled task.
func TryWithLock(ctx context.Context, name string, ttl time.Duration, fn func(ctx context.Context) error) error {
	l := NewLock(ctx, name, ttl)
	ok, err := l.TryAcquire(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return ErrLockHeld
	}
	return runLocked(ctx, l, fn)
}

func runLocked(ctx context.Context, l *Lock, fn func(ctx context.Context) error) (err error) {
	// Release even if ctx was canceled or fn panics: the lock would
	// otherwise stay held until it expires.
	defer func() {
		if _, rerr := l.Release(context.WithoutCancel(ctx)); rerr != nil {
			err = errors.Join(err, fmt.Errorf("cache: release lock %s: %w", l.name, rerr))
		}
	}()
	return fn(ctx)
}
