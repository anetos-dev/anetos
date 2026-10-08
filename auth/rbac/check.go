// SPDX-License-Identifier: Apache-2.0

package rbac

import (
	"context"
	"log/slog"
	"net/http"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/web"
)

// Can reports whether the request's signed-in user may do p globally:
// false for a guest. Use it to show or hide links and buttons. A failure
// to read the grants is logged and counts as no; [Authorize] returns it.
//
// For a request signed in with an API token, p must also be among the
// token's abilities (or the token must have "*").
func Can(ctx context.Context, p Permission) bool { return CanIn(ctx, Global, p) }

// CanIn reports whether the signed-in user may do p in scope, by a grant
// in scope or a global one ([Can]).
func CanIn(ctx context.Context, scope Scope, p Permission) bool {
	err := AuthorizeIn(ctx, scope, p)
	if err != nil && !auth.IsDenied(err) {
		logger(ctx).Error("rbac: checking a permission failed", "permission", string(p), "scope", scope.String(), "error", err)
	}
	return err == nil
}

// Authorize checks that the signed-in user may do p globally: nil if so,
// [auth.ErrUnauthenticated] (401) for a guest, [auth.ErrForbidden] (403)
// otherwise, so a handler can return the error as it is. Other errors
// (the database is down; p isn't declared, a bug) are returned too.
//
//	if err := rbac.Authorize(c, ManageUsers); err != nil {
//		return err
//	}
func Authorize(ctx context.Context, p Permission) error { return AuthorizeIn(ctx, Global, p) }

// AuthorizeIn checks that the signed-in user may do p in scope, by a grant
// in scope or a global one ([Authorize]).
func AuthorizeIn(ctx context.Context, scope Scope, p Permission) error {
	reg, err := From(ctx)
	if err != nil {
		return err
	}
	if !reg.known[p] {
		return undeclared(p)
	}
	g, err := Current(ctx)
	if err != nil {
		return err
	}
	if !g.CanIn(scope, p) {
		return auth.ErrForbidden
	}
	return nil
}

// HasRole reports whether the signed-in user has the role globally: false
// for a guest. For a request signed in with an API token, it is false
// unless the token has every ability ("*"); prefer permissions, which
// tokens narrow one by one.
func HasRole(ctx context.Context, role string) bool { return HasRoleIn(ctx, Global, role) }

// HasRoleIn reports whether the signed-in user has the role in scope or
// globally ([HasRole]).
func HasRoleIn(ctx context.Context, scope Scope, role string) bool {
	g, err := Current(ctx)
	if err != nil {
		if !auth.IsDenied(err) {
			logger(ctx).Error("rbac: checking a role failed", "role", role, "scope", scope.String(), "error", err)
		}
		return false
	}
	return g.HasRoleIn(scope, role)
}

// AuthorizeRole checks that the signed-in user may give the role to
// others in scope: they must be allowed, in scope, every permission the
// role allows (for a super role, be super there, with a token having
// every ability), so no one can give more than they have. nil if so;
// [auth.ErrUnauthenticated], [auth.ErrForbidden] or [ErrUnknownRole]
// otherwise.
//
// It compares permissions, not names: a role with no permissions, or the
// same as another's, is as easy to give. Gate actions with permissions,
// not with [HasRole]. Check also that the user may manage the scope's
// members, with a permission of the app's, and, before changing a user's
// roles, [AuthorizeRolesOf].
func AuthorizeRole(ctx context.Context, scope Scope, role string) error {
	r, err := FindRole(ctx, role)
	if err != nil {
		return err
	}
	g, err := Current(ctx)
	if err != nil {
		return err
	}
	return g.mayGive(scope, r)
}

// AuthorizeRolesOf checks that the signed-in user may give every role
// the user with userID has in exactly scope ([AuthorizeRole]), so may
// change or take them away: a team's owners can't be demoted or removed
// by someone who couldn't make owners. nil if so (or the user has no
// role there); [auth.ErrUnauthenticated] or [auth.ErrForbidden]
// otherwise.
func AuthorizeRolesOf(ctx context.Context, scope Scope, userID string) error {
	g, err := Current(ctx)
	if err != nil {
		return err
	}
	target, err := load(ctx, userID)
	if err != nil {
		return err
	}
	l := target.byScope[scope]
	if l == nil {
		return nil
	}
	for _, name := range l.roles {
		r, err := FindRole(ctx, name)
		if err != nil {
			return err
		}
		if err := g.mayGive(scope, r); err != nil {
			return err
		}
	}
	return nil
}

// AuthorizeOver returns nil if the signed-in user holds, in every scope,
// every permission userID has there (and a super role wherever userID
// has one), and [auth.ErrForbidden] otherwise: for managing their account
// (editing it, disabling it, acting as them, their roles), so no one
// takes over the account of someone with more power. It doesn't refuse
// the user themselves; check that apart where it matters.
func AuthorizeOver(ctx context.Context, userID string) error {
	g, err := Current(ctx)
	if err != nil {
		return err
	}
	target, err := load(ctx, userID)
	if err != nil {
		return err
	}
	for s, l := range target.byScope {
		if l.super {
			if err := g.mayGive(s, Role{Super: true}); err != nil {
				return err
			}
			continue
		}
		for p := range l.perms {
			if !g.CanIn(s, p) {
				return auth.ErrForbidden
			}
		}
	}
	return nil
}

// mayGive returns nil if the grants allow giving r in scope.
func (g *Grants) mayGive(scope Scope, r Role) error {
	if r.Super {
		for _, s := range scopes(scope) {
			if l := g.g.byScope[s]; l != nil && l.super && (g.token == nil || g.token.Can("*")) {
				return nil
			}
		}
		return auth.ErrForbidden
	}
	for _, p := range r.Permissions {
		if g.g.reg.known[p] && !g.CanIn(scope, p) { // a stored role may hold a permission no longer declared
			return auth.ErrForbidden
		}
	}
	return nil
}

// Require is middleware letting through only signed-in users allowed
// every one of perms globally; others get the error of [Authorize] (401,
// 403). Put it after the auth middleware, and after auth's Require on
// pages, which sends guests to the login page. API descriptions (package
// web/openapi) list its 401 and 403.
//
//	admin := members.Group("/admin", rbac.Require(ManageUsers))
func Require(perms ...Permission) web.Middleware {
	return RequireIn(func(*http.Request) (Scope, error) { return Global, nil }, perms...)
}

// RequireIn is [Require] in the scope that scope returns for the request,
// such as [PathScope]'s. An error from scope is written as the response.
//
//	team := r.Group("/teams/{team}", rbac.RequireIn(rbac.PathScope("team", "team"), ViewProjects))
func RequireIn(scope func(*http.Request) (Scope, error), perms ...Permission) web.Middleware {
	if len(perms) == 0 {
		panic("rbac: Require needs permissions")
	}
	return func(next http.Handler) http.Handler {
		return web.Documented(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s, err := scope(r)
			if err != nil {
				web.WriteError(w, r, err)
				return
			}
			for _, p := range perms {
				if err := AuthorizeIn(r.Context(), s, p); err != nil {
					web.WriteError(w, r, err)
					return
				}
			}
			next.ServeHTTP(w, r)
		}), requireDoc)
	}
}

// requireDoc is what Require and RequireIn tell API descriptions
// (web.Documented).
var requireDoc = web.MiddlewareDoc{Responses: map[int]string{
	http.StatusUnauthorized: "The request isn't signed in.",
	http.StatusForbidden:    "The user may not do this.",
}}

// PathScope returns a function for [RequireIn] that makes the scope of
// kind from the route's path parameter: PathScope("team", "team") on
// /teams/{team}/projects gives "team:42" for /teams/42/projects. A
// request without the parameter gets 404.
func PathScope(kind, param string) func(*http.Request) (Scope, error) {
	ScopeOf(kind, "") // panics for an invalid kind, at setup
	return func(r *http.Request) (Scope, error) {
		id := r.PathValue(param)
		if id == "" {
			return Global, web.Error(http.StatusNotFound, "")
		}
		return ScopeOf(kind, id), nil
	}
}

// logger returns the registry's logger, or slog's default.
func logger(ctx context.Context) *slog.Logger {
	if reg, err := From(ctx); err == nil {
		return reg.log
	}
	return slog.Default()
}
