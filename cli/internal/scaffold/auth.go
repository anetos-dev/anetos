// SPDX-License-Identifier: Apache-2.0

package scaffold

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

// authFiles are the files make:auth writes: template → path in the
// project. The migration's path is added with its timestamp.
var authFiles = [][2]string{
	{"user.go.tmpl", "app/models/user.go"},
	{"handlers.go.tmpl", "app/handlers/auth.go"},
	{"mailers.go.tmpl", "app/mailers/auth.go"},
	{"pages.templ.tmpl", "views/auth.templ"},
	{"mail.templ.tmpl", "views/auth_mail.templ"},
	{"routes.go.tmpl", "routes/auth.go"},
	{"setup.go.tmpl", "auth.go"},
	{"test.go.tmpl", "auth_test.go"},
}

// authCall is what make:auth adds to setup in main.go, after the routes.
const authCall = `	// Accounts (anetos make:auth): registration, login, email
	// verification, password reset and API tokens.
	if _, err := setupAuth(app, srv.Router(), sessions); err != nil {
		return nil, err
	}
`

// routesCall is the line of a anetos new project's main.go that
// make:auth adds its call after.
const routesCall = "routes.Register(srv.Router(), sessions)"

// AuthResult is what [MakeAuth] did.
type AuthResult struct {
	// Created are the files written, relative to the project.
	Created []string
	// Wired says main.go now calls setupAuth; when false, the app must
	// call it itself.
	Wired bool
}

// MakeAuth writes the account scaffolding into the project at root:
// the User model, the handlers, the emails, the pages, the routes, the
// users table's migration, setupAuth and its tests. It writes nothing if
// one of the files exists already, or a name they declare is taken in
// its package; if a write fails, it removes what it wrote. When setup in
// main.go has the routes.Register call of a anetos new project, it adds
// the call to setupAuth after it.
func MakeAuth(root string, now time.Time) (AuthResult, error) {
	var res AuthResult
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return res, err
	}
	mod := modfile.ModulePath(b)
	if mod == "" {
		return res, errors.New("go.mod has no module line")
	}
	for _, dir := range []string{"app/models", "app/handlers", "views", "routes", "database/migrations"} {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			return res, fmt.Errorf("no %s directory: make:auth adds to a project made with anetos new", dir)
		}
	}
	files := append([][2]string(nil), authFiles...)
	if m, ok := existingMigration(filepath.Join(root, "database", "migrations"), "_create_users_table.go"); ok {
		return res, fmt.Errorf("database/migrations/%s exists: the app has users already", m)
	}
	ts := now.UTC().Truncate(time.Second)
	if last, ok := latestMigration(filepath.Join(root, "database", "migrations")); ok && !ts.After(last) {
		ts = last.Add(time.Second)
	}
	id := ts.Format(idLayout) + "_create_users_table"
	files = append(files, [2]string{"migration.go.tmpl", "database/migrations/" + id + ".go"})
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f[1]))); err == nil {
			return res, fmt.Errorf("%s exists: make:auth writes nothing over the app's files", f[1])
		}
	}

	// The names the files declare mustn't be taken in their packages.
	for dir, names := range authNames {
		taken, err := declared(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			return res, err
		}
		for _, n := range names {
			if taken[n] {
				return res, fmt.Errorf("%s already declares %s, which make:auth would add: rename yours, or add accounts by hand", dirLabel(dir), n)
			}
		}
	}

	data := struct{ Module, ID string }{mod, id}
	out := make([][]byte, len(files))
	for i, f := range files {
		if out[i], err = render("templates/auth/"+f[0], "", data); err != nil {
			return res, err
		}
	}
	for i, f := range files {
		if err := writeNew(filepath.Join(root, filepath.FromSlash(f[1])), out[i], 0o644); err != nil {
			// Leave the project as it was.
			for _, c := range res.Created {
				_ = os.Remove(filepath.Join(root, filepath.FromSlash(c)))
			}
			_ = os.Remove(filepath.Join(root, "app", "mailers")) // if make:auth created it, it's empty
			return AuthResult{}, fmt.Errorf("%w (the files written were removed)", err)
		}
		res.Created = append(res.Created, f[1])
	}

	wired, err := wireAuth(filepath.Join(root, "main.go"))
	res.Wired = wired
	return res, err
}

// authNames are the top-level names the generated files declare, by
// package directory ("" is the root, package main).
var authNames = map[string][]string{
	"app/models":   {"User", "Users", "UserCols"},
	"app/handlers": {"Accounts", "RegisterInput", "LoginInput", "ForgotInput", "ResetInput", "TokenQuery", "NewTokenInput", "TokenID", "emailTaken", "cleanName"},
	"app/mailers":  {"VerifyEmail", "ResetPassword"},
	"views":        {"Register", "Login", "ForgotPassword", "ResetPassword", "Dashboard", "authError", "VerifyEmailMail", "ResetPasswordMail", "authMail"},
	"routes":       {"Auth"},
	"":             {"setupAuth", "authRegister", "authLink", "TestRegisterAndVerify", "TestResendVerification", "TestRegisterValidation", "TestLoginAndLogout", "TestLoginReturnsToTheRequestedPage", "TestPasswordReset", "TestResetSignsOutAndRevokesTokens", "TestAPIToken"},
}

func dirLabel(dir string) string {
	if dir == "" {
		return "package main"
	}
	return dir
}

// declared returns the top-level names the Go files of dir declare
// (tests included; generated templ code included). A missing directory
// declares none.
func declared(dir string) (map[string]bool, error) {
	names := map[string]bool{}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					names[d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch spec := spec.(type) {
					case *ast.TypeSpec:
						names[spec.Name.Name] = true
					case *ast.ValueSpec:
						for _, n := range spec.Names {
							names[n.Name] = true
						}
					}
				}
			}
		}
	}
	// templ files declare components; their generated code may not exist
	// yet.
	templs, err := filepath.Glob(filepath.Join(dir, "*.templ"))
	if err != nil {
		return nil, err
	}
	for _, file := range templs {
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for _, m := range templDecl.FindAllSubmatch(b, -1) {
			names[string(m[1])] = true
		}
	}
	return names, nil
}

var templDecl = regexp.MustCompile(`(?m)^templ\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

// wireAuth adds the setupAuth call to setup in main.go, after its
// routes.Register(…) statement, and reports whether setup calls
// setupAuth. It leaves main.go alone, reporting false, when it has no
// such setup.
func wireAuth(main string) (bool, error) {
	src, err := os.ReadFile(main)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, main, src, parser.SkipObjectResolution)
	if err != nil {
		return false, nil //nolint:nilerr // not ours to fix: the app wires it
	}
	var setup *ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "setup" && fn.Body != nil {
			setup = fn
		}
	}
	if setup == nil {
		return false, nil
	}
	called := false
	ast.Inspect(setup.Body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "setupAuth" {
				called = true
			}
		}
		return true
	})
	if called {
		return true, nil
	}
	// The routes.Register(…) statement, directly in setup's body.
	var after ast.Stmt
	for _, st := range setup.Body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Register" {
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "routes" && after == nil {
				after = st
			}
		}
	}
	params := setup.Type.Params.List
	if after == nil || setup.Type.Results == nil || setup.Type.Results.NumFields() != 2 ||
		len(params) == 0 || len(params[0].Names) == 0 || params[0].Names[0].Name != "app" {
		return false, nil // the call below needs app, srv, sessions and a (value, error) return
	}
	start, end := fset.Position(after.Pos()).Offset, fset.Position(after.End()).Offset
	if string(src[start:end]) != routesCall {
		return false, nil
	}
	nl := bytes.IndexByte(src[end:], '\n')
	if nl < 0 {
		return false, nil
	}
	at := end + nl + 1
	patched := append(append(append([]byte(nil), src[:at]...), authCall...), src[at:]...)
	return true, os.WriteFile(main, patched, 0o644)
}

// existingMigration returns the name of a migration file in dir ending in
// suffix.
func existingMigration(dir, suffix string) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), suffix) {
			return e.Name(), true
		}
	}
	return "", false
}
