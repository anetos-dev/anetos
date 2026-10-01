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
and throttling, logout, email verification, password reset and API
tokens. It writes the User model, the handlers, the pages and emails,
the routes, the users table's migration, setupAuth (auth.go) and its
tests, then calls setupAuth from setup in main.go. The code is yours to
change; hashing, tokens, sessions and throttling stay in package auth.
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
	if res.Wired {
		fmt.Fprintln(stdout, "updated main.go: setup calls setupAuth")
	}
	// The modules the new imports need, the User model's typed columns,
	// the pages' Go code, and a check that it all builds.
	finish := func(err error) int {
		fmt.Fprintf(stderr, "anetos make:auth: %v\nThe files are written; once fixed, finish with:\n\tgo mod tidy && go tool anetos gen && go tool templ generate && go build ./...\n", err)
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
	var templOut bytes.Buffer // templ reports progress on stderr: shown only if it fails
	if err := runGoOut(ctx, root, &templOut, &templOut, "tool", "templ", "generate"); err != nil {
		fmt.Fprint(stderr, templOut.String())
		return finish(err)
	}
	if err := runGo(ctx, root, stderr, "build", "./..."); err != nil {
		return finish(fmt.Errorf("the project doesn't build: %w", err))
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
	fmt.Fprint(stdout, `
Next:
  go run . migrate     create the users and api_tokens tables
  go test ./...        the account tests (auth_test.go)
  go tool anetos dev   then open /register; emails go to the log (MAIL_DRIVER=log)
`)
	return 0
}
