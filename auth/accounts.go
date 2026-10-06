// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"

	"anetos.dev/anetos/session"
)

// Disabled reports whether u's account is disabled (Users.Disabled).
func (a *Auth[U]) Disabled(u U) bool { return a.disabled(u) }

// CanSignOutEverywhere reports whether [Auth.SignOutEverywhere] is
// available: Users has SessionKey and SetSessionKey.
func (a *Auth[U]) CanSignOutEverywhere() bool { return a.users.SetSessionKey != nil }

var errNoSessionKey = errors.New("auth: SignOutEverywhere needs Users.SessionKey and Users.SetSessionKey")

// SignOutEverywhere ends every session of u, and every browser they are
// remembered in: it gives them a new session key (Users.SetSessionKey),
// which their sessions and remember-me cookies no longer match, and a new
// remember-me token. Their API tokens are untouched (RevokeAllTokens).
// The current request, if it is u's, stays signed in until it ends.
func (a *Auth[U]) SignOutEverywhere(ctx context.Context, u U) error {
	if a.users.SetSessionKey == nil {
		return errNoSessionKey
	}
	if err := a.users.SetSessionKey(ctx, u, randomToken()); err != nil {
		return err
	}
	if a.users.SetRememberToken != nil && a.users.RememberToken(u) != "" {
		return a.users.SetRememberToken(ctx, u, randomToken())
	}
	return nil
}

var (
	errImpersonating       = errors.New("auth: already acting as another user: stop first")
	errImpersonateToken    = errors.New("auth: Impersonate needs a session, not an API token")
	errImpersonateYourself = errors.New("auth: can't act as yourself")
)

// Impersonate signs the current user in as u, to see the app as u does
// (support, an admin's "act as"): the session keeps who they are, and
// [Auth.StopImpersonating] signs them back in. It is for sessions only
// (not API tokens), doesn't nest, and refuses a disabled u
// ([ErrDisabled]) or the user themselves. While it lasts, [Impersonator]
// returns the impersonator's ID, and each request checks that they may
// still sign in; Logout ends both. Check that the current user may act
// as u first (rbac.AuthorizeOver).
func (a *Auth[U]) Impersonate(ctx context.Context, u U) error {
	st := stateFrom(ctx)
	s := session.From(ctx)
	if st == nil || s == nil {
		return errNoMiddleware
	}
	if st.acting {
		return errActing
	}
	me, err := Current[U](ctx)
	if err != nil {
		return err
	}
	switch {
	case st.token != nil:
		return errImpersonateToken
	case s.String(keyImpersonator) != "":
		return errImpersonating
	case me.AuthID() == u.AuthID():
		return errImpersonateYourself
	}
	id, hash := s.String(keyID), s.String(keyHash)
	if err := a.login(ctx, u, u.AuthPassword(), false); err != nil {
		return err
	}
	s.Put(keyImpersonator, id)
	s.Put(keyImpersonatorHash, hash)
	return nil
}

// StopImpersonating ends [Auth.Impersonate]: it signs the impersonator
// back in and returns them. If they can no longer sign in (deleted,
// disabled, password changed, signed out everywhere), the session is
// signed out and the error says why. Without impersonation it returns
// [ErrNotImpersonating].
func (a *Auth[U]) StopImpersonating(ctx context.Context) (U, error) {
	var zero U
	st := stateFrom(ctx)
	s := session.From(ctx)
	if st == nil || s == nil {
		return zero, errNoMiddleware
	}
	if st.acting {
		return zero, errActing
	}
	id := s.String(keyImpersonator)
	if id == "" {
		return zero, ErrNotImpersonating
	}
	u, err := a.users.ByID(ctx, id)
	switch {
	case notFound(err):
		err = ErrUnauthenticated
	case err == nil && a.sessionPrint(u, u.AuthPassword()) != s.String(keyImpersonatorHash):
		err = ErrUnauthenticated
	}
	if err == nil {
		err = a.login(ctx, u, u.AuthPassword(), false) // ErrDisabled for a disabled one
	}
	if err != nil {
		s.Invalidate()
		a.clearRemember(st)
		st.set(nil, nil)
		return zero, err
	}
	return u, nil
}

// impersonatorValid reports whether the session's impersonator may still
// sign in.
func (a *Auth[U]) impersonatorValid(ctx context.Context, s *session.Session) (bool, error) {
	u, err := a.users.ByID(ctx, s.String(keyImpersonator))
	switch {
	case notFound(err):
		return false, nil
	case err != nil:
		return false, err
	}
	return a.sessionPrint(u, u.AuthPassword()) == s.String(keyImpersonatorHash) && !a.disabled(u), nil
}

// Impersonator returns the ID of the user acting as the signed-in one
// ([Auth.Impersonate]), and false if no one is.
func Impersonator(ctx context.Context) (string, bool) {
	s := session.From(ctx)
	if s == nil {
		return "", false
	}
	if id := s.String(keyImpersonator); id != "" && Check(ctx) {
		return id, true
	}
	return "", false
}
