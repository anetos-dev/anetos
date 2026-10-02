// SPDX-License-Identifier: Apache-2.0

package rbac_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"anetos.dev/anetos/auth"
	"anetos.dev/anetos/auth/rbac"
	"anetos.dev/anetos/web"
)

const (
	view   rbac.Permission = "posts.view"
	edit   rbac.Permission = "posts.edit"
	remove rbac.Permission = "posts.delete"
)

func TestNew(t *testing.T) {
	reg, err := rbac.New([]rbac.Permission{view, edit, remove},
		rbac.Role{Name: "admin", Super: true},
		rbac.Role{Name: "editor", Title: "Editor", Permissions: []rbac.Permission{view, edit}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Permissions(); len(got) != 3 || got[2] != remove {
		t.Errorf("Permissions() = %v", got)
	}
	if !reg.Declared(edit) || reg.Declared("posts.publish") {
		t.Error("Declared")
	}
	editor, ok := reg.Role("editor")
	if !ok || editor.DisplayName() != "Editor" || !editor.Allows(edit) || editor.Allows(remove) || editor.Custom {
		t.Errorf("Role(editor) = %+v, %v", editor, ok)
	}
	editor.Permissions[0] = remove // a copy
	if again, _ := reg.Role("editor"); again.Permissions[0] != view {
		t.Error("Role returns the registry's slice")
	}
	if roles := reg.Roles(); len(roles) != 2 || !roles[0].Allows(remove) || roles[0].DisplayName() != "admin" {
		t.Errorf("Roles() = %+v", roles)
	}

	for _, c := range []struct {
		name  string
		perms []rbac.Permission
		roles []rbac.Role
		want  string
	}{
		{"bad permission", []rbac.Permission{"Posts.View"}, nil, "invalid permission name"},
		{"long permission", []rbac.Permission{rbac.Permission(strings.Repeat("a", 101))}, nil, "invalid permission name"},
		{"twice", []rbac.Permission{view, view}, nil, "declared twice"},
		{"bad role", []rbac.Permission{view}, []rbac.Role{{Name: "Editor"}}, "invalid role name"},
		{"unknown permission", []rbac.Permission{view}, []rbac.Role{{Name: "editor", Permissions: []rbac.Permission{edit}}}, "unknown permission"},
		{"role twice", []rbac.Permission{view}, []rbac.Role{{Name: "a"}, {Name: "a"}}, `role "a" declared twice`},
		{"permission twice in a role", []rbac.Permission{view}, []rbac.Role{{Name: "a", Permissions: []rbac.Permission{view, view}}}, "twice"},
		{"super with permissions", []rbac.Permission{view}, []rbac.Role{{Name: "a", Super: true, Permissions: []rbac.Permission{view}}}, "leave its Permissions empty"},
	} {
		_, err := rbac.New(c.perms, c.roles...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

func TestScopes(t *testing.T) {
	s := rbac.ScopeOf("team", 42)
	if s != "team:42" || s.Kind() != "team" || s.ID() != "42" || s.String() != "team:42" {
		t.Errorf("ScopeOf = %q (%q, %q)", s, s.Kind(), s.ID())
	}
	if rbac.Global.String() != "global" || rbac.Global.Kind() != "" || rbac.Global.ID() != "" {
		t.Error("Global")
	}
	if id := rbac.ScopeOf("org", "a:b").ID(); id != "a:b" {
		t.Errorf("an ID with a colon: %q", id)
	}
	for _, kind := range []string{"", "Team", "team:x", "1team"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("ScopeOf(%q) didn't panic", kind)
				}
			}()
			rbac.ScopeOf(kind, 1)
		}()
	}
}

func TestErrors(t *testing.T) {
	for err, want := range map[error]int{
		rbac.ErrUnknownRole:       http.StatusUnprocessableEntity,
		rbac.ErrUnknownPermission: http.StatusUnprocessableEntity,
		rbac.ErrInvalidRole:       http.StatusUnprocessableEntity,
		rbac.ErrInvalidScope:      http.StatusUnprocessableEntity,
		rbac.ErrRoleExists:        http.StatusConflict,
	} {
		if got := web.StatusOf(err); got != want {
			t.Errorf("%v: status %d, want %d", err, got, want)
		}
	}
}

func TestNoRegistry(t *testing.T) {
	ctx := context.Background()
	if _, err := rbac.From(ctx); !errors.Is(err, rbac.ErrNoRegistry) {
		t.Errorf("From: %v", err)
	}
	if err := rbac.Authorize(ctx, view); !errors.Is(err, rbac.ErrNoRegistry) {
		t.Errorf("Authorize: %v", err)
	}
	if rbac.Can(ctx, view) || rbac.HasRole(ctx, "admin") {
		t.Error("allowed without a registry")
	}
	if err := rbac.Assign(ctx, "1", rbac.Global, "admin"); !errors.Is(err, rbac.ErrNoRegistry) {
		t.Errorf("Assign: %v", err)
	}
}

func TestChecksWithoutUser(t *testing.T) {
	reg, err := rbac.New([]rbac.Permission{view})
	if err != nil {
		t.Fatal(err)
	}
	ctx := rbac.WithRegistry(context.Background(), reg)
	// No auth middleware: a guest. No database is needed to say no.
	if err := rbac.Authorize(ctx, view); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("Authorize: %v", err)
	}
	if err := rbac.AuthorizeIn(ctx, rbac.ScopeOf("team", 1), "posts.typo"); err == nil || !strings.Contains(err.Error(), "isn't declared") || auth.IsDenied(err) {
		t.Errorf("an undeclared permission: %v", err)
	}
	if rbac.Can(ctx, view) || rbac.CanIn(ctx, rbac.ScopeOf("team", 1), view) || rbac.HasRoleIn(ctx, rbac.ScopeOf("team", 1), "x") {
		t.Error("a guest is allowed")
	}
	if _, err := rbac.Current(ctx); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Errorf("Current: %v", err)
	}

	// The middleware refuses guests, and requests without the path's
	// parameter.
	h := rbac.RequireIn(rbac.PathScope("team", "team"), view)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("let through")
	}))
	for path, want := range map[string]int{"/teams/1": http.StatusUnauthorized, "/teams/": http.StatusNotFound} {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		req.Header.Set("Accept", "application/json")
		if strings.HasSuffix(path, "1") {
			req.SetPathValue("team", "1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("GET %s: %d, want %d", path, w.Code, want)
		}
	}
	w := httptest.NewRecorder()
	rbac.Require(view)(http.NotFoundHandler()).ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Require: %d", w.Code)
	}
}

func TestSetupPanics(t *testing.T) {
	for name, fn := range map[string]func(){
		"no permissions":    func() { rbac.Require() },
		"invalid path kind": func() { rbac.PathScope("Team", "team") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			fn()
		}()
	}
}
