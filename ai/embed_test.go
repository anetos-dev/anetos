// SPDX-License-Identifier: Apache-2.0

package ai_test

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"anetos.dev/anetos"
	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/config"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/web"
)

func TestFakeEmbed(t *testing.T) {
	f := ai.NewFake()
	ctx := ai.WithClient(context.Background(), ai.New(f))
	vs, err := ai.Embed(ctx, 0, "Cats sleep all day", "cats SLEEP", "rockets go to orbit", "")
	check(t, err)
	if len(vs) != 4 || len(vs[0]) != 64 {
		t.Fatalf("vectors: %d of %d", len(vs), len(vs[0]))
	}
	near, _ := db.CosineDistance(vs[0], vs[1])
	far, _ := db.CosineDistance(vs[0], vs[2])
	if near >= far {
		t.Errorf("texts sharing words aren't nearer: %v >= %v", near, far)
	}
	again, err := ai.EmbedQuery(ctx, 0, "Cats sleep all day")
	check(t, err)
	if !slices.Equal(again, vs[0]) {
		t.Error("the fake isn't deterministic")
	}
	reqs := f.Embeddings()
	if len(reqs) != 2 || reqs[0].Purpose != ai.EmbedForDocument || reqs[1].Purpose != ai.EmbedForQuery || reqs[0].Model != "fake-embedding" {
		t.Errorf("requests: %+v", reqs)
	}
	if f.Remaining() != 0 || len(f.Requests()) != 0 {
		t.Error("embeddings used replies")
	}
	if got := ai.New(f).EmbeddingModel(); got != "fake-embedding" {
		t.Errorf("EmbeddingModel = %q", got)
	}
}

func TestEmbedBatches(t *testing.T) {
	f := ai.NewFake()
	ctx := ai.WithClient(context.Background(), ai.New(f))
	texts := make([]string, 200)
	for i := range texts {
		texts[i] = strings.Repeat("word ", i%7+1)
	}
	vs, err := ai.Embed(ctx, 8, texts...)
	check(t, err)
	if len(vs) != 200 || len(vs[199]) != 8 {
		t.Fatalf("%d vectors", len(vs))
	}
	var sizes []int
	for _, r := range f.Embeddings() {
		sizes = append(sizes, len(r.Inputs))
		if r.Dimensions != 8 {
			t.Errorf("dimensions %d", r.Dimensions)
		}
	}
	if !slices.Equal(sizes, []int{96, 96, 8}) {
		t.Errorf("batches %v", sizes)
	}
}

// wrongEmbedder makes vectors of the wrong size, or too few.
type wrongEmbedder struct{ few bool }

func (w wrongEmbedder) Embed(_ context.Context, req *ai.EmbedRequest) (*ai.EmbedResponse, error) {
	if w.few {
		return &ai.EmbedResponse{}, nil
	}
	return &ai.EmbedResponse{Vectors: []ai.Vector{{1, 2}}}, nil
}

// textOnly is a provider without embeddings.
type textOnly struct{}

func (textOnly) Name() string { return "text-only" }
func (textOnly) Generate(context.Context, *ai.Request) (*ai.Response, error) {
	return nil, errors.New("no")
}
func (textOnly) Stream(context.Context, *ai.Request) iter.Seq2[ai.Event, error] {
	return func(func(ai.Event, error) bool) {}
}

func TestEmbedErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := ai.Embed(ctx, 0, "x"); !errors.Is(err, ai.ErrNoClient) {
		t.Errorf("no client: %v", err)
	}
	c := ai.New(textOnly{})
	if _, err := ai.Embed(ai.WithClient(ctx, c), 0, "x"); !errors.Is(err, ai.ErrNoEmbedder) {
		t.Errorf("no embedder: %v", err)
	}
	if c.EmbeddingModel() != "" {
		t.Error("a model without embeddings")
	}
	c.SetEmbedder(wrongEmbedder{}, "")
	if _, err := ai.Embed(ai.WithClient(ctx, c), 0, "x"); err == nil || !strings.Contains(err.Error(), "AI_EMBEDDING_MODEL isn't set") {
		t.Errorf("no model: %v", err)
	}
	c.SetEmbedder(wrongEmbedder{}, "m")
	if _, err := ai.Embed(ai.WithClient(ctx, c), 3, "x"); err == nil || !strings.Contains(err.Error(), "vectors of 2 dimensions, not the 3") {
		t.Errorf("wrong size: %v", err)
	}
	if _, err := ai.Embed(ai.WithClient(ctx, c), -1, "x"); err == nil {
		t.Error("negative dimensions")
	}
	c.SetEmbedder(wrongEmbedder{few: true}, "m")
	if _, err := ai.Embed(ai.WithClient(ctx, c), 0, "x"); err == nil || !strings.Contains(err.Error(), "0 vectors for 1 texts") {
		t.Errorf("too few: %v", err)
	}
	// Fake replaces a separate embedder too.
	f := c.Fake()
	if _, err := ai.Embed(ai.WithClient(ctx, c), 0, "x"); err != nil || len(f.Embeddings()) != 1 {
		t.Errorf("after Fake: %v, %d", err, len(f.Embeddings()))
	}
}

func TestSplitChunks(t *testing.T) {
	text := "Title\n\nFirst  paragraph\nwraps.\r\n\r\nSecond. It has two sentences! Third? " + strings.Repeat("long ", 30) + "\n\n" + strings.Repeat("x", 45)
	chunks := ai.SplitChunks(text, 40)
	for _, c := range chunks {
		if n := utf8.RuneCountInString(c); n > 40 || n == 0 {
			t.Errorf("chunk of %d: %q", n, c)
		}
	}
	if chunks[0] != "Title\n\nFirst paragraph wraps.\n\nSecond." {
		t.Errorf("paragraphs, and a long one's sentences, fill a chunk: %q", chunks[0])
	}
	if chunks[1] != "It has two sentences! Third? long long" {
		t.Errorf("a long sentence's words: %q", chunks[1])
	}
	if last := chunks[len(chunks)-2:]; last[0] != strings.Repeat("x", 40) || last[1] != "xxxxx" {
		t.Errorf("a long word: %q", last)
	}
	if got := ai.SplitChunks(" \n\n ", 10); len(got) != 0 {
		t.Errorf("blank: %q", got)
	}
	if got := ai.SplitChunks("héllo wörld", 5); !slices.Equal(got, []string{"héllo", "wörld"}) {
		t.Errorf("runes: %q", got)
	}
}

func TestForAppEmbeddings(t *testing.T) {
	embedder := ai.Driver{Name: "vectors", Open: func(_ *anetos.App, cfg ai.Config) (ai.Provider, error) {
		if cfg.Model != "vec-1" {
			t.Errorf("the embeddings driver's model: %q", cfg.Model)
		}
		return ai.NewFake(), nil
	}}
	text := ai.Driver{Name: "text", Open: func(*anetos.App, ai.Config) (ai.Provider, error) { return textOnly{}, nil }}
	for _, tc := range []struct {
		env  config.Map
		want string // an error, or the model
	}{
		{config.Map{"AI_PROVIDER": "fake"}, "fake-embedding"},
		{config.Map{"AI_PROVIDER": "fake", "AI_EMBEDDING_MODEL": "named"}, "named"},
		{config.Map{"AI_PROVIDER": "text"}, ""},
		{config.Map{"AI_PROVIDER": "text", "AI_EMBEDDING_PROVIDER": "vectors", "AI_EMBEDDING_MODEL": "vec-1"}, "vec-1"},
		{config.Map{"AI_PROVIDER": "text", "AI_EMBEDDING_PROVIDER": "fake"}, "fake-embedding"},
		{config.Map{"AI_PROVIDER": "text", "AI_EMBEDDING_PROVIDER": "vectors"}, "AI_EMBEDDING_PROVIDER is vectors, but AI_EMBEDDING_MODEL isn't set"},
		{config.Map{"AI_PROVIDER": "fake", "AI_EMBEDDING_PROVIDER": "nope"}, `AI_EMBEDDING_PROVIDER is "nope"`},
		{config.Map{"AI_PROVIDER": "vectors", "AI_MODEL": "vec-1", "AI_EMBEDDING_PROVIDER": "vectors"}, "AI_EMBEDDING_PROVIDER is vectors, but AI_EMBEDDING_MODEL isn't set"},
		{config.Map{"AI_PROVIDER": "vectors", "AI_MODEL": "vec-1", "AI_EMBEDDING_PROVIDER": "vectors", "AI_EMBEDDING_MODEL": "vec-1"}, "vec-1"},
		{config.Map{"AI_PROVIDER": "fake", "AI_EMBEDDING_PROVIDER": "text", "AI_EMBEDDING_MODEL": "m"}, "text, which has no embeddings"},
	} {
		c, err := ai.ForApp(newApp(t, tc.env, nil), embedder, text)
		switch {
		case err != nil:
			if !strings.Contains(err.Error(), tc.want) || tc.want == "" {
				t.Errorf("%v: %v", tc.env, err)
			}
		case c.EmbeddingModel() != tc.want:
			t.Errorf("%v: model %q, want %q", tc.env, c.EmbeddingModel(), tc.want)
		}
	}
}

func TestEmbedHTTPStatus(t *testing.T) {
	// The fake's embeddings fail as the context does.
	ctx, cancel := context.WithCancel(ai.WithClient(context.Background(), ai.New(ai.NewFake())))
	cancel()
	if _, err := ai.Embed(ctx, 0, "x"); !errors.Is(err, context.Canceled) || web.StatusOf(err) == http.StatusOK {
		t.Errorf("canceled: %v", err)
	}
}

func TestSplitChunksEdges(t *testing.T) {
	// A line of spaces is a paragraph break.
	if got := ai.SplitChunks("one\n  \ntwo", 4); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("a blank line of spaces: %q", got)
	}
	// Full-width sentence ends split Chinese and Japanese text.
	if got := ai.SplitChunks("猫は寝る。犬は走る。鳥は飛ぶ。", 6); !slices.Equal(got, []string{"猫は寝る。", "犬は走る。", "鳥は飛ぶ。"}) {
		t.Errorf("full-width sentences: %q", got)
	}
	// A long run without spaces is split in linear time.
	start := time.Now()
	got := ai.SplitChunks(strings.Repeat("a", 4<<20), 2000)
	if len(got) != (4<<20+1999)/2000 || time.Since(start) > 2*time.Second {
		t.Errorf("%d chunks in %v", len(got), time.Since(start))
	}
}

func TestFixedSizeModelOfAnotherSize(t *testing.T) {
	// A real model of one size, not the expected one: an error naming it.
	c := ai.New(textOnly{})
	c.SetEmbedder(wrongEmbedder{}, "m") // makes 2
	if _, err := ai.EmbedFixed(ai.WithClient(context.Background(), c), 3, "x"); err == nil || !strings.Contains(err.Error(), "makes vectors of 2 dimensions, not 3") {
		t.Errorf("a fixed-size model of another size: %v", err)
	}
	if got := ai.SplitChunksLimit(strings.Repeat("a", 100)+" b c", 10, 2); len(got) != 2 {
		t.Errorf("limit 2: %q", got)
	}
}
