// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Forever is the ttl of items that never expire.
const Forever time.Duration = 0

// MaxKeyLen is the longest key, prefix included, every store accepts.
const MaxKeyLen = 250

// ErrNoCache is returned when the context has no cache: the app didn't
// call [New], or the context didn't come from the app.
var ErrNoCache = errors.New("cache: no cache in context (call cache.New while setting up the app, or use cache.WithCache)")

// Store is a cache backend: memory, database, Redis. Keys arrive with the
// cache's prefix. A ttl of [Forever] (0) means no expiry; expired items
// behave as if absent in every method. Stores are safe for concurrent use.
type Store interface {
	// Get returns the value of key, and whether it was there.
	Get(ctx context.Context, key string) ([]byte, bool, error)
	// Set stores value under key.
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	// Add stores value under key only if the key is absent, atomically,
	// and reports whether it did.
	Add(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// Replace stores value under key only if the key is there, atomically,
	// and reports whether it did. Sessions are saved with it, so a session
	// removed meanwhile (logged out) isn't written back.
	Replace(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// Delete removes key; a missing key is not an error.
	Delete(ctx context.Context, key string) error
	// Increment adds delta to the integer stored under key, atomically,
	// and returns the new value. A missing key starts at 0 and gets ttl;
	// an existing one keeps its expiry. The value is stored as decimal
	// text.
	Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error)
	// DeleteIf removes key if it holds value, atomically, and reports
	// whether it did. Locks are released with it.
	DeleteIf(ctx context.Context, key string, value []byte) (bool, error)
	// ExpireIf sets the ttl (> 0) of key if it holds value, atomically,
	// and reports whether it did. Locks are extended with it.
	ExpireIf(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error)
	// Flush removes every key that starts with prefix ("": every key of
	// the store).
	Flush(ctx context.Context, prefix string) error
	// Close releases the store's resources.
	Close() error
}

// Cache stores values of any type, encoded as JSON, in a [Store] under a
// key prefix. Handlers reach it through their context: [New] adds it
// to every context the app creates, and the package functions ([Get],
// [Set], [Remember], …) find it there. A Cache is safe for concurrent use.
type Cache struct {
	store  Store
	prefix string
	log    *slog.Logger

	mu     sync.Mutex
	flight map[string]*call // Remember calls in progress, by key
}

type call struct {
	done chan struct{}
	val  []byte
	err  error
	gone bool // the computing caller's context ended
}

// NewWithStore returns a Cache over store, with every key prefixed by prefix
// ("blog:").
func NewWithStore(store Store, prefix string) *Cache {
	return &Cache{store: store, prefix: prefix, log: slog.Default(), flight: map[string]*call{}}
}

// Store returns the cache's store.
func (c *Cache) Store() Store { return c.store }

// Prefix returns the prefix of the cache's keys.
func (c *Cache) Prefix() string { return c.prefix }

func (c *Cache) key(key string) (string, error) {
	if key == "" {
		return "", errors.New("cache: empty key")
	}
	k := c.prefix + key
	if !utf8.ValidString(k) || strings.ContainsRune(k, 0) {
		return "", fmt.Errorf("cache: key %q isn't valid UTF-8 text (without NUL)", k)
	}
	if len(k) > MaxKeyLen {
		return "", fmt.Errorf("cache: key %q is longer than %d bytes with its prefix", key, MaxKeyLen)
	}
	return k, nil
}

type cacheKey struct{}

// WithCache returns ctx with c as the cache the package functions use.
func WithCache(ctx context.Context, c *Cache) context.Context {
	return context.WithValue(ctx, cacheKey{}, c)
}

// From returns the cache in ctx, or [ErrNoCache].
func From(ctx context.Context) (*Cache, error) {
	if c, ok := ctx.Value(cacheKey{}).(*Cache); ok && c != nil {
		return c, nil
	}
	return nil, ErrNoCache
}

func checkTTL(ttl time.Duration) error {
	if ttl < 0 {
		return fmt.Errorf("cache: negative ttl %s (use cache.Forever for no expiry)", ttl)
	}
	return nil
}

// Get returns the value stored under key, decoded as a T, and whether it
// was there:
//
//	profile, ok, err := cache.Get[Profile](ctx, "profile:7")
func Get[T any](ctx context.Context, key string) (T, bool, error) {
	var v T
	c, err := From(ctx)
	if err != nil {
		return v, false, err
	}
	return getFrom[T](ctx, c, key)
}

func getFrom[T any](ctx context.Context, c *Cache, key string) (T, bool, error) {
	var v T
	k, err := c.key(key)
	if err != nil {
		return v, false, err
	}
	b, ok, err := c.store.Get(ctx, k)
	if err != nil || !ok {
		return v, false, err
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return v, false, fmt.Errorf("cache: %s holds something that isn't a %T: %w", key, v, err)
	}
	return v, true, nil
}

// Has reports whether key is in the cache.
func Has(ctx context.Context, key string) (bool, error) {
	c, err := From(ctx)
	if err != nil {
		return false, err
	}
	k, err := c.key(key)
	if err != nil {
		return false, err
	}
	_, ok, err := c.store.Get(ctx, k)
	return ok, err
}

// Set stores v, encoded as JSON, under key for ttl ([Forever]: no
// expiry):
//
//	err := cache.Set(ctx, "profile:7", profile, time.Hour)
func Set(ctx context.Context, key string, v any, ttl time.Duration) error {
	c, err := From(ctx)
	if err != nil {
		return err
	}
	return c.set(ctx, key, v, ttl)
}

func (c *Cache) set(ctx context.Context, key string, v any, ttl time.Duration) error {
	k, err := c.key(key)
	if err != nil {
		return err
	}
	if err := checkTTL(ttl); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cache: encode %s: %w", key, err)
	}
	return c.store.Set(ctx, k, b, ttl)
}

// Add stores v under key only if the key isn't in the cache, and reports
// whether it did. Only one of several concurrent Adds of a key succeeds.
func Add(ctx context.Context, key string, v any, ttl time.Duration) (bool, error) {
	c, err := From(ctx)
	if err != nil {
		return false, err
	}
	k, err := c.key(key)
	if err != nil {
		return false, err
	}
	if err := checkTTL(ttl); err != nil {
		return false, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return false, fmt.Errorf("cache: encode %s: %w", key, err)
	}
	return c.store.Add(ctx, k, b, ttl)
}

// Delete removes key from the cache.
func Delete(ctx context.Context, key string) error {
	c, err := From(ctx)
	if err != nil {
		return err
	}
	k, err := c.key(key)
	if err != nil {
		return err
	}
	return c.store.Delete(ctx, k)
}

// Forget is [Delete].
//
// Deprecated: Use Delete; Forget is removed in v0.6.
//
//go:fix inline
func Forget(ctx context.Context, key string) error { return Delete(ctx, key) }

// Increment adds delta (negative to decrement) to the counter under key
// and returns its new value. A new counter starts at 0 and expires after
// ttl ([Forever]: never); incrementing doesn't extend it, which makes
// windows for rate limits:
//
//	n, err := cache.Increment(ctx, "logins:"+ip, 1, time.Minute)
//
// Read a counter with Get[int64].
func Increment(ctx context.Context, key string, delta int64, ttl time.Duration) (int64, error) {
	c, err := From(ctx)
	if err != nil {
		return 0, err
	}
	k, err := c.key(key)
	if err != nil {
		return 0, err
	}
	if err := checkTTL(ttl); err != nil {
		return 0, err
	}
	return c.store.Increment(ctx, k, delta, ttl)
}

// Remember returns the value under key, or computes it with fn, stores it
// for ttl and returns it:
//
//	stats, err := cache.Remember(ctx, "stats", 10*time.Minute, func(ctx context.Context) (Stats, error) {
//		return computeStats(ctx)
//	})
//
// Concurrent calls for one key in this process wait for a single fn call
// (if its caller's context ends first, a waiter computes the value
// itself). fn's error is returned and nothing is stored. The cache is an
// optimization here: if the store fails, or holds a value that no longer
// decodes as a T (after a deploy changed the type), Remember logs it and
// uses fn.
func Remember[T any](ctx context.Context, key string, ttl time.Duration, fn func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	c, err := From(ctx)
	if err != nil {
		return zero, err
	}
	if _, err := c.key(key); err != nil {
		return zero, err
	}
	if err := checkTTL(ttl); err != nil {
		return zero, err
	}
	for {
		v, ok := rememberGet[T](ctx, c, key)
		if ok {
			return v, nil
		}

		// One computation per key in this process.
		c.mu.Lock()
		cl, busy := c.flight[key]
		if !busy {
			cl = &call{done: make(chan struct{})}
			c.flight[key] = cl
		}
		c.mu.Unlock()
		if !busy {
			return rememberCompute(ctx, c, cl, key, ttl, fn)
		}
		select {
		case <-cl.done:
		case <-ctx.Done():
			return zero, ctx.Err()
		}
		switch {
		case cl.err == nil:
			var out T
			if err := json.Unmarshal(cl.val, &out); err == nil {
				return out, nil
			}
			// Another caller's T: compute this one's.
			return fn(ctx)
		case cl.gone:
			// The computing caller's context ended; this one's may not
			// have: try again.
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
		default:
			return zero, cl.err
		}
	}
}

// rememberGet reads key for Remember, logging failures.
func rememberGet[T any](ctx context.Context, c *Cache, key string) (T, bool) {
	v, ok, err := getFrom[T](ctx, c, key)
	if err != nil {
		c.log.Warn("cache: Remember: reading failed; computing the value", "key", key, "error", err)
	}
	return v, ok && err == nil
}

// rememberCompute calls fn as the one computation of key in progress,
// stores its result and hands it to the callers waiting on cl.
func rememberCompute[T any](ctx context.Context, c *Cache, cl *call, key string, ttl time.Duration, fn func(ctx context.Context) (T, error)) (v T, err error) {
	done := false
	defer func() {
		if !done { // fn panicked
			cl.err = fmt.Errorf("cache: Remember %s: the computation panicked", key)
		}
		c.mu.Lock()
		delete(c.flight, key)
		c.mu.Unlock()
		close(cl.done)
	}()
	// A computation that finished since this caller's read has stored it.
	if v, ok := rememberGet[T](ctx, c, key); ok {
		cl.val, cl.err = json.Marshal(v)
		done = true
		return v, nil
	}
	v, err = fn(ctx)
	done = true
	if err != nil {
		cl.err = err
		cl.gone = ctx.Err() != nil
		var zero T
		return zero, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		cl.err = fmt.Errorf("cache: encode %s: %w", key, err)
		var zero T
		return zero, cl.err
	}
	cl.val = b
	k, _ := c.key(key)
	if err := c.store.Set(ctx, k, b, ttl); err != nil {
		c.log.Warn("cache: Remember: storing failed", "key", key, "error", err)
	}
	return v, nil
}

// Flush removes every key of the cache (those with its prefix; with no
// prefix, everything in the store).
func Flush(ctx context.Context) error {
	c, err := From(ctx)
	if err != nil {
		return err
	}
	return c.store.Flush(ctx, c.prefix)
}
