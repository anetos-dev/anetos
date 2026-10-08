// SPDX-License-Identifier: Apache-2.0

// Package ratelimit limits how often clients may do something: requests
// to routes, with [Middleware], or actions in handlers, such as login
// attempts, with [Allow]. Hits are counted in the app's cache (see
// package cache), so with a shared store (database or Redis) a limit
// holds across every instance of the app.
//
//	api := r.Group("/api", ratelimit.Middleware("api", ratelimit.PerMinute(60)))
//
// Limits count hits in fixed windows aligned to the clock (a limit per
// minute resets at every minute), so a client can make up to twice the
// limit around the end of a window.
package ratelimit

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/web"
)

// Limit allows Max hits per Window for each key: by default, each client
// IP address.
type Limit struct {
	// Max is the number of hits allowed in a window.
	Max int
	// Window is the length of a window.
	Window time.Duration

	key func(r *http.Request) string
}

// Per returns a limit of n hits per window.
func Per(n int, window time.Duration) Limit { return Limit{Max: n, Window: window} }

// PerSecond returns a limit of n hits per second.
func PerSecond(n int) Limit { return Per(n, time.Second) }

// PerMinute returns a limit of n hits per minute.
func PerMinute(n int) Limit { return Per(n, time.Minute) }

// PerHour returns a limit of n hits per hour.
func PerHour(n int) Limit { return Per(n, time.Hour) }

// PerDay returns a limit of n hits per day.
func PerDay(n int) Limit { return Per(n, 24*time.Hour) }

// By returns the limit counting hits per key(r) instead of per client IP,
// for [Middleware]: the signed-in user, an API token, a form field. A key
// of "" leaves the request unlimited by this limit.
//
//	ratelimit.PerMinute(600).By(func(r *http.Request) string { return apiToken(r) })
func (l Limit) By(key func(r *http.Request) string) Limit {
	l.key = key
	return l
}

// Result is the outcome of a hit.
type Result struct {
	// Allowed reports whether the hit was within the limit.
	Allowed bool
	// Limit is the limit's Max.
	Limit int
	// Remaining is how many more hits the window allows.
	Remaining int
	// Reset is when the window ends and the count starts again.
	Reset time.Time

	at time.Time // when the hit was counted, on the app's clock
}

// RetryAfter returns how long until the window resets, from the time of
// the hit (on the app's clock, anetos.Now).
func (r Result) RetryAfter() time.Duration {
	at := r.at
	if at.IsZero() {
		at = time.Now()
	}
	return max(r.Reset.Sub(at), 0)
}

// Allow counts a hit for key against l and reports whether it is within
// the limit. It uses the cache in ctx (cache.ForApp). Use it for actions
// that aren't whole requests:
//
//	res, err := ratelimit.Allow(c, "login:"+email, ratelimit.PerMinute(5))
//	if err != nil {
//		return nil, err
//	}
//	if !res.Allowed {
//		return nil, web.Errorf(http.StatusTooManyRequests, "Too many attempts. Try again in %d seconds.", int(res.RetryAfter().Seconds())+1)
//	}
//
// Hits over the limit are counted too; the count starts again when the
// window ends. Keys are stored as hashes, so they may be secrets or any
// input.
func Allow(ctx context.Context, key string, l Limit) (Result, error) {
	return allow(ctx, "allow\x00"+key, l, 1, now(ctx))
}

// AllowN counts n hits for key at once, like [Allow]: for limits on
// amounts rather than events, such as the tokens of a user's AI calls (n
// tokens against a limit of tokens per day). n must not be negative.
func AllowN(ctx context.Context, key string, n int, l Limit) (Result, error) {
	if n < 0 {
		return Result{}, fmt.Errorf("ratelimit: AllowN with n = %d", n)
	}
	return allow(ctx, "allow\x00"+key, l, int64(n), now(ctx))
}

// now is the clock: the app's (anetos.Now); tests replace it.
var now = anetos.Now

func allow(ctx context.Context, key string, l Limit, hits int64, at time.Time) (Result, error) {
	k, reset, err := storeKey(key, l, at)
	if err != nil {
		return Result{}, err
	}
	// Keep the counter past its window (by up to a minute), for clocks that
	// differ between instances.
	n, err := cache.Increment(ctx, k, hits, reset.Sub(at)+min(max(l.Window, time.Second), time.Minute))
	if err != nil {
		return Result{}, err
	}
	return Result{
		Allowed:   n <= int64(l.Max),
		Limit:     l.Max,
		Remaining: int(max(int64(l.Max)-n, 0)),
		Reset:     reset,
		at:        at,
	}, nil
}

// Check reports how key stands against l without counting a hit: whether
// another hit would be allowed, and how many remain. Use it with [Hit] to
// count only some attempts (failed logins), checking first.
func Check(ctx context.Context, key string, l Limit) (Result, error) {
	at := now(ctx)
	k, reset, err := storeKey("allow\x00"+key, l, at)
	if err != nil {
		return Result{}, err
	}
	n, _, err := cache.Get[int64](ctx, k)
	if err != nil {
		return Result{}, err
	}
	return Result{Allowed: n < int64(l.Max), Limit: l.Max, Remaining: int(max(int64(l.Max)-n, 0)), Reset: reset, at: at}, nil
}

// Hit counts a hit for key against l, like [Allow].
func Hit(ctx context.Context, key string, l Limit) (Result, error) { return Allow(ctx, key, l) }

// Clear forgets the hits counted for key against l in the current window:
// after a successful login, say.
func Clear(ctx context.Context, key string, l Limit) error {
	k, _, err := storeKey("allow\x00"+key, l, now(ctx))
	if err != nil {
		return err
	}
	return cache.Forget(ctx, k)
}

// storeKey returns the cache key counting key's hits against l in the
// window of now, and when that window ends.
func storeKey(key string, l Limit, now time.Time) (string, time.Time, error) {
	if l.Max < 0 || l.Window < time.Millisecond {
		return "", time.Time{}, fmt.Errorf("ratelimit: invalid limit of %d per %s", l.Max, l.Window)
	}
	w := l.Window.Milliseconds()
	idx := now.UnixMilli() / w
	reset := time.UnixMilli((idx + 1) * w)
	// Keys are hashed: they may come from input (any length or bytes) or
	// be secrets (tokens), which the store shouldn't show.
	sum := sha256.Sum256([]byte(key))
	return "ratelimit:" + base64.RawURLEncoding.EncodeToString(sum[:]) + ":" + strconv.Itoa(l.Max) + "/" + l.Window.String() + ":" + strconv.FormatInt(idx, 10), reset, nil
}

// IP returns the client's IP address for rate limiting: web.ClientIP,
// narrowed to its /64 network for IPv6, since a client usually has a
// whole /64 to pick addresses from. Behind a proxy, set
// HTTP_TRUSTED_PROXIES so that web.ClientIP is the client's address.
func IP(r *http.Request) string {
	ip := web.ClientIP(r)
	if a, err := netip.ParseAddr(ip); err == nil && a.Is6() && !a.Is4In6() {
		if p, err := a.Prefix(64); err == nil {
			return p.String()
		}
	}
	return ip
}

// Middleware limits the requests of the routes it wraps. name separates
// its counters from other limits', so give each use its own. The limits
// are counted from the shortest window to the longest; a request over one
// of them gets 429 Too Many Requests (through the router's error handler)
// with a Retry-After header, and isn't counted against the longer ones,
// so a client retrying too fast doesn't use up a daily quota. Responses
// carry X-RateLimit-Limit and X-RateLimit-Remaining for the limit with the
// fewest hits left.
//
//	r.Group("/api", ratelimit.Middleware("api", ratelimit.PerMinute(60), ratelimit.PerDay(5000)))
//
// It needs the app's cache (cache.ForApp); if the cache fails, the
// request fails with its error, rather than going through unlimited.
// API descriptions (package web/openapi) list its 429.
func Middleware(name string, limits ...Limit) web.Middleware {
	for _, l := range limits {
		if l.Max < 0 || l.Window < time.Millisecond {
			panic(fmt.Sprintf("ratelimit: invalid limit of %d per %s", l.Max, l.Window))
		}
	}
	// Shortest window first.
	order := make([]int, len(limits))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(limits[a].Window, limits[b].Window) })
	return func(next http.Handler) http.Handler {
		return web.Documented(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t := now(r.Context())
			var tightest *Result
			var blocked *Result
			for _, i := range order {
				l := limits[i]
				// The kind of key is part of it: a By key equal to an IP
				// address doesn't share that address's count, and an Allow
				// key never matches a middleware's.
				key := "mw\x00" + name + "\x00ip\x00" + IP(r)
				if l.key != nil {
					k := l.key(r)
					if k == "" {
						continue
					}
					key = "mw\x00" + name + "\x00by\x00" + k
				}
				res, err := allow(r.Context(), key, l, 1, t)
				if err != nil {
					web.WriteError(w, r, fmt.Errorf("ratelimit: %w", err))
					return
				}
				if tightest == nil || res.Remaining < tightest.Remaining {
					tightest = &res
				}
				if !res.Allowed {
					blocked = &res
					break
				}
			}
			h := w.Header()
			if tightest != nil {
				h.Set("X-RateLimit-Limit", strconv.Itoa(tightest.Limit))
				h.Set("X-RateLimit-Remaining", strconv.Itoa(tightest.Remaining))
			}
			if blocked != nil {
				wait := int(math.Ceil(blocked.Reset.Sub(t).Seconds()))
				h.Set("X-RateLimit-Limit", strconv.Itoa(blocked.Limit))
				h.Set("X-RateLimit-Remaining", "0")
				h.Set("Retry-After", strconv.Itoa(max(wait, 1)))
				h.Set("X-RateLimit-Reset", strconv.FormatInt(blocked.Reset.Unix(), 10))
				web.WriteError(w, r, web.Error(http.StatusTooManyRequests, "Too many requests. Try again later."))
				return
			}
			next.ServeHTTP(w, r)
		}), doc)
	}
}

// doc is what Middleware tells API descriptions (web.Documented).
var doc = web.MiddlewareDoc{Responses: map[int]string{
	http.StatusTooManyRequests: "Too many requests: retry after the Retry-After header's seconds.",
}}
