// SPDX-License-Identifier: Apache-2.0

// Package cachetest is a conformance suite for cache stores. Each store
// runs it:
//
//	func TestStore(t *testing.T) {
//		cachetest.Run(t, func(t *testing.T) cache.Store { return newStore(t) })
//	}
//
// newStore returns an empty store for each test, or one whose keys don't
// matter: the suite uses keys under a random prefix and flushes it.
package cachetest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos/cache"
)

// ttl is short so expiry tests are quick; wait is comfortably longer.
const (
	ttl  = 300 * time.Millisecond
	wait = 700 * time.Millisecond
)

type test struct {
	name string
	fn   func(t *testing.T, ctx context.Context, s cache.Store, p string)
}

// Run runs the suite against the stores newStore returns.
func Run(t *testing.T, newStore func(t *testing.T) cache.Store) {
	t.Helper()
	for _, tt := range []test{
		{"GetSet", testGetSet},
		{"Expiry", testExpiry},
		{"Add", testAdd},
		{"Increment", testIncrement},
		{"CompareAndDelete", testCompareAndDelete},
		{"Flush", testFlush},
		{"Concurrency", testConcurrency},
		{"Values", testValues},
		{"Locks", testLocks},
		{"Remember", testRemember},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			b := make([]byte, 6)
			_, _ = rand.Read(b)
			p := "ct" + hex.EncodeToString(b) + ":"
			ctx := t.Context()
			t.Cleanup(func() { _ = s.Flush(context.WithoutCancel(ctx), p) })
			tt.fn(t, ctx, s, p)
		})
	}
}

func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, ctx context.Context, s cache.Store, key string) (string, bool) {
	t.Helper()
	v, ok, err := s.Get(ctx, key)
	check(t, err)
	return string(v), ok
}

func testGetSet(t *testing.T, ctx context.Context, s cache.Store, p string) {
	if _, ok := get(t, ctx, s, p+"missing"); ok {
		t.Error("missing key found")
	}
	check(t, s.Set(ctx, p+"a", []byte("one"), cache.Forever))
	check(t, s.Set(ctx, p+"a", []byte("two"), cache.Forever)) // overwrite
	if v, ok := get(t, ctx, s, p+"a"); !ok || v != "two" {
		t.Errorf("Get = %q %v", v, ok)
	}
	check(t, s.Set(ctx, p+"empty", []byte{}, cache.Forever))
	if v, ok := get(t, ctx, s, p+"empty"); !ok || v != "" {
		t.Errorf("empty value = %q %v", v, ok)
	}
	check(t, s.Delete(ctx, p+"a"))
	check(t, s.Delete(ctx, p+"a")) // twice is fine
	if _, ok := get(t, ctx, s, p+"a"); ok {
		t.Error("deleted key found")
	}
	// Keys are exact: case and punctuation matter.
	check(t, s.Set(ctx, p+"Key", []byte("upper"), cache.Forever))
	check(t, s.Set(ctx, p+"key", []byte("lower"), cache.Forever))
	check(t, s.Set(ctx, p+"kéy", []byte("accent"), cache.Forever))
	const spaced = "key " // no padding
	check(t, s.Set(ctx, p+spaced, []byte("space"), cache.Forever))
	for k, want := range map[string]string{"Key": "upper", "key": "lower", "kéy": "accent", spaced: "space"} {
		if v, _ := get(t, ctx, s, p+k); v != want {
			t.Errorf("%s = %q, want %q", k, v, want)
		}
	}
}

func testExpiry(t *testing.T, ctx context.Context, s cache.Store, p string) {
	check(t, s.Set(ctx, p+"short", []byte("x"), ttl))
	check(t, s.Set(ctx, p+"long", []byte("y"), time.Hour))
	check(t, s.Set(ctx, p+"forever", []byte("z"), cache.Forever))
	if _, ok := get(t, ctx, s, p+"short"); !ok {
		t.Fatal("not there before its ttl")
	}
	time.Sleep(wait)
	if _, ok := get(t, ctx, s, p+"short"); ok {
		t.Error("there after its ttl")
	}
	for _, k := range []string{"long", "forever"} {
		if _, ok := get(t, ctx, s, p+k); !ok {
			t.Errorf("%s expired", k)
		}
	}
	// Set replaces the ttl.
	check(t, s.Set(ctx, p+"long", []byte("y"), ttl))
	time.Sleep(wait)
	if _, ok := get(t, ctx, s, p+"long"); ok {
		t.Error("Set didn't replace the ttl")
	}
}

func testAdd(t *testing.T, ctx context.Context, s cache.Store, p string) {
	ok, err := s.Add(ctx, p+"k", []byte("first"), cache.Forever)
	check(t, err)
	if !ok {
		t.Fatal("Add of a new key failed")
	}
	ok, err = s.Add(ctx, p+"k", []byte("second"), cache.Forever)
	check(t, err)
	if v, _ := get(t, ctx, s, p+"k"); ok || v != "first" {
		t.Errorf("Add over an existing key: %v, value %q", ok, v)
	}
	// An expired key can be added again.
	ok, err = s.Add(ctx, p+"e", []byte("old"), ttl)
	check(t, err)
	time.Sleep(wait)
	ok2, err := s.Add(ctx, p+"e", []byte("new"), cache.Forever)
	check(t, err)
	if v, _ := get(t, ctx, s, p+"e"); !ok || !ok2 || v != "new" {
		t.Errorf("Add over an expired key: %v %v %q", ok, ok2, v)
	}
}

func testIncrement(t *testing.T, ctx context.Context, s cache.Store, p string) {
	n, err := s.Increment(ctx, p+"c", 5, cache.Forever)
	check(t, err)
	if n != 5 {
		t.Errorf("new counter = %d", n)
	}
	if n, _ = s.Increment(ctx, p+"c", -7, cache.Forever); n != -2 {
		t.Errorf("after -7 = %d", n)
	}
	if v, _ := get(t, ctx, s, p+"c"); v != "-2" {
		t.Errorf("stored as %q", v)
	}
	// A counter set as text (as Set of a JSON number stores it) increments.
	check(t, s.Set(ctx, p+"s", []byte("40"), cache.Forever))
	if n, _ = s.Increment(ctx, p+"s", 2, cache.Forever); n != 42 {
		t.Errorf("counter from Set = %d", n)
	}
	for name, v := range map[string]string{"string": `"abc"`, "float": "1.5", "padded": " 1", "plus": "+1", "zeros": "01", "big": "99999999999999999999"} {
		check(t, s.Set(ctx, p+name, []byte(v), cache.Forever))
		if _, err := s.Increment(ctx, p+name, 1, cache.Forever); err == nil {
			t.Errorf("incrementing %s (%s): no error", name, v)
		}
		if got, _ := get(t, ctx, s, p+name); got != v {
			t.Errorf("%s changed to %q", name, got)
		}
	}
	// Overflow is an error, and leaves the counter.
	check(t, s.Set(ctx, p+"max", []byte("9223372036854775806"), cache.Forever))
	if n, err := s.Increment(ctx, p+"max", 1, cache.Forever); err != nil || n != math.MaxInt64 {
		t.Errorf("up to MaxInt64 = %d, %v", n, err)
	}
	if _, err := s.Increment(ctx, p+"max", 1, cache.Forever); err == nil {
		t.Error("overflow: no error")
	}
	check(t, s.Set(ctx, p+"min", []byte("-9223372036854775808"), cache.Forever))
	if _, err := s.Increment(ctx, p+"min", -1, cache.Forever); err == nil {
		t.Error("negative overflow: no error")
	}
	if v, _ := get(t, ctx, s, p+"max"); v != "9223372036854775807" {
		t.Errorf("after overflow: %s", v)
	}
	// The ttl applies to a new counter and isn't extended by increments.
	_, err = s.Increment(ctx, p+"w", 1, ttl)
	check(t, err)
	time.Sleep(ttl / 2)
	if n, _ = s.Increment(ctx, p+"w", 1, time.Hour); n != 2 {
		t.Errorf("second increment = %d", n)
	}
	time.Sleep(wait)
	if n, _ = s.Increment(ctx, p+"w", 1, ttl); n != 1 {
		t.Errorf("after the window expired = %d, want a new counter", n)
	}
}

func testCompareAndDelete(t *testing.T, ctx context.Context, s cache.Store, p string) {
	check(t, s.Set(ctx, p+"l", []byte("me"), cache.Forever))
	ok, err := s.DeleteIf(ctx, p+"l", []byte("you"))
	check(t, err)
	if ok {
		t.Error("DeleteIf with another value deleted")
	}
	ok, err = s.ExpireIf(ctx, p+"l", []byte("you"), ttl)
	check(t, err)
	if ok {
		t.Error("ExpireIf with another value changed the ttl")
	}
	ok, err = s.ExpireIf(ctx, p+"l", []byte("me"), ttl)
	check(t, err)
	if !ok {
		t.Error("ExpireIf with the value failed")
	}
	time.Sleep(wait)
	if _, ok := get(t, ctx, s, p+"l"); ok {
		t.Error("ExpireIf didn't set the ttl")
	}
	ok, err = s.DeleteIf(ctx, p+"l", []byte("me"))
	check(t, err)
	if ok {
		t.Error("DeleteIf of an expired key succeeded")
	}
	check(t, s.Set(ctx, p+"m", []byte("me"), cache.Forever))
	ok, err = s.DeleteIf(ctx, p+"m", []byte("me"))
	check(t, err)
	if _, there := get(t, ctx, s, p+"m"); !ok || there {
		t.Errorf("DeleteIf with the value: %v, still there: %v", ok, there)
	}
	if ok, _ := s.ExpireIf(ctx, p+"gone", []byte("me"), ttl); ok {
		t.Error("ExpireIf of a missing key succeeded")
	}
}

func testFlush(t *testing.T, ctx context.Context, s cache.Store, p string) {
	other := p[:len(p)-1] + "x:" // shares all but the last characters
	for _, k := range []string{p + "a", p + "b:c", p + "*?[x]", other + "a"} {
		check(t, s.Set(ctx, k, []byte("v"), cache.Forever))
	}
	check(t, s.Flush(ctx, p))
	for _, k := range []string{p + "a", p + "b:c", p + "*?[x]"} {
		if _, ok := get(t, ctx, s, k); ok {
			t.Errorf("%s survived Flush", k)
		}
	}
	if _, ok := get(t, ctx, s, other+"a"); !ok {
		t.Error("Flush removed a key with another prefix")
	}
	check(t, s.Flush(ctx, other))
	// A prefix with glob and LIKE characters is taken literally.
	check(t, s.Set(ctx, p+"%_*", []byte("v"), cache.Forever))
	check(t, s.Set(ctx, p+"%_*x", []byte("v"), cache.Forever))
	check(t, s.Set(ctx, p+"ab", []byte("v"), cache.Forever))
	check(t, s.Flush(ctx, p+"%_*"))
	if _, ok := get(t, ctx, s, p+"ab"); !ok {
		t.Error("Flush with special characters removed another key")
	}
	// Trailing spaces and multibyte characters count.
	check(t, s.Set(ctx, p+"sp", []byte("v"), cache.Forever))
	check(t, s.Flush(ctx, p+"sp "))
	if _, ok := get(t, ctx, s, p+"sp"); !ok {
		t.Error(`Flush("sp ") removed "sp"`)
	}
	check(t, s.Set(ctx, p+"é1", []byte("v"), cache.Forever))
	check(t, s.Set(ctx, p+"e1", []byte("v"), cache.Forever))
	check(t, s.Flush(ctx, p+"é"))
	if _, ok := get(t, ctx, s, p+"é1"); ok {
		t.Error(`Flush("é") left "é1"`)
	}
	if _, ok := get(t, ctx, s, p+"e1"); !ok {
		t.Error(`Flush("é") removed "e1"`)
	}
}

func testConcurrency(t *testing.T, ctx context.Context, s cache.Store, p string) {
	const n, rounds = 20, 10
	var wg sync.WaitGroup
	var added atomic.Int32
	errs := make(chan error, (rounds+2)*n)
	for i := range n {
		wg.Go(func() {
			for range rounds {
				if _, err := s.Increment(ctx, p+"hits", 1, cache.Forever); err != nil {
					errs <- err
				}
			}
			ok, err := s.Add(ctx, p+"once", []byte(fmt.Sprint(i)), cache.Forever)
			if err != nil {
				errs <- err
			}
			if ok {
				added.Add(1)
			}
			if err := s.Set(ctx, p+fmt.Sprintf("k%d", i), []byte("v"), cache.Forever); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if v, _ := get(t, ctx, s, p+"hits"); v != fmt.Sprint(n*rounds) {
		t.Errorf("hits = %s, want %d", v, n*rounds)
	}
	if added.Load() != 1 {
		t.Errorf("%d Adds of one key succeeded", added.Load())
	}

	// Adds racing on keys that expire: no errors, and never two holders.
	var holders, maxHolders atomic.Int32
	errs = make(chan error, n*rounds)
	for range n {
		wg.Go(func() {
			for range rounds {
				ok, err := s.Add(ctx, p+"lease", []byte("x"), 30*time.Millisecond)
				if err != nil {
					errs <- err
					continue
				}
				if ok {
					h := holders.Add(1)
					for m := maxHolders.Load(); h > m && !maxHolders.CompareAndSwap(m, h); m = maxHolders.Load() {
					}
					time.Sleep(time.Millisecond)
					holders.Add(-1)
				}
				time.Sleep(5 * time.Millisecond)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if m := maxHolders.Load(); m > 1 {
		t.Errorf("%d holders of one lease at once", m)
	}
}

func testValues(t *testing.T, ctx context.Context, s cache.Store, p string) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	big := bytes.Repeat([]byte("0123456789abcdef"), 64<<10) // 1 MiB
	for name, v := range map[string][]byte{"binary": all, "big": big, "utf8": []byte("héllo, 世界")} {
		check(t, s.Set(ctx, p+name, v, cache.Forever))
		got, ok, err := s.Get(ctx, p+name)
		check(t, err)
		if !ok || !bytes.Equal(got, v) {
			t.Errorf("%s value changed (%d bytes back, %d sent)", name, len(got), len(v))
		}
	}
	long := p + string(bytes.Repeat([]byte("k"), cache.MaxKeyLen-len(p)))
	check(t, s.Set(ctx, long, []byte("v"), cache.Forever))
	if _, ok := get(t, ctx, s, long); !ok {
		t.Errorf("a %d-byte key isn't kept", len(long))
	}
}

// testLocks runs the lock functions of the cache package on the store.
func testLocks(t *testing.T, ctx context.Context, s cache.Store, p string) {
	ctx = cache.WithCache(ctx, cache.New(s, p))
	a := cache.NewLock(ctx, "job", ttl)
	b := cache.NewLock(ctx, "job", ttl)
	ok, err := a.TryAcquire(ctx)
	check(t, err)
	if !ok {
		t.Fatal("first TryAcquire failed")
	}
	if ok, _ := b.TryAcquire(ctx); ok {
		t.Error("second owner acquired a held lock")
	}
	if ok, _ := b.Release(ctx); ok {
		t.Error("another owner released the lock")
	}
	if ok, _ := a.Extend(ctx, 2*wait); !ok {
		t.Error("Extend failed")
	}
	time.Sleep(wait) // past the original ttl, within the extension
	if ok, _ := b.TryAcquire(ctx); ok {
		t.Error("acquired an extended lock")
	}
	if ok, _ := a.Release(ctx); !ok {
		t.Error("Release failed")
	}

	// Acquire waits for the holder.
	if ok, _ := a.TryAcquire(ctx); !ok {
		t.Fatal("re-acquire failed")
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = a.Release(context.WithoutCancel(ctx))
	}()
	start := time.Now()
	check(t, b.Acquire(ctx))
	if time.Since(start) < 50*time.Millisecond {
		t.Error("Acquire didn't wait")
	}
	// An expired lock is free again, and its old owner can't release it.
	time.Sleep(wait)
	if ok, _ := a.TryAcquire(ctx); !ok {
		t.Error("expired lock not free")
	}
	if ok, _ := b.Release(ctx); ok {
		t.Error("old owner released a lock it lost")
	}
	// Acquire gives up with the context.
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := b.Acquire(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Acquire past its deadline: %v", err)
	}
	_, _ = a.Release(ctx)

	// TryWithLock and WithLock.
	ran := false
	check(t, cache.TryWithLock(ctx, "once", time.Minute, func(ctx context.Context) error {
		ran = true
		if err := cache.TryWithLock(ctx, "once", time.Minute, func(context.Context) error { return nil }); !errors.Is(err, cache.ErrLockHeld) {
			t.Errorf("nested TryWithLock: %v", err)
		}
		return nil
	}))
	if !ran {
		t.Error("TryWithLock didn't run")
	}
	var mu sync.Mutex
	inside, most, runs := 0, 0, 0
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			for range 3 {
				err := cache.WithLock(ctx, "serial", time.Minute, func(context.Context) error {
					mu.Lock()
					inside++
					runs++
					most = max(most, inside)
					mu.Unlock()
					time.Sleep(2 * time.Millisecond)
					mu.Lock()
					inside--
					mu.Unlock()
					return nil
				})
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if most != 1 || runs != 30 {
		t.Errorf("%d holders at once, %d runs", most, runs)
	}

	// A panic in the locked function releases the lock.
	func() {
		defer func() { _ = recover() }()
		_ = cache.WithLock(ctx, "panics", time.Minute, func(context.Context) error { panic("boom") })
	}()
	if err := cache.TryWithLock(ctx, "panics", time.Minute, func(context.Context) error { return nil }); err != nil {
		t.Errorf("after a panic: %v", err)
	}
}

// testRemember runs Remember on the store.
func testRemember(t *testing.T, ctx context.Context, s cache.Store, p string) {
	ctx = cache.WithCache(ctx, cache.New(s, p))
	type stats struct {
		Posts int `json:"posts"`
	}
	calls := 0
	compute := func(context.Context) (stats, error) {
		calls++
		return stats{Posts: 42}, nil
	}
	for range 3 {
		v, err := cache.Remember(ctx, "stats", time.Minute, compute)
		check(t, err)
		if v.Posts != 42 {
			t.Errorf("Remember = %+v", v)
		}
	}
	if calls != 1 {
		t.Errorf("computed %d times", calls)
	}
	got, ok, err := cache.Get[stats](ctx, "stats")
	check(t, err)
	if !ok || got.Posts != 42 {
		t.Errorf("Get after Remember = %+v %v", got, ok)
	}
}
