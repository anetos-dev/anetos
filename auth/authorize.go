// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
)

// Authorize checks a policy for the request's user and subject: nil if
// allowed, [ErrUnauthenticated] (401) for a guest, [ErrForbidden] (403)
// if the policy says no. Policies are plain functions, usually methods of
// a policy type, so the compiler checks the user and subject types:
//
//	func (PostPolicy) Update(ctx context.Context, u *models.User, p *models.Post) bool {
//		return p.AuthorID == u.ID
//	}
//
//	if err := auth.Authorize(c, policies.Post.Update, &post); err != nil {
//		return nil, err
//	}
func Authorize[U Authenticatable, T any](ctx context.Context, policy func(context.Context, U, T) bool, subject T) error {
	u, err := Current[U](ctx)
	if err != nil {
		return err
	}
	if !policy(ctx, u, subject) {
		return ErrForbidden
	}
	return nil
}

// Allows reports whether a policy allows the request's user the action on
// subject: false for a guest. Use it to show or hide links and buttons.
func Allows[U Authenticatable, T any](ctx context.Context, policy func(context.Context, U, T) bool, subject T) bool {
	return Authorize(ctx, policy, subject) == nil
}

// AuthorizeUser checks a policy about the user alone (an admin area,
// creating something): nil if allowed, [ErrUnauthenticated] or
// [ErrForbidden] otherwise.
//
//	if err := auth.AuthorizeUser(c, policies.Post.Create); err != nil {
func AuthorizeUser[U Authenticatable](ctx context.Context, policy func(context.Context, U) bool) error {
	u, err := Current[U](ctx)
	if err != nil {
		return err
	}
	if !policy(ctx, u) {
		return ErrForbidden
	}
	return nil
}

// AllowsUser reports whether a policy about the user alone allows the
// request's user: false for a guest.
func AllowsUser[U Authenticatable](ctx context.Context, policy func(context.Context, U) bool) bool {
	return AuthorizeUser(ctx, policy) == nil
}

// IsDenied reports whether err is [ErrUnauthenticated] or [ErrForbidden].
func IsDenied(err error) bool {
	return errors.Is(err, ErrUnauthenticated) || errors.Is(err, ErrForbidden)
}
