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

var errActing = errors.New("auth: the context has a user without a session (WithUser): there is no session to log in or out")

// Attempt logs in the user whose login and password match, and returns
// them. It fails with [ErrInvalidCredentials] for an unknown login or a
// wrong password (taking about as long either way), and with a
// [*ThrottledError] after AUTH_THROTTLE attempts in a minute for this
// login, or this account, from this IP address, or AUTH_THROTTLE_IP
// failures in a minute from this IP address. A success clears the
// login's and account's counts, upgrades a weaker password hash (with
// Users.SetPassword) and logs the user in, as [Auth.Login] does: for a
// user with two-factor authentication on, it returns the user and
// [ErrTwoFactorRequired], and the login waits for a code.
//
//	u, err := a.Attempt(c, in.Email, in.Password, in.Remember)
//	if errors.Is(err, auth.ErrInvalidCredentials) {
//		return nil, validate.Fail("email", "These credentials don't match our records.")
//	}
func (a *Auth[U]) Attempt(ctx context.Context, login, pw string, remember bool) (U, error) {
	var zero U
	if remember && a.users.RememberToken == nil {
		return zero, errNoRemember
	}
	u, hash, err := a.checkCredentials(ctx, login, pw)
	if err != nil {
		return zero, err
	}
	return u, a.login(ctx, u, hash, remember)
}

// AttemptCredentials checks a login and password as [Auth.Attempt] does
// (the same throttling, timing and rehashing) and returns the user,
// without logging anyone in to a session: for an API's login, which
// answers with an API token ([Auth.CreateToken]). It fails as Attempt
// does, and with [ErrDisabled] for a disabled account. For a user with
// two-factor authentication on, it fails with a [*TwoFactorChallenge] (which
// is [ErrTwoFactorRequired]): give the client its Token, to send back
// with a code to [Auth.AttemptTwoFactorChallenge]. The route needs
// [Auth.TokenMiddleware] (or [Auth.Middleware]), which knows the
// client's address.
//
//	u, err := a.AttemptCredentials(c, in.Email, in.Password)
//	var challenge *auth.TwoFactorChallenge
//	if errors.As(err, &challenge) {
//		return LoginResponse{TwoFactor: true, Challenge: challenge.Token}, nil
//	}
func (a *Auth[U]) AttemptCredentials(ctx context.Context, login, pw string) (U, error) {
	var zero U
	u, hash, err := a.checkCredentials(ctx, login, pw)
	if err != nil {
		return zero, err
	}
	if a.disabled(u) {
		return zero, ErrDisabled
	}
	st, err := a.twoFactor(u)
	if err != nil {
		return zero, err
	}
	if st != nil && st.Confirmed {
		return zero, &TwoFactorChallenge{Token: a.challengeToken(u, hash)}
	}
	return u, nil
}

// checkCredentials is Attempt's check of a login and password: it
// returns the user and their (possibly upgraded) password hash.
func (a *Auth[U]) checkCredentials(ctx context.Context, login, pw string) (U, string, error) {
	var zero U
	st := stateFrom(ctx)
	if st == nil || st.r == nil {
		return zero, "", errNoMiddleware
	}
	ip := ratelimit.IP(st.r)
	perLogin := ratelimit.PerMinute(a.cfg.Throttle)
	perIP := ratelimit.PerMinute(a.cfg.ThrottleIP)
	loginKey := "auth:login\x00" + strings.ToLower(strings.TrimSpace(login)) + "\x00" + ip
	ipKey := "auth:ip\x00" + ip
	// Per address, whatever the login, counting failures only (an office
	// behind one address logs in a lot).
	if res, err := ratelimit.Check(ctx, ipKey, perIP); err != nil {
		return zero, "", err
	} else if !res.Allowed {
		return zero, "", &ThrottledError{RetryAfter: res.RetryAfter()}
	}
	failed := func() (U, string, error) {
		if _, err := ratelimit.Hit(ctx, ipKey, perIP); err != nil {
			return zero, "", err
		}
		return zero, "", ErrInvalidCredentials
	}
	if err := a.hit(ctx, loginKey, perLogin); err != nil {
		return zero, "", err
	}
	u, err := a.users.ByLogin(ctx, login)
	found := err == nil
	if err != nil && !notFound(err) {
		return zero, "", err
	}
	// Per account too: spellings of a login that ByLogin takes as the
	// same account (case, spaces, lookalike letters) share this count.
	// Unknown logins count under the login, so both cost the same.
	userKey := "auth:user\x00login:" + strings.ToLower(strings.TrimSpace(login)) + "\x00" + ip
	if found {
		userKey = "auth:user\x00" + u.AuthID() + "\x00" + ip
	}
	if err := a.hit(ctx, userKey, perLogin); err != nil {
		return zero, "", err
	}
	hash := ""
	if found {
		hash = u.AuthPassword()
	}
	if hash == "" {
		// Unknown, or without a password (social login only): take as
		// long as a check.
		if err := password.DummyContext(ctx, pw); err != nil {
			return zero, "", err
		}
		return failed()
	}
	ok, err := password.VerifyContext(ctx, pw, hash)
	if err != nil {
		return zero, "", err
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
	return u, hash, nil
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

// Login logs u in: the session gets a new ID (so a session identifier
// seen before the login is useless) and remembers the user; with
// remember, a remember-me cookie keeps them logged in for
// AUTH_REMEMBER_TTL after the session ends. When u has two-factor
// authentication on, the login waits for a code instead, for 10
// minutes, and Login returns [ErrTwoFactorRequired]: send them to
// AUTH_CHALLENGE_URL, whose handler calls [Auth.AttemptTwoFactor]. Use it
// after registration, and for login methods other than passwords
// (package auth/social does); a password login goes through
// [Auth.Attempt].
func (a *Auth[U]) Login(ctx context.Context, u U, remember bool) error {
	return a.login(ctx, u, u.AuthPassword(), remember)
}

// LoginSession writes a logged-in session for u into s, as [Auth.Login]
// without remember-me would for a user without two-factor authentication
// (it doesn't ask for the code), but without a request: for tests (see
// anetostest.ActingAs) and tools that prepare sessions. It returns
// [ErrDisabled] for a disabled account.
func (a *Auth[U]) LoginSession(s *session.Session, u U) error {
	if a.disabled(u) {
		return ErrDisabled
	}
	s.Regenerate()
	s.Put(keyID, u.AuthID())
	s.Put(keyHash, a.sessionPrint(u, u.AuthPassword()))
	for _, k := range []string{keyImpersonator, keyImpersonatorHash, keyPending, keyConfirmed} {
		s.Delete(k)
	}
	return nil
}

var errNoRemember = errors.New("auth: remember me needs Users.RememberToken and Users.SetRememberToken")

// startSession logs u in without asking for a two-factor code, with hash
// as the password hash the session checks.
func (a *Auth[U]) startSession(ctx context.Context, u U, hash string, remember bool) error {
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
		Expires: a.now().Add(a.cfg.RememberTTL).Unix()})
	a.setRemember(st, a.enc.EncryptString(string(b), rememberContext))
	return nil
}

// Logout logs the request's user out: the session is emptied and gets a
// new ID, the remember-me cookie is removed, and the user's remember-me
// token is replaced, which logs them out of every remembered browser.
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
	// Impersonating u (Impersonate): logging out is the impersonator's, and
	// mustn't log u out of their remembered browsers.
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
