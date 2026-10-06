// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/queue"
	goredis "github.com/redis/go-redis/v9"
)

// QueueDriver keeps jobs in Redis (QUEUE_DRIVER=redis), under the keys
// starting with QUEUE_PREFIX, on the app's client from [Connect]:
//
//	q, err := queue.ForApp(app, redis.QueueDriver())
func QueueDriver() queue.Driver {
	return queue.Driver{Name: "redis", Open: func(app *anetos.App, cfg queue.Config) (queue.Store, error) {
		client, err := Connect(context.Background(), app)
		if err != nil {
			return nil, err
		}
		s := NewQueueStore(client, cfg.Prefix)
		s.shared = true
		return s, nil
	}}
}

// QueueStore is a queue store in Redis. Each queue is a sorted set of job
// IDs, scored by the time (in milliseconds, by the server's clock) the
// job becomes available; reserving a job moves its score to the end of
// its lease. Each job is a hash. Failed jobs are hashes too, with a sorted
// set by the time they failed. Every change runs as a Lua script, so it
// is atomic.
//
// The scripts use keys they compute: in a Redis Cluster, put a hash tag
// in the prefix ("{blog}:queue:") so every key is in one slot.
type QueueStore struct {
	client goredis.UniversalClient
	prefix string
	shared bool // the client belongs to the app, which closes it
}

// NewQueueStore returns a store using client, with keys starting with
// prefix. Closing the store closes the client.
func NewQueueStore(client goredis.UniversalClient, prefix string) *QueueStore {
	return &QueueStore{client: client, prefix: prefix}
}

func (s *QueueStore) queueKey(name string) string { return s.prefix + "q:" + name }
func (s *QueueStore) jobKey(id string) string     { return s.prefix + "job:" + id }
func (s *QueueStore) failedKey() string           { return s.prefix + "failed" }
func (s *QueueStore) failedJobKey(id string) string {
	return s.prefix + "failed:" + id
}

// now is the server's time in milliseconds, in Lua.
const luaNow = `
local function now()
	local t = redis.call('TIME')
	return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end
`

// owned checks that the job hash KEYS[2] is reserved with the token
// ARGV[2].
const luaOwned = `
local token = redis.call('HGET', KEYS[2], 'token')
if not token or token == '' or token ~= ARGV[2] then
	return 0
end
`

var (
	// push: KEYS queue, job; ARGV id, queue name, payload, delay (ms).
	qPush = goredis.NewScript(luaNow + `
redis.call('DEL', KEYS[2])
redis.call('HSET', KEYS[2], 'queue', ARGV[2], 'payload', ARGV[3], 'attempts', 0, 'token', '')
redis.call('ZADD', KEYS[1], now() + tonumber(ARGV[4]), ARGV[1])
return 1`)
	// reserve: KEYS queue; ARGV job key prefix, lease (ms), token.
	// Returns {id, payload, attempts} or false.
	qReserve = goredis.NewScript(luaNow + `
local n = now()
while true do
	local ids = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', n, 'LIMIT', 0, 1)
	if #ids == 0 then
		return false
	end
	local id = ids[1]
	local job = ARGV[1] .. id
	if redis.call('EXISTS', job) == 1 then
		redis.call('ZADD', KEYS[1], n + tonumber(ARGV[2]), id)
		local attempts = redis.call('HINCRBY', job, 'attempts', 1)
		redis.call('HSET', job, 'token', ARGV[3])
		return {id, redis.call('HGET', job, 'payload'), attempts}
	end
	redis.call('ZREM', KEYS[1], id) -- its hash is gone: forget it
end`)
	// delete: KEYS queue, job; ARGV id, token.
	qDelete = goredis.NewScript(luaOwned + `
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('DEL', KEYS[2])
return 1`)
	// release: KEYS queue, job; ARGV id, token, delay (ms), refund (0/1).
	qRelease = goredis.NewScript(luaNow + luaOwned + `
redis.call('ZADD', KEYS[1], now() + tonumber(ARGV[3]), ARGV[1])
redis.call('HSET', KEYS[2], 'token', '')
if ARGV[4] == '1' and tonumber(redis.call('HGET', KEYS[2], 'attempts')) > 0 then
	redis.call('HINCRBY', KEYS[2], 'attempts', -1)
end
return 1`)
	// fail: KEYS queue, job, failed set, failed job; ARGV id, token, error.
	qFail = goredis.NewScript(luaNow + luaOwned + `
local job = redis.call('HMGET', KEYS[2], 'queue', 'payload', 'attempts')
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('DEL', KEYS[2])
local n = now()
redis.call('DEL', KEYS[4])
redis.call('HSET', KEYS[4], 'queue', job[1], 'payload', job[2], 'error', ARGV[3], 'attempts', job[3], 'failed_at', n)
redis.call('ZADD', KEYS[3], n, ARGV[1])
return 1`)
	// retry: KEYS failed set, failed job, job; ARGV id, queue key prefix.
	qRetry = goredis.NewScript(luaNow + `
local f = redis.call('HMGET', KEYS[2], 'queue', 'payload')
redis.call('ZREM', KEYS[1], ARGV[1])
if not f[1] then
	return 0
end
redis.call('DEL', KEYS[2])
redis.call('DEL', KEYS[3])
redis.call('HSET', KEYS[3], 'queue', f[1], 'payload', f[2], 'attempts', 0, 'token', '')
redis.call('ZADD', ARGV[2] .. f[1], now(), ARGV[1])
return 1`)
)

// newToken returns a random reservation token.
func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Push implements [queue.Store].
func (s *QueueStore) Push(ctx context.Context, m queue.Message, delay time.Duration) error {
	return qPush.Run(ctx, s.client, []string{s.queueKey(m.Queue), s.jobKey(m.ID)}, m.ID, m.Queue, m.Payload, ms(delay)).Err()
}

// Reserve implements [queue.Store].
func (s *QueueStore) Reserve(ctx context.Context, name string, lease time.Duration) (*queue.Reservation, error) {
	token := newToken()
	v, err := qReserve.Run(ctx, s.client, []string{s.queueKey(name)}, s.prefix+"job:", ms(lease), token).Slice()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(v) != 3 {
		return nil, fmt.Errorf("redis: reserve returned %v", v)
	}
	id, _ := v[0].(string)
	payload, _ := v[1].(string)
	attempts, _ := v[2].(int64)
	return &queue.Reservation{ID: id, Queue: name, Payload: []byte(payload), Attempts: int(attempts), Token: token}, nil
}

// owned runs a script that returns 0 if the reservation has ended.
func owned(cmd *goredis.Cmd) error {
	n, err := cmd.Int64()
	if err == nil && n == 0 {
		err = queue.ErrLeaseLost
	}
	return err
}

// Delete implements [queue.Store].
func (s *QueueStore) Delete(ctx context.Context, r *queue.Reservation) error {
	return owned(qDelete.Run(ctx, s.client, []string{s.queueKey(r.Queue), s.jobKey(r.ID)}, r.ID, r.Token))
}

// Release implements [queue.Store].
func (s *QueueStore) Release(ctx context.Context, r *queue.Reservation, delay time.Duration, refund bool) error {
	flag := "0"
	if refund {
		flag = "1"
	}
	return owned(qRelease.Run(ctx, s.client, []string{s.queueKey(r.Queue), s.jobKey(r.ID)}, r.ID, r.Token, ms(delay), flag))
}

// Fail implements [queue.Store].
func (s *QueueStore) Fail(ctx context.Context, r *queue.Reservation, errMsg string) error {
	return owned(qFail.Run(ctx, s.client, []string{s.queueKey(r.Queue), s.jobKey(r.ID), s.failedKey(), s.failedJobKey(r.ID)},
		r.ID, r.Token, errMsg))
}

// Size implements [queue.Store].
func (s *QueueStore) Size(ctx context.Context, name string) (int64, error) {
	return s.client.ZCard(ctx, s.queueKey(name)).Result()
}

// removeAll removes the members of the sorted set key, and the hashes
// key(member), in batches, and returns how many there were.
func (s *QueueStore) removeAll(ctx context.Context, set string, key func(string) string) (int64, error) {
	var n int64
	for {
		ids, err := s.client.ZRange(ctx, set, 0, 999).Result()
		if err != nil || len(ids) == 0 {
			return n, err
		}
		members := make([]any, len(ids))
		var removed *goredis.IntCmd
		_, err = s.client.Pipelined(ctx, func(p goredis.Pipeliner) error {
			for i, id := range ids {
				members[i] = id
				p.Del(ctx, key(id))
			}
			removed = p.ZRem(ctx, set, members...)
			return nil
		})
		if err != nil {
			return n, err
		}
		n += removed.Val()
	}
}

// Clear implements [queue.Store].
func (s *QueueStore) Clear(ctx context.Context, name string) (int64, error) {
	return s.removeAll(ctx, s.queueKey(name), s.jobKey)
}

// Failed implements [queue.Store].
func (s *QueueStore) Failed(ctx context.Context, offset, limit int) ([]queue.FailedJob, error) {
	if limit <= 0 {
		return nil, nil
	}
	offset = max(offset, 0)
	ids, err := s.client.ZRevRange(ctx, s.failedKey(), int64(offset), int64(offset+limit-1)).Result()
	if err != nil || len(ids) == 0 {
		return nil, err
	}
	cmds := make([]*goredis.MapStringStringCmd, len(ids))
	_, err = s.client.Pipelined(ctx, func(p goredis.Pipeliner) error {
		for i, id := range ids {
			cmds[i] = p.HGetAll(ctx, s.failedJobKey(id))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]queue.FailedJob, 0, len(ids))
	for i, id := range ids {
		h := cmds[i].Val()
		if len(h) == 0 {
			continue // forgotten meanwhile
		}
		attempts, _ := strconv.Atoi(h["attempts"])
		at, _ := strconv.ParseInt(h["failed_at"], 10, 64)
		out = append(out, queue.FailedJob{ID: id, Queue: h["queue"], Payload: []byte(h["payload"]), Error: h["error"],
			Attempts: attempts, FailedAt: time.UnixMilli(at)})
	}
	return out, nil
}

// CountFailed implements [queue.FailedCounter].
func (s *QueueStore) CountFailed(ctx context.Context) (int64, error) {
	return s.client.ZCard(ctx, s.failedKey()).Result()
}

// FindFailed implements [queue.FailedFinder].
func (s *QueueStore) FindFailed(ctx context.Context, id string) (queue.FailedJob, bool, error) {
	h, err := s.client.HGetAll(ctx, s.failedJobKey(id)).Result()
	if err != nil || len(h) == 0 {
		return queue.FailedJob{}, false, err
	}
	attempts, _ := strconv.Atoi(h["attempts"])
	at, _ := strconv.ParseInt(h["failed_at"], 10, 64)
	return queue.FailedJob{ID: id, Queue: h["queue"], Payload: []byte(h["payload"]), Error: h["error"],
		Attempts: attempts, FailedAt: time.UnixMilli(at)}, true, nil
}

// Retry implements [queue.Store].
func (s *QueueStore) Retry(ctx context.Context, id string) (bool, error) {
	n, err := qRetry.Run(ctx, s.client, []string{s.failedKey(), s.failedJobKey(id), s.jobKey(id)}, id, s.prefix+"q:").Int64()
	return n == 1, err
}

// Forget implements [queue.Store].
func (s *QueueStore) Forget(ctx context.Context, id string) (bool, error) {
	var del *goredis.IntCmd
	_, err := s.client.TxPipelined(ctx, func(p goredis.Pipeliner) error {
		p.ZRem(ctx, s.failedKey(), id)
		del = p.Del(ctx, s.failedJobKey(id))
		return nil
	})
	return del.Val() == 1, err
}

// Flush implements [queue.Store].
func (s *QueueStore) Flush(ctx context.Context) (int64, error) {
	return s.removeAll(ctx, s.failedKey(), s.failedJobKey)
}

// Purge removes every key of the store, jobs and failed jobs of every
// queue: anetostest calls it at the end of a test.
func (s *QueueStore) Purge(ctx context.Context) error {
	if s.prefix == "" {
		return errors.New("redis: the queue store has no prefix: Purge would remove every key")
	}
	return (&CacheStore{client: s.client}).Flush(ctx, s.prefix)
}

// Close implements [queue.Store]: it closes the client, unless it is the
// app's.
func (s *QueueStore) Close() error {
	if s.shared {
		return nil
	}
	return s.client.Close()
}
