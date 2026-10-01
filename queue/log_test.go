// SPDX-License-Identifier: Apache-2.0

package queue_test

import (
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// testLogger logs to the test's log, until the test ends.
func testLogger(t *testing.T) *slog.Logger {
	w := &testWriter{t: t}
	t.Cleanup(func() { w.mu.Lock(); w.done = true; w.mu.Unlock() })
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

type testWriter struct {
	mu   sync.Mutex
	t    *testing.T
	done bool
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.done {
		w.t.Log(strings.TrimSuffix(string(p), "\n"))
	}
	return len(p), nil
}
