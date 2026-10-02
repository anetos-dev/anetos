// SPDX-License-Identifier: Apache-2.0

package gemini_test

import (
	"net/http"
	"testing"

	"google.golang.org/genai"

	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/ai/aitest"
	"anetos.dev/anetos/drivers/gemini"
)

func TestConformance(t *testing.T) {
	aitest.Run(t, aitest.Config{
		Name:   gemini.Name,
		Model:  "gemini-2.5-flash",
		KeyEnv: "GEMINI_API_KEY",
		// The Embed recording is written by hand, in the API's format.
		EmbeddingModel: "gemini-embedding-001",
		New: func(t *testing.T, hc *http.Client, key string) ai.Provider {
			one := int32(1)
			p, err := gemini.New(t.Context(), genai.ClientConfig{APIKey: key, HTTPClient: hc, HTTPOptions: genai.HTTPOptions{
				BaseURL: "https://generativelanguage.googleapis.com/", RetryOptions: &genai.HTTPRetryOptions{Attempts: &one},
			}})
			if err != nil {
				t.Fatal(err)
			}
			return p
		},
	})
}
