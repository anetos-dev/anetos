// SPDX-License-Identifier: Apache-2.0

package openai_test

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/openai/openai-go/v3/option"

	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/ai/aitest"
	"anetos.dev/anetos/drivers/openai"
)

func TestConformance(t *testing.T) {
	aitest.Run(t, aitest.Config{
		Name:   openai.Name,
		Model:  "gpt-5-mini",
		KeyEnv: "OPENAI_API_KEY",
		// The Embed recording is written by hand, in the API's format.
		EmbeddingModel: "text-embedding-3-small",
		Dir:            filepath.Join("testdata", "openai"),
		New: func(_ *testing.T, hc *http.Client, key string) ai.Provider {
			return openai.New(key, option.WithHTTPClient(hc), option.WithMaxRetries(0),
				option.WithBaseURL("https://api.openai.com/v1"))
		},
	})
}

// TestCompatibleConformance runs the suite against an OpenAI-compatible
// server: Ollama's recordings, or with ANETOS_AI_RECORD=1, the server at
// OPENAI_COMPATIBLE_URL (with OPENAI_COMPATIBLE_KEY, if it needs one).
func TestCompatibleConformance(t *testing.T) {
	aitest.Run(t, aitest.Config{
		Name:     openai.CompatibleName,
		Model:    "llama3.2",
		NeedsEnv: []string{"OPENAI_COMPATIBLE_URL"},
		Dir:      filepath.Join("testdata", "compatible"),
		New: func(_ *testing.T, hc *http.Client, _ string) ai.Provider {
			url, key := "http://localhost:11434/v1", "" // the recordings'
			if !aitest.Replaying() {
				url, key = os.Getenv("OPENAI_COMPATIBLE_URL"), os.Getenv("OPENAI_COMPATIBLE_KEY")
			}
			return openai.NewCompatible(url, key, option.WithHTTPClient(hc), option.WithMaxRetries(0))
		},
	})
}
