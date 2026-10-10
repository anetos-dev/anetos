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
	{"settings.go.tmpl", "app/handlers/settings.go"},
	{"mailers.go.tmpl", "app/mailers/auth.go"},
	{"pages.templ.tmpl", "views/auth.templ"},
	{"settings.templ.tmpl", "views/settings.templ"},
	{"mail.templ.tmpl", "views/auth_mail.templ"},
	{"routes.go.tmpl", "routes/auth.go"},
	{"setup.go.tmpl", "auth.go"},
	{"test.go.tmpl", "auth_test.go"},
	{"locale.yaml.tmpl", "locales/en/auth.yaml"},
	{"factory.go.tmpl", "database/factories/users.go"},
}

// authAPIFiles are the files make:auth writes in an API project
// (IsAPI): template → path in the project.
var authAPIFiles = [][2]string{
	{"api/user.go.tmpl", "app/models/user.go"},
	{"api/handlers.go.tmpl", "app/handlers/auth.go"},
	{"api/mailers.go.tmpl", "app/mailers/auth.go"},
	{"api/mail.html.tmpl", "app/mailers/auth.html"},
	{"api/routes.go.tmpl", "routes/auth.go"},
	{"api/setup.go.tmpl", "auth.go"},
	{"api/test.go.tmpl", "auth_test.go"},
	{"api/locale.yaml.tmpl", "locales/en/auth.yaml"},
	{"factory.go.tmpl", "database/factories/users.go"},
}

// authCall is what make:auth adds to setup in main.go, after the routes.
const authCall = `	// Accounts (anetos make:auth): registration, login with a password,
	// Google or GitHub, two-factor authentication, account settings, email
	// verification, password reset and API tokens.
	if _, err := setupAuth(app, srv.Router(), sessions); err != nil {
		return nil, err
	}
`

// routesCall is the line of an anetos new project's main.go that
// make:auth adds its call after.
const routesCall = "routes.Register(srv.Router(), sessions)"

// authCallAPI and routesCallAPI are authCall and routesCall in an API
// project.
const (
	authCallAPI = `	// Accounts (anetos make:auth): registration, login with API tokens
	// (and two-factor codes), email verification, password reset and
	// change, token management.
	if _, err := setupAuth(app, srv.Router()); err != nil {
		return nil, err
	}
`
	routesCallAPI = "routes.Register(srv.Router())"
)

// AuthResult is what [MakeAuth] did.
type AuthResult struct {
	// Created are the files written, relative to the project.
	Created []string
	// Wired says main.go now calls setupAuth; when false, the app must
	// call it itself.
	Wired bool
	// Menu says the layout (views/layout.templ) now shows AccountMenu in
	// its header; when false, the app adds it where it wants it. Always
	// false in an API project, which has no layout.
	Menu bool
	// Env are the settings files (.env, .env.example,
	// deploy/production.env.example) the SOCIAL_* settings (in an API
	// project, AUTH_CLIENT_URL) were added to.
	Env []string
	// API says the project is an API project ([IsAPI]): the accounts are
	// JSON endpoints logging in with API tokens.
	API bool
	// Kit is the design kit whose views/ui MakeAuth wrote first, in a
	// project made before v0.5, which had none (the pages call it); ""
	// when the project had views/ui. Its files are in Created.
	Kit string
}

// MakeAuth writes the account scaffolding into the project at root:
// the User model, the handlers, the emails, the pages, the routes, the
// users table's migration, setupAuth and its tests, and the Users
// factory. It writes nothing if
// one of the files exists already, or a name they declare is taken in
// its package; if a write fails, it removes what it wrote. When setup in
// main.go has the routes.Register call of an anetos new project, it adds
// the call to setupAuth after it. It adds the SOCIAL_* settings, empty,
// to .env, .env.example and deploy/production.env.example.
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
	res.API = IsAPI(root)
	dirs, files, names := []string{"app/models", "app/handlers", "views", "routes", "database/migrations", "locales"}, authFiles, authNames
	migration := "migration.go.tmpl"
	if res.API {
		dirs, files, names = []string{"app/models", "app/handlers", "routes", "database/migrations", "locales"}, authAPIFiles, authAPINames
		migration = "api/migration.go.tmpl"
	}
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(root, dir)); err != nil {
			return res, fmt.Errorf("no %s directory: make:auth adds to a project made with anetos new", dir)
		}
	}
	files = append([][2]string(nil), files...)
	if m, ok := existingMigration(filepath.Join(root, "database", "migrations"), "_create_users_table.go"); ok {
		return res, fmt.Errorf("database/migrations/%s exists: the app has users already", m)
	}
	ts := now.UTC().Truncate(time.Second)
	if last, ok := latestMigration(filepath.Join(root, "database", "migrations")); ok && !ts.After(last) {
		ts = last.Add(time.Second)
	}
	id := ts.Format(idLayout) + "_create_users_table"
	files = append(files, [2]string{migration, "database/migrations/" + id + ".go"})
	for _, f := range files {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(f[1]))); err == nil {
			return res, fmt.Errorf("%s exists: make:auth writes nothing over the app's files", f[1])
		}
	}

	// The names the files declare mustn't be taken in their packages.
	for dir, names := range names {
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

	// The settings files, as they are: read now, so a file that can't be
	// read stops make:auth before it writes anything.
	envFiles := []string{".env", ".env.example", "deploy/production.env.example"}
	envBefore := map[string][]byte{}
	for _, name := range envFiles {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return res, err
		}
		envBefore[name] = b
	}

	data := struct{ Module, ID string }{mod, id}
	out := make([][]byte, len(files))
	for i, f := range files {
		if out[i], err = render("templates/auth/"+f[0], "", data); err != nil {
			return res, err
		}
	}
	// undo leaves the project as it was.
	settings := func(name string) string {
		switch {
		case !res.API:
			return socialSettings
		case name == "deploy/production.env.example":
			return clientSettingsProd
		}
		return clientSettings
	}
	undo := func(err error) (AuthResult, error) {
		for _, c := range res.Created {
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(c)))
		}
		_ = os.Remove(filepath.Join(root, "app", "mailers")) // if make:auth created it, it's empty
		if res.Kit != "" {
			_ = os.Remove(filepath.Join(root, "views", "ui"))
		}
		for _, name := range res.Env {
			_ = os.WriteFile(filepath.Join(root, name), envBefore[name], 0o600)
		}
		return AuthResult{}, fmt.Errorf("%w (the files written were removed)", err)
	}
	if !res.API && !HasUI(root) {
		res.Kit = GuessKit(root)
		written, err := WriteUI(root, mod, res.Kit)
		res.Created = append(res.Created, written...)
		if err != nil {
			return undo(err)
		}
	}
	for i, f := range files {
		if err := writeNew(filepath.Join(root, filepath.FromSlash(f[1])), out[i], 0o644); err != nil {
			return undo(err)
		}
		res.Created = append(res.Created, f[1])
	}
	for _, name := range envFiles {
		if envBefore[name] == nil {
			continue // no such file
		}
		added, err := addSettings(filepath.Join(root, name), settings(name))
		if added {
			res.Env = append(res.Env, name)
		}
		if err != nil {
			if !added {
				res.Env = append(res.Env, name) // it may be half written
			}
			return undo(err)
		}
	}
	call, after := authCall, routesCall
	if res.API {
		call, after = authCallAPI, routesCallAPI
	}
	wired, err := wireAuth(filepath.Join(root, "main.go"), call, after)
	res.Wired = wired
	if err != nil || res.API {
		return res, err
	}
	res.Menu, err = addAccountMenu(filepath.Join(root, "views", "layout.templ"))
	return res, err
}

// accountMenu is the line make:auth adds to the layout's header.
const accountMenu = "@AccountMenu()"

// addAccountMenu adds @AccountMenu() to the layout's header, on the line
// after the header's nav (navEnd), as anetos new writes it, and reports
// whether the layout shows it. It leaves a layout without such a nav
// alone, reporting false.
func addAccountMenu(layout string) (bool, error) {
	src, err := os.ReadFile(layout)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if bytes.Contains(src, []byte(accountMenu)) {
		return true, nil
	}
	_, at, indent, ok := navEnd(src)
	if !ok {
		return false, nil
	}
	line := append(append(append([]byte(nil), indent...), accountMenu...), newline(src)...)
	patched := append(append(append([]byte(nil), src[:at]...), line...), src[at:]...)
	return true, os.WriteFile(layout, patched, 0o644)
}

// newline is a file's line ending: \r\n when it has one, else \n.
func newline(src []byte) []byte {
	if bytes.Contains(src, []byte("\r\n")) {
		return []byte("\r\n")
	}
	return []byte("\n")
}

// navEnd finds the line ending the header's nav in a layout: the }
// closing @ui.Nav(…) { (anetos new since v0.5), or else a </nav> inside
// <header> (before). It returns the line's start and end (after its
// newline) and its indentation.
func navEnd(src []byte) (start, end int, indent []byte, ok bool) {
	if m := uiNavOpen.FindSubmatchIndex(src); m != nil {
		indent = src[m[2]:m[3]]
		closing := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(string(indent)) + `\}[ \t]*\r?\n`)
		if c := closing.FindIndex(src[m[1]:]); c != nil {
			return m[1] + c[0], m[1] + c[1], indent, true
		}
		return 0, 0, nil, false
	}
	header := bytes.Index(src, []byte("<header"))
	last := bytes.Index(src, []byte("</header>"))
	if header < 0 || last < header {
		return 0, 0, nil, false
	}
	m := navClose.FindSubmatchIndex(src[header:last])
	if m == nil {
		return 0, 0, nil, false
	}
	return header + m[0], header + m[1], src[header+m[2] : header+m[3]], true
}

// uiNavOpen is the line opening the header's @ui.Nav(…) block.
var uiNavOpen = regexp.MustCompile(`(?m)^([ \t]*)@ui\.Nav\(.*\{[ \t]*\r?\n`)

// navClose is a line closing a <nav>.
var navClose = regexp.MustCompile(`(?m)^([ \t]*)</nav>[ \t]*\r?\n`)

// socialSettings are the settings of login with Google and GitHub.
const socialSettings = `
# Log in with Google and GitHub (anetos make:auth): each is on once its
# client ID and secret are set. Register APP_URL/auth/google/callback
# (or /github/) as the callback URL with the provider.
SOCIAL_GOOGLE_CLIENT_ID=
SOCIAL_GOOGLE_CLIENT_SECRET=
SOCIAL_GITHUB_CLIENT_ID=
SOCIAL_GITHUB_CLIENT_SECRET=
`

// clientSettings and clientSettingsProd are the client app's address of
// an API project's accounts, for development and production.
const (
	clientSettings = `
# The client app's address (anetos make:auth): the verification and
# password reset emails link to its /verify-email and /reset-password
# pages, which send the token in the link to the API.
AUTH_CLIENT_URL=http://localhost:5173
`
	clientSettingsProd = `
# The client app's address (anetos make:auth): the verification and
# password reset emails link to its /verify-email and /reset-password
# pages, which send the token in the link to the API. Required.
AUTH_CLIENT_URL=https://app.example.com
`
)

// addSettings appends block to the settings file, unless the file is
// missing or already has the first setting of block (commented out or
// not), and reports whether it did.
func addSettings(file, block string) (bool, error) {
	cur, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var first string
	for line := range strings.SplitSeq(block, "\n") {
		if k, _, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(line, "#") {
			first = k
			break
		}
	}
	if first != "" && regexp.MustCompile(`(?m)^[#\s]*(export\s+)?`+regexp.QuoteMeta(first)+`\s*=`).Match(cur) {
		return false, nil
	}
	switch {
	case len(cur) == 0 || bytes.HasSuffix(cur, []byte("\n\n")):
		block = strings.TrimPrefix(block, "\n") // a blank line already
	case !bytes.HasSuffix(cur, []byte("\n")):
		block = "\n" + block
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	_, err = f.WriteString(block)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err == nil, err
}

// authNames are the top-level names the generated files declare, by
// package directory ("" is the root, package main).
var authNames = map[string][]string{
	"app/models":         {"User", "Users", "UserCols"},
	"app/handlers":       {"Accounts", "RegisterInput", "LoginInput", "ForgotInput", "ResetInput", "TokenQuery", "NewTokenInput", "TokenID", "CodeInput", "PasswordInput", "ProfileInput", "EmailInput", "NewPasswordInput", "PreferencesInput", "RevertInput", "sendEmailChange", "SocialUser", "SendVerification", "SendPasswordReset", "emailTaken", "cleanName"},
	"app/mailers":        {"VerifyEmail", "ResetPassword", "ChangeEmail", "EmailChanging"},
	"views":              {"AccountMenu", "SocialButton", "Register", "Login", "ForgotPassword", "ResetPassword", "Dashboard", "TwoFactorChallenge", "ConfirmPassword", "TwoFactorPage", "TwoFactor", "Choice", "SettingsPage", "Settings", "RevertEmail", "ChangeEmailMail", "EmailChangingMail", "socialButtons", "localeOptions", "zoneOptions", "VerifyEmailMail", "ResetPasswordMail", "authMail"},
	"routes":             {"Auth"},
	"database/factories": {"Users", "UserPassword", "userHash"},
	"":                   {"setupAuth", "authRegister", "authLink", "TestRegisterAndVerify", "TestResendVerification", "TestRegisterValidation", "TestLoginAndLogout", "TestTwoFactor", "TestSettings", "TestChangeEmail", "TestRevertEmailChange", "TestDisabledAccount", "TestLoginReturnsToTheRequestedPage", "TestPasswordReset", "TestResetLogsOutAndRevokesTokens", "TestAPIToken", "TestSocialLogin", "TestSocialLoginFindsVerifiedAccounts"},
}

// authAPINames are authNames in an API project.
var authAPINames = map[string][]string{
	"app/models":         {"User", "Users", "UserCols"},
	"app/handlers":       {"Accounts", "loginTTL", "tokenTTL", "UserResponse", "userResponse", "LoginResponse", "RegisterInput", "LoginInput", "ChallengeInput", "EmailInput", "LinkInput", "ResetInput", "ChangePasswordInput", "PasswordInput", "CodeInput", "NewTokenInput", "TokenID", "TokenResponse", "NewTokenResponse", "TwoFactorResponse", "TwoFactorSetupResponse", "RecoveryCodesResponse", "emailTaken", "cleanName", "SendVerification", "SendPasswordReset", "tooMany", "tooManyFor", "tokenResponse"},
	"app/mailers":        {"authHTML", "authMail", "authMailData", "authBody", "VerifyEmail", "ResetPassword"},
	"routes":             {"Auth"},
	"database/factories": {"Users", "UserPassword", "userHash"},
	"":                   {"setupAuth", "authClient", "authApp", "authRegister", "linkToken", "authLogin", "TestRegisterAndVerify", "TestResendVerification", "TestRegisterValidation", "TestLoginAndLogout", "TestLoginThrottled", "TestTwoFactor", "TestDisabledAccount", "TestPasswordReset", "TestResetOfUnverifiedAddress", "TestResetRevokesTokens", "TestChangePassword", "TestAPITokens"},
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

// wireAuth adds call, the setupAuth call, to setup in main.go, after
// its routes.Register(…) statement, which must read after, and reports
// whether setup calls setupAuth. It leaves main.go alone, reporting
// false, when it has no such setup.
func wireAuth(main, call, after string) (bool, error) {
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
	var register ast.Stmt
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
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "routes" && register == nil {
				register = st
			}
		}
	}
	params := setup.Type.Params.List
	if register == nil || setup.Type.Results == nil || setup.Type.Results.NumFields() != 2 ||
		len(params) == 0 || len(params[0].Names) == 0 || params[0].Names[0].Name != "app" {
		return false, nil // the call below needs app, srv, sessions and a (value, error) return
	}
	start, end := fset.Position(register.Pos()).Offset, fset.Position(register.End()).Offset
	if string(src[start:end]) != after {
		return false, nil
	}
	nl := bytes.IndexByte(src[end:], '\n')
	if nl < 0 {
		return false, nil
	}
	at := end + nl + 1
	patched := append(append(append([]byte(nil), src[:at]...), call...), src[at:]...)
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
