// SPDX-License-Identifier: Apache-2.0

package cache_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/cache/cachetest"
	"anetos.dev/anetos/cmd"
	"anetos.dev/anetos/config"
)

func TestMemoryStore(t *testing.T) {
	t.Parallel()
	cachetest.Run(t, func(*testing.T) cache.Store { return cache.NewMemoryStore() })
}

func newCtx(store cache.Store) context.Context {
	return cache.WithCache(context.Background(), cache.New(store, "t:"))
}

func TestNoCache(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if _, _, err := cache.Get[int](ctx, "k"); !errors.Is(err, cache.ErrNoCache) {
		t.Errorf("Get: %v", err)
	}
	if err := cache.Set(ctx, "k", 1, 0); !errors.Is(err, cache.ErrNoCache) {
		t.Errorf("Set: %v", err)
	}
	if _, err := cache.Remember(ctx, "k", 0, func(context.Context) (int, error) { return 1, nil }); !errors.Is(err, cache.ErrNoCache) {
		t.Errorf("Remember: %v", err)
	}
	if _, err := cache.NewLock(ctx, "l", time.Second).TryAcquire(ctx); !errors.Is(err, cache.ErrNoCache) {
		t.Errorf("lock: %v", err)
	}
}

func TestValuesAndKeys(t *testing.T) {
	t.Parallel()
	store := cache.NewMemoryStore()
	ctx := newCtx(store)

	type profile struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	want := profile{Name: "Ada", Tags: []string{"a", "b"}}
	if err := cache.Set(ctx, "p", want, time.Hour); err != nil {
		t.Fatal(err)
	}
	got, ok, err := cache.Get[profile](ctx, "p")
	if err != nil || !ok || got.Name != "Ada" || len(got.Tags) != 2 {
		t.Fatalf("Get = %+v, %v, %v", got, ok, err)
	}
	// The key is stored with the prefix.
	if _, ok, _ := store.Get(ctx, "t:p"); !ok {
		t.Error("the key isn't prefixed in the store")
	}
	if ok, err := cache.Has(ctx, "p"); !ok || err != nil {
		t.Errorf("Has = %v, %v", ok, err)
	}
	if _, _, err := cache.Get[int](ctx, "p"); err == nil || !strings.Contains(err.Error(), "isn't a int") {
		t.Errorf("Get of the wrong type: %v", err)
	}
	if _, ok, err := cache.Get[int](ctx, "missing"); ok || err != nil {
		t.Errorf("Get missing = %v, %v", ok, err)
	}
	if added, err := cache.Add(ctx, "p", 1, 0); added || err != nil {
		t.Errorf("Add existing = %v, %v", added, err)
	}
	if err := cache.Forget(ctx, "p"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := cache.Has(ctx, "p"); ok {
		t.Error("Forget left the key")
	}
	n, err := cache.Increment(ctx, "n", 5, 0)
	if err != nil || n != 5 {
		t.Fatalf("Increment = %d, %v", n, err)
	}
	if v, ok, err := cache.Get[int64](ctx, "n"); v != 5 || !ok || err != nil {
		t.Errorf("Get[int64] of a counter = %d, %v, %v", v, ok, err)
	}

	// Keys: not empty, and at most MaxKeyLen bytes with the prefix.
	if err := cache.Set(ctx, "", 1, 0); err == nil {
		t.Error("empty key accepted")
	}
	long := strings.Repeat("k", cache.MaxKeyLen-len("t:"))
	if err := cache.Set(ctx, long, 1, 0); err != nil {
		t.Errorf("key of MaxKeyLen bytes: %v", err)
	}
	if err := cache.Set(ctx, long+"k", 1, 0); err == nil {
		t.Error("key over MaxKeyLen accepted")
	}
	// Keys are text: every store accepts them alike.
	for _, k := range []string{"bad\xff", "nul\x00"} {
		if err := cache.Set(ctx, k, 1, 0); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
	// Negative ttls are mistakes; Forever is 0.
	if err := cache.Set(ctx, "k", 1, -time.Second); err == nil {
		t.Error("negative ttl accepted")
	}
	if _, err := cache.Increment(ctx, "k", 1, -time.Second); err == nil {
		t.Error("negative ttl accepted by Increment")
	}
	if err := cache.Set(ctx, "bad", func() {}, 0); err == nil {
		t.Error("unencodable value accepted")
	}

	if err := cache.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if store.Len() != 0 {
		t.Errorf("Flush left %d items", store.Len())
	}
}

func TestLockTTL(t *testing.T) {
	t.Parallel()
	ctx := newCtx(cache.NewMemoryStore())
	if _, err := cache.NewLock(ctx, "l", 0).TryAcquire(ctx); err == nil {
		t.Error("lock without a ttl accepted")
	}
	if err := cache.WithLock(ctx, "l", -time.Second, func(context.Context) error { return nil }); err == nil {
		t.Error("WithLock with a negative ttl accepted")
	}
	// fn's error comes back, and the lock is released anyway.
	boom := errors.New("boom")
	if err := cache.WithLock(ctx, "l", time.Minute, func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Errorf("WithLock = %v", err)
	}
	if ok, _ := cache.NewLock(ctx, "l", time.Minute).TryAcquire(ctx); !ok {
		t.Error("WithLock didn't release the lock after an error")
	}
}

// failing is a store whose every call fails.
type failing struct{ cache.Store }

var errDown = errors.New("store down")

func (failing) Get(context.Context, string) ([]byte, bool, error) { return nil, false, errDown }
func (failing) Set(context.Context, string, []byte, time.Duration) error {
	return errDown
}

func TestRemember(t *testing.T) {
	t.Parallel()
	ctx := newCtx(cache.NewMemoryStore())

	// Concurrent calls share one computation.
	var calls atomic.Int32
	release := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]int, 10)
	for i := range results {
		wg.Go(func() {
			v, err := cache.Remember(ctx, "k", time.Minute, func(context.Context) (int, error) {
				calls.Add(1)
				<-release
				return 42, nil
			})
			if err != nil {
				t.Error(err)
			}
			results[i] = v
		})
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	// A goroutine that started after the value was stored reads it, so at
	// most one computation happened.
	if n := calls.Load(); n != 1 {
		t.Errorf("fn ran %d times", n)
	}
	for _, v := range results {
		if v != 42 {
			t.Errorf("results = %v", results)
			break
		}
	}

	// fn's error is returned, and nothing is stored.
	boom := errors.New("boom")
	if _, err := cache.Remember(ctx, "e", time.Minute, func(context.Context) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Errorf("Remember error = %v", err)
	}
	if ok, _ := cache.Has(ctx, "e"); ok {
		t.Error("a failed computation was stored")
	}

	// A value that no longer decodes is recomputed and replaced.
	if err := cache.Set(ctx, "old", "text", time.Minute); err != nil {
		t.Fatal(err)
	}
	v, err := cache.Remember(ctx, "old", time.Minute, func(context.Context) (int, error) { return 7, nil })
	if err != nil || v != 7 {
		t.Errorf("Remember over a stale type = %d, %v", v, err)
	}
	if v, _, err := cache.Get[int](ctx, "old"); v != 7 || err != nil {
		t.Errorf("stale value not replaced: %d, %v", v, err)
	}

	// If the computing caller's context ends, a waiter computes the value
	// itself.
	leaderCtx, cancel := context.WithCancel(ctx)
	started := make(chan struct{})
	leaderDone := make(chan error)
	go func() {
		_, err := cache.Remember(leaderCtx, "c", time.Minute, func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			return 0, ctx.Err()
		})
		leaderDone <- err
	}()
	<-started
	waiter := make(chan int)
	go func() {
		v, err := cache.Remember(ctx, "c", time.Minute, func(context.Context) (int, error) { return 9, nil })
		if err != nil {
			t.Error(err)
		}
		waiter <- v
	}()
	time.Sleep(20 * time.Millisecond) // the waiter waits
	cancel()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Errorf("leader: %v", err)
	}
	if v := <-waiter; v != 9 {
		t.Errorf("waiter after the leader's cancellation = %d", v)
	}

	// fn's own timeout is its error, shared with the waiters.
	var timeouts atomic.Int32
	var wg2 sync.WaitGroup
	for range 5 {
		wg2.Go(func() {
			_, err := cache.Remember(ctx, "t", time.Minute, func(ctx context.Context) (int, error) {
				timeouts.Add(1)
				upstream, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
				<-upstream.Done()
				return 0, upstream.Err()
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("fn's timeout: %v", err)
			}
		})
	}
	wg2.Wait()
	if n := timeouts.Load(); n > 2 { // a late caller may start a second one
		t.Errorf("fn ran %d times", n)
	}

	// A panic in fn reaches its caller; waiters get an error.
	started = make(chan struct{})
	release = make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		_, _ = cache.Remember(ctx, "p", time.Minute, func(context.Context) (int, error) {
			close(started)
			<-release
			panic("boom")
		})
	}()
	<-started
	errc := make(chan error)
	go func() {
		_, err := cache.Remember(ctx, "p", time.Minute, func(context.Context) (int, error) { return 1, nil })
		errc <- err
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if err := <-errc; err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Errorf("waiter of a panicking computation: %v", err)
	}

	// A failing store degrades to computing, with warnings.
	var logs bytes.Buffer
	c := cache.New(failing{}, "")
	cache.SetLogger(c, slog.New(slog.NewTextHandler(&logs, nil)))
	down := cache.WithCache(context.Background(), c)
	v, err = cache.Remember(down, "k", time.Minute, func(context.Context) (int, error) { return 3, nil })
	if err != nil || v != 3 {
		t.Errorf("Remember with a failing store = %d, %v", v, err)
	}
	if !strings.Contains(logs.String(), "store down") {
		t.Errorf("no warning logged: %q", logs.String())
	}
}

func newApp(t *testing.T, env config.Map) *anetos.App {
	t.Helper()
	env["APP_NAME"] = "blog"
	app, err := anetos.New(anetos.WithSource(env), anetos.WithLogOutput(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func TestForApp(t *testing.T) {
	t.Parallel()
	app := newApp(t, config.Map{})
	c, err := cache.ForApp(app)
	if err != nil {
		t.Fatal(err)
	}
	if c.Prefix() != "blog:cache:" {
		t.Errorf("default prefix = %q", c.Prefix())
	}
	if _, ok := c.Store().(*cache.MemoryStore); !ok {
		t.Errorf("default store = %T", c.Store())
	}
	if got, err := anetos.Resolve[*cache.Cache](app); err != nil || got != c {
		t.Errorf("Resolve = %v, %v", got, err)
	}
	ctx := app.Context(context.Background())
	if got, err := cache.From(ctx); err != nil || got != c {
		t.Errorf("From(app context) = %v, %v", got, err)
	}
	if err := cache.Set(ctx, "a", 1, 0); err != nil {
		t.Fatal(err)
	}
	// Another app's keys in the same store are left alone by cache:clear.
	if err := c.Store().Set(ctx, "other:a", []byte("1"), 0); err != nil {
		t.Fatal(err)
	}

	var clear cmd.Command
	for _, k := range app.Commands() {
		if k.Name == "cache:clear" {
			clear = k
		}
	}
	if clear.Run == nil {
		t.Fatal("no cache:clear command")
	}
	var out bytes.Buffer
	if err := clear.Run(ctx, &cmd.Args{Name: "cache:clear", Args: []string{"x"}, Stdout: &out, Stderr: &out}); !errors.Is(err, cmd.ErrUsage) {
		t.Errorf("cache:clear x = %v", err)
	}
	if err := clear.Run(ctx, &cmd.Args{Name: "cache:clear", Stdout: &out, Stderr: &out}); err != nil {
		t.Fatal(err)
	}
	if want := `Cleared the memory cache (keys starting with "blog:cache:").`; !strings.Contains(out.String(), want) {
		t.Errorf("output = %q", out.String())
	}
	if ok, _ := cache.Has(ctx, "a"); ok {
		t.Error("cache:clear left the app's key")
	}
	if _, ok, _ := c.Store().Get(ctx, "other:a"); !ok {
		t.Error("cache:clear removed another prefix's key")
	}
}

func TestForAppConfig(t *testing.T) {
	t.Parallel()
	app := newApp(t, config.Map{"CACHE_STORE": "redis"})
	_, err := cache.ForApp(app)
	if err == nil || !strings.Contains(err.Error(), "[memory, database]") || !strings.Contains(err.Error(), "redis.CacheDriver()") {
		t.Errorf("unknown store: %v", err)
	}

	// A driver passed to ForApp is selected by its name, with the settings.
	var got cache.Config
	custom := cache.Driver{Name: "custom", Open: func(_ *anetos.App, cfg cache.Config) (cache.Store, error) {
		got = cfg
		return cache.NewMemoryStore(), nil
	}}
	app = newApp(t, config.Map{"CACHE_STORE": "custom", "CACHE_PREFIX": "x/"})
	c, err := cache.ForApp(app, custom)
	if err != nil {
		t.Fatal(err)
	}
	if c.Prefix() != "x/" || got.Store != "custom" || got.Table != "cache" {
		t.Errorf("prefix %q, config %+v", c.Prefix(), got)
	}

	// A driver's error is reported.
	failingDriver := cache.Driver{Name: "custom", Open: func(*anetos.App, cache.Config) (cache.Store, error) {
		return nil, errDown
	}}
	app = newApp(t, config.Map{"CACHE_STORE": "custom"})
	if _, err := cache.ForApp(app, failingDriver); !errors.Is(err, errDown) || !strings.Contains(err.Error(), "open the custom store") {
		t.Errorf("driver error: %v", err)
	}

	// The database store needs the app's database.
	app = newApp(t, config.Map{"CACHE_STORE": "database"})
	if _, err := cache.ForApp(app); err == nil || !strings.Contains(err.Error(), "db.Connect") {
		t.Errorf("database store without a database: %v", err)
	}
}
