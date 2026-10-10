// SPDX-License-Identifier: Apache-2.0

package web_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"anetos.dev/anetos/web"
)

func TestEvents(t *testing.T) {
	r := web.NewRouter()
	r.UseGlobal(web.Timeout(50 * time.Millisecond))
	var hadDeadline bool
	var lateErr error
	r.Get("/events", func(c *web.Ctx) error {
		stream, err := c.EventStream()
		if err != nil {
			return err
		}
		_, hadDeadline = c.Deadline()
		if err := stream.Comment("hello\nthere"); err != nil {
			return err
		}
		if err := stream.Send("", "first"); err != nil {
			return err
		}
		time.Sleep(100 * time.Millisecond) // past the request timeout
		lateErr = c.Err()
		if err := stream.Send("text", "two\r\nlines\rand a third"); err != nil {
			return err
		}
		if err := stream.Send("bad\nname", "x"); err == nil {
			t.Error("an event name with a line break was sent")
		}
		return stream.Send("done", "")
	})
	srv := httptest.NewServer(r)
	defer srv.Close()
	res, err := http.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" || res.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("headers: %v", res.Header)
	}
	// The first event arrives before the handler ends: it's flushed.
	br := bufio.NewReader(res.Body)
	first, err := br.ReadString('\n')
	if err != nil || first != ": hello there\n" {
		t.Errorf("first line %q, %v", first, err)
	}
	rest, err := io.ReadAll(br)
	if err != nil {
		t.Fatal(err)
	}
	want := "\ndata: first\n\nevent: text\ndata: two\ndata: lines\ndata: and a third\n\nevent: done\ndata: \n\n"
	if string(rest) != want {
		t.Errorf("stream:\n%q\nwant\n%q", rest, want)
	}
	if hadDeadline || lateErr != nil {
		t.Errorf("the stream kept the request's deadline: %v, %v", hadDeadline, lateErr)
	}
}

func TestWithoutTimeout(t *testing.T) {
	type key struct{}
	base, cancelBase := context.WithCancel(context.Background())
	var ctx context.Context
	// A route group's Timeout inside the server's: both go.
	h := web.Timeout(time.Hour)(web.Timeout(time.Minute)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctx = web.WithoutTimeout(context.WithValue(r.Context(), key{}, "v"))
	})))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(base, http.MethodGet, "/", nil))
	if _, ok := ctx.Deadline(); ok || ctx.Value(key{}) != "v" {
		t.Error("deadline kept, or value lost")
	}
	cancelBase() // the client went away
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Error("not canceled with the request")
	}
	plain := context.WithValue(context.Background(), key{}, 1)
	if web.WithoutTimeout(plain) != plain {
		t.Error("without the middleware, the context changed")
	}
}
