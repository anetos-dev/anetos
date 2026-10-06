// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"anetos.dev/anetos/cli/internal/scaffold"
)

const newUsage = `Usage: anetos new <directory> [--module=path] [--db=sqlite|postgres|mysql] [--css=anetos|none]

Creates an Anetos project: routes, handlers, templ views with a layout
styled by Anetos's starter theme (--css=none: no styles),
sessions and CSRF protection, migrations, static files with htmx, a test,
and a .env with a fresh APP_KEY. Then it downloads the dependencies and
generates the views (skip with --skip-install).
`

func newProject(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos new", flag.ContinueOnError)
	module := fs.String("module", "", "Go module path (default: the directory's name)")
	dbName := fs.String("db", "sqlite", "database: sqlite, postgres or mysql")
	css := fs.String("css", "anetos", "stylesheet: anetos (a starter theme, no build step) or none")
	replace := fs.String("replace", "", "use a local Anetos checkout at this path (for framework development)")
	skip := fs.Bool("skip-install", false, "don't download dependencies or generate code")
	pos, code := parse(fs, args, stderr, newUsage)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2
	}
	dir := pos[0]
	files, err := scaffold.Create(scaffold.Project{Dir: dir, Module: *module, DB: *dbName, Replace: *replace, CSS: *css})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Created %s (%d files).\n", dir, len(files))
	if !*skip {
		for _, step := range installSteps(*dbName, *replace != "") {
			fmt.Fprintf(stdout, "  go %s\n", strings.Join(step, " "))
			if out, err := goCmd(ctx, dir, step...); err != nil {
				fmt.Fprintf(stderr, "anetos new: go %s failed: %v\n%s\nFix the problem, then run the remaining steps in %s yourself.\n", strings.Join(step, " "), err, out, dir)
				return 1
			}
		}
	}
	fmt.Fprintf(stdout, "\nNext:\n  cd %s\n", dir)
	if *dbName != "sqlite" {
		fmt.Fprintf(stdout, "  # create the database and set DB_* in .env\n")
	}
	fmt.Fprintf(stdout, "  go run . migrate\n  go tool anetos dev    # http://localhost:8080\n")
	return 0
}

// installSteps are the go commands that finish a new project.
func installSteps(db string, replaced bool) [][]string {
	var steps [][]string
	if !replaced {
		// The modules are versioned separately: the tool pins itself, the
		// core and driver take their latest releases.
		steps = append(steps,
			[]string{"get", "anetos.dev/anetos@latest", "anetos.dev/anetos/drivers/" + db + "@latest"},
			[]string{"get", "-tool", "anetos.dev/anetos/cli/cmd/anetos@" + anetosVersion()})
	}
	return append(steps,
		[]string{"get", "-tool", "github.com/a-h/templ/cmd/templ@" + scaffold.TemplVersion},
		[]string{"tool", "templ", "generate"}, // views/ has no Go files before this
		[]string{"mod", "tidy"},
	)
}

// anetosVersion is the version of Anetos to put in new projects: this
// tool's, or latest when it isn't a release.
func anetosVersion() string {
	if v := version(); strings.HasPrefix(v, "v") && !strings.Contains(v, "-0.") && v != "v0.0.0" {
		return v
	}
	return "latest"
}

// goCmd runs the go command in dir, returning its combined output.
func goCmd(ctx context.Context, dir string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, "go", args...)
	c.Dir = dir
	c.Env = os.Environ()
	var out bytes.Buffer
	c.Stdout, c.Stderr = &out, &out
	err := c.Run()
	return out.Bytes(), err
}
