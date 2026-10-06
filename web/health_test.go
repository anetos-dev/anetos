// SPDX-License-Identifier: Apache-2.0

package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLocalAddr(t *testing.T) {
	for addr, want := range map[string]string{
		":8080":          "127.0.0.1:8080",
		"0.0.0.0:8080":   "127.0.0.1:8080",
		"[::]:8080":      "127.0.0.1:8080",
		"localhost:8080": "localhost:8080",
		"[::1]:8080":     "[::1]:8080",
		"10.0.0.5:80":    "10.0.0.5:80",
		"nonsense":       "nonsense",
	} {
		if got := localAddr(addr); got != want {
			t.Errorf("localAddr(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestCheckHealthNotReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	var out bytes.Buffer
	err := checkHealth(context.Background(), srv.URL+"/health/ready", time.Second, &out)
	if err == nil || err.Error() != srv.URL+"/health/ready answered 503 Service Unavailable" || out.Len() != 0 {
		t.Errorf("err = %v, out %q", err, out.String())
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slow.Close()
	if err := checkHealth(context.Background(), slow.URL, 50*time.Millisecond, &out); err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Errorf("slow: %v", err)
	}
}
