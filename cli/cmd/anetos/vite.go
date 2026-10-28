// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"

	"anetos.dev/anetos/cli/internal/frontend"
	"anetos.dev/anetos/cli/internal/scaffold"
)

const viteInstallUsage = `Usage: anetos vite:install [--js] [--skip-install]

Adds a frontend that Vite builds: frontend/ with package.json,
vite.config.ts, tsconfig.json and src/main.ts (with --js, vite.config.js
and src/main.js), and the app's side. What it can't change safely (an
app's own edits), it says how to add. Then it installs the packages
(Node.js 20.19 or 22.12 or later), unless --skip-install.

In a web project, the frontend is the pages' scripts and styles:
vite.New in main.go's setup, the /build/ route in routes/web.go,
@vite.Tags in the layout's head, the build in .gitignore and
.dockerignore, and a Node stage in the Dockerfile; then it runs templ
generate. anetos dev runs Vite and forwards /build/ to it; anetos build
builds the frontend into public/build, which the binary embeds.

In an API project, it is a single-page app without a framework
(index.html, and a login page once make:auth has run): the app serves
it at / (vite.SPA in main.go, public/public.go), and writes its API
client (typescript.Register; then it runs go run . types);
HTTP_SPA_ORIGINS lists Vite's address and APP_URL's origin in .env
and .env.example, and APP_URL in deploy/production.env.example; .gitignore, .dockerignore and the
Dockerfile as above. anetos dev runs Vite at http://localhost:5173,
which forwards /api to the app; anetos build builds the SPA into
public/spa.
`

func viteInstall(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos vite:install", flag.ContinueOnError)
	js := fs.Bool("js", false, "JavaScript instead of TypeScript")
	skip := fs.Bool("skip-install", false, "don't install the frontend's packages")
	pos, code := parse(fs, args, stderr, viteInstallUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fs.Usage()
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "anetos vite:install:", err)
		return 1
	}
	root, err := projectRoot()
	if err != nil {
		return fail(err)
	}
	lang := "ts"
	if *js {
		lang = "js"
	}
	res, err := scaffold.AddVite(root, lang)
	if errors.Is(err, scaffold.ErrHasFrontend) {
		return fail(errors.New("the project has frontend/ already; its vite.config lists the entries"))
	}
	if err != nil {
		return fail(err)
	}
	for _, f := range res.Created {
		fmt.Fprintln(stdout, "created", f)
	}
	for _, f := range res.Updated {
		fmt.Fprintln(stdout, "updated", f)
	}
	if len(res.Manual) > 0 {
		fmt.Fprintln(stdout, "\nAdd by hand, where your changes moved things:")
		for _, m := range res.Manual {
			fmt.Fprintln(stdout, "  - "+m)
		}
	}
	if res.SPA {
		// The client src/main calls the API with.
		if frontend.UsesTypes(root) {
			if out, err := goCmd(ctx, root, "run", ".", "types"); err != nil {
				fmt.Fprint(stderr, string(out))
				return fail(fmt.Errorf("go run . types: %w", err))
			}
		}
	} else {
		// The layout's tags, in its generated Go.
		var templOut bytes.Buffer // templ reports progress on stderr: shown only if it fails
		if err := runGoOut(ctx, root, &templOut, &templOut, "tool", "templ", "generate"); err != nil {
			fmt.Fprint(stderr, templOut.String())
			return fail(fmt.Errorf("templ generate: %w", err))
		}
	}
	if !*skip {
		if err := frontend.CheckNode(ctx); err != nil {
			fmt.Fprintf(stdout, "\nNot installing the frontend's packages: %v.\nOnce Node.js is installed, anetos dev installs them.\n", err)
		} else if err := installFrontend(ctx, root, stdout); err != nil {
			return fail(err)
		}
	}
	switch {
	case res.SPA && slices.Contains(res.Updated, "main.go"):
		fmt.Fprintln(stdout, "\nThe app serves the SPA at /; go tool anetos dev runs Vite with the app: open http://localhost:5173.")
	case res.SPA:
		fmt.Fprintln(stdout, "\nOnce main.go serves the SPA, go tool anetos dev runs Vite with the app: open http://localhost:5173.")
	case slices.Contains(res.Updated, "views/layout.templ"):
		fmt.Fprintf(stdout, "\nThe layout loads %s; go tool anetos dev runs Vite with the app.\n", res.Entry)
	default:
		fmt.Fprintf(stdout, "\nOnce the layout loads %s, go tool anetos dev runs Vite with the app.\n", res.Entry)
	}
	fmt.Fprintln(stdout, "Run go tool anetos agent:update so coding agents know about frontend/.")
	return 0
}
