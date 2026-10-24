// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const securityUsage = `Usage: anetos security:check [--level=basic|advanced] [--format=text|json|sarif|markdown] [--out=FILE] [--ignore=FILE] [--strict]

Builds the app and runs its security:check command in the project: its
settings and routes checked against security rules (CSRF, logins on
admin routes, rate limits, cookies, TLS, debug endpoints…). The flags
are the app's; see go tool anetos docs guides/security-check.

The settings checked are this machine's (.env and the environment), as
doctor's: run ./<app> security:check where the production settings
are too. Without a .env (and no APP_KEY in the environment), it says
how to write one. Files given to --out and --ignore are relative to
the current folder. Exits 1 on a problem (--strict: on a warning too).
`

// securityCheck runs anetos security:check.
func securityCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if slices.ContainsFunc(args, isHelp) {
		fmt.Fprint(stdout, securityUsage)
		return 0
	}
	root, err := projectRoot()
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); err != nil && os.Getenv("APP_KEY") == "" {
		fmt.Fprintln(stderr, "anetos security:check: there's no .env (a fresh clone, CI?), and the app needs its settings to boot. Write one: cp .env.example .env && go tool anetos key:generate (or set APP_ENV and APP_KEY)")
		return 1
	}
	args = absPaths(args)
	tmp, err := os.MkdirTemp("", "anetos-security-")
	if err != nil {
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "app")
	if err := runGo(ctx, root, stderr, "build", "-o", bin, "."); err != nil {
		fmt.Fprintln(stderr, "anetos security:check: the app doesn't build:", err)
		return 1
	}
	var appErr bytes.Buffer
	if err := runApp(ctx, root, bin, stdout, io.MultiWriter(stderr, &appErr), append([]string{"security:check"}, args...)...); err != nil {
		if strings.Contains(appErr.String(), `unknown command "security:check"`) {
			fmt.Fprintln(stderr, "anetos security:check: the app's version of Anetos has no security:check command (v0.5 has)")
			return 1
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode() // the app printed why
		}
		fmt.Fprintln(stderr, "anetos security:check:", err)
		return 1
	}
	return 0
}

// absPaths makes the files --out and --ignore name absolute: the app
// runs in the project's root, and they are relative to where you are.
func absPaths(args []string) []string {
	out := slices.Clone(args)
	for i := 0; i < len(out); i++ {
		name, value, hasValue := strings.Cut(strings.TrimLeft(out[i], "-"), "=")
		if !strings.HasPrefix(out[i], "-") || name != "out" && name != "ignore" {
			continue
		}
		if !hasValue {
			if i+1 == len(out) {
				break
			}
			i++
			value = out[i]
		}
		if value == "" || value == "-" || filepath.IsAbs(value) {
			continue
		}
		abs, err := filepath.Abs(value)
		if err != nil {
			continue
		}
		if hasValue {
			out[i] = "--" + name + "=" + abs
		} else {
			out[i] = abs
		}
	}
	return out
}

func isHelp(s string) bool { return s == "-h" || s == "-help" || s == "--help" }
