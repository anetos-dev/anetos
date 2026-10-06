// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
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
	if res.Routed {
		fmt.Fprintf(stdout, "updated routes/web.go: Register calls %s(pages)\n", res.Plural)
	}
	if res.Linked {
		fmt.Fprintf(stdout, "updated views/layout.templ: the nav links to %s\n", res.Path)
	}
	finish := func(err error) int {
		fmt.Fprintf(stderr, "anetos make:crud: %v\nThe files are written; once fixed, finish with:\n\tgo tool anetos gen && go tool templ generate && go build ./...\n", err)
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
	var templOut bytes.Buffer // templ reports progress on stderr: shown only if it fails
	if err := runGoOut(ctx, root, &templOut, &templOut, "tool", "templ", "generate"); err != nil {
		fmt.Fprint(stderr, templOut.String())
		return finish(err)
	}
	if err := runGo(ctx, root, stderr, "build", "./..."); err != nil {
		return finish(fmt.Errorf("the project doesn't build: %w", err))
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
  go test ./...        the pages' test (%s_test.go)
  go tool anetos dev   then open %s
`, strings.TrimPrefix(strings.ReplaceAll(res.Path, "-", "_"), "/"), res.Path)
	return 0
}
