// SPDX-License-Identifier: Apache-2.0

package anthropic_test

import (
	"net/http"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"anetos.dev/anetos/ai"
	"anetos.dev/anetos/ai/aitest"
	"anetos.dev/anetos/drivers/anthropic"
)

func TestConformance(t *testing.T) {
	aitest.Run(t, aitest.Config{
		Name:   anthropic.Name,
		Model:  "claude-haiku-4-5",
		KeyEnv: "ANTHROPIC_API_KEY",
		New: func(_ *testing.T, hc *http.Client, key string) ai.Provider {
			return anthropic.New(key, option.WithHTTPClient(hc), option.WithMaxRetries(0),
				option.WithBaseURL("https://api.anthropic.com"))
		},
	})
}
