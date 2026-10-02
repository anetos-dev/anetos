// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"sort"
)

// HTTPError is an error with an HTTP status and a message that is safe to
// show to clients. Return one from a handler to control the error response:
//
//	if post == nil {
//		return web.Error(http.StatusNotFound, "post not found")
//	}
//
// Err holds an internal cause for logs; it is shown to clients only in debug
// mode.
type HTTPError struct {
	Status  int               // the HTTP status, 400–599
	Message string            // client-safe; defaults to the status text
	Fields  map[string]string // per-field problems, e.g. from binding or validation
	Err     error             // internal cause
}

// Error returns an [HTTPError] with the given status and client-safe
// message. An empty message uses the standard status text.
func Error(status int, message string) *HTTPError {
	return &HTTPError{Status: status, Message: message}
}

// Errorf is like [Error] with a formatted message.
func Errorf(status int, format string, args ...any) *HTTPError {
	return &HTTPError{Status: status, Message: fmt.Sprintf(format, args...)}
}

// Wrap sets the internal cause and returns e, for chaining.
func (e *HTTPError) Wrap(err error) *HTTPError {
	e.Err = err
	return e
}

// Error implements the error interface.
func (e *HTTPError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Err != nil {
		return fmt.Sprintf("%d %s: %v", e.Status, msg, e.Err)
	}
	return fmt.Sprintf("%d %s", e.Status, msg)
}

// Unwrap returns Err, for errors.Is and errors.As.
func (e *HTTPError) Unwrap() error { return e.Err }

// HTTPStatus implements [StatusCoder].
func (e *HTTPError) HTTPStatus() int { return e.Status }

// ClientMessage returns the client-safe message: Message, or the status
// text. Clients see it for a 4xx error (a 5xx one shows only the status
// text, outside debug mode); package ai tells it to a model when a tool
// fails with a 4xx error.
func (e *HTTPError) ClientMessage() string {
	if e.Message == "" {
		return http.StatusText(e.Status)
	}
	return e.Message
}

// ClientFields returns Fields, the per-field messages clients see.
// Package ai tells them to a model when a tool fails with a 4xx error.
func (e *HTTPError) ClientFields() map[string]string { return e.Fields }

// StatusCoder can be implemented by domain errors to choose their HTTP
// status without depending on this package, for example a "not found" error
// in a data layer returning 404.
type StatusCoder interface {
	// HTTPStatus returns the status, 400–599 (others are ignored).
	HTTPStatus() int
}

// FieldErrorer is implemented by errors that carry one message per request
// field, such as *[validate.Errors]. The default error handler lists them in
// the "errors" member of problem responses and in the HTML error page.
type FieldErrorer interface {
	// FieldErrors returns a message per field name.
	FieldErrors() map[string]string
}

// PanicError is the error passed to the error handler when a handler panics.
type PanicError struct {
	Value any    // the value passed to panic
	Stack []byte // the handler goroutine's stack
}

func newPanicError(v any) *PanicError { return &PanicError{Value: v, Stack: debug.Stack()} }

// Error implements the error interface.
func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// StatusOf returns the HTTP status for err: the status of an [HTTPError] or
// [StatusCoder] in its chain, 503 for a deadline exceeded, 499 (client closed
// request) for cancellation, and 500 otherwise.
func StatusOf(err error) int {
	var sc StatusCoder
	if errors.As(err, &sc) {
		if s := sc.HTTPStatus(); s >= 400 && s <= 599 {
			return s
		}
	}
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled):
		return statusClientClosed
	}
	return http.StatusInternalServerError
}

// statusClientClosed is the de facto status for requests the client
// abandoned. It is only logged; nothing can be sent.
const statusClientClosed = 499

// DefaultErrorHandler logs err and writes an error response, unless the
// response has already started (then it only logs).
//
// Clients that want JSON (see [Ctx.WantsJSON]) get RFC 9457 problem details;
// others get an HTML page. A browser's form post that fails validation
// (422, or 400 with field errors) on a route with a session is redirected
// back instead, with the errors and the submitted input flashed for the
// form to show (see the view package's Errors and Old). Messages of 5xx errors are replaced by the
// generic status text unless the router is in debug mode, so internal
// details never leak in production. In debug mode, HTML errors show the
// error chain, stack trace (for panics) and request details.
func DefaultErrorHandler(c *Ctx, err error) {
	status := StatusOf(err)
	log := c.Logger()

	if status == statusClientClosed && c.r.Context().Err() == nil {
		// Canceled by something inside the app, not by the client, who is
		// still waiting for an answer.
		status = http.StatusInternalServerError
	}

	switch {
	case status == statusClientClosed:
		log.Debug("client closed request", "path", c.r.URL.Path)
		return
	case status >= 500:
		attrs := []any{"error", err, "method", c.r.Method, "path", c.r.URL.Path, "status", status}
		if pe, ok := errors.AsType[*PanicError](err); ok {
			attrs = append(attrs, "stack", string(pe.Stack))
		}
		log.Error("request failed", attrs...)
	default:
		log.Debug("request error", "error", err, "status", status)
	}

	if c.w.started() {
		return
	}
	if redirectBack(c, err, status) {
		return
	}

	p := newProblem(c, err, status)
	if c.WantsJSON() {
		c.w.Header().Set("Content-Type", "application/problem+json")
		c.w.Header().Set("X-Content-Type-Options", "nosniff")
		c.w.WriteHeader(status)
		if c.r.Method != http.MethodHead {
			_, _ = c.w.Write(p.json())
		}
		return
	}
	renderErrorPage(c, p)
}

// problem is an RFC 9457 problem details object.
type problem struct {
	Type      string            `json:"type"`
	Title     string            `json:"title"`
	Status    int               `json:"status"`
	Detail    string            `json:"detail,omitempty"`
	Errors    map[string]string `json:"errors,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Debug     *problemDebug     `json:"debug,omitempty"`
}

type problemDebug struct {
	Error string   `json:"error"`
	Chain []string `json:"chain,omitempty"`
	Stack string   `json:"stack,omitempty"`
}

func newProblem(c *Ctx, err error, status int) *problem {
	// err's own status may differ from the one chosen (e.g. an internal
	// cancellation reported as 500); only show HTTPError details that match.
	p := &problem{
		Type:      "about:blank",
		Title:     http.StatusText(status),
		Status:    status,
		RequestID: RequestID(c.r.Context()),
	}
	if p.Title == "" {
		p.Title = fmt.Sprintf("Error %d", status)
	}
	if status < 500 || c.router.core.debug {
		he, isHTTPError := errors.AsType[*HTTPError](err)
		if isHTTPError {
			p.Detail, p.Errors = he.Message, he.Fields
		}
		if fe := FieldErrorer(nil); p.Errors == nil && errors.As(err, &fe) {
			p.Errors = fe.FieldErrors()
			if !isHTTPError {
				p.Detail = "The given data was invalid."
			}
		}
	}
	if c.router.core.debug {
		d := &problemDebug{Error: err.Error()}
		for e := errors.Unwrap(err); e != nil; e = errors.Unwrap(e) {
			d.Chain = append(d.Chain, fmt.Sprintf("%T: %v", e, e))
		}
		if pe, ok := errors.AsType[*PanicError](err); ok {
			d.Stack = string(pe.Stack)
		}
		p.Debug = d
	}
	return p
}

func (p *problem) json() []byte {
	b, err := marshalJSON(p)
	if err != nil {
		return []byte(`{"type":"about:blank","status":500}`)
	}
	return b
}

// sortedKeys returns m's keys in order, for stable HTML output.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
