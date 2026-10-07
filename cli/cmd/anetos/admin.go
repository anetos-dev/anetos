// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"anetos.dev/anetos/cli/internal/scaffold"
)

const makeAdminUsage = `Usage: anetos make:admin

Adds the admin interface (module anetos.dev/anetos/admin) to a project
with make:auth's accounts: setupAdmin (admin.go), which sets up roles
and permissions (package auth/rbac) with an admin role and mounts the
admin at ADMIN_PATH (/admin) or ADMIN_HOST, with the users (app/admin/
users.go) and roles; and the app/admin package, where make:admin:resource
adds a resource per model. It adds the module to go.mod, the ADMIN_*
settings to .env and .env.example, the banner shown while acting as a
user to views/layout.templ, and calls setupAdmin from setup in main.go.
`

const makeAdminResourceUsage = `Usage: anetos make:admin:resource <Model>

Writes app/admin/<models>.go, the admin's resource for a model of
app/models: its list's columns and search, and a form struct with the
fields the admin may change. It adds the resource to app/admin/admin.go.
Change it as the app needs: columns, filters, validation, actions.
`

// makeAdmin runs anetos make:admin.
func makeAdmin(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos make:admin", flag.ContinueOnError)
	pos, code := parse(fs, args, stderr, makeAdminUsage)
	if code >= 0 {
		return code
	}
	if len(pos) != 0 {
		fs.Usage()
		return 2
	}
	root, err := projectRoot()
	if err == nil {
		err = scaffold.RefuseAPI(root, "make:admin", scaffold.AdminInAPI) // before go get
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:admin:", err)
		return 1
	}
	restore, err := backup(root, "go.mod", "go.sum")
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:admin:", err)
		return 1
	}
	// The module first: if it can't be had, nothing is written.
	replace, replaced, err := scaffold.AdminReplace(root)
	if err == nil && replace != "" {
		err = runGo(ctx, root, stderr, "mod", "edit", "-replace", scaffold.AdminModule+"="+replace)
	}
	if err == nil {
		err = runGo(ctx, root, stderr, "get", scaffold.AdminModule+"@"+adminVersion(replaced || replace != ""))
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:admin:", err)
		if rerr := restore(); rerr != nil {
			fmt.Fprintln(stderr, "anetos make:admin: restore go.mod and go.sum:", rerr)
		}
		return 1
	}
	res, err := scaffold.MakeAdmin(root)
	for _, p := range res.Created {
		fmt.Fprintln(stdout, "created", p)
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:admin:", err)
		if rerr := restore(); rerr != nil {
			fmt.Fprintln(stderr, "anetos make:admin: restore go.mod and go.sum:", rerr)
		}
		return 1
	}
	for _, f := range res.Env {
		fmt.Fprintf(stdout, "updated %s: ADMIN_* settings\n", f)
	}
	if res.Wired {
		fmt.Fprintln(stdout, "updated main.go: setup calls setupAdmin")
	}
	if res.Banner {
		fmt.Fprintln(stdout, "updated views/layout.templ: the banner shown while acting as a user")
	}
	finish := func(err error) int {
		fmt.Fprintf(stderr, "anetos make:admin: %v\nThe files are written; once fixed, finish with:\n\tgo mod tidy && go tool templ generate && go build ./...\n", err)
		return 1
	}
	if err := runGo(ctx, root, stderr, "mod", "tidy"); err != nil {
		return finish(err)
	}
	if res.Banner {
		var templOut bytes.Buffer // templ reports progress on stderr: shown only if it fails
		if err := runGoOut(ctx, root, &templOut, &templOut, "tool", "templ", "generate"); err != nil {
			fmt.Fprint(stderr, templOut.String())
			return finish(err)
		}
	} else {
		fmt.Fprint(stdout, `
views/layout.templ isn't anetos new's: show the banner of acting as a
user yourself, at the top of <body>: @admin.Banner()
`)
	}
	if res.Users && (!res.Disabled || !res.SessionKey) {
		fmt.Fprint(stdout, `
app/models/user.go is an older make:auth's: disabling accounts and
signing users out everywhere need a disabled_at and a session_key column
and auth.Users' Disabled, SessionKey and SetSessionKey (see the guide
"Add an admin panel"). The rest works without them.
`)
	}
	if !res.Wired {
		fmt.Fprint(stdout, `
main.go doesn't call setupAuth as make:auth wrote it: call setupAdmin
yourself in setup, with the *auth.Auth that setupAuth returns:

	a, err := setupAuth(app, srv.Router(), sessions)
	if err != nil {
		return nil, err
	}
	if err := setupAdmin(app, srv.Router(), sessions, a); err != nil {
		return nil, err
	}
`)
	} else if err := runGo(ctx, root, stderr, "build", "./..."); err != nil {
		return finish(fmt.Errorf("the project doesn't build: %w", err))
	}
	next := `
Next:
  go tool anetos make:admin:resource Post   a resource per model (app/admin)
  go run . migrate                          create the roles tables
  go run . rbac:assign <user-id> admin      let a user in
  go tool anetos dev                        then open /admin
`
	if !res.RBAC {
		next = `
The app sets up roles already (rbac.ForApp): give admin.access, and the
resources' admin.<name>.view/create/update/delete, with a role of yours.

Next:
  go tool anetos make:admin:resource Post   a resource per model (app/admin)
  go tool anetos dev                        then open /admin
`
	}
	fmt.Fprint(stdout, next)
	return 0
}

// adminVersion is the version of the admin module to add: any, for a
// module replaced by a checkout, else this tool's.
func adminVersion(replaced bool) string {
	if replaced {
		return "v0.0.0-00010101000000-000000000000"
	}
	return anetosVersion()
}

// makeAdminResource runs anetos make:admin:resource.
func makeAdminResource(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos make:admin:resource", flag.ContinueOnError)
	pos, code := parse(fs, args, stderr, makeAdminResourceUsage)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2
	}
	root, err := projectRoot()
	if err == nil {
		var files []string
		files, err = scaffold.MakeAdminResource(root, pos[0])
		if err == nil {
			fmt.Fprintln(stdout, "created", files[0])
			fmt.Fprintln(stdout, "updated", files[1])
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:admin:resource:", err)
		return 1
	}
	return 0
}

// projectRoot returns the project's directory: that of the go.mod at or
// above the working directory.
func projectRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return scaffold.FindRoot(wd)
}
