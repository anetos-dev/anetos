// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // TOTP (RFC 6238) is HMAC-SHA-1, which authenticator apps expect
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos/auth/password"
	"anetos.dev/anetos/cache"
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"
)

// Two-factor authentication: a code from an authenticator app (TOTP, RFC 6238:
// six digits, a new one every 30 seconds), or a recovery code, after the
// password. See docs/site/guides/two-factor.md.

// Errors of two-factor authentication.
var (
	// ErrTwoFactorRequired is returned by Attempt and Login when the
	// password (or other login) checked out and the user has two-factor
	// authentication on: the login waits for a code ([Auth.AttemptTwoFactor]),
	// at AUTH_CHALLENGE_URL. 401.
	ErrTwoFactorRequired error = &statusError{http.StatusUnauthorized, "auth: a two-factor code is required"}
	// ErrNoPendingLogin is returned by AttemptTwoFactor when no login
	// waits for a code: there was none, it expired (after 10 minutes),
	// or the user's password changed meanwhile. 401.
	ErrNoPendingLogin error = &statusError{http.StatusUnauthorized, "auth: no login waits for a code"}
	// ErrNoPendingSignIn is ErrNoPendingLogin.
	//
	// Deprecated: Use ErrNoPendingLogin; ErrNoPendingSignIn is removed in
	// v0.6.
	ErrNoPendingSignIn error = ErrNoPendingLogin
	// ErrInvalidCode is returned for a wrong, expired or used two-factor
	// or recovery code. 422.
	ErrInvalidCode error = &statusError{http.StatusUnprocessableEntity, "auth: invalid code"}
	// ErrTwoFactorOn is returned by StartTwoFactor for a user who has
	// two-factor authentication on already: turn it off first. 409.
	ErrTwoFactorOn error = &statusError{http.StatusConflict, "auth: two-factor authentication is on already"}
	// ErrTwoFactorOff is returned by ConfirmTwoFactor without a started
	// setup, and by NewRecoveryCodes for a user without two-factor
	// authentication. 409.
	ErrTwoFactorOff error = &statusError{http.StatusConflict, "auth: two-factor authentication isn't on"}
)

var errNoTwoFactor = errors.New("auth: two-factor authentication needs Users.TwoFactor and Users.SetTwoFactor")

// pendingTTL is how long a login waits for its code.
const pendingTTL = 10 * time.Minute

// recoveryCodes is how many recovery codes a user gets.
const recoveryCodes = 8

// codesPerDay caps a user's wrong two-factor codes at login in a day,
// beyond AUTH_THROTTLE a minute: someone with the password can't try
// codes for long (at most about 0.015% a day, UTC, to guess one).
const codesPerDay = 50

// confirmsPerDay caps a user's wrong passwords in a day when confirming
// or changing it while logged in, beyond AUTH_THROTTLE a minute: someone
// with a stolen session can't keep guessing the password, which unlocks
// two-factor settings, the email address and API tokens.
const confirmsPerDay = 50

// dayAllowed returns a [*ThrottledError] when key has used up limit, without
// counting this try: a day's budget counts failures only.
func dayAllowed(ctx context.Context, key string, limit ratelimit.Limit) error {
	res, err := ratelimit.Check(ctx, key, limit)
	if err != nil {
		return err
	}
	if !res.Allowed {
		return &ThrottledError{RetryAfter: res.RetryAfter()}
	}
	return nil
}

// twoFactorState is a user's two-factor state, stored encrypted
// (Users.TwoFactor).
type twoFactorState struct {
	Secret    []byte   `json:"s"`
	Confirmed bool     `json:"c,omitempty"`
	Codes     []string `json:"r,omitempty"` // recovery codes' SHA-256, hex
	LastStep  int64    `json:"t,omitempty"` // the last TOTP step used
}

// pending is a login waiting for a code, in the session.
type pending struct {
	ID       string `json:"id"`
	Print    string `json:"p"`
	Remember bool   `json:"r,omitempty"`
	Expires  int64  `json:"e"`
}

func twoFactorContext(id string) string { return "anetos/auth\x00two-factor\x00" + id }

// twoFactor reads u's state; nil if none.
func (a *Auth[U]) twoFactor(u U) (*twoFactorState, error) {
	if a.users.TwoFactor == nil {
		return nil, nil
	}
	raw := a.users.TwoFactor(u)
	if raw == "" {
		return nil, nil
	}
	plain, err := a.enc.DecryptString(raw, twoFactorContext(u.AuthID()))
	if err != nil {
		return nil, errors.New("auth: the user's two-factor state can't be read (was APP_KEY changed without APP_PREVIOUS_KEYS?)")
	}
	var st twoFactorState
	if err := json.Unmarshal([]byte(plain), &st); err != nil {
		return nil, errors.New("auth: the user's two-factor state is corrupt")
	}
	return &st, nil
}

func (a *Auth[U]) saveTwoFactor(ctx context.Context, u U, st *twoFactorState) error {
	if st == nil {
		return a.users.SetTwoFactor(ctx, u, "")
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return a.users.SetTwoFactor(ctx, u, a.enc.EncryptString(string(b), twoFactorContext(u.AuthID())))
}

// TwoFactorStatus is a user's two-factor authentication, as [Auth.TwoFactor]
// reports it.
type TwoFactorStatus struct {
	// On says login asks for a code.
	On bool
	// Started says a setup was started and not confirmed.
	Started bool
	// RecoveryCodes is how many unused recovery codes are left.
	RecoveryCodes int
}

// TwoFactor reports u's two-factor authentication. It fails if the state can't
// be read (APP_KEY changed without APP_PREVIOUS_KEYS).
func (a *Auth[U]) TwoFactor(u U) (TwoFactorStatus, error) {
	st, err := a.twoFactor(u)
	if err != nil || st == nil {
		return TwoFactorStatus{}, err
	}
	return TwoFactorStatus{On: st.Confirmed, Started: !st.Confirmed, RecoveryCodes: len(st.Codes)}, nil
}

// SupportsTwoFactor reports whether two-factor authentication is
// available: Users has TwoFactor and SetTwoFactor.
func (a *Auth[U]) SupportsTwoFactor() bool { return a.users.TwoFactor != nil }

// CanTwoFactor is [Auth.SupportsTwoFactor].
//
// Deprecated: Use SupportsTwoFactor; CanTwoFactor is removed in v0.6.
//
//go:fix inline
func (a *Auth[U]) CanTwoFactor() bool { return a.SupportsTwoFactor() }

// TwoFactorSetup is a started two-factor setup: what the user's
// authenticator app needs.
type TwoFactorSetup struct {
	// Secret is the key, in base32, for typing it in.
	Secret string
	// URI is the otpauth:// URI, for a QR code (package qr).
	URI string
}

// StartTwoFactor starts turning on u's two-factor authentication: it stores a
// new secret (replacing a setup started before) and returns it. The
// user adds it to their authenticator app (account names them there,
// with APP_NAME as the issuer), and [Auth.ConfirmTwoFactor] turns it on
// with a code. It fails with [ErrTwoFactorOn] if it is on.
func (a *Auth[U]) StartTwoFactor(ctx context.Context, u U, account string) (TwoFactorSetup, error) {
	if a.users.TwoFactor == nil {
		return TwoFactorSetup{}, errNoTwoFactor
	}
	st, err := a.twoFactor(u)
	if err != nil {
		return TwoFactorSetup{}, err
	}
	if st != nil && st.Confirmed {
		return TwoFactorSetup{}, ErrTwoFactorOn
	}
	secret := make([]byte, 20)
	_, _ = rand.Read(secret)
	if err := a.saveTwoFactor(ctx, u, &twoFactorState{Secret: secret}); err != nil {
		return TwoFactorSetup{}, err
	}
	return twoFactorSetup(secret, a.issuer, account), nil
}

// StartedTwoFactor returns u's setup started with [Auth.StartTwoFactor]
// and not yet confirmed, to show it again. It fails with
// [ErrTwoFactorOff] if none was started, and [ErrTwoFactorOn] once it is
// on (the secret is never shown again).
func (a *Auth[U]) StartedTwoFactor(u U, account string) (TwoFactorSetup, error) {
	if a.users.TwoFactor == nil {
		return TwoFactorSetup{}, errNoTwoFactor
	}
	st, err := a.twoFactor(u)
	switch {
	case err != nil:
		return TwoFactorSetup{}, err
	case st == nil:
		return TwoFactorSetup{}, ErrTwoFactorOff
	case st.Confirmed:
		return TwoFactorSetup{}, ErrTwoFactorOn
	}
	return twoFactorSetup(st.Secret, a.issuer, account), nil
}

// TwoFactorCode returns the code an authenticator app shows at t for
// secret (TwoFactorSetup.Secret): for tests.
func TwoFactorCode(secret string, t time.Time) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", errors.New("auth: the secret isn't base32")
	}
	return totp(key, t.Unix()/totpStep, 6), nil
}

func twoFactorSetup(secret []byte, issuer, account string) TwoFactorSetup {
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(secret)
	// As Google's Key URI format has it: the label "Issuer:account", each
	// escaped (spaces as %20, which apps read alike), no colon in either.
	esc := func(v string) string {
		return strings.ReplaceAll(url.QueryEscape(strings.ReplaceAll(v, ":", "")), "+", "%20")
	}
	label := esc(account)
	query := "secret=" + s
	if issuer != "" {
		label = esc(issuer) + ":" + label
		query += "&issuer=" + esc(issuer)
	}
	return TwoFactorSetup{Secret: s, URI: "otpauth://totp/" + label + "?" + query}
}

// ConfirmTwoFactor turns on u's two-factor authentication, started with
// [Auth.StartTwoFactor], when code is the current one of their
// authenticator app, and returns their recovery codes: shown once, each
// logs in once without the app. It fails with [ErrInvalidCode] for a
// wrong code, [ErrTwoFactorOff] without a started setup, and
// [ErrTwoFactorOn] if it is on already. Attempts are throttled
// (AUTH_THROTTLE a minute).
func (a *Auth[U]) ConfirmTwoFactor(ctx context.Context, u U, code string) ([]string, error) {
	if a.users.TwoFactor == nil {
		return nil, errNoTwoFactor
	}
	st, err := a.twoFactor(u)
	switch {
	case err != nil:
		return nil, err
	case st == nil:
		return nil, ErrTwoFactorOff
	case st.Confirmed:
		return nil, ErrTwoFactorOn
	}
	key := "auth:2fa\x00" + u.AuthID()
	limit := ratelimit.PerMinute(a.cfg.Throttle)
	if err := a.hit(ctx, key, limit); err != nil {
		return nil, err
	}
	var codes []string
	err = a.lockedTwoFactor(ctx, u, func(ctx context.Context, st *twoFactorState) error {
		switch {
		case st == nil:
			return ErrTwoFactorOff // turned off meanwhile
		case st.Confirmed:
			return ErrTwoFactorOn
		}
		step, ok := checkTOTP(st.Secret, code, a.now(), 0)
		if !ok {
			return ErrInvalidCode
		}
		var hashes []string
		codes, hashes = newRecoveryCodes()
		st.Confirmed, st.Codes, st.LastStep = true, hashes, step
		return a.saveTwoFactor(ctx, u, st)
	})
	if err != nil {
		return nil, err
	}
	a.clearHits(ctx, limit, key)
	if err := a.LogoutOthers(ctx, u); err != nil {
		return nil, err
	}
	return codes, nil
}

// LogoutOthers ends u's other sessions (with Users.SessionKey), their
// other remember-me cookies and their password-reset links, keeping the
// request's session if it is u's: it gets a new ID (copies of its cookie
// stop working, with a server-side session driver), and a remember-me
// cookie it had is issued again. ChangePassword and ConfirmTwoFactor call
// it; call it when something else about the account changes, such as its
// email address.
func (a *Auth[U]) LogoutOthers(ctx context.Context, u U) error {
	s, st := session.From(ctx), stateFrom(ctx)
	mine := s != nil && st != nil && !st.acting && s.String(keyID) == u.AuthID() && s.String(keyImpersonator) == ""
	var remembered rememberValue
	if mine && st.r != nil {
		if v, ok := a.rememberCookie(st.r); ok && v.ID == u.AuthID() {
			remembered = v
		}
	}
	if a.users.SetRememberToken != nil && a.users.RememberToken(u) != "" {
		if err := a.users.SetRememberToken(ctx, u, randomToken()); err != nil {
			return err
		}
	}
	if a.users.SetSessionKey != nil {
		if err := a.users.SetSessionKey(ctx, u, randomToken()); err != nil {
			return err
		}
	}
	if !mine {
		return nil
	}
	fresh, err := a.users.ByID(ctx, u.AuthID())
	if err != nil {
		return err
	}
	fp := a.sessionPrint(fresh, fresh.AuthPassword())
	s.Regenerate()
	s.Put(keyHash, fp)
	st.set(fresh, nil)
	if remembered.ID == "" || a.users.RememberToken == nil {
		a.clearRemember(st)
		return nil
	}
	tok := a.users.RememberToken(fresh)
	if tok == "" {
		tok = randomToken()
		if err := a.users.SetRememberToken(ctx, fresh, tok); err != nil {
			return err
		}
	}
	b, _ := json.Marshal(rememberValue{ID: fresh.AuthID(), Token: tok, Hash: fp, Expires: remembered.Expires})
	a.setRemember(st, a.enc.EncryptString(string(b), rememberContext))
	return nil
}

// DisableTwoFactor turns off u's two-factor authentication (or drops a started
// setup); their recovery codes stop working.
func (a *Auth[U]) DisableTwoFactor(ctx context.Context, u U) error {
	if a.users.TwoFactor == nil {
		return errNoTwoFactor
	}
	return cache.WithLock(ctx, twoFactorLock(u.AuthID()), 10*time.Second, func(ctx context.Context) error {
		return a.saveTwoFactor(ctx, u, nil)
	})
}

// NewRecoveryCodes replaces u's recovery codes with new ones, and returns
// them. It fails with [ErrTwoFactorOff] if two-factor authentication is off.
func (a *Auth[U]) NewRecoveryCodes(ctx context.Context, u U) ([]string, error) {
	if a.users.TwoFactor == nil {
		return nil, errNoTwoFactor
	}
	var codes []string
	err := a.lockedTwoFactor(ctx, u, func(ctx context.Context, st *twoFactorState) error {
		if st == nil || !st.Confirmed {
			return ErrTwoFactorOff
		}
		var hashes []string
		codes, hashes = newRecoveryCodes()
		st.Codes = hashes
		return a.saveTwoFactor(ctx, u, st)
	})
	if err != nil {
		return nil, err
	}
	return codes, nil
}

// twoFactorLock names the lock of a user's two-factor state, which every
// change of it holds (a code used, new recovery codes, two-factor authentication
// turned on or off), so concurrent changes don't overwrite each other.
func twoFactorLock(id string) string { return "auth:2fa:" + id }

// lockedTwoFactor runs fn holding u's two-factor lock, with the state
// read again from the store: u may be older than a change made
// meanwhile. fn saves through u.
func (a *Auth[U]) lockedTwoFactor(ctx context.Context, u U, fn func(ctx context.Context, st *twoFactorState) error) error {
	return cache.WithLock(ctx, twoFactorLock(u.AuthID()), 10*time.Second, func(ctx context.Context) error {
		fresh, err := a.users.ByID(ctx, u.AuthID())
		if err != nil {
			return err
		}
		st, err := a.twoFactor(fresh)
		if err != nil {
			return err
		}
		return fn(ctx, st)
	})
}

// SignIn is [Auth.Login], which now asks for the two-factor code too.
//
// Deprecated: Use Login; SignIn is removed in v0.6.
//
//go:fix inline
func (a *Auth[U]) SignIn(ctx context.Context, u U, remember bool) error {
	return a.Login(ctx, u, remember)
}

// login is Login with hash, the password hash the session checks.
func (a *Auth[U]) login(ctx context.Context, u U, hash string, remember bool) error {
	st, err := a.twoFactor(u)
	if err != nil {
		return err
	}
	if st == nil || !st.Confirmed {
		if err := a.startSession(ctx, u, hash, remember); err != nil {
			return err
		}
		a.markFresh(ctx, u, hash)
		return nil
	}
	s := session.From(ctx)
	ast := stateFrom(ctx)
	if s == nil || ast == nil {
		return errNoMiddleware
	}
	if ast.acting {
		return errActing
	}
	if remember && a.users.RememberToken == nil {
		return errNoRemember
	}
	if a.disabled(u) {
		return ErrDisabled
	}
	// Whoever was logged in isn't any more: the session waits for u.
	s.Regenerate()
	for _, k := range []string{keyID, keyHash, keyImpersonator, keyImpersonatorHash, keyConfirmed} {
		s.Delete(k)
	}
	a.clearRemember(ast)
	ast.set(nil, nil)
	s.Put(keyPending, pending{ID: u.AuthID(), Print: a.sessionPrint(u, hash), Remember: remember,
		Expires: a.now().Add(pendingTTL).Unix()})
	return ErrTwoFactorRequired
}

// TwoFactorPending reports whether the session has a login waiting for
// a two-factor code: the challenge page shows only then.
func (a *Auth[U]) TwoFactorPending(ctx context.Context) bool {
	_, ok := a.pending(ctx)
	return ok
}

func (a *Auth[U]) pending(ctx context.Context) (pending, bool) {
	s := session.From(ctx)
	var p pending
	if s == nil || !s.Get(keyPending, &p) || p.ID == "" || a.now().Unix() >= p.Expires {
		return pending{}, false
	}
	return p, true
}

// AttemptTwoFactor finishes the login waiting for a code ([Auth.Login],
// [Auth.Attempt]): code is the current one of the user's authenticator
// app (each used once), or one of their recovery codes (then used up).
// It fails with [ErrInvalidCode] for a wrong code, [ErrNoPendingLogin]
// if no login waits (send them to log in again), and a
// [*ThrottledError] after AUTH_THROTTLE attempts in a minute for the
// user, from any address.
//
//	u, err := a.AttemptTwoFactor(c, in.Code)
//	if errors.Is(err, auth.ErrInvalidCode) {
//		return nil, validate.Fail("code", "That code isn't right.")
//	}
func (a *Auth[U]) AttemptTwoFactor(ctx context.Context, code string) (U, error) {
	var zero U
	s := session.From(ctx)
	if stateFrom(ctx) == nil || s == nil {
		return zero, errNoMiddleware
	}
	p, ok := a.pending(ctx)
	if !ok {
		s.Delete(keyPending)
		return zero, ErrNoPendingLogin
	}
	u, hash, err := a.checkCode(ctx, p.ID, p.Print, code)
	if errors.Is(err, ErrNoPendingLogin) {
		s.Delete(keyPending) // the password changed, or they logged out everywhere
	}
	if err != nil {
		return zero, err
	}
	s.Delete(keyPending)
	if err := a.startSession(ctx, u, hash, p.Remember); err != nil {
		return zero, err
	}
	a.markFresh(ctx, u, hash)
	return u, nil
}

// checkCode checks a two-factor code for the login of user id, whose
// password and session key had the fingerprint print when it began, and
// returns the user and their password hash. Two-factor authentication turned
// off meanwhile lets the login through (the password was enough).
func (a *Auth[U]) checkCode(ctx context.Context, id, print, code string) (U, string, error) {
	var zero U
	key := "auth:2fa\x00" + id
	limit := ratelimit.PerMinute(a.cfg.Throttle)
	dayKey, perDay := "auth:2fa-day\x00"+id, ratelimit.PerDay(codesPerDay)
	// Failures only count a day; every try counts a minute.
	if res, err := ratelimit.Check(ctx, dayKey, perDay); err != nil {
		return zero, "", err
	} else if !res.Allowed {
		return zero, "", &ThrottledError{RetryAfter: res.RetryAfter()}
	}
	if err := a.hit(ctx, key, limit); err != nil {
		return zero, "", err
	}
	// One code check at a time per user, from reading the state to
	// storing it: two requests with the same code (or recovery code)
	// can't both use it.
	var (
		u       U
		hash    string
		skipped bool // two-factor authentication was turned off meanwhile
	)
	err := cache.WithLock(ctx, twoFactorLock(id), 10*time.Second, func(ctx context.Context) error {
		var err error
		u, err = a.users.ByID(ctx, id)
		if notFound(err) {
			return ErrNoPendingLogin
		}
		if err != nil {
			return err
		}
		hash = u.AuthPassword()
		if a.sessionPrint(u, hash) != print {
			return ErrNoPendingLogin // the password changed, or they logged out everywhere
		}
		st, err := a.twoFactor(u)
		if err != nil {
			return err
		}
		if st == nil || !st.Confirmed {
			skipped = true // turned off meanwhile: the password was enough
			return nil
		}
		if step, ok := checkTOTP(st.Secret, code, a.now(), st.LastStep); ok {
			st.LastStep = step
		} else if i := matchRecovery(st.Codes, code); i >= 0 {
			st.Codes = append(st.Codes[:i:i], st.Codes[i+1:]...)
		} else {
			a.log.WarnContext(ctx, "auth: a wrong two-factor code", "user", id)
			if _, err := ratelimit.Hit(ctx, dayKey, perDay); err != nil {
				return err
			}
			return ErrInvalidCode
		}
		// Stored before logging in: a code is used once, even if the
		// rest fails.
		return a.saveTwoFactor(ctx, u, st)
	})
	if err != nil {
		return zero, "", err
	}
	if !skipped {
		a.clearHits(ctx, limit, key)
		a.clearHits(ctx, perDay, dayKey)
	}
	return u, hash, nil
}

// TwoFactorChallenge is the error of [Auth.AttemptCredentials] for a
// user with two-factor authentication on: the password was right, and the
// login waits for a code. errors.Is(err, ErrTwoFactorRequired) is
// true. 401.
type TwoFactorChallenge struct {
	// Token is the challenge to give the client, which sends it back
	// with a code to [Auth.AttemptTwoFactorChallenge]. It works for 10
	// minutes, until the user's password or session key changes; it is
	// encrypted with APP_KEY and names the user.
	Token string
}

// Error implements error.
func (*TwoFactorChallenge) Error() string { return ErrTwoFactorRequired.Error() }

// Is makes the challenge [ErrTwoFactorRequired].
func (*TwoFactorChallenge) Is(target error) bool { return target == ErrTwoFactorRequired }

// HTTPStatus implements web.StatusCoder: 401.
func (*TwoFactorChallenge) HTTPStatus() int { return http.StatusUnauthorized }

const challengeContext = "anetos/auth\x00challenge"

// challengeToken returns a two-factor challenge for u, whose password
// hash is hash.
func (a *Auth[U]) challengeToken(u U, hash string) string {
	b, _ := json.Marshal(signed{ID: u.AuthID(), Expires: a.now().Add(pendingTTL).Unix(), Hash: a.sessionPrint(u, hash)})
	return a.enc.EncryptString(string(b), challengeContext)
}

// AttemptTwoFactorChallenge finishes an API's login that
// [Auth.AttemptCredentials] answered with a [*TwoFactorChallenge]:
// challenge is its Token, code the current one of the user's
// authenticator app (each used once) or one of their recovery codes
// (then used up). It returns the user, to give an API token; it logs
// no one in to a session. It fails as [Auth.AttemptTwoFactor] does:
// [ErrInvalidCode], a [*ThrottledError] (AUTH_THROTTLE tries a minute
// and 50 wrong codes a day for the user), and [ErrNoPendingLogin] for
// a malformed or expired challenge, or one made before the user's
// password or session key changed; and [ErrDisabled] for a disabled
// account. A challenge works more than once in its 10 minutes, each
// time with a new code.
func (a *Auth[U]) AttemptTwoFactorChallenge(ctx context.Context, challenge, code string) (U, error) {
	var zero U
	v, ok := a.open(challenge, challengeContext)
	if !ok || v.Hash == "" {
		return zero, ErrNoPendingLogin
	}
	u, _, err := a.checkCode(ctx, v.ID, v.Hash, code)
	if err != nil {
		return zero, err
	}
	if a.disabled(u) {
		return zero, ErrDisabled
	}
	return u, nil
}

func (a *Auth[U]) clearHits(ctx context.Context, limit ratelimit.Limit, keys ...string) {
	for _, k := range keys {
		if err := ratelimit.Clear(ctx, k, limit); err != nil {
			a.log.Warn("auth: clearing failed attempts", "error", err)
		}
	}
}

// totpStep is a TOTP code's period.
const totpStep = 30

// totp returns the six-digit code of secret for a time step.
func totp(secret []byte, step int64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // steps are positive
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	s := strconv.FormatUint(uint64(v%mod), 10)
	return strings.Repeat("0", digits-len(s)) + s
}

// checkTOTP checks code against the steps around now (one either side,
// for clocks a little off), later than after, and returns its step.
func checkTOTP(secret []byte, code string, now time.Time, after int64) (int64, bool) {
	code = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, code)
	if len(code) != 6 {
		return 0, false
	}
	step := now.Unix() / totpStep
	for _, s := range []int64{step, step - 1, step + 1} {
		if s > after && subtle.ConstantTimeCompare([]byte(totp(secret, s, 6)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

// recoveryAlphabet is the recovery codes' characters: letters and the
// digits 2 to 7. A 0 or 1 typed for o or l is read as them.
const recoveryAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// newRecoveryCodes returns new codes ("abcde-fghij", 50 bits each) and
// their hashes.
func newRecoveryCodes() (codes, hashes []string) {
	for range recoveryCodes {
		b := make([]byte, 10)
		_, _ = rand.Read(b)
		for i := range b {
			b[i] = recoveryAlphabet[b[i]&31]
		}
		c := string(b[:5]) + "-" + string(b[5:])
		codes = append(codes, c)
		hashes = append(hashes, recoveryHash(c))
	}
	return codes, hashes
}

// recoveryHash hashes a code, as typed: without spaces or dashes, in any
// case, 0 and 1 for o and l.
func recoveryHash(code string) string {
	code = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-':
			return -1
		case '0':
			return 'o'
		case '1':
			return 'l'
		}
		return r
	}, strings.ToLower(strings.TrimSpace(code)))
	sum := sha256.Sum256([]byte("anetos/auth\x00recovery\x00" + code))
	return hex.EncodeToString(sum[:])
}

// matchRecovery returns the index of code's hash in hashes, -1 if none.
func matchRecovery(hashes []string, code string) int {
	if len(strings.TrimSpace(code)) < 10 {
		return -1
	}
	h := recoveryHash(code)
	found := -1
	for i, x := range hashes {
		if subtle.ConstantTimeCompare([]byte(x), []byte(h)) == 1 {
			found = i
		}
	}
	return found
}

// Password confirmation: a logged-in user types their password again
// before something dangerous; it holds for AUTH_CONFIRM_TTL.

// ErrPasswordNotConfirmed is returned by RequireConfirmed to API clients
// and htmx requests whose user hasn't confirmed their password lately.
// 423.
var ErrPasswordNotConfirmed error = &statusError{http.StatusLocked, "auth: confirm your password first"}

var errConfirmActing = &statusError{http.StatusForbidden, "auth: a password can't be confirmed or changed while impersonating another user"}

// ConfirmPassword checks the logged-in user's password and, if it is
// right, marks it confirmed in the session for AUTH_CONFIRM_TTL
// ([Auth.PasswordConfirmed]). It fails with [ErrInvalidCredentials] for a
// wrong one (or a user without a password), [ErrUnauthenticated] for a
// guest, and a [*ThrottledError] after AUTH_THROTTLE attempts in a
// minute, or 50 wrong passwords for the user in a day (UTC; wrong
// passwords given to [Auth.ChangePassword] count too), so a stolen
// session can't be used to guess the password. Not while impersonating
// another user ([Auth.Impersonate]).
func (a *Auth[U]) ConfirmPassword(ctx context.Context, pw string) error {
	s := session.From(ctx)
	st := stateFrom(ctx)
	if st == nil || st.r == nil || s == nil {
		return errNoMiddleware
	}
	u, err := Current[U](ctx)
	if err != nil {
		return err
	}
	if st.token != nil {
		return ErrUnauthenticated // a session's, not an API token's
	}
	if s.String(keyImpersonator) != "" {
		return errConfirmActing
	}
	if err := a.CheckPassword(ctx, u, pw); err != nil {
		return err
	}
	s.Put(keyConfirmed, confirmed{ID: u.AuthID(), At: a.now().Unix()})
	return nil
}

// CheckPassword checks that pw is u's password, with the budget of
// [Auth.ConfirmPassword] (and [Auth.ChangePassword]): AUTH_THROTTLE
// tries a minute and 50 wrong passwords a day (UTC) for the user, from
// any address, so a stolen session or API token can't be used to guess
// it. It needs no session: an API asks for the password in the request
// of what the pages put behind [Auth.RequireConfirmed] (creating a
// token, changing two-factor authentication). It fails with
// [ErrInvalidCredentials] for a wrong password (or a user without one)
// and a [*ThrottledError].
func (a *Auth[U]) CheckPassword(ctx context.Context, u U, pw string) error {
	key := "auth:confirm\x00" + u.AuthID() // logged in already: from any address
	limit := ratelimit.PerMinute(a.cfg.Throttle)
	dayKey, perDay := "auth:confirm-day\x00"+u.AuthID(), ratelimit.PerDay(confirmsPerDay)
	if err := dayAllowed(ctx, dayKey, perDay); err != nil {
		return err
	}
	if err := a.hit(ctx, key, limit); err != nil {
		return err
	}
	hash := u.AuthPassword()
	if hash == "" {
		if err := password.DummyContext(ctx, pw); err != nil {
			return err
		}
		return ErrInvalidCredentials
	}
	ok, err := password.VerifyContext(ctx, pw, hash)
	if err != nil {
		return err
	}
	if !ok {
		if _, err := ratelimit.Hit(ctx, dayKey, perDay); err != nil {
			return err
		}
		return ErrInvalidCredentials
	}
	a.clearHits(ctx, limit, key)
	a.clearHits(ctx, perDay, dayKey)
	return nil
}

// confirmed is when the password was confirmed, and by whom.
type confirmed struct {
	ID string `json:"i"`
	At int64  `json:"t"`
}

// markFresh counts a login just made as a confirmation for a user
// without a password (logging in with Google, say), who can't confirm
// one: they confirm by logging in again.
func (a *Auth[U]) markFresh(ctx context.Context, u U, hash string) {
	if s := session.From(ctx); s != nil && hash == "" {
		s.Put(keyConfirmed, confirmed{ID: u.AuthID(), At: a.now().Unix()})
	}
}

// PasswordConfirmed reports whether the logged-in user confirmed their
// password ([Auth.ConfirmPassword]) in the last AUTH_CONFIRM_TTL; for a
// user without a password, whether they logged in within that time. Logging
// in, out, or impersonating another user forgets it; requests logged in with
// an API token never have it.
func (a *Auth[U]) PasswordConfirmed(ctx context.Context) bool {
	s := session.From(ctx)
	st := stateFrom(ctx)
	if s == nil || st == nil || s.String(keyImpersonator) != "" {
		return false
	}
	id, err := CurrentID(ctx)
	if err != nil || st.token != nil {
		return false
	}
	var c confirmed
	if !s.Get(keyConfirmed, &c) || c.ID != id {
		return false
	}
	since := a.now().Sub(time.Unix(c.At, 0))
	return since >= -time.Minute && since < a.cfg.ConfirmTTL
}

// RequireConfirmed lets through users who confirmed their password in
// the last AUTH_CONFIRM_TTL. Others are sent to AUTH_CONFIRM_URL, to
// come back after (to the page for GET requests, else to the page the
// form was on); API clients and htmx requests get
// [ErrPasswordNotConfirmed]. Put it after [Auth.Require].
func (a *Auth[U]) RequireConfirmed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if a.PasswordConfirmed(ctx) {
			next.ServeHTTP(w, r)
			return
		}
		s := session.From(ctx)
		if s == nil || web.WantsJSON(r) || r.Header.Get("HX-Request") != "" {
			web.WriteError(w, r, ErrPasswordNotConfirmed)
			return
		}
		back := r.URL.RequestURI()
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			back = sameSitePath(r, r.Referer())
		}
		if back != "" {
			s.Put(keyIntended, back)
		} else {
			s.Delete(keyIntended) // not an older page's
		}
		http.Redirect(w, r, web.LocalePath(ctx, a.cfg.ConfirmURL), http.StatusSeeOther)
	})
}

// sameSitePath returns the path and query of ref if it is on r's host,
// "" if not.
func sameSitePath(r *http.Request, ref string) string {
	u, err := url.Parse(ref)
	if err != nil || u.Host != r.Host || u.User != nil {
		return ""
	}
	p := u.RequestURI()
	if !localPath(p) {
		return ""
	}
	return p
}

// SignOutOthers is [Auth.LogoutOthers].
//
// Deprecated: Use LogoutOthers; SignOutOthers is removed in v0.6.
//
//go:fix inline
func (a *Auth[U]) SignOutOthers(ctx context.Context, u U) error { return a.LogoutOthers(ctx, u) }
