// SPDX-License-Identifier: Apache-2.0

// Package session keeps per-visitor state across requests in an encrypted
// cookie: values, flash messages, the CSRF token, and the validation errors
// and form input a failed form post leaves for the next page.
//
// Add the middleware to the routes that serve HTML, then use the session
// from any request context:
//
//	sessions, err := session.New(app) // SESSION_* settings, APP_KEY
//	web := r.Group("", sessions.Middleware, web.CSRF())
//
//	s := session.From(c)
//	s.Put("theme", "dark")
//	s.Flash("status", "Post saved.")
//
// By default the cookie holds the whole session, encrypted and
// authenticated with APP_KEY, so there is nothing to store on the server;
// it is limited to about 4 KB. With SESSION_DRIVER=database (or redis, from
// drivers/redis) the session is kept in a store and the cookie holds its
// encrypted ID, so sessions can be revoked. Sessions end after
// SESSION_TTL without a request.
package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Session is one visitor's session. Get it with [From]. Its methods are
// safe for concurrent use, but changes made after the response has
// started are not saved.
type Session struct {
	mu      sync.Mutex
	id      string
	token   string // CSRF token, base64url; "" until first used
	created time.Time
	last    time.Time

	data     map[string]json.RawMessage
	flashNow []string // keys flashed by the previous request: removed at save
	flashNew []string // keys flashed by this request: kept for the next

	errsNow []FieldError        // flashed by the previous request
	errsNew []FieldError        // flashed by this request
	oldNow  map[string][]string // input flashed by the previous request
	oldNew  map[string][]string // input flashed by this request

	// dirty says a method changed what is saved, so the middleware writes
	// the session back; a session nothing changed isn't encoded again.
	dirty bool
}

// FieldError is a validation message for one form field.
type FieldError struct {
	Field   string `json:"f"` // the form field's name
	Message string `json:"m"` // the message to show
}

type ctxKey struct{}

// From returns the request's session, or nil if the session middleware
// doesn't run for the request.
func From(ctx context.Context) *Session {
	s, _ := ctx.Value(ctxKey{}).(*Session)
	return s
}

// NewContext returns ctx carrying s. The middleware does this; tests can
// use it to call handlers with a session.
func NewContext(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// NewSession returns an empty session, for tests. Requests get theirs from the
// middleware.
func NewSession() *Session {
	now := time.Now()
	return &Session{id: randomString(16), created: now, last: now, data: map[string]json.RawMessage{}}
}

// changed reports whether a method changed what is saved.
func (s *Session) changed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirty
}

// ID returns the session's random identifier. It changes on [Session.Regenerate].
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Put stores v under key, encoded as JSON. It panics if v can't be
// encoded (a channel, a function), which is a programming error.
func (s *Session) Put(key string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("session: can't store %q: %v", key, err))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.data[key]; !ok || !bytes.Equal(old, b) || slices.Contains(s.flashNow, key) {
		s.dirty = true
	}
	s.data[key] = b
	s.flashNow = slices.DeleteFunc(s.flashNow, func(k string) bool { return k == key })
}

// Get decodes the value stored under key into dst (a pointer) and reports
// whether it was there and decoded.
func (s *Session) Get(key string, dst any) bool {
	s.mu.Lock()
	raw, ok := s.data[key]
	s.mu.Unlock()
	return ok && json.Unmarshal(raw, dst) == nil
}

// Value returns the value stored under key as a T, and whether it was
// there and decoded:
//
//	userID, ok := session.Value[int64](s, "user_id")
func Value[T any](s *Session, key string) (T, bool) {
	var v T
	ok := s.Get(key, &v)
	return v, ok
}

// String returns the string stored under key, or "".
func (s *Session) String(key string) string {
	v, _ := Value[string](s, key)
	return v
}

// Has reports whether key is set.
func (s *Session) Has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.data[key]
	return ok
}

// Delete removes key.
func (s *Session) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[key]; ok {
		s.dirty = true
		delete(s.data, key)
	}
}

// Pull decodes the value under key into dst and removes it.
func (s *Session) Pull(key string, dst any) bool {
	ok := s.Get(key, dst)
	s.Delete(key)
	return ok
}

// Flash stores v under key for this request and the next one only: the
// usual way to show a message after a redirect.
//
//	s.Flash("status", "Post saved.")
//	return c.RedirectRoute("posts.index")
func (s *Session) Flash(key string, v any) {
	s.Put(key, v)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.flashNew, key) {
		s.flashNew = append(s.flashNew, key)
		s.dirty = true
	}
}

// Reflash keeps every flashed value, errors and input for one more
// request.
func (s *Session) Reflash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	for _, k := range s.flashNow {
		if !slices.Contains(s.flashNew, k) {
			s.flashNew = append(s.flashNew, k)
		}
	}
	s.flashNow = nil
	if s.errsNew == nil {
		s.errsNew = s.errsNow
	}
	if s.oldNew == nil {
		s.oldNew = s.oldNow
	}
}

// Keep keeps the given flashed values for one more request.
func (s *Session) Keep(keys ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		if i := slices.Index(s.flashNow, k); i >= 0 {
			s.flashNow = slices.Delete(s.flashNow, i, i+1)
			s.flashNew = append(s.flashNew, k)
			s.dirty = true
		}
	}
}

// Clear removes every value, flashed ones included. The ID and CSRF token
// stay.
func (s *Session) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	s.data = map[string]json.RawMessage{}
	s.flashNow, s.flashNew = nil, nil
	s.errsNow, s.errsNew, s.oldNow, s.oldNew = nil, nil, nil, nil
}

// Regenerate gives the session a new ID and CSRF token, keeping its
// values, and restarts its maximum lifetime. Call it when the user logs
// in, so an identifier or token seen before can't be reused (with a
// server-side store, the session under the old ID is removed).
func (s *Session) Regenerate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	s.id = randomString(16)
	if s.token != "" {
		s.token = randomString(tokenSize)
	}
	s.created = s.last
}

// Invalidate removes everything and starts a new session: call it when the
// user logs out. With a server-side store, the old session is removed
// from it, so no copy of the old cookie works any more. With cookie
// sessions it clears the session in this browser only: a copy of an
// earlier cookie stays valid until it expires (SESSION_TTL idle,
// SESSION_MAX_TTL in all).
func (s *Session) Invalidate() {
	s.Clear()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.id = randomString(16)
	s.token = ""
	s.created = s.last
}

// ---- CSRF token ----

const tokenSize = 32

// Token returns the session's CSRF token, creating it on first use, for
// forms (the "_token" field) and the X-CSRF-Token header. Each call returns
// a differently masked form of the same token, so pages don't repeat a
// secret that compression could leak (BREACH).
func (s *Session) Token() string {
	s.mu.Lock()
	if s.token == "" {
		s.token = randomString(tokenSize)
		s.dirty = true
	}
	raw, _ := base64.RawURLEncoding.DecodeString(s.token)
	s.mu.Unlock()
	masked := make([]byte, 2*tokenSize)
	_, _ = rand.Read(masked[:tokenSize])
	subtle.XORBytes(masked[tokenSize:], masked[:tokenSize], raw)
	return base64.RawURLEncoding.EncodeToString(masked)
}

// VerifyToken reports whether t is a token returned by [Session.Token].
func (s *Session) VerifyToken(t string) bool {
	s.mu.Lock()
	stored := s.token
	s.mu.Unlock()
	if stored == "" {
		return false
	}
	raw, _ := base64.RawURLEncoding.DecodeString(stored)
	masked, err := base64.RawURLEncoding.DecodeString(t)
	if err != nil || len(masked) != 2*tokenSize || len(raw) != tokenSize {
		return false
	}
	got := make([]byte, tokenSize)
	subtle.XORBytes(got, masked[:tokenSize], masked[tokenSize:])
	return subtle.ConstantTimeCompare(got, raw) == 1
}

// RegenerateToken replaces the CSRF token; pages rendered earlier can no
// longer post.
func (s *Session) RegenerateToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = randomString(tokenSize)
	s.dirty = true
}

// ---- Form errors and old input ----

// FlashErrors keeps validation errors for the next request, where
// [Session.Errors] returns them. The web package calls it when a form post
// fails validation.
func (s *Session) FlashErrors(errs ...FieldError) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(errs) > 0 {
		s.dirty = true
	}
	s.errsNew = append(s.errsNew, errs...)
}

// Errors returns the validation errors flashed by the previous request, in
// order.
func (s *Session) Errors() []FieldError {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.errsNow)
}

// FlashInput keeps submitted form values for the next request, where
// [Session.OldInput] returns them, so a form can be refilled. "_method"
// and fields whose name contains "password", "secret" or "token" (the CSRF
// token among them) are left out.
func (s *Session) FlashInput(values url.Values) {
	old := map[string][]string{}
	for k, vs := range values {
		if k == "_method" || sensitive(k) {
			continue
		}
		old[k] = slices.Clone(vs)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dirty = true
	s.oldNew = old
}

// OldInput returns the form values flashed by the previous request.
func (s *Session) OldInput() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return url.Values(maps.Clone(s.oldNow))
}

// Old returns the first value flashed for a form field by the previous
// request, and whether there was one.
func (s *Session) Old(field string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vs, ok := s.oldNow[field]
	if !ok || len(vs) == 0 {
		return "", ok
	}
	return vs[0], true
}

// sensitive reports whether a form field must not be kept in the cookie.
func sensitive(field string) bool {
	f := strings.ToLower(field)
	return strings.Contains(f, "password") || strings.Contains(f, "secret") || strings.Contains(f, "token")
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
