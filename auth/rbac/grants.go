// SPDX-License-Identifier: Apache-2.0

package rbac

import (
	"context"
	"maps"
	"slices"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/db"
)

// grants are one user's roles and permissions, as stored.
type grants struct {
	reg     *Registry
	byScope map[Scope]*level
}

// level is what a user has in exactly one scope.
type level struct {
	roles []string // declared or stored roles, in name order
	stale []string // roles neither declared nor stored: they allow nothing
	perms map[Permission]bool
	super bool
}

// empty reports whether the level allows nothing and has no role.
func (l *level) empty() bool { return len(l.roles) == 0 && len(l.perms) == 0 && !l.super }

// Grants are a user's roles and permissions, read once: [Of] returns any
// user's, [Current] the logged-in user's. A grant in [Global] applies in
// every scope. A Grants is a snapshot, safe for concurrent use.
type Grants struct {
	g     *grants
	token *auth.Token // limits a token-authenticated request; nil otherwise
}

// Of returns the grants of the user with userID (an AuthID). They are
// read from the database once per unit of work (a request, a job) and
// kept until the package's own writes change them.
func Of(ctx context.Context, userID string) (*Grants, error) {
	g, err := load(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &Grants{g: g}, nil
}

// Current returns the grants of the request's logged-in user (see [Of]),
// [auth.ErrUnauthenticated] for a guest, or the error that kept them from
// being loaded. For a request logged in with an API token, they allow
// only the permissions among the token's abilities, and have no roles
// unless the token has every ability ("*").
func Current(ctx context.Context) (*Grants, error) {
	id, err := auth.CurrentID(ctx)
	if err != nil {
		return nil, err
	}
	g, err := load(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &Grants{g: g}
	if tok, ok := auth.CurrentToken(ctx); ok {
		out.token = tok
	}
	return out, nil
}

// Can reports whether the grants allow p globally.
func (g *Grants) Can(p Permission) bool { return g.CanIn(Global, p) }

// CanIn reports whether the grants allow p in scope: by a grant in scope,
// or a global one. False for a permission that isn't declared.
func (g *Grants) CanIn(scope Scope, p Permission) bool {
	if !g.g.reg.known[p] {
		return false
	}
	if g.token != nil && !g.token.Can(string(p)) {
		return false
	}
	for _, s := range scopes(scope) {
		if l := g.g.byScope[s]; l != nil && (l.super || l.perms[p]) {
			return true
		}
	}
	return false
}

// rolesHidden reports whether the grants' roles are hidden: for a request
// logged in with an API token without every ability, since a role says
// nothing about what the token was given.
func (g *Grants) rolesHidden() bool { return g.token != nil && !g.token.Can("*") }

// HasRole reports whether the user has the role globally.
func (g *Grants) HasRole(role string) bool { return g.HasRoleIn(Global, role) }

// HasRoleIn reports whether the user has the role in scope or globally
// (false for a role neither declared nor in the database). For a request
// logged in with an API token, it is false unless the token has every
// ability ("*"): gate actions with permissions, which tokens narrow one
// by one.
func (g *Grants) HasRoleIn(scope Scope, role string) bool {
	if g.rolesHidden() {
		return false
	}
	for _, s := range scopes(scope) {
		if l := g.g.byScope[s]; l != nil && slices.Contains(l.roles, role) {
			return true
		}
	}
	return false
}

// Roles returns the names of the roles assigned in exactly scope (not the
// global ones, for another scope), in name order. Roles neither declared
// nor in the database are left out, and, as for [Grants.HasRoleIn], all
// of them for a token without every ability.
func (g *Grants) Roles(scope Scope) []string {
	l := g.g.byScope[scope]
	if l == nil || g.rolesHidden() {
		return nil
	}
	return slices.Clone(l.roles)
}

// Permissions returns the permissions allowed in scope (by grants in it,
// and global ones), in the order they were declared.
func (g *Grants) Permissions(scope Scope) []Permission {
	var out []Permission
	for _, p := range g.g.reg.perms {
		if g.CanIn(scope, p) {
			out = append(out, p)
		}
	}
	return out
}

// Scopes returns the scopes of kind ("team") where the user has a role or
// a declared permission, sorted: their teams. A global grant isn't
// counted: list the scopes of users with global roles from the app's own
// tables.
func (g *Grants) Scopes(kind string) []Scope {
	var out []Scope
	for s, l := range g.g.byScope {
		if s != Global && s.Kind() == kind && !l.empty() {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

// scopes returns the scopes whose grants apply in s.
func scopes(s Scope) []Scope {
	if s == Global {
		return []Scope{Global}
	}
	return []Scope{Global, s}
}

// load returns the user's grants, from the unit's cache or the database.
func load(ctx context.Context, userID string) (*grants, error) {
	reg, err := From(ctx)
	if err != nil {
		return nil, err
	}
	c := cacheFrom(ctx)
	var gen uint64
	if c != nil {
		c.mu.Lock()
		g, cached := c.users[userID], c.gen
		c.mu.Unlock()
		if g != nil {
			return g, nil
		}
		gen = cached
	}
	rows, err := db.Query[grant](ctx).Where(colUserID.Eq(userID)).OrderBy(colName.Asc()).Get()
	if err != nil {
		return nil, err
	}
	g := &grants{reg: reg, byScope: map[Scope]*level{}}
	assigned := map[*level][]string{}
	var stored []string // role names to look for in the database
	for _, row := range rows {
		if row.UserID != userID {
			continue // a database comparing without case or accents
		}
		s := Scope(row.Scope)
		l := g.byScope[s]
		if l == nil {
			l = &level{perms: map[Permission]bool{}}
			g.byScope[s] = l
		}
		switch row.Kind {
		case kindRole:
			assigned[l] = append(assigned[l], row.Name)
			if reg.byName[row.Name] == nil && !slices.Contains(stored, row.Name) {
				stored = append(stored, row.Name)
			}
		case kindPermission:
			if reg.known[Permission(row.Name)] {
				l.perms[Permission(row.Name)] = true
			}
		}
	}
	custom := map[string]*Role{}
	if len(stored) > 0 {
		if custom, err = customRoles(ctx, c, stored); err != nil {
			return nil, err
		}
	}
	for l, names := range assigned {
		for _, name := range names {
			role := reg.byName[name]
			if role == nil {
				role = custom[name]
			}
			if role == nil {
				l.stale = append(l.stale, name)
				continue
			}
			l.roles = append(l.roles, name)
			l.super = l.super || role.Super
			for _, p := range role.Permissions {
				if reg.known[p] { // a stored role may hold a permission no longer declared
					l.perms[p] = true
				}
			}
		}
	}
	if c != nil {
		// Kept once what was read is committed: never what a transaction
		// that rolls back wrote, or a read older than a change.
		db.AfterCommit(ctx, func(context.Context) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.gen != gen {
				return
			}
			if c.users == nil {
				c.users = map[string]*grants{}
			}
			c.users[userID] = g
		})
	}
	return g, nil
}

// customRoles returns the roles of the database with the names, from the
// unit's cache (if c isn't nil) or the database. Names not found are left
// out.
func customRoles(ctx context.Context, c *cache, names []string) (map[string]*Role, error) {
	out := map[string]*Role{}
	missing := names
	var gen uint64
	if c != nil {
		missing = nil
		c.mu.Lock()
		gen = c.gen
		for _, name := range names {
			if role, ok := c.custom[name]; ok {
				if role != nil {
					out[name] = role
				}
			} else {
				missing = append(missing, name)
			}
		}
		c.mu.Unlock()
	}
	if len(missing) == 0 {
		return out, nil
	}
	rows, err := db.Query[roleRow](ctx).Where(colName.In(missing...)).Get()
	if err != nil {
		return nil, err
	}
	found := map[string]*Role{}
	for _, row := range rows {
		role := row.role()
		found[role.Name] = &role
	}
	if c != nil {
		db.AfterCommit(ctx, func(context.Context) {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.gen != gen {
				return
			}
			if c.custom == nil {
				c.custom = map[string]*Role{}
			}
			for _, name := range missing {
				c.custom[name] = found[name] // nil: not found
			}
		})
	}
	maps.Copy(out, found)
	return out, nil
}

// forget drops users' grants from the unit's cache after a change (none:
// every user's, and the database's roles), now and again once the change
// is committed, so a read in between isn't kept.
func forget(ctx context.Context, userIDs ...string) {
	c := cacheFrom(ctx)
	if c == nil {
		return
	}
	drop := func(context.Context) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.gen++
		if len(userIDs) == 0 {
			c.users, c.custom = nil, nil
			return
		}
		for _, id := range userIDs {
			delete(c.users, id)
		}
	}
	drop(ctx)
	db.AfterCommit(ctx, drop)
}
