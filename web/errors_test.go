// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type notFoundErr struct{}

func (notFoundErr) Error() string   { return "record not found" }
func (notFoundErr) HTTPStatus() int { return http.StatusNotFound }

func TestStatusOf(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{Error(418, "x"), 418},
		{fmt.Errorf("wrapped: %w", Error(403, "")), 403},
		{notFoundErr{}, 404},
		{fmt.Errorf("repo: %w", notFoundErr{}), 404},
		{context.DeadlineExceeded, 503},
		{context.Canceled, 499},
		{errors.New("boom"), 500},
	}
	for _, tt := range tests {
		if got := StatusOf(tt.err); got != tt.want {
			t.Errorf("StatusOf(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestHTTPErrorFormatting(t *testing.T) {
	cause := errors.New("db down")
	e := Error(503, "try later").Wrap(cause)
	if e.Error() != "503 try later: db down" || !errors.Is(e, cause) {
		t.Errorf("Error() = %q", e.Error())
	}
	if Errorf(404, "post %d not found", 7).Error() != "404 post 7 not found" {
		t.Error("Errorf")
	}
	if Error(404, "").Error() != "404 Not Found" {
		t.Error("default message")
	}
}

func errRouter(debug bool, logs *bytes.Buffer) *Router {
	r := NewRouter(WithDebug(debug), WithLogger(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))))
	r.UseGlobal(RequestIDs)
	r.Get("/internal", func(*Ctx) error { return fmt.Errorf("saving post: %w", errors.New("connection refused")) })
	r.Get("/notfound", func(*Ctx) error { return Error(404, "post not found") })
	r.Get("/fields", func(*Ctx) error {
		return &HTTPError{Status: 422, Message: "Invalid input.", Fields: map[string]string{"title": "is required"}}
	})
	r.Get("/panic", func(*Ctx) error { panic("kaboom") })
	r.Get("/started", func(c *Ctx) error {
		_ = c.Text(200, "partial")
		return errors.New("too late")
	})
	r.Get("/canceled", func(*Ctx) error { return context.Canceled })
	return r
}

func TestErrorResponsesJSON(t *testing.T) {
	var logs bytes.Buffer
	r := errRouter(false, &logs)
	json := []string{"Accept", "application/json", "X-Request-ID", "req-123"}

	got := do(t, r, "GET", "/internal", nil, json...)
	if got.status != 500 || got.header.Get("Content-Type") != "application/problem+json" {
		t.Fatalf("= %d %v", got.status, got.header)
	}
	p := decode[problem](t, got.body)
	if p.Title != "Internal Server Error" || p.Detail != "" || p.Debug != nil || p.RequestID != "req-123" {
		t.Errorf("production 500 leaked details or lost request id: %+v", p)
	}
	if !strings.Contains(logs.String(), "connection refused") || !strings.Contains(logs.String(), "request_id=req-123") {
		t.Errorf("500 not logged with cause and request id:\n%s", logs.String())
	}

	p = decode[problem](t, do(t, r, "GET", "/notfound", nil, json...).body)
	if p.Status != 404 || p.Detail != "post not found" {
		t.Errorf("404 problem = %+v", p)
	}
	p = decode[problem](t, do(t, r, "GET", "/fields", nil, json...).body)
	if p.Status != 422 || p.Errors["title"] != "is required" {
		t.Errorf("fields problem = %+v", p)
	}
}

func TestErrorResponsesDebug(t *testing.T) {
	var logs bytes.Buffer
	r := errRouter(true, &logs)

	p := decode[problem](t, do(t, r, "GET", "/panic", nil, "Accept", "application/json").body)
	if p.Status != 500 || p.Debug == nil || !strings.Contains(p.Debug.Stack, "errors_test.go") {
		t.Errorf("debug panic problem = %+v", p)
	}
	if !strings.Contains(logs.String(), "panic: kaboom") {
		t.Error("panic not logged")
	}

	page := do(t, r, "GET", "/internal", nil, "Accept", "text/html", "Authorization", "Bearer secret", "X-Custom", "visible")
	for _, want := range []string{"500", "connection refused", "*errors.errorString", "[redacted]", "X-Custom", "visible", "APP_DEBUG=true"} {
		if !strings.Contains(page.body, want) {
			t.Errorf("debug page missing %q", want)
		}
	}
	if strings.Contains(page.body, "Bearer secret") {
		t.Error("debug page leaked the Authorization header")
	}
}

func TestErrorPageProduction(t *testing.T) {
	var logs bytes.Buffer
	r := errRouter(false, &logs)
	page := do(t, r, "GET", "/internal", nil)
	if page.status != 500 || strings.Contains(page.body, "connection refused") || strings.Contains(page.body, "APP_DEBUG") {
		t.Errorf("production page leaked details: %s", page.body)
	}
	page = do(t, r, "GET", "/fields", nil)
	if !strings.Contains(page.body, "is required") || !strings.Contains(page.body, "title") {
		t.Errorf("field errors missing from page")
	}
	if got := do(t, r, "HEAD", "/notfound", nil); got.status != 404 || got.body != "" {
		t.Errorf("HEAD error = %d %q", got.status, got.body)
	}
}

func TestErrorAfterResponseStartedAborts(t *testing.T) {
	var logs bytes.Buffer
	r := errRouter(false, &logs)
	defer func() {
		// net/http turns this panic into an aborted connection, so the
		// client can't mistake the truncated body for a complete response.
		if v := recover(); v != http.ErrAbortHandler { //nolint:errorlint // identity comparison, as net/http does
			t.Errorf("recover() = %v, want http.ErrAbortHandler", v)
		}
		if !strings.Contains(logs.String(), "too late") {
			t.Errorf("error not logged: %s", logs.String())
		}
	}()
	do(t, r, "GET", "/started", nil)
}

func TestClientCanceledWritesNothing(t *testing.T) {
	var logs bytes.Buffer
	r := errRouter(false, &logs)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client went away
	req := httptest.NewRequestWithContext(ctx, "GET", "/canceled", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 || strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("canceled request: %d %q; logs: %s", rec.Code, rec.Body.String(), logs.String())
	}
}

func TestInternalCancellationIs500(t *testing.T) {
	var logs bytes.Buffer
	r := errRouter(false, &logs)
	// The request itself is fine; the handler returned a cancellation from
	// some internal context. The client must not get an empty 200.
	got := do(t, r, "GET", "/canceled", nil, "Accept", "application/json")
	if got.status != 500 || !strings.Contains(logs.String(), "level=ERROR") {
		t.Errorf("= %d %q", got.status, got.body)
	}
}

func TestCustomErrorHandler(t *testing.T) {
	r := newTestRouter(WithErrorHandler(func(c *Ctx, err error) {
		_ = c.Text(StatusOf(err), "custom: "+err.Error())
	}))
	r.Get("/x", func(*Ctx) error { return Error(409, "conflict") })
	if got := do(t, r, "GET", "/x", nil); got.status != 409 || got.body != "custom: 409 conflict" {
		t.Errorf("= %d %q", got.status, got.body)
	}
	if got := do(t, r, "GET", "/missing", nil); got.status != 404 || got.body != "custom: 404 Not Found" {
		t.Errorf("404 via custom handler = %d %q", got.status, got.body)
	}
}
