// SPDX-License-Identifier: Apache-2.0

package ai_test

import (
	"context"
	"errors"
	"io"
	"iter"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/web"
)

func TestPriceCost(t *testing.T) {
	p := ai.Price{Input: 3, Output: 15, CacheRead: 0.3, CacheWrite: 3.75}
	u := ai.Usage{InputTokens: 1_000_000, OutputTokens: 100_000, CacheReadTokens: 400_000, CacheWriteTokens: 100_000}
	// 500k plain input at 3, 400k read at 0.3, 100k written at 3.75, 100k output at 15.
	if got, want := p.Cost(u), 1.5+0.12+0.375+1.5; abs(got-want) > 1e-9 {
		t.Errorf("Cost = %v, want %v", got, want)
	}
	// Without cache prices, cached tokens cost what input does.
	if got := (ai.Price{Input: 2}).Cost(ai.Usage{InputTokens: 1_000_000, CacheReadTokens: 500_000}); got != 2 {
		t.Errorf("without cache prices: %v", got)
	}
}

func abs(f float64) float64 { return max(f, -f) }

func TestBudget(t *testing.T) {
	ctx := cache.WithCache(context.Background(), cache.NewWithStore(cache.NewMemoryStore(), "t:"))
	f := ai.NewFake(ai.FakeText("one two three four five six seven eight"), ai.FakeText("never"))
	c := ai.NewWithProvider(f)
	c.TrackUsage(ai.UsageConfig{Budget: func(_ context.Context, userID string) (ai.Budget, error) {
		if userID == "broken" {
			return ai.Budget{}, errors.New("plans unavailable")
		}
		return ai.Budget{Tokens: 10, Per: time.Hour}, nil
	}})
	ctx = ai.WithClient(ctx, c)
	quiet(t)

	// No database: the record is logged as failed; the budget counts.
	if _, err := ai.Generate(ctx, "hello there", ai.ForUser("ada")); err != nil {
		t.Fatal(err)
	}
	_, err := ai.Generate(ctx, "again", ai.ForUser("ada"))
	be, ok := errors.AsType[*ai.BudgetError](err)
	if !ok || web.StatusOf(err) != http.StatusTooManyRequests || be.RetryAfter <= 0 || be.RetryAfter > time.Hour {
		t.Fatalf("over budget: %v", err)
	}
	he, _ := errors.AsType[*web.HTTPError](err)
	if he == nil || !strings.HasPrefix(he.Message, "You've reached your AI usage limit. Try again in ") {
		t.Errorf("message: %v", he)
	}
	if f.Remaining() != 1 {
		t.Error("the model was asked")
	}
	// Another user has their own budget; no user, none.
	if _, err := ai.Generate(ctx, "hi", ai.ForUser("bob")); err != nil {
		t.Error(err)
	}
	if _, err := ai.Generate(ctx, "hi", ai.ForUser("broken")); err == nil || !strings.Contains(err.Error(), "plans unavailable") {
		t.Errorf("a failing Budget: %v", err)
	}
	// Budgets need the cache.
	f.Add(ai.FakeText("x"))
	if _, err := ai.Generate(ai.WithClient(context.Background(), c), "hi", ai.ForUser("ada")); !errors.Is(err, cache.ErrNoCache) {
		t.Errorf("without a cache: %v", err)
	}
}

// quiet keeps the default logger's output out of the test's.
func quiet(t *testing.T) {
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
}

func TestSSE(t *testing.T) {
	f := ai.NewFake()
	r := web.NewRouter()
	r.UseGlobal(web.Timeout(time.Minute))
	r.Get("/answer", func(c *web.Ctx) error {
		return ai.SSE(c, ai.Stream(ai.WithClient(c, ai.NewWithProvider(f)), "hi", ai.Tools(lookupTool)))
	})
	get := func() string {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/answer", nil))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "text/event-stream" {
			t.Errorf("status %d, %v", w.Code, w.Header())
		}
		return w.Body.String()
	}
	quiet(t)
	f.Add(ai.FakeToolCall("lookup", map[string]int{"number": 1}), ai.FakeText("Order <1> & co"))
	// The fake streams word by word.
	want := "event: tool\ndata: lookup\n\nevent: text\ndata: Order \n\nevent: text\ndata: &lt;1&gt; \n\n" +
		"event: text\ndata: &amp; \n\nevent: text\ndata: co\n\nevent: done\ndata: \n\n"
	if got := get(); got != want {
		t.Errorf("stream:\n%q\nwant\n%q", got, want)
	}
	f.Add(ai.FakeError(web.Error(http.StatusTooManyRequests, "Slow <down>.")))
	if got := get(); got != "event: error\ndata: Slow &lt;down&gt;.\n\nevent: done\ndata: \n\n" {
		t.Errorf("4xx: %q", got)
	}
	f.Add(ai.FakeError(errors.New("database password is hunter2")))
	if got := get(); got != "event: error\ndata: Something went wrong. Try again.\n\nevent: done\ndata: \n\n" {
		t.Errorf("5xx: %q", got)
	}
}

type lookupInput struct {
	Number int `json:"number"`
}

var lookupTool = ai.Func("lookup", "Look up an order", func(_ context.Context, in lookupInput) (string, error) {
	return "shipped", nil
})

func TestStoppedStreamCounts(t *testing.T) {
	ctx := cache.WithCache(context.Background(), cache.NewWithStore(cache.NewMemoryStore(), "t:"))
	f := ai.NewFake(ai.FakeText("one two three four five six seven eight"), ai.FakeText("never"))
	c := ai.NewWithProvider(f)
	c.TrackUsage(ai.UsageConfig{Budget: func(context.Context, string) (ai.Budget, error) {
		return ai.Budget{Tokens: 20, Per: time.Hour}, nil
	}})
	ctx = ai.WithClient(ctx, c)
	quiet(t)
	// The reader leaves after the first piece: the request's input and
	// what was streamed count all the same.
	for ev, err := range ai.Stream(ctx, strings.Repeat("a long question ", 10), ai.ForUser("ada")) {
		if err != nil {
			t.Fatal(err)
		}
		if ev.Kind == ai.EventText {
			break
		}
	}
	if _, err := ai.Generate(ctx, "again", ai.ForUser("ada")); !errors.As(err, new(*ai.BudgetError)) {
		t.Errorf("after a stopped stream: %v", err)
	}
}

func TestSSEKeepAlive(t *testing.T) {
	defer ai.SetKeepAlive(5 * time.Millisecond)()
	slow := ai.Func("slow", "Take a while", func(context.Context, struct{}) (string, error) {
		time.Sleep(50 * time.Millisecond)
		return "done", nil
	})
	f := ai.NewFake(ai.FakeToolCall("slow", struct{}{}), ai.FakeText("Finished."))
	r := web.NewRouter()
	r.Get("/answer", func(c *web.Ctx) error {
		return ai.SSE(c, ai.Stream(ai.WithClient(c, ai.NewWithProvider(f)), "go", ai.Tools(slow)))
	})
	quiet(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/answer", nil))
	if body := w.Body.String(); !strings.Contains(body, ": keep-alive\n\n") || !strings.HasSuffix(body, "event: done\ndata: \n\n") {
		t.Errorf("stream:\n%s", body)
	}
}

// hangingProvider answers nothing until the request's context ends.
type hangingProvider struct{}

func (hangingProvider) Name() string { return "hanging" }
func (hangingProvider) Generate(ctx context.Context, _ *ai.Request) (*ai.Response, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (p hangingProvider) Stream(ctx context.Context, req *ai.Request) iter.Seq2[ai.Event, error] {
	return func(yield func(ai.Event, error) bool) {
		_, err := p.Generate(ctx, req)
		yield(ai.Event{}, err)
	}
}

func TestInterruptedRequestCounts(t *testing.T) {
	ctx := cache.WithCache(context.Background(), cache.NewWithStore(cache.NewMemoryStore(), "t:"))
	c := ai.NewWithProvider(hangingProvider{})
	c.TrackUsage(ai.UsageConfig{Budget: func(context.Context, string) (ai.Budget, error) {
		return ai.Budget{Tokens: 20, Per: time.Hour}, nil
	}})
	ctx = ai.WithClient(ctx, c)
	quiet(t)
	// Timed out before a word: the input was sent, and counts.
	for range ai.Stream(ctx, strings.Repeat("a long question ", 10), ai.ForUser("ada"), ai.Timeout(10*time.Millisecond)) {
	}
	if _, err := ai.Generate(ctx, "again", ai.ForUser("ada")); !errors.As(err, new(*ai.BudgetError)) {
		t.Errorf("after an interrupted request: %v", err)
	}
}
