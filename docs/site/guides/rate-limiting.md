---
title: Rate limiting
since: v0.2.0
group: "Accounts and security"
weight: 307
---

# Rate limiting

Limit how often clients can call your routes, and how often they can
attempt actions such as logging in.

## Before you start

Rate limits count hits in the app's cache, so set it up with
`cache.New` (see [Cache](cache.md)). With the memory store each
instance counts on its own; with several instances, use the database or
Redis store so a limit holds across all of them.

Behind a load balancer or reverse proxy, set `HTTP_TRUSTED_PROXIES` to
your proxies' networks, so the server's `RealIP` middleware finds the
client's address; otherwise every request seems to come from the proxy,
and all clients share one limit.

## Steps

### 1. Limit a group of routes

```go
// 120 requests a minute per client IP, counted in the app's cache.
r = r.Group("", ratelimit.Middleware("api", ratelimit.PerMinute(120)))
```

(Copied from [`examples/database`](../../../examples/database/main.go), region `ratelimit`.)

The name (`"api"`) keeps these counts apart from other limits': give each
`Middleware` its own. A request over the limit gets `429 Too Many
Requests` through the app's error handler, with a `Retry-After` header in
seconds. Every limited response carries `X-RateLimit-Limit` and
`X-RateLimit-Remaining`.

Pass several limits to enforce them all. They are counted from the
shortest window to the longest, and a request over one isn't counted
against the longer ones, so a client retrying too fast doesn't use up its
daily quota:

```go
// illustrative
api := r.Group("/api", ratelimit.Middleware("api", ratelimit.PerMinute(60), ratelimit.PerDay(5000)))
```

The limits are `ratelimit.PerSecond`, `PerMinute`, `PerHour`, `PerDay`,
and `ratelimit.Per(n, window)` for any window.

### 2. Count per user or token

By default, hits are counted per client IP address (per `/64` network for
IPv6). Count per something else with `By`, such as the logged-in user:

```go
// illustrative
perUser := ratelimit.PerMinute(600).By(func(r *http.Request) string {
	return userID(r) // the user an authentication middleware found; "" if none
})
perGuest := ratelimit.PerMinute(60).By(func(r *http.Request) string {
	if userID(r) != "" {
		return "" // logged in: perUser applies
	}
	return ratelimit.IP(r)
})
api := r.Group("/api", auth, ratelimit.Middleware("api", perUser, perGuest))
```

Returning `""` leaves the request unlimited by that limit. Key only by
something the server has verified: a key the client chooses (a header, a
token that isn't checked yet) can change with every request, and gets it
a fresh count each time. Make sure every request has some limit, as the
per-IP limit for guests does above. Keys are stored as hashes, so they
may be secrets.

### 3. Limit an action in a handler

For attempts that aren't whole requests, such as logins, count them with
`ratelimit.Allow` and clear the count when the attempt succeeds:

```go
// illustrative
key := "login:" + strings.ToLower(in.Email) + "|" + ratelimit.IP(c.Request())
limit := ratelimit.PerMinute(5)
res, err := ratelimit.Allow(c, key, limit)
if err != nil {
	return nil, err
}
if !res.Allowed {
	return nil, web.Errorf(http.StatusTooManyRequests, "Too many attempts. Try again in %d seconds.", int(res.RetryAfter().Seconds())+1)
}
// … check the password; on success:
_ = ratelimit.Clear(c, key, limit)
```

To count only some attempts (failures), check first with
`ratelimit.Check`, which doesn't count, and count with `ratelimit.Hit`
when the attempt fails.

To limit an amount rather than a number of events (bytes uploaded,
a model's tokens), count it with `ratelimit.AllowN(ctx, key, n, limit)`:
`ratelimit.PerDay(100_000)` then allows 100,000 a day. AI budgets
(`ai.Budget`) work this way.

### 4. Test

Each `anetostest` app has its own cache, so tests don't share counts. To
see a limit trip, send more requests than it allows:

```go
// illustrative
for range 120 {
	app.GetJSON("/stats").AssertOK()
}
app.GetJSON("/stats").AssertStatus(http.StatusTooManyRequests)
```

A test can start a new window partway through; the example's
`TestRateLimit` sends up to two windows' worth of requests to allow for that.

## How it works

Each limit counts hits in fixed windows aligned to the clock: a
per-minute limit starts a new count at every minute. A client can
therefore make up to twice the limit around the end of a window, which is
the usual trade-off for one cache operation per request. A hit is one
atomic `cache.Increment` of a key made of a hash of the middleware's
name, the kind of key and the client's key (or of your `Allow` key),
the limit and the window; it expires after its window, plus up to a minute
for clocks that differ between instances. Hits over a limit count against
it too, and the window's end is the same for every client.

If the cache fails, the request fails with its error (a 500) rather than
go through unlimited.

## Common problems

| Symptom | Cause | Fix |
|---|---|---|
| Every request fails with `ratelimit: cache: no cache in context` | `cache.New` wasn't called | Set up the cache in `setup` |
| All clients share one limit in production | The app sees the proxy's address | Set `HTTP_TRUSTED_PROXIES` to your proxies' networks |
| A limit allows more than expected with several instances | The memory store counts per instance | `CACHE_DRIVER=database` or `redis` |
| Two routes' limits share a count | Their `Middleware` calls have the same name | Give each its own name |

## Next steps

- [Cache](cache.md)
- [Handlers and middleware](handlers.md)
