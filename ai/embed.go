// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"strings"
	"unicode"

	"anetos.dev/anetos/db"
)

// Vector is an embedding: a db.Vector, stored in embeddings tables.
type Vector = db.Vector

// Embedder is a provider's embeddings API, which the drivers of
// providers that have one implement (OpenAI and compatible servers,
// Gemini), and the [Fake].
type Embedder interface {
	// Embed returns a vector per input, in order.
	Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error)
}

// EmbedPurpose says what a text is embedded for; some models embed
// documents and queries differently (Gemini), others ignore it.
type EmbedPurpose string

// The purposes of embeddings.
const (
	EmbedForDocument EmbedPurpose = "document" // a passage to find
	EmbedForQuery    EmbedPurpose = "query"    // what to find it with
)

// EmbedRequest is one call to an embeddings API.
type EmbedRequest struct {
	// Model is the embedding model's name.
	Model string
	// Inputs are the texts.
	Inputs []string
	// Dimensions is the vectors' size, for models that can shorten them
	// (OpenAI's text-embedding-3 models, Gemini's); 0 for the model's.
	Dimensions int
	// Purpose says what the texts are for.
	Purpose EmbedPurpose

	// want is the size the caller expects when it asks for none (a
	// FixedSize model's), for the Fake.
	want int
}

// EmbedResponse is an embeddings API's answer.
type EmbedResponse struct {
	// Vectors are the inputs' embeddings, in order.
	Vectors []Vector
	// Usage counts the input tokens.
	Usage Usage
	// Model is the model that answered, as the provider names it.
	Model string
	// Estimated is set when the provider doesn't count the tokens, and
	// Usage is an estimate.
	Estimated bool
}

// ErrNoEmbedder is returned by [Embed] when the client has no embeddings
// provider: AI_PROVIDER's has none (Anthropic), and
// AI_EMBEDDING_PROVIDER isn't set.
var ErrNoEmbedder = errors.New("ai: the AI provider has no embeddings: set AI_EMBEDDING_PROVIDER (openai, gemini, openai-compatible) and AI_EMBEDDING_MODEL, and pass its driver to ai.ForApp")

// embedder returns the client's embeddings provider and model.
func (cl *Client) embedder() (Embedder, string, error) {
	cl.mu.RLock()
	defer cl.mu.RUnlock()
	e := cl.embed
	if e == nil {
		e, _ = cl.provider.(Embedder)
	}
	if e == nil {
		return nil, "", ErrNoEmbedder
	}
	if cl.embedModel == "" {
		if _, fake := e.(*Fake); !fake {
			return nil, "", errors.New("ai: AI_EMBEDDING_MODEL isn't set: set it to the embedding model's name")
		}
		return e, "fake-embedding", nil
	}
	return e, cl.embedModel, nil
}

// EmbeddingModel returns the name of the client's embedding model
// (AI_EMBEDDING_MODEL), which stored embeddings record; "" if it has
// no embeddings.
func (cl *Client) EmbeddingModel() string {
	_, model, _ := cl.embedder()
	return model
}

// maxEmbedBatch is the most texts sent in one request (OpenAI takes
// 2048, Gemini 100).
const maxEmbedBatch = 96

// Embed returns the embeddings of texts as documents (passages to find),
// with the context's client's embedding model (AI_EMBEDDING_MODEL), in
// requests of at most 96 texts. dims asks for vectors of that size (0 for
// the model's); vectors of another size are an error. With
// [Client.TrackUsage], each request's usage is recorded, for the
// signed-in user if any, and a spent [Budget] refuses it. Most apps use
// [Embeddings], which stores them.
func Embed(ctx context.Context, dims int, texts ...string) ([]Vector, error) {
	return embed(ctx, EmbedForDocument, dims, dims, texts)
}

// EmbedQuery returns the embedding of a query: what to find documents
// embedded with [Embed] by.
func EmbedQuery(ctx context.Context, dims int, text string) (Vector, error) {
	vs, err := embed(ctx, EmbedForQuery, dims, dims, []string{text})
	if err != nil {
		return nil, err
	}
	return vs[0], nil
}

// embed embeds texts, asking for vectors of size ask (0: the model's) and
// checking they have size want (0: any).
func embed(ctx context.Context, purpose EmbedPurpose, ask, want int, texts []string) ([]Vector, error) {
	cl, err := From(ctx)
	if err != nil {
		return nil, err
	}
	e, model, err := cl.embedder()
	if err != nil {
		return nil, err
	}
	if ask < 0 || want < 0 {
		return nil, fmt.Errorf("ai: embed: %d dimensions", min(ask, want))
	}
	c := &call{client: cl, name: "embed", model: model}
	track, err := cl.startTracking(ctx, c)
	if err != nil {
		return nil, err
	}
	name := "embeddings"
	if p, ok := e.(interface{ Name() string }); ok {
		name = p.Name()
	}
	out := make([]Vector, 0, len(texts))
	for i := 0; i < len(texts); i += maxEmbedBatch {
		if err := track.check(ctx); err != nil {
			return nil, err
		}
		batch := texts[i:min(i+maxEmbedBatch, len(texts))]
		resp, err := e.Embed(ctx, &EmbedRequest{Model: model, Inputs: batch, Dimensions: ask, Purpose: purpose, want: want})
		if err != nil {
			return nil, fmt.Errorf("ai: embed: %w", err)
		}
		cl.record(ctx, track, c, name, &Request{Model: model}, &Response{Model: resp.Model, Usage: resp.Usage}, resp.Estimated)
		if len(resp.Vectors) != len(batch) {
			return nil, fmt.Errorf("ai: embed: %d vectors for %d texts", len(resp.Vectors), len(batch))
		}
		for _, v := range resp.Vectors {
			switch {
			case want > 0 && len(v) != want && ask > 0:
				return nil, fmt.Errorf("ai: embed: the model made vectors of %d dimensions, not the %d asked for: choose a model that makes them (or that can shorten its own)", len(v), want)
			case want > 0 && len(v) != want:
				return nil, fmt.Errorf("ai: embed: the model makes vectors of %d dimensions, not %d: set Dimensions (and the embeddings table) to its size", len(v), want)
			}
		}
		out = append(out, resp.Vectors...)
	}
	return out, nil
}

// fakeDims is the Fake's vector size when a request doesn't ask for one.
const fakeDims = 64

// Embed implements [Embedder]: vectors of the texts' words, hashed into
// the dimensions, so texts sharing words are near, deterministically
// (a text without words gets the first axis), of the size asked for, or
// expected (a FixedSize model's), or 64. No reply is needed.
func (f *Fake) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	cp := *req
	cp.Inputs = slices.Clone(req.Inputs)
	f.embeds = append(f.embeds, cp)
	f.mu.Unlock()
	dims := cmp.Or(req.Dimensions, req.want, fakeDims)
	resp := &EmbedResponse{Model: req.Model}
	if resp.Model == "" {
		resp.Model = "fake-embedding"
	}
	for _, text := range req.Inputs {
		v := make(Vector, dims)
		before := resp.Usage.InputTokens
		for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			h := fnv.New64a()
			_, _ = h.Write([]byte(w))
			sum := h.Sum64()
			v[sum%uint64(dims)] += 1
			resp.Usage.InputTokens++
		}
		if resp.Usage.InputTokens == before { // no words: not a zero vector, which databases compare differently
			v[0] = 1
		}
		var norm float64
		for _, x := range v {
			norm += float64(x) * float64(x)
		}
		if norm > 0 {
			n := float32(math.Sqrt(norm))
			for i := range v {
				v[i] /= n
			}
		}
		resp.Vectors = append(resp.Vectors, v)
	}
	return resp, nil
}
