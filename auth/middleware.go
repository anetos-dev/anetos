// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
)

// Session keys.
const (
	keyID       = "_auth.id"
	keyHash     = "_auth.hash"
	keyIntended = "_auth.intended"
)

// state is a request's authentication, in its context.
type state struct {
	loader interface {
		load(ctx context.Context, st *state) (any, error)
	}
	w   http.ResponseWriter
	r   *http.Request
	log *slog.Logger

	mu      sync.Mutex
	loaded  bool
	loading chan struct{} // closed when the load in progress ends
	user    any           // nil for a guest
	token   *Token        // the API token the request authenticated with
	acting  bool          // made by ActAs: no session to sign in or out
}

type stateKey struct{}

func stateFrom(ctx context.Context) *state {
	st, _ := ctx.Value(stateKey{}).(*state)
	return st
}

var errLoading = errors.New("auth: the current user was asked for while it was being loaded (does Users.ByID ask for it?)")

// get returns the request's user (nil for a guest), loading it once.
// Concurrent callers wait for the load in progress; a failed load is
// tried again by the next call.
func (st *state) get(ctx context.Context) (any, error) {
	if ctx.Value(loadingKey{}) == st {
		return nil, errLoading // Users.ByID asked for the user it is finding
	}
	for {
		st.mu.Lock()
		if st.loaded {
			defer st.mu.Unlock()
			return st.user, nil
		}
		if wait := st.loading; wait != nil {
			st.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		st.loading = done
		st.mu.Unlock()
		// Without the lock: ByID may take a while.
		u, err := st.loader.load(context.WithValue(ctx, loadingKey{}, st), st)
		st.mu.Lock()
		st.loading = nil
		close(done)
		if err == nil && !st.loaded { // Login or Logout during the load wins
			st.user, st.loaded = u, true
		}
		user := st.user
		st.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return user, nil
	}
}

// loadingKey marks the context of a load in progress.
type loadingKey struct{}

func (st *state) set(u any, tok *Token) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.user, st.token, st.loaded = u, tok, true
}

// Middleware makes the request's user available to [User], [Check] and
// the Auth methods. Put it after the session middleware: it finds the
// user from the session, or from a remember-me cookie (signing them in
// to a new session). The user is loaded when first asked for, so pages
// that don't need one don't query for it.
func (a *Auth[U]) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if st := stateFrom(r.Context()); st != nil {
			if st.loader != any(a) {
				web.WriteError(w, r, errors.New("auth: two Auths on one route; an app has one"))
				return
			}
			next.ServeHTTP(w, r) // already added
			return
		}
		st := &state{loader: a, w: w, r: r, log: a.log}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, st)))
	})
}

// load finds the user of the request's session or remember-me cookie.
func (a *Auth[U]) load(ctx context.Context, st *state) (any, error) {
	s := session.From(ctx)
	if s == nil {
		return nil, nil
	}
	if id := s.String(keyID); id != "" {
		u, err := a.users.ByID(ctx, id)
		switch {
		case notFound(err):
			a.forget(s) // the user was deleted
			return nil, nil
		case err != nil:
			return nil, err
		case fingerprint(u.AuthPassword()) != s.String(keyHash):
			// The password changed since this session signed in: sign it
			// out, as the password change did everywhere else.
			s.Invalidate()
			return nil, nil
		}
		return u, nil
	}
	return a.fromRemember(ctx, st, s)
}

// forget signs the session out without ending it.
func (a *Auth[U]) forget(s *session.Session) {
	s.Delete(keyID)
	s.Delete(keyHash)
}

// rememberValue is the remember-me cookie's content, encrypted.
type rememberValue struct {
	ID      string `json:"i"`
	Token   string `json:"t"`
	Hash    string `json:"h"` // password fingerprint
	Expires int64  `json:"x"` // Unix seconds
}

const rememberContext = "anetos/auth\x00remember"

// fromRemember signs in the user of a valid remember-me cookie.
func (a *Auth[U]) fromRemember(ctx context.Context, st *state, s *session.Session) (any, error) {
	if a.users.RememberToken == nil || st.r == nil {
		return nil, nil
	}
	v, ok := a.rememberCookie(st.r)
	if !ok {
		a.clearRemember(st)
		return nil, nil
	}
	if v.ID == "" {
		return nil, nil // no cookie
	}
	u, err := a.users.ByID(ctx, v.ID)
	if notFound(err) {
		a.clearRemember(st)
		return nil, nil //nolint:nilerr // a deleted user is a guest
	}
	if err != nil {
		return nil, err
	}
	tok := a.users.RememberToken(u)
	if tok == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(v.Token)) != 1 || v.Hash != fingerprint(u.AuthPassword()) {
		a.clearRemember(st) // signed out everywhere, or the password changed
		return nil, nil
	}
	s.Regenerate()
	s.Put(keyID, u.AuthID())
	s.Put(keyHash, fingerprint(u.AuthPassword()))
	return u, nil
}

// rememberCookie reads the remember-me cookie: ok false if it is invalid,
// a zero value if there is none.
func (a *Auth[U]) rememberCookie(r *http.Request) (rememberValue, bool) {
	var v rememberValue
	c, _ := r.Cookie(a.cookie)
	if c == nil || c.Value == "" {
		return v, true
	}
	plain, err := a.enc.DecryptString(c.Value, rememberContext)
	if err != nil || json.Unmarshal([]byte(plain), &v) != nil || v.ID == "" || v.Token == "" || a.now().Unix() > v.Expires {
		return rememberValue{}, false
	}
	return v, true
}

func (a *Auth[U]) setRemember(st *state, value string) {
	http.SetCookie(st.w, &http.Cookie{
		Name: a.cookie, Value: value, Path: "/", MaxAge: int(a.cfg.RememberLifetime / time.Second),
		Secure: a.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth[U]) clearRemember(st *state) {
	if st.w != nil {
		http.SetCookie(st.w, &http.Cookie{Name: a.cookie, Path: "/", MaxAge: -1, Secure: a.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	}
}

// User returns the request's signed-in user, and whether there is one. A
// failure to load the user (the database is down) is logged and counts as
// no user; use [Current] to tell the two apart.
//
//	u, ok := auth.User[*models.User](c)
func User[U Authenticatable](ctx context.Context) (U, bool) {
	u, err := Current[U](ctx)
	if err != nil && !errors.Is(err, ErrUnauthenticated) {
		if st := stateFrom(ctx); st != nil {
			st.log.Error("auth: loading the signed-in user failed", "error", err)
		}
	}
	return u, err == nil
}

// Current returns the request's signed-in user, [ErrUnauthenticated] if
// there is none, or the error that kept it from being loaded.
func Current[U Authenticatable](ctx context.Context) (U, error) {
	var zero U
	st := stateFrom(ctx)
	if st == nil {
		return zero, ErrUnauthenticated
	}
	u, err := st.get(ctx)
	if err != nil {
		return zero, err
	}
	if u == nil {
		return zero, ErrUnauthenticated
	}
	typed, ok := u.(U)
	if !ok {
		return zero, errors.New("auth: the signed-in user isn't of the type asked for")
	}
	return typed, nil
}

// CurrentID returns the [Authenticatable.AuthID] of the request's
// signed-in user, [ErrUnauthenticated] if there is none, or the error that
// kept it from being loaded. It is for code that works with any user type,
// such as package auth/rbac; handlers use [Current] or [User].
func CurrentID(ctx context.Context) (string, error) {
	st := stateFrom(ctx)
	if st == nil {
		return "", ErrUnauthenticated
	}
	u, err := st.get(ctx)
	if err != nil {
		return "", err
	}
	a, ok := u.(Authenticatable)
	if !ok { // nil: a guest
		return "", ErrUnauthenticated
	}
	return a.AuthID(), nil
}

// Check reports whether the request has a signed-in user.
func Check(ctx context.Context) bool {
	st := stateFrom(ctx)
	if st == nil {
		return false
	}
	u, err := st.get(ctx)
	return err == nil && u != nil
}

// Require lets only signed-in users through. Guests asking for a page are
// redirected to AUTH_LOGIN_URL, and the page they wanted is remembered
// for [Intended]; other requests (JSON, htmx) get 401.
func (a *Auth[U]) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := Current[U](r.Context())
		switch {
		case err == nil:
			next.ServeHTTP(w, r)
		case !errors.Is(err, ErrUnauthenticated):
			web.WriteError(w, r, err)
		case session.From(r.Context()) == nil:
			// A route without sessions (an API): nowhere to log in.
			w.Header().Set("WWW-Authenticate", "Bearer")
			web.WriteError(w, r, ErrUnauthenticated)
		case r.Method == http.MethodGet && !web.WantsJSON(r) && r.Header.Get("HX-Request") == "":
			session.From(r.Context()).Put(keyIntended, r.URL.RequestURI())
			http.Redirect(w, r, a.cfg.LoginURL, http.StatusSeeOther)
		default:
			web.WriteError(w, r, ErrUnauthenticated)
		}
	})
}

// Guest lets only guests through: signed-in users are redirected to
// AUTH_HOME_URL. Use it on the login and registration pages.
func (a *Auth[U]) Guest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Check(r.Context()) {
			http.Redirect(w, r, a.cfg.HomeURL, http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Intended returns the page a guest asked for before [Auth.Require] sent
// them to the login page (removing it from the session), or fallback:
// where to redirect after a login.
func Intended(ctx context.Context, fallback string) string {
	s := session.From(ctx)
	if s == nil {
		return fallback
	}
	var u string
	if s.Pull(keyIntended, &u) && localPath(u) {
		return u
	}
	return fallback
}

// actor finds users by ID for [ActAs]; ForApp puts the app's Auth in its
// contexts as one.
type actor interface {
	ActAs(ctx context.Context, userID string, opts ...ActOption) context.Context
}

// ActOption configures [Auth.ActAs].
type ActOption func(*state)

// WithAbilities limits the context to abilities, as an API token with
// them would ([TokenCan], and packages that read [CurrentToken], such as
// auth/rbac): for work done for a request that was signed in with a
// token, so that it can't do more than the token could. Its Token has
// the abilities and user ID, and no ID.
func WithAbilities(abilities []string) ActOption {
	return func(st *state) {
		st.token = &Token{Abilities: slices.Clone(abilities)}
	}
}

type actorKey struct{}

// idLoader loads the user an [Auth.ActAs] context acts as.
type idLoader[U Authenticatable] struct {
	a  *Auth[U]
	id string
}

func (l idLoader[U]) load(ctx context.Context, _ *state) (any, error) {
	u, err := l.a.users.ByID(ctx, l.id)
	switch {
	case notFound(err):
		return nil, nil // deleted since: a guest
	case err != nil:
		return nil, err
	}
	return u, nil
}

// ActAs returns ctx in which the user with userID is the signed-in user,
// for work done for a user outside their requests: a queue job, a
// command. [User], [Current], [CurrentID], policies and what builds on
// them (package auth/rbac, AI tools) see that user, loaded from
// Users.ByID when first asked for; a user that doesn't exist is a guest.
// There is no session: Attempt, Login and Logout fail. There is no API
// token either, unless [WithAbilities] gives the limits of one.
func (a *Auth[U]) ActAs(ctx context.Context, userID string, opts ...ActOption) context.Context {
	st := &state{loader: idLoader[U]{a, userID}, log: a.log, acting: true}
	for _, opt := range opts {
		opt(st)
	}
	if st.token != nil {
		st.token.UserID = userID
	}
	return context.WithValue(ctx, stateKey{}, st)
}

// ActAs is [Auth.ActAs] with the app's Auth (auth.ForApp), from ctx: for
// packages that don't know the app's user type, such as package ai's
// queued replies.
func ActAs(ctx context.Context, userID string, opts ...ActOption) (context.Context, error) {
	a, ok := ctx.Value(actorKey{}).(actor)
	if !ok {
		return nil, errors.New("auth: ActAs needs the app's Auth in the context (auth.ForApp)")
	}
	return a.ActAs(ctx, userID, opts...), nil
}
