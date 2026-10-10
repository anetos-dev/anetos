// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"anetos.dev/anetos/cli/internal/modelgen"
	"anetos.dev/anetos/cli/internal/scaffold"
)

const makeAuthUsage = `Usage: anetos make:auth

Adds accounts to the project: registration, login with "remember me"
and throttling, login with Google and GitHub, logout, email
verification, password reset and API tokens. It writes the User model,
the handlers, the pages and emails, the routes, the users table's
migration, setupAuth (auth.go) and its tests, adds the SOCIAL_*
settings to .env and .env.example, calls setupAuth from setup in
main.go, and adds the account links (AccountMenu) to the layout's
header. Logging in leads to /dashboard: AUTH_HOME_URL, or the default in
auth.go, sets another page. The code is yours to change; hashing, tokens, sessions and
throttling stay in package auth, the login flow in package social.

In an API project (anetos new --stack=api), it writes JSON endpoints
under /api/v1 instead: registration and login answering with an API
token (with two-factor codes for users who turn them on), logout, /me,
email verification and password reset by emails linking to the client
app (AUTH_CLIENT_URL, added to the settings files), password change,
token management and two-factor authentication; the User model, the users
table's migration, setupAuth (auth.go), the emails (app/mailers) and
their tests.
`

// makeAuth runs anetos make:auth.
func makeAuth(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos make:auth", flag.ContinueOnError)
	pos, code := parse(fs, args, stderr, makeAuthUsage)
	if code >= 0 {
		return code
	}
	if len(pos) != 0 {
		fs.Usage()
		return 2
	}
	wd, err := os.Getwd()
	if err == nil {
		wd, err = scaffold.FindRoot(wd)
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:auth:", err)
		return 1
	}
	root := wd
	res, err := scaffold.MakeAuth(root, time.Now())
	for _, p := range res.Created {
		fmt.Fprintln(stdout, "created", p)
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:auth:", err)
		return 1
	}
	if res.Kit != "" {
		fmt.Fprintf(stdout, "views/ui has the components for %s, which the new pages call: the project had none (made before v0.5).\nThe layout keeps its markup; the upgrade guide shows how to use them there too.\n", scaffold.CSSName(res.Kit))
	}
	settings := "SOCIAL_* settings"
	if res.API {
		settings = "AUTH_CLIENT_URL"
	}
	for _, f := range res.Env {
		fmt.Fprintf(stdout, "updated %s: %s\n", f, settings)
	}
	if res.Wired {
		fmt.Fprintln(stdout, "updated main.go: setup calls setupAuth")
	}
	if res.Menu {
		fmt.Fprintln(stdout, "updated views/layout.templ: the header shows AccountMenu")
	}
	// The modules the new imports need, the User model's typed columns,
	// the pages' Go code, and a check that it all builds.
	finish := func(err error) int {
		steps := "go mod tidy && go tool anetos generate && go tool templ generate && go build ./..."
		if res.API {
			steps = "go mod tidy && go tool anetos generate && go build ./..."
		}
		fmt.Fprintf(stderr, "anetos make:auth: %v\nThe files are written; once fixed, finish with:\n\t%s\n", err, steps)
		return 1
	}
	if err := runGo(ctx, root, stderr, "mod", "tidy"); err != nil {
		return finish(err)
	}
	changes, err := modelgen.Generate(root, "./app/models")
	if err == nil {
		err = modelgen.Apply(changes)
	}
	if err != nil {
		return finish(fmt.Errorf("generating columns: %w", err))
	}
	for _, c := range changes {
		fmt.Fprintln(stdout, "wrote", rel(root, c.Path))
	}
	if !res.API {
		var templOut bytes.Buffer // templ reports progress on stderr: shown only if it fails
		if err := runGoOut(ctx, root, &templOut, &templOut, "tool", "templ", "generate"); err != nil {
			fmt.Fprint(stderr, templOut.String())
			return finish(err)
		}
	}
	if err := runGo(ctx, root, stderr, "build", "./..."); err != nil {
		return finish(fmt.Errorf("the project doesn't build: %w", err))
	}
	if res.API {
		updateOpenAPI(ctx, root, stdout, stderr)
		if !res.Wired {
			fmt.Fprint(stdout, `
main.go doesn't have the routes.Register call of a new project: call
setupAuth yourself in setup, after the routes:

	if _, err := setupAuth(app, srv.Router()); err != nil {
		return nil, err
	}
`)
		}
		fmt.Fprint(stdout, `
Next:
  go run . migrate     create the users and api_tokens tables
  go test ./...        the account tests (auth_test.go)
  go tool anetos dev   then POST /api/v1/register; emails go to the log (MAIL_DRIVER=log)
The emails link to the client app: AUTH_CLIENT_URL in .env.
`)
		return 0
	}
	if !res.Wired {
		fmt.Fprint(stdout, `
main.go doesn't have the routes.Register call of a new project: call
setupAuth yourself in setup, after the routes:

	if _, err := setupAuth(app, srv.Router(), sessions); err != nil {
		return nil, err
	}
`)
	}
	if !res.Menu {
		fmt.Fprint(stdout, `
views/layout.templ has no header with a nav, as a new project's: add the
links to log in, register and log out (views/auth.templ) where you want
them:

	@AccountMenu()
`)
	}
	fmt.Fprint(stdout, `
Next:
  go run . migrate     create the users, api_tokens and social_accounts tables
  go test ./...        the account tests (auth_test.go)
  go tool anetos dev   then open /register; emails go to the log (MAIL_DRIVER=log)
Log in with Google or GitHub: set its SOCIAL_* settings in .env.
`)
	return 0
}
