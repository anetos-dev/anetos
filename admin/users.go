// SPDX-License-Identifier: Apache-2.0

package admin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"anetos.dev/anetos"
	"anetos.dev/anetos/audit"
	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/db"
	"anetos.dev/anetos/validate"
	"anetos.dev/anetos/web"
)

// Accounts are what [Users] does to the app's users beyond editing them:
// disabling, verification, signing out, API tokens, roles, acting as
// them.
type Accounts[U auth.Authenticatable] struct {
	// Auth is the app's auth.Auth (required).
	Auth *auth.Auth[U]
	// DisabledAt is the column of when a user's account was disabled, a
	// *time.Time field ("disabled_at"); "" if accounts can't be disabled
	// here. auth.Users.Disabled must report it, so that package auth
	// refuses them.
	DisabledAt string
	// VerifiedAt is the column of when the user's email address was
	// verified, a *time.Time field ("email_verified_at"); "" if none.
	VerifiedAt string
	// SendVerification emails the user a link to verify their address
	// (make:auth's handlers.SendVerification); nil hides the button.
	SendVerification func(ctx context.Context, u U) error
	// SendPasswordReset emails the user a password-reset link
	// (make:auth's handlers.SendPasswordReset); nil hides the button.
	SendPasswordReset func(ctx context.Context, u U) error
	// NoTokens says the app has no API tokens (auth.Migrations isn't
	// run): their list and buttons are left out.
	NoTokens bool
}

// AssignRoles is the permission to give users roles and take them away,
// in the admin; one gives only roles one could (rbac.AuthorizeRole).
const AssignRoles rbac.Permission = "admin.roles.assign"

// Users adds the app's users as a resource, r, as [Add] does, with their
// accounts managed on their pages (acc): disabling and enabling,
// verifying addresses, emailing verification and reset links, signing
// out everywhere, API tokens, roles and permissions, and acting as them
// ("admin.<name>.impersonate"). Only those who have every permission a
// user has may change them (rbac.AuthorizeOver), and no one disables,
// deletes, acts as or changes the roles of themselves. What it does is
// recorded in the audit log, if the app keeps one. Deleting a user for
// good also removes their roles and API tokens.
//
// U is the app's user type, a pointer to the model T: *models.User.
func Users[T any, U interface {
	*T
	auth.Authenticatable
}, F any](p *Panel, r Resource[T, F], acc Accounts[U]) error {
	if acc.Auth == nil {
		return errors.New("admin: Users needs Accounts.Auth, the app's auth.Auth")
	}
	if r.Name == "" {
		r.Name = "users"
	}
	fields, err := columnFields(reflect.TypeFor[T]())
	if err != nil {
		return err
	}
	timeField := func(what, col string) ([]int, error) {
		if col == "" {
			return nil, nil
		}
		idx, ok := fields.index[col]
		if !ok {
			return nil, fmt.Errorf("admin: Users: %s %q is not a column of the users", what, col)
		}
		if f := reflect.TypeFor[T]().FieldByIndex(idx); f.Type != reflect.TypeFor[*time.Time]() {
			return nil, fmt.Errorf("admin: Users: %s %q must be a *time.Time field, not %s", what, col, f.Type)
		}
		return idx, nil
	}
	u := &userAdmin[T, U, F]{acc: acc}
	if u.disabled, err = timeField("DisabledAt", acc.DisabledAt); err != nil {
		return err
	}
	if u.verified, err = timeField("VerifiedAt", acc.VerifiedAt); err != nil {
		return err
	}
	if u.disabled != nil {
		// Package auth must refuse them too, or disabling does nothing.
		var row T
		now := time.Now()
		reflect.ValueOf(&row).Elem().FieldByIndex(u.disabled).Set(reflect.ValueOf(&now))
		if !acc.Auth.Disabled(U(&row)) {
			return fmt.Errorf("admin: Users: auth.Users.Disabled doesn't report users whose %s is set: add it, so package auth refuses them", acc.DisabledAt)
		}
	}
	if p.reg != nil {
		u.impersonate = rbac.Permission("admin." + r.Name + ".impersonate")
		if err := p.reg.Declare(u.impersonate, AssignRoles); err != nil {
			return err
		}
	}
	r.Filters = append(slices.Clip(r.Filters), u.filters()...)
	r.Actions = append(slices.Clip(r.Actions), u.actions(p)...)
	res, err := build(p, r)
	if err != nil {
		return err
	}
	u.res = res
	p.usersName = r.Name
	p.userLabels = u.labels
	res.hooks = hooks[T]{guard: u.guard, sections: u.sections, routes: u.routes, removed: u.removed}
	return p.add(res)
}

// userAdmin is the users resource's account management.
type userAdmin[T any, U interface {
	*T
	auth.Authenticatable
}, F any] struct {
	acc                Accounts[U]
	res                *res[T, F]
	disabled, verified []int // the time fields' indexes; nil if none
	impersonate        rbac.Permission
}

// timeOf returns a time field of row, nil if unset or there is no field.
func timeOf[T any](row *T, idx []int) *time.Time {
	if idx == nil {
		return nil
	}
	return reflect.ValueOf(row).Elem().FieldByIndex(idx).Interface().(*time.Time) //nolint:forcetypeassert // checked by Users
}

func setTime[T any](row *T, idx []int, t *time.Time) {
	reflect.ValueOf(row).Elem().FieldByIndex(idx).Set(reflect.ValueOf(t))
}

func (u *userAdmin[T, U, F]) filters() []Filter[T] {
	var out []Filter[T]
	if u.disabled != nil {
		col := db.C(u.acc.DisabledAt)
		out = append(out, Filter[T]{Name: "status", Title: "Status",
			Choices: []Choice{{"active", "Active"}, {"disabled", "Disabled"}},
			Apply: func(q *db.Q[T], v string) *db.Q[T] {
				if v == "disabled" {
					return q.Where(col.NotNull())
				}
				return q.Where(col.IsNull())
			}})
	}
	if u.verified != nil {
		col := db.C(u.acc.VerifiedAt)
		out = append(out, Filter[T]{Name: "email", Title: "Email",
			Choices: []Choice{{"verified", "Verified"}, {"unverified", "Not verified"}},
			Apply: func(q *db.Q[T], v string) *db.Q[T] {
				if v == "unverified" {
					return q.Where(col.IsNull())
				}
				return q.Where(col.NotNull())
			}})
	}
	return out
}

// subject is a user's subject in the audit log.
func (u *userAdmin[T, U, F]) subject(row *T) audit.Subject {
	return audit.Subject{Type: u.res.table, ID: U(row).AuthID()}
}

// record adds an event to the audit log, if the app keeps one.
func record(ctx context.Context, action string, subject audit.Subject, props map[string]any) error {
	if !audit.Enabled(ctx) {
		return nil
	}
	return audit.Record(ctx, action, subject, props)
}

// setAndRecord sets a time column of row (only it: a password changed
// meanwhile stays), and records action.
func (u *userAdmin[T, U, F]) setAndRecord(ctx context.Context, row *T, idx []int, col string, t *time.Time, action string) error {
	_, key, err := db.KeyOf(row)
	if err != nil {
		return err
	}
	return db.Tx(ctx, func(ctx context.Context) error {
		if _, err := db.Query[T](ctx).WhereKeys(key).Update(db.C(col).Set(t)); err != nil {
			return err
		}
		setTime(row, idx, t)
		return record(ctx, action, u.subject(row), nil)
	})
}

func (u *userAdmin[T, U, F]) actions(p *Panel) []Action[T] {
	var out []Action[T]
	now := func(ctx context.Context) *time.Time {
		t := anetos.Now(ctx).UTC()
		return &t
	}
	if u.disabled != nil {
		out = append(out,
			Action[T]{Name: "disable", Title: "Disable", Danger: true, Done: "Account disabled.",
				Confirm: "Disable this account? They'll be signed out, and can't sign in or use their API tokens.",
				When:    func(row T) bool { return timeOf(&row, u.disabled) == nil },
				Run: func(ctx context.Context, row *T) error {
					return u.setAndRecord(ctx, row, u.disabled, u.acc.DisabledAt, now(ctx), "user.disabled")
				}},
			Action[T]{Name: "enable", Title: "Enable", Done: "Account enabled.",
				When: func(row T) bool { return timeOf(&row, u.disabled) != nil },
				Run: func(ctx context.Context, row *T) error {
					return u.setAndRecord(ctx, row, u.disabled, u.acc.DisabledAt, nil, "user.enabled")
				}})
	}
	if u.verified != nil {
		unverified := func(row T) bool { return timeOf(&row, u.verified) == nil }
		out = append(out, Action[T]{Name: "verify", Title: "Mark email verified", Done: "Email address marked as verified.",
			When: unverified,
			Run: func(ctx context.Context, row *T) error {
				return u.setAndRecord(ctx, row, u.verified, u.acc.VerifiedAt, now(ctx), "user.verified")
			}})
		if send := u.acc.SendVerification; send != nil {
			out = append(out, Action[T]{Name: "send-verification", Title: "Send verification email", Done: "Verification email sent.",
				When: unverified,
				Run: func(ctx context.Context, row *T) error {
					if err := send(ctx, U(row)); err != nil {
						return err
					}
					return record(ctx, "user.verification_sent", u.subject(row), nil)
				}})
		}
	} else if send := u.acc.SendVerification; send != nil {
		out = append(out, Action[T]{Name: "send-verification", Title: "Send verification email", Done: "Verification email sent.",
			Run: func(ctx context.Context, row *T) error {
				if err := send(ctx, U(row)); err != nil {
					return err
				}
				return record(ctx, "user.verification_sent", u.subject(row), nil)
			}})
	}
	if send := u.acc.SendPasswordReset; send != nil {
		out = append(out, Action[T]{Name: "send-password-reset", Title: "Send password reset", Done: "Password reset email sent.",
			Confirm: "Email this user a link to choose a new password?",
			Run: func(ctx context.Context, row *T) error {
				if err := send(ctx, U(row)); err != nil {
					return err
				}
				return record(ctx, "user.password_reset_sent", u.subject(row), nil)
			}})
	}
	if u.acc.Auth.CanSignOutEverywhere() {
		out = append(out, Action[T]{Name: "sign-out", Title: "Sign out everywhere", Done: "Signed out of every browser and device.",
			Confirm: "Sign this user out of every browser and device? Their API tokens keep working.",
			Run: func(ctx context.Context, row *T) error {
				if err := u.acc.Auth.SignOutEverywhere(ctx, U(row)); err != nil {
					return err
				}
				return record(ctx, "user.signed_out_everywhere", u.subject(row), nil)
			}})
	}
	// Not at a host of its own: the app's pages are on another host, whose
	// session is another (cookies are the host's).
	if u.impersonate != "" && p.cfg.Host == "" {
		out = append(out, Action[T]{Name: "impersonate", Title: "Act as user", Permission: string(u.impersonate),
			Confirm: "Act as this user? You'll see the app as they do, until you stop. It is logged.",
			When:    func(row T) bool { return u.disabled == nil || timeOf(&row, u.disabled) == nil },
			Run:     u.startImpersonating,
			Done:    "You're acting as this user.",
			then:    func(*web.Ctx, T) string { return u.acc.Auth.Config().HomeURL }})
	}
	return out
}

// Session keys of acting as a user, for the banner and for stopping.
const (
	keyActingAs   = "_admin.acting_as"   // the user's name
	keyActingBy   = "_admin.acting_by"   // the admin's name
	keyActingStop = "_admin.acting_stop" // where to stop it
	keyActingBack = "_admin.acting_back" // the user's page in the admin
	keyActingSubj = "_admin.acting_subj" // the user's subject in the log: type, NUL, ID
)

func (u *userAdmin[T, U, F]) startImpersonating(ctx context.Context, row *T) error {
	c, ok := ctx.(*web.Ctx)
	if !ok {
		return errors.New("admin: acting as a user needs the request")
	}
	by := u.res.p.userName(c)
	subject := u.subject(row)
	// The event and the change of user, or neither.
	err := db.Tx(ctx, func(ctx context.Context) error {
		if err := record(ctx, "user.impersonated", subject, nil); err != nil {
			return err
		}
		return u.acc.Auth.Impersonate(ctx, U(row))
	})
	if errors.Is(err, auth.ErrDisabled) {
		return validate.Fail("account", "This account is disabled.")
	} else if err != nil {
		return err
	}
	s := c.Session()
	s.Put(keyActingAs, u.res.label(*row))
	s.Put(keyActingBy, by)
	s.Put(keyActingStop, u.res.p.base+stopPath)
	s.Put(keyActingBack, u.res.url("/"+u.res.keyText(*row)))
	s.Put(keyActingSubj, subject.Type+"\x00"+subject.ID)
	return nil
}

// selfOps are what no one does to themselves here.
var selfOps = map[string]string{
	"delete":             "You can't delete your own account here.",
	"bulk:delete":        "You can't delete your own account here.",
	"action:disable":     "You can't disable your own account.",
	"action:impersonate": "You can't act as yourself.",
	"roles":              "You can't change your own roles.",
}

// guard lets the signed-in user change only users they have every
// permission of, and not do some things to themselves.
func (u *userAdmin[T, U, F]) guard(c *web.Ctx, row T, op string) error {
	me, err := auth.CurrentID(c)
	if err != nil {
		return err
	}
	if op == "action:impersonate" {
		if _, acting := auth.Impersonator(c); acting {
			return validate.Fail("account", "You're acting as someone already: stop first.")
		}
	}
	id := U(&row).AuthID()
	if id == me {
		if msg, ok := selfOps[op]; ok {
			return validate.Fail("account", msg)
		}
		return nil
	}
	if u.res.p.reg == nil {
		return nil
	}
	if err := rbac.AuthorizeOver(c, id); errors.Is(err, auth.ErrForbidden) {
		return validate.Fail("account", "You may not manage "+u.res.label(row)+": they have permissions you don't.")
	} else if err != nil {
		return err
	}
	return nil
}

// labels names users by their IDs.
func (u *userAdmin[T, U, F]) labels(ctx context.Context, ids []string) (map[string]string, error) {
	keys := make([]any, 0, len(ids))
	for _, id := range ids {
		if k, ok := u.res.parseKey(id); ok {
			keys = append(keys, k)
		}
	}
	out := map[string]string{}
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := u.res.query(ctx).WhereKeys(keys...).Get()
	if err != nil {
		return nil, err
	}
	for i := range rows {
		out[U(&rows[i]).AuthID()] = u.res.label(rows[i])
	}
	return out, nil
}

// removed takes a user deleted for good out of the roles and API tokens.
func (u *userAdmin[T, U, F]) removed(ctx context.Context, row T) error {
	id := U(&row).AuthID()
	if u.res.p.reg != nil {
		if err := rbac.RemoveUser(ctx, id); err != nil {
			return err
		}
	}
	if !u.acc.NoTokens {
		return u.acc.Auth.RevokeAllTokens(ctx, U(&row))
	}
	return nil
}

// accountView is the account section.
type accountView struct {
	Status   string
	Disabled bool
	Email    string
	Verified bool
}

// grantView is a role or permission of the user.
type grantView struct {
	Scope      string // "" for everywhere
	Role       string
	Title      string
	Permission string
	RemoveURL  string
}

// rolesView is the roles section.
type rolesView struct {
	CSRF      string
	Grants    []grantView
	Roles     []Choice // the roles the signed-in user may give
	AssignURL string
}

// tokenView is an API token of the user.
type tokenView struct {
	Name      string
	Abilities string
	LastUsed  string
	Expires   string
	Created   string
	RevokeURL string
}

// tokensView is the API tokens section.
type tokensView struct {
	CSRF      string
	Tokens    []tokenView
	RevokeAll string
}

func (u *userAdmin[T, U, F]) sections(c *web.Ctx, row T) ([]section, error) {
	var out []section
	p := u.res.p
	if u.disabled != nil || u.verified != nil {
		var av accountView
		if u.disabled != nil {
			av.Status = "Active"
			if t := timeOf(&row, u.disabled); t != nil {
				av.Status, av.Disabled = "Disabled on "+timeText(c, *t), true
			}
		}
		if u.verified != nil {
			av.Email = "Not verified"
			if t := timeOf(&row, u.verified); t != nil {
				av.Email, av.Verified = "Verified on "+timeText(c, *t), true
			}
		}
		body, err := part("account", av)
		if err != nil {
			return nil, err
		}
		out = append(out, section{"Account", body})
	}
	base := u.res.url("/" + u.res.keyText(row))
	if p.reg != nil {
		rv, err := u.rolesOf(c, row, base)
		if err != nil {
			return nil, err
		}
		body, err := part("roles", rv)
		if err != nil {
			return nil, err
		}
		out = append(out, section{"Roles and permissions", body})
	}
	if !u.acc.NoTokens {
		tokens, err := u.acc.Auth.Tokens(c, U(&row))
		if err != nil {
			return nil, err
		}
		tv := tokensView{CSRF: csrfToken(c)}
		mayRevoke := u.res.can(c, "update") && u.res.allowed(c, row, "tokens")
		for _, t := range tokens {
			v := tokenView{Name: t.Name, Abilities: strings.Join(t.Abilities, ", "), LastUsed: "Never", Expires: "Never",
				Created: timeText(c, t.CreatedAt)}
			if t.LastUsedAt != nil {
				v.LastUsed = timeText(c, *t.LastUsedAt)
			}
			if t.ExpiresAt != nil {
				v.Expires = timeText(c, *t.ExpiresAt)
			}
			if mayRevoke {
				v.RevokeURL = base + "/tokens/" + strconv.FormatInt(t.ID, 10) + "/revoke"
			}
			tv.Tokens = append(tv.Tokens, v)
		}
		if mayRevoke && len(tokens) > 1 {
			tv.RevokeAll = base + "/tokens/revoke-all"
		}
		body, err := part("tokens", tv)
		if err != nil {
			return nil, err
		}
		out = append(out, section{"API tokens", body})
	}
	return out, nil
}

// rolesOf returns the user's roles and permissions, and what the signed-in
// user may change of them.
func (u *userAdmin[T, U, F]) rolesOf(c *web.Ctx, row T, base string) (rolesView, error) {
	rv := rolesView{CSRF: csrfToken(c)}
	given, err := rbac.GivenTo(c, U(&row).AuthID())
	if err != nil {
		return rv, err
	}
	roles, err := rbac.Roles(c)
	if err != nil {
		return rv, err
	}
	titles := map[string]string{}
	for _, r := range roles {
		titles[r.Name] = r.DisplayName()
	}
	may := rbac.Can(c, AssignRoles) && u.res.allowed(c, row, "roles")
	for _, g := range given {
		v := grantView{Scope: string(g.Scope), Role: g.Role, Permission: string(g.Permission)}
		if g.Role != "" {
			v.Title = titles[g.Role]
			if v.Title == "" {
				v.Title = g.Role + " (unknown: allows nothing)"
			}
		}
		if may {
			if g.Role != "" {
				v.RemoveURL = base + "/roles/remove"
			} else {
				v.RemoveURL = base + "/permissions/revoke"
			}
		}
		rv.Grants = append(rv.Grants, v)
	}
	if may {
		for _, r := range roles {
			if rbac.AuthorizeRole(c, rbac.Global, r.Name) == nil {
				rv.Roles = append(rv.Roles, Choice{r.Name, r.DisplayName()})
			}
		}
		if len(rv.Roles) > 0 {
			rv.AssignURL = base + "/roles"
		}
	}
	return rv, nil
}

func (u *userAdmin[T, U, F]) routes(g *web.Router) {
	n := "admin." + u.res.Name + "."
	if u.res.p.reg != nil {
		assign := g.With(rbac.Require(u.res.in.perm("view"), AssignRoles))
		assign.Post("/{id}/roles", u.assignRole).Name(n + "roles.assign")
		assign.Post("/{id}/roles/remove", u.removeRole).Name(n + "roles.remove")
		assign.Post("/{id}/permissions/revoke", u.revokePermission).Name(n + "permissions.revoke")
	}
	if !u.acc.NoTokens {
		update := g.With(rbac.Require(u.res.in.perm("update")))
		update.Post("/{id}/tokens/{token}/revoke", u.revokeToken).Name(n + "tokens.revoke")
		update.Post("/{id}/tokens/revoke-all", u.revokeAllTokens).Name(n + "tokens.revoke-all")
	}
}

// target loads the user of a request and checks op on them; to is their
// page.
func (u *userAdmin[T, U, F]) target(c *web.Ctx, op string) (row T, to string, err error) {
	if row, err = u.res.find(c, false); err != nil {
		return row, "", err
	}
	to = u.res.url("/" + u.res.keyText(row))
	return row, to, u.res.check(c, row, op)
}

// scopeOf reads the scope of a form: "" is everywhere.
func scopeOf(c *web.Ctx) rbac.Scope {
	return rbac.Scope(strings.TrimSpace(c.Request().PostFormValue("scope")))
}

func (u *userAdmin[T, U, F]) assignRole(c *web.Ctx) error {
	row, to, err := u.target(c, "roles")
	if err != nil {
		return refused(c, err, to)
	}
	role, scope := c.Request().PostFormValue("role"), scopeOf(c)
	if err := rbac.AuthorizeRole(c, scope, role); errors.Is(err, auth.ErrForbidden) {
		return failed(c, "You may not give the role "+role+": it has permissions you don't.", to)
	} else if err != nil {
		return roleError(c, err, to)
	}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := rbac.Assign(ctx, U(&row).AuthID(), scope, role); err != nil {
			return err
		}
		return record(ctx, "rbac.role_assigned", u.subject(&row), map[string]any{"role": role, "scope": string(scope)})
	})
	if err != nil {
		return roleError(c, err, to)
	}
	return done(c, "Role "+role+" given"+where(scope)+".", to)
}

func (u *userAdmin[T, U, F]) removeRole(c *web.Ctx) error {
	row, to, err := u.target(c, "roles")
	if err != nil {
		return refused(c, err, to)
	}
	role, scope := c.Request().PostFormValue("role"), scopeOf(c)
	// Exactly a role they have (some databases compare names without
	// case or trailing spaces).
	if ok, err := has(c, U(&row).AuthID(), rbac.Given{Scope: scope, Role: role}); err != nil {
		return err
	} else if !ok {
		return failed(c, u.res.label(row)+" doesn't have the role "+role+where(scope)+".", to)
	}
	// Taking a role away needs being able to give it; a role that no
	// longer exists allows nothing, and anyone here may remove it.
	if err := rbac.AuthorizeRole(c, scope, role); errors.Is(err, auth.ErrForbidden) {
		return failed(c, "You may not take away the role "+role+": it has permissions you don't.", to)
	} else if err != nil && !errors.Is(err, rbac.ErrUnknownRole) {
		return roleError(c, err, to)
	}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := rbac.Unassign(ctx, U(&row).AuthID(), scope, role); err != nil {
			return err
		}
		return record(ctx, "rbac.role_removed", u.subject(&row), map[string]any{"role": role, "scope": string(scope)})
	})
	if err != nil {
		return roleError(c, err, to)
	}
	return done(c, "Role "+role+" taken away"+where(scope)+".", to)
}

func (u *userAdmin[T, U, F]) revokePermission(c *web.Ctx) error {
	row, to, err := u.target(c, "roles")
	if err != nil {
		return refused(c, err, to)
	}
	perm, scope := rbac.Permission(c.Request().PostFormValue("permission")), scopeOf(c)
	if ok, err := has(c, U(&row).AuthID(), rbac.Given{Scope: scope, Permission: perm}); err != nil {
		return err
	} else if !ok {
		return failed(c, u.res.label(row)+" doesn't have the permission "+string(perm)+where(scope)+".", to)
	}
	if u.res.p.reg.Declared(perm) && !rbac.CanIn(c, scope, perm) {
		return failed(c, "You may not take away "+string(perm)+": you don't have it.", to)
	}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := rbac.Revoke(ctx, U(&row).AuthID(), scope, perm); err != nil {
			return err
		}
		return record(ctx, "rbac.permission_revoked", u.subject(&row), map[string]any{"permission": string(perm), "scope": string(scope)})
	})
	if err != nil {
		return roleError(c, err, to)
	}
	return done(c, "Permission "+string(perm)+" taken away"+where(scope)+".", to)
}

// has reports whether a user was given exactly g.
func has(ctx context.Context, userID string, g rbac.Given) (bool, error) {
	given, err := rbac.GivenTo(ctx, userID)
	return slices.Contains(given, g), err
}

// roleError shows the errors of package rbac that are the user's (an
// unknown role, a bad scope) on to.
func roleError(c *web.Ctx, err error, to string) error {
	var sc interface{ HTTPStatus() int }
	if errors.As(err, &sc) && sc.HTTPStatus() < http.StatusInternalServerError {
		return failed(c, strings.TrimPrefix(err.Error(), "rbac: ")+".", to)
	}
	return err
}

// where says where a role applies, for messages.
func where(s rbac.Scope) string {
	if s == rbac.Global {
		return ""
	}
	return " in " + string(s)
}

func (u *userAdmin[T, U, F]) revokeToken(c *web.Ctx) error {
	row, to, err := u.target(c, "tokens")
	if err != nil {
		return refused(c, err, to)
	}
	id, err := strconv.ParseInt(c.Param("token"), 10, 64)
	if err != nil {
		return web.Error(http.StatusNotFound, "")
	}
	tokens, err := u.acc.Auth.Tokens(c, U(&row))
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(tokens, func(t auth.Token) bool { return t.ID == id }) {
		return web.Error(http.StatusNotFound, "")
	}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := u.acc.Auth.RevokeToken(ctx, U(&row), id); err != nil {
			return err
		}
		return record(ctx, "user.token_revoked", u.subject(&row), map[string]any{"token": id})
	})
	if err != nil {
		return err
	}
	return done(c, "API token revoked.", to)
}

func (u *userAdmin[T, U, F]) revokeAllTokens(c *web.Ctx) error {
	row, to, err := u.target(c, "tokens")
	if err != nil {
		return refused(c, err, to)
	}
	err = db.Tx(c, func(ctx context.Context) error {
		if err := u.acc.Auth.RevokeAllTokens(ctx, U(&row)); err != nil {
			return err
		}
		return record(ctx, "user.tokens_revoked", u.subject(&row), nil)
	})
	if err != nil {
		return err
	}
	return done(c, "Every API token revoked.", to)
}

// stopPath is where acting as a user stops, under the admin's path.
const stopPath = "/impersonation/stop"

// stopImpersonating ends acting as a user, for the Panel's stop route.
func stopImpersonating[U auth.Authenticatable](a *auth.Auth[U], p *Panel) web.HandlerFunc {
	return func(c *web.Ctx) error {
		s := c.Session()
		back, subj := s.String(keyActingBack), s.String(keyActingSubj)
		_, err := a.StopImpersonating(c)
		for _, k := range []string{keyActingAs, keyActingBy, keyActingStop, keyActingBack, keyActingSubj} {
			s.Delete(k)
		}
		switch {
		case errors.Is(err, auth.ErrNotImpersonating):
			return c.Redirect(http.StatusSeeOther, p.URL())
		case err != nil:
			// The admin can't sign in any more: signed out.
			return c.Redirect(http.StatusSeeOther, a.Config().LoginURL)
		}
		if typ, id, ok := strings.Cut(subj, "\x00"); ok {
			if err := record(c, "user.impersonation_ended", audit.Subject{Type: typ, ID: id}, nil); err != nil {
				return err
			}
		}
		if back == "" || !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
			back = p.URL()
		}
		return done(c, "You're yourself again.", back)
	}
}

// timeText formats a time as lists show it, in the app's time zone.
func timeText(ctx context.Context, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(anetos.Location(ctx)).Format("2006-01-02 15:04")
}

// csrfToken returns the session's CSRF token.
func csrfToken(c *web.Ctx) string {
	if s := c.Session(); s != nil {
		return s.Token()
	}
	return ""
}
