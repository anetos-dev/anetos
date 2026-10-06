# Changelog

Changes to the `drivers/redis` module. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions are
tagged `drivers/redis/vX.Y.Z`. The framework's own changes are in the
[root CHANGELOG](../../CHANGELOG.md).

## [Unreleased]

### Added
- The queue store counts its failed jobs and finds one by ID at once
  (`queue.FailedCounter`, `queue.FailedFinder`) (AD2a).

## [0.2.0] - 2026-10-02

The first release, with the framework's v0.2.0.

### Added
- `redis.Connect`: the app's shared go-redis client for `REDIS_URL`,
  pinged when the app boots and closed at shutdown (B1).
- The Redis cache store: `redis.CacheDriver()` (`CACHE_STORE=redis`) and
  `redis.NewCacheStore` (also for cluster, failover and ring clients):
  counters in `MULTI` transactions, owner-checked lock operations in Lua
  scripts, and `cache:clear` through `SCAN` and `UNLINK` (B1).
- `redis.SessionDriver()`: sessions in Redis (`SESSION_DRIVER=redis`), on
  the app's shared client (B2).
- `redis.QueueDriver()` and `redis.NewQueueStore`: queued jobs in Redis
  (`QUEUE_DRIVER=redis`, keys under `QUEUE_PREFIX`): a sorted set per
  queue, a hash per job, every change in a Lua script on the server's
  clock; failed jobs; `Purge` for test cleanup (B5).
- `redis.PubSubDriver()` and `redis.NewStreamsBroker`: the pub/sub broker
  on Redis Streams (`PUBSUB_DRIVER=redis`, Redis 7+): consumer groups,
  backoff through pending entries' idle time, `PUBSUB_REDIS_MAXLEN` (B7).
- Runs the `cache/cachetest` (B1), `queue/queuetest` (B5) and
  `pubsub/pubsubtest` (B7) conformance suites against the server in
  `ANETOS_TEST_REDIS_URL`.
