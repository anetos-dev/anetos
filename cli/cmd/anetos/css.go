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

	"anetos.dev/anetos/cli/internal/scaffold"
	"anetos.dev/anetos/cli/internal/tailwind"
)

const cssBuildUsage = `Usage: anetos css:build [--check]

In a project of the tailwind design kit (anetos new --css=tailwind),
compiles views/ui/tailwind.css into public/static/app.css with Tailwind
CSS's standalone CLI, minified, writing it only when it changes. anetos
dev and anetos build run it too; run it yourself before a go build or a
commit without them. The first run downloads Tailwind CSS v` + tailwind.Version + `
for this computer into the user's cache directory and checks its SHA-256;
set ` + tailwind.Env + ` to a tailwindcss binary to use that one instead.
--check writes nothing and exits with status 1 when app.css is out of
date (for CI).
`

func cssBuild(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos css:build", flag.ContinueOnError)
	check := fs.Bool("check", false, "don't write; exit with status 1 if public/static/app.css is out of date")
	pos, code := parse(fs, args, stderr, cssBuildUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 0 {
		fs.Usage()
		return 2
	}
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "anetos css:build:", err)
		return 1
	}
	root, err := scaffold.FindRoot(wd)
	if err != nil {
		fmt.Fprintln(stderr, "anetos css:build:", err)
		return 1
	}
	if !tailwind.Uses(root) {
		fmt.Fprintf(stderr, "anetos css:build: the project has no %s: only the tailwind kit's stylesheet is compiled (anetos new --css=tailwind)\n", tailwind.Input)
		return 1
	}
	if *check {
		bin, err := tailwind.Binary(ctx, logTo(stderr, "anetos css:build"))
		if err != nil {
			fmt.Fprintln(stderr, "anetos css:build:", err)
			return 1
		}
		css, err := tailwind.Compile(ctx, bin, root)
		if err != nil {
			fmt.Fprintln(stderr, "anetos css:build:", err)
			return 1
		}
		if old, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tailwind.Output))); err != nil || !bytes.Equal(old, css) {
			fmt.Fprintf(stderr, "anetos css:build: %s is out of date: run `go tool anetos css:build` and commit the result\n", tailwind.Output)
			return 1
		}
		return 0
	}
	if err := buildCSS(ctx, root, stdout, "anetos css:build"); err != nil {
		fmt.Fprintln(stderr, "anetos css:build:", err)
		return 1
	}
	return 0
}

// buildCSS compiles a Tailwind project's stylesheet, saying through w
// when it changed (and when it downloads Tailwind first).
func buildCSS(ctx context.Context, root string, w io.Writer, cmd string) error {
	bin, err := tailwind.Binary(ctx, logTo(w, cmd))
	if err != nil {
		return err
	}
	changed, err := tailwind.Build(ctx, bin, root)
	if err != nil {
		return err
	}
	if changed {
		fmt.Fprintf(w, "%s: wrote %s\n", cmd, tailwind.Output)
	}
	return nil
}

func logTo(w io.Writer, cmd string) func(string, ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(w, cmd+": "+format+"\n", args...)
	}
}
