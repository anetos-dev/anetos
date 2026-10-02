// SPDX-License-Identifier: Apache-2.0

package rbac

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"anetos.dev/anetos/db"
	"anetos.dev/anetos/db/migrate"
)

// grant is a row of rbac_grants: a role or a permission of a user, in a
// scope.
type grant struct {
	db.Model
	UserID string `db:"user_id"`
	Scope  string `db:"scope"` // "" for Global
	Kind   string `db:"kind"`  // kindRole or kindPermission
	Name   string `db:"name"`
}

func (grant) TableName() string { return "rbac_grants" }

const (
	kindRole       = "role"
	kindPermission = "permission"
)

// roleRow is a row of rbac_roles: a role stored in the database.
type roleRow struct {
	db.Model
	Name        string       `db:"name"`
	Title       string       `db:"title"`
	Permissions []Permission `db:"permissions,json"`
}

func (roleRow) TableName() string { return "rbac_roles" }

func (r roleRow) role() Role {
	return Role{Name: r.Name, Title: r.Title, Permissions: slices.Clone(r.Permissions), Custom: true}
}

var (
	colUserID = db.Col[string]("user_id")
	colScope  = db.Col[string]("scope")
	colKind   = db.Col[string]("kind")
	colName   = db.Col[string]("name")
)

// Migrations returns the migrations creating the rbac_grants and
// rbac_roles tables, for migrate.ForApp.
func Migrations() *migrate.Set {
	s := migrate.NewSet("rbac")
	s.AddFunc("2026_10_02_000300_create_rbac_tables",
		func(s *migrate.Schema) error {
			// Key columns are short enough for MySQL's index length limit.
			if err := s.Create("rbac_grants", func(t *migrate.Table) {
				t.ID()
				t.String("user_id", 100)
				t.String("scope", 100)
				t.String("kind", 10)
				t.String("name", 100)
				t.Timestamps()
				t.Unique("user_id", "scope", "kind", "name")
				t.Index("scope", "kind", "name")
			}); err != nil {
				return err
			}
			if s.Dialect() == "mysql" {
				// User IDs and scopes compare byte for byte, as elsewhere:
				// MySQL's and MariaDB's text collations ignore case, accents
				// and trailing spaces, so "team:ABC" would be "team:abc".
				// (Names are lowercase ASCII.)
				if err := s.Exec("ALTER TABLE rbac_grants MODIFY user_id VARBINARY(400) NOT NULL, MODIFY scope VARBINARY(400) NOT NULL"); err != nil {
					return err
				}
			}
			return s.Create("rbac_roles", func(t *migrate.Table) {
				t.ID()
				t.String("name", 100).Unique()
				t.String("title", 255)
				t.JSON("permissions")
				t.Timestamps()
			})
		},
		func(s *migrate.Schema) error {
			return errors.Join(s.Drop("rbac_roles"), s.Drop("rbac_grants"))
		})
	return s
}

// Assign gives the user with userID (an AuthID, up to 100 bytes) roles
// in scope ([Global] for everywhere). Roles the user has stay. Each role must be declared or
// in the database ([ErrUnknownRole]). Check that the signed-in user may
// give them first: [AuthorizeRole].
func Assign(ctx context.Context, userID string, scope Scope, roles ...string) error {
	if err := checkRoles(ctx, roles); err != nil {
		return err
	}
	return add(ctx, userID, scope, kindRole, roles)
}

// Unassign takes roles of the user away in scope (only there: not a
// global role, for another scope).
func Unassign(ctx context.Context, userID string, scope Scope, roles ...string) error {
	return remove(ctx, userID, scope, kindRole, roles)
}

// Sync makes roles the user's only roles in scope: others in scope are
// taken away (none removes them all, such as when a user leaves a team).
// Permissions given with [Grant] stay.
func Sync(ctx context.Context, userID string, scope Scope, roles ...string) error {
	if err := checkRoles(ctx, roles); err != nil {
		return err
	}
	if err := scope.check(); err != nil {
		return err
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		q := db.Query[grant](ctx).Where(colUserID.Eq(userID), colScope.Eq(string(scope)), colKind.Eq(kindRole))
		if len(roles) > 0 {
			q = q.Where(colName.NotIn(roles...))
		}
		if _, err := q.Delete(); err != nil {
			return err
		}
		return add(ctx, userID, scope, kindRole, roles)
	})
}

// Grant gives the user permissions in scope directly, without a role.
// Each must be declared ([ErrUnknownPermission]).
func Grant(ctx context.Context, userID string, scope Scope, perms ...Permission) error {
	reg, err := From(ctx)
	if err != nil {
		return err
	}
	names := make([]string, len(perms))
	for i, p := range perms {
		if !reg.known[p] {
			return fmt.Errorf("%w %q", ErrUnknownPermission, p)
		}
		names[i] = string(p)
	}
	return add(ctx, userID, scope, kindPermission, names)
}

// Revoke takes permissions given with [Grant] away from the user in
// scope. Permissions of the user's roles stay: take the role away, or
// give a role without them.
func Revoke(ctx context.Context, userID string, scope Scope, perms ...Permission) error {
	names := make([]string, len(perms))
	for i, p := range perms {
		names[i] = string(p)
	}
	return remove(ctx, userID, scope, kindPermission, names)
}

// RemoveUser takes every role and permission of the user away, in every
// scope: call it when deleting a user.
func RemoveUser(ctx context.Context, userID string) error {
	_, err := db.Query[grant](ctx).Where(colUserID.Eq(userID)).Delete()
	forget(ctx, userID)
	return err
}

// RemoveScope takes every role and permission in scope away, from every
// user: call it when deleting a team. It refuses [Global].
func RemoveScope(ctx context.Context, scope Scope) error {
	if scope == Global {
		return fmt.Errorf("%w: RemoveScope refuses the global scope", ErrInvalidScope)
	}
	_, err := db.Query[grant](ctx).Where(colScope.Eq(string(scope))).Delete()
	forget(ctx)
	return err
}

// add stores grants, keeping those that exist.
func add(ctx context.Context, userID string, scope Scope, kind string, names []string) error {
	if err := scope.check(); err != nil {
		return err
	}
	if userID == "" || len(userID) > 100 {
		return fmt.Errorf("rbac: invalid user ID %q (1 to 100 bytes)", userID)
	}
	if len(names) == 0 {
		return nil
	}
	rows := make([]grant, 0, len(names))
	for _, name := range names {
		rows = append(rows, grant{UserID: userID, Scope: string(scope), Kind: kind, Name: name})
	}
	err := db.Upsert(ctx, rows, []string{"user_id", "scope", "kind", "name"})
	forget(ctx, userID)
	return err
}

func remove(ctx context.Context, userID string, scope Scope, kind string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	_, err := db.Query[grant](ctx).Where(colUserID.Eq(userID), colScope.Eq(string(scope)), colKind.Eq(kind), colName.In(names...)).Delete()
	forget(ctx, userID)
	return err
}

// checkRoles returns [ErrUnknownRole] unless every role is declared or in
// the database.
func checkRoles(ctx context.Context, roles []string) error {
	reg, err := From(ctx)
	if err != nil {
		return err
	}
	var stored []string
	for _, name := range roles {
		if reg.byName[name] == nil {
			stored = append(stored, name)
		}
	}
	if len(stored) == 0 {
		return nil
	}
	found, err := customRoles(ctx, nil, stored) // not the unit's: it may be older than a DeleteRole
	if err != nil {
		return err
	}
	for _, name := range stored {
		if found[name] == nil {
			return fmt.Errorf("%w %q", ErrUnknownRole, name)
		}
	}
	return nil
}

// Assignment is a role of a user in a scope.
type Assignment struct {
	// UserID is the user's AuthID.
	UserID string `json:"user_id"`
	// Role is the role's name.
	Role string `json:"role"`
}

// Assignments returns the roles assigned in exactly scope, by user then
// role: a team's members. Global roles aren't included for another
// scope.
func Assignments(ctx context.Context, scope Scope) ([]Assignment, error) {
	rows, err := db.Query[grant](ctx).Where(colScope.Eq(string(scope)), colKind.Eq(kindRole)).
		OrderBy(colUserID.Asc(), colName.Asc()).Get()
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, len(rows))
	for i, row := range rows {
		out[i] = Assignment{UserID: row.UserID, Role: row.Name}
	}
	return out, nil
}

// UsersWith returns the IDs of the users allowed p in scope, by a grant in
// scope or a global one, sorted: whom to notify about a request to
// approve. API tokens play no part.
func UsersWith(ctx context.Context, scope Scope, p Permission) ([]string, error) {
	reg, err := From(ctx)
	if err != nil {
		return nil, err
	}
	if !reg.known[p] {
		return nil, undeclared(p)
	}
	var roles []string
	for _, role := range reg.roles {
		if role.Allows(p) {
			roles = append(roles, role.Name)
		}
	}
	custom, err := db.Query[roleRow](ctx).Get()
	if err != nil {
		return nil, err
	}
	for _, row := range custom {
		if reg.byName[row.Name] == nil && slices.Contains(row.Permissions, p) {
			roles = append(roles, row.Name)
		}
	}
	match := db.And(colKind.Eq(kindPermission), colName.Eq(string(p)))
	if len(roles) > 0 {
		match = db.Or(match, db.And(colKind.Eq(kindRole), colName.In(roles...)))
	}
	q := db.Query[grant](ctx).Where(colScope.In(scopeStrings(scope)...), match).Distinct().OrderBy(colUserID.Asc())
	return db.Pluck(q, colUserID)
}

func scopeStrings(s Scope) []string {
	out := []string{}
	for _, s := range scopes(s) {
		out = append(out, string(s))
	}
	return out
}

// Roles returns every role: those declared in code, in their order, then
// those of the database ([CreateRole]), by name.
func Roles(ctx context.Context) ([]Role, error) {
	reg, err := From(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query[roleRow](ctx).OrderBy(colName.Asc()).Get()
	if err != nil {
		return nil, err
	}
	out := reg.Roles()
	for _, row := range rows {
		if reg.byName[row.Name] == nil {
			out = append(out, row.role())
		}
	}
	return out, nil
}

// FindRole returns the role with the name, declared or in the database,
// or [ErrUnknownRole].
func FindRole(ctx context.Context, name string) (Role, error) {
	reg, err := From(ctx)
	if err != nil {
		return Role{}, err
	}
	if role, ok := reg.Role(name); ok {
		return role, nil
	}
	row, err := db.Query[roleRow](ctx).Where(colName.Eq(name)).First()
	if errors.Is(err, db.ErrNotFound) {
		return Role{}, fmt.Errorf("%w %q", ErrUnknownRole, name)
	}
	if err != nil {
		return Role{}, err
	}
	return row.role(), nil
}

// CreateRole stores a role in the database, made of declared permissions
// ([ErrUnknownPermission]). Its name must be free: [ErrRoleExists] if a
// role of the database has it, [ErrInvalidRole] if one declared in code
// does. It can't be super. Users who still have a role of the name that
// was removed from the code lose it: it allowed nothing until now, and
// mustn't start to.
func CreateRole(ctx context.Context, role Role) error {
	reg, err := checkCustom(ctx, role)
	if err != nil {
		return err
	}
	if reg.byName[role.Name] != nil {
		return fmt.Errorf("%w: %q is declared in code", ErrInvalidRole, role.Name)
	}
	exists, err := db.Query[roleRow](ctx).Where(colName.Eq(role.Name)).Exists()
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("%w: %q", ErrRoleExists, role.Name)
	}
	err = db.Tx(ctx, func(ctx context.Context) error {
		// Users may still have a role of this name that was removed from
		// the code: it allowed nothing, and mustn't start to now.
		if _, err := db.Query[grant](ctx).Where(colKind.Eq(kindRole), colName.Eq(role.Name)).Delete(); err != nil {
			return err
		}
		// Two creations at once: the unique index refuses the second.
		row := roleRow{Name: role.Name, Title: role.Title, Permissions: nonNil(role.Permissions)}
		return db.Create(ctx, &row)
	})
	forget(ctx)
	return err
}

// UpdateRole replaces the title and permissions of a role of the
// database, found by name: [ErrUnknownRole] if there is none,
// [ErrInvalidRole] for a role declared in code (change the code).
func UpdateRole(ctx context.Context, role Role) error {
	reg, err := checkCustom(ctx, role)
	if err != nil {
		return err
	}
	if reg.byName[role.Name] != nil {
		return fmt.Errorf("%w: %q is declared in code: change it there", ErrInvalidRole, role.Name)
	}
	n, err := db.Query[roleRow](ctx).Where(colName.Eq(role.Name)).
		Update(db.Col[string]("title").Set(role.Title), db.JSONCol[[]Permission]("permissions").Set(nonNil(role.Permissions)))
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w %q", ErrUnknownRole, role.Name)
	}
	forget(ctx)
	return nil
}

// DeleteRole deletes a role of the database and takes it away from every
// user: [ErrUnknownRole] if there is none, [ErrInvalidRole] for a role
// declared in code.
func DeleteRole(ctx context.Context, name string) error {
	reg, err := From(ctx)
	if err != nil {
		return err
	}
	if reg.byName[name] != nil {
		return fmt.Errorf("%w: %q is declared in code: remove it there", ErrInvalidRole, name)
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		n, err := db.Query[roleRow](ctx).Where(colName.Eq(name)).Delete()
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w %q", ErrUnknownRole, name)
		}
		if _, err := db.Query[grant](ctx).Where(colKind.Eq(kindRole), colName.Eq(name)).Delete(); err != nil {
			return err
		}
		forget(ctx)
		return nil
	})
}

// checkCustom checks a role for the database.
func checkCustom(ctx context.Context, role Role) (*Registry, error) {
	reg, err := From(ctx)
	if err != nil {
		return nil, err
	}
	if role.Super {
		return nil, fmt.Errorf("%w: only roles declared in code can be super", ErrInvalidRole)
	}
	if err := reg.checkRole(role); err != nil {
		return nil, err
	}
	return reg, nil
}

func nonNil(ps []Permission) []Permission {
	if ps == nil {
		return []Permission{}
	}
	return slices.Clone(ps)
}
