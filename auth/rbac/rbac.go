// SPDX-License-Identifier: Apache-2.0

package rbac

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	"anetos.dev/anetos"
)

// Permission is something a user may do: "projects.create". Declare the
// app's permissions as constants and pass them all to [New]; checking
// one that isn't declared is a bug, reported as an error.
//
// A permission's name is also the API token ability that allows it (see
// [Can]). Names are lowercase letters, digits and . _ : -, up to 100
// characters.
type Permission string

// Role is a named set of permissions. The app's roles are declared in
// code and passed to [New]; administrators can add roles of their own
// in the database with [CreateRole].
type Role struct {
	// Name identifies the role: "owner", "billing-manager". Lowercase
	// letters, digits and . _ : -, up to 100 characters.
	Name string `json:"name"`
	// Title is the role's name for people ("Billing manager"); empty for
	// the Name.
	Title string `json:"title"`
	// Permissions are what the role allows.
	Permissions []Permission `json:"permissions"`
	// Super allows every permission (and its Permissions must be empty).
	// Only roles declared in code can be super.
	Super bool `json:"super"`
	// Custom is true for the roles stored in the database ([CreateRole]),
	// false for those declared in code.
	Custom bool `json:"custom"`
}

// DisplayName returns the Title, or the Name if it has none.
func (r Role) DisplayName() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

// Allows reports whether the role allows p.
func (r Role) Allows(p Permission) bool { return r.Super || slices.Contains(r.Permissions, p) }

// Scope is where a grant applies: [Global], or a scope made with
// [ScopeOf], such as a team ("team:42"). A grant in a scope applies
// there only; a global grant applies everywhere.
type Scope string

// Global is the scope of grants that apply everywhere.
const Global Scope = ""

var (
	nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.:-]{0,99}$`)
	kindRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// ScopeOf returns the scope of kind and id: ScopeOf("team", 42) is
// "team:42". kind is lowercase letters, digits, _ and -, starting with a
// letter; ScopeOf panics otherwise, as kinds are constants in code. id is
// formatted with fmt.Sprint, and compared exactly ("ABC" isn't "abc").
// Grants can be stored in scopes of up to 100 bytes.
func ScopeOf(kind string, id any) Scope {
	if !kindRe.MatchString(kind) {
		panic(fmt.Sprintf("rbac: invalid scope kind %q (lowercase letters, digits, _ and -, starting with a letter)", kind))
	}
	return Scope(kind + ":" + fmt.Sprint(id))
}

// Kind returns the scope's kind ("team"), "" for [Global].
func (s Scope) Kind() string {
	kind, _, _ := strings.Cut(string(s), ":")
	return kind
}

// ID returns the scope's id ("42"), "" for [Global].
func (s Scope) ID() string {
	_, id, _ := strings.Cut(string(s), ":")
	return id
}

// String returns the scope as stored: "team:42", or "global".
func (s Scope) String() string {
	if s == Global {
		return "global"
	}
	return string(s)
}

// check reports whether grants can be stored in s.
func (s Scope) check() error {
	if s == Global {
		return nil
	}
	kind, id, ok := strings.Cut(string(s), ":")
	if !ok || !kindRe.MatchString(kind) || id == "" || len(s) > 100 {
		return fmt.Errorf("%w %q: make scopes with rbac.ScopeOf, up to 100 bytes", ErrInvalidScope, string(s))
	}
	return nil
}

// Registry holds the app's permissions and the roles declared in code.
// Create it with [New] or [NewRegistry]; it is safe for concurrent use.
type Registry struct {
	perms  []Permission
	known  map[Permission]bool
	roles  []Role
	byName map[string]*Role
	log    *slog.Logger
}

// NewRegistry returns a registry of the permissions and roles. It checks them:
// valid and unique names, roles built from the permissions.
func NewRegistry(permissions []Permission, roles ...Role) (*Registry, error) {
	r := &Registry{known: map[Permission]bool{}, byName: map[string]*Role{}, log: slog.Default()}
	for _, p := range permissions {
		if !nameRe.MatchString(string(p)) {
			return nil, fmt.Errorf("rbac: invalid permission name %q (lowercase letters, digits and . _ : -, up to 100 characters)", p)
		}
		if r.known[p] {
			return nil, fmt.Errorf("rbac: permission %q declared twice", p)
		}
		r.known[p] = true
		r.perms = append(r.perms, p)
	}
	r.roles = make([]Role, len(roles))
	for i, role := range roles {
		role.Permissions = append([]Permission{}, role.Permissions...) // never nil: JSON []
		role.Custom = false
		if err := r.checkRole(role); err != nil {
			return nil, err
		}
		if r.byName[role.Name] != nil {
			return nil, fmt.Errorf("rbac: role %q declared twice", role.Name)
		}
		if role.Super && len(role.Permissions) > 0 {
			return nil, fmt.Errorf("rbac: role %q is super, so it has every permission: leave its Permissions empty", role.Name)
		}
		r.roles[i] = role
		r.byName[role.Name] = &r.roles[i]
	}
	return r, nil
}

// checkRole checks a role's name and permissions.
func (r *Registry) checkRole(role Role) error {
	if !nameRe.MatchString(role.Name) {
		return fmt.Errorf("%w: invalid role name %q (lowercase letters, digits and . _ : -, up to 100 characters)", ErrInvalidRole, role.Name)
	}
	if len(role.Title) > 255 {
		return fmt.Errorf("%w: the title of role %q is longer than 255 bytes", ErrInvalidRole, role.Name)
	}
	seen := map[Permission]bool{}
	for _, p := range role.Permissions {
		if !r.known[p] {
			return fmt.Errorf("%w %q, in role %q", ErrUnknownPermission, p, role.Name)
		}
		if seen[p] {
			return fmt.Errorf("%w: role %q has permission %q twice", ErrInvalidRole, role.Name, p)
		}
		seen[p] = true
	}
	return nil
}

// New returns a registry of the permissions and roles ([NewRegistry]) and
// provides it to the app: the package's functions find it in the
// context. It caches each user's grants for the length of an operation
// (anetos.App.AroundOperations), and adds the rbac:roles, rbac:user,
// rbac:assign and rbac:unassign commands. The tables come from
// [Migrations].
func New(app *anetos.App, permissions []Permission, roles ...Role) (*Registry, error) {
	if _, ok := anetos.Lookup[*Registry](app); ok {
		return nil, errors.New("rbac: New called twice for one app")
	}
	r, err := NewRegistry(permissions, roles...)
	if err != nil {
		return nil, err
	}
	r.log = app.Logger().With("component", "rbac")
	if err := addCommands(app); err != nil {
		return nil, err
	}
	app.AroundOperations(func(ctx context.Context, _ anetos.Operation) (context.Context, func()) {
		if cacheFrom(ctx) != nil {
			// An operation inside another (a tool call in a request, a job
			// run synchronously) shares its grants.
			return ctx, nil
		}
		return context.WithValue(ctx, cacheKey{}, &cache{}), nil
	})
	app.AddContextValue(registryKey{}, r)
	anetos.Provide(app, r)
	return r, nil
}

// ForApp is [New].
//
// Deprecated: Use New; ForApp is removed in v0.6.
//
//go:fix inline
func ForApp(app *anetos.App, permissions []Permission, roles ...Role) (*Registry, error) {
	return New(app, permissions, roles...)
}

type registryKey struct{}

// WithRegistry returns ctx carrying r, for code that uses a registry
// made with [NewRegistry] instead of [New].
func WithRegistry(ctx context.Context, r *Registry) context.Context {
	return context.WithValue(ctx, registryKey{}, r)
}

// From returns the registry in ctx ([New], [WithRegistry]), or
// [ErrNoRegistry].
func From(ctx context.Context) (*Registry, error) {
	if r, ok := ctx.Value(registryKey{}).(*Registry); ok {
		return r, nil
	}
	return nil, ErrNoRegistry
}

// Permissions returns the declared permissions, in their order.
func (r *Registry) Permissions() []Permission { return slices.Clone(r.perms) }

// Declare adds permissions to the registry, for packages that bring
// their own (package admin declares admin.access and a permission per
// resource), so the app's list needn't name them. Permissions already
// declared are left as they are. Call it at setup, before the app
// serves: the registry isn't locked for changes.
func (r *Registry) Declare(permissions ...Permission) error {
	for _, p := range permissions {
		if !nameRe.MatchString(string(p)) {
			return fmt.Errorf("rbac: invalid permission name %q (lowercase letters, digits and . _ : -, up to 100 characters)", p)
		}
	}
	for _, p := range permissions {
		if !r.known[p] {
			r.known[p] = true
			r.perms = append(r.perms, p)
		}
	}
	return nil
}

// Declared reports whether p is declared.
func (r *Registry) Declared(p Permission) bool { return r.known[p] }

// Roles returns the roles declared in code, in their order. [Roles]
// (the function) adds the database's.
func (r *Registry) Roles() []Role {
	out := make([]Role, len(r.roles))
	for i, role := range r.roles {
		out[i] = role
		out[i].Permissions = slices.Clone(role.Permissions)
	}
	return out
}

// Role returns the role declared in code with the name.
func (r *Registry) Role(name string) (Role, bool) {
	role, ok := r.byName[name]
	if !ok {
		return Role{}, false
	}
	out := *role
	out.Permissions = slices.Clone(role.Permissions)
	return out, true
}

// undeclared is the error of a check of a permission that isn't declared:
// a bug in the app (500), not the client's.
func undeclared(p Permission) error {
	return fmt.Errorf("rbac: permission %q isn't declared: pass it to rbac.New", p)
}

// Errors. Those with a status can be returned by a handler as they are.
var (
	// ErrNoRegistry is returned when the context has no registry: call
	// [New] in the app's setup.
	ErrNoRegistry = errors.New("rbac: no permissions in the context: call rbac.New")
	// ErrUnknownRole is returned for a role that's neither declared nor
	// in the database (422).
	ErrUnknownRole error = &statusError{"rbac: unknown role", http.StatusUnprocessableEntity}
	// ErrUnknownPermission is returned for a permission that isn't
	// declared, given to a role or a user (422).
	ErrUnknownPermission error = &statusError{"rbac: unknown permission", http.StatusUnprocessableEntity}
	// ErrInvalidRole is returned by [CreateRole] and [UpdateRole] for an
	// invalid name or title, or a role declared in code (422).
	ErrInvalidRole error = &statusError{"rbac: invalid role", http.StatusUnprocessableEntity}
	// ErrRoleExists is returned by [CreateRole] for a name taken (409).
	ErrRoleExists error = &statusError{"rbac: the role exists", http.StatusConflict}
	// ErrInvalidScope is returned when storing a grant in a scope not made
	// with [ScopeOf] (422).
	ErrInvalidScope error = &statusError{"rbac: invalid scope", http.StatusUnprocessableEntity}
)

type statusError struct {
	msg    string
	status int
}

func (e *statusError) Error() string   { return e.msg }
func (e *statusError) HTTPStatus() int { return e.status }

// cache holds the grants read during one operation.
type cache struct {
	mu     sync.Mutex
	gen    uint64 // counts changes: a read started before one isn't kept
	users  map[string]*grants
	custom map[string]*Role // nil: not in the database
}

type cacheKey struct{}

func cacheFrom(ctx context.Context) *cache {
	c, _ := ctx.Value(cacheKey{}).(*cache)
	return c
}
