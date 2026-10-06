// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
)

// Password-reset and email-verification tokens are encrypted with
// APP_KEY, so nothing is stored: they carry the user, an expiry and, for
// resets, a fingerprint of the password hash and the session key, which
// makes a reset token stop working once the password has changed, or the
// user was signed out everywhere ([Auth.SignOutEverywhere],
// [Auth.SignOutOthers]).

const (
	resetContext  = "anetos/auth\x00reset"
	verifyContext = "anetos/auth\x00verify"
	revertContext = "anetos/auth\x00revert"
)

type signed struct {
	ID      string `json:"i"`
	Expires int64  `json:"x"`
	Hash    string `json:"h,omitempty"` // password and session key fingerprint (reset)
	Email   string `json:"e,omitempty"` // address to verify; the new one (revert)
	Old     string `json:"o,omitempty"` // the address to go back to (revert)
}

// PasswordResetToken returns a token for a link that lets u choose a new
// password. It works for AUTH_RESET_TTL, and only until the password
// changes, so it can be used once, or the user is signed out everywhere
// (a new session key: SignOutEverywhere, SignOutOthers).
func (a *Auth[U]) PasswordResetToken(u U) string {
	b, _ := json.Marshal(signed{ID: u.AuthID(), Expires: a.now().Add(a.cfg.ResetTTL).Unix(), Hash: a.sessionPrint(u, u.AuthPassword())})
	return a.enc.EncryptString(string(b), resetContext)
}

// CheckPasswordResetToken returns the user a token from
// [Auth.PasswordResetToken] was made for, or [ErrInvalidToken] if it is
// malformed, expired or already used. Store the new password, then sign
// the user in (or send them to the login page).
func (a *Auth[U]) CheckPasswordResetToken(ctx context.Context, token string) (U, error) {
	var zero U
	v, ok := a.open(token, resetContext)
	if !ok {
		return zero, ErrInvalidToken
	}
	u, err := a.users.ByID(ctx, v.ID)
	if notFound(err) {
		return zero, ErrInvalidToken
	}
	if err != nil {
		return zero, err
	}
	if a.sessionPrint(u, u.AuthPassword()) != v.Hash {
		return zero, ErrInvalidToken // the password or session key changed since: used
	}
	return u, nil
}

// VerificationToken returns a token for a link that confirms u owns
// email. It works for AUTH_VERIFY_TTL.
func (a *Auth[U]) VerificationToken(u U, email string) string {
	b, _ := json.Marshal(signed{ID: u.AuthID(), Expires: a.now().Add(a.cfg.VerifyTTL).Unix(), Email: email})
	return a.enc.EncryptString(string(b), verifyContext)
}

// CheckVerificationToken returns the user and the email address a token
// from [Auth.VerificationToken] was made for, or [ErrInvalidToken]. Mark
// the address verified if it is still the user's.
func (a *Auth[U]) CheckVerificationToken(ctx context.Context, token string) (U, string, error) {
	var zero U
	v, ok := a.open(token, verifyContext)
	if !ok || v.Email == "" {
		return zero, "", ErrInvalidToken
	}
	u, err := a.users.ByID(ctx, v.ID)
	if notFound(err) {
		return zero, "", ErrInvalidToken
	}
	if err != nil {
		return zero, "", err
	}
	return u, v.Email, nil
}

// EmailRevertToken returns a token for a link, sent to u's address
// oldEmail when they ask to change it to newEmail, that undoes the
// change: for the owner of the old address, if the change wasn't theirs.
// It works for AUTH_REVERT_TTL (7 days), after the change too.
func (a *Auth[U]) EmailRevertToken(u U, oldEmail, newEmail string) string {
	b, _ := json.Marshal(signed{ID: u.AuthID(), Expires: a.now().Add(a.cfg.RevertTTL).Unix(), Email: newEmail, Old: oldEmail})
	return a.enc.EncryptString(string(b), revertContext)
}

// CheckEmailRevertToken returns the user, the old address and the new
// one of a token from [Auth.EmailRevertToken], or [ErrInvalidToken].
// Undo the change only if the user's address (or the one they asked for)
// is still the new one.
func (a *Auth[U]) CheckEmailRevertToken(ctx context.Context, token string) (u U, oldEmail, newEmail string, err error) {
	var zero U
	v, ok := a.open(token, revertContext)
	if !ok || v.Email == "" || v.Old == "" {
		return zero, "", "", ErrInvalidToken
	}
	u, err = a.users.ByID(ctx, v.ID)
	if notFound(err) {
		return zero, "", "", ErrInvalidToken
	}
	if err != nil {
		return zero, "", "", err
	}
	return u, v.Old, v.Email, nil
}

func (a *Auth[U]) open(token, context string) (signed, bool) {
	var v signed
	plain, err := a.enc.DecryptString(token, context)
	if err != nil || json.Unmarshal([]byte(plain), &v) != nil || v.ID == "" || a.now().Unix() > v.Expires {
		return v, false
	}
	return v, true
}
