// SPDX-License-Identifier: Apache-2.0

package ai

import (
	"context"
	"time"
)

// SetKeepAlive sets how often SSE sends a comment, for a test.
func SetKeepAlive(d time.Duration) (restore func()) {
	old := keepAlive
	keepAlive = d
	return func() { keepAlive = old }
}

// SplitChunks is splitChunks, for a test.
func SplitChunks(text string, size int) []string { return splitChunks(text, size, 0) }

// SplitChunksLimit is splitChunks with a limit, for a test.
func SplitChunksLimit(text string, size, limit int) []string { return splitChunks(text, size, limit) }

// EmbedFixed embeds as for a FixedSize model of size want, for a test.
func EmbedFixed(ctx context.Context, want int, texts ...string) ([]Vector, error) {
	return embed(ctx, EmbedForDocument, texts, []Option{Dimensions(want)}, true)
}

// ClientMessage is clientMessage, for a test.
func ClientMessage(ctx context.Context, err error) string { return clientMessage(ctx, err) }
