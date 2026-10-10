// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// beforeTimeoutKey holds the request's context before [Timeout] gave it
// a deadline.
type beforeTimeoutKey struct{}

// WithoutTimeout returns ctx without the deadlines of the [Timeout]
// middleware (HTTP_REQUEST_TIMEOUT, and route groups' own) and those set
// after it: it is canceled when the client goes away or the server
// stops, as the request is, but no timeout ends it. It keeps ctx's values. Use it for
// responses that stream for as long as they need, such as server-sent
// events ([Ctx.EventStream] uses it). Without the Timeout middleware, it
// returns ctx.
func WithoutTimeout(ctx context.Context) context.Context {
	parent, ok := ctx.Value(beforeTimeoutKey{}).(context.Context)
	if !ok {
		return ctx
	}
	return valuesFrom{Context: parent, values: ctx}
}

// valuesFrom is a context canceled with Context, with values's values.
type valuesFrom struct {
	context.Context
	values context.Context
}

func (v valuesFrom) Value(key any) any { return v.values.Value(key) }

// EventStream writes server-sent events, from [Ctx.EventStream]. It is safe
// for concurrent use: a goroutine may send keep-alives ([EventStream.Comment])
// while the handler sends events.
type EventStream struct {
	mu sync.Mutex
	w  http.ResponseWriter
	rc *http.ResponseController
}

// EventStream starts a response of server-sent events (text/event-stream),
// for EventSource in the browser and htmx's SSE extension:
//
//	stream, err := c.EventStream()
//	if err != nil {
//		return err
//	}
//	for update := range updates {
//		if err := stream.Send("update", update); err != nil {
//			return nil // the client went away
//		}
//	}
//
// The stream lasts as long as it needs: EventStream removes the request's
// deadline ([WithoutTimeout]; c, as a context, no longer has it) and the
// server's write timeout (HTTP_WRITE_TIMEOUT) for this response. It ends
// when the handler returns; watch c.Done() for a client that goes away,
// and the server's Stopping() to end promptly at shutdown. Once
// EventStream has returned, the response has started: a handler's error can only be
// logged, so send errors as events.
func (c *Ctx) EventStream() (*EventStream, error) {
	inner := http.NewResponseController(c.w.ResponseWriter)
	if err := inner.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, err
	}
	h := c.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // nginx: don't buffer the stream
	h.Del("Content-Length")
	c.w.WriteHeader(http.StatusOK)
	if err := inner.Flush(); err != nil {
		return nil, fmt.Errorf("web: server-sent events need a response writer that flushes: %w", err)
	}
	c.setContext(WithoutTimeout(c.ctx()))
	return &EventStream{w: c.w, rc: inner}, nil
}

// Send writes an event and flushes it: its name (the EventSource event
// type; "" for the default, "message") and its data, which may span
// lines. It returns the write's error, such as when the client has gone
// away.
func (s *EventStream) Send(event, data string) error {
	if strings.ContainsAny(event, "\r\n") {
		return fmt.Errorf("web: event name %q has a line break", event)
	}
	var b strings.Builder
	if event != "" {
		b.WriteString("event: ")
		b.WriteString(event)
		b.WriteByte('\n')
	}
	data = strings.ReplaceAll(data, "\r\n", "\n")
	for line := range strings.SplitSeq(strings.ReplaceAll(data, "\r", "\n"), "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	return s.write(b.String())
}

// Comment writes a comment, which clients ignore: a keep-alive for
// proxies that close quiet connections.
func (s *EventStream) Comment(text string) error {
	return s.write(": " + strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(text) + "\n\n")
}

func (s *EventStream) write(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.w.Write([]byte(text)); err != nil {
		return err
	}
	return s.rc.Flush()
}

// Events is [Ctx.EventStream].
//
// Deprecated: Use EventStream; Events is removed in v0.6.
//
//go:fix inline
func (c *Ctx) Events() (*EventStream, error) { return c.EventStream() }
