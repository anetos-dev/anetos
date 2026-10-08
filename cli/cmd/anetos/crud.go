// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"anetos.dev/anetos/cli/internal/modelgen"
	"anetos.dev/anetos/cli/internal/scaffold"
)

const makeCrudUsage = `Usage: anetos make:crud <Model> <field:type[:optional|:unique]>...

Adds a model with its table and the pages to list, show, create, edit
and delete its rows: the model, the migration, the handlers, the templ
views (styled by the starter theme), the routes, their English text and a
test. It adds the routes to routes/web.go's pages group and a link to
the layout's nav. The code is yours to change.

In an API project (anetos new --stack=api), it writes JSON endpoints
under /api/v1 instead: the model, the migration, the handlers (the list
with pages, sorting and filters; show, create, replace and delete),
the routes, added to routes/api.go's api group, and a test.

Types: ` + "string, text, email, int, float, bool, date" + `. Fields are required,
except numbers and booleans; :optional makes one optional, :unique
(required strings and emails, dates) checks no two rows share it. For
example:

	go tool anetos make:crud Post title:string body:text published:bool
	go tool anetos make:crud Customer name:string email:email:unique notes:text:optional
`

// makeCrud runs anetos make:crud.
func makeCrud(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos make:crud", flag.ContinueOnError)
	pos, code := parse(fs, args, stderr, makeCrudUsage)
	if code >= 0 {
		return code
	}
	if len(pos) < 2 {
		fs.Usage()
		return 2
	}
	wd, err := os.Getwd()
	if err == nil {
		wd, err = scaffold.FindRoot(wd)
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:crud:", err)
		return 1
	}
	root := wd
	res, err := scaffold.MakeCrud(root, pos[0], pos[1:], time.Now())
	for _, p := range res.Created {
		fmt.Fprintln(stdout, "created", p)
	}
	if err != nil {
		fmt.Fprintln(stderr, "anetos make:crud:", err)
		return 1
	}
	file, group := "routes/web.go", "pages"
	if res.API {
		file, group = "routes/api.go", "api"
	}
	if res.Routed {
		fmt.Fprintf(stdout, "updated %s: Register calls %s(%s)\n", file, res.Plural, group)
	}
	if res.Linked {
		fmt.Fprintf(stdout, "updated views/layout.templ: the nav links to %s\n", res.Path)
	}
	finish := func(err error) int {
		steps := "go tool anetos gen && go tool templ generate && go build ./..."
		if res.API {
			steps = "go tool anetos gen && go build ./..."
		}
		fmt.Fprintf(stderr, "anetos make:crud: %v\nThe files are written; once fixed, finish with:\n\t%s\n", err, steps)
		return 1
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
		if !res.Routed {
			fmt.Fprintf(stdout, `
routes/api.go has no Register with an api group, as a new project's: add
the routes to your API's group yourself:

	routes.%s(api)
`, res.Plural)
		} else {
			fmt.Fprintf(stdout, `
The endpoints are open to every client. To let only clients with a token
in (after make:auth), move the %s(api) call to routes/auth.go's me group.
`, res.Plural)
		}
		fmt.Fprintf(stdout, `
Next:
  go run . migrate     create the table
  go test ./...        the endpoints' test (%s)
  go tool anetos dev   then GET %s
`, testFile(res.Created), res.Path)
		return 0
	}
	if !res.Routed {
		fmt.Fprintf(stdout, `
routes/web.go has no Register with a pages group, as a new project's: add
the routes to a group with sessions and CSRF protection yourself:

	routes.%s(pages)
`, res.Plural)
	}
	if !res.Linked {
		fmt.Fprintf(stdout, "\nLink to %s from your layout's nav, if you like.\n", res.Path)
	}
	if res.Routed {
		fmt.Fprintf(stdout, `
The pages are open to everyone. To let only signed-in users in (after
make:auth), move the %s(pages) call to routes/auth.go's members group.
`, res.Plural)
	}
	fmt.Fprintf(stdout, `
Next:
  go run . migrate     create the table
  go test ./...        the pages' test (%s)
  go tool anetos dev   then open %s
`, testFile(res.Created), res.Path)
	return 0
}

// testFile is the test among the files make:crud wrote.
func testFile(created []string) string {
	for _, f := range created {
		if strings.HasSuffix(f, "_test.go") {
			return f
		}
	}
	return "*_test.go"
}

// updateOpenAPI rewrites the API's description (openapi.json) with the
// project's openapi command, in a project whose main.go adds it (an API
// project's, since v0.4). A failure is reported, not fatal: the code is
// written.
func updateOpenAPI(ctx context.Context, root string, stdout, stderr io.Writer) {
	main, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || !bytes.Contains(main, []byte("openapi.ForApp(")) {
		return
	}
	var out bytes.Buffer
	if err := runGoOut(ctx, root, &out, &out, "run", ".", "openapi"); err != nil {
		fmt.Fprintf(stderr, "Updating openapi.json failed (%v):\n%s\nRun `go run . openapi` once it's fixed.\n", err, out.String())
		return
	}
	fmt.Fprintln(stdout, "Updated openapi.json, the API's description.")
}
