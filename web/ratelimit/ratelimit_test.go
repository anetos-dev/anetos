// SPDX-License-Identifier: Apache-2.0

package ratelimit

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
)

// fixedClock stops the clock in the middle of a minute for the test.
func fixedClock(t *testing.T) {
	at := time.Date(2026, 1, 1, 12, 0, 10, 0, time.UTC)
	now = func(context.Context) time.Time { return at }
	t.Cleanup(func() { now = anetos.Now })
}

func withCache(store cache.Store) context.Context {
	return cache.WithCache(context.Background(), cache.NewWithStore(store, "t:"))
}

func TestAllow(t *testing.T) {
	ctx := withCache(cache.NewMemoryStore())
	now := time.Date(2026, 1, 1, 12, 0, 10, 0, time.UTC)
	l := PerMinute(3)
	for i := range 5 {
		res, err := allow(ctx, "login:ada", l, 1, now)
		if err != nil {
			t.Fatal(err)
		}
		if res.Allowed != (i < 3) || res.Remaining != max(2-i, 0) || res.Limit != 3 {
			t.Errorf("hit %d: %+v", i+1, res)
		}
		if !res.Reset.Equal(time.Date(2026, 1, 1, 12, 1, 0, 0, time.UTC)) {
			t.Errorf("reset %v", res.Reset)
		}
	}
	// Another key, another limit and the next window count separately.
	if res, _ := allow(ctx, "login:bob", l, 1, now); !res.Allowed {
		t.Error("another key blocked")
	}
	if res, _ := allow(ctx, "login:ada", PerHour(10), 1, now); !res.Allowed {
		t.Error("another limit blocked")
	}
	if res, _ := allow(ctx, "login:ada", l, 1, now.Add(50*time.Second)); !res.Allowed || res.Remaining != 2 {
		t.Errorf("next window: %+v", res)
	}
	// Long and binary keys are hashed to fit the cache.
	for _, k := range []string{strings.Repeat("k", 500), "bad\xff"} {
		if _, err := allow(ctx, k, l, 1, now); err != nil {
			t.Errorf("key %.20q: %v", k, err)
		}
	}
	if _, err := Allow(ctx, "x", Limit{Max: 1}); err == nil {
		t.Error("limit without a window accepted")
	}
	if _, err := Allow(context.Background(), "x", l); !errors.Is(err, cache.ErrNoCache) {
		t.Errorf("no cache: %v", err)
	}
}

func TestClear(t *testing.T) {
	fixedClock(t)
	ctx := withCache(cache.NewMemoryStore())
	l := PerMinute(1)
	if _, err := Allow(ctx, "k", l); err != nil {
		t.Fatal(err)
	}
	if res, _ := Allow(ctx, "k", l); res.Allowed {
		t.Fatal("second hit allowed")
	}
	if err := Clear(ctx, "k", l); err != nil {
		t.Fatal(err)
	}
	if res, _ := Allow(ctx, "k", l); !res.Allowed {
		t.Error("hit after Clear blocked")
	}
}

func TestMiddleware(t *testing.T) {
	fixedClock(t)
	store := cache.NewMemoryStore()
	h := Middleware("api", PerMinute(2), PerHour(100))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	do := func(remote string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(withCache(store), http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := range 2 {
		w := do("192.0.2.1:1234")
		if w.Code != http.StatusNoContent || w.Header().Get("X-RateLimit-Limit") != "2" || w.Header().Get("X-RateLimit-Remaining") != strconv.Itoa(1-i) {
			t.Errorf("request %d: %d %v", i+1, w.Code, w.Header())
		}
	}
	w := do("192.0.2.1:5678")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("X-RateLimit-Remaining") != "0" {
		t.Errorf("third request: %d %v", w.Code, w.Header())
	}
	if w.Header().Get("Retry-After") != "50" {
		t.Errorf("Retry-After %q", w.Header().Get("Retry-After"))
	}
	if w.Header().Get("X-RateLimit-Reset") == "" {
		t.Error("no X-RateLimit-Reset")
	}
	if do("192.0.2.2:1234").Code != http.StatusNoContent {
		t.Error("another client blocked")
	}
	// IPv6 clients are counted by /64.
	do("[2001:db8::1]:1")
	do("[2001:db8::2]:1")
	if do("[2001:db8::3]:1").Code != http.StatusTooManyRequests {
		t.Error("IPv6 addresses in one /64 counted apart")
	}
	if do("[2001:db8:0:1::1]:1").Code != http.StatusNoContent {
		t.Error("another /64 blocked")
	}

	// No cache: the request fails rather than go through unlimited.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("without a cache: %d", w.Code)
	}
}

func TestMiddlewareBy(t *testing.T) {
	fixedClock(t)
	store := cache.NewMemoryStore()
	byUser := PerMinute(1).By(func(r *http.Request) string { return r.Header.Get("X-User") })
	h := Middleware("user", byUser)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	do := func(user string) int {
		r := httptest.NewRequestWithContext(withCache(store), http.MethodGet, "/", nil)
		r.Header.Set("X-User", user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if do("ada") != 200 || do("ada") != 429 || do("bob") != 200 {
		t.Error("per-user counting")
	}
	// An empty key isn't limited.
	for range 3 {
		if do("") != 200 {
			t.Error("empty key limited")
		}
	}
	// Separate names count separately.
	other := Middleware("other", byUser)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequestWithContext(withCache(store), http.MethodGet, "/", nil)
	r.Header.Set("X-User", "ada")
	w := httptest.NewRecorder()
	other.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Error("another middleware's name shares the count")
	}

	defer func() {
		if recover() == nil {
			t.Error("invalid limit accepted")
		}
	}()
	Middleware("bad", Limit{Max: 1})
}

func TestMiddlewareLimits(t *testing.T) {
	fixedClock(t)
	store := cache.NewMemoryStore()
	var hits int
	serve := func(h http.Handler, remote, user string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(withCache(store), http.MethodPost, "/", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-User", user)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ })

	// A By key equal to an IP address doesn't share that address's count.
	byUser := PerMinute(2).By(func(r *http.Request) string { return r.Header.Get("X-User") })
	h := Middleware("login", PerMinute(2), byUser)(next)
	serve(h, "198.51.100.9:1", "203.0.113.7")
	serve(h, "198.51.100.9:1", "203.0.113.7")
	if w := serve(h, "203.0.113.7:1", "ada"); w.Code != http.StatusOK {
		t.Errorf("the victim's IP was limited by another client's key: %d", w.Code)
	}

	// Blocked requests don't use up the longer limits, whatever the order
	// the limits are given in.
	h = Middleware("quota", PerDay(5), PerMinute(2))(next)
	for range 10 {
		serve(h, "192.0.2.50:1", "")
	}
	now = func(context.Context) time.Time { return time.Date(2026, 1, 1, 12, 1, 10, 0, time.UTC) } // the next minute
	w := serve(h, "192.0.2.50:1", "")
	if w.Code != http.StatusOK || w.Header().Get("X-RateLimit-Remaining") != "1" {
		t.Errorf("next minute: %d, remaining %s (the daily quota was used up by blocked requests?)", w.Code, w.Header().Get("X-RateLimit-Remaining"))
	}
	// The daily limit blocks with its own reset.
	for range 2 {
		now = func(context.Context) time.Time { return time.Date(2026, 1, 1, 12, 2+hits, 10, 0, time.UTC) }
		serve(h, "192.0.2.50:1", "")
	}
	now = func(context.Context) time.Time { return time.Date(2026, 1, 1, 13, 0, 0, 0, time.UTC) }
	w = serve(h, "192.0.2.50:1", "")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("X-RateLimit-Limit") != "5" || w.Header().Get("Retry-After") != "39600" {
		t.Errorf("daily limit: %d %v", w.Code, w.Header())
	}
}

func TestAllowKeys(t *testing.T) {
	fixedClock(t)
	ctx := withCache(cache.NewMemoryStore())
	for _, k := range []string{"nul\x00key", "", strings.Repeat("é", 300)} {
		if _, err := Allow(ctx, k, PerMinute(1)); err != nil {
			t.Errorf("key %.20q: %v", k, err)
		}
	}
}

func TestCheck(t *testing.T) {
	fixedClock(t)
	ctx := withCache(cache.NewMemoryStore())
	l := PerMinute(2)
	if res, err := Check(ctx, "k", l); err != nil || !res.Allowed || res.Remaining != 2 {
		t.Fatalf("fresh: %+v %v", res, err)
	}
	for range 2 {
		if _, err := Hit(ctx, "k", l); err != nil {
			t.Fatal(err)
		}
	}
	if res, _ := Check(ctx, "k", l); res.Allowed || res.Remaining != 0 {
		t.Errorf("after two hits: %+v", res)
	}
	if res, _ := Check(ctx, "k", l); res.Remaining != 0 {
		t.Error("Check counted a hit")
	}
}

func TestAllowN(t *testing.T) {
	fixedClock(t)
	ctx := withCache(cache.NewMemoryStore())
	l := PerDay(1000)
	if res, err := AllowN(ctx, "tokens:ada", 600, l); err != nil || !res.Allowed || res.Remaining != 400 {
		t.Fatalf("600: %+v %v", res, err)
	}
	if res, _ := AllowN(ctx, "tokens:ada", 500, l); res.Allowed || res.Remaining != 0 {
		t.Errorf("1100: %+v", res)
	}
	if res, _ := Check(ctx, "tokens:ada", l); res.Allowed {
		t.Error("over the limit, Check allows")
	}
	if res, _ := AllowN(ctx, "tokens:bob", 0, l); !res.Allowed || res.Remaining != 1000 {
		t.Errorf("0: %+v", res)
	}
	if _, err := AllowN(ctx, "tokens:ada", -1, l); err == nil {
		t.Error("a negative n accepted")
	}
}
