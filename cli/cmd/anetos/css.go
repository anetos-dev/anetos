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
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"anetos.dev/anetos/cli/internal/scaffold"
	"anetos.dev/anetos/cli/internal/tailwind"
)

const cssBuildUsage = `Usage: anetos css:build [--check]

In a project that uses Tailwind CSS (anetos new --css=tailwind),
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
		fmt.Fprintf(stderr, "anetos css:build: the project has no %s: only Tailwind CSS's stylesheet is compiled (anetos new --css=tailwind)\n", tailwind.Input)
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

const cssUseUsage = `Usage: anetos css:use [<framework>] [--force]

Switches the project to another CSS framework: anetos (the starter
theme), none (plain HTML), pico, bootstrap, bulma or tailwind. It writes
the framework's components (views/ui) and stylesheets (public/static),
removes the old one's files the new one hasn't (pico.min.css, theme.js,
tailwind.css…), records the framework in views/ui/css.json, and runs
templ generate (and, for tailwind, css:build). The layout, the pages and
your own files in views/ui are left as they are: the pages call the
components, so they take the new look; classes written in your own pages
and components stay.

It refuses when a file it wrote for the old framework changed since (a
component you edited, your colors in app.css), naming them: --force
overwrites them; commit first, so git shows what changed. With the
framework the project has, it updates its files to this anetos's
version. Without <framework>, it prints the project's.
`

func cssUse(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos css:use", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite the files of views/ui and public/static changed since anetos wrote them")
	pos, code := parse(fs, args, stderr, cssUseUsage)
	if code >= 0 {
		return code
	}
	if len(pos) > 1 {
		fs.Usage()
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "anetos css:use:", err)
		return 1
	}
	wd, err := os.Getwd()
	if err != nil {
		return fail(err)
	}
	root, err := scaffold.FindRoot(wd)
	if err != nil {
		return fail(err)
	}
	if !scaffold.HasUI(root) {
		if _, err := os.Stat(filepath.Join(root, "views")); err != nil {
			return fail(errors.New("the project has no views/: an api project has no pages to style"))
		}
		return fail(errors.New("the project has no views/ui (made before v0.5): its pages carry the starter theme's classes, which another CSS framework doesn't change. The upgrade guide (docs/site/upgrade/v0.5.md) shows how to move the layout to the components first"))
	}
	rec, recErr := scaffold.ReadKitRecord(root)
	if len(pos) == 0 {
		var about []string
		if rec.Version != "" {
			about = append(about, "its framework v"+rec.Version)
		}
		if rec.Anetos != "" {
			about = append(about, "written by anetos "+rec.Anetos)
		}
		switch {
		case recErr == nil && len(about) > 0:
			fmt.Fprintf(stdout, "%s (%s)\n", rec.Kit, strings.Join(about, ", "))
		case recErr == nil:
			fmt.Fprintln(stdout, rec.Kit)
		case errors.Is(recErr, scaffold.ErrBadKitRecord):
			fmt.Fprintf(stdout, "%s, by its files: %v\n", scaffold.GuessKit(root), recErr)
		default:
			fmt.Fprintf(stdout, "%s, by its files: the project has no %s\n", scaffold.GuessKit(root), scaffold.KitRecordFile)
		}
		return 0
	}
	kit := pos[0]
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return fail(err)
	}
	c, err := scaffold.UseKit(root, modfile.ModulePath(mod), kit, *force)
	switch {
	case errors.Is(err, scaffold.ErrKitChanged):
		fmt.Fprintf(stderr, "anetos css:use: these files changed since anetos wrote them for %s, or aren't its:\n", scaffold.CSSName(c.From))
		for _, f := range c.Changed {
			fmt.Fprintln(stderr, "  "+f)
		}
		fmt.Fprintln(stderr, "Nothing was written. Run again with --force to replace them (commit first: git then shows what changed), or undo the changes.")
		return 1
	case errors.Is(err, scaffold.ErrNoKitRecord):
		return fail(fmt.Errorf("%w: run again with --force to replace views/ui's components and public/static/app.css (commit first: git then shows what changed)", err))
	case errors.Is(err, scaffold.ErrBadKitRecord):
		return fail(fmt.Errorf("%w. Fix the file, or run again with --force, which writes a new one (commit first: git then shows what changed)", err))
	case err != nil:
		for _, f := range c.Written {
			fmt.Fprintln(stderr, "wrote", f)
		}
		return fail(fmt.Errorf("%w; run the command again", err))
	}
	changed := map[string]bool{}
	for _, f := range c.Changed {
		changed[f] = true
	}
	note := func(f string) string {
		if changed[f] {
			return f + " (it had changed)"
		}
		return f
	}
	for _, f := range c.Written {
		fmt.Fprintln(stdout, "wrote", note(f))
	}
	for _, f := range c.Removed {
		fmt.Fprintln(stdout, "removed", note(f))
	}
	if added, err := scaffold.AddNavMenu(root); err != nil {
		fmt.Fprintln(stdout, "anetos css:use:", err)
	} else if added {
		fmt.Fprintln(stdout, `added nav.menu ("Menu") to locales/en/app.yaml`)
	}
	if missing := scaffold.LocalesWithoutNavMenu(root); len(missing) > 0 {
		fmt.Fprintf(stdout, "Translate nav.menu (the name of the header's menu button) in locales/%s/app.yaml too.\n", strings.Join(missing, "/app.yaml, locales/"))
	}
	var templOut bytes.Buffer // templ reports progress on stderr: shown only if it fails
	if err := runGoOut(ctx, root, &templOut, &templOut, "tool", "templ", "generate"); err != nil {
		fmt.Fprint(stderr, templOut.String())
		return fail(fmt.Errorf("the CSS framework was switched, but %w", err))
	}
	if err := runGo(ctx, root, stderr, "build", "./..."); err != nil {
		// The app's own code calls something the old kit had.
		return fail(fmt.Errorf("the CSS framework was switched, but the project doesn't build: %w", err))
	}
	if kit == "tailwind" {
		if err := buildCSS(ctx, root, stdout, "anetos css:use"); err != nil {
			fmt.Fprintf(stdout, "Tailwind CSS didn't run (%v):\npublic/static/app.css is the one anetos ships, compiled for the components; run go tool anetos css:build for classes of your own.\n", err)
		}
	}
	if len(c.Own) > 0 {
		fmt.Fprintf(stdout, "Your own files in views/ui keep their classes, which %s may not style: %s\n", scaffold.CSSName(kit), strings.Join(c.Own, ", "))
	}
	if len(c.Others) > 0 {
		fmt.Fprintf(stdout, "Without a record, public/static's other files stay: remove the old CSS framework's yourself (%s).\n", strings.Join(c.Others, ", "))
	}
	dockerHint(root, c, stdout)
	if c.From == kit {
		fmt.Fprintf(stdout, "The files for %s are this anetos's.\n", scaffold.CSSName(kit))
	} else {
		fmt.Fprintf(stdout, "The project uses %s. Pages with classes of their own keep them.\n", scaffold.CSSName(kit))
	}
	return 0
}

// dockerCache is the Dockerfile's cache mount of a Tailwind project.
const dockerCache = "--mount=type=cache,target=/root/.cache/anetos"

// dockerHint says how the Dockerfile's build step changes when a project
// moves to or from Tailwind CSS (its download's cache); the file is the
// developer's, so it isn't edited.
func dockerHint(root string, c scaffold.KitChange, w io.Writer) {
	b, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil || (c.From == "tailwind") == (c.To == "tailwind") {
		return
	}
	has := bytes.Contains(b, []byte(dockerCache))
	switch {
	case c.To == "tailwind" && !has:
		fmt.Fprintf(w, "Dockerfile: add %s to the RUN line of go tool anetos build, so Docker keeps Tailwind CSS between builds.\n", dockerCache)
	case c.From == "tailwind" && has:
		fmt.Fprintf(w, "Dockerfile: the RUN line's %s is no longer needed.\n", dockerCache)
	}
}
