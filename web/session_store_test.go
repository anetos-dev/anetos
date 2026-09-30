// SPDX-License-Identifier: Apache-2.0

package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/encryption"
	"anetos.dev/anetos/session"
)

// downStore fails every read.
type downStore struct{ cache.Store }

func (downStore) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("store down")
}

// TestSessionStoreFailure checks that the session middleware's 503 goes
// through the router's error handler (JSON for API clients).
func TestSessionStoreFailure(t *testing.T) {
	k, _ := encryption.ParseKey(encryption.GenerateKey())
	enc, _ := encryption.New(k)
	m, err := session.NewManager(session.DefaultConfig(), enc, session.WithStore(downStore{}, "t:"))
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter()
	r.Group("", m.Middleware).Get("/", func(c *Ctx) error { return c.NoContent() })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: m.CookieName(), Value: enc.EncryptString("some-id", "anetos/session\x00"+m.CookieName())})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Header().Get("Content-Type"), "json") || w.Header().Get("Retry-After") == "" {
		t.Errorf("%d %q %v: %s", w.Code, w.Header().Get("Content-Type"), w.Header(), w.Body)
	}
}
