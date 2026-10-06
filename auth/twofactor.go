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
	"anetos.dev/anetos/session"
	"anetos.dev/anetos/web"
	"anetos.dev/anetos/web/ratelimit"
)

// Two-factor sign-in: a code from an authenticator app (TOTP, RFC 6238:
// six digits, a new one every 30 seconds), or a recovery code, after the
// password. See docs/site/guides/two-factor.md.

// Errors of two-factor sign-in.
var (
	// ErrTwoFactorRequired is returned by Attempt and SignIn when the
	// password (or other sign-in) checked out and the user has two-factor
	// sign-in on: the sign-in waits for a code ([Auth.AttemptTwoFactor]),
	// at AUTH_CHALLENGE_URL. 401.
	ErrTwoFactorRequired error = &statusError{http.StatusUnauthorized, "auth: a two-factor code is required"}
	// ErrNoPendingSignIn is returned by AttemptTwoFactor when no sign-in
	// waits for a code: there was none, it expired (after 10 minutes),
	// or the user's password changed meanwhile. 401.
	ErrNoPendingSignIn error = &statusError{http.StatusUnauthorized, "auth: no sign-in waits for a code"}
	// ErrInvalidCode is returned for a wrong, expired or used two-factor
	// or recovery code. 422.
	ErrInvalidCode error = &statusError{http.StatusUnprocessableEntity, "auth: invalid code"}
	// ErrTwoFactorOn is returned by StartTwoFactor for a user who has
	// two-factor sign-in on already: turn it off first. 409.
	ErrTwoFactorOn error = &statusError{http.StatusConflict, "auth: two-factor sign-in is on already"}
	// ErrTwoFactorOff is returned by ConfirmTwoFactor without a started
	// setup, and by NewRecoveryCodes for a user without two-factor
	// sign-in. 409.
	ErrTwoFactorOff error = &statusError{http.StatusConflict, "auth: two-factor sign-in isn't on"}
)

var errNoTwoFactor = errors.New("auth: two-factor sign-in needs Users.TwoFactor and Users.SetTwoFactor")

// pendingTTL is how long a sign-in waits for its code.
const pendingTTL = 10 * time.Minute

// recoveryCodes is how many recovery codes a user gets.
const recoveryCodes = 8

// codesPerDay caps a user's wrong two-factor codes at sign-in in a day,
// beyond AUTH_THROTTLE a minute: someone with the password can't try
// codes for long (at most about 0.015% a day to guess one).
const codesPerDay = 50

// twoFactorState is a user's two-factor state, stored encrypted
// (Users.TwoFactor).
type twoFactorState struct {
	Secret    []byte   `json:"s"`
	Confirmed bool     `json:"c,omitempty"`
	Codes     []string `json:"r,omitempty"` // recovery codes' SHA-256, hex
	LastStep  int64    `json:"t,omitempty"` // the last TOTP step used
}

// pending is a sign-in waiting for a code, in the session.
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

// TwoFactorStatus is a user's two-factor sign-in, as [Auth.TwoFactor]
// reports it.
type TwoFactorStatus struct {
	// On says sign-in asks for a code.
	On bool
	// Started says a setup was started and not confirmed.
	Started bool
	// RecoveryCodes is how many unused recovery codes are left.
	RecoveryCodes int
}

// TwoFactor reports u's two-factor sign-in. It fails if the state can't
// be read (APP_KEY changed without APP_PREVIOUS_KEYS).
func (a *Auth[U]) TwoFactor(u U) (TwoFactorStatus, error) {
	st, err := a.twoFactor(u)
	if err != nil || st == nil {
		return TwoFactorStatus{}, err
	}
	return TwoFactorStatus{On: st.Confirmed, Started: !st.Confirmed, RecoveryCodes: len(st.Codes)}, nil
}

// CanTwoFactor reports whether two-factor sign-in is available: Users has
// TwoFactor and SetTwoFactor.
func (a *Auth[U]) CanTwoFactor() bool { return a.users.TwoFactor != nil }

// TwoFactorSetup is a started two-factor setup: what the user's
// authenticator app needs.
type TwoFactorSetup struct {
	// Secret is the key, in base32, for typing it in.
	Secret string
	// URI is the otpauth:// URI, for a QR code (package qr).
	URI string
}

// StartTwoFactor starts turning on u's two-factor sign-in: it stores a
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

// ConfirmTwoFactor turns on u's two-factor sign-in, started with
// [Auth.StartTwoFactor], when code is the current one of their
// authenticator app, and returns their recovery codes: shown once, each
// signs in once without the app. It fails with [ErrInvalidCode] for a
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
	step, ok := checkTOTP(st.Secret, code, a.now(), 0)
	if !ok {
		return nil, ErrInvalidCode
	}
	codes, hashes := newRecoveryCodes()
	st.Confirmed, st.Codes, st.LastStep = true, hashes, step
	if err := a.saveTwoFactor(ctx, u, st); err != nil {
		return nil, err
	}
	a.clearHits(ctx, limit, key)
	if err := a.signOutOthers(ctx, u); err != nil {
		return nil, err
	}
	return codes, nil
}

// signOutOthers ends u's other sessions (with Users.SessionKey) and
// remember-me cookies, which were signed in without a code, keeping the
// request's session if it is u's.
func (a *Auth[U]) signOutOthers(ctx context.Context, u U) error {
	if a.users.SetRememberToken != nil && a.users.RememberToken(u) != "" {
		if err := a.users.SetRememberToken(ctx, u, randomToken()); err != nil {
			return err
		}
	}
	if a.users.SetSessionKey == nil {
		return nil
	}
	if err := a.users.SetSessionKey(ctx, u, randomToken()); err != nil {
		return err
	}
	s, st := session.From(ctx), stateFrom(ctx)
	if s == nil || st == nil || st.acting || s.String(keyID) != u.AuthID() || s.String(keyImpersonator) != "" {
		return nil
	}
	fresh, err := a.users.ByID(ctx, u.AuthID())
	if err != nil {
		return err
	}
	s.Put(keyHash, a.sessionPrint(fresh, fresh.AuthPassword()))
	a.clearRemember(st)
	return nil
}

// DisableTwoFactor turns off u's two-factor sign-in (or drops a started
// setup); their recovery codes stop working.
func (a *Auth[U]) DisableTwoFactor(ctx context.Context, u U) error {
	if a.users.TwoFactor == nil {
		return errNoTwoFactor
	}
	return a.saveTwoFactor(ctx, u, nil)
}

// NewRecoveryCodes replaces u's recovery codes with new ones, and returns
// them. It fails with [ErrTwoFactorOff] if two-factor sign-in is off.
func (a *Auth[U]) NewRecoveryCodes(ctx context.Context, u U) ([]string, error) {
	if a.users.TwoFactor == nil {
		return nil, errNoTwoFactor
	}
	st, err := a.twoFactor(u)
	if err != nil {
		return nil, err
	}
	if st == nil || !st.Confirmed {
		return nil, ErrTwoFactorOff
	}
	codes, hashes := newRecoveryCodes()
	st.Codes = hashes
	return codes, a.saveTwoFactor(ctx, u, st)
}

// SignIn signs u in, as [Auth.Login] does, unless they have two-factor
// sign-in on: then the sign-in waits for a code, for 10 minutes, and it
// returns [ErrTwoFactorRequired]; send them to AUTH_CHALLENGE_URL, whose
// handler calls [Auth.AttemptTwoFactor]. Use it for sign-in methods
// other than passwords (package auth/social does).
func (a *Auth[U]) SignIn(ctx context.Context, u U, remember bool) error {
	return a.signIn(ctx, u, u.AuthPassword(), remember)
}

// signIn is SignIn with hash, the password hash the session checks.
func (a *Auth[U]) signIn(ctx context.Context, u U, hash string, remember bool) error {
	st, err := a.twoFactor(u)
	if err != nil {
		return err
	}
	if st == nil || !st.Confirmed {
		if err := a.login(ctx, u, hash, remember); err != nil {
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
	// Whoever was signed in isn't any more: the session waits for u.
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

// TwoFactorPending reports whether the session has a sign-in waiting for
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

// AttemptTwoFactor finishes the sign-in waiting for a code ([Auth.SignIn],
// [Auth.Attempt]): code is the current one of the user's authenticator
// app (each used once), or one of their recovery codes (then used up).
// It fails with [ErrInvalidCode] for a wrong code, [ErrNoPendingSignIn]
// if no sign-in waits (send them to sign in again), and a
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
		return zero, ErrNoPendingSignIn
	}
	key := "auth:2fa\x00" + p.ID
	limit := ratelimit.PerMinute(a.cfg.Throttle)
	dayKey, perDay := "auth:2fa-day\x00"+p.ID, ratelimit.PerDay(codesPerDay)
	// Failures only count a day; every try counts a minute.
	if res, err := ratelimit.Check(ctx, dayKey, perDay); err != nil {
		return zero, err
	} else if !res.Allowed {
		return zero, &ThrottledError{RetryAfter: res.RetryAfter()}
	}
	if err := a.hit(ctx, key, limit); err != nil {
		return zero, err
	}
	u, err := a.users.ByID(ctx, p.ID)
	if notFound(err) {
		s.Delete(keyPending)
		return zero, ErrNoPendingSignIn
	}
	if err != nil {
		return zero, err
	}
	hash := u.AuthPassword()
	if a.sessionPrint(u, hash) != p.Print {
		s.Delete(keyPending) // the password changed, or they signed out everywhere
		return zero, ErrNoPendingSignIn
	}
	st, err := a.twoFactor(u)
	if err != nil {
		return zero, err
	}
	if st == nil || !st.Confirmed {
		// Turned off meanwhile: the password was enough.
		s.Delete(keyPending)
		if err := a.login(ctx, u, hash, p.Remember); err != nil {
			return zero, err
		}
		a.markFresh(ctx, u, hash)
		return u, nil
	}
	if step, ok := checkTOTP(st.Secret, code, a.now(), st.LastStep); ok {
		st.LastStep = step
	} else if i := matchRecovery(st.Codes, code); i >= 0 {
		st.Codes = append(st.Codes[:i:i], st.Codes[i+1:]...)
	} else {
		a.log.WarnContext(ctx, "auth: a wrong two-factor code", "user", p.ID)
		if _, err := ratelimit.Hit(ctx, dayKey, perDay); err != nil {
			return zero, err
		}
		return zero, ErrInvalidCode
	}
	// Stored before signing in: a code is used once, even if the rest
	// fails.
	if err := a.saveTwoFactor(ctx, u, st); err != nil {
		return zero, err
	}
	a.clearHits(ctx, limit, key)
	a.clearHits(ctx, perDay, dayKey)
	s.Delete(keyPending)
	if err := a.login(ctx, u, hash, p.Remember); err != nil {
		return zero, err
	}
	a.markFresh(ctx, u, hash)
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

// Password confirmation: a signed-in user types their password again
// before something dangerous; it holds for AUTH_CONFIRM_TTL.

// ErrPasswordNotConfirmed is returned by RequireConfirmed to API clients
// and htmx requests whose user hasn't confirmed their password lately.
// 423.
var ErrPasswordNotConfirmed error = &statusError{http.StatusLocked, "auth: confirm your password first"}

var errConfirmActing = &statusError{http.StatusForbidden, "auth: a password can't be confirmed while acting as another user"}

// ConfirmPassword checks the signed-in user's password and, if it is
// right, marks it confirmed in the session for AUTH_CONFIRM_TTL
// ([Auth.PasswordConfirmed]). It fails with [ErrInvalidCredentials] for a
// wrong one (or a user without a password), [ErrUnauthenticated] for a
// guest, and a [*ThrottledError] after AUTH_THROTTLE attempts in a
// minute. Not while acting as another user ([Auth.Impersonate]).
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
	key := "auth:confirm\x00" + u.AuthID() // signed in already: from any address
	limit := ratelimit.PerMinute(a.cfg.Throttle)
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
		return ErrInvalidCredentials
	}
	a.clearHits(ctx, limit, key)
	s.Put(keyConfirmed, confirmed{ID: u.AuthID(), At: a.now().Unix()})
	return nil
}

// confirmed is when the password was confirmed, and by whom.
type confirmed struct {
	ID string `json:"i"`
	At int64  `json:"t"`
}

// markFresh counts a sign-in just made as a confirmation for a user
// without a password (signing in with Google, say), who can't confirm
// one: they confirm by signing in again.
func (a *Auth[U]) markFresh(ctx context.Context, u U, hash string) {
	if s := session.From(ctx); s != nil && hash == "" {
		s.Put(keyConfirmed, confirmed{ID: u.AuthID(), At: a.now().Unix()})
	}
}

// PasswordConfirmed reports whether the signed-in user confirmed their
// password ([Auth.ConfirmPassword]) in the last AUTH_CONFIRM_TTL; for a
// user without a password, whether they signed in in that time. Signing
// in, out, or acting as another user forgets it; requests signed in with
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
