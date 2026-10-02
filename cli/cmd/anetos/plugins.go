// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
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
	"time"

	"anetos.dev/anetos/cli/internal/scaffold"
	"golang.org/x/mod/module"
)

const addUsage = `Usage: anetos add <module>[@version]

Installs a plugin: go get the module (default @latest), list its
Plugin() in plugins.go, tidy go.mod, check that the app builds, and add
its settings to .env.example. Plugins are Go code compiled into your app, with its
privileges: add only code you trust.
`

const removeUsage = `Usage: anetos remove <module>

Uninstalls a plugin: take it out of plugins.go, drop the module from
go.mod, and check that the app builds. Its settings stay in .env and
.env.example, and its tables in the database: drop them with a
migration of your own if you don't want them.
`

// addPlugin runs anetos add.
func addPlugin(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos add", flag.ContinueOnError)
	pos, code := parse(fs, args, stderr, addUsage)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2
	}
	mod, version, _ := strings.Cut(pos[0], "@")
	if version == "" {
		version = "latest"
	}
	if err := module.CheckPath(mod); err != nil {
		fmt.Fprintln(stderr, "anetos add:", err)
		return 2
	}
	root, installed, code := pluginProject(stderr, "add")
	if code >= 0 {
		return code
	}
	if slices.Contains(installed, mod) {
		fmt.Fprintf(stderr, "anetos add: %s is already in %s\n", mod, scaffold.PluginsFile)
		return 1
	}
	restore, err := backup(root, "go.mod", "go.sum", scaffold.PluginsFile)
	if err != nil {
		fmt.Fprintln(stderr, "anetos add:", err)
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "anetos add:", err)
		if rerr := restore(); rerr != nil {
			fmt.Fprintln(stderr, "anetos add: restore go.mod, go.sum and plugins.go:", rerr)
		} else {
			fmt.Fprintln(stderr, "anetos add: go.mod, go.sum and plugins.go are as they were")
		}
		return 1
	}
	fmt.Fprintf(stdout, "Adding %s@%s. Plugins run with your app's privileges: add only code you trust.\n", mod, version)
	coreBefore := moduleVersion(ctx, root, corePath)
	if err := runGo(ctx, root, stderr, "get", mod+"@"+version); err != nil {
		return fail(err)
	}
	if v := moduleVersion(ctx, root, mod); v != "" {
		fmt.Fprintf(stdout, "Installed %s %s.\n", mod, v)
	}
	if coreAfter := moduleVersion(ctx, root, corePath); coreAfter != coreBefore {
		fmt.Fprintf(stdout, "It needs a newer Anetos: %s went from %s to %s. Read its CHANGELOG.\n", corePath, coreBefore, coreAfter)
	}
	if err := scaffold.WritePlugins(root, append(installed, mod)); err != nil {
		return fail(err)
	}
	// plugins.go imports it now: a direct requirement, with the
	// requirements of its packages.
	if err := runGo(ctx, root, stderr, "mod", "tidy"); err != nil {
		return fail(err)
	}
	tmp, err := os.MkdirTemp("", "anetos-add-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "app")
	if err := runGo(ctx, root, stderr, "build", "-o", bin, "."); err != nil {
		return fail(fmt.Errorf("the app doesn't build with %s (does its package have a Plugin() ext.Plugin function?): %w", mod, err))
	}

	// Load the plugin as the app does, and get its settings: plugins:env
	// doesn't boot the app. ext.Load's errors (a version requirement, a
	// name taken) refuse the plugin; others are the app's own.
	var env, envErr bytes.Buffer
	if err := runApp(ctx, root, bin, &env, &envErr, "plugins:env"); err != nil {
		if strings.TrimSpace(envErr.String()) == "" {
			envErr.WriteString(err.Error())
		}
		if msg := envErr.String(); strings.Contains(msg, "ext: plugin ") || strings.Contains(msg, "ext: Mount(") {
			return fail(fmt.Errorf("ext.Load refuses the plugin:\n%s", strings.TrimSpace(msg)))
		}
		fmt.Fprintf(stdout, "Listed it in %s, but the app didn't start to list its settings (go run . plugins:env lists them):\n%s\n", scaffold.PluginsFile, strings.TrimSpace(envErr.String()))
	} else {
		fmt.Fprintf(stdout, "Listed it in %s.\n", scaffold.PluginsFile)
		if added, err := appendEnv(filepath.Join(root, ".env.example"), env.String()); err != nil {
			fmt.Fprintln(stderr, "anetos add: .env.example:", err)
		} else if len(added) > 0 {
			fmt.Fprintf(stdout, "Added its settings to .env.example: %s. Set them in .env.\n", strings.Join(added, ", "))
		}
	}
	fmt.Fprint(stdout, `Next:
  go run . plugins:list   what it adds
  go run . migrate        if it adds migrations
`)
	return 0
}

// removePlugin runs anetos remove.
func removePlugin(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("anetos remove", flag.ContinueOnError)
	pos, code := parse(fs, args, stderr, removeUsage)
	if code >= 0 {
		return code
	}
	if len(pos) != 1 {
		fs.Usage()
		return 2
	}
	mod, _, _ := strings.Cut(pos[0], "@")
	root, installed, code := pluginProject(stderr, "remove")
	if code >= 0 {
		return code
	}
	i := slices.Index(installed, mod)
	if i < 0 {
		fmt.Fprintf(stderr, "anetos remove: %s isn't in %s\n", mod, scaffold.PluginsFile)
		return 1
	}
	restore, err := backup(root, "go.mod", "go.sum", scaffold.PluginsFile)
	if err != nil {
		fmt.Fprintln(stderr, "anetos remove:", err)
		return 1
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "anetos remove:", err)
		if rerr := restore(); rerr != nil {
			fmt.Fprintln(stderr, "anetos remove: restore go.mod, go.sum and plugins.go:", rerr)
		} else {
			fmt.Fprintln(stderr, "anetos remove: go.mod, go.sum and plugins.go are as they were")
		}
		return 1
	}
	if err := scaffold.WritePlugins(root, slices.Delete(installed, i, i+1)); err != nil {
		return fail(err)
	}
	if err := runGo(ctx, root, stderr, "mod", "tidy"); err != nil {
		return fail(err)
	}
	if err := runGo(ctx, root, stderr, "build", "-o", os.DevNull, "."); err != nil {
		return fail(fmt.Errorf("the app doesn't build without %s (does your code use it?): %w", mod, err))
	}
	fmt.Fprintf(stdout, `Removed %s.
Its settings stay in .env and .env.example, and its tables in the
database: drop them with a migration of your own if you don't want them.
`, mod)
	return 0
}

// pluginProject finds the project and its plugins.
func pluginProject(stderr io.Writer, cmd string) (root string, installed []string, code int) {
	wd, err := os.Getwd()
	if err == nil {
		root, err = scaffold.FindRoot(wd)
	}
	if err == nil {
		installed, err = scaffold.ReadPlugins(root)
	}
	if err != nil {
		fmt.Fprintf(stderr, "anetos %s: %v\n", cmd, err)
		return "", nil, 1
	}
	return root, installed, -1
}

// backup saves files of root, and returns a function that puts them back
// (removing those that didn't exist).
func backup(root string, names ...string) (func() error, error) {
	saved := map[string][]byte{}
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(root, n))
		switch {
		case errors.Is(err, os.ErrNotExist):
			saved[n] = nil
		case err != nil:
			return nil, err
		default:
			saved[n] = b
		}
	}
	return func() error {
		var errs []error
		for n, b := range saved {
			p := filepath.Join(root, n)
			if b == nil {
				if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
					errs = append(errs, err)
				}
				continue
			}
			errs = append(errs, os.WriteFile(p, b, 0o644))
		}
		return errors.Join(errs...)
	}, nil
}

const corePath = "anetos.dev/anetos"

// moduleVersion returns the version of module mod in the project at
// root ("" if it has none), with its replacement if it is replaced.
func moduleVersion(ctx context.Context, root, mod string) string {
	var out bytes.Buffer
	if err := runGoOut(ctx, root, &out, io.Discard, "list", "-m", "-f", "{{.Version}}{{with .Replace}} => {{.Path}} {{.Version}}{{end}}", mod); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// appTimeout bounds runApp: a setup that hangs (a server that doesn't
// answer) mustn't hang anetos add.
var appTimeout = 2 * time.Minute

// runApp runs the app's binary in dir with args, killing it after
// appTimeout.
func runApp(ctx context.Context, dir, bin string, stdout, stderr io.Writer, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, appTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, bin, args...)
	c.Dir = dir
	c.Stdout, c.Stderr = stdout, stderr
	c.WaitDelay = 5 * time.Second
	if err := c.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: no answer in %v: %w", strings.Join(args, " "), appTimeout, err)
		}
		return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// runGo runs the go command in dir, its output going to stderr.
func runGo(ctx context.Context, dir string, stderr io.Writer, args ...string) error {
	return runGoOut(ctx, dir, stderr, stderr, args...)
}

// runGoOut runs the go command in dir with the given outputs.
func runGoOut(ctx context.Context, dir string, stdout, stderr io.Writer, args ...string) error {
	c := exec.CommandContext(ctx, "go", args...)
	c.Dir = dir
	c.Stdout, c.Stderr = stdout, stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// appendEnv adds to the file the lines of env (plugins:env's output)
// whose keys it doesn't have yet, with their plugin's comment, and
// returns the keys it added. A missing file is left missing.
func appendEnv(file, env string) ([]string, error) {
	cur, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for line := range strings.SplitSeq(string(cur), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		if k, _, ok := strings.Cut(line, "="); ok {
			have[strings.TrimSpace(k)] = true
		}
	}
	var out strings.Builder
	var added []string
	header := ""
	sc := bufio.NewScanner(strings.NewReader(env))
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "# ") {
			header = line
			continue
		}
		k, _, ok := strings.Cut(line, "=")
		if !ok || have[k] {
			continue
		}
		if header != "" {
			out.WriteString("\n" + header + " plugin\n")
			header = ""
		}
		out.WriteString(line + "\n")
		added = append(added, k)
	}
	if len(added) == 0 {
		return nil, nil
	}
	prefix := ""
	if len(cur) > 0 && !bytes.HasSuffix(cur, []byte("\n")) {
		prefix = "\n"
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return nil, err
	}
	if _, err := f.WriteString(prefix + out.String()); err != nil {
		_ = f.Close()
		return nil, err
	}
	return added, f.Close()
}
