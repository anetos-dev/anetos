# Changelog

Changes to the `drivers/redis` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/redis/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added
- `redis.Connect`: the app's shared go-redis client for `REDIS_URL`,
  pinged when the app boots and closed at shutdown (B1).
- The Redis cache store: `redis.CacheDriver()` (`CACHE_STORE=redis`) and
  `redis.NewCacheStore` (also for cluster, failover and ring clients):
  counters in `MULTI` transactions, owner-checked lock operations in Lua
  scripts, and `cache:clear` through `SCAN` and `UNLINK` (B1).
- `redis.SessionDriver()`: sessions in Redis (`SESSION_DRIVER=redis`), on
  the app's shared client (B2).
- Runs the `cache/cachetest` conformance suite against the server in
  `ANETOS_TEST_REDIS_URL` (B1).
