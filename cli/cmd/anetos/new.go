// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"anetos.dev/anetos/cli/internal/frontend"
	"anetos.dev/anetos/cli/internal/scaffold"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const newUsage = `Usage: anetos new <directory> [--module=path] [--db=sqlite|postgres|mysql] [--stack=web|api] [--css=anetos|none|pico|bootstrap|bulma|tailwind] [--vite[=ts|js]]

Creates an Anetos project: routes, handlers, migrations, a test, the
files to deploy it, and a .env with a fresh APP_KEY. The web stack (the
default) has templ views made of the components in views/ui, styled by
Anetos's starter theme, sessions and CSRF protection, and static files
with htmx. --css picks a CSS framework instead: none (components writing
plain HTML without classes, and no styles), Pico, Bootstrap or Bulma
(their components, with the framework's files in public/static; no
build step), or Tailwind CSS (components with its classes; anetos dev
and anetos build compile views/ui/tailwind.css with its standalone CLI,
downloaded on first use). The api stack (--stack=api) serves JSON only:
routes under /api/v1, errors as JSON problem details, CORS settings,
an OpenAPI description (openapi.json); no views. --vite adds a
frontend that Vite builds (frontend/, in TypeScript, or JavaScript with
--vite=js): the web stack's layout loads it; the api stack serves it as
a single-page app at / (in development, Vite's address,
http://localhost:5173). It needs Node.js 20.19 or 22.12 or later (see
anetos vite:install). Then it
downloads the dependencies and generates the views (web) or
openapi.json (api); skip with --skip-install. It also writes the files
coding agents read (AGENTS.md, CLAUDE.md, GEMINI.md, and the skills in
.agents/skills and .claude/skills; see anetos agent:install), unless
--no-agent.
`

func newProject(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos new", flag.ContinueOnError)
	module := fs.String("module", "", "Go module path (default: the directory's name)")
	dbName := fs.String("db", "sqlite", "database: sqlite, postgres or mysql")
	css := fs.String("css", "", "web stack's CSS framework (views/ui and its stylesheet): anetos (the starter theme; the default), none (plain HTML, no styles), pico, bootstrap, bulma or tailwind")
	stack := fs.String("stack", "web", "kind of app: web (pages, sessions) or api (JSON only)")
	replace := fs.String("replace", "", "use a local Anetos checkout at this path (for framework development)")
	skip := fs.Bool("skip-install", false, "don't download dependencies or generate code")
	noAgent := fs.Bool("no-agent", false, "don't write the files coding agents read (AGENTS.md, CLAUDE.md, GEMINI.md, the skills)")
	var lang viteFlag
	fs.Var(&lang, "vite", "add a frontend that Vite builds (frontend/; an api project's is a single-page app): --vite for TypeScript, --vite=js for JavaScript (needs Node.js)")
	args = slices.Clone(args)
	for i, a := range args {
		if a == "--vite" || a == "-vite" { // alone: TypeScript
			args[i] = "--vite=ts"
		}
	}
	pos, code := parse(fs, args, stderr, newUsage)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2
	}
	dir := pos[0]
	agents := scaffold.Agents
	if *noAgent {
		agents = nil
	}
	files, err := scaffold.Create(scaffold.Project{Dir: dir, Module: *module, DB: *dbName, Replace: *replace, CSS: *css, Stack: *stack, Agents: agents, Vite: string(lang)})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "Created %s (%d files).\n", dir, len(files))
	if !*skip {
		for _, step := range installSteps(*dbName, *stack, *replace != "", lang != "") {
			fmt.Fprintf(stdout, "  go %s\n", strings.Join(step, " "))
			if out, err := goCmd(ctx, dir, step...); err != nil {
				fmt.Fprintf(stderr, "anetos new: go %s failed: %v\n%s\nFix the problem, then run the remaining steps in %s yourself.\n", strings.Join(step, " "), err, out, dir)
				return 1
			}
		}
		if lang != "" {
			if err := frontend.CheckNode(ctx); err != nil {
				fmt.Fprintf(stdout, "  Not installing the frontend's packages: %v.\n  Once Node.js is installed, anetos dev installs them.\n", err)
			} else if err := installFrontend(ctx, dir, stdout); err != nil {
				fmt.Fprintf(stderr, "anetos new: %v\nFix the problem, then run npm install in %s.\n", err, filepath.Join(dir, frontend.Dir))
				return 1
			}
		}
	}
	fmt.Fprintf(stdout, "\nNext:\n  cd %s\n", dir)
	if *skip && *stack == "api" {
		fmt.Fprintf(stdout, "  # go mod tidy, then go run . openapi: writes openapi.json, which main_test.go checks\n")
		if lang != "" {
			fmt.Fprintf(stdout, "  # and go run . types: the SPA's client of the API (frontend/src/types)\n")
		}
	}
	if *dbName != "sqlite" {
		fmt.Fprintf(stdout, "  # create the database and set DB_* in .env\n")
	}
	url := "http://localhost:8080"
	switch {
	case *stack == "api" && lang != "":
		url = "the SPA at http://localhost:5173 (Vite's), the API under /api/v1"
	case *stack == "api":
		url += "/api/v1"
	}
	fmt.Fprintf(stdout, "  go run . migrate\n  go tool anetos dev    # %s\n", url)
	return 0
}

// installSteps are the go commands that finish a new project of a
// stack: templ and its generated views for the web stack only, the
// API's description (openapi.json) for the api stack, and with a
// frontend (vite), its client of the API (go run . types).
func installSteps(db, stack string, replaced, vite bool) [][]string {
	var steps [][]string
	if !replaced {
		// Every module is released with the same version: a tool of one
		// release makes projects of that release, whose code it wrote.
		v := anetosVersion()
		steps = append(steps,
			[]string{"get", "anetos.dev/anetos@" + v, "anetos.dev/anetos/drivers/" + db + "@" + v},
			[]string{"get", "-tool", "anetos.dev/anetos/cli/cmd/anetos@" + v})
	}
	if stack != "api" {
		steps = append(steps,
			[]string{"get", "-tool", "github.com/a-h/templ/cmd/templ@" + scaffold.TemplVersion},
			[]string{"tool", "templ", "generate"}, // views/ has no Go files before this
		)
	}
	steps = append(steps, []string{"mod", "tidy"})
	if stack == "api" {
		steps = append(steps, []string{"run", ".", "openapi"}) // openapi.json, which a test checks
		if vite {
			steps = append(steps, []string{"run", ".", "types"}) // the SPA's client
		}
	}
	return steps
}

// toolVersion is this tool's version (a variable for tests).
var toolVersion = version

// anetosVersion is the version of Anetos to put in new projects: this
// tool's, or latest when it isn't a release.
func anetosVersion() string {
	return releaseVersion(toolVersion())
}

// releaseVersion is v when it's a release or a pre-release (v0.5.0,
// v0.5.0-rc.1), else "latest": for "(devel)", v0.0.0 and the
// pseudo-versions of builds from a commit or a checkout.
func releaseVersion(v string) string {
	if !semver.IsValid(v) || module.IsPseudoVersion(v) || v == "v0.0.0" {
		return "latest"
	}
	return v
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

// viteFlag is --vite's language: "ts" (--vite alone, which newProject
// rewrites as --vite=ts) or "js".
type viteFlag string

func (f *viteFlag) String() string { return string(*f) }

func (f *viteFlag) Set(v string) error {
	if v != "ts" && v != "js" {
		return errors.New("want ts or js")
	}
	*f = viteFlag(v)
	return nil
}

// installFrontend installs the packages of the frontend of the project in
// dir, saying so; their installer's output only on failure.
func installFrontend(ctx context.Context, dir string, stdout io.Writer) error {
	fdir := filepath.Join(dir, frontend.Dir)
	fmt.Fprintf(stdout, "  %s install (frontend/)\n", frontend.ManagerOf(fdir).Name)
	var out bytes.Buffer
	if err := frontend.Install(ctx, fdir, &out, true); err != nil {
		return fmt.Errorf("installing the frontend's packages failed: %w\n%s", err, out.String())
	}
	return nil
}
