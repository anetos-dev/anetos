// SPDX-License-Identifier: Apache-2.0

package aitest

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecordAndReplay(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		b, _ := io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"echo":`+string(b)+`}`)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "Echo.json")
	send := func(t *testing.T, tr http.RoundTripper, body string) string {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/echo?key=secret&alt=sse", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		resp, err := (&http.Client{Transport: tr}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	t.Run("record", func(t *testing.T) {
		if got := send(t, newTransport(t, record, path), `{"b":1,"a":2}`); got != `{"echo":{"b":1,"a":2}}` {
			t.Errorf("recorded response %q", got)
		}
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || !strings.Contains(string(data), `"path": "/v1/echo?alt=sse"`) {
		t.Errorf("cassette:\n%s", data)
	}

	t.Run("replay", func(t *testing.T) {
		if got := send(t, newTransport(t, replay, path), `{"a":2, "b":1}`); got != `{"echo":{"b":1,"a":2}}` {
			t.Errorf("replayed response %q", got)
		}
	})

	// Another request is reported; a missing one too.
	var errs []string
	tr := &transport{t: t, mode: replay, path: path, tape: loadTape(t, path),
		fail: func(format string, _ ...any) { errs = append(errs, format) }}
	send(t, tr, `{"a":3}`)
	if len(errs) != 1 || !strings.Contains(errs[0], "differs from the recording") {
		t.Errorf("errors: %q", errs)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/echo", strings.NewReader(`{}`))
	if resp, err := tr.RoundTrip(req); err == nil {
		_ = resp.Body.Close()
		t.Error("a request past the recording: no error")
	}
	if len(errs) != 2 || !strings.Contains(errs[1], "isn't in the recording") {
		t.Errorf("errors: %q", errs)
	}
}
