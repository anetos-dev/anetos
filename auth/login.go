// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web/ratelimit"
)

var errNoMiddleware = errors.New("auth: no auth state in the context; add the Auth middleware (after the session middleware) to the route")

var errActing = errors.New("auth: the context acts as a user (ActAs): there is no session to sign in or out")

// Attempt signs in the user whose login and password match, and returns
// them. It fails with [ErrInvalidCredentials] for an unknown login or a
// wrong password (taking about as long either way), and with a
// [*ThrottledError] after AUTH_THROTTLE attempts in a minute for this
// login, or this account, from this IP address, or AUTH_THROTTLE_IP
// failures in a minute from this IP address. A success clears the
// login's and account's counts, upgrades a weaker password hash (with
// Users.SetPassword) and signs the user in, as [Auth.SignIn] does: for a
// user with two-factor sign-in on, it returns the user and
// [ErrTwoFactorRequired], and the sign-in waits for a code.
//
//	u, err := a.Attempt(c, in.Email, in.Password, in.Remember)
//	if errors.Is(err, auth.ErrInvalidCredentials) {
//		return nil, validate.Fail("email", "These credentials don't match our records.")
//	}
func (a *Auth[U]) Attempt(ctx context.Context, login, pw string, remember bool) (U, error) {
	var zero U
	st := stateFrom(ctx)
	if st == nil || st.r == nil {
		return zero, errNoMiddleware
	}
	if remember && a.users.RememberToken == nil {
		return zero, errNoRemember
	}
	ip := ratelimit.IP(st.r)
	perLogin := ratelimit.PerMinute(a.cfg.Throttle)
	perIP := ratelimit.PerMinute(a.cfg.ThrottleIP)
	loginKey := "auth:login\x00" + strings.ToLower(strings.TrimSpace(login)) + "\x00" + ip
	ipKey := "auth:ip\x00" + ip
	// Per address, whatever the login, counting failures only (an office
	// behind one address logs in a lot).
	if res, err := ratelimit.Check(ctx, ipKey, perIP); err != nil {
		return zero, err
	} else if !res.Allowed {
		return zero, &ThrottledError{RetryAfter: res.RetryAfter()}
	}
	failed := func() (U, error) {
		if _, err := ratelimit.Hit(ctx, ipKey, perIP); err != nil {
			return zero, err
		}
		return zero, ErrInvalidCredentials
	}
	if err := a.hit(ctx, loginKey, perLogin); err != nil {
		return zero, err
	}
	u, err := a.users.ByLogin(ctx, login)
	found := err == nil
	if err != nil && !notFound(err) {
		return zero, err
	}
	// Per account too: spellings of a login that ByLogin takes as the
	// same account (case, spaces, lookalike letters) share this count.
	// Unknown logins count under the login, so both cost the same.
	userKey := "auth:user\x00login:" + strings.ToLower(strings.TrimSpace(login)) + "\x00" + ip
	if found {
		userKey = "auth:user\x00" + u.AuthID() + "\x00" + ip
	}
	if err := a.hit(ctx, userKey, perLogin); err != nil {
		return zero, err
	}
	hash := ""
	if found {
		hash = u.AuthPassword()
	}
	if hash == "" {
		// Unknown, or without a password (social sign-in only): take as
		// long as a check.
		if err := password.DummyContext(ctx, pw); err != nil {
			return zero, err
		}
		return failed()
	}
	ok, err := password.VerifyContext(ctx, pw, hash)
	if err != nil {
		return zero, err
	}
	if !ok {
		return failed()
	}
	for _, k := range []string{loginKey, userKey} {
		if err := ratelimit.Clear(ctx, k, perLogin); err != nil {
			a.log.Warn("auth: clearing failed logins", "error", err)
		}
	}
	// Upgrade an old hash. Not for bcrypt with a password past its 72
	// bytes: bcrypt ignored the rest, which the new hash wouldn't.
	if a.users.SetPassword != nil && password.NeedsRehash(hash) && (!password.IsBcrypt(hash) || len(pw) <= 72) {
		if newHash, err := password.Hash(pw); err == nil {
			if err := a.users.SetPassword(ctx, u, newHash); err != nil {
				a.log.Warn("auth: upgrading a password hash failed", "error", err)
			} else {
				hash = newHash
			}
		}
	}
	return u, a.signIn(ctx, u, hash, remember)
}

// hit counts an attempt against limit and returns a *ThrottledError past
// it.
func (a *Auth[U]) hit(ctx context.Context, key string, limit ratelimit.Limit) error {
	res, err := ratelimit.Allow(ctx, key, limit)
	if err != nil {
		return err
	}
	if !res.Allowed {
		return &ThrottledError{RetryAfter: res.RetryAfter()}
	}
	return nil
}

// Login signs u in: the session gets a new ID (so a session identifier
// seen before the login is useless) and remembers the user; with
// remember, a remember-me cookie keeps them signed in for
// AUTH_REMEMBER_LIFETIME after the session ends. Use it after
// registration. It doesn't ask for a two-factor code: for sign-in
// methods other than passwords, use [Auth.SignIn].
func (a *Auth[U]) Login(ctx context.Context, u U, remember bool) error {
	return a.login(ctx, u, u.AuthPassword(), remember)
}

var errNoRemember = errors.New("auth: remember me needs Users.RememberToken and Users.SetRememberToken")

// login signs u in, with hash as the password hash the session checks.
func (a *Auth[U]) login(ctx context.Context, u U, hash string, remember bool) error {
	st := stateFrom(ctx)
	s := session.From(ctx)
	if st == nil || s == nil {
		return errNoMiddleware
	}
	if st.acting {
		return errActing
	}
	if remember && a.users.RememberToken == nil {
		return errNoRemember
	}
	if a.disabled(u) {
		return ErrDisabled
	}
	var tok string
	if remember {
		if tok = a.users.RememberToken(u); tok == "" {
			tok = randomToken()
			if err := a.users.SetRememberToken(ctx, u, tok); err != nil {
				return err
			}
		}
	}
	s.Regenerate()
	s.Put(keyID, u.AuthID())
	s.Put(keyHash, a.sessionPrint(u, hash))
	for _, k := range []string{keyImpersonator, keyImpersonatorHash, keyPending, keyConfirmed} {
		s.Delete(k)
	}
	st.set(u, nil)
	if !remember {
		a.clearRemember(st) // an earlier user's cookie mustn't stay
		return nil
	}
	b, _ := json.Marshal(rememberValue{ID: u.AuthID(), Token: tok, Hash: a.sessionPrint(u, hash),
		Expires: a.now().Add(a.cfg.RememberLifetime).Unix()})
	a.setRemember(st, a.enc.EncryptString(string(b), rememberContext))
	return nil
}

// Logout signs the request's user out: the session is emptied and gets a
// new ID, the remember-me cookie is removed, and the user's remember-me
// token is replaced, which signs them out of every remembered browser.
// With cookie sessions (SESSION_DRIVER=cookie), a copy of the session
// cookie taken before the logout keeps working until it expires; a
// server-side session driver revokes it.
func (a *Auth[U]) Logout(ctx context.Context) error {
	st := stateFrom(ctx)
	s := session.From(ctx)
	if st == nil || s == nil {
		return errNoMiddleware
	}
	if st.acting {
		return errActing
	}
	u, err := Current[U](ctx)
	// Acting as u (Impersonate): logging out is the impersonator's, and
	// mustn't sign u out of their remembered browsers.
	acting := s.String(keyImpersonator) != ""
	s.Invalidate()
	a.clearRemember(st)
	st.set(nil, nil)
	if err == nil && !acting && a.users.SetRememberToken != nil && a.users.RememberToken(u) != "" {
		return a.users.SetRememberToken(ctx, u, randomToken())
	}
	if errors.Is(err, ErrUnauthenticated) {
		return nil
	}
	return err
}
