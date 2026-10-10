// SPDX-License-Identifier: Apache-2.0

package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/pubsub"
	goredis "github.com/redis/go-redis/v9"
)

// streamsConfig is the Redis broker's own setting.
type streamsConfig struct {
	MaxLen int64 `env:"PUBSUB_REDIS_MAXLEN" default:"1000000"`
}

// PubSubDriver is the Redis Streams broker's driver (PUBSUB_DRIVER=redis),
// on the app's client from [Connect]:
//
//	ps, err := pubsub.New(app, redis.PubSubDriver())
//
// PUBSUB_REDIS_MAXLEN (default 1000000; 0 for none) caps each stream,
// about: older messages are trimmed, even if a subscription hasn't read
// them.
func PubSubDriver() pubsub.Driver {
	return pubsub.Driver{Name: "redis", Open: func(app *anetos.App, cfg pubsub.Config) (pubsub.Broker, error) {
		sc, err := config.Get[streamsConfig](app.Source())
		if err != nil {
			return nil, err
		}
		if sc.MaxLen < 0 {
			return nil, errors.New("PUBSUB_REDIS_MAXLEN can't be negative")
		}
		client, err := Connect(context.Background(), app)
		if err != nil {
			return nil, err
		}
		b := NewStreamsBroker(client, cfg.Prefix, sc.MaxLen)
		b.shared = true
		return b, nil
	}}
}

// StreamsBroker is a pub/sub broker on Redis Streams (Redis 7 or later:
// it relies on how Redis 7 claims deleted entries). In a Redis Cluster, each stream and its retry
// sets must share a slot: put a hash tag in the prefix ("{events}:"): a topic is a stream (the key is the prefix and
// the topic), a subscription a consumer group of it. Messages a listener
// fails stay pending, with their retry time in a sorted set (the stream's
// key, "\x00retry\x00" and the group); they are claimed again
// (XAUTOCLAIM) once it has come. Those of a listener that stopped are
// claimed after their ack timeout. Message.Attempt is the group's
// delivery count.
type StreamsBroker struct {
	client goredis.UniversalClient
	prefix string
	maxLen int64
	shared bool // the client belongs to the app, which closes it

	offset atomic.Int64 // the server's clock minus ours, in ms: retry times follow the server's
}

// now is the server's time in Unix ms, by our clock and the last offset.
func (b *StreamsBroker) now() int64 { return time.Now().UnixMilli() + b.offset.Load() }

// syncClock measures the offset of the server's clock.
func (b *StreamsBroker) syncClock(ctx context.Context) {
	before := time.Now()
	t, err := b.client.Time(ctx).Result()
	if err == nil {
		mid := before.Add(time.Since(before) / 2)
		b.offset.Store(t.UnixMilli() - mid.UnixMilli())
	}
}

// NewStreamsBroker returns a broker using client, with stream keys
// starting with prefix, capping streams to about maxLen messages (0 for
// no cap). Closing the broker closes the client.
func NewStreamsBroker(client goredis.UniversalClient, prefix string, maxLen int64) *StreamsBroker {
	return &StreamsBroker{client: client, prefix: prefix, maxLen: maxLen}
}

func (b *StreamsBroker) key(topic string) string { return b.prefix + topic }

// Publish implements [pubsub.Broker].
func (b *StreamsBroker) Publish(ctx context.Context, topic string, m pubsub.Outgoing) (string, error) {
	values := []any{"data", m.Data}
	if len(m.Attributes) > 0 {
		attrs, err := json.Marshal(m.Attributes)
		if err != nil {
			return "", err
		}
		values = append(values, "attrs", attrs)
	}
	return b.client.XAdd(ctx, &goredis.XAddArgs{Stream: b.key(topic), MaxLen: b.maxLen, Approx: true, Values: values}).Result()
}

// Prepare implements [pubsub.Broker]: it creates the consumer group (and
// the stream), reading from the stream's end.
func (b *StreamsBroker) Prepare(ctx context.Context, s pubsub.SubscriptionSpec) error {
	err := b.client.XGroupCreateMkStream(ctx, b.key(s.Topic), s.Name, "$").Err()
	if err != nil && strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

// retryKey is the sorted set of a group's failed messages, scored by
// when (Unix ms) they may be delivered again.
func (b *StreamsBroker) retryKey(key, group string) string { return key + "\x00retry\x00" + group }

// idleConsumer is how long a consumer without pending messages may be
// idle before another one removes it.
var idleConsumer = time.Hour

// Subscribe implements [pubsub.Broker].
func (b *StreamsBroker) Subscribe(ctx context.Context, s pubsub.SubscriptionSpec, handle func(ctx context.Context, m *pubsub.Message) pubsub.Outcome) error {
	if err := b.Prepare(ctx, s); err != nil {
		return err
	}
	key := b.key(s.Topic)
	consumer := "c-" + newToken()[:16]
	b.tidy(ctx, key, s.Name)
	lastTidy := time.Now()
	conc := max(s.Concurrency, 1)
	slots := make(chan struct{}, conc)
	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		b.dropConsumer(key, s.Name, consumer)
	}()
	cursor := "0-0"
	for {
		select {
		case <-ctx.Done():
			return nil
		case slots <- struct{}{}:
		}
		free := 1
	fill:
		for free < conc {
			select {
			case slots <- struct{}{}:
				free++
			default:
				break fill
			}
		}
		if time.Since(lastTidy) > 10*time.Minute {
			b.tidy(ctx, key, s.Name)
			lastTidy = time.Now()
		}
		msgs, next, err := b.take(ctx, key, s, consumer, cursor, free)
		cursor = next
		for range free - len(msgs) {
			<-slots
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// The stream (or the group) was deleted, maybe while we were
			// waiting for messages.
			if msg := err.Error(); strings.HasPrefix(msg, "NOGROUP") || strings.HasPrefix(msg, "UNBLOCKED") {
				if err := b.Prepare(ctx, s); err != nil {
					return err
				}
				continue
			}
			return err
		}
		for _, m := range msgs {
			wg.Go(func() {
				defer func() { <-slots }()
				o := handle(ctx, m)
				sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				defer cancel()
				b.settle(sctx, key, s, consumer, m.ID, o)
			})
		}
	}
}

// tidy measures the server's clock and removes what stopped consumers
// left: consumers without pending messages idle for an hour, and retry
// times of messages long gone. Subscribe runs it when it starts, then
// every 10 minutes.
func (b *StreamsBroker) tidy(ctx context.Context, key, group string) {
	b.syncClock(ctx)
	if cs, err := b.client.XInfoConsumers(ctx, key, group).Result(); err == nil {
		for _, c := range cs {
			if c.Pending == 0 && c.Idle > idleConsumer {
				_ = b.client.XGroupDelConsumer(ctx, key, group, c.Name).Err()
			}
		}
	}
	old := b.now() - (24 * time.Hour).Milliseconds()
	_ = b.client.ZRemRangeByScore(ctx, b.retryKey(key, group), "-inf", strconv.FormatInt(old, 10)).Err()
}

// take returns up to n messages: first failed ones whose retry time has
// come, then those of stopped consumers (pending past the ack timeout),
// else new ones, waiting briefly for them.
func (b *StreamsBroker) take(ctx context.Context, key string, s pubsub.SubscriptionSpec, consumer, cursor string, n int) ([]*pubsub.Message, string, error) {
	retry := b.retryKey(key, s.Name)
	due, err := b.client.ZRangeByScore(ctx, retry, &goredis.ZRangeBy{Min: "-inf", Max: strconv.FormatInt(b.now(), 10), Count: int64(n)}).Result()
	if err != nil {
		return nil, cursor, err
	}
	if len(due) > 0 {
		msgs, err := b.claim(ctx, key, s, consumer, due)
		if err != nil || len(msgs) > 0 {
			return msgs, cursor, err
		}
	}
	// JUSTID: claiming doesn't count as a delivery until the message is
	// known to be due. A few passes, so failed messages waiting longer
	// than the ack timeout don't hide abandoned ones for long.
	for range 10 {
		ids, next, err := b.client.XAutoClaimJustID(ctx, &goredis.XAutoClaimArgs{Stream: key, Group: s.Name, Consumer: consumer,
			MinIdle: s.AckTimeout, Start: cursor, Count: int64(n)}).Result()
		if err != nil {
			return nil, cursor, err
		}
		if next == "" {
			next = "0-0"
		}
		wrapped := next == "0-0"
		cursor = next
		if len(ids) == 0 {
			break
		}
		msgs, err := b.claim(ctx, key, s, consumer, ids)
		if err != nil || len(msgs) > 0 || wrapped {
			return msgs, cursor, err
		}
	}
	streams, err := b.client.XReadGroup(ctx, &goredis.XReadGroupArgs{Group: s.Name, Consumer: consumer,
		Streams: []string{key, ">"}, Count: int64(n), Block: 500 * time.Millisecond}).Result()
	if errors.Is(err, goredis.Nil) {
		return nil, cursor, nil
	}
	if err != nil {
		return nil, cursor, err
	}
	var msgs []*pubsub.Message
	for _, st := range streams {
		for _, x := range st.Messages {
			msgs = append(msgs, b.message(s.Topic, x, 1))
		}
	}
	return msgs, cursor, nil
}

// psClaim decides, atomically, which of the messages ARGV[5..] this
// consumer delivers: KEYS[1] is the stream, KEYS[2] its group's retry
// set; ARGV are the group, the consumer, the time (ms) and the ack
// timeout (ms). A message with a retry time to come is put back to
// sleep. One whose retry time has come is delivered by whoever removes
// that time. One without a retry time (abandoned by a stopped consumer)
// is delivered only if this consumer owns it: XAUTOCLAIM just gave it, and
// no one else has claimed it since. Delivering claims it without JUSTID,
// which counts the delivery. It returns {id, fields, delivery count} for
// each message to deliver.
var psClaim = goredis.NewScript(`
local now, ack = tonumber(ARGV[3]), tonumber(ARGV[4])
local out = {}
for i = 5, #ARGV do
	local id = ARGV[i]
	local score = redis.call('ZSCORE', KEYS[2], id)
	local deliver = false
	if score then
		local wait = tonumber(score) - now
		if wait > 0 then
			redis.call('XCLAIM', KEYS[1], ARGV[1], ARGV[2], 0, id, 'IDLE', ack - math.min(wait, ack), 'JUSTID')
		else
			redis.call('ZREM', KEYS[2], id)
			deliver = true
		end
	else
		local p = redis.call('XPENDING', KEYS[1], ARGV[1], id, id, 1)
		deliver = #p == 1 and p[1][2] == ARGV[2]
	end
	if deliver then
		local e = redis.call('XCLAIM', KEYS[1], ARGV[1], ARGV[2], 0, id)
		if #e == 1 and type(e[1]) == 'table' and e[1][2] then
			local p = redis.call('XPENDING', KEYS[1], ARGV[1], id, id, 1)
			local count = 0
			if #p == 1 then count = p[1][4] end
			table.insert(out, {e[1][1], e[1][2], count})
		end
	end
end
return out`)

// claim delivers the messages ids that this consumer should (see
// psClaim).
func (b *StreamsBroker) claim(ctx context.Context, key string, s pubsub.SubscriptionSpec, consumer string, ids []string) ([]*pubsub.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{s.Name, consumer, b.now(), s.AckTimeout.Milliseconds()}
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := psClaim.Run(ctx, b.client, []string{key, b.retryKey(key, s.Name)}, args...).Slice()
	if err != nil {
		return nil, err
	}
	var msgs []*pubsub.Message
	for _, r := range res {
		row, ok := r.([]any)
		if !ok || len(row) != 3 {
			continue
		}
		id, _ := row[0].(string)
		fields, _ := row[1].([]any)
		count, _ := row[2].(int64)
		values := map[string]any{}
		for i := 0; i+1 < len(fields); i += 2 {
			if k, ok := fields[i].(string); ok {
				values[k] = fields[i+1]
			}
		}
		msgs = append(msgs, b.message(s.Topic, goredis.XMessage{ID: id, Values: values}, int(count)))
	}
	return msgs, nil
}

// idleFor returns the idle time to give a pending message so it can be
// claimed after wait (or after ackTimeout, if wait is longer: it is then
// put back to sleep).
func idleFor(ackTimeout, wait time.Duration) int64 {
	return (ackTimeout - min(wait, ackTimeout)).Milliseconds()
}

// message converts a stream entry.
func (b *StreamsBroker) message(topic string, x goredis.XMessage, attempt int) *pubsub.Message {
	m := &pubsub.Message{ID: x.ID, Topic: topic, Attempt: attempt}
	if d, ok := x.Values["data"].(string); ok {
		m.Data = []byte(d)
	}
	if a, ok := x.Values["attrs"].(string); ok {
		_ = json.Unmarshal([]byte(a), &m.Attributes)
	}
	if ms, _, ok := strings.Cut(x.ID, "-"); ok {
		if n, err := strconv.ParseInt(ms, 10, 64); err == nil {
			m.PublishedAt = time.UnixMilli(n)
		}
	}
	return m
}

// settle acknowledges a message, or schedules its next delivery: its
// retry time goes in the group's sorted set, and its idle time is set so
// that it can be claimed then (or after the ack timeout, when it is
// checked again).
func (b *StreamsBroker) settle(ctx context.Context, key string, s pubsub.SubscriptionSpec, consumer, id string, o pubsub.Outcome) {
	retry := b.retryKey(key, s.Name)
	_, _ = b.client.Pipelined(ctx, func(p goredis.Pipeliner) error {
		if o.Ack {
			p.XAck(ctx, key, s.Name, id)
			p.ZRem(ctx, retry, id)
			return nil
		}
		wait := max(o.RetryAfter, 0)
		p.ZAdd(ctx, retry, goredis.Z{Score: float64(b.now() + wait.Milliseconds()), Member: id})
		p.Do(ctx, "XCLAIM", key, s.Name, consumer, 0, id, "IDLE", idleFor(s.AckTimeout, wait), "JUSTID")
		return nil
	})
}

// dropConsumer removes a consumer that has no pending messages, so
// restarts don't pile up consumers.
func (b *StreamsBroker) dropConsumer(key, group, consumer string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := b.client.XPendingExt(ctx, &goredis.XPendingExtArgs{Stream: key, Group: group, Start: "-", End: "+", Count: 1, Consumer: consumer}).Result()
	if err == nil && len(p) == 0 {
		_ = b.client.XGroupDelConsumer(ctx, key, group, consumer).Err()
	}
}

// Purge removes every stream under the broker's prefix: anetostest calls
// it at the end of a test.
func (b *StreamsBroker) Purge(ctx context.Context) error {
	if b.prefix == "" {
		return fmt.Errorf("redis: the pub/sub broker has no prefix: Purge would remove every key")
	}
	return (&CacheStore{client: b.client}).Flush(ctx, b.prefix)
}

// Close implements [pubsub.Broker]: it closes the client, unless it is
// the app's.
func (b *StreamsBroker) Close() error {
	if b.shared {
		return nil
	}
	return b.client.Close()
}
