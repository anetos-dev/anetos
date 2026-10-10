// SPDX-License-Identifier: Apache-2.0

package pubsub

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/supervisor"
)

const (
	stageListeners   = supervisor.StageListeners
	restartOnFailure = supervisor.RestartOnFailure
	// ackMargin is how much longer than its timeout a message may be
	// handled before the broker may deliver it again.
	ackMargin = 30 * time.Second
	// releaseMargin is kept before the shutdown deadline to settle the
	// messages stopped by it.
	releaseMargin = 2 * time.Second
	// dlTimeout bounds publishing a message to the dead-letter topic.
	dlTimeout = 10 * time.Second
)

// listener is a registered listener, and the component running it.
type listener struct {
	p    *PubSub
	sub  SubscriptionSpec
	o    listenOptions
	call func(ctx context.Context, data []byte) error

	warned atomic.Bool // about Tries without delivery counts
}

// Name implements supervisor.Component.
func (l *listener) Name() string {
	return "pubsub-listener[" + l.sub.Topic + "→" + l.sub.Name + "]"
}

// Run implements supervisor.Component.
func (l *listener) Run(ctx context.Context) error {
	grace := l.o.grace
	var deadline func() time.Time
	if l.p.app != nil {
		if grace < 0 {
			grace = l.p.app.Config().ShutdownTimeout / 2
		}
		deadline = l.p.app.Supervisor().ShutdownDeadline
	} else if grace < 0 {
		grace = 15 * time.Second
	}
	return l.run(ctx, grace, deadline)
}

// errShutdown cancels the messages still being handled at the end of the
// grace period.
var errShutdown = errors.New("pubsub: the listener is stopping")

func (l *listener) run(ctx context.Context, grace time.Duration, deadline func() time.Time) error {
	log := l.p.log.With("topic", l.sub.Topic, "subscription", l.sub.Name)
	log.Info("pubsub: listener started", "concurrency", l.sub.Concurrency)
	base := context.WithoutCancel(ctx) // messages outlive ctx by the grace period
	msgCtx, stop := context.WithCancelCause(base)
	defer stop(nil)
	// When ctx ends, give the messages being handled the grace period.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
			return
		case <-ctx.Done():
		}
		g := grace
		if deadline != nil {
			if d := deadline(); !d.IsZero() {
				g = max(min(g, time.Until(d)-releaseMargin), 0)
			}
		}
		t := time.NewTimer(g)
		defer t.Stop()
		select {
		case <-done:
		case <-t.C:
			stop(errShutdown)
		}
	}()
	err := l.p.broker.Subscribe(ctx, l.sub, func(_ context.Context, m *Message) Outcome {
		return l.handle(msgCtx, base, m)
	})
	if err != nil && ctx.Err() == nil {
		log.Error("pubsub: listener stopped", "error", err)
		return err
	}
	log.Info("pubsub: listener stopped")
	return nil
}

// handle runs the listener on a message and decides its outcome. ctx is
// canceled at the end of the shutdown grace period; base is not.
func (l *listener) handle(ctx, base context.Context, m *Message) Outcome {
	log := l.p.log.With("topic", l.sub.Topic, "subscription", l.sub.Name, "message", m.ID, "attempt", m.Attempt)
	start := time.Now()
	err := l.callSafe(ctx, m)
	if err == nil {
		log.Debug("pubsub: message handled", "duration", time.Since(start).Round(time.Millisecond))
		return Outcome{Ack: true}
	}
	if ctx.Err() != nil && errors.Is(context.Cause(ctx), errShutdown) && !IsPermanent(err) {
		log.Info("pubsub: message stopped by the shutdown, to be delivered again", "error", err)
		return Outcome{}
	}
	if l.o.tries > 0 && m.Attempt == 0 && l.warned.CompareAndSwap(false, true) {
		log.Warn("pubsub: the broker doesn't count this subscription's deliveries, so Tries can't apply (with Google Pub/Sub, give the subscription a dead-letter policy)")
	}
	if !IsPermanent(err) && (l.o.tries == 0 || m.Attempt < l.o.tries) {
		d := l.backoff(m.Attempt)
		log.Warn("pubsub: message failed, to be delivered again", "error", err, "retry_in", d.Round(time.Millisecond))
		return Outcome{RetryAfter: d}
	}
	if l.o.deadLetter == "" {
		log.Error("pubsub: message failed for good, dropped (no dead-letter topic)", "error", err)
		return Outcome{Ack: true}
	}
	attrs := map[string]string{}
	maps.Copy(attrs, m.Attributes)
	attrs["anetos.topic"] = l.sub.Topic
	attrs["anetos.subscription"] = l.sub.Name
	attrs["anetos.error"] = errorText(err)
	attrs["anetos.attempts"] = strconv.Itoa(m.Attempt)
	if perr := l.deadLetter(base, Outgoing{Data: m.Data, Attributes: attrs}); perr != nil {
		d := l.backoff(m.Attempt)
		log.Error("pubsub: couldn't publish the failed message to its dead-letter topic: to be delivered again", "error", err,
			"dead_letter", l.o.deadLetter, "publish_error", perr, "retry_in", d.Round(time.Millisecond))
		return Outcome{RetryAfter: d}
	}
	log.Error("pubsub: message failed for good, sent to the dead-letter topic", "error", err, "dead_letter", l.o.deadLetter)
	return Outcome{Ack: true}
}

// deadLetter publishes a message to the dead-letter topic, trying again
// a few times (within dlTimeout), so the listener doesn't run again for a
// passing failure.
func (l *listener) deadLetter(base context.Context, out Outgoing) error {
	ctx, cancel := context.WithTimeout(base, dlTimeout)
	defer cancel()
	wait := 100 * time.Millisecond
	for {
		_, err := l.p.broker.Publish(ctx, l.o.deadLetter, out)
		if err == nil || ctx.Err() != nil {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return err
		case <-t.C:
		}
		wait *= 2
	}
}

// callSafe runs the listener with the message's context and timeout,
// recovering a panic.
func (l *listener) callSafe(ctx context.Context, m *Message) (err error) {
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, msgKey{}, m), l.o.timeout)
	defer cancel()
	if l.p.app != nil && l.p.app.HasAroundOperations() {
		var end func()
		ctx, end = l.p.app.StartOperation(ctx, anetos.Operation{Kind: "message", Name: l.sub.Topic + " (" + l.sub.Name + ")"})
		defer end()
	}
	defer func() {
		if v := recover(); v != nil {
			l.p.log.ErrorContext(ctx, "pubsub: listener panicked", "topic", l.sub.Topic, "message", m.ID, "panic", v, "stack", string(debug.Stack()))
			err = fmt.Errorf("panic: %v", v)
		}
	}()
	err = l.call(ctx, m.Data)
	if err != nil && ctx.Err() == context.DeadlineExceeded && errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s: %w", l.o.timeout, err)
	}
	return err
}

// backoff returns the wait before the delivery after attempt (1-based;
// 0, unknown, counts as 1).
func (l *listener) backoff(attempt int) time.Duration {
	attempt = max(attempt, 1)
	var d time.Duration
	if l.o.backoff != nil {
		d = l.o.backoff[min(attempt, len(l.o.backoff))-1]
	} else {
		d = 10 * time.Second
		for i := 1; i < attempt && d < 10*time.Minute; i++ {
			d *= 2
		}
		d = min(d, 10*time.Minute)
	}
	var b [8]byte
	_, _ = rand.Read(b[:])
	f := 0.8 + 0.4*float64(binary.LittleEndian.Uint64(b[:])>>11)/(1<<53)
	return time.Duration(float64(d) * f)
}

// errorText is err's message, cut to 1000 bytes of valid UTF-8, for an
// attribute.
func errorText(err error) string {
	s := strings.ToValidUTF8(err.Error(), "�")
	if len(s) > 1000 {
		s = strings.ToValidUTF8(s[:1000], "") + "…"
	}
	return s
}

// Run runs the listeners until ctx is canceled, then lets the messages
// being handled finish (see [ShutdownGrace]) and returns. A listener
// that fails (its broker returns an error) is started again after a
// backoff. Messages get ctx's values. With [New], the listeners run as
// components of the app instead: don't call Run.
func (p *PubSub) Run(ctx context.Context) error {
	p.mu.Lock()
	ls := append([]*listener(nil), p.listeners...)
	p.mu.Unlock()
	if len(ls) == 0 {
		return errors.New("pubsub: no listeners to run")
	}
	for _, l := range ls {
		if err := p.broker.Prepare(ctx, l.sub); err != nil {
			return fmt.Errorf("pubsub: prepare the subscription %s of %s: %w", l.sub.Name, l.sub.Topic, err)
		}
	}
	var wg sync.WaitGroup
	for _, l := range ls {
		wg.Go(func() {
			grace := l.o.grace
			if grace < 0 {
				grace = 15 * time.Second
			}
			// Start the listener again after a failure, as the app's
			// supervisor does, waiting 1s, doubling up to 30s.
			wait := time.Second
			for {
				start := time.Now()
				if l.run(ctx, grace, nil) == nil || ctx.Err() != nil {
					return
				}
				if time.Since(start) > 30*time.Second {
					wait = time.Second // it ran for a while: healthy again
				}
				t := time.NewTimer(wait)
				select {
				case <-ctx.Done():
					t.Stop()
					return
				case <-t.C:
				}
				wait = min(wait*2, 30*time.Second)
			}
		})
	}
	wg.Wait()
	return nil
}
