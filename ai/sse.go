// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"html"
	"iter"
	"sync"
	"time"

	"anetos.dev/anetos/web"
)

// keepAlive is how often SSE sends a comment.
var keepAlive = 15 * time.Second

// SSE sends a streamed answer ([Stream], [Conversation.StreamReply]…) to
// the browser as server-sent events ([web.Ctx.Events]), ready for htmx's
// SSE extension or EventSource:
//
//   - "text": a piece of the answer's text, HTML-escaped, to append;
//   - "tool": the name of a tool the model calls, HTML-escaped;
//   - "error": the call failed: for a 4xx error (a spent budget, say)
//     its message for the user, else a general one; the error is logged;
//   - "done": the stream ends (empty data): after the answer, an error,
//     or events that end without one (nothing to answer).
//
// Close the EventSource on "done": browsers reconnect to a stream that
// ends, which would ask again. In a page, with htmx and its SSE
// extension (both in package view/htmx):
//
//	<div hx-ext="sse" sse-connect="/chat/7/reply" sse-close="done">
//		<p sse-swap="text" hx-swap="beforeend"></p>
//		<p sse-swap="error"></p>
//	</div>
//
// The answer goes on without the request's timeout (HTTP_REQUEST_TIMEOUT),
// with a comment every 15 seconds to keep proxies from closing a quiet
// stream, and stops when the browser goes away. SSE returns the error of
// starting the stream; after that, errors are events.
func SSE(c *web.Ctx, events iter.Seq2[Event, error]) error {
	stream, err := c.Events()
	if err != nil {
		return err
	}
	// Keep-alives while the model thinks or tools run: proxies close
	// quiet connections, and the browser would reconnect and ask again.
	stop, done := make(chan struct{}), make(chan struct{})
	var once sync.Once
	quiet := func() { // no keep-alive after the last event, or the handler's return
		once.Do(func() {
			close(stop)
			<-done
		})
	}
	defer quiet()
	every := keepAlive
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = stream.Comment("keep-alive")
			}
		}
	}()
	for ev, err := range events {
		if err != nil {
			msg := "Something went wrong. Try again."
			switch {
			case clientStatus(err) != 0:
				msg = clientMessage(err)
			case c.Err() != nil:
				return nil // the browser went away
			default:
				c.Logger().ErrorContext(c, "ai: streamed answer failed", "error", err)
			}
			quiet()
			if stream.Send("error", html.EscapeString(msg)) == nil {
				_ = stream.Send("done", "")
			}
			return nil
		}
		var sendErr error
		switch ev.Kind {
		case EventText:
			sendErr = stream.Send("text", html.EscapeString(ev.Text))
		case EventToolCall:
			sendErr = stream.Send("tool", html.EscapeString(ev.ToolCall.Name))
		}
		if sendErr != nil {
			return nil // the browser went away: stopping the loop stops the answer
		}
	}
	quiet()
	_ = stream.Send("done", "")
	return nil
}
